-- Persist tenant-scoped policy mutation replay results and align lease TTL with OpenAPI.
BEGIN;

ALTER TABLE inference_access_policies
    DROP CONSTRAINT IF EXISTS inference_access_policies_lease_ttl_seconds_check;
ALTER TABLE inference_access_policies
    ADD CONSTRAINT inference_access_policies_lease_ttl_seconds_check
    CHECK (lease_ttl_seconds BETWEEN 1 AND 3600);

CREATE TABLE IF NOT EXISTS inference_access_policy_mutations (
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    operation_scope TEXT NOT NULL,
    idempotency_key UUID NOT NULL,
    request_hash TEXT NOT NULL,
    result_snapshot JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT inference_access_policy_mutations_pkey
        PRIMARY KEY (tenant_id, operation_scope, idempotency_key),
    CONSTRAINT inference_access_policy_mutations_request_hash_check
        CHECK (request_hash ~ '^sha256:[0-9a-f]{64}$')
);

ALTER TABLE inference_access_policy_mutations ENABLE ROW LEVEL SECURITY;
ALTER TABLE inference_access_policy_mutations FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS inference_access_policy_mutations_tenant_isolation
    ON inference_access_policy_mutations;
CREATE POLICY inference_access_policy_mutations_tenant_isolation
    ON inference_access_policy_mutations
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

COMMIT;
