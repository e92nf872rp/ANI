package extauth

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const validAPIKey = "ani_test_secret_value"

type fakeValidator struct {
	calls     int
	token     string
	principal *commonv1.TenantContext
	err       error
}

func (f *fakeValidator) ValidateToken(_ context.Context, token string) (*commonv1.TenantContext, error) {
	f.calls++
	f.token = token
	return f.principal, f.err
}

func checkRequest(authHeader, tenantID, serviceID string) *authv3.CheckRequest {
	return &authv3.CheckRequest{Attributes: &authv3.AttributeContext{
		Request: &authv3.AttributeContext_Request{Http: &authv3.AttributeContext_HttpRequest{
			Headers: map[string]string{"authorization": authHeader},
		}},
		ContextExtensions: map[string]string{
			"ani.target_tenant_id":     tenantID,
			"ani.inference_service_id": serviceID,
		},
	}}
}

func TestCheckDenials(t *testing.T) {
	tests := []struct {
		name      string
		request   *authv3.CheckRequest
		principal *commonv1.TenantContext
		err       error
		wantHTTP  int
		wantCalls int
	}{
		{name: "missing authorization", request: checkRequest("", "tenant-a", "service-a"), wantHTTP: http.StatusUnauthorized},
		{name: "basic authorization", request: checkRequest("Basic abc", "tenant-a", "service-a"), wantHTTP: http.StatusUnauthorized},
		{name: "malformed bearer", request: checkRequest("Bearer", "tenant-a", "service-a"), wantHTTP: http.StatusUnauthorized},
		{name: "jwt bearer token", request: checkRequest("Bearer eyJhbGciOiJIUzI1NiJ9", "tenant-a", "service-a"), wantHTTP: http.StatusUnauthorized},
		{name: "missing target tenant", request: checkRequest("Bearer "+validAPIKey, "", "service-a"), wantHTTP: http.StatusServiceUnavailable},
		{name: "missing inference service", request: checkRequest("Bearer "+validAPIKey, "tenant-a", ""), wantHTTP: http.StatusServiceUnavailable},
		{name: "auth unauthenticated", request: checkRequest("Bearer "+validAPIKey, "tenant-a", "service-a"), err: status.Error(codes.Unauthenticated, "sensitive auth detail"), wantHTTP: http.StatusUnauthorized, wantCalls: 1},
		{name: "auth rate limited", request: checkRequest("Bearer "+validAPIKey, "tenant-a", "service-a"), err: status.Error(codes.ResourceExhausted, "sensitive auth detail"), wantHTTP: http.StatusTooManyRequests, wantCalls: 1},
		{name: "auth deadline exceeded", request: checkRequest("Bearer "+validAPIKey, "tenant-a", "service-a"), err: status.Error(codes.DeadlineExceeded, "sensitive auth detail"), wantHTTP: http.StatusServiceUnavailable, wantCalls: 1},
		{name: "auth unavailable", request: checkRequest("Bearer "+validAPIKey, "tenant-a", "service-a"), err: status.Error(codes.Unavailable, "sensitive auth detail"), wantHTTP: http.StatusServiceUnavailable, wantCalls: 1},
		{name: "tenant mismatch", request: checkRequest("Bearer "+validAPIKey, "tenant-a", "service-a"), principal: &commonv1.TenantContext{TenantId: "tenant-b"}, wantHTTP: http.StatusNotFound, wantCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := &fakeValidator{principal: tt.principal, err: tt.err}
			response, err := New(validator).Check(context.Background(), tt.request)
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			assertDeniedHTTPStatus(t, response, tt.wantHTTP)
			if validator.calls != tt.wantCalls {
				t.Fatalf("ValidateToken calls = %d, want %d", validator.calls, tt.wantCalls)
			}
			if strings.Contains(response.String(), validAPIKey) || strings.Contains(response.String(), "sensitive auth detail") {
				t.Fatal("denial response leaks a token or Auth error detail")
			}
		})
	}
}

func TestCheckAllowsMatchingTenantWithoutScopeEnforcementAndStripsCredentials(t *testing.T) {
	validator := &fakeValidator{principal: &commonv1.TenantContext{
		TenantId: "tenant-a",
		Roles:    []string{"scope:models:read"},
	}}
	response, err := New(validator).Check(context.Background(), checkRequest("Bearer "+validAPIKey, "tenant-a", "service-a"))
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got := response.GetStatus().GetCode(); got != int32(codes.OK) {
		t.Fatalf("response status = %d, want %d", got, codes.OK)
	}
	if got, want := response.GetOkResponse().GetHeadersToRemove(), []string{"authorization", "x-api-key", "x-ani-tenant-id", "x-ani-user-id"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("HeadersToRemove = %v, want %v", got, want)
	}
	if validator.calls != 1 || validator.token != validAPIKey {
		t.Fatalf("ValidateToken calls/token = %d/%q, want 1/raw AK", validator.calls, validator.token)
	}
	if strings.Contains(response.String(), validAPIKey) {
		t.Fatal("success response leaks API key")
	}
}

func TestCheckNormalizesAuthorizationHeaderNames(t *testing.T) {
	validator := &fakeValidator{principal: &commonv1.TenantContext{TenantId: "tenant-a"}}
	request := checkRequest("", "tenant-a", "service-a")
	request.GetAttributes().GetRequest().GetHttp().Headers = map[string]string{"Authorization": "Bearer " + validAPIKey}

	response, err := New(validator).Check(context.Background(), request)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got := response.GetStatus().GetCode(); got != int32(codes.OK) {
		t.Fatalf("response status = %d, want %d", got, codes.OK)
	}
	if validator.calls != 1 || validator.token != validAPIKey {
		t.Fatalf("ValidateToken calls/token = %d/%q, want 1/raw AK", validator.calls, validator.token)
	}
}

func TestCheckIgnoresCredentialsOutsideAuthorization(t *testing.T) {
	for name, headers := range map[string]map[string]string{
		"x-api-key": {"x-api-key": "ani_dev_not_a_real_key"},
		"cookie":    {"cookie": "api_key=ani_dev_not_a_real_key"},
		"query":     {":path": "/v1/chat/completions?api_key=ani_dev_not_a_real_key"},
	} {
		t.Run(name, func(t *testing.T) {
			validator := &fakeValidator{}
			request := checkRequest("", "tenant-a", "service-a")
			request.Attributes.Request.Http.Headers = headers
			response, err := New(validator).Check(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if got := response.GetDeniedResponse().GetStatus().GetCode(); got != typev3.StatusCode(http.StatusUnauthorized) {
				t.Fatalf("status = %v, want 401", got)
			}
			if validator.calls != 0 {
				t.Fatalf("validator calls = %d, want 0", validator.calls)
			}
		})
	}
}

func TestCheckSSECallsValidatorOnce(t *testing.T) {
	validator := &fakeValidator{principal: &commonv1.TenantContext{TenantId: "tenant-a"}}
	request := checkRequest("Bearer "+validAPIKey, "tenant-a", "service-a")
	request.GetAttributes().GetRequest().GetHttp().Path = "/v1/chat/completions"
	request.GetAttributes().GetRequest().GetHttp().Headers["accept"] = "text/event-stream"

	response, err := New(validator).Check(context.Background(), request)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got := response.GetStatus().GetCode(); got != int32(codes.OK) {
		t.Fatalf("response status = %d, want %d", got, codes.OK)
	}
	if validator.calls != 1 {
		t.Fatalf("ValidateToken calls = %d, want one authorization check for SSE", validator.calls)
	}
}

func assertDeniedHTTPStatus(t *testing.T, response *authv3.CheckResponse, want int) {
	t.Helper()
	if response.GetStatus() == nil {
		t.Fatal("response is missing google.rpc.Status")
	}
	if got := response.GetStatus().GetCode(); got != int32(codes.PermissionDenied) {
		t.Fatalf("google.rpc.Status code = %d, want %d", got, codes.PermissionDenied)
	}
	denied := response.GetDeniedResponse()
	if denied == nil || denied.GetStatus() == nil {
		t.Fatal("response is missing denied HTTP status")
	}
	if got := denied.GetStatus().GetCode(); got != typev3.StatusCode(want) {
		t.Fatalf("denied HTTP status = %d, want %d", got, want)
	}
	if denied.GetBody() != "" || len(denied.GetHeaders()) != 0 {
		t.Fatal("denial response must not expose Auth details")
	}
}
