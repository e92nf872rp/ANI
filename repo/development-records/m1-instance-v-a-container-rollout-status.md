# M1-INSTANCE-V-A — Container Rollout Status

完成日期：2026-05-20
对应 Sprint：Sprint 2（2026-05-19 提前启动；计划窗口 2026-06-01 ~ 06-15）
验证结果：make test EXIT:0，make validate-core-alpha EXIT:0，make validate-demo-instances EXIT:0，make validate-instance-store EXIT:0，make validate-instance-service EXIT:0，make validate-architecture EXIT:0，git diff --check EXIT:0

## 实现了什么

完成 M1-INSTANCE-V 的第一个可验证切片：Container/GPU Container 在 create 后生成可依赖的 rollout 状态摘要，包括 `replicas`、`ready_replicas`、`revision`、`rollout_status` 和 `history[]`。Gateway `/api/v1/instances` 响应现在返回 `container` 字段，CreateInstanceRequest 的 `replicas` 会进入本地 profile 并反映在实例记录中。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/workload_runtime.go` | 修改 | 增加 `ContainerInstanceStatus`、`ContainerRevisionHistory` 和 `ContainerInstanceSpec.Replicas` |
| `pkg/adapters/runtime/instance_orchestrator.go` | 修改 | Container/GPU Container create 时生成 revision、rollout status 和 history |
| `pkg/adapters/runtime/instance_store.go` | 修改 | `workload_instances.container_status` JSONB 持久化读写 |
| `services/ani-gateway/internal/router/demo_instances.go` | 修改 | create 支持 `replicas`，响应返回 `container` 状态摘要 |
| `api/openapi/v1.yaml` | 修改 | `InstanceRecord.container` 和 `CreateInstanceRequest.replicas` 进入 Core Alpha 契约 |
| `deploy/migrations/20260519000400_instance_u_vm_protection.sql` | 修改 | 增加 `container_status` JSONB 列 |
| `scripts/validate_core_alpha_contract.py` | 修改 | 合同守卫覆盖 container rollout 字段和持久化 token |
| `pkg/adapters/runtime/instance_orchestrator_test.go` | 修改 | 覆盖 Container rollout 状态生成 |
| `services/ani-gateway/internal/router/demo_instances_test.go` | 修改 | 覆盖 demo service 返回 container rollout 状态 |

## 完工标准达成

- [x] Container/GPU Container create 支持 `replicas`
- [x] InstanceRecord 返回 `container.replicas/ready_replicas/revision/rollout_status/history`
- [x] rollout 状态持久化到 instance record
- [x] `make test` 通过
- [x] `make validate-architecture` 通过

## 备注

本切片先稳定 Container/GPU Container 的读取面；rollback、rollout history 更新和 GPU 调度原因/利用率已分别由 V-B、V-C 闭环。
