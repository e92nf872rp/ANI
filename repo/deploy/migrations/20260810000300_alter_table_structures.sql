-- ANI Platform · Migration 20260810000300
-- Description:
--   1) 移除 tenants 表废弃的配额上限列（max_gpu_count / max_cpu_cores / max_memory_gb）
--   2) 修复 audit_logs 分区主键并补建 2026-08 分区
--   3) 补齐 audit_logs 平台审计 RLS（PERMISSIVE bypass + tenant self）
--   4) 补齐 tenant_plans / plan_quota_limits 对 ani_app_user 的表级 GRANT
-- Depends on: 20260501000100_init_schema.sql, 20260810000100_resource_quota.sql, 20260810000200_tenant_plan_management.sql
-- Rationale:
--   配额治理已迁移到 resource_quota 表（见 20260810000100_resource_quota.sql）：
--     - 旧列: tenants.max_gpu_count INT NOT NULL DEFAULT 0 / max_cpu_cores / max_memory_gb
--     - 新表: resource_quota(tenant_id, resource_type, total, reserved, used) 承载按维度账本
--   三个旧列已无代码引用（配额读写改走 resource_quota），此处清理避免双重来源。
--   audit_logs 原先仅有 RESTRICTIVE tenant_isolation 且无 PERMISSIVE，导致默认拒绝；
--   平台套餐操作写入 tenant_id IS NULL 的审计行也会被拦。改为与 resource_quota 同构的
--   platform_bypass + tenant_self PERMISSIVE 策略（对齐租户管理 plan v3.0）。
--   tenant-service 默认 DATABASE_URL 使用 ani_app_user，需对套餐表 GRANT。


-- 1) 移除 tenants 表废弃的配额上限列
ALTER TABLE tenants DROP COLUMN IF EXISTS max_gpu_count;
ALTER TABLE tenants DROP COLUMN IF EXISTS max_cpu_cores;
ALTER TABLE tenants DROP COLUMN IF EXISTS max_memory_gb;

-- 2) 修复 audit_logs 分区表主键：分区表唯一约束须含分区键 created_at
ALTER TABLE audit_logs DROP CONSTRAINT IF EXISTS audit_logs_pkey;
ALTER TABLE audit_logs ADD PRIMARY KEY (id, created_at);

-- 3) 补建 audit_logs 月分区
CREATE TABLE IF NOT EXISTS audit_logs_2026_08
    PARTITION OF audit_logs
    FOR VALUES FROM ('2026-08-01') TO ('2026-09-01');

-- 4) audit_logs RLS：平台上下文可读写全部行；租户上下文仅本租户行
DROP POLICY IF EXISTS tenant_isolation ON audit_logs;
DROP POLICY IF EXISTS audit_platform_bypass ON audit_logs;
DROP POLICY IF EXISTS audit_tenant_self ON audit_logs;
CREATE POLICY audit_platform_bypass ON audit_logs
  AS PERMISSIVE FOR ALL
  USING (
    current_setting('app.current_tenant_id', true) IS NULL
    OR current_setting('app.current_tenant_id', true) = ''
  );
CREATE POLICY audit_tenant_self ON audit_logs
  AS PERMISSIVE FOR ALL
  USING (
    tenant_id IS NOT NULL
    AND tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid
  );

-- 5) 套餐表授权给 tenant-service 所用 ani_app_user（幂等：重复执行可接受）
GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_plans TO ani_app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON plan_quota_limits TO ani_app_user;


-- ===========================================================================
-- Rollback
-- ===========================================================================
-- REVOKE SELECT, INSERT, UPDATE, DELETE ON tenant_plans FROM ani_app_user;
-- REVOKE SELECT, INSERT, UPDATE, DELETE ON plan_quota_limits FROM ani_app_user;
-- DROP POLICY IF EXISTS audit_platform_bypass ON audit_logs;
-- DROP POLICY IF EXISTS audit_tenant_self ON audit_logs;
-- CREATE POLICY tenant_isolation ON audit_logs
--   AS RESTRICTIVE
--   USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
-- ALTER TABLE tenants
--   ADD COLUMN max_gpu_count INT NOT NULL DEFAULT 0,
--   ADD COLUMN max_cpu_cores INT NOT NULL DEFAULT 0,
--   ADD COLUMN max_memory_gb INT NOT NULL DEFAULT 0;
-- ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_pkey;
-- ALTER TABLE audit_logs ADD PRIMARY KEY (id);
-- DROP TABLE IF EXISTS audit_logs_2026_08;
