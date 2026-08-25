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
	// wire 契约：sub 是 V2 服务身份，uid 是 legacy UUID 投影，二者必须分离。
	if raw["sub"] != "inference-service" {
		t.Fatalf("sub = %#v, want inference-service", raw["sub"])
	}
	if raw["uid"] != serviceActorUserID.String() {
		t.Fatalf("uid = %#v, want %s", raw["uid"], serviceActorUserID.String())
	}
	if claims.Legacy.Scope != "scope:platform-workloads:write" {
		t.Fatalf("scope = %q", claims.Legacy.Scope)
	}

	// V2 接口返回服务名作为 SubjectId，供 CheckPermissionV2 授权/审计使用。
	principal, err := svc.ValidatePrincipal(context.Background(), &authv1.ValidatePrincipalRequest{
		Credential:       issued.GetAccessToken(),
		CredentialScheme: "bearer",
	})
	if err != nil {
		t.Fatalf("ValidatePrincipal: %v", err)
	}
	if principal.GetSubjectId() != "inference-service" {
		t.Fatalf("V2 subject = %q, want inference-service", principal.GetSubjectId())
	}

	// 旧接口 UserId 必须是合法 UUID（magic），Gateway 侧 uuid.Parse 才能通过。
	ctx, err := svc.ValidateToken(context.Background(), &authv1.ValidateTokenRequest{Token: issued.GetAccessToken()})
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if ctx.GetTenantId() != tenantID.String() || ctx.GetScope() != "scope:platform-workloads:write" {
		t.Fatalf("tenant context = %+v", ctx)
	}
	if ctx.GetUserId() != serviceActorUserID.String() {
		t.Fatalf("legacy user_id = %q, want %s", ctx.GetUserId(), serviceActorUserID.String())
	}
}

// TestValidateTokenProjectsServiceActorUUIDForNonUUIDSubject 覆盖已流出的错误 token：
// sub=服务名、uid 为空时，V2 仍返回服务名，legacy 投影必须回填 magic UUID。
func TestValidateTokenProjectsServiceActorUUIDForNonUUIDSubject(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	issuedAt := time.Unix(1_700_000_000, 0)
	token := signTestJWT(t, key, map[string]any{
		"iss":               "ani-test",
		"principal_kind":    "service",
		"sub":               "inference-service",
		"tid":               tenantID.String(),
		"aud":               serviceAudience,
		"credential_domain": "tenant",
		"permissions":       []string{"scope:platform-workloads:write"},
		"scope":             "scope:platform-workloads:write",
		"roles":             []string{"service"},
		"exp":               issuedAt.Add(time.Hour).Unix(),
		"iat":               issuedAt.Unix(),
	})
	svc := &AuthService{jwt: mustValidator(t, key, issuedAt)}

	principal, err := svc.ValidatePrincipal(context.Background(), &authv1.ValidatePrincipalRequest{
		Credential:       token,
		CredentialScheme: "bearer",
	})
	if err != nil {
		t.Fatalf("ValidatePrincipal: %v", err)
	}
	if principal.GetSubjectId() != "inference-service" {
		t.Fatalf("V2 subject = %q, want inference-service", principal.GetSubjectId())
	}

	legacy, err := svc.ValidateToken(context.Background(), &authv1.ValidateTokenRequest{Token: token})
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if legacy.GetUserId() != serviceActorUserID.String() {
		t.Fatalf("legacy user_id = %q, want %s", legacy.GetUserId(), serviceActorUserID.String())
	}
	if _, err := uuid.Parse(legacy.GetUserId()); err != nil {
		t.Fatalf("legacy user_id is not a valid UUID: %v", err)
	}
}

func mustValidator(t *testing.T, key *rsa.PrivateKey, issuedAt time.Time) *JWTValidator {
	t.Helper()
	validator, err := NewJWTValidator(JWTConfig{
		PublicKeyPEM: publicKeyPEM(t, &key.PublicKey),
		Issuer:       "ani-test",
	}, nil)
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return issuedAt.Add(time.Minute) }
	return validator
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
