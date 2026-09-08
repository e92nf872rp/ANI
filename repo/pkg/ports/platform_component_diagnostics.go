package ports

import (
	"context"
	"errors"
	"time"
)

// 平台组件诊断（平台健康组件状态行内「指标 / 日志」下钻）只读端口。
// 指标来自 Prometheus cAdvisor 聚合（namespace + pod 前缀正则）；
// 日志来自 Loki 持久化存储（namespace + pod 前缀正则）。
// 组件对象名必须命中组件状态静态注册表（componentStatusRegistry），未注册返回 ErrNotFound。
var (
	// ErrPlatformComponentDiagnosticsUnsupported 表示当前 provider 不支持组件诊断查询。
	ErrPlatformComponentDiagnosticsUnsupported = errors.New("platform component diagnostics: unsupported provider")
)

// PlatformComponentRef 组件引用（注册表固定信息）。
type PlatformComponentRef struct {
	Name      string
	Namespace string
	Group     string
}

// PlatformComponentMetrics 单个组件资源指标快照。
// 各指标字段单源失败时为 nil（缺失不等于 0，禁止用 0 代替缺失）。
type PlatformComponentMetrics struct {
	Component    PlatformComponentRef
	Timestamp    time.Time
	CPUCores     *float64
	MemoryUsedMB *float64
	// MemoryTotalMB 取容器内存 limit 聚合；未设 limits 时为 nil。
	MemoryTotalMB        *float64
	NetworkRxBytesPerSec *float64
	NetworkTxBytesPerSec *float64
	DevProfile           DevProfileInfo
}

// PlatformComponentMetricsReader 平台级（跨租户）只读组件资源指标端口。
// 未注册组件返回包装 ports.ErrNotFound 的错误（HTTP 404）。
type PlatformComponentMetricsReader interface {
	GetComponentMetrics(ctx context.Context, component string) (PlatformComponentMetrics, error)
}

// PlatformComponentLogEntry 单条组件日志（语义同 InstanceLogEntry，额外带 pod 维度）。
type PlatformComponentLogEntry struct {
	Timestamp time.Time
	Level     string
	Message   string
	Pod       string
	Container string
	Stream    string
}

// PlatformComponentLogQueryRequest 组件日志列表查询请求。
// Cursor 为 RFC3339 时间戳（上一页最早一条 timestamp），空时 end=now。
type PlatformComponentLogQueryRequest struct {
	Component string
	Level     string
	Limit     int
	Cursor    string
}

// PlatformComponentLogListResult 组件日志列表结果（backward 倒序）。
type PlatformComponentLogListResult struct {
	Items      []PlatformComponentLogEntry
	Total      int
	NextCursor string
	DevProfile DevProfileInfo
}

// PlatformComponentLogStreamRequest 组件日志流式请求。
type PlatformComponentLogStreamRequest struct {
	Component string
	Level     string
	// Limit 是首屏回放条数上限，0 表示 adapter 默认值（1000）。
	Limit int
	// IntervalSeconds 是增量轮询间隔；0 表示 adapter 默认值（2s）。
	IntervalSeconds int
}

// PlatformComponentLogReader 平台级只读组件日志端口（列表 + SSE 流式）。
// 未注册组件返回包装 ports.ErrNotFound 的错误。
type PlatformComponentLogReader interface {
	QueryComponentLogs(ctx context.Context, request PlatformComponentLogQueryRequest) (PlatformComponentLogListResult, error)
	// StreamComponentLogs 先回放最近 Limit 条历史日志（时间正序），再持续增量推送，
	// 每条通过 sink 回调写出；sink 返回 error 表示下游断开，实现必须立即退出；
	// ctx 取消（客户端断开）也应立即退出并返回 nil。
	StreamComponentLogs(ctx context.Context, request PlatformComponentLogStreamRequest, sink func(PlatformComponentLogEntry) error) error
}
