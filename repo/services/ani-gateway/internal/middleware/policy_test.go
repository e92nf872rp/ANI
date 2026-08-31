package middleware

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
)

// zeroUUID 与旧 platform token 的 tenant_id 占位值一致（auth-service uuid.Nil）。
const zeroUUID = "00000000-0000-0000-0000-000000000000"

// TestRateLimitAppliesToPlatformPrincipal 验证 platform 请求（空 tenant）
// 通过 identity key 参与限流，不再因空 tenant 被绕过。
func TestRateLimitAppliesToPlatformPrincipal(t *testing.T) {
	t.Setenv("GATEWAY_RATE_LIMIT_REQUESTS", "2")
	t.Setenv("GATEWAY_RATE_LIMIT_WINDOW", "1s")

	client := tokenStub{tenant: &commonv1.TenantContext{
		TenantId: zeroUUID,
		UserId:   "22222222-2222-2222-2222-222222222222",
		Roles:    []string{"platform-admin"},
		Scope:    "platform",
	}}
	store := newMemoryGatewayStoreForTest()
	h := server.New()
	h.Use(
		RequestID(),
		ResolveAuthzPolicy(authz.CoreRegistry(), authz.Config{}),
		AuthenticatePrincipal(client),
		AuthorizePrincipal(client),
		RateLimit(store),
	)
	h.GET("/api/v1/admin/plans/bound-tenant-counts", func(ctx context.Context, c *app.RequestContext) {
		c.Status(http.StatusOK)
	})

	statuses := make([]int, 3)
	for i := 0; i < 3; i++ {
		resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/admin/plans/bound-tenant-counts", nil,
			ut.Header{Key: "Authorization", Value: "Bearer platform-token"},
		).Result()
		statuses[i] = resp.StatusCode()
	}
	if statuses[0] != http.StatusOK || statuses[1] != http.StatusOK {
		t.Fatalf("first two statuses = %v, want 200 200", statuses)
	}
	if statuses[2] != http.StatusTooManyRequests {
		t.Fatalf("third status = %d, want 429 (platform must not bypass rate limit)", statuses[2])
	}
}

// TestRegisterSucceedsWithAuthServiceMode 验证 auth_service 模式下无 policy env 注册成功。
func TestRegisterSucceedsWithAuthServiceMode(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "auth_service")
	if err := Register(server.New(), newMemoryGatewayStoreForTest()); err != nil {
		t.Fatalf("auth_service config: %v", err)
	}
}

// TestIsCorePolicyPath 覆盖 policy 域规则：
// healthz/readyz 属于 Core policy 域；/health、/ready 豁免；svc/demo 排除。
func TestIsCorePolicyPath(t *testing.T) {
	cases := map[string]bool{
		"/healthz":               true,
		"/readyz":                true,
		"/health":                false,
		"/ready":                 false,
		"/api/v1/instances":      true,
		"/api/v1/admin/tenants":  true,
		"/api/v1/svc/models":     false,
		"/api/v1/demo/instances": false,
		"/v1/chat/completions":   false,
		"/api/v1":                false,
		"/api/v2/instances":      false,
	}
	for path, want := range cases {
		if got := IsCorePolicyPath(path); got != want {
			t.Fatalf("IsCorePolicyPath(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestAuthenticatePrincipalRejectsMissingPolicy 验证 Core 注册路由缺 registry 时 fail closed。
func TestAuthenticatePrincipalRejectsMissingPolicy(t *testing.T) {
	// 空 registry 模拟生成物漂移：路由已注册但 policy 缺失。
	registry, err := authz.NewRegistry(nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	cfg := authz.Config{}
	h := server.New()
	h.Use(
		RequestID(),
		ResolveAuthzPolicy(registry, cfg),
		AuthenticatePrincipal(tokenStub{}),
	)
	h.GET("/api/v1/instances", func(ctx context.Context, c *app.RequestContext) {
		c.Status(http.StatusOK)
	})
	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances", nil).Result()
	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode())
	}
	if !strings.Contains(string(resp.Body()), "AUTHZ_POLICY_MISSING") {
		t.Fatalf("body = %s, want AUTHZ_POLICY_MISSING", resp.Body())
	}
}

// TestUnmatchedRouteReturns404Not503 验证未匹配路由经过完整 C 阶段链后
// 由 Hertz NoRoute 返回 404，而非因 resolved policy 缺失返回 503。
func TestUnmatchedRouteReturns404Not503(t *testing.T) {
	registry, err := authz.NewRegistry(nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	cfg := authz.Config{}
	h := server.New()
	h.Use(
		RequestID(),
		ResolveAuthzPolicy(registry, cfg),
		AuthenticatePrincipal(tokenStub{}),
		AuthorizePrincipal(tokenStub{}),
	)
	// 不注册 /api/v1/nonexistent 路由，触发 Hertz NoRoute 404。
	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/nonexistent", nil).Result()
	if resp.StatusCode() == http.StatusServiceUnavailable {
		t.Fatalf("status = 503, want 404 (unmatched route must not hit authz 503)")
	}
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode())
	}
}
