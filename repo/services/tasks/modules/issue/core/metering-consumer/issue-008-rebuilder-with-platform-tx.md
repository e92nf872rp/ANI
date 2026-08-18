# Issue 008: 实现 rebuilder（直接查 DB + WithPlatformTx 绕 RLS）

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/metering/prd-metering-consumer.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/metering/spec-metering-consumer.md`

## Description

实现 rebuilder，启动时跨租户查所有 running 实例并建 ticker。用 `WithPlatformTx` 绕 RLS，不新增真相源，PG 为唯一 source of truth。

## Scope
- Product line: core
- Code paths allowed: `repo/services/metering-service/internal/`

## Acceptance Criteria
- [ ] 新增 `services/metering-service/internal/rebuilder.go`
- [ ] `Rebuilder` 结构持有 `metadataStore ports.MetadataStore`（用 WithPlatformTx 绕 RLS）、`metering ports.MeteringCollectionService`、`logger *slog.Logger`
- [ ] `Rebuild(ctx)` 用 `metadataStore.WithPlatformTx` 跨租户查 `workload_instances WHERE state='running'`
- [ ] SQL 查询 4 个字段：`tenant_id::text`、`instance_id`、`workload_kind`、`gpu_status`（JSONB）
- [ ] 查询用 `ORDER BY updated_at ASC`
- [ ] 解析 `gpu_status` JSONB 获取 GPU 卡数（`{"count": N}`，缺失返回 0）——调用 `parseGPUCount`
- [ ] 对每个 running 实例调 `buildSpec` + `metering.StartCollection`
- [ ] 单个实例 StartCollection 失败不阻塞，记 Error 日志继续重建其余实例
- [ ] 重建完成后记 Info 日志（"rebuild done", running_instances count）
- [ ] 单测覆盖：WithPlatformTx 调用、running 实例建 ticker、gpu_status 解析、单实例失败不阻塞
- [ ] Typecheck/lint 通过

## Dependencies
- Issue #002（port 接口）
- Issue #004（meteringCollectionService）
- Issue #006（buildSpec + parseGPUCount）

## Type
core

## Priority
high

## Labels
core

## Batch
PR-M3

## SPEC Reference
- SPEC §5.1.5 Rebuilder Rebuild
- PRD FR-35 ~ FR-37
