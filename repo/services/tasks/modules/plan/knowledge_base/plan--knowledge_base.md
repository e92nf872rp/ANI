# 知识库功能模块 规划文档 (plan.md)

> 版本：v1.2.0-draft
> 日期：2026-07-22
> 变更：v1.2 精简文档；rag-engine 直连 Milvus（移除 CoreAPIVectorStore）；补充各格式解析规则
> 关联契约：`repo/api/proto/kb/v1/kb_service.proto`、`repo/api/openapi/services/v1.yaml`、`ANI-09-数据模型设计.md`

---

## 0. 摘要

ANI 平台知识库模块，支持多租户隔离、多知识库管理、文档上传解析、父子分块 + 文档级摘要、混合检索、RAG 问答。

**P0 交付：**
- 后端：kb-service（薄业务层）+ rag-engine（厚 AI 层，直连 Milvus）+ ani-gateway（SSE）+ Core API 扩展
- 前端：知识库列表 + 单库 3 tab（概览 / 文档 / 问答）
- 闭环：上传 → outbox → NATS → 异步解析 → 父子分块 + 摘要 → 向量化 → 混合检索 → 问答

**P1 预留：** 权限控制、操作历史、数据接入、检索实验室、rerank

**技术栈：** Python 3.11 + FastAPI + LlamaIndex 0.11+ + Docling 2.94 + PaddleOCR（经 AI 服务 API 调用，非本地依赖）+ pymilvus（rag-engine 直连）+ Milvus + PostgreSQL（RLS + pg_trgm）+ Redis + MinIO + NATS

> **v1.2 架构优化：**
> - **rag-engine 直连 Milvus**：移除 CoreAPIVectorStore 适配器，直接用 LlamaIndex `MilvusVectorStore`（包名 `llama-index-vector-stores-milvus`），减少一层 HTTP 调用，性能更好
> - **嵌入统一在 rag-engine**：写入与查询嵌入均由 rag-engine 的 `HuggingFaceEmbedding` 完成（MilvusVectorStore 本身不做嵌入，需经 `VectorStoreIndex` 包装后由 Index 层嵌入），不再走 Core 端嵌入

---

## 1. 设计决策汇总

| # | 决策项 | 选择 | 说明 |
|---|---|---|---|
| 1 | 主技术栈 | Python + LlamaIndex | RAG 专精框架，混合检索+RRF+流式开箱即用 |
| 2 | 服务拆分 | kb-service（薄）+ rag-engine（厚） | 元数据 vs 解析/检索/问答，职责清晰 |
| 3 | 解析模式 | 异步任务队列 | 上传返回 202 + task_id，DB outbox 派发 |
| 4 | 检索策略 | 向量 + 关键词（pg_trgm）混合 + RRF | 子块检索 + 父块回填 |
| 5 | 问答模式 | 同步 JSON + SSE 流式 | SSE 在 gateway，rag-engine 仅同步 |
| 6 | 权限（P1） | KB 级 ACL + RBAC | P0 仅靠 RLS 隔离 |
| 7 | 操作历史（P1） | audit_log 表 | P0 不建表 |
| 8 | 分块策略 | 父子分块 + 文档级摘要 + 元数据 | 父块 2048、子块 256-512（SentenceSplitter） |
| 9 | rerank（P1） | 独立增强项 | P0 不引入重型依赖 |
| 10 | Milvus 访问 | rag-engine 直连 | 移除 CoreAPIVectorStore，用 LlamaIndex MilvusVectorStore |
| 11 | 文档格式 | PDF/Word/Excel/PPT/MD/TXT/扫描件 | Docling + PaddleOCR（AI 服务 API） |

---

## 2. 架构设计

### 2.1 架构图

```
┌─────────────────────────────────────────────────────────────────┐
│                    前端 Console (TanStack Router)                 │
│  /kb (列表) → /kb/$kbId/{overview|documents|chat}               │
│  P1 占位：/kb/$kbId/{permissions|history|data-ingestion|lab}    │
└───────────────────────┬─────────────────────────────────────────┘
                        │ HTTPS REST
                        ▼
┌─────────────────────────────────────────────────────────────────┐
│                  ani-gateway (Go)                               │
│  /api/v1/svc/knowledge-bases/* → kb-service (gRPC)              │
│  /api/v1/vector-stores/*       → Core vector-store             │
│  /api/v1/objects/*             → Core object-store             │
│  SSE 端点 → rag-engine 检索 + vLLM streaming                    │
└──────┬───────────────────────┬───────────────────────────────────┘
       │ gRPC                  │ Core OpenAPI
       ▼                       ▼
┌─────────────────┐   ┌─────────────────────────┐
│ kb-service      │   │ Core 服务               │
│ - KB 元数据 CRUD │   │ - VectorStore (集合级)  │
│ - 文档上传协调   │   │ - ObjectStore (MinIO)  │
│ - 会话管理       │   │ - task-service outbox   │
│ - outbox 派发   │   └─────────────────────────┘
└──────┬──────────┘
       │ gRPC
       ▼
┌─────────────────────────────────────────────────────────────────┐
│              rag-engine (Python/FastAPI, 厚 AI 层)               │
│  - 文档解析 (DoclingReader + PaddleOCR 扫描件)                   │
│  - 父子分块 (SentenceSplitter 256-512 + 窗套叠 2048)            │
│  - 文档级摘要 (LLM 生成 → 向化)                                 │
│  - 嵌入 (HuggingFaceEmbedding，写入与查询统一)                    │
│  - 混合检索 (QueryFusionRetriever: MilvusVectorStore + pg_trgm) │
│  - RAG 问答 (ContextChatEngine + OpenAILike，仅同步)             │
└──┬──────────┬────────────────┬──────────────────┬───────────────┘
   │ pymilvus  │ Core API       │ AI 服务           │ PG
   ▼           ▼ /objects       ▼                   ▼
 Milvus      MinIO(下载)     model-service      kb_chunks 表
 (直连)                    (模型列表)          (pg_trgm 关键词)
```

### 2.2 调用链路

**上传与解析（异步）：**
1. 前端 POST `/documents` → kb-service 调 Core `/objects/upload` 获取 pre-signed URL
2. 前端 PUT MinIO → kb-service `NotifyDocumentUploaded` → 同事务写 `kb_documents` + `async_tasks` + `outbox_events`
3. task-service outbox publisher 轮询发布到 NATS `ani.tasks.kb.parse`
4. rag-engine 订阅 → 领取任务 → Core `/objects/{id}/download` 下载 → 解析 → 父子分块 → 文档摘要 → **直连 Milvus 写入子块 + 摘要** → 写 `kb_chunks` 表
5. rag-engine 回写任务状态 → 更新 `kb_documents.parse_status`

**检索问答（同步 + SSE）：**
1. 同步：gateway → kb-service.Query → rag-engine.Query → 嵌入查询 → Milvus 子块检索 + pg_trgm 关键词 → RRF 融合 → 父块回填 → vLLM 生成 → 返回
2. SSE：gateway 持有端点 → rag-engine 检索 + prompt → vLLM streaming → token 透传 → 末尾 sources 事件

### 2.3 服务边界

| 服务 | 职责 | 不做 |
|---|---|---|
| kb-service | 元数据 CRUD、会话、outbox 派发、pre-signed URL 协调 | 不解析/嵌入/检索，不做权限/审计（P1） |
| rag-engine | 解析、分块、摘要、嵌入（写入与查询统一）、检索、问答、NATS 订阅 | 不管理元数据/会话，不实现 SSE |
| ani-gateway | 路由、RBAC、租户注入、限流、SSE | 不实现 KB 业务逻辑 |
| Core | VectorStore 集合级 API、ObjectStore、task-service | 不实现 KB 业务 |

---

## 3. 数据模型

### 3.1 PostgreSQL（P0 新增 1 张表）

```sql
-- knowledge_bases/kb_documents/kb_sessions/kb_messages 已在 20260501_001_init_schema.sql 迁移
-- P0 仅新增 kb_chunks 表；kb_permissions/kb_audit_log 降级为 P1

CREATE TABLE kb_chunks (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  kb_id            UUID NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
  doc_id           UUID NOT NULL REFERENCES kb_documents(id) ON DELETE CASCADE,
  tenant_id        UUID NOT NULL,
  chunk_index      INT  NOT NULL,
  parent_chunk_id  UUID,                          -- 父块 ID（子块指向父块）
  chunk_type       TEXT NOT NULL DEFAULT 'child' CHECK (chunk_type IN ('parent','child','doc_summary')),
  content_type     TEXT NOT NULL DEFAULT 'text',  -- text/table/image/code
  page_number      INT,
  content          TEXT NOT NULL,                 -- 块文本
  parent_content   TEXT,                          -- 父块完整文本（回填用，含 HTML 表格）
  summary          TEXT,                          -- 预留：父块摘要
  file_name        TEXT NOT NULL,
  custom_metadata  JSONB NOT NULL DEFAULT '{}',   -- 用户自定义元数据
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (doc_id, chunk_index)
);

CREATE INDEX idx_kb_chunks_kb_doc ON kb_chunks(kb_id, doc_id);
CREATE INDEX idx_kb_chunks_parent ON kb_chunks(parent_chunk_id);
CREATE INDEX idx_kb_chunks_type ON kb_chunks(kb_id, chunk_type);

-- pg_trgm 扩展（P0 必需迁移）
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX idx_kb_chunks_content_trgm ON kb_chunks USING GIN (content gin_trgm_ops);
```

### 3.2 Milvus 向量集合（rag-engine 直连）

```
集合命名：kb_{kb_id 去横杠}
维度：动态（按 Embedding 模型）
索引：HNSW, metric=COSINE, M=16, efConstruction=200

Schema:
  chunk_id        VARCHAR(64) PRIMARY KEY
  doc_id          VARCHAR(64)
  kb_id           VARCHAR(64)
  tenant_id       VARCHAR(64)
  content         VARCHAR(4096)
  file_name       VARCHAR(512)
  page_number     INT32
  chunk_index     INT32
  parent_chunk_id VARCHAR(64)
  chunk_type      VARCHAR(16)          -- parent/child/doc_summary
  content_type    VARCHAR(16)          -- text/table/image/code
  custom_metadata VARCHAR(4096)       -- JSON 字符串
  embedding       FLOAT_VECTOR(dim=动态)
```

> **v1.2 直连 Milvus**：rag-engine 用 LlamaIndex `MilvusVectorStore` 直接操作，移除 CoreAPIVectorStore 适配器。kb-service 创建/删除向量集合仍走 Core OpenAPI。

### 3.3 Redis / MinIO

```
Redis:  ani:prod:session:kb:{session_id}  List, TTL 24h, LTRIM 20
MinIO:  ani-kb-docs  key={tenant_id}/{kb_id}/{doc_id}/{filename}  pre-signed 15min
```

---

## 4. 文档解析规则

### 4.1 各格式解析工具与输出

| 格式 | 解析工具 | 关键参数 | 输出格式 | 特殊处理 |
|---|---|---|---|---|
| PDF（文本层） | DoclingReader | `export_type=markdown` | Markdown | 保留页码、表格转 HTML |
| PDF（扫描件） | AI 服务 PaddleOCR API | `lang=ch, use_angle_cls=True` | 纯文本 + HTML 表格 | 逐页检测文本层，无则调 AI 服务 OCR API |
| Word (.docx) | DoclingReader | `export_type=markdown` | Markdown | 保留标题层级（heading_path），表格转 HTML |
| Excel (.xlsx) | DoclingReader | `export_type=markdown` | Markdown 表格 | 每个 Sheet 独立段，表格转 HTML，大表按 token 阈值切分行（行不可截断，每个父块保留表头） |
| PPT (.pptx) | DoclingReader | `export_type=markdown` | Markdown | 每页幻灯片独立段，含备注页 |
| Markdown | DoclingReader | `export_type=markdown` | 原样 | 提取 frontmatter、保留代码块 |
| TXT | DoclingReader | `export_type=text` | 纯文本 | chardet 编码检测，按段落切分 |

### 4.2 表格处理规则（全格式统一）

- **父块**：HTML 表格（`<table>` + `<tr>`/`<td>`/`<th>` + `rowspan`/`colspan`），LLM 天然理解
- **子块**：去 HTML 标签转逐行自然语言（如 `Q1：产品A 100，产品B 200`），利于 Embedding 语义捕捉
- **合并单元格**：只在左上角输出值，其他位置用 `rowspan`/`colspan` 占位
- **跨页表格**：按页拆分为多个表格块，每块附表头
- **表格 > 2048 tokens**：按行分组拆分为多个父块，每个父块**必须保留表头行（列名）**，行作为最小切分单元不可截断（单行超 2048 时整行独立成块）
- **表格元数据**：`content_type=table`、`table_index`、`caption`（表格上方"表 X：xxx"正则匹配）

### 4.3 扫描件 OCR 规则（经 AI 服务 API 调用）

- **检测策略**：逐页判定，`page.extract_text()` 字符数 < 50 判为扫描页（支持同一 PDF 混合）
- **OCR 调用**：扫描页渲染为图片，调 AI 服务 PaddleOCR API（PP-OCRv4，`lang=ch, use_angle_cls=True`），不本地安装 PaddleOCR
- **版面分析**：AI 服务返回版面区域分类（文字/表格/图片），应用层按 type 过滤跳过图片区域
- **表格区域**：AI 服务表格识别返回 HTML 表格（与数字版统一格式）
- **文字区域**：OCR 识别后按阅读顺序拼接
- **后处理**：去多余空行、合并断行
- **元数据**：`source_type=pdf_scan`、`ocr_confidence`（AI 服务返回）、`low_confidence`（<0.6 标记）

### 4.4 图片处理规则

- **提取**：DoclingReader 解析图片，上传 MinIO 获取永久 URL
- **占位节点**：在文本流对应位置插入 `[图片: caption](OSS_URL)`
- **元数据**：`content_type=image`、`image_url`、`caption`（正则匹配"图 X：xxx"）
- **向量化**：图片占位节点也向量化（便于检索"某文档含某图"）
- **问答展示**：引用命中图片节点时直接渲染 `<img>` 指向 `image_url`

### 4.5 父子分块算法

1. **子块切分**：`SentenceSplitter` 按 `chunk_size`（256-512 tokens）切分，优先在句子边界切分；**注意**：单句超过 chunk_size 时仍会强制截断（fallback），中文场景建议显式指定 `chunking_tokenizer_fn`
2. **父块套叠**：连续子块累积 token 到 2048 归为一个父块（固定窗套叠）
3. **边界处理**：文档末尾不足 2048 时，剩余子块归最后一个父块
4. **不可切断单元**：以下内容作为原子单元，切分时整体归属到一个子块，禁止从中间截断：
   - **图片链接**：`[图片: caption](OSS_URL)` 整体归属一个子块，不跨子块拆分
   - **表格**：整个表格（HTML `<table>...</table>`）整体归属一个父块；表格 > 2048 tokens 时按行分组拆分为多个父块，**每个父块必须保留表头行（列名）**，**行作为最小切分单元不可截断**（单行超 2048 时整行独立成块）；子块按逐行自然语言切分但不破坏单行完整性
   - **代码块**：` ```...``` ` 整体归属，按函数/逻辑边界切分子块
   - **超链接**：`[text](url)` 整体不截断
5. **不同 content_type 的分块特殊处理**：

| content_type | 父块切分 | 子块切分 |
|---|---|---|
| text | 按句子 + token 阈值切分 | 按句子切分，256-512 tokens |
| table | 整个表格为一个父块（>2048 按行分组拆分，每个父块保留表头行/列名，行不可截断） | 逐行自然语言，单行不截断 |
| image | 超链接占位文本为一个父块（不拆分） | 同父块，整体不截断 |
| code | 代码块为一个父块（不拆分，即使超 2048） | 按代码行/函数边界切分 |

6. **元数据继承**：每个子块继承父块的 doc_id/kb_id/tenant_id/file_name/page_number/content_type；自定义 metadata 全局继承
7. **父子关系**：子块 `parent_chunk_id` 指向父块；父块完整文本存入子块的 `parent_content` 字段（回填用）
8. **分隔符规则**：
   - **子块之间**：用 `\n`（单换行）分隔，保持自然段落边界
   - **父块之间**：用 `\n\n`（双换行）分隔，明确区分不同父块上下文
   - 父块 `parent_content` 存储：子块文本按 `\n` 拼接
   - 多父块送 LLM 上下文时：父块之间按 `\n\n` 拼接
   - 表格/代码块内部不加分隔符（已有结构化标记）

### 4.6 文档级摘要

- 每文档生成一段摘要：拼接文档前 N 个父块（覆盖主要内容）→ LLM 生成 200-500 字
- 摘要向化存 Milvus，`chunk_type=doc_summary`，参与检索
- 摘要命中后回填该文档的父块作为上下文
- 摘要生成失败不阻断入库（降级为仅父子分块，记录 warning）

---

## 5. 服务实现

### 5.1 kb-service

**位置：** `repo/services/kb-service/`（新建）

**目录结构：**
```
repo/services/kb-service/
├── main.py                  # FastAPI 入口
├── grpc_server.py           # kb_service.proto 10 个 RPC（P1 新增 3 个权限/审计 RPC 声明，返回 UNIMPLEMENTED）
├── routers/                 # knowledge_bases.py, documents.py, sessions.py
├── services/                # kb_service.py, document_service.py, session_service.py
├── repositories/            # kb_repo, doc_repo, session_repo, chunk_repo
├── clients/                 # rag_engine_client, core_api_client
├── outbox/task_dispatcher.py # 同事务写 async_tasks + outbox_events
└── models/db.py             # SQLAlchemy + RLS
```

**关键 RPC：**

| RPC | 实现要点 |
|---|---|
| CreateKB | 写 knowledge_bases；调 Core `POST /vector-stores` 创建向量集合 |
| GetKB/ListKBs | RLS 过滤（P0 无 KB 级权限校验） |
| DeleteKB | 软删；调 Core `DELETE /vector-stores/{id}` 删集合 |
| GetDocumentUploadURL | 调 Core `POST /objects/upload` 获取 pre-signed URL |
| NotifyDocumentUploaded | 同事务写 kb_documents + async_tasks + outbox_events |
| DeleteDocument | 软删 + 通知 rag-engine 删 Milvus 文档向量（按 doc_id filter） |
| Query | 转发 rag-engine.Query；写 kb_messages + Redis |
| GetKBPermissions/UpdateKBPermissions/ListAuditLogs | **P1 预留**：proto 当前无此 3 个 RPC，Phase A 需新增声明；P0 返回 UNIMPLEMENTED |

### 5.2 rag-engine

**位置：** `repo/ai/rag-engine/`（扩展现有脚手架）

**目录结构：**
```
repo/ai/rag-engine/
├── main.py                  # FastAPI + gRPC 双协议
├── grpc_server.py           # Query RPC（仅同步）
├── app/
│   ├── core/config.py       # 配置
│   ├── routers/             # documents.py, query.py（内部调试）
│   ├── services/
│   │   ├── parse_service.py     # 文档解析：DoclingReader + PaddleOCR
│   │   ├── chunk_service.py     # 父子分块：SentenceSplitter + 窗套叠
│   │   ├── summary_service.py   # 文档级摘要：LLM 生成 → 向化
│   │   ├── embed_service.py     # HuggingFaceEmbedding（写入与查询统一）
│   │   ├── retrieve_service.py  # 混合检索：MilvusVectorStore + pg_trgm + RRF + 父块回填
│   │   └── qa_service.py        # ContextChatEngine + OpenAILike
│   ├── clients/
│   │   ├── milvus_client.py     # v1.2 直连 Milvus（LlamaIndex MilvusVectorStore）
│   │   ├── core_api_client.py   # Core OpenAPI（/objects/{id}/download）
│   │   ├── ocr_client.py        # AI 服务 OCR API 客户端（调 PaddleOCR，非本地依赖）
│   │   ├── llm_client.py        # 推理服务
│   │   └── task_service_client.py
│   └── workers/parse_worker.py  # NATS 订阅
└── requirements.txt
```

**关键能力：**

1. **解析**：DoclingReader 解析常规格式，PaddleOCR 处理扫描件（见第 4 节规则）
2. **分块**：SentenceSplitter 切子块 256-512（优先句子边界，单句超 chunk_size 时强制截断）+ 窗套叠父块 2048 + 写 kb_chunks 表
3. **摘要**：每文档 LLM 生成摘要 → 向化存 Milvus（chunk_type=doc_summary）
4. **嵌入**：HuggingFaceEmbedding 动态加载，**写入与查询统一**（MilvusVectorStore 本身不做嵌入，需经 `VectorStoreIndex.from_vector_store(vector_store, embed_model=...)` 包装；写入时 Index 用 embed_model 嵌入后调 `vector_store.add()`；查询时 `VectorIndexRetriever` 自动用 embed_model 嵌入查询再调 `vector_store.query()`）
5. **检索**：`QueryFusionRetriever`（MilvusVectorStore 需先包为 `VectorStoreIndex` 再 `as_retriever()` 得 `VectorIndexRetriever`；+ pg_trgm 关键词 `BaseRetriever` 子类 + RRF 融合 + 父块回填），`num_queries=1` 关闭查询生成
6. **问答**：`ContextChatEngine.from_defaults(retriever=fusion_retriever, memory=ChatMemoryBuffer(chat_store=RedisChatStore), llm=OpenAILike(model=..., api_base=vllm_url, api_key="...", is_chat_model=True, context_window=...))` + `.chat()` 同步返回（`OpenAILike` 来自 `llama-index-llms-openai-like` 包，LlamaIndex 官方推荐用于 vLLM OpenAI 兼容服务器；注意参数名是 `api_base` 非 `api_url`，`is_chat_model=True` 和 `context_window` 为必填）

### 5.3 ani-gateway

- 补齐缺失路由（citations/sessions/permissions），12 端点全部就位
- 实现 gRPC client 替换 9 个 stub handler
- SSE 端点：调 rag-engine 检索 + prompt → vLLM streaming → token 透传 → 末尾 sources 事件

---

## 6. 前端设计

### 6.1 路由

```
repo/frontends/console/src/routes/kb/
├── index.tsx                       # 列表页（增强）
└── $kbId/
    ├── __root.tsx                  # 3 tab 布局
    ├── overview.tsx                # 概览（P0）
    ├── documents.tsx               # 文档与解析（P0）
    ├── chat.tsx                    # 问答（P0）
    ├── data-ingestion.tsx          # P1 占位
    ├── lab.tsx                     # P1 占位
    ├── permissions.tsx             # P1 占位
    └── history.tsx                 # P1 占位
```

### 6.2 页面概要

| 页面 | 布局 | 关键功能 |
|---|---|---|
| 列表页 | 表格 | 新建模态框（名称/描述/嵌入模型/chunk_size/top_k）、删除、状态 Tag |
| 概览页 | 信息 + 三段配置 | 入库（Embedding/chunk_size/OCR）+ 问答（TopK/score_threshold/检索策略）+ 规划（P1）；改 Embedding/chunk_size 触发重建 |
| 文档页 | 工具栏 + 表格 | 上传（拖拽多文件 + 自定义元数据）、状态筛选、重试、解析详情（父子块层级 + 摘要 + metadata） |
| 问答页 | 会话列表 + 消息流 | 多会话、同步/流式、TopK 调节、引用卡片（子块 + 父块上下文） |

---

## 7. API 契约修复

### 7.1 必须修复

| 问题 | 修复 |
|---|---|
| KBDocument 字段名不一致 | 统一为 `parse_status`，枚举值对齐 proto |
| 文档上传契约冲突 | 改为两步式 pre-signed URL，对齐 proto |
| KBQueryRequest 字段缺失 | 补齐 `score_threshold`/`inference_service_name`/`idempotency_key` |
| 文档上传缺 custom_metadata | proto + OpenAPI 新增 `custom_metadata`（JSONB） |
| 缺少 reparse/config/rebuild/models 端点 | 新增 Services OpenAPI 端点 |
| Core 缺向量文档级删除 | 新增 `DELETE /vector-stores/{id}/documents?filter=...` |
| model proto 缺 ocr capability | 新增 `ocr` 字符串值注释 |
| pg_trgm 未迁移 | 新增迁移：CREATE EXTENSION + GIN 索引 |
| **AI 服务缺 OCR API 端点** | **当前 model-service/inference-service 均无 OCR RPC**；P0 需新增 OCR API 端点（建议在 inference-service 新增 OCR 推理 RPC，或独立 ocr-service），供 rag-engine 调用 PaddleOCR |

### 7.2 P1 预留端点

```
# 权限/审计
GET/PUT /knowledge-bases/{kb_id}/permissions
GET    /knowledge-bases/{kb_id}/audit-logs

# 数据接入
POST/GET/DELETE /knowledge-bases/{kb_id}/data-sources[/{id}][/sync]

# 检索实验室
POST /knowledge-bases/{kb_id}/lab/{debug|evalsets|evalsets/{id}/run|compare}
```

---

## 8. 错误码

| 场景 | HTTP | 错误码 |
|---|---|---|
| KB 不存在 | 404 | `kb.not_found` |
| 无权限访问 KB | 403 | `kb.forbidden`（P1 预留） |
| 文件格式不支持 | 422 | `doc.unsupported_type` |
| 文件大小超限 | 413 | `doc.too_large`（P0 上限 100MB） |
| SHA256 校验失败 | 422 | `doc.checksum_mismatch` |
| 解析失败 | 200 | `doc.parse_failed`（可重试） |
| 向量维度不匹配 | 500 | `vector.dim_mismatch` |
| 推理服务不可用 | 503 | `inference.unavailable` |
| 全库重建进行中 | 409 | `kb.rebuilding` |

---

## 9. 测试策略

| 层 | 覆盖 |
|---|---|
| kb-service | repository CRUD、gRPC server、outbox 派发 |
| rag-engine | 解析（各格式 + OCR）、父子分块（窗套叠 + parent_chunk_id）、文档摘要、嵌入（写入与查询统一）、**MilvusVectorStore 直连**（add/delete/query/delete_nodes）、混合检索+RRF+父块回填、问答 |
| 契约 | proto/OpenAPI 一致性、custom_metadata 字段 |
| e2e | 上传→解析→分块→摘要→检索（子块+父块回填）→问答 |
| 前端 | 列表/概览/文档/问答组件、路由、SSE |

---

## 10. 实施计划

### 阶段划分

| 阶段 | 内容 | 依赖 |
|---|---|---|
| A | 契约修复 + 数据库迁移 | 无 |
| B | kb-service 骨架 + gRPC + outbox | A |
| C | rag-engine 核心能力 | A |
| G | ani-gateway SSE + KB 路由 | B, C |
| D | 异步任务链路验证 | B, C |
| E | 前端 3 tab + 列表页 | B, G |
| F | 端到端联调 + 测试 | E |

### 任务清单

**Phase A — 契约与数据模型**
- A1: 修复 services/v1.yaml（KBDocument 字段名/枚举值/上传两步式/QueryRequest 字段/custom_metadata）
- A2: 新增端点（reparse/config/rebuild/models）
- A3: Core 新增 `DELETE /vector-stores/{id}/documents?filter=...`
- A4: model proto 新增 `ocr` capability 注释
- A5: 生成 SDK 生成物
- A6: 迁移脚本（kb_chunks 表 + pg_trgm + 索引）
- A7: proto/OpenAPI 一致性校验
- A8: **新增 AI 服务 OCR API 端点**（当前 model-service/inference-service 均无 OCR RPC；建议在 inference-service 新增 OCR 推理 RPC 或独立 ocr-service，后端部署 PaddleOCR 模型，供 rag-engine 调用）

**Phase B — kb-service**
- B1: 创建骨架（Dockerfile/requirements/main）
- B2: gRPC server（10 个现有 RPC；Phase A 新增 3 个权限/审计 RPC 声明，返回 UNIMPLEMENTED）
- B3: repositories（4 已迁移表 + kb_chunks）
- B4: Core API client（/vector-stores 集合级、/objects/upload、/objects/{id}/download）
- B5: rag-engine gRPC client
- B6: Python 侧 outbox 派发（同事务写 async_tasks + outbox_events）
- B7: Redis 会话缓存
- B8: 测试

**Phase C — rag-engine**
- C1: 移除 pymilvus/langchain 旧依赖，改为 llama-index-core + readers-docling + embeddings-huggingface + **llms-openai-like** + vector-stores-milvus + **pymilvus（v1.2 直连）**
- C2: parse_service（DoclingReader + PaddleOCR 扫描件，按第 4 节规则）
- C3: chunk_service（父子分块：SentenceSplitter + 窗套叠 + 写 kb_chunks）
- C4: summary_service（文档级摘要：LLM 生成 + 向化）
- C5: embed_service（HuggingFaceEmbedding，写入与查询统一）
- C6: **MilvusVectorStore 直连**（用 LlamaIndex MilvusVectorStore，经 `VectorStoreIndex.from_vector_store()` 包装，不封装适配器）
- C7: retrieve_service（QueryFusionRetriever：VectorStoreIndex.as_retriever() + pg_trgm BaseRetriever + RRF + 父块回填）
- C8: qa_service（ContextChatEngine + OpenAILike(api_base=vllm_url, is_chat_model=True) + RedisChatStore）
- C9: parse_worker（NATS 订阅 + task-service 领取）
- C10: gRPC server（Query RPC）
- C11: 测试

**Phase G — ani-gateway**
- G1: 补齐 3 个缺失路由
- G2: gRPC client 替换 stub
- G3: SSE 端点实现
- G4: RBAC + 限流
- G5: SSE 错误处理
- G6: 测试

**Phase D — 异步链路验证**
- D1-D4: outbox → NATS → rag-engine → 状态回写 端到端验证

**Phase E — 前端**
- E1: 列表页增强
- E2: __root.tsx 3 tab 布局 + parentRoute 改造
- E3: overview 页
- E4: documents 页（上传 + 自定义元数据 + 解析详情）
- E5: chat 页（多会话 + SSE + TopK + 引用）
- E6: P1 占位页（4 个）
- E7: 测试

**Phase F — 联调**
- F1: 端到端链路（上传→分块→摘要→检索→问答）
- F2: SSE 流式
- F3: RLS 隔离
- F4: 解析重试
- F5: 全库重建
- F6: Core 文档级删除
- F7: pg_trgm 检索
- F8: 父子分块验证
- F9: 文档摘要验证
- F10: live gate
- F11: make test 全绿
- F12: 文档更新

---

## 11. P1 预留设计

### 11.1 权限控制

KB 级 ACL + RBAC：`public_read` + `allowed_user_ids` + Core RBAC scope

```sql
CREATE TABLE kb_permissions (
  kb_id          UUID PRIMARY KEY REFERENCES knowledge_bases(id) ON DELETE CASCADE,
  public_read    BOOLEAN NOT NULL DEFAULT false,
  allowed_user_ids JSONB NOT NULL DEFAULT '[]',
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### 11.2 操作历史

```sql
CREATE TABLE kb_audit_log (
  id           BIGSERIAL PRIMARY KEY,
  tenant_id    UUID NOT NULL, kb_id UUID NOT NULL, actor_id UUID NOT NULL,
  action       TEXT NOT NULL, resource_id UUID, resource_type TEXT,
  before_state JSONB, after_state JSONB, error_code TEXT, error_msg TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### 11.3 数据接入

URL 抓取 / 对象存储 / 批量文件，`kb_data_sources` 表（config JSONB + sync_mode + cron_expr）

### 11.4 检索实验室

调试（单次检索）、评测（评测集 + 批量执行 + 指标）、对比（策略 A/B 对比），`kb_eval_sets`/`kb_eval_cases`/`kb_eval_runs` 表

### 11.5 rerank

启用时需装 `llama-index-postprocessor-sbert-rerank` + `sentence-transformers` + `torch`，显式设 `top_n`

---

## 12. 验收标准

1. **功能闭环**：创建/删除 KB、上传 7 格式文档异步解析、概览配置可改触发重建、问答多会话+同步/流式+TopK+引用、父子分块（2048/256-512）、文档级摘要、自定义元数据
2. **契约一致**：proto 与 services/v1.yaml 一致、SDK 无漂移、`make validate-services` 通过
3. **架构合规**：rag-engine 直连 Milvus（v1.2）、kb-service 走 Core OpenAPI、SSE 在 gateway、`make validate-architecture` 通过
4. **测试通过**：后端+前端测试全绿、live gate 通过、`make test` 全绿
5. **文档闭环**：development-records + CURRENT-SPRINT + ANI-06 更新

---

## 13. 附录

### 关联文件

| 文件 | 作用 |
|---|---|
| `repo/api/proto/kb/v1/kb_service.proto` | gRPC 契约 |
| `repo/api/openapi/services/v1.yaml` | Services REST 契约 |
| `repo/api/openapi/v1.yaml` | Core OpenAPI |
| `ANI-09-数据模型设计.md` | 数据模型设计 |
| `2026-07-22-lamaindex-知识库-design.md` | LlamaIndex 解析规则参考 |

### 依赖就绪状态

| 依赖 | 状态 |
|---|---|
| Core VectorStore API（集合级） | Ready |
| Core VectorStore API（文档级 DELETE） | 需 Phase A3 新增 |
| Core ObjectStore API | Ready |
| task-service outbox | Ready |
| NATS `ani.tasks.kb.parse` | Ready |
| PostgreSQL 4 张 KB 表 | Ready |
| PostgreSQL kb_chunks 表 | 需 Phase A6 迁移 |
| pg_trgm 扩展 | 需 Phase A6 迁移 |
| Redis / Milvus / MinIO | Ready |
| model-service ListModels | Ready（无 capability 过滤） |
| model proto ocr capability | 需 Phase A4 |
| **AI 服务 OCR API 端点** | **需 Phase A8 新增**（当前无 OCR RPC） |
| ani-gateway KB 路由 | 部分（9/12 已注册） |
| ani-gateway SSE 端点 | 需 Phase G3 新实现 |

---

**END OF plan.md**
