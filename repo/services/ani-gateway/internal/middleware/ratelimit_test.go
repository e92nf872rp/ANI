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
)

func TestRateLimitRejectsOverQuotaAndRecoversAfterWindow(t *testing.T) {
	t.Setenv("GATEWAY_RATE_LIMIT_REQUESTS", "2")
	t.Setenv("GATEWAY_RATE_LIMIT_WINDOW", "40ms")

	store := newMemoryGatewayStoreForTest()
	h := server.New()
	h.Use(
		RequestID(),
		func(ctx context.Context, c *app.RequestContext) {
			setTenantContext(c, "tenant-a", "user-a", []string{"tenant-admin"}, "tenant")
			c.Next(ctx)
		},
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
