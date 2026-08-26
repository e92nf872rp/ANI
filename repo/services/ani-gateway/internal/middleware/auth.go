package middleware

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/google/uuid"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	"github.com/kubercloud/ani/pkg/security/sandboxtoken"
	"github.com/kubercloud/ani/pkg/types"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
)

// Auth validates JWT Bearer tokens or API Keys.
// On success it sets "tenant_id", "user_id", "roles", and "scope" in the request context.
// This is fail-closed by default. Local development may set ANI_AUTH_MODE=dev
// and pass X-Dev-Tenant-ID to exercise routes before auth-service exists.
func Auth() app.HandlerFunc {
	return AuthWithClient(NewAuthClientFromEnv())
}

// AuthWithClient 保持既有直接调用入口：解析 policy 后走 legacy 认证。
// 内部复用 ResolvePolicyForRequest（不触发 c.Next），再交给
// AuthWithResolvedPolicy 执行且只 c.Next 一次，避免 Hertz 链路提前推进。
func AuthWithClient(authClient AuthClient) app.HandlerFunc {
	registry := authz.CoreRegistry()
	cfg := authz.Config{Mode: authz.ModeOff}
	return func(ctx context.Context, c *app.RequestContext) {
		resolved, err := ResolvePolicyForRequest(registry, cfg, c)
		if err != nil {
			respond503(c, "registered route has no authz policy")
			return
		}
		SetResolvedPolicy(c, resolved)
		AuthWithResolvedPolicy(authClient)(ctx, c)
	}
}

// AuthWithResolvedPolicy 是 policy 分流后的认证入口。
// B0 只允许 public/legacy：出现 generated 说明配置/接线违约，直接 fail closed。
func AuthWithResolvedPolicy(client AuthClient) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		resolved, err := GetResolvedPolicy(c)
		if err != nil {
			respond503(c, "authz policy context missing")
			return
		}
		if resolved.Source == authz.PolicySourcePublic {
			c.Next(ctx)
			return
		}
		if resolved.Source != authz.PolicySourceLegacy {
			// B0 不允许新链路；出现 generated 说明配置/接线违约。
			respond503(c, "generated authentication is not enabled")
			return
		}
		authenticateLegacy(ctx, c, client)
	}
}

// legacyViewFromContext 从旧 TenantContext 构造 legacy view；
// 严格规范化失败时降级为未规范化 view（identity key 仍按 scheme 规则校验）。
func legacyViewFromContext(tc *commonv1.TenantContext, scheme authz.CredentialScheme, claims *sandboxtoken.Claims) authz.LegacyPrincipalView {
	view, err := authz.LegacyViewFromTenantContext(tc, scheme)
	if err != nil {
		view = authz.LegacyPrincipalView{
			CredentialScheme: scheme,
			TenantID:         strings.TrimSpace(tc.GetTenantId()),
			SubjectID:        tc.GetUserId(),
			Scope:            tc.GetScope(),
			Roles:            append([]string(nil), tc.GetRoles()...),
		}
	}
	if claims != nil {
		view.SandboxClaims = &authz.SandboxClaims{TenantID: claims.TenantID, InstanceID: claims.InstanceID}
	}
	return view
}

// authenticateLegacy 是从 AuthWithClient 提取的旧认证逻辑，行为不变；
// 各认证成功分支额外写入 LegacyPrincipalView 供横切 identity key 使用。
func authenticateLegacy(ctx context.Context, c *app.RequestContext, authClient AuthClient) {
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
		setPrincipalContext(c, string(c.GetHeader("X-Dev-Principal-Kind")), string(c.GetHeader("X-Dev-Service-Scope")))
		SetLegacyPrincipalView(c, authz.LegacyPrincipalView{
			CredentialScheme: authz.CredentialBearer,
			TenantID:         tenantID,
			SubjectID:        userID,
			Scope:            "tenant",
			Roles:            []string{"tenant-admin"},
		})
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
					respond401(c, "sandbox token expired")
					return
				}
				respond401(c, "invalid sandbox token")
				return
			}
			if !scopeAllowedForPath(string(c.Path()), sandboxtoken.ScopeSandbox) {
				respond403(c, "sandbox token not allowed for this path")
				return
			}
			setTenantContext(c, claims.TenantID, sandboxtoken.SandboxActorUID, []string{"sandbox-token"}, sandboxtoken.ScopeSandbox)
			setSandboxContext(c, claims)
			SetLegacyPrincipalView(c, legacyViewFromContext(&commonv1.TenantContext{
				TenantId: claims.TenantID,
				UserId:   sandboxtoken.SandboxActorUID,
				Roles:    []string{"sandbox-token"},
				Scope:    sandboxtoken.ScopeSandbox,
			}, authz.CredentialSandboxToken, &claims))
			ctx, err = withTenantContextStrict(ctx, claims.TenantID, sandboxtoken.SandboxActorUID, []string{"sandbox-token"})
			if err != nil {
				respond401(c, err.Error())
				return
			}
			c.Next(ctx)
			return
		}

		if authClient == nil {
			respond401(c, "auth service unavailable")
			return
		}
		tenantCtx, err := authClient.ValidateToken(ctx, token)
		if err != nil {
			respond401(c, "invalid or expired token")
			return
		}
		scope := tenantCtx.GetScope()
		if scope == "" {
			scope = "tenant"
		}
		if !scopeAllowedForPath(string(c.Path()), scope) {
			respond403(c, "token scope not allowed for this path")
			return
		}
		setTenantContext(c, tenantCtx.GetTenantId(), tenantCtx.GetUserId(), tenantCtx.GetRoles(), scope)
		if isPlatformWorkloadScope(scope) {
			setPrincipalContext(c, "service", scope)
		} else {
			setPrincipalContext(c, "user", "")
		}
		SetLegacyPrincipalView(c, legacyViewFromContext(tenantCtx, authz.CredentialBearer, nil))
		ctx, err = withTenantContextStrict(ctx, tenantCtx.GetTenantId(), serviceActorOrUserID(tenantCtx.GetUserId(), scope), tenantCtx.GetRoles())
		if err != nil {
			respond401(c, err.Error())
			return
		}
		c.Next(ctx)
		return
	}

	// 2. Try API Key
	apiKey := string(c.GetHeader("X-API-Key"))
	if apiKey != "" {
		if authClient == nil {
			respond401(c, "auth service unavailable")
			return
		}
		tenantCtx, err := authClient.ValidateToken(ctx, apiKey)
		if err != nil {
			respond401(c, "invalid api key")
			return
		}
		scope := tenantCtx.GetScope()
		if scope == "" {
			scope = "tenant"
		}
		// API keys are tenant-scoped only; they cannot access platform endpoints.
		if !scopeAllowedForPath(string(c.Path()), scope) {
			respond403(c, "token scope not allowed for this path")
			return
		}
		setTenantContext(c, tenantCtx.GetTenantId(), tenantCtx.GetUserId(), tenantCtx.GetRoles(), scope)
		setPrincipalContext(c, "api_key", "")
		SetLegacyPrincipalView(c, legacyViewFromContext(tenantCtx, authz.CredentialAPIKey, nil))
		ctx, err = withTenantContextStrict(ctx, tenantCtx.GetTenantId(), tenantCtx.GetUserId(), tenantCtx.GetRoles())
		if err != nil {
			respond401(c, err.Error())
			return
		}
		c.Next(ctx)
		return
	}

	respond401(c, "authentication required")
}

func setTenantContext(c *app.RequestContext, tenantID, userID string, roles []string, scope string) {
	c.Set("tenant_id", tenantID)
	c.Set("user_id", userID)
	c.Set("roles", roles)
	c.Set("scope", scope)
}

func setPrincipalContext(c *app.RequestContext, principalKind, serviceScope string) {
	kind := strings.TrimSpace(principalKind)
	if kind == "" {
		kind = "user"
	}
	c.Set("principal_kind", kind)
	c.Set("service_scope", strings.TrimSpace(serviceScope))
}

func GetPrincipalKind(c *app.RequestContext) string {
	kind := strings.TrimSpace(c.GetString("principal_kind"))
	if kind == "" {
		return "user"
	}
	return kind
}

func GetServiceScope(c *app.RequestContext) string {
	return strings.TrimSpace(c.GetString("service_scope"))
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
	if isPlatformWorkloadPath(path) {
		return isPlatformWorkloadScope(scope)
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

func isPlatformWorkloadPath(path string) bool {
	return path == "/api/v1/platform-workload-capabilities" ||
		strings.HasPrefix(path, "/api/v1/platform-workloads")
}

func isPlatformWorkloadScope(scope string) bool {
	return strings.Contains(scope, "scope:platform-workloads:read") ||
		strings.Contains(scope, "scope:platform-workloads:write")
}

func serviceActorOrUserID(userID, scope string) string {
	if strings.TrimSpace(userID) != "" {
		return userID
	}
	if isPlatformWorkloadScope(scope) {
		return "00000000-0000-0000-0000-0000000000aa"
	}
	return userID
}
