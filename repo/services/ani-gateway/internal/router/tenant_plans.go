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
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// 本文件实现配额套餐网关接入：/api/v1/svc/tenant-plans* 与 /api/v1/svc/tenants/{id}/plan
// 把 REST 请求转发到 tenant-service 的 gRPC，并把 gRPC 错误映射为 HTTP 状态与业务码。
// gRPC client 在 router 层持有（不放 middleware），每方法施加 5s 调用超时（对应 SPEC §2.4 / issue-004）。
// 通用常量与工具函数（tenantCallCtx / idempotencyHeader / cursorLimit / nullIfEmpty）见 tenant_common.go。

// tenantPlansAPI 持有 tenant-service 两个 gRPC 客户端，作为各路由 handler 的接收者。
// plans=TenantPlanServiceClient；tenants=TenantServiceClient(供 BindPlanQuota)。
// conn 建立失败时字段为 nil，由各 handler 做 nil 守卫兜底返回 502。
type tenantPlansAPI struct {
	plans   tenantv1.TenantPlanServiceClient
	tenants tenantv1.TenantServiceClient
}

// newTenantPlansAPI 由 TENANT_SERVICE_ADDR（缺省 127.0.0.1:9105）创建 gRPC 客户端并建立共享 conn。
func newTenantPlansAPI() *tenantPlansAPI {
	addr := strings.TrimSpace(os.Getenv("TENANT_SERVICE_ADDR"))
	if addr == "" {
		addr = tenantServiceDefaultAddr
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		// Connection failure is surfaced lazily per-call via nil client guards.
		return &tenantPlansAPI{}
	}
	return &tenantPlansAPI{
		plans:   tenantv1.NewTenantPlanServiceClient(conn),
		tenants: tenantv1.NewTenantServiceClient(conn),
	}
}

// registerTenantPlans 在 /api/v1/svc 下注册套餐全部端点，逐一映射到对应 gRPC RPC（见各 handler）。
func registerTenantPlans(svc *route.RouterGroup) {
	api := newTenantPlansAPI()

	// writes (idempotency handled by the Idempotency middleware)
	svc.POST("/tenant-plans", api.createTenantPlan)
	svc.GET("/tenant-plans", api.listTenantPlans)
	svc.GET("/quota-meta", api.listQuotaMeta)
	// 路径参数名与 Services OpenAPI 一致：{planId}/{tenantId}（非 snake_case）
	svc.GET("/tenant-plans/:planId", api.getTenantPlan)
	svc.PUT("/tenant-plans/:planId", api.updateTenantPlan)
	svc.PUT("/tenant-plans/:planId/quota-limits", api.updateTenantPlanQuotaLimits)
	svc.POST("/tenant-plans/:planId/activate", api.activateTenantPlan)
	svc.POST("/tenant-plans/:planId/disable", api.disableTenantPlan)
	svc.GET("/tenant-plans/:planId/quota-limits", api.getTenantPlanQuotaLimits)
	svc.GET("/tenant-plans/:planId/tenants", api.listTenantPlanBoundTenants)
	svc.GET("/tenant-plans/:planId/bindable-tenants", api.listBindableTenants)
	svc.GET("/tenant-plans/:planId/audit-logs", api.listTenantPlanAuditLogs)
	svc.DELETE("/tenant-plans/:planId", api.deleteTenantPlan)
	svc.POST("/tenants/:tenantId/plan", api.bindPlanQuota)
}

// listTenantPlans GET /tenant-plans：经 status/search 过滤的套餐列表，支持游标分页（limit 默认20 上限100）。
func (api *tenantPlansAPI) listTenantPlans(ctx context.Context, c *app.RequestContext) {
	// 判断 gRPC 客户端是否可用
	if api.plans == nil {
		writeTenantPlanError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "plan grpc client unavailable")
		return
	}
	// 设置调用超时
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 调用 gRPC 方法
	res, err := api.plans.ListTenantPlans(callCtx, &tenantv1.ListTenantPlansRequest{
		Status: c.Query("status"),
		Search: c.Query("search"),
		Page:   &commonv1.CursorPageRequest{Limit: cursorLimit(c), Cursor: c.Query("cursor")},
	})
	if err != nil {
		mapTenantPlanError(c, err)
		return
	}
	// 返回结果（映射为 OpenAPI JSON，避免 protobuf Timestamp / omitempty）
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, item := range res.GetItems() {
		items = append(items, tenantPlanJSON(item))
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"total":       res.GetTotal(),
		"next_cursor": nullIfEmpty(res.GetNextCursor()),
	})
}

// listQuotaMeta GET /quota-meta：透传 Core 启用配额维度元数据（创建/编辑套餐表单用）。
func (api *tenantPlansAPI) listQuotaMeta(ctx context.Context, c *app.RequestContext) {
	// 步骤 1：判断 gRPC 客户端是否可用
	if api.plans == nil {
		writeTenantPlanError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "plan grpc client unavailable")
		return
	}
	// 步骤 2：调用 tenant-service ListQuotaMeta
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.plans.ListQuotaMeta(callCtx, &tenantv1.ListQuotaMetaRequest{})
	if err != nil {
		mapTenantPlanError(c, err)
		return
	}
	// 步骤 3：映射为 OpenAPI JSON
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, it := range res.GetItems() {
		items = append(items, map[string]any{
			"resource_type": it.GetResourceType(),
			"display_name":  it.GetDisplayName(),
			"unit":          it.GetUnit(),
			"default_quota": it.GetDefaultQuota(),
			"is_discrete":   it.GetIsDiscrete(),
		})
	}
	c.JSON(http.StatusOK, map[string]any{"items": items})
}

// createTenantPlan POST /tenant-plans：创建套餐，入参缺省回退 Idempotency-Key 头部。
func (api *tenantPlansAPI) createTenantPlan(ctx context.Context, c *app.RequestContext) {
	// 判断 gRPC 客户端是否可用
	if api.plans == nil {
		writeTenantPlanError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "plan grpc client unavailable")
		return
	}
	// 解析请求体：quota_limits.total 按 OpenAPI 为可空 int64，不能直接 Bind 到 protobuf Int64Value
	var body struct {
		Code           string               `json:"code"`
		Name           string               `json:"name"`
		Description    string               `json:"description"`
		QuotaLimits    []planQuotaLimitJSON `json:"quota_limits"`
		IdempotencyKey string               `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeTenantPlanError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	// 设置调用超时
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 调用 gRPC 方法
	res, err := api.plans.CreateTenantPlan(callCtx, &tenantv1.CreateTenantPlanRequest{
		Code:           body.Code,
		Name:           body.Name,
		Description:    body.Description,
		QuotaLimits:    toPlanQuotaLimitInputs(body.QuotaLimits),
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		mapTenantPlanError(c, err)
		return
	}
	// 返回结果
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

// getTenantPlan GET /tenant-plans/:planId：按 id 返回单个套餐详情。
func (api *tenantPlansAPI) getTenantPlan(ctx context.Context, c *app.RequestContext) {
	// 判断 gRPC 客户端是否可用
	if api.plans == nil {
		writeTenantPlanError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "plan grpc client unavailable")
		return
	}
	// 设置调用超时
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 调用 gRPC 方法
	res, err := api.plans.GetTenantPlan(callCtx, &tenantv1.GetTenantPlanRequest{PlanId: c.Param("planId")})
	if err != nil {
		mapTenantPlanError(c, err)
		return
	}
	// 返回结果（映射为 OpenAPI JSON）
	c.JSON(http.StatusOK, tenantPlanJSON(res))
}

// updateTenantPlan PUT /tenant-plans/:planId：更新套餐 name / description（可选字段；null/未传=不更新，空串=清空）。
func (api *tenantPlansAPI) updateTenantPlan(ctx context.Context, c *app.RequestContext) {
	// 步骤 1：判断 gRPC 客户端是否可用
	if api.plans == nil {
		writeTenantPlanError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "plan grpc client unavailable")
		return
	}
	// 步骤 2：解析可选字段（*string 区分未传/null 与空串）
	var body struct {
		Name           *string `json:"name"`
		Description    *string `json:"description"`
		IdempotencyKey string  `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeTenantPlanError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	// 步骤 3：组装 gRPC 请求（StringValue 表达 optional）
	req := &tenantv1.UpdateTenantPlanRequest{
		PlanId:         c.Param("planId"),
		IdempotencyKey: body.IdempotencyKey,
	}
	if body.Name != nil {
		req.Name = wrapperspb.String(*body.Name)
	}
	if body.Description != nil {
		req.Description = wrapperspb.String(*body.Description)
	}
	// 步骤 4：调用 gRPC 并映射响应
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.plans.UpdateTenantPlan(callCtx, req)
	if err != nil {
		mapTenantPlanError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

// deleteTenantPlan DELETE /tenant-plans/:planId：软删除套餐；有租户关联时后端返回 409。
func (api *tenantPlansAPI) deleteTenantPlan(ctx context.Context, c *app.RequestContext) {
	// 判断 gRPC 客户端是否可用
	if api.plans == nil {
		writeTenantPlanError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "plan grpc client unavailable")
		return
	}
	// 设置调用超时
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 调用 gRPC 方法
	res, err := api.plans.DeleteTenantPlan(callCtx, &tenantv1.DeleteTenantPlanRequest{PlanId: c.Param("planId")})
	if err != nil {
		mapTenantPlanError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

// getTenantPlanQuotaLimits GET /tenant-plans/:planId/quota-limits：返回套餐各维度限额展示视图（COALESCE 后的具体值）。
func (api *tenantPlansAPI) getTenantPlanQuotaLimits(ctx context.Context, c *app.RequestContext) {
	// 判断 gRPC 客户端是否可用
	if api.plans == nil {
		writeTenantPlanError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "plan grpc client unavailable")
		return
	}
	// 设置调用超时
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 调用 gRPC 方法
	res, err := api.plans.GetTenantPlanQuotaLimits(callCtx, &tenantv1.GetTenantPlanQuotaLimitsRequest{PlanId: c.Param("planId")})
	if err != nil {
		mapTenantPlanError(c, err)
		return
	}
	// 返回结果（显式 map，避免 protobuf JSON 字段名差异）
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, it := range res.GetItems() {
		items = append(items, map[string]any{
			"resource_type": it.GetResourceType(),
			"display_name":  it.GetDisplayName(),
			"unit":          it.GetUnit(),
			"total":         it.GetTotal(),
		})
	}
	c.JSON(http.StatusOK, map[string]any{"items": items})
}

// updateTenantPlanQuotaLimits PUT /tenant-plans/:planId/quota-limits：整体更新套餐限额并同步存量租户配额。
func (api *tenantPlansAPI) updateTenantPlanQuotaLimits(ctx context.Context, c *app.RequestContext) {
	// 判断 gRPC 客户端是否可用
	if api.plans == nil {
		writeTenantPlanError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "plan grpc client unavailable")
		return
	}
	var body struct {
		Items          []planQuotaLimitJSON `json:"items"`
		IdempotencyKey string               `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeTenantPlanError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	// 设置调用超时
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 调用 gRPC 方法
	res, err := api.plans.UpdateTenantPlanQuotaLimits(callCtx, &tenantv1.UpdateTenantPlanQuotaLimitsRequest{
		PlanId:         c.Param("planId"),
		Items:          toPlanQuotaLimitInputs(body.Items),
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		mapTenantPlanError(c, err)
		return
	}
	// 返回结果
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

// activateTenantPlan POST /tenant-plans/:planId/activate：发布套餐（draft/disabled → active）。
func (api *tenantPlansAPI) activateTenantPlan(ctx context.Context, c *app.RequestContext) {
	// 调用通用状态转换，绑定 ActivateTenantPlan RPC
	api.planStateChange(ctx, c, func(callCtx context.Context, id, idemKey string) (*tenantv1.IdempotentResult, error) {
		return api.plans.ActivateTenantPlan(callCtx, &tenantv1.ActivateTenantPlanRequest{PlanId: id, IdempotencyKey: idemKey})
	})
}

// disableTenantPlan POST /tenant-plans/:planId/disable：禁用套餐（active → disabled）。
func (api *tenantPlansAPI) disableTenantPlan(ctx context.Context, c *app.RequestContext) {
	// 调用通用状态转换，绑定 DisableTenantPlan RPC
	api.planStateChange(ctx, c, func(callCtx context.Context, id, idemKey string) (*tenantv1.IdempotentResult, error) {
		return api.plans.DisableTenantPlan(callCtx, &tenantv1.DisableTenantPlanRequest{PlanId: id, IdempotencyKey: idemKey})
	})
}

// planStateChange 通用状态转换 handler：由 activate/disable 传入绑定方法，解析 body 幂等键并转发。
func (api *tenantPlansAPI) planStateChange(ctx context.Context, c *app.RequestContext, change func(context.Context, string, string) (*tenantv1.IdempotentResult, error)) {
	// 判断 gRPC 客户端是否可用
	if api.plans == nil {
		writeTenantPlanError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "plan grpc client unavailable")
		return
	}
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	_ = c.BindJSON(&body)
	planID := c.Param("planId")
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	// 设置调用超时
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 调用 gRPC 方法（activate/disable）
	res, err := change(callCtx, planID, body.IdempotencyKey)
	if err != nil {
		mapTenantPlanError(c, err)
		return
	}
	// 返回结果
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

// bindPlanQuota POST /tenants/:tenantId/plan：为租户绑定 active 套餐并向下游 Core 同步有效配额。
func (api *tenantPlansAPI) bindPlanQuota(ctx context.Context, c *app.RequestContext) {
	// 判断 gRPC 客户端是否可用
	if api.tenants == nil {
		writeTenantPlanError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "tenant grpc client unavailable")
		return
	}
	var body struct {
		PlanId         string `json:"plan_id"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeTenantPlanError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	// 设置调用超时
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 调用 gRPC 方法
	res, err := api.tenants.BindPlanQuota(callCtx, &tenantv1.BindPlanQuotaRequest{
		TenantId:       c.Param("tenantId"),
		PlanId:         body.PlanId,
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		mapTenantPlanError(c, err)
		return
	}
	// 返回结果
	c.JSON(http.StatusOK, map[string]any{"id": res.GetId(), "message": res.GetMessage()})
}

// listTenantPlanBoundTenants GET /tenant-plans/:planId/tenants：返回已绑定该套餐的租户摘要（不分页）。
func (api *tenantPlansAPI) listTenantPlanBoundTenants(ctx context.Context, c *app.RequestContext) {
	// 判断 gRPC 客户端是否可用
	if api.plans == nil {
		writeTenantPlanError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "plan grpc client unavailable")
		return
	}
	// 设置调用超时
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 调用 gRPC 方法
	res, err := api.plans.ListTenantPlanBoundTenants(callCtx, &tenantv1.ListTenantPlanBoundTenantsRequest{PlanId: c.Param("planId")})
	if err != nil {
		mapTenantPlanError(c, err)
		return
	}
	// 返回结果
	c.JSON(http.StatusOK, map[string]any{"items": boundTenantsJSON(res.GetItems())})
}

// listBindableTenants GET /tenant-plans/:planId/bindable-tenants：返回可绑定该套餐的租户摘要。
func (api *tenantPlansAPI) listBindableTenants(ctx context.Context, c *app.RequestContext) {
	if api.plans == nil {
		writeTenantPlanError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "plan grpc client unavailable")
		return
	}
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.plans.ListBindableTenants(callCtx, &tenantv1.ListBindableTenantsRequest{
		PlanId: c.Param("planId"),
	})
	if err != nil {
		mapTenantPlanError(c, err)
		return
	}
	c.JSON(http.StatusOK, map[string]any{"items": boundTenantsJSON(res.GetItems())})
}

// listTenantPlanAuditLogs GET /tenant-plans/:planId/audit-logs：返回套餐操作历史（游标分页）。
func (api *tenantPlansAPI) listTenantPlanAuditLogs(ctx context.Context, c *app.RequestContext) {
	// 判断 gRPC 客户端是否可用
	if api.plans == nil {
		writeTenantPlanError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "plan grpc client unavailable")
		return
	}
	// 设置 request_id 透传到 gRPC metadata
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	// 调用 gRPC 方法
	res, err := api.plans.ListTenantPlanAuditLogs(callCtx, &tenantv1.ListTenantPlanAuditLogsRequest{
		PlanId: c.Param("planId"),
		Page:   &commonv1.CursorPageRequest{Limit: cursorLimit(c), Cursor: c.Query("cursor")},
	})
	if err != nil {
		mapTenantPlanError(c, err)
		return
	}
	// 返回结果（显式 map：Timestamp / Struct 不能直接 JSON 化 protobuf）
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, it := range res.GetItems() {
		items = append(items, auditLogJSON(it))
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"total":       res.GetTotal(),
		"next_cursor": nullIfEmpty(res.GetNextCursor()),
	})
}

// ---- helpers ----

// tenantPlanJSON 把 gRPC TenantPlan 映射为 OpenAPI TenantPlan / TenantPlanListItem。
// 必须显式构造 map：直接 JSON 序列化 protobuf 会把 Timestamp 打成 {seconds,nanos}，
// 且 tenant_count=0 会因 omitempty 被省略。
func tenantPlanJSON(p *tenantv1.TenantPlan) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	out := map[string]any{
		"id":           p.GetId(),
		"code":         p.GetCode(),
		"name":         p.GetName(),
		"status":       p.GetStatus(),
		"tenant_count": p.GetTenantCount(),
		"created_at":   pbTimestampFormat(p.GetCreatedAt()),
		"updated_at":   pbTimestampFormat(p.GetUpdatedAt()),
	}
	if desc := p.GetDescription(); desc != "" {
		out["description"] = desc
	} else {
		out["description"] = nil
	}
	return out
}

// boundTenantsJSON 把 gRPC BoundTenant 列表映射为 OpenAPI BoundTenant items。
func boundTenantsJSON(items []*tenantv1.BoundTenant) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		out = append(out, map[string]any{
			"id":           it.GetId(),
			"name":         it.GetName(),
			"display_name": it.GetDisplayName(),
			"status":       it.GetStatus(),
		})
	}
	return out
}

// auditLogJSON 把 gRPC AuditLog 映射为 OpenAPI PlanAuditLog（id/action/result/details/created_at）。
func auditLogJSON(it *tenantv1.AuditLog) map[string]any {
	if it == nil {
		return map[string]any{}
	}
	out := map[string]any{
		"id":         it.GetId(),
		"action":     it.GetAction(),
		"result":     it.GetResult(),
		"created_at": pbTimestampFormat(it.GetCreatedAt()),
	}
	if it.GetDetails() != nil {
		out["details"] = it.GetDetails().AsMap()
	} else {
		out["details"] = nil
	}
	return out
}

// pbTimestampFormat 将 protobuf Timestamp 转为「年-月-日 时:分:秒」（Asia/Shanghai 当地时间）。
func pbTimestampFormat(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	t := ts.AsTime()
	if t.IsZero() {
		return ""
	}
	return t.In(tenantPlanDisplayLoc).Format("2006-01-02 15:04:05")
}

// tenantPlanDisplayLoc 套餐 API 时间字段展示时区（中国当地）。
var tenantPlanDisplayLoc = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*60*60)
	}
	return loc
}()

// planQuotaLimitJSON 对齐 OpenAPI PlanQuotaLimitInput：total 为可空整数，不是 protobuf wrapper 对象。
type planQuotaLimitJSON struct {
	ResourceType string `json:"resource_type"`
	Total        *int64 `json:"total"`
}

// toPlanQuotaLimitInputs 把 REST JSON DTO 转为 gRPC PlanQuotaLimitInput（total → Int64Value）。
func toPlanQuotaLimitInputs(items []planQuotaLimitJSON) []*tenantv1.PlanQuotaLimitInput {
	out := make([]*tenantv1.PlanQuotaLimitInput, 0, len(items))
	for _, item := range items {
		in := &tenantv1.PlanQuotaLimitInput{ResourceType: item.ResourceType}
		if item.Total != nil {
			in.Total = wrapperspb.Int64(*item.Total)
		}
		out = append(out, in)
	}
	return out
}

// businessCodeByHTTP 是套餐业务码 → HTTP 状态码映射表（对齐 SPEC §6.1 Error Taxonomy）。
var businessCodeByHTTP = map[string]int{
	"VALIDATION_FAILED":             http.StatusBadRequest,
	"TENANT_PLAN_NOT_FOUND":         http.StatusNotFound,
	"PLAN_CODE_CONFLICT":            http.StatusConflict,
	"PLAN_STATE_INVALID":            http.StatusConflict,
	"TENANT_PLAN_IN_USE":            http.StatusConflict,
	"TENANT_STATE_INVALID":          http.StatusConflict,
	"TENANT_NOT_FOUND":              http.StatusNotFound,
	"QUOTA_NOT_FOUND":               http.StatusNotFound,
	"QUOTA_ALREADY_EXISTS":          http.StatusConflict,
	"PLAN_NOT_ACTIVE":               http.StatusUnprocessableEntity,
	"QUOTA_RESOURCE_NOT_REGISTERED": http.StatusUnprocessableEntity,
}

// sortedBusinessCodes 按业务码长度降序排列，确保前缀匹配（"<CODE>: detail"）优先命中更具体的码。
var sortedBusinessCodes = func() []string {
	codes := make([]string, 0, len(businessCodeByHTTP))
	for code := range businessCodeByHTTP {
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

// mapTenantPlanError 把 gRPC 错误映射为 HTTP 响应：先用 status message 的业务码前缀精确还原，
// 未命中再用 gRPC code 粗粒度兜底（区分同为 409 的 4 个码、同为 422 的 2 个码）。
func mapTenantPlanError(c *app.RequestContext, err error) {
	msg := status.Convert(err).Message()
	for _, code := range sortedBusinessCodes {
		if strings.HasPrefix(msg, code+":") || msg == code {
			writeTenantPlanError(c, businessCodeByHTTP[code], code, strings.TrimSpace(strings.TrimPrefix(msg, code+":")))
			return
		}
	}
	switch status.Code(err) {
	case codes.NotFound:
		writeTenantPlanError(c, http.StatusNotFound, "TENANT_PLAN_NOT_FOUND", msg)
	case codes.InvalidArgument:
		writeTenantPlanError(c, http.StatusBadRequest, "VALIDATION_FAILED", msg)
	case codes.AlreadyExists, codes.FailedPrecondition, codes.Aborted:
		writeTenantPlanError(c, http.StatusConflict, "CONFLICT", msg)
	case codes.DeadlineExceeded:
		writeTenantPlanError(c, http.StatusGatewayTimeout, "GATEWAY_TIMEOUT", msg)
	case codes.Unavailable:
		writeTenantPlanError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", msg)
	default:
		writeTenantPlanError(c, http.StatusInternalServerError, "INTERNAL_ERROR", msg)
	}
}

// writeTenantPlanError 输出统一的 {code, message, request_id} 错误响应并中断请求。
func writeTenantPlanError(c *app.RequestContext, statusCode int, code, message string) {
	c.JSON(statusCode, map[string]any{
		"code":       code,
		"message":    message,
		"request_id": middleware.GetRequestID(c),
	})
	c.Abort()
}
