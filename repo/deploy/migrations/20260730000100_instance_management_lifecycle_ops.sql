-- ANI Platform - approved instance-management summaries and lifecycle operations.
-- Depends on: 20260519000400_instance_u_vm_protection.sql


ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS description TEXT;

ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS labels JSONB NOT NULL DEFAULT '{}';

ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS image_summary JSONB NOT NULL DEFAULT '{}';

ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS compute_summary JSONB NOT NULL DEFAULT '{}';

ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS network_summary JSONB NOT NULL DEFAULT '{}';

ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS access_summary JSONB NOT NULL DEFAULT '{}';

ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS storage_attachments JSONB NOT NULL DEFAULT '[]';

ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS sandbox_status JSONB NOT NULL DEFAULT '{}';

ALTER TABLE workload_instance_operation_steps
    ADD COLUMN IF NOT EXISTS task_id TEXT,
    ADD COLUMN IF NOT EXISTS resource_type TEXT,
    ADD COLUMN IF NOT EXISTS resource_id TEXT;

ALTER TABLE workload_instances
    DROP CONSTRAINT IF EXISTS workload_instances_workload_kind_check;

ALTER TABLE workload_instances
    ADD CONSTRAINT workload_instances_workload_kind_check
    CHECK (workload_kind IN (
        'vm',
        'container',
        'gpu_container',
        'inference',
        'notebook',
        'agent_sandbox',
        'sandbox',
        'batch_job',
        'k8s_cluster',
        'bare_metal',
        'dpu_node'
    ));

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
        'attach_filesystem',
        'detach_filesystem',
        'rollback',
        'scale',
        'update_image',
        'bind_secret',
        'unbind_secret',
        'change_security_groups',
        'set_termination_protection',
        'pause',
        'resume',
        'extend',
        'touch_idle',
        'console_session'
    ));

