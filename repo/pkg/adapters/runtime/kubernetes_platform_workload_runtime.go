package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

const (
	platformWorkloadClassLabel   = "ani.platform_workload"
	platformWorkloadTenantLabel  = "ani.kubercloud.io/tenant-id"
	platformWorkloadIDLabel      = "ani.kubercloud.io/platform-workload"
	platformWorkloadNameLabel    = "ani.kubercloud.io/platform-workload-name"
	platformWorkloadOwnerLabel   = "ani.kubercloud.io/owner-ref"
	platformWorkloadRuntimeShape = "deployment"
	platformWorkloadProviderName = "kubernetes"
	platformWorkloadRoleLabel    = "ani.kubercloud.io/inference-role"
	kubeOVNDefaultLogicalSwitch  = "ovn-default"
	kubeOVNDefaultVPC            = "ovn-cluster"
)

var (
	pvcClaimPattern   = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	vgpuSpecIDPattern = regexp.MustCompile(`-\d+x$`)
)

type platformWorkloadRuntime interface {
	Apply(ctx context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec) (platformWorkloadObservation, error)
	Observe(ctx context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec) (platformWorkloadObservation, error)
	Delete(ctx context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec) error
	Logs(ctx context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec, limit int, cursor, level string) (ports.PlatformWorkloadLogList, error)
}

type platformWorkloadCapabilitySource interface {
	DiscoverCapabilities(context.Context) (ports.PlatformWorkloadCapabilities, error)
}

type platformWorkloadObservation struct {
	ReadyReplicas int
	Endpoint      string
	Ready         bool
	Reason        string
}

type KubernetesPlatformWorkloadRuntime struct {
	client *KubernetesRESTClient
}

func NewKubernetesPlatformWorkloadRuntime(client *KubernetesRESTClient) *KubernetesPlatformWorkloadRuntime {
	return &KubernetesPlatformWorkloadRuntime{client: client}
}

func (r *KubernetesPlatformWorkloadRuntime) Apply(ctx context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec) (platformWorkloadObservation, error) {
	if r == nil || r.client == nil {
		return platformWorkloadObservation{}, fmt.Errorf("%w: kubernetes platform workload client is not configured", ports.ErrUnavailable)
	}
	nodeCIDRs, err := r.client.ListNodeInternalCIDRs(ctx)
	if err != nil {
		return platformWorkloadObservation{}, err
	}
	manifests := append([]ports.WorkloadManifest{renderPlatformWorkloadNamespace(tenantID)}, renderPlatformWorkloadManifests(tenantID, workloadID, spec, nodeCIDRs)...)
	if _, err := r.client.ApplyManifests(ctx, manifests); err != nil {
		return platformWorkloadObservation{}, err
	}
	return platformWorkloadObservation{
		Endpoint: platformWorkloadEndpoint(tenantID, spec),
		Reason:   "applied",
	}, nil
}

func (r *KubernetesPlatformWorkloadRuntime) Observe(ctx context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec) (platformWorkloadObservation, error) {
	if r == nil || r.client == nil {
		return platformWorkloadObservation{}, fmt.Errorf("%w: kubernetes platform workload client is not configured", ports.ErrUnavailable)
	}
	_ = workloadID
	kind := "Deployment"
	if spec.Topology.Mode == "leader_worker" {
		kind = "LeaderWorkerSet"
	}
	resource, err := resourceFromRef(platformWorkloadProviderName, tenantNamespace(tenantID), "kubernetes/"+kind+"/"+platformWorkloadResourceName(spec.Name))
	if err != nil {
		return platformWorkloadObservation{}, err
	}
	body, status, err := r.client.Do(ctx, http.MethodGet, r.client.resourceURL(resource, ""), "", nil)
	if err != nil {
		if status == http.StatusNotFound || isKubernetesNotFound(err) {
			return platformWorkloadObservation{Reason: "NotFound"}, nil
		}
		return platformWorkloadObservation{}, err
	}
	var readyReplicas int
	if spec.Topology.Mode == "leader_worker" {
		readyReplicas, err = readyReplicasFromLeaderWorkerSet(body)
	} else {
		readyReplicas, err = readyReplicasFromDeployment(body)
	}
	if err != nil {
		return platformWorkloadObservation{}, err
	}
	endpoint := platformWorkloadEndpoint(tenantID, spec)
	return platformWorkloadObservation{
		ReadyReplicas: readyReplicas,
		Endpoint:      endpoint,
		Ready:         readyReplicas >= spec.Replicas && spec.Replicas > 0 && endpoint != "",
	}, nil
}

func (r *KubernetesPlatformWorkloadRuntime) Delete(ctx context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("%w: kubernetes platform workload client is not configured", ports.ErrUnavailable)
	}
	_ = workloadID
	namespace := tenantNamespace(tenantID)
	resourceName := platformWorkloadResourceName(spec.Name)
	refs := []string{
		"kubernetes/Service/" + platformWorkloadServiceName(spec),
		"kubernetes/NetworkPolicy/" + resourceName,
	}
	if spec.Topology.Mode == "leader_worker" {
		refs = append(refs,
			"kubernetes/Service/"+resourceName,
			"kubernetes/LeaderWorkerSet/"+resourceName,
			"kubernetes/PodGroup/"+resourceName,
			"kubernetes/StatefulSet/"+resourceName,
			"kubernetes/StatefulSet/"+resourceName+"-0",
		)
	} else {
		refs = append(refs, "kubernetes/Deployment/"+resourceName)
	}
	for _, ref := range refs {
		resource, err := resourceFromRef(platformWorkloadProviderName, namespace, ref)
		if err != nil {
			return err
		}
		_, status, err := r.client.Do(ctx, http.MethodDelete, r.client.resourceURL(resource, ""), "", nil)
		if err != nil && status != http.StatusNotFound && !isKubernetesNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *KubernetesPlatformWorkloadRuntime) Logs(ctx context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec, limit int, cursor, level string) (ports.PlatformWorkloadLogList, error) {
	if r == nil || r.client == nil {
		return ports.PlatformWorkloadLogList{}, fmt.Errorf("%w: kubernetes platform workload client is not configured", ports.ErrUnavailable)
	}
	_ = workloadID
	_ = cursor
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	namespace := tenantNamespace(tenantID)
	pods, err := r.listWorkloadPods(ctx, namespace, spec.Name)
	if err != nil {
		return ports.PlatformWorkloadLogList{}, err
	}
	items := make([]ports.PlatformWorkloadLogEntry, 0)
	for _, pod := range pods {
		query := url.Values{}
		query.Set("timestamps", "true")
		query.Set("tailLines", strconv.Itoa(limit))
		if pod.container != "" {
			query.Set("container", pod.container)
		}
		body, status, err := r.client.Do(ctx, http.MethodGet, r.client.host+podPath(namespace, pod.name)+"/log?"+query.Encode(), "", nil)
		if err != nil {
			if status == http.StatusNotFound || status == http.StatusBadRequest || isKubernetesNotFound(err) {
				continue
			}
			return ports.PlatformWorkloadLogList{}, err
		}
		items = append(items, parsePlatformWorkloadPodLogs(body, pod.container, level)...)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Timestamp.Before(items[j].Timestamp) })
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	return ports.PlatformWorkloadLogList{Items: items}, nil
}

type platformWorkloadPod struct {
	name      string
	container string
}

func (r *KubernetesPlatformWorkloadRuntime) listWorkloadPods(ctx context.Context, namespace, resourceName string) ([]platformWorkloadPod, error) {
	selector := url.QueryEscape(platformWorkloadNameLabel + "=" + resourceName)
	endpoint := r.client.host + "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods?labelSelector=" + selector
	body, status, err := r.client.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		if status == http.StatusNotFound || isKubernetesNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%w: invalid Kubernetes pod list: %v", ports.ErrInvalid, err)
	}
	rawItems, _ := doc["items"].([]any)
	pods := make([]platformWorkloadPod, 0, len(rawItems))
	for _, raw := range rawItems {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		metadata, _ := item["metadata"].(map[string]any)
		name, _ := metadata["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		container := resourceName
		spec, _ := item["spec"].(map[string]any)
		containers, _ := spec["containers"].([]any)
		if len(containers) > 0 {
			first, _ := containers[0].(map[string]any)
			if value, _ := first["name"].(string); strings.TrimSpace(value) != "" {
				container = value
			}
		}
		pods = append(pods, platformWorkloadPod{name: name, container: container})
	}
	return pods, nil
}

func parsePlatformWorkloadPodLogs(body []byte, container, level string) []ports.PlatformWorkloadLogEntry {
	wanted := strings.ToLower(strings.TrimSpace(level))
	lines := strings.Split(string(body), "\n")
	items := make([]ports.PlatformWorkloadLogEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		timestamp := time.Now().UTC()
		message := line
		if stamp, rest, ok := strings.Cut(line, " "); ok {
			if parsed, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
				timestamp = parsed.UTC()
				message = rest
			}
		}
		message = redactPlatformWorkloadLog(message)
		entryLevel := platformWorkloadLogLevel(message)
		if wanted != "" && entryLevel != wanted {
			continue
		}
		items = append(items, ports.PlatformWorkloadLogEntry{
			Timestamp: timestamp,
			Level:     entryLevel,
			Message:   message,
			Container: container,
			Stream:    "stdout",
		})
	}
	return items
}

func platformWorkloadLogLevel(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "error") || strings.Contains(lower, "fatal"):
		return "error"
	case strings.Contains(lower, "warn"):
		return "warn"
	case strings.Contains(lower, "debug"):
		return "debug"
	default:
		return "info"
	}
}

func redactPlatformWorkloadLog(message string) string {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "authorization") ||
		strings.Contains(lower, "bearer ") ||
		strings.Contains(lower, "password") ||
		strings.Contains(message, "eyJ") {
		return "[redacted]"
	}
	return message
}

func renderPlatformWorkloadNamespace(tenantID string) ports.WorkloadManifest {
	name := tenantNamespace(tenantID)
	content := manifest(map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name": name,
			"labels": map[string]string{
				"app.kubernetes.io/part-of": "ani-platform",
				platformWorkloadTenantLabel: tenantID,
			},
		},
	})
	return ports.WorkloadManifest{Name: name, Kind: "Namespace", Provider: platformWorkloadProviderName, Content: content}
}

func renderPlatformWorkloadManifests(tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec, nodeCIDRs []string) []ports.WorkloadManifest {
	if spec.Topology.Mode == "leader_worker" {
		return renderLeaderWorkerPlatformWorkloadManifests(tenantID, workloadID, spec, nodeCIDRs)
	}
	namespace := tenantNamespace(tenantID)
	resourceName := platformWorkloadResourceName(spec.Name)
	podLabels := platformWorkloadPodLabels(tenantID, workloadID, spec)
	selector := platformWorkloadSelectorLabels(tenantID, spec)
	containerPorts, servicePorts := platformWorkloadNetworkPorts(spec)
	volumes, volumeMounts := platformWorkloadPodVolumes(spec)
	container := platformWorkloadContainer(spec, resourceName, spec.Resources, spec.Command, spec.Args, containerPorts, volumeMounts, true)
	podSpec := platformWorkloadPodSpec(spec, []any{container}, volumes, "", false)
	templateMeta := map[string]any{"labels": podLabels}
	if annotations := platformWorkloadPodAnnotations(spec, ""); len(annotations) > 0 {
		templateMeta["annotations"] = annotations
	}
	deployment := manifest(map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      resourceName,
			"namespace": namespace,
			"labels":    podLabels,
		},
		"spec": map[string]any{
			"replicas": spec.Replicas,
			"strategy": map[string]any{"type": "Recreate"},
			"selector": map[string]any{"matchLabels": selector},
			"template": map[string]any{
				"metadata": templateMeta,
				"spec":     podSpec,
			},
		},
	})
	return []ports.WorkloadManifest{
		{Name: resourceName, Kind: "Deployment", Provider: platformWorkloadProviderName, Content: deployment},
		renderPlatformWorkloadService(tenantID, workloadID, spec, selector, servicePorts),
		renderPlatformWorkloadNetworkPolicy(tenantID, workloadID, spec, nodeCIDRs),
	}
}

func renderLeaderWorkerPlatformWorkloadManifests(tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec, nodeCIDRs []string) []ports.WorkloadManifest {
	namespace := tenantNamespace(tenantID)
	resourceName := platformWorkloadResourceName(spec.Name)
	podLabels := platformWorkloadPodLabels(tenantID, workloadID, spec)
	leaderLabels := mergeStringMap(podLabels, map[string]string{platformWorkloadRoleLabel: "leader"})
	workerLabels := mergeStringMap(podLabels, map[string]string{platformWorkloadRoleLabel: "worker"})
	serviceSelector := mergeStringMap(platformWorkloadSelectorLabels(tenantID, spec), map[string]string{platformWorkloadRoleLabel: "leader"})
	containerPorts, servicePorts := platformWorkloadNetworkPorts(spec)
	volumes, volumeMounts := platformWorkloadPodVolumes(spec)
	leaderResources := roleResourcesOrFallback(spec.Topology.Leader.Resources, spec.Resources)
	workerResources := roleResourcesOrFallback(spec.Topology.Workers.Resources, spec.Resources)
	if leaderResources.AcceleratorCount < 1 {
		leaderResources.AcceleratorCount = 1
		leaderResources.AcceleratorSpecID = spec.Resources.AcceleratorSpecID
		leaderResources.AcceleratorMemoryMB = spec.Resources.AcceleratorMemoryMB
	}
	if workerResources.AcceleratorCount < 1 {
		workerResources.AcceleratorCount = 1
		workerResources.AcceleratorSpecID = spec.Resources.AcceleratorSpecID
		workerResources.AcceleratorMemoryMB = spec.Resources.AcceleratorMemoryMB
	}
	size := 1 + spec.Topology.Workers.Count
	leaderContainer := platformWorkloadContainer(spec, resourceName, leaderResources, spec.Command, spec.Args, containerPorts, volumeMounts, true)
	workerCommand, workerArgs := platformWorkloadWorkerLaunch()
	workerContainer := platformWorkloadContainer(spec, resourceName, workerResources, workerCommand, workerArgs, nil, volumeMounts, false)
	leaderTemplate := map[string]any{
		"metadata": map[string]any{
			"labels":      leaderLabels,
			"annotations": platformWorkloadPodAnnotations(spec, resourceName),
		},
		"spec": platformWorkloadPodSpec(spec, []any{leaderContainer}, volumes, resourceName, true),
	}
	workerTemplate := map[string]any{
		"metadata": map[string]any{
			"labels":      workerLabels,
			"annotations": platformWorkloadPodAnnotations(spec, resourceName),
		},
		"spec": platformWorkloadPodSpec(spec, []any{workerContainer}, volumes, resourceName, true),
	}
	lws := manifest(map[string]any{
		"apiVersion": "leaderworkerset.x-k8s.io/v1",
		"kind":       "LeaderWorkerSet",
		"metadata": map[string]any{
			"name":      resourceName,
			"namespace": namespace,
			"labels":    podLabels,
		},
		"spec": map[string]any{
			"replicas": spec.Replicas,
			"leaderWorkerTemplate": map[string]any{
				"size":           size,
				"restartPolicy":  "RecreateGroupOnPodRestart",
				"leaderTemplate": leaderTemplate,
				"workerTemplate": workerTemplate,
			},
		},
	})
	podGroup := manifest(map[string]any{
		"apiVersion": "scheduling.volcano.sh/v1beta1",
		"kind":       "PodGroup",
		"metadata": map[string]any{
			"name":      resourceName,
			"namespace": namespace,
			"labels":    podLabels,
		},
		"spec": map[string]any{
			"minMember":    size,
			"minResources": platformWorkloadAcceleratorResourceMap(spec.Resources),
		},
	})
	return []ports.WorkloadManifest{
		{Name: resourceName, Kind: "LeaderWorkerSet", Provider: platformWorkloadProviderName, Content: lws},
		{Name: resourceName, Kind: "PodGroup", Provider: platformWorkloadProviderName, Content: podGroup},
		renderPlatformWorkloadService(tenantID, workloadID, spec, serviceSelector, servicePorts),
		renderPlatformWorkloadNetworkPolicy(tenantID, workloadID, spec, nodeCIDRs),
	}
}

func renderPlatformWorkloadService(tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec, selector map[string]string, servicePorts []any) ports.WorkloadManifest {
	serviceName := platformWorkloadServiceName(spec)
	content := manifest(map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      serviceName,
			"namespace": tenantNamespace(tenantID),
			"labels":    platformWorkloadPodLabels(tenantID, workloadID, spec),
		},
		"spec": map[string]any{
			"type":     "ClusterIP",
			"selector": selector,
			"ports":    servicePorts,
		},
	})
	return ports.WorkloadManifest{Name: serviceName, Kind: "Service", Provider: platformWorkloadProviderName, Content: content}
}

func renderPlatformWorkloadNetworkPolicy(tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec, nodeCIDRs []string) ports.WorkloadManifest {
	namespace := tenantNamespace(tenantID)
	resourceName := platformWorkloadResourceName(spec.Name)
	selector := platformWorkloadSelectorLabels(tenantID, spec)
	policyPorts := make([]any, 0, len(spec.Network.Ports))
	for _, port := range spec.Network.Ports {
		policyPorts = append(policyPorts, map[string]any{
			"protocol": "TCP",
			"port":     port.Port,
		})
	}
	if len(policyPorts) == 0 {
		policyPorts = append(policyPorts, map[string]any{"protocol": "TCP", "port": 8000})
	}
	ingress := []any{
		map[string]any{
			"from":  platformWorkloadNetworkPolicyFrom(nodeCIDRs),
			"ports": policyPorts,
		},
	}
	if spec.Topology.Mode == "leader_worker" {
		// LWS worker 要用 CoreDNS 解析 leader，并用 Ray/NCCL 动态端口连同组 Pod。
		ingress = append(ingress,
			map[string]any{
				"from": []any{map[string]any{"namespaceSelector": map[string]any{
					"matchLabels": map[string]any{"kubernetes.io/metadata.name": "kube-system"},
				}}},
				"ports": []any{
					map[string]any{"protocol": "UDP", "port": 53},
					map[string]any{"protocol": "TCP", "port": 53},
				},
			},
			map[string]any{
				"from": []any{map[string]any{"podSelector": map[string]any{"matchLabels": selector}}},
			},
		)
	}
	content := manifest(map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata": map[string]any{
			"name":      resourceName,
			"namespace": namespace,
			"labels":    platformWorkloadPodLabels(tenantID, workloadID, spec),
		},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": selector},
			"policyTypes": []any{"Ingress"},
			"ingress":     ingress,
		},
	})
	return ports.WorkloadManifest{Name: resourceName, Kind: "NetworkPolicy", Provider: platformWorkloadProviderName, Content: content}
}

// platformWorkloadPodVolumes mounts tenant-local model directories. Only
// pvc://<claim> is applied; object:// and hostPath are ignored here so a
// missing local PVC cannot silently become a node path.
func platformWorkloadPodVolumes(spec ports.PlatformWorkloadCreateSpec) ([]any, []any) {
	volumes := []any{
		map[string]any{"name": "shm", "emptyDir": map[string]any{"medium": "Memory", "sizeLimit": platformWorkloadSHMSize(spec)}},
	}
	mounts := []any{
		map[string]any{"name": "shm", "mountPath": "/dev/shm"},
	}
	for index, artifact := range spec.Artifacts {
		claim, ok := pvcClaimName(artifact.ObjectRef)
		if !ok {
			continue
		}
		name := "artifact-" + strconv.Itoa(index+1)
		path := strings.TrimSpace(artifact.MountPath)
		if path == "" {
			path = "/models"
		}
		volumes = append(volumes, map[string]any{
			"name":                  name,
			"persistentVolumeClaim": map[string]any{"claimName": claim},
		})
		mounts = append(mounts, map[string]any{"name": name, "mountPath": path})
	}
	return volumes, mounts
}

func platformWorkloadSHMSize(spec ports.PlatformWorkloadCreateSpec) string {
	if spec.Topology.Mode == "leader_worker" || spec.Resources.AcceleratorCount >= 2 {
		return "12Gi"
	}
	return "1Gi"
}

func pvcClaimName(objectRef string) (string, bool) {
	ref := strings.TrimSpace(objectRef)
	ref, _, _ = strings.Cut(ref, "#")
	name, ok := strings.CutPrefix(ref, "pvc://")
	name = strings.TrimSpace(name)
	if !ok || !pvcClaimPattern.MatchString(name) {
		return "", false
	}
	return name, true
}

func platformWorkloadContainerResources(resources ports.PlatformWorkloadResources) map[string]any {
	requests := map[string]any{"cpu": resources.CPU, "memory": resources.Memory}
	for name, value := range platformWorkloadAcceleratorResourceMap(resources) {
		requests[name] = value
	}
	return map[string]any{"requests": requests, "limits": requests}
}

// volcanoGPUMemoryFactor 是现网 volcano-device-plugin 的 gpuMemoryFactor。
// 产品 memory 是 MiB；写入 volcano.sh/vgpu-memory 时除以该因子。
const volcanoGPUMemoryFactor = 10

// platformWorkloadAcceleratorResourceMap 按 memory 选择资源：
// 有 AcceleratorMemoryMB → vGPU；没有 → 整卡。不看 spec_id 后缀。
func platformWorkloadAcceleratorResourceMap(resources ports.PlatformWorkloadResources) map[string]any {
	if resources.AcceleratorCount < 1 {
		return nil
	}
	if resources.AcceleratorMemoryMB > 0 {
		return map[string]any{
			kubernetesVolcanoVGPUNumberResource: strconv.Itoa(resources.AcceleratorCount),
			kubernetesVolcanoVGPUMemoryResource: strconv.Itoa(volcanoVGPUMemoryUnits(resources.AcceleratorMemoryMB)),
		}
	}
	return map[string]any{kubernetesNVIDIAGPUResource: strconv.Itoa(resources.AcceleratorCount)}
}

func volcanoVGPUMemoryUnits(memoryMB int) int {
	if memoryMB < 1 {
		return 0
	}
	units := (memoryMB + volcanoGPUMemoryFactor - 1) / volcanoGPUMemoryFactor
	if units < 1 {
		return 1
	}
	return units
}

// canonicalAcceleratorSpecID 把历史 -full / -Nx 剥掉，得到型号 ID。
func canonicalAcceleratorSpecID(specID string) string {
	id := strings.ToLower(strings.TrimSpace(specID))
	id = vgpuSpecIDPattern.ReplaceAllString(id, "")
	return strings.TrimSuffix(id, "-full")
}

func platformWorkloadNetworkPorts(spec ports.PlatformWorkloadCreateSpec) ([]any, []any) {
	containerPorts := make([]any, 0, len(spec.Network.Ports))
	servicePorts := make([]any, 0, len(spec.Network.Ports))
	for index, port := range spec.Network.Ports {
		name := strings.TrimSpace(port.Name)
		if name == "" {
			name = "port-" + strconv.Itoa(index+1)
		}
		containerPorts = append(containerPorts, map[string]any{
			"name":          name,
			"containerPort": port.Port,
			"protocol":      "TCP",
		})
		servicePorts = append(servicePorts, map[string]any{
			"name":       name,
			"port":       port.Port,
			"targetPort": port.Port,
			"protocol":   "TCP",
		})
	}
	return containerPorts, servicePorts
}

func platformWorkloadReadinessProbe(spec ports.PlatformWorkloadCreateSpec) map[string]any {
	healthName, healthPort := platformWorkloadHealthPort(spec)
	probe := map[string]any{
		"httpGet": map[string]any{
			"path": spec.HealthCheck.Path,
			"port": healthName,
		},
		"periodSeconds":    10,
		"timeoutSeconds":   3,
		"failureThreshold": 90,
	}
	if healthName == "" {
		probe["httpGet"].(map[string]any)["port"] = healthPort
	}
	return probe
}

func platformWorkloadContainer(spec ports.PlatformWorkloadCreateSpec, name string, resources ports.PlatformWorkloadResources, command, args []string, containerPorts []any, volumeMounts []any, ready bool) map[string]any {
	container := map[string]any{
		"name":            name,
		"image":           spec.ImageRef,
		"imagePullPolicy": "IfNotPresent",
		"command":         omitEmptySlice(command),
		"args":            omitEmptySlice(args),
		"resources":       platformWorkloadContainerResources(resources),
		"volumeMounts":    volumeMounts,
	}
	if len(containerPorts) > 0 {
		container["ports"] = containerPorts
	}
	if ready {
		container["readinessProbe"] = platformWorkloadReadinessProbe(spec)
	}
	if env := platformWorkloadContainerEnv(spec, resources); len(env) > 0 {
		container["env"] = env
	}
	return container
}

func platformWorkloadContainerEnv(spec ports.PlatformWorkloadCreateSpec, resources ports.PlatformWorkloadResources) []any {
	env := make([]any, 0, len(spec.Env)+3)
	if spec.Topology.Mode == "leader_worker" && resources.AcceleratorCount >= 1 {
		// 写进 container env，避免 raylet/C++ worker 只拿到 nvidia runtime 的初始 void。
		// 不要在 Pod spec 里写 NVIDIA_VISIBLE_DEVICES=all，那会绕过 device-plugin 隔离。
		env = append(env,
			map[string]any{"name": "CUDA_VISIBLE_DEVICES", "value": "0"},
			map[string]any{"name": "RAY_EXPERIMENTAL_NOSET_CUDA_VISIBLE_DEVICES", "value": "1"},
			map[string]any{"name": "PYTHONPATH", "value": "/tmp"},
			map[string]any{"name": "VLLM_USE_RAY_COMPILED_DAG", "value": "0"},
		)
	}
	for _, item := range spec.Env {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		env = append(env, map[string]any{"name": name, "value": item.Value})
	}
	return env
}

func platformWorkloadPodSpec(spec ports.PlatformWorkloadCreateSpec, containers []any, volumes []any, podGroupName string, forceVolcano bool) map[string]any {
	podSpec := map[string]any{
		"restartPolicy": "Always",
		"containers":    containers,
		"volumes":       volumes,
	}
	if forceVolcano || spec.Resources.AcceleratorCount > 0 {
		podSpec["schedulerName"] = kubernetesVolcanoSchedulerName
	}
	_ = podGroupName
	return podSpec
}

// platformWorkloadPodAnnotations always pins the cluster default overlay.
// PlatformWorkload has no VPC/subnet field; inheriting a tenant instance VPC
// makes kubelet health probes fail with no route to host.
func platformWorkloadPodAnnotations(spec ports.PlatformWorkloadCreateSpec, podGroupName string) map[string]string {
	annotations := map[string]string{
		"ovn.kubernetes.io/logical_switch": kubeOVNDefaultLogicalSwitch,
		"ovn.kubernetes.io/vpc":            kubeOVNDefaultVPC,
	}
	if podGroupName != "" {
		annotations["scheduling.k8s.io/group-name"] = podGroupName
	}
	return annotations
}

const (
	// vLLM Ray executor 会按集群 GPU 编号覆盖 CUDA_VISIBLE_DEVICES；每个 LWS Pod
	// 只有容器内 index 0。sitecustomize 把后续赋值钉死，避免 worker 变成 1 或空串。
	writeRayCUDASiteCustomize = `python3 -c 'open("/tmp/sitecustomize.py","w").write("import os\n_s=os.environ.__class__.__setitem__\ndef _g(e,k,v):\n    if k==\"CUDA_VISIBLE_DEVICES\": v=\"0\"\n    if k==\"NVIDIA_VISIBLE_DEVICES\": v=\"all\"\n    _s(e,k,v)\nos.environ.__class__.__setitem__=_g\nos.environ[\"NVIDIA_VISIBLE_DEVICES\"]=\"all\"\nos.environ[\"CUDA_VISIBLE_DEVICES\"]=\"0\"\n")'`
	rayGPUProcessEnv          = "env NVIDIA_VISIBLE_DEVICES=all CUDA_VISIBLE_DEVICES=0 PYTHONPATH=/tmp RAY_EXPERIMENTAL_NOSET_CUDA_VISIBLE_DEVICES=1 VLLM_USE_RAY_COMPILED_DAG=0"
)

func platformWorkloadWorkerLaunch() ([]string, []string) {
	return []string{"sh", "-c"}, []string{
		writeRayCUDASiteCustomize + " && " + rayGPUProcessEnv + " ray start --address=${LWS_LEADER_ADDRESS}:6379 --num-gpus=1 --block",
	}
}

func roleResourcesOrFallback(role, fallback ports.PlatformWorkloadResources) ports.PlatformWorkloadResources {
	out := role
	if strings.TrimSpace(out.CPU) == "" {
		out.CPU = fallback.CPU
	}
	if strings.TrimSpace(out.Memory) == "" {
		out.Memory = fallback.Memory
	}
	if strings.TrimSpace(out.AcceleratorSpecID) == "" {
		out.AcceleratorSpecID = fallback.AcceleratorSpecID
	}
	if out.AcceleratorMemoryMB < 1 {
		out.AcceleratorMemoryMB = fallback.AcceleratorMemoryMB
	}
	return out
}

func platformWorkloadPodLabels(tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec) map[string]string {
	labels := mergeStringMap(spec.Metadata.Labels, map[string]string{
		"app.kubernetes.io/part-of":    "ani-platform",
		"app.kubernetes.io/name":       "platform-workload",
		"app.kubernetes.io/component":  "runtime",
		"app.kubernetes.io/managed-by": "ani-platform-workload",
		platformWorkloadClassLabel:     spec.WorkloadClass,
		platformWorkloadTenantLabel:    tenantID,
		platformWorkloadIDLabel:        workloadID,
		platformWorkloadNameLabel:      spec.Name,
		platformWorkloadOwnerLabel:     spec.Metadata.OwnerRef,
	})
	if spec.Resources.AcceleratorSpecID != "" {
		labels["ani.kubercloud.io/accelerator-spec-id"] = spec.Resources.AcceleratorSpecID
	}
	return labels
}

func platformWorkloadSelectorLabels(tenantID string, spec ports.PlatformWorkloadCreateSpec) map[string]string {
	return map[string]string{
		platformWorkloadClassLabel:  spec.WorkloadClass,
		platformWorkloadTenantLabel: tenantID,
		platformWorkloadNameLabel:   spec.Name,
	}
}

func platformWorkloadHealthPort(spec ports.PlatformWorkloadCreateSpec) (string, int) {
	for _, port := range spec.Network.Ports {
		if port.Name == spec.HealthCheck.PortName {
			return port.Name, port.Port
		}
	}
	if len(spec.Network.Ports) > 0 {
		return spec.Network.Ports[0].Name, spec.Network.Ports[0].Port
	}
	return "http", 8000
}

func platformWorkloadEndpoint(tenantID string, spec ports.PlatformWorkloadCreateSpec) string {
	_, port := platformWorkloadHealthPort(spec)
	if len(spec.Network.Ports) > 0 {
		port = spec.Network.Ports[0].Port
	}
	return "http://" + platformWorkloadServiceName(spec) + "." + tenantNamespace(tenantID) + ".svc:" + strconv.Itoa(port)
}

func platformWorkloadServiceName(spec ports.PlatformWorkloadCreateSpec) string {
	name := platformWorkloadResourceName(spec.Name)
	if spec.Topology.Mode == "leader_worker" {
		// LWS controller 占用同名 headless Service 做 Ray DNS；产品 ClusterIP 必须让开。
		return name + "-http"
	}
	return name
}

func platformWorkloadNetworkPolicyFrom(nodeCIDRs []string) []any {
	from := []any{
		map[string]any{"podSelector": map[string]any{}},
		map[string]any{"namespaceSelector": map[string]any{
			"matchLabels": map[string]any{"kubernetes.io/metadata.name": "kube-system"},
		}},
		map[string]any{"namespaceSelector": map[string]any{
			"matchLabels": map[string]any{"kubernetes.io/metadata.name": "ani-system"},
		}},
	}
	for _, cidr := range platformWorkloadNodeCIDRs(nodeCIDRs) {
		from = append(from, map[string]any{"ipBlock": map[string]any{"cidr": cidr}})
	}
	return from
}

func platformWorkloadNodeCIDRs(cidrs []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(cidrs))
	for _, raw := range cidrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "/") {
			raw += "/32"
		}
		ip, ipnet, err := net.ParseCIDR(raw)
		if err != nil || ip == nil || ip.To4() == nil || ipnet == nil {
			continue
		}
		ones, bits := ipnet.Mask.Size()
		if ones != 32 || bits != 32 {
			continue
		}
		cidr := ip.To4().String() + "/32"
		if _, ok := seen[cidr]; ok {
			continue
		}
		seen[cidr] = struct{}{}
		out = append(out, cidr)
	}
	sort.Strings(out)
	return out
}

func platformWorkloadResourceName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "pw"
	}
	if name[0] < 'a' || name[0] > 'z' {
		return "pw-" + name
	}
	return name
}

func readyReplicasFromDeployment(body []byte) (int, error) {
	return readyReplicasFromStatus(body, "deployment")
}

func readyReplicasFromLeaderWorkerSet(body []byte) (int, error) {
	return readyReplicasFromStatus(body, "leaderworkerset")
}

func readyReplicasFromStatus(body []byte, kind string) (int, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return 0, fmt.Errorf("%w: invalid Kubernetes %s observation: %v", ports.ErrInvalid, kind, err)
	}
	status, _ := doc["status"].(map[string]any)
	return jsonInt(status["readyReplicas"]), nil
}

func jsonInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
		return parsed
	default:
		return 0
	}
}

var _ platformWorkloadRuntime = (*KubernetesPlatformWorkloadRuntime)(nil)
