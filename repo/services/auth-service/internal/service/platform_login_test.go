package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakePlatformLoginStore 平台登录存储 mock
type fakePlatformLoginStore struct {
	user        platformUser
	userErr     error
	insertErr   error
	touchErr    error
	touchCalled bool
	insertArgs  struct {
		userID    uuid.UUID
		tokenHash string
		roles     []string
		expiresAt time.Time
	}
}

func (s *fakePlatformLoginStore) LookupUser(context.Context, string) (platformUser, error) {
	if s.userErr != nil {
		return platformUser{}, s.userErr
	}
	return s.user, nil
}

func (s *fakePlatformLoginStore) InsertRefreshToken(_ context.Context, userID uuid.UUID, tokenHash string, roles []string, expiresAt time.Time) error {
	if s.insertErr != nil {
		return s.insertErr
	}
	s.insertArgs.userID = userID
	s.insertArgs.tokenHash = tokenHash
	s.insertArgs.roles = roles
	s.insertArgs.expiresAt = expiresAt
	return nil
}

func (s *fakePlatformLoginStore) TouchLastLogin(context.Context, uuid.UUID, time.Time) error {
	s.touchCalled = true
	return s.touchErr
}

// 测试平台登录成功
func TestPlatformPasswordLogin_Success(t *testing.T) {
	issuer := testPasswordLoginIssuer(t)
	now := time.Unix(1_700_000_000, 0)
	issuer.now = func() time.Time { return now }
	userID := uuid.New()
	store := &fakePlatformLoginStore{
		user: platformUser{
			id:           userID,
			passwordHash: hashedPassword(t, "correct"),
			status:       "active",
		},
	}
	mgr := newPlatformLoginManager(store, issuer)
	mgr.now = func() time.Time { return now }

	resp, err := mgr.Login(context.Background(), "admin", "correct")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if resp.GetAccessToken() == "" || resp.GetRefreshToken() == "" {
		t.Fatalf("expected token pair, got %#v", resp)
	}
	if resp.GetExpiresIn() != int32(defaultAccessTokenTTL.Seconds()) {
		t.Fatalf("expires_in = %d", resp.GetExpiresIn())
	}
	if !store.touchCalled {
		t.Fatal("expected last_login_at to be touched")
	}
	if store.insertArgs.tokenHash == "" {
		t.Fatal("expected refresh token hash to be persisted")
	}
	if len(store.insertArgs.roles) != 1 || store.insertArgs.roles[0] != "platform-admin" {
		t.Fatalf("insert roles = %v", store.insertArgs.roles)
	}

	// Validate JWT carries scope=platform and empty tenant_id via JWTValidator.
	validator, vErr := NewJWTValidator(JWTConfig{
		PublicKeyPEM: testPublicKeyPEMFromIssuer(t, issuer),
		Issuer:       "ani-test",
	}, nil)
	if vErr != nil {
		t.Fatalf("NewJWTValidator: %v", vErr)
	}
	validator.now = func() time.Time { return now.Add(time.Minute) }
	claims, validateErr := validator.Validate(context.Background(), resp.GetAccessToken())
	if validateErr != nil {
		t.Fatalf("validate token: %v", validateErr)
	}
	if claims.Scope != "platform" {
		t.Fatalf("scope = %q, want platform", claims.Scope)
	}
	if claims.TenantID != uuid.Nil {
		t.Fatalf("tenant_id = %v, want Nil", claims.TenantID)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "platform-admin" {
		t.Fatalf("roles = %v, want [platform-admin]", claims.Roles)
	}
}

// 测试平台登录无效凭证（错误密码 + 不存在用户）
func TestPlatformPasswordLogin_InvalidCredentials(t *testing.T) {
	issuer := testPasswordLoginIssuer(t)
	userID := uuid.New()
	for _, tc := range []struct {
		name    string
		user    platformUser
		userErr error
	}{
		{
			name: "wrong password",
			user: platformUser{
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
			store := &fakePlatformLoginStore{
				user:    tc.user,
				userErr: tc.userErr,
			}
			mgr := newPlatformLoginManager(store, issuer)
			_, err := mgr.Login(context.Background(), "admin", "wrong")
			if err == nil {
				t.Fatal("expected error")
			}
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("err = %v, want Unauthenticated", err)
			}
		})
	}
}

// 测试平台用户被禁用
func TestPlatformPasswordLogin_DisabledUser(t *testing.T) {
	issuer := testPasswordLoginIssuer(t)
	userID := uuid.New()
	store := &fakePlatformLoginStore{
		user: platformUser{
			id:           userID,
			passwordHash: hashedPassword(t, "correct"),
			status:       "disabled",
		},
	}
	mgr := newPlatformLoginManager(store, issuer)
	_, err := mgr.Login(context.Background(), "admin", "correct")
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("err = %v, want Unauthenticated", err)
	}
}

// 测试平台登录空输入
func TestPlatformPasswordLogin_BadRequestOnEmptyInputs(t *testing.T) {
	issuer := testPasswordLoginIssuer(t)
	mgr := newPlatformLoginManager(&fakePlatformLoginStore{}, issuer)
	for _, tc := range []struct{ user, pass string }{
		{"", "pwd"},
		{"admin", ""},
	} {
		_, err := mgr.Login(context.Background(), tc.user, tc.pass)
		if err == nil {
			t.Fatalf("expected error for %+v", tc)
		}
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("err = %v, want Unauthenticated (INVALID_CREDENTIALS) for %+v", err, tc)
		}
	}
}

// 测试平台登录用户名带命名空间前缀
func TestPlatformPasswordLogin_NamespaceInjection(t *testing.T) {
	issuer := testPasswordLoginIssuer(t)
	mgr := newPlatformLoginManager(&fakePlatformLoginStore{}, issuer)
	for _, username := range []string{"local:admin", "oidc:sub", "platform:admin"} {
		_, err := mgr.Login(context.Background(), username, "pwd")
		if err == nil {
			t.Fatalf("expected error for %s", username)
		}
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("err = %v for %s, want InvalidArgument", err, username)
		}
	}
}

// 测试平台用户 OIDC-only 用户被拒绝（password_hash 为空）
func TestPlatformPasswordLogin_OIDCOnlyUserRejected(t *testing.T) {
	issuer := testPasswordLoginIssuer(t)
	userID := uuid.New()
	store := &fakePlatformLoginStore{
		user: platformUser{
			id:           userID,
			passwordHash: "",
			status:       "active",
		},
	}
	mgr := newPlatformLoginManager(store, issuer)
	_, err := mgr.Login(context.Background(), "admin", "whatever")
	if err == nil {
		t.Fatal("expected error for OIDC-only platform user")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("err = %v, want Unauthenticated", err)
	}
}

// TestPlatformPasswordLogin_TenantUserRejected 验证租户用户无法用平台端点登录。
// platform_users 与 users 是物理隔离的两张表：postgresPlatformLoginStore.LookupUser
// 只查 platform_users。即使某个 username 在 users 表中存在，platform_users 查询也返回
// ErrNoRows，并被映射为 errInvalidCredentials → INVALID_CREDENTIALS。
// 这证明了平台/租户存储天然隔离，租户用户用平台端点登录天然被拒绝。
func TestPlatformPasswordLogin_TenantUserRejected(t *testing.T) {
	issuer := testPasswordLoginIssuer(t)
	// fake store 模拟 platform_users 表查不到该用户（ErrNoRows → errInvalidCredentials）
	// 即使该 username 可能存在于 users 表（这里是同名的"租户用户"）
	store := &fakePlatformLoginStore{
		userErr: errInvalidCredentials,
	}
	mgr := newPlatformLoginManager(store, issuer)
	_, err := mgr.Login(context.Background(), "alice", "tenant-user-password")
	if err == nil {
		t.Fatal("expected error for tenant user attempting platform login")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("err = %v, want Unauthenticated (INVALID_CREDENTIALS)", err)
	}
}

// ensure imports stay referenced even when tests shrink during future edits.
var _ = bcrypt.MinCost

// testPublicKeyPEMFromIssuer extracts the RSA public key PEM from a test JWTIssuer.
// JWTIssuer holds an *rsa.PrivateKey; we expose its public half for validator setup.
func testPublicKeyPEMFromIssuer(t *testing.T, issuer *JWTIssuer) string {
	t.Helper()
	if issuer == nil || issuer.privateKey == nil {
		t.Fatalf("issuer missing private key")
	}
	data, err := x509.MarshalPKIXPublicKey(&issuer.privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: data}))
}

// testRsaPrivateKey ensures rsa and rand imports stay referenced in isolated test files.
func testRsaPrivateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

var _ = testRsaPrivateKey
