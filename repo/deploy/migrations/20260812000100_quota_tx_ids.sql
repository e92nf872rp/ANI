-- ANI Platform · workload_instances + quota_tx_ids JSONB column
-- Description: Add quota_tx_ids JSONB column to workload_instances for TCC tx_id storage
-- Rollback: ALTER TABLE workload_instances DROP COLUMN quota_tx_ids

ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS quota_tx_ids JSONB NOT NULL DEFAULT '[]';

