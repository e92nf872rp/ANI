-- ANI Platform · Migration 002
-- Description: Instance operations tracking + idempotency + workload identity
-- Depends on: 20260501000100_init_schema.sql
-- Run: atlas migrate apply  OR  psql $DATABASE_URL -f <this_file>


-- ===========================================================================
-- 1. IDEMPOTENCY ON workload_instances
--    Prevents duplicate instance creation on client retry.
-- ===========================================================================

ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS idempotency_key TEXT;

-- Enforce uniqueness only when idempotency_key is set (NULL = legacy caller).
CREATE UNIQUE INDEX IF NOT EXISTS idx_workload_instances_idem
    ON workload_instances (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

COMMENT ON COLUMN workload_instances.idempotency_key IS
    'Client UUID per create intent. Server caches result for 24h on duplicate submission.';

-- ===========================================================================
-- 2. INSTANCE OPERATION RECORDS
--    Every lifecycle action (create/start/stop/resize/delete) has a record.
--    Backs GET /api/v1/instances/{id}/operations
-- ===========================================================================

CREATE TABLE workload_instance_operations (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    instance_id             TEXT        NOT NULL,
    operation               TEXT        NOT NULL
                                CHECK (operation IN ('create','start','stop','restart','resize','delete')),
    status                  TEXT        NOT NULL DEFAULT 'accepted'
                                CHECK (status IN ('accepted','in_progress','succeeded','failed','cancelled')),
    idempotency_key         TEXT,
    requested_by            TEXT        NOT NULL,
    precheck_json           JSONB       NOT NULL DEFAULT '{}',
    destructive_impact_json JSONB       NOT NULL DEFAULT '{}',
    before_spec_json        JSONB       NOT NULL DEFAULT '{}',
    after_spec_json         JSONB       NOT NULL DEFAULT '{}',
    provider_refs_json      JSONB       NOT NULL DEFAULT '[]',
    failure_reason          TEXT,
    failure_message         TEXT,
    retry_eligible          BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE workload_instance_operations ENABLE ROW LEVEL SECURITY;

CREATE POLICY wio_tenant_isolation ON workload_instance_operations
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', TRUE), '')::uuid);

CREATE INDEX idx_wio_tenant_instance ON workload_instance_operations (tenant_id, instance_id);
CREATE INDEX idx_wio_active_status   ON workload_instance_operations (tenant_id, status)
    WHERE status NOT IN ('succeeded','failed','cancelled');
CREATE UNIQUE INDEX idx_wio_idempotency ON workload_instance_operations (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- ===========================================================================
-- 3. OPERATION STEP TIMELINE
--    Ordered steps per operation (plan→render→admission→audit→dry-run→apply→reconcile)
-- ===========================================================================

CREATE TABLE workload_instance_operation_steps (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID        NOT NULL,
    operation_id UUID        NOT NULL REFERENCES workload_instance_operations(id) ON DELETE CASCADE,
    step_name    TEXT        NOT NULL,
    status       TEXT        NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','running','succeeded','failed','skipped')),
    message      TEXT,
    started_at   TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE workload_instance_operation_steps ENABLE ROW LEVEL SECURITY;

CREATE POLICY wios_tenant_isolation ON workload_instance_operation_steps
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', TRUE), '')::uuid);

CREATE INDEX idx_wios_operation ON workload_instance_operation_steps (operation_id);

-- ===========================================================================
-- 4. WORKLOAD IDENTITY — lifecycle-bound API keys (P0 implementation)
--    Instances get scoped API keys that auto-revoke on instance deletion.
-- ===========================================================================

DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_tables WHERE schemaname='public' AND tablename='api_keys') THEN
        CREATE TABLE api_keys (
            id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
            tenant_id    UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
            user_id      UUID        REFERENCES users(id) ON DELETE SET NULL,
            name         TEXT        NOT NULL,
            key_prefix   TEXT        NOT NULL,
            key_hash     TEXT        NOT NULL UNIQUE,
            -- Workload Identity fields (NULL = regular user API key)
            instance_id  UUID,
            -- [{"resource":"instances","actions":["read","list"]}]
            scope        JSONB       NOT NULL DEFAULT '[]',
            status       TEXT        NOT NULL DEFAULT 'active'
                             CHECK (status IN ('active','revoked')),
            expires_at   TIMESTAMPTZ,
            last_used_at TIMESTAMPTZ,
            created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
            revoked_at   TIMESTAMPTZ
        );
        ALTER TABLE api_keys ENABLE ROW LEVEL SECURITY;
        CREATE POLICY api_keys_tenant_isolation ON api_keys
            USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', TRUE), '')::uuid);
        CREATE INDEX idx_api_keys_tenant   ON api_keys (tenant_id, status);
        CREATE INDEX idx_api_keys_instance ON api_keys (instance_id) WHERE instance_id IS NOT NULL;
    ELSE
        ALTER TABLE api_keys
            ADD COLUMN IF NOT EXISTS instance_id UUID,
            ADD COLUMN IF NOT EXISTS scope       JSONB NOT NULL DEFAULT '[]';
        CREATE INDEX IF NOT EXISTS idx_api_keys_instance ON api_keys (instance_id)
            WHERE instance_id IS NOT NULL;
    END IF;
END $$;

COMMENT ON COLUMN api_keys.instance_id IS
    'Bound instance. When set, key is auto-revoked on instance deletion.';
COMMENT ON COLUMN api_keys.scope IS
    'Permission scope. Format: [{"resource":"instances","actions":["read"]}]. '
    'Workload Identity keys must have explicit non-empty scope.';

