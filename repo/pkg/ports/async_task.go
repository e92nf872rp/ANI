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

type AsyncTaskStore interface {
	Create(context.Context, AsyncTaskRecord) (AsyncTaskRecord, bool, error)
	Get(context.Context, string, string) (AsyncTaskRecord, error)
	Update(context.Context, AsyncTaskUpdate) (AsyncTaskRecord, error)
}
