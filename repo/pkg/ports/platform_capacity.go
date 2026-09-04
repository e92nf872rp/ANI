package ports

import (
	"context"
	"errors"
)

// 平台容量态势（BOSS「资源池与容量态势」/「平台资源池总览」）只读汇总端口。
// 整平台作为 1 个默认区域返回；数据从真实集群计算，不做区域主数据 CRUD。
var (
	// ErrPlatformCapacityUnsupported 表示当前 provider 不支持平台容量计算。
	ErrPlatformCapacityUnsupported = errors.New("platform capacity: unsupported provider")
	// ErrPlatformCapacityInvalid 表示平台容量请求参数非法（当前只读无参数，预留）。
	ErrPlatformCapacityInvalid = errors.New("platform capacity: invalid request")
)

// PlatformRegionCapacity 单区域容量口径：
//   - GPUTotal：全部 GPU 设备数（含 vGPU 切片）；
//   - GPUFree：GPUTotal - InUse - Fault；InUse 为跨租户 Running GPU Pod 数（每 Pod 占 1 设备），
//     Fault 为 NotReady 节点上的设备数；
//   - Nodes：仅统计 Ready 的 GPU 节点（ListNodeClasses 只含 GPU 节点）；
//   - CPUCores / MemoryGiB：Ready GPU 节点 Allocatable 求和（总量口径，非可用量）。
type PlatformRegionCapacity struct {
	GPUTotal  int64
	GPUFree   int64
	Nodes     int64
	CPUCores  int64
	MemoryGiB int64
}

// PlatformRegion 平台默认区域记录；ID/Code/Name/DisplayName/Status/OpenForTenant
// 为平台主数据常量（整平台 = 1 区域，写死；后续区域主数据落地后由主数据驱动）。
type PlatformRegion struct {
	ID            string
	Code          string
	Name          string
	DisplayName   string
	Status        string
	OpenForTenant bool
	AZs           []string
	TenantCount   int64
	Capacity      PlatformRegionCapacity
}

// PlatformCapacityOverview 平台容量汇总响应。
type PlatformCapacityOverview struct {
	Regions    []PlatformRegion
	Summary    PlatformCapacitySummary
	DevProfile DevProfileInfo
}

// PlatformCapacitySummary regions 汇总（当前即单区域值），供顶部指标直接消费。
type PlatformCapacitySummary struct {
	RegionCount int64
	GPUTotal    int64
	GPUFree     int64
	TenantCount int64
	Nodes       int64
	AZs         []string
}

// PlatformCapacityService 平台级（跨租户）只读容量汇总能力端口。
type PlatformCapacityService interface {
	GetCapacityOverview(ctx context.Context) (PlatformCapacityOverview, error)
}
