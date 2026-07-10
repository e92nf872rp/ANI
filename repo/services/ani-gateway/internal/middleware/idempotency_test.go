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

func TestIdempotentReplayReturnsSameResponse(t *testing.T) {
	store := newMemoryGatewayStoreForTest()
	h := server.New()
	h.Use(
		RequestID(),
		func(ctx context.Context, c *app.RequestContext) {
			setTenantContext(c, "tenant-a", "user-a", []string{"tenant-admin"})
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
			setTenantContext(c, "tenant-a", "user-a", []string{"tenant-admin"})
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

func TestIdempotencySkipsBinaryUploadProxy(t *testing.T) {
	store := newMemoryGatewayStoreForTest()
	h := server.New()
	h.Use(
		RequestID(),
		func(ctx context.Context, c *app.RequestContext) {
			setTenantContext(c, "tenant-a", "user-a", []string{"tenant-admin"})
			c.Next(ctx)
		},
		Idempotency(store),
	)

	var calls int32
	h.POST("/api/v1/images/upload-proxy", func(ctx context.Context, c *app.RequestContext) {
		atomic.AddInt32(&calls, 1)
		// Handler must still see the raw body stream; middleware must not
		// have consumed Request.Body() for idempotency_key JSON parsing.
		if got := string(c.Request.Body()); got != "ISO-BYTES" {
			t.Fatalf("handler body = %q, want ISO-BYTES (middleware buffered/consumed body)", got)
		}
		c.Status(http.StatusOK)
	})

	payload := []byte("ISO-BYTES")
	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/images/upload-proxy", &ut.Body{Body: bytes.NewReader(payload), Len: len(payload)},
		ut.Header{Key: "Content-Type", Value: "application/octet-stream"},
		ut.Header{Key: "Authorization", Value: "Bearer upload-tok"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode())
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1", calls)
	}
}

func TestIsBinaryUploadPath(t *testing.T) {
	if !isBinaryUploadPath("/api/v1/images/upload-proxy") {
		t.Fatal("expected upload-proxy to be binary")
	}
	if isBinaryUploadPath("/api/v1/images/uploads") {
		t.Fatal("JSON create-upload session must still use idempotency")
	}
}
