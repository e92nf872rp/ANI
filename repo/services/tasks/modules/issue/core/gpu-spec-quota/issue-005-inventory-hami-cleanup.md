# GPU Inventory 扩展 + HAMi 清理

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
- UX: N/A（非前端）
- SPEC: `repo/services/tasks/modules/spec/core/gpu-spec-quota/spec-gpu-spec-quota.md`

## Description

作为开发者，扩展 GPU Inventory 实现 ListSpecAvailability（四态可用性计算）、GPUNodeClass 从节点标签派生新字段、用 parseVolcanoVGPUAnnotation 替代 HAMi 解析并删除所有 HAMi 代码分支。

## Scope

- Product line: core
- Code paths allowed: `repo/pkg/adapters/runtime/kubernetes_gpu_inventory.go`

## Acceptance Criteria

- [ ] 实现 `ListSpecAvailability(tenant_id)`：获取租户配额(total/used/reserved) + 预留额度(allocated_gpu_count) → quota_remaining = allocated - used - reserved；遍历所有规格匹配节点标签 → has_matching_nodes；解析 volcano.sh/node-vgpu-register annotation 得设备总量 + 查已调度 Pod 资源请求求差得 device_idle_count；四态判定（available/full/device_full/unavailable）；available_count = min(quota_remaining, device_idle_count)
- [ ] GPUNodeClass 新增 gpu_mode/gpu_spec/gpu_sharing_spec/gpu_sharing_policy 字段，从节点标签 `ani.kubercloud.io/gpu-mode`/`gpu-spec`/`gpu-sharing-spec`/`gpu-sharing-policy` 派生（只读，不写入 PG）
- [ ] 新增 `parseVolcanoVGPUAnnotation` 替代旧 `parseHAMIAnnotation`，解析 volcano.sh/node-vgpu-register annotation 得 vGPU 设备总量
- [ ] 删除 `isHAMINode` 函数、`kubernetesHAMISchedulerName` 常量、`hami-scheduler` 分支代码
- [ ] 过渡期 inventory 优先读新 label 回退读旧 label（nvidia.com/gpu.product / ani.kubercloud.io/gpu-model）
- [ ] `go build ./pkg/adapters/runtime/...` 通过
- [ ] `go test ./pkg/adapters/runtime/ -run "TestListSpecAvailability|TestHAMiRemoved|TestInventory" -count=1` 通过
- [ ] `make validate-architecture` 通过
- [ ] `git diff --check` 通过

## Dependencies

#3

## Type

core

## Priority

high

## Labels

core

## Batch

M2.1-TASK-A

## References

- SPEC: §5.1 (ListSpecAvailability 算法), §3.2 (GPUNodeClass 扩展)
- UX: N/A
