package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
)

var (
	ErrRuntimeNotFound        = errors.New("inference runtime not found")
	ErrStaleRuntimeGeneration = errors.New("inference runtime generation is stale")
	ErrRuntimeIntentConflict  = errors.New("inference runtime idempotency intent conflicts")
	ErrRuntimeUnsupported     = errors.New("inference runtime is not supported")
)

type EnsureRequest struct {
	TenantID        uuid.UUID
	ServiceID       uuid.UUID
	RuntimeRef      uuid.UUID
	Generation      int64
	IdempotencyKey  uuid.UUID
	Name            string
	ServedModelName string
	Spec            domain.Spec
}

type Observation struct {
	RuntimeRef      uuid.UUID
	RuntimeEndpoint string
	ReadyReplicas   int
	Ready           bool
}

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

type InferenceRuntime interface {
	Ensure(context.Context, EnsureRequest) (Observation, error)
	Observe(context.Context, RuntimeIdentity) (Observation, error)
	ApplyLifecycle(context.Context, LifecycleRequest) (Observation, error)
	Delete(context.Context, DeleteRequest) error
	Health(context.Context, uuid.UUID) error
	Smoke(context.Context, uuid.UUID, string) error
	Logs(context.Context, LogQuery) (LogPage, error)
}
