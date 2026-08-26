package middleware

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
)

// RateLimit enforces per-principal windowed request limiting through the shared gateway store.
// identity key 取代旧 tenant 粒度：user/platform/sandbox 各自隔离，platform 空 tenant 不再绕过限流。
func RateLimit(store GatewayStore) app.HandlerFunc {
	limit := gatewayRateLimitFromEnv()
	return func(ctx context.Context, c *app.RequestContext) {
		if isPublicPath(string(c.Path())) {
			c.Next(ctx)
			return
		}
		// 与 AuthenticatePrincipal / AuthorizePrincipal 一致：
		// resolved policy 为 public 时放行，交由后续 NoRoute 返回 404。
		if resolved, err := GetResolvedPolicy(c); err == nil && resolved.Source == authz.PolicySourcePublic {
			c.Next(ctx)
			return
		}
		identityKey, err := RequestIdentityKey(c)
		if err != nil {
			respondError(c, http.StatusUnauthorized, "INVALID_PRINCIPAL", "invalid principal")
			return
		}
		allowed, err := checkLimit(ctx, store, identityKey, string(c.Method()), string(c.Path()), limit)
		if err != nil {
			respondError(c, http.StatusServiceUnavailable, "RATE_LIMIT_UNAVAILABLE",
				"rate limit store unavailable")
			return
		}
		if !allowed {
			respondError(c, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED",
				"request rate limit exceeded for this principal")
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
