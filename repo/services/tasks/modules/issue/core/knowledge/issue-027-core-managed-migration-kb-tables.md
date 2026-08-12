# Core 受管迁移编排（迁移 kb-service 7 表 DDL 到 Core）

## Document Links
- PRD: N/A — 由设计文档驱动
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/design-kb-persistence-to-core-datapipe.md` (§3.2 迁移编排, §4.4)

## Description
作为平台开发者，我需要把 kb-service 私有的建表迁移（`001_pg_trgm_extension`、`002_kb_chunks`、`003_kb_retrieval_mode`）收口为 Core 受管迁移，使 7 张表的 schema 由 Core 统一管控。

## Scope
- Product line: core
- Code paths allowed: `repo/deploy/migrations/`, `repo/services/kb-service/migrations/` only

## Acceptance Criteria
- [ ] [SPEC] pg_trgm 扩展迁移纳入 Core 受管迁移（SPEC §3.2 迁移编排）
- [ ] [SPEC] kb_chunks / kb_retrieval_mode 等 7 表 DDL 纳入 Core 受管迁移（SPEC §4.4）
- [ ] 迁移走 `POST /data/tables`（受管 DDL）+ Core 迁移编排，记录审计（SPEC §3.2）
- [ ] kb-service 启动不再自助建表；`migrations/` 目录在 kb-service 侧移除（SPEC §4.4）
- [ ] 迁移可重复执行/幂等；`make validate-services` 通过

## Dependencies
#24 (数据面契约)

## Type
core

## Priority
high

## Labels
core

## Batch
TBD（Phase A）

## References
- SPEC: design-kb-persistence-to-core-datapipe §3.2, §4.4, §5 (Phase A)
