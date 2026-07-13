package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/kubercloud/ani/pkg/ports"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// 创建测试用的JWTIssuer
func testPasswordLoginIssuer(t *testing.T) *JWTIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	issuer, err := NewJWTIssuer(JWTConfig{
		PrivateKeyPEM: privateKeyPEM(t, key),
		Issuer:        "ani-test",
	})
	if err != nil {
		t.Fatalf("NewJWTIssuer: %v", err)
	}
	return issuer
}

// 对密码进行哈希处理
func hashedPassword(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	return string(hash)
}

// 创建测试用的结构体
type fakePasswordLoginStore struct {
	tenantID    uuid.UUID
	user        passwordUser
	roles       []string
	tenantErr   error
	userErr     error
	rolesErr    error
	insertErr   error
	touchErr    error
	touchCalled bool
	insertArgs  struct {
		tenantID  uuid.UUID
		userID    uuid.UUID
		tokenHash string
		roles     []string
		expiresAt time.Time
	}
}

// 实现LookupTenant方法
func (s *fakePasswordLoginStore) LookupTenant(context.Context, string) (uuid.UUID, error) {
	if s.tenantErr != nil {
		return uuid.Nil, s.tenantErr
	}
	return s.tenantID, nil
}

// 实现LookupUser方法
func (s *fakePasswordLoginStore) LookupUser(context.Context, uuid.UUID, string) (passwordUser, error) {
	if s.userErr != nil {
		return passwordUser{}, s.userErr
	}
	return s.user, nil
}

// 实现LoadRoles方法
func (s *fakePasswordLoginStore) LoadRoles(context.Context, uuid.UUID) ([]string, error) {
	if s.rolesErr != nil {
		return nil, s.rolesErr
	}
	return s.roles, nil
}

// 实现InsertRefreshToken方法
func (s *fakePasswordLoginStore) InsertRefreshToken(_ context.Context, tenantID, userID uuid.UUID, tokenHash string, roles []string, expiresAt time.Time) error {
	if s.insertErr != nil {
		return s.insertErr
	}
	s.insertArgs.tenantID = tenantID
	s.insertArgs.userID = userID
	s.insertArgs.tokenHash = tokenHash
	s.insertArgs.roles = roles
	s.insertArgs.expiresAt = expiresAt
	return nil
}

// 实现TouchLastLogin方法
func (s *fakePasswordLoginStore) TouchLastLogin(context.Context, uuid.UUID, time.Time) error {
	s.touchCalled = true
	return s.touchErr
}

// 测试成功登录
func TestPasswordLogin_Success(t *testing.T) {
	issuer := testPasswordLoginIssuer(t)
	now := time.Unix(1_700_000_000, 0)
	issuer.now = func() time.Time { return now }
	tenantID := uuid.New()
	userID := uuid.New()
	store := &fakePasswordLoginStore{
		tenantID: tenantID,
		user: passwordUser{
			id:           userID,
			passwordHash: hashedPassword(t, "correct"),
			status:       "active",
		},
		roles: []string{"tenant-admin"},
	}
	mgr := newPasswordLoginManager(store, issuer)
	mgr.now = func() time.Time { return now }

	resp, err := mgr.Login(context.Background(), "tenant-a", "alice", "correct")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if resp.GetAccessToken() == "" || resp.GetRefreshToken() == "" {
		t.Fatalf("expected token pair, got %#v", resp)
	}
	if resp.GetExpiresIn() != int32(defaultAccessTokenTTL.Seconds()) {
		t.Fatalf("expires_in = %d", resp.GetExpiresIn())
	}
	if resp.GetIssuedAt().AsTime().UTC() != now.UTC() {
		t.Fatalf("issued_at = %v, want %v", resp.GetIssuedAt().AsTime(), now)
	}
	if !store.touchCalled {
		t.Fatal("expected last_login_at to be touched")
	}
	if store.insertArgs.tokenHash == "" {
		t.Fatal("expected refresh token hash to be persisted")
	}
	if len(store.insertArgs.roles) != 1 || store.insertArgs.roles[0] != "tenant-admin" {
		t.Fatalf("insert roles = %v", store.insertArgs.roles)
	}
}

// 测试无效登录
func TestPasswordLogin_InvalidCredentials(t *testing.T) {
	issuer := testPasswordLoginIssuer(t)
	tenantID := uuid.New()
	userID := uuid.New()
	for _, tc := range []struct {
		name    string
		user    passwordUser
		userErr error
	}{
		{
			name: "wrong password",
			user: passwordUser{
				id:           userID,
				passwordHash: hashedPassword(t, "different"),
				status:       "active",
			},
		},
		{
			name:    "no such user",
			userErr: errInvalidCredentials,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakePasswordLoginStore{
				tenantID: tenantID,
				user:     tc.user,
				userErr:  tc.userErr,
			}
			mgr := newPasswordLoginManager(store, issuer)
			_, err := mgr.Login(context.Background(), "tenant-a", "alice", "wrong")
			if err == nil {
				t.Fatal("expected error")
			}
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("err = %v, want Unauthenticated", err)
			}
		})
	}
}

// 测试租户不存在
func TestPasswordLogin_TenantNotFound(t *testing.T) {
	issuer := testPasswordLoginIssuer(t)
	store := &fakePasswordLoginStore{tenantErr: errTenantNotFound}
	mgr := newPasswordLoginManager(store, issuer)
	_, err := mgr.Login(context.Background(), "missing-tenant", "alice", "pwd")
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.NotFound {
		t.Fatalf("err = %v, want NotFound", err)
	}
}

// 测试命名空间注入
func TestPasswordLogin_NamespaceInjection(t *testing.T) {
	issuer := testPasswordLoginIssuer(t)
	mgr := newPasswordLoginManager(&fakePasswordLoginStore{}, issuer)
	for _, username := range []string{"local:alice", "oidc:sub", "platform:admin"} {
		_, err := mgr.Login(context.Background(), "tenant-a", username, "pwd")
		if err == nil {
			t.Fatalf("expected error for %s", username)
		}
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("err = %v for %s, want InvalidArgument", err, username)
		}
	}
}

// 测试禁用用户
func TestPasswordLogin_DisabledUser(t *testing.T) {
	issuer := testPasswordLoginIssuer(t)
	tenantID := uuid.New()
	userID := uuid.New()
	store := &fakePasswordLoginStore{
		tenantID: tenantID,
		user: passwordUser{
			id:           userID,
			passwordHash: hashedPassword(t, "correct"),
			status:       "disabled",
		},
		roles: []string{"tenant-admin"},
	}
	mgr := newPasswordLoginManager(store, issuer)
	_, err := mgr.Login(context.Background(), "tenant-a", "alice", "correct")
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("err = %v, want Unauthenticated", err)
	}
}

// 测试OIDC用户拒绝拒绝
func TestPasswordLogin_OIDCUserRejected(t *testing.T) {
	issuer := testPasswordLoginIssuer(t)
	tenantID := uuid.New()
	userID := uuid.New()
	store := &fakePasswordLoginStore{
		tenantID: tenantID,
		user: passwordUser{
			id:           userID,
			passwordHash: "", // OIDC-only user has NULL password_hash
			status:       "active",
		},
		roles: []string{"tenant-admin"},
	}
	mgr := newPasswordLoginManager(store, issuer)
	_, err := mgr.Login(context.Background(), "tenant-a", "alice", "whatever")
	if err == nil {
		t.Fatal("expected error for OIDC-only user")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("err = %v, want Unauthenticated", err)
	}
}

// TestTenantPasswordLogin_PlatformUserRejected 验证平台用户无法用租户端点登录。
// users 表通过 tenant_id NOT NULL 约束保证只能存租户用户（init_schema.sql:52）。
// 平台管理员存储在独立的 platform_users 表中，与 users 物理隔离。
// 即使某个 username 在 platform_users 中存在，users 表查询也返回 ErrNoRows，
// 被映射为 errInvalidCredentials → INVALID_CREDENTIALS。
// 这是平台/租户存储天然隔离的安全保证。
func TestTenantPasswordLogin_PlatformUserRejected(t *testing.T) {
	issuer := testPasswordLoginIssuer(t)
	tenantID := uuid.New()
	// fake store 模拟 users 表查不到该用户（ErrNoRows → errInvalidCredentials）
	// 即使该 username 可能存在于 platform_users 表（这里是同名的"平台用户"）
	store := &fakePasswordLoginStore{
		tenantID: tenantID,
		userErr:  errInvalidCredentials,
	}
	mgr := newPasswordLoginManager(store, issuer)
	_, err := mgr.Login(context.Background(), "tenant-a", "admin", "platform-user-password")
	if err == nil {
		t.Fatal("expected error for platform user attempting tenant login")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("err = %v, want Unauthenticated (INVALID_CREDENTIALS)", err)
	}
}

// 测试空输入错误
func TestPasswordLogin_BadRequestOnEmptyInputs(t *testing.T) {
	issuer := testPasswordLoginIssuer(t)
	mgr := newPasswordLoginManager(&fakePasswordLoginStore{}, issuer)
	for _, tc := range []struct{ tenant, user, pass string }{
		{"", "alice", "pwd"},
		{"tenant-a", "", "pwd"},
		{"tenant-a", "alice", ""},
	} {
		_, err := mgr.Login(context.Background(), tc.tenant, tc.user, tc.pass)
		if err == nil {
			t.Fatalf("expected error for %+v", tc)
		}
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("err = %v, want Unauthenticated (INVALID_CREDENTIALS) for %+v", err, tc)
		}
	}
}

// brokenCache returns errors on every operation, simulating Redis down.
// Retained for reuse by other test files (e.g. oidc). passwordLogin no longer
// touches cache after lockout removal, so this is not exercised by passwordLogin tests.
type brokenCache struct {
	mu sync.Mutex
}

var _ ports.CacheStore = (*brokenCache)(nil)

func (b *brokenCache) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("redis down")
}
func (b *brokenCache) Set(context.Context, string, []byte, time.Duration) error {
	return errors.New("redis down")
}
func (b *brokenCache) SetNX(context.Context, string, []byte, time.Duration) (bool, error) {
	return false, errors.New("redis down")
}
func (b *brokenCache) Delete(context.Context, string) error { return errors.New("redis down") }
func (b *brokenCache) Increment(context.Context, string, time.Duration) (int64, error) {
	return 0, errors.New("redis down")
}
func (b *brokenCache) Exists(context.Context, string) (bool, error) {
	return false, errors.New("redis down")
}

// ensure imports stay referenced even when tests shrink during future edits.
var _ = timestamppb.Now
var _ = strconv.Itoa
var _ = pgx.ErrNoRows
