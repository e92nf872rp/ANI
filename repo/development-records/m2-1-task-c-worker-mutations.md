# M2.1-TASK-C Development Record

Date: 2026-05-11

Product plan mapping:
- `ANI-06 / 模块 2 / 2.1 Gateway 骨架 / NATS JetStream 异步任务框架`
- Slice: `M2.1-TASK-C`
- Scope: worker mutation RPCs with tenant-safe writes.

Version impact:
- Current line: `v0.x` development.
- Release impact: `MINOR`.
- Reason: `task_service.proto` adds tenant and worker security context fields to worker mutation requests and adds `AsyncTask.lease_owner`; the schema gains `async_tasks.lease_owner`.
- Compatibility note: existing Protobuf field numbers were preserved; new fields were appended.
- No formal version tag was created.

## Implemented

- Added explicit `tenant_id` and `worker_id` to worker mutation RPC requests:
  - `AcquireTaskLease`
  - `HeartbeatTaskLease`
  - `UpdateTaskProgress`
  - `FailTask`
  - `CompleteTask`
- Added `lease_seconds` to `HeartbeatTaskLeaseRequest` so heartbeats renew leases instead of only updating heartbeat time.
- Added `async_tasks.lease_owner` and a tenant-scoped running-task lease owner index.
- Updated `pkg/repo.AsyncTaskRepo` and the PostgreSQL implementation to enforce:
  - PostgreSQL RLS via tenant context before every task write.
  - Lease acquisition only for pending/failed/expired running tasks.
  - Heartbeat, progress, completion, and failure only by the current `lease_owner` while the lease is still valid.
- Implemented task-service worker mutation RPCs.
- Kept `CompleteTask` and `FailTask` transactional with outbox event insertion in the same DB transaction.
- Added focused service tests for worker mutation validation and lease conflict error mapping.
- Regenerated protobuf Go code from `api/proto/task/v1/task_service.proto`.

## Files Changed

- `api/proto/task/v1/task_service.proto`
- `pkg/generated/pb/task/v1/task_service.pb.go`
- `pkg/repo/task_repo.go`
- `services/task-service/internal/service/task_service.go`
- `services/task-service/internal/service/task_service_test.go`
- `deploy/migrations/20260501000100_init_schema.sql`
- `Makefile`
- `../ANI-11-代码实现规范.md`
- `development-records/README.md`

## Validation

Commands run:

```bash
cd repo
make gen-proto
make test
make build
```

Result:
- `make gen-proto`: passed.
- `make test`: passed.
- `make build`: passed.

Note:
- `Makefile` now reuses existing `.bin/protoc-gen-*` tools and only downloads missing generators, so protobuf generation is reproducible in restricted-network environments.

## Remaining Risks

- `CancelTask` remains unimplemented pending explicit cancellation transition design.
- Worker authentication is represented in the RPC contract as `worker_id`; the future internal auth layer still needs to bind this value to mTLS or ServiceAccount identity so callers cannot spoof a worker id.
- The current schema file is the initial migration. If this has already been applied in an environment, a follow-up additive migration is required to add `async_tasks.lease_owner` online.

## Next Boundary

This slice is validation-complete. The next implementation slice may move to `ANI-06 / 模块 2 / 2.2 认证授权`.
