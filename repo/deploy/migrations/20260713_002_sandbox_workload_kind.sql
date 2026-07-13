-- Allow Core's current WorkloadKindSandbox value in persisted workload metadata.
-- Keep agent_sandbox for historical rows and old fixtures.

ALTER TABLE instance_plan_audits
    DROP CONSTRAINT IF EXISTS instance_plan_audits_workload_kind_check;

ALTER TABLE instance_plan_audits
    ADD CONSTRAINT instance_plan_audits_workload_kind_check
    CHECK (workload_kind IN ('vm','container','gpu_container','inference','notebook','sandbox','agent_sandbox','batch_job'));

ALTER TABLE workload_instances
    DROP CONSTRAINT IF EXISTS workload_instances_workload_kind_check;

ALTER TABLE workload_instances
    ADD CONSTRAINT workload_instances_workload_kind_check
    CHECK (workload_kind IN ('vm','container','gpu_container','inference','notebook','sandbox','agent_sandbox','batch_job'));
