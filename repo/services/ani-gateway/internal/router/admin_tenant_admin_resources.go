package router

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/kubercloud/ani/pkg/ports"
)

// 本文件实现 Core 层租户管理员用户管理网关接入：/api/v1/admin/tenants/*/users/*
// 与 /api/v1/admin/tenant-users。所有端点直接调用 Core SDK
// （ports.TenantAdminService），不经过 tenant-service gRPC。
// 对齐 Core OpenAPI v1.yaml 中 TenantUsers 标签下的全部端点。

// adminTenantAdminAPI 持有 Core TenantAdminService。
type adminTenantAdminAPI struct {
	admin ports.TenantAdminService
}

// registerAdminTenantAdminResources 注册 Core 层租户成员管理端点。
//
//	GET    /admin/tenant-users
//	GET    /admin/tenants/:tenant_id/user-lookup
//	GET    /admin/tenants/:tenant_id/users/:user_id
//	GET    /admin/tenants/:tenant_id/users/batch
//	DELETE /admin/tenants/:tenant_id/users/:user_id
//	GET    /admin/tenants/:tenant_id/users/:user_id/role
//	PUT    /admin/tenants/:tenant_id/users/:user_id/role
//	GET    /admin/tenants/:tenant_id/users/:user_id/changeable-roles
//	GET    /admin/tenants/:tenant_id/roles
//	POST   /admin/tenants/:tenant_id/users/:user_id/status
//	POST   /admin/tenants/:tenant_id/users/:user_id/reset-password
func registerAdminTenantAdminResources(v1 *route.RouterGroup, admin ports.TenantAdminService) {
	if admin == nil {
		return
	}
	api := &adminTenantAdminAPI{admin: admin}
	v1.GET("/admin/tenant-users", api.listTenantUsers)
	v1.GET("/admin/tenants/:tenant_id/user-lookup", api.lookupTenantUser)
	v1.GET("/admin/tenants/:tenant_id/users/:user_id", api.getTenantUser)
	v1.GET("/admin/tenants/:tenant_id/users/batch", api.batchGetUsers)
	v1.DELETE("/admin/tenants/:tenant_id/users/:user_id", api.deleteTenantUser)
	v1.GET("/admin/tenants/:tenant_id/users/:user_id/role", api.getTenantUserRole)
	v1.PUT("/admin/tenants/:tenant_id/users/:user_id/role", api.updateTenantUserRole)
	v1.GET("/admin/tenants/:tenant_id/users/:user_id/changeable-roles", api.getChangeableRoles)
	v1.GET("/admin/tenants/:tenant_id/roles", api.listAssignableRoles)
	v1.POST("/admin/tenants/:tenant_id/users/:user_id/status", api.updateTenantUserStatus)
	v1.POST("/admin/tenants/:tenant_id/users/:user_id/reset-password", api.resetTenantUserPassword)
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (api *adminTenantAdminAPI) listTenantUsers(ctx context.Context, c *app.RequestContext) {
	// 1. 组装过滤参数
	filter := ports.UserListFilter{
		Limit:    int(cursorLimit(c)),
		Cursor:   c.Query("cursor"),
		TenantID: c.Query("tenant_id"),
		Role:     c.Query("role"),
		Status:   c.Query("status"),
		Search:   c.Query("search"),
	}
	// 2. 调用 Core SDK
	result, err := api.admin.ListUsers(ctx, filter)
	if err != nil {
		writeAdminTenantAdminError(c, err)
		return
	}
	// 3. 组装响应
	items := make([]map[string]any, 0, len(result.Items))
	for _, u := range result.Items {
		items = append(items, adminTenantUserJSON(u))
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nullIfEmpty(result.NextCursor),
	})
}

func (api *adminTenantAdminAPI) lookupTenantUser(ctx context.Context, c *app.RequestContext) {
	// 1. 解析查询参数
	tenantID := c.Param("tenant_id")
	email := c.Query("email")
	username := c.Query("username")
	// 2. 调用 Core SDK
	user, err := api.admin.LookupUser(ctx, tenantID, email, username)
	if err != nil {
		writeAdminTenantAdminError(c, err)
		return
	}
	// 3. 返回匹配到的用户
	c.JSON(http.StatusOK, adminTenantUserJSON(user))
}

func (api *adminTenantAdminAPI) getTenantUser(ctx context.Context, c *app.RequestContext) {
	// 1. 解析路径参数
	tenantID := c.Param("tenant_id")
	userID := c.Param("user_id")
	// 2. 调用 Core SDK
	user, err := api.admin.GetUser(ctx, tenantID, userID)
	if err != nil {
		writeAdminTenantAdminError(c, err)
		return
	}
	// 3. 返回用户详情
	c.JSON(http.StatusOK, adminTenantUserJSON(user))
}

func (api *adminTenantAdminAPI) batchGetUsers(ctx context.Context, c *app.RequestContext) {
	tenantID := c.Param("tenant_id")
	rawIDs := strings.TrimSpace(c.Query("user_ids"))
	if rawIDs == "" {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "user_ids required")
		return
	}
	userIDs := strings.Split(rawIDs, ",")
	for i := range userIDs {
		userIDs[i] = strings.TrimSpace(userIDs[i])
	}
	users, err := api.admin.BatchGetUsers(ctx, tenantID, userIDs)
	if err != nil {
		writeAdminTenantAdminError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(users))
	for _, u := range users {
		items = append(items, adminTenantUserJSON(u))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items})
}

func (api *adminTenantAdminAPI) deleteTenantUser(ctx context.Context, c *app.RequestContext) {
	// 1. 解析路径参数
	tenantID := c.Param("tenant_id")
	userID := c.Param("user_id")
	// 2. 调用 Core SDK（软删除，不幂等）
	if err := api.admin.SoftDelete(ctx, tenantID, userID); err != nil {
		writeAdminTenantAdminError(c, err)
		return
	}
	// 3. 返回结果
	c.JSON(http.StatusOK, map[string]any{
		"id":      userID,
		"message": "deleted",
	})
}

func (api *adminTenantAdminAPI) getTenantUserRole(ctx context.Context, c *app.RequestContext) {
	// 1. 解析路径参数
	tenantID := c.Param("tenant_id")
	userID := c.Param("user_id")
	// 2. 调用 Core SDK
	perms, err := api.admin.GetRolePermissions(ctx, tenantID, userID)
	if err != nil {
		writeAdminTenantAdminError(c, err)
		return
	}
	// 3. 返回角色与权限
	c.JSON(http.StatusOK, adminUserPermissionsJSON(perms))
}

func (api *adminTenantAdminAPI) updateTenantUserRole(ctx context.Context, c *app.RequestContext) {
	// 1. 解析请求体
	var body struct {
		Role string `json:"role"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeAdminTenantAdminError(c, ports.ErrInvalid)
		return
	}
	// 2. 解析路径参数
	tenantID := c.Param("tenant_id")
	userID := c.Param("user_id")
	// 3. 调用 Core SDK
	if err := api.admin.ChangeRole(ctx, tenantID, userID, body.Role); err != nil {
		writeAdminTenantAdminError(c, err)
		return
	}
	// 4. 返回结果
	c.JSON(http.StatusOK, map[string]any{
		"id":      userID,
		"message": "role updated",
	})
}

func (api *adminTenantAdminAPI) getChangeableRoles(ctx context.Context, c *app.RequestContext) {
	// 1. 解析路径参数
	tenantID := c.Param("tenant_id")
	userID := c.Param("user_id")
	// 2. 调用 Core SDK
	roles, err := api.admin.GetChangeableRoles(ctx, tenantID, userID)
	if err != nil {
		writeAdminTenantAdminError(c, err)
		return
	}
	// 3. 组装可变角色列表
	options := make([]map[string]any, 0, len(roles.Options))
	for _, opt := range roles.Options {
		options = append(options, map[string]any{
			"role":  opt.Role,
			"label": opt.Label,
		})
	}
	// 4. 返回结果
	c.JSON(http.StatusOK, map[string]any{
		"current_role":     roles.CurrentRole,
		"changeable_roles": options,
	})
}

func (api *adminTenantAdminAPI) listAssignableRoles(ctx context.Context, c *app.RequestContext) {
	// 1. 解析路径参数
	tenantID := c.Param("tenant_id")
	// 2. 调用 Core SDK
	roles, err := api.admin.ListAssignableRoles(ctx, tenantID)
	if err != nil {
		writeAdminTenantAdminError(c, err)
		return
	}
	// 3. 组装 { items: [{id,tenant_id,name,permissions}] }
	items := make([]map[string]any, 0, len(roles))
	for _, r := range roles {
		item := map[string]any{
			"id":          r.ID.String(),
			"name":        r.Name,
			"permissions": r.Permissions,
		}
		if r.TenantID != nil {
			item["tenant_id"] = r.TenantID.String()
		} else {
			item["tenant_id"] = nil
		}
		if item["permissions"] == nil {
			item["permissions"] = []any{}
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, map[string]any{"items": items})
}

func (api *adminTenantAdminAPI) updateTenantUserStatus(ctx context.Context, c *app.RequestContext) {
	// 1. 解析请求体
	var body struct {
		Status string `json:"status"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeAdminTenantAdminError(c, ports.ErrInvalid)
		return
	}
	// 2. 解析路径参数
	tenantID := c.Param("tenant_id")
	userID := c.Param("user_id")
	// 3. 调用 Core SDK
	if err := api.admin.SetStatus(ctx, tenantID, userID, body.Status); err != nil {
		writeAdminTenantAdminError(c, err)
		return
	}
	// 4. 返回结果
	c.JSON(http.StatusOK, map[string]any{
		"id":      userID,
		"message": "status updated",
	})
}

func (api *adminTenantAdminAPI) resetTenantUserPassword(ctx context.Context, c *app.RequestContext) {
	// 1. 解析请求体
	var body struct {
		NewPassword string `json:"new_password"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeAdminTenantAdminError(c, ports.ErrInvalid)
		return
	}
	// 2. 解析路径参数
	tenantID := c.Param("tenant_id")
	userID := c.Param("user_id")
	// 3. 调用 Core SDK（明文不落日志）
	if err := api.admin.ResetPassword(ctx, tenantID, userID, body.NewPassword); err != nil {
		writeAdminTenantAdminError(c, err)
		return
	}
	// 4. 返回结果
	c.JSON(http.StatusOK, map[string]any{
		"id":      userID,
		"message": "password reset",
	})
}

// ── JSON mappers ──────────────────────────────────────────────────────────────

func adminTenantUserJSON(u ports.User) map[string]any {
	out := map[string]any{
		"id":         u.ID,
		"email":      u.Email,
		"username":   u.Username,
		"role":       u.Role,
		"status":     u.Status,
		"source":     u.Source,
		"created_at": u.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": u.UpdatedAt.UTC().Format(time.RFC3339),
		"tenant": map[string]any{
			"id":           u.Tenant.ID,
			"name":         u.Tenant.Name,
			"display_name": u.Tenant.DisplayName,
		},
	}
	if u.DisplayName != nil {
		out["display_name"] = *u.DisplayName
	} else {
		out["display_name"] = nil
	}
	if u.LastLoginAt != nil {
		out["last_login_at"] = u.LastLoginAt.UTC().Format(time.RFC3339)
	} else {
		out["last_login_at"] = nil
	}
	return out
}

func adminUserPermissionsJSON(perms ports.UserPermissions) map[string]any {
	permItems := make([]any, 0, len(perms.Permissions))
	for _, p := range perms.Permissions {
		permItems = append(permItems, map[string]any{
			"resource": p.Resource,
			"action":   p.Action,
			"scope":    p.Scope,
		})
	}
	return map[string]any{
		"user_id":     perms.UserID,
		"tenant_id":   perms.TenantID,
		"role":        perms.Role,
		"permissions": permItems,
	}
}

// ── Error mapping ─────────────────────────────────────────────────────────────

func writeAdminTenantAdminError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", err.Error())
	case errors.Is(err, ports.ErrUserNotFound):
		writeDemoError(c, http.StatusNotFound, "USER_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrRoleChangeInvalid):
		writeDemoError(c, http.StatusUnprocessableEntity, "ROLE_CHANGE_INVALID", err.Error())
	case errors.Is(err, ports.ErrPasswordSameAsOld):
		writeDemoError(c, http.StatusUnprocessableEntity, "PASSWORD_SAME_AS_OLD", err.Error())
	default:
		writeDemoError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}
