# rag-engine 嵌入与 Milvus 直连

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-013)
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-rag-engine.md` (§2, §3, §5)

## Description
作为 AI 层开发者，我需要实现嵌入服务并直连 Milvus（v1.2 架构优化，移除 CoreAPIVectorStore）。

## Scope
- Product line: core (Services / rag-engine)
- Code paths allowed: `repo/ai/rag-engine/app/services/embed_service.py`, `app/core/milvus.py`, `app/core/embeddings.py` only

## Acceptance Criteria
- [ ] [SPEC] `embed_service` 用 `HuggingFaceEmbedding` 动态加载，写入与查询嵌入统一（SPEC §5.1 embed_service）
- [ ] [SPEC] `MilvusVectorStore`（LlamaIndex 包 `llama-index-vector-stores-milvus`）直接操作 Milvus（SPEC §5.1）
- [ ] [SPEC] 经 `VectorStoreIndex.from_vector_store(vector_store, embed_model=...)` 包装，由 Index 层嵌入后调 `vector_store.add()`（SPEC §5.1）
- [ ] [SPEC] Milvus 集合命名 `kb_{kb_id 去横杠}`，索引 HNSW、metric=COSINE、M=16、efConstruction=200（SPEC §3.1, §5.1）
- [ ] [SPEC] 不封装 CoreAPIVectorStore 适配器（v1.2 架构，SPEC §1.3）
- [ ] `make test` 通过

## Dependencies
#9 (parse service, LlamaIndex dependency migration) — per SPEC §10.2 (US-013 depends on US-011).

## Type
core (services)

## Priority
high

## Labels
core, services

## Batch
M2.1-TASK-B

## References
- SPEC: spec-services-rag-engine §1.3, §3.1, §5.1 (embed_service)
