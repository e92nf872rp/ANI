package ports

import (
	"context"
	"time"
)

type MeteringResourceType string

const (
	MeteringResourceInstanceCPUSeconds    MeteringResourceType = "instance_cpu_seconds"
	MeteringResourceInstanceMemorySeconds MeteringResourceType = "instance_memory_gib_seconds"
	MeteringResourceInstanceGPUSeconds    MeteringResourceType = "instance_gpu_seconds"
	MeteringResourceTokenInput            MeteringResourceType = "token_input"
	MeteringResourceTokenOutput           MeteringResourceType = "token_output"
	MeteringResourceTokenTotal            MeteringResourceType = "token_total"
)

type TokenUsageReportState string

const (
	TokenUsageReportAccepted  TokenUsageReportState = "accepted"
	TokenUsageReportDuplicate TokenUsageReportState = "duplicate"
)

type MeteringUsageQueryRequest struct {
	TenantID     string
	StartTime    time.Time
	EndTime      time.Time
	ResourceType MeteringResourceType
	GroupBy      string
	// IsPlatform 标记平台跨租户查询。true 时 TenantID 可为空（全平台）或指定单租户筛选。
	IsPlatform bool
}

type MeteringUsageRecord struct {
	TenantID      string
	ResourceType  MeteringResourceType
	TotalQuantity float64
	Unit          string
	Period        string
}

type MeteringUsageResult struct {
	Items      []MeteringUsageRecord
	DevProfile DevProfileInfo
}

type TokenUsageReportRequest struct {
	TenantID       string
	IdempotencyKey string
	Source         string
	Model          string
	InputTokens    int64
	OutputTokens   int64
	RequestID      string
	InstanceID     string
	OccurredAt     time.Time
	Labels         map[string]string
}

type TokenUsageReportRecord struct {
	TenantID     string
	ReportID     string
	Source       string
	Model        string
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	RequestID    string
	InstanceID   string
	State        TokenUsageReportState
	DevProfile   DevProfileInfo
	CreatedAt    time.Time
}

type MeteringService interface {
	QueryUsage(ctx context.Context, request MeteringUsageQueryRequest) (MeteringUsageResult, error)
	// QueryPlatformUsage 查询平台跨租户用量。平台视角下 items[].tenant_id 必填。
	// 当 request.TenantID 非空时按单租户筛选，否则返回全平台聚合数据。
	QueryPlatformUsage(ctx context.Context, request MeteringUsageQueryRequest) (MeteringUsageResult, error)
	ReportTokenUsage(ctx context.Context, request TokenUsageReportRequest) (TokenUsageReportRecord, error)
}
