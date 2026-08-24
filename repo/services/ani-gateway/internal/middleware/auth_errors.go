package middleware

import (
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
)

// respond401 统一 401 响应包装，内部最终调用 respondError。
func respond401(c *app.RequestContext, _ string) {
	respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid credential")
}

// respond403 统一 403 响应包装，reason 为空时使用默认文案。
func respond403(c *app.RequestContext, reason string) {
	if reason == "" {
		reason = "permission denied"
	}
	respondError(c, http.StatusForbidden, "FORBIDDEN", reason)
}

// respond503 统一 503 响应包装（authz 基础设施不可用）。
func respond503(c *app.RequestContext, _ string) {
	respondError(c, http.StatusServiceUnavailable, "AUTHZ_UNAVAILABLE", "authorization service unavailable")
}
