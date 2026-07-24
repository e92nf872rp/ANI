# 数据库迁移 kb_chunks + pg_trgm

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-005)
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-service.md` (§3.1, §3.4)

## Description
作为平台开发者，我需要新增 kb_chunks 表与 pg_trgm 扩展以支撑关键词检索与父子分块存储。

## Scope
- Product line: core (DB migration)
- Code paths allowed: `repo/services/kb-service/migrations/` only

## Acceptance Criteria
- [ ] 新增 `kb_chunks` 表迁移脚本，字段与 plan.md §3.1 / SPEC §3.1 一致（含 `parent_chunk_id`、`chunk_type`、`parent_content`、`custom_metadata`）
- [ ] 新增 `CREATE EXTENSION IF NOT EXISTS pg_trgm` 迁移
- [ ] 新增 `idx_kb_chunks_content_trgm` GIN 索引
- [ ] 新增 `idx_kb_chunks_kb_doc`、`idx_kb_chunks_parent`、`idx_kb_chunks_type` 索引
- [ ] [SPEC] 迁移脚本可重复执行（idempotent，全部用 `CREATE ... IF NOT EXISTS`，SPEC §3.4）
- [ ] `make test` 通过

## Dependencies
None — DB migration, parallel with contract fixes.

## Type
core (migration)

## Priority
high

## Labels
core, migration

## Batch
M2.1-TASK-A

## References
- SPEC: spec-services-kb-service §3.1, §3.4
