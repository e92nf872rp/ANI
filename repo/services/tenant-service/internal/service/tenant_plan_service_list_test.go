package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
func (f *listFakePlanStore) ListBoundTenants(context.Context, uuid.UUID) ([]ports.BoundTenant, error) {
	panic("unused")
}
func (f *listFakePlanStore) ListBindableTenants(context.Context, uuid.UUID) ([]ports.BoundTenant, error) {
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
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, &fakeQuotaClient{})

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
	svc := NewTenantPlanService(&listFakePlanStore{}, &fakeAuditStore{}, &fakeQuotaClient{})
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
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, &fakeQuotaClient{})
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
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, &fakeQuotaClient{})

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
	svc := NewTenantPlanService(plans, &fakeAuditStore{}, &fakeQuotaClient{})
	_, err := svc.GetTenantPlan(context.Background(), &tenantv1.GetTenantPlanRequest{
		PlanId: "11111111-1111-1111-1111-111111111111",
	})
	if status.Code(err) != codes.NotFound || !strings.HasPrefix(status.Convert(err).Message(), "TENANT_PLAN_NOT_FOUND") {
		t.Fatalf("expected TENANT_PLAN_NOT_FOUND, got %v", err)
	}
}

func TestTenantPlanService_Get_InvalidID(t *testing.T) {
	svc := NewTenantPlanService(&listFakePlanStore{}, &fakeAuditStore{}, &fakeQuotaClient{})
	_, err := svc.GetTenantPlan(context.Background(), &tenantv1.GetTenantPlanRequest{PlanId: "not-a-uuid"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}
