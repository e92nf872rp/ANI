# [功能] rag-engine Parse/Embed/Generate RPC 实现 (步骤 2 功能)

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX: N/A — backend-only
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` (§2.2-§2.5, 步骤 2)

## Description
作为 AI 层开发者，我需要完成 rag-engine 四个无状态 RPC 的实现逻辑：Parse（download_url 下载+解析+分块+图片提取）、Embed（直调 OpenAI SDK）、Generate（纯 Python openai SDK + CompactAndRefine 复现）、GenerateStream（流式），保留旧 Query RPC 兼容回滚。跑通全部测试。

## Scope
- Product line: core (Services / rag-engine)
- Code paths allowed: `repo/ai/rag-engine/app/grpc/server.py`, `app/core/embeddings.py`, 新增 `app/services/embed_rpc_service.py`, `app/services/generate_rpc_service.py`, 复用 `app/services/parse_service.py` + `chunk_service.py`

## Acceptance Criteria
- [ ] [Plan §2.2] Parse RPC 实现：download_url → httpx 流式下载临时文件 → 复用 `ParseService.parse` + `ChunkService.chunk` → 独立 `_extract_image_bytes` 提取图片 bytes（PDF/Office 各格式专用库 API）→ `_build_parse_chunks` 组装 ParsedChunk（parents + children + images，child 传 `parent_content` + `parent_chunk_id`）
- [ ] [Plan §2.3] Embed RPC 实现：`embeddings.py` 移除 `_as_base_embedding` wrapper，`get_embed_model()` 直接返回 `OpenAICompatibleEmbedding`；`EmbedRPCService.embed` 直调 `get_text_embedding_batch`
- [ ] [Plan §2.4] Generate RPC 实现：纯 Python `openai` SDK 调 vLLM `/v1/chat/completions`；复现 `DEFAULT_CONTEXT_TEMPLATE` + `DEFAULT_REFINE_TEMPLATE`；上下文粗略截断复现 CompactAndRefine repack；消息序列 `[SYSTEM: context, *history(含当前轮user), USER: question]`（复现 `{query_str}` 重复 user 旧行为）；超时 120s → `TimeoutError` → gRPC `DEADLINE_EXCEEDED`；`response.usage` 提取 tokens
- [ ] [Plan §2.4] GenerateStream 实现：`stream_options={"include_usage": True}`，最后 chunk 返回 usage；事件序列 token* → done
- [ ] [Plan §2.5] 旧 Query RPC 标注 `[DEPRECATED]`，代码不变，保留至步骤 11
- [ ] 新增 `test_parse_rpc.py` — download_url → chunks + 图片 bytes
- [ ] 新增 `test_embed_rpc.py` — texts → vectors_flat + dimension + count
- [ ] 新增 `test_generate_rpc.py` — history 含当前轮 user + 末尾追加 question + DEFAULT_CONTEXT/REFINE_TEMPLATE + CompactAndRefine 截断 + 超时→DEADLINE_EXCEEDED + response.usage token
- [ ] 新增 `test_embeddings_no_llamaindex.py` — `get_embed_model()` 返回 `OpenAICompatibleEmbedding`
- [ ] `python -m pytest tests/ -v` 旧测试全通过

## Dependencies
#025 (rag-engine 契约) — 实现依赖 proto 定义的 RPC 和 message。

## Type
core (feature)

## Priority
high

## Labels
core, feature

## Batch
RAG-REFACTOR-STEP-2-FEATURE

## References
- Plan: §2.2 (Parse RPC), §2.3 (Embed RPC), §2.4 (Generate RPC + CompactAndRefine 复现), §2.5 (旧 Query 保留)
