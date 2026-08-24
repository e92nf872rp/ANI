package router

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/pkg/ports"
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
// 邀请/重发/跨租户列表/详情/审计经 gRPC 转发 tenant-service；
// 用户/角色写读（改角色/查权限/重置/禁用启用/删除）经 TenantAdminService 直连 Core DB。
type tenantAdminAPI struct {
	admins      tenantv1.TenantAdminServiceClient
	tenantAdmin ports.TenantAdminService
	tenantSvc   ports.TenantService
}

func registerTenantAdmins(svc *route.RouterGroup, tenantAdmin ports.TenantAdminService, tenantSvc ports.TenantService) {
	registerTenantAdminsWithClient(svc, tenantAdmin, tenantSvc, dialTenantAdminGRPCClient())
}

func registerTenantAdminsWithClient(svc *route.RouterGroup, tenantAdmin ports.TenantAdminService, tenantSvc ports.TenantService, client tenantv1.TenantAdminServiceClient) {
	api := &tenantAdminAPI{admins: client, tenantAdmin: tenantAdmin, tenantSvc: tenantSvc}

	// 读端点
	svc.GET("/tenant-admins/tenants", api.listActiveTenants)
	svc.GET("/tenant-admins", api.listAllTenantAdmins)
	svc.GET("/tenants/:tenantId/admins/:userId", api.getTenantAdminDetail)
	svc.GET("/tenants/:tenantId/admins/:userId/audit-logs", api.listTenantAdminAuditLogs)

	// 写端点 — gRPC（邀请/重发）
	svc.POST("/tenants/:tenantId/admins/invite", api.inviteTenantAdmin)
	svc.POST("/tenants/:tenantId/admins/:userId/invitation/resend", api.resendTenantAdminInvitation)

	// 写端点 — TenantAdminService（用户/角色）
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

// ── gRPC 转发 handlers ───────────────────────────────────────────────────────

// listActiveTenants returns all non-disabled tenants for the invite-admin
// tenant selector (Core DB direct, no gRPC).
func (api *tenantAdminAPI) listActiveTenants(ctx context.Context, c *app.RequestContext) {
	if api.tenantSvc == nil {
		writeTenantAdminError(ctx, c, http.StatusServiceUnavailable, "TENANT_ADMIN_UNAVAILABLE", "tenant service unavailable")
		return
	}
	tenants, err := api.tenantSvc.ListActiveTenants(ctx)
	if err != nil {
		writeTenantAdminError(ctx, c, http.StatusInternalServerError, gatewayerrors.CodeInternalError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(tenants))
	for _, t := range tenants {
		items = append(items, map[string]any{
			"id":           t.ID,
			"name":         t.Name,
			"display_name": t.DisplayName,
			"status":       t.Status,
		})
	}
	c.JSON(http.StatusOK, map[string]any{"items": items})
}

func (api *tenantAdminAPI) listAllTenantAdmins(ctx context.Context, c *app.RequestContext) {
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
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
	res, err := api.admins.ListAllTenantAdmins(callCtx, req)
	if err != nil {
		mapTenantAdminGRPCError(ctx, c, err)
		return
	}
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
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.admins.GetTenantAdminDetail(callCtx, &tenantv1.GetTenantAdminDetailRequest{
		TenantId: c.Param("tenantId"),
		UserId:   c.Param("userId"),
	})
	if err != nil {
		mapTenantAdminGRPCError(ctx, c, err)
		return
	}
	c.JSON(http.StatusOK, adminWithTenantJSON(res, true))
}

func (api *tenantAdminAPI) listTenantAdminAuditLogs(ctx context.Context, c *app.RequestContext) {
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
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
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	var body struct {
		Email          string `json:"email"`
		Username       string `json:"username"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeTenantAdminError(ctx, c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
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
	c.JSON(http.StatusOK, invitationResultJSON(res))
}

func (api *tenantAdminAPI) resendTenantAdminInvitation(ctx context.Context, c *app.RequestContext) {
	if api.admins == nil {
		writeTenantAdminError(ctx, c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant admin grpc client unavailable")
		return
	}
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeTenantAdminError(ctx, c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.admins.ResendTenantAdminInvitation(callCtx, &tenantv1.ResendTenantAdminInvitationRequest{
		TenantId:       c.Param("tenantId"),
		UserId:         c.Param("userId"),
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		mapTenantAdminGRPCError(ctx, c, err)
		return
	}
	c.JSON(http.StatusOK, invitationResultJSON(res))
}

// ── TenantAdminService handlers ───────────────────────────────────────────────

func (api *tenantAdminAPI) updateTenantAdminRole(ctx context.Context, c *app.RequestContext) {
	if api.tenantAdmin == nil {
		writeTenantAdminError(ctx, c, http.StatusServiceUnavailable, "TENANT_ADMIN_UNAVAILABLE", "tenant admin service unavailable")
		return
	}
	var body struct {
		Role           string `json:"role"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeTenantAdminError(ctx, c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	if err := api.tenantAdmin.ChangeRole(ctx, c.Param("tenantId"), c.Param("userId"), body.Role); err != nil {
		mapTenantAdminCoreError(ctx, c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"id":      c.Param("userId"),
		"message": "role updated",
	})
}

func (api *tenantAdminAPI) getTenantAdminRole(ctx context.Context, c *app.RequestContext) {
	if api.tenantAdmin == nil {
		writeTenantAdminError(ctx, c, http.StatusServiceUnavailable, "TENANT_ADMIN_UNAVAILABLE", "tenant admin service unavailable")
		return
	}
	perms, err := api.tenantAdmin.GetRolePermissions(ctx, c.Param("tenantId"), c.Param("userId"))
	if err != nil {
		mapTenantAdminCoreError(ctx, c, err)
		return
	}
	c.JSON(http.StatusOK, userPermissionsJSON(perms))
}

func (api *tenantAdminAPI) getChangeableRoles(ctx context.Context, c *app.RequestContext) {
	if api.tenantAdmin == nil {
		writeTenantAdminError(ctx, c, http.StatusServiceUnavailable, "TENANT_ADMIN_UNAVAILABLE", "tenant admin service unavailable")
		return
	}
	roles, err := api.tenantAdmin.GetChangeableRoles(ctx, c.Param("tenantId"), c.Param("userId"))
	if err != nil {
		mapTenantAdminCoreError(ctx, c, err)
		return
	}
	options := make([]map[string]any, 0, len(roles.Options))
	for _, opt := range roles.Options {
		options = append(options, map[string]any{
			"role":  opt.Role,
			"label": opt.Label,
		})
	}
	c.JSON(http.StatusOK, map[string]any{
		"current_role":      roles.CurrentRole,
		"changeable_roles": options,
	})
}

func (api *tenantAdminAPI) resetTenantAdminPassword(ctx context.Context, c *app.RequestContext) {
	if api.tenantAdmin == nil {
		writeTenantAdminError(ctx, c, http.StatusServiceUnavailable, "TENANT_ADMIN_UNAVAILABLE", "tenant admin service unavailable")
		return
	}
	var body struct {
		NewPassword    string `json:"new_password"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeTenantAdminError(ctx, c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	if err := api.tenantAdmin.ResetPassword(ctx, c.Param("tenantId"), c.Param("userId"), body.NewPassword); err != nil {
		mapTenantAdminCoreError(ctx, c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"id":      c.Param("userId"),
		"message": "password reset",
	})
}

func (api *tenantAdminAPI) disableTenantAdmin(ctx context.Context, c *app.RequestContext) {
	api.setTenantAdminStatus(ctx, c, "disabled", "disabled")
}

func (api *tenantAdminAPI) enableTenantAdmin(ctx context.Context, c *app.RequestContext) {
	api.setTenantAdminStatus(ctx, c, "active", "enabled")
}

func (api *tenantAdminAPI) setTenantAdminStatus(ctx context.Context, c *app.RequestContext, status, message string) {
	if api.tenantAdmin == nil {
		writeTenantAdminError(ctx, c, http.StatusServiceUnavailable, "TENANT_ADMIN_UNAVAILABLE", "tenant admin service unavailable")
		return
	}
	if err := api.tenantAdmin.SetStatus(ctx, c.Param("tenantId"), c.Param("userId"), status); err != nil {
		mapTenantAdminCoreError(ctx, c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"id":      c.Param("userId"),
		"message": message,
	})
}

func (api *tenantAdminAPI) deleteTenantAdmin(ctx context.Context, c *app.RequestContext) {
	if api.tenantAdmin == nil {
		writeTenantAdminError(ctx, c, http.StatusServiceUnavailable, "TENANT_ADMIN_UNAVAILABLE", "tenant admin service unavailable")
		return
	}
	if err := api.tenantAdmin.SoftDelete(ctx, c.Param("tenantId"), c.Param("userId")); err != nil {
		mapTenantAdminCoreError(ctx, c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"id":      c.Param("userId"),
		"message": "deleted",
	})
}

// ── JSON mappers ─────────────────────────────────────────────────────────────

func adminWithTenantJSON(item *tenantv1.AdminWithTenant, includeTimestamps bool) map[string]any {
	if item == nil {
		return map[string]any{}
	}
	out := map[string]any{
		"id":           item.GetId(),
		"email":        item.GetEmail(),
		"username":     item.GetUsername(),
		"role":         item.GetRole(),
		"status":       item.GetStatus(),
		"is_inviting":  item.GetIsInviting(),
		"is_expired":   item.GetIsExpired(),
		"source":       item.GetSource(),
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

func userPermissionsJSON(perms ports.UserPermissions) map[string]any {
	items := make([]map[string]any, 0, len(perms.Permissions))
	for _, p := range perms.Permissions {
		items = append(items, map[string]any{
			"resource": p.Resource,
			"action":   p.Action,
			"scope":    p.Scope,
		})
	}
	out := map[string]any{
		"user_id":     perms.UserID,
		"role":        perms.Role,
		"permissions": items,
	}
	if perms.TenantID != "" {
		out["tenant_id"] = perms.TenantID
	} else {
		out["tenant_id"] = nil
	}
	return out
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

func mapTenantAdminCoreError(ctx context.Context, c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrUserNotFound):
		writeTenantAdminError(ctx, c, http.StatusNotFound, "TENANT_ADMIN_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrRoleChangeInvalid):
		writeTenantAdminError(ctx, c, http.StatusUnprocessableEntity, "ROLE_CHANGE_INVALID", err.Error())
	case errors.Is(err, ports.ErrPasswordSameAsOld):
		writeTenantAdminError(ctx, c, http.StatusUnprocessableEntity, "PASSWORD_SAME_AS_OLD", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeTenantAdminError(ctx, c, http.StatusBadRequest, "VALIDATION_FAILED", err.Error())
	case errors.Is(err, ports.ErrNotConfigured), errors.Is(err, ports.ErrUnsupported), errors.Is(err, ports.ErrUnavailable):
		writeTenantAdminError(ctx, c, http.StatusServiceUnavailable, "TENANT_ADMIN_UNAVAILABLE", err.Error())
	default:
		writeTenantAdminError(ctx, c, http.StatusInternalServerError, gatewayerrors.CodeInternalError, err.Error())
	}
}

func writeTenantAdminError(ctx context.Context, c *app.RequestContext, statusCode int, code, message string) {
	gatewayerrors.RespondError(ctx, c, statusCode, code, message)
}
