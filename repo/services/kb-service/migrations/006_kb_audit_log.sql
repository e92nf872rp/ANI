-- B8 (#21): KB management-plane audit trail. Insert-only (no UPDATE path).
-- action enum reserves B6/B7 values (kb.config.update / kb.rebuild); this
-- batch instruments the existing write paths, B6/B7 add theirs on merge
-- (kb-p1-plan §6.3).
CREATE TABLE IF NOT EXISTS kb_audit_log (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kb_id         UUID NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    actor_user_id UUID,                          -- NULL = internal system actor (parse/rebuild consumer)
    action        VARCHAR(64) NOT NULL,          -- kb.create|kb.update|kb.delete|kb.permissions.update|
                                                 -- kb.config.update(B7)|kb.rebuild(B6)|doc.create|doc.parse|
                                                 -- doc.delete|doc.reparse
    before_state  JSONB,                         -- NULL = creation-type operation
    after_state   JSONB,                         -- NULL = deletion-type operation
    error_code    VARCHAR(64),                   -- NULL = success
    error_msg     TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_kb_audit_log_kb_time
    ON kb_audit_log (tenant_id, kb_id, created_at DESC, id DESC);

ALTER TABLE kb_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_audit_log FORCE ROW LEVEL SECURITY;

-- Standard double-policy RLS pattern (matches 005_kb_permissions.sql):
-- a single RESTRICTIVE policy with no PERMISSIVE companion denies ALL
-- access. kal_self scopes rows by tenant; kal_platform_bypass allows
-- no-tenant-context platform paths.
DROP POLICY IF EXISTS kal_self ON kb_audit_log;
DROP POLICY IF EXISTS kal_platform_bypass ON kb_audit_log;
CREATE POLICY kal_self ON kb_audit_log
  AS PERMISSIVE
  FOR ALL
  TO public
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
CREATE POLICY kal_platform_bypass ON kb_audit_log
  AS PERMISSIVE
  FOR ALL
  TO public
  USING (current_setting('app.current_tenant_id', true) IS NULL)
  WITH CHECK (current_setting('app.current_tenant_id', true) IS NULL);

-- Insert-only trail: no UPDATE/DELETE grants.
GRANT SELECT, INSERT ON kb_audit_log TO ani_app;
