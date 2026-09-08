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
	// PlatformTenantID 平台查询时可选筛选单租户（v1.yaml tenant_id query 参数）。
	PlatformTenantID string
}

// MeteringUsageRecord 表示一条计量用量记录。
// ResourceRef 为新增字段，标识采集的资源引用（如 instance_id），现有字段保持不变。
type MeteringUsageRecord struct {
	TenantID      string
	ResourceRef   string
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
	// QueryPlatformUsage 平台视角跨租户用量查询（BOSS 平台计量页使用）。
	QueryPlatformUsage(ctx context.Context, request MeteringUsageQueryRequest) (MeteringUsageResult, error)
	ReportTokenUsage(ctx context.Context, request TokenUsageReportRequest) (TokenUsageReportRecord, error)
}

// CollectionDimension 描述采集的一个维度，包含资源类型和 collector 来源标识。
type CollectionDimension struct {
	ResourceType MeteringResourceType
	Source       string
}

// CollectionSpec 描述单个资源的周期采集规格，由 consumer/rebuilder 构造后传入
// MeteringCollectionService.StartCollection。
type CollectionSpec struct {
	ResourceRef  string
	WorkloadName string
	TenantID     string
	WorkloadKind string
	Dimensions   []CollectionDimension
	IntervalSec  int
	StartedAt    time.Time
	GPUSpec      *GPUEventSpec
}

// MeteringCollectionService 定义采集生命周期控制契约，与 MeteringService（查询/上报）分离。
//
// StartCollection 启动指定资源的周期采集。幂等语义：进程内 map 按 ResourceRef 去重，
// 已有 ticker 时返回 nil（no-op）；DB UNIQUE 约束兜底重启/重放场景的重复写入。
//
// StopCollection 停止指定资源的周期采集。幂等语义：无 ticker 时返回 nil（no-op）。
type MeteringCollectionService interface {
	StartCollection(ctx context.Context, spec CollectionSpec) error
	StopCollection(ctx context.Context, resourceRef string) error
}
