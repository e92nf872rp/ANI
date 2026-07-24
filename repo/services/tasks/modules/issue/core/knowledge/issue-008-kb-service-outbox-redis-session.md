# kb-service outbox 派发与 Redis 会话缓存

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-010)
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-service.md` (§2, §6)

## Description
作为平台开发者，我需要实现 Python 侧 outbox 派发与 Redis 会话缓存。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/app/outbox/`, `app/session/` only

## Acceptance Criteria
- [ ] `NotifyDocumentUploaded` 同事务写 `kb_documents` + `async_tasks` + `outbox_events`
- [ ] [SPEC] outbox 派发器轮询发布到 NATS `ani.tasks.kb.parse`（独立协程，100/批，SPEC §6.1 dispatcher 算法）
- [ ] [SPEC] Query RPC 写 `kb_messages` + Redis 会话缓存（key `ani:prod:session:kb:{session_id}`，TTL 24h，LTRIM 20，SPEC §6.1 Query 算法）
- [ ] `make test` 通过

## Dependencies
#7 (repositories + clients) — per SPEC §11.2 (US-010 depends on US-009).

## Type
core (services)

## Priority
high

## Labels
core, services

## Batch
M2.1-TASK-B

## References
- SPEC: spec-services-kb-service §2.2, §6.1 (dispatcher, Query)
