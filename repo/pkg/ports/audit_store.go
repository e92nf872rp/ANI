package ports

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// 审计日志端口（ports）定义。
// 复用现有 audit_logs 分区表。平台级操作（tenant_id 为 NULL）与租户级操作
// 共用一个表；通过 resource + details 进行归属区分。
//
// 字段与既有 audit_logs 表约定保持一致：
//   - result 枚举为 success / failure
//   - 平台级操作 tenant_id 为 NULL
//   - 套餐相关记录使用 resource='tenant_plan'，并以 details->>'plan_id' 关联套餐

// AuditLog 表示一条审计日志记录（对应 audit_logs 表一行）。
type AuditLog struct {
	ID        uuid.UUID
	TenantID  *uuid.UUID // 平台级操作（如套餐管理）为 NULL
	UserID    *uuid.UUID // 操作者；系统/后台触发可为 NULL
	RequestID *uuid.UUID // 关联请求（幂等/追踪）
	Action    string     // 操作类型，如 plan.create / plan.activate / tenant.bind_plan_quota
	Resource  string     // 资源类型，如 tenant_plan
	Result    string     // success | failure
	Details   map[string]any // 扩展信息，如 {plan_id, skipped_approved, updated}
	IPAddress string     // 来源 IP
	UserAgent string     // UA
	CreatedAt time.Time
}

// AuditLogFilter 是审计日志查询的过滤条件（游标分页）。
type AuditLogFilter struct {
	Resource string // 资源类型过滤
	Action   string // 操作类型过滤
	Result   string // "" = 全部；否则 success | failure
	Limit    int    // 每页数量，default 20，max 100
	Cursor   string // 上一页返回的 next_cursor；空串 = 第一页
}

// AuditLogListResult 是审计日志查询的返回（游标分页）。
// 风格与项目内其他分页结果一致（Items + Total + NextCursor，具体类型、不使用泛型）。
type AuditLogListResult struct {
	Items      []AuditLog // 本页数据
	Total      int        // 满足过滤条件的总条数
	NextCursor string     // 下一页游标；空串 = 已无更多数据
}

// AuditStore 定义审计日志的数据访问接口。
// 实现：repo/pkg/adapters/postgres/audit_store.go。
type AuditStore interface {
	// Create 写入一条审计日志并返回其 ID。
	Create(ctx context.Context, log AuditLog) (uuid.UUID, error)

	// ListByResource 按资源标记（如 plan_id）与过滤条件，游标分页查询审计日志。
	// 用于 GET /tenant-plans/{planId}/audit-logs 等操作历史查询。
	ListByResource(ctx context.Context, resourceID string, filter AuditLogFilter) (AuditLogListResult, error)
}
