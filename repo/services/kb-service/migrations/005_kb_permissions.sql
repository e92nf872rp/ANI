-- B4 (#20/#19): KB-level ACL store. Defaults when no row exists:
-- public_read=false, allowed_user_ids=[] (plan §2.3).
CREATE TABLE IF NOT EXISTS kb_permissions (
  kb_id            UUID PRIMARY KEY REFERENCES knowledge_bases(id) ON DELETE CASCADE,
  tenant_id        UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  public_read      BOOLEAN NOT NULL DEFAULT FALSE,
  allowed_user_ids UUID[] NOT NULL DEFAULT '{}',
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE kb_permissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_permissions FORCE ROW LEVEL SECURITY;

-- Standard double-policy RLS pattern (matches knowledge_bases /
-- kb_documents / kb_chunks / async_tasks): a single RESTRICTIVE policy
-- with no PERMISSIVE companion denies ALL access (PostgreSQL requires at
-- least one PERMISSIVE policy to pass). kbp_self scopes rows by tenant;
-- kbp_platform_bypass allows no-tenant-context platform paths.
DROP POLICY IF EXISTS tenant_isolation ON kb_permissions;
DROP POLICY IF EXISTS kbp_self ON kb_permissions;
DROP POLICY IF EXISTS kbp_platform_bypass ON kb_permissions;
CREATE POLICY kbp_self ON kb_permissions
  AS PERMISSIVE
  FOR ALL
  TO public
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
CREATE POLICY kbp_platform_bypass ON kb_permissions
  AS PERMISSIVE
  FOR ALL
  TO public
  USING (current_setting('app.current_tenant_id', true) IS NULL)
  WITH CHECK (current_setting('app.current_tenant_id', true) IS NULL);

CREATE INDEX IF NOT EXISTS idx_kb_permissions_tenant ON kb_permissions(tenant_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON kb_permissions TO ani_app;
