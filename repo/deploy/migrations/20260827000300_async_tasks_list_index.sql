-- TASKCENTER-A1: cursor pagination index for GET /api/v1/tasks.
-- Existing async_tasks indexes cover (tenant_id, status), (task_type, status)
-- and the (tenant_id, idempotency_key) / (tenant_id, id) keys; none covers the
-- list ordering (created_at DESC, id DESC) within a tenant. The composite
-- index keeps the keyset comparison (created_at, id) < (anchor) fully
-- index-served. Shared migration set with kb-service: name is unique.
CREATE INDEX IF NOT EXISTS idx_async_tasks_tenant_created
    ON async_tasks(tenant_id, created_at DESC, id DESC);
