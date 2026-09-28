-- kb-service Migration 009: kb_chunks RLS 修复
-- Description: RESTRICTIVE-only 改为双 PERMISSIVE 模式（platform_bypass +
--              self），与 005_kb_permissions / 006_kb_audit_log 及 deploy 侧
--              20260825000100_workload_instances_rls_fix.sql 同一模式。
-- Depends on: 002_kb_chunks.sql（建表与 RESTRICTIVE tenant_isolation 策略）
-- Background:
--   002 号迁移给 kb_chunks 建的是单条 RESTRICTIVE 策略，无 PERMISSIVE 伴随 =
--   全表拒绝。30080 环境的现行策略由 scripts/_verify_aniappuser.sql 手工建立
--   （kbc_platform_bypass/kbc_self），本迁移将其固化进受控迁移历史。
--   策略命名与服务器手工修复保持一致，重放安全。

DROP POLICY IF EXISTS tenant_isolation ON kb_chunks;
DROP POLICY IF EXISTS kbc_platform_bypass ON kb_chunks;
DROP POLICY IF EXISTS kbc_self ON kb_chunks;

ALTER TABLE kb_chunks ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_chunks FORCE ROW LEVEL SECURITY;

CREATE POLICY kbc_platform_bypass ON kb_chunks
    AS PERMISSIVE
    FOR ALL
    TO public
    USING (current_setting('app.current_tenant_id', true) IS NULL)
    WITH CHECK (current_setting('app.current_tenant_id', true) IS NULL);
CREATE POLICY kbc_self ON kb_chunks
    AS PERMISSIVE
    FOR ALL
    TO public
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ===========================================================================
-- Rollback
-- ===========================================================================
-- DROP POLICY IF EXISTS kbc_self ON kb_chunks;
-- DROP POLICY IF EXISTS kbc_platform_bypass ON kb_chunks;
-- CREATE POLICY tenant_isolation ON kb_chunks
--     AS RESTRICTIVE
--     USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
