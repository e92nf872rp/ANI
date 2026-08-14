# 后端：套餐列表 + 详情 API

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)

## Description
实现套餐查询后端逻辑（两个关联读 API）：`GET /api/v1/svc/tenant-plans`（列表，游标分页 + status/search 过滤）与 `GET /api/v1/svc/tenant-plans/{planId}`（详情）。填充 `ListTenantPlans`/`GetTenantPlan` RPC 与 `TenantPlanStore.List`/`GetByID`。覆盖 US-002、US-003。

> **实现补充说明：** 原 issue 规格提到实现 `TenantPlanStore.GetByCode`（代码查询用，内部校验唯一性），实际未实现独立 `GetByCode` 方法。code 唯一性由数据库 partial unique index 保证，Create 时 unique violation → `PLAN_CODE_CONFLICT`，无需查询校验。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/tenant_plan_service.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go`

## Acceptance Criteria
- [x] `GET /tenant-plans` 游标分页（limit/cursor + next_cursor）+ status 过滤 + search 模糊匹配 name（ILIKE）
- [x] items 含 id/code/name/status/tenant_count/created_at/updated_at，不含 quota_limits；total=满足筛选条件总条数；软删除套餐不出现
- [x] 实现 `TenantPlanStore.List`：游标分页查询返回 `TenantPlanListResult`（Items/Total/NextCursor 具体类型，不用泛型）+ tenant_count 子查询（COUNT tenants WHERE plan_id=? AND status != 'disabled'）
- [x] `GET /tenant-plans/{planId}` 返回完整对象，不存在/已软删除 → 404 `TENANT_PLAN_NOT_FOUND`
- [x] 实现 `TenantPlanStore.GetByID`（WHERE id=$id AND is_deleted=FALSE）+ ~~`GetByCode`~~ — **未实现独立 `GetByCode`**，code 唯一性由数据库 partial unique index 保证
- [x] `TenantPlanService.ListTenantPlans` / `GetTenantPlan` RPC 方法体实现，返回 `tenantv1.ListTenantPlansResponse` / `tenantv1.TenantPlan`
- [x] 网关层 GET 转发与错误码映射在 #4 网关 issue 完成
- [x] 单元测试：列表分页/status/search、tenant_count、详情 404（SPEC §9.1 TestTenantPlanService_*）
- [x] `go build ./...` 编译通过

## Dependencies
#2、#3、#4

## Type
backend

## Priority
high

## References
- SPEC: §5.1 / §4.2 列表与详情 schema / §6.1 / §9
- Plan: 租户管理plan v3.0 §5.3
