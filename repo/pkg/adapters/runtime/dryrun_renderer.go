package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

type KubernetesDryRunRenderer struct {
	planner *PlanningRuntime
}

func NewKubernetesDryRunRenderer(planner *PlanningRuntime) *KubernetesDryRunRenderer {
	if planner == nil {
		planner = NewPlanningRuntime()
	}
	return &KubernetesDryRunRenderer{planner: planner}
}

func (r *KubernetesDryRunRenderer) Render(ctx context.Context, spec ports.WorkloadSpec) ([]ports.WorkloadManifest, error) {
	planned, err := r.planner.plan(ctx, spec)
	if err != nil {
		return nil, err
	}

	var manifests []ports.WorkloadManifest
	switch planned.Kind {
	case ports.WorkloadKindVM:
		manifests = []ports.WorkloadManifest{renderVM(planned)}
	case ports.WorkloadKindBatchJob:
		manifests = []ports.WorkloadManifest{renderJob(planned)}
	default:
		if planned.Kind == ports.WorkloadKindSandbox {
			manifests = []ports.WorkloadManifest{renderSandboxWorkspacePVC(planned), renderDeployment(planned)}
		} else {
			manifests = []ports.WorkloadManifest{renderDeployment(planned)}
		}
		if planned.Container != nil && len(containerPortSpecs(planned)) > 0 {
			manifests = append(manifests, renderService(planned))
		}
	}
	// When a workload identity binding exists, render the K8s Secret that
	// backs the ANI_WORKLOAD_TOKEN env var so the Deployment can reference it.
	if planned.Identity != nil && planned.Identity.KeyValue != "" {
		manifests = append(manifests, renderWorkloadIdentitySecret(planned))
	}
	return manifests, nil
}

func renderVM(spec ports.WorkloadSpec) ports.WorkloadManifest {
	networks, interfaces := vmNetworksAndInterfaces(spec)
	content := manifest(map[string]any{
		"apiVersion": "kubevirt.io/v1",
		"kind":       "VirtualMachine",
		"metadata":   metadata(spec, "vm"),
		"spec": map[string]any{
			"running": spec.Lifecycle.AutoStart,
			"template": map[string]any{
				"metadata": map[string]any{
					"labels":      labels(spec),
					"annotations": podTemplateAnnotations(spec),
				},
				"spec": map[string]any{
					"domain": map[string]any{
						"machine": map[string]any{"type": firstNonEmpty(spec.VM.MachineType, "q35")},
						"devices": map[string]any{
							// K8s 1.28 without SidecarContainers treats guest-console-log
							// (virt-tail) as a blocking init container; disable for lab/live.
							"logSerialConsole": false,
							"disks":            vmDisks(spec),
							"interfaces":       interfaces,
						},
						"resources": map[string]any{
							"requests": resourceRequests(spec),
						},
					},
					"volumes":  vmVolumes(spec),
					"networks": networks,
				},
			},
		},
	})
	return ports.WorkloadManifest{Name: spec.Name, Kind: "VirtualMachine", Provider: "kubevirt", Content: content}
}

func renderDeployment(spec ports.WorkloadSpec) ports.WorkloadManifest {
	content := manifest(map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   metadata(spec, "workload"),
		"spec": map[string]any{
			"replicas": containerReplicas(spec),
			"selector": map[string]any{"matchLabels": selectorLabels(spec)},
			"template": podTemplate(spec),
		},
	})
	return ports.WorkloadManifest{Name: spec.Name, Kind: "Deployment", Provider: "kubernetes", Content: content}
}

func renderService(spec ports.WorkloadSpec) ports.WorkloadManifest {
	portsSpec := containerPortSpecs(spec)
	servicePorts := make([]any, 0, len(portsSpec))
	for index, port := range portsSpec {
		name := strings.TrimSpace(port.Name)
		if name == "" {
			name = "port-" + strconv.Itoa(index+1)
		}
		servicePorts = append(servicePorts, map[string]any{
			"name":       name,
			"port":       port.ContainerPort,
			"targetPort": port.ContainerPort,
			"protocol":   strings.ToUpper(firstNonEmpty(port.Protocol, "TCP")),
		})
	}
	content := manifest(map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   metadata(spec, "service"),
		"spec": map[string]any{
			"type":     "ClusterIP",
			"selector": selectorLabels(spec),
			"ports":    servicePorts,
		},
	})
	return ports.WorkloadManifest{Name: spec.Name, Kind: "Service", Provider: "kubernetes", Content: content}
}

func renderJob(spec ports.WorkloadSpec) ports.WorkloadManifest {
	content := manifest(map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata":   metadata(spec, "batch-job"),
		"spec": map[string]any{
			"backoffLimit": 0,
			"template":     podTemplate(spec),
		},
	})
	return ports.WorkloadManifest{Name: spec.Name, Kind: "Job", Provider: "kubernetes", Content: content}
}

func podTemplate(spec ports.WorkloadSpec) map[string]any {
	storage := renderStorageAttachments(spec)
	envFrom := secretEnvFromIDs(spec)
	envFrom = append(envFrom, secretEnvFrom(spec.SecretBindings)...)
	resources := containerResources(spec)
	// Merge Volcano resource-request annotations (e.g. volcano.sh/vgpu-memory,
	// volcano.sh/vgpu-number, nvidia.com/gpu) into the container resources.
	if existing, ok := resources["limits"].(map[string]string); ok {
		merged := resourceRequestsFromAnnotations(spec, existing)
		resources["limits"] = merged
	}
	if existing, ok := resources["requests"].(map[string]string); ok {
		merged := resourceRequestsFromAnnotations(spec, existing)
		resources["requests"] = merged
	}
	podSpec := map[string]any{
		"restartPolicy": "Always",
		"containers": []any{
			map[string]any{
				"name":         spec.Name,
				"image":        spec.Image,
				"command":      omitEmptySlice(spec.Command),
				"args":         omitEmptySlice(spec.Args),
				"env":          containerEnv(spec),
				"envFrom":      envFrom,
				"resources":    resources,
				"ports":        containerPorts(spec),
				"volumeMounts": append(volumeMounts(storage), secretVolumeMounts(spec.SecretBindings)...),
			},
		},
		"volumes": append(volumes(storage), secretVolumes(spec.SecretBindings)...),
	}
	if spec.Kind == ports.WorkloadKindSandbox {
		containers := podSpec["containers"].([]any)
		container := containers[0].(map[string]any)
		container["volumeMounts"] = append(container["volumeMounts"].([]any), map[string]any{
			"name":      "sandbox-workspace",
			"mountPath": sandboxWorkspaceRoot,
		})
		podSpec["volumes"] = append(podSpec["volumes"].([]any), map[string]any{
			"name": "sandbox-workspace",
			"persistentVolumeClaim": map[string]any{
				"claimName": sandboxWorkspacePVCName(spec.Name),
			},
		})
	}
	if spec.Kind == ports.WorkloadKindBatchJob {
		podSpec["restartPolicy"] = "Never"
	}
	if spec.RuntimeClassName != "" {
		podSpec["runtimeClassName"] = spec.RuntimeClassName
	}
	if spec.SchedulerName != "" {
		podSpec["schedulerName"] = spec.SchedulerName
	}
	if spec.ServiceAccountName != "" {
		podSpec["serviceAccountName"] = spec.ServiceAccountName
	}
	// Apply Volcano nodeSelector from annotations (ani.kubercloud.io/node-selector/*).
	if nodeSelector := nodeSelectorFromAnnotations(spec); nodeSelector != nil {
		podSpec["nodeSelector"] = nodeSelector
	}

	return map[string]any{
		"metadata": map[string]any{
			"labels":      selectorLabels(spec),
			"annotations": podTemplateAnnotations(spec),
		},
		"spec": podSpec,
	}
}

func renderSandboxWorkspacePVC(spec ports.WorkloadSpec) ports.WorkloadManifest {
	name := sandboxWorkspacePVCName(spec.Name)
	meta := metadata(spec, "sandbox-workspace")
	meta["name"] = name
	pvcSpec := map[string]any{
		"accessModes": []any{"ReadWriteOnce"},
		"resources": map[string]any{
			"requests": map[string]any{"storage": "5Gi"},
		},
	}
	if snapshotName := sandboxCheckpointSnapshotName(spec.SandboxCheckpointSourceRef); snapshotName != "" {
		pvcSpec["dataSource"] = map[string]any{
			"apiGroup": "snapshot.storage.k8s.io",
			"kind":     "VolumeSnapshot",
			"name":     snapshotName,
		}
	}
	return ports.WorkloadManifest{
		Name: name, Kind: "PersistentVolumeClaim", Provider: "kubernetes",
		Content: manifest(map[string]any{
			"apiVersion": "v1",
			"kind":       "PersistentVolumeClaim",
			"metadata":   meta,
			"spec":       pvcSpec,
		}),
	}
}

func sandboxWorkspacePVCName(instanceName string) string {
	return instanceName + "-workspace"
}

func sandboxCheckpointSnapshotName(ref string) string {
	parts := strings.Split(strings.TrimSpace(ref), "/")
	if len(parts) == 3 && parts[0] == "kubernetes" && parts[1] == "VolumeSnapshot" {
		return parts[2]
	}
	return ""
}

// volcanoPodTemplateAnnotationPrefixes are annotation key prefixes that must
// land on the PodTemplate metadata (spec.template.metadata.annotations), not
// the top-level object metadata. metadata() filters these out so they do not
// leak onto the Deployment/Job/Service top-level annotations.
var volcanoPodTemplateAnnotationPrefixes = []string{
	"scheduling.volcano.sh/",
	"ani.kubercloud.io/scheduler-name",
	volcanoNodeSelectorAnnotation,
	volcanoResourceRequestAnnotation,
}

// isPodTemplateOnlyAnnotation reports whether the annotation key should only
// appear on the PodTemplate metadata and be filtered out of the top-level
// object metadata.
func isPodTemplateOnlyAnnotation(key string) bool {
	for _, prefix := range volcanoPodTemplateAnnotationPrefixes {
		// "scheduling.volcano.sh/" is a prefix with trailing slash, so
		// HasPrefix matches keys under that domain (e.g. .../queue-name).
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func metadata(spec ports.WorkloadSpec, component string) map[string]any {
	return map[string]any{
		"name":      spec.Name,
		"namespace": tenantNamespace(spec.TenantID),
		"labels": mergeStringMap(labels(spec), map[string]string{
			"app.kubernetes.io/component": component,
		}),
		"annotations": objectAnnotations(spec),
	}
}

func labels(spec ports.WorkloadSpec) map[string]string {
	return mergeStringMap(map[string]string{
		"app.kubernetes.io/part-of":       "ani-platform",
		"ani.kubercloud.io/tenant-id":     spec.TenantID,
		"ani.kubercloud.io/instance":      spec.Name,
		"ani.kubercloud.io/instance-kind": string(spec.Kind),
	}, spec.Labels)
}

func selectorLabels(spec ports.WorkloadSpec) map[string]string {
	return map[string]string{
		"ani.kubercloud.io/tenant-id": spec.TenantID,
		"ani.kubercloud.io/instance":  spec.Name,
	}
}

func annotationsWithInstancePlan(spec ports.WorkloadSpec) map[string]string {
	annotations := mergeStringMap(map[string]string{
		"ani.kubercloud.io/network-planes":  networkPlanes(spec.Network.Attachments),
		"ani.kubercloud.io/storage-kinds":   storageKinds(renderStorageAttachments(spec)),
		"ani.kubercloud.io/render-mode":     "dry-run",
		"ani.kubercloud.io/runtime-adapter": "planning",
	}, spec.Annotations)
	if vpcID := strings.TrimSpace(spec.Network.VPCID); vpcID != "" {
		annotations["ani.kubercloud.io/vpc-id"] = vpcID
	}
	if subnetID := strings.TrimSpace(spec.Network.SubnetID); subnetID != "" {
		annotations["ani.kubercloud.io/subnet-id"] = subnetID
		annotations["ovn.kubernetes.io/logical_switch"] = networkProviderName("subnet", subnetID)
	}
	if privateIP := strings.TrimSpace(spec.Network.PrivateIP); privateIP != "" {
		annotations["ovn.kubernetes.io/ip_address"] = privateIP
	}
	if securityGroups := joinSecurityGroupIDs(spec.Network.SecurityGroupIDs); securityGroups != "" {
		annotations["ani.kubercloud.io/security-groups"] = securityGroups
	}
	if spec.Identity != nil {
		annotations["ani.kubercloud.io/workload-identity-key-id"] = spec.Identity.KeyID
		annotations["ani.kubercloud.io/workload-identity-secret"] = workloadIdentitySecretName(spec)
	}
	if spec.Kind == ports.WorkloadKindVM {
		if mounts := vmSecretMountAnnotation(spec.SecretBindings); mounts != "" {
			annotations["ani.kubercloud.io/vm-secret-mounts"] = mounts
		}
	}
	return annotations
}

// objectAnnotations returns the platform annotations for top-level object
// metadata (Deployment/Job/Service). It calls annotationsWithInstancePlan and
// then strips Volcano PodTemplate-only keys so they do not leak onto the
// top-level metadata where Volcano would ignore them.
func objectAnnotations(spec ports.WorkloadSpec) map[string]string {
	annotations := annotationsWithInstancePlan(spec)
	for key := range annotations {
		if isPodTemplateOnlyAnnotation(key) {
			delete(annotations, key)
		}
	}
	return annotations
}

// podTemplateAnnotations returns the annotations that should appear on the
// PodTemplate metadata (spec.template.metadata.annotations). This includes
// all platform annotations plus Volcano PodTemplate-only keys.
func podTemplateAnnotations(spec ports.WorkloadSpec) map[string]string {
	return annotationsWithInstancePlan(spec)
}

// nodeSelectorFromAnnotations extracts the JSON-encoded nodeSelector injected
// by the Volcano translator/planner (ani.kubercloud.io/volcano-node-selector)
// into a K8s Pod spec nodeSelector map.
func nodeSelectorFromAnnotations(spec ports.WorkloadSpec) map[string]string {
	raw, ok := spec.Annotations[volcanoNodeSelectorAnnotation]
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	result := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// resourceRequestsFromAnnotations extracts the JSON-encoded Volcano resource
// requests (ani.kubercloud.io/volcano-resource-requests) and merges them into
// the container resources map. Values from annotations override existing
// entries for the same key.
func resourceRequestsFromAnnotations(spec ports.WorkloadSpec, existing map[string]string) map[string]string {
	result := map[string]string{}
	for k, v := range existing {
		result[k] = v
	}
	raw, ok := spec.Annotations[volcanoResourceRequestAnnotation]
	if !ok || strings.TrimSpace(raw) == "" {
		return result
	}
	decoded := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return result
	}
	for k, v := range decoded {
		result[k] = v
	}
	return result
}

func joinSecurityGroupIDs(ids []string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		parts = append(parts, id)
	}
	return strings.Join(parts, ",")
}

func workloadIdentityEnv(spec ports.WorkloadSpec) []any {
	if spec.Identity == nil {
		return nil
	}
	secretName := workloadIdentitySecretName(spec)
	return []any{
		map[string]any{
			"name": "ANI_WORKLOAD_TOKEN",
			"valueFrom": map[string]any{
				"secretKeyRef": map[string]any{
					"name": secretName,
					"key":  "token",
				},
			},
		},
		map[string]any{
			"name":  "ANI_WORKLOAD_ID",
			"value": spec.Identity.InstanceID,
		},
	}
}

func containerEnv(spec ports.WorkloadSpec) []any {
	items := workloadIdentityEnv(spec)
	if spec.Container == nil {
		return items
	}
	for _, env := range spec.Container.Env {
		entry := map[string]any{"name": env.Name}
		if env.SecretRef != "" {
			entry["valueFrom"] = map[string]any{"secretKeyRef": map[string]any{"name": env.SecretRef, "key": env.Name}}
		} else if env.Value != nil {
			entry["value"] = *env.Value
		}
		items = append(items, entry)
	}
	return items
}

func workloadIdentitySecretName(spec ports.WorkloadSpec) string {
	if spec.Identity == nil {
		return ""
	}
	seed := firstNonEmpty(spec.Identity.KeyID, spec.Identity.InstanceID, spec.Name)
	seed = strings.ReplaceAll(seed, "_", "-")
	seed = strings.ReplaceAll(seed, ":", "-")
	seed = strings.Trim(seed, "-")
	if len(seed) > 24 {
		seed = strings.Trim(seed[:24], "-")
	}
	return "ani-wi-" + seed
}

func renderWorkloadIdentitySecret(spec ports.WorkloadSpec) ports.WorkloadManifest {
	secretName := workloadIdentitySecretName(spec)
	content := manifest(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      secretName,
			"namespace": tenantNamespace(spec.TenantID),
			"labels": mergeStringMap(labels(spec), map[string]string{
				"app.kubernetes.io/component": "workload-identity",
			}),
			"annotations": objectAnnotations(spec),
		},
		"type": "Opaque",
		"data": map[string]string{
			"token": base64.StdEncoding.EncodeToString([]byte(spec.Identity.KeyValue)),
		},
	})
	return ports.WorkloadManifest{Name: secretName, Kind: "Secret", Provider: "kubernetes", Content: content}
}

func containerResources(spec ports.WorkloadSpec) map[string]any {
	limits := map[string]string{}
	requests := map[string]string{}
	if spec.Resources.CPU != "" {
		requests["cpu"] = spec.Resources.CPU
		limits["cpu"] = spec.Resources.CPU
	}
	if spec.Resources.Memory != "" {
		requests["memory"] = spec.Resources.Memory
		limits["memory"] = spec.Resources.Memory
	}
	if requiresGPU(spec.Kind, spec.Resources) {
		// When Volcano resource requests are present (vGPU/wholecard spec),
		// they fully define the GPU resources — skip the legacy default.
		if _, hasVolcanoRequests := spec.Annotations[volcanoResourceRequestAnnotation]; !hasVolcanoRequests {
			resourceName := firstNonEmpty(spec.Annotations["ani.kubercloud.io/gpu-resource-name"], "nvidia.com/gpu")
			quantity := firstNonEmpty(spec.Annotations["ani.kubercloud.io/gpu-resource-quantity"], strconv.Itoa(spec.Resources.GPU.RequiredCount))
			limits[resourceName] = quantity
		}
	}
	return map[string]any{
		"requests": requests,
		"limits":   limits,
	}
}

func resourceRequests(spec ports.WorkloadSpec) map[string]string {
	requests := map[string]string{}
	if spec.Resources.CPU != "" {
		requests["cpu"] = spec.Resources.CPU
	}
	if spec.Resources.Memory != "" {
		requests["memory"] = spec.Resources.Memory
	}
	return requests
}

func containerPorts(spec ports.WorkloadSpec) []any {
	if spec.Container == nil {
		return nil
	}
	if len(spec.Container.PortSpecs) > 0 {
		items := make([]any, 0, len(spec.Container.PortSpecs))
		for _, port := range spec.Container.PortSpecs {
			entry := map[string]any{"containerPort": port.ContainerPort, "protocol": strings.ToUpper(firstNonEmpty(port.Protocol, "TCP"))}
			if port.Name != "" {
				entry["name"] = port.Name
			}
			items = append(items, entry)
		}
		return items
	}
	ports := make([]any, 0, len(spec.Container.Ports))
	for _, port := range spec.Container.Ports {
		ports = append(ports, map[string]any{"containerPort": port, "protocol": "TCP"})
	}
	return ports
}

func containerPortSpecs(spec ports.WorkloadSpec) []ports.InstancePortSpec {
	if spec.Container == nil {
		return nil
	}
	if len(spec.Container.PortSpecs) > 0 {
		return append([]ports.InstancePortSpec(nil), spec.Container.PortSpecs...)
	}
	items := make([]ports.InstancePortSpec, 0, len(spec.Container.Ports))
	for _, port := range spec.Container.Ports {
		items = append(items, ports.InstancePortSpec{ContainerPort: port, Protocol: "TCP"})
	}
	return items
}

func containerReplicas(spec ports.WorkloadSpec) int32 {
	if spec.Container == nil || spec.Container.Replicas < 1 {
		return 1
	}
	return spec.Container.Replicas
}

func renderStorageAttachments(spec ports.WorkloadSpec) []ports.WorkloadStorageAttachment {
	items := make([]ports.WorkloadStorageAttachment, 0, len(spec.Storage))
	seen := map[string]struct{}{}
	for _, item := range spec.Storage {
		item = normalizeContainerStorageAttachment(item)
		item.Name = firstNonEmpty(item.Name, storageMountName(item.ResourceType, item.ResourceID))
		key := item.ResourceType + ":" + item.ResourceID + ":" + item.MountPath
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, item)
	}
	if spec.Container == nil {
		return items
	}
	for _, mount := range spec.Container.VolumeMounts {
		item := normalizeContainerStorageAttachment(ports.WorkloadStorageAttachment{
			Name:         storageMountName("volume", mount.VolumeID),
			ResourceType: "volume",
			ResourceID:   mount.VolumeID,
			MountPath:    mount.MountPath,
			ReadOnly:     mount.ReadOnly,
		})
		key := item.ResourceType + ":" + item.ResourceID + ":" + item.MountPath
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			items = append(items, item)
		}
	}
	for _, mount := range spec.Container.FilesystemMounts {
		item := normalizeContainerStorageAttachment(ports.WorkloadStorageAttachment{
			Name:         storageMountName("filesystem", mount.FilesystemID),
			ResourceType: "filesystem",
			ResourceID:   mount.FilesystemID,
			MountPath:    mount.MountPath,
			ReadOnly:     mount.ReadOnly,
		})
		key := item.ResourceType + ":" + item.ResourceID + ":" + item.MountPath
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			items = append(items, item)
		}
	}
	return items
}

// normalizeContainerStorageAttachment maps resolved volume/filesystem refs onto
// shared PVC claims so container pods mount existing Storage provider PVCs
// instead of emptyDir placeholders.
func normalizeContainerStorageAttachment(item ports.WorkloadStorageAttachment) ports.WorkloadStorageAttachment {
	resourceID := strings.TrimSpace(item.ResourceID)
	if resourceID == "" {
		return item
	}
	switch strings.TrimSpace(item.ResourceType) {
	case "filesystem":
		item.Kind = ports.StorageAttachmentSharedPVC
		item.SourceRef = firstNonEmpty(item.SourceRef, storageProviderName("fs", resourceID))
	case "volume", "":
		item.ResourceType = "volume"
		if item.Kind == "" || item.Kind == ports.StorageAttachmentDataDisk || item.Kind == ports.StorageAttachmentSharedPVC {
			item.Kind = ports.StorageAttachmentSharedPVC
			item.SourceRef = firstNonEmpty(item.SourceRef, storageProviderName("vol", resourceID))
		}
	}
	return item
}

func storageMountName(kind, resourceID string) string {
	name := strings.TrimSpace(resourceID)
	if name == "" {
		name = kind
	}
	name = strings.NewReplacer("/", "-", "_", "-", ".", "-").Replace(name)
	return kind + "-" + name
}

func volumeMounts(storage []ports.WorkloadStorageAttachment) []any {
	mounts := make([]any, 0, len(storage))
	for _, attachment := range storage {
		if attachment.MountPath == "" {
			continue
		}
		mounts = append(mounts, map[string]any{
			"name":      attachment.Name,
			"mountPath": attachment.MountPath,
			"readOnly":  attachment.ReadOnly,
		})
	}
	return mounts
}

func secretEnvFrom(bindings []ports.WorkloadSecretBinding) []any {
	envFrom := make([]any, 0, len(bindings))
	for _, binding := range bindings {
		if binding.SecretID == "" || binding.EnvPrefix == "" {
			continue
		}
		envFrom = append(envFrom, map[string]any{
			"prefix": binding.EnvPrefix,
			"secretRef": map[string]any{
				"name": binding.SecretID,
			},
		})
	}
	if len(envFrom) == 0 {
		return nil
	}
	return envFrom
}

func secretEnvFromIDs(spec ports.WorkloadSpec) []any {
	if spec.Container == nil {
		return nil
	}
	envFrom := make([]any, 0, len(spec.Container.SecretIDs))
	seen := map[string]struct{}{}
	for _, secretID := range spec.Container.SecretIDs {
		secretID = strings.TrimSpace(secretID)
		if secretID == "" {
			continue
		}
		if _, ok := seen[secretID]; ok {
			continue
		}
		seen[secretID] = struct{}{}
		envFrom = append(envFrom, map[string]any{
			"secretRef": map[string]any{
				"name": secretID,
			},
		})
	}
	if len(envFrom) == 0 {
		return nil
	}
	return envFrom
}

func secretVolumeMounts(bindings []ports.WorkloadSecretBinding) []any {
	mounts := make([]any, 0, len(bindings))
	for i, binding := range bindings {
		if binding.SecretID == "" || binding.MountPath == "" {
			continue
		}
		mounts = append(mounts, map[string]any{
			"name":      secretVolumeName(binding, i),
			"mountPath": binding.MountPath,
			"readOnly":  true,
		})
	}
	if len(mounts) == 0 {
		return nil
	}
	return mounts
}

func volumes(storage []ports.WorkloadStorageAttachment) []any {
	result := make([]any, 0, len(storage))
	for _, attachment := range storage {
		volume := map[string]any{"name": attachment.Name}
		switch attachment.Kind {
		case ports.StorageAttachmentSharedPVC:
			volume["persistentVolumeClaim"] = map[string]any{"claimName": firstNonEmpty(attachment.SourceRef, attachment.Name)}
		case ports.StorageAttachmentObjectFuse:
			volume["emptyDir"] = map[string]any{}
			volume["aniObjectFuseSourceRef"] = attachment.SourceRef
		default:
			volume["emptyDir"] = map[string]any{"sizeLimit": sizeGi(attachment.SizeGiB)}
		}
		result = append(result, volume)
	}
	return result
}

func secretVolumes(bindings []ports.WorkloadSecretBinding) []any {
	result := make([]any, 0, len(bindings))
	for i, binding := range bindings {
		if binding.SecretID == "" || binding.MountPath == "" {
			continue
		}
		result = append(result, map[string]any{
			"name": secretVolumeName(binding, i),
			"secret": map[string]any{
				"secretName": binding.SecretID,
			},
		})
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func secretVolumeName(binding ports.WorkloadSecretBinding, index int) string {
	seed := strings.ToLower(binding.SecretID)
	var builder strings.Builder
	for _, r := range seed {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			builder.WriteRune(r)
			continue
		}
		builder.WriteByte('-')
	}
	name := strings.Trim(builder.String(), "-")
	if name == "" {
		name = "secret"
	}
	name = "secret-" + name + "-" + strconv.Itoa(index+1)
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

func vmVolumes(spec ports.WorkloadSpec) []any {
	volumes := []any{
		map[string]any{
			"name": "containerdisk",
			"containerDisk": map[string]any{
				"image": spec.VM.BootImage,
			},
		},
	}
	for _, attachment := range spec.Storage {
		if isContainerDiskPlaceholderRoot(spec, attachment) {
			continue
		}
		volumes = append(volumes, map[string]any{
			"name": attachment.Name,
			"persistentVolumeClaim": map[string]any{
				"claimName": firstNonEmpty(attachment.ResourceID, attachment.SourceRef, spec.Name+"-"+attachment.Name),
			},
		})
	}
	if cloudInit := vmCloudInitVolume(spec); cloudInit != nil {
		volumes = append(volumes, cloudInit)
	}
	volumes = append(volumes, secretVolumes(spec.SecretBindings)...)
	return volumes
}

func vmDisks(spec ports.WorkloadSpec) []any {
	disks := []any{
		map[string]any{
			"name": "containerdisk",
			"disk": map[string]any{"bus": "virtio"},
		},
	}
	for _, attachment := range spec.Storage {
		if isContainerDiskPlaceholderRoot(spec, attachment) {
			continue
		}
		disks = append(disks, map[string]any{
			"name": attachment.Name,
			"disk": map[string]any{"bus": "virtio"},
		})
	}
	if spec.VM != nil && (strings.TrimSpace(spec.VM.CloudInitSecret) != "" || strings.TrimSpace(spec.VM.UserData) != "") {
		disks = append(disks, map[string]any{
			"name": "cloudinitdisk",
			"disk": map[string]any{"bus": "virtio"},
		})
	}
	for i, binding := range spec.SecretBindings {
		if binding.SecretID == "" || binding.MountPath == "" {
			continue
		}
		disks = append(disks, map[string]any{
			"name": secretVolumeName(binding, i),
			"disk": map[string]any{"bus": "virtio"},
		})
	}
	return disks
}

func vmCloudInitVolume(spec ports.WorkloadSpec) map[string]any {
	if spec.VM == nil {
		return nil
	}
	cloudInit := map[string]any{}
	if secretID := strings.TrimSpace(spec.VM.CloudInitSecret); secretID != "" {
		cloudInit["secretRef"] = map[string]any{"name": secretID}
	}
	if userData := strings.TrimSpace(spec.VM.UserData); userData != "" {
		cloudInit["userData"] = userData
	}
	if len(cloudInit) == 0 {
		return nil
	}
	return map[string]any{
		"name":             "cloudinitdisk",
		"cloudInitNoCloud": cloudInit,
	}
}

func vmSecretMountAnnotation(bindings []ports.WorkloadSecretBinding) string {
	mounts := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		if binding.SecretID == "" || binding.MountPath == "" {
			continue
		}
		mounts = append(mounts, binding.SecretID+":"+binding.MountPath)
	}
	return strings.Join(mounts, ",")
}

func isContainerDiskPlaceholderRoot(spec ports.WorkloadSpec, attachment ports.WorkloadStorageAttachment) bool {
	if attachment.Kind != ports.StorageAttachmentRootDisk {
		return false
	}
	if strings.TrimSpace(attachment.ResourceID) != "" {
		return false
	}
	source := strings.TrimSpace(attachment.SourceRef)
	if source == "" {
		return true
	}
	if spec.VM != nil && source == strings.TrimSpace(spec.VM.BootImage) {
		return true
	}
	// Gateway defaults SourceRef to an image path when no concrete PVC/volume is chosen.
	return looksLikeImageReference(source)
}

func looksLikeImageReference(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.Contains(value, ":") {
		return true
	}
	return strings.Contains(value, "/") && strings.Contains(value, ".")
}

func isPlaceholderNetworkAttachment(attachment ports.WorkloadNetworkAttachment) bool {
	networkID := strings.TrimSpace(attachment.NetworkID)
	plane := strings.TrimSpace(string(attachment.Plane))
	if networkID == "" || networkID == plane {
		return true
	}
	if strings.ReplaceAll(networkID, "-", "_") == plane {
		return true
	}
	switch networkID {
	case "tenant-vpc", "foundation-mesh", "management", "storage":
		return true
	default:
		return false
	}
}

// vmNetworksAndInterfaces renders KubeVirt networks/interfaces as a matched pair.
// Planning/Gateway defaults use plane-like NetworkIDs as placeholders; those fall
// back to the pod network until a real Multus NAD is supplied.
func vmNetworksAndInterfaces(spec ports.WorkloadSpec) (networks []any, interfaces []any) {
	for _, attachment := range spec.Network.Attachments {
		if isPlaceholderNetworkAttachment(attachment) {
			continue
		}
		networkID := strings.TrimSpace(attachment.NetworkID)
		plane := strings.TrimSpace(string(attachment.Plane))
		name := firstNonEmpty(plane, networkID)
		networks = append(networks, map[string]any{
			"name": name,
			"multus": map[string]any{
				"networkName": networkID,
			},
		})
		interfaces = append(interfaces, map[string]any{
			"name":   name,
			"bridge": map[string]any{},
		})
	}
	if len(networks) == 0 {
		networks = []any{
			map[string]any{
				"name": "default",
				"pod":  map[string]any{},
			},
		}
		interfaces = []any{
			map[string]any{
				"name":       "default",
				"masquerade": map[string]any{},
			},
		}
	}
	return networks, interfaces
}

func manifest(value map[string]any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data) + "\n"
}

func tenantNamespace(tenantID string) string {
	return "ani-tenant-" + strings.ReplaceAll(tenantID, "_", "-")
}

func mergeStringMap(base map[string]string, overlay map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range base {
		result[key] = value
	}
	for key, value := range overlay {
		result[key] = value
	}
	return result
}

func networkPlanes(attachments []ports.WorkloadNetworkAttachment) string {
	values := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		values = append(values, string(attachment.Plane))
	}
	return strings.Join(values, ",")
}

func storageKinds(storage []ports.WorkloadStorageAttachment) string {
	values := make([]string, 0, len(storage))
	for _, attachment := range storage {
		values = append(values, string(attachment.Kind))
	}
	return strings.Join(values, ",")
}

func sizeGi(size int64) string {
	if size <= 0 {
		return ""
	}
	return strconv.FormatInt(size, 10) + "Gi"
}

func omitEmptySlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return values
}

var _ ports.WorkloadRenderer = (*KubernetesDryRunRenderer)(nil)
