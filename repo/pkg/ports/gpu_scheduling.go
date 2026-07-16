package ports

import (
	"context"
	"errors"
	"time"
)

// WorkloadClass categorises GPU scheduling queues by workload intent.
type WorkloadClass string

const (
	WorkloadClassInference WorkloadClass = "inference"
	WorkloadClassTraining  WorkloadClass = "training"
	WorkloadClassBatch     WorkloadClass = "batch"
)

// GPUSchedulingQueue is the tenant-scoped Volcano Queue abstraction surfaced
// through the Core API. Data persists in Volcano Queue CRD, not PostgreSQL.
type GPUSchedulingQueue struct {
	ID                string
	Name              string
	Weight            int
	Reclaimable       bool
	WorkloadClass     WorkloadClass
	ProjectID         string // optional; empty means tenant-level queue
	IsPlatformDefault bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// GPUSchedulingQueueCreateRequest carries user-supplied queue fields.
type GPUSchedulingQueueCreateRequest struct {
	Name          string
	Weight        int
	Reclaimable   bool
	WorkloadClass WorkloadClass
	ProjectID     string // optional
}

// GPUSchedulingQueueUpdateRequest carries optional queue patch fields.
// Pointer fields distinguish "omit" from "set to zero".
type GPUSchedulingQueueUpdateRequest struct {
	Weight        *int
	Reclaimable   *bool
	WorkloadClass *WorkloadClass
	ProjectID     *string
}

// GPUSchedulingQueueStore abstracts queue CRUD over Volcano Queue CRD.
// Implementations MUST enforce tenant isolation using the tenant_id from
// the request context and MUST reject PATCH/DELETE on platform default queues.
type GPUSchedulingQueueStore interface {
	List(ctx context.Context, tenantID string) ([]GPUSchedulingQueue, error)
	Get(ctx context.Context, tenantID, id string) (GPUSchedulingQueue, error)
	Create(ctx context.Context, tenantID string, req GPUSchedulingQueueCreateRequest) (GPUSchedulingQueue, error)
	Update(ctx context.Context, tenantID, id string, req GPUSchedulingQueueUpdateRequest) (GPUSchedulingQueue, error)
	Delete(ctx context.Context, tenantID, id string) error
}

// Queue store errors. Use errors.Is to let handlers map to HTTP status codes.
var (
	ErrQueueNotFound            = errors.New("gpu scheduling queue not found")
	ErrQueueNameConflict        = errors.New("gpu scheduling queue name already exists")
	ErrPlatformDefaultProtected = errors.New("platform default queue is protected")
	ErrQueueStoreUnavailable    = errors.New("gpu scheduling queue store unavailable")
)
