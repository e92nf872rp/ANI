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
)

type bindFakeTenantStore struct {
	tenant      ports.Tenant
	updateCalls int
	updatedPlan uuid.UUID
	updateErr   error
}

func (f *bindFakeTenantStore) GetByID(_ context.Context, id uuid.UUID) (ports.Tenant, error) {
	if f.tenant.ID == uuid.Nil || f.tenant.ID != id {
		return ports.Tenant{}, ports.ErrTenantNotFound
	}
	return f.tenant, nil
}

func (f *bindFakeTenantStore) UpdatePlan(_ context.Context, id uuid.UUID, planID uuid.UUID) (ports.Tenant, error) {
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

type bindFakePlanStore struct {
	plan     ports.TenantPlan
	limits   []ports.PlanQuotaLimit
	approved map[uuid.UUID][]ports.ApprovedQuotaChange
	bound    []ports.BoundTenant
	bindable []ports.BoundTenant
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
func (f *bindFakePlanStore) ListBoundTenants(_ context.Context, planID uuid.UUID) ([]ports.BoundTenant, error) {
	if f.plan.ID == uuid.Nil || f.plan.ID != planID {
		return nil, ports.ErrTenantPlanNotFound
	}
	return append([]ports.BoundTenant(nil), f.bound...), nil
}
func (f *bindFakePlanStore) ListBindableTenants(_ context.Context, planID uuid.UUID) ([]ports.BoundTenant, error) {
	if f.plan.ID == uuid.Nil || f.plan.ID != planID {
		return nil, ports.ErrTenantPlanNotFound
	}
	return append([]ports.BoundTenant(nil), f.bindable...), nil
}
func (f *bindFakePlanStore) GetApprovedQuotaChanges(_ context.Context, tenantID uuid.UUID) ([]ports.ApprovedQuotaChange, error) {
	return append([]ports.ApprovedQuotaChange(nil), f.approved[tenantID]...), nil
}

func TestTenantService_BindPlanQuota(t *testing.T) {
	planID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	oldPlan := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	tenantID := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	total := int64(16)

	tenants := &bindFakeTenantStore{
		tenant: ports.Tenant{ID: tenantID, Name: "acme", Status: ports.TenantStatusActive, PlanID: oldPlan},
	}
	plans := &bindFakePlanStore{
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
	audit := &fakeAuditStore{}
	svc := NewTenantService(tenants, plans, core, audit)

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
	if core.createCalls != 1 || core.putCalls != 0 {
		t.Fatalf("createCalls=%d putCalls=%d", core.createCalls, core.putCalls)
	}
	if tenants.updateCalls != 1 || tenants.updatedPlan != planID {
		t.Fatalf("UpdatePlan calls=%d plan=%s", tenants.updateCalls, tenants.updatedPlan)
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
	tenants := &bindFakeTenantStore{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusActive, PlanID: uuid.New()},
	}
	plans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusDraft},
	}
	svc := NewTenantService(tenants, plans, &fakeQuotaClient{}, &fakeAuditStore{})

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
	tenants := &bindFakeTenantStore{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusDisabled, PlanID: uuid.New()},
	}
	plans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusActive},
	}
	svc := NewTenantService(tenants, plans, &fakeQuotaClient{}, &fakeAuditStore{})

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

	tenants := &bindFakeTenantStore{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusActive, PlanID: oldPlan},
	}
	plans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusActive},
		limits: []ports.PlanQuotaLimit{
			{PlanID: planID, ResourceType: "gpu_count", Total: &total},
		},
	}
	core := &fakeQuotaClient{
		meta: []ports.QuotaMeta{
			{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"},
		},
	}
	audit := &fakeAuditStore{
		createFn: func(context.Context, ports.AuditLog) (uuid.UUID, error) {
			return uuid.Nil, errors.New("db down")
		},
	}
	svc := NewTenantService(tenants, plans, core, audit)

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
	if core.createCalls != 1 {
		t.Fatalf("createCalls=%d want 1", core.createCalls)
	}
}

func Test_BindPlanQuota_CoreFailRollsBackPlanID(t *testing.T) {
	planID := uuid.New()
	oldPlan := uuid.New()
	tenantID := uuid.New()
	total := int64(16)

	tenants := &bindFakeTenantStore{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusActive, PlanID: oldPlan},
	}
	plans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusActive},
		limits: []ports.PlanQuotaLimit{
			{PlanID: planID, ResourceType: "gpu_count", Total: &total},
		},
	}
	core := &fakeQuotaClient{
		meta: []ports.QuotaMeta{
			{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"},
		},
		createFn: func(context.Context, uuid.UUID, []ports.CoreQuotaItem) ([]ports.CoreQuotaResult, error) {
			return nil, ports.ErrCoreUnavailable
		},
	}
	audit := &fakeAuditStore{}
	svc := NewTenantService(tenants, plans, core, audit)

	_, err := svc.BindPlanQuota(context.Background(), &tenantv1.BindPlanQuotaRequest{
		TenantId: tenantID.String(),
		PlanId:   planID.String(),
	})
	if err == nil {
		t.Fatal("expected Core error")
	}
	if tenants.updateCalls != 2 {
		t.Fatalf("UpdatePlan calls=%d want 2 (bind + rollback)", tenants.updateCalls)
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

	tenants := &bindFakeTenantStore{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusActive, PlanID: planID}, // 同套餐 → 不 UpdatePlan
	}
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
	core := &fakeQuotaClient{
		meta: []ports.QuotaMeta{
			{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"},
			{ResourceType: "cpu_core", Enabled: true, DefaultQuota: 16, DisplayName: "CPU", Unit: "core"},
		},
		existing: map[uuid.UUID][]ports.CoreQuotaResult{
			tenantID: {{ResourceType: "cpu_core", Total: 16}},
		},
	}
	audit := &fakeAuditStore{}
	svc := NewTenantService(tenants, plans, core, audit)

	_, err := svc.BindPlanQuota(context.Background(), &tenantv1.BindPlanQuotaRequest{
		TenantId: tenantID.String(),
		PlanId:   planID.String(),
	})
	if err != nil {
		t.Fatalf("BindPlanQuota: %v", err)
	}
	if core.putCalls != 1 || len(core.putItems) != 1 || core.putItems[0].ResourceType != "cpu_core" {
		t.Fatalf("putCalls=%d putItems=%+v", core.putCalls, core.putItems)
	}
	if core.createCalls != 0 {
		t.Fatalf("createCalls=%d want 0", core.createCalls)
	}
	if tenants.updateCalls != 0 {
		t.Fatalf("same plan_id should skip UpdatePlan, calls=%d", tenants.updateCalls)
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
		bound: []ports.BoundTenant{
			{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Name: "a", DisplayName: "A", Status: ports.TenantStatusActive},
			{ID: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Name: "b", DisplayName: "B", Status: ports.TenantStatusFrozen},
		},
	}
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, &fakeQuotaClient{})

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
		bindable: []ports.BoundTenant{
			{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Name: "acme", DisplayName: "Acme", Status: ports.TenantStatusActive},
			{ID: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Name: "beta", DisplayName: "Beta", Status: ports.TenantStatusFrozen},
			{ID: uuid.MustParse("33333333-3333-3333-3333-333333333333"), Name: "gamma", DisplayName: "Gamma", Status: ports.TenantStatusActive},
		},
	}
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, &fakeQuotaClient{})

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
		plan:     ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusActive},
		bindable: nil,
	}
	emptyRes, err := NewTenantPlanService(emptyPlans, &fakeAuditStore{}, &fakeQuotaClient{}).
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
