package middleware

import (
	"bytes"
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestIdempotentReplayReturnsSameResponseForPublicPlatformEndpoint(t *testing.T) {
	store := newMemoryGatewayStoreForTest()
	h := server.New()
	h.Use(
		RequestID(),
		// Public endpoint path: Auth middleware skips via isPublicPath; no tenant_id is set.
		// Scope defaults to "tenant" via GetScope when unset, matching public tenant endpoints.
		// For platform password login the idempotency key still must dedupe correctly
		// because path is in the cache key.
		Idempotency(store),
	)

	var calls int32
	h.POST("/api/v1/auth/platform/password/login", func(ctx context.Context, c *app.RequestContext) {
		call := atomic.AddInt32(&calls, 1)
		c.JSON(http.StatusOK, map[string]any{"call": call, "access_token": "tok-a"})
	})

	body := `{"username":"admin","password":"correct","idempotency_key":"idem-platform"}`
	first := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/auth/platform/password/login", &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	second := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/auth/platform/password/login", &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if first.StatusCode() != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.StatusCode())
	}
	if second.StatusCode() != http.StatusOK {
		t.Fatalf("second status = %d, want 200", second.StatusCode())
	}
	if string(second.Body()) != string(first.Body()) {
		t.Fatalf("replay body = %s, want %s", second.Body(), first.Body())
	}
	if got := string(second.Header.Get("Idempotent-Replay")); got != "true" {
		t.Fatalf("Idempotent-Replay header = %q, want true", got)
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1", calls)
	}
}

func TestIdempotentReplayDifferentKeysProduceDifferentResponsesForPublicEndpoint(t *testing.T) {
	store := newMemoryGatewayStoreForTest()
	h := server.New()
	h.Use(
		RequestID(),
		Idempotency(store),
	)

	var calls int32
	h.POST("/api/v1/auth/password/login", func(ctx context.Context, c *app.RequestContext) {
		call := atomic.AddInt32(&calls, 1)
		c.JSON(http.StatusOK, map[string]any{"call": call})
	})

	bodyA := `{"tenant_name":"t","username":"a","password":"x","idempotency_key":"idem-1"}`
	bodyB := `{"tenant_name":"t","username":"a","password":"x","idempotency_key":"idem-2"}`
	ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/auth/password/login", &ut.Body{Body: bytes.NewBufferString(bodyA), Len: len(bodyA)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/auth/password/login", &ut.Body{Body: bytes.NewBufferString(bodyB), Len: len(bodyB)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if calls != 2 {
		t.Fatalf("handler calls = %d, want 2 (different idempotency keys must not dedupe)", calls)
	}
}

// TestPlatformPasswordLogin_IdempotencyKey 端到端验证 Issue 003 AC：
// 同 idempotency_key 重复提交 /api/v1/auth/platform/password/login 返回同一 TokenPair。
// 验证 C2 修复后，公开端点（无 tenant_id 注入）的幂等中间件按
// (scope, tenantID="", method, path, idempotencyKey) 维度正确 dedupe。
func TestPlatformPasswordLogin_IdempotencyKey(t *testing.T) {
	store := newMemoryGatewayStoreForTest()
	h := server.New()
	h.Use(
		RequestID(),
		Idempotency(store),
	)

	var calls int32
	h.POST("/api/v1/auth/platform/password/login", func(ctx context.Context, c *app.RequestContext) {
		call := atomic.AddInt32(&calls, 1)
		c.JSON(http.StatusOK, map[string]any{
			"call":          call,
			"access_token":  "platform-access-1",
			"refresh_token": "platform-refresh-1",
			"expires_in":    3600,
		})
	})

	body := `{"username":"admin","password":"correct","idempotency_key":"idem-platform-1"}`

	first := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/auth/platform/password/login",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	second := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/auth/platform/password/login",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if first.StatusCode() != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.StatusCode())
	}
	if second.StatusCode() != http.StatusOK {
		t.Fatalf("second status = %d, want 200", second.StatusCode())
	}
	if string(second.Body()) != string(first.Body()) {
		t.Fatalf("replay body = %s, want %s (same TokenPair)", second.Body(), first.Body())
	}
	if got := string(second.Header.Get("Idempotent-Replay")); got != "true" {
		t.Fatalf("Idempotent-Replay header = %q, want true", got)
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1 (idempotency_key must dedupe)", calls)
	}
}

// TestPasswordLogin_IdempotencyKey 端到端验证 Issue 002 AC：
// 同 idempotency_key 重复提交 /api/v1/auth/password/login 返回同一 TokenPair。
// 与 TestPlatformPasswordLogin_IdempotencyKey 对称，覆盖租户登录公开端点。
func TestPasswordLogin_IdempotencyKey(t *testing.T) {
	store := newMemoryGatewayStoreForTest()
	h := server.New()
	h.Use(
		RequestID(),
		Idempotency(store),
	)

	var calls int32
	h.POST("/api/v1/auth/password/login", func(ctx context.Context, c *app.RequestContext) {
		call := atomic.AddInt32(&calls, 1)
		c.JSON(http.StatusOK, map[string]any{
			"call":          call,
			"access_token":  "tenant-access-1",
			"refresh_token": "tenant-refresh-1",
			"expires_in":    3600,
		})
	})

	body := `{"tenant_name":"tenant-a","username":"alice","password":"correct","idempotency_key":"idem-tenant-1"}`

	first := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/auth/password/login",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	second := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/auth/password/login",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if first.StatusCode() != http.StatusOK {
		t.Fatalf("first status = %d, want 200", first.StatusCode())
	}
	if second.StatusCode() != http.StatusOK {
		t.Fatalf("second status = %d, want 200", second.StatusCode())
	}
	if string(second.Body()) != string(first.Body()) {
		t.Fatalf("replay body = %s, want %s (same TokenPair)", second.Body(), first.Body())
	}
	if got := string(second.Header.Get("Idempotent-Replay")); got != "true" {
		t.Fatalf("Idempotent-Replay header = %q, want true", got)
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1 (idempotency_key must dedupe)", calls)
	}
}

func TestIdempotentReplayReturnsSameResponse(t *testing.T) {
	store := newMemoryGatewayStoreForTest()
	h := server.New()
	h.Use(
		RequestID(),
		func(ctx context.Context, c *app.RequestContext) {
			setTenantContext(c, "tenant-a", "user-a", []string{"tenant-admin"}, "tenant")
			c.Next(ctx)
		},
		Idempotency(store),
	)

	var calls int32
	h.POST("/api/v1/instances", func(ctx context.Context, c *app.RequestContext) {
		call := atomic.AddInt32(&calls, 1)
		c.JSON(http.StatusAccepted, map[string]any{"call": call, "task_id": "task-a"})
	})

	body := `{"idempotency_key":"idem-a","name":"instance-a"}`
	first := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances", &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	second := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances", &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if first.StatusCode() != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", first.StatusCode())
	}
	if second.StatusCode() != http.StatusAccepted {
		t.Fatalf("second status = %d, want 202", second.StatusCode())
	}
	if string(second.Body()) != string(first.Body()) {
		t.Fatalf("replay body = %s, want %s", second.Body(), first.Body())
	}
	if got := string(second.Header.Get("Idempotent-Replay")); got != "true" {
		t.Fatalf("Idempotent-Replay header = %q, want true", got)
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1", calls)
	}
}

func TestConcurrentIdempotentInProgressReturns409(t *testing.T) {
	store := newMemoryGatewayStoreForTest()
	h := server.New()
	h.Use(
		RequestID(),
		func(ctx context.Context, c *app.RequestContext) {
			setTenantContext(c, "tenant-a", "user-a", []string{"tenant-admin"}, "tenant")
			c.Next(ctx)
		},
		Idempotency(store),
	)

	entered := make(chan struct{})
	release := make(chan struct{})
	h.POST("/api/v1/instances", func(ctx context.Context, c *app.RequestContext) {
		close(entered)
		<-release
		c.JSON(http.StatusAccepted, map[string]any{"task_id": "task-a"})
	})

	body := `{"name":"instance-a"}`
	firstDone := make(chan int, 1)
	go func() {
		resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances", &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
			ut.Header{Key: "Content-Type", Value: "application/json"},
			ut.Header{Key: "Idempotency-Key", Value: "idem-a"},
		).Result()
		firstDone <- resp.StatusCode()
	}()
	<-entered

	second := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances", &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "Idempotency-Key", Value: "idem-a"},
	).Result()
	if second.StatusCode() != http.StatusConflict {
		t.Fatalf("in-progress status = %d, want 409", second.StatusCode())
	}

	close(release)
	if status := <-firstDone; status != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", status)
	}
}
