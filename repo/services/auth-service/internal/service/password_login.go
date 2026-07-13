package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
)

// 登录流程所需的数据访问方法
// 用于查询租户、用户、角色和插入刷新令牌。
type passwordLoginStore interface {
	LookupTenant(ctx context.Context, tenantName string) (uuid.UUID, error)
	LookupUser(ctx context.Context, tenantID uuid.UUID, namespacedUsername string) (passwordUser, error)
	LoadRoles(ctx context.Context, userID uuid.UUID) ([]string, error)
	InsertRefreshToken(ctx context.Context, tenantID, userID uuid.UUID, tokenHash string, roles []string, expiresAt time.Time) error
	TouchLastLogin(ctx context.Context, userID uuid.UUID, at time.Time) error
}

// 密码登录用户结构体
type passwordUser struct {
	id           uuid.UUID
	passwordHash string
	status       string
}

type postgresPasswordLoginStore struct {
	db *pgxpool.Pool
}

// 构造函数，注入pgx连接池
func newPostgresPasswordLoginStore(db *pgxpool.Pool) *postgresPasswordLoginStore {
	return &postgresPasswordLoginStore{db: db}
}

// 根据租户名称查询活跃的租户ID
func (s *postgresPasswordLoginStore) LookupTenant(ctx context.Context, tenantName string) (uuid.UUID, error) {
	var tenantID uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT id FROM tenants
		WHERE name=$1 AND status='active'
	`, tenantName).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errTenantNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	return tenantID, nil
}

// 根据租户ID和用户名查询密码登录用户
func (s *postgresPasswordLoginStore) LookupUser(ctx context.Context, tenantID uuid.UUID, namespacedUsername string) (passwordUser, error) {
	var user passwordUser
	err := s.db.QueryRow(ctx, `
		SELECT id, password_hash, status
		FROM users
		WHERE tenant_id=$1 AND username=$2
	`, tenantID, namespacedUsername).Scan(&user.id, &user.passwordHash, &user.status)
	if errors.Is(err, pgx.ErrNoRows) {
		return passwordUser{}, errInvalidCredentials
	}
	if err != nil {
		return passwordUser{}, err
	}
	if user.passwordHash == "" {
		// OIDC-only user (password_hash NULL) cannot log in via password.
		return passwordUser{}, errInvalidCredentials
	}
	return user, nil
}

// 根据用户ID查询用户权限
func (s *postgresPasswordLoginStore) LoadRoles(ctx context.Context, userID uuid.UUID) ([]string, error) {
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

// 插入刷新令牌
func (s *postgresPasswordLoginStore) InsertRefreshToken(ctx context.Context, tenantID, userID uuid.UUID, tokenHash string, roles []string, expiresAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO refresh_tokens (tenant_id, user_id, token_hash, roles, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, tenantID, userID, tokenHash, roles, expiresAt)
	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

// 更新用户最后登录时间
func (s *postgresPasswordLoginStore) TouchLastLogin(ctx context.Context, userID uuid.UUID, at time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET last_login_at=$1, updated_at=$1 WHERE id=$2`, at, userID)
	return err
}

// 租户不存在错误
var errTenantNotFound = errors.New("tenant not found")

// 无效凭证错误
var errInvalidCredentials = errors.New("invalid credentials")

// 密码登录管理器
type passwordLoginManager struct {
	store passwordLoginStore // 数据访问层
	issuer *JWTIssuer         // JWT 发布者层
	now    func() time.Time   // 时间函数
}

// 构造函数
func newPasswordLoginManager(store passwordLoginStore, issuer *JWTIssuer) *passwordLoginManager {
	return &passwordLoginManager{
		store:  store,
		issuer: issuer,
		now:    time.Now,
	}
}

// 登录流程
func (m *passwordLoginManager) Login(ctx context.Context, tenantName, username, password string) (*authv1.TokenPair, error) {
	tenantName = strings.TrimSpace(tenantName) // 租户名
	username = strings.TrimSpace(username)     // 用户名
	// 验证输入参数
	if tenantName == "" || username == "" || password == "" {
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "tenant_name, username, and password required"))
	}
	// 验证用户名是否包含命名空间前缀
	if strings.Contains(username, ":") {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "username must not include namespace prefix"))
	}
	// 验证密码登录管理器是否完整
	if m == nil || m.store == nil || m.issuer == nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "password login is not configured"))
	}
	// 验证租户是否存在
	tenantID, err := m.store.LookupTenant(ctx, tenantName)
	if err != nil {
		if errors.Is(err, errTenantNotFound) {
			return nil, statusFromAuthError(newAuthError(ErrCodeTenantNotFound, "tenant not found"))
		}
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "invalid credentials"))
	}

	// 验证用户是否存在
	user, err := m.store.LookupUser(ctx, tenantID, "local:"+username)
	if err != nil {
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "invalid credentials"))
	}
	// 验证用户密码是否正确
	if err := verifyPassword(user.passwordHash, password); err != nil {
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "invalid credentials"))
	}
	// 验证用户状态是否为活动
	if user.status != "active" {
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "invalid credentials"))
	}
	// 加载用户角色
	roles, err := m.store.LoadRoles(ctx, user.id)
	if err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to load roles"))
	}
	// 生成刷新令牌
	rawRefresh, err := generateRefreshToken()
	if err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to issue refresh token"))
	}
	// 插入刷新令牌
	if err := m.store.InsertRefreshToken(ctx, tenantID, user.id, hashRefreshToken(rawRefresh), roles, m.now().Add(defaultRefreshTokenTTL)); err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to issue refresh token"))
	}
	// 生成访问令牌
	accessToken, err := m.issuer.IssueAccessToken(refreshPrincipal{TenantID: tenantID, UserID: user.id, Roles: roles}, defaultAccessTokenTTL)
	if err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to issue access token"))
	}
	// 更新用户最后登录时间
	if err := m.store.TouchLastLogin(ctx, user.id, m.now()); err != nil {
		// last_login_at update is best-effort; do not fail the login.
		_ = err
	}

	return &authv1.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresIn:    int32(defaultAccessTokenTTL.Seconds()),
		IssuedAt:     timestamppb.New(m.now()),
	}, nil
}

// 验证密码是否正确
func verifyPassword(hashed, password string) error {
	if hashed == "" {
		return errInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hashed), []byte(password)); err != nil {
		return errInvalidCredentials
	}
	return nil
}
