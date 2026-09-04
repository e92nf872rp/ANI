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
	Publish          bool
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

type PublicationTarget struct {
	TenantID        uuid.UUID
	ServiceID       uuid.UUID
	Generation      int64
	Desired         domain.PublicationDesired
	ServedModelName string
	Task            domain.InferenceTask
	RuntimeEndpoint string
	LeaseToken      uuid.UUID
}

type PublicationResult struct {
	TenantID      uuid.UUID
	ServiceID     uuid.UUID
	Generation    int64
	LeaseToken    uuid.UUID
	Phase         domain.PublicationPhase
	InvocationURL string
	Now           time.Time
}

type PublicationStore interface {
	ClaimPublication(context.Context, string, time.Time, time.Duration) (PublicationTarget, bool, error)
	RenewPublication(context.Context, PublicationTarget, time.Time, time.Duration) error
	CompletePublication(context.Context, PublicationResult) error
	FailPublication(context.Context, PublicationTarget, string, time.Time) error
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
	PublicationWithdrawn(context.Context, uuid.UUID, uuid.UUID, int64) (bool, error)
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

// AccessPolicyStore 是推理访问策略控制面和数据面检查共用的持久化边界。
// 实现必须在每次调用的事务内设置 app.current_tenant_id，禁止跨租户查询。
type AccessPolicyStore interface {
	ListAccessPolicies(context.Context, uuid.UUID) ([]domain.AccessPolicy, error)
	GetAccessPolicy(context.Context, uuid.UUID, uuid.UUID) (domain.AccessPolicy, error)
	CreateAccessPolicy(context.Context, domain.AccessPolicy, uuid.UUID) (domain.AccessPolicy, error)
	UpdateAccessPolicy(context.Context, domain.AccessPolicy, uuid.UUID, string) (domain.AccessPolicy, error)
	DeleteAccessPolicy(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error
	ListServiceAccessPolicies(context.Context, uuid.UUID, uuid.UUID) ([]domain.AccessPolicy, error)
	ReplaceServiceAccessPolicies(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID, uuid.UUID) ([]domain.AccessPolicy, error)
	ListAccessPolicyEvents(context.Context, uuid.UUID, domain.AccessPolicyEventQuery) ([]domain.AccessPolicyEvent, string, error)
	RecordAccessPolicyEvent(context.Context, domain.AccessPolicyEvent) error
	ResolvePublishedService(context.Context, uuid.UUID, string) (domain.Service, error)
}
