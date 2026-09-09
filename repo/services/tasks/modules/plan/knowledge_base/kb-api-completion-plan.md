# 知识库管理接口补全清单

## 0. 接口全景总览

路径前缀：`/api/v1/svc`。状态标记：✅ 已实现 ｜ 🟡 契约已声明待实现 ｜ 🔴 契约缺失需新增。

| # | 能力域 | 方法 | 路径 | operationId | 状态 |
|---|---|---|---|---|---|
| 1 | KB 主对象 | GET | `/knowledge-bases` | listKnowledgeBases | ✅ |
| 2 | KB 主对象 | POST | `/knowledge-bases` | createKnowledgeBase | ✅ |
| 3 | KB 主对象 | GET | `/knowledge-bases/{kb_id}` | getKnowledgeBase | ✅ |
| 4 | KB 主对象 | DELETE | `/knowledge-bases/{kb_id}` | deleteKnowledgeBase | ✅ |
| 5 | KB 主对象 | PUT | `/knowledge-bases/{kb_id}` | updateKnowledgeBase | 🔴 |
| 6 | 文档 | GET | `.../documents` | listKnowledgeBaseDocuments | ✅ |
| 7 | 文档 | POST | `.../documents` | getDocumentUploadURL | ✅ |
| 8 | 文档 | POST | `.../documents/{doc_id}/notify-uploaded` | notifyDocumentUploaded | ✅ |
| 9 | 文档 | DELETE | `.../documents/{doc_id}` | deleteKnowledgeBaseDocument | ✅ |
| 10 | 文档 | GET | `.../documents/{doc_id}` | getKnowledgeBaseDocument | 🔴 |
| 11 | 文档 | GET | `.../documents/{doc_id}/chunks` | —（建议 listKnowledgeBaseDocumentChunks） | 🔴 |
| 12 | 文档 | POST | `.../documents/{doc_id}/reparse` | reparseKnowledgeBaseDocument | 🟡 |
| 13 | 问答 | POST | `.../query` | queryKnowledgeBase | ✅ |
| 14 | 问答 | GET | `.../query/stream` | streamQueryKnowledgeBase（SSE） | ✅ |
| 15 | 问答 | GET | `.../citations` | listKnowledgeBaseCitations | 🟡 |
| 16 | 问答 | GET | `.../sessions` | listKnowledgeBaseSessions | 🟡 |
| 17 | 问答 | GET | `.../sessions/{session_id}/messages` | —（建议 listKnowledgeBaseSessionMessages） | 🔴 |
| 18 | 问答 | DELETE | `.../sessions/{session_id}` | —（建议 deleteKnowledgeBaseSession） | 🔴 |
| 19 | 权限 | GET | `.../permissions` | —（建议 getKnowledgeBasePermissions） | 🔴 |
| 20 | 权限 | PUT | `.../permissions` | updateKnowledgeBasePermissions | 🟡 |
| 21 | 操作历史 | GET | `.../audit-logs` | —（建议 listKnowledgeBaseAuditLogs） | 🔴 |
| 22 | 配置/重建 | GET | `.../config` | getKnowledgeBaseConfig | 🟡 |
| 23 | 配置/重建 | PUT | `.../config` | updateKnowledgeBaseConfig | 🟡 |
| 24 | 配置/重建 | POST | `.../rebuild` | rebuildKnowledgeBase | 🟡 |
| 25 | 配置/重建 | GET | `.../models` | listKnowledgeBaseModels | 🟡 |

完整目标接口面 = **25 个操作**（✅ 11 ｜ 🟡 8 ｜ 🔴 6）。

---

## 第一部分：已声明待实现（🟡 8 个操作）

契约（OpenAPI + proto）已就绪，仅缺实现。分两批：P1 三声明（kb-service handler 返回 UNIMPLEMENTED → HTTP 501）、US-002 五端点（Gateway 未注册路由，记录于 `repo/architecture/services-route-baseline.yaml` 标记 `spec_not_in_code` + `accepted_baseline`）。

### 1.1 ListKBCitations — 知识库引用来源列表

| 项 | 内容 |
|---|---|
| HTTP | `GET /knowledge-bases/{kb_id}/citations?limit=20&cursor=`（limit 1-100，cursor 分页） |
| gRPC | `rpc ListKBCitations(ListKBCitationsRequest) returns (ListKBCitationsResponse)`（proto L47-48, L245-265） |
| 契约位置 | services/v1.yaml L2318-2335 |
| 幂等 | GET，无需 idempotency_key |

**功能：** 返回某个 KB 问答时实际引用过的来源片段列表，支撑前端「哪些回答引用了哪些文档」的溯源视图（console 模块 `kb-source-citation.md`，TASK-SVC-014）。与问答页 citations 区块同源。

**响应 schema（KBCitationListResponse / KBCitation，已有）：**

```yaml
KBCitation:
  id:         uuid        # 引用记录 ID
  kb_id:      uuid
  doc_id:     uuid        # 被引用文档
  file_name:  string
  page:       int | null  # 页码（可空）
  content:    string      # 引用的内容片段
  score:      float | null
  created_at: date-time
KBCitationListResponse: { items: KBCitation[], next_cursor: string | null }
```

**错误码：** 401 / 403 / 404。空列表返回 `200 + items: []`（非 404）。租户边界：仅当前租户。

**实现前置：**
- 数据来源缺失：问答时需持久化引用记录（建议 `kb_messages` 已有 sources 字段则聚合查询，否则建 `kb_citations` 表）。当前 Query/SSE 已在 `_persist_assistant` 落库 sources，可实现基于 `kb_messages` 的聚合。
- 替换 `repo/services/kb-service/app/api/p1_rpcs.py` 中 `list_kb_citations` 的 UNIMPLEMENTED stub。
- Gateway 路由已注册（citations），kb-service client 非 nil 时透传，实现后自动生效。

**已记录待补边界（kb-source-citation.md）：** 引用单条详情 GET（`{citation_id}` 未声明）、引用导出、与向量检索 debug 视图——本文档不扩写，留待后续规划。

### 1.2 ListKBSessions — 知识库对话历史列表

| 项 | 内容 |
|---|---|
| HTTP | `GET /knowledge-bases/{kb_id}/sessions?limit=20&cursor=`（limit 1-100，cursor 分页） |
| gRPC | `rpc ListKBSessions(ListKBSessionsRequest) returns (ListKBSessionsResponse)`（proto L49-50, L267-285） |
| 契约位置 | services/v1.yaml L2337-2354 |
| 幂等 | GET，无需 |

**功能：** 返回 KB 下的对话会话列表，支撑对话历史侧栏（console 模块 `kb-chat-history.md`，TASK-SVC-014）。点击会话携带 `session_id` 进入问答页续聊。

**响应 schema（KBSessionListResponse / KBSession，已有）：**

```yaml
KBSession:
  id:             uuid
  kb_id:          uuid
  message_count:  int          # 消息数
  last_query:     string | null
  created_at:     date-time
  last_active_at: date-time | null
```

**错误码：** 401 / 403 / 404。排序以服务端默认为准（建议 last_active_at DESC）。

**实现前置：**
- 底层数据已存在：kb-service 有 `repositories/message`（kb_messages 表）+ Redis 会话缓存（key `ani:prod:session:kb:{session_id}`，TTL 24h，LTRIM 20）。缺「按 KB 聚合列出所有 session」的查询：可基于 `kb_messages` 按 session_id 分组聚合（message_count、last_query、last_active_at）。
- 替换 `p1_rpcs.py` 的 `list_kb_sessions` stub。
- 会话创建/续聊入口在 query / query/stream（不在本接口）。

### 1.3 UpdateKBPermissions — 更新 KB 访问权限（P1）

| 项 | 内容 |
|---|---|
| HTTP | `PUT /knowledge-bases/{kb_id}/permissions` |
| gRPC | `rpc UpdateKBPermissions(UpdateKBPermissionsRequest) returns (KnowledgeBase)`（proto L51-52, L287-293） |
| 契约位置 | services/v1.yaml L2356-2377 |
| 幂等 | **idempotency_key 必填**（uuid） |

**功能：** KB 级 ACL 控制入口（console 模块 `kb-permissions.md`）。设置 `public_read`（是否公开可读）与 `allowed_user_ids`（白名单），返回更新后的 KnowledgeBase。

**请求 schema（UpdateKBPermissionsRequest，已有）：**

```yaml
idempotency_key:  uuid      # 必填
public_read:      bool
allowed_user_ids: string[]  # 租户内用户白名单
```

**错误码：** 400（幂等键缺失/校验失败）/ 401 / 403 / 404 / 409（幂等冲突）。

**实现前置：**
- **建表**：`kb_permissions`（plan §11.1 DDL 已预留）：

```sql
CREATE TABLE kb_permissions (
  kb_id          UUID PRIMARY KEY REFERENCES knowledge_bases(id) ON DELETE CASCADE,
  public_read    BOOLEAN NOT NULL DEFAULT FALSE,
  allowed_user_ids UUID[] NOT NULL DEFAULT '{}',
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

- **鉴权链路**：P0 仅靠 RLS 租户隔离（FR-15），本接口需接入 Core RBAC scope（KB 级 ACL + RBAC，仅管理员可改权限）。
- 查询侧联动：list/get/query 需增加 KB 级权限校验（public_read 或在白名单内）。
- 替换 `p1_rpcs.py` 的 `update_kb_permissions` stub。

### 1.4 reparseKnowledgeBaseDocument — 重新解析文档（US-002）

| 项 | 内容 |
|---|---|
| HTTP | `POST /knowledge-bases/{kb_id}/documents/{doc_id}/reparse` |
| gRPC | **proto RPC 未声明**——实现前需先补 `rpc ReparseDocument(...)` 契约 |
| 契约位置 | services/v1.yaml L2379-2407；Gateway 未注册（baseline `spec_not_in_code`） |
| 幂等 | **idempotency_key 必填**（ReparseDocumentRequest 唯一字段） |

**功能：** 对解析失败（`parse_status=failed`）或需重试的文档触发重新解析，`parse_status` 回到 `pending`，覆盖现有分块（UX §6.3 Popconfirm「重新解析将覆盖现有分块」，PRD FR-18，console US-020 文档页「状态筛选 + 重试」）。

**请求/响应：**

```yaml
ReparseDocumentRequest: { idempotency_key: uuid }   # 必填
响应: 202 + AsyncTask（task_type 建议 kb.document.reparse）
```

**错误码：** 202 / 400 / 401 / 403 / 404 / 409（`kb.rebuilding`：KB 重建期间拒绝）/ 503（`inference.unavailable` 解析服务暂不可用）。

**实现前置（底层能力大半就绪）：**
- ParseOrchestrator 已实现幂等重解析（RAG-REFACTOR-STEP-5）：删旧 chunks + Core 向量再写入，reparse 幂等语义已验证。
- 需补：proto RPC + Gateway 路由注册 + async_tasks 记录（复用 notify-uploaded 的任务派发模式：同事务写 async_tasks + outbox_events → 派发到 NATS `ani.tasks.kb.parse`）。
- 从 baseline 移除该条目（`make validate-architecture` 的 spec_not_in_code 对账）。

### 1.5 getKnowledgeBaseConfig — 读取 KB 配置（US-002）

| 项 | 内容 |
|---|---|
| HTTP | `GET /knowledge-bases/{kb_id}/config` |
| gRPC | **proto RPC 未声明**——需补 `rpc GetKBConfig(...)` |
| 契约位置 | services/v1.yaml L2410-2426；Gateway 未注册 |
| 幂等 | GET，无需 |

**功能：** 返回 KB 入库配置与问答配置，支撑前端概览页配置区回显（UX §4.3，console US-020「概览页展示入库配置 + 问答配置」）。

**响应 schema（KBConfig，已有）：**

```yaml
embedding_model:    string            # 嵌入模型名，修改触发全库重建
chunk_size:         int (1-8192)      # 分块大小（tokens）
ocr_enabled:        bool (default false)
top_k:              int (1-20)
score_threshold:    float (0.0-1.0)
retrieval_strategy: vector | hybrid   # 检索策略
```

**错误码：** 200 / 401 / 403 / 404。

**实现前置：** `knowledge_bases` 表已有 embedding_model/chunk_size/top_k/score_threshold 字段；`ocr_enabled`、`retrieval_strategy` 若无列需补迁移（rag-engine 端 `retrieval_mode` 迁移已有先例）。其余为 RPC + 路由注册。

### 1.6 updateKnowledgeBaseConfig — 更新 KB 配置（US-002）

| 项 | 内容 |
|---|---|
| HTTP | `PUT /knowledge-bases/{kb_id}/config` |
| gRPC | **proto RPC 未声明**——需补 `rpc UpdateKBConfig(...)` |
| 契约位置 | services/v1.yaml L2427-2452；Gateway 未注册 |
| 幂等 | **idempotency_key 必填** |

**功能：** 更新 KB 配置。**修改 `embedding_model` 或 `chunk_size` 触发全库重建**（UX §4.3 保存配置 → Popconfirm「修改配置将触发全库重建」→ POST /rebuild；PRD FR-17）。返回更新后的 KnowledgeBase（含新 status）。

**请求 schema（UpdateKBConfigRequest，已有）：** 与 KBConfig 同字段 + `idempotency_key: uuid`（必填）。全部配置字段可选，仅传变更项。

**错误码：** 200 / 400 / 401 / 403 / 404 / 409（rebuilding 期间拒绝）。

**实现前置：**
- 触发重建的联动逻辑：检测 embedding_model / chunk_size 变化 → 自动调 rebuild 语义（写 async_tasks + outbox_events，KB status → rebuilding）。
- 配置持久化迁移（ocr_enabled / retrieval_strategy，同 1.5）。

### 1.7 rebuildKnowledgeBase — 全库重建（US-002）

| 项 | 内容 |
|---|---|
| HTTP | `POST /knowledge-bases/{kb_id}/rebuild` |
| gRPC | **proto RPC 未声明**——需补 `rpc RebuildKB(...)` |
| 契约位置 | services/v1.yaml L2454-2479；Gateway 未注册 |
| 幂等 | **idempotency_key 必填**（RebuildKnowledgeBaseRequest 唯一字段） |

**功能：** 触发全库重建：KB `status` 转为 `rebuilding`（UX §6.2 rebuilding 状态），对全部 `parse_status=ready` 文档重新走 解析 → 分块 → 摘要 → 嵌入 → 向量写入 管线。**重建期间拒绝写操作**（上传/删除/查询写入 409 `kb.rebuilding`；PRD FR-17；409 具体语义边界见 PRD §8 待确认项）。

**请求/响应：**

```yaml
RebuildKnowledgeBaseRequest: { idempotency_key: uuid }   # 必填
响应: 202 + AsyncTask（task_type 建议 kb.rebuild）
```

**错误码：** 202 / 400 / 401 / 403 / 404 / 409（已在重建中）。

**实现前置：**
- 复用 ParseOrchestrator 管线 + reparse 幂等模式（删旧 chunks + Core 向量再写入），按文档批量编排。
- 重建进度可写 AsyncTask.progress_pct（0-100，前端轮询 Core `GET /api/v1/tasks/{task_id}`）。

### 1.8 listKnowledgeBaseModels — 可用模型列表（US-002）

| 项 | 内容 |
|---|---|
| HTTP | `GET /knowledge-bases/{kb_id}/models` |
| gRPC | **proto RPC 未声明**——需补 `rpc ListKBModels(...)` |
| 契约位置 | services/v1.yaml L2481-2499；Gateway 未注册 |
| 幂等 | GET，无需 |

**功能：** 返回当前租户可用于该 KB 的嵌入模型与推理模型列表，支撑概览页 embedding_model 选择项（UX §4.3 入库配置区）。

**响应 schema（ModelList + Model，已有）：**

```yaml
ModelList: { embedding_models: Model[], inference_models: Model[] }
Model:
  id / name / display_name / description
  source: upload | huggingface | modelscope | builtin
  capabilities: string[]        # 如 embedding / text-generation
  status: pending | downloading | ready | error | deleted
  total_size_bytes / created_at / updated_at / versions
```

**错误码：** 200 / 401 / 403 / 404。

**实现前置：**
- 数据来源：过滤租户可用模型。嵌入模型可基于内置 + 已导入（对齐 Model 中心 `/models` 列表 + capability=embedding）；推理模型对齐租户 inference-services（vLLM 服务名）。
- 需明确哪些模型进入候选（如 `status=ready` 且 capability 匹配）——建议实现时与模型中心团队对齐口径。

---

## 第二部分：需新增契约（🔴 6 个操作）

OpenAPI 与 proto 均未声明，需按「契约 → 迁移 → 实现 → 路由」完整流程补全。schema 为建议定义（对齐现有命名与分页风格）。

### 2.1 updateKnowledgeBase — 更新知识库基础信息

| 项 | 内容 |
|---|---|
| HTTP | `PUT /knowledge-bases/{kb_id}`（新增） |
| gRPC | `rpc UpdateKB(UpdateKBRequest) returns (KnowledgeBase)`（新增） |
| 幂等 | idempotency_key 必填 |
| 依赖 | 无新表；`knowledge_bases` 已有 name/description 列 |

**功能：** 修改 KB 名称与描述。当前缺口：配置类字段走 `/config`，但 `name` / `description` 无任何接口可改（前端列表页重命名、详情页改描述无支撑）。

**建议请求：**

```yaml
UpdateKBRequest:
  idempotency_key: uuid      # 必填
  name: string               # 可选，仅传变更项
  description: string       # 可选
```

**响应：** 200 + KnowledgeBase。**错误码：** 400 / 401 / 403 / 404 / 409（rebuilding 期间可允许或拒绝，建议拒绝写操作保持一致性）。

### 2.2 getKnowledgeBaseDocument — 文档详情

| 项 | 内容 |
|---|---|
| HTTP | `GET /knowledge-bases/{kb_id}/documents/{doc_id}`（新增；**proto 已有 `rpc GetDocument`**，YAML 该路径当前只声明了 DELETE——契约不一致需修复） |
| gRPC | `GetDocument(GetDocumentRequest) returns (KBDocument)`（已有，kb-service 已实现） |
| 幂等 | GET，无需 |
| 依赖 | 无新表 |

**功能：** 单文档详情：解析状态、错误信息、chunk_count、custom_metadata、parsed_at。支撑文档页单条查看与失败详情展示（console US-020「文档页展示解析详情」入口）。

**响应 schema（KBDocument，已有）：** `id / tenant_id / kb_id / file_name / file_type(pdf|docx|xlsx|pptx|md|txt) / file_size_bytes / parse_status(pending|parsing|indexing|ready|failed) / chunk_count / error_message / custom_metadata / created_at / parsed_at`。

**错误码：** 200 / 401 / 403 / 404。**实现前置：** 仅需 OpenAPI 补声明 + Gateway 注册路由（gRPC 侧已就绪，工作量最小）。

### 2.3 listKnowledgeBaseDocumentChunks — 文档解析详情（分块）

| 项 | 内容 |
|---|---|
| HTTP | `GET /knowledge-bases/{kb_id}/documents/{doc_id}/chunks?limit=&cursor=&chunk_type=`（新增） |
| gRPC | `rpc ListDocumentChunks(...)`（新增） |
| 幂等 | GET，无需 |
| 依赖 | **`kb_chunks` 表已存在**（child/parent/doc_summary 三类型 + pg_trgm GIN 索引，M2.1-TASK-A 迁移已落地） |

**功能：** 展示文档的解析结果明细：父子分块层级（parent → child）+ doc_summary + metadata，支撑 US-020 验收项「文档页展示解析详情（父子块层级 + 摘要 + metadata）」——当前该验收项**无接口支撑**。

**建议响应：**

```yaml
KBChunk:
  id: uuid
  doc_id: uuid
  chunk_type: parent | child | doc_summary
  parent_id: uuid | null      # child 指向 parent
  content: string
  token_count: int
  metadata: object            # 页码、标题等
  created_at: date-time
DocumentChunkListResponse: { items: KBChunk[], next_cursor: string | null }
```

**筛选参数：** `chunk_type`（可选，按类型过滤）；分页 limit/cursor 对齐列表风格。**错误码：** 200 / 401 / 403 / 404。

### 2.4 listKnowledgeBaseSessionMessages — 会话消息明细

| 项 | 内容 |
|---|---|
| HTTP | `GET /knowledge-bases/{kb_id}/sessions/{session_id}/messages?limit=&cursor=`（新增） |
| gRPC | `rpc ListSessionMessages(...)`（新增） |
| 幂等 | GET，无需 |
| 依赖 | `kb_messages` 表已存在（message repository 已实现）；Redis 会话缓存仅存最近 20 条（LTRIM），完整历史以 DB 为准 |

**功能：** 回放指定会话的完整对话（user / assistant 消息 + sources 引用 + token 用量）。当前缺口：`/sessions` 能列出会话，但**无法查看会话内容**——续聊时前端只能从 Redis 缓存的最近 20 条恢复上下文，历史会话无法回放。

**建议响应：**

```yaml
KBMessage:
  id: uuid
  session_id: uuid
  role: user | assistant
  content: string
  sources: SourceChunk[]      # assistant 消息的引用来源（对齐 KBQueryResponse.sources）
  input_tokens / output_tokens: int | null
  created_at: date-time
SessionMessageListResponse: { items: KBMessage[], next_cursor: string | null }
```

**错误码：** 200 / 401 / 403 / 404（session 不存在或不属于该 KB）。排序建议 created_at ASC（对话顺序回放）。

### 2.5 deleteKnowledgeBaseSession — 删除会话

| 项 | 内容 |
|---|---|
| HTTP | `DELETE /knowledge-bases/{kb_id}/sessions/{session_id}`（新增） |
| gRPC | `rpc DeleteSession(...)`（新增） |
| 幂等 | DELETE 天然幂等，建议仍带 idempotency_key（对齐项目写操作约定）或按 idempotent delete 语义（重复删除返回 204） |
| 依赖 | kb_messages 按会话删除 + Redis 缓存 key 失效 |

**功能：** 删除指定会话（消息记录 + Redis 会话缓存），支撑对话历史侧栏的会话管理。[kb-chat-history.md](../../docs/console-modules/knowledge/kb-chat-history.md) L63 明确标注「YAML 未声明」，操作可用性矩阵中「删除/重命名会话」对全部角色不可用（待补）。

**响应：** 204。**错误码：** 401 / 403 / 404 / 409（rebuilding 期间可允许——会话删除不影响索引，建议允许）。

**同步待补（低优先级）：** 会话重命名 PATCH——console 文档同样标注未声明，可随本接口一并评估。

### 2.6 getKnowledgeBasePermissions — 读取 KB 权限

| 项 | 内容 |
|---|---|
| HTTP | `GET /knowledge-bases/{kb_id}/permissions`（新增） |
| gRPC | `rpc GetKBPermissions(...) returns (KBPermissions)`（新增） |
| 幂等 | GET，无需 |
| 依赖 | `kb_permissions` 表（与 1.3 UpdateKBPermissions 共建，plan §11.1 DDL） |

**功能：** 回显当前 KB 权限配置（public_read + allowed_user_ids）。当前缺口：只有 PUT 无 GET——**前端权限配置页无法初始化回显**，用户无法知道当前权限状态。

**建议响应：**

```yaml
KBPermissions:
  kb_id: uuid
  public_read: bool
  allowed_user_ids: string[]   # 建议返回展示名（display_name）便于前端渲染
  updated_at: date-time
```

**错误码：** 200 / 401 / 403 / 404（无记录时返回默认值 `public_read=false, allowed_user_ids=[]`，而非 404）。与 1.3 组成完整读写对，实现批次建议合并。

### 2.7 listKnowledgeBaseAuditLogs — 知识库操作历史

| 项 | 内容 |
|---|---|
| HTTP | `GET /knowledge-bases/{kb_id}/audit-logs?limit=&cursor=&action=`（新增） |
| gRPC | `rpc ListKBAuditLogs(...)`（新增） |
| 幂等 | GET，无需 |
| 依赖 | **建表 `kb_audit_log`**（plan §11.2 DDL 已预留，P0 明确不建）；写侧埋点（各写操作同事务插入） |

**功能：** 查询 KB 的操作审计历史（谁在何时对哪个资源做了什么、结果如何），支撑「操作历史」页面（PRD P1 五大能力之一）。可对齐现有 `/tenant-plans/{planId}/audit-logs`（listTenantPlanAuditLogs）的模式。

**建议表结构（plan §11.2）：**

```sql
CREATE TABLE kb_audit_log (
  id           BIGSERIAL PRIMARY KEY,
  tenant_id    UUID NOT NULL,
  kb_id        UUID NOT NULL,
  actor_id     UUID NOT NULL,        -- 操作人
  action       TEXT NOT NULL,        -- kb.create / kb.delete / doc.upload / doc.reparse / kb.rebuild / config.update / perm.update ...
  resource_id  UUID,                 -- 操作对象（doc_id 等）
  resource_type TEXT,
  result       TEXT NOT NULL,        -- success / failed
  detail       JSONB,                -- 变更前后快照/摘要
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_kb_audit_kb_time ON kb_audit_log(kb_id, created_at DESC);
```

**建议响应：**

```yaml
KBAuditLog:
  id / kb_id / actor_id / actor_name
  action: string           # 服务端枚举，不做用户输入过滤参数的注入面
  resource_id / resource_type
  result: success | failed
  detail: object | null
  created_at: date-time
KBAuditLogListResponse: { items: KBAuditLog[], next_cursor: string | null }
```

**筛选：** `action`（可选）。**错误码：** 200 / 401 / 403 / 404 / 502 / 504（对齐 tenant-plans audit-logs 风格）。

**实现前置：** 写侧埋点是最重工作量——CreateKB/DeleteKB/上传/reparse/重建/配置/权限各写操作需在 repository 事务内追加 audit 插入（可参考 outbox_events 同事务模式）。
