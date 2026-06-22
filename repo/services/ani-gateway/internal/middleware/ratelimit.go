package middleware

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
)

// RateLimit enforces per-tenant windowed request limiting through the shared gateway store.
func RateLimit(store GatewayStore) app.HandlerFunc {
	limit := gatewayRateLimitFromEnv()
	return func(ctx context.Context, c *app.RequestContext) {
		if isPublicPath(string(c.Path())) {
			c.Next(ctx)
			return
		}
		tenantID := GetTenantID(c)
		if tenantID == "" {
			c.Next(ctx)
			return
		}
		allowed, err := checkLimit(ctx, store, tenantID, string(c.Method()), string(c.Path()), limit)
		if err != nil {
			respondError(c, http.StatusServiceUnavailable, "RATE_LIMIT_UNAVAILABLE",
				"rate limit store unavailable")
			return
		}
		if !allowed {
			respondError(c, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED",
				"request rate limit exceeded for this tenant")
			return
		}
		c.Next(ctx)
	}
}

type gatewayRateLimit struct {
	requests int64
	window   time.Duration
}

func checkLimit(ctx context.Context, store GatewayStore, tenantID, method, path string, limit gatewayRateLimit) (bool, error) {
	if store == nil || limit.requests <= 0 {
		return true, nil
	}
	count, err := store.Increment(ctx, rateLimitKey(tenantID, method, path), limit.window)
	if err != nil {
		return false, err
	}
	return count <= limit.requests, nil
}

func gatewayRateLimitFromEnv() gatewayRateLimit {
	requests := int64(100)
	if raw := os.Getenv("GATEWAY_RATE_LIMIT_REQUESTS"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed >= 0 {
			requests = parsed
		}
	}
	window := time.Second
	if raw := os.Getenv("GATEWAY_RATE_LIMIT_WINDOW"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			window = parsed
		}
	}
	return gatewayRateLimit{requests: requests, window: window}
}

func rateLimitKey(tenantID, method, path string) string {
	return "ratelimit:" + tenantID + ":" + method + ":" + routeClass(path)
}

func routeClass(path string) string {
	path = strings.TrimPrefix(path, "/api/v1/")
	path = strings.Trim(path, "/")
	if path == "" {
		return "root"
	}
	parts := strings.Split(path, "/")
	return parts[0]
}
