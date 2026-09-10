-- Model repository P0: tenant-scoped idempotency for synchronous mutations.
-- A replay returns the original resource; a reused key with a different
-- request hash is rejected before any model row is mutated.
CREATE TABLE IF NOT EXISTS model_mutation_idempotency (
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    operation_scope TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash    TEXT NOT NULL,
    resource_id     UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, operation_scope, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_model_mutation_idempotency_tenant
    ON model_mutation_idempotency(tenant_id, created_at DESC);

ALTER TABLE model_mutation_idempotency ENABLE ROW LEVEL SECURITY;
ALTER TABLE model_mutation_idempotency FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS model_mutation_idempotency_tenant_isolation
    ON model_mutation_idempotency;
CREATE POLICY model_mutation_idempotency_tenant_isolation
    ON model_mutation_idempotency
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- Keep the application role aligned with the existing model tables.  The
-- service only claims/replays and records mutations; it never deletes them.
GRANT SELECT, INSERT, UPDATE ON model_mutation_idempotency TO ani_app;

-- A restrictive policy is only a boundary predicate; PostgreSQL also needs a
-- permissive policy for rows to be eligible.  Keep the tenant predicate in
-- the restrictive policy above and make the permissive side explicit.
DROP POLICY IF EXISTS model_mutation_idempotency_access
    ON model_mutation_idempotency;
CREATE POLICY model_mutation_idempotency_access
    ON model_mutation_idempotency
    AS PERMISSIVE
    FOR ALL
    USING (TRUE)
    WITH CHECK (TRUE);
