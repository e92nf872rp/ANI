// platform_capacity.go 实现平台容量态势只读汇总接口：
// GET /api/v1/platform/capacity（BOSS「资源池与容量态势」/「平台资源池总览」）。
// 整平台 = 1 个默认区域；数据由 PlatformCapacityService 从真实集群计算。
package router

import (
	"context"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

type platformCapacityAPI struct {
	service ports.PlatformCapacityService
}

type platformCapacityResponse struct {
	Regions    []platformRegionResponse `json:"regions"`
	Summary    platformCapacitySummary  `json:"summary"`
	DevProfile coreDevProfileResponse   `json:"dev_profile"`
}

type platformRegionResponse struct {
	ID            string                        `json:"id"`
	Code          string                        `json:"code"`
	Name          string                        `json:"name"`
	DisplayName   string                        `json:"display_name"`
	Status        string                        `json:"status"`
	OpenForTenant bool                          `json:"open_for_tenant"`
	AZs           []string                      `json:"azs"`
	TenantCount   int64                         `json:"tenant_count"`
	Capacity      platformRegionCapacityResponse `json:"capacity"`
}

type platformRegionCapacityResponse struct {
	GPUTotal  int64 `json:"gpu_total"`
	GPUFree   int64 `json:"gpu_free"`
	Nodes     int64 `json:"nodes"`
	CPUCores  int64 `json:"cpu_cores"`
	MemoryGiB int64 `json:"memory_gib"`
}

type platformCapacitySummary struct {
	RegionCount int64    `json:"region_count"`
	GPUTotal    int64    `json:"gpu_total"`
	GPUFree     int64    `json:"gpu_free"`
	TenantCount int64    `json:"tenant_count"`
	Nodes       int64    `json:"nodes"`
	AZs         []string `json:"azs"`
}

// newPlatformCapacityAPI 注入为 nil 时回退 local 降级实现（与 gpu-inventory
// fallback 惯例一致），保证 gateway 在未配置 provider 时仍可启动并返回 200。
func newPlatformCapacityAPI(service ports.PlatformCapacityService) *platformCapacityAPI {
	if service == nil {
		service = runtimeadapter.NewLocalPlatformCapacityService(nil)
	}
	return &platformCapacityAPI{service: service}
}

func registerPlatformCapacity(v1 *route.RouterGroup, service ports.PlatformCapacityService) {
	api := newPlatformCapacityAPI(service)
	v1.GET("/platform/capacity", api.getCapacityOverview)
}

func (api *platformCapacityAPI) getCapacityOverview(ctx context.Context, c *app.RequestContext) {
	overview, err := api.service.GetCapacityOverview(ctx)
	if err != nil {
		writeInstanceError(c, http.StatusInternalServerError, "PLATFORM_CAPACITY_FAILED", err.Error())
		return
	}
	c.JSON(http.StatusOK, platformCapacityResponseFromOverview(overview))
}

func platformCapacityResponseFromOverview(overview ports.PlatformCapacityOverview) platformCapacityResponse {
	regions := make([]platformRegionResponse, 0, len(overview.Regions))
	for _, region := range overview.Regions {
		azs := region.AZs
		if azs == nil {
			azs = []string{}
		}
		regions = append(regions, platformRegionResponse{
			ID:            region.ID,
			Code:          region.Code,
			Name:          region.Name,
			DisplayName:   region.DisplayName,
			Status:        region.Status,
			OpenForTenant: region.OpenForTenant,
			AZs:           azs,
			TenantCount:   region.TenantCount,
			Capacity: platformRegionCapacityResponse{
				GPUTotal:  region.Capacity.GPUTotal,
				GPUFree:   region.Capacity.GPUFree,
				Nodes:     region.Capacity.Nodes,
				CPUCores:  region.Capacity.CPUCores,
				MemoryGiB: region.Capacity.MemoryGiB,
			},
		})
	}
	summaryAZs := overview.Summary.AZs
	if summaryAZs == nil {
		summaryAZs = []string{}
	}
	return platformCapacityResponse{
		Regions: regions,
		Summary: platformCapacitySummary{
			RegionCount: overview.Summary.RegionCount,
			GPUTotal:    overview.Summary.GPUTotal,
			GPUFree:     overview.Summary.GPUFree,
			TenantCount: overview.Summary.TenantCount,
			Nodes:       overview.Summary.Nodes,
			AZs:         summaryAZs,
		},
		DevProfile: coreDevProfileResponse{
			Mode:         overview.DevProfile.Mode,
			Provider:     overview.DevProfile.Provider,
			RealProvider: overview.DevProfile.RealProvider,
			Reason:       overview.DevProfile.Reason,
		},
	}
}
