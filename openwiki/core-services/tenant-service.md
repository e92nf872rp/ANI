---
type: service
title: Tenant Service (gRPC)
description: "Standalone gRPC service: tenant plan CRUD (draft/active/disabled), BindPlanQuota with 2PC (update plan_id → sync quota → rollback on failure), audit logging for quota plan changes. Uses Services-layer bootstrap (services/pkg/bootstrap/)."
tags: [core-services, tenant, plans, quota, audit]
---

# Tenant Service (gRPC)

## Overview

**Service**: `services/tenant-service/` · **Bootstrap**: uses `services/pkg/bootstrap/` (the architecturally correct Services bootstrap — NOT Core `pkg/bootstrap/`)

Standalone gRPC service for tenant plan management and quota binding:

- **TenantPlan CRUD** — Create, Get, List, Update, SoftDelete tenant plans (status machine: draft → active → disabled)
- **BindPlanQuota** — Bind a quota plan to a tenant with 2PC: update plan_id → sync Core quota → rollback on failure
- **Audit Logging** — Every plan/operation change is logged to `tenant_plan_audit_log` table

### Scope Boundary: Tenant Service Does NOT Create Tenants

Tenant **creation** (INSERT into `tenants` table) is **owned by the Core API** — the Services layer has no tenant-creation or provisioning endpoint. The Tenant Service reads tenants via `TenantSvcClient` (HTTP proxy to Core API `GET /admin/tenants/{id}`) and updates a single field (`plan_id`) via `PATCH /admin/tenants/{id}`. There is **no async bootstrap workflow** (no schema provisioning, seed data insertion, or default resource creation). Plan binding is the only tenant lifecycle operation at the Services layer.

## Entrypoint

```go
func main() {
    cfg := config.Load()
    deps := bootstrap.MustConnect(cfg)
    defer deps.Close()

    plans := postgres.NewPostgresTenantPlanStore(deps.DB)
    audit := postgres.NewPostgresTenantPlanAuditStore(deps.DB)
    coreQuota := core.NewQuotaSvcClient()
    coreTenants := core.NewTenantSvcClient()

    tenantPlanSvc := service.NewTenantPlanService(plans, audit, coreQuota, coreTenants)
    tenantSvc := service.NewTenantService(plans, coreTenants, coreQuota, audit)

    bootstrap.RunGRPC(cfg.GRPCPort, func(s *grpc.Server) {
        tenantPlanSvc.Register(s)
        tenantSvc.Register(s)
    }, deps)
}
```

## TenantPlan State Machine

```text
draft ──→ active ──→ disabled
  │          │          │
  └─(edit)──┘          │
                (not deletable while tenants are bound)
```

`draft` → `active`: plan becomes available for tenant binding
`active` → `disabled`: plan is retired (existing bindings unaffected, new bindings blocked)
`active`/`disabled` → soft delete: only if `CountBoundTenants == 0`

## Tenant Status State Machine (`ports.TenantStatus`)

The tenant status is managed by Core API and read by Tenant Service via `TenantSvcClient`:

```text
active ──→ frozen ──→ disabled
  │          │
  └──(edit)──┘
```

| Status | Semantics | Accepts Plan Binding? |
|--------|-----------|-----------------------|
| `active` | Normal operation | Yes |
| `frozen` | Suspended (read-only) | No → `422 TENANT_STATE_INVALID` |
| `disabled` | Terminated | No → `422 TENANT_STATE_INVALID` |

### Tenant Struct

```go
type Tenant struct {
    ID           uuid.UUID
    Name         string
    DisplayName  string
    ContactEmail string
    Status       TenantStatus     // active | frozen | disabled
    PlanID       uuid.UUID
    FrozenAt     *time.Time
    DisabledAt   *time.Time
    CreatedAt    time.Time
    UpdatedAt    time.Time
}
```

**Source**: `repo/services/tenant-service/internal/repo/ports/tenant_store.go`

## TenantPlan Store (`ports.TenantPlanStore`)

| Method | Description |
|--------|-------------|
| `Create(ctx, plan) -> TenantPlan` | Insert new plan (default state: draft) |
| `GetByID(ctx, id) -> TenantPlan` | Read plan by ID (includes soft-deleted) |
| `List(ctx, filter) -> []TenantPlan, total, nextCursor` | List plans (filter by status, name search) |
| `Update(ctx, plan) -> TenantPlan` | Update plan fields (non-destructive) |
| `UpdateStatus(ctx, id, status) -> TenantPlan` | Transition state (validates allowed transitions) |
| `SoftDelete(ctx, id) -> error` | Soft delete (sets is_deleted, deleted_at) |

## BindPlanQuota Flow (2PC)

```text
1. Validate: tenant_id + plan_id → parse + check plan is active
2. Update plan_id on tenant via CoreTenantAPI (UpdateTenantPlan)
3. Sync quota:
   - Read plan's quota limits
   - Read tenant's current quota (used + reserved)
   - Upsert: total = max(plan_limit, used + reserved) [never clamp below current consumption]
   - Call CorePutQuota for each dimension
4. On success: commit plan_id change, write audit log
5. On quota sync failure: rollback plan_id to previous value, write audit log with error
```

## Adapter Layer (Services → Core API)

### `QuotaSvcClient` (`internal/repo/adapters/core/quota_svc_client.go`)

Wraps Core Go SDK (`anisdk.Client`) to call Core Quota Admin API:

| Method | Core API Call |
|--------|--------------|
| `ListQuotaMeta(ctx)` | `GET /admin/quota-meta` |
| `GetQuota(ctx, tenantID)` | `GET /admin/tenants/{tenant_id}/quota` |
| `PutQuota(ctx, tenantID, items)` | `PUT /admin/tenants/{tenant_id}/quota` |

### `TenantSvcClient` (`internal/repo/adapters/core/tenant_svc_client.go`)

Wraps Core Go SDK to call Core Tenant API:

| Method | Core API Call |
|--------|--------------|
| `GetTenant(ctx, tenantID)` | `GET /admin/tenants/{tenant_id}` |
| `UpdateTenantPlan(ctx, tenantID, planID)` | `PATCH /admin/tenants/{tenant_id}` (plan_id field) |
| `CountBoundTenants(ctx, planIDs)` | `POST /admin/tenants/count-by-plan` |
| `ListBoundTenants(ctx, planID)` | `GET /admin/tenants?plan_id={planID}` |
| `ListBindableTenants(ctx, planID)` | `GET /admin/tenants/bindable?plan_id={planID}` |

## Postgres Adapters

### `PostgresTenantPlanStore` (`internal/repo/adapters/postgres/tenant_plan_store.go`)

PostgreSQL-backed implementation of `ports.TenantPlanStore`. Table: `tenant_plans` with columns: id, code, name, description, status, quota_limits (JSONB), metadata (JSONB), is_deleted, created_at, updated_at, deleted_at.

### `PostgresTenantPlanAuditStore` (`internal/repo/adapters/postgres/tenant_plan_audit_store.go`)

Audit logging for plan operations. Table: `tenant_plan_audit_log` with columns: id, tenant_id, action, entity_type, entity_id, before (JSONB), after (JSONB), performed_by, performed_at, result (success/failure), error_message.

## Tests

- `service/tenant_plan_test.go` — Plan CRUD, status transitions, binding validation. **Includes rollback test**: validates that when the Core Quota API call fails during `BindPlanQuota` 2PC, the `plan_id` is rolled back to its previous value and failure is written to the audit log.
- `core/quota_svc_client_test.go` — Core Quota API client integration
- `adapters/postgres/*_test.go` — Postgres store tests

## References

- [Architecture Baselines](../ci-cd/architecture-baselines.md) — Services bootstrap migration plan
- [Quota & Tenancy](../core/quota-tenancy.md) — Core quota API and reservation protocol
- Source: `services/tenant-service/`, `services/pkg/bootstrap/`