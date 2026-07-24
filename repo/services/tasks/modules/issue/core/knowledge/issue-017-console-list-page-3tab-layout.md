# Console 前端列表页与 3 tab 布局

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-019)
- UX: `repo/services/tasks/modules/ux/console/knowledge/ux-console-knowledge-base-platform.md` (§1, §3.1, §4.1, §4.2, §6.1)
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-console-knowledge-base-platform.md` (§2, §5, §9)

## Description
作为前端开发者，我需要实现知识库列表页与单库 3 tab 布局。

## Scope
- Product line: console (frontend)
- Code paths allowed: `repo/frontends/console/src/routes/kb/` only

## Acceptance Criteria
- [ ] [SPEC] 列表页 `repo/frontends/console/src/routes/kb/index.tsx` 展示知识库表格（名称/状态/文档数/创建时间，SPEC §2.4 MODIFY）
- [ ] [UI] 列表页含新建模态框（名称/描述/嵌入模型/chunk_size/top_k）、删除确认、状态 Tag（UX §4.1, §5 组件表）
- [ ] [SPEC] `__root.tsx`（实为 `$kbId/route.tsx`）实现 3 tab 布局（概览/文档/问答，SPEC §2.4 NEW）
- [ ] [SPEC] parentRoute 改造正确（SPEC §2.4 注：用 `$kbId/route.tsx` parentRoute 模式）
- [ ] [UI] P1 占位页（data-ingestion/lab/permissions/history）4 个（UX §4.6, SPEC §2.4 NEW）
- [ ] [UI] 状态设计对齐 UX §6.1（idle/loading/empty/error/creating/deleting 等）
- [ ] Typecheck/lint passes
- [ ] [SPEC] Verify in browser: 列表加载/空态/错误态（SPEC §9.4 US-019 AC7）

## Dependencies
#1 + #15 (contract fix + new endpoints, for openapi-fetch types) — per SPEC §10.2 (US-019 depends on spec-services-kb-service US-001/002).

## Type
console (frontend)

## Priority
high

## Labels
console

## Batch
M2.1-TASK-C

## References
- SPEC: spec-console §2.2, §2.4, §9.4
- UX: §1, §3.1 (步 1-2), §4.1, §4.2, §5, §6.1
