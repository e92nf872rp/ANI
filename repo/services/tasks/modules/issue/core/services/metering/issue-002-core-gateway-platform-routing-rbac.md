# [Core] Gateway 平台查询路由 + 分轨鉴权（FR-15）

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/core/services/metering/spec-core-metering-service.md`

## Description

在 `metering_resources.go` 新增 `GET /metering/usage/platform` 路由处理器，实现按 path 分轨鉴权：租户 path 校验 `scope:metering:read` 从 JWT 取 tenant_id；平台 path 校验 `scope:metering:platform:read`，可选 tenant_id query 须二次 RBAC 校验。

## Scope
- Product line: core
- Code paths allowed: `repo/services/ani-gateway/internal/router/metering_resources.go`, `repo/services/ani-gateway/internal/middleware/rbac.go`

## Acceptance Criteria
- [ ] `registerMetering` 新增 `v1.GET("/metering/usage/platform", api.queryPlatformUsage)`
- [ ] 租户 path: 从 JWT 提取 tenant_id，忽略 query 中的 tenant_id
- [ ] 平台 path: 校验 `scope:metering:platform:read`；带 tenant_id query 时二次 RBAC 校验
- [ ] 平台视角下 `items[].tenant_id` 必填
- [ ] 400: start_time ≥ end_time 或 group_by 枚举非法
- [ ] 403: 缺少对应 scope
- [ ] 集成测试: 租户/平台 scope 分离校验

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
- SPEC: §5.1, §6.1, §7.1, §8.1
- PRD: FR-8, FR-15, FR-16
