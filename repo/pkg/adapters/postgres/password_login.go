package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
)

// passwordLoginStore implements ports.PasswordLoginStore using PostgreSQL.
// It uses *pgxpool.Pool directly (same pattern as MetadataStore) because login
// occurs before authentication — there is no TenantContext in ctx yet, so
// WithTenantTx (which calls SetDBTenant → FromContext) cannot be used.
type passwordLoginStore struct {
	db *pgxpool.Pool
}

var _ ports.PasswordLoginStore = (*passwordLoginStore)(nil)

func NewPasswordLoginStore(db *pgxpool.Pool) ports.PasswordLoginStore {
	return &passwordLoginStore{db: db}
}

func (s *passwordLoginStore) LookupTenant(ctx context.Context, tenantName string) (uuid.UUID, error) {
	var tenantID uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT id FROM tenants
		WHERE name=$1 AND status='active'
	`, tenantName).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ports.ErrTenantNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	return tenantID, nil
}

func (s *passwordLoginStore) LookupUser(ctx context.Context, tenantID uuid.UUID, namespacedUsername string) (ports.PasswordUser, error) {
	var user ports.PasswordUser
	err := s.db.QueryRow(ctx, `
		SELECT id, password_hash, status
		FROM users
		WHERE tenant_id=$1 AND username=$2
	`, tenantID, namespacedUsername).Scan(&user.ID, &user.PasswordHash, &user.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.PasswordUser{}, ports.ErrInvalidCredentials
	}
	if err != nil {
		return ports.PasswordUser{}, err
	}
	if user.PasswordHash == "" {
		return ports.PasswordUser{}, ports.ErrInvalidCredentials
	}
	return user, nil
}

func (s *passwordLoginStore) LoadRoles(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.name
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id=$1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		roles = append(roles, name)
	}
	if len(roles) == 0 {
		roles = []string{"user"}
	}
	return roles, nil
}

// FinalizeLogin 在单事务内 SetDBTenant + 插入 refresh token + 更新 last_login_at。
// 必须设 app.current_tenant_id，否则 refresh_tokens 的 RLS 策略
// (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), ”)::uuid)
// 对 tenant_id NOT NULL 的行求值为 false，INSERT 会被拒绝
// （生产 ani_app_user 无 BYPASSRLS；dev superuser 绕过 RLS 会掩盖此 bug）。
func (s *passwordLoginStore) FinalizeLogin(ctx context.Context, tenantID, userID uuid.UUID, tokenHash string, roles []string, expiresAt time.Time) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin finalize login: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ctx = types.WithTenant(ctx, &types.TenantContext{TenantID: tenantID, UserID: userID, Roles: roles})
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return fmt.Errorf("set db tenant: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (tenant_id, user_id, token_hash, roles, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, tenantID, userID, tokenHash, roles, expiresAt); err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE users SET last_login_at=$1, updated_at=$1 WHERE id=$2`, expiresAt, userID); err != nil {
		return fmt.Errorf("touch last login: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit finalize login: %w", err)
	}
	return nil
}

// platformLoginStore implements ports.PlatformLoginStore using PostgreSQL.
type platformLoginStore struct {
	db *pgxpool.Pool
}

var _ ports.PlatformLoginStore = (*platformLoginStore)(nil)

func NewPlatformLoginStore(db *pgxpool.Pool) ports.PlatformLoginStore {
	return &platformLoginStore{db: db}
}

func (s *platformLoginStore) LookupUser(ctx context.Context, namespacedUsername string) (ports.PlatformUser, error) {
	var user ports.PlatformUser
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.password_hash, u.status
		FROM users u
		WHERE u.username=$1
		  AND u.tenant_id IS NULL
		  AND EXISTS (
		    SELECT 1
		    FROM user_roles ur
		    JOIN roles r ON r.id = ur.role_id
		    WHERE ur.user_id = u.id
		      AND r.name='platform-admin'
		      AND r.tenant_id IS NULL
		  )
	`, namespacedUsername).Scan(&user.ID, &user.PasswordHash, &user.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.PlatformUser{}, ports.ErrInvalidCredentials
	}
	if err != nil {
		return ports.PlatformUser{}, err
	}
	if user.PasswordHash == "" {
		return ports.PlatformUser{}, ports.ErrInvalidCredentials
	}
	return user, nil
}

func (s *platformLoginStore) LoadRoles(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.name
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id=$1 AND r.tenant_id IS NULL
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		roles = append(roles, name)
	}
	if len(roles) == 0 {
		roles = []string{"platform-admin"}
	}
	return roles, nil
}

// FinalizeLogin 在单事务内插入平台 refresh token + 更新 last_login_at。
// 平台账号 tenant_id=NULL，refresh_tokens 的 RLS 策略对 tenant_id IS NULL 的行
// 直接放行，无需 SetDBTenant。与租户版保持一致的事务语义。
func (s *platformLoginStore) FinalizeLogin(ctx context.Context, userID uuid.UUID, tokenHash string, roles []string, expiresAt time.Time) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin finalize platform login: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (tenant_id, user_id, token_hash, roles, expires_at)
		VALUES (NULL, $1, $2, $3, $4)
	`, userID, tokenHash, roles, expiresAt); err != nil {
		return fmt.Errorf("insert platform refresh token: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE users SET last_login_at=$1, updated_at=$1 WHERE id=$2`, expiresAt, userID); err != nil {
		return fmt.Errorf("touch platform last login: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit finalize platform login: %w", err)
	}
	return nil
}
