# Core 数据面契约：v1.yaml 新增 /data/query 与 /data/tables

## Document Links
- PRD: N/A — 由设计文档驱动
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/design-kb-persistence-to-core-datapipe.md` (§3.1)

## Description
作为平台开发者，我需要为 Core 新增通用数据面契约，使 Services 业务（如 kb-service）能通过 Core OpenAPI REST API 托管建表与读写，而非直连 PostgreSQL（CLAUDE.md §3 跨层边界）。

## Scope
- Product line: core
- Code paths allowed: `repo/api/openapi/v1.yaml` only

## Acceptance Criteria
- [ ] [SPEC] `POST /api/v1/data/query`：body `{sql, params:[], role}`，单请求=单事务；`role` enum `[tenant, service]`，默认 `tenant`（SPEC §3.1）
- [ ] [SPEC] `POST /api/v1/data/tables`：body `{name, definition}` 受管建表，平台管理员安全；非受管 DDL 返回 422（SPEC §3.1）
- [ ] 响应 schema 含 `rows`/`rowcount`/`last_result`；错误语义（400/403/429/422）明确（SPEC §3.1 x-errors）
- [ ] `servers[0].url` 为 `https://{host}/api/v1`（CLAUDE.md §4）
- [ ] SDK / API 文档 / 前端 schema 生成无漂移（`make validate-services` 通过）

## Dependencies
None

## Type
core

## Priority
high

## Labels
core

## Batch
TBD（Phase A）

## References
- SPEC: design-kb-persistence-to-core-datapipe §3.1, §5 (Phase A), §7.1
