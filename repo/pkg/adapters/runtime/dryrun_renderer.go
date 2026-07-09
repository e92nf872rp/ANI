package runtime

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

type KubernetesDryRunRenderer struct {
	planner *PlanningRuntime
}

const defaultKubeOVNLogicalSwitch = "ovn-default"

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

	switch planned.Kind {
	case ports.WorkloadKindVM:
		manifests := []ports.WorkloadManifest{renderVM(planned)}
		if secret, ok := renderWorkloadIdentitySecret(planned); ok {
			manifests = append([]ports.WorkloadManifest{secret}, manifests...)
		}
		return manifests, nil
	case ports.WorkloadKindBatchJob:
		manifests := []ports.WorkloadManifest{renderJob(planned)}
		if secret, ok := renderWorkloadIdentitySecret(planned); ok {
			manifests = append([]ports.WorkloadManifest{secret}, manifests...)
		}
		return manifests, nil
	default:
		manifests := []ports.WorkloadManifest{renderDeployment(planned)}
		if secret, ok := renderWorkloadIdentitySecret(planned); ok {
			manifests = append([]ports.WorkloadManifest{secret}, manifests...)
		}
		return manifests, nil
	}
}

func renderVM(spec ports.WorkloadSpec) ports.WorkloadManifest {
	vmSpec := map[string]any{
		"running": spec.Lifecycle.AutoStart,
		"template": map[string]any{
			"metadata": map[string]any{
				"labels":      labels(spec),
				"annotations": annotationsWithInstancePlan(spec),
			},
			"spec": map[string]any{
				"domain": map[string]any{
					"machine": map[string]any{"type": firstNonEmpty(spec.VM.MachineType, "q35")},
					"devices": vmDevices(spec),
					"resources": map[string]any{
						"requests": resourceRequests(spec),
					},
				},
				"volumes":  vmVolumes(spec),
				"networks": vmNetworkRefs(spec),
			},
		},
	}
	if spec.VM != nil && spec.VM.BootMedia == ports.VMBootMediaISO {
		if templates := vmISODataVolumeTemplates(spec); len(templates) > 0 {
			vmSpec["dataVolumeTemplates"] = templates
		}
	}
	content := manifest(map[string]any{
		"apiVersion": "kubevirt.io/v1",
		"kind":       "VirtualMachine",
		"metadata":   metadata(spec, "vm"),
		"spec":       vmSpec,
	})
	return ports.WorkloadManifest{Name: spec.Name, Kind: "VirtualMachine", Provider: "kubevirt", Content: content}
}

func renderDeployment(spec ports.WorkloadSpec) ports.WorkloadManifest {
	content := manifest(map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   metadata(spec, "workload"),
		"spec": map[string]any{
			"replicas": 1,
			"selector": map[string]any{"matchLabels": selectorLabels(spec)},
			"template": podTemplate(spec),
		},
	})
	return ports.WorkloadManifest{Name: spec.Name, Kind: "Deployment", Provider: "kubernetes", Content: content}
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
	podSpec := map[string]any{
		"restartPolicy": "Always",
		"containers": []any{
			map[string]any{
				"name":         spec.Name,
				"image":        spec.Image,
				"command":      omitEmptySlice(spec.Command),
				"args":         omitEmptySlice(spec.Args),
				"env":          workloadIdentityEnv(spec),
				"envFrom":      secretEnvFrom(spec.SecretBindings),
				"resources":    containerResources(spec),
				"ports":        containerPorts(spec),
				"volumeMounts": append(volumeMounts(spec.Storage), secretVolumeMounts(spec.SecretBindings)...),
			},
		},
		"volumes": append(volumes(spec.Storage), secretVolumes(spec.SecretBindings)...),
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

	return map[string]any{
		"metadata": map[string]any{
			"labels":      selectorLabels(spec),
			"annotations": annotationsWithInstancePlan(spec),
		},
		"spec": podSpec,
	}
}

func metadata(spec ports.WorkloadSpec, component string) map[string]any {
	return map[string]any{
		"name":      spec.Name,
		"namespace": tenantNamespace(spec.TenantID),
		"labels": mergeStringMap(labels(spec), map[string]string{
			"app.kubernetes.io/component": component,
		}),
		"annotations": annotationsWithInstancePlan(spec),
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
		"ani.kubercloud.io/storage-kinds":   storageKinds(spec.Storage),
		"ani.kubercloud.io/render-mode":     "dry-run",
		"ani.kubercloud.io/runtime-adapter": "planning",
	}, spec.Annotations)
	for _, attachment := range spec.Network.Attachments {
		if !attachment.Primary || attachment.Plane != ports.NetworkPlaneTenantVPC {
			continue
		}
		subnetID := strings.TrimSpace(attachment.SubnetID)
		if subnetID == "" {
			annotations["ovn.kubernetes.io/logical_switch"] = defaultKubeOVNLogicalSwitch
			break
		}
		annotations["ovn.kubernetes.io/logical_switch"] = KubeOVNProviderName("subnet", subnetID)
		annotations["ani.kubercloud.io/vpc-id"] = strings.TrimSpace(attachment.NetworkID)
		annotations["ani.kubercloud.io/subnet-id"] = subnetID
		if ip := strings.TrimSpace(attachment.IPAddress); ip != "" {
			annotations["ovn.kubernetes.io/ip_address"] = ip
		}
		break
	}
	if spec.Identity != nil {
		annotations["ani.kubercloud.io/workload-identity-key-id"] = spec.Identity.KeyID
		annotations["ani.kubercloud.io/workload-identity-secret"] = workloadIdentitySecretName(spec)
	}
	if spec.Kind == ports.WorkloadKindVM {
		if spec.VM != nil && strings.TrimSpace(spec.VM.RootDisk.SourceRef) != "" {
			annotations["ani.kubercloud.io/root-disk-source-ref"] = strings.TrimSpace(spec.VM.RootDisk.SourceRef)
		}
		if mounts := vmSecretMountAnnotation(spec.SecretBindings); mounts != "" {
			annotations["ani.kubercloud.io/vm-secret-mounts"] = mounts
		}
	}
	return annotations
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

func workloadIdentitySecretName(spec ports.WorkloadSpec) string {
	if spec.Identity == nil {
		return ""
	}
	seed := firstNonEmpty(spec.Identity.KeyID, spec.Identity.InstanceID, spec.Name)
	seed = strings.ToLower(seed)
	seed = strings.ReplaceAll(seed, "_", "-")
	var sanitized strings.Builder
	for _, r := range seed {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sanitized.WriteRune(r)
		}
	}
	seed = strings.Trim(sanitized.String(), "-")
	if seed == "" {
		seed = "workload"
	}
	if len(seed) > 24 {
		seed = strings.TrimRight(seed[:24], "-")
	}
	if seed == "" {
		seed = "workload"
	}
	return "ani-wi-" + seed
}

func renderWorkloadIdentitySecret(spec ports.WorkloadSpec) (ports.WorkloadManifest, bool) {
	if spec.Identity == nil || strings.TrimSpace(spec.Identity.KeyValue) == "" {
		return ports.WorkloadManifest{}, false
	}
	secretName := workloadIdentitySecretName(spec)
	if secretName == "" {
		return ports.WorkloadManifest{}, false
	}
	meta := metadata(spec, "workload-identity")
	meta["name"] = secretName
	content := manifest(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   meta,
		"type":       "Opaque",
		"stringData": map[string]string{
			"token": spec.Identity.KeyValue,
		},
	})
	return ports.WorkloadManifest{
		Name:     secretName,
		Kind:     "Secret",
		Provider: "kubernetes",
		Content:  content,
	}, true
}

func containerResources(spec ports.WorkloadSpec) map[string]any {
	limits := map[string]string{}
	requests := map[string]string{}
	if spec.Resources.CPU != "" {
		requests["cpu"] = spec.Resources.CPU
	}
	if spec.Resources.Memory != "" {
		requests["memory"] = spec.Resources.Memory
	}
	if requiresGPU(spec.Kind) {
		resourceName := firstNonEmpty(spec.Annotations["ani.kubercloud.io/gpu-resource-name"], "nvidia.com/gpu")
		quantity := firstNonEmpty(spec.Annotations["ani.kubercloud.io/gpu-resource-quantity"], strconv.Itoa(spec.Resources.GPU.RequiredCount))
		limits[resourceName] = quantity
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
	ports := make([]any, 0, len(spec.Container.Ports))
	for _, port := range spec.Container.Ports {
		ports = append(ports, map[string]any{"containerPort": port})
	}
	return ports
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

func vmDevices(spec ports.WorkloadSpec) map[string]any {
	devices := map[string]any{
		"disks": vmDisks(spec),
	}
	if ifaces := vmInterfaces(spec); len(ifaces) > 0 {
		devices["interfaces"] = ifaces
	}
	return devices
}

func vmInterfaces(spec ports.WorkloadSpec) []any {
	if vmPodNetworkEnabled() {
		return []any{
			map[string]any{
				"name":       "default",
				"masquerade": map[string]any{},
			},
		}
	}
	ifaces := make([]any, 0, len(spec.Network.Attachments))
	for _, attachment := range spec.Network.Attachments {
		ifaces = append(ifaces, map[string]any{
			"name":   string(attachment.Plane),
			"bridge": map[string]any{},
		})
	}
	return ifaces
}

func vmVolumes(spec ports.WorkloadSpec) []any {
	if spec.VM != nil && spec.VM.BootMedia == ports.VMBootMediaISO {
		return vmISOVolumes(spec)
	}
	volumes := []any{
		map[string]any{
			"name": "containerdisk",
			"containerDisk": map[string]any{
				"image": spec.VM.BootImage,
			},
		},
	}
	for _, attachment := range spec.Storage {
		if attachment.Kind == ports.StorageAttachmentRootDisk {
			continue
		}
		volumes = append(volumes, map[string]any{
			"name": attachment.Name,
			"persistentVolumeClaim": map[string]any{
				"claimName": firstNonEmpty(attachment.SourceRef, spec.Name+"-"+attachment.Name),
			},
		})
	}
	volumes = append(volumes, secretVolumes(spec.SecretBindings)...)
	return volumes
}

// vmISOVolumes renders the boot_media=iso path: a blank root disk backed by
// spec.dataVolumeTemplates (see vmISODataVolumeTemplates) plus a cdrom PVC
// sourced from a Ready Image. It never emits a containerDisk volume.
// DataVolume/PVC names are internal render details only; they are not
// returned in any API response.
func vmISOVolumes(spec ports.WorkloadSpec) []any {
	volumes := make([]any, 0, len(spec.Storage)+1)
	for _, attachment := range spec.Storage {
		switch attachment.Kind {
		case ports.StorageAttachmentRootDisk:
			volumes = append(volumes, map[string]any{
				"name": "rootdisk",
				"dataVolume": map[string]any{
					"name": vmISORootDiskName(spec, attachment),
				},
			})
		case ports.StorageAttachmentCDROM:
			volumes = append(volumes, map[string]any{
				"name": "iso",
				"persistentVolumeClaim": map[string]any{
					"claimName": firstNonEmpty(attachment.SourceRef, spec.VM.BootMediaImageID),
				},
			})
		default:
			volumes = append(volumes, map[string]any{
				"name": attachment.Name,
				"persistentVolumeClaim": map[string]any{
					"claimName": firstNonEmpty(attachment.SourceRef, spec.Name+"-"+attachment.Name),
				},
			})
		}
	}
	volumes = append(volumes, secretVolumes(spec.SecretBindings)...)
	return volumes
}

// vmISODataVolumeTemplates renders the spec.dataVolumeTemplates entry that
// makes the boot_media=iso root disk self-creating: KubeVirt/CDI provisions
// a blank PVC from this template (CDI blank source) sized from the
// root_disk storage attachment, instead of assuming a pre-existing claim.
// The template name matches vmISOVolumes' "rootdisk" dataVolume.name.
func vmISODataVolumeTemplates(spec ports.WorkloadSpec) []any {
	for _, attachment := range spec.Storage {
		if attachment.Kind != ports.StorageAttachmentRootDisk {
			continue
		}
		return []any{
			map[string]any{
				"metadata": map[string]any{
					"name": vmISORootDiskName(spec, attachment),
					"annotations": map[string]any{
						cdiImmediateBindAnnotation: "true",
					},
				},
				"spec": map[string]any{
					"source": map[string]any{"blank": map[string]any{}},
					"storage": map[string]any{
						"resources":        map[string]any{"requests": map[string]any{"storage": sizeGi(attachment.SizeGiB)}},
						"storageClassName": firstNonEmpty(attachment.StorageClass, defaultCDIStorageClass),
					},
				},
			},
		}
	}
	return nil
}

// vmISORootDiskName is the shared name for the blank root DataVolume
// template, its PVC, and the "rootdisk" volume's dataVolume.name reference.
func vmISORootDiskName(spec ports.WorkloadSpec, attachment ports.WorkloadStorageAttachment) string {
	return firstNonEmpty(attachment.Name, spec.Name+"-root")
}

func vmDisks(spec ports.WorkloadSpec) []any {
	if spec.VM != nil && spec.VM.BootMedia == ports.VMBootMediaISO {
		return vmISODisks(spec)
	}
	disks := []any{
		map[string]any{
			"name": "containerdisk",
			"disk": map[string]any{"bus": "virtio"},
		},
	}
	for _, attachment := range spec.Storage {
		if attachment.Kind == ports.StorageAttachmentRootDisk {
			continue
		}
		disks = append(disks, map[string]any{
			"name": attachment.Name,
			"disk": map[string]any{"bus": "virtio"},
		})
	}
	for i, binding := range spec.SecretBindings {
		if binding.SecretID == "" || binding.MountPath == "" {
			continue
		}
		disks = append(disks, map[string]any{
			"name":     secretVolumeName(binding, i),
			"disk":     map[string]any{"bus": "virtio"},
			"readOnly": true,
		})
	}
	return disks
}

func vmISODisks(spec ports.WorkloadSpec) []any {
	disks := make([]any, 0, len(spec.Storage)+1)
	for _, attachment := range spec.Storage {
		switch attachment.Kind {
		case ports.StorageAttachmentRootDisk:
			disks = append(disks, map[string]any{
				"name": "rootdisk",
				"disk": map[string]any{"bus": "virtio"},
			})
		case ports.StorageAttachmentCDROM:
			disks = append(disks, map[string]any{
				"name":      "iso",
				"cdrom":     map[string]any{"bus": "sata"},
				"bootOrder": vmISOBootOrder(spec),
			})
		default:
			disks = append(disks, map[string]any{
				"name": attachment.Name,
				"disk": map[string]any{"bus": "virtio"},
			})
		}
	}
	for i, binding := range spec.SecretBindings {
		if binding.SecretID == "" || binding.MountPath == "" {
			continue
		}
		disks = append(disks, map[string]any{
			"name":     secretVolumeName(binding, i),
			"disk":     map[string]any{"bus": "virtio"},
			"readOnly": true,
		})
	}
	return disks
}

func vmISOBootOrder(spec ports.WorkloadSpec) int32 {
	if spec.VM != nil && spec.VM.BootMediaBootOrder > 0 {
		return spec.VM.BootMediaBootOrder
	}
	return 1
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

func vmNetworkRefs(spec ports.WorkloadSpec) []any {
	if vmPodNetworkEnabled() {
		return []any{
			map[string]any{
				"name": "default",
				"pod":  map[string]any{},
			},
		}
	}
	return networkRefs(spec)
}

func vmPodNetworkEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WORKLOAD_VM_POD_NETWORK"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func networkRefs(spec ports.WorkloadSpec) []any {
	networks := make([]any, 0, len(spec.Network.Attachments))
	for _, attachment := range spec.Network.Attachments {
		networks = append(networks, map[string]any{
			"name": string(attachment.Plane),
			"multus": map[string]any{
				"networkName": attachment.NetworkID,
			},
		})
	}
	return networks
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
