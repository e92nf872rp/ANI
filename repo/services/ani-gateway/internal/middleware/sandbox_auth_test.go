package middleware

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/pkg/security/sandboxtoken"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
)

func TestScopeAllowedForPathSandbox(t *testing.T) {
	if !scopeAllowedForPath("/api/v1/instances/sandbox_1/sandbox/files", sandboxtoken.ScopeSandbox) {
		t.Fatal("sandbox files path should be allowed")
	}
	if scopeAllowedForPath("/api/v1/instances", sandboxtoken.ScopeSandbox) {
		t.Fatal("sandbox token must not access /instances list")
	}
	if scopeAllowedForPath("/api/v1/auth/platform/users", sandboxtoken.ScopeSandbox) {
		t.Fatal("sandbox token must not access platform paths")
	}
}

func TestSandboxTokenAllowsScopes(t *testing.T) {
	c := &app.RequestContext{}
	setSandboxContext(c, sandboxtoken.Claims{
		InstanceID: "sandbox_1",
		Scopes:     []string{"files", "ports"},
	})
	if !sandboxTokenAllows(c, "/api/v1/instances/sandbox_1/sandbox/files") {
		t.Fatal("files scope should allow files path")
	}
	if !sandboxTokenAllows(c, "/api/v1/instances/sandbox_1/sandbox/ports") {
		t.Fatal("ports scope should allow ports path")
	}
	if sandboxTokenAllows(c, "/api/v1/instances/sandbox_1/sandbox/code-runs") {
		t.Fatal("files/ports scopes must not allow code-runs")
	}
	if sandboxTokenAllows(c, "/api/v1/instances/sandbox_2/sandbox/files") {
		t.Fatal("wrong instance must be denied")
	}
	if sandboxTokenAllows(c, "/api/v1/instances/sandbox_1/sandbox/tokens") {
		t.Fatal("tokens endpoint must be denied for sandbox token")
	}
}

func TestAuthAcceptsSandboxBearerToken(t *testing.T) {
	prevMode := os.Getenv("ANI_AUTH_MODE")
	_ = os.Unsetenv("ANI_AUTH_MODE")
	t.Cleanup(func() {
		if prevMode == "" {
			_ = os.Unsetenv("ANI_AUTH_MODE")
			return
		}
		_ = os.Setenv("ANI_AUTH_MODE", prevMode)
	})

	key := []byte("middleware-sandbox-auth-test-key")
	_ = os.Setenv(sandboxtoken.EnvSigningKey, string(key))
	t.Cleanup(func() { _ = os.Unsetenv(sandboxtoken.EnvSigningKey) })

	now := time.Now().UTC()
	token, err := sandboxtoken.Issue(sandboxtoken.Claims{
		TenantID:   "11111111-1111-1111-1111-111111111111",
		InstanceID: "sandbox_9",
		Scopes:     []string{"files"},
		ExpiresAt:  now.Add(10 * time.Minute).Unix(),
		JTI:        "jti-auth-test",
	}, key, now)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	h := server.New()
	h.Use(
		ResolveAuthzPolicy(authz.CoreRegistry(), authz.Config{}),
		AuthenticatePrincipal(nil),
		AuthorizePrincipal(nil),
	)
	h.GET("/api/v1/instances/:instance_id/sandbox/files", func(ctx context.Context, c *app.RequestContext) {
		if GetTenantID(c) != "11111111-1111-1111-1111-111111111111" {
			t.Fatalf("tenant_id = %q", GetTenantID(c))
		}
		if GetScope(c) != sandboxtoken.ScopeSandbox {
			t.Fatalf("scope = %q", GetScope(c))
		}
		if GetSandboxInstanceID(c) != "sandbox_9" {
			t.Fatalf("sandbox instance = %q", GetSandboxInstanceID(c))
		}
		c.JSON(http.StatusOK, map[string]string{"ok": "true"})
	})

	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances/sandbox_9/sandbox/files", nil,
		ut.Header{Key: "Authorization", Value: "Bearer " + token},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode(), resp.Body())
	}

	denied := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances/sandbox_9/sandbox/files", nil,
		ut.Header{Key: "Authorization", Value: "Bearer " + token + "tamper"},
	).Result()
	if denied.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("tampered status = %d, want 401; body=%s", denied.StatusCode(), denied.Body())
	}
}
