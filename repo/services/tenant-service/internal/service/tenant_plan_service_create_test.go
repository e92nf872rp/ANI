package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

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
func (f *fakePlanStore) ListBoundTenants(context.Context, uuid.UUID) ([]ports.BoundTenant, error) {
	panic("unused")
}
func (f *fakePlanStore) ListBindableTenants(context.Context, uuid.UUID) ([]ports.BoundTenant, error) {
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
	return NewTenantPlanService(plans, audit, core)
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
	svc := NewTenantPlanService(plans, audit, &fakeQuotaClient{meta: nil})

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
