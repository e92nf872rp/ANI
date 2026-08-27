# M1-INSTANCE-J: Instance Persistence and Query API

Date: 2026-05-11

## Product Plan Mapping

- `ANI-06` / `模块 1：基础设施底座`.
- Continues the instance foundation before `模块 3：模型管理平台`.

## Scope

Added persistent instance state and query boundary:

- `pkg/ports/workload_runtime.go` adds `WorkloadInstanceStore`.
- `pkg/adapters/runtime/instance_store.go`
- `pkg/adapters/runtime/instance_store_test.go`
- `deploy/migrations/20260501000100_init_schema.sql` adds `workload_instances`
  with tenant RLS.
- `deploy/manifests/m1-instance-j/`
- `scripts/validate_instance_store.py`

Updated:

- `pkg/adapters/runtime/instance_orchestrator.go` writes planned and reconciled
  status when an instance store is configured.
- `pkg/bootstrap/deps.go` wires `WorkloadStore`.
- `Makefile` adds `validate-instance-store` and wires it into `validate-infra`.

## Behavior

`MetadataInstanceStore` persists queryable instance state:

- tenant id and instance id.
- instance name and workload kind.
- provider and provider id.
- audit id.
- provider resource refs.
- standard ANI workload state.
- endpoint, node, reason, network, and storage state.

`LocalInstanceOrchestrator` writes the planned status before provider status
observation and writes reconciled status after `WorkloadStatusReconciler`.

Business query paths should use `WorkloadInstanceStore.Get/List` or a
higher-level service wrapping it instead of relying on `PlanningRuntime` memory.

## Validation

Passed:

```bash
make validate-instance-store
make validate-infra
make test
make build
git diff --check
```

## Version Impact

Expected release impact: `MINOR`.

Reason:
- Adds a new persistent instance state store and schema behind the workload
  runtime abstraction.

## Next Recommended Slice

- Real provider adapter integration or instance service API layer.
