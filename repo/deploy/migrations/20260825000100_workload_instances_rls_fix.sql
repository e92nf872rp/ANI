-- Fix workload_instances RLS: replace RESTRICTIVE-only policy with
-- PERMISSIVE dual-policy pattern (platform_bypass + self), aligned with
-- resource_quota / resource_reservations.
--
-- Background:
--   workload_instances had a single RESTRICTIVE tenant_isolation policy
--   with NO PERMISSIVE policy. PostgreSQL RLS denies all rows when there
--   is no PERMISSIVE policy to pass, regardless of current_setting value.
--   This caused:
--     - WithPlatformTx (no tenant_id set): COUNT(*) returns 0 →
--       specInUse false-negative → allows deleting in-use GPU spec.
--     - WithTenantTx (correct tenant_id set): also returns 0 for
--       non-BYPASSRLS roles → masked in dev/test by superuser connections.
--
-- Fix:
--   1. DROP the old RESTRICTIVE tenant_isolation policy.
--   2. CREATE PERMISSIVE platform_bypass: app.current_tenant_id NULL → all rows.
--   3. CREATE PERMISSIVE self: tenant_id matches current_setting → own rows.
--   This matches the dual-policy pattern used by resource_quota et al.


-- 1. Drop the old RESTRICTIVE-only policy
DROP POLICY IF EXISTS tenant_isolation ON workload_instances;

-- 2. Platform context (app.current_tenant_id unset/NULL) → all rows visible
CREATE POLICY workload_instances_platform_bypass
  ON workload_instances FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

-- 3. Tenant context → only own rows
CREATE POLICY workload_instances_self
  ON workload_instances FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);


-- ===========================================================================
-- Rollback
-- ===========================================================================
-- DROP POLICY IF EXISTS workload_instances_self ON workload_instances;
-- DROP POLICY IF EXISTS workload_instances_platform_bypass ON workload_instances;
-- CREATE POLICY tenant_isolation ON workload_instances
--     AS RESTRICTIVE
--     USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
