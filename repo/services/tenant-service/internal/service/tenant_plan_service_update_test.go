package service

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

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
func (f *updateFakePlanStore) ListBoundTenants(context.Context, uuid.UUID) ([]ports.BoundTenant, error) {
	panic("unused")
}
func (f *updateFakePlanStore) ListBindableTenants(context.Context, uuid.UUID) ([]ports.BoundTenant, error) {
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
	svc := NewTenantPlanService(plans, audit, &fakeQuotaClient{})

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
	res, err = svc.UpdateTenantPlan(context.Background(), &tenantv1.UpdateTenantPlanRequest{
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
