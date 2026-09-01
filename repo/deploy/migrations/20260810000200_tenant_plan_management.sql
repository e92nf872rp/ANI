-- ANI Platform · Migration 20260810000200
-- Description: 配额套餐管理表 — tenant_plans（套餐主表）+ plan_quota_limits（套餐维度限额）
--              + tenants.plan_id（租户当前关联套餐）
-- Depends on: 20260501000100_init_schema.sql (tenants), 20260810000100_resource_quota.sql
--             （starter 套餐 seed 的 resource_type 取值对齐 resource_quota_meta 初始维度；
--              本迁移不对 resource_quota_meta 建外键）
-- Rationale:
--   BOSS 配额套餐管理：
--     - tenant_plans       套餐主表（code/name/description/status: draft|active|disabled）。
--                          is_deleted/deleted_at 支撑软删除；code 唯一约束仅对未删除套餐生效
--                          （partial unique index WHERE is_deleted = FALSE），软删除后 code 可复用。
--     - plan_quota_limits  套餐各维度配额上限（total NULL = 用 Core resource_quota_meta.default_quota 兜底）。
--                          外键仅 → tenant_plans (ON DELETE CASCADE)。
--                          resource_type 不建外键（Core resource_quota_meta 与 BOSS 表跨层；
--                          合法性由 tenant-service 经 Core ListQuotaMeta / SDK 在应用层校验）。
--     - tenants.plan_id    租户当前关联套餐，ON DELETE RESTRICT（有租户关联的套餐不可物理删除，
--                          删除走 tenant_plans 软删除）。
--   tenant_plans / plan_quota_limits 为平台治理数据（套餐管理）；应用层 RBAC 限制为
--   platform-admin，同时 GRANT 表级读写给 ani_app_user（tenant-service 默认 DB 用户）。
--   依赖 20260810000100_resource_quota.sql 先执行（本文件序号 002 > 001，顺序天然满足），
--   以便 starter seed 与 Core 维度命名一致；非因 FK 引用。


-- ===========================================================================
-- 1. tenant_plans — 套餐主表
-- ===========================================================================
CREATE TABLE tenant_plans (
  id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  code            TEXT        NOT NULL,
  name            TEXT        NOT NULL,
  description     TEXT,
  status          TEXT        NOT NULL DEFAULT 'draft'
                    CHECK (status IN ('draft', 'active', 'disabled')),
  is_deleted      BOOLEAN     NOT NULL DEFAULT FALSE,
  deleted_at      TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- code 唯一约束仅对未删除的套餐生效
CREATE UNIQUE INDEX idx_tenant_plans_code_active
  ON tenant_plans(code) WHERE is_deleted = FALSE;

-- ===========================================================================
-- 2. plan_quota_limits — 套餐维度限额
-- ===========================================================================
CREATE TABLE plan_quota_limits (
    plan_id        UUID        NOT NULL REFERENCES tenant_plans(id) ON DELETE CASCADE,
    resource_type  TEXT        NOT NULL,  -- 不建 FK → resource_quota_meta；应用层经 Core 校验
    total          BIGINT,             -- NULL = 用 resource_quota_meta.default_quota（经 Core API 兜底）
    CHECK (total IS NULL OR total >= 0),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plan_id, resource_type)
);

-- ===========================================================================
-- 3. tenants.plan_id — 租户当前关联套餐（ON DELETE RESTRICT，最终 NOT NULL）
--    分步实现以满足 NOT NULL：
--      3a) 先加「可空」plan_id（否则存量行空值会导致 ADD COLUMN NOT NULL 失败）
--      3b) 插入固定 UUID 的 starter 入门套餐，作为存量租户的默认套餐
--      3c) 把所有 plan_id IS NULL 的存量租户指向 starter
--      3d) 收紧为 NOT NULL（此时已无 NULL 行，约束可成功添加）
-- ===========================================================================

-- 3a) 加可空列（NOT NULL 需在回填完成后收紧）
ALTER TABLE tenants
  ADD COLUMN IF NOT EXISTS plan_id UUID
    REFERENCES tenant_plans(id) ON DELETE RESTRICT;

-- 3b) 固定 UUID 的入门套餐 starter（幂等：冲突则跳过）
INSERT INTO tenant_plans (id, code, name, description, status, is_deleted, deleted_at)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    'starter',
    '入门版',
    '面向小型团队的入门级配额套餐（系统内置默认套餐）',
    'active',
    FALSE,
    NULL
)
ON CONFLICT (id) DO NOTHING;

-- 3b-2) starter 套餐各配额维度限额（幂等：每个维度冲突则跳过）
INSERT INTO plan_quota_limits (plan_id, resource_type, total)
SELECT '00000000-0000-0000-0000-000000000001', l.resource_type, l.total
FROM (VALUES
    ('gpu_count',               2),
    ('cpu_core',                4),
    ('memory_gb',               16),
    ('storage_gb',              32),
    ('token_count',             500000),
    ('kb_query_count',          1000),
    ('member_count',            5),
    ('inference_service_count', 3)
) AS l(resource_type, total)
ON CONFLICT (plan_id, resource_type) DO NOTHING;

-- 3c) 存量 / 未关联套餐的租户统一回填到 starter
UPDATE tenants
SET plan_id = '00000000-0000-0000-0000-000000000001'
WHERE plan_id IS NULL;

-- 3d) 收紧为 NOT NULL
ALTER TABLE tenants ALTER COLUMN plan_id SET NOT NULL;

-- 注：tenant_plans / plan_quota_limits 为平台治理数据（套餐管理），应用层 RBAC
-- 限制为 platform-admin；tenant-service 以 ani_app_user 连接 DB，故仍需表级 GRANT。
GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_plans TO ani_app_user;
GRANT SELECT, INSERT, UPDATE, DELETE ON plan_quota_limits TO ani_app_user;


-- ===========================================================================
-- Rollback
-- ===========================================================================
-- ALTER TABLE tenants DROP COLUMN IF EXISTS plan_id;
-- DROP TABLE plan_quota_limits;
-- DROP TABLE tenant_plans;
