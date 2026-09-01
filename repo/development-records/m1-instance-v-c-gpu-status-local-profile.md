# M1-INSTANCE-V-C — GPU Status Local Profile

完成日期：2026-05-20
对应 Sprint：Sprint 2（2026-05-19 提前启动；计划窗口 2026-06-01 ~ 06-15）
验证结果：make test EXIT:0，make validate-core-alpha EXIT:0，make validate-demo-instances EXIT:0，make validate-instance-store EXIT:0，make validate-instance-service EXIT:0，make validate-architecture EXIT:0，git diff --check EXIT:0

## 实现了什么

完成 M1-INSTANCE-V 的第三个可验证切片：GPU Container 在 local/dev profile 中返回 `gpu.vendor`、`gpu.model`、`gpu.count`、`gpu.scheduling_reason` 和 `gpu.utilization_percent`。该状态随实例记录持久化到 `gpu_status` JSONB，Gateway `/api/v1/instances` 响应可直接供 Services/Console 展示调度原因和利用率占位。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/workload_runtime.go` | 修改 | 增加 `GPUInstanceStatus` 和 `WorkloadInstanceRecord.GPU` |
| `pkg/adapters/runtime/instance_orchestrator.go` | 修改 | GPU Container create 时生成 GPU 状态摘要 |
| `pkg/adapters/runtime/instance_store.go` | 修改 | `workload_instances.gpu_status` JSONB 持久化读写 |
| `services/ani-gateway/internal/router/demo_instances.go` | 修改 | 响应返回 `gpu` 状态摘要 |
| `deploy/migrations/20260519000400_instance_u_vm_protection.sql` | 修改 | 增加 `gpu_status` JSONB 列 |
| `scripts/validate_core_alpha_contract.py` | 修改 | 合同守卫覆盖 GPU 状态字段和持久化 token |
| `pkg/adapters/runtime/instance_orchestrator_test.go` | 修改 | 覆盖 GPU 状态生成 |
| `services/ani-gateway/internal/router/demo_instances_test.go` | 修改 | 覆盖 demo service 返回 GPU 状态 |

## 完工标准达成

- [x] GPU Container 返回 `vendor/model/count`
- [x] GPU Container 返回 `scheduling_reason`
- [x] GPU Container 返回 `utilization_percent`
- [x] GPU 状态持久化到 instance record
- [x] `make test` 通过
- [x] `make validate-architecture` 通过

## 备注

本切片完成 Core API Alpha 可依赖的 GPU 状态读取面；真实 DCGM/HAMi/厂商监控采样和 provider-native 利用率回写留给 real provider beta 路径继续对齐。
