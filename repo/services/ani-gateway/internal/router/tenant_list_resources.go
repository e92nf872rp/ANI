package router

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// tenantListAPI 持有 tenant-service TenantService gRPC 客户端（租户列表 19 端点）。
type tenantListAPI struct {
	tenants tenantv1.TenantServiceClient
}

// registerTenantList 在 /api/v1/svc 下注册租户列表全部端点。
func registerTenantList(svc *route.RouterGroup) {
	registerTenantListWithClient(svc, dialTenantListGRPCClient())
}

func registerTenantListWithClient(svc *route.RouterGroup, client tenantv1.TenantServiceClient) {
	api := &tenantListAPI{tenants: client}

	svc.GET("/tenants/available-plans", api.listAvailablePlans)
	svc.GET("/tenants", api.listTenants)
	svc.POST("/tenants", api.createTenant)
	svc.GET("/tenants/:tenantId", api.getTenantDetail)
	svc.PUT("/tenants/:tenantId", api.updateTenant)
	svc.POST("/tenants/:tenantId/freeze", api.freezeTenant)
	svc.POST("/tenants/:tenantId/unfreeze", api.unfreezeTenant)
	svc.POST("/tenants/:tenantId/disable", api.disableTenant)
	svc.GET("/tenants/:tenantId/auth/sso", api.getTenantAuth)
	svc.PUT("/tenants/:tenantId/auth/sso", api.updateTenantSso)
	svc.POST("/tenants/:tenantId/auth/sso/test", api.testTenantSso)
	svc.PUT("/tenants/:tenantId/auth/mfa", api.updateTenantMfa)
	svc.GET("/tenants/:tenantId/quota", api.getTenantQuota)
	svc.GET("/tenants/:tenantId/quota-requests", api.listQuotaChangeRequests)
	svc.POST("/tenants/:tenantId/quota-requests", api.submitQuotaChangeRequest)
	svc.POST("/tenants/:tenantId/quota-requests/:reqId/approve", api.reviewQuotaChangeRequest)
	svc.GET("/tenants/:tenantId/lifecycle", api.listTenantLifecycle)
	svc.GET("/tenants/:tenantId/audit-logs", api.listTenantAuditLogs)
	svc.GET("/tenants/:tenantId/admins", api.listTenantAdmins)
}

func dialTenantListGRPCClient() tenantv1.TenantServiceClient {
	addr := strings.TrimSpace(os.Getenv("TENANT_SERVICE_ADDR"))
	if addr == "" {
		addr = tenantServiceDefaultAddr
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil
	}
	return tenantv1.NewTenantServiceClient(conn)
}

func (api *tenantListAPI) listAvailablePlans(ctx context.Context, c *app.RequestContext) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.tenants.ListAvailablePlans(callCtx, &tenantv1.ListAvailablePlansRequest{})
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, p := range res.GetItems() {
		items = append(items, map[string]any{
			"id":   p.GetId(),
			"code": p.GetCode(),
			"name": p.GetName(),
		})
	}
	c.JSON(http.StatusOK, map[string]any{"items": items})
}

func (api *tenantListAPI) listTenants(ctx context.Context, c *app.RequestContext) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.tenants.ListTenants(callCtx, &tenantv1.ListTenantsRequest{
		Status: c.Query("status"),
		Search: c.Query("search"),
		Page:   &commonv1.CursorPageRequest{Limit: cursorLimit(c), Cursor: c.Query("cursor")},
	})
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, t := range res.GetItems() {
		items = append(items, tenantListItemJSON(t))
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nullIfEmpty(res.GetNextCursor()),
	})
}

func (api *tenantListAPI) createTenant(ctx context.Context, c *app.RequestContext) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		Name           string `json:"name"`
		DisplayName    string `json:"display_name"`
		Email          string `json:"email"`
		PlanID         string `json:"plan_id"`
		AdminEmail     string `json:"admin_email"`
		AdminName      string `json:"admin_name"`
		AdminPassword  string `json:"admin_password"`
	}
	if err := json.Unmarshal(c.Request.Body(), &body); err != nil {
		writeTenantListError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	idem := strings.TrimSpace(body.IdempotencyKey)
	if idem == "" {
		idem = idempotencyHeader(c)
	}
	// 创建含 bcrypt(12)+Core 事务+配额初始化；默认 5s gRPC 超时易导致「Core 已成功、网关已取消」竞态。
	callCtx, cancel := tenantWriteCallCtx(ctx, c, 30*time.Second)
	defer cancel()
	res, err := api.tenants.CreateTenant(callCtx, &tenantv1.CreateTenantRequest{
		Name:           body.Name,
		DisplayName:    body.DisplayName,
		Email:          body.Email,
		PlanId:         body.PlanID,
		AdminEmail:     body.AdminEmail,
		AdminName:      body.AdminName,
		AdminPassword:  body.AdminPassword,
		IdempotencyKey: idem,
	})
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

func (api *tenantListAPI) getTenantDetail(ctx context.Context, c *app.RequestContext) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.tenants.GetTenantDetail(callCtx, &tenantv1.GetTenantDetailRequest{TenantId: c.Param("tenantId")})
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	c.JSON(http.StatusOK, tenantDetailJSON(res))
}

func (api *tenantListAPI) updateTenant(ctx context.Context, c *app.RequestContext) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	var body struct {
		IdempotencyKey string  `json:"idempotency_key"`
		DisplayName    *string `json:"display_name"`
		ContactEmail   *string `json:"contact_email"`
	}
	if err := json.Unmarshal(c.Request.Body(), &body); err != nil {
		writeTenantListError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	idem := strings.TrimSpace(body.IdempotencyKey)
	if idem == "" {
		idem = idempotencyHeader(c)
	}
	req := &tenantv1.UpdateTenantRequest{
		TenantId:       c.Param("tenantId"),
		IdempotencyKey: idem,
	}
	if body.DisplayName != nil {
		req.DisplayName = wrapperspb.String(*body.DisplayName)
	}
	if body.ContactEmail != nil {
		req.ContactEmail = wrapperspb.String(*body.ContactEmail)
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.tenants.UpdateTenant(callCtx, req)
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

func (api *tenantListAPI) freezeTenant(ctx context.Context, c *app.RequestContext) {
	api.stateTransition(ctx, c, func(callCtx context.Context, tenantID, idem string) (*commonv1.IdempotentResult, error) {
		return api.tenants.FreezeTenant(callCtx, &tenantv1.FreezeTenantRequest{TenantId: tenantID, IdempotencyKey: idem})
	})
}

func (api *tenantListAPI) unfreezeTenant(ctx context.Context, c *app.RequestContext) {
	api.stateTransition(ctx, c, func(callCtx context.Context, tenantID, idem string) (*commonv1.IdempotentResult, error) {
		return api.tenants.UnfreezeTenant(callCtx, &tenantv1.UnfreezeTenantRequest{TenantId: tenantID, IdempotencyKey: idem})
	})
}

func (api *tenantListAPI) disableTenant(ctx context.Context, c *app.RequestContext) {
	api.stateTransition(ctx, c, func(callCtx context.Context, tenantID, idem string) (*commonv1.IdempotentResult, error) {
		return api.tenants.DisableTenant(callCtx, &tenantv1.DisableTenantRequest{TenantId: tenantID, IdempotencyKey: idem})
	})
}

func (api *tenantListAPI) stateTransition(
	ctx context.Context,
	c *app.RequestContext,
	fn func(context.Context, string, string) (*commonv1.IdempotentResult, error),
) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	idem := idempotencyHeader(c)
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	_ = json.Unmarshal(c.Request.Body(), &body)
	if strings.TrimSpace(body.IdempotencyKey) != "" {
		idem = strings.TrimSpace(body.IdempotencyKey)
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := fn(callCtx, c.Param("tenantId"), idem)
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

func (api *tenantListAPI) getTenantAuth(ctx context.Context, c *app.RequestContext) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.tenants.GetTenantAuth(callCtx, &tenantv1.GetTenantAuthRequest{TenantId: c.Param("tenantId")})
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	c.JSON(http.StatusOK, tenantAuthJSON(res))
}

func (api *tenantListAPI) updateTenantSso(ctx context.Context, c *app.RequestContext) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	var body struct {
		IdempotencyKey string  `json:"idempotency_key"`
		SsoEnabled     *bool   `json:"sso_enabled"`
		Provider       *string `json:"provider"`
	}
	if err := json.Unmarshal(c.Request.Body(), &body); err != nil {
		writeTenantListError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	idem := strings.TrimSpace(body.IdempotencyKey)
	if idem == "" {
		idem = idempotencyHeader(c)
	}
	req := &tenantv1.UpdateTenantSsoRequest{TenantId: c.Param("tenantId"), IdempotencyKey: idem}
	if body.SsoEnabled != nil {
		req.SsoEnabled = wrapperspb.Bool(*body.SsoEnabled)
	}
	if body.Provider != nil {
		req.Provider = wrapperspb.String(*body.Provider)
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.tenants.UpdateTenantSso(callCtx, req)
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

func (api *tenantListAPI) testTenantSso(ctx context.Context, c *app.RequestContext) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.tenants.TestTenantSso(callCtx, &tenantv1.TestTenantSsoRequest{TenantId: c.Param("tenantId")})
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	out := map[string]any{
		"success":   res.GetSuccess(),
		"tested_at": pbTimestampRFC3339(res.GetTestedAt()),
	}
	if res.GetDiscoveryResult() != nil {
		out["discovery_result"] = res.GetDiscoveryResult().AsMap()
	} else {
		out["discovery_result"] = nil
	}
	if res.GetError() != nil {
		out["error"] = res.GetError().GetValue()
	} else {
		out["error"] = nil
	}
	c.JSON(http.StatusOK, out)
}

func (api *tenantListAPI) updateTenantMfa(ctx context.Context, c *app.RequestContext) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		MfaRequired    bool   `json:"mfa_required"`
	}
	if err := json.Unmarshal(c.Request.Body(), &body); err != nil {
		writeTenantListError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	idem := strings.TrimSpace(body.IdempotencyKey)
	if idem == "" {
		idem = idempotencyHeader(c)
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.tenants.UpdateTenantMfa(callCtx, &tenantv1.UpdateTenantMfaRequest{
		TenantId:       c.Param("tenantId"),
		MfaRequired:    body.MfaRequired,
		IdempotencyKey: idem,
	})
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

func (api *tenantListAPI) getTenantQuota(ctx context.Context, c *app.RequestContext) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.tenants.GetTenantQuota(callCtx, &tenantv1.GetTenantQuotaRequest{TenantId: c.Param("tenantId")})
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, it := range res.GetItems() {
		items = append(items, map[string]any{
			"resource_type": it.GetResourceType(),
			"display_name":  it.GetDisplayName(),
			"used":          it.GetUsed(),
			"total":         it.GetTotal(),
			"unit":          it.GetUnit(),
		})
	}
	c.JSON(http.StatusOK, map[string]any{"items": items})
}

func (api *tenantListAPI) listQuotaChangeRequests(ctx context.Context, c *app.RequestContext) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.tenants.ListQuotaChangeRequests(callCtx, &tenantv1.ListQuotaChangeRequestsRequest{
		TenantId: c.Param("tenantId"),
		Status:   c.Query("status"),
	})
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, it := range res.GetItems() {
		items = append(items, map[string]any{
			"id":            it.GetId(),
			"tenant_id":     it.GetTenantId(),
			"resource_type": it.GetResourceType(),
			"old_value":     it.GetOldValue(),
			"new_value":     it.GetNewValue(),
			"status":        it.GetStatus(),
			"requested_by":  it.GetRequestedBy(),
			"created_at":    pbTimestampRFC3339(it.GetCreatedAt()),
		})
	}
	c.JSON(http.StatusOK, map[string]any{"items": items})
}

func (api *tenantListAPI) submitQuotaChangeRequest(ctx context.Context, c *app.RequestContext) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		Items          []struct {
			ResourceType string `json:"resource_type"`
			NewValue     int64  `json:"new_value"`
		} `json:"items"`
	}
	if err := json.Unmarshal(c.Request.Body(), &body); err != nil {
		writeTenantListError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	idem := strings.TrimSpace(body.IdempotencyKey)
	if idem == "" {
		idem = idempotencyHeader(c)
	}
	items := make([]*tenantv1.QuotaChangeRequestInput, 0, len(body.Items))
	for _, it := range body.Items {
		items = append(items, &tenantv1.QuotaChangeRequestInput{
			ResourceType: it.ResourceType,
			NewValue:     it.NewValue,
		})
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.tenants.SubmitQuotaChangeRequest(callCtx, &tenantv1.SubmitQuotaChangeRequestRequest{
		TenantId:       c.Param("tenantId"),
		Items:          items,
		IdempotencyKey: idem,
	})
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

func (api *tenantListAPI) reviewQuotaChangeRequest(ctx context.Context, c *app.RequestContext) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		Approved       bool   `json:"approved"`
	}
	if err := json.Unmarshal(c.Request.Body(), &body); err != nil {
		writeTenantListError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	idem := strings.TrimSpace(body.IdempotencyKey)
	if idem == "" {
		idem = idempotencyHeader(c)
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.tenants.ReviewQuotaChangeRequest(callCtx, &tenantv1.ReviewQuotaChangeRequestRequest{
		TenantId:       c.Param("tenantId"),
		RequestId:      c.Param("reqId"),
		Approved:       body.Approved,
		IdempotencyKey: idem,
	})
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

func (api *tenantListAPI) listTenantLifecycle(ctx context.Context, c *app.RequestContext) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.tenants.ListTenantLifecycle(callCtx, &tenantv1.ListTenantLifecycleRequest{
		TenantId: c.Param("tenantId"),
		Action:   c.Query("action"),
		Page:     &commonv1.CursorPageRequest{Limit: cursorLimit(c), Cursor: c.Query("cursor")},
	})
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, it := range res.GetItems() {
		row := map[string]any{
			"id":         it.GetId(),
			"action":     it.GetAction(),
			"created_at": pbTimestampRFC3339(it.GetCreatedAt()),
		}
		if it.GetReason() != nil {
			row["reason"] = it.GetReason().GetValue()
		} else {
			row["reason"] = nil
		}
		if it.GetUserId() != nil {
			row["user_id"] = it.GetUserId().GetValue()
		} else {
			row["user_id"] = nil
		}
		if it.GetRequestId() != nil {
			row["request_id"] = it.GetRequestId().GetValue()
		} else {
			row["request_id"] = nil
		}
		items = append(items, row)
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nullIfEmpty(res.GetNextCursor()),
	})
}

func (api *tenantListAPI) listTenantAuditLogs(ctx context.Context, c *app.RequestContext) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.tenants.ListTenantAuditLogs(callCtx, &tenantv1.ListTenantAuditLogsRequest{
		TenantId: c.Param("tenantId"),
		Action:   c.Query("action"),
		Result:   c.Query("result"),
		Page:     &commonv1.CursorPageRequest{Limit: cursorLimit(c), Cursor: c.Query("cursor")},
	})
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, it := range res.GetItems() {
		row := map[string]any{
			"id":         it.GetId(),
			"action":     it.GetAction(),
			"resource":   it.GetResource(),
			"result":     it.GetResult(),
			"created_at": pbTimestampRFC3339(it.GetCreatedAt()),
		}
		if it.GetUserId() != nil {
			row["user_id"] = it.GetUserId().GetValue()
		} else {
			row["user_id"] = nil
		}
		if it.GetDetails() != nil {
			row["details"] = it.GetDetails().AsMap()
		} else {
			row["details"] = nil
		}
		items = append(items, row)
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nullIfEmpty(res.GetNextCursor()),
	})
}

func (api *tenantListAPI) listTenantAdmins(ctx context.Context, c *app.RequestContext) {
	if api.tenants == nil {
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.tenants.ListTenantAdmins(callCtx, &tenantv1.ListTenantAdminsRequest{
		TenantId: c.Param("tenantId"),
		Role:     c.Query("role"),
		Status:   c.Query("status"),
		Search:   c.Query("search"),
		Page:     &commonv1.CursorPageRequest{Limit: cursorLimit(c), Cursor: c.Query("cursor")},
	})
	if err != nil {
		mapTenantListError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, it := range res.GetItems() {
		tenantRef := map[string]any{}
		if t := it.GetTenant(); t != nil {
			tenantRef = map[string]any{
				"id":           t.GetId(),
				"name":         t.GetName(),
				"display_name": t.GetDisplayName(),
			}
		}
		var displayName any
		if dn := it.GetDisplayName(); dn != nil {
			displayName = dn.GetValue()
		}
		var lastLogin any
		if s := pbTimestampRFC3339(it.GetLastLoginAt()); s != "" {
			lastLogin = s
		}
		items = append(items, map[string]any{
			"id":            it.GetId(),
			"email":         it.GetEmail(),
			"username":      it.GetUsername(),
			"display_name":  displayName,
			"role":          it.GetRole(),
			"status":        it.GetStatus(),
			"source":        it.GetSource(),
			"last_login_at": lastLogin,
			"tenant":        tenantRef,
		})
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nullIfEmpty(res.GetNextCursor()),
	})
}

func tenantListItemJSON(t *tenantv1.TenantListItem) map[string]any {
	if t == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":           t.GetId(),
		"name":         t.GetName(),
		"display_name": t.GetDisplayName(),
		"plan_id":      t.GetPlanId(),
		"plan_code":    t.GetPlanCode(),
		"status":       t.GetStatus(),
		"admin_count":  t.GetAdminCount(),
		"created_at":   pbTimestampRFC3339(t.GetCreatedAt()),
	}
}

func tenantDetailJSON(t *tenantv1.TenantDetail) map[string]any {
	if t == nil {
		return map[string]any{}
	}
	out := map[string]any{
		"id":           t.GetId(),
		"name":         t.GetName(),
		"display_name": t.GetDisplayName(),
		"plan_id":      t.GetPlanId(),
		"plan_code":    t.GetPlanCode(),
		"status":       t.GetStatus(),
		"user_count":   t.GetUserCount(),
		"admin_count":  t.GetAdminCount(),
		"created_at":   pbTimestampRFC3339(t.GetCreatedAt()),
		"updated_at":   pbTimestampRFC3339(t.GetUpdatedAt()),
	}
	if t.GetContactEmail() != nil {
		out["contact_email"] = t.GetContactEmail().GetValue()
	} else {
		out["contact_email"] = nil
	}
	if t.GetFrozenAt() != nil {
		out["frozen_at"] = pbTimestampRFC3339(t.GetFrozenAt())
	} else {
		out["frozen_at"] = nil
	}
	if t.GetDisabledAt() != nil {
		out["disabled_at"] = pbTimestampRFC3339(t.GetDisabledAt())
	} else {
		out["disabled_at"] = nil
	}
	return out
}

func tenantAuthJSON(a *tenantv1.TenantAuthConfig) map[string]any {
	if a == nil {
		return map[string]any{}
	}
	out := map[string]any{
		"sso_enabled":  a.GetSsoEnabled(),
		"mfa_required": a.GetMfaRequired(),
		"updated_at":   pbTimestampRFC3339(a.GetUpdatedAt()),
	}
	if a.GetProvider() != nil {
		out["provider"] = a.GetProvider().GetValue()
	} else {
		out["provider"] = nil
	}
	return out
}

func pbTimestampRFC3339(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	t := ts.AsTime()
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// tenantListBusinessCodeByHTTP 对齐 SPEC §6.1 Error Taxonomy。
var tenantListBusinessCodeByHTTP = map[string]int{
	"VALIDATION_FAILED":               http.StatusBadRequest,
	"TENANT_NOT_FOUND":                http.StatusNotFound,
	"TENANT_NAME_CONFLICT":            http.StatusConflict,
	"TENANT_STATE_INVALID":            http.StatusConflict,
	"TENANT_HAS_RUNNING_RESOURCES":    http.StatusConflict,
	"PLAN_NOT_ACTIVE":                 http.StatusUnprocessableEntity,
	"TENANT_SSO_CONFIG_INVALID":       http.StatusUnprocessableEntity,
	"QUOTA_CHANGE_REQUEST_INVALID":    http.StatusUnprocessableEntity,
	"QUOTA_CHANGE_REQUEST_NOT_PENDING": http.StatusConflict,
	"QUOTA_CHANGE_REQUEST_NOT_FOUND":  http.StatusNotFound,
	"NOT_IMPLEMENTED":                 http.StatusNotImplemented,
	"GRPC_CLIENT_UNAVAILABLE":         http.StatusBadGateway,
	"STORE_UNAVAILABLE":               http.StatusBadGateway,
}

var tenantListSortedBusinessCodes = func() []string {
	codes := make([]string, 0, len(tenantListBusinessCodeByHTTP))
	for code := range tenantListBusinessCodeByHTTP {
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

// mapTenantListError 两阶段 gRPC→HTTP 映射（SPEC §6.1）。
func mapTenantListError(c *app.RequestContext, err error) {
	msg := status.Convert(err).Message()
	for _, code := range tenantListSortedBusinessCodes {
		if strings.HasPrefix(msg, code+":") || msg == code {
			writeTenantListError(c, tenantListBusinessCodeByHTTP[code], code, strings.TrimSpace(strings.TrimPrefix(msg, code+":")))
			return
		}
	}
	switch status.Code(err) {
	case codes.NotFound:
		writeTenantListError(c, http.StatusNotFound, "TENANT_NOT_FOUND", msg)
	case codes.InvalidArgument:
		writeTenantListError(c, http.StatusBadRequest, "VALIDATION_FAILED", msg)
	case codes.AlreadyExists, codes.FailedPrecondition, codes.Aborted:
		writeTenantListError(c, http.StatusConflict, "CONFLICT", msg)
	case codes.Unimplemented:
		writeTenantListError(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", msg)
	case codes.DeadlineExceeded:
		writeTenantListError(c, http.StatusGatewayTimeout, "GATEWAY_TIMEOUT", msg)
	case codes.Unavailable:
		writeTenantListError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", msg)
	default:
		writeTenantListError(c, http.StatusInternalServerError, "INTERNAL", msg)
	}
}

func writeTenantListError(c *app.RequestContext, statusCode int, code, message string) {
	c.JSON(statusCode, map[string]any{
		"code":       code,
		"message":    message,
		"request_id": middleware.GetRequestID(c),
	})
	c.Abort()
}
