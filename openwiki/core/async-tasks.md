---
type: concept
title: Async Task System
description: "Async task lifecycle, PostgreSQL-backed task store, outbox pattern, NATS JetStream messaging — the foundation for all async operations in ANI"
tags: [core, async-tasks, outbox, nats, jetstream, task-lifecycle]
---

# Async Task System

## Overview

ANI uses an **async task + outbox** pattern for all operations that involve long-running provider workflows (instance provisioning, model imports, document parsing, etc.). The task encapsulates state tracking, and the outbox ensures reliable delivery of NATS events.

## Task Lifecycle

```text
Create ──→ AcquireLease ──→ Heartbeat ──→ Complete
                                      └─→ Fail ──→ DeadLetter
```

1. **Create** — A task record is created with `IdempotencyKey`, `TaskType`, `ResourceType`, `ResourceID`, `MaxAttempts`, and optional `WebhookURL`. An outbox event is inserted in the same PG transaction.
2. **AcquireLease** — A worker acquires the task lease (PG advisory lock or lease row with expiry). Failed acquisition returns the task to the queue.
3. **Heartbeat** — The worker periodically extends the lease to indicate liveness. Expired leases are re-enqueued by a sweeper.
4. **Complete** — Task marked succeeded with result JSON. Outbox event published to NATS (e.g., `ani.events.task.completed.{task_id}`).
5. **Fail** — Task marked failed with error message and optional compensating action. After `MaxAttempts` exhausted, goes to dead letter.
6. **Dead Letter** — Failed tasks beyond max attempts. Requires manual intervention or automated compensation.

## Port Interface (`AsyncTaskStore`)

```go
type AsyncTaskStore interface {
    Create(ctx, record) -> (AsyncTaskRecord, bool, error)
    Get(ctx, tenantID, taskID) -> (AsyncTaskRecord, error)
    Update(ctx, update) -> (AsyncTaskRecord, error)
}
```

## PostgreSQL Implementation (`PostgresAsyncTaskRepo`)

The `PostgresAsyncTaskRepo` in `pkg/repo/task_repo.go` provides:

| Method | SQL | Purpose |
|--------|-----|---------|
| `Create(ctx, tx, req)` | INSERT INTO async_tasks + INSERT INTO outbox_events | Atomic task + outbox creation |
| `GetByID(ctx, pool, id)` | SELECT FROM async_tasks WHERE id = $1 | Read task state |
| `GetByIdempotencyKey(ctx, pool, tenantID, key)` | SELECT ... WHERE idempotency_key = $1 AND tenant_id = $2 | Idempotency check |
| `AcquireLease(ctx, pool, taskID, workerID, duration)` | UPDATE async_tasks SET leased_by = $1, lease_expires_at = $2 WHERE id = $3 AND (lease_expires_at IS NULL OR lease_expires_at < NOW()) | Safe lease acquisition |
| `Heartbeat(ctx, pool, taskID, workerID, duration)` | UPDATE async_tasks SET lease_expires_at = $1 WHERE id = $2 AND leased_by = $3 | Extend lease |
| `UpdateProgress(ctx, pool, taskID, workerID, pct)` | UPDATE async_tasks SET progress_pct = $1 WHERE id = $2 | Progress tracking |
| `Complete(ctx, pool, taskID, workerID, result)` | UPDATE async_tasks SET status = 'succeeded', result = $1, completed_at = NOW() | Success |
| `Fail(ctx, pool, taskID, workerID, errMsg, compAction)` | UPDATE async_tasks SET status = 'failed', error_message = $1 | Failure |
| `GetExpiredLeases(ctx, pool, limit)` | SELECT ... WHERE lease_expires_at < NOW() AND status = 'in_progress' LIMIT $1 | Lease recovery |

## Outbox Pattern

The outbox pattern ensures reliable NATS event delivery:

1. **Domain operation** writes to domain table + `outbox_events` table in the same PG transaction
2. **`OutboxPublisher`** (in task-service) periodically polls `outbox_events` for unpicked rows
3. Each unpicked row is published to its NATS subject
4. On successful publish, the row is marked `published_at = NOW()`
5. A cleanup job deletes old published rows

**`OutboxRepo`** (`pkg/repo/outbox_repo.go`):

- `Insert(ctx, tx, event)` — Insert outbox record
- `PickEvents(ctx, pool, batchSize)` — Select unpicked events (FOR UPDATE SKIP LOCKED)
- `MarkPublished(ctx, tx, id)` — Mark as published

## Canonical NATS Subjects

See [Shared Repositories](shared-repos.md) for the complete subject catalog and payload types.

## References

- [Shared Repositories](shared-repos.md) — pkg/repo, pkg/nats, pkg/types
- [Task Service](../core-services/task-service.md) — Task CRUD gRPC service
- [Instances](instances.md) — Async instance provisioning uses this system
- Source: `repo/pkg/repo/task_repo.go`, `repo/pkg/repo/outbox_repo.go`, `repo/pkg/nats/messages.go`