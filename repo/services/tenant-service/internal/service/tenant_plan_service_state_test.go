package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
func (f *stateFakePlanStore) GetByID(context.Context, uuid.UUID) (ports.TenantPlan, error) {
	panic("unused")
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
	if f.bound[id] > 0 {
		return ports.ErrTenantPlanInUse
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
func (f *stateFakePlanStore) ListBoundTenants(context.Context, uuid.UUID) ([]ports.BoundTenant, error) {
	panic("unused")
}
func (f *stateFakePlanStore) ListBindableTenants(context.Context, uuid.UUID) ([]ports.BoundTenant, error) {
	panic("unused")
}
func (f *stateFakePlanStore) GetApprovedQuotaChanges(context.Context, uuid.UUID) ([]ports.ApprovedQuotaChange, error) {
	panic("unused")
}

func newStateSvc(plans *stateFakePlanStore, audit *fakeAuditStore) *TenantPlanService {
	if audit == nil {
		audit = &fakeAuditStore{}
	}
	return NewTenantPlanService(plans, audit, &fakeQuotaClient{})
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
