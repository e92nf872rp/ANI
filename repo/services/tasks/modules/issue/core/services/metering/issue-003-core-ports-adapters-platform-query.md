# [Core] Ports/Adapters 平台查询扩展——MeteringService port + LocalMeteringService

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/core/services/metering/spec-core-metering-service.md`

## Description

在 `pkg/ports/metering.go` 的 `MeteringUsageQueryRequest` 新增 `IsPlatform bool` 字段。在 `pkg/adapters/runtime/local_metering_service.go` 新增平台查询实现：`IsPlatform=true` 时遍历所有租户 reports，按 tenant_id 聚合，返回 `items[].tenant_id` 必填。

## Scope
- Product line: core
- Code paths allowed: `repo/pkg/ports/metering.go`, `repo/pkg/adapters/runtime/local_metering_service.go`

## Acceptance Criteria
- [ ] `MeteringUsageQueryRequest` 新增 `IsPlatform bool`
- [ ] LocalMeteringService.QueryUsage 支持 `IsPlatform=true`：遍历全租户、按 tenant_id 聚合
- [ ] 平台视角下返回 `items[].tenant_id` 非空
- [ ] local profile 仍仅产出 Token 数据（`instance_*` 为空为预期）
- [ ] `dev_profile.real_provider=false` 在平台查询响应中保留
- [ ] 单元测试: 全租户聚合、tenant_id 筛选、group_by=tenant_id

## Dependencies
#1（OpenAPI v1.yaml FR-8 契约扩展）

## Type
core

## Priority
high

## Labels
core

## Batch
M-METERING-PLATFORM-A

## References
- SPEC: §3.2, §6.1, §6.4
- PRD: FR-7, FR-9, FR-12
