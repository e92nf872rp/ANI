# [Core] OpenAPI v1.yaml FR-8 契约扩展——新增平台查询端点 + 补全租户读 scope

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/core/services/metering/spec-core-metering-service.md`

## Description

在 `repo/api/openapi/v1.yaml` 中新增 `GET /metering/usage/platform` 端点（operationId=`getPlatformMeteringUsage`，x-ani-rbac-scope=`scope:metering:platform:read`），补全 `GET /metering/usage` 的 operationId=`getMeteringUsage` + scope=`scope:metering:read`。平台 group_by 独立 enum `[tenant_id, day, hour]`，租户 group_by 不变 `[resource_type, az, day, hour]`。

## Scope
- Product line: core
- Code paths allowed: `repo/api/openapi/v1.yaml`

## Acceptance Criteria
- [ ] `GET /metering/usage` 补全 `operationId: getMeteringUsage` + `x-ani-rbac-scope: scope:metering:read`
- [ ] `GET /metering/usage/platform` 新增端点，operationId=`getPlatformMeteringUsage`，scope=`scope:metering:platform:read`
- [ ] 平台 group_by enum: `[tenant_id, day, hour]`；租户 group_by 不变: `[resource_type, az, day, hour]`
- [ ] 平台端点可选 `tenant_id` query 参数（须二次 RBAC 校验描述）
- [ ] `MeteringUsageResponse` 复用；平台视角下 `items[].tenant_id` 必填（端点级约束）
- [ ] `make validate-openapi` / YAML lint 通过

## Dependencies
None — 这是依赖链的起点，所有其他 Issue 依赖此契约。

## Type
core

## Priority
high

## Labels
core

## Batch
M-METERING-PLATFORM-A

## References
- SPEC: §4.2, §4.3
- PRD: FR-8, FR-10
