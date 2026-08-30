package middleware

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	"github.com/kubercloud/ani/pkg/security/sandboxtoken"
	"github.com/kubercloud/ani/services/ani-gateway/internal/authz"
)

// RBAC checks whether the authenticated user has permission to access the route.
// Permission is encoded as "{resource}:{action}", matched against the user's roles.
// This is a stub; production will call OPA or an internal RBAC service.
func RBAC() app.HandlerFunc {
	return RBACWithClient(NewAuthClientFromEnv())
}

// RBACWithClient 保持既有直接调用入口：解析 policy 后走 legacy 授权。
// 内部复用 ResolvePolicyForRequest（不触发 c.Next），再交给
// RBACWithResolvedPolicy 执行且只 c.Next 一次，避免 Hertz 链路提前推进。
func RBACWithClient(authClient AuthClient) app.HandlerFunc {
	registry := authz.CoreRegistry()
	cfg := authz.Config{Mode: authz.ModeOff}
	return func(ctx context.Context, c *app.RequestContext) {
		resolved, err := ResolvePolicyForRequest(registry, cfg, c)
		if err != nil {
			respond503(c, "registered route has no authz policy")
			return
		}
		SetResolvedPolicy(c, resolved)
		RBACWithResolvedPolicy(authClient)(ctx, c)
	}
}

// RBACWithResolvedPolicy 是 policy 分流后的授权入口。
// B0 只允许 public/legacy：出现 generated 说明配置/接线违约，直接 fail closed。
func RBACWithResolvedPolicy(client AuthClient) app.HandlerFunc {
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
			respond503(c, "generated authorization is not enabled")
			return
		}
		authorizeLegacy(ctx, c, client)
	}
}

// authorizeLegacy 是从 RBACWithClient 提取的旧授权逻辑，行为不变，只调旧 RPC。
func authorizeLegacy(ctx context.Context, c *app.RequestContext, authClient AuthClient) {
	if isPublicPath(string(c.Path())) {
		c.Next(ctx)
		return
	}
	if os.Getenv("ANI_AUTH_MODE") == "dev" {
		c.Next(ctx)
		return
	}
	tenantID := GetTenantID(c)
	scope := GetScope(c)
	if scope == sandboxtoken.ScopeSandbox {
		if !sandboxTokenAllows(c, string(c.Path())) {
			respondError(c, http.StatusForbidden, "FORBIDDEN", "sandbox token scope denied for this path")
			return
		}
		c.Next(ctx)
		return
	}
	if GetPrincipalKind(c) == "service" {
		if !isPlatformWorkloadPath(string(c.Path())) {
			respondError(c, http.StatusForbidden, "FORBIDDEN", "service token not allowed for this path")
			return
		}
		c.Next(ctx)
		return
	}
	if tenantID == "" && scope != "platform" {
		// Auth middleware should have already rejected unauthenticated requests.
		respondError(c, http.StatusForbidden, "FORBIDDEN", "tenant context missing")
		return
	}
	if authClient == nil {
		respondError(c, http.StatusForbidden, "FORBIDDEN", "auth service unavailable")
		return
	}
	resource, action := inferPermission(string(c.Method()), string(c.Path()))
	resp, err := authClient.CheckPermission(ctx, &authv1.CheckPermissionRequest{
		TenantId: tenantID,
		UserId:   getStringValue(c, "user_id"),
		Roles:    getStringSliceValue(c, "roles"),
		Resource: resource,
		Action:   action,
	})
	if err != nil {
		respondError(c, http.StatusForbidden, "FORBIDDEN", "permission check failed")
		return
	}
	if !resp.GetAllowed() {
		reason := resp.GetReason()
		if reason == "" {
			reason = "permission denied"
		}
		respondError(c, http.StatusForbidden, "FORBIDDEN", reason)
		return
	}
	c.Next(ctx)
}

func inferPermission(method, path string) (string, string) {
	resource := "unknown"
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, part := range parts {
		if part == "v1" && i+1 < len(parts) {
			resource = parts[i+1]
			if resource == "svc" && i+2 < len(parts) {
				resource = parts[i+2]
			}
			break
		}
	}
	switch method {
	case http.MethodGet, http.MethodHead:
		return resource, "get"
	case http.MethodPost:
		return resource, "create"
	case http.MethodPut, http.MethodPatch:
		return resource, "update"
	case http.MethodDelete:
		return resource, "delete"
	default:
		return resource, strings.ToLower(method)
	}
}

func getStringValue(c *app.RequestContext, key string) string {
	v, _ := c.Get(key)
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func getStringSliceValue(c *app.RequestContext, key string) []string {
	v, _ := c.Get(key)
	if values, ok := v.([]string); ok {
		return values
	}
	return nil
}
