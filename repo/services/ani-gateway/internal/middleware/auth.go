package middleware

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/types"
)

// Auth validates JWT Bearer tokens or API Keys.
// On success it sets "tenant_id" and "user_id" in the request context.
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
			setTenantContext(c, tenantID, userID, []string{"tenant-admin"})
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
			if authClient == nil {
				respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "auth service unavailable")
				return
			}
			tenantCtx, err := authClient.ValidateToken(ctx, token)
			if err != nil {
				respondError(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
				return
			}
			setTenantContext(c, tenantCtx.GetTenantId(), tenantCtx.GetUserId(), tenantCtx.GetRoles())
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
			setTenantContext(c, tenantCtx.GetTenantId(), tenantCtx.GetUserId(), tenantCtx.GetRoles())
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

func setTenantContext(c *app.RequestContext, tenantID, userID string, roles []string) {
	c.Set("tenant_id", tenantID)
	c.Set("user_id", userID)
	c.Set("roles", roles)
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
	case "/health", "/ready", "/healthz", "/readyz", "/api/v1/branding", "/api/v1/auth/oidc/begin", "/api/v1/auth/token", "/api/v1/auth/refresh":
		return true
	default:
		return false
	}
}
