package ports

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// 租户管理员邀请与审计的端口（ports）定义。
// 本文件只声明接口与领域结构体，不含实现。
// Postgres 适配器仅操作 tenant_admin_invitation / audit_logs；
// users / user_roles / roles 一律经 TenantAdminSvcClient（见 core_tenant_admin.go）。

// =============================================================================
// 枚举常量
// =============================================================================

const (
	InvitationStatusInviting = "inviting"
	InvitationStatusAccepted = "accepted"
	InvitationStatusRejected = "rejected"
	InvitationStatusExpired  = "expired"

	TenantAdminRoleAdmin   = "tenant-admin"
	TenantAdminRoleUser    = "user"
	TenantAdminRoleAuditor = "auditor"

	TenantAdminUserStatusActive   = "active"
	TenantAdminUserStatusDisabled = "disabled"

	TenantAdminSourceLocal      = "local"
	TenantAdminSourceThirdParty = "third_party"
)

// =============================================================================
// 实体与 DTO（对齐 SPEC §3.2 / §4.2）
// =============================================================================

// TenantAdminInvitation 表示一条邀请记录（对应 tenant_admin_invitation 表一行）。
// 库中只存 TokenHash=SHA-256(token)，明文 token 仅一次性返回。
type TenantAdminInvitation struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	UserID     uuid.UUID
	TokenHash  string // SHA-256 hex
	Status     string // inviting | accepted | rejected | expired
	ExpireAt   time.Time
	CreatedAt  time.Time
	AcceptedAt *time.Time
	RejectedAt *time.Time
}

// TenantRef 是管理员所属租户的摘要（列表/详情内嵌对象）。
type TenantRef struct {
	ID          uuid.UUID
	Name        string
	DisplayName string
}

// AdminWithTenant 是跨租户列表项 / 详情视图。
// 不含 password_hash、无顶层 tenant_id 冗余。
// CreatedAt / UpdatedAt 仅详情填充；列表保持 nil。
type AdminWithTenant struct {
	ID          uuid.UUID
	Email       string
	Username    string
	DisplayName *string
	Role        string // tenant-admin | user | auditor（列表仅含 admin/邀请中/已过期）
	Status      string // active | disabled
	IsInviting  bool   // true = 最新邀请 status='inviting'；仅作标记
	IsExpired   bool   // true = 最新邀请 status='expired'；仅作标记
	Source      string // third_party | local
	LastLoginAt *time.Time
	CreatedAt   *time.Time // 详情返回
	UpdatedAt   *time.Time // 详情返回
	Tenant      TenantRef
}

// TenantAdminListFilter 是跨租户管理员列表的过滤条件（游标分页）。
type TenantAdminListFilter struct {
	Limit      int
	Cursor     string
	TenantID   *uuid.UUID
	Status     string
	IsInviting *bool
	IsExpired  *bool
	Search     string
}

// InvitationFlag 是 (tenant_id, user_id) 上的邀请中/已过期标记。
type InvitationFlag struct {
	TenantID   uuid.UUID
	UserID     uuid.UUID
	IsInviting bool
	IsExpired  bool
}

// ListResult 是跨租户管理员列表返回（游标分页；具体类型、不用泛型）。
type ListResult struct {
	Items      []AdminWithTenant
	NextCursor string // "" = 无更多
}

// InviteInput 是邀请入参（service 层；store 只接收已生成 token_hash 的 Invitation）。
type InviteInput struct {
	TenantID uuid.UUID
	Email    string
	Username string
}

// InvitationResult 是邀请/重发的一次性返回；Token 明文仅本次出现。
type InvitationResult struct {
	ID       uuid.UUID
	Token    string // 原始 token，仅本次返回
	ExpireAt time.Time
	Message  string
}

// UserPermissions 是指定管理员的权限模型。
// 仅返回租户成员（TenantID 非空）；平台账户不可经本模块查询。
type UserPermissions struct {
	UserID      uuid.UUID
	TenantID    *uuid.UUID
	RoleID      uuid.UUID // 无绑定时为零值
	Role        string
	Permissions []any // roles.permissions JSONB 原样
}

// ChangeableRoleOption 是可变更角色下拉的一项。
type ChangeableRoleOption struct {
	Role  string // user | auditor | tenant-admin
	Label string
}

// ChangeableRoles 是 GET .../changeable-roles 的返回。
type ChangeableRoles struct {
	CurrentRole     string
	ChangeableRoles []ChangeableRoleOption
}

// AssignableRole 是 GET /tenants/{tenantId}/roles 的一项。
type AssignableRole struct {
	ID          uuid.UUID
	TenantID    *uuid.UUID // nil = 平台内置
	Name        string
	Permissions []any // roles.permissions JSONB 原样
}

// AuditLogListItem 是租户管理员操作历史列表项。
type AuditLogListItem struct {
	ID        uuid.UUID
	Action    string
	Resource  string
	Result    string // success | failure
	UserID    *uuid.UUID
	Details   map[string]any
	CreatedAt time.Time
}

// TenantAdminAuditLogFilter 是操作历史查询过滤（游标分页）。
type TenantAdminAuditLogFilter struct {
	Limit  int
	Cursor string
	Action string
	Result string // success | failure
}

// TenantAdminAuditLogListResult 是操作历史查询返回。
type TenantAdminAuditLogListResult struct {
	Items      []AuditLogListItem
	NextCursor string // "" = 无更多
}

// =============================================================================
// Store 接口（仅 invitation / audit_logs）
// =============================================================================

// TenantAdminStore 定义租户管理员模块对本地表的数据访问。
// 实现：internal/repo/adapters/postgres/tenant_admin_store.go。
//
// 禁止直接 SQL 操作 users / user_roles / roles；这些走 TenantAdminSvcClient。
type TenantAdminStore interface {
	// HasPendingInvitation 报告该租户下该用户是否存在 status='inviting' 的邀请。
	HasPendingInvitation(ctx context.Context, tenantID, userID uuid.UUID) (bool, error)

	// InsertInvitation 写入邀请（status='inviting'，token_hash，expire_at）。
	// 实现须在事务内：先查该租户+用户最新一条；若为 expired 则原地更新 token/过期时间并回归 inviting，否则 INSERT。
	// 不改 users.status、不预绑角色。token_hash 冲突 → 实现侧映射领域错误。
	InsertInvitation(ctx context.Context, inv TenantAdminInvitation) (TenantAdminInvitation, error)

	// GetLatestInvitation 按 tenant_id+user_id 取最新一条邀请。
	// 无记录 → ErrTenantAdminInvitationNotFound。
	GetLatestInvitation(ctx context.Context, tenantID, userID uuid.UUID) (TenantAdminInvitation, error)

	// UpdateInvitation 更新邀请（重发：新 token_hash / expire_at / status='inviting'，清空 accepted_at/rejected_at）。
	UpdateInvitation(ctx context.Context, inv TenantAdminInvitation) (TenantAdminInvitation, error)

	// ListInvitationFlags 返回 inviting/expired 标记；读取前先将「最新邀请仍为 inviting 且 expire_at < now」落库为 expired。
	// tenantID 非空时按租户过滤；statusFilter 为 inviting|expired 时仅返回带该标记的行；空串返回两者。
	ListInvitationFlags(ctx context.Context, tenantID *uuid.UUID, statusFilter string) ([]InvitationFlag, error)

	// GetInvitationFlags 按最新邀请设置标记（inviting/expired 互斥；其余/无记录两者 false）。
	// 读取后若最新为 inviting 且 expire_at < now，先写回 expired 再返回。
	GetInvitationFlags(ctx context.Context, tenantID, userID uuid.UUID) (InvitationFlag, error)

	// ListAuditLogs 按 tenant_id + 目标 user_id 查询操作历史，游标分页。
	ListAuditLogs(ctx context.Context, tenantID, userID uuid.UUID, filter TenantAdminAuditLogFilter) (TenantAdminAuditLogListResult, error)
}
