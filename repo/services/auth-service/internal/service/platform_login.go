package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/google/uuid"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
)

// platformLoginStore 数据访问接口（platform_users 表）
type platformLoginStore interface {
	LookupUser(ctx context.Context, username string) (platformUser, error)
	InsertRefreshToken(ctx context.Context, userID uuid.UUID, tokenHash string, roles []string, expiresAt time.Time) error
	TouchLastLogin(ctx context.Context, userID uuid.UUID, at time.Time) error
}

// platformUser 平台管理员结构体
type platformUser struct {
	id           uuid.UUID
	passwordHash string
	status       string
}

// postgresPlatformLoginStore PostgreSQL 平台用户存储实现
type postgresPlatformLoginStore struct {
	db *pgxpool.Pool
}

func newPostgresPlatformLoginStore(db *pgxpool.Pool) *postgresPlatformLoginStore {
	return &postgresPlatformLoginStore{db: db}
}

// LookupUser 根据用户名查询平台用户
func (s *postgresPlatformLoginStore) LookupUser(ctx context.Context, username string) (platformUser, error) {
	var user platformUser
	err := s.db.QueryRow(ctx, `
		SELECT id, password_hash, status
		FROM platform_users
		WHERE username=$1
	`, username).Scan(&user.id, &user.passwordHash, &user.status)
	if errors.Is(err, pgx.ErrNoRows) {
		return platformUser{}, errInvalidCredentials
	}
	if err != nil {
		return platformUser{}, err
	}
	if user.passwordHash == "" {
		// 平台用户不应有空密码（初始化时 bcrypt 设置），防御性返回 INVALID_CREDENTIALS
		return platformUser{}, errInvalidCredentials
	}
	return user, nil
}

// InsertRefreshToken 插入平台 refresh token（tenant_id 为 NULL）
func (s *postgresPlatformLoginStore) InsertRefreshToken(ctx context.Context, userID uuid.UUID, tokenHash string, roles []string, expiresAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO refresh_tokens (tenant_id, user_id, token_hash, roles, expires_at)
		VALUES (NULL, $1, $2, $3, $4)
	`, userID, tokenHash, roles, expiresAt)
	if err != nil {
		return fmt.Errorf("insert platform refresh token: %w", err)
	}
	return nil
}

// TouchLastLogin 更新平台用户最后登录时间
func (s *postgresPlatformLoginStore) TouchLastLogin(ctx context.Context, userID uuid.UUID, at time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE platform_users SET last_login_at=$1, updated_at=$1 WHERE id=$2`, at, userID)
	return err
}

// platformPrincipal 平台 token 主体（无 tenant_id）
type platformPrincipal struct {
	UserID  uuid.UUID
	Roles   []string
}

// platformLoginManager 平台账密登录管理器
type platformLoginManager struct {
	store  platformLoginStore
	issuer *JWTIssuer
	now    func() time.Time
}

// newPlatformLoginManager 构造平台登录管理器
func newPlatformLoginManager(store platformLoginStore, issuer *JWTIssuer) *platformLoginManager {
	return &platformLoginManager{
		store:  store,
		issuer: issuer,
		now:    time.Now,
	}
}

// Login 平台账密登录算法（SPEC §5.1）
func (m *platformLoginManager) Login(ctx context.Context, username, password string) (*authv1.TokenPair, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "username and password required"))
	}
	// 防御性：拒绝包含命名空间前缀的用户名（平台用户不应包含 ":"）
	if strings.Contains(username, ":") {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "username must not include namespace prefix"))
	}
	if m == nil || m.store == nil || m.issuer == nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "platform login is not configured"))
	}
	// 1. 查询平台用户
	user, err := m.store.LookupUser(ctx, username)
	if err != nil {
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "invalid credentials"))
	}
	// 2. 校验密码
	if err := verifyPassword(user.passwordHash, password); err != nil {
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "invalid credentials"))
	}
	// 3. 校验状态
	if user.status != "active" {
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "invalid credentials"))
	}
	// 4. 平台用户固定角色 platform-admin
	roles := []string{"platform-admin"}
	// 5. 生成 refresh token
	rawRefresh, err := generateRefreshToken()
	if err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to issue refresh token"))
	}
	// 6. 持久化 refresh token（tenant_id=NULL）
	if err := m.store.InsertRefreshToken(ctx, user.id, hashRefreshToken(rawRefresh), roles, m.now().Add(defaultRefreshTokenTTL)); err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to issue refresh token"))
	}
	// 7. 签发平台 access token (scope=platform, tenant_id=空, roles=["platform-admin"])
	accessToken, err := m.issuer.IssuePlatformAccessToken(platformPrincipal{UserID: user.id, Roles: roles}, defaultAccessTokenTTL)
	if err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to issue access token"))
	}
	// 8. 更新 last_login_at
	if err := m.store.TouchLastLogin(ctx, user.id, m.now()); err != nil {
		_ = err // best-effort
	}

	return &authv1.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresIn:    int32(defaultAccessTokenTTL.Seconds()),
		IssuedAt:     timestamppb.New(m.now()),
	}, nil
}
