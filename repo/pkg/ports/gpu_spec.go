package ports

import (
	"context"
	"errors"
)

// GPUSpecStore persists GPU spec definitions backed by a Kubernetes CRD. It
// follows the CRD operation mode established by volcano_queue_store.go: specs
// are cluster-scoped custom resources aligned to node labels, and the adapter
// translates Volcano resource declarations during instance creation.
type GPUSpecStore interface {
	List(ctx context.Context) ([]GPUSpecCRD, error)
	Get(ctx context.Context, specID string) (GPUSpecCRD, error)
	Create(ctx context.Context, idempotencyKey string, spec GPUSpecCRD) (GPUSpecCRD, error)
	Delete(ctx context.Context, idempotencyKey string, specID string) error
}

// GPUSpecCRD is the Go representation of a GPUSpec custom resource (SPEC §3.2).
// It aligns with node labels (ani.kubercloud.io/gpu-spec, gpu-mode,
// gpu-sharing-spec) and carries Volcano scheduling resource declarations that
// the adapter translates into Pod spec during instance creation.
type GPUSpecCRD struct {
	ID               string
	Name             string // Console display name; derived from spec_id if empty
	GPUType          string
	GPUMode          string // wholecard | vgpu
	MemoryTotalMB    int64
	Shares           int
	MBPerShare       int
	Available        bool   // whether the spec is enabled for new instance creation
	ComputePerShare  *int64 // compute reservation; nil for this batch
	NodeAffinity     GPUSpecNodeAffinity
	VolcanoResources GPUSpecVolcanoResources
}

// GPUSpecNodeAffinity maps spec node selection to Kubernetes node labels
// (plan.md §4.2 节点标签体系).
type GPUSpecNodeAffinity struct {
	GPUSpec          string // wholecard: ani.kubercloud.io/gpu-spec value
	GPUSharingSpec   string // vgpu: ani.kubercloud.io/gpu-sharing-spec value
	GPUSharingPolicy string // vgpu: ani.kubercloud.io/gpu-sharing-policy value
	GPUMode          string // ani.kubercloud.io/gpu-mode value (wholecard | vgpu)
}

// GPUSpecVolcanoResources embeds wholecard / vgpu resource declarations
// (plan.md §4.1/§4.3). The adapter formats these with {count} / {mb_per_share}
// placeholders when building Pod resource requests.
type GPUSpecVolcanoResources struct {
	Wholecard map[string]string // nvidia.com/gpu / huawei.com/Ascend910
	VGPU      map[string]string // volcano.sh/vgpu-memory / volcano.sh/vgpu-number
}

// GPUSpec sentinel errors (SPEC §6.1 error taxonomy).
var (
	ErrGPUSpecNotFound = errors.New("gpu spec not found")
	ErrGPUSpecConflict = errors.New("gpu spec already exists")
	ErrGPUSpecInUse    = errors.New("gpu spec in use by running instances")
)
