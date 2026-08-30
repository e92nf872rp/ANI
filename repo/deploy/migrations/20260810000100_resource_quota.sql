-- ANI Platform · Migration 20260810000100
-- Description: 配额基础表 — resource_quota_meta（配额维度元数据注册表）
--              + resource_quota（租户配额配置 + 运行时账本）+ resource_reservations（TCC 配额流水）
-- Depends on: 20260501000100_init_schema.sql (tenants)
-- Rationale:
--   Core 配额治理所需的持久化基础：
--     - resource_quota_meta  维度注册表（display_name/unit/default_quota/enabled）。平台治理数据，
--                            跨租户可见，写权限由应用层 RBAC 限制为 platform-admin，不加 RLS。
--     - resource_quota       各租户各维度的 total（上限）与 reserved/used（占用）同行的账本。
--                            配置与占用不同步会引发超卖，故放同一行。
--                            该表加 RLS：租户只能看自己的行，平台管理员（app.current_tenant_id 为空）绕过看所有。
--     - resource_reservations TCC 配额流水（reserved → confirmed/cancelled/expired/released）。
--                            度量配额占用变更的基础，TS 提供 TTL 孤儿回收扫描依据。同样加 RLS 隔离租户。
--   RLS 采用 current_setting('app.current_tenant_id', true) 判租户：
--     - 平台上下文（值为 NULL/空）→ platform_bypass 策略放行全部行
--     - 租户上下文 → self 策略仅放行该租户自己的行
--   应用用户授权按「租户管理 plan v3.0 §4.3.4 应用用户权限分配」：
--   配额相关表直接 GRANT 给 ani_app_user。


-- ===========================================================================
-- 1. resource_quota_meta — 配额维度元数据注册表（不加 RLS：平台治理数据）
-- ===========================================================================
CREATE TABLE resource_quota_meta (
    resource_type     TEXT        PRIMARY KEY,
    display_name      TEXT        NOT NULL,
    unit              TEXT        NOT NULL,
    is_discrete       BOOLEAN     NOT NULL DEFAULT TRUE,
    default_quota     BIGINT      NOT NULL,
    collector_id      TEXT,
    description       TEXT,
    enabled           BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 初始 seed（8 维度）；已有数据时跳过
INSERT INTO resource_quota_meta (resource_type, display_name, unit, is_discrete, default_quota, collector_id, description) VALUES
  ('gpu_count',               'GPU 份数',     '份',     true,  8,        'prometheus_dcgm',    '单租户可持有的 GPU 份数上限'),
  ('cpu_core',                'CPU 核数',     '核',     true,  8,       'prometheus_kubelet', '单租户可占用的 CPU 核数上限'),
  ('memory_gb',               '内存 GB',      'gb',     true,  32,      'prometheus_kubelet', '单租户可占用的内存 GB 上限'),
  ('storage_gb',              '存储 GB',      'gb',     true,  64,     NULL,                  '单租户可占用的存储 GB 上限'),
  ('token_count',             'Token 数',     'token',  true,  1000000, 'inference_token',    '单租户可消耗的 Token 总量上限'),
  ('kb_query_count',          'KB 查询次数',  '次',     true,  10000,   NULL,                 '单租户知识库查询次数上限'),
  ('member_count',            '成员上限',     '人',     true,  20,      NULL,                 '单租户可邀请的成员数量上限'),
  ('inference_service_count', '推理服务上限', '个',     true,  10,      NULL,                 '单租户可创建的推理服务数量上限')
ON CONFLICT (resource_type) DO NOTHING;

-- ===========================================================================
-- 2. resource_quota — 租户配额配置 + 运行时账本
--    total（上限）与 reserved/used（占用）在同一行，避免配置与占用不同步。
-- ===========================================================================
CREATE TABLE resource_quota (
    tenant_id      UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    resource_type  TEXT        NOT NULL REFERENCES resource_quota_meta(resource_type),
    total          BIGINT      NOT NULL DEFAULT 0,
    reserved       BIGINT      NOT NULL DEFAULT 0,
    used           BIGINT      NOT NULL DEFAULT 0,
    CHECK (total >= 0 AND reserved >= 0 AND used >= 0),
    CHECK (reserved + used <= total),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, resource_type)
);

ALTER TABLE resource_quota ENABLE ROW LEVEL SECURITY;
ALTER TABLE resource_quota FORCE ROW LEVEL SECURITY;
-- 平台上下文（app.current_tenant_id 未设置/为空）→ 全部可见
CREATE POLICY resource_quota_platform_bypass
  ON resource_quota FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);
-- 租户上下文 → 仅自己的行
CREATE POLICY resource_quota_self
  ON resource_quota FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ===========================================================================
-- 3. resource_reservations — TCC 配额流水
--    state: reserved → confirmed / cancelled / expired / released
-- ===========================================================================
CREATE TABLE resource_reservations (
    tx_id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    resource_type TEXT        NOT NULL REFERENCES resource_quota_meta(resource_type),
    amount        BIGINT      NOT NULL CHECK (amount > 0),
    state         TEXT        NOT NULL DEFAULT 'reserved'
        CHECK (state IN ('reserved','confirmed','cancelled','expired','released')),
    resource_ref  TEXT,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_res_state_expires
    ON resource_reservations(state, expires_at) WHERE state = 'reserved';
CREATE INDEX idx_res_tenant
    ON resource_reservations(tenant_id, state);

ALTER TABLE resource_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE resource_reservations FORCE ROW LEVEL SECURITY;
-- 平台上下文 → 全部可见
CREATE POLICY resource_reservations_platform_bypass
  ON resource_reservations FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);
-- 租户上下文 → 仅自己的流水
CREATE POLICY resource_reservations_self
  ON resource_reservations FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ===========================================================================
-- 4. Grants — 应用用户权限分配（租户管理 plan v3.0 §4.3.4）
--    resource_quota / resource_reservations / resource_quota_meta → ani_app_user
-- ===========================================================================
GRANT SELECT, INSERT, UPDATE, DELETE ON resource_quota TO ani_app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON resource_reservations TO ani_app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON resource_quota_meta TO ani_app_user;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO ani_app_user;


-- ===========================================================================
-- Rollback
-- ===========================================================================
-- REVOKE SELECT, INSERT, UPDATE, DELETE ON resource_quota, resource_reservations, resource_quota_meta FROM ani_app_user;
-- DROP TABLE resource_reservations;
-- DROP TABLE resource_quota;
-- DROP TABLE resource_quota_meta;
