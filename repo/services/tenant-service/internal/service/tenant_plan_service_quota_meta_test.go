package service

import (
	"context"
	"strings"
	"testing"

	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTenantPlanService_ListQuotaMeta(t *testing.T) {
	core := &fakeQuotaClient{
		meta: []ports.QuotaMeta{
			{ResourceType: "gpu_count", Enabled: true, DefaultQuota: 4, DisplayName: "GPU", Unit: "card", IsDiscrete: true},
			{ResourceType: "cpu_core", Enabled: true, DefaultQuota: 16, DisplayName: "CPU", Unit: "core", IsDiscrete: true},
		},
	}
	svc := NewTenantPlanService(&fakePlanStore{}, &fakeAuditStore{}, core)

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
	svc := NewTenantPlanService(&fakePlanStore{}, &fakeAuditStore{}, core)

	_, err := svc.ListQuotaMeta(context.Background(), nil)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code=%v want Unavailable", status.Code(err))
	}
	msg := status.Convert(err).Message()
	if !strings.HasPrefix(msg, "GRPC_CLIENT_UNAVAILABLE") {
		t.Fatalf("msg=%q want GRPC_CLIENT_UNAVAILABLE prefix", msg)
	}
}
