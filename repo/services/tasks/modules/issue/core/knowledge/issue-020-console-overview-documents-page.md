# Console 概览页与文档页

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-020)
- UX: `repo/services/tasks/modules/ux/console/knowledge/ux-console-knowledge-base-platform.md` (§3.1 步 3-5, §4.3, §4.4, §6.2, §6.3)
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-console-knowledge-base-platform.md` (§2, §5, §9)

## Description
作为前端开发者，我需要实现概览页与文档页。

## Scope
- Product line: console (frontend)
- Code paths allowed: `repo/frontends/console/src/routes/kb/$kbId/overview.tsx`, `documents.tsx` only

## Acceptance Criteria
- [ ] [UI] 概览页展示入库配置（Embedding/chunk_size/OCR）+ 问答配置（TopK/score_threshold/检索策略）+ P1 规划区（UX §4.3）
- [ ] [SPEC] 改 Embedding/chunk_size 触发重建（调 `/rebuild`，SPEC §5.1 概览重建算法）
- [ ] [UI] 文档页工具栏 + 表格，支持拖拽多文件上传 + 自定义元数据（UX §4.4, SPEC §5.1 两步式上传）
- [ ] [SPEC] 文档页支持状态筛选、重试（调 `/reparse`，SPEC §5.1 文档解析重试算法）
- [ ] [UI] 文档页展示解析详情（父子块层级 Tree + 摘要 + metadata，UX §4.4 解析详情 Drawer）
- [ ] [UI] 状态设计对齐 UX §6.2（概览 saving/rebuilding/rebuild-conflict）+ §6.3（文档 uploading/parsing/indexing/ready/failed/reparse-confirm）
- [ ] [SPEC] 两步式上传：GET upload URL → PUT MinIO → POST notify（SPEC §5.1 两步式上传算法）
- [ ] Typecheck/lint passes
- [ ] [SPEC] Verify in browser: 上传 loading/空态/错误态/解析详情（SPEC §9.4 US-020 AC7）

## Dependencies
#17 (list + tab shell) — per SPEC §10.2 (US-020 depends on US-019).

## Type
console (frontend)

## Priority
high

## Labels
console

## Batch
M2.1-TASK-C

## References
- SPEC: spec-console §2.2, §2.4, §5.1, §9.4
- UX: §3.1 (步 3-5), §4.3, §4.4, §5, §6.2, §6.3
