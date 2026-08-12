# 迁移验证收口（语义等价对比 + 真实 PG 端到端 + 回归）

## Document Links
- PRD: N/A — 由设计文档驱动
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/design-kb-persistence-to-core-datapipe.md` (§6, §5 Phase C, §9)

## Description
作为平台开发者，我需要完成数据面迁移的验证收口：确认迁移前后语义等价（US-010 原子事务、Query 持久化、pg_trgm 检索、RLS 隔离），并通过全部回归门禁。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/`, `repo/api/openapi/v1.yaml` (验证), `repo/scripts/` (验证) only

## Acceptance Criteria
- [ ] [SPEC] 语义等价对比：同一输入下迁移前后落库结果与响应一致（US-010 原子 outbox、Query 会话持久化）（SPEC §6-3）
- [ ] [SPEC] 真实 PG 端到端：Core 数据面 + kb-service；验证 RLS 生效（跨租户读不到）、pg_trgm 检索正常（SPEC §6-4）
- [ ] Core 数据面安全用例（注入、越权表、DDL 拒绝、param 拼接拒绝）通过（SPEC §6-1）
- [ ] kb-service 无 `import asyncpg`、无 `rls.py`、无 `migrations/`；7 个 repository 全走数据面（SPEC §9-3）
- [ ] `make validate-services`、`make validate-architecture`、`git diff --check` 全绿（SPEC §9）
- [ ] Core 共同评审（CODEOWNERS @e92nf872rp）approval 记录在案（SPEC §9-6）

## Dependencies
#31 (装配简化)；且 Phase A 全部合入

## Type
core (services, verification)

## Priority
high

## Labels
core, services

## Batch
TBD（Phase C）

## References
- SPEC: design-kb-persistence-to-core-datapipe §5 (Phase C), §6, §9
