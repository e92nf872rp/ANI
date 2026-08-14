# 后端：更新套餐基本信息 API（name / description）

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)
- Core API: [配额core层对接api设计.md](../../../../../配额core层对接api设计.md)

## Description
实现套餐基本信息更新端点 `PUT /tenant-plans/{planId}`，允许 platform-admin / platform-ops 修改套餐的 `name` 和 `description` 字段（不影响限额、状态、绑定关系）。填充 `TenantPlanService.UpdateTenantPlan` RPC，复用 store 层已有的 `Update` 方法。覆盖 US-016。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/tenant_plan_service.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go`、`repo/services/tenant-service/internal/repo/ports/tenant_plan_store.go`、`repo/api/proto/tenant/v1/tenant_plan.proto`、`repo/api/openapi/services/v1.yaml`、`repo/services/ani-gateway/internal/router/tenant_plans.go`、`repo/services/ani-gateway/internal/router/router.go`

## Acceptance Criteria
### 更新套餐基本信息（US-016）
- [x] `PUT /tenant-plans/{planId}` 入参 `name`（可选）、`description`（可选），需 `idempotency_key`（可选）
- [x] 校验：plan_id 存在且 is_deleted=FALSE（否则 404 `TENANT_PLAN_NOT_FOUND`）
- [x] name 和 description 均为可选字段：未传或为 null 表示不更新，传空串表示清空（proto `StringValue` 可选语义）
- [x] 复用 store 层 `Update(ctx, id, UpdateTenantPlanInput)` 方法（已实现）
- [x] 更新成功后写审计 action='tenant_plan.update'，details={plan_id, name_updated, description_updated}
- [x] 响应 `{ id, message: "tenant plan updated" }`
- [x] `TenantPlanService.UpdateTenantPlan` gRPC 方法体实现
- [x] proto 新增 `UpdateTenantPlan` RPC + `UpdateTenantPlanRequest` / `UpdateTenantPlanResponse` message
- [x] OpenAPI v1.yaml 新增 `PUT /tenant-plans/{planId}` 端点定义
- [x] 网关 `tenant_plans.go` 新增 `updateTenantPlan` handler + 路由注册

### 通用
- [x] 错误码映射：`TENANT_PLAN_NOT_FOUND`(404) / `VALIDATION_FAILED`(400) / `INTERNAL_ERROR`(500)
- [x] 单元测试：正常更新 name、正常更新 description、同时更新两者、套餐不存在 404、空 body 不报错
- [x] `go build ./...` 编译通过

## Dependencies
#2（gRPC 接口与 ports）、#4（网关接入框架）

## Type
backend

## Priority
medium

## References
- SPEC: §5.1.4 UpdateTenantPlan / §6.1 错误码
- Plan: 租户管理plan v3.0 §5.2
