# ANI Development Records

This directory records implementation progress that maps AI-generated code stages back to the product development plan.

## Current Position

As of 2026-05-11, development is in:

- Product plan file: `ANI-06-开发计划.md`
- Product plan section: `模块 2：ANI Gateway（统一 Web Server 层）（M1-M2）`
- Product plan item: `2.1 Gateway 骨架 / NATS JetStream 异步任务框架`
- Implementation status: `M2.1-TASK-A + M2.1-TASK-B` completed; `M2.1-TASK-C` is next.

Important naming note:
- This is not `模块 3：模型管理平台`.
- Earlier records used `Stage 3A/3B/3C` as internal code-generation slice names.
- To avoid confusion with the product plan modules, use `M2.1-TASK-A/B/C` going forward.

## Stage Mapping

| Implementation Slice | Legacy Name | Product Plan Mapping | Status | Record |
|---|---|---|---|---|
| M2.1-TASK-A | Stage 3A | `ANI-06` / `2.1 Gateway 骨架` / `NATS JetStream 异步任务框架` / task query path | Completed | `m2-1-task-a-b-task-service-outbox.md` |
| M2.1-TASK-B | Stage 3B | `ANI-06` / `2.1 Gateway 骨架` / `NATS JetStream 异步任务框架` / transactional outbox + NATS publisher | Completed | `m2-1-task-a-b-task-service-outbox.md` |
| M2.1-TASK-C | Stage 3C | `ANI-06` / `2.1 Gateway 骨架` / `NATS JetStream 异步任务框架` / worker mutation RPCs with tenant-safe writes | Pending | To be created: `m2-1-task-c-worker-mutations.md` |

## Read Order For Continuation

Before continuing code generation, read these files in order:

1. `ANI-06-开发计划.md`
2. `ANI-08-安全架构设计.md`
3. `ANI-09-数据模型设计.md`
4. `ANI-11-代码实现规范.md`
5. `ANI-12-版本管理策略.md`
6. `repo/development-records/m2-1-task-a-b-task-service-outbox.md`
7. `repo/development-records/2026-05-11-handoff-codex-cloud.md`

## Version Position

- Current release line: `v0.x` development.
- First formal release target: `v1.0.0` on 2026-09-30.
- Versioning policy: `ANI-12-版本管理策略.md`.
- Current `M2.1-TASK-C` work should be recorded as `no-release-impact` unless it changes API/Proto/DB compatibility; if it changes `task_service.proto`, classify the impact in the stage record.

## Next Work Boundary

Next implementation should be `M2.1-TASK-C` only:

- Fix `repo/api/proto/task/v1/task_service.proto` so worker mutation RPCs have tenant/security context.
- Regenerate protobuf code.
- Implement tenant-safe task worker mutation RPCs.
- Preserve PostgreSQL RLS and transactional outbox guarantees.
- Run `make gen-proto`, `make test`, and `make build`.
- Add `repo/development-records/m2-1-task-c-worker-mutations.md`.
