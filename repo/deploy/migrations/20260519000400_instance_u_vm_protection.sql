-- ANI Platform · Migration 004
-- Description: M1-INSTANCE-U VM lifecycle protection and SSH metadata
-- Depends on: 20260502000300_permissions_schema.sql


ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS lifecycle_policy JSONB NOT NULL DEFAULT '{}';

ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS ssh_connection JSONB NOT NULL DEFAULT '{}';

ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS snapshots JSONB NOT NULL DEFAULT '[]';

ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS container_status JSONB NOT NULL DEFAULT '{}';

ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS gpu_status JSONB NOT NULL DEFAULT '{}';

COMMENT ON COLUMN workload_instances.lifecycle_policy IS
    'Instance lifecycle policy snapshot, including termination_protection for VM dangerous operation prechecks.';

COMMENT ON COLUMN workload_instances.ssh_connection IS
    'VM SSH connection metadata only: username, host, port, key reference, readiness and reason. Private keys are never stored here.';

COMMENT ON COLUMN workload_instances.snapshots IS
    'VM snapshot metadata for local/dev profile and operation-visible snapshot records. Provider-native snapshot execution can reconcile this later.';

COMMENT ON COLUMN workload_instances.container_status IS
    'Container/GPU container rollout status metadata: replicas, ready replicas, revision, rollout status and revision history.';

COMMENT ON COLUMN workload_instances.gpu_status IS
    'GPU container scheduling and utilization status metadata: vendor, model, count, scheduling reason and utilization percent.';

ALTER TABLE workload_instance_operations
    DROP CONSTRAINT IF EXISTS workload_instance_operations_operation_check;

ALTER TABLE workload_instance_operations
    ADD CONSTRAINT workload_instance_operations_operation_check
    CHECK (operation IN (
        'create',
        'start',
        'stop',
        'restart',
        'resize',
        'rebuild',
        'delete',
        'snapshot',
        'attach_volume',
        'detach_volume',
        'rollback',
        'console_session'
    ));

