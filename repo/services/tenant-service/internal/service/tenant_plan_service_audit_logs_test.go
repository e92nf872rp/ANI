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

	svc := NewTenantPlanService(plans, audit, &fakeQuotaClient{})

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
