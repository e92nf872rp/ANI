package ports

import (
	"context"
	"time"
)

type AsyncTaskRecord struct {
	TenantID       string
	ID             string
	IdempotencyKey string
	TaskType       string
	ResourceType   string
	ResourceID     string
	Status         string
	AttemptCount   int
	MaxAttempts    int
	ProgressPct    int
	Result         map[string]any
	ErrorMessage   string
	DeadLetterAt   time.Time
	CreatedAt      time.Time
	CompletedAt    time.Time
}

type AsyncTaskUpdate struct {
	TenantID     string
	ID           string
	Status       string
	AttemptCount int
	ProgressPct  int
	Result       map[string]any
	ErrorMessage string
	DeadLetterAt time.Time
	CompletedAt  time.Time
}

// AsyncTaskListFilter selects tenant tasks for List. Empty filter fields
// mean "no filter". Cursor is an opaque keyset token encoding the
// (created_at, id) anchor of the last returned row.
type AsyncTaskListFilter struct {
	Status, TaskType, ResourceType string
	Limit                          int
	Cursor                         string
}

type AsyncTaskStore interface {
	Create(context.Context, AsyncTaskRecord) (AsyncTaskRecord, bool, error)
	Get(context.Context, string, string) (AsyncTaskRecord, error)
	// Update overwrites the mutable task fields. Terminal rows (completed,
	// failed, cancelled, dead_letter) cannot be rewritten with a different
	// status: implementations must enforce this atomically (SQL guard or
	// lock-held comparison) and, when the guard blocks the write, return the
	// current stored record instead of an error, so a late concurrent writer
	// cannot revert a terminal task to running.
	Update(context.Context, AsyncTaskUpdate) (AsyncTaskRecord, error)
	// List returns tenant-scoped tasks ordered by created_at DESC, id DESC
	// with keyset cursor pagination (limit+1 probing yields nextCursor; an
	// empty nextCursor means the last page). An invalid cursor must surface
	// as ErrInvalid.
	List(context.Context, string, AsyncTaskListFilter) ([]AsyncTaskRecord, string, error)
}
