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
	// AcceleratorSpecID 是 GPU 型号，例如 gpu-nvidia-geforce-rtx-4090。
	// 只表示型号，不表示整卡或 vGPU。历史 -full / -Nx 剥后缀后仍按型号处理。
	AcceleratorSpecID string
	// AcceleratorCount 是申请卡数。整卡和 vGPU 都必填，最小 1。
	AcceleratorCount int
	// AcceleratorMemoryMB 是申请 GPU 显存，单位 MiB。
	// 内部 0 表示请求未填，按整卡（nvidia.com/gpu）；>0 表示 vGPU（volcano.sh/vgpu-*）。
	// JSON 若出现 memory，必须 >= 1，不得把 0 或负数静默当整卡。
	// 这不是 Memory 字段的内存预算。
	AcceleratorMemoryMB int
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
	IdempotencyKey string
	Name           string
	WorkloadClass  string
	RuntimeKind    string
	ImageRef       string
	Command        []string
	Args           []string
	Env            []PlatformWorkloadEnvVar
	Replicas       int
	Resources      PlatformWorkloadResources
	Topology       PlatformWorkloadTopology
	Scheduling     PlatformWorkloadScheduling
	Network        PlatformWorkloadNetwork
	Artifacts      []PlatformWorkloadArtifact
	SecretBindings []PlatformWorkloadSecretBinding
	HealthCheck    PlatformWorkloadHealthCheck
	Metadata       PlatformWorkloadMetadata
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
	// SpecID 是 GPU 型号，例如 gpu-nvidia-geforce-rtx-4090。
	// capabilities 只广告型号，不广告 -full / -Nx；整卡或 vGPU 由创建请求有没有 memory 决定。
	SpecID             string
	Available          bool
	MaxSingleNodeCount int // 对外提示：整卡与 vGPU 单节点上限的较大值，不是准入依据
	MaxWholeCardCount  int // 内部：单节点 nvidia.com/gpu 上限；不对外广告
	MaxVGPUCount       int // 内部：单节点 volcano.sh/vgpu-number 上限；不对外广告
	MemoryPerShareMB   int // 内部残留，不对外广告；创建显存以请求 memory 为准
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
