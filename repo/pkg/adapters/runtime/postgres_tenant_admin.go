package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
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
func (u *PostgresTenantAdmin) IsTenantAdmin(ctx context.Context, tenantID, userID string) (bool, error) {
	// 步骤 1：复用 GetUser 校验存在性并读取角色
	user, err := u.GetUser(ctx, tenantID, userID)
	if err != nil {
		return false, err
	}
	// 步骤 2：判断是否为 tenant-admin（platform-* 等非该角色一律 false）
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
	// 步骤 1：规范化分页与过滤
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	role := strings.TrimSpace(filter.Role)
	if role == "" {
		role = tenantAdminRoleName
	}
	if role != tenantAdminRoleName {
		return ports.UserListResult{}, fmt.Errorf("%w: role must be tenant-admin", ports.ErrInvalid)
	}
	status := strings.TrimSpace(filter.Status)
	if status != "" && status != "active" && status != "disabled" {
		return ports.UserListResult{}, fmt.Errorf("%w: status must be active or disabled", ports.ErrInvalid)
	}
	search := strings.TrimSpace(filter.Search)
	var tenantFilter *uuid.UUID
	if raw := strings.TrimSpace(filter.TenantID); raw != "" {
		tid, err := parseAdminUUID(raw, "tenant_id")
		if err != nil {
			return ports.UserListResult{}, err
		}
		tenantFilter = &tid
	}

	var cursorCreatedAt *time.Time
	var cursorID *uuid.UUID
	if raw := strings.TrimSpace(filter.Cursor); raw != "" {
		createdAt, id, err := types.DecodeCursor(raw)
		if err != nil {
			return ports.UserListResult{}, fmt.Errorf("%w: invalid cursor", ports.ErrInvalid)
		}
		cursorCreatedAt = &createdAt
		cursorID = &id
	}

	// 步骤 2：平台事务内跨租户列出持有 tenant-admin 角色的未软删除用户
	var items []ports.User
	err := u.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		args := []any{tenantAdminRoleName, status, search}
		where := `
			WHERE COALESCE(u.is_deleted, FALSE) = FALSE
			  AND EXISTS (
				SELECT 1
				FROM user_roles ur0
				JOIN roles r0 ON r0.id = ur0.role_id
				WHERE ur0.user_id = u.id
				  AND r0.name = $1
			  )
			  AND ($2 = '' OR u.status = $2)
			  AND ($3 = '' OR u.email ILIKE '%' || $3 || '%' OR u.username ILIKE '%' || $3 || '%' OR u.display_name ILIKE '%' || $3 || '%')
		`
		if tenantFilter != nil {
			args = append(args, *tenantFilter)
			where += fmt.Sprintf(" AND u.tenant_id = $%d", len(args))
		}
		if cursorCreatedAt != nil && cursorID != nil {
			args = append(args, *cursorCreatedAt, *cursorID)
			where += fmt.Sprintf(" AND (u.created_at, u.id) < ($%d, $%d)", len(args)-1, len(args))
		}
		args = append(args, limit+1)
		limitArg := len(args)

		rows, queryErr := tx.Query(ctx, `
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
				ORDER BY CASE WHEN r2.name = $1 THEN 0 ELSE 1 END, r2.name
				LIMIT 1
			) r ON TRUE
			`+where+`
			ORDER BY u.created_at DESC, u.id DESC
			LIMIT $`+fmt.Sprintf("%d", limitArg), args...)
		if queryErr != nil {
			return fmt.Errorf("list tenant admin users: %w", queryErr)
		}
		defer rows.Close()

		out := make([]ports.User, 0, limit+1)
		for rows.Next() {
			user, scanErr := scanTenantAdminUserRow(rows)
			if scanErr != nil {
				return scanErr
			}
			out = append(out, user)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate tenant admin users: %w", err)
		}
		items = out
		return nil
	})
	if err != nil {
		return ports.UserListResult{}, err
	}

	// 步骤 3：多取 1 条判断是否还有下一页
	nextCursor := ""
	if len(items) > limit {
		last := items[limit-1]
		uid, parseErr := uuid.Parse(last.ID)
		if parseErr != nil {
			return ports.UserListResult{}, fmt.Errorf("encode cursor: %w", parseErr)
		}
		nextCursor = types.EncodeCursor(last.CreatedAt, uid)
		items = items[:limit]
	}
	return ports.UserListResult{Items: items, NextCursor: nextCursor}, nil
}

func (u *PostgresTenantAdmin) ChangeRole(ctx context.Context, tenantID, userID, roleID string) error {
	tid, err := parseAdminUUID(tenantID, "tenant_id")
	if err != nil {
		return err
	}
	uid, err := parseAdminUUID(userID, "user_id")
	if err != nil {
		return err
	}
	rid, err := parseAdminUUID(roleID, "role_id")
	if err != nil {
		return fmt.Errorf("%w: role_id invalid", ports.ErrRoleChangeInvalid)
	}

	return u.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 1：确认用户属于该租户且未软删除
		var exists bool
		if qErr := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM users
				WHERE id = $1 AND tenant_id = $2 AND COALESCE(is_deleted, FALSE) = FALSE
			)
		`, uid, tid).Scan(&exists); qErr != nil {
			return fmt.Errorf("check user for change role: %w", qErr)
		}
		if !exists {
			return ports.ErrUserNotFound
		}

		// 步骤 2：校验目标角色可分配（非 platform-*、非 tenant-admin；tenant_id NULL 或等于路径租户）
		var roleName string
		qErr := tx.QueryRow(ctx, `
			SELECT name
			FROM roles
			WHERE id = $1
			  AND name NOT LIKE 'platform-%'
			  AND name <> 'tenant-admin'
			  AND (tenant_id IS NULL OR tenant_id = $2)
		`, rid, tid).Scan(&roleName)
		if errors.Is(qErr, pgx.ErrNoRows) {
			return ports.ErrRoleChangeInvalid
		}
		if qErr != nil {
			return fmt.Errorf("lookup role for change: %w", qErr)
		}

		// 步骤 3：upsert（user_id 唯一索引保证单角色）
		if _, execErr := tx.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)
			ON CONFLICT (user_id) DO UPDATE SET role_id = EXCLUDED.role_id
		`, uid, rid); execErr != nil {
			return fmt.Errorf("upsert user role: %w", execErr)
		}
		_ = roleName
		return nil
	})
}

func (u *PostgresTenantAdmin) GetRolePermissions(ctx context.Context, tenantID, userID string) (ports.UserPermissions, error) {
	tid, err := parseAdminUUID(tenantID, "tenant_id")
	if err != nil {
		return ports.UserPermissions{}, err
	}
	uid, err := parseAdminUUID(userID, "user_id")
	if err != nil {
		return ports.UserPermissions{}, err
	}

	var out ports.UserPermissions
	err = u.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		var (
			userTenantID uuid.UUID
			roleID       *uuid.UUID
			roleTenant   *uuid.UUID
			roleName     string
			permRaw      []byte
		)
		// 单角色模型：业务保证一用户一角色绑定，LIMIT 1 取唯一行
		qErr := tx.QueryRow(ctx, `
		SELECT u.tenant_id, r.id, r.tenant_id, COALESCE(r.name, ''), COALESCE(r.permissions, '[]'::jsonb)
		FROM users u
		LEFT JOIN LATERAL (
			SELECT r2.id, r2.tenant_id, r2.name, r2.permissions
			FROM user_roles ur
			JOIN roles r2 ON r2.id = ur.role_id
			WHERE ur.user_id = u.id
			ORDER BY r2.name
			LIMIT 1
		) r ON TRUE
		WHERE u.id = $1
		  AND u.tenant_id = $2
		  AND COALESCE(u.is_deleted, FALSE) = FALSE
	`, uid, tid).Scan(&userTenantID, &roleID, &roleTenant, &roleName, &permRaw)
		if errors.Is(qErr, pgx.ErrNoRows) {
			return ports.ErrUserNotFound
		}
		if qErr != nil {
			return fmt.Errorf("get role permissions: %w", qErr)
		}
		// 平台账户不可经本端点查询（users.tenant_id 理论上已由 WHERE 约束）
		if userTenantID == uuid.Nil {
			return ports.ErrUserNotFound
		}
		// 当前角色为 platform-*，或角色 tenant_id 既非空也不等于路径租户 → 不可查
		if strings.HasPrefix(roleName, "platform-") {
			return ports.ErrUserNotFound
		}
		if roleTenant != nil && *roleTenant != tid {
			return ports.ErrUserNotFound
		}
		perms, decodeErr := decodeRolePermissionsJSON(permRaw)
		if decodeErr != nil {
			return decodeErr
		}
		out = ports.UserPermissions{
			UserID:      uid.String(),
			TenantID:    userTenantID.String(),
			Role:        roleName,
			Permissions: perms,
		}
		if roleID != nil {
			out.RoleID = roleID.String()
		}
		return nil
	})
	if err != nil {
		return ports.UserPermissions{}, err
	}
	return out, nil
}

// ListAssignableRoles 返回租户可分配角色（排除 platform-*；系统角色 + 该租户自定义角色）。
// 若租户不存在或已停用（status='disabled'），仅返回系统角色（tenant_id IS NULL）。
func (u *PostgresTenantAdmin) ListAssignableRoles(ctx context.Context, tenantID string) ([]ports.RoleRef, error) {
	tid, err := parseAdminUUID(tenantID, "tenant_id")
	if err != nil {
		return nil, err
	}
	var out []ports.RoleRef
	err = u.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, queryErr := tx.Query(ctx, `
			SELECT r.id, r.tenant_id, r.name, r.permissions
			FROM roles r
			WHERE r.name NOT LIKE 'platform-%'
			  AND r.name <> 'tenant-admin'
			  AND (
			    r.tenant_id IS NULL
			    OR (r.tenant_id = $1 AND EXISTS (
			      SELECT 1 FROM tenants t WHERE t.id = $1 AND t.status <> 'disabled'
			    ))
			  )
			ORDER BY r.name ASC, r.id ASC
		`, tid)
		if queryErr != nil {
			return fmt.Errorf("list assignable roles: %w", queryErr)
		}
		defer rows.Close()
		out = make([]ports.RoleRef, 0)
		for rows.Next() {
			var (
				ref      ports.RoleRef
				tenantID *uuid.UUID
				permRaw  []byte
			)
			if scanErr := rows.Scan(&ref.ID, &tenantID, &ref.Name, &permRaw); scanErr != nil {
				return fmt.Errorf("scan assignable role: %w", scanErr)
			}
			ref.TenantID = tenantID
			perms, decodeErr := decodeRolePermissionsJSON(permRaw)
			if decodeErr != nil {
				return decodeErr
			}
			ref.Permissions = perms
			out = append(out, ref)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func decodeRolePermissionsJSON(raw []byte) ([]any, error) {
	if len(raw) == 0 {
		return []any{}, nil
	}
	var perms []any
	if err := json.Unmarshal(raw, &perms); err != nil {
		return nil, fmt.Errorf("decode role permissions: %w", err)
	}
	if perms == nil {
		return []any{}, nil
	}
	return perms, nil
}

func (u *PostgresTenantAdmin) SetStatus(ctx context.Context, tenantID, userID, status string) error {
	tid, err := parseAdminUUID(tenantID, "tenant_id")
	if err != nil {
		return err
	}
	uid, err := parseAdminUUID(userID, "user_id")
	if err != nil {
		return err
	}
	switch status {
	case "active", "disabled":
	default:
		return ports.ErrInvalid
	}

	return u.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 1：查询当前状态
		var currentStatus string
		qErr := tx.QueryRow(ctx, `
			SELECT status
			FROM users
			WHERE id = $1
			  AND tenant_id = $2
			  AND COALESCE(is_deleted, FALSE) = FALSE
		`, uid, tid).Scan(&currentStatus)
		if errors.Is(qErr, pgx.ErrNoRows) {
			return ports.ErrUserNotFound
		}
		if qErr != nil {
			return fmt.Errorf("load user for set status: %w", qErr)
		}

		// 步骤 2：状态未变化 → ErrUserStateInvalid
		if currentStatus == status {
			return ports.ErrUserStateInvalid
		}

		// 步骤 3：UPDATE users.status
		tag, execErr := tx.Exec(ctx, `
			UPDATE users
			SET status = $1, updated_at = NOW()
			WHERE id = $2
			  AND tenant_id = $3
			  AND COALESCE(is_deleted, FALSE) = FALSE
		`, status, uid, tid)
		if execErr != nil {
			return fmt.Errorf("update user status: %w", execErr)
		}
		if tag.RowsAffected == 0 {
			return ports.ErrUserNotFound
		}
		return nil
	})
}

// SoftDelete 软删除租户成员（is_deleted=TRUE, deleted_at=now()；不改 users.status）。
func (u *PostgresTenantAdmin) SoftDelete(ctx context.Context, tenantID, userID string) error {
	tid, err := parseAdminUUID(tenantID, "tenant_id")
	if err != nil {
		return err
	}
	uid, err := parseAdminUUID(userID, "user_id")
	if err != nil {
		return err
	}

	return u.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		tag, execErr := tx.Exec(ctx, `
			UPDATE users
			SET is_deleted = TRUE,
			    deleted_at = NOW(),
			    updated_at = NOW()
			WHERE id = $1
			  AND tenant_id = $2
			  AND COALESCE(is_deleted, FALSE) = FALSE
		`, uid, tid)
		if execErr != nil {
			return fmt.Errorf("soft delete user: %w", execErr)
		}
		if tag.RowsAffected == 0 {
			return ports.ErrUserNotFound
		}
		return nil
	})
}

// ResetPassword 重置租户成员 password_hash（bcrypt cost=12）。
// 软删除 → ErrUserNotFound；与旧 hash 相同 → ErrPasswordSameAsOld；status=disabled 允许重置。
func (u *PostgresTenantAdmin) ResetPassword(ctx context.Context, tenantID, userID, newPassword string) error {
	tid, err := parseAdminUUID(tenantID, "tenant_id")
	if err != nil {
		return err
	}
	uid, err := parseAdminUUID(userID, "user_id")
	if err != nil {
		return err
	}

	return u.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 1：加载未软删除用户的旧 password_hash（禁用 status 不过滤）
		var oldHash string
		qErr := tx.QueryRow(ctx, `
			SELECT COALESCE(password_hash, '')
			FROM users
			WHERE id = $1
			  AND tenant_id = $2
			  AND COALESCE(is_deleted, FALSE) = FALSE
		`, uid, tid).Scan(&oldHash)
		if errors.Is(qErr, pgx.ErrNoRows) {
			return ports.ErrUserNotFound
		}
		if qErr != nil {
			return fmt.Errorf("load user for reset password: %w", qErr)
		}

		// 步骤 2：与旧 hash 相同 → PASSWORD_SAME_AS_OLD
		if oldHash != "" {
			if cmpErr := bcrypt.CompareHashAndPassword([]byte(oldHash), []byte(newPassword)); cmpErr == nil {
				return ports.ErrPasswordSameAsOld
			}
		}

		// 步骤 3：bcrypt(cost=12) 生成新 hash
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
		if hashErr != nil {
			return fmt.Errorf("hash password: %w", hashErr)
		}

		// 步骤 4：UPDATE users.password_hash
		tag, execErr := tx.Exec(ctx, `
			UPDATE users
			SET password_hash = $1, updated_at = NOW()
			WHERE id = $2
			  AND tenant_id = $3
			  AND COALESCE(is_deleted, FALSE) = FALSE
		`, string(hash), uid, tid)
		if execErr != nil {
			return fmt.Errorf("update password_hash: %w", execErr)
		}
		if tag.RowsAffected == 0 {
			return ports.ErrUserNotFound
		}
		return nil
	})
}

// BatchGetUsers 批量查询租户内未软删除用户（按 user_id 列表）。不存在的用户跳过。
func (u *PostgresTenantAdmin) BatchGetUsers(ctx context.Context, tenantID string, userIDs []string) ([]ports.User, error) {
	tid, err := parseAdminUUID(tenantID, "tenant_id")
	if err != nil {
		return nil, err
	}
	if len(userIDs) == 0 {
		return nil, nil
	}
	uids := make([]uuid.UUID, 0, len(userIDs))
	for _, raw := range userIDs {
		id, parseErr := parseAdminUUID(raw, "user_id")
		if parseErr != nil {
			return nil, parseErr
		}
		uids = append(uids, id)
	}
	var out []ports.User
	err = u.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, queryErr := tx.Query(ctx, `
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
				ORDER BY r2.name
				LIMIT 1
			) r ON TRUE
			WHERE u.tenant_id = $1
			  AND u.id = ANY($2::uuid[])
			  AND COALESCE(u.is_deleted, FALSE) = FALSE
		`, tid, uids)
		if queryErr != nil {
			return fmt.Errorf("batch get users: %w", queryErr)
		}
		defer rows.Close()
		out = make([]ports.User, 0)
		for rows.Next() {
			user, scanErr := scanTenantAdminUserRow(rows)
			if scanErr != nil {
				return scanErr
			}
			out = append(out, user)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// scanTenantAdminUser 执行 QueryRow 并映射为 ports.User；无行 → ErrUserNotFound。
func scanTenantAdminUser(ctx context.Context, tx ports.MetadataTx, query string, args ...any) (ports.User, error) {
	row := tx.QueryRow(ctx, query, args...)
	user, err := scanTenantAdminUserDest(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.User{}, ports.ErrUserNotFound
	}
	if err != nil {
		return ports.User{}, fmt.Errorf("scan tenant admin user: %w", err)
	}
	return user, nil
}

// scanTenantAdminUserRow 从多行结果集扫描一行。
func scanTenantAdminUserRow(rows ports.Rows) (ports.User, error) {
	user, err := scanTenantAdminUserDest(rows)
	if err != nil {
		return ports.User{}, fmt.Errorf("scan tenant admin user: %w", err)
	}
	return user, nil
}

type tenantAdminUserScanner interface {
	Scan(dest ...any) error
}

func scanTenantAdminUserDest(s tenantAdminUserScanner) (ports.User, error) {
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
	err := s.Scan(
		&id, &tenantUUID, &email, &username, &displayName, &status, &role,
		&lastLoginAt, &createdAt, &updatedAt,
		&tenantID, &tenantName, &tenantDisplay,
	)
	if err != nil {
		return ports.User{}, err
	}
	source := inferTenantUserSource(username)
	return ports.User{
		ID:          id.String(),
		TenantID:    tenantUUID.String(),
		Email:       email,
		Username:    stripTenantUserUsernamePrefix(username),
		DisplayName: displayName,
		Role:        role,
		Status:      status,
		Source:      source,
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

// inferTenantUserSource 按 username 前缀推断来源（SPEC：oidc: → third_party；其余 local）。
func inferTenantUserSource(username string) string {
	if strings.HasPrefix(username, "oidc:") {
		return userSourceOIDC
	}
	return userSourceLocal
}

// stripTenantUserUsernamePrefix 对外返回时去掉存储前缀 oidc: / local:。
func stripTenantUserUsernamePrefix(username string) string {
	switch {
	case strings.HasPrefix(username, "oidc:"):
		return strings.TrimPrefix(username, "oidc:")
	case strings.HasPrefix(username, "local:"):
		return strings.TrimPrefix(username, "local:")
	default:
		return username
	}
}

// PostgresUserAdmin is kept as a compatibility alias during the rename.
type PostgresUserAdmin = PostgresTenantAdmin

// NewPostgresUserAdmin is kept as a compatibility wrapper during the rename.
func NewPostgresUserAdmin(store ports.MetadataStore) *PostgresTenantAdmin {
	return NewPostgresTenantAdmin(store)
}
