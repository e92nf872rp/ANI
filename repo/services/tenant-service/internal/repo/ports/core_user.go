package ports

import (
	"context"

	"github.com/google/uuid"
)

// Core 用户/角色最小 API 客户端端口。
// 封装 Core OpenAPI `/api/v1/admin/...` 租户用户能力（见 pkg/ports.UserAdminService）：
//
//	GET    /admin/tenants/{tenant_id}/user-lookup
//	GET    /admin/tenant-users
//	GET    /admin/tenants/{tenant_id}/users/{user_id}
//	PUT    /admin/tenants/{tenant_id}/users/{user_id}/role
//	GET    /admin/tenants/{tenant_id}/users/{user_id}/role
//	GET    /admin/tenants/{tenant_id}/users/{user_id}/changeable-roles
//	POST   /admin/tenants/{tenant_id}/transfer-ownership
//	POST   /admin/tenants/{tenant_id}/users/{user_id}/status
//	POST   /admin/tenants/{tenant_id}/users/{user_id}/reset-password
//	DELETE /admin/tenants/{tenant_id}/users/{user_id}
//
// tenant-service 不直接 SQL 操作 users / user_roles / roles。
// 实现：后续 issue 在 internal/repo/adapters/core 封装 Core Go SDK。

// UserSvcClient 定义通向 Core 用户 API 的调用客户端接口。
type UserSvcClient interface {
	// MatchUser 按租户 + email + username 匹配已有用户（Core lookupTenantUser）。
	// 无匹配 → ErrTenantAdminNotFound（邀请不新建用户）。
	MatchUser(ctx context.Context, tenantID uuid.UUID, email, username string) (uuid.UUID, error)

	// IsAlreadyAdmin 报告该用户在本租户是否已是 tenant-admin / tenant-owner。
	IsAlreadyAdmin(ctx context.Context, tenantID, userID uuid.UUID) (bool, error)

	// GetUser 查询租户内用户最小视图（角色/状态/来源等）。
	// 不存在或已软删除 → ErrTenantAdminNotFound。
	GetUser(ctx context.Context, tenantID, userID uuid.UUID) (AdminWithTenant, error)

	// GetAdminDetail 查询管理员详情（含 created_at/updated_at + tenant 对象）。
	// is_inviting 由 service 结合 TenantAdminStore.HasPendingInvitation 填充。
	GetAdminDetail(ctx context.Context, tenantID, userID uuid.UUID) (AdminWithTenant, error)

	// ListTenantAdmins 跨租户列出 owner/admin（邀请中用户由 service 与邀请表合并）。
	ListTenantAdmins(ctx context.Context, filter TenantAdminListFilter) (ListResult, error)

	// ChangeRole 修改租户内角色（Core updateTenantUserRole）。
	// 目标为 tenant-owner → ErrTenantOwnerRoleLocked；role 非法 → ErrRoleChangeInvalid。
	ChangeRole(ctx context.Context, tenantID, userID uuid.UUID, role string) error

	// GetRolePermissions 查询角色及 4 维 permissions。
	// 仅租户成员（tenant_id 非空）；平台账户不可查。
	GetRolePermissions(ctx context.Context, tenantID, userID uuid.UUID) (UserPermissions, error)

	// GetChangeableRoles 查询可变更角色选项（排除 tenant-owner）。
	// 当前为 tenant-owner 时返回空列表。
	GetChangeableRoles(ctx context.Context, tenantID, userID uuid.UUID) (ChangeableRoles, error)

	// TransferOwnership 原子交换：target→tenant-owner，原 owner→tenant-admin。
	// target 非本租户 active tenant-admin → ErrTransferTargetInvalid。
	TransferOwnership(ctx context.Context, tenantID, targetUserID uuid.UUID) error

	// SetStatus 更新 users.status（active | disabled）。
	// 禁用唯一活跃 tenant-owner → ErrLastTenantOwner。
	SetStatus(ctx context.Context, tenantID, userID uuid.UUID, status string) error

	// SoftDelete 软删除（is_deleted=TRUE, deleted_at=now(), status=disabled）。
	// 目标为 tenant-owner → ErrTenantOwnerRoleLocked；唯一活跃 owner → ErrLastTenantOwner。
	SoftDelete(ctx context.Context, tenantID, userID uuid.UUID) error

	// ResetPassword 更新 password_hash（明文不落日志/审计/响应）。
	// 禁用/已删 → ErrTenantAdminNotFound；与旧密码相同 → ErrPasswordSameAsOld。
	ResetPassword(ctx context.Context, tenantID, userID uuid.UUID, newPassword string) error
}
