package ports

import (
	"context"
	"time"
)

type PlatformWorkloadState string

const (
	PlatformWorkloadPending      PlatformWorkloadState = "pending"
	PlatformWorkloadProvisioning PlatformWorkloadState = "provisioning"
	PlatformWorkloadRunning      PlatformWorkloadState = "running"
	PlatformWorkloadStarting     PlatformWorkloadState = "starting"
	PlatformWorkloadStopping     PlatformWorkloadState = "stopping"
	PlatformWorkloadStopped      PlatformWorkloadState = "stopped"
	PlatformWorkloadFailed       PlatformWorkloadState = "failed"
	PlatformWorkloadDeleting     PlatformWorkloadState = "deleting"
	PlatformWorkloadDeleted      PlatformWorkloadState = "deleted"
)

type PlatformWorkloadResources struct {
	CPU    string
	Memory string // Pod 内存预算，例如 16Gi；不是 GPU 显存
	// AcceleratorSpecID 是用户选择的 Core GPUSpec ID，例如 rtx4090-12g-4。
	// 旧节点型号 ID 只在迁移期由 Core 解析，不再作为新请求的规范值。
	AcceleratorSpecID string
	// AcceleratorCount 是申请卡数。整卡和 vGPU 都必填，最小 1。
	AcceleratorCount int
	// AcceleratorMemoryMB 是申请 GPU 显存，单位 MiB。
	// 内部 0 表示请求未填，按整卡（nvidia.com/gpu）；>0 表示 vGPU（volcano.sh/vgpu-*）。
	// JSON 若出现 memory，必须 >= 1，不得把 0 或负数静默当整卡。
	// 这不是 Memory 字段的内存预算。
	AcceleratorMemoryMB int
	// The following fields are filled by the Core runtime after resolving the
	// public GPUSpec ID. They are an internal scheduling snapshot and are not
	// part of the tenant-facing request contract.
	AcceleratorNodeSelector     map[string]string
	AcceleratorResourceRequests map[string]string
	AcceleratorSchedulerName    string
	AcceleratorAnnotations      map[string]string
}

type PlatformWorkloadEnvVar struct {
	Name  string
	Value string
}

type PlatformWorkloadRole struct {
	Count     int
	Resources PlatformWorkloadResources
}

type PlatformWorkloadTopology struct {
	Mode           string
	ProfileID      string
	ProfileVersion string
	HasLeader      bool
	HasWorkers     bool
	Leader         PlatformWorkloadRole
	Workers        PlatformWorkloadRole
}

type PlatformWorkloadScheduling struct {
	QueueClass string
	Gang       bool
}

type PlatformWorkloadNetwork struct {
	Exposure string
	Ports    []PlatformWorkloadPort
}

type PlatformWorkloadPort struct {
	Name string
	Port int
}

type PlatformWorkloadArtifact struct {
	ObjectRef string
	MountPath string
}

// PlatformWorkloadModelMaterialization describes a tenant-fenced object model
// that must be fetched and verified before the runtime container starts.
// It intentionally contains no signed URL or credentials.
type PlatformWorkloadModelMaterialization struct {
	TenantID             string
	ModelVersionID       string
	ObjectRef            string
	SizeBytes            int64
	ChecksumSHA256       string
	ModelServiceGRPCAddr string
	FetcherImageRef      string
	TargetPath           string
}

type PlatformWorkloadSecretBinding struct {
	SecretRef string
	MountPath string
}

type PlatformWorkloadHealthCheck struct {
	Protocol string
	Path     string
	PortName string
}

type PlatformWorkloadMetadata struct {
	OwnerRef string
	Labels   map[string]string
}

type PlatformWorkloadCreateSpec struct {
	IdempotencyKey       string
	Name                 string
	WorkloadClass        string
	RuntimeKind          string
	ImageRef             string
	Command              []string
	Args                 []string
	Env                  []PlatformWorkloadEnvVar
	Replicas             int
	Resources            PlatformWorkloadResources
	Topology             PlatformWorkloadTopology
	Scheduling           PlatformWorkloadScheduling
	Network              PlatformWorkloadNetwork
	Artifacts            []PlatformWorkloadArtifact
	ModelMaterialization *PlatformWorkloadModelMaterialization
	SecretBindings       []PlatformWorkloadSecretBinding
	HealthCheck          PlatformWorkloadHealthCheck
	Metadata             PlatformWorkloadMetadata
}

type PlatformWorkloadRecord struct {
	ID                     string
	TenantID               string
	Name                   string
	State                  PlatformWorkloadState
	Generation             int64
	ObservedGeneration     int64
	DesiredReplicas        int
	ReadyReplicas          int
	RuntimeShape           string
	TopologyProfileID      string
	TopologyProfileVersion string
	InternalEndpoint       string
	Reason                 string
	Message                string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type PlatformWorkloadCapabilities struct {
	SupportedTopologyModes []string
	LeaderWorkerSetReady   bool
	GangSchedulingReady    bool
	SupportedProfiles      []PlatformWorkloadTopologyProfile
	AcceleratorSpecs       []PlatformWorkloadAcceleratorCapability
}

type PlatformWorkloadTopologyProfile struct {
	ID      string
	Version string
	Mode    string
}

type PlatformWorkloadAcceleratorCapability struct {
	// SpecID 是用户可引用的 Core GPUSpec ID，例如 rtx4090-12g-4。
	SpecID             string
	Available          bool
	MaxSingleNodeCount int // 对外提示：整卡与 vGPU 单节点上限的较大值，不是准入依据
	MaxWholeCardCount  int // 内部：单节点 nvidia.com/gpu 上限；不对外广告
	MaxVGPUCount       int // 内部：单节点 volcano.sh/vgpu-number 上限；不对外广告
	MemoryPerShareMB   int // vGPU 规格的每份显存；整卡为 0
	GPUMode            string
	Aliases            []string // 兼容窗口内接受的旧节点型号 ID
}

type PlatformWorkloadLogEntry struct {
	Timestamp time.Time
	Level     string
	Message   string
	Replica   string
	Container string
	Stream    string
}

type PlatformWorkloadLogList struct {
	Items      []PlatformWorkloadLogEntry
	NextCursor string
}

// PlatformWorkloadService is the Core product boundary for service-only
// platform workloads. It must not create tenant /instances records.
type PlatformWorkloadService interface {
	Capabilities(context.Context) (PlatformWorkloadCapabilities, error)
	Create(context.Context, string, PlatformWorkloadCreateSpec) (PlatformWorkloadRecord, error)
	Get(context.Context, string, string) (PlatformWorkloadRecord, error)
	UpdateReplicas(context.Context, string, string, string, int) (PlatformWorkloadRecord, error)
	ApplyLifecycle(context.Context, string, string, string, string) (PlatformWorkloadRecord, error)
	Delete(context.Context, string, string, string) (PlatformWorkloadRecord, error)
	Logs(context.Context, string, string, int, string, string) (PlatformWorkloadLogList, error)
}
