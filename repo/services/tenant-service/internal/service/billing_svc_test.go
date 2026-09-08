package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc/codes"
)

// 计费结算 gRPC 服务层单测：幂等重放、(tenant_id, period) 冲突、状态机、
// 金额折算（定价 × 用量 + 调账）、overdue 动态计算、总览行集合与过滤。
// postgres store / core client 分别由 fake 内存实现替身，adapter 层另有专属单测。

// ── fakes ───────────────────────────────────────────────────────────────

// fakeBillingStore 内存版 BillingStore（模拟 (tenant_id, period)/幂等键/no 唯一约束）。
type fakeBillingStore struct {
	invoices    map[uuid.UUID]*ports.BillingInvoice
	byPeriod    map[string]*ports.BillingInvoice // key: tenantID|period
	adjustments []ports.BillingAdjustment
	credits     map[uuid.UUID]float64
	pricing     []ports.BillingPricing
	footprint   []uuid.UUID

	createInvoiceErr   error // 首次 CreateInvoice 返回该错误后自动清除（模拟唯一冲突一次）
	createInvoiceCalls int
	createAdjustErr    error
	hideKeyUntilCreate uuid.UUID // 置位时 GetAdjustmentByIdempotencyKey 跳过该键（模拟并发插入后可见）
}

func newFakeBillingStore() *fakeBillingStore {
	return &fakeBillingStore{
		invoices: map[uuid.UUID]*ports.BillingInvoice{},
		byPeriod: map[string]*ports.BillingInvoice{},
		credits:  map[uuid.UUID]float64{},
	}
}

func billingPeriodKey(tenantID uuid.UUID, period string) string {
	return tenantID.String() + "|" + period
}

func (f *fakeBillingStore) ListInvoicesByTenant(_ context.Context, tenantID uuid.UUID) ([]ports.BillingInvoice, error) {
	out := make([]ports.BillingInvoice, 0)
	for _, inv := range f.invoices {
		if inv.TenantID == tenantID {
			out = append(out, *inv)
		}
	}
	return out, nil
}

func (f *fakeBillingStore) GetInvoiceByPeriod(_ context.Context, tenantID uuid.UUID, period string) (*ports.BillingInvoice, error) {
	if inv, ok := f.byPeriod[billingPeriodKey(tenantID, period)]; ok {
		cp := *inv
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeBillingStore) GetInvoice(_ context.Context, id uuid.UUID) (*ports.BillingInvoice, error) {
	if inv, ok := f.invoices[id]; ok {
		cp := *inv
		return &cp, nil
	}
	return nil, ports.ErrBillingInvoiceNotFound
}

func (f *fakeBillingStore) CountInvoicesByNoPrefix(_ context.Context, prefix string) (int, error) {
	n := 0
	for _, inv := range f.invoices {
		if strings.HasPrefix(inv.No, prefix) {
			n++
		}
	}
	return n, nil
}

func (f *fakeBillingStore) CreateInvoice(_ context.Context, in ports.CreateBillingInvoiceInput) (*ports.BillingInvoice, error) {
	f.createInvoiceCalls++
	if f.createInvoiceErr != nil {
		err := f.createInvoiceErr
		f.createInvoiceErr = nil // 模拟只冲突一次
		return nil, err
	}
	if _, exists := f.byPeriod[billingPeriodKey(in.TenantID, in.Period)]; exists {
		return nil, ports.ErrBillingInvoiceExists
	}
	inv := &ports.BillingInvoice{
		ID:             uuid.New(),
		TenantID:       in.TenantID,
		Period:         in.Period,
		No:             in.No,
		AmountUSD:      in.AmountUSD,
		Status:         ports.BillingInvoiceIssued,
		DueDate:        in.DueDate,
		IssuedAt:       in.IssuedAt,
		IdempotencyKey: in.IdempotencyKey,
		CreatedAt:      time.Now(),
	}
	f.invoices[inv.ID] = inv
	f.byPeriod[billingPeriodKey(in.TenantID, in.Period)] = inv
	cp := *inv
	return &cp, nil
}

func (f *fakeBillingStore) UpdateInvoiceStatus(_ context.Context, id uuid.UUID, action ports.BillingInvoiceAction, at time.Time) (*ports.BillingInvoice, error) {
	inv, ok := f.invoices[id]
	if !ok {
		return nil, ports.ErrBillingInvoiceNotFound
	}
	if inv.Status != ports.BillingInvoiceIssued {
		return nil, ports.ErrBillingStateConflict
	}
	switch action {
	case ports.BillingActionSettle:
		inv.Status = ports.BillingInvoiceSettled
		inv.SettledAt = &at
	case ports.BillingActionCredit:
		inv.Status = ports.BillingInvoiceCredited
		inv.CreditedAt = &at
	}
	cp := *inv
	return &cp, nil
}

func (f *fakeBillingStore) ListAdjustmentsByTenant(_ context.Context, tenantID uuid.UUID) ([]ports.BillingAdjustment, error) {
	out := make([]ports.BillingAdjustment, 0)
	for _, adj := range f.adjustments {
		if adj.TenantID == tenantID {
			out = append(out, adj)
		}
	}
	return out, nil
}

func (f *fakeBillingStore) GetAdjustmentByIdempotencyKey(_ context.Context, key uuid.UUID) (*ports.BillingAdjustment, error) {
	for _, adj := range f.adjustments {
		if adj.IdempotencyKey != key {
			continue
		}
		if f.hideKeyUntilCreate == key {
			continue // 模拟并发插入尚未可见
		}
		cp := adj
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeBillingStore) CreateAdjustment(_ context.Context, in ports.CreateBillingAdjustmentInput) (*ports.BillingAdjustment, error) {
	if f.createAdjustErr != nil {
		err := f.createAdjustErr
		f.createAdjustErr = nil
		f.hideKeyUntilCreate = uuid.Nil
		return nil, err
	}
	adj := ports.BillingAdjustment{
		ID:             uuid.New(),
		TenantID:       in.TenantID,
		Period:         in.Period,
		AmountUSD:      in.AmountUSD,
		Reason:         in.Reason,
		Operator:       in.Operator,
		IdempotencyKey: in.IdempotencyKey,
		CreatedAt:      time.Now(),
	}
	f.adjustments = append(f.adjustments, adj)
	return &adj, nil
}

func (f *fakeBillingStore) GetCredit(_ context.Context, tenantID uuid.UUID) (*float64, error) {
	if v, ok := f.credits[tenantID]; ok {
		return &v, nil
	}
	return nil, nil
}

func (f *fakeBillingStore) ListPricing(context.Context) ([]ports.BillingPricing, error) {
	return f.pricing, nil
}

func (f *fakeBillingStore) ListBillingTenantIDs(_ context.Context, tenantFilter *uuid.UUID) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0)
	for _, id := range f.footprint {
		if tenantFilter != nil && id != *tenantFilter {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

// fakeBillingMetering 内存版平台跨租户用量客户端。
type fakeBillingMetering struct {
	usage      []ports.BillingUsageRecord
	err        error
	calls      int
	lastFilter *uuid.UUID
}

func (f *fakeBillingMetering) GetPlatformUsage(_ context.Context, _, _ time.Time, tenantFilter *uuid.UUID) ([]ports.BillingUsageRecord, error) {
	f.calls++
	f.lastFilter = tenantFilter
	if f.err != nil {
		return nil, f.err
	}
	out := make([]ports.BillingUsageRecord, 0)
	for _, u := range f.usage {
		if tenantFilter != nil && u.TenantID != *tenantFilter {
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// newBillingTestSvc 组装被测服务（tenant 存在 + 名称映射可配）。
func newBillingTestSvc(store *fakeBillingStore, meter *fakeBillingMetering, tenants *fakeTenantClient) *BillingService {
	if tenants == nil {
		tenants = &fakeTenantClient{}
	}
	return NewBillingService(store, meter, tenants)
}

var (
	billingTenantA = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	billingTenantB = uuid.MustParse("22222222-2222-2222-2222-222222222222")
)

// seedBillingPricing 方案 §6.5 种子单价（与迁移 SQL 一致）。
func seedBillingPricing() []ports.BillingPricing {
	return []ports.BillingPricing{
		{ResourceType: "instance_gpu_seconds", DisplayMetric: "gpu_hours", UnitCost: 1.2},
		{ResourceType: "instance_cpu_seconds", DisplayMetric: "cpu_hours", UnitCost: 0.05},
		{ResourceType: "instance_memory_gib_seconds", DisplayMetric: "memory_gb_hours", UnitCost: 0.02},
		{ResourceType: "token_total", DisplayMetric: "tokens", UnitCost: 0.000002},
		{ResourceType: "storage", DisplayMetric: "storage_gi", UnitCost: 0.02},
		{ResourceType: "kb", DisplayMetric: "kb_queries", UnitCost: 0.001},
	}
}

// ── GenerateInvoice ─────────────────────────────────────────────────────

func TestBillingGenerateInvoiceSuccess(t *testing.T) {
	store := newFakeBillingStore()
	store.pricing = seedBillingPricing()
	store.adjustments = []ports.BillingAdjustment{{
		ID: uuid.New(), TenantID: billingTenantA, Period: "2026-09", AmountUSD: -0.4, Reason: " goodwill",
	}}
	meter := &fakeBillingMetering{usage: []ports.BillingUsageRecord{
		{TenantID: billingTenantA, ResourceType: "instance_gpu_seconds", TotalQuantity: 7200},
	}}
	tenants := &fakeTenantClient{tenant: ports.Tenant{ID: billingTenantA, DisplayName: "acme"}}
	svc := newBillingTestSvc(store, meter, tenants)

	issuedAt := time.Now()
	res, err := svc.GenerateInvoice(context.Background(), &tenantv1.GenerateInvoiceRequest{
		TenantId: billingTenantA.String(), Period: "2026-09", IdempotencyKey: uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("GenerateInvoice: %v", err)
	}
	// 金额 = 7200s/3600 × 1.2 = 2.4 用量成本 + (-0.4) 调账 = 2.0
	if res.GetAmountUsd() != 2.0 {
		t.Fatalf("amount = %v, want 2.0", res.GetAmountUsd())
	}
	if res.GetStatus() != "issued" {
		t.Fatalf("status = %q, want issued（生成即出账）", res.GetStatus())
	}
	if res.GetNo() != "INV-2609-01" {
		t.Fatalf("no = %q, want INV-2609-01", res.GetNo())
	}
	if len(res.GetDueDate()) != 10 {
		t.Fatalf("due_date = %q, want YYYY-MM-DD", res.GetDueDate())
	}
	// 到期日 = issued_at + 30 天（date 粒度）
	wantDue := billingDueDate(issuedAt).Format("2006-01-02")
	if res.GetDueDate() != wantDue {
		t.Fatalf("due_date = %q, want %q", res.GetDueDate(), wantDue)
	}
	if store.createInvoiceCalls != 1 {
		t.Fatalf("create calls = %d, want 1", store.createInvoiceCalls)
	}
}

func TestBillingGenerateInvoiceIdempotentReplay(t *testing.T) {
	store := newFakeBillingStore()
	store.pricing = seedBillingPricing()
	meter := &fakeBillingMetering{}
	tenants := &fakeTenantClient{tenant: ports.Tenant{ID: billingTenantA}}
	svc := newBillingTestSvc(store, meter, tenants)

	key := uuid.New()
	seeded, err := store.CreateInvoice(context.Background(), ports.CreateBillingInvoiceInput{
		TenantID: billingTenantA, Period: "2026-09", No: "INV-2609-01",
		AmountUSD: 5, DueDate: billingDueDate(time.Now()), IssuedAt: time.Now(), IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("seed invoice: %v", err)
	}

	// 同幂等键重放 → 返回原账单，不新建
	res, err := svc.GenerateInvoice(context.Background(), &tenantv1.GenerateInvoiceRequest{
		TenantId: billingTenantA.String(), Period: "2026-09", IdempotencyKey: key.String(),
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.GetId() != seeded.ID.String() {
		t.Fatalf("replay id = %q, want %q", res.GetId(), seeded.ID)
	}
	if store.createInvoiceCalls != 1 {
		t.Fatalf("create calls = %d, want 1（重放不得新建）", store.createInvoiceCalls)
	}

	// 换幂等键再发 → 409 BILLING_INVOICE_EXISTS（一期一单）
	_, err = svc.GenerateInvoice(context.Background(), &tenantv1.GenerateInvoiceRequest{
		TenantId: billingTenantA.String(), Period: "2026-09", IdempotencyKey: uuid.New().String(),
	})
	requireBizCode(t, err, codes.AlreadyExists, "BILLING_INVOICE_EXISTS")
}

func TestBillingGenerateInvoiceValidation(t *testing.T) {
	store := newFakeBillingStore()
	store.pricing = seedBillingPricing()
	meter := &fakeBillingMetering{}
	svc := newBillingTestSvc(store, meter, nil)

	// 租户不存在 → 404 BILLING_TENANT_NOT_FOUND
	_, err := svc.GenerateInvoice(context.Background(), &tenantv1.GenerateInvoiceRequest{
		TenantId: billingTenantB.String(), Period: "2026-09", IdempotencyKey: uuid.New().String(),
	})
	requireBizCode(t, err, codes.NotFound, "BILLING_TENANT_NOT_FOUND")

	// period 非法 → 400 BILLING_INVALID_PERIOD
	_, err = svc.GenerateInvoice(context.Background(), &tenantv1.GenerateInvoiceRequest{
		TenantId: billingTenantA.String(), Period: "2026-13", IdempotencyKey: uuid.New().String(),
	})
	requireBizCode(t, err, codes.InvalidArgument, "BILLING_INVALID_PERIOD")

	// 幂等键缺失 → 400 VALIDATION_FAILED
	_, err = svc.GenerateInvoice(context.Background(), &tenantv1.GenerateInvoiceRequest{
		TenantId: billingTenantA.String(), Period: "2026-09",
	})
	requireBizCode(t, err, codes.InvalidArgument, "VALIDATION_FAILED")

	// tenant_id 非 UUID → 400 VALIDATION_FAILED
	_, err = svc.GenerateInvoice(context.Background(), &tenantv1.GenerateInvoiceRequest{
		TenantId: "not-a-uuid", Period: "2026-09", IdempotencyKey: uuid.New().String(),
	})
	requireBizCode(t, err, codes.InvalidArgument, "VALIDATION_FAILED")
}

func TestBillingGenerateInvoiceNoConflictRetry(t *testing.T) {
	store := newFakeBillingStore()
	store.pricing = seedBillingPricing()
	store.createInvoiceErr = ports.ErrBillingInvoiceExists // 模拟 no 唯一冲突（(tenant_id,period) 无账单）
	meter := &fakeBillingMetering{}
	tenants := &fakeTenantClient{tenant: ports.Tenant{ID: billingTenantA}}
	svc := newBillingTestSvc(store, meter, tenants)

	res, err := svc.GenerateInvoice(context.Background(), &tenantv1.GenerateInvoiceRequest{
		TenantId: billingTenantA.String(), Period: "2026-09", IdempotencyKey: uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("GenerateInvoice with no conflict: %v", err)
	}
	// 撞号后 seq 递增重试一次：首号 INV-2609-01 冲突 → INV-2609-02
	if res.GetNo() != "INV-2609-02" {
		t.Fatalf("no = %q, want INV-2609-02（撞号重试）", res.GetNo())
	}
	if store.createInvoiceCalls != 2 {
		t.Fatalf("create calls = %d, want 2", store.createInvoiceCalls)
	}
}

// ── InvoiceAction ───────────────────────────────────────────────────────

func TestBillingInvoiceActionStateMachine(t *testing.T) {
	store := newFakeBillingStore()
	meter := &fakeBillingMetering{}
	svc := newBillingTestSvc(store, meter, nil)

	seeded, err := store.CreateInvoice(context.Background(), ports.CreateBillingInvoiceInput{
		TenantID: billingTenantA, Period: "2026-09", No: "INV-2609-01",
		AmountUSD: 10, DueDate: billingDueDate(time.Now()), IssuedAt: time.Now(), IdempotencyKey: uuid.New(),
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// settle → settled
	res, err := svc.InvoiceAction(context.Background(), &tenantv1.InvoiceActionRequest{
		InvoiceId: seeded.ID.String(), Action: "settle", IdempotencyKey: uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if res.GetStatus() != "settled" || res.GetSettledAt() == nil {
		t.Fatalf("status=%q settled_at=%v, want settled + timestamp", res.GetStatus(), res.GetSettledAt())
	}

	// 终态再 settle/credit → 409 BILLING_STATE_CONFLICT
	_, err = svc.InvoiceAction(context.Background(), &tenantv1.InvoiceActionRequest{
		InvoiceId: seeded.ID.String(), Action: "settle", IdempotencyKey: uuid.New().String(),
	})
	requireBizCode(t, err, codes.FailedPrecondition, "BILLING_STATE_CONFLICT")
	_, err = svc.InvoiceAction(context.Background(), &tenantv1.InvoiceActionRequest{
		InvoiceId: seeded.ID.String(), Action: "credit", IdempotencyKey: uuid.New().String(),
	})
	requireBizCode(t, err, codes.FailedPrecondition, "BILLING_STATE_CONFLICT")

	// 新账单 credit → credited
	seeded2, err := store.CreateInvoice(context.Background(), ports.CreateBillingInvoiceInput{
		TenantID: billingTenantA, Period: "2026-08", No: "INV-2608-01",
		AmountUSD: 3, DueDate: billingDueDate(time.Now()), IssuedAt: time.Now(), IdempotencyKey: uuid.New(),
	})
	if err != nil {
		t.Fatalf("seed2: %v", err)
	}
	res, err = svc.InvoiceAction(context.Background(), &tenantv1.InvoiceActionRequest{
		InvoiceId: seeded2.ID.String(), Action: "credit", IdempotencyKey: uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("credit: %v", err)
	}
	if res.GetStatus() != "credited" || res.GetCreditedAt() == nil {
		t.Fatalf("status=%q credited_at=%v, want credited + timestamp", res.GetStatus(), res.GetCreditedAt())
	}

	// 非法 action → 400；账单不存在 → 404；幂等键缺失 → 400
	_, err = svc.InvoiceAction(context.Background(), &tenantv1.InvoiceActionRequest{
		InvoiceId: seeded2.ID.String(), Action: "void", IdempotencyKey: uuid.New().String(),
	})
	requireBizCode(t, err, codes.InvalidArgument, "BILLING_ACTION_INVALID")
	_, err = svc.InvoiceAction(context.Background(), &tenantv1.InvoiceActionRequest{
		InvoiceId: uuid.New().String(), Action: "settle", IdempotencyKey: uuid.New().String(),
	})
	requireBizCode(t, err, codes.NotFound, "BILLING_INVOICE_NOT_FOUND")
	_, err = svc.InvoiceAction(context.Background(), &tenantv1.InvoiceActionRequest{
		InvoiceId: seeded2.ID.String(), Action: "settle",
	})
	requireBizCode(t, err, codes.InvalidArgument, "VALIDATION_FAILED")
}

// ── CreateAdjustment ────────────────────────────────────────────────────

func TestBillingCreateAdjustment(t *testing.T) {
	store := newFakeBillingStore()
	meter := &fakeBillingMetering{}
	tenants := &fakeTenantClient{tenant: ports.Tenant{ID: billingTenantA}}
	svc := newBillingTestSvc(store, meter, tenants)

	// 负金额调账成功
	res, err := svc.CreateAdjustment(context.Background(), &tenantv1.CreateAdjustmentRequest{
		TenantId: billingTenantA.String(), Period: "2026-09", AmountUsd: -1.5,
		Reason: "promo credit", IdempotencyKey: uuid.New().String(), Operator: "boss-admin",
	})
	if err != nil {
		t.Fatalf("create adjustment: %v", err)
	}
	if res.GetAmountUsd() != -1.5 || res.GetOperator() != "boss-admin" {
		t.Fatalf("adjustment = %+v", res)
	}

	// 金额为 0 → 400 BILLING_AMOUNT_INVALID
	_, err = svc.CreateAdjustment(context.Background(), &tenantv1.CreateAdjustmentRequest{
		TenantId: billingTenantA.String(), Period: "2026-09", AmountUsd: 0,
		Reason: "x", IdempotencyKey: uuid.New().String(),
	})
	requireBizCode(t, err, codes.InvalidArgument, "BILLING_AMOUNT_INVALID")

	// reason 缺失 / 超长 → 400 VALIDATION_FAILED
	_, err = svc.CreateAdjustment(context.Background(), &tenantv1.CreateAdjustmentRequest{
		TenantId: billingTenantA.String(), Period: "2026-09", AmountUsd: 1,
		IdempotencyKey: uuid.New().String(),
	})
	requireBizCode(t, err, codes.InvalidArgument, "VALIDATION_FAILED")
	_, err = svc.CreateAdjustment(context.Background(), &tenantv1.CreateAdjustmentRequest{
		TenantId: billingTenantA.String(), Period: "2026-09", AmountUsd: 1,
		Reason: strings.Repeat("x", 513), IdempotencyKey: uuid.New().String(),
	})
	requireBizCode(t, err, codes.InvalidArgument, "VALIDATION_FAILED")

	// 同幂等键重放 → 返回已有记录，不新建
	key := uuid.New()
	first, err := svc.CreateAdjustment(context.Background(), &tenantv1.CreateAdjustmentRequest{
		TenantId: billingTenantA.String(), Period: "2026-09", AmountUsd: 2,
		Reason: "dup", IdempotencyKey: key.String(),
	})
	if err != nil {
		t.Fatalf("first adjustment: %v", err)
	}
	replayed, err := svc.CreateAdjustment(context.Background(), &tenantv1.CreateAdjustmentRequest{
		TenantId: billingTenantA.String(), Period: "2026-09", AmountUsd: 2,
		Reason: "dup", IdempotencyKey: key.String(),
	})
	if err != nil {
		t.Fatalf("replay adjustment: %v", err)
	}
	if replayed.GetId() != first.GetId() {
		t.Fatalf("replay id = %q, want %q", replayed.GetId(), first.GetId())
	}
	if len(store.adjustments) != 2 { // -1.5 + dup（重放不新增）
		t.Fatalf("adjustments = %d, want 2", len(store.adjustments))
	}
}

func TestBillingCreateAdjustmentIdempotencyConflictReplay(t *testing.T) {
	store := newFakeBillingStore()
	meter := &fakeBillingMetering{}
	tenants := &fakeTenantClient{tenant: ports.Tenant{ID: billingTenantA}}
	svc := newBillingTestSvc(store, meter, tenants)

	// 模拟并发：同 key 已插入但首次查询未可见；插入撞唯一约束后重查可见 → 按重放返回
	key := uuid.New()
	store.adjustments = append(store.adjustments, ports.BillingAdjustment{
		ID: uuid.New(), TenantID: billingTenantA, Period: "2026-09",
		AmountUSD: 3, Reason: "concurrent", IdempotencyKey: key,
	})
	store.hideKeyUntilCreate = key
	store.createAdjustErr = ports.ErrBillingIdempotencyConflict

	res, err := svc.CreateAdjustment(context.Background(), &tenantv1.CreateAdjustmentRequest{
		TenantId: billingTenantA.String(), Period: "2026-09", AmountUsd: 3,
		Reason: "concurrent", IdempotencyKey: key.String(),
	})
	if err != nil {
		t.Fatalf("conflict replay: %v", err)
	}
	if res.GetAmountUsd() != 3 || res.GetReason() != "concurrent" {
		t.Fatalf("replayed adjustment = %+v", res)
	}
}

// ── GetBillingOverview ──────────────────────────────────────────────────

func TestBillingGetBillingOverviewEmpty(t *testing.T) {
	svc := newBillingTestSvc(newFakeBillingStore(), &fakeBillingMetering{}, nil)
	res, err := svc.GetBillingOverview(context.Background(), &tenantv1.GetBillingOverviewRequest{Period: "2026-09"})
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if res.GetTotal() != 0 || len(res.GetItems()) != 0 {
		t.Fatalf("total=%d items=%d, want empty（无足迹且无用量 → 空态）", res.GetTotal(), len(res.GetItems()))
	}
}

func TestBillingGetBillingOverviewUsageOnlyTenant(t *testing.T) {
	store := newFakeBillingStore()
	store.pricing = seedBillingPricing()
	meter := &fakeBillingMetering{usage: []ports.BillingUsageRecord{
		{TenantID: billingTenantA, ResourceType: "instance_gpu_seconds", TotalQuantity: 7200},
	}}
	tenants := &fakeTenantClient{available: []ports.BoundTenant{{ID: billingTenantA, DisplayName: "acme"}}}
	svc := newBillingTestSvc(store, meter, tenants)

	res, err := svc.GetBillingOverview(context.Background(), &tenantv1.GetBillingOverviewRequest{Period: "2026-09"})
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if res.GetTotal() != 1 || len(res.GetItems()) != 1 {
		t.Fatalf("total=%d, want 1", res.GetTotal())
	}
	item := res.GetItems()[0]
	if item.GetTenantName() != "acme" || item.GetStatus() != "current" {
		t.Fatalf("name=%q status=%q, want acme/current", item.GetTenantName(), item.GetStatus())
	}
	if item.UsageCostUsd == nil || *item.UsageCostUsd != 2.4 {
		t.Fatalf("usage_cost = %v, want 2.4", item.UsageCostUsd)
	}
	if item.CreditUsd != nil || item.BalanceUsd != nil {
		t.Fatalf("credit/balance should be null without credit account")
	}
	if item.InvoiceNo != nil || item.DueDate != nil {
		t.Fatalf("invoice_no/due_date should be null without invoice")
	}
	// breakdown：gpu metered；tokens/storage/kb unavailable（本地无采集 → 过渡态）
	bd := breakdownByMetric(item.GetUsageBreakdown())
	gpu := bd["gpu_hours"]
	if gpu.GetDataSource() != "metered" || gpu.GetAmount() != 2 || gpu.GetCost() != 2.4 {
		t.Fatalf("gpu_hours row = %+v", gpu)
	}
	for _, metric := range []string{"tokens", "storage_gi", "kb_queries"} {
		row := bd[metric]
		if row.GetDataSource() != "unavailable" || row.Amount != nil || row.Cost != nil {
			t.Fatalf("%s row = %+v, want unavailable + null amount/cost", metric, row)
		}
	}
	if len(item.GetInvoices()) != 0 || len(item.GetAdjustments()) != 0 {
		t.Fatalf("sub arrays should be empty")
	}
}

func TestBillingGetBillingOverviewOverdueAndBalance(t *testing.T) {
	store := newFakeBillingStore()
	store.pricing = seedBillingPricing()
	meter := &fakeBillingMetering{}
	svc := newBillingTestSvc(store, meter, nil)

	// 账单 issued 且到期日已过 → 行状态 overdue（动态计算，落库仍 issued）
	now := time.Now()
	past := now.AddDate(0, 0, -40)
	if _, err := store.CreateInvoice(context.Background(), ports.CreateBillingInvoiceInput{
		TenantID: billingTenantA, Period: "2026-09", No: "INV-2609-01", AmountUSD: 10,
		DueDate: past.AddDate(0, 0, 30), IssuedAt: past, IdempotencyKey: uuid.New(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	store.credits[billingTenantA] = 100
	store.footprint = []uuid.UUID{billingTenantA}
	// 当期调账（已出账 → 不重复扣）+ 上期调账（未出账 → 生效）
	store.adjustments = []ports.BillingAdjustment{
		{ID: uuid.New(), TenantID: billingTenantA, Period: "2026-09", AmountUSD: 1},
		{ID: uuid.New(), TenantID: billingTenantA, Period: "2026-08", AmountUSD: -5},
	}

	res, err := svc.GetBillingOverview(context.Background(), &tenantv1.GetBillingOverviewRequest{Period: "2026-09"})
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if res.GetTotal() != 1 {
		t.Fatalf("total=%d, want 1（足迹租户）", res.GetTotal())
	}
	item := res.GetItems()[0]
	if item.GetStatus() != "overdue" {
		t.Fatalf("status = %q, want overdue", item.GetStatus())
	}
	if item.InvoiceNo == nil || *item.InvoiceNo != "INV-2609-01" {
		t.Fatalf("invoice_no = %v, want INV-2609-01", item.InvoiceNo)
	}
	// 余额 = 100 − 10（未清账单）+（当期调账 +1 已出账不重复扣）− 5（上期未出账调账）= 85
	if item.BalanceUsd == nil || *item.BalanceUsd != 85 {
		t.Fatalf("balance = %v, want 85", item.BalanceUsd)
	}
	if item.UsageCostUsd != nil {
		t.Fatalf("usage_cost should be null（无用量）")
	}
	if len(item.GetInvoices()) != 1 || item.GetInvoices()[0].GetStatus() != "issued" {
		t.Fatalf("invoices = %+v, want issued stored", item.GetInvoices())
	}
	if len(item.GetAdjustments()) != 1 {
		t.Fatalf("period adjustments = %d, want 1（仅当期）", len(item.GetAdjustments()))
	}
}

func TestBillingGetBillingOverviewStatusFilter(t *testing.T) {
	store := newFakeBillingStore()
	store.pricing = seedBillingPricing()
	now := time.Now()
	past := now.AddDate(0, 0, -40)

	// A：issued 且到期已过 → overdue；B：settle 终态 → settled
	if _, err := store.CreateInvoice(context.Background(), ports.CreateBillingInvoiceInput{
		TenantID: billingTenantA, Period: "2026-09", No: "INV-2609-01", AmountUSD: 10,
		DueDate: past.AddDate(0, 0, 30), IssuedAt: past, IdempotencyKey: uuid.New(),
	}); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	invB, err := store.CreateInvoice(context.Background(), ports.CreateBillingInvoiceInput{
		TenantID: billingTenantB, Period: "2026-09", No: "INV-2609-02", AmountUSD: 4,
		DueDate: billingDueDate(now), IssuedAt: now, IdempotencyKey: uuid.New(),
	})
	if err != nil {
		t.Fatalf("seed B: %v", err)
	}
	if _, err := store.UpdateInvoiceStatus(context.Background(), invB.ID, ports.BillingActionSettle, now); err != nil {
		t.Fatalf("settle B: %v", err)
	}
	store.footprint = []uuid.UUID{billingTenantA, billingTenantB}
	meter := &fakeBillingMetering{}
	svc := newBillingTestSvc(store, meter, nil)

	overdue, err := svc.GetBillingOverview(context.Background(), &tenantv1.GetBillingOverviewRequest{Period: "2026-09", Status: "overdue"})
	if err != nil {
		t.Fatalf("overdue filter: %v", err)
	}
	if overdue.GetTotal() != 1 || overdue.GetItems()[0].GetTenantId() != billingTenantA.String() {
		t.Fatalf("overdue rows = %d, want A only", overdue.GetTotal())
	}
	settled, err := svc.GetBillingOverview(context.Background(), &tenantv1.GetBillingOverviewRequest{Period: "2026-09", Status: "settled"})
	if err != nil {
		t.Fatalf("settled filter: %v", err)
	}
	if settled.GetTotal() != 1 || settled.GetItems()[0].GetTenantId() != billingTenantB.String() {
		t.Fatalf("settled rows = %d, want B only", settled.GetTotal())
	}
	// 非法 status → 400
	_, err = svc.GetBillingOverview(context.Background(), &tenantv1.GetBillingOverviewRequest{Period: "2026-09", Status: "bogus"})
	requireBizCode(t, err, codes.InvalidArgument, "VALIDATION_FAILED")
}

func TestBillingGetBillingOverviewTenantFilterAndSort(t *testing.T) {
	store := newFakeBillingStore()
	store.footprint = []uuid.UUID{billingTenantB, billingTenantA} // 故意倒序
	meter := &fakeBillingMetering{}
	svc := newBillingTestSvc(store, meter, nil)

	// tenant_id 过滤 → 单租户钻取（metering 侧同步过滤）
	res, err := svc.GetBillingOverview(context.Background(), &tenantv1.GetBillingOverviewRequest{
		Period: "2026-09", TenantId: billingTenantA.String(),
	})
	if err != nil {
		t.Fatalf("tenant filter: %v", err)
	}
	if res.GetTotal() != 1 || res.GetItems()[0].GetTenantId() != billingTenantA.String() {
		t.Fatalf("tenant filter rows = %d, want A only", res.GetTotal())
	}
	if meter.lastFilter == nil || *meter.lastFilter != billingTenantA {
		t.Fatalf("metering filter = %v, want tenant A", meter.lastFilter)
	}

	// 无过滤 → 全量且按 tenant_id 排序（稳定输出）
	all, err := svc.GetBillingOverview(context.Background(), &tenantv1.GetBillingOverviewRequest{Period: "2026-09"})
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if all.GetTotal() != 2 {
		t.Fatalf("total = %d, want 2", all.GetTotal())
	}
	if all.GetItems()[0].GetTenantId() >= all.GetItems()[1].GetTenantId() {
		t.Fatalf("items not sorted by tenant_id")
	}
	// 名称映射缺失 → 回退 tenant_id 字符串
	if all.GetItems()[0].GetTenantName() != billingTenantA.String() {
		t.Fatalf("tenant_name fallback = %q, want tenant_id", all.GetItems()[0].GetTenantName())
	}

	// tenant_id 非 UUID → 400
	_, err = svc.GetBillingOverview(context.Background(), &tenantv1.GetBillingOverviewRequest{Period: "2026-09", TenantId: "bad"})
	requireBizCode(t, err, codes.InvalidArgument, "VALIDATION_FAILED")
}

func TestBillingGetBillingOverviewPeriodValidationAndCoreDown(t *testing.T) {
	svc := newBillingTestSvc(newFakeBillingStore(), &fakeBillingMetering{}, nil)

	// period 非法 → 400
	_, err := svc.GetBillingOverview(context.Background(), &tenantv1.GetBillingOverviewRequest{Period: "2026-9"})
	requireBizCode(t, err, codes.InvalidArgument, "BILLING_INVALID_PERIOD")

	// Core 计量不可用 → Unavailable GRPC_CLIENT_UNAVAILABLE（不静默出 0）
	svcDown := newBillingTestSvc(newFakeBillingStore(), &fakeBillingMetering{err: ports.ErrCoreUnavailable}, nil)
	_, err = svcDown.GetBillingOverview(context.Background(), &tenantv1.GetBillingOverviewRequest{Period: "2026-09"})
	requireBizCode(t, err, codes.Unavailable, "GRPC_CLIENT_UNAVAILABLE")
}

// ── ExportBillingOverview ───────────────────────────────────────────────

func TestBillingExportOverviewCSV(t *testing.T) {
	store := newFakeBillingStore()
	store.pricing = seedBillingPricing()
	meter := &fakeBillingMetering{usage: []ports.BillingUsageRecord{
		{TenantID: billingTenantA, ResourceType: "instance_gpu_seconds", TotalQuantity: 7200},
	}}
	tenants := &fakeTenantClient{available: []ports.BoundTenant{{ID: billingTenantA, DisplayName: "acme"}}}
	svc := newBillingTestSvc(store, meter, tenants)

	res, err := svc.ExportBillingOverview(context.Background(), &tenantv1.ExportBillingOverviewRequest{Period: "2026-09"})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if res.GetFilename() != "billing-overview-2026-09.csv" {
		t.Fatalf("filename = %q", res.GetFilename())
	}
	for _, want := range []string{
		"tenant_id,tenant_name,period,status,usage_cost_usd,credit_usd,balance_usd,invoice_no,due_date,metric,usage_amount,unit_cost,cost,data_source",
		billingTenantA.String(),
		"acme",
		"gpu_hours",
	} {
		if !strings.Contains(res.GetCsv(), want) {
			t.Fatalf("csv missing %q:\n%s", want, res.GetCsv())
		}
	}
}

// breakdownByMetric 按展示指标索引 breakdown 行。
func breakdownByMetric(rows []*tenantv1.BillingUsageBreakdown) map[string]*tenantv1.BillingUsageBreakdown {
	out := make(map[string]*tenantv1.BillingUsageBreakdown, len(rows))
	for _, r := range rows {
		out[r.GetMetric()] = r
	}
	return out
}
