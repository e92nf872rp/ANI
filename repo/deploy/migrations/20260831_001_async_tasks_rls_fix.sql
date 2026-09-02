-- Fix async_tasks RLS + table grants: align the repository with the
-- dual-policy form already verified live on the dev database.
--
-- Background (live-verified 2026-08-31 with ani_app_user, non-BYPASSRLS,
-- on the dev PG instance; see TASKCENTER-A2):
--   1. init_schema creates async_tasks with a single RESTRICTIVE
--      tenant_isolation policy and NO PERMISSIVE policy. PostgreSQL RLS
--      denies all rows when no PERMISSIVE policy passes, for non-BYPASSRLS
--      roles, regardless of current_setting — on a fresh deployment the
--      task center (Get/List/Create/lazy-sync Update via WithTenantTx)
--      would see an empty async_tasks under the production app role.
--      The dev database was repaired out of band to the dual-policy form;
--      that repair never landed in the repository.
--   2. Same drift for table-level grants: init_schema's
--      GRANT ... ON ALL TABLES runs before any table exists in the fresh
--      deployment path, so async_tasks carries no ani_app grant. Dev was
--      granted SELECT/INSERT/UPDATE manually. DELETE is intentionally NOT
--      granted: no product code path deletes async_tasks rows (tasks are
--      state-machined, not removed), matching the verified least-privilege
--      form.
--
-- Fix (matches the 20260825_001 workload_instances pattern):
--   1. GRANT SELECT, INSERT, UPDATE on async_tasks to ani_app
--      (member ani_app_user inherits).
--   2. DROP the RESTRICTIVE-only tenant_isolation policy.
--   3. CREATE PERMISSIVE platform_bypass: app.current_tenant_id unset/NULL →
--      all rows (platform context, WithPlatformTx).
--   4. CREATE PERMISSIVE self: tenant_id matches current_setting →
--      own rows only (tenant context, WithTenantTx).
--
-- platform_bypass uses the NULLIF(..., '') form instead of a bare IS NULL:
-- WithTenantTx's set_config(..., is_local=true) leaves the GUC as an empty
-- string on pooled connections after the transaction ends, and a bare
-- IS NULL would then misread the platform context as a tenant context and
-- return 0 rows. The NULLIF form treats NULL and '' the same, mirroring the
-- self policy's own NULLIF empty-string handling.
--
-- Idempotent: safe to re-apply on the dev database (already in this form).

BEGIN;

GRANT SELECT, INSERT, UPDATE ON async_tasks TO ani_app;

DROP POLICY IF EXISTS tenant_isolation ON async_tasks;
DROP POLICY IF EXISTS async_tasks_platform_bypass ON async_tasks;
DROP POLICY IF EXISTS async_tasks_self ON async_tasks;

CREATE POLICY async_tasks_platform_bypass
  ON async_tasks FOR ALL
  USING (NULLIF(current_setting('app.current_tenant_id', true), '') IS NULL);

CREATE POLICY async_tasks_self
  ON async_tasks FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

COMMIT;

-- ===========================================================================
-- Rollback
-- ===========================================================================
-- REVOKE SELECT, INSERT, UPDATE ON async_tasks FROM ani_app;
-- DROP POLICY IF EXISTS async_tasks_self ON async_tasks;
-- DROP POLICY IF EXISTS async_tasks_platform_bypass ON async_tasks;
-- CREATE POLICY tenant_isolation ON async_tasks
--     AS RESTRICTIVE
--     USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
