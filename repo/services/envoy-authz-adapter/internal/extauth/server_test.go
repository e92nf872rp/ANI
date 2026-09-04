package extauth

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
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

type fakeChecker struct {
	calls     int
	tenant    string
	user      string
	keyID     string
	keyPrefix string
	model     string
	path      string
	requestID string
	stream    bool
	decision  AccessDecision
	err       error
}

func (f *fakeChecker) CheckInferenceAccess(_ context.Context, tenant, user, keyID, keyPrefix, model, path, requestID string, stream bool) (AccessDecision, error) {
	f.calls++
	f.tenant, f.user, f.keyID, f.keyPrefix = tenant, user, keyID, keyPrefix
	f.model, f.path, f.requestID, f.stream = model, path, requestID, stream
	return f.decision, f.err
}

func checkRequest(path string, headers map[string]string) *authv3.CheckRequest {
	return &authv3.CheckRequest{Attributes: &authv3.AttributeContext{
		Request: &authv3.AttributeContext_Request{Http: &authv3.AttributeContext_HttpRequest{
			Path:    path,
			Headers: headers,
		}},
	}}
}

func validHeaders() map[string]string {
	return map[string]string{"authorization": "Bearer " + validAPIKey, "x-ai-eg-model": "ani-qwen3"}
}

func TestCheckDenials(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		headers   map[string]string
		principal *commonv1.TenantContext
		err       error
		wantHTTP  int
		wantCalls int
	}{
		{name: "missing authorization", path: "/v1/chat/completions", headers: map[string]string{"x-ai-eg-model": "ani-qwen3"}, wantHTTP: http.StatusUnauthorized},
		{name: "basic authorization", path: "/v1/chat/completions", headers: map[string]string{"authorization": "Basic abc", "x-ai-eg-model": "ani-qwen3"}, wantHTTP: http.StatusUnauthorized},
		{name: "malformed bearer", path: "/v1/chat/completions", headers: map[string]string{"authorization": "Bearer", "x-ai-eg-model": "ani-qwen3"}, wantHTTP: http.StatusUnauthorized},
		{name: "jwt bearer token", path: "/v1/chat/completions", headers: map[string]string{"authorization": "Bearer eyJhbGciOiJIUzI1NiJ9", "x-ai-eg-model": "ani-qwen3"}, wantHTTP: http.StatusUnauthorized},
		{name: "auth unauthenticated", path: "/v1/chat/completions", headers: validHeaders(), err: status.Error(codes.Unauthenticated, "sensitive auth detail"), wantHTTP: http.StatusUnauthorized, wantCalls: 1},
		{name: "auth rate limited", path: "/v1/chat/completions", headers: validHeaders(), err: status.Error(codes.ResourceExhausted, "sensitive auth detail"), wantHTTP: http.StatusTooManyRequests, wantCalls: 1},
		{name: "auth deadline exceeded", path: "/v1/chat/completions", headers: validHeaders(), err: status.Error(codes.DeadlineExceeded, "sensitive auth detail"), wantHTTP: http.StatusServiceUnavailable, wantCalls: 1},
		{name: "auth unavailable", path: "/v1/chat/completions", headers: validHeaders(), err: status.Error(codes.Unavailable, "sensitive auth detail"), wantHTTP: http.StatusServiceUnavailable, wantCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := &fakeValidator{principal: tt.principal, err: tt.err}
			response, err := New(validator).Check(context.Background(), checkRequest(tt.path, tt.headers))
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

func TestCheckAuthRateLimitPropagatesRetryAfter(t *testing.T) {
	grpcErr, err := status.New(codes.ResourceExhausted, "rate limited").WithDetails(&errdetails.ErrorInfo{
		Reason:   "RATE_LIMITED",
		Metadata: map[string]string{"retry_after_seconds": "42"},
	})
	if err != nil {
		t.Fatalf("WithDetails: %v", err)
	}
	validator := &fakeValidator{err: grpcErr.Err()}
	response, err := New(validator).Check(context.Background(), checkRequest("/v1/chat/completions", validHeaders()))
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	assertDeniedHTTPStatus(t, response, http.StatusTooManyRequests)
	headers := response.GetDeniedResponse().GetHeaders()
	if len(headers) != 1 || headers[0].GetHeader().GetKey() != "retry-after" || headers[0].GetHeader().GetValue() != "42" {
		t.Fatalf("Retry-After headers = %#v, want 42 seconds", headers)
	}
}

func TestCheckAccessMatrix(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		headers     map[string]string
		validateErr error
		decision    AccessDecision
		wantStatus  int
	}{
		{"missing AK", "/v1/chat/completions", map[string]string{"x-ai-eg-model": "ani-qwen3"}, nil, AccessDecision{}, 401},
		{"login JWT", "/v1/chat/completions", map[string]string{"authorization": "Bearer ey.test", "x-ai-eg-model": "ani-qwen3"}, nil, AccessDecision{}, 401},
		{"unknown model", "/v1/chat/completions", validHeaders(), nil, AccessDecision{HTTPStatus: 404}, 404},
		{"policy deny", "/v1/chat/completions", validHeaders(), nil, AccessDecision{HTTPStatus: 403}, 403},
		{"rate limit", "/v1/chat/completions", validHeaders(), nil, AccessDecision{HTTPStatus: 429, RetryAfterSeconds: 7}, 429},
		{"dependency down", "/v1/chat/completions", validHeaders(), nil, AccessDecision{HTTPStatus: 503}, 503},
		{"unexpected policy status fails closed", "/v1/chat/completions", validHeaders(), nil, AccessDecision{HTTPStatus: 418}, 503},
		{"models blocked", "/v1/models", validHeaders(), nil, AccessDecision{}, 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := &fakeValidator{principal: &commonv1.TenantContext{TenantId: "tenant-a", ApiKeyId: "key-a"}, err: tt.validateErr}
			checker := &fakeChecker{decision: tt.decision}
			response, err := New(validator).WithAccessChecker(checker).Check(context.Background(), checkRequest(tt.path, tt.headers))
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			assertDeniedHTTPStatus(t, response, tt.wantStatus)
			if tt.wantStatus == http.StatusUnauthorized || tt.path == "/v1/models" {
				if checker.calls != 0 {
					t.Fatalf("checker calls = %d, want 0", checker.calls)
				}
			}
			if tt.wantStatus == http.StatusTooManyRequests {
				if got := response.GetDeniedResponse().GetHeaders(); len(got) != 1 || got[0].GetHeader().GetKey() != "retry-after" || got[0].GetHeader().GetValue() != "7" {
					t.Fatalf("Retry-After headers = %#v", got)
				}
			}
		})
	}
}

func TestCheckAllowsAuthenticatedPrincipalAndOverwritesSpoofedHeaders(t *testing.T) {
	validator := &fakeValidator{principal: &commonv1.TenantContext{
		TenantId: "tenant-a",
		ApiKeyId: "key-a",
	}}
	checker := &fakeChecker{decision: AccessDecision{HTTPStatus: http.StatusOK, InferenceServiceID: "service-a", LeaseID: "lease-a"}}
	headers := validHeaders()
	headers["x-ani-tenant-id"] = "attacker-tenant"
	headers["x-ani-inference-service-id"] = "attacker-service"
	response, err := New(validator).WithAccessChecker(checker).Check(context.Background(), checkRequest("/v1/chat/completions", headers))
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got := response.GetStatus().GetCode(); got != int32(codes.OK) {
		t.Fatalf("response status = %d, want %d", got, codes.OK)
	}
	if got, want := response.GetOkResponse().GetHeadersToRemove(), []string{"authorization", "x-api-key", "x-ani-user-id"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("HeadersToRemove = %v, want %v", got, want)
	}
	if validator.calls != 1 || validator.token != validAPIKey {
		t.Fatalf("ValidateToken calls/token = %d/%q, want 1/raw AK", validator.calls, validator.token)
	}
	if checker.keyPrefix != validAPIKey[:12] || checker.model != "ani-qwen3" || checker.path != "/v1/chat/completions" || checker.keyID != "key-a" {
		t.Fatalf("checker input = %+v", checker)
	}
	wantHeaders := []*corev3.HeaderValueOption{
		{Header: &corev3.HeaderValue{Key: "x-ani-tenant-id", Value: "tenant-a"}, AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD},
		{Header: &corev3.HeaderValue{Key: "x-ani-inference-service-id", Value: "service-a"}, AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD},
	}
	if got := response.GetOkResponse().GetHeaders(); !reflect.DeepEqual(got, wantHeaders) {
		t.Fatalf("trusted headers = %#v, want %#v", got, wantHeaders)
	}
	if strings.Contains(response.String(), validAPIKey) {
		t.Fatal("success response leaks API key")
	}
	if strings.Contains(response.String(), checker.decision.LeaseID) {
		t.Fatal("success response leaks policy lease ID")
	}
	for _, header := range response.GetOkResponse().GetHeaders() {
		if header.GetHeader().GetValue() == checker.decision.LeaseID {
			t.Fatal("success response injects policy lease ID")
		}
	}
}

func TestCheckFailsClosedForMissingAuthoritativeIdentityOrResolvedService(t *testing.T) {
	tests := []struct {
		name      string
		principal *commonv1.TenantContext
		decision  AccessDecision
	}{
		{name: "empty tenant", principal: &commonv1.TenantContext{ApiKeyId: "key-a"}, decision: AccessDecision{HTTPStatus: http.StatusOK, InferenceServiceID: "service-a"}},
		{name: "whitespace tenant", principal: &commonv1.TenantContext{TenantId: " \t ", ApiKeyId: "key-a"}, decision: AccessDecision{HTTPStatus: http.StatusOK, InferenceServiceID: "service-a"}},
		{name: "empty API key ID", principal: &commonv1.TenantContext{TenantId: "tenant-a"}, decision: AccessDecision{HTTPStatus: http.StatusOK, InferenceServiceID: "service-a"}},
		{name: "whitespace API key ID", principal: &commonv1.TenantContext{TenantId: "tenant-a", ApiKeyId: " \n "}, decision: AccessDecision{HTTPStatus: http.StatusOK, InferenceServiceID: "service-a"}},
		{name: "empty resolved service", principal: &commonv1.TenantContext{TenantId: "tenant-a", ApiKeyId: "key-a"}, decision: AccessDecision{HTTPStatus: http.StatusOK}},
		{name: "whitespace resolved service", principal: &commonv1.TenantContext{TenantId: "tenant-a", ApiKeyId: "key-a"}, decision: AccessDecision{HTTPStatus: http.StatusOK, InferenceServiceID: " \t "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := &fakeValidator{principal: tt.principal}
			checker := &fakeChecker{decision: tt.decision}
			response, err := New(validator).WithAccessChecker(checker).Check(context.Background(), checkRequest("/v1/chat/completions", validHeaders()))
			if err != nil {
				t.Fatal(err)
			}
			assertDeniedHTTPStatus(t, response, http.StatusServiceUnavailable)
			if (tt.name == "empty tenant" || tt.name == "whitespace tenant" || tt.name == "empty API key ID" || tt.name == "whitespace API key ID") && checker.calls != 0 {
				t.Fatalf("checker calls = %d, want 0", checker.calls)
			}
		})
	}
}

func TestCheckIgnoresCallerSuppliedInternalContext(t *testing.T) {
	validator := &fakeValidator{principal: &commonv1.TenantContext{TenantId: "tenant-a", ApiKeyId: "key-a"}}
	checker := &fakeChecker{decision: AccessDecision{HTTPStatus: http.StatusOK, InferenceServiceID: "service-a"}}
	request := checkRequest("/v1/chat/completions?client=value", validHeaders())
	request.Attributes.ContextExtensions = map[string]string{"ani.target_tenant_id": "attacker-tenant", "ani.inference_service_id": "attacker-service"}
	response, err := New(validator).WithAccessChecker(checker).Check(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetStatus().GetCode() != int32(codes.OK) {
		t.Fatalf("response = %#v", response)
	}
	if checker.tenant != "tenant-a" || checker.path != "/v1/chat/completions" {
		t.Fatalf("checker accepted caller context: %+v", checker)
	}
}

func TestCheckNormalizesAuthorizationHeaderNames(t *testing.T) {
	validator := &fakeValidator{principal: &commonv1.TenantContext{TenantId: "tenant-a", ApiKeyId: "key-a"}}
	request := checkRequest("/v1/chat/completions", map[string]string{"Authorization": "Bearer " + validAPIKey, "x-ai-eg-model": "ani-qwen3"})

	response, err := New(validator).WithAccessChecker(&fakeChecker{decision: AccessDecision{HTTPStatus: http.StatusOK, InferenceServiceID: "service-a"}}).Check(context.Background(), request)
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
			request := checkRequest("/v1/chat/completions", headers)
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
	validator := &fakeValidator{principal: &commonv1.TenantContext{TenantId: "tenant-a", ApiKeyId: "key-a"}}
	request := checkRequest("/v1/chat/completions", validHeaders())
	request.GetAttributes().GetRequest().GetHttp().Headers["accept"] = "text/event-stream"

	response, err := New(validator).WithAccessChecker(&fakeChecker{decision: AccessDecision{HTTPStatus: http.StatusOK, InferenceServiceID: "service-a"}}).Check(context.Background(), request)
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
	if denied.GetBody() != "" || (want != http.StatusTooManyRequests && len(denied.GetHeaders()) != 0) {
		t.Fatal("denial response must not expose Auth details")
	}
}
