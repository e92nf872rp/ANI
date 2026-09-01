package middleware

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestAccessLogEmitsStructuredRequestLines(t *testing.T) {
	var buf bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(original)

	h := server.New()
	h.Use(RequestID(), AccessLog())
	h.GET("/api/v1/ping", func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.String(http.StatusOK, "pong")
	})
	h.GET("/api/v1/boom", func(ctx context.Context, c *app.RequestContext) {
		c.String(http.StatusInternalServerError, "boom")
	})
	h.GET("/health", func(ctx context.Context, c *app.RequestContext) {
		c.String(http.StatusOK, "ok")
	})

	if got := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/ping", nil).Result().StatusCode(); got != http.StatusOK {
		t.Fatalf("ping status = %d, want 200", got)
	}
	if got := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/boom", nil).Result().StatusCode(); got != http.StatusInternalServerError {
		t.Fatalf("boom status = %d, want 500", got)
	}
	if got := ut.PerformRequest(h.Engine, http.MethodGet, "/health", nil).Result().StatusCode(); got != http.StatusOK {
		t.Fatalf("health status = %d, want 200", got)
	}
	if got := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/missing", nil).Result().StatusCode(); got != http.StatusNotFound {
		t.Fatalf("missing route status = %d, want 404", got)
	}

	output := buf.String()
	for _, want := range []string{
		`msg="http request"`,
		`path=/api/v1/ping`,
		`status=200`,
		`tenant_id=tenant-a`,
		`user_id=user-a`,
		`level=WARN`,
		`path=/api/v1/boom`,
		`status=500`,
		`level=DEBUG`,
		`path=/health`,
		`status=404`,
		`path=/api/v1/missing`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("access log output missing %q, output:\n%s", want, output)
		}
	}
	if !strings.Contains(output, "level=ERROR") {
		t.Fatalf("access log output missing ERROR level for 500 response, output:\n%s", output)
	}
}
