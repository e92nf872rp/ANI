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
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// 租户（TenantService）service 层单测。

// bindFakePlanStore 供 BindPlanQuota 等租户域测试复用的最小套餐 store。
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
func (f *bindFakePlanStore) ListActivePlans(context.Context) ([]ports.AvailableTenantPlan, error) {
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
	svc := NewTenantService(plans, tenants, tenants, quota, nil, audit, nil, nil, nil)

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
	if quota.upsertCalls != 1 || quota.putCalls != 0 || quota.createCalls != 0 {
		t.Fatalf("upsertCalls=%d putCalls=%d createCalls=%d", quota.upsertCalls, quota.putCalls, quota.createCalls)
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
	svc := NewTenantService(plans, tenants, tenants, &fakeQuotaClient{}, nil, &fakeAuditStore{}, nil, nil, nil)

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
	svc := NewTenantService(plans, tenants, tenants, &fakeQuotaClient{}, nil, &fakeAuditStore{}, nil, nil, nil)

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
	svc := NewTenantService(plans, tenants, tenants, quota, nil, audit, nil, nil, nil)

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
	if quota.upsertCalls != 1 {
		t.Fatalf("upsertCalls=%d want 1", quota.upsertCalls)
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
		upsertFn: func(context.Context, uuid.UUID, []ports.CoreQuotaItem) ([]ports.CoreQuotaResult, error) {
			return nil, ports.ErrCoreUnavailable
		},
	}
	audit := &fakeAuditStore{}
	svc := NewTenantService(plans, tenants, tenants, quota, nil, audit, nil, nil, nil)

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
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusActive, PlanID: planID},
	}
	quota := &fakeQuotaClient{
		meta: []ports.QuotaMeta{
			{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"},
			{ResourceType: "cpu_core", Enabled: true, DefaultQuota: 16, DisplayName: "CPU", Unit: "core"},
		},
	}
	audit := &fakeAuditStore{}
	svc := NewTenantService(plans, tenants, tenants, quota, nil, audit, nil, nil, nil)

	_, err := svc.BindPlanQuota(context.Background(), &tenantv1.BindPlanQuotaRequest{
		TenantId: tenantID.String(),
		PlanId:   planID.String(),
	})
	if err != nil {
		t.Fatalf("BindPlanQuota: %v", err)
	}
	if quota.upsertCalls != 1 || len(quota.upsertItems) != 1 || quota.upsertItems[0].ResourceType != "cpu_core" {
		t.Fatalf("upsertCalls=%d upsertItems=%+v", quota.upsertCalls, quota.upsertItems)
	}
	if quota.putCalls != 0 || quota.createCalls != 0 {
		t.Fatalf("putCalls=%d createCalls=%d want 0", quota.putCalls, quota.createCalls)
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

func TestTenantService_GetTenantDetail_MinimalClosedLoop(t *testing.T) {
	tenantID := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	planID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{
			ID: tenantID, Name: "acme", DisplayName: "ACME",
			Status: ports.TenantStatusActive, PlanID: planID,
			ContactEmail: "ops@acme.io",
		},
	}
	svc := NewTenantService(nil, tenants, nil, nil, nil, nil, nil, nil, nil)

	res, err := svc.GetTenantDetail(context.Background(), &tenantv1.GetTenantDetailRequest{TenantId: tenantID.String()})
	if err != nil {
		t.Fatalf("GetTenantDetail: %v", err)
	}
	if res.GetId() != tenantID.String() || res.GetName() != "acme" || res.GetPlanId() != planID.String() {
		t.Fatalf("detail=%+v", res)
	}
	if res.GetContactEmail() == nil || res.GetContactEmail().GetValue() != "ops@acme.io" {
		t.Fatalf("contact_email=%v", res.GetContactEmail())
	}
	if res.GetPlanCode() != "" || res.GetUserCount() != 0 {
		t.Fatalf("minimal detail should omit plan_code/user_count, got plan_code=%q user_count=%d", res.GetPlanCode(), res.GetUserCount())
	}
}

func TestTenantService_GetTenantDetail_NotFound(t *testing.T) {
	svc := NewTenantService(nil, &fakeTenantClient{}, nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.GetTenantDetail(context.Background(), &tenantv1.GetTenantDetailRequest{
		TenantId: uuid.NewString(),
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code=%v err=%v", status.Code(err), err)
	}
	if !strings.HasPrefix(status.Convert(err).Message(), "TENANT_NOT_FOUND") {
		t.Fatalf("msg=%q", status.Convert(err).Message())
	}
}

func TestMapStoreError_TenantListCodes(t *testing.T) {
	cases := []struct {
		err  error
		code codes.Code
		msg  string
	}{
		{ports.ErrTenantNameConflict, codes.AlreadyExists, "TENANT_NAME_CONFLICT"},
		{ports.ErrTenantHasRunningResources, codes.FailedPrecondition, "TENANT_HAS_RUNNING_RESOURCES"},
		{ports.ErrTenantSsoConfigInvalid, codes.FailedPrecondition, "TENANT_SSO_CONFIG_INVALID"},
		{ports.ErrQuotaChangeRequestInvalid, codes.FailedPrecondition, "QUOTA_CHANGE_REQUEST_INVALID"},
		{ports.ErrQuotaChangeRequestNotPending, codes.FailedPrecondition, "QUOTA_CHANGE_REQUEST_NOT_PENDING"},
		{ports.ErrQuotaChangeRequestNotFound, codes.NotFound, "QUOTA_CHANGE_REQUEST_NOT_FOUND"},
		{ports.ErrNotImplemented, codes.Unimplemented, "NOT_IMPLEMENTED"},
	}
	for _, tc := range cases {
		mapped := mapStoreError(tc.err)
		if status.Code(mapped) != tc.code {
			t.Fatalf("%v: code=%v want %v", tc.err, status.Code(mapped), tc.code)
		}
		if !strings.HasPrefix(status.Convert(mapped).Message(), tc.msg) {
			t.Fatalf("%v: msg=%q want prefix %q", tc.err, status.Convert(mapped).Message(), tc.msg)
		}
	}
}

type availablePlansFakeStore struct {
	bindFakePlanStore
	items []ports.AvailableTenantPlan
}

func (f *availablePlansFakeStore) ListActivePlans(context.Context) ([]ports.AvailableTenantPlan, error) {
	return append([]ports.AvailableTenantPlan(nil), f.items...), nil
}

func TestTenantService_ListAvailablePlans_OnlyActive(t *testing.T) {
	activeID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	plans := &availablePlansFakeStore{
		items: []ports.AvailableTenantPlan{
			{ID: activeID, Code: "pro", Name: "Pro"},
		},
	}
	svc := NewTenantService(plans, nil, nil, nil, nil, nil, nil, nil, nil)
	res, err := svc.ListAvailablePlans(context.Background(), &tenantv1.ListAvailablePlansRequest{})
	if err != nil {
		t.Fatalf("ListAvailablePlans: %v", err)
	}
	if len(res.GetItems()) != 1 || res.GetItems()[0].GetCode() != "pro" {
		t.Fatalf("items=%v", res.GetItems())
	}
}

func TestTenantService_ListAvailablePlans_Empty(t *testing.T) {
	svc := NewTenantService(&availablePlansFakeStore{}, nil, nil, nil, nil, nil, nil, nil, nil)
	res, err := svc.ListAvailablePlans(context.Background(), &tenantv1.ListAvailablePlansRequest{})
	if err != nil {
		t.Fatalf("ListAvailablePlans: %v", err)
	}
	if res.GetItems() == nil {
		t.Fatal("items should be empty slice not nil for JSON []")
	}
	if len(res.GetItems()) != 0 {
		t.Fatalf("len=%d", len(res.GetItems()))
	}
}

func TestValidateAdminPassword(t *testing.T) {
	cases := []struct {
		name    string
		pwd     string
		wantErr bool
	}{
		{"too_short", "Ab1!", true},
		{"too_long", strings.Repeat("Aa1!", 20), true}, // 80 chars
		{"two_classes", "Abcdefgh", true},
		{"three_classes", "Abcdefg1", false},
		{"three_with_special", "Abcdefg!", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAdminPassword(tc.pwd)
			if tc.wantErr && err == nil {
				t.Fatal("want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected: %v", err)
			}
		})
	}
}

func TestTenantService_CreateTenant_NilRequest(t *testing.T) {
	svc := NewTenantService(&bindFakePlanStore{}, &fakeTenantClient{}, nil, &fakeQuotaClient{}, nil, &fakeAuditStore{}, nil, nil, nil)
	_, err := svc.CreateTenant(context.Background(), nil)
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(status.Convert(err).Message(), "request required") {
		t.Fatalf("err=%v", err)
	}
}

func TestTenantService_CreateTenant_Success(t *testing.T) {
	prev := enablePutQuotaRetry
	enablePutQuotaRetry = false
	defer func() { enablePutQuotaRetry = prev }()

	planID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	actorID := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	total := int64(8)
	plans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Code: "pro", Status: ports.TenantPlanStatusActive},
		limits: []ports.PlanQuotaLimit{
			{PlanID: planID, ResourceType: "gpu_count", Total: &total},
		},
	}
	tenants := &fakeTenantClient{}
	quota := &fakeQuotaClient{
		meta: []ports.QuotaMeta{
			{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card"},
		},
	}
	audit := &fakeAuditStore{}
	svc := NewTenantService(plans, tenants, nil, quota, nil, audit, nil, nil, nil)

	mdCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"x-request-id", "req_create-tenant-1",
		"x-user-id", actorID.String(),
	))
	res, err := svc.CreateTenant(mdCtx, &tenantv1.CreateTenantRequest{
		Name: "acme-co", DisplayName: "Acme", Email: "ops@acme.io",
		PlanId: planID.String(), AdminEmail: "admin@acme.io", AdminName: "acme_admin",
		AdminPassword: "Abcdefg1",
	})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if res.GetId() == "" || strings.Contains(strings.ToLower(res.GetMessage()), "password") {
		t.Fatalf("response=%+v", res)
	}
	if tenants.createCalls != 1 || tenants.createIn == nil {
		t.Fatalf("createCalls=%d", tenants.createCalls)
	}
	if tenants.createIn.AdminPasswordHash == "" || tenants.createIn.AdminPasswordHash == "Abcdefg1" {
		t.Fatal("password must be bcrypt hash, never plaintext")
	}
	if tenants.createIn.RequestID != "req_create-tenant-1" {
		t.Fatalf("RequestID=%q", tenants.createIn.RequestID)
	}
	if tenants.createIn.ActorUserID != actorID.String() {
		t.Fatalf("ActorUserID=%q, want BOSS operator", tenants.createIn.ActorUserID)
	}
	if quota.upsertCalls != 1 {
		t.Fatalf("upsertCalls=%d", quota.upsertCalls)
	}
	if len(audit.logs) == 0 || audit.logs[0].Action != "tenant.create" || audit.logs[0].Result != "success" {
		t.Fatalf("audit=%+v", audit.logs)
	}
	if _, ok := audit.logs[0].Details["admin_password"]; ok {
		t.Fatal("audit must not contain password")
	}
}

func TestTenantService_CreateTenant_PlanNotActive(t *testing.T) {
	planID := uuid.New()
	plans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusDraft},
	}
	svc := NewTenantService(plans, &fakeTenantClient{}, nil, &fakeQuotaClient{}, nil, &fakeAuditStore{}, nil, nil, nil)
	_, err := svc.CreateTenant(context.Background(), &tenantv1.CreateTenantRequest{
		Name: "acme-co", DisplayName: "Acme", Email: "ops@acme.io",
		PlanId: planID.String(), AdminEmail: "admin@acme.io", AdminName: "acme_admin",
		AdminPassword: "Abcdefg1",
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "PLAN_NOT_ACTIVE") {
		t.Fatalf("err=%v", err)
	}
}

func TestTenantService_CreateTenant_NameConflict(t *testing.T) {
	planID := uuid.New()
	total := int64(1)
	plans := &bindFakePlanStore{
		plan:   ports.TenantPlan{ID: planID, Status: ports.TenantPlanStatusActive},
		limits: []ports.PlanQuotaLimit{{PlanID: planID, ResourceType: "gpu_count", Total: &total}},
	}
	tenants := &fakeTenantClient{
		createFn: func(context.Context, ports.CreateTenantInput) (ports.Tenant, error) {
			return ports.Tenant{}, ports.ErrTenantNameConflict
		},
	}
	quota := &fakeQuotaClient{
		meta: []ports.QuotaMeta{{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 1}},
	}
	svc := NewTenantService(plans, tenants, nil, quota, nil, &fakeAuditStore{}, nil, nil, nil)
	_, err := svc.CreateTenant(context.Background(), &tenantv1.CreateTenantRequest{
		Name: "acme-co", DisplayName: "Acme", Email: "ops@acme.io",
		PlanId: planID.String(), AdminEmail: "admin@acme.io", AdminName: "acme_admin",
		AdminPassword: "Abcdefg1",
	})
	if status.Code(err) != codes.AlreadyExists || !strings.HasPrefix(status.Convert(err).Message(), "TENANT_NAME_CONFLICT") {
		t.Fatalf("err=%v", err)
	}
}
