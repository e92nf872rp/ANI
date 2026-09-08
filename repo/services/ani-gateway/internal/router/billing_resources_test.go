package router

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// 计费网关 handler 单测：gRPC 错误 → HTTP 状态/业务码映射、幂等键回退、
// optional 字段 null 语义、nil 客户端 502 守卫、CSV 附件响应头。

// ── 错误映射 ────────────────────────────────────────────────────────────

func TestMapBillingError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "validation failed", err: status.Error(codes.InvalidArgument, "VALIDATION_FAILED: tenant_id required"), wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_FAILED"},
		{name: "invalid period", err: status.Error(codes.InvalidArgument, "BILLING_INVALID_PERIOD: period must match YYYY-MM"), wantStatus: http.StatusBadRequest, wantCode: "BILLING_INVALID_PERIOD"},
		{name: "action invalid", err: status.Error(codes.InvalidArgument, "BILLING_ACTION_INVALID: action must be settle or credit"), wantStatus: http.StatusBadRequest, wantCode: "BILLING_ACTION_INVALID"},
		{name: "amount invalid", err: status.Error(codes.InvalidArgument, "BILLING_AMOUNT_INVALID: amount_usd must not be zero"), wantStatus: http.StatusBadRequest, wantCode: "BILLING_AMOUNT_INVALID"},
		{name: "tenant not found", err: status.Error(codes.NotFound, "BILLING_TENANT_NOT_FOUND: tenant not found"), wantStatus: http.StatusNotFound, wantCode: "BILLING_TENANT_NOT_FOUND"},
		{name: "invoice not found", err: status.Error(codes.NotFound, "BILLING_INVOICE_NOT_FOUND: billing invoice not found"), wantStatus: http.StatusNotFound, wantCode: "BILLING_INVOICE_NOT_FOUND"},
		{name: "invoice exists", err: status.Error(codes.AlreadyExists, "BILLING_INVOICE_EXISTS: invoice already exists for tenant and period"), wantStatus: http.StatusConflict, wantCode: "BILLING_INVOICE_EXISTS"},
		{name: "state conflict", err: status.Error(codes.FailedPrecondition, "BILLING_STATE_CONFLICT: invoice already settled or credited"), wantStatus: http.StatusConflict, wantCode: "BILLING_STATE_CONFLICT"},
		{name: "idempotency conflict", err: status.Error(codes.AlreadyExists, "BILLING_IDEMPOTENCY_CONFLICT: idempotency key conflict, please retry"), wantStatus: http.StatusConflict, wantCode: "BILLING_IDEMPOTENCY_CONFLICT"},
		{name: "core unavailable", err: status.Error(codes.Unavailable, "CORE_UNAVAILABLE: core metering api unavailable"), wantStatus: http.StatusBadGateway, wantCode: "CORE_UNAVAILABLE"},
		{name: "grpc deadline", err: status.Error(codes.DeadlineExceeded, "context deadline exceeded"), wantStatus: http.StatusGatewayTimeout, wantCode: "GATEWAY_TIMEOUT"},
		{name: "grpc unavailable no code", err: status.Error(codes.Unavailable, "connection refused"), wantStatus: http.StatusBadGateway, wantCode: "GRPC_CLIENT_UNAVAILABLE"},
		{name: "grpc not found no code", err: status.Error(codes.NotFound, "missing"), wantStatus: http.StatusNotFound, wantCode: "BILLING_INVOICE_NOT_FOUND"},
		{name: "grpc internal", err: status.Error(codes.Internal, "boom"), wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL_ERROR"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &app.RequestContext{}
			mapBillingError(c, tc.err)
			body := string(c.Response.Body())
			if c.Response.StatusCode() != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", c.Response.StatusCode(), tc.wantStatus, body)
			}
			if !strings.Contains(body, `"code":"`+tc.wantCode+`"`) {
				t.Fatalf("body = %s, want code %q", body, tc.wantCode)
			}
			// 统一错误结构：code/message/request_id 三字段
			if !strings.Contains(body, `"message"`) || !strings.Contains(body, `"request_id"`) {
				t.Fatalf("body missing message/request_id: %s", body)
			}
		})
	}
}

// ── JSON 映射（optional → null 语义）─────────────────────────────────────

func TestBillingInvoiceJSONMapping(t *testing.T) {
	issuedAt := timestamppb.New(time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)) // 10:00 +08
	out := billingInvoiceJSON(&tenantv1.BillingInvoice{
		Id: "i-1", No: "INV-2609-01", Period: "2026-09", AmountUsd: 2, Status: "issued",
		DueDate: "2026-10-01", IssuedAt: issuedAt,
	})
	if out["id"] != "i-1" || out["no"] != "INV-2609-01" || out["status"] != "issued" || out["amount_usd"] != float64(2) {
		t.Fatalf("invoice json = %+v", out)
	}
	if out["issued_at"] != "2026-09-01 10:00:00" {
		t.Fatalf("issued_at = %v, want Asia/Shanghai format", out["issued_at"])
	}
	if v, ok := out["settled_at"]; !ok || v != nil {
		t.Fatalf("settled_at = %v, want null（未结清）", out["settled_at"])
	}
	if v, ok := out["credited_at"]; !ok || v != nil {
		t.Fatalf("credited_at = %v, want null", out["credited_at"])
	}
}

func TestBillingOverviewItemJSONMapping(t *testing.T) {
	cost := 2.4
	item := &tenantv1.BillingOverviewItem{
		TenantId: "tid", TenantName: "acme", Period: "2026-09", Status: "current",
		UsageCostUsd: &cost,
		UsageBreakdown: []*tenantv1.BillingUsageBreakdown{
			{Metric: "gpu_hours", Amount: &cost, UnitCost: &cost, Cost: &cost, DataSource: "metered"},
			{Metric: "tokens", DataSource: "unavailable"},
		},
		UpdatedAt: timestamppb.Now(),
	}
	out := billingOverviewItemJSON(item)
	if out["credit_usd"] != nil || out["balance_usd"] != nil || out["invoice_no"] != nil || out["due_date"] != nil {
		t.Fatalf("optional fields should be null: %+v", out)
	}
	breakdown := out["usage_breakdown"].([]map[string]any)
	if breakdown[0]["data_source"] != "metered" || breakdown[0]["amount"] != 2.4 {
		t.Fatalf("metered row = %+v", breakdown[0])
	}
	if breakdown[1]["data_source"] != "unavailable" || breakdown[1]["amount"] != nil || breakdown[1]["cost"] != nil {
		t.Fatalf("unavailable row = %+v, want null amount/cost", breakdown[1])
	}
}

func TestBillingTimestampOrNil(t *testing.T) {
	if got := billingTimestampOrNil(nil); got != nil {
		t.Fatalf("nil ts → %v, want nil", got)
	}
	if got := billingTimestampOrNil(timestamppb.New(time.Time{})); got != nil {
		t.Fatalf("go-zero ts → %v, want nil", got)
	}
	got := billingTimestampOrNil(timestamppb.New(time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)))
	if got != "2026-09-01 10:00:00" {
		t.Fatalf("valid ts → %v", got)
	}
}

// ── handler（fake gRPC client 注入）─────────────────────────────────────

// fakeBillingGRPCClient 是 tenantv1.BillingServiceClient 的内存替身。
type fakeBillingGRPCClient struct {
	overview    *tenantv1.GetBillingOverviewResponse
	overviewErr error
	export      *tenantv1.ExportBillingOverviewResponse
	exportErr   error
	invoice     *tenantv1.BillingInvoice
	genErr      error
	actionRes   *tenantv1.BillingInvoice
	actionErr   error
	adjustment  *tenantv1.BillingAdjustment
	adjustErr   error
	operations  *tenantv1.ListBillingOperationsResponse
	opsErr      error

	lastOverviewReq *tenantv1.GetBillingOverviewRequest
	lastGenReq      *tenantv1.GenerateInvoiceRequest
	lastActionReq   *tenantv1.InvoiceActionRequest
	lastAdjustReq   *tenantv1.CreateAdjustmentRequest
	lastOpsReq      *tenantv1.ListBillingOperationsRequest
}

func (f *fakeBillingGRPCClient) GetBillingOverview(_ context.Context, in *tenantv1.GetBillingOverviewRequest, _ ...grpc.CallOption) (*tenantv1.GetBillingOverviewResponse, error) {
	f.lastOverviewReq = in
	if f.overviewErr != nil {
		return nil, f.overviewErr
	}
	if f.overview != nil {
		return f.overview, nil
	}
	return &tenantv1.GetBillingOverviewResponse{}, nil
}

func (f *fakeBillingGRPCClient) ExportBillingOverview(_ context.Context, in *tenantv1.ExportBillingOverviewRequest, _ ...grpc.CallOption) (*tenantv1.ExportBillingOverviewResponse, error) {
	if f.exportErr != nil {
		return nil, f.exportErr
	}
	if f.export != nil {
		return f.export, nil
	}
	return &tenantv1.ExportBillingOverviewResponse{}, nil
}

func (f *fakeBillingGRPCClient) GenerateInvoice(_ context.Context, in *tenantv1.GenerateInvoiceRequest, _ ...grpc.CallOption) (*tenantv1.BillingInvoice, error) {
	f.lastGenReq = in
	if f.genErr != nil {
		return nil, f.genErr
	}
	if f.invoice != nil {
		return f.invoice, nil
	}
	return &tenantv1.BillingInvoice{}, nil
}

func (f *fakeBillingGRPCClient) InvoiceAction(_ context.Context, in *tenantv1.InvoiceActionRequest, _ ...grpc.CallOption) (*tenantv1.BillingInvoice, error) {
	f.lastActionReq = in
	if f.actionErr != nil {
		return nil, f.actionErr
	}
	if f.actionRes != nil {
		return f.actionRes, nil
	}
	return &tenantv1.BillingInvoice{}, nil
}

func (f *fakeBillingGRPCClient) CreateAdjustment(_ context.Context, in *tenantv1.CreateAdjustmentRequest, _ ...grpc.CallOption) (*tenantv1.BillingAdjustment, error) {
	f.lastAdjustReq = in
	if f.adjustErr != nil {
		return nil, f.adjustErr
	}
	if f.adjustment != nil {
		return f.adjustment, nil
	}
	return &tenantv1.BillingAdjustment{}, nil
}

func (f *fakeBillingGRPCClient) ListBillingOperations(_ context.Context, in *tenantv1.ListBillingOperationsRequest, _ ...grpc.CallOption) (*tenantv1.ListBillingOperationsResponse, error) {
	f.lastOpsReq = in
	if f.opsErr != nil {
		return nil, f.opsErr
	}
	if f.operations != nil {
		return f.operations, nil
	}
	return &tenantv1.ListBillingOperationsResponse{}, nil
}

// newBillingTestServer 用注入的 gRPC 替身组装 billing 路由（路径与 registerBilling 一致）。
func newBillingTestServer(client tenantv1.BillingServiceClient) *server.Hertz {
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("request_id", "req-billing-1")
		c.Set("user_id", "user-boss")
		c.Next(ctx)
	})
	api := &billingAPI{billing: client}
	svc := h.Group("/api/v1/svc")
	svc.GET("/billing/overview", api.getBillingOverview)
	svc.GET("/billing/overview/export", api.exportBillingOverview)
	svc.POST("/billing/invoices/generate", api.generateInvoice)
	svc.POST("/billing/invoices/:invoiceId/actions", api.invoiceAction)
	svc.POST("/billing/adjustments", api.createAdjustment)
	svc.GET("/billing/operations", api.listBillingOperations)
	return h
}

func postJSON(h *server.Hertz, path, body string) *ut.ResponseRecorder {
	return ut.PerformRequest(h.Engine, http.MethodPost, path,
		&ut.Body{Body: strings.NewReader(body), Len: len(body)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	)
}

func TestBillingGenerateInvoiceHandler(t *testing.T) {
	fake := &fakeBillingGRPCClient{invoice: &tenantv1.BillingInvoice{
		Id: "i-1", No: "INV-2609-01", Period: "2026-09", AmountUsd: 2, Status: "issued", DueDate: "2026-10-01",
	}}
	h := newBillingTestServer(fake)

	resp := postJSON(h, "/api/v1/svc/billing/invoices/generate",
		`{"tenant_id":"11111111-1111-1111-1111-111111111111","period":"2026-09","idempotency_key":"11111111-2222-4333-8444-555555555555"}`).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode(), resp.Body())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["no"] != "INV-2609-01" || body["status"] != "issued" {
		t.Fatalf("body = %+v", body)
	}
	if fake.lastGenReq == nil || fake.lastGenReq.Operator != "user-boss" {
		t.Fatalf("operator passthrough = %+v", fake.lastGenReq)
	}

	// Idempotency-Key 头部回退：body 缺 key 时取头部
	headerBody := `{"tenant_id":"11111111-1111-1111-1111-111111111111","period":"2026-09"}`
	resp = ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/svc/billing/invoices/generate",
		&ut.Body{Body: strings.NewReader(headerBody), Len: len(headerBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "Idempotency-Key", Value: "99999999-8888-4777-8666-555555555555"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("header-fallback status = %d", resp.StatusCode())
	}
	if fake.lastGenReq == nil || fake.lastGenReq.IdempotencyKey != "99999999-8888-4777-8666-555555555555" {
		t.Fatalf("idempotency header fallback = %+v", fake.lastGenReq)
	}
}

func TestBillingGenerateInvoiceHandlerConflictPassthrough(t *testing.T) {
	fake := &fakeBillingGRPCClient{genErr: status.Error(codes.AlreadyExists,
		"BILLING_INVOICE_EXISTS: invoice already exists for tenant and period")}
	h := newBillingTestServer(fake)

	resp := postJSON(h, "/api/v1/svc/billing/invoices/generate",
		`{"tenant_id":"11111111-1111-1111-1111-111111111111","period":"2026-09","idempotency_key":"11111111-2222-4333-8444-555555555555"}`).Result()
	if resp.StatusCode() != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", resp.StatusCode(), resp.Body())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "BILLING_INVOICE_EXISTS" {
		t.Fatalf("code = %v", body["code"])
	}
	if body["request_id"] != "req-billing-1" {
		t.Fatalf("request_id = %v, want 透传", body["request_id"])
	}
}

func TestBillingHandlerNilClientGuard(t *testing.T) {
	h := newBillingTestServer(nil) // billing client 不可用（newBillingAPI 连接失败场景）
	paths := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/svc/billing/overview"},
		{http.MethodGet, "/api/v1/svc/billing/overview/export"},
		{http.MethodPost, "/api/v1/svc/billing/invoices/generate"},
		{http.MethodPost, "/api/v1/svc/billing/invoices/inv-1/actions"},
		{http.MethodPost, "/api/v1/svc/billing/adjustments"},
		{http.MethodGet, "/api/v1/svc/billing/operations"},
	}
	for _, p := range paths {
		resp := ut.PerformRequest(h.Engine, p.method, p.path, nil).Result()
		if resp.StatusCode() != http.StatusBadGateway {
			t.Fatalf("%s %s status = %d, want 502; body=%s", p.method, p.path, resp.StatusCode(), resp.Body())
		}
		if !strings.Contains(string(resp.Body()), "GRPC_CLIENT_UNAVAILABLE") {
			t.Fatalf("%s %s body = %s", p.method, p.path, resp.Body())
		}
	}
}

func TestBillingOverviewHandlerQueryPassthrough(t *testing.T) {
	fake := &fakeBillingGRPCClient{overview: &tenantv1.GetBillingOverviewResponse{
		Items: []*tenantv1.BillingOverviewItem{{
			TenantId: "tid", TenantName: "acme", Period: "2026-09", Status: "current",
		}},
		Total: 1,
	}}
	h := newBillingTestServer(fake)

	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/billing/overview?period=2026-09&tenant_id=tid&status=current", nil).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d; body=%s", resp.StatusCode(), resp.Body())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["total"] != float64(1) || len(body["items"].([]any)) != 1 {
		t.Fatalf("body = %+v", body)
	}
	// dev_profile 标记
	dp, _ := body["dev_profile"].(map[string]any)
	if dp == nil || dp["provider"] != "tenant-service" {
		t.Fatalf("dev_profile = %+v", body["dev_profile"])
	}
	q := fake.lastOverviewReq
	if q.GetPeriod() != "2026-09" || q.GetTenantId() != "tid" || q.GetStatus() != "current" {
		t.Fatalf("query passthrough = %+v", q)
	}
}

func TestBillingExportHandlerCSVResponse(t *testing.T) {
	fake := &fakeBillingGRPCClient{export: &tenantv1.ExportBillingOverviewResponse{
		Csv: "tenant_id,tenant_name\r\n", Filename: "billing-overview-2026-09.csv",
	}}
	h := newBillingTestServer(fake)

	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/billing/overview/export?period=2026-09", nil).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode())
	}
	if ct := string(resp.Header.ContentType()); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("content-type = %q, want text/csv", ct)
	}
	if cd := string(resp.Header.Peek("Content-Disposition")); !strings.Contains(cd, `attachment; filename="billing-overview-2026-09.csv"`) {
		t.Fatalf("content-disposition = %q", cd)
	}
}

func TestBillingInvoiceActionAndAdjustmentHandlers(t *testing.T) {
	fake := &fakeBillingGRPCClient{
		actionRes: &tenantv1.BillingInvoice{Id: "i-1", Status: "settled"},
		adjustment: &tenantv1.BillingAdjustment{
			Id: "a-1", TenantId: "tid", Period: "2026-09", AmountUsd: -1.5, Reason: "promo",
		},
	}
	h := newBillingTestServer(fake)

	// action：路径参数 invoiceId + body action
	resp := postJSON(h, "/api/v1/svc/billing/invoices/i-1/actions", `{"action":"settle","idempotency_key":"11111111-2222-4333-8444-555555555555"}`).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("action status = %d; body=%s", resp.StatusCode(), resp.Body())
	}
	if fake.lastActionReq.GetInvoiceId() != "i-1" || fake.lastActionReq.GetAction() != "settle" {
		t.Fatalf("action req = %+v", fake.lastActionReq)
	}

	// adjustment：负金额透传
	resp = postJSON(h, "/api/v1/svc/billing/adjustments",
		`{"tenant_id":"tid","period":"2026-09","amount_usd":-1.5,"reason":"promo","idempotency_key":"11111111-2222-4333-8444-555555555555"}`).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("adjustment status = %d; body=%s", resp.StatusCode(), resp.Body())
	}
	if fake.lastAdjustReq.GetAmountUsd() != -1.5 || fake.lastAdjustReq.GetReason() != "promo" {
		t.Fatalf("adjustment req = %+v", fake.lastAdjustReq)
	}

	// 非法 JSON → 400 VALIDATION_FAILED
	resp = postJSON(h, "/api/v1/svc/billing/adjustments", `{bad json`).Result()
	if resp.StatusCode() != http.StatusBadRequest || !strings.Contains(string(resp.Body()), "VALIDATION_FAILED") {
		t.Fatalf("bad json status/body = %d/%s", resp.StatusCode(), resp.Body())
	}
}

func TestBillingOperationsHandler(t *testing.T) {
	period := "2026-09"
	refID := "inv-1"
	fake := &fakeBillingGRPCClient{operations: &tenantv1.ListBillingOperationsResponse{
		Items: []*tenantv1.BillingOperationLog{
			{
				Id: "log-1", TenantId: "tid", Period: &period, Action: "invoice.generated",
				RefId: &refID, Message: "生成账单 INV-2609-01 $2.00", Operator: "user-boss",
				CreatedAt: timestamppb.New(time.Date(2026, 9, 8, 15, 4, 5, 0, time.FixedZone("CST", 8*3600))),
			},
			{
				Id: "log-2", TenantId: "tid", Action: "adjustment_created",
				Message: "调账 -$1.50", Operator: "user-boss",
				CreatedAt: timestamppb.New(time.Date(2026, 9, 8, 15, 5, 0, 0, time.FixedZone("CST", 8*3600))),
			},
		},
		Total: 2,
	}}
	h := newBillingTestServer(fake)

	resp := ut.PerformRequest(h.Engine, http.MethodGet,
		"/api/v1/svc/billing/operations?tenant_id=tid&limit=10&offset=5", nil).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d; body=%s", resp.StatusCode(), resp.Body())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["total"] != float64(2) || len(body["items"].([]any)) != 2 {
		t.Fatalf("body = %+v", body)
	}
	first := body["items"].([]any)[0].(map[string]any)
	if first["action"] != "invoice.generated" || first["message"] != "生成账单 INV-2609-01 $2.00" || first["operator"] != "user-boss" {
		t.Fatalf("first item = %+v", first)
	}
	// ref_id 有值透传、period 有值透传
	if first["ref_id"] != "inv-1" || first["period"] != "2026-09" {
		t.Fatalf("optional passthrough = %+v", first)
	}
	// 第二条：period/ref_id 缺省 → null（optional 语义）
	second := body["items"].([]any)[1].(map[string]any)
	if second["period"] != nil || second["ref_id"] != nil {
		t.Fatalf("optional null semantics = %+v", second)
	}
	// dev_profile 标记
	dp, _ := body["dev_profile"].(map[string]any)
	if dp == nil || dp["provider"] != "tenant-service" {
		t.Fatalf("dev_profile = %+v", body["dev_profile"])
	}
	// 查询参数透传
	if fake.lastOpsReq == nil || fake.lastOpsReq.GetTenantId() != "tid" || fake.lastOpsReq.GetLimit() != 10 || fake.lastOpsReq.GetOffset() != 5 {
		t.Fatalf("query passthrough = %+v", fake.lastOpsReq)
	}
}

func TestBillingOperationsHandlerErrorMapping(t *testing.T) {
	fake := &fakeBillingGRPCClient{opsErr: status.Error(codes.InvalidArgument, "VALIDATION_FAILED: tenant_id required")}
	h := newBillingTestServer(fake)

	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/svc/billing/operations", nil).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d; body=%s", resp.StatusCode(), resp.Body())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "VALIDATION_FAILED" || body["request_id"] != "req-billing-1" {
		t.Fatalf("body = %+v", body)
	}
}
