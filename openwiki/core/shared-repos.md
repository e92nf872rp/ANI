---
type: concept
title: Core Shared Repositories
description: "pkg/repo (async task + outbox repos), pkg/nats (canonical NATS subjects and payloads), pkg/types (tenant context, pagination, error types) — shared abstractions used across Core microservices"
tags: [core, repo, nats, types, async-tasks, outbox]
---

# Core Shared Repositories

## pkg/repo/

### AsyncTaskRepo

PostgreSQL-backed async task repository used by `task-service` and referenced by any component that creates or manages async operations. Defines `AsyncTaskRepo` interface:

- `Create(ctx, tx, req)` — Insert task + outbox event in same PG transaction
- `GetByID(ctx, pool, id)` — Read task by UUID
- `GetByIdempotencyKey(ctx, pool, tenantID, key)` — Idempotency key lookup
- `AcquireLease(ctx, pool, taskID, workerID, duration)` — Lease acquisition for worker assignment
- `Heartbeat(ctx, pool, taskID, workerID, duration)` — Extend lease
- `UpdateProgress(ctx, pool, taskID, workerID, pct)` — Progress reporting (0-100)
- `Complete(ctx, tx, taskID, workerID, result)` — Mark succeeded with result JSON
- `Fail(ctx, tx, taskID, workerID, errMsg, compensatingAction)` — Mark failed
- `GetExpiredLeases(ctx, pool, limit)` — Find expired leases for re-enqueue

Implementation: `PostgresAsyncTaskRepo` (stateless struct, operates on *pgxpool.Pool).

### OutboxRepo

`OutboxRepo` stores events in `outbox_events` table with `picked_at` / `published_at` columns. Published by `OutboxPublisher` worker running in `task-service`:

- `Insert(ctx, tx, event)` — Insert outbox record in same transaction as domain operation
- `PickEvents(ctx, pool, batchSize)` — Select unpicked events for publishing
- `MarkPublished(ctx, tx, id)` — Mark as published
- `DeletePublished(ctx, pool, olderThan)` — Cleanup old published records

## pkg/nats/

Canonical NATS subject definitions and payload structs. ALL publishers and consumers must use these types — no individual service should define its own task payload structs.

### Subjects

| Subject | Direction | Purpose |
|---------|-----------|---------|
| `ani.tasks.inference.deploy` | Task → Operator | Deploy inference service request |
| `ani.tasks.inference.delete` | Task → Operator | Delete inference service request |
| `ani.tasks.kb.parse` | Task → Doc Parser | Parse KB document |
| `ani.tasks.kb.index` | Task → RAG Engine | Index KB chunks to vector store |
| `ani.tasks.model.import` | Task → Model Import Worker | Import model from external registry |
| `ani.events.task.completed` | Task → Any subscriber | Task completion notification (`.{task_id}` suffix) |
| `ani.events.instance.*` | Core → Subscribers | Instance lifecycle events (used by metering) |

### Payload Types

- `InferenceDeployMsg` — TaskID, IdempotencyKey, TenantID, ServiceID, ModelVersionID, IsEncrypted, EncryptAlgo, GPUType, GPUCount, Replicas, MaxConcurrency, DrainSeconds
- `InferenceDeleteMsg` — TaskID, TenantID, ServiceID, DrainSeconds
- `KBParseMsg` — TaskID, IdempotencyKey, TenantID, KBID, DocID, StoragePath, FileType, FileSizeBytes, ChecksumSHA256
- `ParsedChunk` — Index, Content, PageNumber

## pkg/types/

Common value types shared across Core and Services:

- `types.TenantContext` — TenantID embedded in context via `WithTenant(ctx, tenantID)` / `FromContext(ctx)`
- `types.Pagination` — Cursor-based pagination: `Limit`, `Cursor`, `NextCursor`
- Common error types — `ErrBadRequest`, `ErrNotFound`, `ErrConflict`, etc.

## References

- [Task Service](../core-services/task-service.md)
- [Async Tasks](async-tasks.md)
- Source: `repo/pkg/repo/`, `repo/pkg/nats/`, `repo/pkg/types/`