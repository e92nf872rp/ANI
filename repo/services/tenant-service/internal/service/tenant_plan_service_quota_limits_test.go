package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type quotaLimitsFakeStore struct {
	plan        ports.TenantPlan
	limits      []ports.PlanQuotaLimit
	updateCalls int
	updated     []ports.PlanQuotaLimitInput
	updateErr   error
	tenants     []ports.BoundTenant
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
func (f *quotaLimitsFakeStore) ListBoundTenants(context.Context, uuid.UUID) ([]ports.BoundTenant, error) {
	return append([]ports.BoundTenant(nil), f.tenants...), nil
}
func (f *quotaLimitsFakeStore) ListBindableTenants(context.Context, uuid.UUID) ([]ports.BoundTenant, error) {
	return nil, nil
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
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, core)

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
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, core)

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
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, core)

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
		tenants: []ports.BoundTenant{
			{ID: tenantA, Name: "a", Status: ports.TenantStatusActive},
			{ID: tenantB, Name: "b", Status: ports.TenantStatusActive},
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
	svc := NewTenantPlanService(plans, audit, core)

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
		tenants: []ports.BoundTenant{{ID: tenantID, Name: "t", Status: ports.TenantStatusActive}},
	}
	audit := &fakeAuditStore{}
	core := &fakeQuotaClient{
		meta:         []ports.QuotaMeta{{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"}},
		putTightened: true,
		existing: map[uuid.UUID][]ports.CoreQuotaResult{
			tenantID: {{ResourceType: "gpu_count", Total: 4}},
		},
	}
	svc := NewTenantPlanService(plans, audit, core)

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
		tenants: []ports.BoundTenant{{ID: tenantID, Name: "t", Status: ports.TenantStatusActive}},
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
	svc := NewTenantPlanService(plans, audit, core)

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
		tenants: []ports.BoundTenant{{ID: tenantID, Name: "t", Status: ports.TenantStatusActive}},
	}
	core := &fakeQuotaClient{
		meta: []ports.QuotaMeta{{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"}},
		// GetQuota 空列表 → CreateQuota
	}
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, core)

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
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, core)

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
