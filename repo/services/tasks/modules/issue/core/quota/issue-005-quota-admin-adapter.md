# 实现 QuotaAdminService 租户生命周期管理 adapter

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/quota/prd-quota-service.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/quota/spec-quota-service.md`
- Plan: `repo/services/tasks/modules/plan/plan-quota-service.md`

## Description

实现 `QuotaAdminService` 的 PG adapter，支持批量新建/修改/查询/删除租户配额和查询配额元数据。与 #3/#4 共享同一个 `PostgresQuota` struct，在同一文件 `pkg/adapters/runtime/postgres_quota.go` 中追加方法。

## Scope

- Product line: core
- Code paths allowed: `pkg/adapters/runtime/postgres_quota.go`

## Acceptance Criteria

- [ ] `CreateTenantQuota`：自开 `WithPlatformTx`，校验租户存在（tenants 表）+ meta enabled → total<=0 取 default_quota → ON CONFLICT DO NOTHING 跳过已存在 → 回读 items 涉及维度
- [ ] `UpdateTenantQuota`：自开 `WithPlatformTx`，校验 meta enabled → `SET total = GREATEST($3, reserved + used)` 缩容 clamp → 行不存在返回 `ErrQuotaNotFound` → 回读计算 tightened 标记（回读 total > 请求 total 时 tightened=true）
- [ ] `GetTenantQuota`：自开 `WithPlatformTx`，JOIN resource_quota_meta 返回 unit/display_name/is_discrete
- [ ] `DeleteTenantQuota`：自开 `WithPlatformTx`，校验租户存在 → 删除 resource_reservations + resource_quota（不守卫 used/reserved）
- [ ] `ListQuotaMeta`：自开 `WithPlatformTx`，返回 enabled=true 的维度列表（含 display_name/unit/default_quota/is_discrete），ORDER BY resource_type
- [ ] Typecheck/lint 通过

## Dependencies

#0（RLS 前提验证通过）, #2

## Type

core

## Priority

high

## Labels

core

## Batch

TBD

## References

- SPEC: §5.2 (Create/Update/Get/Delete/ListQuotaMeta), §5.4
- Plan: §6
