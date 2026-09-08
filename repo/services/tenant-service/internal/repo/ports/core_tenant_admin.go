package ports

import (
	"context"

	"github.com/google/uuid"
)

// Core 租户管理员用户/角色最小 API 客户端端口。
// 封装 Core OpenAPI `/api/v1/admin/...` 租户管理员能力（见 pkg/ports.TenantAdminService）：
//
//	GET    /admin/tenants/{tenant_id}/user-lookup
//	GET    /admin/tenant-users
//	GET    /admin/tenants/{tenant_id}/users/{user_id}
//	PUT    /admin/tenants/{tenant_id}/users/{user_id}/role
//	GET    /admin/tenants/{tenant_id}/users/{user_id}/role
//	GET    /admin/tenants/{tenant_id}/roles
//	POST   /admin/tenants/{tenant_id}/users/{user_id}/status
//	POST   /admin/tenants/{tenant_id}/users/{user_id}/reset-password
//	DELETE /admin/tenants/{tenant_id}/users/{user_id}
//
// tenant-service 不直接 SQL 操作 users / user_roles / roles。
// 实现：internal/repo/adapters/core/tenant_admin_svc_client.go。

// TenantAdminSvcClient 定义通向 Core 租户管理员 API 的调用客户端接口。
type TenantAdminSvcClient interface {
	// MatchUser 按租户 + email + username 匹配已有用户（Core lookupTenantUser）。
	// 无匹配 → ErrTenantAdminNotFound（邀请不新建用户）。
	MatchUser(ctx context.Context, tenantID uuid.UUID, email, username string) (uuid.UUID, error)

	// IsAlreadyAdmin 报告该用户在本租户是否已是 tenant-admin。
	IsAlreadyAdmin(ctx context.Context, tenantID, userID uuid.UUID) (bool, error)

	// GetUser 查询租户内用户最小视图（角色/状态/来源等）。
	// 不存在或已软删除 → ErrTenantAdminNotFound。
	GetUser(ctx context.Context, tenantID, userID uuid.UUID) (AdminWithTenant, error)

	// BatchGetUsers 批量查询租户内用户（Core GET /admin/tenants/{id}/users/batch）。
	// 不存在的用户跳过；返回 map key 为 user_id。
	BatchGetUsers(ctx context.Context, tenantID uuid.UUID, userIDs []uuid.UUID) (map[uuid.UUID]AdminWithTenant, error)

	// GetAdminDetail 查询管理员详情（含 created_at/updated_at + tenant 对象）。
	// is_inviting / is_expired 由 service 结合 TenantAdminStore 邀请状态填充。
	GetAdminDetail(ctx context.Context, tenantID, userID uuid.UUID) (AdminWithTenant, error)

	// ListTenantAdmins 跨租户列出 admin（邀请中/已过期用户由 service 与邀请表合并）。
	ListTenantAdmins(ctx context.Context, filter TenantAdminListFilter) (ListResult, error)

	// ChangeRole 修改租户内角色（Core updateTenantUserRole，按 role_id upsert）。
	// role_id 非法 → ErrRoleChangeInvalid。
	ChangeRole(ctx context.Context, tenantID, userID, roleID uuid.UUID) error

	// GetRolePermissions 查询角色及 permissions（roles.permissions JSONB 原样）。
	// 仅租户成员（tenant_id 非空）；平台账户或 platform-* 角色不可查。
	GetRolePermissions(ctx context.Context, tenantID, userID uuid.UUID) (UserPermissions, error)

	// ListAssignableRoles 查询租户可分配角色（Core GET /admin/tenants/{id}/roles）。
	ListAssignableRoles(ctx context.Context, tenantID uuid.UUID) ([]AssignableRole, error)

	// SetStatus 更新 users.status（active | disabled）。
	SetStatus(ctx context.Context, tenantID, userID uuid.UUID, status string) error

	// SoftDelete 软删除（is_deleted=TRUE, deleted_at=now()；不改 users.status）。
	SoftDelete(ctx context.Context, tenantID, userID uuid.UUID) error

	// ResetPassword 更新 password_hash（明文不落日志/审计/响应）。
	// 已软删除 → ErrTenantAdminNotFound；与旧密码相同 → ErrPasswordSameAsOld。
	ResetPassword(ctx context.Context, tenantID, userID uuid.UUID, newPassword string) error
}

// UserSvcClient is kept as an alias for backward compatibility.
// Prefer TenantAdminSvcClient in new code.
type UserSvcClient = TenantAdminSvcClient
