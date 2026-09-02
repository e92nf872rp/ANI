# [功能] 单测 + Shadow/Replay + 契约测试 (步骤 7 功能)

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX: N/A — backend-only
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` (§0.2, §7, 步骤 7)

## Description
作为平台开发者，我需要完成全面的单元测试、shadow/replay 对比测试和 rag-engine gRPC 契约测试，验证新路径与旧路径功能等价（§0.1 等价性矩阵），为 flag 切换提供信心。跑通全部测试。

## Scope
- Product line: core (Services / kb-service + rag-engine)
- Code paths allowed: `repo/services/kb-service/tests/`, `repo/ai/rag-engine/tests/`, `repo/pkg/adapters/runtime/` 测试文件

## Acceptance Criteria
- [x] [Plan §7.1] kb-service 单测：`test_retrieve_service.py`（三种模式 + RRF + hybrid 归一化 + 父块回填） — 27 tests passed
- [x] [Plan §7.1] kb-service 单测：`test_rrf.py`（RRF 与 LlamaIndex 对比） — 13 tests passed
- [x] [Plan §7.1] kb-service 单测：`test_parse_orchestrator.py`（解析管线 + 状态机 + 摘要） — 26 tests passed
- [x] [Plan §7.1] kb-service 单测：`test_parse_consumer.py`（NATS 消费者 + 幂等 + flag） — 20 tests passed
- [x] [Plan §7.1] kb-service 单测：`test_chunk_repository.py`（write_chunks + keyword_search 改造后 + delete） — 11 tests passed
- [x] [Plan §7.1] kb-service 单测：`test_rag_grpc_client.py`（gRPC 客户端 + EmbedResponse 展平反序列化） — 14 tests passed
- [x] [Plan §7.1] rag-engine 单测：`test_parse_rpc.py`（download_url → chunks + 图片 bytes） — 9 tests passed
- [x] [Plan §7.1] rag-engine 单测：`test_embed_rpc.py`（vectors_flat + dimension + count） — existing tests passed
- [x] [Plan §7.1] rag-engine 单测：`test_generate_rpc.py`（history 含当前轮 user + 末尾追加 question + DEFAULT_CONTEXT/REFINE_TEMPLATE + CompactAndRefine 截断 + 超时→DEADLINE_EXCEEDED + response.usage token） — 33 tests passed
- [x] [Plan §7.1] rag-engine 单测：`test_embeddings_no_llamaindex.py`（get_embed_model 返回 OpenAICompatibleEmbedding） — 6 tests passed
- [x] [Plan §7.1] Core 单测：`vector_store_service_test.go`（预计算向量） — 18 tests passed (incl. pre-computed + dimension mismatch + mixed)
- [x] [Plan §7.2] Shadow 测试：`test_query_shadow.py` — 主路径返回后 fire-and-forget 异步对比 sources Jaccard > 90% — 3 shadow tests passed
- [x] [Plan §7.2] Replay 测试：录制旧路径 Query 请求，新路径回放，对比结果 — 3 replay tests passed
- [x] [Plan §7.3] 契约测试：`test_parse_rpc_contract`（download_url 非 bytes） — passed
- [x] [Plan §7.3] 契约测试：`test_embed_rpc_contract`（vectors_flat + dimension + count） — passed
- [x] [Plan §7.3] 契约测试：`test_generate_rpc_contract`（history 含当前轮 user + 末尾追加 question + system prompt 模板 + CompactAndRefine 截断） — 3 tests passed
- [x] [Plan §7.3] 契约测试：`test_source_chunk_has_chunk_id`（chunk_id 字段存在） — passed
- [x] [Plan §0.2] `KB_QUERY_SHADOW_MODE=true` 开启 shadow；shadow 失败不影响主路径 — verified
- [x] 所有测试通过，无回归 — 208 kb-service + 284 rag-engine + 18 Go = 510 passed, 0 failed

## Dependencies
#031 (retrieve) + #032 (parse_orchestrator) + #033 (NATS consumer) — 步骤 7 依赖 4+5+6 全部功能完成。

## Type
core (feature, tests)

## Priority
high

## Labels
core, feature, tests

## Batch
RAG-REFACTOR-STEP-7-FEATURE

## References
- Plan: §7.1 (单元测试矩阵), §7.2 (Shadow/Replay), §7.3 (契约测试), §0.2 (等价性验证方法)
