# Issue 3: 数据库迁移 — tenant_list_management（Core 侧表与列扩展）

> **Status: DONE（以实现为准）**  
> 证据：`repo/development-records/tenant-list-issue-003-database-migration.md`  
> 文件：`repo/deploy/migrations/20260902_001_tenant_list_management.sql`

## Document Links
- PRD: [prd-new-boss-tenant-list.md](../../../../prd/boss/tenant/prd-new-boss-tenant-list.md)
- UX: [ux-boss-tenant-list.md](../../../../ux/boss/tenant/ux-boss-tenant-list.md)
- SPEC: [spec-new-boss-tenant-list.md](../../../../spec/boss/tenant/spec-new-boss-tenant-list.md)

## Description

单迁移：tenants 列扩展与三态 CHECK、`tenant_auth`、`tenant_lifecycle`、RLS/GRANT、存量映射与 lifecycle create 回填。

## Scope
- Product line: boss（Core 数据层）
- Code paths: `repo/deploy/migrations/20260902_001_tenant_list_management.sql`

## Acceptance Criteria

### tenants
- [x] CHECK：`status IN ('active','frozen','disabled')`；存量 `suspended→frozen`、`deleted→disabled`
- [x] 列：`contact_email` / `frozen_at` / `disabled_at`（`ADD COLUMN IF NOT EXISTS`）

### tenant_auth
- [x] 1:1 PK=tenant_id；sso_enabled / sso_provider / mfa_required / updated_at
- [x] 存量回填：`INSERT … SELECT id FROM tenants ON CONFLICT DO NOTHING`

### tenant_lifecycle
- [x] action CHECK：`create|freeze|unfreeze|disable`；reason/user_id/request_id 可空
- [x] 索引：`(tenant_id, created_at DESC)`
- [x] **存量回填**：对尚无 lifecycle 的租户插入 `action='create'`（`created_at = tenants.created_at`，幂等 `WHERE NOT EXISTS`）

### RLS 与权限
- [x] 平台绕过使用 **`NULLIF(current_setting('app.current_tenant_id', true), '') IS NULL`**（非裸 `IS NULL`）
- [x] `GRANT SELECT, INSERT, UPDATE, DELETE` TO `ani_app`
- [x] 不新建 `tenant_quota_change` / `audit_logs`
- [x] 文件头 Depends on / Rationale / Rollback 完整

## Dependencies
None（可与 1/2 并行）

## Type
backend

## Priority
high

## References
- SPEC: §3.1 / §3.3 / §3.4
- Record: `repo/development-records/tenant-list-issue-003-database-migration.md`
