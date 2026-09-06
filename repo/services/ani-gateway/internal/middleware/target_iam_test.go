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
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
	"github.com/kubercloud/ani/services/ani-gateway/internal/targetiam"
	iamv1 "github.com/kubercloud/ani/services/ani-gateway/internal/targetiam/gen"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	lastCheck  *iamv1.CheckPermissionRequest
	response   *iamv1.CheckPermissionResponse
	err        error
}

var _ targetiam.Client = (*targetIAMStub)(nil)

func (s *targetIAMStub) PasswordLogin(context.Context, *iamv1.PasswordLoginRequest) (*iamv1.PasswordLoginResponse, error) {
	panic("password login must not be called by the authorization middleware")
}

func (s *targetIAMStub) CheckPermission(_ context.Context, request *iamv1.CheckPermissionRequest) (*iamv1.CheckPermissionResponse, error) {
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
	if client.lastCheck.GetOperationId() != "listInstances" || client.lastCheck.GetPolicyRevision() != authz.TargetPolicyRevision {
		t.Fatalf("decision identity = %q/%q", client.lastCheck.GetOperationId(), client.lastCheck.GetPolicyRevision())
	}
	if client.lastCheck.GetCredential().GetValue() != token || client.lastCheck.GetTarget().GetTenantId() != targetTenantID {
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
	policyMismatch := status.New(codes.Unavailable, "revision mismatch")
	policyMismatch, err := policyMismatch.WithDetails(&errdetails.ErrorInfo{
		Reason: "AUTHZ_POLICY_MISMATCH",
		Domain: "iam.ani.internal",
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		token      string
		response   *iamv1.CheckPermissionResponse
		err        error
		wantStatus int
		wantCode   string
		wantCalls  int
	}{
		{name: "missing credential", wantStatus: 401, wantCode: "CREDENTIAL_INVALID"},
		{name: "malformed credential", token: "not-a-jwt", wantStatus: 401, wantCode: "CREDENTIAL_INVALID"},
		{name: "denied", token: unsignedTargetJWT(t, targetTenantID), response: deniedTargetDecision(), wantStatus: 403, wantCode: "PERMISSION_DENIED", wantCalls: 1},
		{name: "dependency unavailable", token: unsignedTargetJWT(t, targetTenantID), err: status.Error(codes.Unavailable, "down"), wantStatus: 503, wantCode: "IAM_UNAVAILABLE", wantCalls: 1},
		{name: "policy mismatch", token: unsignedTargetJWT(t, targetTenantID), err: policyMismatch.Err(), wantStatus: 503, wantCode: "AUTHZ_POLICY_MISMATCH", wantCalls: 1},
		{name: "deadline", token: unsignedTargetJWT(t, targetTenantID), err: status.Error(codes.DeadlineExceeded, "late"), wantStatus: 504, wantCode: "IAM_TIMEOUT", wantCalls: 1},
		{name: "missing decision", token: unsignedTargetJWT(t, targetTenantID), response: &iamv1.CheckPermissionResponse{}, wantStatus: 503, wantCode: "IAM_UNAVAILABLE", wantCalls: 1},
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
			if client.checkCalls != tc.wantCalls {
				t.Fatalf("CheckPermission calls = %d, want %d", client.checkCalls, tc.wantCalls)
			}
		})
	}
}

func newTargetIAMTestServer(t *testing.T, client targetiam.Client) *server.Hertz {
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

func allowedTargetDecision() *iamv1.CheckPermissionResponse {
	return &iamv1.CheckPermissionResponse{Decision: &iamv1.AuthorizationDecision{
		Allowed:        true,
		Reason:         "ALLOWED",
		DecisionId:     targetDecisionID,
		PolicyRevision: authz.TargetPolicyRevision,
		Principal: &iamv1.PrincipalContext{
			PrincipalId:     targetPrincipalID,
			PrincipalType:   iamv1.PrincipalType_PRINCIPAL_TYPE_HUMAN,
			PrincipalStatus: iamv1.PrincipalStatus_PRINCIPAL_STATUS_ACTIVE,
			Boundary: &iamv1.Boundary{Boundary: &iamv1.Boundary_Tenant{
				Tenant: &iamv1.TenantBoundary{TenantId: targetTenantID},
			}},
			SessionId:    targetSessionID,
			GrantId:      targetGrantID,
			AuthnMethods: []iamv1.AuthnMethod{iamv1.AuthnMethod_AUTHN_METHOD_PASSWORD},
		},
	}}
}

func deniedTargetDecision() *iamv1.CheckPermissionResponse {
	response := allowedTargetDecision()
	response.Decision.Allowed = false
	response.Decision.Reason = "PERMISSION_DENIED"
	return response
}
