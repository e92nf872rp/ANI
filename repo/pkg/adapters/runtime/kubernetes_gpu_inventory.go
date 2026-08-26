package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

const (
	kubernetesNVIDIAGPUResource         = "nvidia.com/gpu"
	kubernetesNVIDIAVGPUResource        = "nvidia.com/vgpu"
	kubernetesVolcanoVGPUNumberResource = "volcano.sh/vgpu-number"
	kubernetesVolcanoVGPUMemoryResource = "volcano.sh/vgpu-memory"
	kubernetesNVIDIAGPUProductLabel     = "nvidia.com/gpu.product"
	kubernetesANIGPUModelLabel          = "ani.kubercloud.io/gpu-model"
	kubernetesHostnameLabel             = "kubernetes.io/hostname"
	kubernetesANIGPUPoolLabel           = "ani.kubercloud.io/gpu-pool"
	kubernetesVolcanoSchedulerName      = "volcano"
	kubernetesDefaultInferenceQueue     = "ani-inference"
	kubernetesDefaultTrainingQueue      = "ani-training"
	kubernetesGPUNodeSelectorLabel      = "ani.kubercloud.io/gpu-node"

	// Node label keys for GPU mode / spec derivation (plan.md §4.2).
	kubernetesANIGPUModeLabel          = "ani.kubercloud.io/gpu-mode"
	kubernetesANIGPUSpecLabel          = "ani.kubercloud.io/gpu-spec"
	kubernetesANIGPUSharingSpecLabel   = "ani.kubercloud.io/gpu-sharing-spec"
	kubernetesANIGPUSharingPolicyLabel = "ani.kubercloud.io/gpu-sharing-policy"

	// Node label key for physical GPU memory in MiB (NVIDIA device plugin).
	kubernetesNVIDIAGPUMemoryLabel = "nvidia.com/gpu.memory"

	// Volcano vGPU device registration annotation key. The Volcano vGPU
	// device plugin (based on HAMi-core) writes this annotation on each
	// GPU node. The format is a colon-separated list of physical GPU
	// records, each record being comma-separated fields:
	// GPU-ID,count,mb,type,health,mode (plan.md §4.6).
	// Example: "GPU-xxx,10,4914,NVIDIA-RTX4090,true,hami-core:GPU-yyy,10,4914,..."
	kubernetesVolcanoVGPURegisterAnnotation = "volcano.sh/node-vgpu-register"
)

// KubernetesGPUInventory discovers GPU capacity from Kubernetes nodes and
// maps workload intent to scheduling constraints. When a queue store is
// injected it resolves and validates Volcano queues; without one it still
// serves inventory lookups but PlanScheduling falls back to workload-class
// defaults without tenant validation. When a spec store and quota store are
// injected it can compute per-spec availability via ListSpecAvailability.
// When a quota admin service is injected, ListSpecAvailability uses the
// tenant's allocated_gpu_count (reservation) instead of the quota total,
// per plan.md §4.4.1.
type KubernetesGPUInventory struct {
	client     *KubernetesRESTClient
	queueStore ports.GPUSchedulingQueueStore
	specStore  ports.GPUSpecStore
	quotaStore ports.QuotaStoreService
	quotaAdmin ports.QuotaAdminService
}

// NewKubernetesGPUInventory builds an inventory adapter without queue
// validation. Use NewKubernetesGPUInventoryWithQueueStore to enable it.
func NewKubernetesGPUInventory(client *KubernetesRESTClient) *KubernetesGPUInventory {
	return &KubernetesGPUInventory{client: client}
}

// NewKubernetesGPUInventoryWithQueueStore returns an inventory that resolves
// and validates Volcano queues through the provided store.
func NewKubernetesGPUInventoryWithQueueStore(client *KubernetesRESTClient, store ports.GPUSchedulingQueueStore) *KubernetesGPUInventory {
	return &KubernetesGPUInventory{client: client, queueStore: store}
}

// NewKubernetesGPUInventoryWithSpecStore returns an inventory wired with a
// GPUSpecStore and QuotaStoreService so ListSpecAvailability can compute
// per-spec availability (SPEC §5.1).
func NewKubernetesGPUInventoryWithSpecStore(client *KubernetesRESTClient, queueStore ports.GPUSchedulingQueueStore, specStore ports.GPUSpecStore, quotaStore ports.QuotaStoreService) *KubernetesGPUInventory {
	return &KubernetesGPUInventory{client: client, queueStore: queueStore, specStore: specStore, quotaStore: quotaStore}
}

// WithQuotaAdmin injects a QuotaAdminService so ListSpecAvailability can
// read the tenant's allocated_gpu_count (reservation) per plan.md §4.4.1.
// When not set, ListSpecAvailability falls back to quota total.
func (i *KubernetesGPUInventory) WithQuotaAdmin(admin ports.QuotaAdminService) *KubernetesGPUInventory {
	i.quotaAdmin = admin
	return i
}

func (i *KubernetesGPUInventory) ListNodeClasses(ctx context.Context, filter ports.GPUDiscoveryFilter) ([]ports.GPUNodeClass, error) {
	if i.client == nil {
		return nil, fmt.Errorf("%w: Kubernetes REST client is required for GPU inventory", ports.ErrNotConfigured)
	}
	body, err := i.client.do(ctx, http.MethodGet, strings.TrimRight(i.client.host, "/")+"/api/v1/nodes", "", nil)
	if err != nil {
		return nil, err
	}
	nodes, err := gpuNodeClassesFromKubernetesNodeList(body)
	if err != nil {
		return nil, err
	}
	filtered := make([]ports.GPUNodeClass, 0, len(nodes))
	for _, node := range nodes {
		if matchesGPUDiscoveryFilter(node, filter) {
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

func (i *KubernetesGPUInventory) GetNodeClass(ctx context.Context, nodeName string) (ports.GPUNodeClass, error) {
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		return ports.GPUNodeClass{}, fmt.Errorf("%w: node_name is required", ports.ErrInvalid)
	}
	nodes, err := i.ListNodeClasses(ctx, ports.GPUDiscoveryFilter{Labels: map[string]string{kubernetesHostnameLabel: nodeName}})
	if err != nil {
		return ports.GPUNodeClass{}, err
	}
	for _, node := range nodes {
		if node.NodeName == nodeName {
			return node, nil
		}
	}
	return ports.GPUNodeClass{}, ports.ErrNotFound
}

// PlanScheduling maps a GPU scheduling request to Volcano scheduling
// constraints. It rejects unsupported vendors (Ascend/Hygon) and MIG mode in
// P0, resolves the Volcano queue (explicit or workload-class default), and
// selects whole-card or vGPU resource based on the requested virtualization
// mode. When no ready node satisfies the request it returns a decision with
// Reasons populated so the caller can surface a 422 error.
func (i *KubernetesGPUInventory) PlanScheduling(ctx context.Context, request ports.GPUSchedulingRequest) (ports.GPUSchedulingDecision, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.WorkloadID) == "" {
		return ports.GPUSchedulingDecision{}, fmt.Errorf("%w: tenant_id and workload_id are required", ports.ErrInvalid)
	}

	// P0 vendor gate: only NVIDIA is supported. Ascend/Hygon return a
	// decision with Reasons so callers can map to 422.
	if reasons := rejectUnsupportedVendorP0(request); len(reasons) > 0 {
		return ports.GPUSchedulingDecision{Reasons: reasons}, nil
	}

	// MIG is out of scope for P0.
	if reasons := rejectMIGModeP0(request); len(reasons) > 0 {
		return ports.GPUSchedulingDecision{Reasons: reasons}, nil
	}

	queueName, queueReason := i.resolveQueueName(ctx, request)
	if queueName == "" {
		return ports.GPUSchedulingDecision{Reasons: []string{queueReason}}, nil
	}

	requiredCount := positiveInt(request.RequiredCount, 1)
	_, mode := selectResourceName(request)

	nodes, err := i.ListNodeClasses(ctx, ports.GPUDiscoveryFilter{Vendors: request.PreferredVendors, Pool: request.Pool})
	if err != nil {
		return ports.GPUSchedulingDecision{}, err
	}

	for _, node := range nodes {
		if !node.Ready {
			continue
		}
		if !gpuNodeSupportsSchedulingRequest(node, request) {
			continue
		}
		resourceName := resourceNameForNode(node, mode)
		available := gpuAllocatableCount(node, resourceName)
		if available < requiredCount {
			continue
		}
		return ports.GPUSchedulingDecision{
			NodeSelector: map[string]string{
				kubernetesHostnameLabel:        node.NodeName,
				kubernetesGPUNodeSelectorLabel: "true",
			},
			ResourceName:      resourceName,
			ResourceQuantity:  strconv.Itoa(requiredCount),
			RuntimeClassName:  runtimeClassNameForNode(node, mode),
			SchedulerName:     kubernetesVolcanoSchedulerName,
			QueueName:         queueName,
			Reasons:           []string{fmt.Sprintf("Kubernetes node %s provides %d %s", node.NodeName, available, resourceName)},
			SelectedNodeModel: node.Model,
		}, nil
	}

	// Fallback resource name for the "no node found" message.
	fallbackResource := kubernetesNVIDIAGPUResource
	if mode == ports.GPUVirtualizationVGPU {
		fallbackResource = kubernetesNVIDIAVGPUResource
	}
	return ports.GPUSchedulingDecision{
		SchedulerName: kubernetesVolcanoSchedulerName,
		QueueName:     queueName,
		ResourceName:  fallbackResource,
		Reasons:       []string{fmt.Sprintf("no ready GPU node satisfies %s >= %d", fallbackResource, requiredCount)},
	}, nil
}

// resourceNameForNode returns the K8s extended resource name to request for
// scheduling on this node. Whole-card requests use nvidia.com/gpu; vGPU
// requests use nvidia.com/vgpu (the Volcano vGPU device plugin resource).
func resourceNameForNode(node ports.GPUNodeClass, mode ports.GPUVirtualizationMode) string {
	if mode != ports.GPUVirtualizationVGPU {
		return kubernetesNVIDIAGPUResource
	}
	return kubernetesNVIDIAVGPUResource
}

// rejectUnsupportedVendorP0 returns Reasons for non-NVIDIA vendors. P0 only
// supports NVIDIA; Ascend (huawei) and Hygon are P1.
func rejectUnsupportedVendorP0(request ports.GPUSchedulingRequest) []string {
	vendors := request.PreferredVendors
	if len(vendors) == 0 {
		return nil
	}
	for _, vendor := range vendors {
		switch vendor {
		case ports.GPUVendorNVIDIA, ports.GPUVendorUnknown:
			continue
		case ports.GPUVendorHuawei:
			return []string{"Ascend GPU is P1 未启用"}
		case ports.GPUVendorHygon:
			return []string{"Hygon GPU is P1 未启用"}
		}
	}
	return nil
}

// rejectMIGModeP0 returns Reasons when MIG virtualization is requested.
func rejectMIGModeP0(request ports.GPUSchedulingRequest) []string {
	for _, mode := range request.VirtualizationModes {
		if mode == ports.GPUVirtualizationMIG {
			return []string{"MIG is P1 未启用"}
		}
	}
	return nil
}

// resolveQueueName validates an explicit queue or selects a workload-class
// default. When a queue store is injected the explicit queue must exist and
// belong to the tenant; without a store only the default resolution runs.
func (i *KubernetesGPUInventory) resolveQueueName(ctx context.Context, request ports.GPUSchedulingRequest) (string, string) {
	explicit := strings.TrimSpace(request.QueueName)
	if explicit != "" {
		if i.queueStore == nil {
			return explicit, ""
		}
		queues, err := i.queueStore.List(ctx, request.TenantID)
		if err != nil {
			return "", fmt.Sprintf("queue store unavailable: %v", err)
		}
		for _, queue := range queues {
			if queue.Name == explicit {
				return explicit, ""
			}
		}
		return "", fmt.Sprintf("queue %q not found for tenant", explicit)
	}
	return defaultQueueName(request.WorkloadClass), ""
}

// defaultQueueName maps a workload class to the platform default Volcano
// queue. inference→ani-inference; training and batch→ani-training.
func defaultQueueName(class ports.WorkloadClass) string {
	switch class {
	case ports.WorkloadClassInference:
		return kubernetesDefaultInferenceQueue
	case ports.WorkloadClassTraining, ports.WorkloadClassBatch:
		return kubernetesDefaultTrainingQueue
	default:
		return kubernetesDefaultInferenceQueue
	}
}

// selectResourceName returns the K8s extended resource name and the effective
// virtualization mode for the request. Non-HAMi clusters distinguish via
// nvidia.com/vgpu for vGPU and nvidia.com/gpu for whole-card.
func selectResourceName(request ports.GPUSchedulingRequest) (string, ports.GPUVirtualizationMode) {
	for _, mode := range request.VirtualizationModes {
		if mode == ports.GPUVirtualizationVGPU {
			return kubernetesNVIDIAVGPUResource, ports.GPUVirtualizationVGPU
		}
	}
	return kubernetesNVIDIAGPUResource, ports.GPUVirtualizationNone
}

// runtimeClassNameForNode returns the runtime class for a node given the
// requested virtualization mode. With HAMi removed, all nodes use the
// Volcano/native device plugin and the runtime class is always empty.
func runtimeClassNameForNode(node ports.GPUNodeClass, mode ports.GPUVirtualizationMode) string {
	return ""
}

// runtimeClassNameForMode returns the runtime class for a virtualization mode
// when the node management type is unknown. With HAMi removed, the runtime
// class is always empty. Kept for backward compatibility with local_gpu_inventory.go.
func runtimeClassNameForMode(mode ports.GPUVirtualizationMode) string {
	return ""
}

// gpuAllocatableCount reads an extended resource from the node allocatable
// map and returns its integer value. Returns 0 when the resource is absent
// or not a positive integer.
func gpuAllocatableCount(node ports.GPUNodeClass, resourceName string) int {
	value := strings.TrimSpace(node.Allocatable[resourceName])
	if value == "" {
		return 0
	}
	count, err := strconv.Atoi(value)
	if err != nil || count < 0 {
		return 0
	}
	return count
}

type kubernetesNodeListDocument struct {
	Items []kubernetesNodeDocument `json:"items"`
}

type kubernetesNodeDocument struct {
	Metadata kubernetesObjectMetadata `json:"metadata"`
	Status   kubernetesNodeStatus     `json:"status"`
}

type kubernetesObjectMetadata struct {
	Name        string            `json:"name"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

type kubernetesNodeStatus struct {
	Capacity    map[string]string         `json:"capacity"`
	Allocatable map[string]string         `json:"allocatable"`
	NodeInfo    kubernetesNodeSystemInfo  `json:"nodeInfo"`
	Conditions  []kubernetesNodeCondition `json:"conditions"`
}

type kubernetesNodeSystemInfo struct {
	KernelVersion  string `json:"kernelVersion"`
	OSImage        string `json:"osImage"`
	KubeletVersion string `json:"kubeletVersion"`
}

type kubernetesNodeCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

func gpuNodeClassesFromKubernetesNodeList(body []byte) ([]ports.GPUNodeClass, error) {
	var doc kubernetesNodeListDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%w: invalid Kubernetes node list response: %v", ports.ErrInvalid, err)
	}
	nodes := make([]ports.GPUNodeClass, 0, len(doc.Items))
	for _, item := range doc.Items {
		if !hasGPUResource(item.Status.Capacity, item.Status.Allocatable) {
			continue
		}
		nodeName := firstNonEmpty(item.Metadata.Labels[kubernetesHostnameLabel], item.Metadata.Name)
		// Model derivation: new labels first, fall back to legacy
		// nvidia.com/gpu.product / ani.kubercloud.io/gpu-model during
		// the transition period.
		model := firstNonEmpty(
			item.Metadata.Labels[kubernetesNVIDIAGPUProductLabel],
			item.Metadata.Labels[kubernetesANIGPUModelLabel],
			"nvidia-gpu",
		)
		ready, reason := kubernetesNodeReady(item.Status.Conditions)

		// Derive GPUMode / GPUSpec / GPUSharingSpec / GPUSharingPolicy
		// from node labels (plan.md §4.2). These are read-only fields
		// derived from labels, never written to PG.
		gpuMode := item.Metadata.Labels[kubernetesANIGPUModeLabel]
		gpuSpec := item.Metadata.Labels[kubernetesANIGPUSpecLabel]
		gpuSharingSpec := item.Metadata.Labels[kubernetesANIGPUSharingSpecLabel]
		gpuSharingPolicy := item.Metadata.Labels[kubernetesANIGPUSharingPolicyLabel]

		// Physical GPU memory in MiB from the NVIDIA device plugin node
		// label (nvidia.com/gpu.memory). Applied to every device on this
		// node so the handler can populate memory_total_mb.
		gpuMemoryMiB, _ := strconv.ParseInt(item.Metadata.Labels[kubernetesNVIDIAGPUMemoryLabel], 10, 64)

		// Device parsing: parse the volcano.sh/node-vgpu-register
		// annotation to discover vGPU devices managed by the Volcano
		// vGPU device plugin. When the annotation is absent, fall back
		// to nvidia.com/gpu (whole-card) and nvidia.com/vgpu resource
		// counts.
		vgpuDevices := parseVolcanoVGPUAnnotation(item.Metadata.Annotations)
		var devices []ports.GPUDeviceClass
		if vgpuDevices > 0 {
			devices = make([]ports.GPUDeviceClass, 0, vgpuDevices)
			for range vgpuDevices {
				devices = append(devices, ports.GPUDeviceClass{
					Vendor:             ports.GPUVendorNVIDIA,
					Model:              model,
					MemoryMiB:          gpuMemoryMiB,
					ResourceName:       kubernetesVolcanoVGPUNumberResource,
					VirtualizationMode: ports.GPUVirtualizationVGPU,
					DriverVersion:      firstNonEmpty(item.Metadata.Labels["nvidia.com/cuda.driver.major"], "volcano-vgpu"),
					RuntimeVersion:     item.Status.NodeInfo.KubeletVersion,
					Capabilities:       []string{"cuda", "compute", "vgpu"},
				})
			}
		} else {
			// No Volcano vGPU annotation: parse nvidia.com/gpu (whole
			// cards) and nvidia.com/vgpu (vGPU slices) from resources.
			wholeCount := gpuResourceCount(item.Status.Capacity, item.Status.Allocatable, kubernetesNVIDIAGPUResource)
			vgpuCount := gpuResourceCount(item.Status.Capacity, item.Status.Allocatable, kubernetesNVIDIAVGPUResource)
			devices = make([]ports.GPUDeviceClass, 0, wholeCount+vgpuCount)
			for range wholeCount {
				devices = append(devices, ports.GPUDeviceClass{
					Vendor:             ports.GPUVendorNVIDIA,
					Model:              model,
					MemoryMiB:          gpuMemoryMiB,
					ResourceName:       kubernetesNVIDIAGPUResource,
					VirtualizationMode: ports.GPUVirtualizationNone,
					DriverVersion:      firstNonEmpty(item.Metadata.Labels["nvidia.com/cuda.driver.major"], "device-plugin"),
					RuntimeVersion:     item.Status.NodeInfo.KubeletVersion,
					Capabilities:       []string{"cuda", "compute"},
				})
			}
			for range vgpuCount {
				devices = append(devices, ports.GPUDeviceClass{
					Vendor:             ports.GPUVendorNVIDIA,
					Model:              model,
					MemoryMiB:          gpuMemoryMiB,
					ResourceName:       kubernetesNVIDIAVGPUResource,
					VirtualizationMode: ports.GPUVirtualizationVGPU,
					DriverVersion:      firstNonEmpty(item.Metadata.Labels["nvidia.com/cuda.driver.major"], "volcano-vgpu"),
					RuntimeVersion:     item.Status.NodeInfo.KubeletVersion,
					Capabilities:       []string{"cuda", "compute", "vgpu"},
				})
			}
			volcanoNumber := gpuResourceCount(item.Status.Capacity, item.Status.Allocatable, kubernetesVolcanoVGPUNumberResource)
			volcanoMemory := gpuResourceCount(item.Status.Capacity, item.Status.Allocatable, kubernetesVolcanoVGPUMemoryResource)
			memoryPerSlice := int64(0)
			if volcanoNumber > 0 && volcanoMemory > 0 {
				memoryPerSlice = int64(volcanoMemory / volcanoNumber)
			}
			for range volcanoNumber {
				devices = append(devices, ports.GPUDeviceClass{
					Vendor:             ports.GPUVendorNVIDIA,
					Model:              model,
					MemoryMiB:          memoryPerSlice,
					ResourceName:       kubernetesVolcanoVGPUNumberResource,
					VirtualizationMode: ports.GPUVirtualizationVGPU,
					DriverVersion:      firstNonEmpty(item.Metadata.Labels["nvidia.com/cuda.driver.major"], "volcano-vgpu"),
					RuntimeVersion:     item.Status.NodeInfo.KubeletVersion,
					Capabilities:       []string{"cuda", "compute", "vgpu"},
				})
			}
		}
		nodes = append(nodes, ports.GPUNodeClass{
			NodeName:         nodeName,
			Vendor:           ports.GPUVendorNVIDIA,
			Model:            model,
			KernelVersion:    item.Status.NodeInfo.KernelVersion,
			OSImage:          item.Status.NodeInfo.OSImage,
			Pool:             firstNonEmpty(item.Metadata.Labels[kubernetesANIGPUPoolLabel], "default"),
			Labels:           cloneGPUStringMap(item.Metadata.Labels),
			Annotations:      cloneGPUStringMap(item.Metadata.Annotations),
			Devices:          devices,
			Allocatable:      cloneGPUStringMap(item.Status.Allocatable),
			Ready:            ready,
			Reason:           reason,
			GPUMode:          gpuMode,
			GPUSpec:          gpuSpec,
			GPUSharingSpec:   gpuSharingSpec,
			GPUSharingPolicy: gpuSharingPolicy,
		})
	}
	return nodes, nil
}

// volcanoVGPUDevice represents a single vGPU device entry in the
// volcano.sh/node-vgpu-register annotation.
type volcanoVGPUDevice struct {
	ID      string `json:"id"`
	Index   int    `json:"index"`
	Count   int    `json:"count"`
	DevMem  int64  `json:"devmem"`
	DevCore int    `json:"devcore"`
	Type    string `json:"type"`
	Health  bool   `json:"health"`
}

// parseVolcanoVGPUAnnotation parses the volcano.sh/node-vgpu-register
// annotation to count total vGPU slices. The annotation has two supported
// formats:
//
//  1. Comma-separated string (plan.md §4.6, current Volcano/HAMi-core format):
//     colon separates physical GPUs, comma separates fields within each GPU:
//     "GPU-ID,count,mb,type,health,mode:GPU-ID,count,mb,type,health,mode"
//     The second field (count) is the number of vGPU slices each physical
//     GPU is split into. Returns the sum of all count fields (total slices).
//
//  2. JSON array (legacy format):
//     [{"id":"GPU-xxx","count":10,...},{"id":"GPU-yyy",...}]
//     Returns the sum of all count fields.
//
// Returns 0 when the annotation is absent or invalid, signalling the caller
// to fall back to nvidia.com/gpu / nvidia.com/vgpu resource parsing.
func parseVolcanoVGPUAnnotation(annotations map[string]string) int {
	raw, ok := annotations[kubernetesVolcanoVGPURegisterAnnotation]
	if !ok || strings.TrimSpace(raw) == "" {
		return 0
	}
	raw = strings.TrimSpace(raw)
	// Try JSON array format first (legacy compatibility).
	if strings.HasPrefix(raw, "[") {
		var devices []volcanoVGPUDevice
		if err := json.Unmarshal([]byte(raw), &devices); err != nil {
			return 0
		}
		total := 0
		for _, d := range devices {
			if d.Count > 0 {
				total += d.Count
			} else {
				total++
			}
		}
		return total
	}
	// Comma-separated string format (plan.md §4.6):
	// GPU-ID,count,mb,type,health,mode:GPU-ID,count,mb,type,health,mode
	// The count field (2nd comma-separated field) is the number of vGPU
	// slices per physical GPU. Sum across all physical GPUs.
	// Guard: non-JSON strings that don't look like GPU records return 0.
	if !strings.HasPrefix(raw, "GPU-") {
		return 0
	}
	segments := strings.Split(raw, ":")
	total := 0
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		fields := strings.Split(seg, ",")
		if len(fields) >= 2 {
			if n, err := strconv.Atoi(strings.TrimSpace(fields[1])); err == nil && n > 0 {
				total += n
			} else {
				total++
			}
		} else {
			total++
		}
	}
	return total
}

// hasGPUResource reports whether the node advertises any NVIDIA GPU resource
// (whole card, vGPU, or Volcano vGPU slices).
func hasGPUResource(capacity, allocatable map[string]string) bool {
	return gpuResourceCount(capacity, allocatable, kubernetesNVIDIAGPUResource) > 0 ||
		gpuResourceCount(capacity, allocatable, kubernetesNVIDIAVGPUResource) > 0 ||
		gpuResourceCount(capacity, allocatable, kubernetesVolcanoVGPUNumberResource) > 0
}

// gpuResourceCount reads an extended resource from capacity falling back to
// allocatable, returning 0 when absent or invalid.
func gpuResourceCount(capacity, allocatable map[string]string, resourceName string) int {
	value := strings.TrimSpace(capacity[resourceName])
	if value == "" {
		value = strings.TrimSpace(allocatable[resourceName])
	}
	count, err := strconv.Atoi(value)
	if err != nil || count < 0 {
		return 0
	}
	return count
}

func kubernetesNodeReady(conditions []kubernetesNodeCondition) (bool, string) {
	for _, condition := range conditions {
		if condition.Type != "Ready" {
			continue
		}
		if condition.Status == "True" {
			return true, firstNonEmpty(condition.Reason, "KubeletReady")
		}
		return false, firstNonEmpty(condition.Reason, condition.Message, "Kubernetes node is not ready")
	}
	return false, "Kubernetes node Ready condition not found"
}

func gpuNodeSupportsSchedulingRequest(node ports.GPUNodeClass, request ports.GPUSchedulingRequest) bool {
	// When PreferredModels is set, prefer matching nodes but do not reject
	// others — falling through to any available GPU node is safer than
	// failing to schedule.
	if len(request.PreferredModels) > 0 {
		for _, model := range request.PreferredModels {
			if strings.EqualFold(node.Model, strings.TrimSpace(model)) {
				return true
			}
		}
	}
	// No exact model match — still allow this node if it has GPUs available.
	if request.RequiredMemoryMiB > 0 {
		for _, device := range node.Devices {
			if device.MemoryMiB >= request.RequiredMemoryMiB {
				return true
			}
		}
		return false
	}
	return true
}

var _ ports.GPUInventory = (*KubernetesGPUInventory)(nil)

// ListSpecAvailability computes per-spec availability for a tenant following
// SPEC §5.1: it retrieves tenant quota remaining, lists all GPU specs from
// the spec store, iterates cluster nodes to find matching nodes per spec,
// parses the volcano.sh/node-vgpu-register annotation for device idle count,
// and determines a four-state status (available / full / device_full /
// unavailable).
func (i *KubernetesGPUInventory) ListSpecAvailability(ctx context.Context, tenantID string) ([]ports.GPUSpecAvailability, error) {
	if i.specStore == nil || i.quotaStore == nil {
		return nil, ports.ErrUnsupported
	}

	// Step 1: get tenant quota (total / used / reserved).
	quota, err := i.quotaStore.GetMy(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// Step 2: quota_remaining = allocated_gpu_count - used - reserved
	// (plan.md §4.4.1). When quotaAdmin is available, use the tenant's
	// allocated_gpu_count (BOSS reservation). Fall back to total when
	// quotaAdmin is not configured or the reservation row doesn't exist.
	total := quota.Total[ports.QuotaGPUCount]
	used := quota.Used[ports.QuotaGPUCount]
	reserved := quota.Reserved[ports.QuotaGPUCount]
	allocatedLimit := total
	if i.quotaAdmin != nil {
		reservation, err := i.quotaAdmin.GetReservation(ctx, tenantID)
		if err == nil && reservation.AllocatedGPUCount > 0 {
			allocatedLimit = reservation.AllocatedGPUCount
		}
	}
	quotaRemaining := allocatedLimit - used - reserved

	// Step 3: list all specs.
	specs, err := i.specStore.List(ctx)
	if err != nil {
		return nil, err
	}

	// Step 4: list all node classes.
	nodes, err := i.ListNodeClasses(ctx, ports.GPUDiscoveryFilter{})
	if err != nil {
		return nil, err
	}

	// Step 4b: query running pods across all namespaces to compute
	// per-node GPU resource usage (plan.md §五 #14: 空闲 = 总切分数 - 已占用).
	podUsageByNode, err := i.fetchPodGPUUsage(ctx)
	if err != nil {
		// Non-fatal: fall back to treating deviceIdleCount as total
		// (pre-existing behavior) if pod query fails.
		podUsageByNode = nil
	}

	// Step 5: for each spec, determine availability.
	result := make([]ports.GPUSpecAvailability, 0, len(specs))
	for _, spec := range specs {
		availability := computeSpecAvailability(spec, nodes, quotaRemaining, podUsageByNode)
		result = append(result, availability)
	}
	return result, nil
}

// computeSpecAvailability determines the four-state availability for a single
// spec by matching nodes and computing device idle count from the Volcano
// vGPU annotation (SPEC §5.1). Per plan.md §五 #14, deviceIdleCount is
// total device count minus running pod GPU resource requests on matching nodes.
func computeSpecAvailability(spec ports.GPUSpecCRD, nodes []ports.GPUNodeClass, quotaRemaining int64, podUsageByNode map[string]int) ports.GPUSpecAvailability {
	// Step 5a: check if any node matches this spec.
	// Wholecard: node.GPUMode == "wholecard" AND node.GPUSpec == spec.GPUType
	// vGPU: node.GPUMode == "vgpu" AND node.GPUSharingSpec == spec.GPUType
	hasMatchingNodes := false
	deviceTotalCount := 0
	for _, node := range nodes {
		if !specMatchesNode(spec, node) {
			continue
		}
		hasMatchingNodes = true
		// Step 5b-i: compute device total count based on spec mode.
		// Wholecard: read nvidia.com/gpu allocatable count from node
		// resources. vGPU: parse volcano.sh/node-vgpu-register
		// annotation for vGPU slice count (plan.md §五 #14).
		if spec.GPUMode == "wholecard" {
			deviceTotalCount += gpuAllocatableCount(node, kubernetesNVIDIAGPUResource)
		} else {
			deviceTotalCount += parseVolcanoVGPUAnnotation(node.Annotations)
		}
	}

	// Step 5b-ii: deviceIdleCount = total - podUsage (plan.md §五 #14).
	// podUsageByNode maps nodeName → total GPU units consumed by running
	// pods on that node. For wholecard, 1 pod = 1 nvidia.com/gpu unit.
	// For vGPU, 1 pod = N volcano.sh/vgpu-number units.
	// Since podUsage is node-level (not per-spec), we subtract the
	// matching node's usage from the deviceTotalCount for this spec.
	// When podUsageByNode is nil (query failed), idle = total (fallback).
	deviceIdleCount := deviceTotalCount
	if podUsageByNode != nil {
		podUsage := 0
		for _, node := range nodes {
			if !specMatchesNode(spec, node) {
				continue
			}
			podUsage += podUsageByNode[node.NodeName]
		}
		deviceIdleCount = deviceTotalCount - podUsage
		if deviceIdleCount < 0 {
			deviceIdleCount = 0
		}
	}

	availability := ports.GPUSpecAvailability{
		SpecID:           spec.ID,
		HasMatchingNodes: hasMatchingNodes,
		DeviceIdleCount:  deviceIdleCount,
		HasIdleDevices:   deviceIdleCount > 0,
		// GPUCount is the number of GPU cards consumed per instance
		// request. Per plan.md §四.4.2 and 实现方案 §二.2, 1 vGPU
		// slice = 1 card = 1 gpu_count. A single instance creation
		// requests count=1, so GPUCount is always 1 regardless of
		// whether the spec is wholecard (shares=1) or vGPU (shares>1).
		GPUCount: 1,
	}

	// Step 5b: four-state determination.
	if !hasMatchingNodes {
		// Step 5b (no matching nodes): status=unavailable.
		availability.Status = ports.GPUSpecStatusUnavailable
		availability.AvailableCount = 0
		return availability
	}

	if quotaRemaining <= 0 {
		// Quota exhausted: status=full.
		availability.Status = ports.GPUSpecStatusFull
		availability.AvailableCount = 0
		return availability
	}

	if deviceIdleCount <= 0 {
		// No idle devices on matching nodes: status=device_full.
		availability.Status = ports.GPUSpecStatusDeviceFull
		availability.AvailableCount = 0
		return availability
	}

	// Available: available_count = min(quota_remaining, device_idle_count).
	availability.Status = ports.GPUSpecStatusAvailable
	availability.AvailableCount = minInt64(quotaRemaining, int64(deviceIdleCount))
	return availability
}

// specMatchesNode checks whether a GPU spec matches a node based on the
// spec's GPUMode and node labels. Wholecard specs match nodes where
// GPUMode == "wholecard" and GPUSpec == spec.GPUType; vGPU specs match nodes
// where GPUMode == "vgpu" and GPUSharingSpec == spec.GPUType.
func specMatchesNode(spec ports.GPUSpecCRD, node ports.GPUNodeClass) bool {
	if spec.GPUMode == "wholecard" {
		return node.GPUMode == "wholecard" && node.GPUSpec == spec.GPUType
	}
	return node.GPUMode == "vgpu" && node.GPUSharingSpec == spec.GPUType
}

// fetchPodGPUUsage queries all Running pods across the cluster and returns
// a map of nodeName → total GPU resource units consumed by running pods
// on that node. For wholecard pods, the resource is nvidia.com/gpu; for
// vGPU pods, the resource is volcano.sh/vgpu-number. Each pod's resource
// request is summed per node (plan.md §五 #14: 空闲 = 总切分数 - 已占用).
//
// Only Running pods with a non-empty nodeName and non-zero GPU resource
// requests are counted. Pending/Failed/Succeeded pods are excluded.
func (i *KubernetesGPUInventory) fetchPodGPUUsage(ctx context.Context) (map[string]int, error) {
	if i.client == nil {
		return nil, nil
	}
	// Query all pods in all namespaces with Running phase.
	// Using fieldSelector=spec.nodeName!= for broad query, then filter
	// by phase in code since K8s field selectors for phase vary by version.
	endpoint := i.client.Host() + "/api/v1/pods?fieldSelector=status.phase%3DRunning"
	body, status, err := i.client.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil || status != 200 || len(body) == 0 {
		return nil, fmt.Errorf("fetch pods: status=%d err=%v", status, err)
	}
	var podList struct {
		Items []struct {
			Spec struct {
				NodeName   string `json:"nodeName"`
				Containers []struct {
					Resources struct {
						Requests map[string]string `json:"requests"`
					} `json:"resources"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(body, &podList) != nil {
		return nil, fmt.Errorf("unmarshal pod list")
	}
	usageByNode := make(map[string]int)
	for _, pod := range podList.Items {
		if !strings.EqualFold(pod.Status.Phase, "Running") {
			continue
		}
		nodeName := strings.TrimSpace(pod.Spec.NodeName)
		if nodeName == "" {
			continue
		}
		// Sum GPU resource requests across all containers.
		for _, c := range pod.Spec.Containers {
			for resName, resVal := range c.Resources.Requests {
				if resName == kubernetesNVIDIAGPUResource || resName == kubernetesVolcanoVGPUNumberResource {
					n, parseErr := strconv.Atoi(strings.TrimSpace(resVal))
					if parseErr == nil && n > 0 {
						usageByNode[nodeName] += n
					}
				}
			}
		}
	}
	return usageByNode, nil
}

// minInt64 returns the smaller of two int64 values.
func minInt64(a, b int64) int {
	if a < b {
		return int(a)
	}
	return int(b)
}
