package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusDeploying Status = "deploying"
	StatusRunning   Status = "running"
	StatusStopping  Status = "stopping"
	StatusStopped   Status = "stopped"
	StatusFailed    Status = "failed"
)

type DesiredState string

const (
	DesiredStateRunning DesiredState = "running"
	DesiredStateStopped DesiredState = "stopped"
	DesiredStateDeleted DesiredState = "deleted"
)

type Action string

const (
	ActionCreate  Action = "create"
	ActionScale   Action = "scale"
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
	ActionDelete  Action = "delete"
)

type InferenceTask string

const (
	InferenceTaskGenerate InferenceTask = "generate"
	InferenceTaskEmbed    InferenceTask = "embed"
)

func NormalizeInferenceTask(task InferenceTask) InferenceTask {
	if task == InferenceTaskEmbed {
		return InferenceTaskEmbed
	}
	return InferenceTaskGenerate
}

type OperationState string

const (
	OperationPending    OperationState = "pending"
	OperationRunning    OperationState = "running"
	OperationCompleted  OperationState = "completed"
	OperationFailed     OperationState = "failed"
	OperationCancelled  OperationState = "cancelled"
	OperationDeadLetter OperationState = "dead_letter"
)

// Accelerator 引用 Core 加速器。nil 表示 CPU 推理。
type Accelerator struct {
	// SpecID 是 GPU 型号，例如 gpu-nvidia-geforce-rtx-4090。
	// 只表示型号，不表示整卡或 vGPU。历史 -full / -Nx 剥后缀后仍按型号处理。
	SpecID string `json:"spec_id"`
	// CountPerReplica 是每个副本申请的卡数。整卡和 vGPU 都必填，最小 1。
	CountPerReplica int `json:"count_per_replica"`
	// MemoryMB 是申请 GPU 显存，单位 MiB。对应产品字段 accelerator.memory。
	// 内部 0 表示未填，按整卡；>0 表示 vGPU。JSON 若出现 memory 必须 >= 1。
	// 这不是 Spec.Memory 的内存预算。
	MemoryMB int `json:"memory,omitempty"`
}

// ExecutionProfile 在创建时冻结，后续 catalog/镜像变更不改写已有服务。
type ExecutionProfile struct {
	ID             string        `json:"id"`
	Version        string        `json:"version"`
	Runtime        string        `json:"runtime"` // vllm | sglang
	Task           InferenceTask `json:"task,omitempty"`
	ImageID        string        `json:"image_id,omitempty"`
	ImageRef       string        `json:"image_ref"`
	ArtifactRef    string        `json:"artifact_ref"` // pvc://...#/models/...
	ArtifactDigest string        `json:"artifact_digest"`
	SecretRef      string        `json:"secret_ref,omitempty"`
}

// EngineEnvVar 是创建时冻结的租户环境变量，不是 shell 赋值。
type EngineEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Engine 是创建时冻结的前端 env 与完整启动命令。nil 表示沿用平台默认。
type Engine struct {
	Env     []EngineEnvVar `json:"env,omitempty"`
	Command []string       `json:"command,omitempty"`
}

// Spec 是一次部署的期望规格。产品 PATCH 目前只改 replicas。
type Spec struct {
	Replicas             int              `json:"replicas"`
	CPU                  string           `json:"cpu,omitempty"`
	Memory               string           `json:"memory,omitempty"` // Pod 内存预算，例如 16Gi；GPU 显存在 Accelerator.MemoryMB
	Accelerator          *Accelerator     `json:"accelerator,omitempty"`
	PlacementMode        string           `json:"placement_mode,omitempty"` // auto | single_node | multi_node
	Engine               *Engine          `json:"engine,omitempty"`
	ExecutionProfile     ExecutionProfile `json:"execution_profile"`
	LegacyGPUType        string           `json:"gpu_type,omitempty"`
	LegacyGPUCountPerPod int              `json:"gpu_count_per_pod,omitempty"`
}

func (s Spec) UsesAccelerator() bool {
	return s.Accelerator != nil
}

// Service 是推理服务主资源。产品对外投影不含 RuntimeRef / RuntimeEndpoint。
type Service struct {
	ID                 uuid.UUID
	TenantID           uuid.UUID
	Name               string
	ModelVersionID     uuid.UUID
	ServedModelName    string          // 引擎 --served-model-name，默认等于 Name
	ModelSnapshot      json.RawMessage // 创建时冻结的展示名/format，供列表 model 字段
	Status             Status
	StatusReason       string
	StatusMessage      string
	DesiredState       DesiredState
	Generation         int64 // 每次有效 mutation +1，与 Core 幂等键绑定
	ObservedGeneration int64
	DesiredSpec        Spec
	AppliedSpec        Spec      // 已在 Core 落地的规格，scale 失败时回滚到这里
	RuntimeRef         uuid.UUID // Core platform-workload ID
	RuntimeEndpoint    string    // 集群内 ClusterIP，不对租户返回
	InvocationURL      string    // P0 固定空
	ReadyReplicas      int
	CurrentOperationID uuid.UUID
	ActiveOperationID  uuid.UUID
	ActiveOperation    Action
	CreatedAt          time.Time
	UpdatedAt          time.Time
	DeletedAt          *time.Time
	LegacyQuarantined  bool // 旧行不可继续 mutation，需显式迁移
}

// Operation 是一次 create/scale/lifecycle/delete。worker 按 lease 领取 pending 行。
type Operation struct {
	ID                   uuid.UUID
	TenantID             uuid.UUID
	ServiceID            uuid.UUID
	Type                 Action
	State                OperationState
	TargetGeneration     int64
	RollbackGeneration   int64 // 非 0 表示正在做 scale 补偿
	BeforeSpec           Spec
	TargetSpec           Spec
	PreemptedOperationID uuid.UUID
	OperationScope       string
	IdempotencyKey       uuid.UUID
	RequestHash          string
	Attempt              int
	NextAttemptAt        time.Time
	LeaseOwner           string
	LeaseUntil           *time.Time
	LeaseToken           uuid.UUID
	RuntimeTaskID        string
	ErrorCode            string
	ErrorMessage         string
	ResultSnapshot       json.RawMessage
	CreatedAt            time.Time
	UpdatedAt            time.Time
	CompletedAt          *time.Time
	Replayed             bool
}

func (o Operation) TaskType() string {
	return "inference_service." + string(o.Type)
}
