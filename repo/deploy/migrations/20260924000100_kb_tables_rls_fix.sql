-- ANI Platform · Migration 20260924000100
-- Description: 知识库 B 层 4 表（knowledge_bases / kb_documents / kb_sessions /
--              kb_messages）RLS 修复：RESTRICTIVE-only 改为双 PERMISSIVE 模式，
--              并显式 ENABLE/FORCE ROW LEVEL SECURITY。
-- Depends on: 20260501000100_init_schema.sql（4 表建表与 RESTRICTIVE 策略）
-- Background:
--   init_schema 给上述 4 表建的是单条 RESTRICTIVE tenant_isolation 策略。
--   PostgreSQL 语义：无任何 PERMISSIVE 策略伴随时全表拒绝——新环境以非超级
--   用户（ani_app_user）连接时，即使租户上下文正确也查不到任何行（列表恒空）。
--   30080 环境当前的双 PERMISSIVE 策略由 scripts/_verify_aniappuser.sql 手工
--   建立，且漏执行 ENABLE ROW LEVEL SECURITY，导致 RLS 长期未生效、租户隔离
--   缺失（KB 列表跨租户混出）。本迁移把服务器手工修复固化进受控迁移历史，
--   与 20260825000100_workload_instances_rls_fix.sql 同一模式。
-- 策略命名与服务器手工修复保持一致（kb_platform_bypass/kb_self 等），重放安全。

-- ===========================================================================
-- 1. knowledge_bases
-- ===========================================================================
DROP POLICY IF EXISTS tenant_isolation ON knowledge_bases;
DROP POLICY IF EXISTS kb_platform_bypass ON knowledge_bases;
DROP POLICY IF EXISTS kb_self ON knowledge_bases;

ALTER TABLE knowledge_bases ENABLE ROW LEVEL SECURITY;
ALTER TABLE knowledge_bases FORCE ROW LEVEL SECURITY;

CREATE POLICY kb_platform_bypass ON knowledge_bases
    AS PERMISSIVE
    FOR ALL
    TO public
    USING (current_setting('app.current_tenant_id', true) IS NULL)
    WITH CHECK (current_setting('app.current_tenant_id', true) IS NULL);
CREATE POLICY kb_self ON knowledge_bases
    AS PERMISSIVE
    FOR ALL
    TO public
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ===========================================================================
-- 2. kb_documents
-- ===========================================================================
DROP POLICY IF EXISTS tenant_isolation ON kb_documents;
DROP POLICY IF EXISTS kbd_platform_bypass ON kb_documents;
DROP POLICY IF EXISTS kbd_self ON kb_documents;

ALTER TABLE kb_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_documents FORCE ROW LEVEL SECURITY;

CREATE POLICY kbd_platform_bypass ON kb_documents
    AS PERMISSIVE
    FOR ALL
    TO public
    USING (current_setting('app.current_tenant_id', true) IS NULL)
    WITH CHECK (current_setting('app.current_tenant_id', true) IS NULL);
CREATE POLICY kbd_self ON kb_documents
    AS PERMISSIVE
    FOR ALL
    TO public
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ===========================================================================
-- 3. kb_sessions
-- ===========================================================================
DROP POLICY IF EXISTS tenant_isolation ON kb_sessions;
DROP POLICY IF EXISTS kbs_platform_bypass ON kb_sessions;
DROP POLICY IF EXISTS kbs_self ON kb_sessions;

ALTER TABLE kb_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_sessions FORCE ROW LEVEL SECURITY;

CREATE POLICY kbs_platform_bypass ON kb_sessions
    AS PERMISSIVE
    FOR ALL
    TO public
    USING (current_setting('app.current_tenant_id', true) IS NULL)
    WITH CHECK (current_setting('app.current_tenant_id', true) IS NULL);
CREATE POLICY kbs_self ON kb_sessions
    AS PERMISSIVE
    FOR ALL
    TO public
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ===========================================================================
-- 4. kb_messages
-- ===========================================================================
DROP POLICY IF EXISTS tenant_isolation ON kb_messages;
DROP POLICY IF EXISTS kbm_platform_bypass ON kb_messages;
DROP POLICY IF EXISTS kbm_self ON kb_messages;

ALTER TABLE kb_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_messages FORCE ROW LEVEL SECURITY;

CREATE POLICY kbm_platform_bypass ON kb_messages
    AS PERMISSIVE
    FOR ALL
    TO public
    USING (current_setting('app.current_tenant_id', true) IS NULL)
    WITH CHECK (current_setting('app.current_tenant_id', true) IS NULL);
CREATE POLICY kbm_self ON kb_messages
    AS PERMISSIVE
    FOR ALL
    TO public
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- ===========================================================================
-- 验证（手动执行，不在迁移内）
-- ===========================================================================
-- SELECT c.relname, c.relrowsecurity AS rls_on, c.relforcerowsecurity AS force_on
--   FROM pg_class c WHERE c.relname IN
--   ('knowledge_bases','kb_documents','kb_sessions','kb_messages');
-- 以 ani_app_user 连接：
--   SET LOCAL app.current_tenant_id = '<tenant_id>';
--   SELECT count(*) FROM knowledge_bases;  -- 仅本租户
--   RESET app.current_tenant_id;
--   SELECT count(*) FROM knowledge_bases;  -- 全部（platform bypass）
--
-- ===========================================================================
-- Rollback
-- ===========================================================================
-- 各表 DROP 两个 PERMISSIVE 策略，重建单条 RESTRICTIVE tenant_isolation
-- （回滚即恢复"全表拒绝"缺陷态，仅用于结构对齐，不建议执行）。
