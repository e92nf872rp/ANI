package ports

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// PlatformUserAdminStore manages platform accounts and role bindings
// (users / user_roles / roles，tenant_id IS NULL) under platform RLS bypass.
// Implementations live in pkg/adapters/runtime (PostgresPlatformUserAdminStore).
//
// REST surface (Core OpenAPI, called by platform-settings-service CorePlatformUserClient):
//
//	POST   /admin/platform-users
//	GET    /admin/platform-users
//	GET    /admin/platform-users/{userId}
//	PUT    /admin/platform-users/{userId}/role
//	POST   /admin/platform-users/{userId}/reset-password
//	POST   /admin/platform-users/{userId}/disable
//	POST   /admin/platform-users/{userId}/enable
//	DELETE /admin/platform-users/{userId}
//	GET    /admin/platform-users/roles
//	GET    /admin/platform-users/{userId}/permissions
type PlatformUserAdminStore interface {
	// Create inserts a platform account (tenant_id IS NULL) and binds a platform role.
	// passwordHash is pre-computed by the caller; Store does not hash.
	// Unknown / non-platform role → ErrRoleNotFound; username conflict → ErrUsernameAlreadyExists.
	// Email 允许重复（平台侧无 email 唯一约束，无 EMAIL_ALREADY_EXISTS）。
	Create(ctx context.Context, in PlatformUserCreate) (PlatformUserAdmin, error)

	// List returns cursor-paginated platform accounts (tenant_id IS NULL, is_deleted=FALSE).
	List(ctx context.Context, filter PlatformUserFilter) (PlatformUserListResult, error)

	// Get returns one platform account by ID (no password_hash).
	// Missing / soft-deleted → ErrPlatformUserNotFound.
	Get(ctx context.Context, userID uuid.UUID) (PlatformUserAdmin, error)

	// ChangeRole upserts the platform role binding for a platform account.
	// Existing platform role row → UPDATE role_id; otherwise INSERT.
	// Unknown / non-platform role_id → ErrRoleNotFound; illegal transition → ErrRoleChangeInvalid.
	// Demoting the last active platform-admin → ErrLastPlatformAdmin.
	ChangeRole(ctx context.Context, userID uuid.UUID, roleID uuid.UUID) error

	// ResetPassword validates the new password differs from the old, hashes, and updates password_hash.
	// Same as old → ErrPasswordSameAsOld; missing user → ErrPlatformUserNotFound.
	ResetPassword(ctx context.Context, userID uuid.UUID, newPassword string) error

	// SetStatus updates users.status (active/disabled) with last-admin protection.
	// Same status as current → ErrStatusUnchanged.
	// Disabling the last active platform-admin → ErrLastPlatformAdmin.
	SetStatus(ctx context.Context, userID uuid.UUID, status string) error

	// SoftDelete sets is_deleted=TRUE, deleted_at=now(), status=disabled.
	// Last active platform-admin → ErrLastPlatformAdmin.
	SoftDelete(ctx context.Context, userID uuid.UUID) error

	// CountActivePlatformAdmins counts active platform-admin rows, excluding excludeUserID.
	CountActivePlatformAdmins(ctx context.Context, excludeUserID uuid.UUID) (int, error)

	// ListPlatformRoles returns platform built-in roles (tenant_id IS NULL) from the roles table.
	ListPlatformRoles(ctx context.Context) ([]PlatformRole, error)

	// GetPlatformUserPermissions returns the bound platform role and permissions JSONB for one account.
	// Missing / soft-deleted / non-platform → ErrPlatformUserNotFound.
	GetPlatformUserPermissions(ctx context.Context, userID uuid.UUID) (PlatformUserPermissions, error)
}

// PlatformUserAdmin is the Core platform-admin view (never includes password_hash).
// Named ...Admin to avoid clashing with PlatformLoginStore's PlatformUser (login view).
//
// Username 为库内值（含 local:/oidc: 前缀）。TODO(list/detail): 对外列表/详情响应后续剥前缀。
type PlatformUserAdmin struct {
	ID          uuid.UUID
	Email       string
	Username    string
	DisplayName *string
	RoleID      uuid.UUID
	Role        string // platform-admin | platform-ops | platform-readonly
	Status      string // active | disabled
	Source      string // local | third_party | unknown（由 username 前缀推断）
	LastLoginAt *time.Time
	CreatedAt   time.Time
}

// PlatformUserCreate is the input for Create (password already bcrypt-hashed).
type PlatformUserCreate struct {
	Email        string
	Username     string // without prefix; Store prepends local:
	DisplayName  string
	RoleID       uuid.UUID // roles.id（tenant_id IS NULL 且 name LIKE 'platform-%'）
	PasswordHash string
}

// PlatformUserFilter is the cursor-page filter for List.
type PlatformUserFilter struct {
	Limit  int
	Cursor string
	RoleID uuid.UUID // uuid.Nil = 不按角色过滤
	Status string
	Source string // local | third_party (username prefix filter；third_party → oidc:)
	Search string // 对外 username ILIKE（剥 local:/oidc: 前缀后匹配）
}

// PlatformUserListResult is a cursor page of platform admins.
type PlatformUserListResult struct {
	Items      []PlatformUserAdmin
	NextCursor string // "" = no more
}

// PlatformRole is a platform built-in role; Permissions is roles.permissions JSONB as-is.
type PlatformRole struct {
	ID          uuid.UUID
	Name        string
	Permissions []map[string]any // resource/actions/scope entries (tenants / resource_pool / users / metering)
}

// PlatformUserPermissions is one platform account's bound role and permissions JSONB as-is.
type PlatformUserPermissions struct {
	UserID      uuid.UUID
	RoleID      uuid.UUID
	Role        string
	Permissions []map[string]any
}
