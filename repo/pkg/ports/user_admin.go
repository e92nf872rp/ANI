package ports

import (
	"context"
	"time"
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
	Role        string // tenant-owner | tenant-admin | user | auditor
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
	Role     string // tenant-owner | tenant-admin; empty = both plus no extra roles
	Status   string // active | disabled; empty = all
	Search   string // fuzzy match email/username
}

// UserListResult is a cursor page of tenant users.
type UserListResult struct {
	Items      []User
	NextCursor string // empty = no more
}

// UserPermissions is the four-dimension tenant permission model.
// Platform accounts (tenant_id empty) are not queryable through UserAdminService.
type UserPermissions struct {
	UserID      string
	TenantID    string
	Role        string
	Permissions map[string]string // compute/inference/member/transfer → read/write/none
}

// ChangeableRoleOption is one selectable target role.
type ChangeableRoleOption struct {
	Role  string // user | auditor | tenant-admin
	Label string
}

// ChangeableRoles is GET .../changeable-roles.
// CurrentRole tenant-owner yields an empty Options list.
type ChangeableRoles struct {
	CurrentRole string
	Options     []ChangeableRoleOption
}

// UserAdminService manages tenant users and role bindings (users / user_roles / roles)
// under platform RLS bypass. Invitation lifecycle stays in Services.
//
// REST surface (Core OpenAPI, called by tenant-service UserSvcClient):
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
type UserAdminService interface {
	// LookupUser matches an existing tenant user by email AND username.
	// No match → ErrUserNotFound. Does not create users.
	LookupUser(ctx context.Context, tenantID, email, username string) (User, error)

	// IsTenantAdmin reports whether the user holds tenant-admin or tenant-owner
	// in this tenant. Missing user → ErrUserNotFound.
	IsTenantAdmin(ctx context.Context, tenantID, userID string) (bool, error)

	// GetUser returns one tenant member (no password_hash). Soft-deleted or
	// missing → ErrUserNotFound. Platform accounts are not returned.
	GetUser(ctx context.Context, tenantID, userID string) (User, error)

	// ListUsers returns tenant-owner / tenant-admin members (cursor page).
	// Role filter is optional; implementations must not return ordinary user
	// members unless a later caller merges invitation rows in Services.
	ListUsers(ctx context.Context, filter UserListFilter) (UserListResult, error)

	// ChangeRole replaces the tenant role (user / auditor / tenant-admin).
	// tenant-owner target → ErrTenantOwnerRoleLocked; illegal role → ErrRoleChangeInvalid.
	ChangeRole(ctx context.Context, tenantID, userID, role string) error

	// GetRolePermissions returns role + permissions JSONB for a tenant member.
	GetRolePermissions(ctx context.Context, tenantID, userID string) (UserPermissions, error)

	// GetChangeableRoles returns selectable roles excluding tenant-owner.
	GetChangeableRoles(ctx context.Context, tenantID, userID string) (ChangeableRoles, error)

	// TransferOwnership atomically promotes target to tenant-owner and demotes
	// the current owner to tenant-admin.
	// Invalid target → ErrTransferTargetInvalid.
	TransferOwnership(ctx context.Context, tenantID, targetUserID string) error

	// SetStatus sets users.status to active or disabled.
	// Disabling the last active tenant-owner → ErrLastTenantOwner.
	SetStatus(ctx context.Context, tenantID, userID, status string) error

	// SoftDelete marks the user deleted (and status=disabled).
	// tenant-owner → ErrTenantOwnerRoleLocked; last active owner → ErrLastTenantOwner.
	SoftDelete(ctx context.Context, tenantID, userID string) error

	// ResetPassword updates password_hash. Plaintext must not be logged or returned.
	// Disabled/deleted → ErrUserNotFound; same as old → ErrPasswordSameAsOld.
	ResetPassword(ctx context.Context, tenantID, userID, newPassword string) error
}
