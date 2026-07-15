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
	"google.golang.org/protobuf/types/known/timestamppb"

	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
)

// platformLoginStore 数据访问接口（users 表，通过 user_roles + roles 判定平台管理员）
type platformLoginStore interface {
	LookupUser(ctx context.Context, namespacedUsername string) (platformUser, error)
	LoadRoles(ctx context.Context, userID uuid.UUID) ([]string, error)
	InsertRefreshToken(ctx context.Context, userID uuid.UUID, tokenHash string, roles []string, expiresAt time.Time) error
	TouchLastLogin(ctx context.Context, userID uuid.UUID, at time.Time) error
}

// platformUser 平台管理员结构体（与 passwordUser 共享 users 表，由 user_roles 关联 platform-admin 角色区分）
type platformUser struct {
	id           uuid.UUID
	passwordHash string
	status       string
}

// postgresPlatformLoginStore PostgreSQL 平台用户存储实现
// 平台管理员存于 users 表，username 使用 `local:` 前缀（与租户用户一致），
// 通过 user_roles + roles 关联 platform-admin 角色（roles.tenant_id IS NULL 表示平台内置角色）区分
type postgresPlatformLoginStore struct {
	db *pgxpool.Pool
}

func newPostgresPlatformLoginStore(db *pgxpool.Pool) *postgresPlatformLoginStore {
	return &postgresPlatformLoginStore{db: db}
}

// LookupUser 根据用户名（含 `local:` 前缀）查询平台管理员。
// 判定条件：users 表中存在 user_roles + roles 关联且 r.name='platform-admin' 的用户。
// roles.tenant_id IS NULL 表示平台内置角色（与租户内自定义角色隔离）。
// 注：平台管理员当前存储约定为 users.tenant_id IS NULL（由迁移保证），但查询谓词
// 以 role 判定为准，与存储约定解耦，便于未来调整平台管理员的 tenant_id 取值。
func (s *postgresPlatformLoginStore) LookupUser(ctx context.Context, namespacedUsername string) (platformUser, error) {
	var user platformUser
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.password_hash, u.status
		FROM users u
		WHERE u.username=$1
		  AND EXISTS (
		    SELECT 1
		    FROM user_roles ur
		    JOIN roles r ON r.id = ur.role_id
		    WHERE ur.user_id = u.id
		      AND r.name='platform-admin'
		      AND r.tenant_id IS NULL
		  )
	`, namespacedUsername).Scan(&user.id, &user.passwordHash, &user.status)
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

// LoadRoles 根据用户ID查询平台管理员角色（user_roles + roles，roles.tenant_id IS NULL 表示平台内置角色）
func (s *postgresPlatformLoginStore) LoadRoles(ctx context.Context, userID uuid.UUID) ([]string, error) {
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

// InsertRefreshToken 插入平台 refresh token（tenant_id 为 NULL，user_id 引用 users.id）
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

// TouchLastLogin 更新平台管理员最后登录时间（users 表，与租户用户共用）
func (s *postgresPlatformLoginStore) TouchLastLogin(ctx context.Context, userID uuid.UUID, at time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE users SET last_login_at=$1, updated_at=$1 WHERE id=$2`, at, userID)
	return err
}

// platformPrincipal 平台 token 主体（无 tenant_id）
type platformPrincipal struct {
	UserID uuid.UUID
	Roles  []string
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
	// 防御性：拒绝包含命名空间前缀的用户名（用户不应输入 `local:`/`oidc:` 等前缀）
	if strings.Contains(username, ":") {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "username must not include namespace prefix"))
	}
	if m == nil || m.store == nil || m.issuer == nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "platform login is not configured"))
	}
	// 1. 查询平台管理员（users WHERE username='local:'+username AND EXISTS user_roles→roles.name='platform-admin'）
	// 平台管理员与租户用户统一使用 `local:` 前缀，由 user_roles 角色绑定区分
	user, err := m.store.LookupUser(ctx, "local:"+username)
	if err != nil {
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "user not found"))
	}
	// 2. 校验密码
	if err := verifyPassword(user.passwordHash, password); err != nil {
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "password error"))
	}
	// 3. 校验状态
	if user.status != "active" {
		return nil, statusFromAuthError(newAuthError(ErrCodeInvalidCredentials, "user inactive"))
	}
	// 4. 加载平台管理员角色（user_roles + roles，roles.tenant_id IS NULL）
	roles, err := m.store.LoadRoles(ctx, user.id)
	if err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to load roles"))
	}
	// 5. 生成 refresh token
	rawRefresh, err := generateRefreshToken()
	if err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to generate refresh token"))
	}
	// 6. 持久化 refresh token（tenant_id=NULL）
	if err := m.store.InsertRefreshToken(ctx, user.id, hashRefreshToken(rawRefresh), roles, m.now().Add(defaultRefreshTokenTTL)); err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to insert refresh token"))
	}
	// 7. 签发平台 access token (scope=platform, tenant_id=空, roles 来自 user_roles)
	accessToken, err := m.issuer.IssuePlatformAccessToken(platformPrincipal{UserID: user.id, Roles: roles}, defaultAccessTokenTTL)
	if err != nil {
		return nil, statusFromAuthError(newAuthError("BAD_REQUEST", "failed to generate access token"))
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
