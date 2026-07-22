package ports

import "context"

// LogQueryRequest 是日志持久化查询的入参，port 层不绑定存储后端语义。
//
// Cursor 为 opaque string，port 层不约束其内部语义，由 adapter 内部映射为具体存储的游标
// （Loki time / ES search_after / K8s tailLines 等）。空字符串表示从头开始查询。
type LogQueryRequest struct {
	// TenantID 租户 ID，用于多租户隔离。
	TenantID string
	// InstanceID 实例 ID，对应 record.Name（也是 pod name / VMI name）。
	InstanceID string
	// Namespace 租户 namespace，格式 ani-tenant-<tenant_id>。
	Namespace string
	// Limit 单页条数上限。
	Limit int
	// Cursor opaque string，空字符串表示从头开始。
	Cursor string
	// Level 日志级别过滤（info/warn/error/空表示全部），adapter 可选实现。
	Level string
}

// LogQueryResult 是日志持久化查询的出参。
//
// NextCursor 为空表示已到末尾；非空表示下一页起点，对调用方透明。
// Items 复用 InstanceLogEntry（与现有 InstanceObservability.ListLogs 返回类型一致）。
type LogQueryResult struct {
	Items      []InstanceLogEntry
	NextCursor string
}

// LogStore 是日志持久化存储的 port 抽象。
//
// 实现方示例：LokiLogStore（推荐）、ElasticsearchLogStore（后续 PRD）、
// K8s API fallback（不走此 port）。
//
// Cursor 语义由 adapter 自定义，port 层不约束。
// LogStore 是内部组合能力，不对外暴露到 InstanceObservability interface，
// 由 adapter 持有可选字段并决定 fallback 行为。
type LogStore interface {
	// QueryLogs 按租户/实例维度查询持久化日志，支持 cursor 分页。
	QueryLogs(ctx context.Context, req LogQueryRequest) (LogQueryResult, error)
}
