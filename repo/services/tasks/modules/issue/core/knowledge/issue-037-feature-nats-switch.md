# [功能] NATS 切换 — Outbox Dispatcher 切 v2 subject (步骤 9 功能)

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX: N/A — backend-only
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` (步骤 9)

## Description
作为运维开发者，我需要将 parse 消息投递从旧 NATS subject 切换到 v2 subject，使 kb-service consumer 接管解析任务，停止 rag-engine parse_worker。可随时回滚。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/app/consumers/` 配置 + Outbox Dispatcher 配置

## Acceptance Criteria
- [ ] [Plan 步骤9] 确认 `KB_PARSE_CONSUMER_ENABLED=true` 已部署且稳定
- [ ] [Plan 步骤9] Outbox Dispatcher 切换到 `nats_parse_subject_v2`（`ani.tasks.kb.parse.v2`）
- [ ] [Plan 步骤9] 停止 rag-engine parse_worker（`NATS_URL=""`）
- [ ] [Plan 步骤9] 观察 outbox_events 消费速度正常
- [ ] [Plan 步骤9] 回滚方案：切回旧 subject + 重启 rag-engine parse_worker
- [ ] [Plan §0.1] 切换后 Parse 管线行为不变：下载→解析→分块→摘要→嵌入→写 kb_chunks→写 Milvus
- [ ] [Plan §0.3] 旧 subject `ani.tasks.kb.parse` 保留（回滚用）

## Dependencies
#036 (E2E 测试) — 步骤 9 依赖 8B。

## Type
core (feature, ops)

## Priority
medium

## Labels
core, feature, ops

## Batch
RAG-REFACTOR-STEP-9-FEATURE

## References
- Plan: 步骤 9 (NATS 切换操作步骤), §0.1 (Parse 管线等价), §0.3 (NATS subject 不变更项)
