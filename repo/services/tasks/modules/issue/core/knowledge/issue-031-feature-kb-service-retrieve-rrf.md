# [功能] kb-service retrieve_service + Python RRF 混合检索编排 (步骤 4 功能)

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX: N/A — backend-only
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` (§2.1, §4, 步骤 4)

## Description
作为 Services 层开发者，我需要实现 kb-service 混合检索编排器 `RetrieveService`，保证与 rag-engine RetrieveService 结果等价。编排：rag-engine.Embed（query）→ Core API 向量检索 → PG 关键词检索 → RRF 融合 → 父块回填 → 去重。跑通全部测试。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/app/services/rrf.py`, `app/services/retrieve_service.py`

## Acceptance Criteria
- [ ] [Plan §4.1] 实现 `rrf.py`：`reciprocal_rank_fusion(rank_lists, k=60, top_n=10)` 与 LlamaIndex `QueryFusionRetriever(mode='reciprocal_rerank', num_queries=1)` 等价
- [ ] [Plan §4.2] 实现 `RetrieveService`：实现 `RetrieveServiceProtocol` 接口，编排 rag-engine.Embed（query）→ Core API `search_vector_store` → PG `keyword_search` → RRF → 父块回填 → `_return_parent_and_dedup`
- [ ] [Plan §4.2] 支持三种检索模式：hybrid / vector / keyword
- [ ] [Plan §4.2] `_build_sources_from_fusion`：hybrid 模式 chunk_id 在 vector_results 中用 cosine score；keyword-only 用 RRF score min-max 归一化
- [ ] [Plan §4.2] `_process_vector_only`：从 Core API 结果组装 sources（含 content）
- [ ] [Plan §4.2] `_process_keyword_only`：从 PG 结果组装 sources
- [ ] [Plan §4.2] `_return_parent_and_dedup`：child 有 parent_content → 替换 content；同 parent_chunk_id 去重保留 score 最高；doc_summary 不参与去重
- [ ] [Plan §4.2] 父块回填：child `parent_content` 为空时从 kb_chunks 回查 `parent_chunk_id`；doc_summary 回填该文档所有 parent blocks
- [ ] [Plan §4.2] hybrid score_threshold 归一化：`max_score = max(vector_results cosine)`（RRF 分数不可直接与 cosine threshold 比较）
- [ ] 新增 `test_retrieve_service.py` — 三种模式 + RRF + hybrid 归一化 + 父块回填
- [ ] 新增 `test_rrf.py` — 与 LlamaIndex 对比
- [ ] **等价性测试**：同一 KB，对比 sources chunk_id 集合 Jaccard > 90%

## Dependencies
#027 (接口) + #030 (kb-service infra) — 实现依赖接口定义 + CoreClient/gRPC 客户端 + chunk_repo。与 #031 可并行。

## Type
core (feature)

## Priority
high

## Labels
core, feature

## Batch
RAG-REFACTOR-STEP-4-FEATURE

## References
- Plan: §4.1 (RRF), §4.2 (retrieve_service 编排), §2.1 (hybrid 归一化)
