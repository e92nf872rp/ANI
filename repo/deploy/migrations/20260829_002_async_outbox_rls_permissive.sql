-- Repair tenant RLS for the async task and outbox write path.
--
-- The initial schema installed only a RESTRICTIVE tenant_isolation policy on
-- these tables. PostgreSQL requires at least one PERMISSIVE policy to grant a
-- row, so tenant-scoped task creation failed even after the application set
-- app.current_tenant_id. Keep the same platform/self policy shape used by the
-- platform workload tables: platform context can run the dispatcher, while a
-- tenant context can only read/write its own rows.

BEGIN;

ALTER TABLE async_tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE async_tasks FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON async_tasks;
DROP POLICY IF EXISTS async_tasks_platform_bypass ON async_tasks;
DROP POLICY IF EXISTS async_tasks_self ON async_tasks;

CREATE POLICY async_tasks_platform_bypass ON async_tasks
  AS PERMISSIVE FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL)
  WITH CHECK (current_setting('app.current_tenant_id', true) IS NULL);

CREATE POLICY async_tasks_self ON async_tasks
  AS PERMISSIVE FOR ALL
  USING (
    tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid
  );

ALTER TABLE outbox_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbox_events FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON outbox_events;
DROP POLICY IF EXISTS outbox_events_platform_bypass ON outbox_events;
DROP POLICY IF EXISTS outbox_events_self ON outbox_events;

CREATE POLICY outbox_events_platform_bypass ON outbox_events
  AS PERMISSIVE FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL)
  WITH CHECK (current_setting('app.current_tenant_id', true) IS NULL);

CREATE POLICY outbox_events_self ON outbox_events
  AS PERMISSIVE FOR ALL
  USING (
    tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid
  )
  WITH CHECK (
    tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid
  );

COMMIT;

-- Rollback (restores the original restrictive-only behavior):
-- BEGIN;
-- DROP POLICY IF EXISTS async_tasks_platform_bypass ON async_tasks;
-- DROP POLICY IF EXISTS async_tasks_self ON async_tasks;
-- CREATE POLICY tenant_isolation ON async_tasks AS RESTRICTIVE
--   USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
-- DROP POLICY IF EXISTS outbox_events_platform_bypass ON outbox_events;
-- DROP POLICY IF EXISTS outbox_events_self ON outbox_events;
-- CREATE POLICY tenant_isolation ON outbox_events AS RESTRICTIVE
--   USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
-- COMMIT;
