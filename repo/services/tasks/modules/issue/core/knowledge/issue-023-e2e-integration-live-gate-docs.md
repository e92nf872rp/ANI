# 端到端联调与 live gate + 文档闭环

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-022)
- UX: `repo/services/tasks/modules/ux/console/knowledge/ux-console-knowledge-base-platform.md` (全流程验证)
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-console-knowledge-base-platform.md` (§9.4 US-022)

## Description
作为平台开发者，我需要完成端到端联调、测试与文档闭环。

## Scope
- Product line: core + console (full stack E2E)
- Code paths allowed: cross-service verification scripts + docs only

## Acceptance Criteria
- [ ] [SPEC] 端到端链路：创建/删除 KB → 上传 7 格式文档异步解析 → 概览配置改触发重建 → 问答多会话+同步/流式+TopK+引用（SPEC §9.4 US-022 AC1）
- [ ] [SPEC] 父子分块（2048/256-512）验证（SPEC §9.4 US-022 AC2）
- [ ] [SPEC] 文档级摘要验证（SPEC §9.4 US-022 AC3）
- [ ] [SPEC] 自定义元数据验证（SPEC §9.4 US-022 AC4）
- [ ] [SPEC] SSE 流式验证（SPEC §9.4 US-022 AC5）
- [ ] [SPEC] RLS 隔离验证（SPEC §9.4 US-022 AC6）
- [ ] [SPEC] 解析重试验证（SPEC §9.4 US-022 AC7）
- [ ] [SPEC] 全库重建验证（SPEC §9.4 US-022 AC8）
- [ ] [SPEC] Core 文档级删除验证（SPEC §9.4 US-022 AC9）
- [ ] [SPEC] pg_trgm 检索验证（SPEC §9.4 US-022 AC10）
- [ ] [SPEC] live gate 通过（SPEC §9.4 US-022 AC11）
- [ ] `make test` 全绿（PRD US-022 AC12）
- [ ] `make validate-services` 通过（PRD US-022 AC13）
- [ ] `make validate-architecture` 通过（PRD US-022 AC14）
- [ ] [SPEC] development-records + CURRENT-SPRINT + ANI-06 更新（SPEC §9.4 + PRD US-022 AC15）

## Dependencies
#21 (all frontend) + #22 (E2E async chain) — final integration gate; all prior issues effectively.

## Type
docs (e2e)

## Priority
high

## Labels
core, console, e2e

## Batch
M2.1-TASK-D

## References
- SPEC: spec-console §9.4 (US-022 AC1-14)
- PRD: US-022
