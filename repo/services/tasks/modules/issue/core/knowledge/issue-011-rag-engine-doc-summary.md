# rag-engine 文档级摘要

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-012, summary portion)
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-rag-engine.md` (§2, §5)

## Description
作为 AI 层开发者，我需要实现文档级摘要：拼接前 N 父块 → LLM 生成 200-500 字摘要 → 向化存 Milvus。

## Scope
- Product line: core (Services / rag-engine)
- Code paths allowed: `repo/ai/rag-engine/app/services/summary_service.py` only

## Acceptance Criteria
- [ ] [SPEC] `summary_service` 拼接前 N 个父块 → LLM 生成 200-500 字摘要 → 向化存 Milvus（`chunk_type=doc_summary`，SPEC §5.1 summary_service）
- [ ] [SPEC] 摘要生成失败不阻断入库（降级为仅父子分块，记录 warning，SPEC §5.1）
- [ ] `make test` 通过

## Dependencies
#10 (parent-child chunking, needs parent chunks) — per SPEC §10.1 phase 4 (chunk+summary together), §10.2 (US-012 depends on US-011).

## Type
core (services)

## Priority
high

## Labels
core, services

## Batch
M2.1-TASK-B

## References
- SPEC: spec-services-rag-engine §2.2, §5.1 (summary_service)
