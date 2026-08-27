-- ANI Services inference control-plane state.
-- Additive over the legacy inference_services table from 20260501000100_init_schema.sql.


ALTER TABLE inference_services
    ADD COLUMN IF NOT EXISTS served_model_name TEXT,
    ADD COLUMN IF NOT EXISTS model_display_snapshot JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS desired_spec JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS applied_spec JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS placement_mode TEXT NOT NULL DEFAULT 'auto',
    ADD COLUMN IF NOT EXISTS status_reason TEXT,
    ADD COLUMN IF NOT EXISTS status_message TEXT,
    ADD COLUMN IF NOT EXISTS generation BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS observed_generation BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS desired_state TEXT NOT NULL DEFAULT 'running',
    ADD COLUMN IF NOT EXISTS runtime_ref UUID,
    ADD COLUMN IF NOT EXISTS runtime_endpoint TEXT,
    ADD COLUMN IF NOT EXISTS invocation_url TEXT,
    ADD COLUMN IF NOT EXISTS ready_replicas INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS current_operation_id UUID,
    ADD COLUMN IF NOT EXISTS legacy_quarantined BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

UPDATE inference_services
SET served_model_name = name,
    desired_spec = jsonb_build_object(
        'replicas', GREATEST(replicas, 1),
        'placement_mode', 'auto',
        'gpu_type', gpu_type,
        'gpu_count_per_pod', GREATEST(gpu_count_per_pod, 1)
    ),
    applied_spec = CASE
        WHEN status IN ('running', 'stopped') THEN jsonb_build_object(
            'replicas', GREATEST(replicas, 1),
            'placement_mode', 'auto',
            'gpu_type', gpu_type,
            'gpu_count_per_pod', GREATEST(gpu_count_per_pod, 1)
        )
        ELSE '{}'::jsonb
    END,
    desired_state = CASE WHEN status IN ('stopping', 'stopped') THEN 'stopped' ELSE 'running' END,
    observed_generation = CASE WHEN status IN ('running', 'stopped') THEN 1 ELSE 0 END,
    status = CASE WHEN status IN ('downloading', 'decrypting') THEN 'deploying' ELSE status END,
    status_reason = COALESCE(status_reason, 'LEGACY_CONTROL_PLANE_QUARANTINED'),
    status_message = COALESCE(status_message, 'Legacy inference service requires explicit migration before reconciliation'),
    legacy_quarantined = TRUE
WHERE served_model_name IS NULL;

ALTER TABLE inference_services
    ALTER COLUMN served_model_name SET NOT NULL;

ALTER TABLE inference_services
    DROP CONSTRAINT IF EXISTS inference_services_tenant_id_name_key;

CREATE UNIQUE INDEX IF NOT EXISTS idx_inference_services_active_name
    ON inference_services(tenant_id, name)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_inference_services_active_served_model_name
    ON inference_services(tenant_id, served_model_name)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS inference_operations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    service_id UUID NOT NULL REFERENCES inference_services(id),
    type TEXT NOT NULL
        CHECK (type IN ('create','scale','start','stop','restart','delete')),
    operation_scope TEXT NOT NULL,
    idempotency_key UUID NOT NULL,
    request_hash TEXT NOT NULL,
    target_generation BIGINT NOT NULL,
    rollback_generation BIGINT,
    before_spec JSONB NOT NULL DEFAULT '{}',
    target_spec JSONB NOT NULL DEFAULT '{}',
    state TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending','running','completed','failed','cancelled','dead_letter')),
    attempt INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_owner TEXT,
    lease_until TIMESTAMPTZ,
    lease_token UUID,
    runtime_task_id TEXT,
    result_snapshot JSONB,
    error_code TEXT,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    UNIQUE (tenant_id, operation_scope, idempotency_key)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_inference_operations_active_generation
    ON inference_operations(service_id, target_generation)
    WHERE state IN ('pending', 'running');

CREATE INDEX IF NOT EXISTS idx_inference_operations_claim
    ON inference_operations(next_attempt_at, created_at)
    WHERE state IN ('pending', 'running');

CREATE INDEX IF NOT EXISTS idx_inference_operations_tenant_service
    ON inference_operations(tenant_id, service_id, created_at DESC);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'inference_services_current_operation_fk'
          AND conrelid = 'inference_services'::regclass
    ) THEN
        ALTER TABLE inference_services
            ADD CONSTRAINT inference_services_current_operation_fk
            FOREIGN KEY (current_operation_id) REFERENCES inference_operations(id)
            DEFERRABLE INITIALLY DEFERRED;
    END IF;
END $$;

ALTER TABLE inference_operations ENABLE ROW LEVEL SECURITY;
ALTER TABLE inference_operations FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS inference_services_tenant_access ON inference_services;
CREATE POLICY inference_services_tenant_access ON inference_services
    AS PERMISSIVE
    USING (TRUE)
    WITH CHECK (TRUE);
DROP POLICY IF EXISTS inference_operations_tenant_access ON inference_operations;
CREATE POLICY inference_operations_tenant_access ON inference_operations
    AS PERMISSIVE
    USING (TRUE)
    WITH CHECK (TRUE);
DROP POLICY IF EXISTS inference_operations_tenant_isolation ON inference_operations;
CREATE POLICY inference_operations_tenant_isolation ON inference_operations
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

