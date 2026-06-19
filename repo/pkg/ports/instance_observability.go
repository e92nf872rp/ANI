package ports

import (
	"context"
	"time"
)

type InstanceObservationListRequest struct {
	TenantID   string
	InstanceID string
	Limit      int
	Cursor     string
	Level      string
	Type       string
	Severity   string
}

type InstanceObservationGetRequest struct {
	TenantID   string
	InstanceID string
}

type InstanceLogEntry struct {
	Timestamp time.Time
	Level     string
	Message   string
	Container string
	Stream    string
}

type InstanceLogListResult struct {
	Items      []InstanceLogEntry
	Total      int
	NextCursor string
	DevProfile DevProfileInfo
}

type InstanceEventRecord struct {
	ID         string
	InstanceID string
	Type       string
	Reason     string
	Message    string
	Count      int
	OccurredAt time.Time
}

type InstanceEventListResult struct {
	Items      []InstanceEventRecord
	Total      int
	NextCursor string
	DevProfile DevProfileInfo
}

type InstanceMetricsRecord struct {
	InstanceID        string
	Timestamp         time.Time
	CPUUtilizationPct *float64
	MemoryUsedMB      *float64
	MemoryTotalMB     *float64
	GPUUtilizationPct *float64
	GPUMemoryUsedMB   *float64
	GPUMemoryTotalMB  *float64
	NetworkRXBytes    *int64
	NetworkTXBytes    *int64
	DevProfile        DevProfileInfo
}

type InstanceSecurityEventRecord struct {
	ID          string
	InstanceID  string
	EventType   string
	Severity    string
	Description string
	OccurredAt  time.Time
}

type InstanceSecurityEventListResult struct {
	Items      []InstanceSecurityEventRecord
	Total      int
	NextCursor string
	DevProfile DevProfileInfo
}

type InstanceExecSessionCreateRequest struct {
	TenantID       string
	InstanceID     string
	IdempotencyKey string
	Container      string
	Command        []string
	TTY            bool
	Rows           int
	Cols           int
}

type InstanceExecSessionRecord struct {
	ID         string
	InstanceID string
	WSURL      string
	Token      string
	ExpiresAt  time.Time
	DevProfile DevProfileInfo
}

// InstanceObservability exposes local/real runtime observations without
// leaking Kubernetes, kubelet, Prometheus, or terminal provider SDK objects.
type InstanceObservability interface {
	ListLogs(ctx context.Context, request InstanceObservationListRequest) (InstanceLogListResult, error)
	ListEvents(ctx context.Context, request InstanceObservationListRequest) (InstanceEventListResult, error)
	GetMetrics(ctx context.Context, request InstanceObservationGetRequest) (InstanceMetricsRecord, error)
	ListSecurityEvents(ctx context.Context, request InstanceObservationListRequest) (InstanceSecurityEventListResult, error)
	CreateExecSession(ctx context.Context, request InstanceExecSessionCreateRequest) (InstanceExecSessionRecord, error)
}
