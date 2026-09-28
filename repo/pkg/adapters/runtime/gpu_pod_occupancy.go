package runtime

// GPU 占用口径共享解析：GET /gpu-inventory/occupancy（gateway）与
// GET /platform/capacity（KubernetesPlatformCapacityService）必须给出同一套
// 「什么算占用一个 GPU 设备」的判定，否则同一集群两个接口的空闲数会互相矛盾。
//
// 口径（plan-platform-capacity §3.4）：
//   - 只有 Running 且真的请求 GPU 扩展资源的 Pod 才算占用；CPU-only 工作负载
//     （VM virt-launcher、探针、作业 Pod 等）即使带租户 label 也不占 GPU；
//   - 只统计 ani-tenant-* 租户命名空间，避免把平台自身组件算进来；
//   - 每个 Pod 占 1 个设备记录（整卡 Pod = 1 张物理卡，vGPU Pod = 1 个切片），
//     与 gpuInventoryRecordFromDevice 的 in_use 语义一致。

import (
	"encoding/json"
	"strings"
)

const (
	// GPUTenantLabel 是租户工作负载 Pod 上的租户归属 label；平台级统计以它的
	// 存在性作为「租户 Pod」的判定条件。
	GPUTenantLabel = "ani.kubercloud.io/tenant-id"
	// GPUInstanceLabel 是实例名 label，仅用于 occupancy 的 instance_id 回显。
	GPUInstanceLabel = "ani.kubercloud.io/instance"
	// GPUTenantNamespacePrefix 是租户命名空间前缀（ani-tenant-<tenant_id>）。
	GPUTenantNamespacePrefix = "ani-tenant-"
)

// gpuPodResourceNames 是判定「Pod 真的请求 GPU」的扩展资源名，与
// gpuNodeClassesFromKubernetesNodeList 的设备枚举口径保持一致。
var gpuPodResourceNames = []string{
	kubernetesNVIDIAGPUResource,
	kubernetesNVIDIAVGPUResource,
	kubernetesVolcanoVGPUNumberResource,
}

// kubernetesPodContainerSpec 是 Pod 容器的最小资源描述。
type kubernetesPodContainerSpec struct {
	Resources struct {
		// K8s 对扩展资源要求写在 limits（只写 requests 会被 API server 拒绝），
		// 因此只读 limits 即可覆盖全部合法形态。
		Limits map[string]string `json:"limits"`
	} `json:"resources"`
}

// GPUPodOccupancyRecord 是从 K8s Pod 提取的最小 GPU 占用信息。
// InstanceName 可能为空（未打实例 label 的裸 GPU Pod），此时只参与占用计数，
// 不参与 instance_id 回显。
type GPUPodOccupancyRecord struct {
	TenantID     string
	InstanceName string
	NodeName     string
}

// ParseRunningGPUPodOccupancy 解析 K8s Pod list JSON，只保留「Running + 位于
// ani-tenant-* 命名空间 + 请求 GPU 扩展资源」的 Pod。body 为空时返回 nil。
func ParseRunningGPUPodOccupancy(body []byte) ([]GPUPodOccupancyRecord, error) {
	if len(body) == 0 {
		return nil, nil
	}
	var podList struct {
		Items []struct {
			Metadata struct {
				Namespace string            `json:"namespace"`
				Labels    map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				NodeName   string                       `json:"nodeName"`
				Containers []kubernetesPodContainerSpec `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &podList); err != nil {
		return nil, err
	}
	records := make([]GPUPodOccupancyRecord, 0, len(podList.Items))
	for _, pod := range podList.Items {
		if !strings.EqualFold(strings.TrimSpace(pod.Status.Phase), "Running") {
			continue
		}
		nodeName := strings.TrimSpace(pod.Spec.NodeName)
		if nodeName == "" {
			continue
		}
		if !strings.HasPrefix(pod.Metadata.Namespace, GPUTenantNamespacePrefix) {
			continue
		}
		if !podRequestsGPU(pod.Spec.Containers) {
			continue
		}
		records = append(records, GPUPodOccupancyRecord{
			TenantID:     strings.TrimSpace(pod.Metadata.Labels[GPUTenantLabel]),
			InstanceName: strings.TrimSpace(pod.Metadata.Labels[GPUInstanceLabel]),
			NodeName:     nodeName,
		})
	}
	return records, nil
}

// podRequestsGPU 判断任一容器是否请求了 GPU 扩展资源。
func podRequestsGPU(containers []kubernetesPodContainerSpec) bool {
	for _, container := range containers {
		for _, name := range gpuPodResourceNames {
			if strings.TrimSpace(container.Resources.Limits[name]) != "" {
				return true
			}
		}
	}
	return false
}
