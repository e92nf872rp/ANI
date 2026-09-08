package runtime

import (
	"context"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalPlatformCapacityServiceDeterministicOverview(t *testing.T) {
	service := NewLocalPlatformCapacityService(nil)

	overview, err := service.GetCapacityOverview(context.Background())
	if err != nil {
		t.Fatalf("GetCapacityOverview() error = %v", err)
	}
	if len(overview.Regions) != 1 {
		t.Fatalf("regions = %d, want 1 (整平台 = 1 区域)", len(overview.Regions))
	}
	region := overview.Regions[0]
	if region.ID != "platform" || region.Code != "platform" || region.Status != "enabled" || !region.OpenForTenant {
		t.Fatalf("region = %+v, want platform default region constants", region)
	}
	// LocalGPUInventory：ani-gpu-a Ready 2×A100、ani-gpu-b NotReady 1×L40S。
	if region.Capacity.GPUTotal != 3 {
		t.Fatalf("gpu_total = %d, want 3", region.Capacity.GPUTotal)
	}
	if region.Capacity.GPUFree != 2 {
		t.Fatalf("gpu_free = %d, want 2 (3 - 0 in_use - 1 fault)", region.Capacity.GPUFree)
	}
	if region.Capacity.Nodes != 1 {
		t.Fatalf("nodes = %d, want 1 (仅 Ready GPU 节点)", region.Capacity.Nodes)
	}
	if region.AZs == nil || len(region.AZs) != 0 {
		t.Fatalf("azs = %v, want empty (local 无 zone label)", region.AZs)
	}
	if region.TenantCount != 0 {
		t.Fatalf("tenant_count = %d, want 0 (无 TenantService 注入)", region.TenantCount)
	}
	if overview.Summary.RegionCount != 1 || overview.Summary.GPUTotal != 3 || overview.Summary.GPUFree != 2 ||
		overview.Summary.Nodes != 1 || overview.Summary.TenantCount != 0 {
		t.Fatalf("summary = %+v, want single-region aggregate", overview.Summary)
	}
	if overview.DevProfile.Mode != "local" || overview.DevProfile.RealProvider {
		t.Fatalf("dev_profile = %+v, want local non-real profile", overview.DevProfile)
	}
}

type platformCapacityFakeTenantService struct {
	tenants []ports.TenantSummary
	err     error
}

func (f *platformCapacityFakeTenantService) GetTenant(ctx context.Context, tenantID string) (ports.Tenant, error) {
	return ports.Tenant{}, nil
}

func (f *platformCapacityFakeTenantService) ListAvailableTenants(ctx context.Context) ([]ports.TenantSummary, error) {
	return f.tenants, f.err
}

func TestLocalPlatformCapacityServiceTenantCount(t *testing.T) {
	service := NewLocalPlatformCapacityService(&platformCapacityFakeTenantService{
		tenants: []ports.TenantSummary{{ID: "t1"}, {ID: "t2"}, {ID: "t3"}},
	})

	overview, err := service.GetCapacityOverview(context.Background())
	if err != nil {
		t.Fatalf("GetCapacityOverview() error = %v", err)
	}
	if overview.Regions[0].TenantCount != 3 || overview.Summary.TenantCount != 3 {
		t.Fatalf("tenant_count = %d/%d, want 3/3", overview.Regions[0].TenantCount, overview.Summary.TenantCount)
	}
}

func TestLocalPlatformCapacityServiceTenantErrorDegradesToZero(t *testing.T) {
	service := NewLocalPlatformCapacityService(&platformCapacityFakeTenantService{
		err: context.DeadlineExceeded,
	})

	overview, err := service.GetCapacityOverview(context.Background())
	if err != nil {
		t.Fatalf("GetCapacityOverview() error = %v, want degraded 200 semantics", err)
	}
	if overview.Regions[0].TenantCount != 0 {
		t.Fatalf("tenant_count = %d, want 0 on tenant service failure", overview.Regions[0].TenantCount)
	}
}
