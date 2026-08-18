package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// 配额套餐（tenant plan）service 层单测合集：create/list/get/update/state/quota/bind/audit。

// ---- from tenant_plan_service_create_test.go ----

// ── fakes ───────────────────────────────────────────────────────────────────

type fakePlanStore struct {
	createFn    func(ctx context.Context, in ports.CreateTenantPlanInput) (ports.TenantPlan, error)
	created     []ports.CreateTenantPlanInput
	createCalls int
	plan        ports.TenantPlan // 非空 ID 时 GetByID 可用（issue-010 等）
}

func (f *fakePlanStore) Create(ctx context.Context, in ports.CreateTenantPlanInput) (ports.TenantPlan, error) {
	f.createCalls++
	f.created = append(f.created, in)
	if f.createFn != nil {
		return f.createFn(ctx, in)
	}
	return ports.TenantPlan{
		ID:        uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Code:      in.Code,
		Name:      in.Name,
		Status:    ports.TenantPlanStatusDraft,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}, nil
}

func (f *fakePlanStore) GetByID(_ context.Context, id uuid.UUID) (ports.TenantPlan, error) {
	if f.plan.ID == uuid.Nil {
		panic("unused")
	}
	if f.plan.ID != id {
		return ports.TenantPlan{}, ports.ErrTenantPlanNotFound
	}
	return f.plan, nil
}
func (f *fakePlanStore) List(context.Context, ports.TenantPlanListFilter) (ports.TenantPlanListResult, error) {
	panic("unused")
}
func (f *fakePlanStore) Update(context.Context, uuid.UUID, ports.UpdateTenantPlanInput) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *fakePlanStore) Activate(context.Context, uuid.UUID) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *fakePlanStore) Disable(context.Context, uuid.UUID) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *fakePlanStore) Delete(context.Context, uuid.UUID) error { panic("unused") }
func (f *fakePlanStore) GetQuotaLimits(context.Context, uuid.UUID) ([]ports.PlanQuotaLimit, error) {
	panic("unused")
}
func (f *fakePlanStore) UpdateQuotaLimits(context.Context, uuid.UUID, []ports.PlanQuotaLimitInput) error {
	panic("unused")
}
func (f *fakePlanStore) GetApprovedQuotaChanges(context.Context, uuid.UUID) ([]ports.ApprovedQuotaChange, error) {
	panic("unused")
}

type fakeAuditStore struct {
	createFn func(ctx context.Context, log ports.AuditLog) (uuid.UUID, error)
	logs     []ports.AuditLog
}

func (f *fakeAuditStore) Create(ctx context.Context, log ports.AuditLog) (uuid.UUID, error) {
	f.logs = append(f.logs, log)
	if f.createFn != nil {
		return f.createFn(ctx, log)
	}
	return uuid.New(), nil
}

func (f *fakeAuditStore) ListPlanAuditLogs(_ context.Context, planID uuid.UUID, filter ports.AuditLogFilter) (ports.AuditLogListResult, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	matched := make([]ports.AuditLog, 0, len(f.logs))
	for _, log := range f.logs {
		pid, _ := log.Details["plan_id"].(string)
		if pid != planID.String() {
			continue
		}
		matched = append(matched, log)
	}

	// 简单倒序：后写入的在前（单测 Create 顺序）
	for i, j := 0, len(matched)-1; i < j; i, j = i+1, j-1 {
		matched[i], matched[j] = matched[j], matched[i]
	}

	total := len(matched)
	if len(matched) > limit {
		matched = matched[:limit]
	}
	return ports.AuditLogListResult{Items: matched, Total: total, NextCursor: ""}, nil
}

type fakeQuotaClient struct {
	meta         []ports.QuotaMeta
	metaErr      error
	calls        int
	putCalls     int
	putTenant    uuid.UUID
	putItems     []ports.CoreQuotaItem
	putFn        func(ctx context.Context, tenantID uuid.UUID, items []ports.CoreQuotaItem) ([]ports.CoreQuotaResult, error)
	putTightened bool
	createCalls  int
	createItems  []ports.CoreQuotaItem
	createFn     func(ctx context.Context, tenantID uuid.UUID, items []ports.CoreQuotaItem) ([]ports.CoreQuotaResult, error)
	// existing 模拟租户已有配额维度（GetQuota 返回）
	existing map[uuid.UUID][]ports.CoreQuotaResult
	getFn    func(ctx context.Context, tenantID uuid.UUID) ([]ports.CoreQuotaResult, error)
}

type fakeTenantClient struct {
	tenant      ports.Tenant
	updateCalls int
	updatedPlan uuid.UUID
	updateErr   error
	counts      map[uuid.UUID]int64
	countErr    error
	bound       []ports.BoundTenant
	bindable    []ports.BoundTenant
}

func (f *fakeQuotaClient) ListQuotaMeta(context.Context) ([]ports.QuotaMeta, error) {
	f.calls++
	if f.metaErr != nil {
		return nil, f.metaErr
	}
	return f.meta, nil
}

func (f *fakeQuotaClient) PutQuota(ctx context.Context, tenantID uuid.UUID, items []ports.CoreQuotaItem) ([]ports.CoreQuotaResult, error) {
	f.putCalls++
	f.putTenant = tenantID
	f.putItems = append([]ports.CoreQuotaItem(nil), items...)
	if f.putFn != nil {
		return f.putFn(ctx, tenantID, items)
	}
	out := make([]ports.CoreQuotaResult, 0, len(items))
	for _, it := range items {
		out = append(out, ports.CoreQuotaResult{
			ResourceType: it.ResourceType,
			Total:        it.Total,
			Tightened:    f.putTightened,
		})
	}
	return out, nil
}

func (f *fakeQuotaClient) GetQuota(ctx context.Context, tenantID uuid.UUID) ([]ports.CoreQuotaResult, error) {
	if f.getFn != nil {
		return f.getFn(ctx, tenantID)
	}
	return append([]ports.CoreQuotaResult(nil), f.existing[tenantID]...), nil
}

func (f *fakeQuotaClient) CreateQuota(ctx context.Context, tenantID uuid.UUID, items []ports.CoreQuotaItem) ([]ports.CoreQuotaResult, error) {
	f.createCalls++
	f.createItems = append([]ports.CoreQuotaItem(nil), items...)
	if f.createFn != nil {
		return f.createFn(ctx, tenantID, items)
	}
	out := make([]ports.CoreQuotaResult, 0, len(items))
	for _, it := range items {
		out = append(out, ports.CoreQuotaResult{
			ResourceType: it.ResourceType,
			Total:        it.Total,
		})
	}
	return out, nil
}

func (f *fakeQuotaClient) DeleteQuota(context.Context, uuid.UUID) error {
	panic("unused")
}

func (f *fakeTenantClient) GetTenant(_ context.Context, id uuid.UUID) (ports.Tenant, error) {
	if f.tenant.ID == uuid.Nil || f.tenant.ID != id {
		return ports.Tenant{}, ports.ErrTenantNotFound
	}
	return f.tenant, nil
}

func (f *fakeTenantClient) UpdateTenantPlan(_ context.Context, id uuid.UUID, planID uuid.UUID) (ports.Tenant, error) {
	f.updateCalls++
	if f.tenant.ID == uuid.Nil || f.tenant.ID != id {
		return ports.Tenant{}, ports.ErrTenantNotFound
	}
	if f.updateErr != nil {
		return ports.Tenant{}, f.updateErr
	}
	f.updatedPlan = planID
	f.tenant.PlanID = planID
	return f.tenant, nil
}

func (f *fakeTenantClient) CountBoundTenants(_ context.Context, planIDs []uuid.UUID) (map[uuid.UUID]int64, error) {
	if f.countErr != nil {
		return nil, f.countErr
	}
	out := make(map[uuid.UUID]int64, len(planIDs))
	for _, id := range planIDs {
		out[id] = f.counts[id]
	}
	return out, nil
}

func (f *fakeTenantClient) ListBoundTenants(context.Context, uuid.UUID) ([]ports.BoundTenant, error) {
	return append([]ports.BoundTenant(nil), f.bound...), nil
}

func (f *fakeTenantClient) ListBindableTenants(context.Context, uuid.UUID) ([]ports.BoundTenant, error) {
	return append([]ports.BoundTenant(nil), f.bindable...), nil
}

func newCreateSvc(plans *fakePlanStore, audit *fakeAuditStore, core *fakeQuotaClient) *TenantPlanService {
	if plans == nil {
		plans = &fakePlanStore{}
	}
	if audit == nil {
		audit = &fakeAuditStore{}
	}
	if core == nil {
		core = &fakeQuotaClient{meta: []ports.QuotaMeta{
			{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4},
			{ResourceType: "cpu_core", Enabled: true, DefaultQuota: 32},
		}}
	}
	return NewTenantPlanService(plans, audit, core, &fakeTenantClient{})
}

// ── tests ───────────────────────────────────────────────────────────────────

func TestTenantPlanService_Create(t *testing.T) {
	t.Parallel()

	plans := &fakePlanStore{}
	audit := &fakeAuditStore{}
	core := &fakeQuotaClient{meta: []ports.QuotaMeta{
		{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4},
		{ResourceType: "cpu_core", Enabled: true, DefaultQuota: 32},
	}}
	svc := newCreateSvc(plans, audit, core)

	total := int64(8)
	res, err := svc.CreateTenantPlan(context.Background(), &tenantv1.CreateTenantPlanRequest{
		Code:        "pro-plan",
		Name:        "专业版",
		Description: "desc",
		QuotaLimits: []*tenantv1.PlanQuotaLimitInput{
			{ResourceType: "gpu_count", Total: wrapperspb.Int64(total)},
			{ResourceType: "cpu_core"}, // nil total → 填 meta.default_quota
		},
		IdempotencyKey: "k1",
	})
	if err != nil {
		t.Fatalf("CreateTenantPlan: %v", err)
	}
	if res.GetId() == "" || res.GetMessage() != "tenant plan created" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if plans.createCalls != 1 {
		t.Fatalf("expected 1 create call, got %d", plans.createCalls)
	}
	if len(plans.created[0].QuotaLimits) != 2 {
		t.Fatalf("expected 2 quota limits persisted, got %d", len(plans.created[0].QuotaLimits))
	}
	if plans.created[0].QuotaLimits[0].Total == nil || *plans.created[0].QuotaLimits[0].Total != 8 {
		t.Fatalf("gpu total not mapped: %+v", plans.created[0].QuotaLimits[0])
	}
	if plans.created[0].QuotaLimits[1].Total == nil || *plans.created[0].QuotaLimits[1].Total != 32 {
		t.Fatalf("cpu total should fallback to default_quota=32, got %+v", plans.created[0].QuotaLimits[1])
	}
	if len(audit.logs) != 1 {
		t.Fatalf("expected audit write, got %d", len(audit.logs))
	}
	if audit.logs[0].Action != "tenant_plan.create" || audit.logs[0].Resource != "tenant_plan" || audit.logs[0].Result != "success" {
		t.Fatalf("unexpected audit: %+v", audit.logs[0])
	}
	if audit.logs[0].Details["code"] != "pro-plan" {
		t.Fatalf("audit missing code: %+v", audit.logs[0].Details)
	}

	// 带网关 x-request-id / x-user-id 时写入审计 RequestID / UserID
	plans2 := &fakePlanStore{}
	audit2 := &fakeAuditStore{}
	svc2 := newCreateSvc(plans2, audit2, core)
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	mdCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"x-request-id", "req_test-create-1",
		"x-user-id", userID.String(),
	))
	_, err = svc2.CreateTenantPlan(mdCtx, &tenantv1.CreateTenantPlanRequest{
		Code: "pro-plan-2",
		Name: "专业版2",
	})
	if err != nil {
		t.Fatalf("CreateTenantPlan with request_id: %v", err)
	}
	if len(audit2.logs) != 1 || audit2.logs[0].RequestID != "req_test-create-1" {
		t.Fatalf("expected audit RequestID from gateway metadata, got %+v", audit2.logs)
	}
	if audit2.logs[0].UserID == nil || *audit2.logs[0].UserID != userID {
		t.Fatalf("expected audit UserID from gateway metadata, got %+v", audit2.logs[0].UserID)
	}

	// code 格式 ^[a-z0-9-]{3,40}$；空 / 非法格式均拒绝
	_, err = svc.CreateTenantPlan(context.Background(), &tenantv1.CreateTenantPlanRequest{
		Code: "AB",
		Name: "x",
	})
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(status.Convert(err).Message(), "code must match") {
		t.Fatalf("expected VALIDATION_FAILED for invalid code format, got %v", err)
	}
	_, err = svc.CreateTenantPlan(context.Background(), &tenantv1.CreateTenantPlanRequest{
		Code: "Bad_Code",
		Name: "x",
	})
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(status.Convert(err).Message(), "code must match") {
		t.Fatalf("expected VALIDATION_FAILED for uppercase/underscore code, got %v", err)
	}
	_, err = svc.CreateTenantPlan(context.Background(), &tenantv1.CreateTenantPlanRequest{
		Code: "   ",
		Name: "x",
	})
	if status.Code(err) != codes.InvalidArgument || !strings.HasPrefix(status.Convert(err).Message(), "VALIDATION_FAILED") {
		t.Fatalf("expected VALIDATION_FAILED for empty code, got %v", err)
	}

	// name 超长
	longName := strings.Repeat("名", 65)
	_, err = svc.CreateTenantPlan(context.Background(), &tenantv1.CreateTenantPlanRequest{
		Code: "ok-code",
		Name: longName,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for long name, got %v", err)
	}

	// 重复 resource_type
	_, err = svc.CreateTenantPlan(context.Background(), &tenantv1.CreateTenantPlanRequest{
		Code: "ok-code",
		Name: "n",
		QuotaLimits: []*tenantv1.PlanQuotaLimitInput{
			{ResourceType: "gpu_count"},
			{ResourceType: "gpu_count"},
		},
	})
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(status.Convert(err).Message(), "duplicate") {
		t.Fatalf("expected duplicate validation, got %v", err)
	}

	// total < 0
	_, err = svc.CreateTenantPlan(context.Background(), &tenantv1.CreateTenantPlanRequest{
		Code: "ok-code",
		Name: "n",
		QuotaLimits: []*tenantv1.PlanQuotaLimitInput{
			{ResourceType: "gpu_count", Total: wrapperspb.Int64(-1)},
		},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for negative total, got %v", err)
	}

	// 无 quota_limits 时不调用 Core
	core.calls = 0
	_, err = svc.CreateTenantPlan(context.Background(), &tenantv1.CreateTenantPlanRequest{
		Code: "lite",
		Name: "轻量",
	})
	if err != nil {
		t.Fatalf("create without limits: %v", err)
	}
	if core.calls != 0 {
		t.Fatalf("ListQuotaMeta should be skipped when quota_limits empty, calls=%d", core.calls)
	}
}

func TestTenantPlanService_Create_DuplicateCode(t *testing.T) {
	t.Parallel()

	plans := &fakePlanStore{
		createFn: func(context.Context, ports.CreateTenantPlanInput) (ports.TenantPlan, error) {
			return ports.TenantPlan{}, ports.ErrPlanCodeConflict
		},
	}
	audit := &fakeAuditStore{}
	svc := newCreateSvc(plans, audit, nil)

	_, err := svc.CreateTenantPlan(context.Background(), &tenantv1.CreateTenantPlanRequest{
		Code: "starter",
		Name: "入门",
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists, got %v", err)
	}
	msg := status.Convert(err).Message()
	if !strings.HasPrefix(msg, "PLAN_CODE_CONFLICT") {
		t.Fatalf("expected PLAN_CODE_CONFLICT prefix, got %q", msg)
	}
	if len(audit.logs) != 1 {
		t.Fatalf("expected failure audit write, got %d", len(audit.logs))
	}
	if audit.logs[0].Result != "failure" || audit.logs[0].Action != "tenant_plan.create" {
		t.Fatalf("unexpected audit: %+v", audit.logs[0])
	}
	if reason, _ := audit.logs[0].Details["reason"].(string); !strings.HasPrefix(reason, "PLAN_CODE_CONFLICT") {
		t.Fatalf("failure audit missing reason: %+v", audit.logs[0].Details)
	}
}

func Test_Create_QuotaResourceNotRegistered(t *testing.T) {
	t.Parallel()

	plans := &fakePlanStore{}
	audit := &fakeAuditStore{}
	core := &fakeQuotaClient{meta: []ports.QuotaMeta{
		{ResourceType: "gpu_count", Enabled: true},
	}}
	svc := newCreateSvc(plans, audit, core)

	_, err := svc.CreateTenantPlan(context.Background(), &tenantv1.CreateTenantPlanRequest{
		Code: "pro",
		Name: "专业版",
		QuotaLimits: []*tenantv1.PlanQuotaLimitInput{
			{ResourceType: "not_a_real_dim", Total: wrapperspb.Int64(1)},
		},
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v", err)
	}
	if !strings.HasPrefix(status.Convert(err).Message(), "QUOTA_RESOURCE_NOT_REGISTERED") {
		t.Fatalf("unexpected message: %v", err)
	}
	if plans.createCalls != 0 {
		t.Fatalf("store must not run when validation fails")
	}
	if len(audit.logs) != 1 || audit.logs[0].Result != "failure" {
		t.Fatalf("expected failure audit, got %+v", audit.logs)
	}
}

func TestTenantPlanService_Create_CoreUnavailable(t *testing.T) {
	t.Parallel()

	audit := &fakeAuditStore{}
	svc := newCreateSvc(&fakePlanStore{}, audit, &fakeQuotaClient{
		metaErr: ports.ErrCoreUnavailable,
	})
	_, err := svc.CreateTenantPlan(context.Background(), &tenantv1.CreateTenantPlanRequest{
		Code: "pro",
		Name: "专业版",
		QuotaLimits: []*tenantv1.PlanQuotaLimitInput{
			{ResourceType: "gpu_count"},
		},
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable, got %v", err)
	}
	if !strings.HasPrefix(status.Convert(err).Message(), "GRPC_CLIENT_UNAVAILABLE") {
		t.Fatalf("unexpected message: %v", err)
	}
	if len(audit.logs) != 1 || audit.logs[0].Result != "failure" {
		t.Fatalf("expected failure audit, got %+v", audit.logs)
	}
}

func TestTenantPlanService_Create_AuditWriteError(t *testing.T) {
	t.Parallel()

	// 业务已成功后审计写失败：不阻断（套餐已创建，审计为事后记录）。
	plans := &fakePlanStore{}
	audit := &fakeAuditStore{
		createFn: func(context.Context, ports.AuditLog) (uuid.UUID, error) {
			return uuid.Nil, errors.New("db down")
		},
	}
	svc := NewTenantPlanService(plans, audit, &fakeQuotaClient{meta: nil}, &fakeTenantClient{})

	res, err := svc.CreateTenantPlan(context.Background(), &tenantv1.CreateTenantPlanRequest{
		Code: "ok-plan",
		Name: "ok",
	})
	if err != nil {
		t.Fatalf("audit failure must not fail create: %v", err)
	}
	if res.GetMessage() != "tenant plan created" {
		t.Fatalf("res=%+v", res)
	}
	if plans.createCalls != 1 {
		t.Fatalf("expected plan create once, got %d", plans.createCalls)
	}
}

// ---- from tenant_plan_service_list_test.go ----

type listFakePlanStore struct {
	listFn     func(ctx context.Context, filter ports.TenantPlanListFilter) (ports.TenantPlanListResult, error)
	getByIDFn  func(ctx context.Context, id uuid.UUID) (ports.TenantPlan, error)
	lastFilter ports.TenantPlanListFilter
}

func (f *listFakePlanStore) Create(context.Context, ports.CreateTenantPlanInput) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *listFakePlanStore) GetByID(ctx context.Context, id uuid.UUID) (ports.TenantPlan, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	panic("unused")
}
func (f *listFakePlanStore) List(ctx context.Context, filter ports.TenantPlanListFilter) (ports.TenantPlanListResult, error) {
	f.lastFilter = filter
	if f.listFn != nil {
		return f.listFn(ctx, filter)
	}
	return ports.TenantPlanListResult{}, nil
}
func (f *listFakePlanStore) Update(context.Context, uuid.UUID, ports.UpdateTenantPlanInput) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *listFakePlanStore) Activate(context.Context, uuid.UUID) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *listFakePlanStore) Disable(context.Context, uuid.UUID) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *listFakePlanStore) Delete(context.Context, uuid.UUID) error { panic("unused") }
func (f *listFakePlanStore) GetQuotaLimits(context.Context, uuid.UUID) ([]ports.PlanQuotaLimit, error) {
	panic("unused")
}
func (f *listFakePlanStore) UpdateQuotaLimits(context.Context, uuid.UUID, []ports.PlanQuotaLimitInput) error {
	panic("unused")
}
func (f *listFakePlanStore) GetApprovedQuotaChanges(context.Context, uuid.UUID) ([]ports.ApprovedQuotaChange, error) {
	panic("unused")
}

func TestTenantPlanService_List(t *testing.T) {
	now := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	id1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	id2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	plans := &listFakePlanStore{
		listFn: func(_ context.Context, filter ports.TenantPlanListFilter) (ports.TenantPlanListResult, error) {
			if filter.Limit != 2 {
				t.Fatalf("limit = %d, want 2", filter.Limit)
			}
			if filter.Status != ports.TenantPlanStatusActive {
				t.Fatalf("status = %q, want active", filter.Status)
			}
			if filter.Search != "专业" {
				t.Fatalf("search = %q, want 专业", filter.Search)
			}
			return ports.TenantPlanListResult{
				Items: []ports.TenantPlanListItem{
					{ID: id1, Code: "pro", Name: "专业版", Status: ports.TenantPlanStatusActive, TenantCount: 12, CreatedAt: now, UpdatedAt: now},
					{ID: id2, Code: "lite", Name: "轻量", Status: ports.TenantPlanStatusActive, TenantCount: 3, CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
				},
				Total:      5,
				NextCursor: "next-page",
			}, nil
		},
	}
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, &fakeQuotaClient{}, &fakeTenantClient{
		counts: map[uuid.UUID]int64{id1: 12, id2: 3},
	})

	res, err := svc.ListTenantPlans(context.Background(), &tenantv1.ListTenantPlansRequest{
		Status: string(ports.TenantPlanStatusActive),
		Search: "专业",
		Page:   &commonv1.CursorPageRequest{Limit: 2, Cursor: ""},
	})
	if err != nil {
		t.Fatalf("ListTenantPlans: %v", err)
	}
	if res.GetTotal() != 5 || res.GetNextCursor() != "next-page" {
		t.Fatalf("total/cursor = %d/%q", res.GetTotal(), res.GetNextCursor())
	}
	if len(res.GetItems()) != 2 {
		t.Fatalf("items len = %d", len(res.GetItems()))
	}
	if res.Items[0].GetTenantCount() != 12 || res.Items[0].GetCode() != "pro" {
		t.Fatalf("first item = %+v", res.Items[0])
	}
	// 列表项不应依赖 quota_limits 字段（proto 本身无该字段；此处确认 description 可空）
	if res.Items[0].GetId() != id1.String() {
		t.Fatalf("id = %s", res.Items[0].GetId())
	}
}

func TestTenantPlanService_List_InvalidStatus(t *testing.T) {
	svc := NewTenantPlanService(&listFakePlanStore{}, &fakeAuditStore{}, &fakeQuotaClient{}, &fakeTenantClient{})
	_, err := svc.ListTenantPlans(context.Background(), &tenantv1.ListTenantPlansRequest{Status: "deleted"})
	if status.Code(err) != codes.InvalidArgument || !strings.HasPrefix(status.Convert(err).Message(), "VALIDATION_FAILED") {
		t.Fatalf("expected VALIDATION_FAILED, got %v", err)
	}
}

func TestTenantPlanService_List_DefaultLimit(t *testing.T) {
	plans := &listFakePlanStore{
		listFn: func(_ context.Context, filter ports.TenantPlanListFilter) (ports.TenantPlanListResult, error) {
			return ports.TenantPlanListResult{Items: nil, Total: 0}, nil
		},
	}
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, &fakeQuotaClient{}, &fakeTenantClient{})
	_, err := svc.ListTenantPlans(context.Background(), &tenantv1.ListTenantPlansRequest{})
	if err != nil {
		t.Fatalf("ListTenantPlans: %v", err)
	}
	if plans.lastFilter.Limit != 20 {
		t.Fatalf("default limit = %d, want 20", plans.lastFilter.Limit)
	}
}

func TestTenantPlanService_Get(t *testing.T) {
	now := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	plans := &listFakePlanStore{
		getByIDFn: func(_ context.Context, got uuid.UUID) (ports.TenantPlan, error) {
			if got != id {
				t.Fatalf("id = %s", got)
			}
			return ports.TenantPlan{
				ID: id, Code: "pro", Name: "专业版", Description: "含GPU",
				Status: ports.TenantPlanStatusActive, TenantCount: 12, CreatedAt: now, UpdatedAt: now,
			}, nil
		},
	}
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, &fakeQuotaClient{}, &fakeTenantClient{
		counts: map[uuid.UUID]int64{id: 12},
	})

	res, err := svc.GetTenantPlan(context.Background(), &tenantv1.GetTenantPlanRequest{PlanId: id.String()})
	if err != nil {
		t.Fatalf("GetTenantPlan: %v", err)
	}
	if res.GetCode() != "pro" || res.GetTenantCount() != 12 || res.GetDescription() != "含GPU" {
		t.Fatalf("got %+v", res)
	}
}

func TestTenantPlanService_Get_NotFound(t *testing.T) {
	plans := &listFakePlanStore{
		getByIDFn: func(context.Context, uuid.UUID) (ports.TenantPlan, error) {
			return ports.TenantPlan{}, ports.ErrTenantPlanNotFound
		},
	}
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, &fakeQuotaClient{}, &fakeTenantClient{})
	_, err := svc.GetTenantPlan(context.Background(), &tenantv1.GetTenantPlanRequest{
		PlanId: "11111111-1111-1111-1111-111111111111",
	})
	if status.Code(err) != codes.NotFound || !strings.HasPrefix(status.Convert(err).Message(), "TENANT_PLAN_NOT_FOUND") {
		t.Fatalf("expected TENANT_PLAN_NOT_FOUND, got %v", err)
	}
}

func TestTenantPlanService_Get_InvalidID(t *testing.T) {
	svc := NewTenantPlanService(&listFakePlanStore{}, &fakeAuditStore{}, &fakeQuotaClient{}, &fakeTenantClient{})
	_, err := svc.GetTenantPlan(context.Background(), &tenantv1.GetTenantPlanRequest{PlanId: "not-a-uuid"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

// ---- from tenant_plan_service_update_test.go ----

type updateFakePlanStore struct {
	plan      ports.TenantPlan
	lastInput ports.UpdateTenantPlanInput
	updateErr error
}

func (f *updateFakePlanStore) Create(context.Context, ports.CreateTenantPlanInput) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *updateFakePlanStore) GetByID(_ context.Context, id uuid.UUID) (ports.TenantPlan, error) {
	if f.plan.ID == uuid.Nil || f.plan.ID != id {
		return ports.TenantPlan{}, ports.ErrTenantPlanNotFound
	}
	return f.plan, nil
}
func (f *updateFakePlanStore) List(context.Context, ports.TenantPlanListFilter) (ports.TenantPlanListResult, error) {
	panic("unused")
}
func (f *updateFakePlanStore) Update(_ context.Context, id uuid.UUID, in ports.UpdateTenantPlanInput) (ports.TenantPlan, error) {
	if f.updateErr != nil {
		return ports.TenantPlan{}, f.updateErr
	}
	if f.plan.ID == uuid.Nil || f.plan.ID != id {
		return ports.TenantPlan{}, ports.ErrTenantPlanNotFound
	}
	f.lastInput = in
	if in.Name != nil {
		f.plan.Name = *in.Name
	}
	if in.Description != nil {
		f.plan.Description = *in.Description
	}
	return f.plan, nil
}
func (f *updateFakePlanStore) Activate(context.Context, uuid.UUID) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *updateFakePlanStore) Disable(context.Context, uuid.UUID) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *updateFakePlanStore) Delete(context.Context, uuid.UUID) error { panic("unused") }
func (f *updateFakePlanStore) GetQuotaLimits(context.Context, uuid.UUID) ([]ports.PlanQuotaLimit, error) {
	panic("unused")
}
func (f *updateFakePlanStore) UpdateQuotaLimits(context.Context, uuid.UUID, []ports.PlanQuotaLimitInput) error {
	panic("unused")
}
func (f *updateFakePlanStore) GetApprovedQuotaChanges(context.Context, uuid.UUID) ([]ports.ApprovedQuotaChange, error) {
	panic("unused")
}

func TestTenantPlanService_UpdateTenantPlan(t *testing.T) {
	planID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	plans := &updateFakePlanStore{
		plan: ports.TenantPlan{
			ID:          planID,
			Code:        "pro",
			Name:        "old-name",
			Description: "old-desc",
			Status:      ports.TenantPlanStatusActive,
		},
	}
	audit := &fakeAuditStore{}
	svc := NewTenantPlanService(plans, audit, &fakeQuotaClient{}, &fakeTenantClient{})

	// 正常更新 name
	res, err := svc.UpdateTenantPlan(context.Background(), &tenantv1.UpdateTenantPlanRequest{
		PlanId: planID.String(),
		Name:   wrapperspb.String("new-name"),
	})
	if err != nil {
		t.Fatalf("update name: %v", err)
	}
	if res.GetMessage() != "tenant plan updated" || res.GetId() != planID.String() {
		t.Fatalf("res=%+v", res)
	}
	if plans.plan.Name != "new-name" || plans.plan.Description != "old-desc" {
		t.Fatalf("plan=%+v", plans.plan)
	}
	if plans.lastInput.Name == nil || *plans.lastInput.Name != "new-name" || plans.lastInput.Description != nil {
		t.Fatalf("lastInput=%+v", plans.lastInput)
	}
	if len(audit.logs) != 1 || audit.logs[0].Action != "tenant_plan.update" || audit.logs[0].Result != "success" {
		t.Fatalf("audit=%+v", audit.logs)
	}
	if v, _ := audit.logs[0].Details["name_updated"].(bool); !v {
		t.Fatalf("name_updated=%v", audit.logs[0].Details["name_updated"])
	}
	if v, _ := audit.logs[0].Details["description_updated"].(bool); v {
		t.Fatalf("description_updated=%v want false", audit.logs[0].Details["description_updated"])
	}

	// 正常更新 description（含清空）
	audit.logs = nil
	_, err = svc.UpdateTenantPlan(context.Background(), &tenantv1.UpdateTenantPlanRequest{
		PlanId:      planID.String(),
		Description: wrapperspb.String(""),
	})
	if err != nil {
		t.Fatalf("update description: %v", err)
	}
	if plans.plan.Name != "new-name" || plans.plan.Description != "" {
		t.Fatalf("plan after desc clear=%+v", plans.plan)
	}
	if v, _ := audit.logs[0].Details["description_updated"].(bool); !v {
		t.Fatalf("description_updated=%v", audit.logs[0].Details["description_updated"])
	}

	// 同时更新两者
	audit.logs = nil
	_, err = svc.UpdateTenantPlan(context.Background(), &tenantv1.UpdateTenantPlanRequest{
		PlanId:      planID.String(),
		Name:        wrapperspb.String("both-name"),
		Description: wrapperspb.String("both-desc"),
	})
	if err != nil {
		t.Fatalf("update both: %v", err)
	}
	if plans.plan.Name != "both-name" || plans.plan.Description != "both-desc" {
		t.Fatalf("plan=%+v", plans.plan)
	}
	if n, _ := audit.logs[0].Details["name_updated"].(bool); !n {
		t.Fatalf("name_updated missing")
	}
	if d, _ := audit.logs[0].Details["description_updated"].(bool); !d {
		t.Fatalf("description_updated missing")
	}

	// 空 body 不报错（仅刷新 updated_at / 无字段变更）
	audit.logs = nil
	prevName, prevDesc := plans.plan.Name, plans.plan.Description
	res, err = svc.UpdateTenantPlan(context.Background(), &tenantv1.UpdateTenantPlanRequest{
		PlanId: planID.String(),
	})
	if err != nil {
		t.Fatalf("empty body: %v", err)
	}
	if res.GetMessage() != "tenant plan updated" {
		t.Fatalf("res=%+v", res)
	}
	if plans.plan.Name != prevName || plans.plan.Description != prevDesc {
		t.Fatalf("empty body should not change fields: %+v", plans.plan)
	}
	if plans.lastInput.Name != nil || plans.lastInput.Description != nil {
		t.Fatalf("empty body lastInput=%+v", plans.lastInput)
	}

	// 套餐不存在 404
	_, err = svc.UpdateTenantPlan(context.Background(), &tenantv1.UpdateTenantPlanRequest{
		PlanId: uuid.New().String(),
		Name:   wrapperspb.String("x"),
	})
	if status.Code(err) != codes.NotFound || !strings.HasPrefix(status.Convert(err).Message(), "TENANT_PLAN_NOT_FOUND") {
		t.Fatalf("expected TENANT_PLAN_NOT_FOUND, got %v", err)
	}

	// name 超长 → 400 VALIDATION_FAILED
	audit.logs = nil
	longName := strings.Repeat("名", 65)
	_, err = svc.UpdateTenantPlan(context.Background(), &tenantv1.UpdateTenantPlanRequest{
		PlanId: planID.String(),
		Name:   wrapperspb.String(longName),
	})
	if status.Code(err) != codes.InvalidArgument || !strings.HasPrefix(status.Convert(err).Message(), "VALIDATION_FAILED") {
		t.Fatalf("expected VALIDATION_FAILED for long name, got %v", err)
	}
	if len(audit.logs) != 1 || audit.logs[0].Result != "failure" {
		t.Fatalf("audit after long name=%+v", audit.logs)
	}

	// description 超长 → 400 VALIDATION_FAILED
	audit.logs = nil
	longDesc := strings.Repeat("描", 513)
	_, err = svc.UpdateTenantPlan(context.Background(), &tenantv1.UpdateTenantPlanRequest{
		PlanId:      planID.String(),
		Description: wrapperspb.String(longDesc),
	})
	if status.Code(err) != codes.InvalidArgument || !strings.HasPrefix(status.Convert(err).Message(), "VALIDATION_FAILED") {
		t.Fatalf("expected VALIDATION_FAILED for long description, got %v", err)
	}
}

// ---- from tenant_plan_service_state_test.go ----

type stateFakePlanStore struct {
	byID      map[uuid.UUID]ports.TenantPlan
	bound     map[uuid.UUID]int64 // plan_id → tenant count (for Delete)
	deleted   []uuid.UUID
	activated []uuid.UUID
	disabled  []uuid.UUID
}

func newStateFake() *stateFakePlanStore {
	return &stateFakePlanStore{
		byID:  make(map[uuid.UUID]ports.TenantPlan),
		bound: make(map[uuid.UUID]int64),
	}
}

func (f *stateFakePlanStore) Create(context.Context, ports.CreateTenantPlanInput) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *stateFakePlanStore) GetByID(_ context.Context, id uuid.UUID) (ports.TenantPlan, error) {
	plan, ok := f.byID[id]
	if !ok || plan.IsDeleted {
		return ports.TenantPlan{}, ports.ErrTenantPlanNotFound
	}
	return plan, nil
}
func (f *stateFakePlanStore) List(context.Context, ports.TenantPlanListFilter) (ports.TenantPlanListResult, error) {
	panic("unused")
}
func (f *stateFakePlanStore) Update(context.Context, uuid.UUID, ports.UpdateTenantPlanInput) (ports.TenantPlan, error) {
	panic("unused")
}

func (f *stateFakePlanStore) Activate(_ context.Context, id uuid.UUID) (ports.TenantPlan, error) {
	plan, ok := f.byID[id]
	if !ok || plan.IsDeleted {
		return ports.TenantPlan{}, ports.ErrTenantPlanNotFound
	}
	switch plan.Status {
	case ports.TenantPlanStatusDraft, ports.TenantPlanStatusDisabled:
		plan.Status = ports.TenantPlanStatusActive
		plan.UpdatedAt = time.Now().UTC()
		f.byID[id] = plan
		f.activated = append(f.activated, id)
		return plan, nil
	default:
		return ports.TenantPlan{}, ports.ErrPlanStateInvalid
	}
}

func (f *stateFakePlanStore) Disable(_ context.Context, id uuid.UUID) (ports.TenantPlan, error) {
	plan, ok := f.byID[id]
	if !ok || plan.IsDeleted {
		return ports.TenantPlan{}, ports.ErrTenantPlanNotFound
	}
	if plan.Status != ports.TenantPlanStatusActive {
		return ports.TenantPlan{}, ports.ErrPlanStateInvalid
	}
	plan.Status = ports.TenantPlanStatusDisabled
	plan.UpdatedAt = time.Now().UTC()
	f.byID[id] = plan
	f.disabled = append(f.disabled, id)
	return plan, nil
}

func (f *stateFakePlanStore) Delete(_ context.Context, id uuid.UUID) error {
	plan, ok := f.byID[id]
	if !ok || plan.IsDeleted {
		return ports.ErrTenantPlanNotFound
	}
	plan.IsDeleted = true
	now := time.Now().UTC()
	plan.DeletedAt = &now
	f.byID[id] = plan
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *stateFakePlanStore) GetQuotaLimits(context.Context, uuid.UUID) ([]ports.PlanQuotaLimit, error) {
	panic("unused")
}
func (f *stateFakePlanStore) UpdateQuotaLimits(context.Context, uuid.UUID, []ports.PlanQuotaLimitInput) error {
	panic("unused")
}
func (f *stateFakePlanStore) GetApprovedQuotaChanges(context.Context, uuid.UUID) ([]ports.ApprovedQuotaChange, error) {
	panic("unused")
}

func newStateSvc(plans *stateFakePlanStore, audit *fakeAuditStore) *TenantPlanService {
	if audit == nil {
		audit = &fakeAuditStore{}
	}
	return NewTenantPlanService(plans, audit, &fakeQuotaClient{}, &fakeTenantClient{counts: plans.bound})
}

func putPlan(f *stateFakePlanStore, status ports.TenantPlanStatus) ports.TenantPlan {
	id := uuid.New()
	plan := ports.TenantPlan{
		ID:        id,
		Code:      "code-" + id.String()[:8],
		Name:      "套餐",
		Status:    status,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	f.byID[id] = plan
	return plan
}

func requireBizCode(t *testing.T, err error, wantCode codes.Code, wantBiz string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status, got %v", err)
	}
	if st.Code() != wantCode {
		t.Fatalf("grpc code=%v want %v msg=%q", st.Code(), wantCode, st.Message())
	}
	if !strings.HasPrefix(st.Message(), wantBiz) {
		t.Fatalf("message=%q want prefix %q", st.Message(), wantBiz)
	}
}

func TestTenantPlanService_Activate(t *testing.T) {
	t.Run("draft_to_active", func(t *testing.T) {
		plans := newStateFake()
		audit := &fakeAuditStore{}
		plan := putPlan(plans, ports.TenantPlanStatusDraft)
		svc := newStateSvc(plans, audit)

		res, err := svc.ActivateTenantPlan(context.Background(), &tenantv1.ActivateTenantPlanRequest{PlanId: plan.ID.String()})
		if err != nil {
			t.Fatalf("Activate: %v", err)
		}
		if res.GetMessage() != "tenant plan activated" {
			t.Fatalf("message=%q", res.GetMessage())
		}
		if plans.byID[plan.ID].Status != ports.TenantPlanStatusActive {
			t.Fatalf("status=%s", plans.byID[plan.ID].Status)
		}
		if len(audit.logs) != 1 || audit.logs[0].Action != "tenant_plan.activate" || audit.logs[0].Result != "success" {
			t.Fatalf("audit=%+v", audit.logs)
		}
	})

	t.Run("disabled_to_active", func(t *testing.T) {
		plans := newStateFake()
		plan := putPlan(plans, ports.TenantPlanStatusDisabled)
		svc := newStateSvc(plans, nil)

		if _, err := svc.ActivateTenantPlan(context.Background(), &tenantv1.ActivateTenantPlanRequest{PlanId: plan.ID.String()}); err != nil {
			t.Fatalf("Activate: %v", err)
		}
		if plans.byID[plan.ID].Status != ports.TenantPlanStatusActive {
			t.Fatalf("status=%s", plans.byID[plan.ID].Status)
		}
	})

	t.Run("active_again_409", func(t *testing.T) {
		plans := newStateFake()
		plan := putPlan(plans, ports.TenantPlanStatusActive)
		audit := &fakeAuditStore{}
		svc := newStateSvc(plans, audit)

		_, err := svc.ActivateTenantPlan(context.Background(), &tenantv1.ActivateTenantPlanRequest{PlanId: plan.ID.String()})
		requireBizCode(t, err, codes.FailedPrecondition, "PLAN_STATE_INVALID")
		if len(audit.logs) != 1 || audit.logs[0].Result != "failure" {
			t.Fatalf("expected failure audit, got %+v", audit.logs)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		audit := &fakeAuditStore{}
		svc := newStateSvc(newStateFake(), audit)
		_, err := svc.ActivateTenantPlan(context.Background(), &tenantv1.ActivateTenantPlanRequest{
			PlanId: uuid.New().String(),
		})
		requireBizCode(t, err, codes.NotFound, "TENANT_PLAN_NOT_FOUND")
		if len(audit.logs) != 1 || audit.logs[0].Result != "failure" {
			t.Fatalf("expected failure audit, got %+v", audit.logs)
		}
	})
}

func TestTenantPlanService_Disable(t *testing.T) {
	t.Run("active_to_disabled", func(t *testing.T) {
		plans := newStateFake()
		audit := &fakeAuditStore{}
		plan := putPlan(plans, ports.TenantPlanStatusActive)
		svc := newStateSvc(plans, audit)

		res, err := svc.DisableTenantPlan(context.Background(), &tenantv1.DisableTenantPlanRequest{PlanId: plan.ID.String()})
		if err != nil {
			t.Fatalf("Disable: %v", err)
		}
		if res.GetMessage() != "tenant plan disabled" {
			t.Fatalf("message=%q", res.GetMessage())
		}
		if plans.byID[plan.ID].Status != ports.TenantPlanStatusDisabled {
			t.Fatalf("status=%s", plans.byID[plan.ID].Status)
		}
		if len(audit.logs) != 1 || audit.logs[0].Action != "tenant_plan.disable" || audit.logs[0].Result != "success" {
			t.Fatalf("audit=%+v", audit.logs)
		}
	})

	t.Run("draft_disable_409", func(t *testing.T) {
		plans := newStateFake()
		plan := putPlan(plans, ports.TenantPlanStatusDraft)
		audit := &fakeAuditStore{}
		svc := newStateSvc(plans, audit)

		_, err := svc.DisableTenantPlan(context.Background(), &tenantv1.DisableTenantPlanRequest{PlanId: plan.ID.String()})
		requireBizCode(t, err, codes.FailedPrecondition, "PLAN_STATE_INVALID")
		if len(audit.logs) != 1 || audit.logs[0].Result != "failure" {
			t.Fatalf("expected failure audit, got %+v", audit.logs)
		}
	})
}

func TestTenantPlanService_Delete(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		plans := newStateFake()
		audit := &fakeAuditStore{}
		plan := putPlan(plans, ports.TenantPlanStatusDraft)
		svc := newStateSvc(plans, audit)

		res, err := svc.DeleteTenantPlan(context.Background(), &tenantv1.DeleteTenantPlanRequest{PlanId: plan.ID.String()})
		if err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if res.GetMessage() != "tenant plan deleted" {
			t.Fatalf("message=%q", res.GetMessage())
		}
		if !plans.byID[plan.ID].IsDeleted {
			t.Fatal("expected soft deleted")
		}
		if len(audit.logs) != 1 || audit.logs[0].Action != "tenant_plan.delete" || audit.logs[0].Result != "success" {
			t.Fatalf("audit=%+v", audit.logs)
		}
	})

	t.Run("in_use_409", func(t *testing.T) {
		plans := newStateFake()
		plan := putPlan(plans, ports.TenantPlanStatusActive)
		plans.bound[plan.ID] = 2 // 非 disabled 绑定
		audit := &fakeAuditStore{}
		svc := newStateSvc(plans, audit)

		_, err := svc.DeleteTenantPlan(context.Background(), &tenantv1.DeleteTenantPlanRequest{PlanId: plan.ID.String()})
		requireBizCode(t, err, codes.FailedPrecondition, "TENANT_PLAN_IN_USE")
		if len(audit.logs) != 1 || audit.logs[0].Result != "failure" {
			t.Fatalf("expected failure audit, got %+v", audit.logs)
		}
	})

	t.Run("not_found_before_in_use", func(t *testing.T) {
		plans := newStateFake()
		missing := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
		plans.bound[missing] = 2
		audit := &fakeAuditStore{}
		svc := newStateSvc(plans, audit)

		_, err := svc.DeleteTenantPlan(context.Background(), &tenantv1.DeleteTenantPlanRequest{PlanId: missing.String()})
		requireBizCode(t, err, codes.NotFound, "TENANT_PLAN_NOT_FOUND")
	})

	t.Run("only_disabled_tenants_ok", func(t *testing.T) {
		// disabled 租户不计入绑定，允许删套餐
		plans := newStateFake()
		plan := putPlan(plans, ports.TenantPlanStatusActive)
		plans.bound[plan.ID] = 0
		svc := newStateSvc(plans, nil)

		if _, err := svc.DeleteTenantPlan(context.Background(), &tenantv1.DeleteTenantPlanRequest{PlanId: plan.ID.String()}); err != nil {
			t.Fatalf("Delete with only disabled tenants semantics: %v", err)
		}
	})

	t.Run("soft_delete_code_reuse", func(t *testing.T) {
		// 模拟 store：软删除后 Create 同 code 成功（partial unique index 语义由 DB 保证，此处验 service 删除路径）。
		plans := newStateFake()
		plan := putPlan(plans, ports.TenantPlanStatusActive)
		plan.Code = "reuse-me"
		plans.byID[plan.ID] = plan
		svc := newStateSvc(plans, nil)

		if _, err := svc.DeleteTenantPlan(context.Background(), &tenantv1.DeleteTenantPlanRequest{PlanId: plan.ID.String()}); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if !plans.byID[plan.ID].IsDeleted {
			t.Fatal("expected soft deleted")
		}
		// 再 Create 同 code：用真实 Create 路径的 fake（独立 store）验证不冲突语义
		createPlans := &fakePlanStore{
			createFn: func(_ context.Context, in ports.CreateTenantPlanInput) (ports.TenantPlan, error) {
				if in.Code != "reuse-me" {
					t.Fatalf("code=%q", in.Code)
				}
				return ports.TenantPlan{ID: uuid.New(), Code: in.Code, Name: in.Name, Status: ports.TenantPlanStatusDraft}, nil
			},
		}
		createSvc := newCreateSvc(createPlans, &fakeAuditStore{}, &fakeQuotaClient{
			meta: []ports.QuotaMeta{{ResourceType: "gpu", Enabled: true, DefaultQuota: 1}},
		})
		res, err := createSvc.CreateTenantPlan(context.Background(), &tenantv1.CreateTenantPlanRequest{
			Code: "reuse-me", Name: "新套餐", IdempotencyKey: uuid.New().String(),
		})
		if err != nil {
			t.Fatalf("Create after soft delete: %v", err)
		}
		if res.GetId() == "" {
			t.Fatal("expected new id")
		}
	})
}

func TestMapStoreError_StateSentinels(t *testing.T) {
	err := mapStoreError(fmt.Errorf("%w: current status active", ports.ErrPlanStateInvalid))
	requireBizCode(t, err, codes.FailedPrecondition, "PLAN_STATE_INVALID")

	err = mapStoreError(ports.ErrTenantPlanInUse)
	requireBizCode(t, err, codes.FailedPrecondition, "TENANT_PLAN_IN_USE")
}

// ---- from tenant_plan_service_quota_limits_test.go ----

type quotaLimitsFakeStore struct {
	plan        ports.TenantPlan
	limits      []ports.PlanQuotaLimit
	updateCalls int
	updated     []ports.PlanQuotaLimitInput
	updateErr   error
	approved    map[uuid.UUID][]ports.ApprovedQuotaChange
}

func (f *quotaLimitsFakeStore) Create(context.Context, ports.CreateTenantPlanInput) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *quotaLimitsFakeStore) GetByID(context.Context, uuid.UUID) (ports.TenantPlan, error) {
	if f.plan.ID == uuid.Nil {
		return ports.TenantPlan{}, ports.ErrTenantPlanNotFound
	}
	return f.plan, nil
}
func (f *quotaLimitsFakeStore) List(context.Context, ports.TenantPlanListFilter) (ports.TenantPlanListResult, error) {
	panic("unused")
}
func (f *quotaLimitsFakeStore) Update(context.Context, uuid.UUID, ports.UpdateTenantPlanInput) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *quotaLimitsFakeStore) Activate(context.Context, uuid.UUID) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *quotaLimitsFakeStore) Disable(context.Context, uuid.UUID) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *quotaLimitsFakeStore) Delete(context.Context, uuid.UUID) error { panic("unused") }
func (f *quotaLimitsFakeStore) GetQuotaLimits(_ context.Context, planID uuid.UUID) ([]ports.PlanQuotaLimit, error) {
	if f.plan.ID == uuid.Nil || f.plan.ID != planID {
		return nil, ports.ErrTenantPlanNotFound
	}
	return append([]ports.PlanQuotaLimit(nil), f.limits...), nil
}
func (f *quotaLimitsFakeStore) UpdateQuotaLimits(_ context.Context, planID uuid.UUID, items []ports.PlanQuotaLimitInput) error {
	f.updateCalls++
	if f.plan.ID == uuid.Nil || f.plan.ID != planID {
		return ports.ErrTenantPlanNotFound
	}
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updated = append([]ports.PlanQuotaLimitInput(nil), items...)
	for _, item := range items {
		found := false
		for i := range f.limits {
			if f.limits[i].ResourceType == item.ResourceType {
				f.limits[i].Total = item.Total
				found = true
				break
			}
		}
		if !found {
			f.limits = append(f.limits, ports.PlanQuotaLimit{
				PlanID:       planID,
				ResourceType: item.ResourceType,
				Total:        item.Total,
			})
		}
	}
	return nil
}
func (f *quotaLimitsFakeStore) GetApprovedQuotaChanges(_ context.Context, tenantID uuid.UUID) ([]ports.ApprovedQuotaChange, error) {
	return append([]ports.ApprovedQuotaChange(nil), f.approved[tenantID]...), nil
}

func TestTenantPlanService_GetQuotaLimits(t *testing.T) {
	t.Parallel()

	planID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	total := int64(16)
	plans := &quotaLimitsFakeStore{
		plan: ports.TenantPlan{ID: planID, Code: "pro", Status: ports.TenantPlanStatusActive},
		limits: []ports.PlanQuotaLimit{
			{PlanID: planID, ResourceType: "gpu_count", Total: &total},
		},
	}
	core := &fakeQuotaClient{
		meta: []ports.QuotaMeta{
			{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"},
		},
	}
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, core, &fakeTenantClient{})

	res, err := svc.GetTenantPlanQuotaLimits(context.Background(), &tenantv1.GetTenantPlanQuotaLimitsRequest{
		PlanId: planID.String(),
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(res.GetItems()) != 1 || res.GetItems()[0].GetTotal() != 16 || res.GetItems()[0].GetDisplayName() != "GPU" {
		t.Fatalf("items=%+v", res.GetItems())
	}
	if core.calls != 1 {
		t.Fatalf("ListQuotaMeta calls=%d want 1", core.calls)
	}
	if plans.updateCalls != 0 {
		t.Fatalf("non-null total must not backfill, updateCalls=%d", plans.updateCalls)
	}

	_, err = svc.GetTenantPlanQuotaLimits(context.Background(), &tenantv1.GetTenantPlanQuotaLimitsRequest{
		PlanId: uuid.New().String(),
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestTenantPlanService_GetQuotaLimits_BackfillNullTotal(t *testing.T) {
	t.Parallel()

	planID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaab")
	plans := &quotaLimitsFakeStore{
		plan: ports.TenantPlan{ID: planID, Code: "pro", Status: ports.TenantPlanStatusActive},
		limits: []ports.PlanQuotaLimit{
			{PlanID: planID, ResourceType: "gpu_count", Total: nil},
			{PlanID: planID, ResourceType: "cpu_core", Total: int64Ptr(8)},
		},
	}
	core := &fakeQuotaClient{
		meta: []ports.QuotaMeta{
			{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"},
			{ResourceType: "cpu_core", Enabled: true, DefaultQuota: 32, DisplayName: "CPU", Unit: "core"},
		},
	}
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, core, &fakeTenantClient{})

	res, err := svc.GetTenantPlanQuotaLimits(context.Background(), &tenantv1.GetTenantPlanQuotaLimitsRequest{
		PlanId: planID.String(),
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(res.GetItems()) != 2 {
		t.Fatalf("items=%+v", res.GetItems())
	}
	byType := map[string]*tenantv1.PlanQuotaLimitView{}
	for _, it := range res.GetItems() {
		byType[it.GetResourceType()] = it
	}
	if byType["gpu_count"].GetTotal() != 4 || byType["cpu_core"].GetTotal() != 8 {
		t.Fatalf("items=%+v", res.GetItems())
	}
	if plans.updateCalls != 1 || len(plans.updated) != 1 {
		t.Fatalf("backfill updateCalls=%d updated=%+v", plans.updateCalls, plans.updated)
	}
	if plans.updated[0].ResourceType != "gpu_count" || plans.updated[0].Total == nil || *plans.updated[0].Total != 4 {
		t.Fatalf("backfill item=%+v", plans.updated[0])
	}
	if plans.limits[0].Total == nil || *plans.limits[0].Total != 4 {
		t.Fatalf("store limit after backfill=%+v", plans.limits[0])
	}
}

func TestTenantPlanService_GetQuotaLimits_BackfillFailStillReturnsView(t *testing.T) {
	t.Parallel()

	planID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaac")
	plans := &quotaLimitsFakeStore{
		plan: ports.TenantPlan{ID: planID, Code: "pro", Status: ports.TenantPlanStatusActive},
		limits: []ports.PlanQuotaLimit{
			{PlanID: planID, ResourceType: "gpu_count", Total: nil},
		},
		updateErr: errors.New("db write failed"),
	}
	core := &fakeQuotaClient{
		meta: []ports.QuotaMeta{
			{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"},
		},
	}
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, core, &fakeTenantClient{})

	res, err := svc.GetTenantPlanQuotaLimits(context.Background(), &tenantv1.GetTenantPlanQuotaLimitsRequest{
		PlanId: planID.String(),
	})
	if err != nil {
		t.Fatalf("Get should succeed despite backfill failure: %v", err)
	}
	if len(res.GetItems()) != 1 || res.GetItems()[0].GetTotal() != 4 {
		t.Fatalf("items=%+v want coalesced total=4", res.GetItems())
	}
	if plans.updateCalls != 1 {
		t.Fatalf("updateCalls=%d want 1", plans.updateCalls)
	}
	if plans.limits[0].Total != nil {
		t.Fatalf("store must remain NULL when backfill fails, got %+v", plans.limits[0])
	}
}

func TestTenantPlanService_UpdateQuotaLimits(t *testing.T) {
	enablePutQuotaRetry = false
	t.Cleanup(func() { enablePutQuotaRetry = true })

	planID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	tenantA := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	tenantB := uuid.MustParse("dddddddd-dddd-dddd-dddd-dddddddddddd")

	plans := &quotaLimitsFakeStore{
		plan: ports.TenantPlan{ID: planID, Code: "pro", Status: ports.TenantPlanStatusDraft},
		limits: []ports.PlanQuotaLimit{
			{PlanID: planID, ResourceType: "gpu_count", Total: int64Ptr(4)},
			{PlanID: planID, ResourceType: "cpu_core", Total: nil},
		},
		approved: map[uuid.UUID][]ports.ApprovedQuotaChange{
			tenantA: {{TenantID: tenantA, ResourceType: "gpu_count"}},
		},
	}
	core := &fakeQuotaClient{
		meta: []ports.QuotaMeta{
			{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"},
			{ResourceType: "cpu_core", Enabled: true, DefaultQuota: 32, DisplayName: "CPU", Unit: "core"},
		},
		// tenantA：仅有 cpu_core（gpu 已 approved 跳过）→ Put cpu_core
		// tenantB：无配额行 → Create gpu+cpu
		existing: map[uuid.UUID][]ports.CoreQuotaResult{
			tenantA: {{ResourceType: "cpu_core", Total: 16}},
		},
	}
	audit := &fakeAuditStore{}
	svc := NewTenantPlanService(plans, audit, core, &fakeTenantClient{
		bound: []ports.BoundTenant{
			{ID: tenantA, Name: "a", Status: ports.TenantStatusActive},
			{ID: tenantB, Name: "b", Status: ports.TenantStatusActive},
		},
	})

	total := int64(8)
	res, err := svc.UpdateTenantPlanQuotaLimits(context.Background(), &tenantv1.UpdateTenantPlanQuotaLimitsRequest{
		PlanId: planID.String(),
		Items: []*tenantv1.PlanQuotaLimitInput{
			{ResourceType: "gpu_count", Total: wrapperspb.Int64(total)},
			{ResourceType: "cpu_core"}, // nil → Core default_quota(32) 落库
		},
		IdempotencyKey: "k-update-1",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if res.GetMessage() != "quota limits updated" {
		t.Fatalf("message=%q", res.GetMessage())
	}
	if plans.updateCalls != 1 || len(plans.updated) != 2 {
		t.Fatalf("updateCalls=%d updated=%+v", plans.updateCalls, plans.updated)
	}
	if plans.updated[1].Total == nil || *plans.updated[1].Total != 32 {
		t.Fatalf("cpu_core total should be default 32, got %+v", plans.updated[1])
	}

	// tenantA：Put cpu_core；tenantB：Create gpu+cpu
	if core.putCalls != 1 {
		t.Fatalf("putCalls=%d want 1", core.putCalls)
	}
	if len(core.putItems) != 1 || core.putItems[0].ResourceType != "cpu_core" || core.putItems[0].Total != 32 {
		t.Fatalf("putItems=%+v", core.putItems)
	}
	if core.createCalls != 1 {
		t.Fatalf("createCalls=%d want 1", core.createCalls)
	}
	if len(core.createItems) != 2 {
		t.Fatalf("createItems=%+v", core.createItems)
	}
	if len(audit.logs) != 1 || audit.logs[0].Result != "success" || audit.logs[0].Action != "tenant_plan.update_quota_limits" {
		t.Fatalf("audit=%+v", audit.logs)
	}
	details := audit.logs[0].Details
	if details["synced_tenant_count"] != 2 {
		t.Fatalf("synced_tenant_count=%v", details["synced_tenant_count"])
	}
	if details["skipped_approved"] != 1 {
		t.Fatalf("skipped_approved=%v", details["skipped_approved"])
	}
	if details["tightened"] != 0 {
		t.Fatalf("tightened=%v want 0", details["tightened"])
	}
}

func Test_UpdateQuotaLimits_Tightened(t *testing.T) {
	enablePutQuotaRetry = false
	t.Cleanup(func() { enablePutQuotaRetry = true })

	planID := uuid.New()
	tenantID := uuid.New()
	plans := &quotaLimitsFakeStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusActive},
		limits: []ports.PlanQuotaLimit{
			{PlanID: planID, ResourceType: "gpu_count", Total: int64Ptr(1)},
		},
	}
	audit := &fakeAuditStore{}
	core := &fakeQuotaClient{
		meta:         []ports.QuotaMeta{{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"}},
		putTightened: true,
		existing: map[uuid.UUID][]ports.CoreQuotaResult{
			tenantID: {{ResourceType: "gpu_count", Total: 4}},
		},
	}
	svc := NewTenantPlanService(plans, audit, core, &fakeTenantClient{
		bound: []ports.BoundTenant{{ID: tenantID, Name: "t", Status: ports.TenantStatusActive}},
	})

	_, err := svc.UpdateTenantPlanQuotaLimits(context.Background(), &tenantv1.UpdateTenantPlanQuotaLimitsRequest{
		PlanId: planID.String(),
		Items: []*tenantv1.PlanQuotaLimitInput{
			{ResourceType: "gpu_count", Total: wrapperspb.Int64(1)},
		},
	})
	if err != nil {
		t.Fatalf("tightened must not error: %v", err)
	}
	if core.putCalls != 1 {
		t.Fatalf("putCalls=%d", core.putCalls)
	}
	if core.createCalls != 0 {
		t.Fatalf("createCalls=%d want 0", core.createCalls)
	}
	if len(audit.logs) != 1 || audit.logs[0].Result != "success" {
		t.Fatalf("audit=%+v", audit.logs)
	}
	if audit.logs[0].Details["tightened"] != 1 {
		t.Fatalf("tightened=%v want 1", audit.logs[0].Details["tightened"])
	}
	if audit.logs[0].Details["skipped_approved"] != 0 {
		t.Fatalf("skipped_approved=%v want 0", audit.logs[0].Details["skipped_approved"])
	}
}

func Test_UpdateQuotaLimits_CoreFailAudits(t *testing.T) {
	enablePutQuotaRetry = false
	t.Cleanup(func() { enablePutQuotaRetry = true })

	planID := uuid.New()
	tenantID := uuid.New()
	plans := &quotaLimitsFakeStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusActive},
		limits: []ports.PlanQuotaLimit{
			{PlanID: planID, ResourceType: "gpu_count", Total: int64Ptr(4)},
		},
	}
	core := &fakeQuotaClient{
		meta: []ports.QuotaMeta{{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"}},
		existing: map[uuid.UUID][]ports.CoreQuotaResult{
			tenantID: {{ResourceType: "gpu_count", Total: 2}},
		},
		putFn: func(context.Context, uuid.UUID, []ports.CoreQuotaItem) ([]ports.CoreQuotaResult, error) {
			return nil, ports.ErrQuotaNotFound
		},
	}
	audit := &fakeAuditStore{}
	svc := NewTenantPlanService(plans, audit, core, &fakeTenantClient{
		bound: []ports.BoundTenant{{ID: tenantID, Name: "t", Status: ports.TenantStatusActive}},
	})

	res, err := svc.UpdateTenantPlanQuotaLimits(context.Background(), &tenantv1.UpdateTenantPlanQuotaLimitsRequest{
		PlanId: planID.String(),
		Items: []*tenantv1.PlanQuotaLimitInput{
			{ResourceType: "gpu_count", Total: wrapperspb.Int64(4)},
		},
	})
	if err != nil {
		t.Fatalf("update should succeed even if Core sync fails: %v", err)
	}
	if res.GetMessage() != "quota limits updated" {
		t.Fatalf("message=%q", res.GetMessage())
	}
	var failAudit bool
	for _, log := range audit.logs {
		if log.Action == "tenant.quota_init_failed" && log.Result == "failure" {
			failAudit = true
		}
	}
	if !failAudit {
		t.Fatalf("expected tenant.quota_init_failed audit, got %+v", audit.logs)
	}
}

func Test_UpdateQuotaLimits_CreateWhenMissing(t *testing.T) {
	enablePutQuotaRetry = false
	t.Cleanup(func() { enablePutQuotaRetry = true })

	planID := uuid.New()
	tenantID := uuid.New()
	plans := &quotaLimitsFakeStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusActive},
		limits: []ports.PlanQuotaLimit{
			{PlanID: planID, ResourceType: "gpu_count", Total: int64Ptr(4)},
		},
	}
	core := &fakeQuotaClient{
		meta: []ports.QuotaMeta{{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"}},
		// GetQuota 空列表 → CreateQuota
	}
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, core, &fakeTenantClient{
		bound: []ports.BoundTenant{{ID: tenantID, Name: "t", Status: ports.TenantStatusActive}},
	})

	_, err := svc.UpdateTenantPlanQuotaLimits(context.Background(), &tenantv1.UpdateTenantPlanQuotaLimitsRequest{
		PlanId: planID.String(),
		Items: []*tenantv1.PlanQuotaLimitInput{
			{ResourceType: "gpu_count", Total: wrapperspb.Int64(4)},
		},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if core.createCalls != 1 || core.putCalls != 0 {
		t.Fatalf("createCalls=%d putCalls=%d", core.createCalls, core.putCalls)
	}
	if len(core.createItems) != 1 || core.createItems[0].ResourceType != "gpu_count" || core.createItems[0].Total != 4 {
		t.Fatalf("createItems=%+v", core.createItems)
	}
}

func Test_UpdateQuotaLimits_Validation(t *testing.T) {
	t.Parallel()

	planID := uuid.New()
	plans := &quotaLimitsFakeStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusActive},
	}
	core := &fakeQuotaClient{meta: []ports.QuotaMeta{{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4}}}
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, core, &fakeTenantClient{})

	_, err := svc.UpdateTenantPlanQuotaLimits(context.Background(), &tenantv1.UpdateTenantPlanQuotaLimitsRequest{
		PlanId: planID.String(),
		Items:  nil,
	})
	if status.Code(err) != codes.InvalidArgument || !strings.HasPrefix(status.Convert(err).Message(), "VALIDATION_FAILED") {
		t.Fatalf("empty items: %v", err)
	}

	_, err = svc.UpdateTenantPlanQuotaLimits(context.Background(), &tenantv1.UpdateTenantPlanQuotaLimitsRequest{
		PlanId: planID.String(),
		Items: []*tenantv1.PlanQuotaLimitInput{
			{ResourceType: "unknown_dim", Total: wrapperspb.Int64(1)},
		},
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "QUOTA_RESOURCE_NOT_REGISTERED") {
		t.Fatalf("unknown dim: %v", err)
	}
}

func int64Ptr(v int64) *int64 { return &v }

// ---- from tenant_plan_service_quota_meta_test.go ----

func TestTenantPlanService_ListQuotaMeta(t *testing.T) {
	core := &fakeQuotaClient{
		meta: []ports.QuotaMeta{
			{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card", IsDiscrete: true},
			{ResourceType: "cpu_core", Enabled: true, DefaultQuota: 16, DisplayName: "CPU", Unit: "core", IsDiscrete: true},
		},
	}
	svc := NewTenantPlanService(&fakePlanStore{}, &fakeAuditStore{}, core, &fakeTenantClient{})

	res, err := svc.ListQuotaMeta(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListQuotaMeta: %v", err)
	}
	if core.calls != 1 {
		t.Fatalf("ListQuotaMeta calls=%d want 1", core.calls)
	}
	if len(res.GetItems()) != 2 {
		t.Fatalf("items=%+v", res.GetItems())
	}
	it := res.GetItems()[0]
	if it.GetResourceType() != "gpu_count" || it.GetDisplayName() != "GPU" || it.GetUnit() != "card" ||
		it.GetDefaultQuota() != 4 || !it.GetIsDiscrete() {
		t.Fatalf("item0=%+v", it)
	}
}

func TestTenantPlanService_ListQuotaMeta_CoreUnavailable(t *testing.T) {
	core := &fakeQuotaClient{metaErr: ports.ErrCoreUnavailable}
	svc := NewTenantPlanService(&fakePlanStore{}, &fakeAuditStore{}, core, &fakeTenantClient{})

	_, err := svc.ListQuotaMeta(context.Background(), nil)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code=%v want Unavailable", status.Code(err))
	}
	msg := status.Convert(err).Message()
	if !strings.HasPrefix(msg, "GRPC_CLIENT_UNAVAILABLE") {
		t.Fatalf("msg=%q want GRPC_CLIENT_UNAVAILABLE prefix", msg)
	}
}

// ---- from tenant_plan_service_audit_logs_test.go ----

func TestTenantPlanService_ListTenantPlanAuditLogs(t *testing.T) {
	planID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	otherPlan := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	createLogID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	activateLogID := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	plans := &fakePlanStore{
		plan: ports.TenantPlan{
			ID:     planID,
			Code:   "pro",
			Name:   "Pro",
			Status: ports.TenantPlanStatusActive,
		},
	}
	audit := &fakeAuditStore{}
	// 模拟创建成功后的审计 + 另一套餐噪声
	writeAuditSuccess(context.Background(), audit, "tenant_plan.create", map[string]any{
		"plan_id": planID.String(),
		"code":    "pro",
	}, nil)
	audit.logs[0].ID = createLogID
	audit.logs[0].CreatedAt = time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)

	writeAuditSuccess(context.Background(), audit, "tenant_plan.activate", map[string]any{
		"plan_id": planID.String(),
		"status":  "active",
	}, nil)
	audit.logs[1].ID = activateLogID
	audit.logs[1].CreatedAt = time.Date(2026, 8, 13, 11, 0, 0, 0, time.UTC)

	writeAuditSuccess(context.Background(), audit, "tenant_plan.create", map[string]any{
		"plan_id": otherPlan.String(),
		"code":    "other",
	}, nil)

	svc := NewTenantPlanService(plans, audit, &fakeQuotaClient{}, &fakeTenantClient{})

	// 创建后能查到对应审计（不含其他套餐）
	res, err := svc.ListTenantPlanAuditLogs(context.Background(), &tenantv1.ListTenantPlanAuditLogsRequest{
		PlanId: planID.String(),
		Page:   &commonv1.CursorPageRequest{Limit: 20},
	})
	if err != nil {
		t.Fatalf("ListTenantPlanAuditLogs: %v", err)
	}
	if res.GetTotal() != 2 || len(res.GetItems()) != 2 {
		t.Fatalf("total=%d items=%d", res.GetTotal(), len(res.GetItems()))
	}
	// fake 倒序：后写入的 activate 在前
	if res.GetItems()[0].GetId() != activateLogID.String() || res.GetItems()[0].GetAction() != "tenant_plan.activate" {
		t.Fatalf("items[0]=%+v", res.GetItems()[0])
	}
	if res.GetItems()[1].GetId() != createLogID.String() || res.GetItems()[1].GetAction() != "tenant_plan.create" {
		t.Fatalf("items[1]=%+v", res.GetItems()[1])
	}
	if res.GetItems()[1].GetDetails().GetFields()["plan_id"].GetStringValue() != planID.String() {
		t.Fatalf("details=%v", res.GetItems()[1].GetDetails())
	}

	// 套餐不存在 404
	_, err = svc.ListTenantPlanAuditLogs(context.Background(), &tenantv1.ListTenantPlanAuditLogsRequest{
		PlanId: uuid.New().String(),
	})
	if status.Code(err) != codes.NotFound || !strings.HasPrefix(status.Convert(err).Message(), "TENANT_PLAN_NOT_FOUND") {
		t.Fatalf("expected TENANT_PLAN_NOT_FOUND, got %v", err)
	}
}

// ---- from tenant_service_bind_test.go ----

type bindFakePlanStore struct {
	plan     ports.TenantPlan
	limits   []ports.PlanQuotaLimit
	approved map[uuid.UUID][]ports.ApprovedQuotaChange
}

func (f *bindFakePlanStore) Create(context.Context, ports.CreateTenantPlanInput) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *bindFakePlanStore) GetByID(_ context.Context, id uuid.UUID) (ports.TenantPlan, error) {
	if f.plan.ID == uuid.Nil || f.plan.ID != id {
		return ports.TenantPlan{}, ports.ErrTenantPlanNotFound
	}
	return f.plan, nil
}
func (f *bindFakePlanStore) List(context.Context, ports.TenantPlanListFilter) (ports.TenantPlanListResult, error) {
	panic("unused")
}
func (f *bindFakePlanStore) Update(context.Context, uuid.UUID, ports.UpdateTenantPlanInput) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *bindFakePlanStore) Activate(context.Context, uuid.UUID) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *bindFakePlanStore) Disable(context.Context, uuid.UUID) (ports.TenantPlan, error) {
	panic("unused")
}
func (f *bindFakePlanStore) Delete(context.Context, uuid.UUID) error { panic("unused") }
func (f *bindFakePlanStore) GetQuotaLimits(_ context.Context, planID uuid.UUID) ([]ports.PlanQuotaLimit, error) {
	if f.plan.ID == uuid.Nil || f.plan.ID != planID {
		return nil, ports.ErrTenantPlanNotFound
	}
	return append([]ports.PlanQuotaLimit(nil), f.limits...), nil
}
func (f *bindFakePlanStore) UpdateQuotaLimits(_ context.Context, planID uuid.UUID, items []ports.PlanQuotaLimitInput) error {
	if f.plan.ID == uuid.Nil || f.plan.ID != planID {
		return ports.ErrTenantPlanNotFound
	}
	for _, item := range items {
		found := false
		for i := range f.limits {
			if f.limits[i].ResourceType == item.ResourceType {
				f.limits[i].Total = item.Total
				found = true
				break
			}
		}
		if !found {
			f.limits = append(f.limits, ports.PlanQuotaLimit{PlanID: planID, ResourceType: item.ResourceType, Total: item.Total})
		}
	}
	return nil
}
func (f *bindFakePlanStore) GetApprovedQuotaChanges(_ context.Context, tenantID uuid.UUID) ([]ports.ApprovedQuotaChange, error) {
	return append([]ports.ApprovedQuotaChange(nil), f.approved[tenantID]...), nil
}

func TestTenantService_BindPlanQuota(t *testing.T) {
	planID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	oldPlan := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	tenantID := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	total := int64(16)

	plans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Code: "pro", Status: ports.TenantPlanStatusActive},
		limits: []ports.PlanQuotaLimit{
			{PlanID: planID, ResourceType: "gpu_count", Total: &total},
		},
	}
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Name: "acme", Status: ports.TenantStatusActive, PlanID: oldPlan},
	}
	quota := &fakeQuotaClient{
		meta: []ports.QuotaMeta{
			{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"},
		},
	}
	audit := &fakeAuditStore{}
	svc := NewTenantService(plans, tenants, quota, audit)

	res, err := svc.BindPlanQuota(context.Background(), &tenantv1.BindPlanQuotaRequest{
		TenantId: tenantID.String(),
		PlanId:   planID.String(),
	})
	if err != nil {
		t.Fatalf("BindPlanQuota: %v", err)
	}
	if res.GetMessage() != "quota bound to plan" || res.GetId() != tenantID.String() {
		t.Fatalf("res=%+v", res)
	}
	if quota.createCalls != 1 || quota.putCalls != 0 {
		t.Fatalf("createCalls=%d putCalls=%d", quota.createCalls, quota.putCalls)
	}
	if tenants.updateCalls != 1 || tenants.updatedPlan != planID {
		t.Fatalf("UpdateTenantPlan calls=%d plan=%s", tenants.updateCalls, tenants.updatedPlan)
	}
	if len(audit.logs) != 1 || audit.logs[0].Action != "tenant.bind_plan_quota" || audit.logs[0].Result != "success" {
		t.Fatalf("audit=%+v", audit.logs)
	}
	if audit.logs[0].TenantID == nil || *audit.logs[0].TenantID != tenantID {
		t.Fatalf("audit TenantID=%v want %s", audit.logs[0].TenantID, tenantID)
	}

	// plan 404
	_, err = svc.BindPlanQuota(context.Background(), &tenantv1.BindPlanQuotaRequest{
		TenantId: tenantID.String(),
		PlanId:   uuid.New().String(),
	})
	if status.Code(err) != codes.NotFound || !strings.HasPrefix(status.Convert(err).Message(), "TENANT_PLAN_NOT_FOUND") {
		t.Fatalf("plan 404: %v", err)
	}
}

func Test_BindPlanQuota_PlanNotActive(t *testing.T) {
	planID := uuid.New()
	tenantID := uuid.New()
	plans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusDraft},
	}
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusActive, PlanID: uuid.New()},
	}
	svc := NewTenantService(plans, tenants, &fakeQuotaClient{}, &fakeAuditStore{})

	_, err := svc.BindPlanQuota(context.Background(), &tenantv1.BindPlanQuotaRequest{
		TenantId: tenantID.String(),
		PlanId:   planID.String(),
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "PLAN_NOT_ACTIVE") {
		t.Fatalf("expected PLAN_NOT_ACTIVE, got %v", err)
	}
}

func Test_BindPlanQuota_DisabledTenant(t *testing.T) {
	planID := uuid.New()
	tenantID := uuid.New()
	plans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusActive},
	}
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusDisabled, PlanID: uuid.New()},
	}
	svc := NewTenantService(plans, tenants, &fakeQuotaClient{}, &fakeAuditStore{})

	_, err := svc.BindPlanQuota(context.Background(), &tenantv1.BindPlanQuotaRequest{
		TenantId: tenantID.String(),
		PlanId:   planID.String(),
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "TENANT_STATE_INVALID") {
		t.Fatalf("expected TENANT_STATE_INVALID, got %v", err)
	}
}

func Test_BindPlanQuota_AuditWriteErrorStillSucceeds(t *testing.T) {
	planID := uuid.New()
	oldPlan := uuid.New()
	tenantID := uuid.New()
	total := int64(16)

	plans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusActive},
		limits: []ports.PlanQuotaLimit{
			{PlanID: planID, ResourceType: "gpu_count", Total: &total},
		},
	}
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusActive, PlanID: oldPlan},
	}
	quota := &fakeQuotaClient{
		meta: []ports.QuotaMeta{
			{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"},
		},
	}
	audit := &fakeAuditStore{
		createFn: func(context.Context, ports.AuditLog) (uuid.UUID, error) {
			return uuid.Nil, errors.New("db down")
		},
	}
	svc := NewTenantService(plans, tenants, quota, audit)

	res, err := svc.BindPlanQuota(context.Background(), &tenantv1.BindPlanQuotaRequest{
		TenantId: tenantID.String(),
		PlanId:   planID.String(),
	})
	if err != nil {
		t.Fatalf("audit failure must not fail bind: %v", err)
	}
	if res.GetId() != tenantID.String() || res.GetMessage() != "quota bound to plan" {
		t.Fatalf("res=%+v", res)
	}
	if tenants.tenant.PlanID != planID {
		t.Fatalf("plan_id=%s want %s", tenants.tenant.PlanID, planID)
	}
	if quota.createCalls != 1 {
		t.Fatalf("createCalls=%d want 1", quota.createCalls)
	}
}

func Test_BindPlanQuota_CoreFailRollsBackPlanID(t *testing.T) {
	planID := uuid.New()
	oldPlan := uuid.New()
	tenantID := uuid.New()
	total := int64(16)

	plans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusActive},
		limits: []ports.PlanQuotaLimit{
			{PlanID: planID, ResourceType: "gpu_count", Total: &total},
		},
	}
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusActive, PlanID: oldPlan},
	}
	quota := &fakeQuotaClient{
		meta: []ports.QuotaMeta{
			{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"},
		},
		createFn: func(context.Context, uuid.UUID, []ports.CoreQuotaItem) ([]ports.CoreQuotaResult, error) {
			return nil, ports.ErrCoreUnavailable
		},
	}
	audit := &fakeAuditStore{}
	svc := NewTenantService(plans, tenants, quota, audit)

	_, err := svc.BindPlanQuota(context.Background(), &tenantv1.BindPlanQuotaRequest{
		TenantId: tenantID.String(),
		PlanId:   planID.String(),
	})
	if err == nil {
		t.Fatal("expected Core error")
	}
	if tenants.updateCalls != 2 {
		t.Fatalf("UpdateTenantPlan calls=%d want 2 (bind + rollback)", tenants.updateCalls)
	}
	if tenants.updatedPlan != oldPlan || tenants.tenant.PlanID != oldPlan {
		t.Fatalf("plan_id after rollback=%s want %s", tenants.tenant.PlanID, oldPlan)
	}
	if len(audit.logs) == 0 || audit.logs[len(audit.logs)-1].Result != "failure" {
		t.Fatalf("audit=%+v", audit.logs)
	}
	if rb, _ := audit.logs[len(audit.logs)-1].Details["rolled_back"].(bool); !rb {
		t.Fatalf("rolled_back=%v", audit.logs[len(audit.logs)-1].Details["rolled_back"])
	}
}

func Test_BindPlanQuota_ApprovedSkip(t *testing.T) {
	planID := uuid.New()
	tenantID := uuid.New()
	gpu := int64(8)
	cpu := int64(32)

	plans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusActive},
		limits: []ports.PlanQuotaLimit{
			{PlanID: planID, ResourceType: "gpu_count", Total: &gpu},
			{PlanID: planID, ResourceType: "cpu_core", Total: &cpu},
		},
		approved: map[uuid.UUID][]ports.ApprovedQuotaChange{
			tenantID: {{TenantID: tenantID, ResourceType: "gpu_count"}},
		},
	}
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusActive, PlanID: planID}, // 同套餐 → 不 UpdateTenantPlan
	}
	quota := &fakeQuotaClient{
		meta: []ports.QuotaMeta{
			{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"},
			{ResourceType: "cpu_core", Enabled: true, DefaultQuota: 16, DisplayName: "CPU", Unit: "core"},
		},
		existing: map[uuid.UUID][]ports.CoreQuotaResult{
			tenantID: {{ResourceType: "cpu_core", Total: 16}},
		},
	}
	audit := &fakeAuditStore{}
	svc := NewTenantService(plans, tenants, quota, audit)

	_, err := svc.BindPlanQuota(context.Background(), &tenantv1.BindPlanQuotaRequest{
		TenantId: tenantID.String(),
		PlanId:   planID.String(),
	})
	if err != nil {
		t.Fatalf("BindPlanQuota: %v", err)
	}
	if quota.putCalls != 1 || len(quota.putItems) != 1 || quota.putItems[0].ResourceType != "cpu_core" {
		t.Fatalf("putCalls=%d putItems=%+v", quota.putCalls, quota.putItems)
	}
	if quota.createCalls != 0 {
		t.Fatalf("createCalls=%d want 0", quota.createCalls)
	}
	if tenants.updateCalls != 0 {
		t.Fatalf("same plan_id should skip UpdateTenantPlan, calls=%d", tenants.updateCalls)
	}
	skipped, ok := audit.logs[0].Details["skipped_approved"].(int)
	if !ok || skipped != 1 {
		t.Fatalf("skipped_approved=%v (want int 1)", audit.logs[0].Details["skipped_approved"])
	}
	tightened, ok := audit.logs[0].Details["tightened"].(int)
	if !ok || tightened != 0 {
		t.Fatalf("tightened=%v (want int 0)", audit.logs[0].Details["tightened"])
	}
}

func TestTenantPlanService_ListBoundTenants(t *testing.T) {
	planID := uuid.MustParse("dddddddd-dddd-dddd-dddd-dddddddddddd")
	plans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusActive},
	}
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, &fakeQuotaClient{}, &fakeTenantClient{
		bound: []ports.BoundTenant{
			{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Name: "a", DisplayName: "A", Status: ports.TenantStatusActive},
			{ID: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Name: "b", DisplayName: "B", Status: ports.TenantStatusFrozen},
		},
	})

	res, err := svc.ListTenantPlanBoundTenants(context.Background(), &tenantv1.ListTenantPlanBoundTenantsRequest{
		PlanId: planID.String(),
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(res.GetItems()) != 2 || res.GetItems()[0].GetName() != "a" || res.GetItems()[1].GetStatus() != "frozen" {
		t.Fatalf("items=%+v", res.GetItems())
	}

	_, err = svc.ListTenantPlanBoundTenants(context.Background(), &tenantv1.ListTenantPlanBoundTenantsRequest{
		PlanId: uuid.New().String(),
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestTenantPlanService_ListBindableTenants(t *testing.T) {
	planID := uuid.MustParse("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")
	plans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusActive},
	}
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, &fakeQuotaClient{}, &fakeTenantClient{
		bindable: []ports.BoundTenant{
			{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Name: "acme", DisplayName: "Acme", Status: ports.TenantStatusActive},
			{ID: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Name: "beta", DisplayName: "Beta", Status: ports.TenantStatusFrozen},
			{ID: uuid.MustParse("33333333-3333-3333-3333-333333333333"), Name: "gamma", DisplayName: "Gamma", Status: ports.TenantStatusActive},
		},
	})

	res, err := svc.ListBindableTenants(context.Background(), &tenantv1.ListBindableTenantsRequest{
		PlanId: planID.String(),
	})
	if err != nil {
		t.Fatalf("ListBindableTenants: %v", err)
	}
	if len(res.GetItems()) != 3 {
		t.Fatalf("items=%+v want 3", res.GetItems())
	}

	_, err = svc.ListBindableTenants(context.Background(), &tenantv1.ListBindableTenantsRequest{
		PlanId: uuid.New().String(),
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}

	emptyPlans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusActive},
	}
	emptyRes, err := NewTenantPlanService(emptyPlans, &fakeAuditStore{}, &fakeQuotaClient{}, &fakeTenantClient{}).
		ListBindableTenants(context.Background(), &tenantv1.ListBindableTenantsRequest{PlanId: planID.String()})
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if len(emptyRes.GetItems()) != 0 {
		t.Fatalf("empty items=%+v", emptyRes.GetItems())
	}

	_, err = svc.ListBindableTenants(context.Background(), &tenantv1.ListBindableTenantsRequest{PlanId: "not-a-uuid"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}
