package middleware

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
)

// Auth validates JWT Bearer tokens or API Keys.
// On success it sets "tenant_id", "user_id", "roles", and "scope" in the request context.
// This is fail-closed by default. Local development may set ANI_AUTH_MODE=dev
// and pass X-Dev-Tenant-ID to exercise routes before auth-service exists.
func Auth() app.HandlerFunc {
	return AuthWithClient(NewAuthClientFromEnv())
}

func AuthWithClient(authClient AuthClient) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if isPublicPath(string(c.Path())) {
			c.Next(ctx)
			return
		}

		if os.Getenv("ANI_AUTH_MODE") == "dev" {
			tenantID := string(c.GetHeader("X-Dev-Tenant-ID"))
			if tenantID == "" {
				tenantID = "00000000-0000-0000-0000-000000000001"
			}
			userID := string(c.GetHeader("X-Dev-User-ID"))
			if userID == "" {
				userID = "00000000-0000-0000-0000-000000000001"
			}
			c.Set("tenant_id", tenantID)
			c.Set("user_id", userID)
			c.Set("roles", []string{"tenant-admin"})
			c.Set("scope", "tenant")
			c.Next(ctx)
			return
		}

		// 1. Try Bearer token
		authHeader := string(c.GetHeader("Authorization"))
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			if authClient == nil {
				respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "auth service unavailable")
				return
			}
			tenantCtx, err := authClient.ValidateToken(ctx, token)
			if err != nil {
				respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
				return
			}
			scope := tenantCtx.GetScope()
			if scope == "" {
				scope = "tenant"
			}
			if !scopeAllowedForPath(string(c.Path()), scope) {
				respondError(c, http.StatusForbidden, "FORBIDDEN", "token scope not allowed for this path")
				return
			}
			setTenantContext(c, tenantCtx.GetTenantId(), tenantCtx.GetUserId(), tenantCtx.GetRoles(), scope)
			c.Next(ctx)
			return
		}

		// 2. Try API Key
		apiKey := string(c.GetHeader("X-API-Key"))
		if apiKey != "" {
			if authClient == nil {
				respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "auth service unavailable")
				return
			}
			tenantCtx, err := authClient.ValidateToken(ctx, apiKey)
			if err != nil {
				respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid api key")
				return
			}
			scope := tenantCtx.GetScope()
			if scope == "" {
				scope = "tenant"
			}
			// API keys are tenant-scoped only; they cannot access platform endpoints.
			if !scopeAllowedForPath(string(c.Path()), scope) {
				respondError(c, http.StatusForbidden, "FORBIDDEN", "token scope not allowed for this path")
				return
			}
			setTenantContext(c, tenantCtx.GetTenantId(), tenantCtx.GetUserId(), tenantCtx.GetRoles(), scope)
			c.Next(ctx)
			return
		}

		respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
	}
}

func setTenantContext(c *app.RequestContext, tenantID, userID string, roles []string, scope string) {
	c.Set("tenant_id", tenantID)
	c.Set("user_id", userID)
	c.Set("roles", roles)
	c.Set("scope", scope)
}

// GetScope returns the token scope set by Auth middleware. Empty when unset.
func GetScope(c *app.RequestContext) string {
	v := c.GetString("scope")
	if v == "" {
		return "tenant"
	}
	return v
}

// isPublicPath 公开端点白名单（无需 token）
func isPublicPath(path string) bool {
	switch path {
	case "/health", "/ready", "/healthz", "/readyz",
		"/api/v1/branding",
		"/api/v1/auth/password/login",
		"/api/v1/auth/platform/password/login",
		"/api/v1/auth/oidc/begin",
		"/api/v1/auth/token",
		"/api/v1/auth/refresh":
		return true
	default:
		return false
	}
}

// scopeAllowedForPath 平台 token 与租户 token 路由白名单隔离
// - 平台路由前缀 /auth/platform/* 仅 scope=platform 可访问
// - 其他路由仅 scope=tenant 可访问（API key 默认 tenant scope）
func scopeAllowedForPath(path, scope string) bool {
	if strings.HasPrefix(path, "/api/v1/auth/platform/") {
		return scope == "platform"
	}
	return scope == "tenant"
}
