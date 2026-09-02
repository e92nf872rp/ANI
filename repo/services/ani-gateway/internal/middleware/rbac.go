package middleware

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
	"github.com/kubercloud/ani/pkg/security/sandboxtoken"
)

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
