package ports

import "context"

// GPU sharing policy names for cluster-level equal split (GPU-PARTITION-A).
// They map 1:1 to the ani.kubercloud.io/gpu-sharing-policy node label.
const (
	GPUSharingPolicyHalf    = "half"
	GPUSharingPolicyQuarter = "quarter"
	GPUSharingPolicyEighth  = "eighth"
)

// GPU partition skip reasons (GPU-PARTITION-A). They are stable strings so
// the BOSS frontend can render them; do not reword without a contract note.
const (
	// GPUPartitionSkipBusy marks nodes running at least one pod that holds
	// GPU resources (nvidia.com/gpu > 0 or volcano.sh/vgpu-number > 0).
	GPUPartitionSkipBusy = "node_busy"
	// GPUPartitionSkipNotReady marks nodes whose Ready condition is false.
	GPUPartitionSkipNotReady = "not_ready"
	// GPUPartitionSkipVGPU marks nodes already in vgpu mode; the first
	// partition iteration only converts wholecard nodes.
	GPUPartitionSkipVGPU = "vgpu_mode"
	// GPUPartitionSkipNoMemory marks GPU nodes without a per-card memory
	// label (nvidia.com/gpu.memory), so mb_per_share cannot be derived.
	GPUPartitionSkipNoMemory = "missing_gpu_memory_label"
)

// GPUPartitionBusyPod identifies a pod that keeps a node busy.
type GPUPartitionBusyPod struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// GPUPartitionCandidateNode is a wholecard node eligible for the split.
type GPUPartitionCandidateNode struct {
	NodeName string `json:"node_name"`
	// Model is the spec-label model prefix, e.g. "NVIDIA-RTX-4090".
	Model string `json:"model"`
	// CardCount is the node's nvidia.com/gpu allocatable count.
	CardCount int `json:"card_count"`
	// TotalMemoryMiB is the per-card memory from nvidia.com/gpu.memory.
	TotalMemoryMiB int64  `json:"total_memory_mib"`
	MBPerShare     int64  `json:"mb_per_share"`
	SharingSpec    string `json:"sharing_spec"`
	// SharingPolicy is derived from Shares: 2=half, 4=quarter, 8=eighth.
	SharingPolicy string `json:"sharing_policy"`
}

// GPUPartitionSkippedNode records why a node was not included in the split.
type GPUPartitionSkippedNode struct {
	NodeName string                `json:"node_name"`
	Reason   string                `json:"reason"`
	Pods     []GPUPartitionBusyPod `json:"pods,omitempty"`
}

// GPUPartitionPlan is the read-only planning snapshot returned by
// PlanGPUPartition. The BOSS frontend renders it as the confirmation dialog;
// ApplyGPUPartition consumes it as the execution input.
type GPUPartitionPlan struct {
	Shares        int                         `json:"shares"`
	EligibleNodes []GPUPartitionCandidateNode `json:"eligible_nodes"`
	SkippedNodes  []GPUPartitionSkippedNode   `json:"skipped_nodes"`
}

// GPUPartitionNodeResult records the per-node execution outcome.
type GPUPartitionNodeResult struct {
	NodeName string `json:"node_name"`
	OK       bool   `json:"ok"`
	Reason   string `json:"reason,omitempty"`
}

// GPUPartitionApplyResult is the final task result persisted on the async
// task record: applied nodes, re-checked skipped nodes and per-node failures.
type GPUPartitionApplyResult struct {
	AppliedNodes []string                  `json:"applied_nodes"`
	SkippedNodes []GPUPartitionSkippedNode `json:"skipped_nodes"`
	FailedNodes  []GPUPartitionNodeResult  `json:"failed_nodes"`
}

// GPUPartitionProgressFunc receives per-node execution progress for the
// async task record (step counts nodes, not sub-steps).
type GPUPartitionProgressFunc func(step int, total int, message string)

// GPUPartitionPlanner expresses the cluster GPU partition capability
// (GPU-PARTITION-A): plan a cluster-wide equal split across idle wholecard
// nodes, then apply it by rewriting the Volcano vGPU device plugin node
// config, relabeling nodes and waiting for device re-registration.
// Kubernetes API stays behind this port: callers must not import K8s SDK.
type GPUPartitionPlanner interface {
	// PlanGPUPartition computes the confirmation-dialog snapshot for the
	// requested equal split (2, 4 or 8). Shares outside {2,4,8} return
	// ErrInvalid.
	PlanGPUPartition(ctx context.Context, shares int) (GPUPartitionPlan, error)
	ApplyGPUPartition(ctx context.Context, plan GPUPartitionPlan, progress GPUPartitionProgressFunc) (GPUPartitionApplyResult, error)
}
