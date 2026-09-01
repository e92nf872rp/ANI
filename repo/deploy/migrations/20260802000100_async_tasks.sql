-- Core HTTP AsyncTask persistence for restart-safe task polling.
ALTER TABLE async_tasks
    ADD COLUMN IF NOT EXISTS result JSONB,
    ADD COLUMN IF NOT EXISTS error_message TEXT,
    ADD COLUMN IF NOT EXISTS dead_letter_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE UNIQUE INDEX IF NOT EXISTS idx_async_tasks_tenant_idempotency
    ON async_tasks(tenant_id, idempotency_key);

CREATE INDEX IF NOT EXISTS idx_async_tasks_tenant_id
    ON async_tasks(tenant_id, id);
