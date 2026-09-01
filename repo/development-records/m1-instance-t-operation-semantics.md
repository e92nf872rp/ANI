# M1-INSTANCE-T — 操作语义横切基础

完成日期：2026-05-18
对应 Sprint：Sprint 1（2026-05-15 ~ 05-31）
验证结果：make build EXIT:0；make test EXIT:0；make validate-architecture passed；Go unit tests passed；Python RAG compileall passed；api/openapi/dev.yaml YAML parse passed

## 实现了什么

为实例创建和生命周期操作补齐 `operation_id`、幂等键回放、操作状态、时间线步骤和查询入口。当前实现覆盖 local dev profile 与 metadata store 边界，让 Services/Console 可以基于实例操作历史对接创建、启动、停止、重启、变配和删除链路。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/workload_runtime.go` | 修改 | 新增 WorkloadOperationStore、操作/步骤模型，并在实例结果/记录中暴露 operation_id |
| `pkg/adapters/runtime/operation_store.go` | 新增 | 新增 LocalOperationStore 与 MetadataOperationStore，实现操作记录、幂等键查询、步骤追加和列表查询 |
| `pkg/adapters/runtime/instance_service.go` | 修改 | Create/Start/Stop/Restart/Resize/Delete 接入 operation store、timeline 和幂等回放 |
| `deploy/migrations/20260502000200_operations_idempotency.sql` | 修改 | 对齐当前 instance_id/requested_by 字段类型，并修正 tenant RLS setting |
| `services/ani-gateway/internal/router/demo_instances.go` | 修改 | demo/real path 增加 operation 查询路由、operation_id 响应和 idempotency_key 入参 |
| `api/openapi/dev.yaml` | 修改 | 对齐 demo API 的创建响应、生命周期幂等键和 operation 查询契约 |
| `pkg/adapters/runtime/instance_service_test.go` | 修改 | 覆盖 create/lifecycle operation 记录、步骤和幂等回放 |
| `services/ani-gateway/internal/router/demo_instances_test.go` | 修改 | 覆盖 demo operation 查询链路 |

## 完工标准达成

- [x] `make test` 全通
- [x] `make validate-architecture` 通过
- [x] Create 返回非空 `operation_id`
- [x] 同 `idempotency_key` 重复 Create 返回同一 `operation_id`，不重复创建实例
- [x] Lifecycle/Resize 记录 `operation_id` 与 timeline steps
- [x] `GET /instances/{id}/operations` 与 `GET /instance-operations/{id}` 可查询操作记录

## 备注

本批次完成的是操作语义底座，不等同于完整生产级幂等控制。`M1-IDEM-A` 已在后续批次补齐 metadata store 原子幂等锁、正式 API 的 409/回放策略和 bootstrap wiring；真实 PostgreSQL 并发压测仍归入后续 integration profile。
