# Console 问答页

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-021)
- UX: `repo/services/tasks/modules/ux/console/knowledge/ux-console-knowledge-base-platform.md` (§3.1 步 6-7, §4.5, §6.4)
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-console-knowledge-base-platform.md` (§2, §4.3, §5, §9)

## Description
作为前端开发者，我需要实现问答页支持多会话、同步/流式问答与引用展示。

## Scope
- Product line: console (frontend)
- Code paths allowed: `repo/frontends/console/src/routes/kb/$kbId/chat.tsx` only

## Acceptance Criteria
- [ ] [UI] 问答页左侧会话列表，右侧消息流（UX §4.5 布局）
- [ ] [SPEC] 支持同步问答（`POST /query`）与 SSE 流式问答（`GET /query/stream`，SPEC §4.1, §4.3 SSE 消费）
- [ ] [UI] TopK 可调节（InputNumber 1-20，UX §4.5, §5）
- [ ] [UI] 引用卡片展示子块内容 + 父块上下文（可展开/收起，UX §4.5 引用卡片）
- [ ] [SPEC] SSE 事件解析：`token`/`sources`/`done`/`error` 四事件（SPEC §4.3 EventSource 代码示例）
- [ ] [UI] 状态设计对齐 UX §6.4（querying-sync/streaming-start/streaming-tokens/streaming-sources/streaming-end/streaming-error/session-deleting 等）
- [ ] Typecheck/lint passes
- [ ] [SPEC] Verify in browser: 问答 loading/空态/错误态/SSE 增量输出/结束反馈（SPEC §9.4 US-021 AC6）

## Dependencies
#17 (tab shell) + #19 (gateway SSE endpoint) — per SPEC §10.2 (US-021 depends on US-019 + spec-services-ani-gateway-kb US-017).

## Type
console (frontend)

## Priority
high

## Labels
console

## Batch
M2.1-TASK-C

## References
- SPEC: spec-console §2.2, §4.3, §5.1 (问答 SSE), §9.4
- UX: §3.1 (步 6-7), §4.5, §5, §6.4
