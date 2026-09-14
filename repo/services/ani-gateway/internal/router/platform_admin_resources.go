package router

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	platformsettingsv1 "github.com/kubercloud/ani/pkg/generated/pb/platform_settings/v1"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// 本文件实现平台运营账号网关接入：/api/v1/svc/platform-admins*
// 把 REST 请求转发到 platform-settings-service 的 gRPC，并把 gRPC 错误映射为 HTTP 状态与业务码。
const (
	platformSettingsServiceDefaultAddr = "127.0.0.1:9106"
	// platformAdminCallTimeout 需覆盖 Services→Core SDK（10s）及 gRPC 一跳余量。
	platformAdminCallTimeout = 12 * time.Second
)

// platformAdminsAPI 持有 platform-settings-service gRPC 客户端。
// conn 建立失败时 client 为 nil，由各 handler 做 nil 守卫兜底返回 502。
type platformAdminsAPI struct {
	client platformsettingsv1.PlatformAdminServiceClient
}

// newPlatformAdminsAPI 由 PLATFORM_SETTINGS_SERVICE_ADDR（缺省 127.0.0.1:9106）创建 gRPC 客户端。
func newPlatformAdminsAPI() *platformAdminsAPI {
	addr := strings.TrimSpace(os.Getenv("PLATFORM_SETTINGS_SERVICE_ADDR"))
	if addr == "" {
		addr = platformSettingsServiceDefaultAddr
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return &platformAdminsAPI{}
	}
	return &platformAdminsAPI{
		client: platformsettingsv1.NewPlatformAdminServiceClient(conn),
	}
}

// registerPlatformAdmins 在 /api/v1/svc 下注册平台运营账号全部端点。
func registerPlatformAdmins(svc *route.RouterGroup) {
	registerPlatformAdminsAPI(svc, newPlatformAdminsAPI())
}

// registerPlatformAdminsAPI 注册路由（测试可注入 fake client）。
func registerPlatformAdminsAPI(svc *route.RouterGroup, api *platformAdminsAPI) {
	svc.POST("/platform-admins", api.createPlatformAdmin)
	svc.GET("/platform-admins", api.listPlatformAdmins)
	// 静态段先于参数段：/roles 必须在 /:userId 之前注册。
	svc.GET("/platform-admins/roles", api.listPlatformAdminRoles)
	svc.GET("/platform-admins/:userId", api.getPlatformAdmin)
	svc.GET("/platform-admins/:userId/permissions", api.getPlatformAdminPermissions)
	svc.PUT("/platform-admins/:userId/role", api.updatePlatformAdminRole)
	svc.POST("/platform-admins/:userId/reset-password", api.resetPlatformAdminPassword)
	svc.POST("/platform-admins/:userId/disable", api.disablePlatformAdmin)
	svc.POST("/platform-admins/:userId/enable", api.enablePlatformAdmin)
	svc.DELETE("/platform-admins/:userId", api.deletePlatformAdmin)
	svc.GET("/platform-admins/:userId/audit-logs", api.listPlatformAdminAuditLogs)
}

// platformAdminCallCtx 构造 gRPC 调用 context：12s 超时 + request_id / user_id metadata。
func platformAdminCallCtx(ctx context.Context, c *app.RequestContext) (context.Context, context.CancelFunc) {
	callCtx, cancel := context.WithTimeout(ctx, platformAdminCallTimeout)
	callCtx = metadata.AppendToOutgoingContext(callCtx, "x-request-id", middleware.GetRequestID(c))
	if userID := strings.TrimSpace(middleware.GetUserID(c)); userID != "" {
		callCtx = metadata.AppendToOutgoingContext(callCtx, "x-user-id", userID)
	}
	return callCtx, cancel
}

func (api *platformAdminsAPI) createPlatformAdmin(ctx context.Context, c *app.RequestContext) {
	if api.client == nil {
		writePlatformAdminError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "platform-admins grpc client unavailable")
		return
	}
	var body struct {
		Email          string `json:"email"`
		Username       string `json:"username"`
		DisplayName    string `json:"display_name"`
		RoleID         string `json:"role_id"`
		Password       string `json:"password"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writePlatformAdminError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	callCtx, cancel := platformAdminCallCtx(ctx, c)
	defer cancel()
	res, err := api.client.CreatePlatformAdmin(callCtx, &platformsettingsv1.CreatePlatformAdminRequest{
		IdempotencyKey: body.IdempotencyKey,
		Email:          body.Email,
		Username:       body.Username,
		DisplayName:    body.DisplayName,
		RoleId:         body.RoleID,
		Password:       body.Password,
	})
	if err != nil {
		mapPlatformAdminError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

func (api *platformAdminsAPI) listPlatformAdmins(ctx context.Context, c *app.RequestContext) {
	if api.client == nil {
		writePlatformAdminError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "platform-admins grpc client unavailable")
		return
	}
	callCtx, cancel := platformAdminCallCtx(ctx, c)
	defer cancel()
	limit, err := parseCursorLimitQuery(c)
	if err != nil {
		writePlatformAdminError(c, http.StatusBadRequest, "VALIDATION_FAILED", err.Error())
		return
	}
	res, err := api.client.ListPlatformAdmins(callCtx, &platformsettingsv1.ListPlatformAdminsRequest{
		RoleId: c.Query("role_id"),
		Status: c.Query("status"),
		Source: c.Query("source"),
		Search: c.Query("search"),
		Page:   &commonv1.CursorPageRequest{Limit: limit, Cursor: c.Query("cursor")},
	})
	if err != nil {
		mapPlatformAdminError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, item := range res.GetItems() {
		items = append(items, platformAdminListItemJSON(item))
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nullIfEmpty(res.GetNextCursor()),
	})
}

func (api *platformAdminsAPI) listPlatformAdminRoles(ctx context.Context, c *app.RequestContext) {
	if api.client == nil {
		writePlatformAdminError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "platform-admins grpc client unavailable")
		return
	}
	callCtx, cancel := platformAdminCallCtx(ctx, c)
	defer cancel()
	res, err := api.client.ListPlatformAdminRoles(callCtx, &platformsettingsv1.ListPlatformAdminRolesRequest{})
	if err != nil {
		mapPlatformAdminError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, item := range res.GetItems() {
		items = append(items, platformRoleJSON(item))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items})
}

func (api *platformAdminsAPI) getPlatformAdmin(ctx context.Context, c *app.RequestContext) {
	if api.client == nil {
		writePlatformAdminError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "platform-admins grpc client unavailable")
		return
	}
	callCtx, cancel := platformAdminCallCtx(ctx, c)
	defer cancel()
	res, err := api.client.GetPlatformAdmin(callCtx, &platformsettingsv1.GetPlatformAdminRequest{
		UserId: c.Param("userId"),
	})
	if err != nil {
		mapPlatformAdminError(c, err)
		return
	}
	c.JSON(http.StatusOK, platformAdminDetailJSON(res))
}

func (api *platformAdminsAPI) getPlatformAdminPermissions(ctx context.Context, c *app.RequestContext) {
	if api.client == nil {
		writePlatformAdminError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "platform-admins grpc client unavailable")
		return
	}
	callCtx, cancel := platformAdminCallCtx(ctx, c)
	defer cancel()
	res, err := api.client.GetPlatformAdminPermissions(callCtx, &platformsettingsv1.GetPlatformAdminPermissionsRequest{
		UserId: c.Param("userId"),
	})
	if err != nil {
		mapPlatformAdminError(c, err)
		return
	}
	c.JSON(http.StatusOK, platformAdminPermissionsJSON(res))
}

func (api *platformAdminsAPI) updatePlatformAdminRole(ctx context.Context, c *app.RequestContext) {
	if api.client == nil {
		writePlatformAdminError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "platform-admins grpc client unavailable")
		return
	}
	var body struct {
		RoleID         string `json:"role_id"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writePlatformAdminError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	callCtx, cancel := platformAdminCallCtx(ctx, c)
	defer cancel()
	res, err := api.client.UpdatePlatformAdminRole(callCtx, &platformsettingsv1.UpdatePlatformAdminRoleRequest{
		UserId:         c.Param("userId"),
		RoleId:         body.RoleID,
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		mapPlatformAdminError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

func (api *platformAdminsAPI) resetPlatformAdminPassword(ctx context.Context, c *app.RequestContext) {
	if api.client == nil {
		writePlatformAdminError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "platform-admins grpc client unavailable")
		return
	}
	var body struct {
		NewPassword    string `json:"new_password"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writePlatformAdminError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	callCtx, cancel := platformAdminCallCtx(ctx, c)
	defer cancel()
	res, err := api.client.ResetPlatformAdminPassword(callCtx, &platformsettingsv1.ResetPlatformAdminPasswordRequest{
		UserId:         c.Param("userId"),
		NewPassword:    body.NewPassword,
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		mapPlatformAdminError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

func (api *platformAdminsAPI) disablePlatformAdmin(ctx context.Context, c *app.RequestContext) {
	api.writeIdempotentUserAction(ctx, c, func(callCtx context.Context, userID, key string) (*commonv1.IdempotentResult, error) {
		return api.client.DisablePlatformAdmin(callCtx, &platformsettingsv1.DisablePlatformAdminRequest{
			UserId: userID, IdempotencyKey: key,
		})
	})
}

func (api *platformAdminsAPI) enablePlatformAdmin(ctx context.Context, c *app.RequestContext) {
	api.writeIdempotentUserAction(ctx, c, func(callCtx context.Context, userID, key string) (*commonv1.IdempotentResult, error) {
		return api.client.EnablePlatformAdmin(callCtx, &platformsettingsv1.EnablePlatformAdminRequest{
			UserId: userID, IdempotencyKey: key,
		})
	})
}

func (api *platformAdminsAPI) deletePlatformAdmin(ctx context.Context, c *app.RequestContext) {
	api.writeIdempotentUserAction(ctx, c, func(callCtx context.Context, userID, key string) (*commonv1.IdempotentResult, error) {
		return api.client.DeletePlatformAdmin(callCtx, &platformsettingsv1.DeletePlatformAdminRequest{
			UserId: userID, IdempotencyKey: key,
		})
	})
}

func (api *platformAdminsAPI) writeIdempotentUserAction(
	ctx context.Context,
	c *app.RequestContext,
	call func(context.Context, string, string) (*commonv1.IdempotentResult, error),
) {
	if api.client == nil {
		writePlatformAdminError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "platform-admins grpc client unavailable")
		return
	}
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writePlatformAdminError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	callCtx, cancel := platformAdminCallCtx(ctx, c)
	defer cancel()
	res, err := call(callCtx, c.Param("userId"), body.IdempotencyKey)
	if err != nil {
		mapPlatformAdminError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

func (api *platformAdminsAPI) listPlatformAdminAuditLogs(ctx context.Context, c *app.RequestContext) {
	if api.client == nil {
		writePlatformAdminError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "platform-admins grpc client unavailable")
		return
	}
	callCtx, cancel := platformAdminCallCtx(ctx, c)
	defer cancel()
	limit, err := parseCursorLimitQuery(c)
	if err != nil {
		writePlatformAdminError(c, http.StatusBadRequest, "VALIDATION_FAILED", err.Error())
		return
	}
	res, err := api.client.ListPlatformAdminAuditLogs(callCtx, &platformsettingsv1.ListPlatformAdminAuditLogsRequest{
		UserId: c.Param("userId"),
		Action: c.Query("action"),
		Result: c.Query("result"),
		Page:   &commonv1.CursorPageRequest{Limit: limit, Cursor: c.Query("cursor")},
	})
	if err != nil {
		mapPlatformAdminError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, item := range res.GetItems() {
		items = append(items, platformAdminAuditLogJSON(item))
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nullIfEmpty(res.GetNextCursor()),
	})
}

func platformAdminListItemJSON(item *platformsettingsv1.PlatformAdminListItem) map[string]any {
	if item == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":            item.GetId(),
		"username":      item.GetUsername(),
		"display_name":  item.GetDisplayName(),
		"role_id":       item.GetRoleId(),
		"role":          item.GetRole(),
		"status":        item.GetStatus(),
		"source":        item.GetSource(),
		"last_login_at": platformAdminTimestampJSON(item.GetLastLoginAt()),
	}
}

func platformAdminDetailJSON(item *platformsettingsv1.PlatformAdminDetail) map[string]any {
	if item == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":            item.GetId(),
		"email":         item.GetEmail(),
		"username":      item.GetUsername(),
		"display_name":  item.GetDisplayName(),
		"role_id":       item.GetRoleId(),
		"role":          item.GetRole(),
		"status":        item.GetStatus(),
		"source":        item.GetSource(),
		"last_login_at": platformAdminTimestampJSON(item.GetLastLoginAt()),
		"created_at":    platformAdminTimestampJSON(item.GetCreatedAt()),
	}
}

func platformRoleJSON(item *platformsettingsv1.PlatformRole) map[string]any {
	if item == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":          item.GetId(),
		"name":        item.GetName(),
		"permissions": platformPermissionsJSON(item.GetPermissions()),
	}
}

func platformAdminPermissionsJSON(item *platformsettingsv1.PlatformAdminPermissions) map[string]any {
	if item == nil {
		return map[string]any{}
	}
	return map[string]any{
		"user_id":     item.GetUserId(),
		"role_id":     item.GetRoleId(),
		"role":        item.GetRole(),
		"permissions": platformPermissionsJSON(item.GetPermissions()),
	}
}

func platformPermissionsJSON(items []*structpb.Struct) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		out = append(out, it.AsMap())
	}
	return out
}

func platformAdminAuditLogJSON(item *platformsettingsv1.PlatformAdminAuditLog) map[string]any {
	if item == nil {
		return map[string]any{}
	}
	var details any
	if item.GetDetails() != nil {
		details = item.GetDetails().AsMap()
	}
	out := map[string]any{
		"id":         item.GetId(),
		"action":     item.GetAction(),
		"resource":   item.GetResource(),
		"result":     item.GetResult(),
		"details":    details,
		"created_at": platformAdminTimestampJSON(item.GetCreatedAt()),
	}
	if item.GetUserId() != nil {
		out["user_id"] = item.GetUserId().GetValue()
	} else {
		out["user_id"] = nil
	}
	return out
}

func platformAdminTimestampJSON(ts *timestamppb.Timestamp) any {
	if ts == nil {
		return nil
	}
	return ts.AsTime().UTC().Format(time.RFC3339Nano)
}

// platformAdminBusinessCodeByHTTP 对齐 SPEC §6.1。
var platformAdminBusinessCodeByHTTP = map[string]int{
	"VALIDATION_FAILED":       http.StatusBadRequest,
	"PLATFORM_USER_NOT_FOUND": http.StatusNotFound,
	"ROLE_NOT_FOUND":          http.StatusNotFound,
	"USERNAME_ALREADY_EXISTS": http.StatusConflict,
	"LAST_PLATFORM_ADMIN":     http.StatusUnprocessableEntity,
	"PASSWORD_SAME_AS_OLD":    http.StatusUnprocessableEntity,
	"STATUS_UNCHANGED":        http.StatusUnprocessableEntity,
	"ROLE_CHANGE_INVALID":     http.StatusUnprocessableEntity,
	"CORE_UNAVAILABLE":        http.StatusBadGateway,
	"NOT_IMPLEMENTED":         http.StatusNotImplemented,
}

var sortedPlatformAdminBusinessCodes = func() []string {
	codes := make([]string, 0, len(platformAdminBusinessCodeByHTTP))
	for code := range platformAdminBusinessCodeByHTTP {
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

// mapPlatformAdminError 先按业务码前缀还原 HTTP 状态，再按 gRPC code 兜底。
func mapPlatformAdminError(c *app.RequestContext, err error) {
	msg := status.Convert(err).Message()
	for _, code := range sortedPlatformAdminBusinessCodes {
		if strings.HasPrefix(msg, code+":") || msg == code {
			writePlatformAdminError(c, platformAdminBusinessCodeByHTTP[code], code, strings.TrimSpace(strings.TrimPrefix(msg, code+":")))
			return
		}
	}
	switch status.Code(err) {
	case codes.NotFound:
		writePlatformAdminError(c, http.StatusNotFound, "PLATFORM_USER_NOT_FOUND", msg)
	case codes.InvalidArgument:
		writePlatformAdminError(c, http.StatusBadRequest, "VALIDATION_FAILED", msg)
	case codes.AlreadyExists:
		// 平台账号创建冲突仅剩 username；email 允许重复。
		writePlatformAdminError(c, http.StatusConflict, "USERNAME_ALREADY_EXISTS", msg)
	case codes.FailedPrecondition:
		writePlatformAdminError(c, http.StatusUnprocessableEntity, "ROLE_CHANGE_INVALID", msg)
	case codes.Unimplemented:
		writePlatformAdminError(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", msg)
	case codes.DeadlineExceeded:
		writePlatformAdminError(c, http.StatusGatewayTimeout, "GATEWAY_TIMEOUT", msg)
	case codes.Unavailable:
		// Prefer CORE_UNAVAILABLE when message carries it; otherwise GRPC_CLIENT_UNAVAILABLE.
		if strings.Contains(msg, "CORE_UNAVAILABLE") {
			writePlatformAdminError(c, http.StatusBadGateway, "CORE_UNAVAILABLE", msg)
			return
		}
		writePlatformAdminError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", msg)
	default:
		writePlatformAdminError(c, http.StatusInternalServerError, "INTERNAL_ERROR", msg)
	}
}

func writePlatformAdminError(c *app.RequestContext, statusCode int, code, message string) {
	c.JSON(statusCode, map[string]any{
		"code":       code,
		"message":    message,
		"request_id": middleware.GetRequestID(c),
	})
	c.Abort()
}
