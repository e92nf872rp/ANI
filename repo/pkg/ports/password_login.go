package ports

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// PasswordUser represents a user record looked up for tenant password login.
// It is a shared type used by PasswordLoginStore implementations.
// 用于租户密码登录的用户记录
type PasswordUser struct {
	ID           uuid.UUID
	PasswordHash string
	Status       string
}

// PasswordLoginStore defines the data access interface for tenant password login.
// Implementations must resolve tenant names to IDs, look up users with their
// bcrypt password hashes, load role bindings, persist refresh tokens, and
// touch last-login timestamps — all within the tenant's security boundary.
type PasswordLoginStore interface {
	// LookupTenant resolves an active tenant name to its UUID. 根据租名称查询租ID
	LookupTenant(ctx context.Context, tenantName string) (uuid.UUID, error)

	// LookupUser returns a user row by tenant and namespaced username.
	// The caller is responsible for prepending the appropriate namespace
	// prefix (e.g. "local:") before calling this method. 根据租户ID和命名空间用户名查询用户
	LookupUser(ctx context.Context, tenantID uuid.UUID, namespacedUsername string) (PasswordUser, error)

	// LoadRoles returns the role names bound to the given user. 根据用户ID查询用户角色
	LoadRoles(ctx context.Context, userID uuid.UUID) ([]string, error)

	// InsertRefreshToken persists a hashed refresh token for the user. 插入用户刷新令牌
	InsertRefreshToken(ctx context.Context, tenantID, userID uuid.UUID, tokenHash string, roles []string, expiresAt time.Time) error

	// TouchLastLogin updates the user's last_login_at timestamp. 更新用户最后登录时间
	TouchLastLogin(ctx context.Context, userID uuid.UUID, at time.Time) error
}

// PlatformUser represents a platform admin user looked up for platform password login.
// Platform admins are stored in the users table and distinguished by a
// platform-admin role binding (roles.tenant_id IS NULL).
// 用于平台管理员密码登录的用户记录
type PlatformUser struct {
	ID           uuid.UUID
	PasswordHash string
	Status       string
}

// PlatformLoginStore defines the data access interface for platform admin password login.
// Unlike tenant login, platform login has no tenant scope; users are identified
// by username alone and roles are scoped to platform built-in roles
// (roles.tenant_id IS NULL).
type PlatformLoginStore interface {
	// LookupUser returns a platform admin user by namespaced username.
	// The caller is responsible for prepending the appropriate namespace
	// prefix (e.g. "local:") before calling this method.
	// The implementation must ensure only users with a platform-admin role
	// binding (EXISTS user_roles JOIN roles WHERE roles.name='platform-admin'
	// AND roles.tenant_id IS NULL) are returned. 根据命名空间用户名查询平台管理员用户
	LookupUser(ctx context.Context, namespacedUsername string) (PlatformUser, error)

	// LoadRoles returns role names for the platform admin user.
	// Implementations should filter to roles WHERE tenant_id IS NULL. 根据用户ID查询平台管理员用户角色
	LoadRoles(ctx context.Context, userID uuid.UUID) ([]string, error)

	// InsertRefreshToken persists a hashed refresh token for the platform user.
	// tenant_id is deliberately omitted — platform tokens have no tenant scope. 插入平台管理员用户刷新令牌
	InsertRefreshToken(ctx context.Context, userID uuid.UUID, tokenHash string, roles []string, expiresAt time.Time) error

	// TouchLastLogin updates the platform user's last_login_at timestamp. 更新平台管理员用户最后登录时间
	TouchLastLogin(ctx context.Context, userID uuid.UUID, at time.Time) error
}
