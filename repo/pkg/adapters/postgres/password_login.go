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

func (s *passwordLoginStore) InsertRefreshToken(ctx context.Context, tenantID, userID uuid.UUID, tokenHash string, roles []string, expiresAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO refresh_tokens (tenant_id, user_id, token_hash, roles, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, tenantID, userID, tokenHash, roles, expiresAt)
	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

func (s *passwordLoginStore) TouchLastLogin(ctx context.Context, userID uuid.UUID, at time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET last_login_at=$1, updated_at=$1 WHERE id=$2`, at, userID)
	return err
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

func (s *platformLoginStore) InsertRefreshToken(ctx context.Context, userID uuid.UUID, tokenHash string, roles []string, expiresAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO refresh_tokens (tenant_id, user_id, token_hash, roles, expires_at)
		VALUES (NULL, $1, $2, $3, $4)
	`, userID, tokenHash, roles, expiresAt)
	if err != nil {
		return fmt.Errorf("insert platform refresh token: %w", err)
	}
	return nil
}

func (s *platformLoginStore) TouchLastLogin(ctx context.Context, userID uuid.UUID, at time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET last_login_at=$1, updated_at=$1 WHERE id=$2`, at, userID)
	return err
}
