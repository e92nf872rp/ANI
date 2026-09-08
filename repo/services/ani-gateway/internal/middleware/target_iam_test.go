package middleware

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
)

const (
	targetTenantID    = "0198f062-b76d-7f2a-b0ad-50a417bf1f70"
	targetPrincipalID = "0198f062-b76d-7001-9000-000000000001"
	targetSessionID   = "0198f062-b76d-7001-9000-000000000002"
	targetGrantID     = "0198f062-b76d-7001-9000-000000000003"
	targetDecisionID  = "0198f062-b76d-7001-9000-000000000004"
)

type targetIAMStub struct {
	checkCalls int
	lastCheck  ports.TargetIAMCheckPermissionRequest
	response   ports.TargetIAMAuthorizationDecision
	err        error
}

var _ ports.TargetIAM = (*targetIAMStub)(nil)

func (s *targetIAMStub) PasswordLogin(context.Context, ports.TargetIAMPasswordLoginRequest) (ports.TargetIAMPasswordLoginResult, error) {
	panic("password login must not be called by the authorization middleware")
}

func (s *targetIAMStub) CheckPermission(_ context.Context, request ports.TargetIAMCheckPermissionRequest) (ports.TargetIAMAuthorizationDecision, error) {
	s.checkCalls++
	s.lastCheck = request
	return s.response, s.err
}

type legacyMustNotRun struct{ AuthClient }

func TestTargetIAMListInstancesMakesExactlyOneDecisionAndInstallsTrustedContext(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "auth_service")
	client := &targetIAMStub{response: allowedTargetDecision()}
	h := newTargetIAMTestServer(t, client)
	token := unsignedTargetJWT(t, targetTenantID)

	response := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances", nil,
		ut.Header{Key: "Authorization", Value: "Bearer " + token},
		ut.Header{Key: "X-Ani-Tenant-ID", Value: "attacker-tenant"},
		ut.Header{Key: "X-Ani-Principal-ID", Value: "attacker-principal"},
		ut.Header{Key: "X-Ani-Evil", Value: "attacker-value"},
	).Result()

	if response.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.StatusCode(), response.Body())
	}
	if client.checkCalls != 1 {
		t.Fatalf("CheckPermission calls = %d, want 1", client.checkCalls)
	}
	if client.lastCheck.OperationID != "listInstances" || client.lastCheck.PolicyRevision != authz.TargetPolicyRevision {
		t.Fatalf("decision identity = %q/%q", client.lastCheck.OperationID, client.lastCheck.PolicyRevision)
	}
	if client.lastCheck.Credential != token || client.lastCheck.TenantID != targetTenantID {
		t.Fatalf("credential/target was not mapped from the bearer token")
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["tenant_id"] != targetTenantID || body["principal_id"] != targetPrincipalID || body["decision_id"] != targetDecisionID {
		t.Fatalf("trusted context = %#v", body)
	}
	if body["evil"] != "" || strings.Contains(string(response.Body()), "attacker-") {
		t.Fatalf("client x-ani-* survived: %s", response.Body())
	}
}

func TestTargetIAMPublicRouteMakesZeroDecisions(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "auth_service")
	client := &targetIAMStub{response: allowedTargetDecision()}
	h := newTargetIAMTestServer(t, client)

	response := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/branding", nil).Result()
	if response.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.StatusCode(), response.Body())
	}
	if client.checkCalls != 0 {
		t.Fatalf("public route CheckPermission calls = %d, want 0", client.checkCalls)
	}
}

func TestRegisterWithTargetIAMConnectsTheProductionMiddlewareChain(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "auth_service")
	client := &targetIAMStub{response: allowedTargetDecision()}
	h := server.Default(server.WithHostPorts("127.0.0.1:0"))
	if err := RegisterWithTargetIAM(h, newMemoryGatewayStoreForTest(), client); err != nil {
		t.Fatal(err)
	}
	h.GET("/api/v1/instances", func(ctx context.Context, c *app.RequestContext) {
		c.JSON(http.StatusOK, utils.H{"tenant_id": GetTenantID(c)})
	})

	response := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances", nil,
		ut.Header{Key: "Authorization", Value: "Bearer " + unsignedTargetJWT(t, targetTenantID)},
	).Result()
	if response.StatusCode() != http.StatusOK || client.checkCalls != 1 || !strings.Contains(string(response.Body()), targetTenantID) {
		t.Fatalf("production chain status/calls/body = %d/%d/%s", response.StatusCode(), client.checkCalls, response.Body())
	}
}

func TestTargetIAMListInstancesFailsClosed(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "auth_service")
	policyMismatch := ports.NewTargetIAMError(ports.TargetIAMFailureUnavailable, "AUTHZ_POLICY_MISMATCH", nil)
	cases := []struct {
		name       string
		token      string
		response   ports.TargetIAMAuthorizationDecision
		err        error
		wantStatus int
		wantCode   string
		wantCalls  int
	}{
		{name: "missing credential", wantStatus: 401, wantCode: "CREDENTIAL_INVALID"},
		{name: "malformed credential", token: "not-a-jwt", wantStatus: 401, wantCode: "CREDENTIAL_INVALID"},
		{name: "denied", token: unsignedTargetJWT(t, targetTenantID), response: deniedTargetDecision(), wantStatus: 403, wantCode: "PERMISSION_DENIED", wantCalls: 1},
		{name: "dependency unavailable", token: unsignedTargetJWT(t, targetTenantID), err: ports.NewTargetIAMError(ports.TargetIAMFailureUnavailable, "", nil), wantStatus: 503, wantCode: "IAM_UNAVAILABLE", wantCalls: 1},
		{name: "policy mismatch", token: unsignedTargetJWT(t, targetTenantID), err: policyMismatch, wantStatus: 503, wantCode: "AUTHZ_POLICY_MISMATCH", wantCalls: 1},
		{name: "deadline", token: unsignedTargetJWT(t, targetTenantID), err: ports.NewTargetIAMError(ports.TargetIAMFailureDeadlineExceeded, "", nil), wantStatus: 504, wantCode: "IAM_TIMEOUT", wantCalls: 1},
		{name: "missing decision", token: unsignedTargetJWT(t, targetTenantID), response: ports.TargetIAMAuthorizationDecision{}, wantStatus: 503, wantCode: "IAM_UNAVAILABLE", wantCalls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &targetIAMStub{response: tc.response, err: tc.err}
			h := newTargetIAMTestServer(t, client)
			headers := []ut.Header(nil)
			if tc.token != "" {
				headers = append(headers, ut.Header{Key: "Authorization", Value: "Bearer " + tc.token})
			}
			response := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances", nil, headers...).Result()
			if response.StatusCode() != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.StatusCode(), tc.wantStatus, response.Body())
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body["code"] != tc.wantCode {
				t.Fatalf("code = %v, want %s", body["code"], tc.wantCode)
			}
			if len(body) != 3 || body["message"] == "" || body["request_id"] == "" {
				t.Fatalf("stable error body = %#v, want exactly code/message/request_id", body)
			}
			if retryAfter := string(response.Header.Peek("Retry-After")); retryAfter != "" {
				t.Fatalf("Retry-After = %q, want empty for %s", retryAfter, tc.wantCode)
			}
			if client.checkCalls != tc.wantCalls {
				t.Fatalf("CheckPermission calls = %d, want %d", client.checkCalls, tc.wantCalls)
			}
		})
	}
}

func TestTargetIAMListInstancesRateLimitRequiresRetryAfter(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "auth_service")
	client := &targetIAMStub{err: ports.NewTargetIAMError(
		ports.TargetIAMFailureResourceExhausted,
		"AUTH_RATE_LIMITED",
		map[string]string{"retry_after_seconds": "900"},
	)}
	h := newTargetIAMTestServer(t, client)

	response := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances", nil,
		ut.Header{Key: "Authorization", Value: "Bearer " + unsignedTargetJWT(t, targetTenantID)},
	).Result()
	if response.StatusCode() != http.StatusTooManyRequests ||
		string(response.Header.Peek("Retry-After")) != "900" ||
		!strings.Contains(string(response.Body()), `"code":"AUTH_RATE_LIMITED"`) {
		t.Fatalf(
			"rate limited response = %d Retry-After=%q body=%s, want 429/900/AUTH_RATE_LIMITED",
			response.StatusCode(), response.Header.Peek("Retry-After"), response.Body(),
		)
	}
	if client.checkCalls != 1 {
		t.Fatalf("CheckPermission calls = %d, want 1", client.checkCalls)
	}
}

func TestTargetIAMListInstancesMalformedRateLimitFailsClosed(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "auth_service")
	for _, metadata := range []map[string]string{
		nil,
		{"retry_after_seconds": "0"},
		{"retry_after_seconds": "invalid"},
	} {
		client := &targetIAMStub{err: ports.NewTargetIAMError(
			ports.TargetIAMFailureResourceExhausted,
			"AUTH_RATE_LIMITED",
			metadata,
		)}
		h := newTargetIAMTestServer(t, client)

		response := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances", nil,
			ut.Header{Key: "Authorization", Value: "Bearer " + unsignedTargetJWT(t, targetTenantID)},
		).Result()
		if response.StatusCode() != http.StatusServiceUnavailable ||
			string(response.Header.Peek("Retry-After")) != "" ||
			!strings.Contains(string(response.Body()), `"code":"IAM_UNAVAILABLE"`) {
			t.Fatalf(
				"malformed rate limit = %d Retry-After=%q body=%s, want 503/no-header/IAM_UNAVAILABLE",
				response.StatusCode(), response.Header.Peek("Retry-After"), response.Body(),
			)
		}
	}
}

func newTargetIAMTestServer(t *testing.T, client ports.TargetIAM) *server.Hertz {
	t.Helper()
	registry, err := authz.NewTargetOperationRegistry(authz.TargetPolicyRevision)
	if err != nil {
		t.Fatal(err)
	}
	h := server.Default(server.WithHostPorts("127.0.0.1:0"))
	h.Use(
		RequestID(),
		TargetIAMAuthorization(client, registry),
		ResolveAuthzPolicy(authz.CoreRegistry(), authz.ConfigFromEnv()),
		AuthenticatePrincipal(legacyMustNotRun{}),
		AuthorizePrincipal(legacyMustNotRun{}),
	)
	h.GET("/api/v1/instances", func(ctx context.Context, c *app.RequestContext) {
		c.JSON(http.StatusOK, utils.H{
			"tenant_id":    GetTenantID(c),
			"principal_id": string(c.GetHeader("x-ani-principal-id")),
			"decision_id":  string(c.GetHeader("x-ani-decision-id")),
			"evil":         string(c.GetHeader("x-ani-evil")),
		})
	})
	h.GET("/api/v1/branding", func(ctx context.Context, c *app.RequestContext) {
		c.JSON(http.StatusOK, utils.H{"ok": true})
	})
	return h
}

func unsignedTargetJWT(t *testing.T, tenantID string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"tenant_id": tenantID})
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA"}`)) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func allowedTargetDecision() ports.TargetIAMAuthorizationDecision {
	return ports.TargetIAMAuthorizationDecision{
		Present:        true,
		Allowed:        true,
		DecisionID:     targetDecisionID,
		PolicyRevision: authz.TargetPolicyRevision,
		Principal: ports.TargetIAMPrincipal{
			ID:        targetPrincipalID,
			Type:      ports.TargetIAMPrincipalHuman,
			Status:    ports.TargetIAMPrincipalActive,
			Boundary:  ports.TargetIAMBoundary{Type: ports.TargetIAMBoundaryTenant, TenantID: targetTenantID},
			SessionID: targetSessionID,
			GrantID:   targetGrantID,
			AuthnMethods: []ports.TargetIAMAuthnMethod{
				ports.TargetIAMAuthnPassword,
			},
		},
	}
}

func deniedTargetDecision() ports.TargetIAMAuthorizationDecision {
	response := allowedTargetDecision()
	response.Allowed = false
	return response
}
