package ports

import (
	"context"
	"time"
)

type ObservabilityResultType string

const (
	ObservabilityResultVector ObservabilityResultType = "vector"
	ObservabilityResultMatrix ObservabilityResultType = "matrix"
	ObservabilityResultScalar ObservabilityResultType = "scalar"
	ObservabilityResultString ObservabilityResultType = "string"
)

type ObservabilityAlertSeverity string

const (
	ObservabilityAlertSeverityInfo     ObservabilityAlertSeverity = "info"
	ObservabilityAlertSeverityWarning  ObservabilityAlertSeverity = "warning"
	ObservabilityAlertSeverityCritical ObservabilityAlertSeverity = "critical"
)

type ObservabilityAlertRuleState string

const (
	ObservabilityAlertRuleActive   ObservabilityAlertRuleState = "active"
	ObservabilityAlertRuleDisabled ObservabilityAlertRuleState = "disabled"
	ObservabilityAlertRuleDeleted  ObservabilityAlertRuleState = "deleted"
)

type ObservabilityQueryRequest struct {
	TenantID string
	Query    string
	Time     time.Time
	Timeout  time.Duration
}

type ObservabilityQuerySample struct {
	Metric    map[string]string
	Value     float64
	Timestamp time.Time
}

type ObservabilityQueryResult struct {
	Query      string
	ResultType ObservabilityResultType
	Results    []ObservabilityQuerySample
	DevProfile DevProfileInfo
}

// ObservabilityRangeQueryRequest 区间查询请求，用于绘制时序曲线。
type ObservabilityRangeQueryRequest struct {
	TenantID string
	Query    string
	Start    time.Time
	End      time.Time
	Step     time.Duration
	Timeout  time.Duration
}

// ObservabilityRangePoint 单个时间序列采样点。
type ObservabilityRangePoint struct {
	Timestamp time.Time
	Value     float64
}

// ObservabilityRangeSeries 一条时间序列，含 metric 标签与多个采样点。
type ObservabilityRangeSeries struct {
	Metric map[string]string
	Values []ObservabilityRangePoint
}

// ObservabilityRangeQueryResult 区间查询结果（matrix）。
type ObservabilityRangeQueryResult struct {
	Query      string
	ResultType ObservabilityResultType
	Results    []ObservabilityRangeSeries
	DevProfile DevProfileInfo
}

// ObservabilityResourceTrendMetric 租户级资源趋势维度。
type ObservabilityResourceTrendMetric string

const (
	ObservabilityResourceTrendGPU    ObservabilityResourceTrendMetric = "gpu"
	ObservabilityResourceTrendCPU    ObservabilityResourceTrendMetric = "cpu"
	ObservabilityResourceTrendMemory ObservabilityResourceTrendMetric = "memory"
)

// ObservabilityResourceTrendRequest 租户级资源使用率趋势请求。
// TenantID 必须由后端从 JWT 提取并强制锚定 namespace="ani-tenant-<id>"，
// 禁止透传/接受前端 PromQL，保证租户隔离不破。
type ObservabilityResourceTrendRequest struct {
	TenantID string
	Metric   ObservabilityResourceTrendMetric
	Start    time.Time
	End      time.Time
	Step     time.Duration
	Timeout  time.Duration
}

type ObservabilityAlertRuleRecord struct {
	TenantID    string
	RuleID      string
	Name        string
	PromQL      string
	Duration    time.Duration
	Severity    ObservabilityAlertSeverity
	Labels      map[string]string
	Annotations map[string]string
	Enabled     bool
	State       ObservabilityAlertRuleState
	DevProfile  DevProfileInfo
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ObservabilityAlertRuleCreateRequest struct {
	TenantID       string
	IdempotencyKey string
	Name           string
	PromQL         string
	Duration       time.Duration
	Severity       ObservabilityAlertSeverity
	Labels         map[string]string
	Annotations    map[string]string
	Enabled        bool
}

type ObservabilityAlertRuleUpdateRequest struct {
	TenantID       string
	RuleID         string
	IdempotencyKey string
	Name           string
	PromQL         string
	Duration       time.Duration
	Severity       ObservabilityAlertSeverity
	Labels         map[string]string
	Annotations    map[string]string
	Enabled        *bool
}

type ObservabilityAlertRuleGetRequest struct {
	TenantID string
	RuleID   string
}

type ObservabilityAlertRuleListRequest struct {
	TenantID string
	Limit    int
	Cursor   string
}

type ObservabilityService interface {
	Query(ctx context.Context, request ObservabilityQueryRequest) (ObservabilityQueryResult, error)
	QueryRange(ctx context.Context, request ObservabilityRangeQueryRequest) (ObservabilityRangeQueryResult, error)
	// QueryResourceTrend 返回租户级资源使用率趋势（matrix），
	// 后端强制锚定当前租户 namespace，杜绝跨租户裸聚合。
	QueryResourceTrend(ctx context.Context, request ObservabilityResourceTrendRequest) (ObservabilityRangeQueryResult, error)
	CreateAlertRule(ctx context.Context, request ObservabilityAlertRuleCreateRequest) (ObservabilityAlertRuleRecord, error)
	ListAlertRules(ctx context.Context, request ObservabilityAlertRuleListRequest) ([]ObservabilityAlertRuleRecord, error)
	GetAlertRule(ctx context.Context, request ObservabilityAlertRuleGetRequest) (ObservabilityAlertRuleRecord, error)
	UpdateAlertRule(ctx context.Context, request ObservabilityAlertRuleUpdateRequest) (ObservabilityAlertRuleRecord, error)
	DeleteAlertRule(ctx context.Context, request ObservabilityAlertRuleGetRequest) (ObservabilityAlertRuleRecord, error)
}
