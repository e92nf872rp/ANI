# [功能] 删除 rag-engine 旧路径 + 清 allowlist (步骤 11 功能)

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX: N/A — backend-only
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` (§2.3, §4, 步骤 11)

## Description
作为 AI 层开发者，我需要在确认新路径稳定 ≥ 1 周后，删除 rag-engine 旧路径（qa_service / retrieve_service / embed_service / parse_worker / 直连 Milvus/PG/MinIO），清理 allowlist，将 LlamaIndex 降级为 `llama-index-core` 最小包（仅 SentenceSplitter）。rag-engine 最终成为无状态 RPC 执行引擎。跑通全部测试。

## Scope
- Product line: core (Services / rag-engine)
- Code paths allowed: `repo/ai/rag-engine/` 全部旧路径文件 + `requirements.txt` / allowlist

## Acceptance Criteria
- [ ] [Plan 步骤11 前置] 确认步骤 8A/8B/9/10 全通过；新路径稳定 ≥ 1 周；`kb_query_use_new_path=true`、`kb_parse_consumer_enabled=true`、`KB_SSE_USE_NEW_PATH=true`
- [ ] [Plan 步骤11 顺序1] 删除 `app/services/qa_service.py`（旧 Query 入口）
- [ ] [Plan 步骤11 顺序2] 删除 `app/routers/query.py`（REST query）
- [ ] [Plan 步骤11 顺序3] 删除 `app/routers/documents.py`（含 `DELETE /{kb_id}/documents/{doc_id}/index`，功能已由 kb-service DeleteDocument + Core API 替代）
- [ ] [Plan 步骤11 顺序4] 删除 `app/services/retrieve_service.py`（直连 Milvus + PG）
- [ ] [Plan 步骤11 顺序5] 删除 `app/services/embed_service.py`（直连 Milvus 写入）
- [ ] [Plan 步骤11 顺序6] 删除 `app/services/summary_service.py`（摘要移入 parse_orchestrator）
- [ ] [Plan 步骤11 顺序7] 删除 `app/workers/parse_worker.py`（NATS consumer）
- [ ] [Plan 步骤11 顺序8] 删除 `app/repositories/chunks.py`（asyncpg 直连 PG）
- [ ] [Plan 步骤11 顺序9] 删除 `app/clients/core_api.py`（不再直接下载）
- [ ] [Plan 步骤11 顺序10] 删除 `app/clients/minio_client.py`（直连 MinIO）
- [ ] [Plan 步骤11 顺序11] 删除 `app/core/milvus.py`（直连 Milvus）
- [ ] [Plan 步骤11 顺序12] `app/grpc/server.py` 移除旧 Query RPC
- [ ] [Plan 步骤11 顺序13] `app/grpc/rag.proto` 移除 Query RPC 定义
- [ ] [Plan 步骤11 顺序14] `main.py` 精简：移除 Milvus/NATS/PG pool
- [ ] [Plan 步骤11 顺序15] `app/core/config.py` 精简：移除 Milvus/PG/NATS/MinIO 配置
- [ ] [Plan 步骤11 顺序16] `app/core/embeddings.py` 移除 `_as_base_embedding`（仅旧 Query 用）
- [ ] [Plan 步骤11 顺序17] 运行 `make gen-proto` 重新生成 stub
- [ ] [Plan 步骤11 allowlist] 移除 `pymilvus`、`asyncpg`、`nats-py`、`minio`、`jieba`、`redis`
- [ ] [Plan 步骤11 allowlist] `llama-index` → 降级为 `llama-index-core` 最小包（仅 SentenceSplitter；import 名 `llama_index.core`）
- [ ] [Plan 步骤11 allowlist] 保留 `httpx`、`openai`、`PyMuPDF`/`python-docx`/`openpyxl`/`python-pptx`
- [ ] [Plan §2.3] 保留 rag-engine：`main.py`（精简）+ `app/core/`（config 精简 + embeddings 精简）+ `app/grpc/`（rag.proto Parse/Embed/Generate + server.py 4 RPC）+ `app/services/`（parse_service + chunk_service + embed_rpc_service + generate_rpc_service）+ `app/clients/ocr_api.py`
- [ ] 运行 `make gen-proto`
- [ ] 运行 `make validate-architecture` 通过
- [ ] 运行 `make test` 通过
- [ ] rag-engine 无 `pymilvus`/`asyncpg`/`minio`/`jieba`/`redis` import
- [ ] rag-engine 仅 `llama-index-core`（非完整 `llama-index`）
- [ ] [Plan §4] **最终等价性**：全量 E2E 通过，Jaccard > 90%
- [ ] [Plan §4] 改造后架构合规：§5.3 Services 不直接依赖 Milvus/MinIO SDK → ✅；§3.1 Core 不含模型推理 → ✅；§3.3 Core 不调用 Services → ✅

## Dependencies
#037 (NATS switch) + #038 (SSE switch) — 步骤 11 依赖 9+10。新路径稳定 ≥ 1 周后方可执行。

## Type
core (feature, cleanup)

## Priority
medium

## Labels
core, feature, cleanup

## Batch
RAG-REFACTOR-STEP-11-FEATURE

## References
- Plan: 步骤 11 (删除顺序 + allowlist 清理), §2.3 (rag-engine 去 LlamaIndex), §4 (架构合规性验证), §5 (风险与缓解)
