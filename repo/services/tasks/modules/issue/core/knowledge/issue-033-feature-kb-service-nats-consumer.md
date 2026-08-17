# [功能] kb-service NATS consumer 默认关闭 (步骤 6 功能)

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX: N/A — backend-only
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` (步骤 6)

## Description
作为 Services 层开发者，我需要实现 kb-service NATS consumer 消费 parse 消息，替代 rag-engine parse_worker。默认关闭，通过 flag 切换。使用独立 subject `ani.tasks.kb.parse.v2` 避免与旧路径冲突。跑通全部测试。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/app/consumers/parse_consumer.py`, `app/core/config.py`, `main.py`

## Acceptance Criteria
- [ ] [Plan 步骤6] 实现 `app/consumers/parse_consumer.py`：消费 NATS parse 消息，调用 `ParseOrchestrator.process_document`
- [ ] [Plan 步骤6] `config.py` 新增 `kb_parse_consumer_enabled: bool = False`（默认关闭）
- [ ] [Plan 步骤6] `config.py` 新增 `nats_parse_subject_v2: str = "ani.tasks.kb.parse.v2"`（独立 subject）
- [ ] [Plan 步骤6] `main.py` 按 flag 启动 consumer
- [ ] [Plan 步骤6] Outbox Dispatcher 按 flag 切换 subject（`nats_parse_subject_v2` vs `nats_parse_subject`）
- [ ] [Plan §0.3] 旧 subject `ani.tasks.kb.parse` 不变，新增 v2 subject 由 flag 切换
- [ ] 幂等处理：重复消息不重复解析
- [ ] 新增 `test_parse_consumer.py` — mock NATS + orchestrator + flag 开关
- [ ] flag=false 时 consumer 不启动，现有功能不受影响

## Dependencies
#032 (parse_orchestrator) — consumer 依赖 orchestrator 实现。

## Type
core (feature)

## Priority
medium

## Labels
core, feature

## Batch
RAG-REFACTOR-STEP-6-FEATURE

## References
- Plan: 步骤 6 (NATS consumer 设计), §0.3 (NATS subject 不变更项)
