-- Additive status for platform-workload idempotency intents.
-- pending: provider mutation has not succeeded; matching key retries the provider.
-- succeeded: provider mutation finished; matching key returns the stored record.


ALTER TABLE platform_workload_intents
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'pending';

ALTER TABLE platform_workload_intents
    DROP CONSTRAINT IF EXISTS platform_workload_intents_status_check;

ALTER TABLE platform_workload_intents
    ADD CONSTRAINT platform_workload_intents_status_check
    CHECK (status IN ('pending', 'succeeded'));

COMMENT ON COLUMN platform_workload_intents.status IS
    'Idempotency intent state: pending retries the provider; succeeded returns the stored record.';

