# RAG-REFACTOR-STEP-2-FEATURE — rag-engine Parse/Embed/Generate RPC 实现 (步骤 2 功能)

- **Issue:** issue-029-feature-rag-engine-rpc-impl
- **Branch:** `refactor/architecture-compliance`
- **Date:** 2026-08-18
- **Product line:** core (Services / rag-engine)
- **Type:** feature (实现逻辑, 依赖步骤 1 契约 issue #025)
- **Dependency:** #025 (rag-engine 契约) — proto RPC 和 message 定义已由 #025 完成

## 变更文件

| 文件 | 变更内容 |
|------|----------|
| `ai/rag-engine/app/core/embeddings.py` | 移除 `_wrapped_model` 全局变量；`get_embed_model()` 直接返回 `OpenAICompatibleEmbedding` (无 LlamaIndex 包装)；`_as_base_embedding` 保留并标注 `[DEPRECATED]` 供旧 Query RPC 路径使用 |
| `ai/rag-engine/app/core/milvus.py` | `build_vector_store_index` 在 `embed_model is None` 时通过 `_as_base_embedding(get_embed_model())` 包装适配器 (旧 Query 路径) |
| `ai/rag-engine/app/services/embed_rpc_service.py` | **新建** — `EmbedRPCService.embed()` 无状态嵌入服务, 直调 `get_text_embedding_batch`, 无 LlamaIndex 依赖 |
| `ai/rag-engine/app/services/generate_rpc_service.py` | **新建** — `GenerateRPCService` 使用纯 Python `openai` SDK 复现 LlamaIndex `CompactAndRefine` + `ContextChatEngine` 行为, 包含多轮 refine 循环 |
| `ai/rag-engine/app/grpc/server.py` | 新增 Parse/Embed/Generate/GenerateStream RPC 实现；Query RPC 标注 `[DEPRECATED]`；包含 `_safe_unlink`/`_extract_image_bytes`/`_build_parse_chunks` 辅助函数 |
| `ai/rag-engine/tests/conftest.py` | **新建** — 提取 4 个测试文件中重复的 40+ 行 stub 代码到共享 conftest |
| `ai/rag-engine/tests/test_parse_rpc.py` | **新建** — 11 tests: chunk 组装、图片提取、Parse RPC 成功/失败路径 |
| `ai/rag-engine/tests/test_embed_rpc.py` | **新建** — 6 tests: EmbedRPCService、Embed RPC flat vectors、错误处理 |
| `ai/rag-engine/tests/test_generate_rpc.py` | **新建** — 33 tests: 模板复现、消息序列、context 截断、多轮 refine、generate/generate_stream、gRPC 错误映射 |
| `ai/rag-engine/tests/test_embeddings_no_llamaindex.py` | **新建** — 6 tests: 验证无 LlamaIndex 依赖 |
| `ai/rag-engine/tests/test_embed_service.py` | 适配 `_as_base_embedding` 包装行为 + `llama_index.core.embeddings` stub |
| `ai/rag-engine/tests/test_parse_worker_and_grpc.py` | 移除重复 stub 代码到 conftest.py |

## 1. Design Decisions

### 1.1 CompactAndRefine 多轮 refine 循环

- **Ambiguity:** Plan §2.4 提到 "粗略截断复现 CompactAndRefine repack" 和 "多轮 refine: 第一个 chunk 用 QA 模板, 后续 chunk 用 refine 模板"，但未明确是单轮截断还是多轮分块。
- **Choice:** 实现完整的多轮 refine 循环: `_repack_context` 将 context 文本按 `(context_window - 2048 - 200) * 4` 字符分块, 第一块用 `DEFAULT_CONTEXT_TEMPLATE` (QA), 后续块用 `DEFAULT_REFINE_TEMPLATE` (含 `existing_answer`)。Token 跨轮累加。
- **Rationale:** `DEFAULT_REFINE_TEMPLATE` 在 Plan 中已定义且要求复现, 说明意图是多轮 refine。单轮截断在多 chunk 场景会丢失信息, 不满足功能等价性。当 context fits in one segment (常见场景) 时退化为单轮调用, 等价旧路径。

### 1.2 消息序列中 user 出现两次

- **Ambiguity:** Plan §2.4 要求消息序列 `[SYSTEM: context, *history(含当前轮user), USER: question]`，但这意味着当前轮 user 消息在 history 和 question 中各出现一次。
- **Choice:** `history` 包含当前轮 user 消息 (复现 kb-service 将 user 追加到 Redis 后调 rag-engine 的旧行为), `question` 作为最终 USER 消息追加 (复现 `{query_str}` 模板)。当前轮 user 在消息列表中出现两次。
- **Rationale:** 这是 LlamaIndex `ContextChatEngine` 的实际行为 — chat_history 来自 memory (含当前轮 user), query_str 模板又追加了一次。功能等价性要求精确复现。

### 1.3 Embed RPC 返回扁平化 1-D 向量

- **Ambiguity:** proto 契约定义了 `repeated float vectors_flat = 1` + `int32 dimension = 2` + `int32 count = 3`, 但未说明为什么不用 `repeated float` 嵌套。
- **Choice:** 返回扁平化 1-D 数组 `vectors_flat[i * dim + j] = j-th dim of i-th text`, 附带 `dimension` 和 `count`。
- **Rationale:** Plan §2.3 和 proto 契约明确要求扁平化。这避免了 protobuf `repeated repeated float` 的嵌套开销。kb-service 侧 (issue #030) 通过 `dimension` reshape 即可恢复 2-D。

### 1.4 GenerateStream 不使用多轮 refine

- **Ambiguity:** `generate()` 实现了多轮 refine, `generate_stream()` 是否也需要?
- **Choice:** `generate_stream()` 保持单轮调用 (全 context 截断后单次 LLM 调用)。
- **Rationale:** LlamaIndex `ContextChatEngine.stream_chat` 也不支持 streaming refine — 流式输出无法在 refine 轮次间切换。单轮调用与旧 streaming 路径行为一致。

### 1.5 OpenAI client 复用 (连接池)

- **Ambiguity:** 每次 `generate()` / `generate_stream()` 是否创建新的 `openai.OpenAI` 实例?
- **Choice:** `GenerateRPCService` 在 `__init__` 中缓存 `_client`, `_make_client()` 懒加载复用, `close()` 释放连接池。
- **Rationale:** `openai.OpenAI` 实例包含 httpx 连接池。每次请求新建会泄漏连接池。但 gRPC servicer 每次 RPC 创建新的 `GenerateRPCService` 实例 (在 `Generate` 方法内), 所以每个请求仍有一个独立 client, 请求结束后 `close()` 释放。未来可考虑 servicer 级别复用。

### 1.6 异常映射使用 `isinstance` + `lru_cache`

- **Ambiguity:** 如何区分 openai SDK 的 `APITimeoutError` / `APIConnectionError` / `APIError`?
- **Choice:** `_import_openai_exceptions()` 使用 `@functools.lru_cache(maxsize=1)` 缓存 SDK 异常类, `_map_openai_exception()` 使用 `isinstance` 检查。包含 `isinstance(val, type) and issubclass(val, BaseException)` 防御性检查, 处理 MagicMock stub 环境。
- **Rationale:** 字符串匹配 `type(exc).__name__` 脆弱 (类名可能变更)。`isinstance` 是 Python 标准的异常类型检查方式。`lru_cache` 避免每次请求重复导入和属性遍历。

### 1.7 `_safe_unlink` 处理 Windows 文件锁

- **Ambiguity:** Parse RPC 下载临时文件后, 在 `finally` 块清理, 但 Windows 上文件可能仍被占用。
- **Choice:** `_safe_unlink(path)` 使用 `try/except OSError: pass` 忽略所有 OS 错误。下载错误路径中, 先退出 `with` 块 (关闭文件句柄), 再调用 `_safe_unlink` + `abort`。
- **Rationale:** Windows 上 `NamedTemporaryFile` 在 `with` 块内时文件被锁定, `os.unlink` 会抛 `PermissionError`。早期实现将 `abort` 放在 `with` 块内, 导致 `PermissionError` 被 outer `except` 捕获, `abort()` 从未执行。重构后先退出 `with` 块再清理, 修复了这个问题。

## 2. Deviations

### 2.1 `conftest.py` 提取超出 Issue scope

- **Spec said:** Issue #029 scope: "Code paths allowed: `app/grpc/server.py`, `app/core/embeddings.py`, 新增 `app/services/embed_rpc_service.py`, `app/services/generate_rpc_service.py`, 复用 `app/services/parse_service.py` + `chunk_service.py`"
- **Implemented:** 新增 `tests/conftest.py` 和 4 个新测试文件, 修改 `test_embed_service.py` 和 `test_parse_worker_and_grpc.py`。
- **Why:** Issue AC 明确要求 4 个新测试文件 + 旧测试全通过。4 个测试文件中有完全相同的 40+ 行 stub 代码 (llama_index/docling/grpc/pymilvus 等 MagicMock stub)。提取到 conftest.py 避免维护性差 (新增 stub 需改 4 个文件)。test_embed_service.py 和 test_parse_worker_and_grpc.py 的修改是为了适配 embeddings.py 的变更 (`_as_base_embedding` 包装行为)。

### 2.2 `milvus.py` 修改超出 Issue scope

- **Spec said:** Issue #029 scope 不包含 `app/core/milvus.py`。
- **Implemented:** `build_vector_store_index` 在 `embed_model is None` 时通过 `_as_base_embedding(get_embed_model())` 包装适配器。
- **Why:** `get_embed_model()` 从返回 `BaseEmbedding` wrapper 改为返回 `OpenAICompatibleEmbedding` 后, 旧 Query RPC 路径 (`build_vector_store_index`) 需要适配 — `VectorStoreIndex.from_vector_store` 要求 `BaseEmbedding`。不修改 milvus.py 会导致旧 Query RPC 在运行时失败。

## 3. Tradeoffs

### 3.1 纯 Python `openai` SDK vs LlamaIndex

- **Alternative 1:** 继续使用 LlamaIndex `OpenAILike` LLM + `ContextChatEngine` + `CompactAndRefine`。
  - Pros: 无需复现行为, 直接复用 LlamaIndex 代码。
  - Cons: 违反 Plan §2.4 "纯 Python openai SDK" 要求; LlamaIndex 依赖臃肿; 版本升级风险。
- **Alternative 2:** 纯 Python `openai` SDK 手动复现。
  - Pros: 无 LlamaIndex 依赖; 完全控制 prompt/消息/错误处理; 与 Plan 一致。
  - Cons: 需要精确复现 `DEFAULT_CONTEXT_TEMPLATE`/`DEFAULT_REFINE_TEMPLATE`/消息序列/context 截断; 复现不完整会导致 answer 语义不一致。
- **Chosen:** Alternative 2 — 与 Plan 要求一致, 通过 33 个测试验证复现精度。

### 3.2 GenerateStream 的线程模型

- **Alternative 1:** 使用 `openai.AsyncOpenAI` 原生 async streaming。
  - Pros: 无需 worker thread; 性能最优。
  - Cons: 需要重写整个 generate_stream 路径为 async; 与 generate() (同步) 不一致。
- **Alternative 2:** 同步 generator + `threading.Thread` + `queue.Queue` 中继。
  - Pros: 与 generate() 共享同步代码路径; 实现简单。
  - Cons: 每 token 通过 `asyncio.to_thread(token_queue.get)` 创建一个线程任务 (性能开销)。
- **Alternative 3:** 同步 generator + 直接在 `async def` 中迭代 (不 offload)。
  - Pros: 最简单。
  - Cons: 阻塞 asyncio 事件循环 (P4 问题)。
- **Chosen:** Alternative 2 — 不阻塞事件循环, 与 generate() 共享同步代码。每 token 线程任务开销可接受 (流式通常 50-200 tokens)。

### 3.3 Context 截断策略

- **Alternative 1:** 使用 tokenizer 精确计算 token 数。
  - Pros: 精确, 不会超出 context window。
  - Cons: 需要 tokenizer 依赖 (tiktoken); 对中文/混合语言不准确; 性能开销。
- **Alternative 2:** Char-based 粗略估计 `chars / 4 ≈ tokens`。
  - Pros: 无依赖; 快速; 与旧 LlamaIndex `PromptHelper` 的粗略估计一致。
  - Cons: 对某些语言 (中文 ~2 chars/token) 可能偏大; 会多分几个 segment (安全方向)。
- **Chosen:** Alternative 2 — `(context_window - 2048 - 200) * 4` chars per segment。粗略截断是 Plan 明确允许的 ("粗略截断复现")。

## 4. Open Questions

### 4.1 `inference_service_name` 参数未实现

- **Question:** `GenerateRequest.inference_service_name` 和 `GenerateStream` 的同名参数当前被忽略, 总是使用 `settings.vllm_model`。proto 契约定义了这个字段, kb-service contracts.py 的 `RagEngineClientProtocol.generate()` 也传递了它。是否需要在后续 issue 中实现多 LLM 路由?
- **Action:** 当前标注为 "Reserved for per-request LLM routing (not yet implemented)"。如果 kb-service (issue #030) 需要传递不同的 `inference_service_name`, 需要在 `generate_rpc_service.py` 中实现基于 name 的 model 选择逻辑。

### 4.2 Generate RPC gRPC 错误映射精度

- **Question:** 当前所有 `RuntimeError` (包括 `APIStatusError` 4xx) 都映射为 `UNAVAILABLE`。`APIStatusError` 4xx 应该映射为 `INVALID_ARGUMENT` 或 `INTERNAL` 而非 `UNAVAILABLE`。这是否需要在后续修复?
- **Action:** `_map_openai_exception` 的消息前缀已区分 "vLLM unavailable:" (ConnectionError) 和 "vLLM error:" (APIError), 但 gRPC 层统一映射为 `UNAVAILABLE`。修复需要引入新的异常类型层次区分 connection error vs API error, 超出 issue #029 范围。可在 review-it 中标记为低优先级后续项。

### 4.3 Parse RPC `file_type` 白名单校验

- **Question:** Parse RPC 接受任意 `file_type`, 但 `_extract_image_bytes` 只处理 pdf/docx/xlsx/pptx。`ParseService.parse` 对不支持的类型抛 `ValueError` (被 catch), 是否应该在入口校验?
- **Action:** 当前 `ValueError` catch 已经处理了这个情况, 不是 bug。但入口白名单校验更防御性。可在后续优化。

## 验证命令

```bash
# rag-engine 单元测试
cd ai/rag-engine && python -m pytest tests/ -v
# Result: 278 passed, 8 skipped, 0 failed

# Go adapters runtime 测试
cd pkg/adapters/runtime && go test -v -run TestLocalVectorStoreService ./...
# Result: PASS

# Go gateway router 测试
cd services/ani-gateway && go test -run TestVectorStore ./internal/router/
# Result: ok (PASS)

# 端到端多格式测试 (gateway + kb-service + rag-engine + 真实 Milvus/PG/MinIO/Redis/NATS/vLLM)
powershell -ExecutionPolicy Bypass -File scripts/test_e2e_multiformat.ps1
# Result: 14/14 ALL PASS (CreateKB → Upload 4 types → Parse → Query text/table/image)
```
