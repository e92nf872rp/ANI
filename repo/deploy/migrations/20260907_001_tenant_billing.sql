-- ANI Platform · Migration 20260907_001
-- Description: BOSS 租户计费结算表 — billing_invoices / billing_adjustments /
--              billing_credit_accounts / billing_pricing（含定价种子 6 行）
-- Depends on: 20260828000200_app_role_privileges.sql（ani_app 角色权限基线）
-- Rationale:
--   计费结算域（方案《租户计费结算-后端接口方案.md》v0.3 §6）：
--     - billing_invoices           账单（生成即出账 status=issued；settled/credited 终态；
--                                  overdue 仅为读取时展示态，不落库）。
--                                  (tenant_id, period) 唯一 = 一期一单；no / idempotency_key 唯一。
--     - billing_adjustments        调账（金额可负，不可为 0 由应用层校验）；idempotency_key 唯一
--                                  支撑并发重放幂等。
--     - billing_credit_accounts    授信账户（tenant_id 主键）；余额读取时推导（§6.3），不落库冗余字段。
--     - billing_pricing            定价表（代码零单价字面量，折算只读此表）；
--                                  种子值 INSERT ... ON CONFLICT DO NOTHING —— 迁移重跑不回滚人工改价。
--   计量原始数据（metering_usage_records）不在本库，用量经 Core OpenAPI
--   GET /metering/usage/platform 获取（Services 禁止直查 Core 库，CLAUDE.md §3）。
--   仅 token_total 计费；token_input/token_output 不设定价行（避免重复计费）。

-- ===========================================================================
-- 1. billing_invoices — 账单
-- ===========================================================================
CREATE TABLE billing_invoices (
  id              UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       UUID           NOT NULL,
  period          TEXT           NOT NULL,             -- 'YYYY-MM'
  no              TEXT           NOT NULL,             -- 'INV-{YYMM}-{seq}'
  amount_usd      NUMERIC(14,2)  NOT NULL,
  status          TEXT           NOT NULL
                    CHECK (status IN ('issued', 'settled', 'credited')),
  due_date        DATE           NOT NULL,             -- issued_at + 30 天
  issued_at       TIMESTAMPTZ    NOT NULL,
  settled_at      TIMESTAMPTZ,
  credited_at     TIMESTAMPTZ,
  idempotency_key UUID           NOT NULL,
  created_at      TIMESTAMPTZ    NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, period),                          -- 一期一单
  UNIQUE (no),
  UNIQUE (idempotency_key)                             -- 同 key 重放幂等
);

CREATE INDEX idx_billing_invoices_tenant ON billing_invoices(tenant_id, issued_at DESC);

-- ===========================================================================
-- 2. billing_adjustments — 调账（金额可负）
-- ===========================================================================
CREATE TABLE billing_adjustments (
  id              UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       UUID           NOT NULL,
  period          TEXT           NOT NULL,             -- 'YYYY-MM'
  amount_usd      NUMERIC(14,2)  NOT NULL,
  reason          TEXT           NOT NULL,
  operator        TEXT           NOT NULL,             -- 操作人（网关透传 user_id）
  idempotency_key UUID           NOT NULL,
  created_at      TIMESTAMPTZ    NOT NULL DEFAULT now(),
  UNIQUE (idempotency_key)                             -- 并发重放幂等
);

CREATE INDEX idx_billing_adjustments_tenant ON billing_adjustments(tenant_id, created_at);

-- ===========================================================================
-- 3. billing_credit_accounts — 授信账户（余额读取时推导，不落库）
-- ===========================================================================
CREATE TABLE billing_credit_accounts (
  tenant_id       UUID           PRIMARY KEY,
  credit_usd      NUMERIC(14,2)  NOT NULL,
  updated_at      TIMESTAMPTZ    NOT NULL DEFAULT now()
);

-- ===========================================================================
-- 4. billing_pricing — 定价表（无接口；改价 = UPDATE 数据，无需发版）
-- ===========================================================================
CREATE TABLE billing_pricing (
  resource_type   TEXT           PRIMARY KEY,          -- Core metering resource_type（预留行除外）
  display_metric  TEXT           NOT NULL,             -- gpu_hours | cpu_hours | memory_gb_hours | tokens | storage_gi | kb_queries
  unit_cost       NUMERIC(14,8)  NOT NULL,             -- 每展示单位价格（USD）
  updated_at      TIMESTAMPTZ    NOT NULL DEFAULT now()
);

-- 种子值（方案 §6.5 暂定价，来源原型 store.js unitCost；ON CONFLICT DO NOTHING
-- 保人工 UPDATE 改价不被迁移重跑回滚）。
INSERT INTO billing_pricing (resource_type, display_metric, unit_cost) VALUES
  ('instance_gpu_seconds',        'gpu_hours',      1.20000000),
  ('instance_cpu_seconds',        'cpu_hours',      0.05000000),
  ('instance_memory_gib_seconds', 'memory_gb_hours', 0.02000000),
  ('token_total',                 'tokens',         0.00000200),
  ('storage',                     'storage_gi',     0.02000000),  -- 预留：Core 枚举未落地，暂不生效
  ('kb',                          'kb_queries',     0.00100000)   -- 预留：采集链路未落地，暂不生效
ON CONFLICT (resource_type) DO NOTHING;

-- ===========================================================================
-- 5. 权限：tenant-service 默认 DB 用户（ani_app_user 继承 ani_app）
-- ===========================================================================
GRANT SELECT, INSERT, UPDATE ON billing_invoices TO ani_app;
GRANT SELECT, INSERT ON billing_adjustments TO ani_app;
GRANT SELECT, UPDATE ON billing_credit_accounts TO ani_app;
GRANT SELECT, UPDATE ON billing_pricing TO ani_app;
