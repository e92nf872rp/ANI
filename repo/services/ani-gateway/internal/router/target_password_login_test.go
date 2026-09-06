package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	"github.com/kubercloud/ani/services/ani-gateway/internal/targetiam"
	iamv1 "github.com/kubercloud/ani/services/ani-gateway/internal/targetiam/gen"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	targetLoginTenantID    = "0198f062-b76d-7f2a-b0ad-50a417bf1f70"
	targetLoginPrincipalID = "0198f062-b76d-7001-9000-000000000001"
	targetLoginSessionID   = "0198f062-b76d-7001-9000-000000000002"
	targetLoginGrantID     = "0198f062-b76d-7001-9000-000000000003"
)

type targetLoginStub struct {
	request *iamv1.PasswordLoginRequest
	err     error
}

var _ targetiam.Client = (*targetLoginStub)(nil)

func (s *targetLoginStub) PasswordLogin(_ context.Context, request *iamv1.PasswordLoginRequest) (*iamv1.PasswordLoginResponse, error) {
	s.request = request
	if s.err != nil {
		return nil, s.err
	}
	createdAt := time.Date(2026, 9, 4, 8, 0, 0, 0, time.UTC)
	boundary := &iamv1.Boundary{Boundary: &iamv1.Boundary_Tenant{Tenant: &iamv1.TenantBoundary{TenantId: targetLoginTenantID}}}
	grant := &iamv1.SessionGrantSummary{
		GrantId:  targetLoginGrantID,
		Boundary: boundary,
		Version:  1,
		Status:   iamv1.GrantStatus_GRANT_STATUS_ACTIVE,
	}
	return &iamv1.PasswordLoginResponse{
		AccessToken:      "target-access-token",
		ExpiresInSeconds: 900,
		RefreshToken:     "target-refresh-secret",
		RefreshExpiresAt: timestamppb.New(createdAt.Add(30 * 24 * time.Hour)),
		Principal: &iamv1.PrincipalContext{
			PrincipalId:     targetLoginPrincipalID,
			PrincipalType:   iamv1.PrincipalType_PRINCIPAL_TYPE_HUMAN,
			PrincipalStatus: iamv1.PrincipalStatus_PRINCIPAL_STATUS_ACTIVE,
			Boundary:        boundary,
			SessionId:       targetLoginSessionID,
			GrantId:         targetLoginGrantID,
			AuthnMethods:    []iamv1.AuthnMethod{iamv1.AuthnMethod_AUTHN_METHOD_PASSWORD},
		},
		Session: &iamv1.SessionSummary{
			SessionId:         targetLoginSessionID,
			Status:            iamv1.SessionStatus_SESSION_STATUS_ACTIVE,
			Grants:            []*iamv1.SessionGrantSummary{grant},
			AuthnMethods:      []iamv1.AuthnMethod{iamv1.AuthnMethod_AUTHN_METHOD_PASSWORD},
			DeviceName:        "browser-a",
			CreatedAt:         timestamppb.New(createdAt),
			IdleExpiresAt:     timestamppb.New(createdAt.Add(24 * time.Hour)),
			AbsoluteExpiresAt: timestamppb.New(createdAt.Add(30 * 24 * time.Hour)),
		},
		Grant: grant,
	}, nil
}

func TestTargetPasswordLoginPreservesStableIAMErrorReasons(t *testing.T) {
	cases := []struct {
		name       string
		grpcCode   codes.Code
		reason     string
		wantStatus int
	}{
		{name: "invalid credential", grpcCode: codes.Unauthenticated, reason: "CREDENTIAL_INVALID", wantStatus: 401},
		{name: "idempotency conflict", grpcCode: codes.AlreadyExists, reason: "IDEMPOTENCY_CONFLICT", wantStatus: 409},
		{name: "idempotency result expired", grpcCode: codes.Aborted, reason: "IDEMPOTENCY_KEY_EXPIRED", wantStatus: 409},
		{name: "authentication rate limited", grpcCode: codes.ResourceExhausted, reason: "AUTH_RATE_LIMITED", wantStatus: 429},
		{name: "tenant IAM not ready", grpcCode: codes.Unavailable, reason: "TENANT_IAM_NOT_READY", wantStatus: 503},
		{name: "IAM unavailable", grpcCode: codes.Unavailable, reason: "IAM_UNAVAILABLE", wantStatus: 503},
		{name: "IAM timeout", grpcCode: codes.DeadlineExceeded, reason: "IAM_TIMEOUT", wantStatus: 504},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			grpcStatus := status.New(tc.grpcCode, "internal detail must not leak")
			grpcStatus, err := grpcStatus.WithDetails(&errdetails.ErrorInfo{Reason: tc.reason, Domain: "iam.ani.internal"})
			if err != nil {
				t.Fatal(err)
			}
			client := &targetLoginStub{err: grpcStatus.Err()}
			h := server.Default(server.WithHostPorts("127.0.0.1:0"))
			h.Use(middleware.RequestID())
			registerAuth(h.Group("/api/v1"), client)
			body := `{"account":"user@example.com","password":"wrong","audience":"console","boundary":{"type":"tenant","tenant_id":"` + targetLoginTenantID + `"}}`
			response := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/auth/password/login",
				&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
				ut.Header{Key: "Content-Type", Value: "application/json"},
				ut.Header{Key: "Idempotency-Key", Value: "login-error"},
			).Result()
			if response.StatusCode() != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.StatusCode(), tc.wantStatus, response.Body())
			}
			var document map[string]any
			if err := json.Unmarshal(response.Body(), &document); err != nil {
				t.Fatal(err)
			}
			if document["code"] != tc.reason || document["message"] == "internal detail must not leak" || document["request_id"] == "" {
				t.Fatalf("stable error = %#v", document)
			}
		})
	}
}

func (s *targetLoginStub) CheckPermission(context.Context, *iamv1.CheckPermissionRequest) (*iamv1.CheckPermissionResponse, error) {
	panic("password login must not call CheckPermission")
}

func TestTargetPasswordLoginReturnsAccessTokenAndRefreshCookie(t *testing.T) {
	client := &targetLoginStub{}
	h := server.Default(server.WithHostPorts("127.0.0.1:0"))
	h.Use(middleware.RequestID())
	registerAuth(h.Group("/api/v1"), client)
	body := `{"account":" User@Example.COM ","password":"correct horse","audience":"console","boundary":{"type":"tenant","tenant_id":"` + targetLoginTenantID + `"},"device_name":"browser-a"}`

	response := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/auth/password/login",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "Idempotency-Key", Value: "login-001"},
	).Result()

	if response.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.StatusCode(), response.Body())
	}
	if client.request == nil || client.request.GetAccount() != "User@Example.COM" || client.request.GetPassword() != "correct horse" {
		t.Fatalf("target request = %#v", client.request)
	}
	if client.request.GetAudience() != iamv1.Audience_AUDIENCE_CONSOLE || client.request.GetBoundary().GetTenant().GetTenantId() != targetLoginTenantID {
		t.Fatalf("audience/boundary = %v/%#v", client.request.GetAudience(), client.request.GetBoundary())
	}
	if client.request.GetIdempotencyKey() != "login-001" || client.request.GetDeviceName() != "browser-a" {
		t.Fatalf("idempotency/device = %q/%q", client.request.GetIdempotencyKey(), client.request.GetDeviceName())
	}
	if strings.Contains(string(response.Body()), "refresh_token") || strings.Contains(string(response.Body()), "target-refresh-secret") {
		t.Fatalf("refresh secret leaked in JSON: %s", response.Body())
	}
	var document map[string]any
	if err := json.Unmarshal(response.Body(), &document); err != nil {
		t.Fatal(err)
	}
	if document["access_token"] != "target-access-token" || document["token_type"] != "Bearer" || document["expires_in"] != float64(900) {
		t.Fatalf("access token response = %#v", document)
	}
	cookie := string(response.Header.Peek("Set-Cookie"))
	cookieLower := strings.ToLower(cookie)
	for _, required := range []string{"ani_console_refresh=target-refresh-secret", "path=/api/v1/auth", "httponly", "secure", "samesite=lax"} {
		if !strings.Contains(cookieLower, required) {
			t.Fatalf("Set-Cookie = %q, missing %q", cookie, required)
		}
	}
}

func TestTargetPasswordLoginRejectsContractViolationsBeforeIAM(t *testing.T) {
	cases := []struct {
		name           string
		account        string
		password       string
		deviceName     string
		tenantID       string
		idempotencyKey string
	}{
		{name: "account too long", account: strings.Repeat("a", 321)},
		{name: "password too long", password: strings.Repeat("p", 1025)},
		{name: "device name too long", deviceName: strings.Repeat("d", 129)},
		{name: "tenant is not UUID", tenantID: "not-a-tenant-uuid"},
		{name: "zero tenant UUID", tenantID: "00000000-0000-0000-0000-000000000000"},
		{name: "idempotency key too long", idempotencyKey: strings.Repeat("i", 129)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := tc.account
			if account == "" {
				account = "user@example.com"
			}
			password := tc.password
			if password == "" {
				password = "correct horse"
			}
			tenantID := tc.tenantID
			if tenantID == "" {
				tenantID = targetLoginTenantID
			}
			idempotencyKey := tc.idempotencyKey
			if idempotencyKey == "" {
				idempotencyKey = "login-contract"
			}
			body, err := json.Marshal(map[string]any{
				"account":     account,
				"password":    password,
				"audience":    "console",
				"boundary":    map[string]string{"type": "tenant", "tenant_id": tenantID},
				"device_name": tc.deviceName,
			})
			if err != nil {
				t.Fatal(err)
			}
			client := &targetLoginStub{}
			h := server.Default(server.WithHostPorts("127.0.0.1:0"))
			h.Use(middleware.RequestID())
			registerAuth(h.Group("/api/v1"), client)
			response := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/auth/password/login",
				&ut.Body{Body: bytes.NewReader(body), Len: len(body)},
				ut.Header{Key: "Content-Type", Value: "application/json"},
				ut.Header{Key: "Idempotency-Key", Value: idempotencyKey},
			).Result()
			if response.StatusCode() != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", response.StatusCode(), response.Body())
			}
			if client.request != nil {
				t.Fatalf("IAM request = %#v, want no call", client.request)
			}
		})
	}
}

func TestRegisterWithOptionsInjectsTargetPasswordLoginClient(t *testing.T) {
	client := &targetLoginStub{}
	h := server.Default(server.WithHostPorts("127.0.0.1:0"))
	h.Use(middleware.RequestID())
	RegisterWithOptions(h, RegisterOptions{TargetIAMClient: client})
	body := `{"account":"user@example.com","password":"correct horse","audience":"console","boundary":{"type":"tenant","tenant_id":"` + targetLoginTenantID + `"}}`

	response := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/auth/password/login",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "Idempotency-Key", Value: "login-production"},
	).Result()
	if response.StatusCode() != http.StatusOK || client.request == nil {
		t.Fatalf("production router status/request = %d/%#v; body=%s", response.StatusCode(), client.request, response.Body())
	}
}
