---
type: concept
title: Quota & Tenancy Management
description: "Resource quota management (try/confirm/cancel/release reservation protocol), tenant plans, RLS-based multi-tenant isolation, Core Quota API (admin endpoints)"
tags: [core, quota, tenancy, tenant-plans, rls, reservation]
---

# Quota & Tenancy Management

## Quota Management (`ports.QuotaAdminService`)

### Resource Types

| Resource Type | Key | Unit | Discrete |
|--------------|-----|------|----------|
| GPU Count | `gpu_count` | count | yes |
| CPU Core | `cpu_core` | count (float) | no |
| Memory | `memory_gb` | GB | no |
| Storage | `storage_gb` | GB | no |
| Token Count | `token_count` | count | yes |
| KB Query Count | `kb_query_count` | count | yes |
| Member Count | `member_count` | count | yes |
| Inference Service Count | `inference_service_count` | count | yes |

### Operations

| Method | Endpoint | Description |
|--------|----------|-------------|
| `ListQuotaMeta` | `GET /admin/quota-meta` | List all enabled quota dimensions with defaults |
| `GetQuota` | `GET /admin/tenants/{tenant_id}/quota` | Get current quota for tenant |
| `PutQuota` | `PUT /admin/tenants/{tenant_id}/quota` | Replace all quota dimensions |
| `UpsertQuota` | `PUT /admin/tenants/{tenant_id}/quota/upsert` | Partial upsert of quota dimensions |
| `DeleteQuota` | `DELETE /admin/tenants/{tenant_id}/quota` | Delete tenant's quota (reset to defaults) |

### Reservation Protocol

```text
Try(request) → (Reservation, error)
    │
    ├── Confirm(reservation.TxID) → (confirmed, error)
    │     Marks the reservation as committed; deducts from available.
    │
    ├── Cancel(reservation.TxID) → (cancelled, error)
    │     Releases the reservation without committing.
    │
    └── Release(reservation.TxID) → (released, error)
          Releases a committed reservation (e.g., instance deleted).
```

Reservations prevent overcommit: `Total - Used - Reserved >= Requested` before a `Try` succeeds. If not enough capacity, the reservation is denied.

### Check Constraints

The Core DB enforces `resource_quota CHECK ((total).int8min >= 0)` to prevent negative totals. The reservation protocol ensures `total >= used + reserved` before confirming a reservation.

## Tenant Service (`ports.TenantService`)

| Method | Description |
|--------|-------------|
| `GetTenant(ctx, tenantID) -> Tenant` | Read tenant (id, name, display_name, status, plan_id) |
| `UpdateTenantPlan(ctx, tenantID, planID) -> Tenant` | Update tenant's plan assignment |
| `CountBoundTenants(ctx, planIDs) -> map[string]int64` | Count tenants per plan (for plan deletion guard) |
| `ListBoundTenants(ctx, planID) -> []TenantSummary` | List tenants currently on a plan |
| `ListBindableTenants(ctx, planID) -> []TenantSummary` | List tenants that can be moved to a plan (excludes already on this plan) |

## Tenant Plan Management (Core Services)

See [Tenant Service](../core-services/tenant-service.md) for the gRPC tenant-plan service that wraps `QuotaAdminService` and `TenantService` for plan → quota binding.

## RLS (Row-Level Security)

Every tenant-scoped table has an RLS policy:

```sql
CREATE POLICY tenant_isolation ON <table>
    USING (tenant_id = current_setting('ani.tenant_id')::uuid);
```

The `ani_app` DB role has RLS enabled. The `ani_outbox_publisher` role has `BYPASSRLS` for cross-tenant outbox reads. Application code sets `ani.tenant_id` via `types.WithTenant(ctx, tenantContext)` → `SetDBTenant(ctx, tenantID)` before any DB operation.

## References

- [Database Migrations](../deployment/database-migrations.md) — Quota and tenant plan tables
- [Tenant Service](../core-services/tenant-service.md) — gRPC service for plan CRUD + quota binding
- [Shared Repositories](shared-repos.md) — Tenant context types
- Source: `repo/pkg/ports/quota.go`, `repo/pkg/ports/tenant.go`, `repo/pkg/types/tenant.go`, `repo/pkg/adapters/runtime/postgres_quota.go`