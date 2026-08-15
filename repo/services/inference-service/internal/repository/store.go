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
}

type Store interface {
	FindCreateReplay(context.Context, uuid.UUID, string, uuid.UUID, string) (CreateResult, bool, error)
	CreateWithOperation(context.Context, domain.Service, domain.Operation) (CreateResult, error)
	GetService(context.Context, uuid.UUID, uuid.UUID) (domain.Service, error)
	GetOperation(context.Context, uuid.UUID, uuid.UUID) (domain.Operation, error)
	ClaimOperation(context.Context, string, time.Time, time.Duration) (domain.Operation, bool, error)
	ApplyObservation(context.Context, Observation) error
	FailOperation(context.Context, Failure) error
}

type ControlStore interface {
	GetService(context.Context, uuid.UUID, uuid.UUID) (domain.Service, error)
	GetOperation(context.Context, uuid.UUID, uuid.UUID) (domain.Operation, error)
	ListServices(context.Context, uuid.UUID) ([]domain.Service, error)
	MutateService(context.Context, MutationRequest) (MutationResult, error)
}
