-- ANI Platform · Migration 20260902_001
-- Description: 租户列表管理 — tenants 状态机收窄（active/frozen/disabled）与列扩展、
--              tenant_auth（1:1 SSO/MFA）、tenant_lifecycle（状态转换审计）
-- Depends on: 20260501000100_init_schema.sql (tenants, users),
--             20260810000200_tenant_plan_management.sql (tenants.plan_id 既有扩展)
-- Rationale:
--   BOSS 租户列表（SPEC §3.1 / plan v3.0）：
--     - tenants.status CHECK 收窄为三态 active|frozen|disabled；
--       存量映射 suspended→frozen、deleted→disabled，保证迁移后无非法值。
--     - contact_email / frozen_at / disabled_at 支撑详情与状态机时间戳。
--     - tenant_auth 与 tenants 1:1，承载 SSO/MFA；存量租户回填默认行。
--     - tenant_lifecycle 追加写状态转换（create/freeze/unfreeze/disable）；
--       存量租户回填 action='create'（created_at 取 tenants.created_at），保证 US-015 非空。
--   RLS：platform_bypass + self_read PERMISSIVE（对齐 tenant_quota_change 双策略；
--        bypass 使用 NULLIF(...,'') IS NULL，避免连接池残留空串误判租户上下文，
--        对齐 20260831_001_async_tasks_rls_fix 已验证写法）。
--   GRANT 授给 ani_app 组角色（ani_app_user 继承），不直接授 ani_app_user。
--
-- 不新建：tenant_quota_change / audit_logs（已存在）。


-- ===========================================================================
-- 1. tenants — 状态机 CHECK 收窄 + 列扩展
-- ===========================================================================

-- 先去掉旧 CHECK，再映射存量，再加新 CHECK（否则 UPDATE 会被旧约束拒绝）
ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_status_check;

UPDATE tenants SET status = 'frozen'   WHERE status = 'suspended';
UPDATE tenants SET status = 'disabled' WHERE status = 'deleted';

ALTER TABLE tenants ADD CONSTRAINT tenants_status_check
    CHECK (status IN ('active', 'frozen', 'disabled'));

ALTER TABLE tenants ADD COLUMN IF NOT EXISTS contact_email TEXT;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS frozen_at   TIMESTAMPTZ;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS disabled_at TIMESTAMPTZ;


-- ===========================================================================
-- 2. tenant_auth — Core 表（与 tenants 1:1）
-- ===========================================================================

CREATE TABLE tenant_auth (
    tenant_id    UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    sso_enabled  BOOLEAN     NOT NULL DEFAULT FALSE,
    sso_provider TEXT,                        -- NULL = 未配置
    mfa_required BOOLEAN     NOT NULL DEFAULT FALSE,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);


-- ===========================================================================
-- 3. tenant_lifecycle — Core 表（状态转换审计）
-- ===========================================================================

CREATE TABLE tenant_lifecycle (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    action     TEXT NOT NULL CHECK (action IN ('create', 'freeze', 'unfreeze', 'disable')),
    reason     TEXT,                          -- 操作原因（预留，当前可 NULL）
    user_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    request_id TEXT,                          -- 全链路追踪
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tenant_lifecycle_tenant
    ON tenant_lifecycle (tenant_id, created_at DESC);


-- ===========================================================================
-- 4. RLS — 平台操作绕过 / 租户自读
-- ===========================================================================

ALTER TABLE tenant_auth ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_auth FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_auth_platform_bypass
    ON tenant_auth
    USING (NULLIF(current_setting('app.current_tenant_id', true), '') IS NULL);

CREATE POLICY tenant_auth_self_read
    ON tenant_auth
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE tenant_lifecycle ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_lifecycle FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_lifecycle_platform_bypass
    ON tenant_lifecycle
    USING (NULLIF(current_setting('app.current_tenant_id', true), '') IS NULL);

CREATE POLICY tenant_lifecycle_self_read
    ON tenant_lifecycle
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);


-- ===========================================================================
-- 5. GRANT — ani_app 组角色
-- ===========================================================================

GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_auth TO ani_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_lifecycle TO ani_app;


-- ===========================================================================
-- 6. 存量回填 — tenant_auth 1:1 + tenant_lifecycle create
-- ===========================================================================

INSERT INTO tenant_auth (tenant_id)
SELECT id FROM tenants
ON CONFLICT DO NOTHING;

-- 保证 US-015 生命周期列表对存量租户非空；幂等：已有 create 行则跳过
INSERT INTO tenant_lifecycle (tenant_id, action, created_at)
SELECT t.id, 'create', t.created_at
FROM tenants t
WHERE NOT EXISTS (
    SELECT 1
    FROM tenant_lifecycle tl
    WHERE tl.tenant_id = t.id
      AND tl.action = 'create'
);


-- ===========================================================================
-- Rollback
-- ===========================================================================
-- DELETE FROM tenant_lifecycle WHERE action = 'create';
-- DELETE FROM tenant_auth;
-- REVOKE SELECT, INSERT, UPDATE, DELETE ON tenant_lifecycle FROM ani_app;
-- REVOKE SELECT, INSERT, UPDATE, DELETE ON tenant_auth FROM ani_app;
-- DROP POLICY IF EXISTS tenant_lifecycle_self_read ON tenant_lifecycle;
-- DROP POLICY IF EXISTS tenant_lifecycle_platform_bypass ON tenant_lifecycle;
-- ALTER TABLE tenant_lifecycle NO FORCE ROW LEVEL SECURITY;
-- ALTER TABLE tenant_lifecycle DISABLE ROW LEVEL SECURITY;
-- DROP POLICY IF EXISTS tenant_auth_self_read ON tenant_auth;
-- DROP POLICY IF EXISTS tenant_auth_platform_bypass ON tenant_auth;
-- ALTER TABLE tenant_auth NO FORCE ROW LEVEL SECURITY;
-- ALTER TABLE tenant_auth DISABLE ROW LEVEL SECURITY;
-- DROP INDEX IF EXISTS idx_tenant_lifecycle_tenant;
-- DROP TABLE IF EXISTS tenant_lifecycle;
-- DROP TABLE IF EXISTS tenant_auth;
-- ALTER TABLE tenants DROP COLUMN IF EXISTS disabled_at;
-- ALTER TABLE tenants DROP COLUMN IF EXISTS frozen_at;
-- ALTER TABLE tenants DROP COLUMN IF EXISTS contact_email;
-- ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_status_check;
-- -- 注意：回滚无法自动还原 suspended/deleted 存量映射；需业务侧确认后再恢复旧 CHECK：
-- -- UPDATE tenants SET status = 'suspended' WHERE status = 'frozen';
-- -- UPDATE tenants SET status = 'deleted'   WHERE status = 'disabled';
-- ALTER TABLE tenants ADD CONSTRAINT tenants_status_check
--     CHECK (status IN ('active', 'suspended', 'deleted'));
