package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/pkg/types"
)

type oidcSessionStore interface {
	CreateSession(ctx context.Context, tenantName string, claims oidcClaims) (refreshPrincipal, string, error)
}

type postgresOIDCSessionStore struct {
	db     *pgxpool.Pool
	mapper oidcGroupRoleMapper
}

func newOIDCSessionStore(db *pgxpool.Pool, mapper oidcGroupRoleMapper) *postgresOIDCSessionStore {
	return &postgresOIDCSessionStore{db: db, mapper: mapper}
}

func (s *postgresOIDCSessionStore) CreateSession(ctx context.Context, tenantName string, claims oidcClaims) (refreshPrincipal, string, error) {
	if s.db == nil {
		return refreshPrincipal{}, "", errJWTNotConfigured
	}
	if tenantName == "" || claims.Subject == "" || claims.Email == "" {
		return refreshPrincipal{}, "", types.ErrUnauthorized
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return refreshPrincipal{}, "", err
	}
	defer rollbackTx(ctx, tx)

	var tenantID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM tenants
		WHERE name=$1 AND status='active'
	`, tenantName).Scan(&tenantID)
	if err != nil {
		return refreshPrincipal{}, "", fmt.Errorf("lookup tenant: %w", err)
	}
	ctx = types.WithTenant(ctx, &types.TenantContext{TenantID: tenantID})
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return refreshPrincipal{}, "", err
	}

	username := "oidc:" + claims.Subject
	displayName := claims.Name
	if displayName == "" {
		displayName = claims.Email
	}

	var userID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO users (tenant_id, username, email, status)
		VALUES ($1, $2, $3, 'active')
		ON CONFLICT (tenant_id, username) DO UPDATE
		SET email=EXCLUDED.email,
		    status='active',
		    last_login_at=NOW(),
		    updated_at=NOW()
		RETURNING id
	`, tenantID, username, claims.Email).Scan(&userID)
	if err != nil {
		return refreshPrincipal{}, "", fmt.Errorf("upsert oidc user: %w", err)
	}
	_ = displayName

	roles := s.mapper.Map(claims.Groups)
	// Dex v2.40 staticPasswords cannot emit groups; bootstrap the well-known
	// local admin account to tenant-admin so lab/console admins are not stuck
	// on the default least-privilege "user" role.
	if isBootstrapAdminEmail(claims.Email) && !hasPrivilegedOIDCRole(roles) {
		roles = []string{"tenant-admin"}
	}
	if err := grantRoles(ctx, tx, userID, tenantID, roles); err != nil {
		return refreshPrincipal{}, "", err
	}
	roles, err = listGrantedRoles(ctx, tx, userID)
	if err != nil {
		return refreshPrincipal{}, "", err
	}

	rawRefreshToken, err := generateRefreshToken()
	if err != nil {
		return refreshPrincipal{}, "", err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO refresh_tokens (tenant_id, user_id, token_hash, roles, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, tenantID, userID, hashRefreshToken(rawRefreshToken), roles, time.Now().Add(defaultRefreshTokenTTL))
	if err != nil {
		return refreshPrincipal{}, "", fmt.Errorf("insert refresh token: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return refreshPrincipal{}, "", err
	}

	return refreshPrincipal{TenantID: tenantID, UserID: userID, Roles: roles}, rawRefreshToken, nil
}

type oidcGroupRoleMapper struct {
	groupToRoles map[string][]string
	defaultRoles []string
}

func newOIDCGroupRoleMapper(rawJSON string) oidcGroupRoleMapper {
	mapper := oidcGroupRoleMapper{groupToRoles: map[string][]string{}, defaultRoles: []string{"user"}}
	if strings.TrimSpace(rawJSON) == "" {
		return mapper
	}
	var raw map[string][]string
	if err := json.Unmarshal([]byte(rawJSON), &raw); err != nil {
		return mapper
	}
	for group, roles := range raw {
		normalizedGroup := normalizeOIDCGroup(group)
		if normalizedGroup == "" {
			continue
		}
		for _, role := range roles {
			normalizedRole := normalizeOIDCRole(role)
			if isAllowedOIDCRole(normalizedRole) {
				mapper.groupToRoles[normalizedGroup] = append(mapper.groupToRoles[normalizedGroup], normalizedRole)
			}
		}
	}
	return mapper
}

func (m oidcGroupRoleMapper) Map(groups []string) []string {
	allowed := map[string]bool{}
	for _, group := range groups {
		for _, role := range m.groupToRoles[normalizeOIDCGroup(group)] {
			allowed[role] = true
		}
	}
	if len(allowed) == 0 {
		return append([]string{}, m.defaultRoles...)
	}
	roles := make([]string, 0, len(allowed))
	for role := range allowed {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

func normalizeOIDCRole(role string) string {
	return strings.TrimSpace(strings.ToLower(role))
}

func isAllowedOIDCRole(role string) bool {
	switch role {
	case "platform-admin", "tenant-admin", "user", "auditor":
		return true
	default:
		return false
	}
}

func hasPrivilegedOIDCRole(roles []string) bool {
	for _, role := range roles {
		if role == "platform-admin" || role == "tenant-admin" {
			return true
		}
	}
	return false
}

func isBootstrapAdminEmail(email string) bool {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return false
	}
	raw := strings.TrimSpace(os.Getenv("AUTH_OIDC_BOOTSTRAP_ADMIN_EMAILS"))
	if raw == "" {
		raw = "admin@ani.local"
	}
	for _, item := range strings.Split(raw, ",") {
		if strings.TrimSpace(strings.ToLower(item)) == email {
			return true
		}
	}
	return false
}

func normalizeOIDCGroup(group string) string {
	group = strings.TrimSpace(strings.ToLower(group))
	if index := strings.LastIndex(group, "/"); index >= 0 {
		group = group[index+1:]
	}
	if index := strings.LastIndex(group, ":"); index >= 0 {
		group = group[index+1:]
	}
	return group
}

func grantRoles(ctx context.Context, tx pgx.Tx, userID, tenantID uuid.UUID, roles []string) error {
	for _, role := range roles {
		_, err := tx.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id)
			SELECT $1, id
			FROM roles
			WHERE name=$2 AND (tenant_id IS NULL OR tenant_id=$3)
			ON CONFLICT DO NOTHING
		`, userID, role, tenantID)
		if err != nil {
			return fmt.Errorf("grant role %s: %w", role, err)
		}
	}
	return nil
}

func listGrantedRoles(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT r.name
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1
		ORDER BY r.name
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list granted roles: %w", err)
	}
	defer rows.Close()

	roles := []string{}
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, fmt.Errorf("scan granted role: %w", err)
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list granted roles: %w", err)
	}
	return roles, nil
}
