-- ANI Platform · BOSS 预留账本表
-- Description: resource_reservation_allocations stores tenant-level allocated_gpu_count
--              (single dimension, no per-spec split). PK tenant_id, CHECK >= 0.
--              RLS enabled for tenant isolation (consistent with all tenant-scoped tables).
-- Rollback: DROP TABLE resource_reservation_allocations
BEGIN;

CREATE TABLE IF NOT EXISTS resource_reservation_allocations (
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    allocated_gpu_count BIGINT NOT NULL DEFAULT 0 CHECK (allocated_gpu_count >= 0),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id)
);

-- Row Level Security: tenant isolation (matches all other tenant-scoped tables)
ALTER TABLE resource_reservation_allocations ENABLE ROW LEVEL SECURITY;
ALTER TABLE resource_reservation_allocations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON resource_reservation_allocations
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

COMMIT;
