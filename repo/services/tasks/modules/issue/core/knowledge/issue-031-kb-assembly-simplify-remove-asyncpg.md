# kb-service 进程装配简化（移除 asyncpg / rls.py / migrations，outbox 跨租户）

## Document Links
- PRD: N/A — 由设计文档驱动
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/design-kb-persistence-to-core-datapipe.md` (§4.2, §4.3, §4.4)

## Description
作为平台开发者，我需要简化 kb-service 进程装配：移除两个 asyncpg pool 及其事件循环绑定逻辑、删除 `rls.py` 与 `migrations/`、outbox 派发器改走数据面跨租户。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/main.py`, `app/outbox/dispatcher.py`, `app/repositories/rls.py`, `migrations/`, `requirements.txt` only

## Acceptance Criteria
- [ ] [SPEC] 删除 `_db_pool`/`_outbox_pool` 两个 asyncpg pool 及 `_build_grpc_pool`/`_build_pool`（SPEC §4.3）
- [ ] [SPEC] outbox dispatcher 改为注入数据面 client（`role="service"`），轮询/标记走 `data_query`（SPEC §4.3）
- [ ] [SPEC] `/readyz` 的 db/outbox_db 语义改为「数据面可达性」探活（SPEC §4.3）
- [ ] 删除 `app/repositories/rls.py`（SPEC §4.2）
- [ ] 删除 `migrations/` 目录；kb-service 启动不再自助建表（SPEC §4.4）
- [ ] `requirements.txt` 移除 `asyncpg`（SPEC §4.3）
- [ ] 导入层无 `asyncpg` 残留；`pytest` 全绿

## Dependencies
#29, #30 (repository 迁移)

## Type
core (services)

## Priority
high

## Labels
core, services

## Batch
TBD（Phase B）

## References
- SPEC: design-kb-persistence-to-core-datapipe §4.2, §4.3, §4.4
