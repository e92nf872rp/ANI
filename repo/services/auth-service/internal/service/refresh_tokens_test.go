package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/google/uuid"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
)

func TestRefreshTokenIssuesAccessToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	issuedAt := time.Unix(1_700_000_000, 0)
	tenantID := uuid.New()
	userID := uuid.New()
	refreshStore := memoryRefreshStore{
		token: "refresh-1",
		principal: refreshPrincipal{
			TenantID: tenantID,
			UserID:   userID,
			Roles:    []string{"tenant-admin"},
			Scope:    "tenant",
		},
	}
	issuer, err := NewJWTIssuer(JWTConfig{
		PrivateKeyPEM: privateKeyPEM(t, key),
		Issuer:        "ani-test",
	})
	if err != nil {
		t.Fatalf("NewJWTIssuer: %v", err)
	}
	issuer.now = func() time.Time { return issuedAt }
	svc := &AuthService{issuer: issuer, refreshTokens: refreshStore}

	resp, err := svc.RefreshToken(context.Background(), &authv1.RefreshTokenRequest{RefreshToken: "refresh-1"})
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if resp.GetAccessToken() == "" {
		t.Fatal("expected access token")
	}
	if resp.GetExpiresIn() != int32(defaultAccessTokenTTL.Seconds()) {
		t.Fatalf("expires_in = %d", resp.GetExpiresIn())
	}

	validator, err := NewJWTValidator(JWTConfig{
		PublicKeyPEM: publicKeyPEM(t, &key.PublicKey),
		Issuer:       "ani-test",
	}, nil)
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return issuedAt.Add(time.Minute) }
	claims, err := validator.Validate(context.Background(), resp.GetAccessToken())
	if err != nil {
		t.Fatalf("Validate issued access token: %v", err)
	}
	if claims.TenantID != tenantID || claims.UserID != userID {
		t.Fatalf("claims tenant/user = %s/%s", claims.TenantID, claims.UserID)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "tenant-admin" {
		t.Fatalf("roles = %v", claims.Roles)
	}
	if claims.Scope != "tenant" {
		t.Fatalf("scope = %q, want tenant", claims.Scope)
	}
}

// TestRefreshToken_PlatformScopeIssuesPlatformToken 验证平台 refresh token 续期后
// 得到 scope=platform 的 access token。
// P0-1 回归：原 RefreshToken RPC 无条件调用 IssueAccessToken（scope=tenant），
// 平台 refresh token (tenant_id=NULL → principal.TenantID=uuid.Nil) 续期后得到
// scope=tenant + tid=零值 UUID 的 token，被 jwt.go 校验拒绝（tid 零值要求 scope=platform）。
// 修复后：principal.Scope=platform 时分流到 IssuePlatformAccessToken。
func TestRefreshToken_PlatformScopeIssuesPlatformToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	issuedAt := time.Unix(1_700_000_000, 0)
	userID := uuid.New()
	refreshStore := memoryRefreshStore{
		token: "refresh-platform",
		principal: refreshPrincipal{
			TenantID: uuid.Nil,
			UserID:   userID,
			Roles:    []string{"platform-admin"},
			Scope:    "platform",
		},
	}
	issuer, err := NewJWTIssuer(JWTConfig{
		PrivateKeyPEM: privateKeyPEM(t, key),
		Issuer:        "ani-test",
	})
	if err != nil {
		t.Fatalf("NewJWTIssuer: %v", err)
	}
	issuer.now = func() time.Time { return issuedAt }
	svc := &AuthService{issuer: issuer, refreshTokens: refreshStore}

	resp, err := svc.RefreshToken(context.Background(), &authv1.RefreshTokenRequest{RefreshToken: "refresh-platform"})
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if resp.GetAccessToken() == "" {
		t.Fatal("expected access token")
	}

	validator, err := NewJWTValidator(JWTConfig{
		PublicKeyPEM: publicKeyPEM(t, &key.PublicKey),
		Issuer:       "ani-test",
	}, nil)
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return issuedAt.Add(time.Minute) }
	claims, err := validator.Validate(context.Background(), resp.GetAccessToken())
	if err != nil {
		t.Fatalf("Validate issued access token: %v", err)
	}
	if claims.Scope != "platform" {
		t.Fatalf("scope = %q, want platform", claims.Scope)
	}
	if claims.TenantID != uuid.Nil {
		t.Fatalf("tenant_id = %v, want Nil for platform token", claims.TenantID)
	}
	if claims.UserID != userID {
		t.Fatalf("user_id = %v, want %v", claims.UserID, userID)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "platform-admin" {
		t.Fatalf("roles = %v, want [platform-admin]", claims.Roles)
	}
}

type memoryRefreshStore struct {
	token     string
	principal refreshPrincipal
}

func (s memoryRefreshStore) Validate(_ context.Context, rawToken string) (refreshPrincipal, error) {
	if rawToken != s.token {
		return refreshPrincipal{}, errInvalidJWT
	}
	return s.principal, nil
}
