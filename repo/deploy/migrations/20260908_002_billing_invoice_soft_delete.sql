-- ANI Platform · Migration 20260908_002
-- Description: 账单软删除 — billing_invoices.deleted_at + 活跃行部分唯一索引 +
--              billing_operation_logs.action 枚举扩展 invoice.deleted
-- Depends on: 20260907_001_tenant_billing.sql / 20260908_001_billing_operation_logs.sql
-- Rationale:
--   DELETE /api/v1/svc/billing/invoices/{invoiceId}（软删除，方案边界）：
--     - 仅 issued 可删；settled / credited 终态拒删（应用层 CAS，409 BILLING_STATE_CONFLICT）。
--     - 软删除（deleted_at 标记）而非物理 DELETE：行保留可审计； ani_app 角色无 DELETE 权限，
--       走 UPDATE 恰好兼容既有权限基线（无需新 GRANT）。
--     - (tenant_id, period) 全量唯一约束改为「仅活跃行唯一」部分唯一索引：
--       删除后同账期可重新出账（一期一单语义只约束未删除行）；
--       no / idempotency_key 保持全量唯一（历史单号与幂等键不复用）。
--     - billing_operation_logs.action 扩展 'invoice.deleted'：删除与流水同一事务落库，
--       删除失败回滚则不落流水（与既有 4 个写点同语义）。

-- ===========================================================================
-- 1. billing_invoices — 软删除列 + 活跃行唯一约束
-- ===========================================================================
ALTER TABLE billing_invoices ADD COLUMN deleted_at TIMESTAMPTZ;

-- 一期一单唯一约束改为部分唯一索引：仅约束未删除行（删除后同账期可重新出账）
ALTER TABLE billing_invoices DROP CONSTRAINT billing_invoices_tenant_id_period_key;
CREATE UNIQUE INDEX billing_invoices_tenant_period_active_uidx
  ON billing_invoices (tenant_id, period)
  WHERE deleted_at IS NULL;

-- ===========================================================================
-- 2. billing_operation_logs — action 枚举扩展 invoice.deleted
-- ===========================================================================
ALTER TABLE billing_operation_logs DROP CONSTRAINT billing_operation_logs_action_check;
ALTER TABLE billing_operation_logs
  ADD CONSTRAINT billing_operation_logs_action_check
  CHECK (action IN ('invoice.generated', 'invoice.settled', 'invoice.credited', 'adjustment.created', 'invoice.deleted'));
