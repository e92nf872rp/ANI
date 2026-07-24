# rag-engine NATS 订阅与 gRPC server

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-015)
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-rag-engine.md` (§2, §5)

## Description
作为 AI 层开发者，我需要实现 parse_worker NATS 订阅与 rag-engine gRPC server。

## Scope
- Product line: core (Services / rag-engine)
- Code paths allowed: `repo/ai/rag-engine/app/workers/parse_worker.py`, `app/grpc/server.py`, `app/clients/core_api.py`, `main.py` only

## Acceptance Criteria
- [ ] [SPEC] `parse_worker` 订阅 NATS `ani.tasks.kb.parse`，领取任务（SPEC §5.1 parse_worker）
- [ ] [SPEC] 领取后调 Core `/objects/{id}/download` 下载 → 解析 → 分块 → 摘要 → 直连 Milvus 写入子块 + 摘要 → 写 kb_chunks 表（SPEC §5.1）
- [ ] [SPEC] 回写任务状态，更新 `kb_documents.parse_status`（pending → parsing → indexing → ready/failed，SPEC §5.1, §5.3）
- [ ] [SPEC] gRPC server 实现 `Query` RPC（仅同步，SPEC §4.1）
- [ ] `make test` 通过

## Dependencies
#13 (retrieve+qa) + #8 (kb-service outbox to NATS) — per SPEC §10.2 (US-015 depends on US-014 + kb-service US-010).

## Type
core (services)

## Priority
high

## Labels
core, services

## Batch
M2.1-TASK-B

## References
- SPEC: spec-services-rag-engine §2.2, §4.1, §5.1 (parse_worker), §5.3
