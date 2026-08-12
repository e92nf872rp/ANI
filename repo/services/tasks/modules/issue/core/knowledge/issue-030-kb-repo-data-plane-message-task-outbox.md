# kb-service repository 改走数据面（message/session + async_task/outbox，跨表原子折叠）

## Document Links
- PRD: N/A — 由设计文档驱动
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/design-kb-persistence-to-core-datapipe.md` (§4.2)

## Description
作为平台开发者，我需要把 kb-service 的 `message`（kb_messages/kb_sessions）与 `async_task`/`outbox` 改为经 `CoreClient.data_query` 调用数据面，并将原有跨表原子事务折叠为单次数据面多语句调用，保留 US-010 与 Query 语义。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/app/repositories/message.py`, `async_task.py`, `outbox.py`, `rls.py` only

## Acceptance Criteria
- [ ] [SPEC] `NotifyDocumentUploaded` 原 3 表同事务（kb_documents + async_tasks + outbox_events）折叠为单次 `data_query` 多语句（SPEC §4.2 跨表原子折叠）
- [ ] [SPEC] `Query` 用户消息分支原 2 表同事务（kb_sessions + kb_messages）折叠为单次 `data_query` 多语句（SPEC §4.2）
- [ ] [SPEC] `outbox.list_undispatched` / `mark_dispatched` 用 `role="service"` 跨租户调用（SPEC §4.2）
- [ ] `async_tasks` 幂等键 UNIQUE 约束不变量在数据面单事务内保持（SPEC §8）
- [ ] 保留 ON CONFLICT / RETURNING / 幂等重放语义
- [ ] `pytest` 通过（test_us010_wiring / test_message_repository / test_outbox_dispatcher 改数据面 mock）

## Dependencies
#28 (CoreClient 扩展)

## Type
core (services)

## Priority
high

## Labels
core, services

## Batch
TBD（Phase B）

## References
- SPEC: design-kb-persistence-to-core-datapipe §4.2, §6, §8
