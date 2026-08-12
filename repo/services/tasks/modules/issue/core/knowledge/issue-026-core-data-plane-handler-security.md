# Core 数据面 Gateway handler + 安全加固（data_query / data_table）

## Document Links
- PRD: N/A — 由设计文档驱动
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/design-kb-persistence-to-core-datapipe.md` (§3.2, §3.3)

## Description
作为平台开发者，我需要实现数据面 Gateway handler（`data_query` / `data_table`）并完成安全加固，确保通用 SQL 执行面安全可控。

## Scope
- Product line: core
- Code paths allowed: `repo/services/ani-gateway/` only

## Acceptance Criteria
- [ ] [SPEC] `POST /data/query` handler 调 `SQLDataPlane.QueryTx`；`POST /data/tables` handler 调 `CreateTable`（SPEC §3.2）
- [ ] handler 从 middleware 获取租户上下文，不信任请求体租户参数（SPEC §3.2）
- [ ] [SPEC] 安全加固：参数化唯一（禁止 params 拼接进 SQL）（SPEC §3.3-1）
- [ ] [SPEC] 目标表白名单校验（knowledge_bases/kb_documents/kb_chunks/kb_messages/kb_sessions/async_tasks/outbox_events...）（SPEC §3.3-2）
- [ ] [SPEC] 禁止破坏性语句（DROP/TRUNCATE/ALTER SYSTEM/COPY/pg_read_file），422 拒绝（SPEC §3.3-4）
- [ ] [SPEC] 按 service identity 限流 + SQL 长度/语句数/耗时上限（SPEC §3.3-5）
- [ ] [SPEC] 完整审计：service、tenant、表、语句哈希、耗时（SPEC §3.3-6）
- [ ] [SPEC] 仅对 Services service identity 开放，不对租户最终用户开放（SPEC §3.3-7）
- [ ] `make validate-services`、`make validate-architecture` 通过

## Dependencies
#25 (port/adapter)

## Type
core

## Priority
high

## Labels
core

## Batch
TBD（Phase A）

## References
- SPEC: design-kb-persistence-to-core-datapipe §3.2, §3.3, §5 (Phase A)
