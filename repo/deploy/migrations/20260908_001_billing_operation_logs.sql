-- ANI Platform · Migration 20260908_001
-- Description: BOSS 租户计费操作历史 — billing_operation_logs（抽屉「操作历史」Tab 数据源）
-- Depends on: 20260907_001_tenant_billing.sql（计费结算域基线）、20260828000200_app_role_privileges.sql（ani_app 角色权限基线）
-- Rationale:
--   操作流水（方案《租户计费操作历史-实施方案.md》）：
--     - 与业务写（生成账单/结清/冲抵/调账）同一事务落库：业务写失败回滚则流水不落库；
--     - 幂等重放与 409 冲突路径不产生流水（重放不开新事务 / CAS 失败事务回滚）；
--     - action 枚举：invoice.generated / invoice.settled / invoice.credited / adjustment.created；
--     - ref_id 关联账单/调账记录主键；period 为关联账期（可空，预留无账期动作）；
--     - 只增不改（无 UPDATE/DELETE 授权）：流水是审计事实，不是可变业务状态。

-- ===========================================================================
-- 1. billing_operation_logs — 计费操作流水
-- ===========================================================================
CREATE TABLE billing_operation_logs (
  id          UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id   UUID           NOT NULL,
  period      TEXT,                                 -- 'YYYY-MM'；关联账期（可空预留）
  action      TEXT           NOT NULL
                CHECK (action IN ('invoice.generated', 'invoice.settled', 'invoice.credited', 'adjustment.created')),
  ref_id      UUID,                                 -- 关联 invoice_id / adjustment_id
  message     TEXT           NOT NULL,              -- 人类可读摘要（含金额）
  operator    TEXT           NOT NULL,              -- 操作人（网关透传 user_id）
  created_at  TIMESTAMPTZ    NOT NULL DEFAULT now()
);

CREATE INDEX idx_billing_operation_logs_tenant ON billing_operation_logs(tenant_id, created_at DESC);

-- ===========================================================================
-- 2. 权限：tenant-service 默认 DB 用户（ani_app_user 继承 ani_app）
-- ===========================================================================
GRANT SELECT, INSERT ON billing_operation_logs TO ani_app;
