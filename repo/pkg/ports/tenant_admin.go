package ports

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// User is the Core tenant-user view used by platform admin flows.
// It never includes password_hash. is_inviting is a Services invitation marker
// and is not part of this Core model.
type User struct {
	ID          string
	TenantID    string // empty for platform accounts (not returned by this API)
	Email       string
	Username    string
	DisplayName *string
	Role        string // tenant-admin | user | auditor
	Status      string // active | disabled
	Source      string // local | third_party (inferred from username prefix)
	LastLoginAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Tenant      UserTenantRef
}

// UserTenantRef is the tenant summary embedded in user list/detail.
type UserTenantRef struct {
	ID          string
	Name        string
	DisplayName string
}

// UserListFilter is the cursor-page filter for ListUsers.
type UserListFilter struct {
	Limit    int
	Cursor   string
	TenantID string // empty = all tenants
	Role     string // tenant-admin; empty = all plus no extra roles
	Status   string // active | disabled; empty = all
	Search   string // fuzzy match email/username
}

// UserListResult is a cursor page of tenant users.
type UserListResult struct {
	Items      []User
	NextCursor string // empty = no more
}

// UserPermissions is the tenant permission model.
// Platform accounts (tenant_id empty) are not queryable through TenantAdminService.
type UserPermissions struct {
	UserID      string
	TenantID    string
	RoleID      string // roles.id；无角色绑定时为空
	Role        string
	Permissions []any // roles.permissions JSONB 原样
}

// RoleRef is an assignable role row for tenant role pickers.
type RoleRef struct {
	ID          uuid.UUID
	TenantID    *uuid.UUID // nil = 平台内置（roles.tenant_id IS NULL）
	Name        string
	Permissions []any // roles.permissions JSONB 原样
}

// TenantAdminService manages tenant users and role bindings (users / user_roles / roles)
// under platform RLS bypass. Invitation lifecycle stays in Services.
//
// REST surface (Core OpenAPI, called by tenant-service TenantAdminSvcClient):
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
type TenantAdminService interface {
	// LookupUser matches an existing tenant user by email AND username.
	// No match → ErrUserNotFound. Does not create users.
	LookupUser(ctx context.Context, tenantID, email, username string) (User, error)

	// IsTenantAdmin reports whether the user holds tenant-admin role
	// in this tenant. Missing user → ErrUserNotFound.
	IsTenantAdmin(ctx context.Context, tenantID, userID string) (bool, error)

	// GetUser returns one tenant member (no password_hash). Soft-deleted or
	// missing → ErrUserNotFound. Platform accounts are not returned.
	GetUser(ctx context.Context, tenantID, userID string) (User, error)

	// ListUsers returns tenant-admin members (cursor page).
	// Role filter is optional; implementations must not return ordinary user
	// members unless a later caller merges invitation rows in Services.
	ListUsers(ctx context.Context, filter UserListFilter) (UserListResult, error)

	// ChangeRole replaces the tenant role by role_id.
	// role_id must exist, not be platform-*, and have tenant_id NULL or equal to tenantID.
	// Illegal role → ErrRoleChangeInvalid.
	ChangeRole(ctx context.Context, tenantID, userID, roleID string) error

	// GetRolePermissions returns role + permissions JSONB for a tenant member.
	// Rejects platform-* roles and platform accounts (users.tenant_id empty).
	GetRolePermissions(ctx context.Context, tenantID, userID string) (UserPermissions, error)

	// ListAssignableRoles returns roles assignable to the tenant
	// (name NOT LIKE 'platform-%' AND (tenant_id IS NULL OR tenant_id = tenantID)).
	ListAssignableRoles(ctx context.Context, tenantID string) ([]RoleRef, error)

	// SetStatus sets users.status to active or disabled.
	SetStatus(ctx context.Context, tenantID, userID, status string) error

	// SoftDelete marks the user deleted (is_deleted + deleted_at; does not change status).
	SoftDelete(ctx context.Context, tenantID, userID string) error

	// ResetPassword updates password_hash. Plaintext must not be logged or returned.
	// Soft-deleted → ErrUserNotFound; same as old → ErrPasswordSameAsOld.
	// Disabled users may reset (status='disabled' is allowed).
	ResetPassword(ctx context.Context, tenantID, userID, newPassword string) error

	// BatchGetUsers 批量查询租户内用户（按 user_id 列表）。不存在的用户跳过。
	BatchGetUsers(ctx context.Context, tenantID string, userIDs []string) ([]User, error)
}

// UserAdminService is kept as an alias for backward compatibility.
// Prefer TenantAdminService in new code.
type UserAdminService = TenantAdminService
