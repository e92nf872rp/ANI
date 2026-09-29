# KB-SPLIT-P5A 仓库拆分执行：不带入清单最终核对 + 整目录迁移 + monorepo 收尾

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/0001-split-kb-from-core.md`
- UX: N/A — backend-only
- SPEC: N/A

## Description
作为 kb-service 拆分负责人，我需要执行仓库拆分（阶段 5：首发即目标态）：按不带入清单最终核对后，将 `repo/services/kb-service/` 整目录迁移独立仓库，记录拆分 commit 与差异；monorepo 侧删除目录后更新 CODEOWNERS 与文档索引；回滚 = git revert + 镜像重建，不设专门预案。

## Scope
- Product line: infra (repo split)
- Code paths allowed: `repo/services/kb-service/`（迁移源）、新独立仓库（迁移目标）、`repo/CODEOWNERS`、monorepo 文档索引
- 不带入清单执行确认：v1 解析路径与 flag 门控（#064 已删）、NotifyDocumentUploaded RPC（#065 已删）、core_api 模块（#065 已删）、`app/generated` 复制件（#060 已收敛）、共享 async_tasks/outbox_events 读写（#050/#051 已切换）——本 issue 为最终核对而非重做

## Acceptance Criteria
- [ ] [ADR §五-阶段5] 不带入清单最终核对（逐项 grep 验证零残留）：v1 解析路径与 flag 门控、NotifyDocumentUploaded RPC、core_api 模块、`app/generated` 散复制、共享 async_tasks/outbox_events 读写
- [ ] [ADR §五-阶段5] 验证 rag-engine 侧 parse_worker 及 PG/向量库直写权限已清（#039 + #064 验证记录，确认 rag-engine 彻底无状态化）
- [ ] [ADR §五-阶段5] `repo/services/kb-service/` 整目录迁移独立仓库：git 历史保留策略（filter-repo 或 squash 记录）落档；拆分 commit 与差异清单记录到 docs/execution/status.md
- [ ] [ADR §五-阶段5] monorepo 收尾：删除 kb-service 目录、更新 CODEOWNERS、更新 monorepo 文档索引与构建入口（摘除 kb 条目，联动 #062 CI 独立）
- [ ] [ADR §三-4] 基线可追溯闭环：#049 基线 commit → 拆分 commit 的 diff 全程可追溯
- [ ] [ADR §五-阶段5] 回滚策略登记：git revert + 镜像重建，不设专门预案（rev2 无过渡性设计原则）
- [ ] 独立仓库 `make test` + `make validate-architecture` + `git diff --check` 通过（迁移后仓库自含门禁）
- [ ] progress 文档闭环 4 件套（CLAUDE.md §6-3）

## Dependencies
#066 #067（阶段 4 验收通过后方可执行阶段 5）

## Type
infra (feature)

## Priority
high

## Labels
core, kb-service, repo-split, infra, split, adr-0001

## Batch
KB-SPLIT-P5A

## References
- ADR: §五-阶段5（仓库拆分全项）、§三-1/-4（独立维护 + 基线可追溯）、§rev2（无过渡性设计）
