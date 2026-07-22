package ports

import "context"

type GPUVendor string

const (
	GPUVendorNVIDIA  GPUVendor = "nvidia"
	GPUVendorHuawei  GPUVendor = "huawei"
	GPUVendorHygon   GPUVendor = "hygon"
	GPUVendorUnknown GPUVendor = "unknown"
)

type GPUVirtualizationMode string

const (
	GPUVirtualizationNone GPUVirtualizationMode = "none"
	GPUVirtualizationMIG  GPUVirtualizationMode = "mig"
	GPUVirtualizationVGPU GPUVirtualizationMode = "vgpu"
)

type GPUDeviceClass struct {
	Vendor             GPUVendor
	Model              string
	MemoryMiB          int64
	ResourceName       string
	VirtualizationMode GPUVirtualizationMode
	DriverVersion      string
	RuntimeVersion     string
	Capabilities       []string
}

type GPUNodeClass struct {
	NodeName      string
	Vendor        GPUVendor
	Model         string
	KernelVersion string
	OSImage       string
	Pool          string
	Labels        map[string]string
	Taints        []string
	Devices       []GPUDeviceClass
	// Allocatable preserves the raw Kubernetes node allocatable map so
	// PlanScheduling can check vendor-specific resource names such as
	// nvidia.com/gpu (whole-card) and nvidia.com/vgpu (HAMi slice).
	Allocatable map[string]string
	Ready       bool
	Reason      string
}

type GPUDiscoveryFilter struct {
	Vendors []GPUVendor
	Pool    string
	Labels  map[string]string
}

type GPUSchedulingRequest struct {
	TenantID             string
	WorkloadID           string
	PreferredVendors     []GPUVendor
	PreferredModels      []string
	RequiredMemoryMiB    int64
	RequiredCount        int
	VirtualizationModes  []GPUVirtualizationMode
	RequiredCapabilities []string
	Pool                 string
	// QueueName is an explicit Volcano queue selection. When empty, the
	// adapter resolves a default queue by WorkloadClass. When non-empty the
	// adapter MUST verify the queue exists and belongs to the tenant.
	QueueName string
	// WorkloadClass drives the default queue selection when QueueName is
	// empty: inference→ani-inference, training/batch→ani-training.
	WorkloadClass WorkloadClass
}

type GPUSchedulingDecision struct {
	NodeSelector     map[string]string
	Tolerations      []string
	ResourceName     string
	ResourceQuantity string
	RuntimeClassName string
	SchedulerName    string
	QueueName        string
	Reasons          []string
	// SelectedNodeModel is the GPU model of the node chosen by PlanScheduling,
	// sourced from the node's nvidia.com/gpu.product label (K8s adapter) or
	// the inventory's hardcoded model (local adapter). It reflects the real
	// GPU hardware the workload is scheduled onto, as opposed to the
	// PreferredModels in the request which is only a scheduling preference.
	SelectedNodeModel string
}

// GPUInventory discovers heterogeneous GPU capacity and maps workload intent to
// scheduling constraints. Implementations may use Kubernetes labels, GPU Feature
// Discovery, vendor device plugins, or customer inventory systems.
type GPUInventory interface {
	ListNodeClasses(ctx context.Context, filter GPUDiscoveryFilter) ([]GPUNodeClass, error)
	GetNodeClass(ctx context.Context, nodeName string) (GPUNodeClass, error)
	PlanScheduling(ctx context.Context, request GPUSchedulingRequest) (GPUSchedulingDecision, error)
}
