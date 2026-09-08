# 数据库迁移：tenant_admin_invitation 建表 + Core 用户/角色表变更

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/tenant/prd-new-boss-tenant-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/tenant/ux-boss-tenant-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md`

## Description
新增租户管理员邀请独立表 `tenant_admin_invitation`，承载邀请生命周期（inviting→accepted/rejected/expired），不改 users.status、不预绑角色。同时对 Core 层 `users` 表补充 `display_name` / `is_deleted` / `deleted_at` 列以支持租户管理员模块的软删除与展示需求。

## Scope
- Product line: boss
- Code paths allowed: `repo/deploy/migrations/`（迁移文件）
- 禁止：.go 实现、前端页面

## Acceptance Criteria

### 3.1.1 Services 层：tenant_admin_invitation 建表
- [ ] 迁移文件 DDL（对齐 SPEC §3.1.1）：
  ```sql
  CREATE TABLE tenant_admin_invitation (
      id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
      tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
      user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
      token_hash   TEXT NOT NULL,                          -- SHA-256(原始 token)，不存明文
      status       TEXT NOT NULL CHECK (status IN ('inviting','accepted','rejected','expired')),
      expire_at    TIMESTAMPTZ NOT NULL,
      created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
      accepted_at  TIMESTAMPTZ,
      rejected_at  TIMESTAMPTZ
  );
  CREATE UNIQUE INDEX uk_tenant_admin_invitation_token_hash ON tenant_admin_invitation(token_hash);
  CREATE INDEX idx_tenant_admin_invitation_user_status ON tenant_admin_invitation(user_id, status);

  -- RLS：平台上下文可读写全部行；租户上下文仅本租户行
  ALTER TABLE tenant_admin_invitation ENABLE ROW LEVEL SECURITY;
  ALTER TABLE tenant_admin_invitation FORCE ROW LEVEL SECURITY;
  CREATE POLICY tenant_admin_invitation_platform_bypass
      ON tenant_admin_invitation
      AS PERMISSIVE FOR ALL
      USING (
          current_setting('app.current_tenant_id', true) IS NULL
          OR current_setting('app.current_tenant_id', true) = ''
      );
  CREATE POLICY tenant_admin_invitation_tenant_self
      ON tenant_admin_invitation
      AS PERMISSIVE FOR ALL
      USING (
          tenant_id IS NOT NULL
          AND tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid
      );

  -- GRANT 表级读写给 ani_app_user
  GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_admin_invitation TO ani_app_user;
  ```
- [ ] 字段说明对齐 SPEC §3.1.1（token 仅存 SHA-256 哈希、status CHECK、expire_at=now()+72h 由应用层写入）
- [ ] RLS 策略对齐现有模式（`platform_bypass` + `tenant_self` PERMISSIVE，参照 `audit_logs` / `tenant_quota_change`）

### 3.1.2 Core 层：users 表列扩展
- [ ] 迁移文件 DDL（对齐 SPEC §3.1.2）：
  ```sql
  -- display_name：租户管理员展示名（昵称），NULL 表示使用 username
  ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name TEXT;

  -- is_deleted / deleted_at：软删除标记，TenantAdminSvcClient.SoftDelete 使用
  ALTER TABLE users ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN NOT NULL DEFAULT FALSE;
  ALTER TABLE users ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
  ```
- [ ] 不破坏现有约束（users.status 仍为 active/disabled，is_deleted 为新增独立软删除标记）

### 通用
- [ ] 迁移可回滚（rollback：DROP TABLE tenant_admin_invitation；DROP COLUMN display_name/is_deleted/deleted_at）
- [ ] 迁移文件命名对齐现有 `YYYYMMDD_NNN_name.sql` 约定（如并入租户管理迁移批次）
- [ ] 迁移执行成功（`make migrate` 或对应流程），表结构与索引就位

## Dependencies
None

## Type
backend

## Priority
high

## References
- SPEC: §3.1 Schema Changes / §3.4 Migration Plan
- Plan: 租户管理plan v3.0 §4.1.4b / §12.3.1
