package middleware

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
)

// testLegacyAuth 模拟旧 Auth 中间件的 context 写入：tenant context + legacy principal view。
func testLegacyAuth(tenantID, subjectID, scope string, scheme authz.CredentialScheme) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		setTenantContext(c, tenantID, subjectID, []string{"tenant-admin"}, scope)
		SetLegacyPrincipalView(c, authz.LegacyPrincipalView{
			CredentialScheme: scheme,
			TenantID:         tenantID,
			SubjectID:        subjectID,
			Scope:            scope,
			Roles:            []string{"tenant-admin"},
		})
		c.Next(ctx)
	}
}

func TestRateLimitRejectsOverQuotaAndRecoversAfterWindow(t *testing.T) {
	t.Setenv("GATEWAY_RATE_LIMIT_REQUESTS", "2")
	t.Setenv("GATEWAY_RATE_LIMIT_WINDOW", "40ms")

	store := newMemoryGatewayStoreForTest()
	h := server.New()
	h.Use(
		RequestID(),
		testLegacyAuth("tenant-a", "11111111-1111-1111-1111-111111111111", "tenant", authz.CredentialBearer),
		RateLimit(store),
	)
	h.GET("/api/v1/instances", func(ctx context.Context, c *app.RequestContext) {
		c.Status(http.StatusNoContent)
	})

	for i := 0; i < 2; i++ {
		resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances", nil).Result()
		if resp.StatusCode() != http.StatusNoContent {
			t.Fatalf("request %d status = %d, want 204", i+1, resp.StatusCode())
		}
	}

	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances", nil).Result()
	if resp.StatusCode() != http.StatusTooManyRequests {
		t.Fatalf("over-quota status = %d, want 429", resp.StatusCode())
	}
	if got := string(resp.Body()); !strings.Contains(got, "RATE_LIMIT_EXCEEDED") {
		t.Fatalf("over-quota body = %s, want RATE_LIMIT_EXCEEDED", got)
	}

	time.Sleep(70 * time.Millisecond)
	resp = ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/instances", nil).Result()
	if resp.StatusCode() != http.StatusNoContent {
		t.Fatalf("after window status = %d, want 204", resp.StatusCode())
	}
}

// TestUnmatchedRouteRateLimitDoesNotReturn401 验证未匹配路由经过完整链
// （含 RateLimit）后由 Hertz NoRoute 返回 404，而非因 identity key
// 缺失被 RateLimit 拦截返回 401。
func TestUnmatchedRouteRateLimitDoesNotReturn401(t *testing.T) {
	registry, err := authz.NewRegistry(nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	cfg := authz.Config{Mode: authz.ModeOff}
	store := newMemoryGatewayStoreForTest()
	h := server.New()
	h.Use(
		RequestID(),
		ResolveAuthzPolicy(registry, cfg),
		AuthenticatePrincipal(tokenStub{}),
		AuthorizePrincipal(tokenStub{}),
		RateLimit(store),
	)
	// 不注册 /api/v1/nonexistent 路由，触发 Hertz NoRoute 404。
	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/nonexistent", nil).Result()
	if resp.StatusCode() == http.StatusUnauthorized {
		t.Fatalf("status = 401, want 404 (unmatched route must not hit RateLimit 401)")
	}
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode())
	}
}
