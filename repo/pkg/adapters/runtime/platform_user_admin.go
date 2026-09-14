package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

// platformUsernameSearchExpr 剥除 local:/oidc: 后再 ILIKE，避免按存储前缀误命中。
const platformUsernameSearchExpr = `REGEXP_REPLACE(u.username, '^(local:|oidc:)', '')`

// PostgresPlatformUserAdminStore 实现 ports.PlatformUserAdminStore（platform RLS bypass）。
type PostgresPlatformUserAdminStore struct {
	store ports.MetadataStore
}

var _ ports.PlatformUserAdminStore = (*PostgresPlatformUserAdminStore)(nil)

// NewPostgresPlatformUserAdminStore 构造基于 MetadataStore 的平台账号适配器。
func NewPostgresPlatformUserAdminStore(store ports.MetadataStore) *PostgresPlatformUserAdminStore {
	return &PostgresPlatformUserAdminStore{store: store}
}

// Create 插入平台账号（tenant_id IS NULL）并绑定平台角色。passwordHash 由调用方预计算。
func (s *PostgresPlatformUserAdminStore) Create(ctx context.Context, in ports.PlatformUserCreate) (ports.PlatformUserAdmin, error) {
	// 步骤 1：规范化入参并做字段校验（email 允许重复；username 不含 ':'）
	email := strings.TrimSpace(in.Email)
	username := strings.TrimSpace(in.Username)
	displayName := strings.TrimSpace(in.DisplayName)
	if err := validatePlatformUserCreateFields(email, username, displayName, in.RoleID, in.PasswordHash); err != nil {
		return ports.PlatformUserAdmin{}, err
	}
	prefixed := "local:" + username

	var out ports.PlatformUserAdmin
	err := s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 2：username（local: 前缀）唯一性检查
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM users
				WHERE tenant_id IS NULL AND username = $1 AND is_deleted = FALSE
			)
		`, prefixed).Scan(&exists); err != nil {
			return fmt.Errorf("check platform username: %w", err)
		}
		if exists {
			return ports.ErrUsernameAlreadyExists
		}

		// 步骤 3：按 role_id 解析平台角色（tenant_id IS NULL 且 name LIKE 'platform-%'）
		roleName, err := lookupPlatformRoleNameByID(ctx, tx, in.RoleID)
		if err != nil {
			return err
		}

		// 步骤 4：插入 users + 绑定 user_roles
		var userID uuid.UUID
		var createdAt time.Time
		if err := tx.QueryRow(ctx, `
			INSERT INTO users (
				tenant_id, email, username, display_name, password_hash, status, is_deleted
			) VALUES (
				NULL, $1, $2, $3, $4, 'active', FALSE
			)
			RETURNING id, created_at
		`, email, prefixed, displayName, in.PasswordHash).Scan(&userID, &createdAt); err != nil {
			if isPGUniqueViolation(err) {
				return ports.ErrUsernameAlreadyExists
			}
			return fmt.Errorf("insert platform user: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)
		`, userID, in.RoleID); err != nil {
			return fmt.Errorf("bind platform role: %w", err)
		}

		// 步骤 5：组装返回 DTO（source 由 username 前缀推断）
		dn := displayName
		out = ports.PlatformUserAdmin{
			ID:          userID,
			Email:       email,
			Username:    prefixed,
			DisplayName: &dn,
			RoleID:      in.RoleID,
			Role:        roleName,
			Status:      "active",
			Source:      inferPlatformSource(prefixed),
			CreatedAt:   createdAt,
		}
		return nil
	})
	if err != nil {
		return ports.PlatformUserAdmin{}, err
	}
	return out, nil
}

// List 游标分页返回平台账号（tenant_id IS NULL，排除软删除）。
func (s *PostgresPlatformUserAdminStore) List(ctx context.Context, filter ports.PlatformUserFilter) (ports.PlatformUserListResult, error) {
	// 步骤 1：规范化 limit / 解析游标
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	var cursorCreatedAt time.Time
	var cursorID uuid.UUID
	hasCursor := strings.TrimSpace(filter.Cursor) != ""
	if hasCursor {
		t, id, err := types.DecodeCursor(filter.Cursor)
		if err != nil {
			return ports.PlatformUserListResult{}, fmt.Errorf("%w: invalid cursor", ports.ErrValidationFailed)
		}
		cursorCreatedAt, cursorID = t, id
	}

	// 步骤 2：拼装 WHERE（固定平台角色条件 + 可选过滤）
	args := []any{}
	where := []string{
		"u.tenant_id IS NULL",
		"u.is_deleted = FALSE",
		"r.tenant_id IS NULL",
		"r.name LIKE 'platform-%'",
	}
	argN := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if filter.RoleID != uuid.Nil {
		where = append(where, "r.id = "+argN(filter.RoleID))
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		switch status {
		case "active", "disabled":
			where = append(where, "u.status = "+argN(status))
		default:
			return ports.PlatformUserListResult{}, fmt.Errorf("%w: status must be active or disabled", ports.ErrValidationFailed)
		}
	}
	switch src := strings.TrimSpace(filter.Source); src {
	case "local":
		where = append(where, "u.username LIKE 'local:%'")
	case "third_party":
		where = append(where, "u.username LIKE 'oidc:%'")
	case "":
	default:
		return ports.PlatformUserListResult{}, fmt.Errorf("%w: source must be local or third_party", ports.ErrValidationFailed)
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		where = append(where, platformUsernameSearchExpr+" ILIKE "+argN("%"+search+"%"))
	}
	if hasCursor {
		p1, p2 := argN(cursorCreatedAt), argN(cursorID)
		where = append(where, fmt.Sprintf("(u.created_at, u.id) < (%s, %s)", p1, p2))
	}

	// 步骤 3：查询 limit+1 行以判断下一页
	limitParam := argN(limit + 1)
	query := `
		SELECT u.id, u.email, u.username, u.display_name, r.id, r.name, u.status,
		       u.last_login_at, u.created_at
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id
		JOIN roles r ON r.id = ur.role_id
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY u.created_at DESC, u.id DESC
		LIMIT ` + limitParam

	var items []ports.PlatformUserAdmin
	err := s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, qErr := tx.Query(ctx, query, args...)
		if qErr != nil {
			return fmt.Errorf("list platform users: %w", qErr)
		}
		defer rows.Close()
		for rows.Next() {
			item, scanErr := scanPlatformUserAdmin(rows)
			if scanErr != nil {
				return scanErr
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return ports.PlatformUserListResult{}, err
	}

	// 步骤 4：截断并生成 next_cursor
	nextCursor := ""
	if len(items) > limit {
		last := items[limit-1]
		nextCursor = types.EncodeCursor(last.CreatedAt, last.ID)
		items = items[:limit]
	}
	return ports.PlatformUserListResult{Items: items, NextCursor: nextCursor}, nil
}

// Get 按 ID 返回单个平台账号（不含 password_hash）。
func (s *PostgresPlatformUserAdminStore) Get(ctx context.Context, userID uuid.UUID) (ports.PlatformUserAdmin, error) {
	var out ports.PlatformUserAdmin
	err := s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 1：JOIN 平台角色查询未软删除账号
		row := tx.QueryRow(ctx, `
			SELECT u.id, u.email, u.username, u.display_name, r.id, r.name, u.status,
			       u.last_login_at, u.created_at
			FROM users u
			JOIN user_roles ur ON ur.user_id = u.id
			JOIN roles r ON r.id = ur.role_id
			  AND r.tenant_id IS NULL
			  AND r.name LIKE 'platform-%'
			WHERE u.id = $1 AND u.tenant_id IS NULL AND u.is_deleted = FALSE
		`, userID)
		// 步骤 2：扫描并映射 source；无行 → PLATFORM_USER_NOT_FOUND
		item, scanErr := scanPlatformUserAdmin(row)
		if scanErr != nil {
			if errors.Is(scanErr, pgx.ErrNoRows) {
				return ports.ErrPlatformUserNotFound
			}
			return scanErr
		}
		out = item
		return nil
	})
	if err != nil {
		return ports.PlatformUserAdmin{}, err
	}
	return out, nil
}

// ChangeRole 若已有平台角色绑定则更新 role_id，否则插入；按 role_id 校验目标角色。
// 将唯一活跃 platform-admin 降级为非 admin → ErrLastPlatformAdmin。
func (s *PostgresPlatformUserAdminStore) ChangeRole(ctx context.Context, userID uuid.UUID, roleID uuid.UUID) error {
	// 步骤 1：校验目标角色 id
	if roleID == uuid.Nil {
		return fmt.Errorf("%w: role_id required", ports.ErrValidationFailed)
	}
	return s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 2：确认平台账号存在
		if err := ensurePlatformUserExists(ctx, tx, userID); err != nil {
			return err
		}
		// 步骤 3：校验目标平台角色 id 存在
		newRoleName, err := lookupPlatformRoleNameByID(ctx, tx, roleID)
		if err != nil {
			return err
		}
		// 步骤 4：已有平台角色绑定 → UPDATE role_id；否则 INSERT
		var bindingExists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM user_roles ur
				JOIN roles r ON r.id = ur.role_id
				WHERE ur.user_id = $1
				  AND r.tenant_id IS NULL
				  AND r.name LIKE 'platform-%'
			)
		`, userID).Scan(&bindingExists); err != nil {
			return fmt.Errorf("check platform role binding: %w", err)
		}
		if bindingExists {
			// 步骤 4a：降级最后活跃 platform-admin → 拒绝
			currentRole, roleErr := platformUserRole(ctx, tx, userID)
			if roleErr != nil {
				return roleErr
			}
			if currentRole == "platform-admin" && newRoleName != "platform-admin" {
				n, countErr := countActivePlatformAdminsTx(ctx, tx, userID)
				if countErr != nil {
					return countErr
				}
				if n <= 0 {
					return ports.ErrLastPlatformAdmin
				}
			}
			tag, err := tx.Exec(ctx, `
				UPDATE user_roles ur
				SET role_id = $2
				FROM roles r
				WHERE ur.user_id = $1
				  AND ur.role_id = r.id
				  AND r.tenant_id IS NULL
				  AND r.name LIKE 'platform-%'
			`, userID, roleID)
			if err != nil {
				return fmt.Errorf("update platform role: %w", err)
			}
			if tag.RowsAffected == 0 {
				return ports.ErrRoleChangeInvalid
			}
			return nil
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)
		`, userID, roleID); err != nil {
			return fmt.Errorf("bind platform role: %w", err)
		}
		return nil
	})
}

// ResetPassword 校验新旧密码不同后更新 password_hash。
func (s *PostgresPlatformUserAdminStore) ResetPassword(ctx context.Context, userID uuid.UUID, newPassword string) error {
	// 步骤 1：密码复杂度校验
	if detail := validatePasswordComplexity(newPassword); detail != "" {
		return fmt.Errorf("%w: %s", ports.ErrValidationFailed, detail)
	}
	return s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 2：读取旧 hash；同旧密码 → PASSWORD_SAME_AS_OLD
		var oldHash *string
		err := tx.QueryRow(ctx, `
			SELECT password_hash FROM users
			WHERE id = $1 AND tenant_id IS NULL AND is_deleted = FALSE
		`, userID).Scan(&oldHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return ports.ErrPlatformUserNotFound
		}
		if err != nil {
			return fmt.Errorf("load password hash: %w", err)
		}
		if oldHash != nil && *oldHash != "" {
			if bcrypt.CompareHashAndPassword([]byte(*oldHash), []byte(newPassword)) == nil {
				return ports.ErrPasswordSameAsOld
			}
		}
		// 步骤 3：bcrypt 哈希并写回
		hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}
		tag, err := tx.Exec(ctx, `
			UPDATE users SET password_hash = $2, updated_at = now()
			WHERE id = $1 AND tenant_id IS NULL AND is_deleted = FALSE
		`, userID, string(hash))
		if err != nil {
			return fmt.Errorf("update password: %w", err)
		}
		if tag.RowsAffected == 0 {
			return ports.ErrPlatformUserNotFound
		}
		return nil
	})
}

// SetStatus 更新 users.status（active/disabled）；同状态拒绝；禁用含最后管理员保护。
func (s *PostgresPlatformUserAdminStore) SetStatus(ctx context.Context, userID uuid.UUID, status string) error {
	// 步骤 1：校验 status 枚举
	status = strings.TrimSpace(status)
	if status != "active" && status != "disabled" {
		return fmt.Errorf("%w: status must be active or disabled", ports.ErrValidationFailed)
	}
	return s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 2：读当前 status + 平台角色；不存在 → NOT_FOUND
		currentStatus, role, err := platformUserStatusAndRole(ctx, tx, userID)
		if err != nil {
			return err
		}
		// 步骤 3：禁止重复启用 / 停用
		if currentStatus == status {
			return fmt.Errorf("%w: already %s", ports.ErrStatusUnchanged, status)
		}
		// 步骤 4：禁用最后一个 platform-admin 则拒绝
		if status == "disabled" && role == "platform-admin" {
			n, countErr := countActivePlatformAdminsTx(ctx, tx, userID)
			if countErr != nil {
				return countErr
			}
			if n <= 0 {
				return ports.ErrLastPlatformAdmin
			}
		}
		// 步骤 5：更新 status
		tag, err := tx.Exec(ctx, `
			UPDATE users SET status = $2, updated_at = now()
			WHERE id = $1 AND tenant_id IS NULL AND is_deleted = FALSE
		`, userID, status)
		if err != nil {
			return fmt.Errorf("set platform user status: %w", err)
		}
		if tag.RowsAffected == 0 {
			return ports.ErrPlatformUserNotFound
		}
		return nil
	})
}

// SoftDelete 置 is_deleted=TRUE、deleted_at=now()、status=disabled。
func (s *PostgresPlatformUserAdminStore) SoftDelete(ctx context.Context, userID uuid.UUID) error {
	return s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 1：读当前角色；删除最后一个 platform-admin 则拒绝
		role, err := platformUserRole(ctx, tx, userID)
		if err != nil {
			return err
		}
		if role == "platform-admin" {
			n, countErr := countActivePlatformAdminsTx(ctx, tx, userID)
			if countErr != nil {
				return countErr
			}
			if n <= 0 {
				return ports.ErrLastPlatformAdmin
			}
		}
		// 步骤 2：软删除标记
		tag, err := tx.Exec(ctx, `
			UPDATE users
			SET is_deleted = TRUE, deleted_at = now(), status = 'disabled', updated_at = now()
			WHERE id = $1 AND tenant_id IS NULL AND is_deleted = FALSE
		`, userID)
		if err != nil {
			return fmt.Errorf("soft delete platform user: %w", err)
		}
		if tag.RowsAffected == 0 {
			return ports.ErrPlatformUserNotFound
		}
		return nil
	})
}

// CountActivePlatformAdmins 统计活跃 platform-admin 数（排除 excludeUserID）。
func (s *PostgresPlatformUserAdminStore) CountActivePlatformAdmins(ctx context.Context, excludeUserID uuid.UUID) (int, error) {
	var n int
	err := s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		var countErr error
		n, countErr = countActivePlatformAdminsTx(ctx, tx, excludeUserID)
		return countErr
	})
	return n, err
}

// ListPlatformRoles 返回平台内置角色（tenant_id IS NULL，name LIKE 'platform-%'）。
func (s *PostgresPlatformUserAdminStore) ListPlatformRoles(ctx context.Context) ([]ports.PlatformRole, error) {
	var out []ports.PlatformRole
	err := s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 1：查询平台角色行
		rows, qErr := tx.Query(ctx, `
			SELECT id, name, permissions
			FROM roles
			WHERE tenant_id IS NULL AND name LIKE 'platform-%'
			ORDER BY name
		`)
		if qErr != nil {
			return fmt.Errorf("list platform roles: %w", qErr)
		}
		defer rows.Close()
		// 步骤 2：解码 permissions JSONB 并组装 DTO
		for rows.Next() {
			var id uuid.UUID
			var name string
			var raw []byte
			if scanErr := rows.Scan(&id, &name, &raw); scanErr != nil {
				return fmt.Errorf("scan platform role: %w", scanErr)
			}
			perms, decErr := decodeRolePermissionsJSON(raw)
			if decErr != nil {
				return decErr
			}
			out = append(out, ports.PlatformRole{
				ID:          id,
				Name:        name,
				Permissions: perms,
			})
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetPlatformUserPermissions 返回指定平台账号当前绑定角色及 permissions JSONB 原样。
func (s *PostgresPlatformUserAdminStore) GetPlatformUserPermissions(ctx context.Context, userID uuid.UUID) (ports.PlatformUserPermissions, error) {
	var out ports.PlatformUserPermissions
	err := s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		// 步骤 1：JOIN 查询平台账号绑定角色
		var roleID uuid.UUID
		var roleName string
		var raw []byte
		qErr := tx.QueryRow(ctx, `
			SELECT r.id, r.name, r.permissions
			FROM users u
			JOIN user_roles ur ON ur.user_id = u.id
			JOIN roles r ON r.id = ur.role_id
			  AND r.tenant_id IS NULL
			  AND r.name LIKE 'platform-%'
			WHERE u.id = $1 AND u.tenant_id IS NULL AND u.is_deleted = FALSE
		`, userID).Scan(&roleID, &roleName, &raw)
		if qErr != nil {
			if errors.Is(qErr, pgx.ErrNoRows) {
				return ports.ErrPlatformUserNotFound
			}
			return fmt.Errorf("get platform user permissions: %w", qErr)
		}
		// 步骤 2：解码 permissions
		perms, decErr := decodeRolePermissionsJSON(raw)
		if decErr != nil {
			return decErr
		}
		out = ports.PlatformUserPermissions{
			UserID:      userID,
			RoleID:      roleID,
			Role:        roleName,
			Permissions: perms,
		}
		return nil
	})
	if err != nil {
		return ports.PlatformUserPermissions{}, err
	}
	return out, nil
}

func decodeRolePermissionsJSON(raw []byte) ([]map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var perms []map[string]any
	if err := json.Unmarshal(raw, &perms); err != nil {
		return nil, fmt.Errorf("decode role permissions: %w", err)
	}
	return perms, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanPlatformUserAdmin(row scannable) (ports.PlatformUserAdmin, error) {
	var (
		id          uuid.UUID
		email       string
		username    string
		displayName *string
		roleID      uuid.UUID
		role        string
		status      string
		lastLoginAt *time.Time
		createdAt   time.Time
	)
	if err := row.Scan(&id, &email, &username, &displayName, &roleID, &role, &status, &lastLoginAt, &createdAt); err != nil {
		return ports.PlatformUserAdmin{}, err
	}
	// TODO(list/detail): username 当前为库内值（含 local:/oidc:）；对外列表/详情后续剥前缀。
	return ports.PlatformUserAdmin{
		ID:          id,
		Email:       email,
		Username:    username,
		DisplayName: displayName,
		RoleID:      roleID,
		Role:        role,
		Status:      status,
		Source:      inferPlatformSource(username),
		LastLoginAt: lastLoginAt,
		CreatedAt:   createdAt,
	}, nil
}

func lookupPlatformRoleNameByID(ctx context.Context, tx ports.MetadataTx, roleID uuid.UUID) (string, error) {
	if roleID == uuid.Nil {
		return "", fmt.Errorf("%w: role_id required", ports.ErrValidationFailed)
	}
	var name string
	err := tx.QueryRow(ctx, `
		SELECT name FROM roles
		WHERE id = $1 AND tenant_id IS NULL AND name LIKE 'platform-%'
	`, roleID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ports.ErrRoleNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lookup platform role: %w", err)
	}
	return name, nil
}

func ensurePlatformUserExists(ctx context.Context, tx ports.MetadataTx, userID uuid.UUID) error {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM users
			WHERE id = $1 AND tenant_id IS NULL AND is_deleted = FALSE
		)
	`, userID).Scan(&exists); err != nil {
		return fmt.Errorf("check platform user: %w", err)
	}
	if !exists {
		return ports.ErrPlatformUserNotFound
	}
	return nil
}

func platformUserRole(ctx context.Context, tx ports.MetadataTx, userID uuid.UUID) (string, error) {
	_, role, err := platformUserStatusAndRole(ctx, tx, userID)
	return role, err
}

func platformUserStatusAndRole(ctx context.Context, tx ports.MetadataTx, userID uuid.UUID) (status, role string, err error) {
	qErr := tx.QueryRow(ctx, `
		SELECT u.status, r.name
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id
		JOIN roles r ON r.id = ur.role_id
		  AND r.tenant_id IS NULL
		  AND r.name LIKE 'platform-%'
		WHERE u.id = $1 AND u.tenant_id IS NULL AND u.is_deleted = FALSE
	`, userID).Scan(&status, &role)
	if errors.Is(qErr, pgx.ErrNoRows) {
		return "", "", ports.ErrPlatformUserNotFound
	}
	if qErr != nil {
		return "", "", fmt.Errorf("load platform user status/role: %w", qErr)
	}
	return status, role, nil
}

func countActivePlatformAdminsTx(ctx context.Context, tx ports.MetadataTx, excludeUserID uuid.UUID) (int, error) {
	var n int64
	err := tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM users u
		JOIN user_roles ur ON ur.user_id = u.id
		JOIN roles r ON r.id = ur.role_id
		WHERE u.tenant_id IS NULL
		  AND u.is_deleted = FALSE
		  AND u.status = 'active'
		  AND r.name = 'platform-admin'
		  AND r.tenant_id IS NULL
		  AND u.id != $1
	`, excludeUserID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count active platform admins: %w", err)
	}
	return int(n), nil
}

func inferPlatformSource(username string) string {
	switch {
	case strings.HasPrefix(username, "oidc:"):
		return "third_party"
	case strings.HasPrefix(username, "local:"):
		return "local"
	default:
		return "unknown"
	}
}

func isPGUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func validatePlatformUserCreateFields(email, username, displayName string, roleID uuid.UUID, passwordHash string) error {
	if email == "" {
		return fmt.Errorf("%w: email required", ports.ErrValidationFailed)
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || !strings.EqualFold(addr.Address, email) {
		return fmt.Errorf("%w: email must be RFC 5322", ports.ErrValidationFailed)
	}
	if n := len([]rune(username)); n < 1 || n > 64 {
		return fmt.Errorf("%w: username must be 1-64 characters", ports.ErrValidationFailed)
	}
	if strings.Contains(username, ":") {
		return fmt.Errorf("%w: username must not contain ':'", ports.ErrValidationFailed)
	}
	if n := len([]rune(displayName)); n < 1 || n > 128 {
		return fmt.Errorf("%w: display_name must be 1-128 characters", ports.ErrValidationFailed)
	}
	if roleID == uuid.Nil {
		return fmt.Errorf("%w: role_id required", ports.ErrValidationFailed)
	}
	if strings.TrimSpace(passwordHash) == "" {
		return fmt.Errorf("%w: password_hash required", ports.ErrValidationFailed)
	}
	return nil
}

func validatePasswordComplexity(password string) string {
	n := len([]rune(password))
	if n < 8 || n > 64 {
		return "password must be 8-64 characters"
	}
	var upper, lower, digit, special bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			special = true
		}
	}
	classes := 0
	if upper {
		classes++
	}
	if lower {
		classes++
	}
	if digit {
		classes++
	}
	if special {
		classes++
	}
	if classes < 3 {
		return "password must include at least 3 of: upper, lower, digit, special"
	}
	return ""
}
