---
type: service
title: Task Service (gRPC)
description: "Standalone gRPC service for async task CRUD: GetTask, CancelTask (unimplemented), UpdateTaskProgress. PostgreSQL-backed AsyncTaskRepo, outbox publisher, NATS JetStream integration."
tags: [core-services, task, async, outbox, nats]
---

# Task Service (gRPC)

## Overview

**Service**: `services/task-service/` · **Bootstrap**: uses Core `pkg/bootstrap/` (accepted_baseline)

Standalone gRPC service managing async task state:
- **GetTask** — Read task by ID
- **CancelTask** — Unimplemented (requires explicit cancellation state machine design)
- **UpdateTaskProgress** — Worker mutation: progress percentage, heartbeat extension
- **Outbox Publishing** — Background worker that polls `outbox_events` PG table and publishes to NATS JetStream

## Implementation

`TaskService` struct (gRPC server):

| Method | Implementation |
|--------|---------------|
| `GetTask(ctx, req) -> AsyncTask` | Parse tenant+task IDs, tenant context into ctx, call `repo.GetByID(pool, taskID)` |
| `CancelTask(ctx, req) -> Empty` | Returns `codes.Unimplemented` with message "task cancellation requires explicit cancel state transition design" |
| `UpdateTaskProgress(ctx, req) -> Empty` | Parse tenant+task+worker IDs, call `repo.UpdateProgress(pool, taskID, workerID, progress)` |

### AsyncTaskRepo (Postgres)

Implemented in `pkg/repo/task_repo.go`:

| Method | SQL Pattern |
|--------|-------------|
| `Create(ctx, tx, req) -> *AsyncTask` | INSERT INTO async_tasks + INSERT INTO outbox_events (same tx) |
| `GetByID(ctx, pool, id) -> *AsyncTask` | SELECT ... WHERE id = $1 |
| `GetByIdempotencyKey(ctx, pool, tenantID, key) -> *AsyncTask` | SELECT ... WHERE tenant_id = $1 AND idempotency_key = $2 |
| `AcquireLease(ctx, pool, taskID, workerID, duration) -> (bool, time, error)` | UPDATE ... SET leased_by=$2, lease_expires_at=NOW()+$4 WHERE id=$1 AND (lease_expires_at IS NULL OR lease_expires_at < NOW()) |
| `UpdateProgress(ctx, pool, taskID, workerID, pct) -> error` | UPDATE async_tasks SET progress_pct=$3 WHERE id=$1 AND leased_by=$2 |
| `Complete(ctx, tx, taskID, workerID, result) -> error` | UPDATE ... SET status=succeeded, result=$4, completed_at=NOW() WHERE id=$1 AND leased_by=$2 |
| `Fail(ctx, tx, taskID, workerID, errMsg, compAction) -> error` | UPDATE ... SET status=failed, error_message=$4, compensating_action=$5 WHERE id=$1 AND leased_by=$2 |
| `GetExpiredLeases(ctx, pool, limit) -> []*AsyncTask` | SELECT ... WHERE lease_expires_at < NOW() AND status = 'in_progress' LIMIT $1 |

### OutboxPublisher (Worker)

Background worker (`worker/outbox_publisher.go`):

1. Polls `outbox_events` with `SELECT ... FOR UPDATE SKIP LOCKED`
2. For each unpicked row, publishes the payload to the specified NATS subject
3. On success: `UPDATE outbox_events SET picked_at = NOW(), published_at = NOW()`
4. On failure: `UPDATE outbox_events SET picked_at = NOW(), attempts = attempts + 1`
5. After `max_attempts` exceeded: row remains for dead letter inspection

## References

- [Async Tasks](../core/async-tasks.md) — Task lifecycle and outbox pattern
- [Shared Repositories](../core/shared-repos.md) — AsyncTaskRepo and OutboxRepo
- [Metering Service](metering-service.md) — Lifecycle event consumer pattern
- Source: `services/task-service/`, `pkg/repo/task_repo.go`, `pkg/repo/outbox_repo.go`