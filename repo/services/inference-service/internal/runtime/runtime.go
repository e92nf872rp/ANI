package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
)

var (
	ErrRuntimeNotFound         = errors.New("inference runtime not found")
	ErrStaleRuntimeGeneration  = errors.New("inference runtime generation is stale")
	ErrRuntimeIntentConflict   = errors.New("inference runtime idempotency intent conflicts")
	ErrRuntimeUnsupported      = errors.New("inference runtime is not supported")
	ErrImageUnavailable        = errors.New("inference runtime image is unavailable")
	ErrEngineProfileUnapproved = errors.New("inference engine profile is not approved")
	ErrReservedFieldConflict   = errors.New("inference reserved field conflicts")
	ErrUnsupportedTopology     = errors.New("inference runtime topology is unsupported")
	ErrInsufficientCapacity    = errors.New("inference runtime capacity is insufficient")
)

// EnsureRequest 是创建或 PATCH replicas 的入参。RuntimeRef 为空则 POST。
type EnsureRequest struct {
	TenantID        uuid.UUID
	ServiceID       uuid.UUID
	RuntimeRef      uuid.UUID // 已有 Core workload 时走 PATCH，否则 POST
	Generation      int64
	IdempotencyKey  uuid.UUID
	Name            string
	ServedModelName string
	Spec            domain.Spec
}

// Observation 是 Core platform-workload 的观测投影。
type Observation struct {
	RuntimeRef      uuid.UUID
	RuntimeEndpoint string // ClusterIP，只给 Health/Smoke 用
	ReadyReplicas   int
	Ready           bool
}

// RuntimeIdentity 只读观察用，不携带 mutation 意图。
type RuntimeIdentity struct {
	TenantID   uuid.UUID
	ServiceID  uuid.UUID
	RuntimeRef uuid.UUID
}

type LifecycleRequest struct {
	TenantID       uuid.UUID
	ServiceID      uuid.UUID
	RuntimeRef     uuid.UUID
	Generation     int64
	IdempotencyKey uuid.UUID
	Action         domain.Action
}

type DeleteRequest struct {
	TenantID       uuid.UUID
	ServiceID      uuid.UUID
	RuntimeRef     uuid.UUID
	Generation     int64
	IdempotencyKey uuid.UUID
}

type LogQuery struct {
	TenantID   uuid.UUID
	ServiceID  uuid.UUID
	RuntimeRef uuid.UUID
	Limit      int
	Cursor     string
	Level      string
}

type LogEntry struct {
	Timestamp time.Time
	Level     string
	Message   string
	Container string
	Stream    string
}

type LogPage struct {
	Items      []LogEntry
	NextCursor string
}

// InferenceRuntime 是 Services 到 Core platform-workloads 的唯一出口。
type InferenceRuntime interface {
	Ensure(context.Context, EnsureRequest) (Observation, error)            // 创建或按 replicas PATCH
	Observe(context.Context, RuntimeIdentity) (Observation, error)         // 只读对齐，不创建
	ApplyLifecycle(context.Context, LifecycleRequest) (Observation, error) // start/stop/restart
	Delete(context.Context, DeleteRequest) error
	Health(context.Context, uuid.UUID, uuid.UUID) error                              // GET runtime /health
	Smoke(context.Context, uuid.UUID, uuid.UUID, string, domain.InferenceTask) error // 有界 Chat/Embeddings 探活
	Logs(context.Context, LogQuery) (LogPage, error)
}
