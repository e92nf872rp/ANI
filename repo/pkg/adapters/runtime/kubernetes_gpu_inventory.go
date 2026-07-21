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
	kubernetesNVIDIAGPUResource       = "nvidia.com/gpu"
	kubernetesNVIDIAVGPUResource      = "nvidia.com/vgpu"
	kubernetesNVIDIAGPUProductLabel   = "nvidia.com/gpu.product"
	kubernetesANIGPUModelLabel        = "ani.kubercloud.io/gpu-model"
	kubernetesHostnameLabel           = "kubernetes.io/hostname"
	kubernetesANIGPUPoolLabel         = "ani.kubercloud.io/gpu-pool"
	kubernetesVolcanoSchedulerName    = "volcano"
	kubernetesHAMISchedulerName       = "hami-scheduler"
	kubernetesDefaultInferenceQueue   = "ani-inference"
	kubernetesDefaultTrainingQueue    = "ani-training"
	kubernetesGPUNodeSelectorLabel    = "ani.kubercloud.io/gpu-node"
	kubernetesHAMIRegisterAnnotation  = "hami.io/node-nvidia-register"
	kubernetesHAMIHandshakeAnnotation = "hami.io/node-handshake"
	kubernetesHAMILocalModel          = "hami-core"
	kubernetesHAMILocalRuntimeClass   = "hami-vgpu"
)

// KubernetesGPUInventory discovers GPU capacity from Kubernetes nodes and
// maps workload intent to scheduling constraints. When a queue store is
// injected it resolves and validates Volcano queues; without one it still
// serves inventory lookups but PlanScheduling falls back to workload-class
// defaults without tenant validation.
type KubernetesGPUInventory struct {
	client     *KubernetesRESTClient
	queueStore ports.GPUSchedulingQueueStore
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
		// Resource name depends on whether HAMi manages this node.
		// HAMi reports vGPU splits as nvidia.com/gpu; non-HAMi clusters
		// use nvidia.com/vgpu for vGPU scheduling.
		resourceName := resourceNameForNode(node, mode)
		available := gpuAllocatableCount(node, resourceName)
		if available < requiredCount {
			continue
		}
		// HAMi-managed nodes require hami-scheduler; non-HAMi nodes use volcano.
		schedulerName := kubernetesVolcanoSchedulerName
		if isHAMINode(node) {
			schedulerName = kubernetesHAMISchedulerName
		}
		return ports.GPUSchedulingDecision{
			NodeSelector: map[string]string{
				kubernetesHostnameLabel:        node.NodeName,
				kubernetesGPUNodeSelectorLabel: "true",
			},
			ResourceName:      resourceName,
			ResourceQuantity:  strconv.Itoa(requiredCount),
			RuntimeClassName:  runtimeClassNameForNode(node, mode),
			SchedulerName:     schedulerName,
			QueueName:         queueName,
			Reasons:           []string{fmt.Sprintf("Kubernetes node %s provides %d %s", node.NodeName, available, resourceName)},
			SelectedNodeModel: node.Model,
		}, nil
	}

	// Fallback resource name for the "no node found" message.
	fallbackResource := kubernetesNVIDIAGPUResource
	if mode == ports.GPUVirtualizationVGPU {
		fallbackResource = kubernetesNVIDIAGPUResource
	}
	return ports.GPUSchedulingDecision{
		SchedulerName: kubernetesVolcanoSchedulerName,
		QueueName:     queueName,
		ResourceName:  fallbackResource,
		Reasons:       []string{fmt.Sprintf("no ready GPU node satisfies %s >= %d", fallbackResource, requiredCount)},
	}, nil
}

// resourceNameForNode returns the K8s extended resource name to request for
// scheduling on this node. HAMi-managed nodes report vGPU splits as
// nvidia.com/gpu (the device's ResourceName field reflects this); non-HAMi
// nodes use nvidia.com/vgpu for vGPU scheduling.
func resourceNameForNode(node ports.GPUNodeClass, mode ports.GPUVirtualizationMode) string {
	if mode != ports.GPUVirtualizationVGPU {
		return kubernetesNVIDIAGPUResource
	}
	// Check if any device on this node is a HAMi vGPU (ResourceName ==
	// nvidia.com/gpu with VirtualizationMode == vgpu). If so, HAMi
	// manages this node and nvidia.com/gpu is the correct resource.
	for _, device := range node.Devices {
		if device.VirtualizationMode == ports.GPUVirtualizationVGPU &&
			device.ResourceName == kubernetesNVIDIAGPUResource {
			return kubernetesNVIDIAGPUResource
		}
	}
	// Non-HAMi node with vGPU request: use nvidia.com/vgpu.
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
// virtualization mode for the request. HAMi uses nvidia.com/gpu for both
// whole-card and vGPU scheduling; the difference is conveyed via
// nvidia.com/gpumem / nvidia.com/gpucores limits and the runtime class.
// Non-HAMi clusters distinguish via nvidia.com/vgpu.
func selectResourceName(request ports.GPUSchedulingRequest) (string, ports.GPUVirtualizationMode) {
	for _, mode := range request.VirtualizationModes {
		if mode == ports.GPUVirtualizationVGPU {
			// HAMi reports vGPU splits as nvidia.com/gpu, not nvidia.com/vgpu.
			return kubernetesNVIDIAGPUResource, ports.GPUVirtualizationVGPU
		}
	}
	return kubernetesNVIDIAGPUResource, ports.GPUVirtualizationNone
}

// runtimeClassNameForNode returns the runtime class for a node given its
// management type (HAMi vs Volcano/native) and the requested virtualization
// mode. HAMi-managed nodes use the hami-vgpu runtime class for vGPU splits;
// whole-card scheduling on HAMi nodes leaves the runtime class empty so the
// HAMi device plugin takes over container creation. Non-HAMi (Volcano/native)
// nodes leave the runtime class empty for both whole-card and vGPU, letting
// the native NVIDIA device plugin or a cluster-defined RuntimeClass handle it.
func runtimeClassNameForNode(node ports.GPUNodeClass, mode ports.GPUVirtualizationMode) string {
	if isHAMINode(node) {
		if mode == ports.GPUVirtualizationVGPU {
			return kubernetesHAMILocalRuntimeClass
		}
		return ""
	}
	// Non-HAMi node: let the cluster's native device plugin handle GPU
	// allocation. Returning an empty runtime class avoids requiring a
	// "nvidia" RuntimeClass that may not exist in the cluster.
	return ""
}

// runtimeClassNameForMode returns the runtime class for a virtualization mode
// when the node management type is unknown. Prefer runtimeClassNameForNode.
func runtimeClassNameForMode(mode ports.GPUVirtualizationMode) string {
	if mode == ports.GPUVirtualizationVGPU {
		return kubernetesHAMILocalRuntimeClass
	}
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

// isHAMINode returns true when any device on the node is managed by HAMi
// (detected via the hami.io/node-nvidia-register annotation or devices with
// VirtualizationMode == vgpu using nvidia.com/gpu).
func isHAMINode(node ports.GPUNodeClass) bool {
	for _, device := range node.Devices {
		if device.VirtualizationMode == ports.GPUVirtualizationVGPU &&
			device.ResourceName == kubernetesNVIDIAGPUResource {
			return true
		}
	}
	return false
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
		model := firstNonEmpty(
			item.Metadata.Labels[kubernetesNVIDIAGPUProductLabel],
			item.Metadata.Labels[kubernetesANIGPUModelLabel],
			"nvidia-gpu",
		)
		ready, reason := kubernetesNodeReady(item.Status.Conditions)

		// HAMi-aware device parsing: when hami.io/node-nvidia-register
		// annotation is present, HAMi has replaced the native NVIDIA device
		// plugin and reports nvidia.com/gpu as vGPU split count (physical
		// cards × deviceSplitCount). We parse the annotation to recover
		// physical card identity, model, VRAM and health, and mark each
		// device with vGPU virtualization mode.
		hamiDevices := parseHAMIAnnotation(item.Metadata.Annotations)
		var devices []ports.GPUDeviceClass
		if len(hamiDevices) > 0 {
			devices = make([]ports.GPUDeviceClass, 0, len(hamiDevices))
			for _, hd := range hamiDevices {
				devices = append(devices, ports.GPUDeviceClass{
					Vendor:             ports.GPUVendorNVIDIA,
					Model:              firstNonEmpty(hd.Type, model),
					MemoryMiB:          hd.DevMem,
					ResourceName:       kubernetesNVIDIAGPUResource,
					VirtualizationMode: ports.GPUVirtualizationVGPU,
					DriverVersion:      firstNonEmpty(item.Metadata.Labels["nvidia.com/cuda.driver.major"], "hami"),
					RuntimeVersion:     item.Status.NodeInfo.KubeletVersion,
					Capabilities:       []string{"cuda", "compute", "vgpu"},
				})
			}
		} else {
			// Non-HAMi node: nvidia.com/gpu = physical whole cards.
			wholeCount := gpuResourceCount(item.Status.Capacity, item.Status.Allocatable, kubernetesNVIDIAGPUResource)
			vgpuCount := gpuResourceCount(item.Status.Capacity, item.Status.Allocatable, kubernetesNVIDIAVGPUResource)
			devices = make([]ports.GPUDeviceClass, 0, wholeCount+vgpuCount)
			for range wholeCount {
				devices = append(devices, ports.GPUDeviceClass{
					Vendor:             ports.GPUVendorNVIDIA,
					Model:              model,
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
					ResourceName:       kubernetesNVIDIAVGPUResource,
					VirtualizationMode: ports.GPUVirtualizationVGPU,
					DriverVersion:      firstNonEmpty(item.Metadata.Labels["nvidia.com/cuda.driver.major"], "hami"),
					RuntimeVersion:     item.Status.NodeInfo.KubeletVersion,
					Capabilities:       []string{"cuda", "compute", "vgpu"},
				})
			}
		}
		nodes = append(nodes, ports.GPUNodeClass{
			NodeName:      nodeName,
			Vendor:        ports.GPUVendorNVIDIA,
			Model:         model,
			KernelVersion: item.Status.NodeInfo.KernelVersion,
			OSImage:       item.Status.NodeInfo.OSImage,
			Pool:          firstNonEmpty(item.Metadata.Labels[kubernetesANIGPUPoolLabel], "default"),
			Labels:        cloneGPUStringMap(item.Metadata.Labels),
			Devices:       devices,
			Allocatable:   cloneGPUStringMap(item.Status.Allocatable),
			Ready:         ready,
			Reason:        reason,
		})
	}
	return nodes, nil
}

// hamiPhysicalDevice represents a single physical GPU as reported by the
// HAMi device plugin via the hami.io/node-nvidia-register node annotation.
type hamiPhysicalDevice struct {
	ID      string `json:"id"`
	Index   int    `json:"index"`
	Count   int    `json:"count"`
	DevMem  int64  `json:"devmem"`
	DevCore int    `json:"devcore"`
	Type    string `json:"type"`
	Mode    string `json:"mode"`
	Health  bool   `json:"health"`
}

// parseHAMIAnnotation parses the hami.io/node-nvidia-register annotation
// to recover physical GPU identity. Returns nil when the annotation is
// absent or invalid, signalling the caller to fall back to the legacy
// nvidia.com/gpu whole-card or nvidia.com/vgpu parsing path.
func parseHAMIAnnotation(annotations map[string]string) []hamiPhysicalDevice {
	raw, ok := annotations[kubernetesHAMIRegisterAnnotation]
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	var devices []hamiPhysicalDevice
	if err := json.Unmarshal([]byte(raw), &devices); err != nil {
		return nil
	}
	return devices
}

// hasGPUResource reports whether the node advertises any NVIDIA GPU resource
// (whole-card or vGPU).
func hasGPUResource(capacity, allocatable map[string]string) bool {
	return gpuResourceCount(capacity, allocatable, kubernetesNVIDIAGPUResource) > 0 ||
		gpuResourceCount(capacity, allocatable, kubernetesNVIDIAVGPUResource) > 0
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
	// others — HAMi nodes report device models from annotations which may
	// not match the user-specified model name exactly. Falling through to
	// any available GPU node is safer than failing to schedule.
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
