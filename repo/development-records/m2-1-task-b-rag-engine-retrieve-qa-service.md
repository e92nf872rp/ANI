# M2.1-TASK-B — rag-engine 混合检索与 RAG 问答 (Issue #013)

完成日期：2026-08-03
对应 Sprint：Sprint 14
验证结果：57 unit tests passed (纯逻辑), 10 e2e checks passed (真实 Milvus + PG + Redis + vLLM), 8/8 accuracy metrics passed, make validate-architecture passed, git diff --check clean

| 字段           | 值                                                                                                     |
| ------------ | ----------------------------------------------------------------------------------------------------- |
| Issue        | #013 — rag-engine 混合检索与 RAG 问答                                                                          |
| PRD          | US-014 (`prd-core-knowledge-base-platform.md`)                                                         |
| UX           | N/A — backend-only                                                                                    |
| SPEC         | `spec-services-rag-engine.md` §2.2, §5.1, §5.4                                                        |
| Batch        | M2.1-TASK-B                                                                                           |
| Dependencies | #012 (embed_service + Milvus 直连) — per SPEC §10.2 (US-014 depends on US-013)                         |

## 实现了什么

实现 `retrieve_service`（混合检索服务）和 `qa_service`（RAG 问答服务）：

- **混合检索**：`QueryFusionRetriever` 融合 Milvus 向量检索（`VectorStoreIndex.as_retriever`）+ PostgreSQL pg_trgm 关键词检索（`PgTrgmRetriever`，`BaseRetriever` 子类），RRF 互逆排序融合（`num_queries=1` 关闭查询生成）。
- **单路检索**：`vector_retrieve`（Milvus HNSW/COSINE 语义检索）+ `keyword_retrieve`（pg_trgm GIN 索引关键词检索），共享父块回填与阈值过滤逻辑。
- **父块回填**：子块命中回填 `parent_content`（优先用反规范化字段，缺失时回退查询 `kb_chunks`）；摘要命中回填该文档的所有父块（SPEC §5.1 AC2/AC3）。
- **RAG 问答**：`ContextChatEngine.from_defaults` + `ChatMemoryBuffer(RedisChatStore)` + `OpenAILike` (vLLM)，同步返回 `answer + sources + session_id + tokens`。
- **多租户 RLS**：`tenant_id` 从 proto `QueryRequest` 透传到 pg_trgm 搜索的 `SET app.current_tenant_id`（每次查询动态设置）。
- **阈值过滤**：`score_threshold` 预检查在 LLM 调用前执行，低于阈值跳过 LLM 返回无结果回答（SPEC §5.4 不幻觉）。

## 关键文件改动

| 文件                                                        | 新增/修改 | 说明                                                                              |
| --------------------------------------------------------- | ----- | ------------------------------------------------------------------------------- |
| `ai/rag-engine/app/services/retrieve_service.py`          | 新增    | 混合检索服务：QueryFusionRetriever + PgTrgmRetriever + 父块回填 + 三路检索方法                    |
| `ai/rag-engine/app/services/qa_service.py`                | 新增    | RAG 问答服务：ContextChatEngine + RedisChatStore + OpenAILike + 阈值预检查                |
| `ai/rag-engine/app/core/config.py`                        | 修改    | 新增 `pg_dsn` / `nats_url` / `nats_parse_subject` / `redis_url`                    |
| `ai/rag-engine/app/core/embeddings.py`                     | 修改    | `_aget_query_embedding` / `_aget_text_embedding` 改用 `asyncio.to_thread` 避免阻塞事件循环 |
| `ai/rag-engine/app/routers/query.py`                       | 修改    | 新增 `idempotency_key` 字段                                                         |
| `ai/rag-engine/tests/test_retrieve_service.py`             | 新增    | 33 个纯逻辑单测                                                                       |
| `ai/rag-engine/tests/test_qa_service.py`                   | 新增    | 24 个纯逻辑单测                                                                       |
| `ai/rag-engine/tests/demo_e2e_retrieve_qa.py`              | 新增    | e2e 演示脚本（10 个验证点，真实基础设施）                                                          |
| `ai/rag-engine/tests/demo_e2e_retrieval_accuracy.py`       | 新增    | 检索准确性测评脚本（8 组查询 × 3 路检索，Precision@K/Recall@K/MRR）                                |
| `ai/rag-engine/requirements.txt`                           | 修改    | 新增 `llama-index-storage-chat-store-redis` / `asyncpg`                            |

## 完工标准达成（Issue 6 条 AC）

- [x] AC1: `QueryFusionRetriever(retrievers=[VectorStoreIndex.as_retriever(), PgTrgmRetriever], num_queries=1, mode=RRF)` — e2e 验证 2 retrievers
- [x] AC2: 子块命中回填 `parent_content`（192 字符） — e2e 验证 1/1 子块已回填
- [x] AC3: 摘要命中回填文档父块 — e2e 验证 1/1 摘要已回填
- [x] AC4: `ContextChatEngine.from_defaults + ChatMemoryBuffer(RedisChatStore) + OpenAILike` — e2e 验证 LLM=OpenAILike, ChatStore=RedisChatStore
- [x] AC5: `chat()` 同步返回 `QAResult(answer, sources, session_id, input_tokens, output_tokens)` — e2e 验证 answer(446字符) + sources(2) + session_id + tokens
- [x] AC6: `make test-python` + `make validate-architecture` + `git diff --check` 通过 — 57/57 通过

## 1. Design Decisions

### 1.1 PgTrgmRetriever 作为 BaseRetriever 子类而非鸭子类型

**Ambiguity**: SPEC §5.1 要求关键词检索通过 `PgTrgmRetriever` 实现，但未指定它是否必须是 `BaseRetriever` 子类。

**Choice**: 实现 `PgTrgmRetriever(BaseRetriever)` 真实子类，在 `_build_pg_trgm_retriever` 内部动态定义并返回实例。

**Rationale**: LlamaIndex `QueryFusionRetriever.__init__` 验证 `isinstance(r, BaseRetriever)`，鸭子类型对象会被拒绝。真实子类确保 fusion retriever 接受 keyword retriever。

### 1.2 `_run_async` 辅助函数处理嵌套事件循环

**Ambiguity**: SPEC 未说明 pg_trgm 搜索（asyncpg 异步）如何在 LlamaIndex 异步检索路径内同步执行。

**Choice**: 提取 `_run_async(coro)` 辅助函数：先尝试 `asyncio.run(coro)`，捕获 `RuntimeError`（仅 "event loop" / "already running" 相关）后 fallback 到 `nest_asyncio.apply()` + `asyncio.run(coro)`。

**Rationale**: LlamaIndex `QueryFusionRetriever._aretrieve` 在事件循环内运行，`asyncio.run()` 会抛 `RuntimeError: cannot be called from a running event loop`。`nest_asyncio` 允许嵌套事件循环。仅捕获 event loop 相关错误，其他 RuntimeError 透传避免掩盖协程内部异常（review-it Issue 9 修复）。

### 1.3 阈值预检查在 LLM 调用前执行

**Ambiguity**: SPEC §5.4 要求 `max_score < score_threshold` 时返回无结果回答，但未说明检查时机（LLM 前 or 后）。

**Choice**: 在 `engine.chat()` 之前执行 `fusion_retriever.retrieve(question)` 预检查，低于阈值直接返回 `NO_RESULT_ANSWER`，跳过 LLM 调用（`input_tokens=0, output_tokens=0`）。

**Rationale**: 避免 LLM 在低质量上下文上浪费计算资源。检索器会运行两次（预检查 + engine.chat 内部），但 `nest_asyncio` 处理嵌套循环，开销远小于一次 LLM 调用（review-it Issue 6 修复）。

### 1.4 `tenant_id` 从构造时绑定改为每次调用动态透传

**Ambiguity**: SPEC §5.1 未说明多租户 RLS 如何在 retrieve_service 中实现。

**Choice**: `make_pg_trgm_search_fn` 构造时绑定默认 `tenant_id`，`_search(query, *, top_k, tenant_id="")` 接受 per-call override。`_execute_pg_trgm_search` 每次执行 `SET app.current_tenant_id`。链路：`chat(tenant_id) → build_fusion_retriever(tenant_id) → PgTrgmRetriever(tenant_id) → _search(tenant_id) → SET app.current_tenant_id`。

**Rationale**: proto `QueryRequest.tenant_id` 需要透传到 RLS。当 `tenant_id` 为空时回退到构造时绑定的默认值，兼容单租户测试场景。

## 2. Deviations

### 2.1 RRF mode 字符串：`reciprocal_reranking` → `reciprocal_rerank`

**Spec**: SPEC §5.1 写 `mode='reciprocal_reranking'`。

**Implementation**: 使用 `FUSION_MODE_RRF = "reciprocal_rerank"`（LlamaIndex 0.14.x `FUSION_MODES.RECIPROCAL_RANK` 枚举值）。

**Reason**: LlamaIndex 0.14.x 的实际枚举值是 `"reciprocal_rerank"`（无 ing）。`"reciprocal_reranking"` 会被 `QueryFusionRetriever` 拒绝。代码注释（L44-46）解释了此差异。

### 2.2 嵌入模型：`HuggingFaceEmbedding` → 远程 OpenAI 兼容端点

**Spec**: SPEC §1.3 / §5.1 写 `HuggingFaceEmbedding(model_name=settings.embedding_model)`。

**Implementation**: 使用自定义 `OpenAICompatibleEmbedding` 适配器调用远程 `/v1/embeddings` 端点。

**Reason**: 此偏离已在 Issue #012 中实现并记录（见 `m2-1-task-b-rag-engine-embed-milvus-direct.md` Deviation 2.1）。retrieve_service 复用 issue-012 的 `get_embed_model()` 单例，写入与查询嵌入统一。

### 2.3 `make_parent_lookup_fn` 支持 DSN 字符串

**Spec**: SPEC §5.1 未指定 parent lookup 的连接方式。

**Implementation**: `make_parent_lookup_fn(pool_or_dsn, *, tenant_id)` 支持两种输入：asyncpg Pool（`pool.acquire()`）或 DSN 字符串（每次创建新连接）。

**Reason**: 与 `make_pg_trgm_search_fn` 保持 API 一致性（review-it Issue 7 修复）。DSN 模式避免 asyncpg Pool 跨事件循环绑定问题。

### 2.4 PgTrgmRetriever metadata 填充 `kb_id` / `tenant_id`

**Spec**: SPEC §3.1 定义了 node metadata schema（含 `kb_id` / `tenant_id`）。

**Implementation**: `_build_pg_trgm_retriever` 接受 `kb_id` 参数，`PgTrgmRetriever.__init__` 存储 `kb_id`，`_retrieve` 将 `kb_id` 和 `tenant_id` 写入 `TextNode.metadata`。

**Reason**: 原实现硬编码 `kb_id=""` / `tenant_id=""`，下游代码若读取这些字段会得到空值（review-it Issue 5 修复）。

## 3. Tradeoffs

### 3.1 检索器运行两次 vs 阈值检查时机

**Alternatives considered**:
- A: 先调用 `engine.chat()` 再从 `response.source_nodes` 检查阈值（原实现）
- B: 先 `fusion.retrieve()` 预检查阈值，再 `engine.chat()`（当前实现）
- C: 自定义 `ContextChatEngine` 子类，在 `chat()` 内部插入阈值检查

**Pros/Cons**:
- A: 检索器只运行一次，但 LLM 在低质量上下文上被调用，浪费计算
- B: 检索器运行两次，但 LLM 在低于阈值时被跳过。`nest_asyncio` 处理嵌套循环，第二次检索的开销远小于一次 LLM 调用
- C: 零重复检索，但需要深入 LlamaIndex 内部实现，维护成本高

**Chosen**: B — 最小改动，在正确性（SPEC §5.4）和性能（跳过 LLM）之间取得平衡。

### 3.2 PgTrgmRetriever `_aretrieve` 委托同步 `_retrieve`

**Alternatives considered**:
- A: `_aretrieve` 直接调 `_retrieve`（当前实现）
- B: `_aretrieve` 实现真正的异步路径（使用 asyncpg 异步 API）

**Pros/Cons**:
- A: 简单，复用同步 SQL 逻辑。但 `asyncio.run()` 在 async 上下文会触发 `nest_asyncio` fallback
- B: 性能更优，但需要重写 `_search` 为异步版本，增加代码复杂度

**Chosen**: A — `nest_asyncio.apply()` 是幂等的，功能正确。当前架构整体为同步（gRPC server 是同步），异步路径仅在 LlamaIndex fusion retriever 内部触发。

### 3.3 `inference_service_name` 参数预留未实现

**Alternatives considered**:
- A: 接受参数但不使用，docstring 标注 "Reserved"（当前实现）
- B: 移除参数，等需要时再加
- C: 实现完整的 per-request LLM 路由

**Pros/Cons**:
- A: proto 兼容（`QueryRequest.inference_service_name` 已定义），不破坏 API 契约
- B: proto 字段无法映射，gRPC server 实现时需要重新加回
- C: 需要维护 LLM 实例池，当前只有一个 vLLM 端点，过度设计

**Chosen**: A — 保持 proto 兼容性，未来实现 per-request LLM 路由时无需修改 API 签名。

## 4. Open Questions

### 4.1 gRPC server 未实现

`qa_service.py` 和 `retrieve_service.py` 的 docstring 引用 `grpc_server`，但 gRPC server 模块尚未创建。proto `QueryRequest` → `QAService.chat()` 的映射需要 US-015 (parse_worker + gRPC server) 实现。**当前 FastAPI `query.py` 仍为 stub**。

**Action**: US-015 实现 gRPC server 时需注意：(1) `chat()` 是同步方法，在 async gRPC handler 中需用 `asyncio.to_thread()` 包装；(2) proto `tenant_id` / `inference_service_name` / `idempotency_key` 字段已对齐。

### 4.2 MilvusVectorStore + Index + FusionRetriever 每次重建

每次 `retrieve()` / `chat()` 调用都创建新的 `MilvusVectorStore`、`VectorStoreIndex` 和 `QueryFusionRetriever`。对同一 KB 的频繁查询，这些对象可复用。

**Action**: 建议单独 issue 实现按 `kb_id` 缓存 fusion retriever，在 KB 文档增删时失效缓存。

### 4.3 SPEC 文档未同步更新

SPEC §1.3 / §5.1 仍写 `HuggingFaceEmbedding`，实际使用远程 OpenAI 兼容端点。SPEC §5.1 写 `mode='reciprocal_reranking'`，实际使用 `reciprocal_rerank`。

**Action**: 更新 SPEC §1.3 决策表、§5.1 伪代码、§5.1 mode 字符串，与实现同步。

### 4.4 检索准确性测评基准

当前测评使用 4 个主题 / 8 组查询的合成数据集。混合检索 Precision@5=1.000，但这是在小数据集上的理想结果。

**Action**: 建议使用真实文档（如 ANI 平台操作手册）构建更真实的 ground truth 评测集，验证在长文档、多主题、跨页引用场景下的准确性。

## 验证命令

```bash
# 单元测试
python -m pytest ai/rag-engine/tests/test_retrieve_service.py ai/rag-engine/tests/test_qa_service.py -q
# → 57 passed

# 架构校验
make validate-architecture
# → component import guard passed

# 端到端测试（需要真实基础设施）
$env:PYTHONPATH="ai/rag-engine"; python ai/rag-engine/tests/demo_e2e_retrieve_qa.py
# → 10/10 checks passed

# 检索准确性测评
$env:PYTHONPATH="ai/rag-engine"; python ai/rag-engine/tests/demo_e2e_retrieval_accuracy.py
# → 混合检索 Precision@5=1.000 Recall@5=1.000 MRR=1.000
```

## review-it 结果

- **审查范围**: 未提交变更 + 新增未跟踪文件 (retrieve_service.py, qa_service.py, config.py, embeddings.py, query.py + 测试)
- **发现问题**: 11 个（2 major, 9 minor）
- **已修复**: 5 个（Issue 1 docstring, 5 metadata, 6 阈值预检查, 7 DSN 支持, 9 _run_async 错误捕获）
- **已拒绝**: 6 个（有意设计 / false positive）
- **测试**: 57/57 通过 + make validate-architecture 通过
- **结论**: review-it clean
