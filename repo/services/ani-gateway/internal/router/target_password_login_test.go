package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

const (
	targetLoginTenantID    = "0198f062-b76d-7f2a-b0ad-50a417bf1f70"
	targetLoginPrincipalID = "0198f062-b76d-7001-9000-000000000001"
	targetLoginSessionID   = "0198f062-b76d-7001-9000-000000000002"
	targetLoginGrantID     = "0198f062-b76d-7001-9000-000000000003"
)

type targetLoginStub struct {
	request *ports.TargetIAMPasswordLoginRequest
	err     error
	calls   int
}

var _ ports.TargetIAM = (*targetLoginStub)(nil)

func (s *targetLoginStub) PasswordLogin(_ context.Context, request ports.TargetIAMPasswordLoginRequest) (ports.TargetIAMPasswordLoginResult, error) {
	s.calls++
	s.request = &request
	if s.err != nil {
		return ports.TargetIAMPasswordLoginResult{}, s.err
	}
	createdAt := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	boundary := ports.TargetIAMBoundary{Type: ports.TargetIAMBoundaryTenant, TenantID: targetLoginTenantID}
	grant := ports.TargetIAMGrant{
		ID: targetLoginGrantID, Boundary: boundary, Version: 1, Status: ports.TargetIAMGrantActive,
	}
	return ports.TargetIAMPasswordLoginResult{
		AccessToken:      "target-access-token",
		ExpiresInSeconds: 900,
		RefreshToken:     "target-refresh-secret",
		RefreshExpiresAt: createdAt.Add(30 * 24 * time.Hour),
		Principal: ports.TargetIAMPrincipal{
			ID: targetLoginPrincipalID, Type: ports.TargetIAMPrincipalHuman,
			Status: ports.TargetIAMPrincipalActive, Boundary: boundary,
			SessionID: targetLoginSessionID, GrantID: targetLoginGrantID,
			AuthnMethods: []ports.TargetIAMAuthnMethod{ports.TargetIAMAuthnPassword},
		},
		Session: ports.TargetIAMSession{
			ID:                targetLoginSessionID,
			Status:            ports.TargetIAMSessionActive,
			Grants:            []ports.TargetIAMGrant{grant},
			AuthnMethods:      []ports.TargetIAMAuthnMethod{ports.TargetIAMAuthnPassword},
			DeviceName:        "browser-a",
			CreatedAt:         createdAt,
			IdleExpiresAt:     createdAt.Add(24 * time.Hour),
			AbsoluteExpiresAt: createdAt.Add(30 * 24 * time.Hour),
		},
		Grant: grant,
	}, nil
}

func TestTargetPasswordLoginPreservesStableIAMErrorReasons(t *testing.T) {
	cases := []struct {
		name           string
		failureKind    ports.TargetIAMFailureKind
		reason         string
		metadata       map[string]string
		wantStatus     int
		wantRetryAfter string
	}{
		{name: "invalid credential", failureKind: ports.TargetIAMFailureUnauthenticated, reason: "CREDENTIAL_INVALID", wantStatus: 401},
		{name: "idempotency conflict", failureKind: ports.TargetIAMFailureAlreadyExists, reason: "IDEMPOTENCY_CONFLICT", wantStatus: 409},
		{name: "idempotency result expired", failureKind: ports.TargetIAMFailureAborted, reason: "IDEMPOTENCY_KEY_EXPIRED", wantStatus: 409},
		{name: "authentication rate limited", failureKind: ports.TargetIAMFailureResourceExhausted, reason: "AUTH_RATE_LIMITED", metadata: map[string]string{"retry_after_seconds": "900"}, wantStatus: 429, wantRetryAfter: "900"},
		{name: "tenant IAM not ready", failureKind: ports.TargetIAMFailureUnavailable, reason: "TENANT_IAM_NOT_READY", wantStatus: 503},
		{name: "IAM unavailable", failureKind: ports.TargetIAMFailureUnavailable, reason: "IAM_UNAVAILABLE", wantStatus: 503},
		{name: "IAM timeout", failureKind: ports.TargetIAMFailureDeadlineExceeded, reason: "IAM_TIMEOUT", wantStatus: 504},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &targetLoginStub{err: ports.NewTargetIAMError(tc.failureKind, tc.reason, tc.metadata)}
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
			if got := string(response.Header.Peek("Retry-After")); got != tc.wantRetryAfter {
				t.Fatalf("Retry-After = %q, want %q", got, tc.wantRetryAfter)
			}
			var document map[string]any
			if err := json.Unmarshal(response.Body(), &document); err != nil {
				t.Fatal(err)
			}
			if len(document) != 3 || document["code"] != tc.reason || document["message"] == "internal detail must not leak" || document["request_id"] == "" {
				t.Fatalf("stable error = %#v", document)
			}
		})
	}
}

func TestTargetPasswordLoginDoesNotEmitMalformedRateLimitResponse(t *testing.T) {
	for _, metadata := range []map[string]string{nil, {"retry_after_seconds": "0"}, {"retry_after_seconds": "invalid"}} {
		client := &targetLoginStub{err: ports.NewTargetIAMError(
			ports.TargetIAMFailureResourceExhausted, "AUTH_RATE_LIMITED", metadata,
		)}
		h := server.Default(server.WithHostPorts("127.0.0.1:0"))
		h.Use(middleware.RequestID())
		registerAuth(h.Group("/api/v1"), client)
		body := `{"account":"user@example.com","password":"wrong","audience":"console","boundary":{"type":"tenant","tenant_id":"` + targetLoginTenantID + `"}}`
		response := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/auth/password/login",
			&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
			ut.Header{Key: "Content-Type", Value: "application/json"},
			ut.Header{Key: "Idempotency-Key", Value: "malformed-rate-limit"},
		).Result()
		if response.StatusCode() != http.StatusServiceUnavailable || string(response.Header.Peek("Retry-After")) != "" || !strings.Contains(string(response.Body()), `"code":"IAM_UNAVAILABLE"`) {
			t.Fatalf("malformed rate limit = %d Retry-After=%q body=%s, want 503 IAM_UNAVAILABLE", response.StatusCode(), response.Header.Peek("Retry-After"), response.Body())
		}
	}
}

func (s *targetLoginStub) CheckPermission(context.Context, ports.TargetIAMCheckPermissionRequest) (ports.TargetIAMAuthorizationDecision, error) {
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
	if client.request == nil || client.request.Account != "User@Example.COM" || client.request.Password != "correct horse" {
		t.Fatalf("target request = %#v", client.request)
	}
	if client.request.Audience != ports.TargetIAMAudienceConsole || client.request.Boundary.Type != ports.TargetIAMBoundaryTenant || client.request.Boundary.TenantID != targetLoginTenantID {
		t.Fatalf("audience/boundary = %v/%#v", client.request.Audience, client.request.Boundary)
	}
	if client.request.IdempotencyKey != "login-001" || client.request.DeviceName != "browser-a" {
		t.Fatalf("idempotency/device = %q/%q", client.request.IdempotencyKey, client.request.DeviceName)
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

func TestProductionChainTargetPasswordLoginRetryReturnsRefreshCookieWithoutGatewayReplay(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "auth_service")
	client := &targetLoginStub{}
	store := newTargetLoginGatewayStore()
	h := server.Default(server.WithHostPorts("127.0.0.1:0"))
	if err := middleware.RegisterWithTargetIAM(h, store, client); err != nil {
		t.Fatal(err)
	}
	RegisterWithOptions(h, RegisterOptions{TargetIAMClient: client})
	body := `{"account":"user@example.com","password":"correct horse","audience":"console","boundary":{"type":"tenant","tenant_id":"` + targetLoginTenantID + `"},"device_name":"browser-a"}`
	perform := func() []byte {
		response := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/auth/password/login",
			&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
			ut.Header{Key: "Content-Type", Value: "application/json"},
			ut.Header{Key: "Idempotency-Key", Value: "production-target-retry"},
		).Result()
		if response.StatusCode() != http.StatusOK {
			t.Fatalf("status = %d, body=%s", response.StatusCode(), response.Body())
		}
		cookie := strings.ToLower(string(response.Header.Peek("Set-Cookie")))
		for _, required := range []string{"ani_console_refresh=target-refresh-secret", "httponly", "secure"} {
			if !strings.Contains(cookie, required) {
				t.Fatalf("Set-Cookie = %q, missing %q", cookie, required)
			}
		}
		return append([]byte(nil), response.Body()...)
	}
	first := perform()
	second := perform()
	if !bytes.Equal(first, second) {
		t.Fatalf("target IAM idempotent bodies differ:\nfirst=%s\nsecond=%s", first, second)
	}
	if client.calls != 2 {
		t.Fatalf("PasswordLogin calls = %d, want 2 requests reaching IAM", client.calls)
	}
	for key, value := range store.snapshot() {
		if strings.HasPrefix(key, "idempotency:") {
			t.Fatalf("generic Gateway idempotency cache contains target login key %q", key)
		}
		if bytes.Contains(value, []byte("target-refresh-secret")) || bytes.Contains(value, []byte("target-access-token")) {
			t.Fatalf("Gateway cache key %q contains target token plaintext", key)
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

type targetLoginGatewayStore struct {
	mu     sync.Mutex
	values map[string][]byte
	counts map[string]int64
}

var _ ports.CacheStore = (*targetLoginGatewayStore)(nil)

func newTargetLoginGatewayStore() *targetLoginGatewayStore {
	return &targetLoginGatewayStore{values: map[string][]byte{}, counts: map[string]int64{}}
}

func (s *targetLoginGatewayStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[key]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *targetLoginGatewayStore) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = append([]byte(nil), value...)
	return nil
}

func (s *targetLoginGatewayStore) SetNX(_ context.Context, key string, value []byte, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.values[key]; ok {
		return false, nil
	}
	s.values[key] = append([]byte(nil), value...)
	return true, nil
}

func (s *targetLoginGatewayStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	return nil
}

func (s *targetLoginGatewayStore) Increment(_ context.Context, key string, _ time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts[key]++
	return s.counts[key], nil
}

func (s *targetLoginGatewayStore) TTL(context.Context, string) (time.Duration, error) {
	return time.Minute, nil
}

func (s *targetLoginGatewayStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.values[key]
	return ok, nil
}

func (s *targetLoginGatewayStore) snapshot() map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string][]byte, len(s.values))
	for key, value := range s.values {
		result[key] = append([]byte(nil), value...)
	}
	return result
}
