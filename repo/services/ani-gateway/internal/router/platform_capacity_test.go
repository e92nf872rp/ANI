package router

import (
	"context"
	"testing"

	"github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestPlatformCapacityLocalFallbackProfile(t *testing.T) {
	api := newPlatformCapacityAPI(nil)

	overview, err := api.service.GetCapacityOverview(context.Background())
	if err != nil {
		t.Fatalf("GetCapacityOverview() error = %v", err)
	}
	response := platformCapacityResponseFromOverview(overview)
	if len(response.Regions) != 1 {
		t.Fatalf("regions = %d, want 1", len(response.Regions))
	}
	if response.Regions[0].Capacity.GPUTotal != 3 || response.Regions[0].Capacity.GPUFree != 2 ||
		response.Regions[0].Capacity.Nodes != 1 {
		t.Fatalf("capacity = %+v, want deterministic local values", response.Regions[0].Capacity)
	}
	if response.Summary.RegionCount != 1 || response.Summary.GPUTotal != 3 {
		t.Fatalf("summary = %+v, want single-region aggregate", response.Summary)
	}
	if response.Regions[0].AZs == nil || response.Summary.AZs == nil {
		t.Fatalf("azs must serialize as [] not null, regions=%v summary=%v", response.Regions[0].AZs, response.Summary.AZs)
	}
	if response.DevProfile.Mode != "local" || response.DevProfile.RealProvider {
		t.Fatalf("dev_profile = %+v, want local non-real profile", response.DevProfile)
	}
}

func TestPlatformCapacityResponseFromOverviewDeviates(t *testing.T) {
	overview := ports.PlatformCapacityOverview{
		Regions: []ports.PlatformRegion{{
			ID:            "platform",
			Code:          "platform",
			Name:          "平台",
			DisplayName:   "平台（默认区域）",
			Status:        "enabled",
			OpenForTenant: true,
			AZs:           []string{"az-a"},
			TenantCount:   7,
			Capacity: ports.PlatformRegionCapacity{
				GPUTotal: 48, GPUFree: 18, Nodes: 16, CPUCores: 512, MemoryGiB: 2048,
			},
		}},
		Summary: ports.PlatformCapacitySummary{
			RegionCount: 1, GPUTotal: 48, GPUFree: 18, TenantCount: 7, Nodes: 16,
			AZs: []string{"az-a"},
		},
		DevProfile: ports.DevProfileInfo{
			Mode: "real", Provider: "kubernetes-platform-capacity", RealProvider: true,
			Reason: "capacity computed from real cluster (GPU inventory + nodes + tenant store)",
		},
	}

	response := platformCapacityResponseFromOverview(overview)
	region := response.Regions[0]
	if region.ID != "platform" || region.Name != "平台" || region.DisplayName != "平台（默认区域）" ||
		region.Status != "enabled" || !region.OpenForTenant || region.TenantCount != 7 {
		t.Fatalf("region = %+v, want platform constants with tenant_count=7", region)
	}
	if region.Capacity.GPUTotal != 48 || region.Capacity.GPUFree != 18 || region.Capacity.Nodes != 16 ||
		region.Capacity.CPUCores != 512 || region.Capacity.MemoryGiB != 2048 {
		t.Fatalf("capacity = %+v, want overview passthrough", region.Capacity)
	}
	if response.Summary.TenantCount != 7 || response.Summary.Nodes != 16 || len(response.Summary.AZs) != 1 {
		t.Fatalf("summary = %+v, want overview passthrough", response.Summary)
	}
	if !response.DevProfile.RealProvider || response.DevProfile.Provider != "kubernetes-platform-capacity" {
		t.Fatalf("dev_profile = %+v, want real passthrough", response.DevProfile)
	}
}

func TestPlatformCapacityRegisterOptionsWiresService(t *testing.T) {
	// RegisterOptions.PlatformCapacityService 注入为 nil 时 handler 走 local 回退；
	// 注入非 nil 时透传。这里验证字段装配路径编译与回退语义。
	options := RegisterOptions{PlatformCapacityService: runtime.NewLocalPlatformCapacityService(nil)}
	if options.PlatformCapacityService == nil {
		t.Fatal("PlatformCapacityService = nil, want injected service")
	}
	api := newPlatformCapacityAPI(options.PlatformCapacityService)
	if api.service != options.PlatformCapacityService {
		t.Fatal("api.service should passthrough the injected service")
	}
}
