package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

// 平台容量态势 real adapter：组合 GPUInventory（节点/设备清单）+
// KubernetesRESTClient（跨租户 Running GPU Pod 统计）+ TenantService（租户数）。
// 口径（容量态势接口方案 §3.3/§3.4）：
//   - gpu_total：全部 GPU 设备数（含 vGPU 切片）；
//   - in_use：跨所有租户 namespace 的 Running GPU Pod 数（每 Pod 占 1 设备）；
//   - fault：NotReady GPU 节点上的设备数；
//   - gpu_free = gpu_total - in_use - fault；
//   - nodes：Ready GPU 节点数；azs：Ready 节点 zone label 去重；
//   - cpu/memory：Ready 节点 Allocatable 求和（总量口径）。
// 任一数据源失败不阻塞 200：缺失字段降级为 0/空，real_provider=false + reason。
type KubernetesPlatformCapacityService struct {
	inventory     ports.GPUInventory
	k8sClient     *KubernetesRESTClient
	tenantService ports.TenantService
}

func NewKubernetesPlatformCapacityService(inventory ports.GPUInventory, k8sClient *KubernetesRESTClient, tenantService ports.TenantService) *KubernetesPlatformCapacityService {
	return &KubernetesPlatformCapacityService{
		inventory:     inventory,
		k8sClient:     k8sClient,
		tenantService: tenantService,
	}
}

// GPU Pod 存在性 label selector：只要带租户 label 即视为租户工作负载 Pod，
// 配合 Running phase 过滤平台自身组件（平台组件不带该 label）。
const platformCapacityTenantLabel = "ani.kubercloud.io/tenant-id"

const (
	platformCapacityZoneLabel        = "topology.kubernetes.io/zone"
	platformCapacityZoneLabelLegacy  = "failure-domain.beta.kubernetes.io/zone"
	platformCapacityProviderName     = "kubernetes-platform-capacity"
	platformCapacityProviderRealReason = "capacity computed from real cluster (GPU inventory + nodes + tenant store)"
)

func (s *KubernetesPlatformCapacityService) GetCapacityOverview(ctx context.Context) (ports.PlatformCapacityOverview, error) {
	region := ports.PlatformRegion{
		ID:            "platform",
		Code:          "platform",
		Name:          "平台",
		DisplayName:   "平台（默认区域）",
		Status:        "enabled",
		OpenForTenant: true,
		AZs:           []string{},
	}
	overview := ports.PlatformCapacityOverview{
		Regions: []ports.PlatformRegion{region},
		Summary: ports.PlatformCapacitySummary{
			RegionCount: 1,
			AZs:         []string{},
		},
		DevProfile: ports.DevProfileInfo{
			Mode:         "real",
			Provider:     platformCapacityProviderName,
			RealProvider: true,
			Reason:       platformCapacityProviderRealReason,
		},
	}

	var degraded []string

	// 1. GPU 节点/设备清单（gpu_total/fault/nodes/azs/cpu/memory）。
	if s.inventory == nil {
		degraded = append(degraded, "gpu inventory not configured")
	} else {
		nodes, err := s.inventory.ListNodeClasses(ctx, ports.GPUDiscoveryFilter{})
		if err != nil {
			degraded = append(degraded, "gpu inventory list failed: "+err.Error())
		} else {
			var gpuTotal, gpuFault, readyNodes, cpuCores, memoryGiB int64
			azSet := map[string]struct{}{}
			readyDevices := map[string]int{}
			for _, node := range nodes {
				// gpu_total 口径：全部 GPU 节点（含 NotReady）的设备总数。
				gpuTotal += int64(len(node.Devices))
				if !node.Ready {
					gpuFault += int64(len(node.Devices))
					continue
				}
				readyNodes++
				readyDevices[node.NodeName] = len(node.Devices)
				cpuCores += platformCapacityAllocatableCores(node.Allocatable)
				memoryGiB += platformCapacityAllocatableMemoryGiB(node.Allocatable)
				if az := platformCapacityZoneOf(node.Labels); az != "" {
					azSet[az] = struct{}{}
				}
			}
			// in_use：跨租户 Running GPU Pod 统计（每 Pod 占 1 设备）。
			// 与 gpuInventoryRecordFromDevice 语义一致：各 Ready 节点取
			// min(设备数, PodCount) 之和，Pod 数超出设备数时按设备数截断。
			podCounts, podErr := s.runningGPUPodCountsByNode(ctx)
			if podErr != nil {
				degraded = append(degraded, "cross-tenant gpu pod occupancy failed: "+podErr.Error())
			}
			var inUse int64
			for nodeName, deviceCount := range readyDevices {
				podCount := podCounts[nodeName]
				if podCount > deviceCount {
					podCount = deviceCount
				}
				inUse += int64(podCount)
			}
			region.Capacity = ports.PlatformRegionCapacity{
				GPUTotal:  gpuTotal,
				GPUFree:   gpuTotal - inUse - gpuFault,
				Nodes:     readyNodes,
				CPUCores:  cpuCores,
				MemoryGiB: memoryGiB,
			}
			region.AZs = sortedAZs(azSet)
			if gpuFree := region.Capacity.GPUFree; gpuFree < 0 {
				// Pod 计数与设备清单口径偶发不一致时不返回负数，保持口径可读。
				region.Capacity.GPUFree = 0
				degraded = append(degraded, fmt.Sprintf("gpu_free clamped to 0 (in_use+fault=%d > gpu_total=%d)", inUse+gpuFault, gpuTotal))
			}
		}
	}

	// 2. 租户数（status <> 'disabled'）。
	if s.tenantService == nil {
		degraded = append(degraded, "tenant service not configured")
	} else {
		tenants, err := s.tenantService.ListAvailableTenants(ctx)
		if err != nil {
			degraded = append(degraded, "tenant list failed: "+err.Error())
		} else {
			region.TenantCount = int64(len(tenants))
		}
	}

	overview.Regions = []ports.PlatformRegion{region}
	overview.Summary = ports.PlatformCapacitySummary{
		RegionCount: 1,
		GPUTotal:    region.Capacity.GPUTotal,
		GPUFree:     region.Capacity.GPUFree,
		TenantCount: region.TenantCount,
		Nodes:       region.Capacity.Nodes,
		AZs:         region.AZs,
	}
	if len(degraded) > 0 {
		overview.DevProfile.RealProvider = false
		overview.DevProfile.Reason = "degraded: " + strings.Join(degraded, "; ")
	}
	return overview, nil
}

// runningGPUPodCountsByNode 集群级统计各节点上的 Running 租户 GPU Pod 数。
// 使用存在性 label selector（ani.kubercloud.io/tenant-id 存在即可）覆盖全部
// 租户 namespace，避免逐租户查询；Pending/Failed 等 phase 不计入占用。
// k8sClient 未注入时返回 nil（in_use=0，降级语义）。
func (s *KubernetesPlatformCapacityService) runningGPUPodCountsByNode(ctx context.Context) (map[string]int, error) {
	if s.k8sClient == nil {
		return nil, nil
	}
	selector := url.QueryEscape(platformCapacityTenantLabel)
	endpoint := s.k8sClient.Host() + "/api/v1/pods?labelSelector=" + selector
	body, _, err := s.k8sClient.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return map[string]int{}, nil
	}
	var podList struct {
		Items []struct {
			Spec struct {
				NodeName string `json:"nodeName"`
			} `json:"spec"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &podList); err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, pod := range podList.Items {
		if !strings.EqualFold(pod.Status.Phase, "Running") {
			continue
		}
		nodeName := strings.TrimSpace(pod.Spec.NodeName)
		if nodeName == "" {
			continue
		}
		counts[nodeName]++
	}
	return counts, nil
}

func platformCapacityZoneOf(labels map[string]string) string {
	if labels == nil {
		return ""
	}
	if az := strings.TrimSpace(labels[platformCapacityZoneLabel]); az != "" {
		return az
	}
	return strings.TrimSpace(labels[platformCapacityZoneLabelLegacy])
}

func sortedAZs(azSet map[string]struct{}) []string {
	if len(azSet) == 0 {
		return []string{}
	}
	azs := make([]string, 0, len(azSet))
	for az := range azSet {
		azs = append(azs, az)
	}
	sort.Strings(azs)
	return azs
}

// platformCapacityAllocatableCores 解析 cpu allocatable（核数，向下取整）。
func platformCapacityAllocatableCores(allocatable map[string]string) int64 {
	raw, ok := allocatable["cpu"]
	if !ok {
		return 0
	}
	return int64(parseK8sQuantityFloor(raw))
}

// platformCapacityAllocatableMemoryGiB 解析 memory allocatable（GiB，向下取整）。
func platformCapacityAllocatableMemoryGiB(allocatable map[string]string) int64 {
	raw, ok := allocatable["memory"]
	if !ok {
		return 0
	}
	return int64(parseK8sQuantityFloor(raw) / (1 << 30))
}

// parseK8sQuantityFloor 解析 Kubernetes quantity 字符串并向下取整为浮点数值。
// 支持十进制（含 m 后缀）与二进制（Ki/Mi/Gi/Ti/Pi/Ei）后缀；解析失败返回 0。
func parseK8sQuantityFloor(raw string) float64 {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0
	}
	// 二进制后缀（Ki/Mi/Gi/...）。
	binarySuffixes := []struct {
		suffix string
		mul    float64
	}{
		{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30},
		{"Ti", 1 << 40}, {"Pi", 1 << 50}, {"Ei", 1 << 60},
	}
	for _, bs := range binarySuffixes {
		if strings.HasSuffix(value, bs.suffix) {
			number, err := strconv.ParseFloat(strings.TrimSuffix(value, bs.suffix), 64)
			if err != nil {
				return 0
			}
			return number * bs.mul
		}
	}
	// 十进制 SI 后缀。
	decimalSuffixes := []struct {
		suffix string
		mul    float64
	}{
		{"n", 1e-9}, {"u", 1e-6}, {"m", 1e-3},
		{"k", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12}, {"P", 1e15}, {"E", 1e18},
	}
	for _, ds := range decimalSuffixes {
		if strings.HasSuffix(value, ds.suffix) {
			number, err := strconv.ParseFloat(strings.TrimSuffix(value, ds.suffix), 64)
			if err != nil {
				return 0
			}
			return number * ds.mul
		}
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return number
}
