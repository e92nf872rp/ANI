package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIssueAndValidateServiceToken(t *testing.T) {
	svc, validator := newServiceTokenFixture(t)
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	issued, err := svc.IssueServiceToken(context.Background(), &authv1.IssueServiceTokenRequest{
		CallerService: "inference-service",
		CallerSecret:  "mint-secret",
		TenantId:      tenantID.String(),
		Scope:         "scope:platform-workloads:write",
		TtlSeconds:    300,
	})
	if err != nil {
		t.Fatalf("IssueServiceToken: %v", err)
	}
	if issued.GetAccessToken() == "" || issued.GetExpiresIn() != 300 {
		t.Fatalf("issued = %+v", issued)
	}

	claims, err := validator.Validate(context.Background(), issued.GetAccessToken())
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if claims.Principal.TenantID != tenantID.String() || claims.Principal.Kind != "service" || claims.Principal.Domain != "tenant" {
		t.Fatalf("claims = %+v", claims)
	}
	raw := decodeJWTClaims(t, issued.GetAccessToken())
	if aud, ok := raw["aud"].(string); !ok || aud != serviceAudience {
		t.Fatalf("aud = %#v, want %q", raw["aud"], serviceAudience)
	}
	if claims.Legacy.Scope != "scope:platform-workloads:write" {
		t.Fatalf("scope = %q", claims.Legacy.Scope)
	}

	ctx, err := svc.ValidateToken(context.Background(), &authv1.ValidateTokenRequest{Token: issued.GetAccessToken()})
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if ctx.GetTenantId() != tenantID.String() || ctx.GetScope() != "scope:platform-workloads:write" {
		t.Fatalf("tenant context = %+v", ctx)
	}
}

func TestIssueServiceTokenRejectsBadCallerAndScope(t *testing.T) {
	svc, _ := newServiceTokenFixture(t)
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	_, err := svc.IssueServiceToken(context.Background(), &authv1.IssueServiceTokenRequest{
		CallerService: "inference-service",
		CallerSecret:  "wrong",
		TenantId:      tenantID.String(),
		Scope:         "scope:platform-workloads:write",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("bad secret code = %v err = %v", status.Code(err), err)
	}

	_, err = svc.IssueServiceToken(context.Background(), &authv1.IssueServiceTokenRequest{
		CallerService: "model-service",
		CallerSecret:  "mint-secret",
		TenantId:      tenantID.String(),
		Scope:         "scope:platform-workloads:write",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("bad caller code = %v err = %v", status.Code(err), err)
	}

	_, err = svc.IssueServiceToken(context.Background(), &authv1.IssueServiceTokenRequest{
		CallerService: "inference-service",
		CallerSecret:  "mint-secret",
		TenantId:      tenantID.String(),
		Scope:         "tenant",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad scope code = %v err = %v", status.Code(err), err)
	}
}

func TestServiceTokenRejectsMissingAudience(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	issuedAt := time.Unix(1_700_000_000, 0)
	token := signTestJWT(t, key, map[string]any{
		"iss":            "ani-test",
		"principal_kind": "service",
		"tid":            "11111111-1111-1111-1111-111111111111",
		"uid":            serviceActorUserID.String(),
		"scope":          "scope:platform-workloads:write",
		"exp":            issuedAt.Add(time.Hour).Unix(),
		"iat":            issuedAt.Unix(),
	})
	validator, err := NewJWTValidator(JWTConfig{
		PublicKeyPEM: publicKeyPEM(t, &key.PublicKey),
		Issuer:       "ani-test",
	}, nil)
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return issuedAt.Add(time.Minute) }
	if _, err := validator.Validate(context.Background(), token); err == nil {
		t.Fatal("expected missing audience to fail")
	}
}

func TestExistingTenantTokenStillValidWithoutAudience(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tenantID := uuid.New()
	userID := uuid.New()
	issuedAt := time.Unix(1_700_000_000, 0)
	token := signTestJWT(t, key, map[string]any{
		"iss":   "ani-test",
		"sub":   userID.String(),
		"tid":   tenantID.String(),
		"uid":   userID.String(),
		"roles": []string{"tenant-admin"},
		"scope": "tenant",
		"exp":   issuedAt.Add(time.Hour).Unix(),
		"iat":   issuedAt.Unix(),
		"jti":   "jwt-user",
	})
	validator, err := NewJWTValidator(JWTConfig{
		PublicKeyPEM: publicKeyPEM(t, &key.PublicKey),
		Issuer:       "ani-test",
	}, nil)
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return issuedAt.Add(time.Minute) }
	claims, err := validator.Validate(context.Background(), token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if claims.Principal.TenantID != tenantID.String() || claims.Principal.Kind != "user" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestParseMintCredentials(t *testing.T) {
	got := parseMintCredentials("inference-service:mint-secret, other:x")
	if got["inference-service"] != "mint-secret" || got["other"] != "x" {
		t.Fatalf("got = %#v", got)
	}
	if strings.Contains("mint-secret", " ") {
		t.Fatal("unexpected")
	}
}

func newServiceTokenFixture(t *testing.T) (*AuthService, *JWTValidator) {
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
	validator, err := NewJWTValidator(JWTConfig{
		PublicKeyPEM: publicKeyPEM(t, &key.PublicKey),
		Issuer:       "ani-test",
	}, nil)
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	return &AuthService{
		jwt:         validator,
		issuer:      issuer,
		mintSecrets: map[string]string{"inference-service": "mint-secret"},
	}, validator
}
