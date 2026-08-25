package router

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	gatewayerrors "github.com/kubercloud/ani/services/ani-gateway/internal/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// 本文件实现租户管理员网关接入：/api/v1/svc/tenant-admins* 与
// /api/v1/svc/tenants/{tenantId}/admins/*。
// 所有端点经 gRPC 转发 tenant-service 的 TenantAdminService RPC；
// tenant-service 内部通过 Core SDK 操作 users/user_roles/roles 表。

// tenantAdminAPI 持有 tenant-service 的 TenantAdminService gRPC 客户端。
// conn 建立失败时字段为 nil，由各 handler 做 nil 守卫兜底返回 502。
type tenantAdminAPI struct {
	admins tenantv1.TenantAdminServiceClient
}

func registerTenantAdmins(svc *route.RouterGroup) {
	registerTenantAdminsWithClient(svc, dialTenantAdminGRPCClient())
}

func registerTenantAdminsWithClient(svc *route.RouterGroup, client tenantv1.TenantAdminServiceClient) {
	api := &tenantAdminAPI{admins: client}

	// 读端点 — gRPC
	svc.GET("/tenant-admins/tenants", api.listAvailableTenants)
	svc.GET("/tenant-admins", api.listAllTenantAdmins)
	svc.GET("/tenants/:tenantId/admins/:userId", api.getTenantAdminDetail)
	svc.GET("/tenants/:tenantId/admins/:userId/audit-logs", api.listTenantAdminAuditLogs)

	// 写端点 — gRPC（邀请/重发）
	svc.POST("/tenants/:tenantId/admins/invite", api.inviteTenantAdmin)
	svc.POST("/tenants/:tenantId/admins/:userId/invitation/resend", api.resendTenantAdminInvitation)

	// 用户/角色 CRUD — gRPC（service 内部通过 Core SDK 操作 users 表）
	svc.PUT("/tenants/:tenantId/admins/:userId/role", api.updateTenantAdminRole)
	svc.GET("/tenants/:tenantId/admins/:userId/role", api.getTenantAdminRole)
	svc.GET("/tenants/:tenantId/admins/:userId/changeable-roles", api.getChangeableRoles)
	svc.POST("/tenants/:tenantId/admins/:userId/reset-password", api.resetTenantAdminPassword)
	svc.POST("/tenants/:tenantId/admins/:userId/disable", api.disableTenantAdmin)
	svc.POST("/tenants/:tenantId/admins/:userId/enable", api.enableTenantAdmin)
	svc.DELETE("/tenants/:tenantId/admins/:userId", api.deleteTenantAdmin)
}

func dialTenantAdminGRPCClient() tenantv1.TenantAdminServiceClient {
	addr := strings.TrimSpace(os.Getenv("TENANT_SERVICE_ADDR"))
	if addr == "" {
		addr = tenantServiceDefaultAddr
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil
	}
	return tenantv1.NewTenantAdminServiceClient(conn)
}

// ── gRPC 转发 handlers（邀请生命周期 + 只读查询）─────────────────────────────

// listAvailableTenants returns all non-disabled tenants for the invite-admin
// tenant selector (gRPC → tenant-service).
func (api *tenantAdminAPI) listAvailableTenants(ctx context.Context, c *app.RequestContext) {
	// 1. 校验 gRPC 客户端依赖是否就绪
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	// 2. 构造 gRPC 调用上下文（携带认证 metadata）
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 3. 调用 tenant-service ListAvailableTenants RPC
	res, err := api.admins.ListAvailableTenants(callCtx, &tenantv1.ListAvailableTenantsRequest{})
	if err != nil {
		mapTenantAdminGRPCError(ctx, c, err)
		return
	}
	// 4. 组装响应 JSON
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, t := range res.GetItems() {
		items = append(items, map[string]any{
			"id":           t.GetId(),
			"name":         t.GetName(),
			"display_name": t.GetDisplayName(),
			"status":       t.GetStatus(),
		})
	}
	// 5. 返回列表
	c.JSON(http.StatusOK, map[string]any{"items": items})
}

func (api *tenantAdminAPI) listAllTenantAdmins(ctx context.Context, c *app.RequestContext) {
	// 1. 校验 gRPC 客户端依赖是否就绪
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	// 2. 构造 gRPC 调用上下文（携带认证 metadata）
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 3. 组装请求参数
	req := &tenantv1.ListAllTenantAdminsRequest{
		TenantId: c.Query("tenant_id"),
		Status:   c.Query("status"),
		Search:   c.Query("search"),
		Page:     &commonv1.CursorPageRequest{Limit: cursorLimit(c), Cursor: c.Query("cursor")},
	}
	if v := strings.TrimSpace(c.Query("is_inviting")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			req.IsInviting = wrapperspb.Bool(b)
		}
	}
	if v := strings.TrimSpace(c.Query("is_expired")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			req.IsExpired = wrapperspb.Bool(b)
		}
	}
	// 4. 调用 tenant-service ListAllTenantAdmins RPC
	res, err := api.admins.ListAllTenantAdmins(callCtx, req)
	if err != nil {
		mapTenantAdminGRPCError(ctx, c, err)
		return
	}
	// 5. 组装并返回列表
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, item := range res.GetItems() {
		items = append(items, adminWithTenantJSON(item, false))
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nullIfEmpty(res.GetNextCursor()),
	})
}

func (api *tenantAdminAPI) getTenantAdminDetail(ctx context.Context, c *app.RequestContext) {
	// 1. 校验 gRPC 客户端依赖是否就绪
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	// 2. 构造 gRPC 调用上下文
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 3. 调用 tenant-service GetTenantAdminDetail RPC
	res, err := api.admins.GetTenantAdminDetail(callCtx, &tenantv1.GetTenantAdminDetailRequest{
		TenantId: c.Param("tenantId"),
		UserId:   c.Param("userId"),
	})
	if err != nil {
		mapTenantAdminGRPCError(ctx, c, err)
		return
	}
	// 4. 返回详情
	c.JSON(http.StatusOK, adminWithTenantJSON(res, true))
}

func (api *tenantAdminAPI) listTenantAdminAuditLogs(ctx context.Context, c *app.RequestContext) {
	// 1. 校验 gRPC 客户端依赖是否就绪
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	// 2. 构造 gRPC 调用上下文
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 3. 调用 tenant-service ListTenantAdminAuditLogs RPC
	res, err := api.admins.ListTenantAdminAuditLogs(callCtx, &tenantv1.ListTenantAdminAuditLogsRequest{
		TenantId: c.Param("tenantId"),
		UserId:   c.Param("userId"),
		Action:   c.Query("action"),
		Result:   c.Query("result"),
		Page:     &commonv1.CursorPageRequest{Limit: cursorLimit(c), Cursor: c.Query("cursor")},
	})
	if err != nil {
		mapTenantAdminGRPCError(ctx, c, err)
		return
	}
	// 4. 组装并返回审计日志列表
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, item := range res.GetItems() {
		items = append(items, tenantAdminAuditLogJSON(item))
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nullIfEmpty(res.GetNextCursor()),
	})
}

func (api *tenantAdminAPI) inviteTenantAdmin(ctx context.Context, c *app.RequestContext) {
	// 1. 校验 gRPC 客户端依赖是否就绪
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	// 2. 解析请求体
	var body struct {
		Email          string `json:"email"`
		Username       string `json:"username"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeTenantAdminError(ctx, c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	// 3. 补充幂等键（优先取 header）
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	// 4. 构造 gRPC 调用上下文
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 5. 调用 tenant-service InviteTenantAdmin RPC
	res, err := api.admins.InviteTenantAdmin(callCtx, &tenantv1.InviteTenantAdminRequest{
		TenantId:       c.Param("tenantId"),
		Email:          body.Email,
		Username:       body.Username,
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		mapTenantAdminGRPCError(ctx, c, err)
		return
	}
	// 6. 返回邀请结果
	c.JSON(http.StatusOK, invitationResultJSON(res))
}

func (api *tenantAdminAPI) resendTenantAdminInvitation(ctx context.Context, c *app.RequestContext) {
	// 1. 校验 gRPC 客户端依赖是否就绪
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	// 2. 解析请求体
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeTenantAdminError(ctx, c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	// 3. 补充幂等键
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	// 4. 构造 gRPC 调用上下文
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 5. 调用 tenant-service ResendTenantAdminInvitation RPC
	res, err := api.admins.ResendTenantAdminInvitation(callCtx, &tenantv1.ResendTenantAdminInvitationRequest{
		TenantId:       c.Param("tenantId"),
		UserId:         c.Param("userId"),
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		mapTenantAdminGRPCError(ctx, c, err)
		return
	}
	// 6. 返回重发结果
	c.JSON(http.StatusOK, invitationResultJSON(res))
}

// ── gRPC 转发 handlers（用户/角色 CRUD）──────────────────────────────────────

func (api *tenantAdminAPI) updateTenantAdminRole(ctx context.Context, c *app.RequestContext) {
	// 1. 校验 gRPC 客户端依赖是否就绪
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	// 2. 解析请求体
	var body struct {
		Role           string `json:"role"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeTenantAdminError(ctx, c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	// 3. 补充幂等键（优先取 header）
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	// 4. 构造 gRPC 调用上下文
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 5. 调用 tenant-service UpdateTenantAdminRole RPC
	res, err := api.admins.UpdateTenantAdminRole(callCtx, &tenantv1.UpdateTenantAdminRoleRequest{
		TenantId:       c.Param("tenantId"),
		UserId:         c.Param("userId"),
		Role:           body.Role,
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		mapTenantAdminGRPCError(ctx, c, err)
		return
	}
	// 6. 返回幂等结果
	c.JSON(http.StatusOK, idempotentResultJSON(res))
}

func (api *tenantAdminAPI) getTenantAdminRole(ctx context.Context, c *app.RequestContext) {
	// 1. 校验 gRPC 客户端依赖是否就绪
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	// 2. 构造 gRPC 调用上下文
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 3. 调用 tenant-service GetTenantAdminRole RPC
	res, err := api.admins.GetTenantAdminRole(callCtx, &tenantv1.GetTenantAdminRoleRequest{
		TenantId: c.Param("tenantId"),
		UserId:   c.Param("userId"),
	})
	if err != nil {
		mapTenantAdminGRPCError(ctx, c, err)
		return
	}
	// 4. 组装并返回权限 JSON
	c.JSON(http.StatusOK, userPermissionsJSON(res))
}

func (api *tenantAdminAPI) getChangeableRoles(ctx context.Context, c *app.RequestContext) {
	// 1. 校验 gRPC 客户端依赖是否就绪
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	// 2. 构造 gRPC 调用上下文
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 3. 调用 tenant-service GetChangeableRoles RPC
	res, err := api.admins.GetChangeableRoles(callCtx, &tenantv1.GetChangeableRolesRequest{
		TenantId: c.Param("tenantId"),
		UserId:   c.Param("userId"),
	})
	if err != nil {
		mapTenantAdminGRPCError(ctx, c, err)
		return
	}
	// 4. 组装可变角色列表
	options := make([]map[string]any, 0, len(res.GetChangeableRoles()))
	for _, opt := range res.GetChangeableRoles() {
		options = append(options, map[string]any{
			"role":  opt.GetRole(),
			"label": opt.GetLabel(),
		})
	}
	// 5. 返回结果
	c.JSON(http.StatusOK, map[string]any{
		"current_role":     res.GetCurrentRole(),
		"changeable_roles": options,
	})
}

func (api *tenantAdminAPI) resetTenantAdminPassword(ctx context.Context, c *app.RequestContext) {
	// 1. 校验 gRPC 客户端依赖是否就绪
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	// 2. 解析请求体
	var body struct {
		NewPassword    string `json:"new_password"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeTenantAdminError(ctx, c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	// 3. 补充幂等键
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	// 4. 构造 gRPC 调用上下文
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 5. 调用 tenant-service ResetTenantAdminPassword RPC
	res, err := api.admins.ResetTenantAdminPassword(callCtx, &tenantv1.ResetTenantAdminPasswordRequest{
		TenantId:       c.Param("tenantId"),
		UserId:         c.Param("userId"),
		NewPassword:    body.NewPassword,
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		mapTenantAdminGRPCError(ctx, c, err)
		return
	}
	// 6. 返回幂等结果
	c.JSON(http.StatusOK, idempotentResultJSON(res))
}

func (api *tenantAdminAPI) disableTenantAdmin(ctx context.Context, c *app.RequestContext) {
	// 1. 校验 gRPC 客户端依赖是否就绪
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	// 2. 解析幂等键（可选 body）
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	_ = c.BindJSON(&body)
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	// 3. 构造 gRPC 调用上下文
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 4. 调用 tenant-service DisableTenantAdmin RPC
	res, err := api.admins.DisableTenantAdmin(callCtx, &tenantv1.DisableTenantAdminRequest{
		TenantId:       c.Param("tenantId"),
		UserId:         c.Param("userId"),
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		mapTenantAdminGRPCError(ctx, c, err)
		return
	}
	// 5. 返回幂等结果
	c.JSON(http.StatusOK, idempotentResultJSON(res))
}

func (api *tenantAdminAPI) enableTenantAdmin(ctx context.Context, c *app.RequestContext) {
	// 1. 校验 gRPC 客户端依赖是否就绪
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	// 2. 解析幂等键
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	_ = c.BindJSON(&body)
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	// 3. 构造 gRPC 调用上下文
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 4. 调用 tenant-service EnableTenantAdmin RPC
	res, err := api.admins.EnableTenantAdmin(callCtx, &tenantv1.EnableTenantAdminRequest{
		TenantId:       c.Param("tenantId"),
		UserId:         c.Param("userId"),
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		mapTenantAdminGRPCError(ctx, c, err)
		return
	}
	// 5. 返回幂等结果
	c.JSON(http.StatusOK, idempotentResultJSON(res))
}

func (api *tenantAdminAPI) deleteTenantAdmin(ctx context.Context, c *app.RequestContext) {
	// 1. 校验 gRPC 客户端依赖是否就绪
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	// 2. 构造 gRPC 调用上下文（DELETE 不幂等，不携带 idempotency_key）
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 3. 调用 tenant-service DeleteTenantAdmin RPC
	res, err := api.admins.DeleteTenantAdmin(callCtx, &tenantv1.DeleteTenantAdminRequest{
		TenantId: c.Param("tenantId"),
		UserId:   c.Param("userId"),
	})
	if err != nil {
		mapTenantAdminGRPCError(ctx, c, err)
		return
	}
	// 4. 返回结果
	c.JSON(http.StatusOK, idempotentResultJSON(res))
}

// ── JSON mappers ─────────────────────────────────────────────────────────────

func adminWithTenantJSON(item *tenantv1.AdminWithTenant, includeTimestamps bool) map[string]any {
	if item == nil {
		return map[string]any{}
	}
	out := map[string]any{
		"id":            item.GetId(),
		"email":         item.GetEmail(),
		"username":      item.GetUsername(),
		"role":          item.GetRole(),
		"status":        item.GetStatus(),
		"is_inviting":   item.GetIsInviting(),
		"is_expired":    item.GetIsExpired(),
		"source":        item.GetSource(),
		"last_login_at": pbRFC3339(item.GetLastLoginAt()),
		"tenant": map[string]any{
			"id":           item.GetTenant().GetId(),
			"name":         item.GetTenant().GetName(),
			"display_name": item.GetTenant().GetDisplayName(),
		},
	}
	if item.GetDisplayName() != nil {
		out["display_name"] = item.GetDisplayName().GetValue()
	} else {
		out["display_name"] = nil
	}
	if includeTimestamps {
		out["created_at"] = pbRFC3339(item.GetCreatedAt())
		out["updated_at"] = pbRFC3339(item.GetUpdatedAt())
	}
	return out
}

func invitationResultJSON(res *tenantv1.InvitationResult) map[string]any {
	if res == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":        res.GetId(),
		"token":     res.GetToken(),
		"expire_at": pbRFC3339(res.GetExpireAt()),
		"message":   res.GetMessage(),
	}
}

func tenantAdminAuditLogJSON(item *tenantv1.TenantAdminAuditLog) map[string]any {
	if item == nil {
		return map[string]any{}
	}
	out := map[string]any{
		"id":         item.GetId(),
		"action":     item.GetAction(),
		"resource":   item.GetResource(),
		"result":     item.GetResult(),
		"created_at": pbRFC3339(item.GetCreatedAt()),
	}
	if item.GetUserId() != nil {
		out["user_id"] = item.GetUserId().GetValue()
	} else {
		out["user_id"] = nil
	}
	if item.GetDetails() != nil {
		out["details"] = item.GetDetails().AsMap()
	} else {
		out["details"] = nil
	}
	return out
}

func userPermissionsJSON(perms *tenantv1.UserPermissions) map[string]any {
	if perms == nil {
		return map[string]any{}
	}
	// permissions 为 ListValue（resource/action/scope JSONB 数组），直接透传为 []any
	var permItems []any
	if lv := perms.GetPermissions(); lv != nil && len(lv.GetValues()) > 0 {
		permItems = make([]any, 0, len(lv.GetValues()))
		for _, v := range lv.GetValues() {
			if s := v.GetStructValue(); s != nil {
				permItems = append(permItems, s.AsMap())
			} else {
				permItems = append(permItems, nil)
			}
		}
	} else {
		permItems = []any{}
	}
	out := map[string]any{
		"user_id":     perms.GetUserId(),
		"role":        perms.GetRole(),
		"permissions": permItems,
	}
	if tID := perms.GetTenantId(); tID != nil {
		out["tenant_id"] = tID.GetValue()
	} else {
		out["tenant_id"] = nil
	}
	return out
}

func idempotentResultJSON(res *commonv1.IdempotentResult) map[string]any {
	if res == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":      res.GetId(),
		"message": res.GetMessage(),
	}
}

func pbRFC3339(ts *timestamppb.Timestamp) any {
	if ts == nil {
		return nil
	}
	t := ts.AsTime()
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

// ── Error mapping ────────────────────────────────────────────────────────────

var tenantAdminBusinessCodeByHTTP = map[string]int{
	"VALIDATION_FAILED":                 http.StatusBadRequest,
	"TENANT_ADMIN_NOT_FOUND":            http.StatusNotFound,
	"TENANT_ADMIN_ALREADY_ADMIN":        http.StatusConflict,
	"TENANT_INVITATION_PENDING":         http.StatusConflict,
	"TENANT_ADMIN_INVITATION_NOT_FOUND": http.StatusNotFound,
	"TENANT_INVITATION_SETTLED":         http.StatusConflict,
	"ROLE_CHANGE_INVALID":               http.StatusUnprocessableEntity,
	"PASSWORD_SAME_AS_OLD":              http.StatusUnprocessableEntity,
	"TENANT_NOT_FOUND":                  http.StatusNotFound,
	"FORBIDDEN":                         http.StatusForbidden,
	"GRPC_CLIENT_UNAVAILABLE":           http.StatusBadGateway,
}

var sortedTenantAdminBusinessCodes = func() []string {
	codes := make([]string, 0, len(tenantAdminBusinessCodeByHTTP))
	for code := range tenantAdminBusinessCodeByHTTP {
		codes = append(codes, code)
	}
	for i := 0; i < len(codes); i++ {
		for j := i + 1; j < len(codes); j++ {
			if len(codes[j]) > len(codes[i]) {
				codes[i], codes[j] = codes[j], codes[i]
			}
		}
	}
	return codes
}()

func mapTenantAdminGRPCError(ctx context.Context, c *app.RequestContext, err error) {
	msg := status.Convert(err).Message()
	for _, code := range sortedTenantAdminBusinessCodes {
		if strings.HasPrefix(msg, code+":") || msg == code {
			writeTenantAdminError(ctx, c, tenantAdminBusinessCodeByHTTP[code], code, strings.TrimSpace(strings.TrimPrefix(msg, code+":")))
			return
		}
	}
	switch status.Code(err) {
	case codes.Unimplemented:
		writeTenantAdminError(ctx, c, http.StatusNotImplemented, "NOT_IMPLEMENTED", msg)
	case codes.NotFound:
		writeTenantAdminError(ctx, c, http.StatusNotFound, "TENANT_ADMIN_NOT_FOUND", msg)
	case codes.InvalidArgument:
		writeTenantAdminError(ctx, c, http.StatusBadRequest, "VALIDATION_FAILED", msg)
	case codes.AlreadyExists, codes.FailedPrecondition, codes.Aborted:
		writeTenantAdminError(ctx, c, http.StatusConflict, "CONFLICT", msg)
	case codes.DeadlineExceeded:
		writeTenantAdminError(ctx, c, http.StatusGatewayTimeout, "GATEWAY_TIMEOUT", msg)
	case codes.Unavailable:
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", msg)
	default:
		writeTenantAdminError(ctx, c, http.StatusInternalServerError, gatewayerrors.CodeInternalError, msg)
	}
}

func writeTenantAdminError(ctx context.Context, c *app.RequestContext, statusCode int, code, message string) {
	gatewayerrors.RespondError(ctx, c, statusCode, code, message)
}
