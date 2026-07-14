-- 20260714_003_api_key_idempotency.sql
-- Description: Add tenant-scoped idempotency metadata for API key creation.

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS idempotency_key TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_idempotency
    ON api_keys (tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL AND idempotency_key <> '';

COMMENT ON COLUMN api_keys.idempotency_key IS
    'Client-generated idempotency key for API key creation; plaintext key_value is only replayed from short-TTL encrypted cache.';
