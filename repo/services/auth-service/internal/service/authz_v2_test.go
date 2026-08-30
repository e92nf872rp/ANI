package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestPermissionsFromScopesFailClosed 校验签名凭证 permissions 的 fail-closed 解析：
// 缺前缀、空 scope、未知格式或非法 resource/action 一律拒绝。
func TestPermissionsFromScopesFailClosed(t *testing.T) {
	invalid := [][]string{
		nil,
		{},
		{"quota:read"},
		{"scope:"},
		{"scope:quota"},
		{"scope:quota:read:extra"},
		{"scope:quota/read"},
	}
	for _, permissions := range invalid {
		if _, err := permissionsFromScopes(permissions, "tenant"); !errors.Is(err, errInvalidPermissionScope) {
			t.Fatalf("permissionsFromScopes(%v) error = %v, want errInvalidPermissionScope", permissions, err)
		}
	}
	got, err := permissionsFromScopes([]string{"scope:quota:read"}, "platform")
	if err != nil {
		t.Fatal(err)
	}
	want := []Permission{{Resource: "quota", Actions: []string{"read"}, Scope: "platform"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("permissions mismatch (-want +got):\n%s", diff)
	}
}

// TestPrincipalDomainAllowsBoundary 冻结 auth-service domain/boundary 不变量：
// tenant 不能进 platform，platform 不能进 tenant/own。
func TestPrincipalDomainAllowsBoundary(t *testing.T) {
	cases := []struct {
		name      string
		domain    string
		tenantID  string
		boundary  string
		wantAllow bool
	}{
		{name: "tenant to tenant", domain: "tenant", tenantID: uuid.NewString(), boundary: "tenant", wantAllow: true},
		{name: "tenant to own", domain: "tenant", tenantID: uuid.NewString(), boundary: "own", wantAllow: true},
		{name: "tenant to platform", domain: "tenant", tenantID: uuid.NewString(), boundary: "platform", wantAllow: false},
		{name: "platform to platform", domain: "platform", tenantID: "", boundary: "platform", wantAllow: true},
		{name: "platform to tenant", domain: "platform", tenantID: "", boundary: "tenant", wantAllow: false},
		{name: "platform to own", domain: "platform", tenantID: "", boundary: "own", wantAllow: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			principal := principalRecord{
				Domain:   tc.domain,
				TenantID: tc.tenantID,
			}

			got := principalDomainAllowsBoundary(
				principal,
				tc.boundary,
			)

			if got != tc.wantAllow {
				t.Fatalf("got %v, want %v", got, tc.wantAllow)
			}
		})
	}
}

// permissionStoreSpy 记录 Allows 调用次数，用于证明 domain 短路发生在权限查询之前。
type permissionStoreSpy struct {
	allowed bool
	calls   int
}

func (s *permissionStoreSpy) Allows(_ context.Context, _ principalRecord, _, _, _ string) (bool, error) {
	s.calls++
	return s.allowed, nil
}

// TestCheckPermissionV2RejectsTenantDomainBeforePermissionLookup 证明即使绕过 Gateway 前置检查，
// auth-service 仍然拒绝跨 domain 请求，且 permission store 根本没有被调用。
func TestCheckPermissionV2RejectsTenantDomainBeforePermissionLookup(t *testing.T) {
	// permissionStoreSpy 记录 Allows 调用次数；
	// allowed=true 模拟数据库意外绑定了 platform-admin 权限。
	permissions := &permissionStoreSpy{
		allowed: true,
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	issuedAt := time.Unix(1_700_000_000, 0)
	tenantID := uuid.New()
	userID := uuid.New()
	token := signTestJWT(t, key, map[string]any{
		"iss":               "ani-test",
		"sub":               userID.String(),
		"tid":               tenantID.String(),
		"uid":               userID.String(),
		"principal_kind":    "user",
		"credential_domain": "tenant",
		"roles":             []string{"tenant-admin"},
		"scope":             "tenant",
		"exp":               issuedAt.Add(time.Hour).Unix(),
		"iat":               issuedAt.Unix(),
		"jti":               "jwt-v2-shortcircuit",
	})

	validator, err := NewJWTValidator(JWTConfig{
		PublicKeyPEM: publicKeyPEM(t, &key.PublicKey),
		Issuer:       "ani-test",
	}, nil)
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return issuedAt.Add(time.Minute) }

	service := &AuthService{
		jwt:         validator,
		permissions: permissions,
	}

	response, err := service.CheckPermissionV2(context.Background(), &authv1.AuthorizationRequest{
		Principal: &authv1.PrincipalContext{
			PrincipalKind:    "user",
			CredentialScheme: "bearer",
			CredentialDomain: "tenant",
			TenantId:         tenantID.String(),
			SubjectId:        userID.String(),
		},
		Resource:         "quota-meta",
		Action:           "read",
		RequiredBoundary: "platform",
		OperationId:      "op-v2-shortcircuit",
		Credential:       token,
		CredentialScheme: "bearer",
	})
	if err != nil {
		t.Fatal(err)
	}

	if response.GetAllowed() {
		t.Fatal("tenant principal must not enter platform boundary")
	}

	if response.GetReasonCode() != "CREDENTIAL_DOMAIN_MISMATCH" {
		t.Fatalf("reason = %q", response.GetReasonCode())
	}

	// 关键断言：permission store 调用次数必须为 0，
	// 证明错误身份域不会进入权限查询。
	if permissions.calls != 0 {
		t.Fatalf("permission store calls = %d, want 0", permissions.calls)
	}
}

// TestCheckPermissionV2AllowsTenantBoundary 验证 tenant user 在 tenant boundary 下
// 权限命中时返回 ALLOWED，未命中时返回 PERMISSION_DENIED。
func TestCheckPermissionV2AllowsTenantBoundary(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	var key *rsa.PrivateKey
	var token string
	var now time.Time

	build := func(allowed bool) (*AuthService, *authv1.AuthorizationRequest) {
		if key == nil {
			var err error
			key, err = rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				t.Fatalf("generate key: %v", err)
			}
			now = time.Unix(1_700_000_000, 0)
			token = signTestJWT(t, key, map[string]any{
				"iss":               "ani-test",
				"sub":               userID.String(),
				"tid":               tenantID.String(),
				"uid":               userID.String(),
				"principal_kind":    "user",
				"credential_domain": "tenant",
				"roles":             []string{"user"},
				"scope":             "tenant",
				"exp":               now.Add(time.Hour).Unix(),
				"iat":               now.Unix(),
				"jti":               "jwt-v2-allow",
			})
		}
		validator, err := NewJWTValidator(JWTConfig{
			PublicKeyPEM: publicKeyPEM(t, &key.PublicKey),
			Issuer:       "ani-test",
		}, nil)
		if err != nil {
			t.Fatalf("NewJWTValidator: %v", err)
		}
		validator.now = func() time.Time { return now.Add(time.Minute) }
		req := &authv1.AuthorizationRequest{
			Principal: &authv1.PrincipalContext{
				PrincipalKind:    "user",
				CredentialScheme: "bearer",
				CredentialDomain: "tenant",
				TenantId:         tenantID.String(),
				SubjectId:        userID.String(),
			},
			Resource:         "instances",
			Action:           "read",
			RequiredBoundary: "tenant",
			OperationId:      "op-v2-allow",
			Credential:       token,
			CredentialScheme: "bearer",
		}
		return &AuthService{jwt: validator, permissions: &permissionStoreSpy{allowed: allowed}}, req
	}

	allowedService, req := build(true)
	resp, err := allowedService.CheckPermissionV2(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckPermissionV2(allowed): %v", err)
	}
	if !resp.GetAllowed() || resp.GetReasonCode() != "ALLOWED" {
		t.Fatalf("decision = %+v", resp)
	}

	deniedService, deniedReq := build(false)
	resp, err = deniedService.CheckPermissionV2(context.Background(), deniedReq)
	if err != nil {
		t.Fatalf("CheckPermissionV2(denied): %v", err)
	}
	if resp.GetAllowed() || resp.GetReasonCode() != "PERMISSION_DENIED" {
		t.Fatalf("decision = %+v", resp)
	}
}

// TestCheckPermissionV2RejectsForgedPrincipal 验证 Gateway 自报 Principal 与
// 重验 Principal 任一字段不一致时返回 PRINCIPAL_MISMATCH。
func TestCheckPermissionV2RejectsForgedPrincipal(t *testing.T) {
	tenantID := uuid.New()
	forgeTenant := uuid.New()
	userID := uuid.New()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	issuedAt := time.Unix(1_700_000_000, 0)
	token := signTestJWT(t, key, map[string]any{
		"iss":               "ani-test",
		"sub":               userID.String(),
		"tid":               tenantID.String(),
		"uid":               userID.String(),
		"principal_kind":    "user",
		"credential_domain": "tenant",
		"roles":             []string{"user"},
		"scope":             "tenant",
		"exp":               issuedAt.Add(time.Hour).Unix(),
		"iat":               issuedAt.Unix(),
		"jti":               "jwt-v2-forge",
	})
	validator, err := NewJWTValidator(JWTConfig{
		PublicKeyPEM: publicKeyPEM(t, &key.PublicKey),
		Issuer:       "ani-test",
	}, nil)
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return issuedAt.Add(time.Minute) }

	service := &AuthService{jwt: validator, permissions: &permissionStoreSpy{allowed: true}}

	forgePrincipal := &authv1.PrincipalContext{
		PrincipalKind:    "user",
		CredentialScheme: "bearer",
		CredentialDomain: "tenant",
		TenantId:         forgeTenant.String(), // 伪造 tenant
		SubjectId:        userID.String(),
	}
	resp, err := service.CheckPermissionV2(context.Background(), &authv1.AuthorizationRequest{
		Principal:        forgePrincipal,
		Resource:         "instances",
		Action:           "read",
		RequiredBoundary: "tenant",
		OperationId:      "op-v2-forge",
		Credential:       token,
		CredentialScheme: "bearer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetAllowed() || resp.GetReasonCode() != "PRINCIPAL_MISMATCH" {
		t.Fatalf("decision = %+v, want PRINCIPAL_MISMATCH", resp)
	}
}

// TestValidatePrincipalBearer 验证 bearer 路径返回 V2 PrincipalContext，
// 且旧 JWT（缺 credential_domain）在 V2 路径 fail closed。
func TestValidatePrincipalBearer(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	issuedAt := time.Unix(1_700_000_000, 0)

	newValidator := func() *JWTValidator {
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

	// V2 JWT：带 credential_domain
	v2Token := signTestJWT(t, key, map[string]any{
		"iss":               "ani-test",
		"sub":               userID.String(),
		"tid":               tenantID.String(),
		"uid":               userID.String(),
		"principal_kind":    "user",
		"credential_domain": "tenant",
		"roles":             []string{"user"},
		"scope":             "tenant",
		"exp":               issuedAt.Add(time.Hour).Unix(),
		"iat":               issuedAt.Unix(),
		"jti":               "jwt-v2-validate",
	})
	service := &AuthService{jwt: newValidator()}
	got, err := service.ValidatePrincipal(context.Background(), &authv1.ValidatePrincipalRequest{
		Credential:       v2Token,
		CredentialScheme: "bearer",
	})
	if err != nil {
		t.Fatalf("ValidatePrincipal: %v", err)
	}
	if got.GetPrincipalKind() != "user" || got.GetCredentialScheme() != "bearer" ||
		got.GetCredentialDomain() != "tenant" || got.GetTenantId() != tenantID.String() ||
		got.GetSubjectId() != userID.String() || got.GetCredentialId() != "" {
		t.Fatalf("principal = %+v", got)
	}

	// 旧 JWT：缺 credential_domain，V2 路径必须拒绝
	legacyToken := signTestJWT(t, key, map[string]any{
		"iss":   "ani-test",
		"sub":   userID.String(),
		"tid":   tenantID.String(),
		"uid":   userID.String(),
		"roles": []string{"user"},
		"scope": "tenant",
		"exp":   issuedAt.Add(time.Hour).Unix(),
		"iat":   issuedAt.Unix(),
		"jti":   "jwt-v2-legacy",
	})
	if _, err := service.ValidatePrincipal(context.Background(), &authv1.ValidatePrincipalRequest{
		Credential:       legacyToken,
		CredentialScheme: "bearer",
	}); err == nil {
		t.Fatal("expected legacy JWT without credential_domain to be rejected by V2")
	}

	// 未知 scheme
	if _, err := service.ValidatePrincipal(context.Background(), &authv1.ValidatePrincipalRequest{
		Credential:       v2Token,
		CredentialScheme: "mtls",
	}); err == nil {
		t.Fatal("expected unsupported scheme to be rejected")
	}
}

// TestValidatePrincipalAPIKeyFormatRejectedAsUnauthenticated 验证格式非法的 API Key
// 在 V2 路径返回 Unauthenticated（HTTP 401），而不是被错误分类为后端不可用（503）。
func TestValidatePrincipalAPIKeyFormatRejectedAsUnauthenticated(t *testing.T) {
	service := &AuthService{apiKeys: newAPIKeyStore(nil, nil)}
	for _, key := range []string{"ani", "ani_dev", "ani_dev_not-a-uuid_secret"} {
		_, err := service.ValidatePrincipal(context.Background(), &authv1.ValidatePrincipalRequest{
			Credential:       key,
			CredentialScheme: "api_key",
		})
		if err == nil {
			t.Fatalf("ValidatePrincipal(%q) error = nil, want rejection", key)
		}
		if got := status.Code(err); got != codes.Unauthenticated {
			t.Fatalf("ValidatePrincipal(%q) code = %v, want Unauthenticated", key, got)
		}
	}
}

// TestValidatePrincipalServiceToken 验证 service JWT 必须携带非空且格式正确的 permissions。
func TestValidatePrincipalServiceToken(t *testing.T) {
	tenantID := uuid.New()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	issuedAt := time.Unix(1_700_000_000, 0)
	newValidator := func() *JWTValidator {
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
	baseClaims := map[string]any{
		"iss":               "ani-test",
		"sub":               "inference-service",
		"aud":               serviceJWTAudience,
		"tid":               tenantID.String(),
		"principal_kind":    "service",
		"credential_domain": "tenant",
		"exp":               issuedAt.Add(time.Hour).Unix(),
		"iat":               issuedAt.Unix(),
		"jti":               "jwt-v2-service",
	}

	// 合法 permissions
	valid := cloneClaims(baseClaims)
	valid["permissions"] = []string{"scope:quota-meta:read"}
	service := &AuthService{jwt: newValidator()}
	got, err := service.ValidatePrincipal(context.Background(), &authv1.ValidatePrincipalRequest{
		Credential:       signTestJWT(t, key, valid),
		CredentialScheme: "bearer",
	})
	if err != nil {
		t.Fatalf("ValidatePrincipal(service): %v", err)
	}
	if got.GetPrincipalKind() != "service" || got.GetSubjectId() != "inference-service" {
		t.Fatalf("principal = %+v", got)
	}

	// 缺 permissions
	noPerm := cloneClaims(baseClaims)
	if _, err := service.ValidatePrincipal(context.Background(), &authv1.ValidatePrincipalRequest{
		Credential:       signTestJWT(t, key, noPerm),
		CredentialScheme: "bearer",
	}); err == nil {
		t.Fatal("expected service token without permissions to be rejected")
	}

	// 非法 permissions 格式
	badPerm := cloneClaims(baseClaims)
	badPerm["permissions"] = []string{"quota:read"}
	if _, err := service.ValidatePrincipal(context.Background(), &authv1.ValidatePrincipalRequest{
		Credential:       signTestJWT(t, key, badPerm),
		CredentialScheme: "bearer",
	}); err == nil {
		t.Fatal("expected service token with invalid permission format to be rejected")
	}
}

func cloneClaims(claims map[string]any) map[string]any {
	out := make(map[string]any, len(claims))
	for k, v := range claims {
		out[k] = v
	}
	return out
}
