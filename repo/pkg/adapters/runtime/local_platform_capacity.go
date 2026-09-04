package runtime

import (
	"context"

	"github.com/kubercloud/ani/pkg/ports"
)

// LocalPlatformCapacityService local/dev profile 降级实现：
// 复用 LocalGPUInventory 派生确定性容量值（ani-gpu-a Ready 2×A100、
// ani-gpu-b NotReady 1×L40S → gpu_total=3、gpu_free=2、fault=1、nodes=1、azs=[]），
// tenant_count 取注入的本地 TenantService 条数（无则 0）。
type LocalPlatformCapacityService struct {
	inventory     ports.GPUInventory
	tenantService ports.TenantService
}

func NewLocalPlatformCapacityService(tenantService ports.TenantService) *LocalPlatformCapacityService {
	return &LocalPlatformCapacityService{
		inventory:     NewLocalGPUInventory(),
		tenantService: tenantService,
	}
}

func (s *LocalPlatformCapacityService) GetCapacityOverview(ctx context.Context) (ports.PlatformCapacityOverview, error) {
	region := ports.PlatformRegion{
		ID:            "platform",
		Code:          "platform",
		Name:          "平台",
		DisplayName:   "平台（默认区域）",
		Status:        "enabled",
		OpenForTenant: true,
		AZs:           []string{},
	}
	var gpuTotal, gpuFault, readyNodes int64
	nodes, err := s.inventory.ListNodeClasses(ctx, ports.GPUDiscoveryFilter{})
	if err != nil {
		// LocalGPUInventory 是确定性内存实现，理论上不会失败；失败按 0 处理。
		gpuTotal, gpuFault, readyNodes = 0, 0, 0
	} else {
		for _, node := range nodes {
			// gpu_total 口径：全部 GPU 节点（含 NotReady）的设备总数。
			gpuTotal += int64(len(node.Devices))
			if !node.Ready {
				gpuFault += int64(len(node.Devices))
				continue
			}
			readyNodes++
		}
	}
	// local profile 无跨租户 Pod 数据源，in_use=0（全部空闲）。
	var inUse int64
	region.Capacity = ports.PlatformRegionCapacity{
		GPUTotal: gpuTotal,
		GPUFree:  gpuTotal - inUse - gpuFault,
		Nodes:    readyNodes,
	}
	region.TenantCount = localPlatformCapacityTenantCount(ctx, s.tenantService)
	return ports.PlatformCapacityOverview{
		Regions: []ports.PlatformRegion{region},
		Summary: ports.PlatformCapacitySummary{
			RegionCount: 1,
			GPUTotal:    region.Capacity.GPUTotal,
			GPUFree:     region.Capacity.GPUFree,
			TenantCount: region.TenantCount,
			Nodes:       region.Capacity.Nodes,
			AZs:         region.AZs,
		},
		DevProfile: ports.DevProfileInfo{
			Mode:         "local",
			Provider:     "local-platform-capacity",
			RealProvider: false,
			Reason:       "local profile computes capacity from the deterministic local GPU inventory; it is not a real cluster execution",
		},
	}, nil
}

func localPlatformCapacityTenantCount(ctx context.Context, tenantService ports.TenantService) int64 {
	if tenantService == nil {
		return 0
	}
	tenants, err := tenantService.ListAvailableTenants(ctx)
	if err != nil {
		return 0
	}
	return int64(len(tenants))
}
