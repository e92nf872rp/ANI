# [功能] kb-service 基础设施：migration + repository + CoreClient + gRPC 客户端 (步骤 3 功能)

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX: N/A — backend-only
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` (§3.1-§3.6, 步骤 3)

## Description
作为 Services 层开发者，我需要完成 kb-service 编排能力基础设施的实现：新增 vector_store_id 持久化 migration、改造 chunk repository（keyword_search + write/delete）、实现 CoreClient 扩展方法、实现 rag-engine gRPC 客户端。跑通全部测试。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/migrations/`, `app/repositories/knowledge_base.py`, `app/repositories/chunk.py`, `app/core_api/client.py`, `app/rag_engine/client.py`, `requirements.txt`

## Acceptance Criteria
- [ ] [Plan §3.1] 新增 `migrations/004_kb_vector_store_id.sql`：`ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS vector_store_id TEXT`
- [ ] [Plan §3.1] `CreateKB` 存储返回的 `vector_store_id`
- [ ] [Plan §3.2] 改造现有 `keyword_search()`（保持签名）：jieba 分词 + 多 token OR + token 覆盖率归一化，与 rag-engine `PgTrgmRetriever` 行为一致
- [ ] [Plan §3.2] 新增 `write_chunks()`：从 rag-engine `repositories/chunks.py` 迁移，批量写入 kb_chunks（parents + children + summaries）
- [ ] [Plan §3.2] 新增 `delete_chunks_by_doc()`：删除文档所有 chunks，RLS 事务内执行
- [ ] [Plan §3.3] CoreClient 实现新增 `insert_vector_documents()`（POST /vector-stores/{id}/documents，body 含 vector 字段）
- [ ] [Plan §3.3] CoreClient 实现新增 `search_vector_store()`（POST /vector-stores/{id}/search，返回含 content）
- [ ] [Plan §3.3] CoreClient 实现新增 `upload_object()`（两步上传：POST /objects/upload → PUT upload_url）
- [ ] [Plan §3.3] 复用现有 `request_download_url()` + `delete_vector_store_documents()`（无需新增）
- [ ] [Plan §3.4] 实现 `RagEngineGRPCClient`：gRPC 客户端，`parse`/`embed`/`generate`/`generate_stream` 方法；`embed` 反序列化展平数组 `vectors[i] = vectors_flat[i*dim:(i+1)*dim]`
- [ ] [Plan §3.4] 保留旧 `RagEngineClient`（REST）标注 `[DEPRECATED]`
- [ ] [Plan §3.6] `requirements.txt` 新增 `jieba>=0.42.1`、`grpcio>=1.60.0`、`grpcio-tools>=1.60.0`
- [ ] 新增 `test_chunk_repository.py` — write_chunks + keyword_search（改造后）+ delete
- [ ] 新增 `test_rag_grpc_client.py` — mock gRPC + EmbedResponse 展平反序列化
- [ ] `python -m pytest tests/ -v` 通过

## Dependencies
#027 (接口) + #028 (Core 功能) + #029 (rag-engine 功能) — 实现依赖接口定义 + Core/rag-engine 能力可用。

## Type
core (feature)

## Priority
high

## Labels
core, feature

## Batch
RAG-REFACTOR-STEP-3-FEATURE

## References
- Plan: §3.1 (vector_store_id), §3.2 (chunk repo), §3.3 (CoreClient), §3.4 (gRPC client), §3.6 (依赖)
