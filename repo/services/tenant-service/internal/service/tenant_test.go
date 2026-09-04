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
func (f *bindFakePlanStore) MapPlanCodes(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	for _, id := range ids {
		if f.plan.ID != uuid.Nil && f.plan.ID == id {
			out[id] = f.plan.Code
		}
	}
	return out, nil
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

func TestTenantService_GetTenantDetail_FullFields(t *testing.T) {
	tenantID := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	planID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	frozen := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{
			ID: tenantID, Name: "acme", DisplayName: "ACME",
			Status: ports.TenantStatusFrozen, PlanID: planID,
			ContactEmail: "ops@acme.io",
			UserCount:    5,
			AdminCount:   2,
			Auth:         &ports.TenantAuthSummary{SsoEnabled: true, MfaRequired: true},
			FrozenAt:     &frozen,
			CreatedAt:    frozen,
			UpdatedAt:    frozen,
		},
	}
	plans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Code: "pro", Status: ports.TenantPlanStatusActive},
	}
	svc := NewTenantService(plans, tenants, nil, nil, nil, nil, nil, nil, nil)

	res, err := svc.GetTenantDetail(context.Background(), &tenantv1.GetTenantDetailRequest{TenantId: tenantID.String()})
	if err != nil {
		t.Fatalf("GetTenantDetail: %v", err)
	}
	if res.GetPlanCode() != "pro" || res.GetUserCount() != 5 || res.GetAdminCount() != 2 {
		t.Fatalf("detail=%+v", res)
	}
	if res.GetAuth() == nil || !res.GetAuth().GetSsoEnabled() || !res.GetAuth().GetMfaRequired() {
		t.Fatalf("auth=%v", res.GetAuth())
	}
	if res.GetFrozenAt() == nil {
		t.Fatal("frozen_at expected")
	}
}

func TestTenantService_GetTenantDetail_AuthDefaults(t *testing.T) {
	tenantID := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	planID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{
			ID: tenantID, Name: "acme", DisplayName: "ACME",
			Status: ports.TenantStatusActive, PlanID: planID,
		},
	}
	svc := NewTenantService(&bindFakePlanStore{plan: ports.TenantPlan{ID: planID, Code: "starter"}}, tenants, nil, nil, nil, nil, nil, nil, nil)
	res, err := svc.GetTenantDetail(context.Background(), &tenantv1.GetTenantDetailRequest{TenantId: tenantID.String()})
	if err != nil {
		t.Fatalf("GetTenantDetail: %v", err)
	}
	if res.GetAuth() == nil || res.GetAuth().GetSsoEnabled() || res.GetAuth().GetMfaRequired() {
		t.Fatalf("auth defaults=%v", res.GetAuth())
	}
}

func TestTenantService_ListTenants_AssemblesPlanCode(t *testing.T) {
	planID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	tenantID := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	tenants := &fakeTenantClient{
		listItems: []ports.TenantListItem{{
			ID: tenantID, Name: "acme", DisplayName: "ACME",
			Status: ports.TenantStatusActive, PlanID: planID, AdminCount: 2,
			CreatedAt: time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC),
		}},
	}
	plans := &bindFakePlanStore{
		plan: ports.TenantPlan{ID: planID, Code: "pro", Status: ports.TenantPlanStatusActive},
	}
	svc := NewTenantService(plans, tenants, nil, nil, nil, nil, nil, nil, nil)
	res, err := svc.ListTenants(context.Background(), &tenantv1.ListTenantsRequest{})
	if err != nil {
		t.Fatalf("ListTenants: %v", err)
	}
	if len(res.GetItems()) != 1 || res.GetItems()[0].GetPlanCode() != "pro" || res.GetItems()[0].GetAdminCount() != 2 {
		t.Fatalf("items=%v", res.GetItems())
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
	if len(audit.logs) == 0 || audit.logs[0].RequestID != "req_create-tenant-1" {
		t.Fatalf("expected audit RequestID from gateway metadata, got %+v", audit.logs)
	}
	if len(audit.logs) == 0 || audit.logs[0].UserID == nil || *audit.logs[0].UserID != actorID {
		t.Fatalf("expected audit UserID from gateway metadata, got %+v", audit.logs)
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

func TestTenantService_UpdateTenant_EmptyRejected(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Name: "acme", DisplayName: "Acme", Status: ports.TenantStatusActive, ContactEmail: "ops@acme.io"},
	}
	svc := NewTenantService(nil, tenants, nil, nil, nil, &fakeAuditStore{}, nil, nil, nil)
	_, err := svc.UpdateTenant(context.Background(), &tenantv1.UpdateTenantRequest{TenantId: tenantID.String()})
	if status.Code(err) != codes.InvalidArgument || !strings.HasPrefix(status.Convert(err).Message(), "VALIDATION_FAILED") {
		t.Fatalf("err=%v", err)
	}
	if tenants.updateTenantCalls != 0 {
		t.Fatalf("UpdateTenant should not be called, got %d", tenants.updateTenantCalls)
	}
}

func TestTenantService_UpdateTenant_DisabledRejected(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{
			ID: tenantID, Name: "acme", DisplayName: "Acme",
			Status: ports.TenantStatusDisabled, ContactEmail: "ops@acme.io",
		},
	}
	svc := NewTenantService(nil, tenants, nil, nil, nil, &fakeAuditStore{}, nil, nil, nil)
	dn := "New Name"
	_, err := svc.UpdateTenant(context.Background(), &tenantv1.UpdateTenantRequest{
		TenantId:    tenantID.String(),
		DisplayName: wrapperspb.String(dn),
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "TENANT_STATE_INVALID") {
		t.Fatalf("err=%v", err)
	}
	if tenants.updateTenantCalls != 0 {
		t.Fatalf("UpdateTenant should not be called on disabled tenant")
	}
}

func TestTenantService_UpdateTenant_PartialFields(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{
			ID: tenantID, Name: "acme", DisplayName: "Acme",
			Status: ports.TenantStatusActive, ContactEmail: "ops@acme.io", PlanID: uuid.New(),
		},
	}
	audit := &fakeAuditStore{}
	svc := NewTenantService(nil, tenants, nil, nil, nil, audit, nil, nil, nil)

	res, err := svc.UpdateTenant(context.Background(), &tenantv1.UpdateTenantRequest{
		TenantId:     tenantID.String(),
		DisplayName:  wrapperspb.String("Acme Corp"),
		ContactEmail: wrapperspb.String("new@acme.io"),
	})
	if err != nil {
		t.Fatalf("UpdateTenant: %v", err)
	}
	if res.GetId() != tenantID.String() || res.GetMessage() == "" {
		t.Fatalf("response=%+v", res)
	}
	if tenants.updateTenantCalls != 1 || tenants.updateTenantIn == nil {
		t.Fatalf("update calls=%d in=%v", tenants.updateTenantCalls, tenants.updateTenantIn)
	}
	if tenants.updateTenantIn.DisplayName == nil || *tenants.updateTenantIn.DisplayName != "Acme Corp" {
		t.Fatalf("display_name=%v", tenants.updateTenantIn.DisplayName)
	}
	if tenants.updateTenantIn.ContactEmail == nil || *tenants.updateTenantIn.ContactEmail != "new@acme.io" {
		t.Fatalf("contact_email=%v", tenants.updateTenantIn.ContactEmail)
	}
	// name / status 不可经 UpdateTenantInput 改写（仅两可选字段）
	if tenants.tenant.Name != "acme" || tenants.tenant.Status != ports.TenantStatusActive {
		t.Fatalf("name/status mutated: %+v", tenants.tenant)
	}
	if len(audit.logs) == 0 || audit.logs[0].Action != "tenant.update" || audit.logs[0].Result != "success" {
		t.Fatalf("audit=%+v", audit.logs)
	}
	before, _ := audit.logs[0].Details["before"].(map[string]any)
	after, _ := audit.logs[0].Details["after"].(map[string]any)
	if before["display_name"] != "Acme" || after["display_name"] != "Acme Corp" {
		t.Fatalf("audit before/after=%v / %v", before, after)
	}
}

func TestTenantService_UpdateTenant_DisplayNameOnly(t *testing.T) {
	tenantID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{
			ID: tenantID, Name: "beta", DisplayName: "Beta",
			Status: ports.TenantStatusFrozen, ContactEmail: "ops@beta.io", PlanID: uuid.New(),
		},
	}
	svc := NewTenantService(nil, tenants, nil, nil, nil, &fakeAuditStore{}, nil, nil, nil)
	_, err := svc.UpdateTenant(context.Background(), &tenantv1.UpdateTenantRequest{
		TenantId:    tenantID.String(),
		DisplayName: wrapperspb.String("Beta Inc"),
	})
	if err != nil {
		t.Fatalf("UpdateTenant: %v", err)
	}
	if tenants.updateTenantIn == nil || tenants.updateTenantIn.ContactEmail != nil {
		t.Fatalf("want contact_email unset, got %+v", tenants.updateTenantIn)
	}
	if tenants.tenant.ContactEmail != "ops@beta.io" || tenants.tenant.DisplayName != "Beta Inc" {
		t.Fatalf("tenant=%+v", tenants.tenant)
	}
}

func TestTenantService_FreezeTenant_Success(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Name: "acme", Status: ports.TenantStatusActive},
	}
	audit := &fakeAuditStore{}
	svc := NewTenantService(nil, tenants, nil, nil, nil, audit, nil, nil, nil)
	res, err := svc.FreezeTenant(context.Background(), &tenantv1.FreezeTenantRequest{TenantId: tenantID.String()})
	if err != nil {
		t.Fatalf("FreezeTenant: %v", err)
	}
	if res.GetId() != tenantID.String() || tenants.tenant.Status != ports.TenantStatusFrozen {
		t.Fatalf("res=%+v tenant=%+v", res, tenants.tenant)
	}
	if len(audit.logs) == 0 || audit.logs[0].Action != "tenant.freeze" || audit.logs[0].Result != "success" {
		t.Fatalf("audit=%+v", audit.logs)
	}
	if audit.logs[0].Details["before_status"] != "active" || audit.logs[0].Details["after_status"] != "frozen" {
		t.Fatalf("details=%v", audit.logs[0].Details)
	}
}

func TestTenantService_FreezeTenant_StateInvalid(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusFrozen},
	}
	svc := NewTenantService(nil, tenants, nil, nil, nil, &fakeAuditStore{}, nil, nil, nil)
	_, err := svc.FreezeTenant(context.Background(), &tenantv1.FreezeTenantRequest{TenantId: tenantID.String()})
	if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "TENANT_STATE_INVALID") {
		t.Fatalf("err=%v", err)
	}
}

func TestTenantService_UnfreezeTenant_Success(t *testing.T) {
	tenantID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusFrozen},
	}
	svc := NewTenantService(nil, tenants, nil, nil, nil, &fakeAuditStore{}, nil, nil, nil)
	_, err := svc.UnfreezeTenant(context.Background(), &tenantv1.UnfreezeTenantRequest{TenantId: tenantID.String()})
	if err != nil {
		t.Fatalf("UnfreezeTenant: %v", err)
	}
	if tenants.tenant.Status != ports.TenantStatusActive {
		t.Fatalf("status=%s", tenants.tenant.Status)
	}
}

func TestTenantService_DisableTenant_UsedBlocked(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusActive},
	}
	quota := &fakeQuotaClient{
		getFn: func(context.Context, uuid.UUID) ([]ports.CoreQuotaResult, error) {
			return []ports.CoreQuotaResult{
				{ResourceType: "gpu_count", Used: 1, Reserved: 0, Total: 8},
				{ResourceType: "token_count", Used: 0, Total: 100},
			}, nil
		},
	}
	svc := NewTenantService(nil, tenants, nil, quota, nil, &fakeAuditStore{}, nil, nil, nil)
	_, err := svc.DisableTenant(context.Background(), &tenantv1.DisableTenantRequest{TenantId: tenantID.String()})
	if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "TENANT_HAS_RUNNING_RESOURCES") {
		t.Fatalf("err=%v", err)
	}
	if tenants.disableCalls != 0 {
		t.Fatal("Core DisableTenant must not be called when used+reserved>0")
	}
}

func TestTenantService_DisableTenant_ReservedBlocked(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusActive},
	}
	quota := &fakeQuotaClient{
		getFn: func(context.Context, uuid.UUID) ([]ports.CoreQuotaResult, error) {
			return []ports.CoreQuotaResult{
				{ResourceType: "gpu_count", Used: 0, Reserved: 2, Total: 8},
				{ResourceType: "cpu_core", Used: 0, Reserved: 0, Total: 16},
				{ResourceType: "memory_gb", Used: 0, Reserved: 0, Total: 32},
				{ResourceType: "storage_gb", Used: 0, Reserved: 0, Total: 100},
			}, nil
		},
	}
	svc := NewTenantService(nil, tenants, nil, quota, nil, &fakeAuditStore{}, nil, nil, nil)
	_, err := svc.DisableTenant(context.Background(), &tenantv1.DisableTenantRequest{TenantId: tenantID.String()})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(status.Convert(err).Message(), "used+reserved > 0") {
		t.Fatalf("err=%v", err)
	}
	if tenants.disableCalls != 0 {
		t.Fatal("Core DisableTenant must not be called when reserved>0")
	}
}

func TestTenantService_DisableTenant_OtherDimUsedAllowed(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusActive},
	}
	quota := &fakeQuotaClient{
		getFn: func(context.Context, uuid.UUID) ([]ports.CoreQuotaResult, error) {
			return []ports.CoreQuotaResult{
				{ResourceType: "gpu_count", Used: 0, Reserved: 0, Total: 8},
				{ResourceType: "cpu_core", Used: 0, Reserved: 0, Total: 16},
				{ResourceType: "memory_gb", Used: 0, Reserved: 0, Total: 32},
				{ResourceType: "storage_gb", Used: 0, Reserved: 0, Total: 100},
				{ResourceType: "token_count", Used: 999, Reserved: 1, Total: 1000}, // 非守卫维度
			}, nil
		},
	}
	audit := &fakeAuditStore{}
	svc := NewTenantService(nil, tenants, nil, quota, nil, audit, nil, nil, nil)
	res, err := svc.DisableTenant(context.Background(), &tenantv1.DisableTenantRequest{TenantId: tenantID.String()})
	if err != nil {
		t.Fatalf("DisableTenant: %v", err)
	}
	if res.GetMessage() == "" || tenants.tenant.Status != ports.TenantStatusDisabled {
		t.Fatalf("res=%+v status=%s", res, tenants.tenant.Status)
	}
	if tenants.disableCalls != 1 {
		t.Fatalf("disableCalls=%d", tenants.disableCalls)
	}
	if len(audit.logs) == 0 || audit.logs[0].Action != "tenant.disable" {
		t.Fatalf("audit=%+v", audit.logs)
	}
}

func TestTenantService_DisableTenant_AllGuardZero(t *testing.T) {
	tenantID := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusFrozen},
	}
	quota := &fakeQuotaClient{
		existing: map[uuid.UUID][]ports.CoreQuotaResult{
			tenantID: {
				{ResourceType: "gpu_count", Used: 0, Reserved: 0},
				{ResourceType: "cpu_core", Used: 0, Reserved: 0},
				{ResourceType: "memory_gb", Used: 0, Reserved: 0},
				{ResourceType: "storage_gb", Used: 0, Reserved: 0},
			},
		},
	}
	svc := NewTenantService(nil, tenants, nil, quota, nil, &fakeAuditStore{}, nil, nil, nil)
	_, err := svc.DisableTenant(context.Background(), &tenantv1.DisableTenantRequest{TenantId: tenantID.String()})
	if err != nil {
		t.Fatalf("DisableTenant: %v", err)
	}
	if tenants.tenant.Status != ports.TenantStatusDisabled {
		t.Fatalf("status=%s", tenants.tenant.Status)
	}
}

func TestTenantService_GetTenantAuth_Defaults(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	tenants := &fakeTenantClient{} // auth.TenantID Nil → defaults
	svc := NewTenantService(nil, tenants, nil, nil, nil, &fakeAuditStore{}, nil, nil, nil)
	got, err := svc.GetTenantAuth(context.Background(), &tenantv1.GetTenantAuthRequest{TenantId: tenantID.String()})
	if err != nil {
		t.Fatalf("GetTenantAuth: %v", err)
	}
	if got.GetSsoEnabled() || got.GetMfaRequired() || got.GetProvider() != nil {
		t.Fatalf("defaults=%+v", got)
	}
}

func TestTenantService_UpdateTenantSso_RequiresProvider(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusActive},
		auth:   ports.TenantAuth{TenantID: tenantID, SsoEnabled: false},
	}
	audit := &fakeAuditStore{}
	svc := NewTenantService(nil, tenants, nil, nil, nil, audit, nil, nil, nil)
	_, err := svc.UpdateTenantSso(context.Background(), &tenantv1.UpdateTenantSsoRequest{
		TenantId:   tenantID.String(),
		SsoEnabled: wrapperspb.Bool(true),
	})
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition || !strings.Contains(st.Message(), "TENANT_SSO_CONFIG_INVALID") {
		t.Fatalf("want TENANT_SSO_CONFIG_INVALID, got %v", err)
	}
	if tenants.updateAuthCalls != 0 {
		t.Fatalf("must not call Core on invalid SSO, calls=%d", tenants.updateAuthCalls)
	}
	if len(audit.logs) == 0 || audit.logs[0].Result != "failure" {
		t.Fatalf("audit=%+v", audit.logs)
	}
}

func TestTenantService_UpdateTenantSso_TenantDisabledAsStateInvalid(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusDisabled},
		auth:   ports.TenantAuth{TenantID: tenantID, SsoEnabled: false},
	}
	svc := NewTenantService(nil, tenants, nil, nil, nil, &fakeAuditStore{}, nil, nil, nil)
	_, err := svc.UpdateTenantSso(context.Background(), &tenantv1.UpdateTenantSsoRequest{
		TenantId:   tenantID.String(),
		SsoEnabled: wrapperspb.Bool(true),
		Provider:   wrapperspb.String("oidc"),
	})
	// Core 层判断 disabled → TENANT_STATE_INVALID，svc 透传 409
	if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "TENANT_STATE_INVALID") {
		t.Fatalf("err=%v", err)
	}
	if tenants.updateAuthCalls != 1 {
		t.Fatalf("must reach Core UpdateTenantAuth, calls=%d", tenants.updateAuthCalls)
	}
}

func TestTenantService_UpdateTenantSso_DisableWithoutProvider(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	provider := "oidc"
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusActive},
		auth:   ports.TenantAuth{TenantID: tenantID, SsoEnabled: true, SsoProvider: &provider},
	}
	svc := NewTenantService(nil, tenants, nil, nil, nil, &fakeAuditStore{}, nil, nil, nil)
	res, err := svc.UpdateTenantSso(context.Background(), &tenantv1.UpdateTenantSsoRequest{
		TenantId:   tenantID.String(),
		SsoEnabled: wrapperspb.Bool(false),
	})
	if err != nil {
		t.Fatalf("disable SSO without provider: %v", err)
	}
	if res.GetId() != tenantID.String() || tenants.auth.SsoEnabled {
		t.Fatalf("res=%+v auth=%+v", res, tenants.auth)
	}
	if tenants.updateAuthIn == nil || tenants.updateAuthIn.SsoProvider != nil {
		t.Fatalf("provider must not be required/patched on disable-only, patch=%+v", tenants.updateAuthIn)
	}
}

func TestTenantService_UpdateTenantSso_ProviderWhileSSOOff(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusFrozen},
		auth:   ports.TenantAuth{TenantID: tenantID, SsoEnabled: false},
	}
	svc := NewTenantService(nil, tenants, nil, nil, nil, &fakeAuditStore{}, nil, nil, nil)
	_, err := svc.UpdateTenantSso(context.Background(), &tenantv1.UpdateTenantSsoRequest{
		TenantId: tenantID.String(),
		Provider: wrapperspb.String("oidc"),
	})
	if err != nil {
		t.Fatalf("set provider while SSO off: %v", err)
	}
	if tenants.auth.SsoProvider == nil || *tenants.auth.SsoProvider != "oidc" || tenants.auth.SsoEnabled {
		t.Fatalf("auth=%+v", tenants.auth)
	}
}

func TestTenantService_UpdateTenantSso_KeepExistingProvider(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	provider := "oidc"
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusActive},
		auth:   ports.TenantAuth{TenantID: tenantID, SsoEnabled: false, SsoProvider: &provider},
	}
	svc := NewTenantService(nil, tenants, nil, nil, nil, &fakeAuditStore{}, nil, nil, nil)
	res, err := svc.UpdateTenantSso(context.Background(), &tenantv1.UpdateTenantSsoRequest{
		TenantId:   tenantID.String(),
		SsoEnabled: wrapperspb.Bool(true),
	})
	if err != nil {
		t.Fatalf("UpdateTenantSso: %v", err)
	}
	if res.GetId() != tenantID.String() || !tenants.auth.SsoEnabled {
		t.Fatalf("res=%+v auth=%+v", res, tenants.auth)
	}
}

func TestTenantService_UpdateTenantMfa_TenantDisabledAsStateInvalid(t *testing.T) {
	tenantID := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusDisabled},
		auth:   ports.TenantAuth{TenantID: tenantID},
	}
	svc := NewTenantService(nil, tenants, nil, nil, nil, &fakeAuditStore{}, nil, nil, nil)
	_, err := svc.UpdateTenantMfa(context.Background(), &tenantv1.UpdateTenantMfaRequest{
		TenantId:    tenantID.String(),
		MfaRequired: true,
	})
	if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "TENANT_STATE_INVALID") {
		t.Fatalf("err=%v", err)
	}
	if tenants.updateAuthCalls != 1 {
		t.Fatalf("must reach Core UpdateTenantAuth, calls=%d", tenants.updateAuthCalls)
	}
}

func TestTenantService_Auth_ReadWriteRoundTrip(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	beforeAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	tenants := &fakeTenantClient{
		tenant: ports.Tenant{ID: tenantID, Status: ports.TenantStatusActive},
		auth:   ports.TenantAuth{TenantID: tenantID, UpdatedAt: beforeAt},
	}
	audit := &fakeAuditStore{}
	svc := NewTenantService(nil, tenants, nil, nil, nil, audit, nil, nil, nil)

	got, err := svc.GetTenantAuth(context.Background(), &tenantv1.GetTenantAuthRequest{TenantId: tenantID.String()})
	if err != nil || got.GetSsoEnabled() || got.GetMfaRequired() {
		t.Fatalf("initial get: err=%v got=%+v", err, got)
	}

	_, err = svc.UpdateTenantSso(context.Background(), &tenantv1.UpdateTenantSsoRequest{
		TenantId:   tenantID.String(),
		SsoEnabled: wrapperspb.Bool(true),
		Provider:   wrapperspb.String("oidc"),
	})
	if err != nil {
		t.Fatalf("UpdateTenantSso: %v", err)
	}
	_, err = svc.UpdateTenantMfa(context.Background(), &tenantv1.UpdateTenantMfaRequest{
		TenantId:    tenantID.String(),
		MfaRequired: true,
	})
	if err != nil {
		t.Fatalf("UpdateTenantMfa: %v", err)
	}

	after, err := svc.GetTenantAuth(context.Background(), &tenantv1.GetTenantAuthRequest{TenantId: tenantID.String()})
	if err != nil {
		t.Fatalf("GetTenantAuth after: %v", err)
	}
	if !after.GetSsoEnabled() || !after.GetMfaRequired() {
		t.Fatalf("after=%+v", after)
	}
	if after.GetProvider() == nil || after.GetProvider().GetValue() != "oidc" {
		t.Fatalf("provider=%v", after.GetProvider())
	}
	if !after.GetUpdatedAt().AsTime().After(beforeAt) {
		t.Fatalf("updated_at not advanced: %v", after.GetUpdatedAt())
	}

	actions := map[string]int{}
	for _, log := range audit.logs {
		if log.Result == "success" {
			actions[log.Action]++
		}
	}
	if actions["tenant.sso.update"] != 1 || actions["tenant.mfa.update"] != 1 {
		t.Fatalf("audit actions=%v logs=%+v", actions, audit.logs)
	}
}
