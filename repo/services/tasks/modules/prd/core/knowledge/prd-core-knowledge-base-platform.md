# PRD: Core 知识库平台 (P0)

## 1. Introduction/Overview

ANI 平台知识库模块（P0）为租户提供多租户隔离的多知识库管理、文档上传与异步解析、父子分块 + 文档级摘要、混合检索与 RAG 问答能力。

本 PRD 覆盖全栈 P0 交付：kb-service（薄业务层）、rag-engine（厚 AI 层，直连 Milvus）、ani-gateway（SSE + KB 路由）、Core API 扩展（向量文档级删除）以及 Console 前端 3 tab 页面。闭环为：创建知识库 → 上传文档 → outbox → NATS → 异步解析 → 父子分块 + 摘要 → 向量化 → 混合检索 → 同步/SSE 问答。

P1 能力（KB 级权限、操作审计、数据接入、检索实验室、rerank）不在本轮范围，仅做 proto/OpenAPI 端点声明与前端占位。

**关联文档：** [plan.md](file:///c:/Users/PC/Desktop/ANI/plan.md)（v1.2.0-draft，技术实现规划）

## 2. Goals

- 交付 kb-service 薄业务层：知识库/文档/会话元数据 CRUD、pre-signed URL 协调、outbox 派发
- 交付 rag-engine 厚 AI 层：Docling + PaddleOCR（经 AI 服务 API）解析、父子分块、文档摘要、嵌入、混合检索、RAG 问答、NATS 订阅
- 交付 ani-gateway KB 路由与 SSE 端点：补齐缺失路由、gRPC client 替换 stub、SSE token 透传
- 扩展 Core API：新增向量文档级删除端点
- 修复 Services OpenAPI 契约：字段名/枚举值统一、两步式上传、QueryRequest 补齐、custom_metadata、新增 reparse/config/rebuild/models 端点
- 新增 AI 服务 OCR API 端点：供 rag-engine 调用 PaddleOCR 处理扫描件
- 交付 Console 前端：列表页 + 单库 3 tab（概览/文档/问答）+ P1 占位页
- 端到端闭环可验证：上传 → 解析 → 分块 → 摘要 → 检索 → 问答

## 3. User Stories

### US-001: 修复 Services OpenAPI 契约
**Description:** 作为平台开发者，我需要修复 Services OpenAPI 与 proto 的契约不一致，使前后端契约对齐。

**Acceptance Criteria:**
- [ ] `KBDocument` 字段名统一为 `parse_status`，枚举值对齐 proto（`pending | parsing | indexing | ready | failed`）
- [ ] 文档上传契约改为两步式 pre-signed URL（`GetDocumentUploadURL` + `NotifyDocumentUploaded`），对齐 proto
- [ ] `KBQueryRequest` 补齐 `score_threshold`、`inference_service_name`、`idempotency_key` 字段
- [ ] 文档上传新增 `custom_metadata`（JSONB）字段到 proto 与 OpenAPI
- [ ] `make validate-services` 通过
- [ ] proto 与 services/v1.yaml 一致性校验通过

### US-002: 新增 Services OpenAPI 端点
**Description:** 作为平台开发者，我需要新增 reparse/config/rebuild/models 端点以支撑前端概览页配置与重建能力。

**Acceptance Criteria:**
- [ ] 新增 `POST /knowledge-bases/{kb_id}/documents/{doc_id}/reparse`（重新解析）
- [ ] 新增 `GET/PUT /knowledge-bases/{kb_id}/config`（读取/更新 KB 配置）
- [ ] 新增 `POST /knowledge-bases/{kb_id}/rebuild`（全库重建）
- [ ] 新增 `GET /knowledge-bases/{kb_id}/models`（可用嵌入/推理模型列表）
- [ ] 新增端点写入 `repo/api/openapi/services/v1.yaml`，不写入 Core `v1.yaml`
- [ ] `make validate-services` 通过

### US-003: Core 新增向量文档级删除端点
**Description:** 作为平台开发者，我需要 Core 提供按 doc_id 过滤的向量文档删除能力，支撑知识库文档删除时清理 Milvus 向量。

**Acceptance Criteria:**
- [ ] Core OpenAPI 新增 `DELETE /vector-stores/{id}/documents?filter=...`（按 filter 删除文档向量）
- [ ] 端点写入 `repo/api/openapi/v1.yaml`
- [ ] Core handler 实现该端点
- [ ] `make validate-architecture` 通过
- [ ] `make test` 通过

### US-004: model proto 新增 OCR capability 标注
**Description:** 作为平台开发者，我需要在 model proto 中标注 OCR 能力，使模型列表可识别 OCR 服务。

**Acceptance Criteria:**
- [ ] model proto 新增 `ocr` 字符串值作为 capability 注释
- [ ] proto 生成物更新
- [ ] `make validate-services` 通过

### US-005: 数据库迁移 kb_chunks + pg_trgm
**Description:** 作为平台开发者，我需要新增 kb_chunks 表与 pg_trgm 扩展以支撑关键词检索与父子分块存储。

**Acceptance Criteria:**
- [ ] 新增 `kb_chunks` 表迁移脚本，字段与 plan.md §3.1 一致（含 `parent_chunk_id`、`chunk_type`、`parent_content`、`custom_metadata`）
- [ ] 新增 `CREATE EXTENSION IF NOT EXISTS pg_trgm` 迁移
- [ ] 新增 `idx_kb_chunks_content_trgm` GIN 索引
- [ ] 新增 `idx_kb_chunks_kb_doc`、`idx_kb_chunks_parent`、`idx_kb_chunks_type` 索引
- [ ] 迁移脚本可重复执行（idempotent）
- [ ] `make test` 通过

### US-006: 新增 AI 服务 OCR API 端点
**Description:** 作为平台开发者，我需要 AI 服务提供 OCR 推理端点，供 rag-engine 调用 PaddleOCR 处理扫描件，而非本地安装 PaddleOCR。

**Acceptance Criteria:**
- [ ] 在 inference-service 新增 OCR 推理 RPC（或独立 ocr-service），后端部署 PaddleOCR（PP-OCRv4）
- [ ] OCR API 支持 `lang=ch`、`use_angle_cls=True` 参数
- [ ] OCR API 返回版面区域分类（文字/表格/图片）与表格 HTML
- [ ] OCR API 返回 `ocr_confidence` 字段
- [ ] rag-engine 可通过 client 调用该 OCR API
- [ ] `make test` 通过

### US-007: 生成 SDK 生成物并校验一致性
**Description:** 作为平台开发者，我需要重新生成 SDK 并校验 proto/OpenAPI 一致性，确保契约变更落地。

**Acceptance Criteria:**
- [ ] 基于 A1/A2/A3/A4 变更重新生成 SDK 生成物
- [ ] `make validate-sdk-beta` 通过
- [ ] `make validate-spec-split` 通过
- [ ] SDK 无漂移

### US-008: kb-service 骨架与 gRPC server
**Description:** 作为平台开发者，我需要创建 kb-service 骨架并实现 gRPC server 承接 proto 10 个 RPC。

**Acceptance Criteria:**
- [ ] 新建 `repo/services/kb-service/`，含 Dockerfile/requirements/main.py
- [ ] 实现 `kb_service.proto` 10 个 RPC：CreateKB/GetKB/ListKBs/DeleteKB/GetDocumentUploadURL/NotifyDocumentUploaded/GetDocument/ListDocuments/DeleteDocument/Query
- [ ] Phase A 新增 3 个权限/审计 RPC 声明，P0 返回 `UNIMPLEMENTED`
- [ ] gRPC server 可启动并响应 RPC
- [ ] `make test` 通过

### US-009: kb-service repositories 与 Core API client
**Description:** 作为平台开发者，我需要实现 kb-service 的数据访问层与 Core API 客户端。

**Acceptance Criteria:**
- [ ] 实现 repositories 覆盖 4 张已迁移表 + kb_chunks（含 RLS 过滤）
- [ ] Core API client 实现 `/vector-stores` 集合级 CRUD、`/objects/upload`、`/objects/{id}/download`、`/vector-stores/{id}/documents` 删除
- [ ] rag-engine gRPC client 实现 Query 调用
- [ ] CreateKB 调 Core `POST /vector-stores` 创建向量集合
- [ ] DeleteKB 软删 + 调 Core `DELETE /vector-stores/{id}` 删集合
- [ ] `make test` 通过

### US-010: kb-service outbox 派发与 Redis 会话缓存
**Description:** 作为平台开发者，我需要实现 Python 侧 outbox 派发与 Redis 会话缓存。

**Acceptance Criteria:**
- [ ] `NotifyDocumentUploaded` 同事务写 `kb_documents` + `async_tasks` + `outbox_events`
- [ ] outbox 派发器轮询发布到 NATS `ani.tasks.kb.parse`
- [ ] Query RPC 写 `kb_messages` + Redis 会话缓存（`ani:prod:session:kb:{session_id}`，TTL 24h，LTRIM 20）
- [ ] `make test` 通过

### US-011: rag-engine 依赖迁移与解析服务
**Description:** 作为 AI 层开发者，我需要迁移 rag-engine 依赖到 LlamaIndex 并实现文档解析服务。

**Acceptance Criteria:**
- [ ] 移除 pymilvus/langchain 旧依赖，改为 `llama-index-core` + `readers-docling` + `embeddings-huggingface` + `llms-openai-like` + `vector-stores-milvus` + `pymilvus`
- [ ] `parse_service` 用 DoclingReader 解析 PDF/Word/Excel/PPT/MD/TXT（按 plan.md §4.1 规则）
- [ ] `parse_service` 对扫描页（`page.extract_text()` < 50 字符）调 AI 服务 PaddleOCR API
- [ ] 表格转 HTML，跨页表格按页拆分，表格 > 2048 tokens 按行分组拆分并保留表头
- [ ] 图片提取上传 MinIO 获取 URL，插入 `[图片: caption](OSS_URL)` 占位节点
- [ ] `make test` 通过

### US-012: rag-engine 父子分块与文档摘要
**Description:** 作为 AI 层开发者，我需要实现父子分块与文档级摘要。

**Acceptance Criteria:**
- [ ] `chunk_service` 用 `SentenceSplitter` 切子块 256-512 tokens（优先句子边界，单句超 chunk_size 强制截断）
- [ ] 连续子块累积到 2048 tokens 归为一个父块（固定窗套叠）
- [ ] 图片链接/表格/代码块/超链接作为不可切断单元
- [ ] 子块 `parent_chunk_id` 指向父块，父块完整文本存入子块 `parent_content`
- [ ] 写入 `kb_chunks` 表，元数据继承（doc_id/kb_id/tenant_id/file_name/page_number/content_type）
- [ ] `summary_service` 拼接前 N 个父块 → LLM 生成 200-500 字摘要 → 向化存 Milvus（`chunk_type=doc_summary`）
- [ ] 摘要生成失败不阻断入库（降级为仅父子分块，记录 warning）
- [ ] `make test` 通过

### US-013: rag-engine 嵌入与 Milvus 直连
**Description:** 作为 AI 层开发者，我需要实现嵌入服务并直连 Milvus（v1.2 架构优化，移除 CoreAPIVectorStore）。

**Acceptance Criteria:**
- [ ] `embed_service` 用 `HuggingFaceEmbedding` 动态加载，写入与查询嵌入统一
- [ ] `MilvusVectorStore`（LlamaIndex 包 `llama-index-vector-stores-milvus`）直接操作 Milvus
- [ ] 经 `VectorStoreIndex.from_vector_store(vector_store, embed_model=...)` 包装，由 Index 层嵌入后调 `vector_store.add()`
- [ ] Milvus 集合命名 `kb_{kb_id 去横杠}`，索引 HNSW、metric=COSINE、M=16、efConstruction=200
- [ ] 不封装 CoreAPIVectorStore 适配器
- [ ] `make test` 通过

### US-014: rag-engine 混合检索与 RAG 问答
**Description:** 作为 AI 层开发者，我需要实现混合检索与 RAG 问答服务。

**Acceptance Criteria:**
- [ ] `retrieve_service` 用 `QueryFusionRetriever`：MilvusVectorStore（包为 `VectorStoreIndex.as_retriever()`）+ pg_trgm 关键词（`BaseRetriever` 子类）+ RRF 融合，`num_queries=1` 关闭查询生成
- [ ] 检索命中子块后回填父块上下文（`parent_content`）
- [ ] 摘要命中后回填该文档的父块
- [ ] `qa_service` 用 `ContextChatEngine.from_defaults(retriever=fusion_retriever, memory=ChatMemoryBuffer(chat_store=RedisChatStore), llm=OpenAILike(model=..., api_base=vllm_url, api_key="...", is_chat_model=True, context_window=...))`
- [ ] `qa_service.chat()` 同步返回 answer + sources + session_id + tokens
- [ ] `make test` 通过

### US-015: rag-engine NATS 订阅与 gRPC server
**Description:** 作为 AI 层开发者，我需要实现 parse_worker NATS 订阅与 rag-engine gRPC server。

**Acceptance Criteria:**
- [ ] `parse_worker` 订阅 NATS `ani.tasks.kb.parse`，领取任务
- [ ] 领取后调 Core `/objects/{id}/download` 下载 → 解析 → 分块 → 摘要 → 直连 Milvus 写入子块 + 摘要 → 写 kb_chunks 表
- [ ] 回写任务状态，更新 `kb_documents.parse_status`
- [ ] gRPC server 实现 `Query` RPC（仅同步）
- [ ] `make test` 通过

### US-016: ani-gateway KB 路由与 gRPC client
**Description:** 作为网关开发者，我需要补齐 ani-gateway 的 KB 路由并用 gRPC client 替换 stub handler。

**Acceptance Criteria:**
- [ ] 补齐 3 个缺失路由（citations/sessions/permissions），12 端点全部就位
- [ ] 用 gRPC client 替换 9 个 stub handler
- [ ] `/api/v1/svc/knowledge-bases/*` 路由到 kb-service（gRPC）
- [ ] `/api/v1/vector-stores/*` 路由到 Core vector-store
- [ ] `/api/v1/objects/*` 路由到 Core object-store
- [ ] RBAC + 租户注入 + 限流生效
- [ ] `make validate-architecture` 通过
- [ ] `make test` 通过

### US-017: ani-gateway SSE 端点
**Description:** 作为网关开发者，我需要实现 SSE 流式问答端点。

**Acceptance Criteria:**
- [ ] SSE 端点 `GET /api/v1/svc/knowledge-bases/{kb_id}/query/stream` 在 gateway 持有
- [ ] 调 rag-engine 检索 + prompt → vLLM streaming → token 透传
- [ ] 末尾发送 sources 事件
- [ ] SSE 错误处理（400/401）
- [ ] `make test` 通过

### US-018: 异步任务链路端到端验证
**Description:** 作为平台开发者，我需要验证 outbox → NATS → rag-engine → 状态回写的完整异步链路。

**Acceptance Criteria:**
- [ ] 上传文档后 outbox 事件发布到 NATS `ani.tasks.kb.parse`
- [ ] rag-engine 订阅领取任务
- [ ] 解析 → 分块 → 摘要 → 向量化 → 写 kb_chunks 完整执行
- [ ] `kb_documents.parse_status` 正确回写（pending → parsing → indexing → ready/failed）
- [ ] 失败可重试
- [ ] 端到端可复跑

### US-019: Console 前端列表页与 3 tab 布局
**Description:** 作为前端开发者，我需要实现知识库列表页与单库 3 tab 布局。

**Acceptance Criteria:**
- [ ] 列表页 `repo/frontends/console/src/routes/kb/index.tsx` 展示知识库表格（名称/状态/文档数/创建时间）
- [ ] 列表页含新建模态框（名称/描述/嵌入模型/chunk_size/top_k）、删除确认、状态 Tag
- [ ] `__root.tsx` 实现 3 tab 布局（概览/文档/问答）
- [ ] parentRoute 改造正确
- [ ] P1 占位页（data-ingestion/lab/permissions/history）4 个
- [ ] Typecheck/lint passes
- [ ] Verify in browser: 列表加载/空态/错误态

### US-020: Console 概览页与文档页
**Description:** 作为前端开发者，我需要实现概览页与文档页。

**Acceptance Criteria:**
- [ ] 概览页展示入库配置（Embedding/chunk_size/OCR）+ 问答配置（TopK/score_threshold/检索策略）+ P1 规划区
- [ ] 改 Embedding/chunk_size 触发重建（调 `/rebuild`）
- [ ] 文档页工具栏 + 表格，支持拖拽多文件上传 + 自定义元数据
- [ ] 文档页支持状态筛选、重试（调 `/reparse`）
- [ ] 文档页展示解析详情（父子块层级 + 摘要 + metadata）
- [ ] Typecheck/lint passes
- [ ] Verify in browser: 上传 loading/空态/错误态/解析详情

### US-021: Console 问答页
**Description:** 作为前端开发者，我需要实现问答页支持多会话、同步/流式问答与引用展示。

**Acceptance Criteria:**
- [ ] 问答页左侧会话列表，右侧消息流
- [ ] 支持同步问答（`POST /query`）与 SSE 流式问答（`GET /query/stream`）
- [ ] TopK 可调节
- [ ] 引用卡片展示子块内容 + 父块上下文
- [ ] Typecheck/lint passes
- [ ] Verify in browser: 问答 loading/空态/错误态/SSE 增量输出/结束反馈

### US-022: 端到端联调与 live gate
**Description:** 作为平台开发者，我需要完成端到端联调、测试与文档闭环。

**Acceptance Criteria:**
- [ ] 端到端链路：创建/删除 KB → 上传 7 格式文档异步解析 → 概览配置改触发重建 → 问答多会话+同步/流式+TopK+引用
- [ ] 父子分块（2048/256-512）验证
- [ ] 文档级摘要验证
- [ ] 自定义元数据验证
- [ ] SSE 流式验证
- [ ] RLS 隔离验证
- [ ] 解析重试验证
- [ ] 全库重建验证
- [ ] Core 文档级删除验证
- [ ] pg_trgm 检索验证
- [ ] live gate 通过
- [ ] `make test` 全绿
- [ ] `make validate-services` 通过
- [ ] `make validate-architecture` 通过
- [ ] development-records + CURRENT-SPRINT + ANI-06 更新

## 4. Functional Requirements

- FR-1: 系统必须支持知识库元数据 CRUD（创建/查询/列表/软删除），通过 kb-service gRPC 承接 proto 10 个 RPC
- FR-2: 系统必须支持文档上传两步式 pre-signed URL（GetDocumentUploadURL + NotifyDocumentUploaded），客户端 PUT MinIO 后通知 kb-service
- FR-3: 系统必须支持文档异步解析：outbox 同事务派发 → NATS → rag-engine 订阅 → 解析 → 分块 → 摘要 → 向量化
- FR-4: 系统必须支持父子分块：子块 256-512 tokens（SentenceSplitter，优先句子边界）+ 父块 2048 tokens（固定窗套叠）
- FR-5: 系统必须支持文档级摘要：拼接前 N 父块 → LLM 生成 200-500 字 → 向化存 Milvus（chunk_type=doc_summary）参与检索
- FR-6: 系统必须支持嵌入统一在 rag-engine（HuggingFaceEmbedding，写入与查询统一），MilvusVectorStore 直连 Milvus（v1.2 架构）
- FR-7: 系统必须支持混合检索：MilvusVectorStore 向量检索 + pg_trgm 关键词检索 + RRF 融合 + 父块回填
- FR-8: 系统必须支持同步问答（`POST /query`，返回 JSON）与 SSE 流式问答（`GET /query/stream`，gateway 持有端点透传 vLLM streaming）
- FR-9: 系统必须支持多会话管理：Redis 缓存会话（`ani:prod:session:kb:{session_id}`，TTL 24h，LTRIM 20）
- FR-10: 系统必须支持文档格式：PDF（文本层 DoclingReader + 扫描件 AI 服务 PaddleOCR API）、Word/Excel/PPT/MD/TXT（DoclingReader）
- FR-11: 系统必须支持表格处理：父块 HTML 表格、子块逐行自然语言、合并单元格、跨页拆分、大表按行分组保留表头、表格元数据（content_type=table/table_index/caption）
- FR-12: 系统必须支持图片处理：提取上传 MinIO、占位节点 `[图片: caption](OSS_URL)`、向量化占位节点、问答渲染 `<img>`
- FR-13: 系统必须支持 Core 向量文档级删除（`DELETE /vector-stores/{id}/documents?filter=...`）
- FR-14: 系统必须支持自定义元数据（custom_metadata JSONB）从上传到检索全链路继承
- FR-15: 系统必须支持 RLS 多租户隔离（P0 无 KB 级权限校验，仅靠 RLS）
- FR-16: 系统必须支持 Console 前端列表页 + 3 tab（概览/文档/问答）+ P1 占位页
- FR-17: 系统必须支持概览页配置修改触发全库重建（调 `/rebuild`）
- FR-18: 系统必须支持文档解析重试（调 `/reparse`）

## 5. Non-Goals (Out of Scope)

- **KB 级权限控制（ACL + RBAC）：** P1 预留，P0 仅 proto/OpenAPI 端点声明 + 前端占位，handler 返回 UNIMPLEMENTED
- **操作审计历史：** P1 预留，P0 不建 `kb_audit_log` 表
- **数据接入（URL 抓取/对象存储/批量文件）：** P1 预留，P0 不建 `kb_data_sources` 表
- **检索实验室（调试/评测/对比）：** P1 预留，P0 不建 `kb_eval_sets`/`kb_eval_cases`/`kb_eval_runs` 表
- **rerank：** P1 预留，P0 不引入 `llama-index-postprocessor-sbert-rerank` + `sentence-transformers` + `torch`
- **本地安装 PaddleOCR：** P0 通过 AI 服务 API 调用 PaddleOCR，不本地依赖
- **CoreAPIVectorStore 适配器：** v1.2 已移除，rag-engine 直连 Milvus，不回退
- **Core 端嵌入：** v1.2 已移除，嵌入统一在 rag-engine，不回退
- **知识库业务资源写入 Core OpenAPI：** KB 资源只进 `services/v1.yaml`，不回流 Core `v1.yaml`
- **KB 业务逻辑在 ani-gateway：** gateway 只做路由/RBAC/SSE，不实现 KB 业务
- **rag-engine 实现 SSE：** SSE 在 gateway，rag-engine 仅同步 Query

## 6. Design Considerations

- 服务拆分：kb-service（薄业务层，Python 3.11 + FastAPI）+ rag-engine（厚 AI 层，Python 3.11 + FastAPI + LlamaIndex 0.11+）
- 架构优化（v1.2）：rag-engine 直连 Milvus（LlamaIndex MilvusVectorStore），移除 CoreAPIVectorStore 适配器，减少一层 HTTP 调用
- 嵌入统一：写入与查询嵌入均由 rag-engine 的 HuggingFaceEmbedding 完成，MilvusVectorStore 本身不做嵌入，经 VectorStoreIndex 包装后由 Index 层嵌入
- 分块策略：父子分块（父块 2048、子块 256-512，SentenceSplitter）+ 文档级摘要 + 元数据
- 检索策略：向量 + 关键词（pg_trgm）混合 + RRF，子块检索 + 父块回填
- 异步任务：DB outbox 派发 + NATS，避免分布式事务
- 问答模式：同步 JSON（kb-service.Query → rag-engine.Query）+ SSE 流式（gateway 持有端点 → rag-engine 检索 + vLLM streaming）
- 前端路由：TanStack Router，`/kb` 列表 → `/kb/$kbId/{overview|documents|chat}`

## 7. Technical Considerations

- **权威源：** `repo/api/proto/kb/v1/kb_service.proto`（gRPC 契约）、`repo/api/openapi/services/v1.yaml`（Services REST）、`repo/api/openapi/v1.yaml`（Core REST）
- **服务前缀：** Services `/api/v1/svc`，Core `/api/v1`
- **idempotency_key：** CreateKB、NotifyDocumentUploaded、Query 必填
- **依赖就绪状态：** Core VectorStore API（集合级）Ready、Core ObjectStore API Ready、task-service outbox Ready、NATS `ani.tasks.kb.parse` Ready、PostgreSQL 4 张 KB 表 Ready、Redis/Milvus/MinIO Ready、model-service ListModels Ready（无 capability 过滤）
- **需新增依赖：** Core VectorStore API 文档级 DELETE（Phase A3）、kb_chunks 表 + pg_trgm（Phase A6）、AI 服务 OCR API（Phase A8）、model proto ocr capability（Phase A4）、ani-gateway SSE 端点（Phase G3）
- **错误码：** 见 plan.md §8（404 kb.not_found、422 doc.unsupported_type、413 doc.too_large、422 doc.checksum_mismatch、200 doc.parse_failed、503 inference.unavailable、409 kb.rebuilding 等）

## 8. Success Metrics

- 端到端闭环可复跑：创建 KB → 上传 7 格式文档 → 异步解析 → 父子分块 + 摘要 → 混合检索 → 同步/SSE 问答
- 契约一致：proto 与 services/v1.yaml 一致、SDK 无漂移、`make validate-services` 通过
- 架构合规：rag-engine 直连 Milvus（v1.2）、kb-service 走 Core OpenAPI、SSE 在 gateway、`make validate-architecture` 通过
- 测试全绿：后端 + 前端测试全绿、live gate 通过、`make test` 全绿
- 文档闭环：development-records + CURRENT-SPRINT + ANI-06 更新

## 9. Open Questions

- 文档上传后是否需要未来单独的解析任务历史页（P1）
- SSE 结束事件和异常事件是否需要统一前端事件协议
- `doc_count` 是否需要拆分为可检索文档数与总文档数
- OCR API 是放在 inference-service 还是独立 ocr-service（plan.md 建议二选一，P0 实现时确认）
- 全库重建期间是否拒绝写入（409 kb.rebuilding 的具体语义边界）

## 10. ANI Boundaries

| Item | Value |
|------|-------|
| Product line | core（主）+ console（前端消费）+ services（kb-service/rag-engine） |
| Code scope | `repo/services/kb-service/`（新建）、`repo/ai/rag-engine/`（扩展）、`repo/` Core handler 扩展、`repo/frontends/console/src/routes/kb/`（新建） |
| OpenAPI authority | Core `v1.yaml` 新增 `DELETE /vector-stores/{id}/documents`；Services `v1.yaml` 修复 + 新增 reparse/config/rebuild/models 端点；proto `kb_service.proto` 修复 + 新增 3 个 P1 RPC 声明 |
| Frozen exclusions | 不把 KB 资源写入 Core `v1.yaml` 业务路径；不在 Core 实现 KB 业务；不在 gateway 实现 KB 业务；不本地安装 PaddleOCR |
| idempotency_key | required on: CreateKB、NotifyDocumentUploaded、Query |
| Module main doc | N/A（Core 平台层，非 Console/BOSS UI 模块；Console 侧已有 `repo/services/docs/console-modules/knowledge/knowledge-base.md`） |
| Non-Goals | KB 级权限、操作审计、数据接入、检索实验室、rerank（P1 预留） |
