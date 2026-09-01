-- Core platform-workloads persistence for CPU Kubernetes provider restart recovery.
-- Independent of /instances. Tenant RLS matches other Core control-plane tables.


CREATE TABLE IF NOT EXISTS platform_workloads (
    id          UUID        PRIMARY KEY,
    tenant_id   UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    deleted     BOOLEAN     NOT NULL DEFAULT FALSE,
    spec        JSONB       NOT NULL,
    record      JSONB       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_workloads_active_name
    ON platform_workloads (tenant_id, name)
    WHERE NOT deleted;

CREATE INDEX IF NOT EXISTS idx_platform_workloads_tenant_updated
    ON platform_workloads (tenant_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS platform_workload_intents (
    tenant_id       UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    idempotency_key UUID        NOT NULL,
    fingerprint     TEXT        NOT NULL,
    workload_id     UUID        NOT NULL REFERENCES platform_workloads(id) ON DELETE CASCADE,
    PRIMARY KEY (tenant_id, idempotency_key)
);

GRANT SELECT, INSERT, UPDATE, DELETE ON
    platform_workloads,
    platform_workload_intents
TO ani_app;

ALTER TABLE platform_workloads ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_workloads FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON platform_workloads;
CREATE POLICY tenant_isolation ON platform_workloads
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE platform_workload_intents ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_workload_intents FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON platform_workload_intents;
CREATE POLICY tenant_isolation ON platform_workload_intents
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

COMMENT ON TABLE platform_workloads IS
    'Core service-only platform workload records. Not tenant /instances.';
COMMENT ON TABLE platform_workload_intents IS
    'Idempotency fingerprints for platform-workloads mutations.';

