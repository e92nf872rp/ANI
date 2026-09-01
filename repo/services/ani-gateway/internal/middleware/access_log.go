package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
)

// AccessLog emits one structured line per request with method, path, status,
// latency and identity so runtime incidents can be traced from gateway logs
// alone. Health probes log at debug level to avoid flooding the stream.
func AccessLog() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		start := time.Now()
		c.Next(ctx)
		path := string(c.Request.URI().Path())
		status := c.Response.StatusCode()
		attrs := []any{
			"method", string(c.Request.Header.Method()),
			"path", path,
			"status", status,
			"latency_ms", time.Since(start).Milliseconds(),
			"request_id", GetRequestID(c),
		}
		if tenant := GetTenantID(c); tenant != "" {
			attrs = append(attrs, "tenant_id", tenant)
		}
		if user := GetUserID(c); user != "" {
			attrs = append(attrs, "user_id", user)
		}
		switch {
		case status >= 500:
			slog.Error("http request", attrs...)
		case status >= 400:
			slog.Warn("http request", attrs...)
		case isAccessLogHealthProbe(path):
			slog.Debug("http request", attrs...)
		default:
			slog.Info("http request", attrs...)
		}
	}
}

func isAccessLogHealthProbe(path string) bool {
	switch path {
	case "/health", "/ready", "/healthz", "/readyz":
		return true
	}
	return false
}
