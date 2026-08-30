# Instance Orchestrator + Store + Outbox + Migrations + Bootstrap 配置

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
- UX: N/A（非前端）
- SPEC: `repo/services/tasks/modules/spec/core/gpu-spec-quota/spec-gpu-spec-quota.md`

## Description

作为开发者，扩展 instance_orchestrator/instance_store/demo_instances 实现 TryManyTx 预占 + Volcano 翻译 + Cancel 异常分支（含 outbox 事件写入）、新增 WorkloadInstanceStoreTx 实现、定义并接线 OutboxWriter 接口、新增两个 migration、扩展 bootstrap 配置和依赖注入。

## Scope

- Product line: core
- Code paths allowed: `repo/pkg/adapters/runtime/instance_orchestrator.go`、`instance_store.go`、`demo_instances.go`、`outbox_writer.go`、`volcano_queue_store.go`、`repo/migrations/`、`repo/pkg/bootstrap/`

## Acceptance Criteria

- [ ] 扩展 `instance_orchestrator.go`：删除时调 Quota.Release（已有接口）+ Apply 失败保留 DB 行 UpsertStatusTx(failed)
- [ ] 扩展 `instance_store.go`：新增 `WorkloadInstanceStoreTx` 实现（UpsertStatusTx）
- [ ] 扩展 `demo_instances.go`：API 层 TryManyTx 预占（同事务）+ Volcano 资源翻译 + Apply 异常分支 Cancel
- [ ] 新增 `pkg/adapters/runtime/outbox_writer.go`：定义 `OutboxWriter` 小接口（导出）+ `metadataOutboxWriter` 生产实现 + `MockOutboxWriter` 测试 mock
- [ ] `demo_instances.go` Apply 失败路径：在 `Cancel` 同事务内调用 `outboxWriter.WriteTx(event_type='instance.create_failed')`（plan.md §6.3.1 行 570）
- [ ] `bootstrap/deps.go`：`GPU_QUOTA_ENABLED=true` 时构造 `NewMetadataOutboxWriter()` 并注入 `QuotaAwareInstanceOrchestrator`（`WithQuotaAwareOutboxWriter`）
- [ ] 扩展 `volcano_queue_store.go`：VolcanoQueueCRD 新增 Status 字段（含 Allocated + State）+ crdToQueue 映射 allocated 与 state（Open→open / Closed→closed / 空/其他→unknown 大小写归一）到 GPUSchedulingQueue.Status
- [ ] 新增 `migrations/quota_tx_ids.sql`：workload_instances 新增 `quota_tx_ids JSONB` 列（NOT NULL DEFAULT '[]'）
- [ ] 新增 `migrations/resource_reservation_allocations.sql`：BOSS 预留账本表（tenant_id → allocated_gpu_count，单维度不分 spec，PK tenant_id，CHECK >= 0）
- [ ] 扩展 `bootstrap/server.go`：新增 `GPUQuotaEnabled` + `GPU_QUOTA_ENABLED` + `PROVISIONING_TIMEOUT_MIN` 配置项
- [ ] 扩展 `bootstrap/deps.go`：注入 GPUSpecStore + WorkloadInstanceStoreTx + 已有 PostgresQuota + OutboxWriter（reconciler + orchestrator 双注入）
- [ ] GPU_QUOTA_ENABLED=false 时 TryManyTx/Confirm/Cancel/Release 全跳过（配额完全旁路），true 时强制生效
- [ ] `go build ./pkg/... ./services/ani-gateway/...` 通过
- [ ] `go test ./pkg/adapters/runtime/ -run "TestUpsertStatusTx|TestQuotaEnabledSwitch" -count=1` 通过
- [ ] `make validate-architecture` 通过
- [ ] `git diff --check` 通过

## Dependencies

#4, #6

## Type

core

## Priority

high

## Labels

core

## Batch

M2.1-TASK-A

## References

- SPEC: §3.1 (Migrations), §5.1 (三道闸校验, GPU_QUOTA_ENABLED 旁路, outbox 同事务写入), §2.4 (File Structure)
- plan.md: §6.3.1 (行 570, Apply 失败 outbox INSERT), §6.3.2 (行 597-606, 状态转移 outbox INSERT)
- UX: N/A
