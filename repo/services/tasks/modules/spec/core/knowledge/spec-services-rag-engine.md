# SPEC: rag-engine 厚 AI 层 (P0)

> Technical specification derived from:
> - PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-006,011,012,013,014,015,018)
> - UX: N/A — backend-only
> Generated: 2026-07-23 | Target branch: main | Product line: core (Services / rag-engine)

## 1. Summary

### 1.1 What This SPEC Covers
扩展 `repo/ai/rag-engine/`（Python 3.11 + FastAPI + LlamaIndex 0.11+），实现：文档解析（DoclingReader + AI 服务 PaddleOCR API）、父子分块 + 文档级摘要、HuggingFaceEmbedding 嵌入 + MilvusVectorStore 直连（v1.2 架构）、混合检索（向量 + pg_trgm + RRF + 父块回填）、RAG 同步问答（ContextChatEngine + RedisChatStore）、NATS 订阅 parse_worker、gRPC server 承接 Query。含 US-006 AI 服务 OCR API 端点（扩展现有 inference-service）与 US-018 异步链路端到端验证。

### 1.2 PRD Reference
- Source: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX source: N/A — backend-only
- User Stories covered: US-006, US-011, US-012, US-013, US-014, US-015, US-018
- Functional Requirements covered: FR-3, FR-4, FR-5, FR-6, FR-7, FR-10, FR-11, FR-12

### 1.3 Design Decisions Summary
| Decision | Choice | Rationale |
|----------|--------|-----------|
| LlamaIndex 依赖 | llama-index-core + readers-docling + embeddings-huggingface + llms-openai-like + vector-stores-milvus + pymilvus | PRD §6 + US-011 强制；移除旧 pymilvus/langchain 直用 |
| 向量存储 | MilvusVectorStore 直连 Milvus，经 VectorStoreIndex 包装嵌入 | v1.2 架构优化，移除 CoreAPIVectorStore 适配器，减少一层 HTTP |
| OCR 部署 | 扩展现有 inference-service 新增 OCR 推理 RPC，后端 PaddleOCR PP-OCRv4 | 用户决策：复用 inference-service 部署框架 |
| 嵌入统一 | 写入与查询嵌入均由 rag-engine HuggingFaceEmbedding 完成 | v1.2 已移除 Core 端嵌入 |
| 检索 | QueryFusionRetriever（MilvusVectorStore as retriever + pg_trgm BaseRetriever 子类 + RRF），num_queries=1 | US-014 强制 |
| 问答 | ContextChatEngine + ChatMemoryBuffer(RedisChatStore) + OpenAILike(api_base=vllm) | US-014 强制 |
| SSE 归属 | rag-engine 仅同步 Query；SSE 在 ani-gateway | PRD Non-Goals 强制 |

---

## 2. Architecture

### 2.1 System Context
rag-engine 是厚 AI 层。订阅 NATS `ani.tasks.kb.parse` 领取解析任务，经 Core `/objects/{id}/download` 下载文档 → 解析 → 分块 → 摘要 → 直连 Milvus 写入 + 写 kb_chunks 表 → 回写 kb_documents.parse_status。对上经 gRPC 承接 kb-service Query（同步）。

```
NATS ani.tasks.kb.parse → rag-engine parse_worker
   ├─ Core /objects/{id}/download (下载文档)
   ├─ parse_service (DoclingReader + AI 服务 OCR API)
   ├─ chunk_service (父子分块)
   ├─ summary_service (LLM 摘要)
   ├─ embed_service (HuggingFaceEmbedding)
   ├─ MilvusVectorStore 直连写入
   ├─ kb_chunks 表写入 (Core DB)
   └─ 回写 kb_documents.parse_status

kb-service Query gRPC → rag-engine gRPC server
   ├─ retrieve_service (QueryFusionRetriever)
   └─ qa_service (ContextChatEngine → vLLM)
```

### 2.2 Component Design
- **parse_service**：DoclingReader 解析多格式，扫描页调 AI 服务 OCR API，表格转 HTML，图片提取上传 MinIO
- **chunk_service**：SentenceSplitter 子块 256-512 + 固定窗套叠父块 2048
- **summary_service**：拼接前 N 父块 → LLM 生成 200-500 字摘要 → 向化存 Milvus（chunk_type=doc_summary）
- **embed_service**：HuggingFaceEmbedding 动态加载，写入/查询统一
- **retrieve_service**：QueryFusionRetriever（向量 + pg_trgm + RRF + 父块回填）
- **qa_service**：ContextChatEngine + RedisChatStore，同步返回 answer+sources+session_id+tokens
- **parse_worker**：NATS 订阅 + 任务领取 + 编排上述服务
- **grpc_server**：实现 Query RPC（同步）
- **inference-service OCR**：新增 OCR 推理端点（PaddleOCR PP-OCRv4）

### 2.3 Module Interactions
1. parse_worker 领取任务 → Core download → parse_service → chunk_service → summary_service
2. embed_service 嵌入子块 + 摘要 → MilvusVectorStore.add（经 VectorStoreIndex 包装嵌入）
3. 写 kb_chunks 表（子块 parent_chunk_id 指父块，父块 parent_content 存子块）
4. 回写 kb_documents.parse_status（pending→parsing→indexing→ready/failed）
5. Query gRPC → retrieve_service 检索子块 + 回填父块 → qa_service ContextChatEngine → vLLM → 返回

### 2.4 File Structure
```
repo/ai/rag-engine/
├── requirements.txt                 [MODIFY: 迁移到 LlamaIndex]
├── main.py                           [MODIFY: 启动 gRPC server + parse_worker]
├── app/
│   ├── routers/query.py              [MODIFY 或废弃: 改为 gRPC server]
│   ├── routers/documents.py          [MODIFY 或废弃]
│   ├── core/
│   │   ├── config.py                 [MODIFY: 新增 vllm/redis/nats/pg/embed 配置]
│   │   ├── milvus.py                 [MODIFY: MilvusVectorStore 封装]
│   │   └── embeddings.py            [MODIFY: HuggingFaceEmbedding]
│   ├── services/
│   │   ├── parse_service.py          [NEW: US-011]
│   │   ├── chunk_service.py          [NEW: US-012]
│   │   ├── summary_service.py        [NEW: US-012]
│   │   ├── embed_service.py          [NEW: US-013]
│   │   ├── retrieve_service.py       [NEW: US-014]
│   │   └── qa_service.py             [NEW: US-014]
│   ├── workers/
│   │   └── parse_worker.py           [NEW: US-015 NATS 订阅]
│   ├── grpc/
│   │   └── server.py                 [NEW: Query RPC]
│   ├── clients/
│   │   ├── core_api.py               [NEW: objects download]
│   │   └── ocr_api.py                [NEW: AI 服务 OCR]
│   └── repositories/
│       └── chunks.py                 [NEW: kb_chunks 写入]
└── tests/                            [NEW/扩展]

repo/services/inference-service/      [MODIFY: US-006 新增 OCR RPC]
```

---

## 3. Data Model

### 3.1 Schema Changes
rag-engine 不新建表，写入 kb-service 管理的 `kb_chunks` 表（见 spec-services-kb-service.md §3.1）。Milvus 集合 schema：

**Milvus 集合 `kb_{kb_id 去横杠}`：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | VARCHAR(64) | 主键（chunk_id） |
| `embedding` | FLOAT_VECTOR(dim=embedding_dim) | 嵌入向量，HNSW 索引，metric=COSINE，M=16，efConstruction=200 |
| `doc_id` | VARCHAR(64) | 文档 ID（过滤/删除用） |
| `kb_id` | VARCHAR(64) | 知识库 ID |
| `tenant_id` | VARCHAR(64) | 租户 ID |
| `chunk_type` | VARCHAR(16) | child / parent / doc_summary |
| `parent_content` | VARCHAR(8192) | 父块上下文（命中子块时回填） |
| `file_name` | VARCHAR(256) | — |
| `page_number` | INT | — |
| `content_type` | VARCHAR(32) | text/table/image/code |

### 3.2 Entity Definitions
对齐 kb_chunks 表（见 spec-services-kb-service.md §3.2）。SourceChunk 对齐 proto。

### 3.3 Relationships
- Milvus 集合 1:1 对应 KB（由 kb-service CreateKB 调 Core vector-store 创建集合；rag-engine 负责文档级写入）
- kb_chunks.parent_chunk_id 自引用

### 3.4 Migration Plan
无 rag-engine 侧迁移。依赖 kb-service US-005 kb_chunks 表先迁移就绪。

---

## 4. API Design

### 4.1 gRPC Endpoints（rag-engine 内部，承接 kb_service.proto Query）

| RPC | Request | Response | 说明 |
|-----|---------|----------|------|
| Query | QueryRequest | QueryResponse | 同步 RAG 问答（与 kb_service.proto Query 同构） |

> rag-engine gRPC server 复用 `kb_service.proto` 的 Query 消息或定义等价内部 proto，由实现时确认。本 SPEC 以 kb_service.proto QueryRequest/QueryResponse 为契约。

### 4.2 AI 服务 OCR API（US-006，扩展现有 inference-service）

| Method | Path | Description | Request | Response |
|--------|------|-------------|---------|----------|
| POST | `/v1/ocr` (或 gRPC RPC) | OCR 推理 | image bytes + params | OCRResult |

**OCR Request：**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `image` | bytes | yes | 图片二进制 |
| `lang` | string | no | 默认 `ch` |
| `use_angle_cls` | bool | no | 默认 true |

**OCR Response（OCRResult）：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `regions` | array | 版面区域分类（text/table/figure） |
| `regions[].type` | string | text / table / figure |
| `regions[].text` | string | 文字内容 |
| `regions[].table_html` | string \| null | 表格 HTML（type=table） |
| `ocr_confidence` | float | 整体置信度 |

### 4.3 Request/Response Schemas
Query 见 kb_service.proto。OCR 见 §4.2。

### 4.4 Error Responses
| gRPC Code | Condition |
|-----------|----------|
| `INVALID_ARGUMENT` | question 空 / top_k 越界 |
| `NOT_FOUND` | kb_id 集合不存在 |
| `UNAVAILABLE` | vLLM / Milvus 不可用 |
| `DEADLINE_EXCEEDED` | LLM 超时 |

OCR HTTP/gRPC 错误：400（图片非法）、413（图片过大）、503（PaddleOCR 不可用）。

### 4.4 Breaking Changes
无。新增 OCR 端点为 additive；rag-engine 内部重构（移除旧 router stub）不影响外部契约（gateway 经 kb-service 调用）。

---

## 5. Business Logic

### 5.1 Core Algorithms

**parse_service（US-011）：**
```
for page in doc.pages:
    text = page.extract_text()
    if len(text) < 50:  # 扫描页
        ocr_result = ocr_api(image=page.to_image(), lang='ch', use_angle_cls=True)
        # 用 ocr_result.regions 重建文本 + 表格 HTML
    tables → HTML; 跨页表格按页拆分; 表格 > 2048 tokens 按行分组保留表头
    images → 上传 MinIO → 插入 [图片: caption](OSS_URL) 占位节点
```

**chunk_service（US-012）：**
```
splitter = SentenceSplitter(chunk_size=512, chunk_overlap=0)  # 子块 256-512，优先句子边界
child_chunks = splitter.split(text)  # 单句超 chunk_size 强制截断
# 固定窗套叠父块：连续子块累积到 2048 tokens 归为一个父块
parent = accumulate_children_until(child_chunks, target_tokens=2048)
for child in parent.children:
    child.parent_chunk_id = parent.id
    child.parent_content = parent.full_text
# 图片链接/表格/代码块/超链接作为不可切断单元
```

**summary_service（US-012）：**
```
first_n_parents = parents[:N]
combined = "\n".join(p.full_text for p in first_n_parents)
summary = llm.generate(f"总结以下内容为 200-500 字摘要：\n{combined}")
embed summary → Milvus.add(chunk_type='doc_summary')
# 失败不阻断入库（降级为仅父子分块，记录 warning）
```

**embed_service（US-013）：**
```
embed_model = HuggingFaceEmbedding(model_name=settings.embedding_model)
vector_store = MilvusVectorStore(uri=..., collection_name=f"kb_{kb_id_no_dash}",
                                  index_type='HNSW', metric_type='COSINE', M=16, efConstruction=200)
index = VectorStoreIndex.from_vector_store(vector_store, embed_model=embed_model)
# 写入：index.insert_nodes(nodes)  # Index 层嵌入后调 vector_store.add
# 查询：retriever = index.as_retriever(similarity_top_k=top_k)
```

**retrieve_service（US-014）：**
```
vector_retriever = VectorStoreIndex.from_vector_store(milvus_vs, embed_model).as_retriever(similarity_top_k=top_k)
keyword_retriever = PgTrgmRetriever(pg_conn, kb_id, top_k)  # BaseRetriever 子类
fusion = QueryFusionRetriever(retrievers=[vector_retriever, keyword_retriever],
                              num_queries=1,  # 关闭查询生成
                              mode='reciprocal_reranking')  # RRF
nodes = fusion.retrieve(question)
# 命中子块 → 回填 parent_content；命中 doc_summary → 回填该文档父块
```

**qa_service（US-014）：**
```
chat_store = RedisChatStore(redis_url=...)
memory = ChatMemoryBuffer(chat_store=chat_store, session_id=session_id)
llm = OpenAILike(model=..., api_base=vllm_url, api_key="...", is_chat_model=True, context_window=...)
engine = ContextChatEngine.from_defaults(retriever=fusion_retriever, memory=memory, llm=llm)
result = engine.chat(question)
# 返回 answer + sources + session_id + tokens
```

**parse_worker（US-015）：**
```
subscribe('ani.tasks.kb.parse')
on_msg(msg):
    payload = json(msg)
    update kb_documents.parse_status='parsing'
    doc = core_api.download(payload.storage_path)
    nodes = parse_service(doc)
    parents, children = chunk_service(nodes)
    summary = summary_service(parents)  # best-effort
    embed_and_write(parents, children, summary)  # Milvus + kb_chunks
    update kb_documents.parse_status='ready'
on_err:
    update kb_documents.parse_status='failed', error_message=...
```

### 5.2 Validation Rules
- chunk_size 256-512，父块 2048（配置来自 KB config）
- top_k 1-20，score_threshold 0.0-1.0
- OCR confidence < 阈值 → 标记 warning，不阻断
- 摘要生成失败 → 降级，不阻断入库

### 5.3 State Machine
parse_status 回写：`pending → parsing → indexing → ready | failed`。failed 可被 reparse 触发回 pending（由 kb-service outbox 重新派发）。

### 5.4 Edge Cases
- 扫描页全部无文字 → 全 OCR
- 表格跨页 → 按页拆分（不合并）
- 大表 > 2048 tokens → 按行分组保留表头
- 图片提取失败 → 占位节点写 `[图片: 提取失败]`
- 摘要 LLM 超时 → 降级，记录 warning
- 检索 max_score < score_threshold → 返回 no-result（不幻觉）
- 解析任务重复派发（at-least-once）→ 按 doc_id 幂等（检查 parse_status，若已 ready 则跳过或按需重解析）

---

## 6. Error Handling

### 6.1 Error Taxonomy
| Error Code | Condition | Handling |
|------------|-----------|----------|
| `doc.parse_failed` | 解析失败 | parse_status=failed + error_message，可重试 |
| `inference.unavailable` | vLLM/OCR 不可用 | 503；parse 失败标记；query 返回 503 |
| `doc.unsupported_type` | 不支持格式 | parse_status=failed |
| LLM 超时 | query | DEADLINE_EXCEEDED，返回 503 |

### 6.2 Retry Strategy
- parse 失败：kb-service 可调 reparse 重新派发（idempotent by doc_id）
- query LLM 超时：不自动重试，返回 503（客户端可复用 idempotency_key 重试）
- OCR 单页失败：重试 1 次，仍失败标记该页

### 6.3 Failure Modes
- Milvus 不可用：parse 写入失败 → parse_status=failed；query 返回 503
- vLLM 不可用：query 返回 503
- Core object download 失败：parse_status=failed
- 摘要失败：降级（仅父子分块）

---

## 7. Security

### 7.1 Authentication & Authorization
- rag-engine 内部服务，不直接面向用户；由 kb-service/gateway 负责 RBAC
- 调用 Core API 使用服务账号
- 调用 AI 服务 OCR 使用内部认证

### 7.2 Input Validation
- 文档格式白名单（pdf/docx/xlsx/pptx/md/txt）
- OCR 图片大小限制
- question 长度 1-2000

### 7.3 Data Protection
- 文档内容经 Core download 临时处理，不持久化明文（仅向量化 + kb_chunks content）
- 向量存储租户隔离（集合名含 kb_id，查询带 tenant_id 过滤）

---

## 8. Performance

### 8.1 Expected Load
- parse：异步，取决于上传频率；单文档解析 10s-120s
- query：同步，p95 < 3s（检索 < 500ms + LLM < 2.5s）

### 8.2 Optimization Strategy
- 嵌入批量编码（多 chunk 一次）
- Milvus HNSW 索引（M=16, efConstruction=200, 查询 ef 动态）
- RRF 融合 num_queries=1 关闭查询生成（减少 LLM 调用）
- Redis 会话缓存避免重复加载历史

### 8.3 Database Considerations
- kb_chunks 写入批量 INSERT
- pg_trgm 关键词检索走 GIN 索引
- 查询 kb_chunks 按 doc_id / parent_chunk_id 索引

---

## 9. Testing Strategy

### 9.1 Unit Tests
- parse_service：各格式解析 + 扫描页 OCR 调用 + 表格 HTML + 图片占位
- chunk_service：句子边界 + 2048 套叠 + 不可切断单元
- summary_service：拼接 + LLM mock + 降级路径
- embed_service：HuggingFaceEmbedding mock
- retrieve_service：向量 mock + pg_trgm mock + RRF 融合 + 父块回填
- qa_service：ContextChatEngine mock + tokens 统计

### 9.2 Integration Tests
- parse_worker 端到端：NATS mock → Core download mock → 解析 → 分块 → 摘要 → Milvus(mock) + kb_chunks(测试 DB) → 回写 parse_status
- Query gRPC：retrieve mock + LLM mock → 返回 answer+sources+session_id
- OCR API：PaddleOCR mock → 返回 regions + table_html + confidence

### 9.3 Edge Case Tests
- 全扫描页文档（OCR 路径）
- 跨页表格拆分
- 大表按行分组
- 摘要失败降级
- 检索无命中（score < threshold）→ no-result
- 重复派发幂等

### 9.4 Acceptance Criteria Mapping
| US/FR | Test | Type | Description |
|-------|------|------|-------------|
| US-006 AC1-6 | test_ocr_api | integration | OCR RPC + lang/angle + regions + table_html + confidence + rag-engine client |
| US-011 AC1-6 | test_parse_service | integration | LlamaIndex 依赖 + DoclingReader + OCR + 表格 + 图片 |
| US-012 AC1-8 | test_chunk_and_summary | integration | SentenceSplitter + 2048 套叠 + parent_content + kb_chunks + 摘要 + 降级 |
| US-013 AC1-6 | test_embed_and_milvus | integration | HuggingFaceEmbedding + MilvusVectorStore + VectorStoreIndex + 集合命名 + HNSW |
| US-014 AC1-6 | test_retrieve_and_qa | integration | QueryFusionRetriever + RRF + 父块回填 + ContextChatEngine + RedisChatStore + OpenAILike |
| US-015 AC1-5 | test_parse_worker_and_grpc | integration | NATS 订阅 + 链路 + parse_status 回写 + Query RPC |
| US-018 AC1-6 | test_e2e_async_chain | e2e | outbox→NATS→parse→分块→摘要→向量化→kb_chunks→parse_status + 重试 |
| FR-3 | test_async_parse | integration | 异步解析链路 |
| FR-4 | test_parent_child_chunk | integration | 父子分块 |
| FR-5 | test_doc_summary | integration | 文档摘要 |
| FR-6 | test_embed_unified | integration | 嵌入统一 + Milvus 直连 |
| FR-7 | test_hybrid_retrieval | integration | 混合检索 + RRF + 父块回填 |
| FR-10 | test_doc_formats | integration | 7 格式 |
| FR-11 | test_table_handling | integration | 表格处理 |
| FR-12 | test_image_handling | integration | 图片处理 |

---

## 10. Implementation Plan

### 10.1 Phases
1. **依赖迁移（US-011）**：requirements.txt 改 LlamaIndex；移除旧 pymilvus/langchain 直用
2. **OCR API（US-006）**：inference-service 新增 OCR RPC + PaddleOCR 部署
3. **parse_service（US-011）**：DoclingReader + OCR + 表格 + 图片
4. **chunk + summary（US-012）**：SentenceSplitter + 套叠 + 摘要 + 降级
5. **embed + Milvus（US-013）**：HuggingFaceEmbedding + MilvusVectorStore
6. **retrieve + qa（US-014）**：QueryFusionRetriever + ContextChatEngine
7. **parse_worker + gRPC（US-015）**：NATS 订阅 + Query RPC
8. **端到端验证（US-018）**：完整链路复跑

### 10.2 Issue Mapping
| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| US-006 | 4.2, 5 | high | US-004 (ocr capability) |
| US-011 | 2, 5 | high | US-006 |
| US-012 | 2, 5 | high | US-011 |
| US-013 | 2, 3, 5 | high | US-011 |
| US-014 | 2, 5 | high | US-013 |
| US-015 | 2, 5 | high | US-014, spec-services-kb-service US-010 |
| US-018 | 9.4 | high | US-015, spec-services-kb-service US-010 |

### 10.3 Incremental Delivery
OCR API 可独立先行；解析→分块→嵌入→检索→问答按序依赖；parse_worker 串联后可端到端。

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions
- rag-engine gRPC server 是否复用 kb_service.proto Query 消息或定义内部 proto（实现时确认，需与 kb-service 对齐）
- VectorStoreIndex 嵌入与 MilvusVectorStore 直连的边界（US-013 AC3 明确由 Index 层嵌入后调 add）
- OCR 表格 HTML 的复杂合并单元格保真度（实现时验证）

### 11.2 Technical Risks
| Risk | Impact | Mitigation |
|------|--------|-----------|
| LlamaIndex 0.11+ API 变更 | medium | 锁定版本 + 单元测试覆盖 |
| PaddleOCR PP-OCRv4 部署复杂度 | medium | 复用 inference-service 部署框架 |
| Milvus HNSW 参数调优 | low | P0 用默认 M=16/efConstruction=200 |
| 摘要 LLM 成本 | low | best-effort 降级 |
| 大文档解析超时 | medium | parse_worker 超时设置 + 失败可重试 |

### 11.3 Assumptions
- kb-service US-005 kb_chunks 表已迁移就绪
- kb-service US-010 outbox 派发到 NATS `ani.tasks.kb.parse` 已就绪
- Core ObjectStore download API Ready
- vLLM 服务可用（OpenAI 兼容）
- Redis 可用（RedisChatStore）
- Milvus 2.x 可用
- embedding_dim 与 KB 创建时 Core vector-store 集合 dim 一致（kb-service 传入）
