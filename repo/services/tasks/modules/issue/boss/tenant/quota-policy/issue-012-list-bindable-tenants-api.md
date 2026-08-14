# 后端：查询可绑定套餐的租户列表 API

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)

## Description
前端绑定套餐时（issue-013 套餐列表页 → 绑定操作）需要知道哪些租户可以绑定该套餐。当前 `GET /tenant-plans/{planId}/tenants` 仅返回**已绑定**的租户，前端无法从已有接口获取**可绑定**的租户列表。实现 `GET /tenant-plans/{planId}/bindable-tenants` 端点，返回 status≠disabled 且未绑定该套餐的租户列表，供前端绑定弹窗渲染可选项。覆盖 US-018。

## Scope
- Product line: boss
- Code paths allowed: `repo/api/proto/tenant/v1/tenant_plan.proto`、`repo/api/openapi/services/v1.yaml`、`repo/services/ani-gateway/internal/router/tenant_plans.go`、`repo/services/tenant-service/internal/service/tenant_plan_service.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_store.go`、`repo/services/tenant-service/internal/repo/ports/tenant_store.go`

## Acceptance Criteria
### 查询可绑定租户列表（US-018）
- [x] `GET /tenant-plans/{planId}/bindable-tenants` 返回可绑定该套餐的租户列表
- [x] 可绑定条件：`status != 'disabled'` 且 `plan_id IS DISTINCT FROM {planId}`（未绑定该套餐）
- [x] 返回 `items[]{id, name, display_name, status}`，按 name 排序
- [x] 校验：plan_id 存在且 is_deleted=FALSE（否则 404 `TENANT_PLAN_NOT_FOUND`）
- [x] ~~支持可选 `search`~~（已按产品确认移除，不做关键字模糊查询）
- [x] `TenantPlanService.ListBindableTenants` gRPC 方法实现
- [x] proto 新增 `ListBindableTenants` RPC + `ListBindableTenantsRequest{plan_id}` + 复用 `BoundTenant` 响应
- [x] OpenAPI v1.yaml 新增 `GET /tenant-plans/{planId}/bindable-tenants` 端点定义
- [x] 网关 `tenant_plans.go` 新增 `listBindableTenants` handler + 路由注册

### 通用
- [x] 错误码映射：`TENANT_PLAN_NOT_FOUND`(404) / `VALIDATION_FAILED`(400) / `INTERNAL_ERROR`(500)
- [x] 单元测试：正常查询、套餐不存在 404、无可用租户返回空列表
- [x] `go build ./...` 编译通过

## Dependencies
#2（gRPC 接口与 ports）、#4（网关接入框架）、#9（TenantStore 基础设施）

## Type
backend

## Priority
medium

## References
- SPEC: §5.1.6 ListBindableTenants
- UX: §4.3 绑定套餐弹窗
