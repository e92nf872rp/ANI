package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/security/sandboxtoken"
	"github.com/kubercloud/ani/pkg/types"
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
			setTenantContext(c, tenantID, userID, []string{"tenant-admin"}, "tenant")
			// Inject TenantContext into Go context.Context so RLS-aware stores
			// (MetadataInstanceStore via WithTenantTx -> SetDBTenant -> FromContext)
			// do not panic when a real DB provider is wired.
			ctx = withTenantContext(ctx, tenantID, userID, []string{"tenant-admin"})
			c.Next(ctx)
			return
		}

		// 1. Try Bearer token
		authHeader := string(c.GetHeader("Authorization"))
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")

			// Sandbox short-lived tokens are verified locally (HMAC), not via auth-service.
			if sandboxtoken.LooksLike(token) {
				claims, err := sandboxtoken.Parse(token, sandboxtoken.SigningKey(), time.Now().UTC())
				if err != nil {
					if errors.Is(err, sandboxtoken.ErrExpiredToken) {
						respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "sandbox token expired")
						return
					}
					respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid sandbox token")
					return
				}
				if !scopeAllowedForPath(string(c.Path()), sandboxtoken.ScopeSandbox) {
					respondError(c, http.StatusForbidden, "FORBIDDEN", "sandbox token not allowed for this path")
					return
				}
				setTenantContext(c, claims.TenantID, sandboxtoken.SandboxActorUID, []string{"sandbox-token"}, sandboxtoken.ScopeSandbox)
				setSandboxContext(c, claims)
				ctx, err = withTenantContextStrict(ctx, claims.TenantID, sandboxtoken.SandboxActorUID, []string{"sandbox-token"})
				if err != nil {
					respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", err.Error())
					return
				}
				c.Next(ctx)
				return
			}

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
			ctx, err = withTenantContextStrict(ctx, tenantCtx.GetTenantId(), tenantCtx.GetUserId(), tenantCtx.GetRoles())
			if err != nil {
				respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", err.Error())
				return
			}
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
			ctx, err = withTenantContextStrict(ctx, tenantCtx.GetTenantId(), tenantCtx.GetUserId(), tenantCtx.GetRoles())
			if err != nil {
				respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", err.Error())
				return
			}
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

// withTenantContext injects a types.TenantContext into the Go context.Context
// so RLS-aware stores that call types.FromContext (e.g. MetadataInstanceStore via
// WithTenantTx -> SetDBTenant) do not panic when a real DB provider is wired.
// Invalid UUIDs fall back to the dev default to keep dev mode resilient.
func withTenantContext(ctx context.Context, tenantID, userID string, roles []string) context.Context {
	tID, err := uuid.Parse(tenantID)
	if err != nil {
		tID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	}
	uID, err := uuid.Parse(userID)
	if err != nil {
		uID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	}
	return types.WithTenant(ctx, &types.TenantContext{
		TenantID: tID,
		UserID:   uID,
		Roles:    roles,
	})
}

// withTenantContextStrict is the authenticated-path variant: it rejects
// non-UUID tenant/user ids instead of silently falling back to the dev default,
// preventing cross-tenant data access when an auth service returns malformed ids.
func withTenantContextStrict(ctx context.Context, tenantID, userID string, roles []string) (context.Context, error) {
	tID, err := uuid.Parse(tenantID)
	if err != nil {
		return ctx, fmt.Errorf("invalid tenant id from auth: %s", tenantID)
	}
	uID, err := uuid.Parse(userID)
	if err != nil {
		return ctx, fmt.Errorf("invalid user id from auth: %s", userID)
	}
	return types.WithTenant(ctx, &types.TenantContext{
		TenantID: tID,
		UserID:   uID,
		Roles:    roles,
	}), nil
}

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
// - 平台/管理路由前缀 /auth/platform/*、/platform/*、/admin/* 仅 scope=platform 可访问
// - sandbox token 仅可访问 /api/v1/instances/{id}/sandbox/* 子资源
// - /api/v1/svc/* Services 层路由允许 platform 和 tenant scope（角色级 RBAC 由 rbac.go 校验）
// - 其他路由仅 scope=tenant 可访问（API key 默认 tenant scope）
func scopeAllowedForPath(path, scope string) bool {
	if scope == sandboxtoken.ScopeSandbox {
		return isSandboxSubresourcePath(path)
	}
	// 平台/管理路由前缀：/auth/platform/*、/platform/*、/admin/*（含 /admin/tenants/*、/admin/quota-meta）
	if strings.HasPrefix(path, "/api/v1/auth/platform/") ||
		strings.HasPrefix(path, "/api/v1/platform/") ||
		strings.HasPrefix(path, "/api/v1/admin/") {
		return scope == "platform"
	}
	// Services 层路由：platform（BOSS 管理端）和 tenant 均可访问，
	// 具体角色准入（platform-admin/ops/readonly vs tenant-admin）由 rbac.go CheckPermission 校验。
	if strings.HasPrefix(path, "/api/v1/svc/") {
		return scope == "platform" || scope == "tenant"
	}
	return scope == "tenant"
}
