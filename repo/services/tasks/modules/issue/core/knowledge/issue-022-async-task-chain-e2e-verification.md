# 异步任务链路端到端验证

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-018)
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-rag-engine.md` (§9.4)

## Description
作为平台开发者，我需要验证 outbox → NATS → rag-engine → 状态回写的完整异步链路。

## Scope
- Product line: core (E2E verification)
- Code paths allowed: cross-service test scripts + verification harness only

## Acceptance Criteria
- [ ] [SPEC] 上传文档后 outbox 事件发布到 NATS `ani.tasks.kb.parse`（SPEC §9.4 US-018 AC1-6）
- [ ] [SPEC] rag-engine 订阅领取任务（SPEC §9.4）
- [ ] [SPEC] 解析 → 分块 → 摘要 → 向量化 → 写 kb_chunks 完整执行（SPEC §9.4）
- [ ] [SPEC] `kb_documents.parse_status` 正确回写（pending → parsing → indexing → ready/failed，SPEC §9.4）
- [ ] [SPEC] 失败可重试（SPEC §9.4）
- [ ] [SPEC] 端到端可复跑（SPEC §9.4）

## Dependencies
#14 (rag-engine parse_worker + gRPC) + #8 (kb-service outbox) — per SPEC §10.2 (US-018 depends on US-015 + kb-service US-010).

## Type
core (e2e)

## Priority
high

## Labels
core, e2e

## Batch
M2.1-TASK-D

## References
- SPEC: spec-services-rag-engine §9.4 (US-018 AC1-6)
