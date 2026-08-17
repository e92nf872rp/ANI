# Issue 006: 新增 buildSpec 维度映射函数 + parseGPUCount

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/metering/prd-metering-consumer.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/metering/spec-metering-consumer.md`

## Description

实现共享的 `buildSpec` 函数，根据 workload_kind 硬编码维度映射，供 consumer 和 rebuilder 共用。包含 `parseGPUCount` 从 gpu_status JSONB 解析 GPU 卡数。

## Scope
- Product line: core
- Code paths allowed: `repo/services/metering-service/internal/`

## Acceptance Criteria
- [ ] 新增 `services/metering-service/internal/spec.go`
- [ ] `buildSpec(tenantID, instanceID, kind string, gpuCount int) ports.CollectionSpec` 作为 internal 包级函数
- [ ] 维度映射硬编码：`gpu_container` → 3 维（GPU+CPU+Mem），`vm` → CPU+Mem，`container` → CPU+Mem，其他 kind → CPU+Mem
- [ ] `gpuCount > 0` 时设置 `spec.GPUSpec = &ports.GPUEventSpec{Count: gpuCount}`，否则 GPUSpec 为 nil
- [ ] `IntervalSec` 默认 60，`StartedAt` 默认 `time.Now()`
- [ ] consumer 调用：`buildSpec(event.TenantID, event.InstanceID, event.WorkloadKind, gpuCount)`，gpuCount 从 `event.GPUSpec.Count` 提取（nil 则 0）
- [ ] rebuilder 调用：`buildSpec(tenantID, instanceID, kind, gpuCount)`，gpuCount 从 `parseGPUCount(gpuStatusJSON)` 提取
- [ ] `parseGPUCount(gpuStatusJSON []byte) int`：解析 `{"count": N}` 格式，缺失或解析失败返回 0
- [ ] Typecheck/lint 通过

## Dependencies
- Issue #002（port 接口 CollectionSpec）

## Type
core

## Priority
high

## Labels
core

## Batch
PR-M2

## SPEC Reference
- SPEC §5.1.7 buildSpec 维度映射
- PRD FR-23
