package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
)

var (
	ErrNotFound            = errors.New("inference control-plane record not found")
	ErrNameConflict        = errors.New("inference service name already exists")
	ErrIdempotencyConflict = errors.New("idempotency key was reused with a different request")
	ErrStaleGeneration     = errors.New("inference service generation is stale")
)

// CreateResult 是创建落库结果。Replayed 表示命中幂等重放。
type CreateResult struct {
	Service   domain.Service
	Operation domain.Operation
	Replayed  bool
}

type MutationRequest struct {
	TenantID       uuid.UUID
	ServiceID      uuid.UUID
	Action         domain.Action
	TargetSpec     domain.Spec
	OperationID    uuid.UUID
	OperationScope string
	IdempotencyKey uuid.UUID
	RequestHash    string
	Now            time.Time
}

// MutationResult 带 TransitionDisposition：新建、重放或已是目标态。
type MutationResult struct {
	Service     domain.Service
	Operation   domain.Operation
	Disposition domain.TransitionDisposition
}

type Observation struct {
	TenantID         uuid.UUID
	ServiceID        uuid.UUID
	OperationID      uuid.UUID
	TargetGeneration int64
	Status           domain.Status
	AppliedSpec      domain.Spec
	RuntimeRef       uuid.UUID
	RuntimeEndpoint  string
	ReadyReplicas    int
	Complete         bool
	Deleted          bool
	LeaseToken       uuid.UUID
}

type Failure struct {
	TenantID         uuid.UUID
	ServiceID        uuid.UUID
	OperationID      uuid.UUID
	TargetGeneration int64
	ErrorCode        string
	ErrorMessage     string
	RetryAt          *time.Time
	LeaseToken       uuid.UUID
	DeadLetter       bool
}

type ScaleRollback struct {
	TenantID         uuid.UUID
	ServiceID        uuid.UUID
	OperationID      uuid.UUID
	TargetGeneration int64
	LeaseToken       uuid.UUID
}

type ScaleRollbackFinish struct {
	TenantID           uuid.UUID
	ServiceID          uuid.UUID
	OperationID        uuid.UUID
	RollbackGeneration int64
	AppliedSpec        domain.Spec
	RuntimeRef         uuid.UUID
	RuntimeEndpoint    string
	ReadyReplicas      int
	LeaseToken         uuid.UUID
	Success            bool
}

type RuntimeBinding struct {
	TenantID    uuid.UUID
	ServiceID   uuid.UUID
	OperationID uuid.UUID
	Generation  int64
	RuntimeRef  uuid.UUID
}

type MutationAbort struct {
	TenantID           uuid.UUID
	ServiceID          uuid.UUID
	OperationID        uuid.UUID
	TargetGeneration   int64
	RestoredGeneration int64
	RestoredSpec       domain.Spec
	RestoredStatus     domain.Status
	RestoredDesired    domain.DesiredState
}

type Store interface {
	FindCreateReplay(context.Context, uuid.UUID, string, uuid.UUID, string) (CreateResult, bool, error)
	CreateWithOperation(context.Context, domain.Service, domain.Operation) (CreateResult, error)
	BindRuntimeRef(context.Context, RuntimeBinding) error // Core 已接受后写入 runtime_ref，状态 deploying
	AbortCreate(context.Context, RuntimeBinding) error    // Core 拒绝创建时删掉未提交行
	GetService(context.Context, uuid.UUID, uuid.UUID) (domain.Service, error)
	GetOperation(context.Context, uuid.UUID, uuid.UUID) (domain.Operation, error)
	ClaimOperation(context.Context, string, time.Time, time.Duration) (domain.Operation, bool, error)
	ApplyObservation(context.Context, Observation) error
	FailOperation(context.Context, Failure) error
	BeginScaleRollback(context.Context, ScaleRollback) (int64, error)
	FinishScaleRollback(context.Context, ScaleRollbackFinish) error
}

// ControlStore 给请求路径的查询与 mutation。AbortPendingMutation 在 Core 拒绝后回滚到点击前。
type ControlStore interface {
	GetService(context.Context, uuid.UUID, uuid.UUID) (domain.Service, error)
	GetOperation(context.Context, uuid.UUID, uuid.UUID) (domain.Operation, error)
	ListServices(context.Context, uuid.UUID) ([]domain.Service, error)
	MutateService(context.Context, MutationRequest) (MutationResult, error)
	BindRuntimeRef(context.Context, RuntimeBinding) error
	AbortPendingMutation(context.Context, MutationAbort) error
}
