# RAG 架构合规改造方案 (v9)

> **状态**: 设计草案，待用户确认
> **创建日期**: 2026-08-13
> **核心约束**: **功能效果不变** — 改造前后用户可感知的 Query/Parse/SSE 行为完全一致
> **涉及层**: ANI Core (pkg/ports, pkg/adapters) + ANI Services (rag-engine, kb-service, ani-gateway)

***

## 0. 功能等价性保证

本方案的核心约束是**功能效果不变**。以下等价性保证贯穿全部步骤：

### 0.1 等价性矩阵

| 功能维度          | 当前行为                                                                                                                                    | 改造后必须保持                                                                          |
| ------------- | --------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| Query sources | 向量+关键词混合检索 + RRF + 父块回填 + 去重                                                                                                            | chunk\_id 集合 Jaccard 相似度 > 90%                                                   |
| Query answer  | RAG prompt + CompactAndRefine 合成器 + vLLM 生成 + 不幻觉 (score < threshold → 空结果)                                                             | answer 语义一致; CompactAndRefine 上下文截断复现 (单 chunk 精确, 多 chunk 粗略截断); no-result 条件不变 |
| Query 无结果     | 三道闸门: ①检索为空 (LLM未调用, tokens=0) ②max\_score < threshold (LLM未调用, tokens=0) ③dedup 后 sources 为空 (LLM已调用, tokens=实际值) → NO\_RESULT\_ANSWER | 三道闸门全复现; ①② tokens=0; ③ tokens=LLM实际值                                            |
| Query 多轮会话    | Redis ChatMemoryBuffer + session\_id (历史含当前轮 user, 因 kb-service 先 append 再调 rag-engine; question 在 {query\_str} template 中重复一次)         | 历史含当前轮 user + question 末尾追加 (复现旧行为: user message 出现两次)                           |
| Query tokens  | input\_tokens + output\_tokens 计数                                                                                                       | 计数结果一致 (允许 ±5% 误差, vLLM 本身非确定性)                                                  |
| Parse 管线      | 下载→解析→分块→摘要→嵌入→写 kb\_chunks→写 Milvus                                                                                                    | 输入相同文档，kb\_chunks 行数和内容一致                                                        |
| Parse 摘要      | LLM 生成 doc\_summary (best-effort)                                                                                                       | 摘要生成行为一致 (best-effort 不阻塞)                                                       |
| Parse 图片      | Office/PDF 内嵌图片提取→MinIO 上传→占位符                                                                                                          | 图片提取→Core API 上传→占位符 (URL 不同但功能一致)                                               |
| Parse 状态机     | pending→parsing→indexing→ready\|failed                                                                                                  | 状态流转不变                                                                           |
| SSE 流式        | rag-engine 检索→vLLM 流式 token→sources→done                                                                                                | 事件序列 (token\*→sources→done) 不变                                                   |
| SSE 降级        | rag-engine/vLLM 不可用时返回空流                                                                                                                | kb-service 不可用时返回空流                                                              |
| 向量删除          | 删除文档时从 Milvus 删除对应向量 (filter expr)                                                                                                      | 删除行为一致, 通过Parse 图片 Core API DeleteDocuments                                      |

### 0.2 等价性验证方法

- **Shadow 模式** (步骤 7): 同一 Query 请求并行走新旧路径，对比 sources 重叠率和 answer 非空率
- **Replay 测试** (步骤 7): 录制旧路径 Query 请求，在新路径回放，对比结果
- **E2E 对比** (步骤 8B): 同一 KB + 同一文档集，新旧路径分别 Query，对比 P50/P99 延迟和准确率
- **Flag 回滚** (步骤 8A/9/10): 任何步骤可回滚到旧路径，回滚后功能不变

### 0.3 不变更项

以下项目在改造期间**完全不变**：

- Gateway 对外 API 接口 (12 个端点路径、请求/响应格式; 新增 `vector`/`content` 可选字段, 旧调用方忽略)
- kb-service gRPC 接口 (10 个 P0 RPC + 3 个 P1 RPC)
- PostgreSQL 表结构 (仅新增 `vector_store_id` 列，不改现有列)
- Redis 会话缓存 key 格式和 TTL
- NATS subject 名称 (`ani.tasks.kb.parse` 旧路径不变; 新增 `ani.tasks.kb.parse.v2` 由 flag 切换)

***

## 1. 问题陈述

### 1.1 当前架构违规

| 违规项                              | 位置                                              | 违反规则                                       |
| -------------------------------- | ----------------------------------------------- | ------------------------------------------ |
| rag-engine 直连 Milvus             | `app/core/milvus.py`                            | CLAUDE.md §5.3: Services 不得直接依赖 Milvus SDK |
| rag-engine 直连 PostgreSQL         | `app/repositories/chunks.py`, `parse_worker.py` | Services 不应绕过 Core 直接操作底层组件                |
| rag-engine 直连 MinIO              | `app/clients/minio_client.py`                   | CLAUDE.md §5.3: Services 不得直接依赖 MinIO SDK  |
| rag-engine NATS 消费者              | `app/workers/parse_worker.py`                   | parse 逻辑应由 kb-service 消费                   |
| rag-engine REST query            | `app/routers/query.py`                          | 查询应由 kb-service 编排                         |
| Gateway SSE 直连 rag-engine + vLLM | `kb_sse.go`                                     | SSE 应由 kb-service.Retrieve 编排              |
| kb-service 调 rag-engine REST     | `app/rag_engine/client.py`                      | 应使用 gRPC 而非 REST                           |

### 1.2 目标

将 RAG 子系统改造为合规架构：**kb-service 作为唯一 Services 层编排者，rag-engine 降级为无状态 RPC 执行引擎，Core 提供可选 vector 能力**。

### 1.3 约束

- **功能效果不变**: 改造前后用户可感知行为完全一致 (见 §0)
- **兼容旧调用**: 改造期间旧路径必须可回滚，不允许中断现有功能
- **渐进切换**: 使用 flag 控制，shadow/replay 验证后再删除旧路径
- **遵守 CLAUDE.md §3.3**: Core 不调用 Services
- **遵守 CLAUDE.md §3.1**: Core 不包含模型推理; embedding 由 rag-engine 计算, Core 只存储预计算向量
- **遵守 CLAUDE.md §5.3**: Services 不直接操作 Milvus/MinIO/PG SDK；跨层走 Core OpenAPI REST

### 1.4 Core API 必须修改 (3 项)

经源码确认，Core API 当前缺少 3 个必要能力，改造前必须先扩展:

| # | 修改                                               | 文件                                                                                                       | 原因                                      |
| - | ------------------------------------------------ | -------------------------------------------------------------------------------------------------------- | --------------------------------------- |
| 1 | `VectorDocumentInput` 新增 `Vector []float32` 字段   | `repo/pkg/ports/vector_store.go` + `repo/services/ani-gateway/internal/router/vector_store_resources.go` | kb-service 预计算向量后传给 Core, Core 不做嵌入     |
| 2 | `vectorDocumentInputBody` 新增 `vector` JSON 字段    | `repo/services/ani-gateway/internal/router/vector_store_resources.go` 第 50-54 行                          | HTTP body 传递预计算向量                       |
| 3 | `vectorSearchHitResponse` 新增 `Content string` 字段 | `repo/services/ani-gateway/internal/router/vector_store_resources.go` 第 92-96 行                          | kb-service 向量检索后需要 chunk 文本 (否则需二次查 PG) |

**修改详情**:

1. **`VectorDocumentInput`** **新增** **`Vector`** **字段** (`repo/pkg/ports/vector_store.go` 第 72-76 行):
   ```go
   type VectorDocumentInput struct {
       ID       string
       Content  string
       Vector   []float32  // 新增: 预计算向量 (可选, 为空则 Core 内部嵌入)
       Metadata map[string]string
   }
   ```
2. **`vectorDocumentInputBody`** **新增** **`vector`** **JSON 字段** (`vector_store_resources.go` 第 50-54 行):
   ```go
   type vectorDocumentInputBody struct {
       ID       string         `json:"id,omitempty"`
       Content  string         `json:"content"`
       Vector   []float32      `json:"vector,omitempty"`  // 新增
       Metadata map[string]any `json:"metadata,omitempty"`
   }
   ```
3. **`vectorSearchHitResponse`** **新增** **`Content`** **字段** (`vector_store_resources.go` 第 92-96 行):
   ```go
   type vectorSearchHitResponse struct {
       ID       string            `json:"id"`
       Score    float32           `json:"score"`
       Content  string            `json:"content"`  // 新增
       Metadata map[string]string `json:"metadata"`
   }
   ```

**向后兼容**: `Vector` 和 `vector` 字段均为 `omitempty`, 旧调用方不传 vector 时 Core 内部用 `localDocumentVector` 生成伪向量 (现有行为不变)。`Content` 字段为新增返回字段, 旧调用方忽略即可。

***

## 2. 目标架构

```
Client
  │
  ▼
ani-gateway (:8080)
  │
  ├─ /api/v1/svc/knowledge-bases/* (CRUD — gRPC → kb-service :50053)
  ├─ /api/v1/svc/knowledge-bases/:kb_id/query/stream (SSE — gRPC → kb-service.Retrieve)
  │
  ▼
kb-service (:50053 gRPC / :8002 HTTP)
  │  ── 唯一 Services 层编排者
  │
  ├─→ Core OpenAPI REST (via Gateway)
  │    POST /vector-stores              # 创建向量库
  │    DELETE /vector-stores/{id}        # 删除向量库
  │    POST /vector-stores/{id}/documents # 文档向量插入 (接收预计算向量)
  │    DELETE /vector-stores/{id}/documents?filter=...  # 按表达式删除向量
  │    POST /vector-stores/{id}/search   # 向量检索 (接收预计算向量)
  │    POST /objects/upload              # 获取上传 URL
  │    GET /objects/{id}/download        # 下载文档
  │
  ├─→ rag-engine (gRPC :50052)
  │    Parse(download_url, file_name, file_type, chunk_size)  # 无状态文档解析+分块
  │    Embed(texts) → vectors             # 无状态嵌入
  │    Generate(question, context, history) → answer  # 无状态 LLM 生成
  │    GenerateStream(...) → stream tokens # 流式生成
  │    Query(question, params) → answer   # 旧 RPC 保留兼容 (deprecated)
  │
  ├─→ PostgreSQL (asyncpg) — kb-service 拥有
  │    knowledge_bases (新增 vector_store_id 列), kb_documents, kb_sessions,
  │    kb_messages, async_tasks, outbox_events, kb_chunks
  │
  ├─→ Redis — 会话缓存 (Query 多轮历史消息)
  │
  ├─→ NATS (publish only) — Outbox 模式投递 parse 任务
  │
  └─→ NATS (consume, default off) — 可选消费者
       替代 rag-engine parse_worker
```

### 2.1 关键变化 (功能等价)

| 方面               | 当前                                                                                                              | 目标                                                                             | 等价性保证                        |
| ---------------- | --------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ | ---------------------------- |
| Milvus 访问        | rag-engine pymilvus 直连                                                                                          | kb-service 通过 Core REST                                                        | 同一 Milvus 实例，向量数据不变          |
| PG kb\_chunks 写入 | rag-engine asyncpg 直写                                                                                           | kb-service asyncpg 写入                                                          | 同一 SQL，同一表                   |
| PG 关键词检索         | rag-engine pg\_trgm 直查                                                                                          | kb-service pg\_trgm 直查 (改造现有 keyword\_search)                                  | 同一 SQL + jieba 分词            |
| MinIO 图片         | rag-engine 直连 MinIO                                                                                             | kb-service 通过 Core API 上传                                                      | 图片 bytes 不变，仅传输路径不同          |
| 文档下载             | rag-engine Core API 下载                                                                                          | kb-service Core API 下载, 传 download\_url 给 rag-engine                           | 同一 Core API 端点               |
| Embedding        | rag-engine 直调远程 API (经 LlamaIndex wrapper)                                                                      | rag-engine Embed RPC 直调 OpenAI SDK (去 LlamaIndex)                              | 同一 embedding 模型, 同一 API      |
| 向量插入             | rag-engine LlamaIndex Index                                                                                     | kb-service 调 rag-engine Embed → Core API Upsert                                | 同一向量值写入同一 Milvus             |
| 向量删除             | rag-engine MilvusClient.delete(expr)                                                                            | kb-service Core API DeleteDocuments(filter)                                    | 同一 filter 表达式, 同一 Milvus     |
| RAG Query        | kb-service → rag-engine REST Query                                                                              | kb-service retrieve → rag-engine Generate                                      | sources 重叠率 > 90%            |
| SSE              | Gateway 直连 rag-engine + vLLM                                                                                    | Gateway → kb-service.Retrieve → rag-engine.GenerateStream                      | 事件序列不变                       |
| 多轮会话             | rag-engine Redis ChatMemoryBuffer (含当前轮 user, 因 kb-service 先 append 再调 rag-engine; question 在 {query\_str} 中重复) | kb-service 从 Redis 拉历史 (含当前轮 user) → 传入 Generate; Generate 末尾追加 question       | 历史消息拼接一致 (复现旧行为: user 出现两次)  |
| LLM 调用           | LlamaIndex OpenAILike + ContextChatEngine + CompactAndRefine                                                    | 纯 Python openai SDK + DEFAULT\_CONTEXT\_TEMPLATE + 上下文截断 (复现 CompactAndRefine) | 同一 vLLM 端点, 同一 model, 同一消息序列 |

### 2.2 Embedding 嵌入策略

**Core 不做 embedding 推理** (CLAUDE.md §3.1 "Core 不得包含模型推理")。嵌入由 rag-engine 计算，kb-service 编排：

```
kb-service 调 rag-engine.Embed RPC → rag-engine 调 OpenAI 兼容 API 算向量 → 返回向量给 kb-service
kb-service 把向量传给 Core API (POST /vector-stores/{id}/documents, body 含 vector 字段)
Core 直接 Upsert 到 Milvus (不做嵌入, 不调任何推理 API)
```

Core 只负责向量存储 CRUD，不包含任何 embedding/推理能力。无 `Vector` 字段时用 `localDocumentVector` 伪向量 (dev/测试占位)。

### 2.3 rag-engine 去 LlamaIndex 依赖

改造后 rag-engine 的 **Parse/Embed/Generate RPC 全部不依赖 LlamaIndex**：

| 组件          | 当前依赖 LlamaIndex                                                                  | 改造后                                                                                                                                                                                                                            |
| ----------- | -------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Embed       | `BaseEmbedding` wrapper + `VectorStoreIndex`                                     | 直接用 `openai.OpenAI.embeddings.create` (已有 `OpenAICompatibleEmbedding` 内部已用 OpenAI SDK)                                                                                                                                         |
| Generate    | `OpenAILike` LLM + `ContextChatEngine` + `ChatMemoryBuffer` + `CompactAndRefine` | 直接用 `openai.OpenAI.chat.completions.create` + 手动拼接 messages + 上下文截断 (复现 CompactAndRefine repack)                                                                                                                               |
| Parse       | `SentenceSplitter` + `TextNode`                                                  | `SentenceSplitter` 保留 (需 `llama-index-core` 最小包); `TextNode` 改为普通 dict                                                                                                                                                         |
| 旧 Query RPC | 完整 LlamaIndex 链路                                                                 | 保留不变, 步骤 11 删除。注: rag-engine 同时暴露 gRPC Query (grpc/server.py 第 85 行) 和 REST query (routers/query.py 第 112 行), 两者共享 QAService.chat。kb-service 当前走 REST (rag\_engine/client.py 第 91 行), 改造后旧路径仍走 REST (flag=false 回滚), 新路径走 gRPC |

`embeddings.py` 移除 `_as_base_embedding` wrapper, 直接暴露 `get_text_embedding_batch` 方法。Embed RPC 实现直接调 `OpenAICompatibleEmbedding.get_text_embedding_batch`。

`generate_rpc_service.py` 用纯 Python `openai` SDK 调 vLLM `/v1/chat/completions`。经源码确认，旧路径 `ContextChatEngine` 使用 **`CompactAndRefine`** **响应合成器** (context.py 第 162 行 `get_response_synthesizer` 返回 `CompactAndRefine`)，上下文不是简单 `\n\n.join()`，而是经过 repack + refine 多轮调用 LLM。新路径必须复现此行为:

1. **上下文组装** (复现 `CompactAndRefine._make_compact_text_chunks`): 使用 `PromptHelper.repack()` 将检索节点文本打包到 LLM 上下文窗口内 (vllm\_context\_window - max\_tokens)。超大 chunk 拆分为多个子 chunk。
2. **消息组装** (复现 `get_prefix_messages_with_context`): `[SYSTEM: context_template.format(context_str=chunk_i), *prefix_messages, *chat_history, USER: question]`。其中 `chat_history` 从 Redis 拉取 (含当前轮 user，因 kb-service 先 append)，`question` 作为最后一个 USER 消息 (即 `{query_str}` template)。
3. **多轮 refine**: 第一个 chunk 用 QA 模板生成初始 answer，后续 chunk 用 refine 模板 (含 `existing_answer`) 让 LLM 优化 answer。与旧路径 `_run_refine_loop` 一致。

**关键修正 — 复现 LlamaIndex ContextChatEngine 默认 prompt + CompactAndRefine 合成器**:

旧路径 `ContextChatEngine.from_defaults()` **未传** **`system_prompt`** (prefix\_messages 为空)，使用 LlamaIndex 内置默认模板。经源码确认 (context.py 第 37-42 行 + utils.py 第 16-28 行)，发送给 vLLM 的消息序列为:

```
[
  SYSTEM: DEFAULT_CONTEXT_TEMPLATE.format(context_str=chunk_1),   # 上下文注入 SYSTEM
  *chat_history,                                                   # Redis 历史 (含当前轮 user)
  USER: question                                                    # {query_str} 模板
]
```

其中:

- `DEFAULT_CONTEXT_TEMPLATE` = `"Use the context information below to assist the user.\n--------------------\n{context_str}\n--------------------\n"`
- `DEFAULT_REFINE_TEMPLATE` = `"Using the context below, refine the following existing answer using the provided context to assist the user.\nIf the context isn't helpful, just repeat the existing answer and nothing more.\n--------------------\n{context_msg}\n--------------------\nExisting Answer:\n{existing_answer}\n--------------------\n"`
- `chat_history` 从 Redis `memory.get()` 读取 (含当前轮 user，因 kb-service 先 append 再调 rag-engine)
- `question` 作为最后一个 USER 消息 (即 `{query_str}`) — **与 chat\_history 中的当前轮 user 重复** (旧行为, 新路径复现)

```python
DEFAULT_CONTEXT_TEMPLATE = (
    "Use the context information below to assist the user."
    "\n--------------------\n"
    "{context_str}"
    "\n--------------------\n"
)

DEFAULT_REFINE_TEMPLATE = (
    "Using the context below, refine the following existing answer"
    " using the provided context to assist the user."
    "\nIf the context isn't helpful, just repeat the existing answer"
    " and nothing more."
    "\n--------------------\n"
    "{context_msg}"
    "\n--------------------\n"
    "Existing Answer:\n"
    "{existing_answer}"
    "\n--------------------\n"
)

def _build_rag_prompt(context: list[dict]) -> str:
    """复现 LlamaIndex CompactAndRefine 上下文组装。
    context_str = 检索到的 chunk content 拼接 (repack 截断到 context window)。"""
    context_str = "\n\n".join(c["content"] for c in context)
    # 估算 token: 1 token ≈ 4 chars (粗略), 截断到 vllm_context_window - max_tokens - system_prompt_overhead
    max_context_chars = (settings.vllm_context_window - 2048 - 200) * 4
    if len(context_str) > max_context_chars:
        context_str = context_str[:max_context_chars]
    return DEFAULT_CONTEXT_TEMPLATE.format(context_str=context_str)
```

**关键修正 — SentenceSplitter 依赖**:

`SentenceSplitter` 是 `llama_index.core.node_parser.SentenceSplitter`，属 LlamaIndex 框架组件。步骤 11 不能完全移除 `llama-index`，改为降级为 `llama-index-core` 最小包 (仅含 `SentenceSplitter`)，移除完整的 `llama-index` 主包。`TextNode` 改为普通 dict，不依赖 LlamaIndex 数据结构。

***

## 3. 分步改造方案

### 步骤 1: Core 加可选 vector (兼容旧调用)

**目标**: Core 向量存储 API 支持接收预计算向量，不破坏现有接口。Core 不做 embedding 推理 (遵守 §3.1)。

**改动范围**:

- `pkg/ports/vector_store.go` — `VectorDocumentInput` 新增 `Vector` 字段; `VectorSearchHit` 新增 `Content` 字段
- `pkg/adapters/runtime/vector_store_service.go` — `InsertDocuments` 优先使用传入向量, 无向量时用伪向量; `SearchVectorStore` 返回 content
- `services/ani-gateway/internal/router/vector_store_resources.go` — `vectorDocumentInputBody` 新增 `vector` JSON 字段; `vectorSearchHitResponse` 新增 `content` JSON 字段

**1.1 VectorDocumentInput 扩展**:

```go
// pkg/ports/vector_store.go — 修改

type VectorDocumentInput struct {
    ID       string
    Content  string
    Vector   []float32        // 新增: 预计算向量 (可选; 为空则 Core 内部生成)
    Metadata map[string]string
}
```

`LocalVectorStoreService.InsertDocuments` 逻辑变更:

```go
for i, doc := range request.Documents {
    var vec []float32
    if len(doc.Vector) > 0 {
        vec = doc.Vector           // 调用方预计算 (生产: kb-service 调 rag-engine.Embed)
    } else {
        vec = localDocumentVector(doc.Content, record.Dimension, i)  // 伪向量 (dev 占位)
    }
    vectorRecords = append(vectorRecords, ports.VectorRecord{ID: ..., Vector: vec, Metadata: ...})
}
```

**设计说明**: Core 不做 embedding 推理 (遵守 CLAUDE.md §3.1 "Core 不得包含模型推理")。
- 生产路径: kb-service 调 rag-engine.Embed RPC 算向量 → 传 `Vector` 字段给 Core → Core 直接存 Milvus
- 无 `Vector` 字段时: 用 `localDocumentVector` 伪向量 (dev/测试占位, 与现有行为一致)

**1.2 Gateway HTTP API 扩展**:

```go
// vector_store_resources.go — 修改
type vectorDocumentInputBody struct {
    ID       string         `json:"id,omitempty"`
    Content  string         `json:"content"`
    Vector   []float32      `json:"vector,omitempty"`  // 新增: 预计算向量 (可选)
    Metadata map[string]any `json:"metadata,omitempty"`
}
```

**1.3 向量删除 API (已有, 无需修改)**:

当前 `DELETE /vector-stores/{id}/documents?filter=doc_id=="..."` 已由 `deleteVectorStoreDocuments` 处理器实现, 调用 `service.DeleteDocuments` → `backend.DeleteByExpr`。kb-service 删除文档时复用此端点, **无需修改 Core API**。

**1.4 searchVectorStore 的向量来源 + 响应新增 content**:

当前 `POST /vector-stores/{id}/search` body 已有 `vector []float32` 字段。新路径下 kb-service 先调 `rag-engine.Embed` RPC 嵌入 query text，再把向量传入。**无需修改 API 入参**。

但**响应需新增** **`content`** **字段** (§1.4 修改项 3): 当前 `vectorSearchHitResponse` 只有 `id`、`score`、`metadata`, kb-service 向量检索后需要 chunk 文本内容 (否则需二次查 PG)。

```go
// vector_store_resources.go — 修改
type vectorSearchHitResponse struct {
    ID       string            `json:"id"`
    Score    float32           `json:"score"`
    Content  string            `json:"content"`  // 新增: chunk 文本 (从存储后端返回)
    Metadata map[string]string `json:"metadata"`
}
```

handler 中 `searchVectorStore` 需要从 `result` 中提取 content:

```go
items = append(items, vectorSearchHitResponse{
    ID:       result.ID,
    Score:    result.Score,
    Content:  result.Content,   // 新增
    Metadata: result.Metadata,
})
```

同时 `ports.VectorSearchHit` 也需新增 `Content` 字段:

```go
// pkg/ports/vector_store.go — 修改
type VectorSearchHit struct {
    ID       string
    Score    float32
    Content  string            // 新增
    Metadata map[string]string
}
```

**兼容性**: `Vector` 字段可选 (`omitempty`)，不传时行为不变。

**验证**:

- `go test ./pkg/adapters/runtime/... -run TestVectorStore`
- `make validate-architecture`
- 现有 Gateway vector store 测试全通过 (不传 vector 时行为不变)

***

### 步骤 2: rag-engine 新增 Parse/Embed/Generate RPC, 保留旧 Query

**目标**: rag-engine 新增四个无状态 RPC，保留旧 Query 兼容。新增 RPC 不依赖 LlamaIndex。

**改动范围**:

- `app/grpc/rag.proto` — 新增 Parse/Embed/Generate/GenerateStream RPC; SourceChunk 新增 chunk\_id
- `app/grpc/server.py` — 新增四个 RPC 实现
- `app/core/embeddings.py` — 移除 LlamaIndex `_as_base_embedding` wrapper, 保留 `OpenAICompatibleEmbedding`
- 新增 `app/services/embed_rpc_service.py` — 嵌入执行 (直调 `OpenAICompatibleEmbedding`, 不经 LlamaIndex)
- 新增 `app/services/generate_rpc_service.py` — LLM 生成 (纯 Python `openai` SDK + 超时 + 错误处理)
- 现有 `parse_service.py` + `chunk_service.py` 复用, 封装为 Parse RPC
- 运行 `make gen-proto` 重新生成 Python stub

**2.1 Proto 设计**:

```protobuf
service RagEngine {
  rpc Query(QueryRequest) returns (QueryResponse);           // 旧, deprecated
  rpc Parse(ParseRequest) returns (ParseResponse);            // 新
  rpc Embed(EmbedRequest) returns (EmbedResponse);            // 新
  rpc Generate(GenerateRequest) returns (GenerateResponse);   // 新
  rpc GenerateStream(GenerateRequest) returns (stream GenerateToken); // 新
}

message ParseRequest {
  string download_url   = 1;   // Core API 预签名下载 URL (避免 gRPC 传大文件 bytes)
  string file_name      = 2;
  string file_type      = 3;
  int32  chunk_size     = 4;
}

message ParsedChunk {
  string chunk_id       = 1;
  string content        = 2;
  string content_type   = 3;
  int32  page_number    = 4;
  string parent_content = 5;
  string chunk_type     = 6;
  string metadata_json  = 7;
  bytes  image_bytes    = 8;
  string image_format   = 9;
  string parent_chunk_id = 10;  // 新增: child chunk 的父块 ID (用于 _return_parent_and_dedup 去重 key)
}

message ParseResponse {
  repeated ParsedChunk chunks = 1;
}

message EmbedRequest {
  repeated string texts = 1;
}

message EmbedResponse {
  // 展平的一维数组: vectors_flat[i * dimension + j] = 第 i 个文本的第 j 维
  // 避免 nested repeated float 序列化/反序列化复杂度
  repeated float vectors_flat = 1;
  int32 dimension = 2;
  int32 count = 3;
}

message GenerateRequest {
  string question              = 1;   // 用户问题, Generate RPC 在 history 末尾追加为 USER 消息 (复现 {query_str} template)
  string session_id            = 2;
  repeated SourceChunk context = 3;
  string inference_service_name = 4;
  int32  max_tokens             = 5;
  repeated ChatMessage history = 6;  // 含当前轮 user message (复现旧行为: kb-service 先 append user 到 Redis)
}

message ChatMessage {
  string role    = 1;
  string content = 2;
}

message GenerateResponse {
  string answer        = 1;
  int32  input_tokens  = 2;
  int32  output_tokens = 3;
  string session_id    = 4;
}

message GenerateToken {
  string content      = 1;
  bool   done         = 2;
  int32  input_tokens  = 3;
  int32  output_tokens = 4;
}

// SourceChunk 新增 chunk_id (RRF 需要 chunk_id 做 rank fusion key)
message SourceChunk {
  string chunk_id  = 1;   // 新增: chunk 唯一 ID
  string doc_id    = 2;
  string file_name = 3;
  int32  page      = 4;
  string content   = 5;
  float  score     = 6;
}
```

**关键修正说明**:

- **SourceChunk 新增 chunk\_id**: 旧 SourceChunk (5 字段) 没有 chunk\_id, RRF 融合需要 chunk\_id 做 key。新增后 proto 向后兼容, 旧 Query RPC 不受影响。
- **ParseRequest 用 download\_url**: gRPC 默认 4MB 消息限制, 大文件会超限。传 download\_url (Core API 预签名 URL), rag-engine 自行下载, 等价于旧 parse\_worker 下载行为。
- **EmbedResponse 展平向量**: `repeated Vector` (嵌套 `repeated repeated float`) 在 Python gRPC 序列化性能差。改为 `vectors_flat` 一维数组 + `dimension` + `count`, kb-service 反序列化时 `vectors[i] = vectors_flat[i*dim:(i+1)*dim]`。

**2.2 Parse RPC 实现 (download\_url + 临时文件 + 图片 bytes)**:

```python
async def Parse(self, request, context):
    # 1. 从 download_url 下载到临时文件 (复用现有 core_api.py httpx 流式下载)
    with tempfile.NamedTemporaryFile(suffix=f".{request.file_type}", delete=False) as f:
        async with httpx.AsyncClient() as client:
            async with client.stream("GET", request.download_url) as resp:
                async for chunk in resp.aiter_bytes():
                    f.write(chunk)
        temp_path = f.name
    try:
        # 2. 调用现有 parse_service (uploader=None, 不提取图片; 第二参数 object_prefix 无用)
        parse_svc = ParseService(uploader=None)
        nodes = await asyncio.to_thread(parse_svc.parse, temp_path, "")
        # 3. 调用现有 chunk_service (返回 parents, children — 2 个值, 非 3 个)
        chunk_svc = ChunkService(child_chunk_size=request.chunk_size or 1024)
        parents, children = await asyncio.to_thread(chunk_svc.chunk, nodes)
        # 4. 提取图片 bytes (不上传, 返回给 kb-service)
        #    独立实现 (不依赖 parse_service 的 uploader 依赖函数):
        #    - PDF: doc.extract_image(xref) → base_image["image"] (与旧 _parse_pdf_lightweight 第 654-655 行一致)
        #    - Office: python-docx/openpyxl/python-pptx 专用 API (与旧 _extract_docx/xlsx/pptx_images 一致)
        #    返回 [{image_bytes, image_format, placeholder}, ...]
        image_chunks = await asyncio.to_thread(_extract_image_bytes, temp_path, request.file_type)
        # 5. 组装 ParsedChunk 列表 (parents + children + images)
        chunks = _build_parse_chunks(parents, children, image_chunks)
        return ParseResponse(chunks=chunks)
    finally:
        os.unlink(temp_path)
```

**`_extract_image_bytes`** **实现** (独立实现, 不依赖 `parse_service` 的 `uploader` 依赖函数; 用各格式专用库 API):

```python
def _extract_image_bytes(file_path: str, file_type: str) -> list[dict]:
    """提取文档内嵌图片 bytes, 返回 [{image_bytes, image_format, placeholder}, ...]。
    独立实现: 不依赖 parse_service._extract_office_images (那些函数依赖 uploader)。
    PDF: doc.extract_image(xref) (与旧 _parse_pdf_lightweight 第 654-655 行一致)
    Office: python-docx/openpyxl/python-pptx 专用 API (与旧 _extract_docx/xlsx/pptx_images 一致)"""
    images = []
    if file_type == "pdf":
        import fitz
        doc = fitz.open(file_path)
        for page in doc:
            for img_info in page.get_images(full=True):
                xref = img_info[0]
                base_image = doc.extract_image(xref)  # 不用 fitz.Pixmap, 与旧代码一致
                images.append({
                    "image_bytes": base_image["image"],
                    "image_format": base_image["ext"],
                    "placeholder": "[图片](placeholder)",
                })
        doc.close()
    elif file_type == "docx":
        from docx import Document
        doc = Document(file_path)
        for rel in doc.part.rels.values():
            if "image" in rel.reltype:
                images.append({
                    "image_bytes": rel.target_part.blob,
                    "image_format": rel.target_part.ext.lstrip("."),
                    "placeholder": "[图片](placeholder)",
                })
    elif file_type == "xlsx":
        from openpyxl import load_workbook
        wb = load_workbook(file_path)
        for ws in wb.worksheets:
            for img in ws._images:
                images.append({
                    "image_bytes": img._data(),
                    "image_format": img.format or "png",
                    "placeholder": "[图片](placeholder)",
                })
        wb.close()
    elif file_type == "pptx":
        from pptx import Presentation
        from pptx.enum.shapes import MSO_SHAPE_TYPE
        prs = Presentation(file_path)
        for slide in prs.slides:
            for shape in slide.shapes:
                if shape.shape_type == MSO_SHAPE_TYPE.PICTURE:
                    images.append({
                        "image_bytes": shape.image.blob,
                        "image_format": shape.image.ext.lstrip("."),
                        "placeholder": "[图片](placeholder)",
                    })
    return images
```

**`_build_parse_chunks`** **实现**:

```python
def _build_parse_chunks(parents, children, image_chunks) -> list[ParsedChunk]:
    """组装 ParsedChunk 列表: parents (chunk_type='parent') + children (chunk_type='child') + images。
    摘要 (doc_summary) 不在 Parse RPC 中生成, 由 kb-service parse_orchestrator 调 Generate RPC 生成。
    child chunk 必须传递 parent_content + parent_chunk_id (用于 _return_parent_and_dedup 去重 key + 父块回填)。"""
    chunks = []
    for p in parents:
        chunks.append(ParsedChunk(
            chunk_id=p.chunk_id, content=p.content, content_type=p.content_type,
            page_number=p.page_number or 0, parent_content="", parent_chunk_id="",
            chunk_type="parent", metadata_json=json.dumps(p.metadata),
        ))
    for c in children:
        chunks.append(ParsedChunk(
            chunk_id=c.chunk_id, content=c.content, content_type=c.content_type,
            page_number=c.page_number or 0,
            parent_content=c.parent_content or "",       # child 的父块全文 (写入时 denormalized)
            parent_chunk_id=c.parent_chunk_id or "",       # child 的父块 ID (去重 key)
            chunk_type="child", metadata_json=json.dumps(c.metadata),
        ))
    for img in image_chunks:
        chunks.append(ParsedChunk(
            chunk_id=str(uuid.uuid4()), content=img["placeholder"],
            content_type="image", page_number=0, parent_content="", parent_chunk_id="",
            chunk_type="image", metadata_json="{}",
            image_bytes=img["image_bytes"], image_format=img["image_format"],
        ))
    return chunks
```

**2.3 Embed RPC 实现 (去 LlamaIndex)**:

`embeddings.py` 修改: 移除 `_as_base_embedding` 函数和 `_wrapped_model`。`get_embed_model()` 直接返回 `OpenAICompatibleEmbedding` (不再包 `BaseEmbedding`)。旧 Query RPC 如仍需 `BaseEmbedding`, 在 `qa_service.py` 内部自行包装 (标注 deprecated)。

```python
# app/services/embed_rpc_service.py

class EmbedRPCService:
    def embed(self, texts: list[str]) -> tuple[list[list[float]], int]:
        from app.core.embeddings import get_embed_model
        model = get_embed_model()  # 返回 OpenAICompatibleEmbedding (非 BaseEmbedding)
        vectors = model.get_text_embedding_batch(texts)  # 内部用 openai SDK
        dim = len(vectors[0]) if vectors else 0
        return vectors, dim
```

**2.4 Generate RPC 实现 (纯 Python openai SDK + CompactAndRefine 复现 + 超时 + 错误处理)**:

```python
# app/services/generate_rpc_service.py

import openai
from app.core.config import settings

class GenerateRPCService:
    """无状态 LLM 生成。纯 Python openai SDK, 不依赖 LlamaIndex。
    复现 LlamaIndex ContextChatEngine + CompactAndRefine 合成器行为。
    多轮历史由调用方传入 (含当前轮 user message, 复现旧行为)。"""

    DEFAULT_CONTEXT_TEMPLATE = (
        "Use the context information below to assist the user."
        "\n--------------------\n"
        "{context_str}"
        "\n--------------------\n"
    )

    DEFAULT_REFINE_TEMPLATE = (
        "Using the context below, refine the following existing answer"
        " using the provided context to assist the user."
        "\nIf the context isn't helpful, just repeat the existing answer"
        " and nothing more."
        "\n--------------------\n"
        "{context_msg}"
        "\n--------------------\n"
        "Existing Answer:\n"
        "{existing_answer}"
        "\n--------------------\n"
    )

    def generate(self, question, session_id, context, history,
                 inference_service_name="", max_tokens=2048):
        # 1. 上下文截断 (复现 CompactAndRefine._make_compact_text_chunks 的 repack 行为)
        #    PromptHelper.repack 将 chunks 打包到 context_window 内, 超大的拆分。
        #    简化复现: 粗略截断到 (context_window - max_tokens - overhead) chars
        context_str = "\n\n".join(c["content"] for c in context)
        max_context_chars = max(1, (settings.vllm_context_window - max_tokens - 200) * 4)
        if len(context_str) > max_context_chars:
            context_str = context_str[:max_context_chars]

        # 2. 构建 messages (复现 get_prefix_messages_with_context + _run_refine_loop)
        #    消息序列: [SYSTEM: context_template, *chat_history, USER: question]
        #    chat_history 含当前轮 user (旧行为: kb-service 先 append user 到 Redis
        #    再调 rag-engine, ChatMemoryBuffer 从 Redis 读到当前轮 user)
        #    question 作为最后一个 USER 消息 (即 {query_str} template)
        #    → 当前轮 user 在 chat_history 和 USER:question 中各出现一次 (旧行为, 复现)
        system_prompt = self.DEFAULT_CONTEXT_TEMPLATE.format(context_str=context_str)
        messages = [{"role": "system", "content": system_prompt}]
        for msg in history:
            messages.append({"role": msg["role"], "content": msg["content"]})
        # 末尾追加 question 为 USER 消息 (复现 {query_str} template, 与旧行为一致)
        messages.append({"role": "user", "content": question})

        # 3. 调用 vLLM (超时 120s, 与旧 OpenAILike timeout=120.0 一致)
        try:
            client = openai.OpenAI(
                base_url=settings.vllm_api_base,
                api_key=settings.vllm_api_key or "EMPTY",
                timeout=120.0,
            )
            response = client.chat.completions.create(
                model=settings.vllm_model,
                messages=messages,
                max_tokens=max_tokens,
            )
        except openai.APITimeoutError:
            raise TimeoutError("vLLM timed out")      # → gRPC DEADLINE_EXCEEDED
        except openai.APIConnectionError:
            raise RuntimeError("vLLM unavailable")    # → gRPC UNAVAILABLE
        except openai.APIError as e:
            raise RuntimeError(f"vLLM error: {e}")    # → gRPC INTERNAL

        # 4. 提取 answer + tokens
        #    旧路径用 _UsageCapturingHandler callback 捕获 (因 ContextChatEngine 丢弃 usage)
        #    新路径直接用 response.usage (openai SDK 不丢弃)
        answer = response.choices[0].message.content or ""
        input_tokens = response.usage.prompt_tokens if response.usage else 0
        output_tokens = response.usage.completion_tokens if response.usage else 0

        return GenerateResponse(answer=answer, input_tokens=input_tokens,
                                output_tokens=output_tokens, session_id=session_id)
```

**关键修正 — CompactAndRefine 复现说明**:

旧路径 `ContextChatEngine` 使用 `CompactAndRefine` 响应合成器, 它会:

1. `_make_compact_text_chunks`: 用 `PromptHelper.repack()` 将检索 chunk 打包到 context window
2. `_run_refine_loop`: 第一个 chunk 用 QA 模板生成初始 answer, 后续 chunk 用 refine 模板 (含 `existing_answer`) 迭代优化

**简化复现**: 对于常见的单 chunk 场景 (context < context\_window), CompactAndRefine 等价于单次 LLM 调用 (无 refine)。对于多 chunk 场景, 新路径用粗略截断 (`context_str[:max_context_chars]`) 替代精确 repack, 可能存在长文档边缘差异。**这是可接受的差异**, 原因:

- vLLM context\_window=32768, max\_tokens=2048, 实际 context 通常 < 30K chars → 单 chunk 场景占 >95%
- 粗略截断 (1 token ≈ 4 chars) 与 PromptHelper.repack 的 tokenizer 精确计算有差异, 但截断方向一致
- E2E 测试 (E2E-10) 验证 answer 语义一致; 若差异超阈值, 回退到保留 `llama-index-core` 的 `PromptHelper` 做精确 repack

**备选方案 (精确复现)**: 若 E2E 测试显示粗略截断导致差异, 在 Generate RPC 中保留 `llama-index-core` 的 `PromptHelper.repack()` 做精确 token 截断, 仅用 `repack` 方法, 不依赖 `CompactAndRefine` 类。

````

**关键修正 — vLLM 超时和错误处理**: 超时 120s 与旧 `OpenAILike(timeout=120.0)` 一致。`APITimeoutError` → `TimeoutError` → gRPC `DEADLINE_EXCEEDED`。`APIConnectionError` → `RuntimeError` → gRPC `UNAVAILABLE`。与旧 Query RPC 错误映射一致。

**关键修正 — token 计数等价**:
- **非流式 Generate**: 旧路径 `ContextChatEngine.chat()` 把 LLM 响应塌缩为 `AgentChatResponse`, 丢弃 usage, 因此用 `_UsageCapturingHandler` callback 从底层 `ChatResponse.raw` 提取。新路径用 `openai` SDK, `response.usage` 直接可用 (不丢弃), `response.usage.prompt_tokens` / `response.usage.completion_tokens` 即 vLLM 真实 usage。等价。
- **流式 GenerateStream**: vLLM 流式模式默认不在 chunk 中返回 usage。需加 `stream_options={"include_usage": True}`, vLLM 在最后一个 chunk 返回 `usage`。实现:
  ```python
  stream = client.chat.completions.create(
      model=..., messages=..., max_tokens=..., stream=True,
      stream_options={"include_usage": True},  # 最后 chunk 带 usage
  )
  for chunk in stream:
      if chunk.choices and chunk.choices[0].delta.content:
          yield GenerateToken(content=chunk.choices[0].delta.content, done=False)
      if chunk.usage:  # 最后一个 chunk
          yield GenerateToken(content="", done=True,
              input_tokens=chunk.usage.prompt_tokens,
              output_tokens=chunk.usage.completion_tokens)
````

**2.5 旧 Query RPC 标注**:

```python
async def Query(self, request, context):
    """[DEPRECATED] 旧全权 Query RPC。保留至步骤 11 删除。
    期间仍需 Milvus/PG 连接和 LlamaIndex (临时妥协)。"""
    ...  # 现有代码不变
```

**验证**:

- `make gen-proto`
- `python -m pytest tests/ -v` — 旧测试全通过
- 新增 `test_parse_rpc.py` — download\_url → chunks + 图片 bytes
- 新增 `test_embed_rpc.py` — texts → vectors\_flat + dimension + count
- 新增 `test_generate_rpc.py` — question + context + history (含当前轮 user) → answer + tokens; system prompt = DEFAULT\_CONTEXT\_TEMPLATE + DEFAULT\_REFINE\_TEMPLATE; 上下文截断复现 CompactAndRefine; 末尾追加 question 为 USER 消息 (复现 {query\_str}); 超时→DEADLINE\_EXCEEDED; response.usage token 提取
- 新增 `test_embeddings_no_llamaindex.py` — `get_embed_model()` 返回 `OpenAICompatibleEmbedding`

***

### 步骤 3: kb-service repo + CoreClient + rag-engine gRPC client

**目标**: kb-service 获得完整的编排能力基础设施。

**改动范围**:

- `migrations/004_kb_vector_store_id.sql` — 新增 `knowledge_bases.vector_store_id` 列
- `app/repositories/knowledge_base.py` — CreateKB 存储返回的 vector\_store\_id
- `app/repositories/chunk.py` — **改造** `keyword_search()` (非新增); **新增** `write_chunks()`, `delete_chunks_by_doc()`
- `app/core_api/client.py` — 新增 `insert_vector_documents()`, `search_vector_store()`, `upload_object()`; **复用**现有 `request_download_url()`, `delete_vector_store_documents()`
- `app/rag_engine/client.py` — **改造** 为 gRPC 客户端; 保留旧 REST `query()` deprecated
- `requirements.txt` — 新增 `jieba`, `grpcio`, `grpcio-tools`
- 新建 `app/services/` 目录 + `__init__.py`

**3.1 vector\_store\_id 持久化**:

```sql
-- migrations/004_kb_vector_store_id.sql
ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS vector_store_id TEXT;
```

**3.2 chunk repository — 改造 keyword\_search + 新增 write/delete**:

**关键修正 — keyword\_search 是改造现有函数, 非新增**:

当前 `chunk.py` 第 64-94 行已有 `keyword_search()`, 使用 `ILIKE` + `similarity()`。需改造为 jieba 分词 + 多 token OR + token 覆盖率归一化, 匹配 rag-engine `PgTrgmRetriever` 行为:

```python
# app/repositories/chunk.py — 改造现有 keyword_search (保持签名不变)

async def keyword_search(
    conn: asyncpg.Connection, *, tenant_id: str, kb_id: str,
    query: str, limit: int = 10,
) -> list[dict[str, Any]]:
    """[改造] pg_trgm + jieba 分词 + token 覆盖率归一化。
    与 rag-engine retrieve_service.py _execute_pg_trgm_search_tx (第 130-211 行) 逻辑一致。
    返回值格式不变: [{id, content, parent_content, file_name, page_number, score, ...}]
    score = token 覆盖率 (0~1), 与 rag-engine 归一化方式一致。"""
    import jieba
    # 分词: 与 rag-engine _tokenize_cn_keywords 一致 (第 117-127 行)
    seen: set[str] = set()
    tokens: list[str] = []
    for t in jieba.cut(query):
        if t and t not in seen:
            seen.add(t)
            tokens.append(t)
    if not tokens:
        return []
    n_tokens = len(tokens)
    # 参数编号: tokens 占 $1..$n, kb_id = $(n+1), tenant_id = $(n+2), top_k = $(n+3)
    # 与 rag-engine 第 176-198 行完全一致
    params: list[Any] = []
    where: list[str] = []
    score_exprs: list[str] = []
    hit_exprs: list[str] = []
    for i, tok in enumerate(tokens, start=1):
        params.append(tok)
        where.append(f"content % ${i}")
        sim_expr = f"coalesce(similarity(content, ${i}), 0)"
        score_exprs.append(sim_expr)
        hit_exprs.append(f"({sim_expr} > 0)::int")
    sum_sql = "(" + " + ".join(score_exprs) + ")"
    hits_sql = "(" + " + ".join(hit_exprs) + ")"
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        await conn.execute("SELECT set_config('pg_trgm.similarity_threshold', '0.0', true)")
        rows = await conn.fetch(
            f"""
            SELECT id::text AS chunk_id, content, parent_content,
                   parent_chunk_id::text AS parent_chunk_id,
                   doc_id::text AS doc_id, file_name, page_number,
                   content_type, chunk_type,
                   {sum_sql} AS sum_sim,
                   {hits_sql} AS n_hits
              FROM kb_chunks
             WHERE kb_id = ${n_tokens + 1} AND tenant_id = ${n_tokens + 2}
               AND chunk_type = 'child'
               AND ({ " OR ".join(where) })
            ORDER BY n_hits DESC, sum_sim DESC
            LIMIT ${n_tokens + 3}
            """,
            *(params + [uuid.UUID(kb_id), uuid.UUID(tenant_id), limit]),
        )
        # 归一化: score = min(1.0, n_hits / n_tokens) (与 rag-engine 第 209 行一致)
        out: list[dict[str, Any]] = []
        for r in rows:
            d = dict(r)
            d["score"] = min(1.0, (d["n_hits"] or 0) / n_tokens)
            out.append(d)
        return out
```

**新增 write\_chunks + delete\_chunks\_by\_doc** (从 rag-engine `repositories/chunks.py` 第 96-106 行迁移):

```python
# app/repositories/chunk.py — 新增

async def write_chunks(
    conn: asyncpg.Connection, *,
    tenant_id: str, kb_id: str, doc_id: str, file_name: str,
    parents: list[dict],       # ParentChunk 列表
    children: list[dict],      # ChildChunk 列表 (不含 summary)
    summaries: list[dict] | None = None,  # doc_summary 列表 (可选)
) -> int:
    """批量写入 kb_chunks。parent 先写, 再 child, 再 summary。
    SQL 与 rag-engine repositories/chunks.py write_chunks 完全一致。
    所有插入在单个事务内执行, RLS tenant context 设置一次。
    返回插入总行数。"""
    # 从 rag-engine app/repositories/chunks.py 第 96 行完整迁移

async def delete_chunks_by_doc(conn, *, tenant_id: str, kb_id: str, doc_id: str):
    """删除文档的所有 chunks。RLS 事务内执行。"""
```

**3.3 CoreClient 扩展**:

**关键修正 — 部分方法已存在, 复用而非新增**:

kb-service `core_api/client.py` 已有 `request_download_url` (第 185-194 行) 和 `delete_vector_store_documents` (第 150-160 行)。方案只需新增以下方法:

```python
# app/core_api/client.py — 新增

async def insert_vector_documents(self, vector_store_id, documents, idempotency_key):
    """POST /vector-stores/{id}/documents
    body: {idempotency_key, documents: [{id, content, vector, metadata}]}
    vector 字段为预计算向量 (Core API §1.4 修改后支持)"""

async def search_vector_store(self, vector_store_id, vector, top_k, filter_expr):
    """POST /vector-stores/{id}/search
    传入预计算向量, 返回 [{id, score, content, metadata}]
    content 字段为 Core API §1.4 修改后新增"""

async def upload_object(self, bucket_id, object_key, content_bytes, content_type):
    """两步上传 (Core API 不接受原始字节):
    1. POST /objects/upload → 获取 {upload_url, object_id}
    2. PUT {upload_url} body=content_bytes → 上传到对象存储
    复用现有 request_upload_url() 获取预签名 URL, 然后用 httpx PUT 上传"""

# 以下方法已存在, 复用, 无需新增:
# - request_download_url(object_id) → GET /objects/{id}/download → {download_url}
# - delete_vector_store_documents(vector_store_id, filter_expr) → DELETE /vector-stores/{id}/documents?filter=...
```

**3.4 rag-engine 客户端 — REST 改 gRPC**:

**关键修正 — 现有是 REST 客户端, 改造为 gRPC**:

```python
# app/rag_engine/client.py — 改造

# 保留旧 RagEngineClient (REST) 标记 deprecated
class RagEngineClient:
    """[DEPRECATED] 旧 REST 客户端, 仅供旧 Query 路径使用。"""
    async def query(self, ...) -> dict: ...

# 新增 gRPC 客户端
class RagEngineGRPCClient:
    """通过 gRPC 调用 rag-engine Parse/Embed/Generate/GenerateStream。"""

    def __init__(self, addr: str = "localhost:50052"):
        self._channel = grpc.aio.insecure_channel(addr)
        self._stub = rag_pb2_grpc.RagEngineStub(self._channel)

    async def parse(self, download_url: str, file_name: str, file_type: str,
                    chunk_size: int = 1024) -> list[dict]:
        """调用 Parse RPC (传 download_url)"""

    async def embed(self, texts: list[str]) -> tuple[list[list[float]], int]:
        """调用 Embed RPC。反序列化展平数组:
        vectors[i] = list(response.vectors_flat[i*dim:(i+1)*dim])"""

    async def generate(self, question, session_id, context, history,
                       inference_service_name="", max_tokens=2048) -> dict:
        """调用 Generate RPC。history 含当前轮 user message (复现旧行为)。
        Generate RPC 在 history 末尾追加 question 为 USER 消息 (复现 {query_str} template)。
        上下文截断复现 CompactAndRefine repack 行为。"""

    async def generate_stream(self, question, session_id, context, history, ...):
        """调用 GenerateStream RPC, 异步迭代器。
        history 含当前轮 user message (与 generate 一致)。"""
```

**3.5 新建 app/services/ 目录**:

kb-service 当前没有 `app/services/` 目录。步骤 4/5/8A 新增的文件放在此目录:

```
kb-service/app/services/
├── __init__.py
├── rrf.py                  # 步骤 4
├── retrieve_service.py     # 步骤 4
├── parse_orchestrator.py   # 步骤 5
└── query_orchestrator.py    # 步骤 8A
```

**3.6 依赖更新**:

```text
# requirements.txt — 新增
jieba>=0.42.1         # 关键词分词 (从 rag-engine 迁移到 kb-service)
grpcio>=1.60.0
grpcio-tools>=1.60.0
```

**验证**:

- `python -m pytest tests/ -v`
- 新增 `test_chunk_repository.py` — write\_chunks + keyword\_search (改造后) + delete
- 新增 `test_rag_grpc_client.py` — mock gRPC + EmbedResponse 展平反序列化

***

### 步骤 4: kb-service retrieve\_service + Python RRF

**目标**: kb-service 自己编排混合检索, 保证与 rag-engine RetrieveService 结果等价。

**改动范围**:

- 新增 `app/services/rrf.py` — RRF 纯 Python 实现
- 新增 `app/services/retrieve_service.py` — 混合检索编排器

**4.1 RRF 实现**:

```python
# app/services/rrf.py

def reciprocal_rank_fusion(rank_lists: list[list[tuple[str, float]]],
                            k: int = 60, top_n: int = 10) -> list[tuple[str, float]]:
    """与 LlamaIndex QueryFusionRetriever(mode='reciprocal_rerank', num_queries=1) 等价。"""
    scores = defaultdict(float)
    for rank_list in rank_lists:
        for rank, (chunk_id, _) in enumerate(rank_list):
            scores[chunk_id] += 1.0 / (k + rank + 1)
    return sorted(scores.items(), key=lambda x: -x[1])[:top_n]
```

**4.2 retrieve\_service 编排**:

```python
# app/services/retrieve_service.py

class RetrieveService:
    """kb-service 混合检索编排器。
    编排: rag-engine.Embed (query) → Core API 向量检索 → PG 关键词检索 → RRF → 父块回填。
    结果与 rag-engine RetrieveService 等价。"""

    def __init__(self, db_pool, core_client_factory, rag_engine_grpc_client, rrf_k=60):
        self._pool = db_pool
        self._core_client_factory = core_client_factory
        self._rag_engine = rag_engine_grpc_client
        self._rrf_k = rrf_k
        # parent_lookup: 用于 parent_content 为空时从 kb_chunks 回查父块 (与旧路径 make_parent_lookup_fn 等价)
        # 旧路径用 make_parent_lookup_fn(pool_or_dsn, tenant_id) 构建同步 lookup
        # 新路径简化: 直接用 kb-service 的 PG 连接查询, 不需要独立 lookup 类
        # 常见情况 parent_content 已在 metadata 中 denormalized (embed_service.py 第 94 行), backfill 是 no-op

    def _parent_lookup(self, parent_chunk_id: str, tenant_id: str) -> dict | None:
        """查询单个 parent chunk (与旧路径 _query_one 一致)。"""
        # 同步调用: 在 retrieve 内部用 asyncio.to_thread 包装
        # SELECT content FROM kb_chunks WHERE id = $1 AND chunk_type = 'parent'
        # RLS: set_config('app.current_tenant_id', tenant_id)
        pass  # 实现时从 chunk_repo.get_chunk 查询

    def _parents_lookup(self, doc_id: str, tenant_id: str) -> list[dict]:
        """查询文档的所有 parent chunks (与旧路径 _query_parents 一致)。"""
        # SELECT content FROM kb_chunks WHERE doc_id = $1 AND chunk_type = 'parent' ORDER BY id
        # RLS: set_config('app.current_tenant_id', tenant_id)
        pass  # 实现时从 chunk_repo.list_chunks_by_doc 过滤 chunk_type='parent'

    async def retrieve(self, tenant_id, kb_id, question, top_k=5,
                       score_threshold=0.3, retrieval_mode="hybrid",
                       vector_store_id=None) -> tuple[list[dict], float]:
        """返回 (sources, max_score)。max_score 用于无结果判断。"""

        vector_results = []
        kw_results = []
        vector_ranked = []
        kw_ranked = []

        if retrieval_mode in ("hybrid", "vector"):
            vectors, dim = await self._rag_engine.embed([question])
            query_vector = vectors[0]
            core = self._core_client_factory(tenant_id)
            vector_results = await core.search_vector_store(
                vector_store_id, query_vector, top_k * 2, filter_expr=None
            )
            vector_ranked = [(r["metadata"]["chunk_id"], r["score"]) for r in vector_results]

        if retrieval_mode in ("hybrid", "keyword"):
            async with self._pool.acquire() as conn:
                kw_results = await chunk_repo.keyword_search(
                    conn, tenant_id=tenant_id, kb_id=kb_id,
                    query=question, limit=top_k * 2
                )
            kw_ranked = [(str(r["chunk_id"]), r["score"]) for r in kw_results]

        if retrieval_mode == "hybrid":
            fused = reciprocal_rank_fusion(
                [vector_ranked, kw_ranked], k=self._rrf_k, top_n=top_k
            )
            results = self._build_sources_from_fusion(fused, vector_results, kw_results)
        elif retrieval_mode == "vector":
            results = self._process_vector_only(vector_results)
        else:
            results = self._process_keyword_only(kw_results)

        # 父块回填 (与旧路径 retrieve_service.py 第 912-918 行一致)
        # 常见情况: parent_content 已在 metadata 中 denormalized (embed_service.py 第 94 行), backfill 是 no-op
        # 边缘情况: metadata 为空时从 kb_chunks 回查父块
        for src in results:
            if src.get("chunk_type") == "child":
                if not src.get("parent_content"):
                    parent = await asyncio.to_thread(
                        self._parent_lookup, src.get("parent_chunk_id", ""), tenant_id
                    )
                    if parent:
                        src["parent_content"] = parent.get("content", "")
            elif src.get("chunk_type") == "doc_summary":
                if not src.get("parent_content"):
                    parents = await asyncio.to_thread(
                        self._parents_lookup, src.get("doc_id", ""), tenant_id
                    )
                    if parents:
                        src["parent_content"] = "\n".join(
                            p.get("content", "") for p in parents if p.get("content")
                        )

        deduped = self._return_parent_and_dedup(results)

        # hybrid score_threshold 归一化 (与旧 QAService 一致):
        # RRF 分数 (~0.016) 不可直接与 cosine threshold 比较
        # 旧路径: max_score = max(vector_similarity_map.values())
        # 新路径: max_score = max(vector_results cosine scores)
        if retrieval_mode == "hybrid":
            max_score = max((r["score"] for r in vector_results), default=0.0)
        elif retrieval_mode == "vector":
            max_score = max((r["score"] for r in vector_results), default=0.0)
        else:  # keyword
            max_score = max((r["score"] for r in kw_results), default=0.0)

        return deduped, max_score

    # ── 辅助方法 (从 rag-engine retrieve_service.py + qa_service.py 迁移) ──

    def _build_sources_from_fusion(self, fused, vector_results, kw_results):
        """将 RRF 融合结果 + 原始检索结果组装为 sources list。
        从 rag-engine retrieve_service.py + qa_service.py 迁移。
        hybrid 模式: chunk_id 在 vector_results 中 → 用 cosine score; 否则用 RRF score 归一化。
        vector_results 来自 Core API search (含 content, chunk_id 等字段, §1.4 修改后)。
        kw_results 来自 kb-service keyword_search (含 content, parent_content 等)。"""
        # 建立 chunk_id → vector_result 映射 (Core API 返回含 content + metadata)
        vec_map = {r["metadata"]["chunk_id"]: r for r in vector_results}
        kw_map = {str(r["chunk_id"]): r for r in kw_results}
        # RRF peak 用于归一化 keyword-only hits
        rrf_peak = max((score for _, score in fused), default=0.0)
        sources = []
        for chunk_id, rrf_score in fused:
            if chunk_id in vec_map:
                r = vec_map[chunk_id]
                score = r["score"]  # cosine similarity
                content = r.get("content", "")  # Core API §1.4 修改后返回 content
                parent_content = r.get("metadata", {}).get("parent_content", "")
                parent_chunk_id = r.get("metadata", {}).get("parent_chunk_id", "")
                doc_id = r.get("metadata", {}).get("doc_id", "")
                file_name = r.get("metadata", {}).get("file_name", "")
                page_number = int(r.get("metadata", {}).get("page_number", 0))
                chunk_type = r.get("metadata", {}).get("chunk_type", "child")
            elif chunk_id in kw_map:
                r = kw_map[chunk_id]
                # keyword-only: RRF score min-max 归一化 (与旧 qa_service 第 566-567 行一致)
                score = max(0.0, min(1.0, rrf_score / rrf_peak)) if rrf_peak > 0 else 0.0
                content = r.get("content", "")
                parent_content = r.get("parent_content", "")
                parent_chunk_id = r.get("parent_chunk_id", "")
                doc_id = r.get("doc_id", "")
                file_name = r.get("file_name", "")
                page_number = r.get("page_number", 0)
                chunk_type = r.get("chunk_type", "child")
            else:
                continue
            sources.append({
                "chunk_id": chunk_id,
                "doc_id": doc_id,
                "file_name": file_name,
                "page": page_number,
                "content": content,
                "parent_content": parent_content,
                "parent_chunk_id": parent_chunk_id,
                "chunk_type": chunk_type,
                "score": score,
            })
        return sources

    def _process_vector_only(self, vector_results):
        """vector 模式: 直接从 Core API 结果组装 sources。
        Core API search 响应含 content (§1.4 修改后)。"""
        return [{
            "chunk_id": r["metadata"]["chunk_id"],
            "doc_id": r["metadata"].get("doc_id", ""),
            "file_name": r["metadata"].get("file_name", ""),
            "page": int(r["metadata"].get("page_number", 0)),
            "content": r.get("content", ""),  # Core API §1.4 修改后返回
            "parent_content": r["metadata"].get("parent_content", ""),
            "parent_chunk_id": r["metadata"].get("parent_chunk_id", ""),
            "chunk_type": r["metadata"].get("chunk_type", "child"),
            "score": r["score"],
        } for r in vector_results]

    def _process_keyword_only(self, kw_results):
        """keyword 模式: 直接从 PG 结果组装 sources。"""
        return [{
            "chunk_id": str(r["chunk_id"]),
            "doc_id": r.get("doc_id", ""),
            "file_name": r.get("file_name", ""),
            "page": r.get("page_number", 0),
            "content": r.get("content", ""),
            "parent_content": r.get("parent_content", ""),
            "parent_chunk_id": r.get("parent_chunk_id", ""),
            "chunk_type": r.get("chunk_type", "child"),
            "score": r["score"],
        } for r in kw_results]

    def _return_parent_and_dedup(self, sources):
        """父块回填 + 去重 (与 rag-engine retrieve_service.py 第 539-562 行一致)。
        1. child chunk 且有 parent_content → content 替换为 parent_content
        2. doc_summary chunk → 不参与去重, 直接保留 (parent_content 已由 _backfill_parents_for_summary 填充)
        3. 同一 parent_chunk_id 的多个 child 去重为该 parent (保留 score 最高的)"""
        finalized = []
        best: dict[str, dict] = {}
        for src in sources:
            # child 且有 parent_content → 用 parent_content 替换 content (第 553-554 行)
            if src.get("chunk_type") == "child" and src.get("parent_content"):
                src["content"] = src["parent_content"]
            key = src.get("parent_chunk_id", "") or ""
            # 无 key 或非 child → 直接保留 (第 556-557 行)
            if not key or src.get("chunk_type") != "child":
                finalized.append(src)
                continue
            # 同 parent 去重, 保留 score 最高 (第 559-560 行)
            if key not in best or src["score"] > best[key]["score"]:
                best[key] = src
        finalized.extend(best.values())
        return finalized

    def _backfill_parent_for_child(self, source, lookup):
        """child chunk parent 回填 (与 retrieve_service.py 第 494-518 行一致)。
        若 parent_content 已存在 (写入时 denormalized) → 直接返回。
        否则从 kb_chunks 查 parent_chunk_id → 回填。"""
        if source.get("parent_content"):
            return source
        parent_chunk_id = source.get("parent_chunk_id", "")
        if not parent_chunk_id or not lookup:
            return source
        parent = lookup(parent_chunk_id)
        if parent:
            source["parent_content"] = parent.get("content", "")
        return source

    def _backfill_parents_for_summary(self, source, lookup):
        """doc_summary chunk → 回填该文档所有 parent blocks (与 retrieve_service.py 第 519-534 行一致)。
        拼接所有 parent content 到 parent_content。"""
        doc_id = source.get("doc_id", "")
        if not doc_id or not lookup:
            return source
        parents = lookup(doc_id)  # 返回该文档的所有 parent chunks
        if parents:
            source["parent_content"] = "\n".join(
                p.get("content", "") for p in parents if p.get("content")
            )
        return source
```

**关键修正 — hybrid score\_threshold 归一化**: 旧 QAService 对 hybrid 模式: RRF 分数 (\~0.016) 不能与 cosine threshold 比较, 用 `vector_similarity_map` 取真实 cosine, `max_score = max(cosine)`。新路径从 `vector_results` 取最大 cosine 做 threshold 判断, 保证过滤行为一致。

**验证**:

- `test_retrieve_service.py` — 三种模式 + RRF + hybrid 归一化
- `test_rrf.py` — 与 LlamaIndex 对比
- **等价性测试**: 同一 KB, 对比 sources chunk\_id 集合

***

### 步骤 5: kb-service parse\_orchestrator

**目标**: kb-service 自己编排文档解析管线, 保证与 rag-engine parse\_worker 等价。

**改动范围**:

- 新增 `app/services/parse_orchestrator.py`

**设计**:

```python
# app/services/parse_orchestrator.py

class ParseOrchestrator:
    """编排: 获取 download_url → rag-engine.Parse RPC → 图片上传 → Embed RPC
    → Core 向量插入 → PG kb_chunks 写入 → 摘要生成 → 状态更新"""

    def __init__(self, db_pool, core_client_factory, rag_engine_grpc_client):
        self._pool = db_pool
        self._core_client_factory = core_client_factory
        self._rag_engine = rag_engine_grpc_client

    async def process_document(self, tenant_id, kb_id, doc_id,
                                object_id, file_name, file_type,
                                chunk_size, vector_store_id):
        # 1. pending → parsing
        async with self._pool.acquire() as conn:
            await doc_repo.update_parse_status(conn, tenant_id, kb_id, doc_id, "parsing")

        try:
            core = self._core_client_factory(tenant_id)

            # 2. 获取 download_url (复用现有 request_download_url, 传给 rag-engine)
            download_url = await core.request_download_url(object_id)

            # 3. rag-engine.Parse RPC (传 download_url)
            chunks = await self._rag_engine.parse(download_url, file_name, file_type, chunk_size)

            # 4. 图片上传到 Core API, 替换占位符
            for chunk in chunks:
                if chunk.get("image_bytes"):
                    obj_id = await core.upload_object(
                        "kb-docs", f"{kb_id}/{doc_id}/{chunk['chunk_id']}",
                        chunk["image_bytes"], f"image/{chunk['image_format']}"
                    )
                    chunk["content"] = chunk["content"].replace("[placeholder]", obj_id)

            # 5. parsing → indexing
            async with self._pool.acquire() as conn:
                await doc_repo.update_parse_status(conn, tenant_id, kb_id, doc_id, "indexing")

            # 6. 摘要 (best-effort, 调 rag-engine.Generate RPC)
            parents = [c for c in chunks if c["chunk_type"] == "parent"]
            summary_chunk = await self._generate_summary(parents)

            # 7. 嵌入 (调 rag-engine.Embed RPC)
            # 7. rag-engine Embed RPC (child chunks + summary, 分别嵌入)
            child_chunks = [c for c in chunks if c["chunk_type"] == "child"]
            # summary chunk 单独嵌入, 不加入 child_chunks (避免 write_chunks 双重写入)
            embed_chunks = list(child_chunks)
            if summary_chunk:
                embed_chunks.append(summary_chunk)
            texts = [c["content"] for c in embed_chunks]
            vectors, dim = await self._rag_engine.embed(texts)

            # 8. Core API 插入向量 (传预计算向量)
            #    metadata 必须包含检索时所需的所有字段:
            #    - chunk_id: RRF 融合 key
            #    - chunk_type: _return_parent_and_dedup 判断
            #    - parent_content: 父块回填 (常见路径, embed_service.py 第 94 行 denormalized)
            #    - parent_chunk_id: _return_parent_and_dedup 去重 key
            #    - page_number, content_type: SourceChunk 构建
            #    与旧路径 embed_service.py _build_text_node metadata 一致 (第 86-97 行)
            documents = [{"id": c["chunk_id"], "content": c["content"], "vector": v,
                          "metadata": {"doc_id": doc_id, "file_name": file_name,
                                       "chunk_id": c["chunk_id"], "chunk_type": c["chunk_type"],
                                       "page_number": str(c.get("page_number", 0)),
                                       "content_type": c.get("content_type", "text"),
                                       "parent_content": c.get("parent_content", ""),
                                       "parent_chunk_id": c.get("parent_chunk_id", "")}}
                         for c, v in zip(embed_chunks, vectors)]
            await core.insert_vector_documents(
                vector_store_id, documents,
                idempotency_key=f"parse-{doc_id}",
            )

            # 9. 写入 kb_chunks (parents + children + summaries 分开传, 避免双重写入)
            async with self._pool.acquire() as conn:
                await chunk_repo.write_chunks(
                    conn, tenant_id=tenant_id, kb_id=kb_id, doc_id=doc_id,
                    file_name=file_name,
                    parents=parents,
                    children=child_chunks,  # 不含 summary
                    summaries=[summary_chunk] if summary_chunk else None,
                )

            # 10. indexing → ready
            async with self._pool.acquire() as conn:
                await doc_repo.update_parse_status(conn, tenant_id, kb_id, doc_id, "ready",
                                                   chunk_count=len(child_chunks))

        except Exception as e:
            error_msg = _sanitize_error(str(e))
            async with self._pool.acquire() as conn:
                await doc_repo.update_parse_status(conn, tenant_id, kb_id, doc_id, "failed", error_msg)

    async def _generate_summary(self, parents):
        """best-effort 摘要, 调 rag-engine.Generate RPC (用 summary prompt)。
        复现旧 SummaryService.summarize(parents) 行为:
        1. 取前 3 个 parent blocks 的 content 拼接
        2. LLM complete: "请总结以下内容为 200-500 字的摘要：\\n{content}"
        3. 失败返回 None (best-effort, 不阻塞)
        
        注意: 旧 SummaryService 用 LlamaIndex OpenAILike.complete() (completion 模式)。
        新路径用 Generate RPC 的 chat.completions.create (chat 模式)。
        差异: completion 模式发纯文本, chat 模式发 [{role: user, content: prompt}]。
        vLLM 对单轮 chat 和 completion 的输出通常等价 (同一 model + temperature=0)。
        E2E 验证: 摘要语义一致即可接受差异。
        """
        if not parents:
            return None
        try:
            # 取前 3 个 parent blocks (与旧 SummaryService.DEFAULT_SUMMARY_PARENT_COUNT 一致)
            combined = "\n".join(p["content"] for p in parents[:3] if p.get("content"))
            if not combined:
                return None
            # 复现旧 SummaryService prompt (summary_service.py 第 52-53 行)
            prompt = f"请总结以下内容为 200-500 字的摘要：\n{combined}"
            # 调 Generate RPC (context=[], history=[], question=prompt)
            result = await self._rag_engine.generate(
                question=prompt, session_id="", context=[], history=[],
                inference_service_name="", max_tokens=500,
            )
            summary = result["answer"].strip()
            if not summary:
                return None
            # 返回 summary chunk (与旧 SummaryService 返回 ChildChunk 一致)
            # write_chunks 期望 summaries 元素有: chunk_id, content, content_type,
            # page_number, parent_chunk_id, parent_content, token_count, metadata
            # (write_chunks 硬编码 chunk_type="doc_summary", 不需要传入)
            return {
                "chunk_id": str(uuid.uuid4()),
                "content": summary,
                "content_type": "text",
                "page_number": 1,
                "parent_chunk_id": None,
                "parent_content": None,
                "token_count": max(1, len(summary) // 2),
                "metadata": {},
            }
        except Exception as e:
            logger.warning("parse_orchestrator: summary failed: %s (degrading)", e)
            return None

    def _sanitize_error(self, error_msg: str) -> str:
        """清理错误信息 (移除敏感路径/凭据), 与旧 parse_worker 一致。"""
        import re
        # 移除文件路径
        error_msg = re.sub(r'/tmp/\S+', '[tempfile]', error_msg)
        error_msg = re.sub(r'C:\\\S+', '[tempfile]', error_msg)
        # 截断过长错误
        return error_msg[:500]
```

**关键修正 — ParseRequest 用 download\_url**: kb-service 不下载文档 bytes, 而是从 Core API 获取 `download_url` 传给 rag-engine。

**验证**:

- `test_parse_orchestrator.py` — mock Core API + rag-engine gRPC + PG
- **等价性测试**: 同一文档, 对比 kb\_chunks 行数和内容

***

### 步骤 6: kb-service NATS consumer (默认关闭)

**目标**: kb-service 消费 NATS parse 消息, 替代 rag-engine parse\_worker。默认关闭。

**改动范围**:

- 新增 `app/consumers/parse_consumer.py`
- `app/core/config.py` — 新增 `kb_parse_consumer_enabled` (默认 `false`)
- `main.py` — 按 flag 启动 consumer

**config.py**:

```python
kb_parse_consumer_enabled: bool = False
nats_parse_subject_v2: str = "ani.tasks.kb.parse.v2"  # 独立 subject 避免冲突
```

**Outbox Dispatcher**: 按 flag 切换 subject (`nats_parse_subject_v2` vs `nats_parse_subject`)。

**验证**:

- `test_parse_consumer.py` — mock NATS + orchestrator

***

### 步骤 7: 单测 + shadow/replay + 契约测试

**7.1 单元测试**:

| 测试文件                                                | 覆盖                                                                                                                                                                                |
| --------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `kb-service/tests/test_retrieve_service.py`         | 三种检索模式 + RRF + hybrid 归一化 + 父块回填                                                                                                                                                  |
| `kb-service/tests/test_rrf.py`                      | RRF 与 LlamaIndex 对比                                                                                                                                                               |
| `kb-service/tests/test_parse_orchestrator.py`       | 解析管线 + 状态机 + 摘要                                                                                                                                                                   |
| `kb-service/tests/test_parse_consumer.py`           | NATS 消费者 + 幂等 + flag                                                                                                                                                              |
| `kb-service/tests/test_chunk_repository.py`         | write\_chunks + keyword\_search (改造后) + delete                                                                                                                                    |
| `kb-service/tests/test_rag_grpc_client.py`          | gRPC 客户端 + EmbedResponse 展平反序列化                                                                                                                                                   |
| `rag-engine/tests/test_parse_rpc.py`                | Parse RPC (download\_url → chunks + 图片 bytes)                                                                                                                                     |
| `rag-engine/tests/test_embed_rpc.py`                | Embed RPC (vectors\_flat + dimension + count)                                                                                                                                     |
| `rag-engine/tests/test_generate_rpc.py`             | Generate RPC (history 含当前轮 user + 末尾追加 question + DEFAULT\_CONTEXT\_TEMPLATE + DEFAULT\_REFINE\_TEMPLATE + CompactAndRefine 截断 + 超时→DEADLINE\_EXCEEDED + response.usage token 提取) |
| `rag-engine/tests/test_embeddings_no_llamaindex.py` | get\_embed\_model() 返回 OpenAICompatibleEmbedding                                                                                                                                  |
| `pkg/adapters/runtime/vector_store_service_test.go` | Core 预计算向量                                                                                                                                                                        |

**7.2 Shadow/Replay**:

```python
# kb-service/tests/test_query_shadow.py
class TestQueryShadow:
    """主路径返回后, fire-and-forget 异步执行 shadow, 对比结果。
    Shadow 失败不影响主路径。KB_QUERY_SHADOW_MODE=true 开启。"""
    async def test_shadow_query_sources_overlap(self):
        """Jaccard > 90%"""
    async def test_replay_query(self):
        """录制旧路径, 新路径回放, 对比"""
```

**7.3 契约测试**:

```python
class TestRagEngineContract:
    async def test_parse_rpc_contract(self):       # download_url (非 bytes)
    async def test_embed_rpc_contract(self):        # vectors_flat + dimension + count
    async def test_generate_rpc_contract(self):     # history 含当前轮 user + 末尾追加 question (复现 {query_str}); system prompt = DEFAULT_CONTEXT_TEMPLATE + DEFAULT_REFINE_TEMPLATE; 上下文截断复现 CompactAndRefine
    async def test_source_chunk_has_chunk_id(self):  # chunk_id 字段存在
```

***

### 步骤 8A: 受控切换同步 Query (flag 可回滚)

**目标**: kb-service.Query RPC 用 flag 切换路径, 可回滚。

**改动范围**:

- `app/core/config.py` — `kb_query_use_new_path` (默认 `false`), `kb_query_shadow_mode` (默认 `false`)
- `app/api/grpc_server.py` — Query RPC 按 flag 选择 + shadow + 无结果优化
- 新增 `app/services/query_orchestrator.py`

**设计**:

```python
# app/api/grpc_server.py

NO_RESULT_ANSWER = "未检索到与问题相关的内容，无法回答。"  # 与 rag-engine qa_service.py 第 79 行一致

async def Query(self, request, context):
    # 1. 验证 + 创建 session
    # 2. 持久化当前轮 user 消息到 DB + Redis (与现有代码顺序一致)
    #    现有代码 (grpc_server.py 第 661-684 行): 先 DB 再 Redis, 然后调 rag-engine
    #    这意味着 Redis 在调 rag-engine 前已含当前轮 user message
    # 3. 按 flag 选择路径
    if settings.kb_query_use_new_path:
        # 新路径: 先 load_history (此时 Redis 已含当前轮 user, 与旧行为一致)
        history = await self._load_history(session_id, tenant_id)
        result = await self._query_new_path(request, kb_row, history)
        if settings.kb_query_shadow_mode:
            asyncio.ensure_future(self._query_shadow(...))
    else:
        result = await self._query_old_path(request, kb_row)  # 旧路径不变
        ...
    # 4. 持久化 assistant 消息 + 返回
```

```python
# app/services/query_orchestrator.py

class QueryOrchestrator:
    def __init__(self, retrieve_service, rag_engine_grpc_client, session_cache, db_pool):
        self._retrieve = retrieve_service
        self._rag_engine = rag_engine_grpc_client
        self._cache = session_cache
        self._pool = db_pool

    async def query(self, tenant_id, kb_id, question, session_id,
                    top_k, score_threshold, retrieval_mode,
                    inference_service_name, vector_store_id, history):
        # 1. retrieve → (sources, max_score)
        #    sources 已经过 dedup + 父块回填
        #    max_score: hybrid 模式取 max(vector cosine); vector 模式取 max(cosine); keyword 模式取 max(token coverage)
        sources, max_score = await self._retrieve.retrieve(
            tenant_id=tenant_id, kb_id=kb_id, question=question,
            top_k=top_k, score_threshold=score_threshold,
            retrieval_mode=retrieval_mode, vector_store_id=vector_store_id,
        )

        # 2. 无结果闸门 ①: 检索为空
        #    等价于旧 QAService 第 471-483 行: if not pre_nodes → NO_RESULT_ANSWER
        #    此时 LLM 未调用, tokens=0
        if not sources:
            return QueryResult(answer=NO_RESULT_ANSWER, sources=[],
                              session_id=session_id, input_tokens=0, output_tokens=0)

        # 3. 无结果闸门 ②: max_score < threshold
        #    等价于旧 QAService 第 522-533 行: if max_score < score_threshold → NO_RESULT_ANSWER
        #    此时 LLM 未调用, tokens=0
        if max_score < score_threshold:
            return QueryResult(answer=NO_RESULT_ANSWER, sources=[],
                              session_id=session_id, input_tokens=0, output_tokens=0)

        # 4. rag-engine.Generate RPC (LLM 调用在此之后)
        #    history 含当前轮 user message (复现旧行为: kb-service 先 append user 到 Redis
        #    再调 rag-engine, rag-engine ChatMemoryBuffer 从 Redis 读到当前轮 user)
        #    Generate RPC 在 history 末尾追加 question 为 USER 消息 (复现 {query_str} template)
        #    → 当前轮 user 在 chat_history 和 USER:question 中各出现一次 (旧行为, 复现)
        result = await self._rag_engine.generate(
            question=question, session_id=session_id, context=sources,
            history=history, inference_service_name=inference_service_name
        )

        # 5. 无结果闸门 ③: dedup 后 sources 为空 (在 LLM 调用后, 与旧路径一致)
        #    等价于旧 QAService 第 576-583 行: if not sources → NO_RESULT_ANSWER
        #    注意: 旧路径在此闸门时已调用 LLM, 返回 LLM 的实际 tokens (非 0)
        #    旧路径第 578-582 行: input_tokens=input_tokens, output_tokens=output_tokens
        #    新路径复现: 返回 Generate RPC 的实际 tokens
        if not sources:
            return QueryResult(answer=NO_RESULT_ANSWER, sources=[],
                              session_id=session_id,
                              input_tokens=result["input_tokens"],
                              output_tokens=result["output_tokens"])

        return QueryResult(answer=result["answer"], sources=sources,
                          session_id=session_id,
                          input_tokens=result["input_tokens"],
                          output_tokens=result["output_tokens"])
```

**关键修正说明**:

- **无结果三道闸门**: 旧 QAService 有三道无结果闸门 (qa\_service.py 第 471/522/576 行):
  1. `not pre_nodes` (检索为空, LLM 未调用) → NO\_RESULT\_ANSWER, tokens=0
  2. `max_score < score_threshold` (分数不够, LLM 未调用) → NO\_RESULT\_ANSWER, tokens=0
  3. `not sources` (dedup 后为空, **LLM 已调用**) → NO\_RESULT\_ANSWER, tokens=LLM 实际值
  新路径复现: 闸门 ①② 在 Generate 之前 (tokens=0); 闸门 ③ 在 Generate 之后 (tokens=LLM 实际值)。
  **注意**: 旧路径闸门 ③ 的 `sources` 来自 `engine.chat()` 后的 `source_nodes` 转换 + dedup, 而新路径的 `sources` 来自 `retrieve_service.retrieve()` 的 dedup。在极罕见情况下 (pre\_nodes 不为空但 dedup 后为空), 旧路径会调用 LLM 而新路径闸门 ①已拦截。**这是可接受的差异** (旧路径闸门 ③ 触发概率 <0.01%)。
- **chat\_history 含当前轮 user message + question 重复**: 旧路径中, kb-service `grpc_server.py` 的 Query RPC 执行顺序是:
  1. 持久化 user 消息到 DB (第 661-678 行)
  2. append user 消息到 Redis (第 680-684 行)
  3. 调 rag-engine query (第 697-708 行)
  这意味着 Redis 在调 rag-engine **之前**已 append 当前轮 user message。rag-engine 的 `ChatMemoryBuffer` 从 Redis 读取时 (`memory.get()`) 会读到当前轮 user message。然后 `ContextChatEngine.chat(question)` 内部通过 `get_prefix_messages_with_context` 在末尾追加 `USER: {query_str}` (即 question)。

  LLM 最终收到的消息序列:
  ```
  SYSTEM: DEFAULT_CONTEXT_TEMPLATE.format(context_str=...)
  [chat_history from Redis, 含当前轮 user]  ← 第一次出现
  USER: question                              ← 第二次出现 ({query_str} template)
  ```
  当前轮 user message **重复出现两次** — 这是旧行为的实际语义, 新路径必须**复现**:
  1. 持久化 user 到 DB + Redis
  2. `_load_history` (此时 Redis 含当前轮 user, 即 history 最后一条是当前轮 user)
  3. 调 Generate RPC (history 含当前轮 user, Generate RPC **仍然在末尾追加 question 为 USER 消息**, 复现 `{query_str}` template)
- **list\_messages 顺序**: Redis RPUSH 追加, `LRANGE 0 limit-1` 取最老 N 条 (cache.py 第 84 行)。但 `LTRIM -max -1` 保留最新 20 条 (cache.py 第 71 行)。当前 `list_messages` 用 `LRANGE 0 limit-1` 取的是 LTRIM 后列表中**最老**的前 N 条, 而旧 `ChatMemoryBuffer.get()` 取**最近** N 条 (`chat_history[-message_count:]`, chat\_memory\_buffer.py 第 125 行)。

  **修正**: `_load_history` 应改用 `LRANGE key -limit -1` (取最近 N 条), 与旧 `ChatMemoryBuffer` token\_limit 行为一致。

  **时序约束**: `list_messages` 的修改需在步骤 8A `kb_query_use_new_path=true` **之后**进行。原因: 旧路径 (rag-engine `ChatMemoryBuffer`) 直接从 Redis 读取, 不经过 kb-service `SessionCache.list_messages`, 因此不受影响。但 `list_messages` 也被 kb-service 其他功能使用 (如 history fallback), 需验证不影响旧路径。
  ```python
  async def _load_history(self, session_id, tenant_id):
      if self._cache:
          # 取最近 N 条 (与旧 ChatMemoryBuffer token_limit 一致)
          # 需修改 list_messages 内部为 LRANGE key -limit -1
          msgs = await self._cache.list_messages(session_id, limit=20)
          return [{"role": m["role"], "content": m["content"]} for m in msgs]
      # 回退到 DB
      async with self._pool.acquire() as conn:
          msgs = await msg_repo.list_session_messages(conn, tenant_id, session_id)
          return [{"role": m["role"], "content": m["content"]} for m in msgs]
  ```
- **token 计数等价**: 旧路径 `ContextChatEngine.chat()` 把 LLM 响应塌缩为 `AgentChatResponse`, 丢弃 usage, 因此用 `_UsageCapturingHandler` callback 从底层 `ChatResponse.raw` 提取。新路径用 `openai` SDK, `response.usage` 直接可用 (不丢弃)。流式 GenerateStream 需加 `stream_options={"include_usage": True}` 在最后 chunk 返回 usage。两者均从 vLLM 真实 usage 提取, 等价。

**验证**:

- flag=false: 现有测试全通过
- flag=true: 新路径单测通过
- 无结果测试: 三道闸门 ①② → `NO_RESULT_ANSWER` + `input_tokens=0`, LLM 未调用; ③ → `NO_RESULT_ANSWER` + `input_tokens=LLM实际值`, LLM 已调用
- 多轮测试: 第 2 轮 history 含第 1 轮 user+assistant **及第 2 轮 user**; Generate RPC 末尾追加 question (复现旧行为: user 出现两次)
- list\_messages 测试: `LRANGE -limit -1` 取最近 N 条 (非最老 N 条); 修改在新路径 flag=true 后进行
- prompt 等价测试: Generate RPC 的 system prompt = `DEFAULT_CONTEXT_TEMPLATE`; refine template = `DEFAULT_REFINE_TEMPLATE`; 上下文截断复现 CompactAndRefine

***

### 步骤 8B: E2E 与观察

**E2E 测试矩阵**:

| 测试     | 说明                                              | 等价性检查                                                                             |
| ------ | ----------------------------------------------- | --------------------------------------------------------------------------------- |
| E2E-1  | KB 创建 + 文档上传 + 解析                               | kb\_chunks 行数与旧路径一致                                                               |
| E2E-2  | Query 三种检索模式                                    | sources Jaccard > 90%                                                             |
| E2E-3  | Query 准确率                                       | answer 非空率一致                                                                      |
| E2E-4  | Query 无结果三道闸门 (①检索空 ②score\<threshold ③dedup后空) | ①② NO\_RESULT\_ANSWER + tokens=0, LLM 未调用; ③ NO\_RESULT\_ANSWER + tokens=LLM实际值   |
| E2E-5  | Query 延迟                                        | P99 < 旧路径 × 1.5                                                                   |
| E2E-6  | SSE 流式                                          | 事件序列不变                                                                            |
| E2E-7  | 删除文档 + 向量清理                                     | kb\_chunks + Milvus 向量均删除                                                         |
| E2E-8  | 多轮会话 Query                                      | 第 2 轮 history 含第 1 轮 + 第 2 轮 user; question 末尾追加 (复现旧行为: user 出现两次)               |
| E2E-10 | Generate prompt 等价                              | system prompt = DEFAULT\_CONTEXT\_TEMPLATE; 上下文截断复现 CompactAndRefine; answer 语义一致 |
| E2E-9  | flag 回滚                                         | 回滚后行为不变                                                                           |

***

### 步骤 9: NATS 切换

**操作步骤**:

1. 确认 `KB_PARSE_CONSUMER_ENABLED=true` 已部署且稳定
2. Outbox Dispatcher 切换到 `nats_parse_subject_v2`
3. 停止 rag-engine parse\_worker (`NATS_URL=""`)
4. 观察 outbox\_events 消费速度

**回滚**: 切回旧 subject + 重启 rag-engine parse\_worker。

***

### 步骤 10: SSE 切换 — kb-service.Retrieve + Gateway 改线

**目标**: SSE 从 Gateway 直连 rag-engine + vLLM 改为 Gateway → kb-service.Retrieve (gRPC stream)。

**改动范围**:

- `kb_service.proto` — 新增 `Retrieve` RPC (server streaming)
- 运行 `make gen-proto`
- `app/api/grpc_server.py` — 新增 `Retrieve` RPC
- `services/ani-gateway/internal/router/kb_sse.go` — 改为 gRPC stream 透传
- `services/ani-gateway/internal/router/kb_grpc_client.go` — 新增 `Retrieve` 方法
- 新增 `KB_SSE_USE_NEW_PATH` flag (默认 false)

**10.1 proto**:

```protobuf
service KBService {
  rpc Retrieve(RetrieveRequest) returns (stream RetrieveEvent);
}

message RetrieveRequest { ... }  // 同 QueryRequest 字段
message RetrieveEvent {
  oneof event {
    RetrieveTokenEvent token = 1;
    RetrieveSourcesEvent sources = 2;
    RetrieveDoneEvent done = 3;
    RetrieveErrorEvent error = 4;
  }
}
```

**10.2 kb-service Retrieve RPC**: 编排 retrieve → sources event → GenerateStream → token events → done event。session 管理和消息持久化与 Query RPC 一致 (含: 先持久化 user 到 DB+Redis, 再 load\_history 含当前轮 user, 再调 GenerateStream, 最后持久化 assistant)。无结果三道闸门与 Query RPC 一致。

**10.3 Gateway 改线**:

```go
func streamQueryKnowledgeBaseSSE(c *hertz.Context) {
    if !sseConfig.UseNewPath || kbClient == nil {
        streamQuerySSELegacy(c, sseConfig)  // 旧路径不变
        return
    }
    // 新路径: gRPC stream → SSE 转码
    stream, err := kbClient.Retrieve(ctx, req)
    if err != nil {
        writeSSEEmptyStream(c)  // 降级空流
        return
    }
    // 透传 token/sources/done/error events
}
```

**10.4 SSE 可选简化方案**:

如果性能测试显示 gRPC stream 透传延迟过高 (多一跳 kb-service), 可简化为: kb-service.Retrieve 只做检索 + session 管理, 返回 sources 后由 Gateway 直连 vLLM 流式生成 (等价于旧路径但检索走 kb-service)。此方案保留 Gateway 对 vLLM 的直连, 减少一跳。由 E2E 延迟测试决定是否采用。

**关键设计**:

- **Gateway 降级为透传** (或可选简化方案)
- **降级行为保持**: kb-service 不可用 → 空流
- **flag 控制**: `KB_SSE_USE_NEW_PATH` (默认 false)
- **事件序列不变**: token\* → sources → done

**验证**:

- `make gen-proto`
- Gateway 单测: `kb_sse_test.go` 适配
- E2E: `run_e2e_sse_test.py` 事件序列不变

***

### 步骤 11: 删除 rag-engine 旧路径 + 清 allowlist

**前置条件**:

- 步骤 8A/8B/9/10 全通过
- 新路径稳定 ≥ 1 周
- `kb_query_use_new_path=true`、`kb_parse_consumer_enabled=true`、`KB_SSE_USE_NEW_PATH=true`

**删除顺序** (依赖关系从上层到下层):

| 顺序 | 文件                                            | 原因                                                                            |
| -- | --------------------------------------------- | ----------------------------------------------------------------------------- |
| 1  | `rag-engine/app/services/qa_service.py`       | 旧 Query 入口, 依赖 retrieve/embed/summary                                         |
| 2  | `rag-engine/app/routers/query.py`             | REST query, 依赖 qa\_service                                                    |
| 3  | `rag-engine/app/routers/documents.py`         | REST 文档端点, 含 `DELETE /{kb_id}/documents/{doc_id}/index` (MilvusClient.delete) |
| 4  | `rag-engine/app/services/retrieve_service.py` | 直连 Milvus + PG                                                                |
| 5  | `rag-engine/app/services/embed_service.py`    | 直连 Milvus 写入                                                                  |
| 6  | `rag-engine/app/services/summary_service.py`  | 摘要移入 parse\_orchestrator                                                      |
| 7  | `rag-engine/app/workers/parse_worker.py`      | NATS consumer                                                                 |
| 8  | `rag-engine/app/repositories/chunks.py`       | asyncpg 直连 PG                                                                 |
| 9  | `rag-engine/app/clients/core_api.py`          | 不再直接下载                                                                        |
| 10 | `rag-engine/app/clients/minio_client.py`      | 直连 MinIO                                                                      |
| 11 | `rag-engine/app/core/milvus.py`               | 直连 Milvus                                                                     |
| 12 | `rag-engine/app/grpc/server.py`               | 移除旧 Query RPC                                                                 |
| 13 | `rag-engine/app/grpc/rag.proto`               | 移除 Query RPC 定义                                                               |
| 14 | `rag-engine/main.py`                          | 精简: 移除 Milvus/NATS/PG pool                                                    |
| 15 | `rag-engine/app/core/config.py`               | 精简: 移除 Milvus/PG/NATS/MinIO 配置                                                |
| 16 | `rag-engine/app/core/embeddings.py`           | 移除 `_as_base_embedding` (仅旧 Query 用)                                          |
| 17 | 运行 `make gen-proto`                           | 重新生成 stub                                                                     |

**documents.py 向量删除说明**: 步骤 3 删除 `documents.py` 时, 其 `DELETE /{kb_id}/documents/{doc_id}/index` 端点 (用 `MilvusClient.delete(expr=f'doc_id == "{doc_id}"')`) 的功能已由 kb-service `DeleteDocument` RPC (grpc\_server.py 第 583-587 行, 已存在) 调 Core API `delete_vector_store_documents(filter=f'doc_id == "{doc_id}"')` (core\_api/client.py 第 150-160 行, 已存在) 替代。Core API `DELETE /vector-stores/{id}/documents?filter=...` → `DeleteByExpr` → Milvus delete, 行为等价。

**保留的 rag-engine**:

```
rag-engine/
├── main.py                         # 精简
├── app/
│   ├── core/
│   │   ├── config.py               # 精简: embedding/vllm only
│   │   └── embeddings.py           # 精简: 无 LlamaIndex wrapper
│   ├── grpc/
│   │   ├── rag.proto               # Parse/Embed/Generate(+Stream)
│   │   ├── server.py               # 4 RPC
│   │   └── rag_pb2*.py
│   ├── services/
│   │   ├── parse_service.py        # 保留
│   │   ├── chunk_service.py        # 保留 (依赖 llama-index-core 的 SentenceSplitter)
│   │   ├── embed_rpc_service.py    # 保留
│   │   └── generate_rpc_service.py # 保留
│   └── clients/
│       └── ocr_api.py              # 保留
```

**allowlist 清理**:

- 移除 `pymilvus`、`asyncpg`、`nats-py`、`minio` (不再使用)
- 移除 `jieba` (关键词检索迁移到 kb-service)
- 移除 `redis` (旧 `ChatMemoryBuffer` 用 `RedisChatStore`, 步骤 11 删除旧 Query 后不再需要)
- `llama-index` → 降级为 `llama-index-core` 最小包 (PyPI 包名 `llama-index-core`, Python import `llama_index.core`; 仅 `SentenceSplitter`; Generate/Embed 已去 LlamaIndex; 旧 Query RPC 的 LlamaIndex 依赖随 qa\_service.py 一起删除)
- 保留 `httpx` (调 vLLM)、`openai` (Embed + Generate)、`PyMuPDF`/`python-docx`/`openpyxl`/`python-pptx` (Parse)

**验证**:

- `make gen-proto`
- `make validate-architecture`
- `make test`
- rag-engine 无 `pymilvus`/`asyncpg`/`minio`/`jieba`/`redis` import
- rag-engine 仅 `llama-index-core` (非完整 `llama-index`; import 名 `llama_index.core`)
- **最终等价性**: 全量 E2E 通过, Jaccard > 90%

***

## 4. 改造后架构合规性验证

| CLAUDE.md 规则                    | 改造前 | 改造后                            |
| ------------------------------- | --- | ------------------------------ |
| §3.2 Services 不 import Core 代码包 | ✅   | ✅                              |
| §3.3 Core 不调用 Services          | ✅   | ✅ (Core 不调 rag-engine; embedding 由 kb-service 编排 rag-engine 计算) |
| §3.1 Core 不含模型推理             | ✅   | ✅ (Core 只存储预计算向量; 无 EmbeddingProvider 端口) |
| §5.3 Services 不直接依赖 Milvus SDK  | ❌   | ✅                              |
| §5.3 Services 不直接依赖 MinIO SDK   | ❌   | ✅                              |
| §5.3 Services 不直接操作 PG          | ⚠️  | ✅ kb-service 拥有 PG             |
| §4.1 先改 API 契约再写实现              | —   | ✅ proto 先行                     |
| §8 Karpathy 原则二: 最小代码           | —   | ✅ RRF 20 行                     |
| §8 Karpathy 原则三: 只改需要改的         | —   | ✅ parse/chunk\_service 复用      |

***

## 5. 风险与缓解

| 风险                                   | 影响                              | 缓解                                                                                                           |
| ------------------------------------ | ------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| Core API 修改 (vector/content 字段)      | 现有调用方行为变化                       | omitempty 可选字段, 不传时行为不变; Go 测试验证                                                                             |
| 新检索路径准确率下降                           | Query 质量变差                      | shadow Jaccard > 90%; flag 回滚                                                                                |
| Core 向量检索延迟增加                        | P99 升高                          | kb-service 预计算向量传 Core                                                                                       |
| parse 管线不稳定                          | 解析失败率升高                         | flag 回滚; E2E 验证                                                                                              |
| SSE 新路径中断                            | 流式体验下降                          | flag 回滚; 降级空流; 可选简化方案                                                                                        |
| 多轮会话历史不一致                            | 上下文丢失                           | history 含当前轮 user (复现旧行为); list\_messages 改 LRANGE -limit -1; E2E 多轮测试                                       |
| CompactAndRefine 粗略截断 vs 精确 repack   | 长文档 answer 语义偏移                 | 单 chunk 场景精确等价; 多 chunk 场景粗略截断 (1 token ≈ 4 chars); E2E-10 验证; 备选: 保留 llama-index-core PromptHelper.repack() |
| prompt 不等价                           | Generate answer 语义偏移            | DEFAULT\_CONTEXT\_TEMPLATE + DEFAULT\_REFINE\_TEMPLATE 原文复现; E2E-10 prompt 等价测试                              |
| 旧 Query RPC 删除过早                     | 回滚不可用                           | 步骤 11 前 ≥ 1 周稳定期                                                                                             |
| 去 LlamaIndex 后行为差异                   | Generate 结果变化                   | 同一 vLLM 端点/model + 同一 prompt + 同一消息序列; E2E answer 非空率对比                                                      |
| SentenceSplitter 依赖 llama-index-core | 步骤 11 无法完全移除 llama-index        | 降级为 llama-index-core 最小包 (仅 SentenceSplitter)                                                                |
| redis 依赖移除                           | 旧 Query RPC ChatMemoryBuffer 失效 | 步骤 11 删除旧 Query RPC 后移除 redis; 期间保留                                                                          |

***

## 6. 依赖关系

```
步骤 1 (Core vector) ──┐
步骤 2 (rag-engine RPC) ──┤
                         ├──→ 步骤 3 (kb-service infra)
                         │         ├──→ 步骤 4 (retrieve_service)
                         │         ├──→ 步骤 5 (parse_orchestrator)
                         │         │         └──→ 步骤 6 (NATS consumer)
                         │         └──→ 步骤 7 (单测 + shadow)
                         │                   ├──→ 步骤 8A (Query flag)
                         │                   │         └──→ 步骤 8B (E2E)
                         │                   │                   ├──→ 步骤 9 (NATS)
                         │                   │                   ├──→ 步骤 10 (SSE)
                         │                   │                   └──→ 步骤 11 (删除旧路径)
步骤 1 (Core 预计算向量) ──┘ (步骤 4 依赖)
```

步骤 1、2 可并行。步骤 3 依赖 1+2。步骤 4、5 可并行 (依赖 3)。步骤 6 依赖 5。步骤 7 依赖 4+5+6。步骤 8A 依赖 7。步骤 8B 依赖 8A。步骤 9、10 依赖 8B。步骤 11 依赖 9+10。
