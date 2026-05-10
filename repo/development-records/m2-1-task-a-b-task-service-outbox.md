# M2.1-TASK-A + M2.1-TASK-B Development Record

Legacy name:
- `Stage 3A + 3B`

Naming clarification:
- This record belongs to `ANI-06 / 模块 2 / 2.1 Gateway 骨架 / NATS JetStream 异步任务框架`.
- It does not mean `ANI-06 / 模块 3：模型管理平台`.
- Use `M2.1-TASK-A/B/C` for future references.

Date: 2026-05-11

Version impact:
- Current line: `v0.x` development.
- Release impact: `no-release-impact`.
- No formal tag was created.

Scope accepted by product owner:
- `M2.1-TASK-A`: implement the minimal `task-service` query interface.
- `M2.1-TASK-B`: implement the outbox publisher loop.

Design sources:
- `ANI-11-代码实现规范.md`
- `ANI-09-数据模型设计.md`
- `ANI-06-开发计划.md`
- `api/proto/task/v1/task_service.proto`

## Implemented

M2.1-TASK-A:
- Added `services/task-service`.
- Added environment-based config loading.
- Added gRPC registration through shared `bootstrap.RunGRPC`.
- Implemented `TaskService.GetTask`.
- `GetTask` sets tenant context before reading `async_tasks`, so PostgreSQL RLS remains active.
- Worker mutation RPCs remain explicitly `Unimplemented` because current proto requests do not carry tenant context for safe RLS-backed writes.

M2.1-TASK-B:
- Added shared `pkg/repo.OutboxRepo`.
- Implemented `FetchUnpublished` using `FOR UPDATE SKIP LOCKED`.
- Implemented `MarkPublished`.
- Added `task-service/internal/worker.OutboxPublisher`.
- Publisher publishes to NATS JetStream first, then marks rows as published in the same DB transaction.
- If NATS publish fails, the transaction rolls back and the event remains unpublished.

## Files Added

- `pkg/repo/outbox_repo.go`
- `services/task-service/go.mod`
- `services/task-service/main.go`
- `services/task-service/internal/config/config.go`
- `services/task-service/internal/service/task_service.go`
- `services/task-service/internal/worker/outbox_publisher.go`
- `development-records/m2-1-task-a-b-task-service-outbox.md`

## Files Updated

- `go.work`
- `Makefile`

## Validation

Required commands:

```bash
make gen-proto
make test
make build
```

Result:
- `make gen-proto`: passed.
- `make test`: passed.
- `make build`: passed.

Generated binaries:
- `bin/ani-gateway`
- `bin/model-service`
- `bin/task-service`

## Deferred

- `CancelTask` state transition.
- Worker mutation RPCs: `AcquireTaskLease`, `HeartbeatTaskLease`, `UpdateTaskProgress`, `FailTask`, `CompleteTask`.
- These need a tenant-aware internal authentication design or proto changes before implementation.
