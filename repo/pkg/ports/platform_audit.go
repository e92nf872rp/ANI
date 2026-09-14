package ports

import (
	"context"
	"errors"
	"time"
)

// 平台审计日志（BOSS「平台审计且合规 → 平台审计日志」）只读检索端口。
//
// 数据源为 kube-apiserver 审计日志（Metadata 级，仅 write verb），经 fluent-bit
// 采集进 Loki 的独立 stream="kubernetes-audit"；查询从 Loki 返回给前端，
// 操作者为 K8s 原生身份（system:serviceaccount:* / system:masters 等），
// 不映射 ANI 用户，也不含 gateway 业务责任人台账（audit.go TODO 专项内容）。
var (
	// ErrPlatformAuditInvalid 表示平台审计查询请求参数非法。
	ErrPlatformAuditInvalid = errors.New("platform audit: invalid request")
)

// PlatformAuditLogQuery 平台审计日志查询请求。
// TimeFrom/TimeTo 必须成对且 TimeFrom<=TimeTo（handler 校验）；after 为游标
// （上一页末条 timestamp，RFC3339）；分页基于 (timestamp,auditID) 全局倒序合并，
// 不依赖 offset，天然去重。
type PlatformAuditLogQuery struct {
	// TimeFrom 查询窗口起点（RFC3339 UTC），可空表示不限起点。
	TimeFrom *time.Time
	// TimeTo 查询窗口终点（RFC3339 UTC），可空表示当前时间。
	TimeTo *time.Time
	// User 按操作者过滤（audit_user 标签），可空。
	User string
	// Verb 按动作过滤（audit_verb 标签：create/update/patch/delete），可空。
	Verb string
	// ResourceType 按资源类型过滤（audit_resource 标签，K8s resource 复数），可空。
	ResourceType string
	// Namespace 按命名空间过滤（audit_namespace 标签），可空。
	Namespace string
	// After 上游游标（上一页末条 timestamp，RFC3339）。
	After string
	// PageSize 每页条数（默认 20，上限 100，超限钳制）。
	PageSize int
	// Keyword 可选全文检索（|~），限时间窗使用，谨慎。
	Keyword string
}

// PlatformAuditUser K8s 原生操作者身份。
type PlatformAuditUser struct {
	Username string
	Groups   []string
}

// PlatformAuditResource 被操作的 K8s 资源引用。
type PlatformAuditResource struct {
	Namespace string
	Resource  string
	Name      string
}

// PlatformAuditDetail 审计详情（已脱敏：不含请求体，敏感 query 已置 ***）。
type PlatformAuditDetail struct {
	RequestURI string
	UserAgent  string
}

// PlatformAuditLogItem 单条 k8s 控制面审计记录（Metadata 级）。
type PlatformAuditLogItem struct {
	AuditID      string
	Timestamp    time.Time
	Verb         string
	User         PlatformAuditUser
	Resource     PlatformAuditResource
	ResponseCode int64
	Detail       PlatformAuditDetail
}

// PlatformAuditLogResult 平台审计日志查询响应。
type PlatformAuditLogResult struct {
	// Items 按 timestamp(+auditID) 全局倒序的一页数据。
	Items []PlatformAuditLogItem
	// NextAfter 下一页游标（末条 timestamp，RFC3339）；空表示已无更多。
	NextAfter string
	// TotalApprox 近似总量（count_over_time 按 step 聚合），非精确值。
	TotalApprox int64
	// DevProfile 数据源 profile（real adapter 为 real + real_provider=true）。
	DevProfile DevProfileInfo
}

// PlatformAuditService 平台级（跨租户）只读审计检索能力端口。
type PlatformAuditService interface {
	QueryAuditLogs(ctx context.Context, query PlatformAuditLogQuery) (PlatformAuditLogResult, error)
}
