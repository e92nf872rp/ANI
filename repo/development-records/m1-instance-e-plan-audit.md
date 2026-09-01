# M1-INSTANCE-E: Plan Audit Trail

Date: 2026-05-11

## Product Plan Mapping

- `ANI-06` / `模块 1：基础设施底座`.
- Continues the instance foundation before `模块 3：模型管理平台`.

## Scope

Added plan/render/admission audit persistence:

- `pkg/ports/workload_runtime.go` adds `WorkloadPlanAuditStore`.
- `pkg/adapters/runtime/plan_audit_store.go`
- `pkg/adapters/runtime/plan_audit_store_test.go`
- `deploy/migrations/20260501000100_init_schema.sql` adds `instance_plan_audits` with tenant RLS.
- `deploy/manifests/m1-instance-e/`
- `scripts/validate_instance_audit.py`

Updated:

- `pkg/bootstrap/deps.go` wires `WorkloadPlanAudit`.
- `Makefile` adds `validate-instance-audit` and wires it into `validate-infra`.

## Behavior

The audit store persists:

- Tenant/user context.
- Instance ID/name/kind.
- Provider name.
- Rendered manifests.
- Admission allowed/denied result.
- Admission reason and warnings.

Denied admission is still auditable. Future provider server-side dry-run/apply
must not run without an audit row.

## Validation

Passed:

```bash
make validate-instance-audit
make validate-infra
make test
make build
git diff --check
```

## Version Impact

Expected release impact: `MINOR`.

Reason:
- Adds a tenant-scoped metadata table and a new audit capability behind the
  instance runtime abstraction.

## Next Recommended Slice

- `M1-INSTANCE-F`: provider server-side dry-run executor.
