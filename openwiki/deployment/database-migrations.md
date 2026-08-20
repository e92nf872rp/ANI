---
type: reference
title: Database Migrations
description: "SQL migration system: date-prefixed naming convention, database roles (ani_app, ani_migrator, ani_outbox_publisher with BYPASSRLS), RLS policy registration, outbox table pattern, 19+ migration files catalog, Atlas execution."
tags: [deployment, migrations, database, postgresql, rls, outbox]
---

# Database Migrations

## Overview

**Directory**: `deploy/migrations/` · **Tool**: Plain SQL (Atlas compatible)

All database migrations are date-prefixed SQL files that must be executed **externally** — there is **no Go-level auto-migration** at service startup. Migrations are run manually or via CI/CD using `psql` or `atlas migrate apply`.

### No Startup Auto-Migration

Neither Core API, Gateway, nor any Services-layer service runs migrations at startup. There is no `AutoMigrate`, `runMigrations`, or equivalent call in any Go entrypoint. The Helm chart defines a `migrationJob` block (currently `.enabled: false`) that would run migrations as a Kubernetes Job hook — but it remains **disabled** in all profiles.

### Schema Model

The platform uses a **single `public` schema** with Row-Level Security (RLS) for multi-tenant isolation — not a split into `control_plane` / `data_plane` schemas. All tenant-scoped tables use RLS policies keyed on `app.current_tenant_id`.

### Down-Migrations

**Not supported.** There are no down-migration files or rollback scripts. Schema changes are additive forward-only. Rollback must be performed by restoring from backup or applying a new forward migration.

### Migration Tests

**None exist.** There are no automated tests that run SQL migrations against a database. The `tests/` directory only contains `e2e/` smoke tests. Migration correctness is validated through manual review and live-gate testing.

## Naming Convention

```
YYYYMMDD_NNN_description.sql
```

- `YYYYMMDD`: Date of migration creation
- `NNN`: Sequence number (001, 002, ...)
- `description`: snake_case description of the change

## Database Roles

| Role | Permissions | Purpose |
|------|-------------|---------|
| `ani_app` | Schema usage, DML on app tables, RLS enforcement | Application user (every service connects as this) |
| `ani_migrator` | DDL on all schemas, owns migrations | Migration execution user |
| `ani_outbox_publisher` | BYPASSRLS, read/write on outbox_events | Cross-tenant outbox polling |

## RLS (Row-Level Security)

Every tenant-scoped table has an RLS policy:

```sql
CREATE POLICY tenant_isolation ON <table>
  FOR ALL
  USING (tenant_id = current_setting('ani.tenant_id')::uuid);
```

The `ani.tenant_id` session variable is set by middleware after JWT/API Key authentication, ensuring all queries are automatically scoped to the caller's tenant.

## Outbox Pattern

The `outbox_events` table enables reliable event publishing to NATS JetStream:

```sql
CREATE TABLE outbox_events (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type    TEXT NOT NULL,
    subject       TEXT NOT NULL,
    payload       JSONB NOT NULL,
    tenant_id     UUID NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    picked_at     TIMESTAMPTZ,
    published_at  TIMESTAMPTZ
);
```

The `OutboxPublisher` polls `outbox_events WHERE picked_at IS NULL`, sets `picked_at`, publishes to NATS, then sets `published_at`.

## Migration Catalog

| # | Migration | Key Changes |
|---|-----------|-------------|
| 001 | `20260501_001_init_schema` | Full initial schema: tenants, users, roles, permissions, RLS, api_keys, jwt_blocklist, refresh_tokens, models, model_versions, knowledge_bases, kb_chunks, instances, networks, subnets, security_groups, volumes, filesystems, secrets, secret_bindings, outbox_events |
| 002 | `20260502_002_operations_idempotency` | Operations table with idempotency key, operation steps |
| 003 | `20260502_003_permissions_schema` | Extended permissions schema |
| 004 | `20260519_004_instance_vm_protection` | VM protection flags (termination_protection) |
| 005 | `20260520_005_network_resources` | Network resource tables (VPC, subnet, SG records) |
| 006 | `20260520_006_storage_resources` | Storage resource tables (volume, filesystem records) |
| 007 | `20260520_007_workload_identity_api_keys` | Workload identity API key bindings |
| 008 | `20260524_008_k8s_cluster_proxy_targets` | K8s cluster proxy target metadata |
| 009 | `20260524_001_control_plane_leases` | Control plane lease table (HA leader election) |
| 010 | `20260620_001_network_routes` | Network route tables |
| 011 | `20260620_013_k8s_cluster_proxy_target_mtls` | mTLS config for K8s cluster proxy targets |
| 012 | `20260707_014_platform_refresh_tokens` | Platform refresh token management |
| 013 | `20260707_014_platform_users` | Platform user management |
| 014 | `20260730_001_instance_management_lifecycle_ops` | Instance lifecycle operation tracking |
| 015 | `20260731_001_metering_usage` | Metering usage records table |
| 016 | `20260802_001_async_tasks` | Async task store table |
| 017 | `20260803_001_storage_control_plane_state` | Storage control plane state tracking |
| 018 | `20260810_001_resource_quota` | Resource quota tables (quota_meta, tenant_quotas, quota_reservations) |
| 019 | `20260810_002_tenant_plan_management` | Tenant plan management tables |
| 020 | `20260810_003_alter_table_structures` | Schema alterations for table structures |
| 021 | `20260811_003_tenant_quota_change` | Tenant quota change tracking |

## Execution

```bash
# Apply all pending migrations
atlas migrate apply --url "postgres://ani_migrator:password@localhost:5432/ani?sslmode=disable"

# Check migration status
atlas migrate status --url "$DATABASE_URL"
```

## References

- [Tenant Isolation](../core/auth-security.md) — RLS and multi-tenant data isolation
- [Async Tasks](../core/async-tasks.md) — Outbox publisher and async task lifecycle
- [Quota & Tenancy](../core/quota-tenancy.md) — Quota management tables
- [Helm Charts](helm-charts.md) — Deployment profiles
- [Docker Compose](docker-compose.md) — Local dev environment