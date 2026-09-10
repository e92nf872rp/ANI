-- Model repository remote import fencing.
--
-- Remote imports are public-only and asynchronous.  The request row is the
-- durable idempotency fence; async_tasks/outbox_events are written in the
-- same transaction by model-service.  Existing rows (if any) remain readable
-- while new writes populate all additive fields.

ALTER TABLE model_import_tasks
    ADD COLUMN IF NOT EXISTS revision TEXT NOT NULL DEFAULT 'main',
    ADD COLUMN IF NOT EXISTS idempotency_key TEXT,
    ADD COLUMN IF NOT EXISTS request_hash TEXT,
    ADD COLUMN IF NOT EXISTS async_task_id UUID,
    ADD COLUMN IF NOT EXISTS target_storage_path TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_model_import_tasks_tenant_idempotency
    ON model_import_tasks (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_model_import_tasks_async_task
    ON model_import_tasks (async_task_id)
    WHERE async_task_id IS NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'model_import_tasks_async_task_fk'
          AND conrelid = 'model_import_tasks'::regclass
    ) THEN
        ALTER TABLE model_import_tasks
            ADD CONSTRAINT model_import_tasks_async_task_fk
            FOREIGN KEY (async_task_id) REFERENCES async_tasks(id)
            ON DELETE CASCADE;
    END IF;
END $$;

-- model_import_tasks was created before the shared RLS policy block and must
-- not become a cross-tenant side channel.  Keep the tenant predicate in a
-- restrictive policy and an explicit permissive policy for PostgreSQL's RLS
-- composition rules (matching the async_tasks/outbox repair migrations).
ALTER TABLE model_import_tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE model_import_tasks FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS model_import_tasks_tenant_isolation ON model_import_tasks;
DROP POLICY IF EXISTS model_import_tasks_tenant_access ON model_import_tasks;
CREATE POLICY model_import_tasks_tenant_isolation
    ON model_import_tasks
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
CREATE POLICY model_import_tasks_tenant_access
    ON model_import_tasks
    AS PERMISSIVE
    FOR ALL
    USING (TRUE)
    WITH CHECK (TRUE);

GRANT SELECT, INSERT, UPDATE ON model_import_tasks TO ani_app;
