# Issue 002: 新增 MeteringCollectionService port 接口和事件 schema

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/metering/prd-metering-consumer.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/metering/spec-metering-consumer.md`

## Description

新增 `MeteringCollectionService` interface 和 `InstanceLifecycleEvent` 事件 schema，为 consumer 和 rebuilder 提供采集生命周期控制契约。扩展 `MeteringUsageRecord` 新增 `ResourceRef` 字段。

## Scope
- Product line: core
- Code paths allowed: `repo/pkg/ports/`

## Acceptance Criteria
- [ ] 新增 `pkg/ports/instance_events.go`，定义 `InstanceLifecycleEvent` 结构（InstanceID/TenantID/WorkloadKind/NewStatus/EventSeq/GPUSpec/ErrorMsg）和 `GPUEventSpec` 结构
- [ ] 在 `pkg/ports/metering.go` 新增 `CollectionSpec` 结构（ResourceRef/TenantID/WorkloadKind/Dimensions/IntervalSec/StartedAt/GPUSpec）
- [ ] 在 `pkg/ports/metering.go` 新增 `CollectionDimension` 结构（ResourceType/Source）
- [ ] 在 `pkg/ports/metering.go` 新增 `MeteringCollectionService` interface，包含 `StartCollection(ctx, spec) error` 和 `StopCollection(ctx, resourceRef) error`
- [ ] `MeteringCollectionService` 与现有 `MeteringService` 分离（采集控制 vs 查询/上报）
- [ ] 扩展 `ports.MeteringUsageRecord` 新增 `ResourceRef string` 字段（现有 5 字段保持不变，新增字段无破坏性变更）
- [ ] `StartCollection`/`StopCollection` 文档注释说明幂等语义（进程内 map 去重 + DB UNIQUE 兜底 / 无 ticker 时 no-op）
- [ ] Typecheck/lint 通过

## Dependencies
None

## Type
core

## Priority
high

## Labels
core

## Batch
PR-M1

## SPEC Reference
- SPEC §3.2 Entity Definitions（Port 层）
- PRD FR-4, FR-5, FR-6
