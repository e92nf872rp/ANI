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
	Annotations   map[string]string
	Taints        []string
	Devices       []GPUDeviceClass
	// Allocatable preserves the raw Kubernetes node allocatable map so
	// PlanScheduling can check vendor-specific resource names such as
	// nvidia.com/gpu (whole-card) and nvidia.com/vgpu (vGPU slice).
	Allocatable map[string]string
	Ready       bool
	Reason      string
	// GPUMode is derived from the ani.kubercloud.io/gpu-mode node label
	// (wholecard | vgpu). Read-only; never written to PG.
	GPUMode string
	// GPUSpec is derived from the ani.kubercloud.io/gpu-spec node label
	// (wholecard mode). Read-only; never written to PG.
	GPUSpec string
	// GPUSharingSpec is derived from the ani.kubercloud.io/gpu-sharing-spec
	// node label (vgpu mode). Read-only; never written to PG.
	GPUSharingSpec string
	// GPUSharingPolicy is derived from the ani.kubercloud.io/gpu-sharing-policy
	// node label (vgpu mode). Read-only; never written to PG.
	GPUSharingPolicy string
}

type GPUDiscoveryFilter struct {
	Vendors []GPUVendor
	Pool    string
	Labels  map[string]string
}

type GPUSpec struct {
	ID            string
	Name          string
	GPUType       string
	MemoryTotalMB int64
	Shares        int
	MBPerShare    int
	Available     bool
}

type GPUSpecListRequest struct {
	GPUType   string
	Available *bool
	Limit     int
	Cursor    string
}

type GPUSpecService interface {
	ListGPUSpecs(ctx context.Context, request GPUSpecListRequest) ([]GPUSpec, error)
	GetGPUSpec(ctx context.Context, specID string) (GPUSpec, error)
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

// GPUSpecAvailabilityStatus enumerates the four availability states for a GPU
// spec (SPEC §5.1, plan.md §4.4): available = can create instances, full = tenant quota
// insufficient, device_full = no idle devices on matching nodes, unavailable =
// spec has no matching nodes in the cluster.
type GPUSpecAvailabilityStatus string

const (
	GPUSpecStatusAvailable   GPUSpecAvailabilityStatus = "available"
	GPUSpecStatusFull        GPUSpecAvailabilityStatus = "full"
	GPUSpecStatusDeviceFull  GPUSpecAvailabilityStatus = "device_full"
	GPUSpecStatusUnavailable GPUSpecAvailabilityStatus = "unavailable"
)

// GPUSpecAvailability is a per-spec availability view computed from tenant
// quota remaining and node label matching (SPEC §5.1).
// QuotaRemaining is a tenant-level shared value; the handler lifts it to the
// GPUSpecAvailabilityListResponse top level (v1.yaml) rather than per-item.
type GPUSpecAvailability struct {
	SpecID           string
	Status           GPUSpecAvailabilityStatus
	AvailableCount   int
	HasMatchingNodes bool
	HasIdleDevices   bool
	DeviceIdleCount  int
	GPUCount         int
}

// GPUInventory discovers heterogeneous GPU capacity and maps workload intent to
// scheduling constraints. Implementations may use Kubernetes labels, GPU Feature
// Discovery, vendor device plugins, or customer inventory systems.
type GPUInventory interface {
	ListNodeClasses(ctx context.Context, filter GPUDiscoveryFilter) ([]GPUNodeClass, error)
	GetNodeClass(ctx context.Context, nodeName string) (GPUNodeClass, error)
	PlanScheduling(ctx context.Context, request GPUSchedulingRequest) (GPUSchedulingDecision, error)
	// ListSpecAvailability computes per-spec availability for a tenant by
	// combining node label matching and idle device counts (SPEC §5.1,
	// plan.md §4.4).
	//
	// The returned []GPUSpecAvailability does NOT include QuotaRemaining;
	// the handler is responsible for querying QuotaService separately and
	// lifting it to the GPUSpecAvailabilityListResponse top-level field
	// (v1.yaml), since quota_remaining is a tenant-level shared value.
	ListSpecAvailability(ctx context.Context, tenantID string) ([]GPUSpecAvailability, error)
}
