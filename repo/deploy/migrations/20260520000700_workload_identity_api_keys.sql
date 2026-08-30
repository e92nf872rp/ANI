-- ===========================================================================
-- 20260520000700_workload_identity_api_keys.sql
-- Description: Align api_keys with Workload Identity P0.
-- ===========================================================================


ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS instance_id TEXT;

ALTER TABLE api_keys
    ALTER COLUMN instance_id TYPE TEXT USING instance_id::text;

CREATE INDEX IF NOT EXISTS idx_api_keys_instance
    ON api_keys (tenant_id, instance_id)
    WHERE instance_id IS NOT NULL;

COMMENT ON COLUMN api_keys.instance_id IS
    'ANI workload instance id bound to this lifecycle-scoped key. Revoked when the instance is deleted.';

