# KB-SPLIT-P1F kb 专属 PostgreSQL 实例 + 最小权限角色体系 + 跨租户负向测试（真实 PG）

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/0001-split-kb-from-core.md`
- UX: N/A — backend-only
- SPEC: N/A

## Description
作为 kb-service 基础设施负责人，我需要新建 kb 专属 PostgreSQL 实例（拆分即切：新代码直连 kb 专属库），重建凭据最小权限角色体系（应用/运维分离，延续最小权限原则），并在真实 PG 上跑跨租户负向测试与全量单测，阶段 1 验收收口。

## Scope
- Product line: core (kb-service / infra)
- Code paths allowed: `repo/services/kb-service/`（连接配置、角色初始化 SQL、compose/test-infra）、`repo/deploy/`（kb PG 实例部署清单，若适用）
- 不动共享库部署（其他服务仍用共享库）

## Acceptance Criteria
- [ ] [ADR §四 #1] 新建 kb 专属 PostgreSQL 实例；`config.py` database_url 指向 kb 专属库（不再默认共享库，config.py L17 默认值修正）
- [ ] [ADR §五-阶段1-3] 凭据最小权限分离：应用角色（仅 DML + 所需 DDL 执行权）与迁移/运维角色分离；沿用现 `ani_app` 角色模式重建独立角色体系，无 platform_bypass 类超级权限
- [ ] [ADR §五-阶段1-5] 真实 PG 跨租户负向测试通过：另一租户 GET/UPDATE/DELETE 必须 404/空集（真实库执行，非 mock；A+B 层 + kb_operations + outbox 全覆盖）
- [ ] [ADR §五-阶段1-验收] 独立库全量单测通过（阶段 1 验收条件）；证据标记 `pass / fail / not_verified` 三态落档 docs/execution/status.md
- [ ] [ADR §五-阶段1-验收] 迁移基线（009 outbox、010 kb_operations、011+ B 层与隔离改造）在专属库上可从零重放建库
- [ ] `make test` + `make validate-architecture` + `git diff --check` 通过
- [ ] progress 文档闭环 4 件套（CLAUDE.md §6-3）

## Dependencies
#050 #053 #054（表结构、隔离改造完成后收口验收）

## Type
infra (feature)

## Priority
high

## Labels
core, kb-service, postgresql, split, security, adr-0001

## Batch
KB-SPLIT-P1F

## References
- ADR: §四 #1（共享数据库耦合点）、§五-阶段1-3/-5/验收、§三-2（独立持久化目标）
