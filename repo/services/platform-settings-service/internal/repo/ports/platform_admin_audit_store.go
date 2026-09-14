package ports

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// 平台运营账号域审计日志端口。
// 复用现有 audit_logs 分区表；平台级操作 tenant_id 为 NULL。
//
// 字段约定与 tenant-service TenantPlanAuditStore 对齐：
//   - result 枚举为 success / failure
//   - resource = 'platform_user'
//   - action 形如 platform_admin.create / change_role / reset_password / disable / enable / delete
//   - 目标账号以 details->>'target_id' 关联；user_id 为操作者（网关透传）

// AuditLog 表示一条审计日志记录（对应 audit_logs 表一行）。
type AuditLog struct {
	ID        uuid.UUID
	TenantID  *uuid.UUID     // 平台级操作固定为 NULL
	UserID    *uuid.UUID     // 操作者；系统/后台触发可为 NULL
	RequestID string         // 网关透传的请求 ID；空则 store 侧生成
	Action    string         // 如 platform_admin.create
	Resource  string         // 固定 platform_user
	Result    string         // success | failure
	Details   map[string]any // 如 {target_id, role, old_role, new_role}
	IPAddress string
	UserAgent string
	CreatedAt time.Time
}

// AuditLogFilter 是审计日志查询的过滤条件（游标分页）。
type AuditLogFilter struct {
	Limit  int    // 每页数量，default 20，max 100
	Cursor string // 上一页 next_cursor；空串 = 第一页
	Action string // 可选：精确匹配 action
	Result string // 可选：success | failure
}

// AuditLogListResult 是审计日志查询返回（游标分页）。
type AuditLogListResult struct {
	Items      []AuditLog
	Total      int
	NextCursor string // 空串 = 已无更多
}

// PlatformAdminAuditStore 定义【平台运营账号域】的审计日志数据访问接口。
// 实现：internal/repo/adapters/postgres/platform_admin_audit_store.go。
// 仅覆盖 resource='platform_user'；不操作 users/roles/user_roles。
type PlatformAdminAuditStore interface {
	// Create 写入一条平台账号域审计日志并返回其 ID。
	// 调用方（service 层 writeAudit*）负责构造完整 AuditLog。
	Create(ctx context.Context, log AuditLog) (uuid.UUID, error)

	// ListUserAuditLogs 按目标账号（details->>'target_id' = userID）查询操作历史，
	// 且 tenant_id IS NULL、resource='platform_user'。用于 GET .../audit-logs。
	ListUserAuditLogs(ctx context.Context, userID uuid.UUID, filter AuditLogFilter) (AuditLogListResult, error)
}
