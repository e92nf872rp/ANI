package middleware

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// C4：quota-meta 契约即开关 E2E 矩阵。
// 所有用例显式 ANI_AUTH_MODE=auth_service，按认证/授权注错阶段精确断言
// ValidatePrincipal/CheckPermissionV2/legacy RPC 调用次数，冻结 V2 短路位置
// 并证明 generated 请求没有调用 legacy RPC。

const (
	quotaMetaTenantID = "11111111-1111-1111-1111-111111111111"
	quotaMetaUserID   = "22222222-2222-2222-2222-222222222222"
	quotaMetaKeyID    = "33333333-3333-3333-3333-333333333333"
)

type authRPCErrorStage string

const (
	authRPCErrorNone     authRPCErrorStage = ""
	authRPCErrorValidate authRPCErrorStage = "validate_principal"
	authRPCErrorCheck    authRPCErrorStage = "check_permission_v2"
)

type authRPCCallCounts struct {
	ValidatePrincipal int
	CheckPermissionV2 int
	ValidateToken     int
	CheckPermission   int
}

type fakeAuthBehavior struct {
	Allowed    bool
	ErrorStage authRPCErrorStage
	Err        error
}

// testCredential 描述进入请求头的原始凭证。
type testCredential struct {
	bearer   string
	apiKey   string
	hasToken bool
}

func (t testCredential) headers() map[string]string {
	if !t.hasToken {
		return nil
	}
	h := map[string]string{}
	if t.bearer != "" {
		h["Authorization"] = "Bearer " + t.bearer
	}
	if t.apiKey != "" {
		h["X-API-Key"] = t.apiKey
	}
	return h
}

func none() testCredential { return testCredential{} }

func tenantUser() testCredential {
	return testCredential{bearer: "tenant-user-jwt", hasToken: true}
}

func tenantAdmin() testCredential {
	return testCredential{bearer: "tenant-admin-jwt", hasToken: true}
}

func tenantAPIKey() testCredential {
	return testCredential{apiKey: "tenant-api-key", hasToken: true}
}

func platformService() testCredential {
	return testCredential{bearer: "platform-service-jwt", hasToken: true}
}

func platformUser() testCredential {
	return testCredential{bearer: "platform-user-jwt", hasToken: true}
}

func platformAdmin() testCredential {
	return testCredential{bearer: "platform-admin-jwt", hasToken: true}
}

// fakePilotAuthClient 按 credential 返回固定 Principal，并按 ErrorStage 注错。
type fakePilotAuthClient struct {
	behavior fakeAuthBehavior
	// legacyTenant 非 nil 时 legacy ValidateToken 成功（legacy 链路验证用）。
	legacyTenant           *commonv1.TenantContext
	ValidatePrincipalCalls int
	CheckPermissionV2Calls int
	ValidateTokenCalls     int
	CheckPermissionCalls   int
}

var _ AuthClient = (*fakePilotAuthClient)(nil)

func principalForCredential(t testCredential) *authv1.PrincipalContext {
	switch {
	case t.apiKey != "":
		return &authv1.PrincipalContext{
			PrincipalKind:    string(authz.PrincipalAPIKey),
			CredentialScheme: string(authz.CredentialAPIKey),
			CredentialDomain: string(authz.DomainTenant),
			TenantId:         quotaMetaTenantID,
			CredentialId:     quotaMetaKeyID,
		}
	case t.bearer == "platform-service-jwt":
		return &authv1.PrincipalContext{
			PrincipalKind:    string(authz.PrincipalService),
			CredentialScheme: string(authz.CredentialBearer),
			CredentialDomain: string(authz.DomainPlatform),
			SubjectId:        "svc-platform",
		}
	case t.bearer == "platform-user-jwt":
		return &authv1.PrincipalContext{
			PrincipalKind:    string(authz.PrincipalUser),
			CredentialScheme: string(authz.CredentialBearer),
			CredentialDomain: string(authz.DomainPlatform),
			SubjectId:        quotaMetaUserID,
		}
	case t.bearer == "platform-admin-jwt":
		// platform-admin V2 Principal：tenant 为空。
		return &authv1.PrincipalContext{
			PrincipalKind:    string(authz.PrincipalUser),
			CredentialScheme: string(authz.CredentialBearer),
			CredentialDomain: string(authz.DomainPlatform),
			SubjectId:        quotaMetaUserID,
		}
	default:
		// tenant user/admin：V2 权威主体是 user。
		return &authv1.PrincipalContext{
			PrincipalKind:    string(authz.PrincipalUser),
			CredentialScheme: string(authz.CredentialBearer),
			CredentialDomain: string(authz.DomainTenant),
			TenantId:         quotaMetaTenantID,
			SubjectId:        quotaMetaUserID,
		}
	}
}

func (f *fakePilotAuthClient) ValidatePrincipal(_ context.Context, credential string, scheme authz.CredentialScheme) (*authv1.PrincipalContext, error) {
	f.ValidatePrincipalCalls++
	if f.behavior.ErrorStage == authRPCErrorValidate {
		return nil, f.behavior.Err
	}
	// 以 credential 反查 fixture（测试中 token 与 fixture 一一对应）。
	for _, tc := range []testCredential{
		tenantUser(), tenantAdmin(), tenantAPIKey(),
		platformService(), platformUser(), platformAdmin(),
	} {
		if tc.bearer == credential || tc.apiKey == credential {
			return principalForCredential(tc), nil
		}
	}
	return nil, status.Error(codes.Unauthenticated, "unknown credential")
}

func (f *fakePilotAuthClient) CheckPermissionV2(_ context.Context, _ *authv1.AuthorizationRequest) (*authv1.AuthorizationDecision, error) {
	f.CheckPermissionV2Calls++
	if f.behavior.ErrorStage == authRPCErrorCheck {
		return nil, f.behavior.Err
	}
	if !f.behavior.Allowed {
		return &authv1.AuthorizationDecision{Allowed: false, ReasonCode: "PERMISSION_DENIED"}, nil
	}
	return &authv1.AuthorizationDecision{Allowed: true, ReasonCode: "ALLOWED"}, nil
}

func (f *fakePilotAuthClient) ValidateToken(_ context.Context, _ string) (*commonv1.TenantContext, error) {
	f.ValidateTokenCalls++
	if f.legacyTenant != nil {
		return f.legacyTenant, nil
	}
	return nil, errors.New("legacy ValidateToken must not be called for generated routes")
}

func (f *fakePilotAuthClient) CheckPermission(_ context.Context, _ *authv1.CheckPermissionRequest) (*authv1.CheckPermissionResponse, error) {
	f.CheckPermissionCalls++
	if f.legacyTenant != nil {
		return &authv1.CheckPermissionResponse{Allowed: true}, nil
	}
	return nil, errors.New("legacy CheckPermission must not be called for generated routes")
}

func (fakePilotAuthClient) BeginOIDCLogin(context.Context, *authv1.BeginOIDCLoginRequest) (*authv1.BeginOIDCLoginResponse, error) {
	return nil, nil
}

func (fakePilotAuthClient) CompleteOIDCLogin(context.Context, *authv1.CompleteOIDCLoginRequest) (*authv1.TokenPair, error) {
	return nil, nil
}

func (fakePilotAuthClient) Login(context.Context, *authv1.LoginRequest) (*authv1.TokenPair, error) {
	return nil, nil
}

func (fakePilotAuthClient) PlatformPasswordLogin(context.Context, *authv1.PlatformPasswordLoginRequest) (*authv1.TokenPair, error) {
	return nil, nil
}

func (fakePilotAuthClient) RefreshToken(context.Context, string) (*authv1.AccessToken, error) {
	return nil, nil
}

func (fakePilotAuthClient) RevokeToken(context.Context, string) error { return nil }

func (fakePilotAuthClient) CreateAPIKey(context.Context, *authv1.CreateAPIKeyRequest) (*authv1.CreateAPIKeyResponse, error) {
	return nil, nil
}

func (fakePilotAuthClient) ListAPIKeys(context.Context, *authv1.ListAPIKeysRequest) (*authv1.ListAPIKeysResponse, error) {
	return nil, nil
}

func (fakePilotAuthClient) RevokeAPIKey(context.Context, *authv1.RevokeAPIKeyRequest) error {
	return nil
}

// newContractSwitchGateway 构造契约即开关的 gateway：显式 auth_service，
// 无任何 policy env。generated（quota-meta）按契约直通 V2，测试结束自动恢复环境。
func newContractSwitchGateway(t *testing.T, behavior fakeAuthBehavior) (*server.Hertz, *fakePilotAuthClient) {
	t.Helper()
	t.Setenv("ANI_AUTH_MODE", "auth_service")

	fake := &fakePilotAuthClient{behavior: behavior}
	h := server.New()
	h.Use(
		RequestID(),
		ResolveAuthzPolicy(authz.CoreRegistry(), authz.Config{AuthMode: "auth_service"}),
		AuthenticatePrincipal(fake),
		AuthorizePrincipal(fake),
	)
	h.GET("/api/v1/admin/quota-meta", func(ctx context.Context, c *app.RequestContext) {
		c.JSON(http.StatusOK, map[string]any{"ok": true, "now": time.Now().UnixNano()})
	})
	return h, fake
}

// newDevGateway 构造 dev 回落 gateway：无 auth-service，generated 自动回落 legacy。
func newDevGateway(t *testing.T) (*server.Hertz, *fakePilotAuthClient) {
	t.Helper()
	t.Setenv("ANI_AUTH_MODE", "dev")

	fake := &fakePilotAuthClient{
		behavior: fakeAuthBehavior{Allowed: true},
		legacyTenant: &commonv1.TenantContext{
			TenantId: quotaMetaTenantID,
			UserId:   quotaMetaUserID,
			Roles:    []string{"platform-admin"},
			Scope:    "platform",
		},
	}
	h := server.New()
	h.Use(
		RequestID(),
		ResolveAuthzPolicy(authz.CoreRegistry(), authz.Config{AuthMode: "dev"}),
		AuthenticatePrincipal(fake),
		AuthorizePrincipal(fake),
	)
	h.GET("/api/v1/admin/quota-meta", func(ctx context.Context, c *app.RequestContext) {
		c.JSON(http.StatusOK, map[string]any{"ok": true})
	})
	return h, fake
}

func doGET(t *testing.T, h *server.Hertz, path string, headers map[string]string) *protocol.Response {
	t.Helper()
	headersList := make([]ut.Header, 0, len(headers))
	for k, v := range headers {
		headersList = append(headersList, ut.Header{Key: k, Value: v})
	}
	return ut.PerformRequest(h.Engine, http.MethodGet, path, nil, headersList...).Result()
}

func countCalls(fake *fakePilotAuthClient) authRPCCallCounts {
	return authRPCCallCounts{
		ValidatePrincipal: fake.ValidatePrincipalCalls,
		CheckPermissionV2: fake.CheckPermissionV2Calls,
		ValidateToken:     fake.ValidateTokenCalls,
		CheckPermission:   fake.CheckPermissionCalls,
	}
}

func TestQuotaMetaAuthorizationMatrix(t *testing.T) {
	cases := []struct {
		name       string
		credential testCredential
		behavior   fakeAuthBehavior
		wantStatus int
		wantCalls  authRPCCallCounts
	}{
		{
			name:       "no credential",
			credential: none(),
			wantStatus: http.StatusUnauthorized,
			wantCalls:  authRPCCallCounts{},
		},
		{
			name:       "tenant user",
			credential: tenantUser(),
			wantStatus: http.StatusForbidden,
			wantCalls:  authRPCCallCounts{ValidatePrincipal: 1},
		},
		{
			name:       "tenant admin",
			credential: tenantAdmin(),
			wantStatus: http.StatusForbidden,
			wantCalls:  authRPCCallCounts{ValidatePrincipal: 1},
		},
		{
			name:       "tenant api key",
			credential: tenantAPIKey(),
			wantStatus: http.StatusForbidden,
			wantCalls:  authRPCCallCounts{ValidatePrincipal: 1},
		},
		{
			name:       "platform service",
			credential: platformService(),
			wantStatus: http.StatusForbidden,
			wantCalls:  authRPCCallCounts{ValidatePrincipal: 1},
		},
		{
			name:       "platform user no permission",
			credential: platformUser(),
			behavior:   fakeAuthBehavior{Allowed: false},
			wantStatus: http.StatusForbidden,
			wantCalls:  authRPCCallCounts{ValidatePrincipal: 1, CheckPermissionV2: 1},
		},
		{
			name:       "platform admin empty tenant",
			credential: platformAdmin(),
			behavior:   fakeAuthBehavior{Allowed: true},
			wantStatus: http.StatusOK,
			wantCalls:  authRPCCallCounts{ValidatePrincipal: 1, CheckPermissionV2: 1},
		},
		{
			name:       "credential revoked between V2 calls",
			credential: platformAdmin(),
			behavior: fakeAuthBehavior{
				ErrorStage: authRPCErrorCheck,
				Err:        status.Error(codes.Unauthenticated, "revoked"),
			},
			wantStatus: http.StatusUnauthorized,
			wantCalls:  authRPCCallCounts{ValidatePrincipal: 1, CheckPermissionV2: 1},
		},
		{
			name:       "credential rate limited",
			credential: platformAdmin(),
			behavior: fakeAuthBehavior{
				ErrorStage: authRPCErrorValidate,
				Err:        status.Error(codes.ResourceExhausted, "limited"),
			},
			wantStatus: http.StatusTooManyRequests,
			wantCalls:  authRPCCallCounts{ValidatePrincipal: 1},
		},
		{
			name:       "validate auth service unavailable",
			credential: platformAdmin(),
			behavior: fakeAuthBehavior{
				ErrorStage: authRPCErrorValidate,
				Err:        status.Error(codes.Unavailable, "down"),
			},
			wantStatus: http.StatusServiceUnavailable,
			wantCalls:  authRPCCallCounts{ValidatePrincipal: 1},
		},
		{
			name:       "check auth service unavailable",
			credential: platformAdmin(),
			behavior: fakeAuthBehavior{
				ErrorStage: authRPCErrorCheck,
				Err:        status.Error(codes.Unavailable, "down"),
			},
			wantStatus: http.StatusServiceUnavailable,
			wantCalls:  authRPCCallCounts{ValidatePrincipal: 1, CheckPermissionV2: 1},
		},
		{
			name:       "auth service deadline",
			credential: platformAdmin(),
			behavior: fakeAuthBehavior{
				ErrorStage: authRPCErrorCheck,
				Err:        status.Error(codes.DeadlineExceeded, "timeout"),
			},
			wantStatus: http.StatusGatewayTimeout,
			wantCalls:  authRPCCallCounts{ValidatePrincipal: 1, CheckPermissionV2: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gateway, fake := newContractSwitchGateway(t, tc.behavior)
			response := doGET(t, gateway, "/api/v1/admin/quota-meta", tc.credential.headers())
			if response.StatusCode() != tc.wantStatus {
				t.Fatalf("got %d, want %d", response.StatusCode(), tc.wantStatus)
			}
			if gotCalls := countCalls(fake); gotCalls != tc.wantCalls {
				t.Fatalf("RPC calls = %+v, want %+v", gotCalls, tc.wantCalls)
			}
		})
	}
}

// TestQuotaMetaDevFallsBackToLegacy dev 回落验证：ANI_AUTH_MODE=dev 时
// generated（quota-meta）自动回落 legacy，等价原 dev+off 的逐请求行为，
// 本地开发无需任何 policy 配置；dev 旁路不调用任何 auth RPC。
func TestQuotaMetaDevFallsBackToLegacy(t *testing.T) {
	gateway, fake := newDevGateway(t)
	response := doGET(t, gateway, "/api/v1/admin/quota-meta", platformAdmin().headers())
	if response.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode())
	}
	wantCalls := authRPCCallCounts{}
	if gotCalls := countCalls(fake); gotCalls != wantCalls {
		t.Fatalf("RPC calls = %+v, want %+v", gotCalls, wantCalls)
	}
}
