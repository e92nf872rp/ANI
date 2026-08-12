# kb-service repository 改走数据面（knowledge_base / document / chunk）

## Document Links
- PRD: N/A — 由设计文档驱动
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/design-kb-persistence-to-core-datapipe.md` (§4.2)

## Description
作为平台开发者，我需要把 kb-service 的 `knowledge_base`/`document`/`chunk` 三个 repository 从 asyncpg 直连接收改为经 `CoreClient.data_query` 调用数据面，并保留 RLS 与 pg_trgm 语义。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/app/repositories/knowledge_base.py`, `document.py`, `chunk.py`, `rls.py` only

## Acceptance Criteria
- [ ] [SPEC] `knowledge_base.create_kb` → `data_query("INSERT … RETURNING …")` 单事务（SPEC §4.2）
- [ ] [SPEC] `document.create_document` → `data_query` 单事务（SPEC §4.2）
- [ ] [SPEC] `chunk.keyword_search` → `data_query("SELECT … similarity(content,$1) …")`，保留 pg_trgm 与 GIN 索引语义（SPEC §4.2）
- [ ] 租户上下文由 `role="tenant"` + X-Tenant-Id 在 Core 侧注入，`rls.py` 不再被这三个 repo 使用（SPEC §4.2）
- [ ] 签名/返回结构与现语义一致（list/count/soft_delete 等）
- [ ] `pytest` 通过（现有 repository 测试改为对数据面 mock 断言）

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
- SPEC: design-kb-persistence-to-core-datapipe §4.2, §6
