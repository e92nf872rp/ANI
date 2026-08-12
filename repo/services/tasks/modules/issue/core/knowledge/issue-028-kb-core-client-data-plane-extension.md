# kb-service CoreClient 扩展（data_query / create_table）

## Document Links
- PRD: N/A — 由设计文档驱动
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/design-kb-persistence-to-core-datapipe.md` (§4.1)

## Description
作为平台开发者，我需要扩展 kb-service 的 `CoreClient`，加入数据面调用能力，作为 7 个 repository 从 asyncpg 直连迁移到 Core 数据面的基础。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/app/core_api/` only

## Acceptance Criteria
- [ ] [SPEC] `data_query(sql, params, role="tenant") -> {rows, rowcount}` 调 `POST /data/query`（SPEC §4.1）
- [ ] [SPEC] `create_table(name, definition)` 调 `POST /data/tables`（SPEC §4.1）
- [ ] 支持 `role="service"`（跨租户，供 outbox 派发器使用）（SPEC §4.1, §4.2）
- [ ] 错误映射为 CoreAPIError（对照现有 `/vector-stores`/`/objects` 惯例）（SPEC §3.3）
- [ ] 复用现有 httpx 持久连接（连接池）（SPEC §8 性能缓解）
- [ ] `pytest test_core_client.py` 通过

## Dependencies
#24 (数据面契约)

## Type
core (services)

## Priority
high

## Labels
core, services

## Batch
TBD（Phase B）

## References
- SPEC: design-kb-persistence-to-core-datapipe §4.1, §8
