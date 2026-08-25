package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/kubercloud/ani/pkg/ports"
)

const (
	tenantAdminRoleName = "tenant-admin"
	userSourceLocal     = "local"
	userSourceOIDC      = "third_party"
)

// PostgresTenantAdmin is the PostgreSQL adapter for ports.TenantAdminService.
// Invitation state is not handled here (Services tenant_admin_invitation).
type PostgresTenantAdmin struct {
	store ports.MetadataStore
}

var _ ports.TenantAdminService = (*PostgresTenantAdmin)(nil)

// NewPostgresTenantAdmin constructs a TenantAdminService backed by MetadataStore
// (platform tx / RLS bypass).
func NewPostgresTenantAdmin(store ports.MetadataStore) *PostgresTenantAdmin {
	return &PostgresTenantAdmin{store: store}
}

// LookupUser 按 tenant + email + username 匹配未软删除用户（不新建）。
func (u *PostgresTenantAdmin) LookupUser(ctx context.Context, tenantID, email, username string) (ports.User, error) {
	// 步骤 1：校验入参
	tid, err := parseAdminUUID(tenantID, "tenant_id")
	if err != nil {
		return ports.User{}, err
	}
	email = strings.TrimSpace(email)
	username = strings.TrimSpace(username)
	if email == "" {
		return ports.User{}, fmt.Errorf("%w: email required", ports.ErrInvalid)
	}
	if username == "" {
		return ports.User{}, fmt.Errorf("%w: username required", ports.ErrInvalid)
	}

	// 步骤 2：平台事务内按 email+username 精确匹配
	var out ports.User
	err = u.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		user, scanErr := scanTenantAdminUser(ctx, tx, `
			SELECT u.id, u.tenant_id, u.email, u.username, u.display_name, u.status,
			       COALESCE(r.name, '') AS role,
			       u.last_login_at, u.created_at, u.updated_at,
			       t.id, t.name, t.display_name
			FROM users u
			JOIN tenants t ON t.id = u.tenant_id
			LEFT JOIN LATERAL (
				SELECT r2.name
				FROM user_roles ur
				JOIN roles r2 ON r2.id = ur.role_id
				WHERE ur.user_id = u.id
				ORDER BY CASE WHEN r2.name = $4 THEN 0 ELSE 1 END, r2.name
				LIMIT 1
			) r ON TRUE
			WHERE u.tenant_id = $1
			  AND lower(u.email) = lower($2)
			  AND u.username = $3
			  AND COALESCE(u.is_deleted, FALSE) = FALSE
		`, tid, email, username, tenantAdminRoleName)
		if scanErr != nil {
			return scanErr
		}
		out = user
		return nil
	})
	if err != nil {
		return ports.User{}, err
	}
	return out, nil
}

// IsTenantAdmin 报告该用户是否持有 tenant-admin 角色；用户不存在 → ErrUserNotFound。
// 平台角色（name 以 platform- 开头）一律视为非租户管理员。
func (u *PostgresTenantAdmin) IsTenantAdmin(ctx context.Context, tenantID, userID string) (bool, error) {
	// 步骤 1：复用 GetUser 校验存在性并读取角色
	user, err := u.GetUser(ctx, tenantID, userID)
	if err != nil {
		return false, err
	}
	// 步骤 2：判断是否为 tenant-admin
	return user.Role == tenantAdminRoleName, nil
}

// GetUser 返回租户内未软删除成员详情（不含 password_hash）。
func (u *PostgresTenantAdmin) GetUser(ctx context.Context, tenantID, userID string) (ports.User, error) {
	// 步骤 1：校验 UUID
	tid, err := parseAdminUUID(tenantID, "tenant_id")
	if err != nil {
		return ports.User{}, err
	}
	uid, err := parseAdminUUID(userID, "user_id")
	if err != nil {
		return ports.User{}, err
	}

	// 步骤 2：平台事务内按 id 查询
	var out ports.User
	err = u.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		user, scanErr := scanTenantAdminUser(ctx, tx, `
			SELECT u.id, u.tenant_id, u.email, u.username, u.display_name, u.status,
			       COALESCE(r.name, '') AS role,
			       u.last_login_at, u.created_at, u.updated_at,
			       t.id, t.name, t.display_name
			FROM users u
			JOIN tenants t ON t.id = u.tenant_id
			LEFT JOIN LATERAL (
				SELECT r2.name
				FROM user_roles ur
				JOIN roles r2 ON r2.id = ur.role_id
				WHERE ur.user_id = u.id
				ORDER BY CASE WHEN r2.name = $3 THEN 0 ELSE 1 END, r2.name
				LIMIT 1
			) r ON TRUE
			WHERE u.tenant_id = $1
			  AND u.id = $2
			  AND COALESCE(u.is_deleted, FALSE) = FALSE
		`, tid, uid, tenantAdminRoleName)
		if scanErr != nil {
			return scanErr
		}
		out = user
		return nil
	})
	if err != nil {
		return ports.User{}, err
	}
	return out, nil
}

func (u *PostgresTenantAdmin) ListUsers(ctx context.Context, filter ports.UserListFilter) (ports.UserListResult, error) {
	_ = ctx
	_ = filter
	return ports.UserListResult{}, ports.ErrUnsupported
}

func (u *PostgresTenantAdmin) ChangeRole(ctx context.Context, tenantID, userID, role string) error {
	_ = ctx
	_ = tenantID
	_ = userID
	_ = role
	return ports.ErrUnsupported
}

func (u *PostgresTenantAdmin) GetRolePermissions(ctx context.Context, tenantID, userID string) (ports.UserPermissions, error) {
	_ = ctx
	_ = tenantID
	_ = userID
	return ports.UserPermissions{}, ports.ErrUnsupported
}

func (u *PostgresTenantAdmin) GetChangeableRoles(ctx context.Context, tenantID, userID string) (ports.ChangeableRoles, error) {
	_ = ctx
	_ = tenantID
	_ = userID
	return ports.ChangeableRoles{}, ports.ErrUnsupported
}

func (u *PostgresTenantAdmin) SetStatus(ctx context.Context, tenantID, userID, status string) error {
	_ = ctx
	_ = tenantID
	_ = userID
	_ = status
	return ports.ErrUnsupported
}

func (u *PostgresTenantAdmin) SoftDelete(ctx context.Context, tenantID, userID string) error {
	_ = ctx
	_ = tenantID
	_ = userID
	return ports.ErrUnsupported
}

func (u *PostgresTenantAdmin) ResetPassword(ctx context.Context, tenantID, userID, newPassword string) error {
	_ = ctx
	_ = tenantID
	_ = userID
	_ = newPassword
	return ports.ErrUnsupported
}

// scanTenantAdminUser 执行 QueryRow 并映射为 ports.User；无行 → ErrUserNotFound。
func scanTenantAdminUser(ctx context.Context, tx ports.MetadataTx, query string, args ...any) (ports.User, error) {
	var (
		id            uuid.UUID
		tenantUUID    uuid.UUID
		email         string
		username      string
		displayName   *string
		status        string
		role          string
		lastLoginAt   *time.Time
		createdAt     time.Time
		updatedAt     time.Time
		tenantID      uuid.UUID
		tenantName    string
		tenantDisplay string
	)
	err := tx.QueryRow(ctx, query, args...).Scan(
		&id, &tenantUUID, &email, &username, &displayName, &status, &role,
		&lastLoginAt, &createdAt, &updatedAt,
		&tenantID, &tenantName, &tenantDisplay,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.User{}, ports.ErrUserNotFound
	}
	if err != nil {
		return ports.User{}, fmt.Errorf("scan tenant admin user: %w", err)
	}
	return ports.User{
		ID:          id.String(),
		TenantID:    tenantUUID.String(),
		Email:       email,
		Username:    username,
		DisplayName: displayName,
		Role:        role,
		Status:      status,
		Source:      inferTenantUserSource(username),
		LastLoginAt: lastLoginAt,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
		Tenant: ports.UserTenantRef{
			ID:          tenantID.String(),
			Name:        tenantName,
			DisplayName: tenantDisplay,
		},
	}, nil
}

// inferTenantUserSource 按 username 前缀推断来源（SPEC：oidc: → third_party，其余 local）。
func inferTenantUserSource(username string) string {
	if strings.HasPrefix(username, "oidc:") {
		return userSourceOIDC
	}
	return userSourceLocal
}

// PostgresUserAdmin is kept as a compatibility alias during the rename.
type PostgresUserAdmin = PostgresTenantAdmin

// NewPostgresUserAdmin is kept as a compatibility wrapper during the rename.
func NewPostgresUserAdmin(store ports.MetadataStore) *PostgresTenantAdmin {
	return NewPostgresTenantAdmin(store)
}
