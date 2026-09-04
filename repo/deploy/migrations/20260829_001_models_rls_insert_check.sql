-- Ensure tenant-scoped model inserts are authorized by the same RLS boundary
-- used for reads. The original policy only had USING, which caused every
-- model INSERT to fail with SQLSTATE 42501 even after setting the tenant.
ALTER POLICY tenant_isolation ON models
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- PostgreSQL requires a permissive policy in addition to restrictive policies;
-- the restrictive tenant_isolation policy remains the authoritative boundary.
DROP POLICY IF EXISTS models_tenant_access ON models;
CREATE POLICY models_tenant_access ON models
  AS PERMISSIVE FOR ALL USING (TRUE) WITH CHECK (TRUE);
