// Package internal 提供 metering-service 内部共享函数。
package internal

import (
	"encoding/json"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// buildSpec 根据 workload_kind 硬编码维度映射，构造 CollectionSpec。
// 供 consumer 和 rebuilder 共用：
//   - consumer: buildSpec(event.TenantID, event.InstanceID, event.Name, event.WorkloadKind, gpuCount)
//     gpuCount 从 event.GPUSpec.Count 提取（nil 则 0）
//   - rebuilder: buildSpec(tenantID, instanceID, name, kind, gpuCount)
//     gpuCount 从 parseGPUCount(gpuStatusJSON) 提取
//
// 维度映射：
//   - gpu_container → 3 维（GPU+CPU+Mem）
//   - vm            → CPU+Mem
//   - container     → CPU+Mem
//   - 其他 kind     → CPU+Mem
//
// ResourceRef 使用 instance_id（用于 ticker 去重和 StopCollection 标识），
// WorkloadName 使用 name（用于 PromQL pod 正则匹配）。
// IntervalSec 默认 60，StartedAt 默认 time.Now()。
// gpuCount > 0 时设置 GPUSpec，否则为 nil。
func buildSpec(tenantID, instanceID, name, kind string, gpuCount int) ports.CollectionSpec {
	spec := ports.CollectionSpec{
		ResourceRef:  instanceID,
		WorkloadName: name,
		TenantID:     tenantID,
		WorkloadKind: kind,
		Dimensions:   dimensionsFor(kind),
		IntervalSec:  60,
		StartedAt:    time.Now(),
	}
	if gpuCount > 0 {
		spec.GPUSpec = &ports.GPUEventSpec{Count: gpuCount}
	}
	return spec
}

// dimensionsFor 根据 workload_kind 返回硬编码的维度列表。
// gpu_container → [GPU+CPU+Mem]；其他 kind（vm/container/...）→ [CPU+Mem]。
func dimensionsFor(kind string) []ports.CollectionDimension {
	switch kind {
	case "gpu_container":
		return []ports.CollectionDimension{
			{ResourceType: ports.MeteringResourceInstanceGPUSeconds, Source: "dcgm_gpu"},
			{ResourceType: ports.MeteringResourceInstanceCPUSeconds, Source: "kubelet_cpu"},
			{ResourceType: ports.MeteringResourceInstanceMemorySeconds, Source: "kubelet_mem"},
		}
	default:
		// vm / container / 其他 kind → CPU+Mem
		return []ports.CollectionDimension{
			{ResourceType: ports.MeteringResourceInstanceCPUSeconds, Source: "kubelet_cpu"},
			{ResourceType: ports.MeteringResourceInstanceMemorySeconds, Source: "kubelet_mem"},
		}
	}
}

// parseGPUCount 从 gpu_status JSONB 解析 GPU 卡数。
// 预期格式：{"count": N}。缺失或解析失败返回 0。
func parseGPUCount(gpuStatusJSON []byte) int {
	if len(gpuStatusJSON) == 0 {
		return 0
	}
	var status struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(gpuStatusJSON, &status); err != nil {
		return 0
	}
	return status.Count
}
