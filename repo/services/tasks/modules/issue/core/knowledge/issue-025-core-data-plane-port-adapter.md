# Core 数据面 port/adapter 实现（SQLDataPlane）

## Document Links
- PRD: N/A — 由设计文档驱动
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/design-kb-persistence-to-core-datapipe.md` (§3.2)

## Description
作为平台开发者，我需要实现 Core 数据面能力抽象与 PostgreSQL 适配器，为 `data_query`/`data_table` handler 提供底层能力，复用 Core PG 连接池（`pkg/bootstrap` DB）。

## Scope
- Product line: core
- Code paths allowed: `repo/pkg/ports/`, `repo/pkg/adapters/` only

## Acceptance Criteria
- [ ] [SPEC] 新增 port 能力抽象（如 `SQLDataPlane` 接口：`QueryTx`、`CreateTable`）（SPEC §3.2）
- [ ] adapter 对接 Core PG 连接池；`QueryTx` 在单事务（BEGIN/COMMIT/ROLLBACK）内执行多语句（SPEC §3.2）
- [ ] `role=tenant` → `SET LOCAL app.current_tenant_id`（由 X-Tenant-Id 派生）触发 RLS（SPEC §3.2）
- [ ] `role=service` → 跨租户执行（对应原 outbox BYPASSRLS 语义），独立审计（SPEC §3.2）
- [ ] 迁移编排能力：CreateTable 执行受管 DDL 并记录审计（SPEC §3.2）
- [ ] `make validate-architecture` 通过

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
- SPEC: design-kb-persistence-to-core-datapipe §3.2, §5 (Phase A)
