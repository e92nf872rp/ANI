-- ANI Platform · Metering Usage Records Migration
-- Version: 20260731000100
-- Description: 新建 metering_usage_records 表和 ani_metering_writer 角色，作为计量采集的 DB 层基础设施
-- Depends on: 20260501000100_init_schema.sql (tenants 表)
-- 执行顺序: ROLE → TABLE → GRANT → RLS
-- Run with: psql $DATABASE_URL -f 20260731000100_metering_usage.sql

-- ===========================================================================
-- 0) 采集写侧专用角色（类比 init_schema.sql:26 ani_outbox_publisher BYPASSRLS）
-- ===========================================================================
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_roles WHERE rolname = 'ani_metering_writer'
    ) THEN
        CREATE ROLE ani_metering_writer BYPASSRLS NOLOGIN;
    ELSE
        ALTER ROLE ani_metering_writer BYPASSRLS NOLOGIN;
    END IF;
END
$$;

-- 让 ani_app_user 成为 ani_metering_writer 成员，
-- 使 metering-service 可通过 SET ROLE ani_metering_writer 切换为 BYPASSRLS 身份跨租户写入
GRANT ani_metering_writer TO ani_app_user;

-- ===========================================================================
-- 1) 建表
-- ===========================================================================
CREATE TABLE metering_usage_records (
    id            BIGSERIAL PRIMARY KEY,
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    resource_ref  TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    period        TEXT NOT NULL,
    quantity      DOUBLE PRECISION NOT NULL,
    unit          TEXT NOT NULL,
    recorded_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, resource_ref, resource_type, period)
);

-- ===========================================================================
-- 2) 索引（支持读侧按租户+类型+时间查询）
-- ===========================================================================
CREATE INDEX idx_meter_tenant_type_time
    ON metering_usage_records(tenant_id, resource_type, recorded_at);

-- ===========================================================================
-- 3) 授权给写侧角色（采集写侧 BYPASSRLS 绕过 RLS）
-- ===========================================================================
GRANT SELECT, INSERT, UPDATE, DELETE ON metering_usage_records TO ani_metering_writer;

-- ===========================================================================
-- 4) 读侧 RLS（展示 QueryUsage 走 ani_app_user，RLS 生效）
--    采集写侧走 ani_metering_writer（BYPASSRLS），不 FORCE RLS
-- ===========================================================================
ALTER TABLE metering_usage_records ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON metering_usage_records
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
