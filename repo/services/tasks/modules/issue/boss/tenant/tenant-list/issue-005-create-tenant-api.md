# Issue 5: 可用套餐 + 创建租户 — GET /tenants/available-plans + POST /tenants

> **Status: DONE（以实现为准）**  
> 证据：`repo/development-records/tenant-list-issue-005-create-tenant-api.md`

## Document Links
- PRD: [prd-new-boss-tenant-list.md](../../../../prd/boss/tenant/prd-new-boss-tenant-list.md)
- UX: [ux-boss-tenant-list.md](../../../../ux/boss/tenant/ux-boss-tenant-list.md)
- SPEC: [spec-new-boss-tenant-list.md](../../../../spec/boss/tenant/spec-new-boss-tenant-list.md)

## Description

US-001 / US-002：`ListAvailablePlans`（自有 tenant_plans，仅 active）+ `CreateTenant` 编排（校验 → bcrypt → Core 事务创建 → 事务外配额初始化/补偿 → 审计）。Core `PostgresTenant.CreateTenant` 单事务写 tenants / tenant_auth / users / user_roles / tenant_lifecycle(`create`)。

## Scope
- Product line: boss
- Code paths:
  - `repo/services/tenant-service/internal/service/tenant_service.go`
  - `repo/pkg/adapters/runtime/postgres_tenant.go`
  - `repo/services/tenant-service/internal/repo/adapters/core/tenant_svc_client.go`
  - 复用既有 tenant_plans store

## Acceptance Criteria

### ListAvailablePlans
- [x] 仅 `status='active'`；`{ items: [{id,code,name}] }`；不分页；不调 Core
- [x] draft/disabled 排除；空列表合法

### service CreateTenant
- [x] name / email / admin_password 强度校验 → 400
- [x] plan 非 active → 422 PLAN_NOT_ACTIVE；不存在 → 404
- [x] bcrypt(cost=12) 后传 Core `admin_password_hash`；明文不出 service
- [x] name 冲突 → 409 TENANT_NAME_CONFLICT
- [x] Core 成功后 UpsertQuota；失败不回滚租户，重试/审计 failure
- [x] 审计 `tenant.create`（不含密码）
- [x] 响应 `{ id, message }`；幂等由 Gateway 处理

### Core CreateTenant
- [x] 单事务：tenants(active) + tenant_auth + users + user_roles(tenant-admin) + lifecycle(`create`)
- [x] lifecycle **user_id / request_id 来自 ctx**（`lifecycleAttributionArgs`），非方法入参
- [x] UNIQUE 冲突 → `ErrTenantNameConflict`；事务原子回滚

### 测试
- [x] 单测覆盖 ListAvailablePlans / 密码边界 / PLAN_NOT_ACTIVE / 冲突映射（真库集成非强制门禁）

## Implementation notes
- 归因链路：`x-user-id` / `x-request-id` → `X-ANI-Actor-User-ID` / `X-Request-ID` → Core ctx。

## Dependencies
Issue 3、4

## Type
backend

## Priority
high

## References
- SPEC: §2.3 / §5.1 / §5.2
- Record: `repo/development-records/tenant-list-issue-005-create-tenant-api.md`
