# KB-SPLIT-P5C 拆分收尾：全量验收 + 文档闭环四件套 + ADR 0001 状态更新

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/0001-split-kb-from-core.md`
- UX: N/A — backend-only
- SPEC: N/A

## Description
作为 kb-service 拆分负责人，我需要收尾整个拆分工程：全量验收（make test + validate-architecture + 全部阶段证据汇总）、完成拆分批次的文档闭环四件套、把 ADR 0001 状态从 Proposed 更新为 Accepted（附拆分 commit 引用），并将演进期事项（阶段 6）登记到后续规划，拆分批次正式关闭。

## Scope
- Product line: core (kb-service / 拆分管理)
- Code paths allowed: `repo/services/tasks/modules/plan/knowledge_base/0001-split-kb-from-core.md`（状态更新）、`docs/`（收尾汇总）、monorepo 侧文档索引
- 无业务代码改动（收尾性质）

## Acceptance Criteria
- [ ] [ADR §五-阶段4/5] 全部阶段验收证据汇总：阶段 0–5 每阶段的 `pass / fail / not_verified` 证据表汇总落档 docs/execution/status.md，无 not_verified 遗留项（或有明确的演进期豁免登记）
- [ ] `make test` + `make validate-architecture` + `make validate-services-route-contract` + `git diff --check` 全部通过（独立仓库 + monorepo 双侧）
- [ ] [ADR §三] 拆分目标五条逐条核销：独立维护（#062/#068）、独立持久化与存储（#053–#058）、契约版本化（#060）、基线可追溯（#049→#068 diff 链）、首发即目标态（#064/#065 不带入清单核销）
- [ ] [ADR 状态] 0001-split-kb-from-core.md 状态 Proposed → Accepted，附拆分 commit 与验收证据引用
- [ ] [ADR §五-阶段6] 演进期事项登记（不在本批次建 issue）：pgvector 收敛评估、task-service 就绪后承接任务展示、D2 身份机制钩子、未来 Core 依赖 RFC 流程
- [ ] [ADR §七] 后果清单核销：正面红利与代价逐项对照实际结果；Milvus/bucket 运维责任交接确认（#063 运维手册就位）
- [ ] progress 文档闭环 4 件套（CLAUDE.md §6-3：development-records/{批次ID}.md、README 索引、CURRENT-SPRINT.md、ANI-06-开发计划.md——批次 KB-SPLIT 全系 #049–#070 关闭记录）

## Dependencies
#068 #069（仓库拆分与切换完成后收尾）

## Type
docs (closeout)

## Priority
high

## Labels
core, kb-service, closeout, adr-0001, split

## Batch
KB-SPLIT-P5C

## References
- ADR: §三（拆分目标五条）、§五-阶段6（演进期）、§七（后果）、状态行（Proposed → Accepted）
