# Issue #3: PlanScheduling 扩展（queue 解析 + vGPU）

> **Priority:** high
> **Depends On:** #1
> **Product line:** Core
> **Document Links:**
> - SPEC: `repo/services/tasks/modules/spec/core/gpu-inventory/spec-core-gpu-scheduling.md` §5.1, §5.3

## Scope

- `repo/pkg/adapters/runtime/kubernetes_gpu_inventory.go`（MODIFY）
- `repo/pkg/ports/gpu_inventory.go`（MODIFY：`GPUSchedulingRequest` 加 `QueueName` / `WorkloadClass`）

## Description

扩展现有 `KubernetesGPUInventory.PlanScheduling()` 以支持队列解析和 HAMi vGPU 模式。

## Acceptance Criteria

- [x] `GPUSchedulingRequest` 新增 `QueueName string` 和 `WorkloadClass WorkloadClass`
- [x] `QueueName` 非空时校验队列存在且属于该租户（调 `VolcanoQueueStore.List` 过滤）
- [x] `QueueName` 为空时按 `WorkloadClass` 选默认队列：inference→`ani-inference`，training→`ani-training`，batch→`ani-training`
- [x] 整卡模式（`VirtualizationNone`）检查节点 `nvidia.com/gpu` allocatable >= RequiredCount
- [x] vGPU 模式（`GPUVirtualizationVGPU`）检查节点 `nvidia.com/vgpu` allocatable >= RequiredCount
- [x] 昇腾/海光请求返回明确不支持，`Reasons: ["P1 未启用"]`
- [x] MIG 请求返回明确不支持
- [x] 无可用 GPU → `Reasons` 填充 + 上层返回 `422 InsufficientGPU`
- [x] 无兼容节点 → `Reasons` 填充 + 上层返回 `422 GPUNodeIncompatible`
- [x] 单元测试覆盖：整卡/vGPU/无可用/昇腾拒绝/MIG 拒绝/queue 解析/默认队列选择

## Validation

```bash
cd repo
make test
make validate-architecture
```
