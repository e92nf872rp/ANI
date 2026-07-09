# Issue #5: GPU smoke A+B live gate

> **Priority:** high
> **Depends On:** #4
> **Product line:** Core
> **Document Links:**
> - PRD: `repo/services/tasks/modules/prd/console/gpu-inventory/prd-k8s-gpu-hami-volcano-scheduling.md` US-002
> - SPEC: `repo/services/tasks/modules/spec/core/gpu-inventory/spec-core-gpu-scheduling.md` §9.4

## Scope

- `repo/deploy/real-k8s-lab/gpu-scheduling-live-gate.yaml`（NEW）
- `repo/deploy/real-k8s-lab/gpu-scheduling-smoke-a-job.yaml`（NEW）
- `repo/deploy/real-k8s-lab/gpu-scheduling-smoke-b-job.yaml`（NEW）
- `repo/scripts/validate_gpu_scheduling_live_gate.py`（NEW）
- `repo/Makefile`（MODIFY：新增 `validate-gpu-scheduling-live-gate` target）

## Description

GPU smoke 测试：Smoke A（整卡调度）+ Smoke B（HAMi vGPU 调度），验证 Volcano 调度器和 HAMi 在真实 lab 环境可用。

## Acceptance Criteria

- [x] Smoke A（整卡）：`schedulerName: volcano` + `nvidia.com/gpu: 1` + `nodeSelector: ani.kubercloud.io/gpu-node=true` Job 调度成功
- [x] Smoke B（vGPU）：HAMi vGPU 模式 `nvidia.com/gpu: 1`（split=10）Job 调度成功
- [x] live gate profile YAML 定义完整（含 `required_tools`、`live_checks`、`readiness_levels`）
- [x] `make validate-gpu-scheduling-live-gate` 通过

## Validation

```bash
cd repo
make validate-gpu-scheduling-live-gate
```

## Development Record

### 变更文件

| 文件 | 类型 | 说明 |
|---|---|---|
| `repo/deploy/real-k8s-lab/gpu-scheduling-live-gate.yaml` | NEW | Live gate profile（含 5 个 live_checks + 2 个 readiness_levels） |
| `repo/deploy/real-k8s-lab/gpu-scheduling-smoke-a-job.yaml` | NEW | Smoke A 整卡 Job（schedulerName: volcano + nvidia.com/gpu: 1） |
| `repo/deploy/real-k8s-lab/gpu-scheduling-smoke-b-job.yaml` | NEW | Smoke B vGPU Job（HAMi 默认调度器 + nvidia.com/gpu: 1 split） |
| `repo/scripts/validate_gpu_scheduling_live_gate.py` | NEW | Live gate YAML 结构验证脚本 |
| `repo/Makefile` | MODIFY | 新增 `validate-gpu-scheduling-live-gate` target |

### Smoke A 验证结果

```
Job: ani-gpu-smoke-a (namespace: ani-system)
Status: Complete 1/1 (47s)
Scheduler: volcano
Node: dev-phys-03

Logs:
=== Smoke A: whole-GPU scheduling ===
GPU 0: NVIDIA GeForce RTX 4090 (UUID: GPU-204847f2-3318-aa4d-dea3-bf171ba14afe)
=== SCHEDULER: unknown ===
SMOKE_A_PASS
```

### Smoke B 验证结果

```
Job: ani-gpu-smoke-b (namespace: ani-system)
Status: Complete 1/1 (12s)
Scheduler: HAMi default (not volcano — HAMi webhook skips pods with different scheduler)
Node: dev-phys-03

Logs:
=== Smoke B: HAMi vGPU scheduling ===
GPU 0: NVIDIA GeForce RTX 4090 (UUID: GPU-275ae2c1-9655-f620-8c8e-b95c55f7bd6e)
[HAMI-core Msg(381:133878433277760:multiprocess_memory_limit.c:703)]: Cleanup on exit for PID 381
[HAMI-core Msg(381:133878433277760:multiprocess_memory_limit.c:739)]: Exit cleanup complete for PID 381
=== HAMi vGPU split (nvidia.com/gpu=1 of 10 per physical GPU) ===
SMOKE_B_PASS
```

### 遇到的问题

1. **HAMi vGPU 资源名称**：HAMi 2.9.0 使用 `--resource-name=nvidia.com/gpu`（默认），而非 `nvidia.com/vgpu`。每张物理 GPU 被 split 成 10 个 vGPU 单元（`--device-split-count=10`），所以节点 allocatable 显示 `nvidia.com/gpu: 20`（2 GPU × 10 split）。
2. **HAMi + Volcano 调度器冲突**：当 pod 指定 `schedulerName: volcano` 时，HAMi webhook 检测到 "Pod already has different scheduler assigned" 并跳过 vGPU binding，导致 `no binding pod found on node` 错误。Smoke B 改用默认调度器（HAMi 管理的）解决此问题。
3. **`nvidia.com/gpumem` 资源**：Volcano 调度器不识别 HAMi 的 `nvidia.com/gpumem` 自定义资源，导致 pod group 无法调度。Smoke B 移除了 `nvidia.com/gpumem` 限制，仅用 `nvidia.com/gpu: 1`。
4. **HAMi device plugin nodeSelector**：HAMi DaemonSet 默认 nodeSelector 是 `gpu=on`，但集群 GPU 节点没有此 label。通过 `kubectl label nodes ... gpu=on` 解决。
