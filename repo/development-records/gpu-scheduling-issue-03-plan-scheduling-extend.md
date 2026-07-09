# GPU-Scheduling-Issue-03: PlanScheduling 扩展（queue 解析 + vGPU）

> **批次类型：** Feature batch
> **依赖：** Issue #1（OpenAPI 契约）
> **完成日期：** 2026-07-06
> **PRD/SPEC：** `spec-core-gpu-scheduling.md` §5.1, §5.3

---

## 1. 范围

扩展现有 `KubernetesGPUInventory.PlanScheduling()` 以支持 Volcano 队列解析和 HAMi vGPU 模式，同时保持 local profile 兼容。

### 变更文件

| 文件 | 类型 | 说明 |
|---|---|---|
| `repo/pkg/ports/gpu_inventory.go` | MODIFY | `GPUSchedulingRequest` 新增 `QueueName` / `WorkloadClass`；`GPUNodeClass` 新增 `Allocatable` |
| `repo/pkg/adapters/runtime/kubernetes_gpu_inventory.go` | MODIFY | 重写 `PlanScheduling`：queue 解析 + vGPU + 昇腾/MIG 拒绝；解析逻辑保留 allocatable map |
| `repo/pkg/adapters/runtime/local_gpu_inventory.go` | MODIFY | local profile 节点加 `Allocatable`；`PlanScheduling` 对齐 Volcano + queue + vGPU 逻辑 |
| `repo/pkg/adapters/runtime/kubernetes_gpu_inventory_test.go` | MODIFY | 新增 13 个 PlanScheduling 测试覆盖 10 项 AC |

---

## 2. 实现要点

### 2.1 Port 扩展

`GPUSchedulingRequest` 新增字段：
- `QueueName string` — 显式队列选择；为空时按 `WorkloadClass` 选默认
- `WorkloadClass WorkloadClass` — 驱动默认队列选择

`GPUNodeClass` 新增字段：
- `Allocatable map[string]string` — 保留 K8s 节点原始 allocatable map，让 `PlanScheduling` 直接查询 `nvidia.com/gpu` 或 `nvidia.com/vgpu`

### 2.2 PlanScheduling 算法（SPEC §5.1）

```text
1. P0 vendor gate: 仅 NVIDIA；昇腾(huawei)/海光(hygon) → decision with Reasons
2. MIG mode → decision with Reasons（P1 未启用）
3. 解析 queue:
   - QueueName 非空 → queueStore.List(tenant) 校验存在且属于该租户
   - QueueName 为空 → defaultQueueName(WorkloadClass)
     inference→ani-inference; training/batch→ani-training
4. selectResourceName:
   - VirtualizationVGPU → nvidia.com/vgpu (runtimeClassName=hami-vgpu)
   - 其他 → nvidia.com/gpu (runtimeClassName=nvidia)
5. 遍历 ready GPU 节点:
   - gpuAllocatableCount(node, resourceName) >= RequiredCount → 匹配
   - 输出 SchedulerName=volcano, QueueName, ResourceName, NodeSelector
6. 无可用 GPU → decision with Reasons（不返回 error，上层 422）
```

### 2.3 Queue Store 注入

新增 `NewKubernetesGPUInventoryWithQueueStore(client, store)` 构造函数。当 `queueStore` 非 nil 时，显式 `QueueName` 会通过 `store.List(tenantID)` 校验；为 nil 时跳过校验（local/dev profile）。

### 2.4 节点解析扩展

`gpuNodeClassesFromKubernetesNodeList` 现在：
- 识别 `nvidia.com/gpu`（整卡）和 `nvidia.com/vgpu`（HAMi 切片）两种资源
- 保留 `Allocatable` map 到 `GPUNodeClass.Allocatable`
- 生成对应 `GPUDeviceClass`（vGPU device 标记 `VirtualizationMode=vgpu`）

---

## 3. 验收

### 3.1 10 项 AC 全部通过

| AC | 测试 |
|---|---|
| GPUSchedulingRequest 新增字段 | 编译通过 + `TestPlanSchedulingWholeCardSelectsNVIDIAGPUResource` |
| QueueName 非空校验存在+租户 | `TestPlanSchedulingExplicitQueueValidatedByStore` |
| QueueName 为空按 WorkloadClass 选默认 | `TestPlanSchedulingDefaultQueueInference/Training/BatchFallsBackToTraining` |
| 整卡 nvidia.com/gpu allocatable | `TestPlanSchedulingWholeCardSelectsNVIDIAGPUResource` |
| vGPU nvidia.com/vgpu allocatable | `TestPlanSchedulingVGPUSelectsNVIDIAGVGPUResource` |
| 昇腾/海光拒绝 | `TestPlanSchedulingAscendVendorRejected` |
| MIG 拒绝 | `TestPlanSchedulingMIGModeRejected` |
| 无可用 GPU Reasons | `TestPlanSchedulingNoAvailableGPUReturnsReasons` |
| 无兼容节点 Reasons | `TestPlanSchedulingInsufficientAllocatableReturnsReasons` |
| 单元测试覆盖 | 13 个新测试 + 2 个原有测试全部 PASS |

### 3.2 验证命令

```bash
cd repo
go build ./pkg/... ./services/ani-gateway/...
go test ./pkg/adapters/runtime/ -run "TestKubernetesGPUInventory|TestPlanScheduling|TestLocalGPUInventory|TestPlanning|TestVolcanoQueueStore" -count=1
python scripts/validate_component_imports.py --root .
```

### 3.3 结果

- `go build ./pkg/... ./services/ani-gateway/...` → 编译成功
- `go test ./pkg/adapters/runtime/` → 33 个测试全部 PASS（13 新 + 2 原 GPU inventory + 2 local + 4 planning + 14 volcano queue store - 2 重复计数）
- `python scripts/validate_component_imports.py --root .` → `component import guard passed`
- `TestDemoInstanceServiceRealShellExecutesCommand` 在 Windows 上失败（pre-existing，与本批次无关）

---

## 4. 架构边界

- 未引入 HAMi SDK / Volcano SDK 依赖
- `KubernetesGPUInventory` 通过 `VolcanoQueueStore` port 接口校验队列，不直接操作 CRD
- K8s REST client 仍在 `pkg/adapters/runtime/` 内使用
- local profile 保持 dev 边界，不声明 real-provider / runtime ready
