package middleware

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
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

func TestIdempotencyReplaysDeleteAndRejectsDifferentIntent(t *testing.T) {
	store := newMemoryGatewayStoreForTest()
	h := server.New()
	h.Use(Idempotency(store))
	var calls int32
	h.DELETE("/api/v1/instances/:id/sandbox/files", func(ctx context.Context, c *app.RequestContext) {
		atomic.AddInt32(&calls, 1)
		c.Status(http.StatusNoContent)
	})

	request := func(path string) *protocol.Response {
		return ut.PerformRequest(h.Engine, http.MethodDelete, path, nil,
			ut.Header{Key: "Idempotency-Key", Value: "delete-a"},
		).Result()
	}
	first := request("/api/v1/instances/sandbox-a/sandbox/files?path=workspace/a.txt")
	second := request("/api/v1/instances/sandbox-a/sandbox/files?path=workspace/a.txt")
	conflict := request("/api/v1/instances/sandbox-a/sandbox/files?path=workspace/b.txt")
	if first.StatusCode() != http.StatusNoContent || second.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE statuses = (%d, %d), want 204", first.StatusCode(), second.StatusCode())
	}
	if conflict.StatusCode() != http.StatusConflict || !bytes.Contains(conflict.Body(), []byte("IDEMPOTENCY_KEY_REUSED")) {
		t.Fatalf("different DELETE intent = %d %s, want 409 IDEMPOTENCY_KEY_REUSED", conflict.StatusCode(), conflict.Body())
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1", calls)
	}
}

func TestIdempotencyRejectsSameKeyWithDifferentJSONBody(t *testing.T) {
	store := newMemoryGatewayStoreForTest()
	h := server.New()
	h.Use(Idempotency(store))
	var calls int32
	h.POST("/api/v1/resources", func(ctx context.Context, c *app.RequestContext) {
		atomic.AddInt32(&calls, 1)
		c.JSON(http.StatusCreated, map[string]any{"ok": true})
	})
	perform := func(body string) *protocol.Response {
		return ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/resources",
			&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
			ut.Header{Key: "Content-Type", Value: "application/json"},
		).Result()
	}
	if got := perform(`{"idempotency_key":"same","name":"a"}`).StatusCode(); got != http.StatusCreated {
		t.Fatalf("first status = %d, want 201", got)
	}
	conflict := perform(`{"name":"b","idempotency_key":"same"}`)
	if conflict.StatusCode() != http.StatusConflict || !bytes.Contains(conflict.Body(), []byte("IDEMPOTENCY_KEY_REUSED")) {
		t.Fatalf("different body = %d %s, want 409", conflict.StatusCode(), conflict.Body())
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1", calls)
	}
}

func TestCheckpointRestoreIdempotencyIsolatedByCheckpointPath(t *testing.T) {
	store := newMemoryGatewayStoreForTest()
	h := server.New()
	h.Use(Idempotency(store))
	var calls int32
	h.POST("/api/v1/instances/:instance_id/sandbox/checkpoints/:checkpoint_id/restore", func(ctx context.Context, c *app.RequestContext) {
		call := atomic.AddInt32(&calls, 1)
		c.JSON(http.StatusAccepted, map[string]any{"call": call, "checkpoint_id": c.Param("checkpoint_id")})
	})
	body := `{"idempotency_key":"restore-shared-key"}`
	perform := func(checkpointID string) *protocol.Response {
		return ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/sandbox-a/sandbox/checkpoints/"+checkpointID+"/restore",
			&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
			ut.Header{Key: "Content-Type", Value: "application/json"},
		).Result()
	}
	first := perform("checkpoint-a")
	second := perform("checkpoint-b")
	if first.StatusCode() != http.StatusAccepted || second.StatusCode() != http.StatusAccepted {
		t.Fatalf("restore statuses = (%d, %d), want 202", first.StatusCode(), second.StatusCode())
	}
	if calls != 2 || bytes.Equal(first.Body(), second.Body()) {
		t.Fatalf("restore calls = %d, bodies = (%s, %s); paths must have isolated idempotency scope", calls, first.Body(), second.Body())
	}
}

func TestSandboxTokenIdempotencyExpiresResponseButKeepsTombstone(t *testing.T) {
	store := newMemoryGatewayStoreForTest()
	h := server.New()
	h.Use(Idempotency(store))
	var calls int32
	h.POST("/api/v1/instances/:id/sandbox/tokens", func(ctx context.Context, c *app.RequestContext) {
		atomic.AddInt32(&calls, 1)
		c.JSON(http.StatusCreated, map[string]any{
			"token": "sensitive-token", "expires_at": time.Now().Add(30 * time.Millisecond).Format(time.RFC3339Nano),
		})
	})
	body := `{"idempotency_key":"token-a","expires_in":"30ms"}`
	perform := func() *protocol.Response {
		return ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/instances/sandbox-a/sandbox/tokens",
			&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
			ut.Header{Key: "Content-Type", Value: "application/json"},
		).Result()
	}
	first, replay := perform(), perform()
	if first.StatusCode() != http.StatusCreated || replay.StatusCode() != http.StatusCreated || calls != 1 {
		t.Fatalf("token responses = (%d, %d) calls=%d, want 201 replay", first.StatusCode(), replay.StatusCode(), calls)
	}
	time.Sleep(50 * time.Millisecond)
	expired := perform()
	if expired.StatusCode() != http.StatusConflict || !bytes.Contains(expired.Body(), []byte("IdempotencyResultExpired")) {
		t.Fatalf("expired response = %d %s, want 409 IdempotencyResultExpired", expired.StatusCode(), expired.Body())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for key, entry := range store.entries {
		if strings.HasSuffix(key, ":metadata") && bytes.Contains(entry.value, []byte("sensitive-token")) {
			t.Fatalf("token leaked into idempotency tombstone: %s", entry.value)
		}
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
		testLegacyAuth("tenant-a", "11111111-1111-1111-1111-111111111111", "tenant", authz.CredentialBearer),
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
		testLegacyAuth("tenant-a", "11111111-1111-1111-1111-111111111111", "tenant", authz.CredentialBearer),
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
