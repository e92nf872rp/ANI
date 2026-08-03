# rag-engine 混合检索与 RAG 问答

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-014)
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-rag-engine.md` (§2, §5)

## Description
作为 AI 层开发者，我需要实现混合检索与 RAG 问答服务。

## Scope
- Product line: core (Services / rag-engine)
- Code paths allowed: `repo/ai/rag-engine/app/services/retrieve_service.py`, `app/services/qa_service.py` only

## Acceptance Criteria
- [ ] [SPEC] `retrieve_service` 用 `QueryFusionRetriever`：MilvusVectorStore（包为 `VectorStoreIndex.as_retriever()`）+ pg_trgm 关键词（`BaseRetriever` 子类）+ RRF 融合，`num_queries=1` 关闭查询生成（SPEC §5.1 retrieve_service）
- [ ] [SPEC] 检索命中子块后回填父块上下文（`parent_content`，SPEC §5.1）
- [ ] [SPEC] 摘要命中后回填该文档的父块（SPEC §5.1）
- [ ] [SPEC] `qa_service` 用 `ContextChatEngine.from_defaults(retriever=fusion_retriever, memory=ChatMemoryBuffer(chat_store=RedisChatStore), llm=OpenAILike(model=..., api_base=vllm_url, api_key="...", is_chat_model=True, context_window=...)`（SPEC §5.1 qa_service）
- [ ] [SPEC] `qa_service.chat()` 同步返回 answer + sources + session_id + tokens（SPEC §5.1）
- [ ] `make test` 通过

## Dependencies
#12 (embed+Milvus) — per SPEC §10.2 (US-014 depends on US-013).

## Type
core (services)

## Priority
high

## Labels
core, services

## Batch
M2.1-TASK-B

## References
- SPEC: spec-services-rag-engine §2.2, §5.1 (retrieve_service, qa_service)
