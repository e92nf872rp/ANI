package router

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// 本文件实现 BOSS 租户计费结算网关接入：/api/v1/svc/billing*。
// 把 REST 请求转发到 tenant-service 的 gRPC BillingService，并把 gRPC 错误映射为
// HTTP 状态与业务码（BILLING_*）。5s 调用超时与 request_id/user_id 透传复用 tenant_common.go。
// 时间字段展示格式（YYYY-MM-DD HH:mm:ss，Asia/Shanghai）复用 pbTimestampFormat。

// billingAPI 持有 tenant-service BillingService gRPC 客户端，作为各路由 handler 的接收者。
// conn 建立失败时字段为 nil，由各 handler 做 nil 守卫兜底返回 502。
type billingAPI struct {
	billing tenantv1.BillingServiceClient
}

// newBillingAPI 由 TENANT_SERVICE_ADDR（缺省 127.0.0.1:9105）创建 gRPC 客户端。
func newBillingAPI() *billingAPI {
	addr := strings.TrimSpace(os.Getenv("TENANT_SERVICE_ADDR"))
	if addr == "" {
		addr = tenantServiceDefaultAddr
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		// Connection failure is surfaced lazily per-call via nil client guards.
		return &billingAPI{}
	}
	return &billingAPI{billing: tenantv1.NewBillingServiceClient(conn)}
}

// registerBilling 在 /api/v1/svc 下注册计费结算全部端点（5 个，方案 §3）。
func registerBilling(svc *route.RouterGroup) {
	api := newBillingAPI()

	svc.GET("/billing/overview", api.getBillingOverview)
	svc.GET("/billing/overview/export", api.exportBillingOverview)
	// 写接口幂等：body idempotency_key（缺省回退 Idempotency-Key 头部）
	svc.POST("/billing/invoices/generate", api.generateInvoice)
	// 路径参数名与 Services OpenAPI 一致：{invoiceId}
	svc.POST("/billing/invoices/:invoiceId/actions", api.invoiceAction)
	svc.POST("/billing/adjustments", api.createAdjustment)
}

// getBillingOverview GET /billing/overview：账务总览（表格 9 列 + 抽屉内嵌子数据）。
func (api *billingAPI) getBillingOverview(ctx context.Context, c *app.RequestContext) {
	// 步骤 1：gRPC 客户端可用性守卫
	if api.billing == nil {
		writeBillingError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "billing grpc client unavailable")
		return
	}
	// 步骤 2：调用 gRPC（query 透传）
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.billing.GetBillingOverview(callCtx, &tenantv1.GetBillingOverviewRequest{
		Period:   c.Query("period"),
		TenantId: c.Query("tenant_id"),
		Status:   c.Query("status"),
	})
	if err != nil {
		mapBillingError(c, err)
		return
	}
	// 步骤 3：映射为 OpenAPI JSON（显式构造，optional 字段 null）
	items := make([]map[string]any, 0, len(res.GetItems()))
	for _, item := range res.GetItems() {
		items = append(items, billingOverviewItemJSON(item))
	}
	c.JSON(http.StatusOK, map[string]any{
		"items":       items,
		"total":       res.GetTotal(),
		"dev_profile": billingDevProfile(),
	})
}

// exportBillingOverview GET /billing/overview/export：导出对账 CSV（text/csv 附件）。
func (api *billingAPI) exportBillingOverview(ctx context.Context, c *app.RequestContext) {
	// 步骤 1：gRPC 客户端可用性守卫
	if api.billing == nil {
		writeBillingError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "billing grpc client unavailable")
		return
	}
	// 步骤 2：调用 gRPC（query 与 overview 一致）
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.billing.ExportBillingOverview(callCtx, &tenantv1.ExportBillingOverviewRequest{
		Period:   c.Query("period"),
		TenantId: c.Query("tenant_id"),
		Status:   c.Query("status"),
	})
	if err != nil {
		mapBillingError(c, err)
		return
	}
	// 步骤 3：text/csv 附件响应（Content-Disposition: attachment）
	filename := res.GetFilename()
	if filename == "" {
		filename = "billing-overview.csv"
	}
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", []byte(res.GetCsv()))
}

// generateInvoice POST /billing/invoices/generate：生成账单（生成即出账；幂等重放返回原账单）。
func (api *billingAPI) generateInvoice(ctx context.Context, c *app.RequestContext) {
	// 步骤 1：gRPC 客户端可用性守卫
	if api.billing == nil {
		writeBillingError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "billing grpc client unavailable")
		return
	}
	// 步骤 2：解析请求体（幂等键缺省回退 Idempotency-Key 头部）
	var body struct {
		TenantId       string `json:"tenant_id"`
		Period         string `json:"period"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeBillingError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	// 步骤 3：调用 gRPC（operator = 网关透传 user_id）
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.billing.GenerateInvoice(callCtx, &tenantv1.GenerateInvoiceRequest{
		TenantId:       body.TenantId,
		Period:         body.Period,
		IdempotencyKey: body.IdempotencyKey,
		Operator:       middleware.GetUserID(c),
	})
	if err != nil {
		mapBillingError(c, err)
		return
	}
	// 步骤 4：返回账单摘要（200；幂等重放同样 200 返回原账单）
	c.JSON(http.StatusOK, billingInvoiceJSON(res))
}

// invoiceAction POST /billing/invoices/:invoiceId/actions：账单状态动作（settle/credit）。
func (api *billingAPI) invoiceAction(ctx context.Context, c *app.RequestContext) {
	// 步骤 1：gRPC 客户端可用性守卫
	if api.billing == nil {
		writeBillingError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "billing grpc client unavailable")
		return
	}
	// 步骤 2：解析请求体（幂等键缺省回退 Idempotency-Key 头部）
	var body struct {
		Action         string `json:"action"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeBillingError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	// 步骤 3：调用 gRPC
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.billing.InvoiceAction(callCtx, &tenantv1.InvoiceActionRequest{
		InvoiceId:      c.Param("invoiceId"),
		Action:         body.Action,
		IdempotencyKey: body.IdempotencyKey,
		Operator:       middleware.GetUserID(c),
	})
	if err != nil {
		mapBillingError(c, err)
		return
	}
	// 步骤 4：返回更新后的账单
	c.JSON(http.StatusOK, billingInvoiceJSON(res))
}

// createAdjustment POST /billing/adjustments：调账（±金额 + 原因）。
func (api *billingAPI) createAdjustment(ctx context.Context, c *app.RequestContext) {
	// 步骤 1：gRPC 客户端可用性守卫
	if api.billing == nil {
		writeBillingError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", "billing grpc client unavailable")
		return
	}
	// 步骤 2：解析请求体（幂等键缺省回退 Idempotency-Key 头部）
	var body struct {
		TenantId       string  `json:"tenant_id"`
		Period         string  `json:"period"`
		AmountUsd      float64 `json:"amount_usd"`
		Reason         string  `json:"reason"`
		IdempotencyKey string  `json:"idempotency_key"`
	}
	if err := c.BindJSON(&body); err != nil {
		writeBillingError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid request body")
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = idempotencyHeader(c)
	}
	// 步骤 3：调用 gRPC（operator = 网关透传 user_id）
	callCtx, cancel := tenantCallCtx(ctx, c)
	defer cancel()
	res, err := api.billing.CreateAdjustment(callCtx, &tenantv1.CreateAdjustmentRequest{
		TenantId:       body.TenantId,
		Period:         body.Period,
		AmountUsd:      body.AmountUsd,
		Reason:         body.Reason,
		IdempotencyKey: body.IdempotencyKey,
		Operator:       middleware.GetUserID(c),
	})
	if err != nil {
		mapBillingError(c, err)
		return
	}
	// 步骤 4：返回调账记录
	c.JSON(http.StatusOK, billingAdjustmentJSON(res))
}

// ---- helpers ----

// billingOverviewItemJSON 把 gRPC BillingOverviewItem 映射为 OpenAPI JSON（optional → null）。
func billingOverviewItemJSON(item *tenantv1.BillingOverviewItem) map[string]any {
	if item == nil {
		return map[string]any{}
	}
	// 步骤 1：breakdown 子数组（amount/unit_cost/cost optional → null）
	breakdown := make([]map[string]any, 0, len(item.GetUsageBreakdown()))
	for _, bd := range item.GetUsageBreakdown() {
		breakdown = append(breakdown, map[string]any{
			"metric":      bd.GetMetric(),
			"amount":      optionalFloatJSON(bd.Amount),
			"unit_cost":   optionalFloatJSON(bd.UnitCost),
			"cost":        optionalFloatJSON(bd.Cost),
			"data_source": bd.GetDataSource(),
		})
	}
	// 步骤 2：invoices / adjustments 子数组
	invoices := make([]map[string]any, 0, len(item.GetInvoices()))
	for _, inv := range item.GetInvoices() {
		invoices = append(invoices, billingInvoiceJSON(inv))
	}
	adjustments := make([]map[string]any, 0, len(item.GetAdjustments()))
	for _, adj := range item.GetAdjustments() {
		adjustments = append(adjustments, billingAdjustmentJSON(adj))
	}
	// 步骤 3：组装行（9 列 + 内嵌子数据）
	return map[string]any{
		"tenant_id":       item.GetTenantId(),
		"tenant_name":     item.GetTenantName(),
		"period":          item.GetPeriod(),
		"status":          item.GetStatus(),
		"usage_cost_usd":  optionalFloatJSON(item.UsageCostUsd),
		"credit_usd":      optionalFloatJSON(item.CreditUsd),
		"balance_usd":     optionalFloatJSON(item.BalanceUsd),
		"invoice_no":      optionalStringJSON(item.InvoiceNo),
		"due_date":        optionalStringJSON(item.DueDate),
		"usage_breakdown": breakdown,
		"invoices":        invoices,
		"adjustments":     adjustments,
		"updated_at":      pbTimestampFormat(item.GetUpdatedAt()),
	}
}

// billingInvoiceJSON 把 gRPC BillingInvoice 映射为 OpenAPI BillingInvoiceSummary。
func billingInvoiceJSON(inv *tenantv1.BillingInvoice) map[string]any {
	if inv == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":         inv.GetId(),
		"no":         inv.GetNo(),
		"period":     inv.GetPeriod(),
		"amount_usd": inv.GetAmountUsd(),
		"status":     inv.GetStatus(),
		"due_date":   inv.GetDueDate(),
		"issued_at":  pbTimestampFormat(inv.GetIssuedAt()),
		"settled_at": billingTimestampOrNil(inv.GetSettledAt()),
		"credited_at": billingTimestampOrNil(inv.GetCreditedAt()),
	}
}

// billingAdjustmentJSON 把 gRPC BillingAdjustment 映射为 OpenAPI BillingAdjustment。
func billingAdjustmentJSON(adj *tenantv1.BillingAdjustment) map[string]any {
	if adj == nil {
		return map[string]any{}
	}
	return map[string]any{
		"id":         adj.GetId(),
		"tenant_id":  adj.GetTenantId(),
		"period":     adj.GetPeriod(),
		"amount_usd": adj.GetAmountUsd(),
		"reason":     adj.GetReason(),
		"operator":   adj.GetOperator(),
		"created_at": pbTimestampFormat(adj.GetCreatedAt()),
	}
}

// optionalFloatJSON optional float → JSON（nil → null）。
func optionalFloatJSON(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

// optionalStringJSON optional string → JSON（nil → null）。
func optionalStringJSON(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

// billingTimestampOrNil 时间戳 → JSON（nil/零值 → null；未结清/未冲抵契约 null 语义）。
func billingTimestampOrNil(ts *timestamppb.Timestamp) any {
	if ts == nil || ts.AsTime().IsZero() {
		return nil
	}
	return pbTimestampFormat(ts)
}

// billingDevProfile Services 响应的 dev_profile 标记（联调观测用；billing 域无独立 provider 状态）。
func billingDevProfile() map[string]any {
	return map[string]any{
		"mode":          "services",
		"provider":      "tenant-service",
		"real_provider": true,
		"reason":        nil,
	}
}

// billingCodeByHTTP 是计费业务码 → HTTP 状态码映射表。
var billingCodeByHTTP = map[string]int{
	"VALIDATION_FAILED":              http.StatusBadRequest,
	"BILLING_INVALID_PERIOD":         http.StatusBadRequest,
	"BILLING_ACTION_INVALID":         http.StatusBadRequest,
	"BILLING_AMOUNT_INVALID":         http.StatusBadRequest,
	"BILLING_TENANT_NOT_FOUND":       http.StatusNotFound,
	"BILLING_INVOICE_NOT_FOUND":      http.StatusNotFound,
	"TENANT_NOT_FOUND":               http.StatusNotFound,
	"BILLING_INVOICE_EXISTS":         http.StatusConflict,
	"BILLING_STATE_CONFLICT":         http.StatusConflict,
	"BILLING_IDEMPOTENCY_CONFLICT":   http.StatusConflict,
	"CORE_UNAVAILABLE":               http.StatusBadGateway,
	"GRPC_CLIENT_UNAVAILABLE":        http.StatusBadGateway,
}

// sortedBillingCodes 按业务码长度降序排列，确保前缀匹配（"<CODE>: detail"）优先命中更具体的码。
var sortedBillingCodes = func() []string {
	codes := make([]string, 0, len(billingCodeByHTTP))
	for code := range billingCodeByHTTP {
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

// mapBillingError 把 gRPC 错误映射为 HTTP 响应：先用 status message 的业务码前缀精确还原，
// 未命中再用 gRPC code 粗粒度兜底。
func mapBillingError(c *app.RequestContext, err error) {
	msg := status.Convert(err).Message()
	for _, code := range sortedBillingCodes {
		if strings.HasPrefix(msg, code+":") || msg == code {
			writeBillingError(c, billingCodeByHTTP[code], code, strings.TrimSpace(strings.TrimPrefix(msg, code+":")))
			return
		}
	}
	switch status.Code(err) {
	case codes.NotFound:
		writeBillingError(c, http.StatusNotFound, "BILLING_INVOICE_NOT_FOUND", msg)
	case codes.InvalidArgument:
		writeBillingError(c, http.StatusBadRequest, "VALIDATION_FAILED", msg)
	case codes.AlreadyExists, codes.FailedPrecondition:
		writeBillingError(c, http.StatusConflict, "CONFLICT", msg)
	case codes.DeadlineExceeded:
		writeBillingError(c, http.StatusGatewayTimeout, "GATEWAY_TIMEOUT", msg)
	case codes.Unavailable:
		writeBillingError(c, http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE", msg)
	default:
		writeBillingError(c, http.StatusInternalServerError, "INTERNAL_ERROR", msg)
	}
}

// writeBillingError 输出统一的 {code, message, request_id} 错误响应并中断请求。
func writeBillingError(c *app.RequestContext, statusCode int, code, message string) {
	c.JSON(statusCode, map[string]any{
		"code":       code,
		"message":    message,
		"request_id": middleware.GetRequestID(c),
	})
	c.Abort()
}
