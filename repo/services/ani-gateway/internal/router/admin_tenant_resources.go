package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/google/uuid"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

// adminTenantAPI holds Core tenant service for /admin/tenants/*.
type adminTenantAPI struct {
	tenant ports.TenantService
}

// registerAdminTenantResources registers Core tenant endpoints:
//
//	GET  /admin/tenant-admins/available-tenants
//	POST /admin/tenants
//	GET  /admin/tenants
//	GET  /admin/tenants/:tenant_id
//	PUT  /admin/tenants/:tenant_id
//	POST /admin/tenants/:tenant_id/freeze|unfreeze|disable
//	GET/PUT /admin/tenants/:tenant_id/auth
//	GET  /admin/tenants/:tenant_id/lifecycle
func registerAdminTenantResources(v1 *route.RouterGroup, tenant ports.TenantService) {
	if tenant == nil {
		return
	}
	api := &adminTenantAPI{tenant: tenant}
	v1.GET("/admin/tenant-admins/available-tenants", api.listAvailableTenants)
	v1.POST("/admin/tenants", api.createTenant)
	v1.GET("/admin/tenants", api.listTenants)
	v1.GET("/admin/tenants/:tenant_id", api.getTenant)
	v1.PUT("/admin/tenants/:tenant_id", api.updateTenant)
	v1.POST("/admin/tenants/:tenant_id/freeze", api.freezeTenant)
	v1.POST("/admin/tenants/:tenant_id/unfreeze", api.unfreezeTenant)
	v1.POST("/admin/tenants/:tenant_id/disable", api.disableTenant)
	v1.GET("/admin/tenants/:tenant_id/auth", api.getTenantAuth)
	v1.PUT("/admin/tenants/:tenant_id/auth", api.updateTenantAuth)
	v1.GET("/admin/tenants/:tenant_id/lifecycle", api.listTenantLifecycle)
}

func (api *adminTenantAPI) listAvailableTenants(ctx context.Context, c *app.RequestContext) {
	if api.tenant == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "TENANT_UNAVAILABLE", "tenant service unavailable")
		return
	}
	items, err := api.tenant.ListAvailableTenants(ctx)
	if err != nil {
		writeAdminTenantError(c, err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, t := range items {
		out = append(out, map[string]any{
			"id":           t.ID,
			"name":         t.Name,
			"display_name": t.DisplayName,
			"status":       string(t.Status),
		})
	}
	c.JSON(http.StatusOK, map[string]any{"items": out})
}

func (api *adminTenantAPI) getTenant(ctx context.Context, c *app.RequestContext) {
	if api.tenant == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "TENANT_UNAVAILABLE", "tenant service unavailable")
		return
	}
	tenant, err := api.tenant.GetTenant(ctx, c.Param("tenant_id"))
	if err != nil {
		writeAdminTenantError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAdminTenantResponse(tenant))
}

func (api *adminTenantAPI) createTenant(ctx context.Context, c *app.RequestContext) {
	// 步骤 1：服务可用性
	if api.tenant == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "TENANT_UNAVAILABLE", "tenant service unavailable")
		return
	}
	// 步骤 2：解析 OpenAPI TenantCreateRequest（email → ContactEmail）
	var body struct {
		Name              string `json:"name"`
		DisplayName       string `json:"display_name"`
		Email             string `json:"email"`
		PlanID            string `json:"plan_id"`
		AdminEmail        string `json:"admin_email"`
		AdminName         string `json:"admin_name"`
		AdminPasswordHash string `json:"admin_password_hash"`
	}
	if err := json.Unmarshal(c.Request.Body(), &body); err != nil {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	// 步骤 3：调用 Core store（lifecycle 归因由 withTenantLifecycleCtx 统一注入）
	tenant, err := api.tenant.CreateTenant(withTenantLifecycleCtx(ctx, c), ports.CreateTenantInput{
		Name:              strings.TrimSpace(body.Name),
		DisplayName:       strings.TrimSpace(body.DisplayName),
		ContactEmail:      strings.TrimSpace(body.Email),
		PlanID:            strings.TrimSpace(body.PlanID),
		AdminEmail:        strings.TrimSpace(body.AdminEmail),
		AdminName:         strings.TrimSpace(body.AdminName),
		AdminPasswordHash: strings.TrimSpace(body.AdminPasswordHash),
	})
	if err != nil {
		writeAdminTenantError(c, err)
		return
	}
	// 步骤 4：返回 Tenant 视图
	c.JSON(http.StatusOK, toAdminTenantResponse(tenant))
}

func (api *adminTenantAPI) listTenants(ctx context.Context, c *app.RequestContext) {
	if api.tenant == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "TENANT_UNAVAILABLE", "tenant service unavailable")
		return
	}
	limit := 20
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
			if limit > 100 {
				limit = 100
			}
		}
	}
	statusFilter, err := ports.ParseTenantStatusFilter(c.Query("status"))
	if err != nil {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "status must be active, frozen, or disabled")
		return
	}
	listed, err := api.tenant.ListTenants(ctx, ports.ListTenantsFilter{
		Limit:  limit,
		Cursor: c.Query("cursor"),
		Status: statusFilter,
		Search: c.Query("search"),
	})
	if err != nil {
		writeAdminTenantError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(listed.Items))
	for _, it := range listed.Items {
		items = append(items, map[string]any{
			"id":           it.ID,
			"name":         it.Name,
			"display_name": it.DisplayName,
			"status":       string(it.Status),
			"plan_id":      it.PlanID,
			"admin_count":  it.AdminCount,
			"created_at":   it.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nullIfEmpty(listed.NextCursor),
	})
}

func (api *adminTenantAPI) updateTenant(ctx context.Context, c *app.RequestContext) {
	if api.tenant == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "TENANT_UNAVAILABLE", "tenant service unavailable")
		return
	}
	var body ports.UpdateTenantInput
	if err := json.Unmarshal(c.Request.Body(), &body); err != nil {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(c.Request.Body(), &raw)
	if v, ok := raw["display_name"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil {
			body.DisplayName = &s
		}
	}
	if v, ok := raw["contact_email"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil {
			body.ContactEmail = &s
		}
	}
	tenant, err := api.tenant.UpdateTenant(ctx, c.Param("tenant_id"), body)
	if err != nil {
		writeAdminTenantError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAdminTenantResponse(tenant))
}

func (api *adminTenantAPI) freezeTenant(ctx context.Context, c *app.RequestContext) {
	api.stateTransition(ctx, c, api.tenant.FreezeTenant)
}

func (api *adminTenantAPI) unfreezeTenant(ctx context.Context, c *app.RequestContext) {
	api.stateTransition(ctx, c, api.tenant.UnfreezeTenant)
}

func (api *adminTenantAPI) disableTenant(ctx context.Context, c *app.RequestContext) {
	api.stateTransition(ctx, c, api.tenant.DisableTenant)
}

func (api *adminTenantAPI) stateTransition(
	ctx context.Context,
	c *app.RequestContext,
	fn func(context.Context, string) (ports.Tenant, error),
) {
	if api.tenant == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "TENANT_UNAVAILABLE", "tenant service unavailable")
		return
	}
	tenant, err := fn(withTenantLifecycleCtx(ctx, c), c.Param("tenant_id"))
	if err != nil {
		writeAdminTenantError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAdminTenantResponse(tenant))
}

// withTenantLifecycleCtx 统一解析 request_id / actor_user_id 并注入 ctx，
// 供 Core PostgresTenant 写 tenant_lifecycle（create/freeze/unfreeze/disable）。
// - RequestID：网关 X-Request-ID 中间件
// - ActorUserID：优先可信头 X-ANI-Actor-User-ID（tenant-service 透传），否则回退认证主体
func withTenantLifecycleCtx(ctx context.Context, c *app.RequestContext) context.Context {
	return runtimeadapter.WithTenantLifecycleAttribution(ctx, middleware.GetRequestID(c), adminActorUserID(c))
}

func (api *adminTenantAPI) getTenantAuth(ctx context.Context, c *app.RequestContext) {
	if api.tenant == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "TENANT_UNAVAILABLE", "tenant service unavailable")
		return
	}
	auth, err := api.tenant.GetTenantAuth(ctx, c.Param("tenant_id"))
	if err != nil {
		writeAdminTenantError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAdminTenantAuthResponse(auth))
}

func (api *adminTenantAPI) updateTenantAuth(ctx context.Context, c *app.RequestContext) {
	if api.tenant == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "TENANT_UNAVAILABLE", "tenant service unavailable")
		return
	}
	var patch ports.TenantAuthPatch
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(c.Request.Body(), &raw); err != nil {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	if v, ok := raw["sso_enabled"]; ok {
		var b bool
		if json.Unmarshal(v, &b) == nil {
			patch.SsoEnabled = &b
		}
	}
	if v, ok := raw["provider"]; ok {
		// 未传 / null → 不更新；"" → 清空
		if string(v) != "null" {
			var str string
			if json.Unmarshal(v, &str) == nil {
				patch.SsoProvider = &str
			}
		}
	}
	if v, ok := raw["mfa_required"]; ok {
		var b bool
		if json.Unmarshal(v, &b) == nil {
			patch.MfaRequired = &b
		}
	}
	auth, err := api.tenant.UpdateTenantAuth(ctx, c.Param("tenant_id"), patch)
	if err != nil {
		writeAdminTenantError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAdminTenantAuthResponse(auth))
}

func (api *adminTenantAPI) listTenantLifecycle(ctx context.Context, c *app.RequestContext) {
	if api.tenant == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "TENANT_UNAVAILABLE", "tenant service unavailable")
		return
	}
	limit := 20
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
			if limit > 100 {
				limit = 100
			}
		}
	}
	listed, err := api.tenant.ListTenantLifecycle(ctx, c.Param("tenant_id"), ports.TenantLifecycleFilter{
		Limit:  limit,
		Cursor: c.Query("cursor"),
		Action: ports.TenantLifecycleAction(c.Query("action")),
	})
	if err != nil {
		writeAdminTenantError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(listed.Items))
	for _, it := range listed.Items {
		items = append(items, map[string]any{
			"id":         it.ID,
			"action":     it.Action,
			"reason":     derefStringOrNil(it.Reason),
			"user_id":    derefStringOrNil(it.UserID),
			"request_id": derefStringOrNil(it.RequestID),
			"created_at": it.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nullIfEmpty(listed.NextCursor),
	})
}

func toAdminTenantResponse(t ports.Tenant) map[string]any {
	auth := map[string]any{"sso_enabled": false, "mfa_required": false}
	if t.Auth != nil {
		auth["sso_enabled"] = t.Auth.SsoEnabled
		auth["mfa_required"] = t.Auth.MfaRequired
	}
	return map[string]any{
		"id":            t.ID,
		"name":          t.Name,
		"display_name":  t.DisplayName,
		"status":        string(t.Status),
		"plan_id":       t.PlanID,
		"contact_email": nullIfEmpty(t.ContactEmail),
		"frozen_at":     timePtrRFC3339OrNil(t.FrozenAt),
		"disabled_at":   timePtrRFC3339OrNil(t.DisabledAt),
		"user_count":    t.UserCount,
		"admin_count":   t.AdminCount,
		"auth":          auth,
		"created_at":    t.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updated_at":    t.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func toAdminTenantAuthResponse(a ports.TenantAuth) map[string]any {
	return map[string]any{
		"sso_enabled":  a.SsoEnabled,
		"mfa_required": a.MfaRequired,
		"provider":     derefStringOrNil(a.SsoProvider),
		"updated_at":   a.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func derefStringOrNil(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func timePtrRFC3339OrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// adminActorUserID 解析写入 tenant_lifecycle.user_id 的操作者。
// 优先可信头 X-ANI-Actor-User-ID（tenant-service 透传的 BOSS 平台用户）；
// 非法/缺失时回退当前认证主体（直调 Core admin API）。
func adminActorUserID(c *app.RequestContext) string {
	if raw := strings.TrimSpace(string(c.GetHeader("X-ANI-Actor-User-ID"))); raw != "" {
		if _, err := uuid.Parse(raw); err == nil {
			return raw
		}
	}
	return middleware.GetUserID(c)
}

func writeAdminTenantError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", err.Error())
	case errors.Is(err, ports.ErrTenantNotFound):
		writeDemoError(c, http.StatusNotFound, "TENANT_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrTenantPlanNotFound):
		writeDemoError(c, http.StatusNotFound, "TENANT_PLAN_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrTenantNameConflict):
		writeDemoError(c, http.StatusConflict, "TENANT_NAME_CONFLICT", err.Error())
	case errors.Is(err, ports.ErrTenantStateInvalid):
		writeDemoError(c, http.StatusConflict, "TENANT_STATE_INVALID", err.Error())
	case errors.Is(err, ports.ErrUnsupported):
		writeDemoError(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", err.Error())
	default:
		writeDemoError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}
