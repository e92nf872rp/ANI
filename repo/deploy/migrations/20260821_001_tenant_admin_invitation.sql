-- ANI Platform · Migration 20260821_001
-- Description: 租户管理员邀请表 + users 软删除/展示名列
--              支撑租户管理员邀请生命周期（inviting→accepted/rejected/expired）、
--              列表展示名、软删除过滤。
-- Depends on: 20260501_001_init_schema.sql (tenants, users, roles),
--             20260502_003_permissions_schema.sql (roles permissions schema + 既有平台角色种子)
-- Rationale:
--   BOSS 租户管理员模块（SPEC §3.1 / plan v3.0 §4.1.4b）：
--     - tenant_admin_invitation  邀请独立表，不改 users.status、不预绑角色。
--                               token 仅存 SHA-256 哈希；expire_at 由应用层写 now()+72h。
--     - users.display_name / is_deleted / deleted_at
--                               展示名与软删除标记；与 status(active/disabled) 独立。
--                               IF NOT EXISTS 幂等，便于与平台运营账号迁移共存。
--   RLS：platform_bypass + tenant_self PERMISSIVE（对齐 audit_logs / tenant_quota_change）。
--   tenant-service 以 ani_app_user 连接 DB，故需表级 GRANT。

BEGIN;

-- ===========================================================================
-- 1. tenant_admin_invitation — 租户管理员邀请表
-- ===========================================================================
CREATE TABLE tenant_admin_invitation (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL,                          -- SHA-256(原始 token)，不存明文
    status       TEXT NOT NULL
        CHECK (status IN ('inviting', 'accepted', 'rejected', 'expired')),
    expire_at    TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    accepted_at  TIMESTAMPTZ,
    rejected_at  TIMESTAMPTZ
);

CREATE UNIQUE INDEX uk_tenant_admin_invitation_token_hash
    ON tenant_admin_invitation(token_hash);
CREATE INDEX idx_tenant_admin_invitation_user_status
    ON tenant_admin_invitation(user_id, status);

-- ===========================================================================
-- 2. RLS — 平台操作绕过 / 租户只能看自己
-- ===========================================================================
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

-- ===========================================================================
-- 3. Core：users 表列扩展（展示名 + 软删除）
-- ===========================================================================
-- display_name：租户管理员展示名（昵称），NULL 表示使用 username
ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name TEXT;

-- is_deleted / deleted_at：软删除标记，与 users.status(active/disabled) 独立
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

COMMIT;

-- ===========================================================================
-- Rollback
-- ===========================================================================
-- ALTER TABLE users DROP COLUMN IF EXISTS deleted_at;
-- ALTER TABLE users DROP COLUMN IF EXISTS is_deleted;
-- ALTER TABLE users DROP COLUMN IF EXISTS display_name;
-- REVOKE SELECT, INSERT, UPDATE, DELETE ON tenant_admin_invitation FROM ani_app_user;
-- DROP POLICY IF EXISTS tenant_admin_invitation_tenant_self ON tenant_admin_invitation;
-- DROP POLICY IF EXISTS tenant_admin_invitation_platform_bypass ON tenant_admin_invitation;
-- ALTER TABLE tenant_admin_invitation NO FORCE ROW LEVEL SECURITY;
-- ALTER TABLE tenant_admin_invitation DISABLE ROW LEVEL SECURITY;
-- DROP TABLE IF EXISTS tenant_admin_invitation;
