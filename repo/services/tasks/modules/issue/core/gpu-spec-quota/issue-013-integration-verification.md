# 集成验收 — 闭环验证 + 文档更新

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
- UX: N/A（非前端）
- SPEC: `repo/services/tasks/modules/spec/core/gpu-spec-quota/spec-gpu-spec-quota.md`

## Description

作为开发者，完成集成验收，确保规格目录 → 配额分配 → 选规格创建实例 → TCC 预检 + Volcano 调度 + Confirm/Cancel 的完整闭环可用，并更新文档记录。

## Scope

- Product line: core + console + boss
- Code paths allowed: 全仓（文档 + 验证脚本）

## Acceptance Criteria

- [ ] 新增 `repo/development-records/gpu-spec-quota-batch.md`，记录批次实现与验证细节
- [ ] 更新 `repo/CURRENT-SPRINT.md`，追加 GPU 规格配额条目
- [ ] 更新 `repo/development-records/README.md`，追加批次索引
- [ ] `make test` 通过
- [ ] `make validate-architecture` 通过
- [ ] `make validate-services` 通过
- [ ] `make validate-doc-entrypoints` 通过
- [ ] `git diff --check` 通过
- [ ] 闭环验证：节点打标签 → BOSS 分配配额/预留 → Console 选规格创建实例 → TCC 预检 + Volcano 调度 + Confirm/Cancel 生效

## Dependencies

#10, #11, #12

## Type

docs

## Priority

high

## Labels

core

## Batch

M2.1-TASK-A

## References

- SPEC: §9 (Testing Strategy), §10 (Implementation Plan)
- UX: N/A
