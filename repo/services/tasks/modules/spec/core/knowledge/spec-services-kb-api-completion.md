# SPEC: KB 接口补全（Services 层 8 接口全链路）

> Technical specification derived from:
> - Plan: [kb-api-completion-plan.md](../../../plan/knowledge_base/kb-api-completion-plan.md)（本 SPEC 的需求来源与批次划分依据）
> - UX: N/A — backend-only
> Generated: 2026-09-02 | Target branch: main | 批次 PR：KB-API-B1 / KB-API-B2 / KB-API-B3
>
> Scope: **仅 Services 层** — `repo/api/openapi/services/v1.yaml`、`repo/api/proto/kb/v1/kb_service.proto`、`repo/services/kb-service/`、`repo/services/ani-gateway/`、`repo/architecture/` 两个 baseline、生成物（SDK/docs/schema.d.ts）。**不触碰** Core API `repo/api/openapi/v1.yaml`。

---

## 1. Summary

### 1.1 What This SPEC Covers

补齐 KB 产品面 8 个缺口 REST 接口的「契约 → proto → kb-service → Gateway → 基线 → 生成物」全链路技术规格，按 3 个批次（B1/B2/B3，各一个 PR）交付：

- **B1**：#5 `PUT /knowledge-bases/{kb_id}`（KB 名称/描述更新）+ #10 `GET .../documents/{doc_id}`（文档详情）
- **B2**：#11 chunks 分块明细 + #16 会话列表 + #17 会话消息 + #18 会话删除 + #15 引用溯源
- **B3**：#12 reparse 重新解析（202 + AsyncTask + outbox）

无数据库 DDL 变更（citations 由 `kb_messages.source_chunks` 展开，不建新表）；无新增微服务。

### 1.2 Plan Reference

- Source: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`（2026-09-01，4 项设计决策已确认）
- 覆盖的缺口项（沿用 gap 分析编号）：#5 / #10 / #11 / #12 / #15 / #16 / #17 / #18
- 不在范围：`config`/`rebuild`/`models` 三组路径（另立批次）、`UpdateKBPermissions`（P1 占位维持）、reparse force 参数（YAGNI）

### 1.3 Design Decisions Summary

| 决策点 | 结论 | 依据 |
|---|---|---|
| KB 更新位置 | 新增 `PUT /knowledge-bases/{kb_id}`，仅改 name/description；与 `PUT .../config`（触发重建）语义分离 | Plan §2 |
| 会话删除 | DB 硬删（kb_messages + kb_sessions 单事务）+ Redis DEL best-effort；不存在也返回 204（幂等） | Plan §2 |
| citations 来源 | `kb_messages.source_chunks`（JSONB, role='assistant'）Python 侧展开为行，不建 citation 表 | Plan §2 |
| reparse 下发 | 复用 outbox + async_tasks 事务模式（与 NotifyDocumentUploaded 同构），202 + AsyncTaskRef | Plan §2 |
| **UpdateKB 名称冲突（本 SPEC 新增决策）** | 捕获 PG `UniqueViolationError`（SQLSTATE 23505，`UNIQUE(tenant_id, name)`）→ gRPC `ALREADY_EXISTS` → Gateway `mapGRPCError` 现有映射输出 **409 ALREADY_EXISTS**（kb_grpc_client.go:338–339 已有，零 Gateway 改动） | 2026-09-02 用户确认 |
| 游标设计 | chunks 单列 id ASC；sessions/citations/messages 均为 `created_at\|id` 复合键集游标（messages ASC，sessions/citations DESC）——id 均为随机 UUID，纯 id 游标不成立 | Plan §5.3/§10-4 |

---

## 2. Architecture

### 2.1 System Context

```
Console ──REST──> ani-gateway ──gRPC──> kb-service ──> PostgreSQL (RLS)
                     │                    │    │
                     │                    │    └── Redis (session cache, best-effort)
                     │                    └── outbox_events ──> NATS ani.tasks.kb.parse
                     │                                              └──> parse_consumer
                     │                                                    └──> parse_orchestrator
                     └── 生成物消费方：SDK(sdks/services) / docs/api/services.html / Console schema.d.ts
```

本批次全部改动位于请求链路（Gateway handler → kb-service servicer → repository）与事件链路（reparse outbox → orchestrator，后者零改动）。

### 2.2 Component Design

| 组件 | 职责 | 现状 → 目标 |
|---|---|---|
| `services/v1.yaml` | Services 契约唯一真实来源 | 5 个 operation 缺失/未声明 → 补齐；`KBCitation` +2 字段；新增 5 个 schema |
| `kb_service.proto` | gRPC 契约 | +5 RPC（UpdateKB/ListDocumentChunks/GetSessionMessages/DeleteSession/ReparseDocument）+4 message；`KBCitation` +2 字段 |
| kb-service servicer | gRPC 专用事件循环（sync 薄壳 + `_run_async`） | +5 个 RPC 实现；`ListKBCitations`/`ListKBSessions` 从 `p1_rpcs.py` 占位替换为真实实现 |
| kb-service repositories | 模块级 async 函数 + `set_tenant_context` RLS | +5 个查询/写入函数 |
| `SessionCache` | Redis 会话缓存 | +`delete_session`（best-effort DEL） |
| ani-gateway `KBGRPCClient` | gRPC client 接口+实现+测试 fake | +5 个方法 |
| ani-gateway handlers | REST handler + 路由注册 | +6 个 handler（citations/sessions 已有，仅补 2 字段映射）；6 条新路由 |
| 架构 baseline ×2 | 路由/语义契约豁免登记 | contract baseline +5 条；route baseline 删 reparse 条目（B3） |

### 2.3 Module Interactions

**#5 UpdateKB 请求流**：handler BindJSON（idempotency_key 必填）→ `client.UpdateKB(ctx, instanceTenantID(c), kbID, idemKey, name, desc)` → servicer：校验 → `async_task_repo.find_by_idempotency_key` 幂等重放 → `kb_repo.update_kb`（COALESCE 部分更新）→ 命中 23505 → `ALREADY_EXISTS` → 写 async_tasks 幂等记录 → `_kb_row_to_pb` 返回 → `kbToJSON` 200。

**#12 reparse 事件流**：handler 校验 idempotency_key → servicer（幂等重放 → get_kb/get_document 前置守卫 → 单事务 [reset doc 行 + INSERT async_tasks + INSERT outbox_events]）→ 202 AsyncTaskRef → dispatcher 发布 outbox → NATS `ani.tasks.kb.parse` → parse_consumer → `parse_orchestrator.process_document`（**已内置** reparse 幂等清理：`delete_chunks_by_doc` + best-effort Core 向量删除，parse_orchestrator.py:315–332，本计划零改动）。

**#18 DeleteSession**：get_kb 门禁 → 单事务（DELETE kb_messages → DELETE kb_sessions WHERE id AND kb_id）→ 提交后 best-effort `cache.delete_session`（失败仅 warning）→ Empty → 204。

**#15 citations 激活路径**：Gateway handler 已存在（kb_resources.go:350–375，kb-service 未实现时 `UNIMPLEMENTED`→501 兜底）；B2 kb-service 上线后自动激活，Gateway 仅补 `message_id`/`session_id` JSON 映射。

### 2.4 File Structure

```
repo/
├── api/
│   ├── openapi/services/v1.yaml                      [MODIFY B1: +put op(L2166 path)+UpdateKnowledgeBaseRequest；+get op(L2265 path)
│   │                                                  B2: +3 paths+KBChunk/KBChunkListResponse/KBSessionMessage/KBSessionMessageListResponse/KBSourceChunk；KBCitation(L837)+2 字段]
│   └── proto/kb/v1/kb_service.proto                  [MODIFY B1: +UpdateKB RPC+msg；B2: +3 RPC+4 msg、KBCitation(L251)+field 9/10；B3: +ReparseDocument RPC+msg]
├── services/kb-service/
│   ├── app/generated/kb/v1/                          [REGEN 每批次（buf.gen.yaml），禁手改]
│   ├── app/repositories/knowledge_base.py            [MODIFY B1: +update_kb]
│   ├── app/repositories/chunk.py                     [MODIFY B2: +list_chunks_by_doc_paged]
│   ├── app/repositories/message.py                   [MODIFY B2: +list_sessions/+get_session/+list_session_messages_paged/+delete_session]
│   ├── app/repositories/document.py                  [MODIFY B3: +reset_for_reparse_in_tx]
│   ├── app/session/cache.py                          [MODIFY B2: SessionCache.+delete_session]
│   ├── app/api/grpc_server.py                        [MODIFY B1: +UpdateKB servicer；B2: +4 servicer、改 p1 委托点(L1236–1245)；B3: +ReparseDocument]
│   ├── app/api/p1_rpcs.py                            [MODIFY B2: 删 citations/sessions 占位（UpdateKBPermissions 占位保留）]
│   └── tests/（test_grpc_server.py / test_grpc_wiring.py / 各 repo 测试） [MODIFY 每批次]
├── pkg/generated/pb/kb/v1/                           [REGEN 每批次，与 kb-service 侧生成物同一批次]
├── services/ani-gateway/internal/router/
│   ├── kb_grpc_client.go                             [MODIFY B1: +UpdateKB；B2: +ListDocumentChunks/+GetSessionMessages/+DeleteSession；B3: +ReparseDocument]
│   ├── kb_resources.go                               [MODIFY B1: +2 handler+2 路由；B2: +3 handler+3 路由、kbCitationJSON(L471)/kbCitationToJSON(L545)+2 字段；B3: +1 handler+1 路由]
│   └── kb_resources_test.go                          [MODIFY 每批次（路由注册断言 + fake client handler 单测）]
├── architecture/
│   ├── services-route-baseline.yaml                  [MODIFY B3: 删 reparse spec_not_in_code 条目(L56–61)；不动 config/rebuild/models 条目]
│   └── services-contract-baseline.yaml               [MODIFY B1: +2 条豁免；B2: +3 条豁免]
├── sdks/services + docs/api/services.html            [REGEN（validate-services 内置校验零漂移）]
└── frontends/console/src/api/schema.d.ts             [REGEN（npm run gen-api）]
```

---

## 3. Data Model

### 3.1 Schema Changes

**无 DDL 变更、无新迁移文件。** 全部接口基于既有表（`scripts/apply_kb_migration.py` + `migrations/002_kb_chunks.sql`，2026-09-02 已核验列结构）：

| 表 | 本批次用途 | 关键列（已核验） |
|---|---|---|
| `knowledge_bases` | #5 更新 | `UNIQUE(tenant_id, name)`（名称冲突源）、name、description、updated_at |
| `kb_documents` | #10 详情 / #12 reset | `parse_status CHECK('pending','parsing','indexing','ready','failed')`、`chunk_count INT NOT NULL DEFAULT 0`（reset 必须=0 非 NULL）、error_message/parsed_at 可空 |
| `kb_chunks` | #11 分块明细 | id/doc_id/kb_id/parent_chunk_id/chunk_type CHECK('child','parent','doc_summary')/content/parent_content/page_number/content_type/`file_name TEXT NOT NULL`/token_count/custom_metadata JSONB/created_at |
| `kb_sessions` | #16 列表 / #18 删除 | id、kb_id FK、tenant_id、user_id、title、created_at（id 随机 UUID） |
| `kb_messages` | #15/#17/#18 | id 随机 UUID、session_id FK、role CHECK('user','assistant')、content、`source_chunks JSONB`（仅 assistant 写入，user 为 NULL）、input_tokens/output_tokens/duration_ms、created_at |
| `async_tasks` | #5/#12 幂等 | `UNIQUE(tenant_id, idempotency_key)`、task_type、status、result JSONB |
| `outbox_events` | #12 事件下发 | event_type='kb.reparse'、payload JSONB |

### 3.2 REST/Proto 实体与 DB 列映射

**KBChunk**（v1.yaml 新增 schema ↔ `kb_chunks` 列）：

| REST 字段 | DB 列 | 类型说明 |
|---|---|---|
| id / doc_id / kb_id / parent_chunk_id | 同名列 | uuid；parent_chunk_id nullable（proto 空串 + REST nullable） |
| chunk_type | chunk_type | enum child/parent/doc_summary（对齐 DB CHECK） |
| content / parent_content | 同名列 | string；parent_content nullable |
| page_number | page_number | int nullable；DB NULL → proto 0 → REST `nullable: true`（§5.4 零值注意） |
| content_type / token_count | 同名列 | DB 可空 → proto 空串/0；非 required，序列化可省略 |
| file_name | file_name | NOT NULL 冗余列 |
| custom_metadata | custom_metadata | JSONB → proto string → Gateway `json.Unmarshal` 输出 object |
| created_at | created_at | date-time |

**KBSessionMessage**（↔ `kb_messages` 列）：`source_chunks`（DB 列名）→ REST `sources`（数组，Gateway Unmarshal 后输出）；user 消息 sources 输出 null/缺省；input_tokens/output_tokens/duration_ms nullable。

**KBCitation**（既有 schema 增强字段）：`message_id`/`session_id` 来自展开所在的 kb_messages 行，**非 DB 列**。

### 3.3 Relationships

- citation 行 = (kb_messages.source_chunks 单条 source) 的投影：`KBCitation.id = uuid.uuid5(NAMESPACE_URL, f"ani:kb:citation:{kb_id}:{message_id}:{doc_id}")`（确定性、分页重放稳定、符合契约 `format: uuid`）；同 (message, doc) 多 chunk 取 score 最高一条（citation 粒度 = message × doc）。
- session 归属：`kb_sessions.kb_id` 必须等于 path 参数 kb_id（#17/#18 前置校验，防跨 KB 枚举 session_id）；RLS 保证租户隔离。

### 3.4 Migration Plan

无 DDL。回滚 = revert PR（路由/契约/生成物随 PR 同批回退；B3 回滚须恢复 route baseline 条目）。reparse 的 `reset_for_reparse_in_tx` 是 DML，无 schema 依赖。

---

## 4. OpenAPI Change Plan（Services 契约）

### 4.1 Frozen Facts Table（基于 2026-09-02 代码核验）

**Frozen（已存在，逐项核验）**：

| 事实 | 位置 |
|---|---|
| `/knowledge-bases/{kb_id}` path 已有 GET/DELETE | v1.yaml:2166（get L2168 / delete L2181） |
| `.../documents/{doc_id}` path 已有 DELETE | v1.yaml:2265（仅 delete） |
| `reparseKnowledgeBaseDocument` 契约已声明（202+AsyncTask+409） | v1.yaml:2379–2385 |
| `listKnowledgeBaseCitations`/`listKnowledgeBaseSessions` 契约已声明 | v1.yaml:2318 / 2337 |
| `KBCitation`(8 字段)/`KBCitationListResponse`/`KBSession`/`KBSessionListResponse` | v1.yaml:837–873 |
| proto `GetDocument` RPC 已声明 | kb_service.proto:27 |
| proto `ListKBCitations`/`ListKBSessions` RPC 已声明 | kb_service.proto:48 / 50 |
| Gateway `GetDocument` client 方法已实现 | kb_grpc_client.go:161–165 |
| Gateway citations/sessions handler 已实现（501 兜底） | kb_resources.go:350–375 / 377–400 |
| KB 域 18 个 op 的 `operation_security` 豁免已登记 | services-contract-baseline.yaml:5–56 |
| reparse 路由 `spec_not_in_code` 条目 | services-route-baseline.yaml:56–61 |
| `CursorPageRequest`（common.v1，limit 默认 20 max 100） | common/v1/common.proto:18–22 |

**Non-Frozen（待补——由本 SPEC 各批次新增，当前不存在）**：

| operationId / schema | 当前状态 |
|---|---|
| `updateKnowledgeBase` / `getKnowledgeBaseDocument` / `listKnowledgeBaseDocumentChunks` / `listKnowledgeBaseSessionMessages` / `deleteKnowledgeBaseSession` | v1.yaml **不存在**（B1/B2 新增） |
| `KBSourceChunk` 命名 schema | 不存在（`KBQueryResponse.sources` 为内联对象 v1.yaml:426–435）；proto 侧 `SourceChunk` 已存在（L156–162） |

**Known Risky Assumptions**（实现前须复核）：
1. chunks `id ASC` 游标语义是「id 字典序」而非「文档位置序」——依赖 `write_chunks` 单事务批量插入（同一事务 `now()` → created_at 全同）这一前提（§11.3-A3）。
2. outbox dispatcher 不按 event_type 过滤、任意事件发布至 `ani.tasks.kb.parse`（Plan §6.3 已核实，B3 实现时再复核）。
3. `KBCitation` 增强字段建立在「该接口从未发布、无兼容性负担」之上。

### 4.2 OpenAPI Change Plan

| Change | operationId | Compatibility | idempotency_key |
|---|---|---|---|
| +put（既有 path） | updateKnowledgeBase | 纯新增，无破坏 | **required**（body，uuid） |
| +get（既有 path） | getKnowledgeBaseDocument | 纯新增 | — |
| +path GET | listKnowledgeBaseDocumentChunks | 纯新增 | — |
| +path GET | listKnowledgeBaseSessionMessages | 纯新增 | — |
| +path DELETE | deleteKnowledgeBaseSession | 纯新增（幂等 204） | — |
| 契约无改动（仅实现） | reparseKnowledgeBaseDocument | 已声明 | required（已有 schema） |
| schema 增强字段 | listKnowledgeBaseCitations | KBCitation +message_id/+session_id（**未发布，无兼容负担**） | — |
| 契约无改动（仅实现） | listKnowledgeBaseSessions | 已声明 | — |

**同批次硬约束**（违反即门禁阻断）：
- 新增 v1.yaml path 必须与 Gateway 路由注册**同一 PR** 落地（`validate-services-route-contract` 未登记 diff 阻断 PR）。
- 新增 operationId 必须同 PR 登记 `services-contract-baseline.yaml` 的 `operation_security` 豁免（KB 域 18 个 op 均无 security 声明，新 op 不登记 → `make validate-services` 失败；未来若补 security 须同步删条目，stale 同样阻断）。
- Gateway 路由仅注册到 `/api/v1/svc` 组（spec-split 门禁禁止 Core 组），且必须 `svc.METHOD("...")` 直调形式（路由校验器正则限定）。

### 4.3 Endpoints（8 接口完整规格）

| # | Method | Path | Auth | Request | Success | 批次 |
|---|---|---|---|---|---|---|
| 5 | PUT | `/knowledge-bases/{kb_id}` | Bearer（网关） | `UpdateKnowledgeBaseRequest` | 200 `KnowledgeBase` | B1 |
| 10 | GET | `/knowledge-bases/{kb_id}/documents/{doc_id}` | 同上 | — | 200 `KBDocument` | B1 |
| 11 | GET | `.../documents/{doc_id}/chunks` | 同上 | query: limit(default 50, 1–100)/cursor/chunk_type(`child\|parent\|doc_summary`) | 200 `KBChunkListResponse` | B2 |
| 16 | GET | `.../sessions` | 同上 | query: limit(default 20)/cursor | 200 `KBSessionListResponse` | B2 |
| 17 | GET | `.../sessions/{session_id}/messages` | 同上 | query: limit(default 100, max 100)/cursor | 200 `KBSessionMessageListResponse` | B2 |
| 18 | DELETE | `.../sessions/{session_id}` | 同上 | — | 204（幂等） | B2 |
| 15 | GET | `.../citations` | 同上 | query: limit(default 20)/cursor | 200 `KBCitationListResponse` | B2 |
| 12 | POST | `.../documents/{doc_id}/reparse` | 同上 | body: idempotency_key(uuid, required) | 202 `AsyncTask` | B3 |

**#5 请求 schema（v1.yaml 新增）**：

```yaml
UpdateKnowledgeBaseRequest:
  type: object
  required: [idempotency_key]
  description: 更新知识库基础信息（名称/描述）；空字段表示不修改。
  properties:
    idempotency_key: { type: string, format: uuid, description: "客户端生成UUID，防重复提交" }
    name:            { type: string, description: "新名称；空表示不修改" }
    description:     { type: string, nullable: true, description: "新描述；空表示不修改" }
```

**#11/#17 新 schema**：`KBChunk`/`KBChunkListResponse`/`KBSessionMessage`/`KBSessionMessageListResponse`/`KBSourceChunk` 字段清单见 Plan §5.1（已核验与 `kb_chunks`/`kb_messages` 真实列一一对应，映射见本 SPEC §3.2）。`KBSourceChunk` 为新增命名 schema（doc_id/file_name/page/content/score），对齐 proto `SourceChunk`（L156–162）与 `KBQueryResponse.sources` 内联对象（L426–435）的字段集；**不回溯改造** `KBQueryResponse`（范围纪律）。

**#15 契约增强**：`KBCitation`（v1.yaml:837–848）追加：

```yaml
    message_id: { type: string, format: uuid, nullable: true, description: "引用该文档的回答消息 id（定位/跳转用）" }
    session_id: { type: string, format: uuid, nullable: true, description: "回答所属会话 id" }
```

（同步：proto `KBCitation` +field 9/10；Gateway `kbCitationJSON`/`kbCitationToJSON` 补 2 字段映射，omitempty 空串不出。）

### 4.4 Error Responses

统一错误体 `{"code","message","request_id"}`（`writeKBError`）。完整分类见 §6.1。

### 4.5 Breaking Changes

**无。** 全部为纯新增 operation/path/schema 字段；`KBCitation` 增强字段作用于从未发布（一直 501）的接口，无兼容性负担。

---

## 5. Business Logic

### 5.1 Core Algorithms

**#5 UpdateKB（servicer，与 `_create_kb` 同模式 grpc_server.py:152）**

```
1. 校验 idempotency_key/kb_id 非空 → INVALID_ARGUMENT
2. async_task_repo.find_by_idempotency_key → 命中则重放 result 行返回（幂等）
3. kb_repo.update_kb:
     UPDATE knowledge_bases
     SET name = COALESCE(NULLIF($name, ''), name),
         description = COALESCE(NULLIF($desc, ''), description),
         updated_at = now()
     WHERE id = $kb_id          -- RLS: 事务内 set_tenant_context
     RETURNING 全列
   - 返回 None → NOT_FOUND
   - asyncpg UniqueViolationError(SQLSTATE 23505) → ALREADY_EXISTS（名称与同租户既有 KB 重复）
4. 写 async_tasks 幂等记录（result=更新后行，与 CreateKB 同模式）
5. _kb_row_to_pb 返回
```

**游标分页（四种列表，均键集分页禁 OFFSET）**

| 接口 | 排序 | 游标格式 | 下一页条件 |
|---|---|---|---|
| #11 chunks | `ORDER BY id ASC` | 单列 chunk id | `id > $cursor` |
| #16 sessions | `ORDER BY created_at DESC, id DESC` | `{created_at_iso}\|{session_id}` | `(created_at, id) < ($ts, $id)` |
| #15 citations | 同上（message 粒度分页） | `{created_at_iso}\|{message_id}` | 同上 |
| #17 messages | `ORDER BY created_at ASC, id ASC` | `{created_at_iso}\|{message_id}` | `(created_at, id) > ($ts, $id)`（tie-break 必须：id 为 gen_random_uuid 随机值，user/assistant 分属不同事务） |

**#15 citations 展开（servicer，替换 p1_rpcs 占位）**

```
分页查询 assistant 消息（source_chunks IS NOT NULL AND <> 'null'）:
  SELECT m.id, m.session_id, m.created_at, m.source_chunks
  FROM kb_messages m JOIN kb_sessions s ON s.id = m.session_id
  WHERE s.kb_id = $1 AND m.role = 'assistant'
    [AND (m.created_at, m.id) < ($ts, $id)]
  ORDER BY m.created_at DESC, m.id DESC LIMIT $limit
对每条消息: json.loads(source_chunks) → 按 doc_id 分组取 score 最高 → 每组生成一个 KBCitation:
  id = uuid.uuid5(NAMESPACE_URL, f"ani:kb:citation:{kb_id}:{message_id}:{doc_id}")
  message_id/session_id/file_name/page/content/score ← source 条目；created_at ← 消息时间
next_cursor = 本页最后一条消息的复合游标（分页以 message 为单位）
```

**#16 sessions 聚合 SQL**

```sql
SELECT s.id, s.kb_id, s.created_at,
       COUNT(m.id) AS message_count,
       MAX(m.created_at) AS last_active_at,
       (SELECT m2.content FROM kb_messages m2
         WHERE m2.session_id = s.id AND m2.role = 'user'
         ORDER BY m2.created_at ASC LIMIT 1) AS last_query
FROM kb_sessions s
LEFT JOIN kb_messages m ON m.session_id = s.id
WHERE s.kb_id = $1
  [AND (s.created_at, s.id) < ($cursor_ts, $cursor_id)]   -- 键集分页，DESC
GROUP BY s.id
ORDER BY s.created_at DESC, s.id DESC
LIMIT $limit
```

**#12 reparse（servicer，与 `_notify_document_uploaded` 同构 grpc_server.py:451–558）**

```
1. 校验 idempotency_key 非空 → INVALID_ARGUMENT
2. 幂等键 = 客户端 request.idempotency_key（不照抄 notify 的合成键——reparse proto 有该字段）
   find_by_idempotency_key 命中且 status ∈ {pending, completed} → 重放同一 AsyncTaskRef
   （failed 任务须换新键重新发起——重放条件与 notify 同构）
3. get_kb: 不存在 → NOT_FOUND；status='rebuilding' → FAILED_PRECONDITION
4. get_document: 不存在/软删 → NOT_FOUND；parse_status='ready' → FAILED_PRECONDITION（防误触发覆盖，无 force 字段）
   doc_row 提供 payload 所需 storage_path/file_name（reparse 无 request 来源，取自 DB；notify 的 file_name 恒空串，此处取真实值）
5. 单事务（conn.transaction()，RLS 事务内 set）:
   a. UPDATE kb_documents SET parse_status='pending', error_message=NULL,
      chunk_count=0, parsed_at=NULL        -- chunk_count 置 0 非 NULL（列 NOT NULL DEFAULT 0，置 NULL 违反约束事务必失败）
      （新增 reset_for_reparse_in_tx——既有 update_parse_status_in_tx 的 COALESCE 语义无法重置为 0/清 NULL）
   b. INSERT async_tasks (task_type='kb.reparse', 幂等键=客户端 idempotency_key)
   c. INSERT outbox_events (event_type='kb.reparse'; payload 与 notify 模板同构:
      doc_id/kb_id/storage_path/tenant_id/file_name/object_id/chunk_size)
6. 返回 AsyncTaskRef
```

**下游清理零改动**：`parse_orchestrator.process_document` 已内置 `delete_chunks_by_doc`（parse_orchestrator.py:315–321）+ best-effort `core.delete_vector_store_documents(filter='doc_id == "..."')`（:322–332），重解析覆盖旧 chunks/向量，与 DeleteDocument 同模式。

**#18 DeleteSession（servicer）**

```
1. get_kb 门禁 → 不存在 NOT_FOUND
2. 单事务: DELETE FROM kb_messages WHERE session_id = $3
          DELETE FROM kb_sessions WHERE id = $3 AND kb_id = $2   -- kb_id 条件做归属校验
   （kb_messages.session_id 有 ON DELETE CASCADE，显式删 messages 为与既有 *_in_tx 模式对齐、便于测试断言）
3. session 行不存在 → 仍返回 Empty（幂等 204；KB 不存在才 NOT_FOUND）
4. 事务提交后 best-effort cache.delete_session（Redis DEL ani:prod:session:kb:{session_id}，失败仅 logger.warning，24h TTL 自然过期）
```

### 5.2 Validation Rules

| 规则 | 违反结果 |
|---|---|
| PUT/POST 副作用必须带 `idempotency_key`（uuid） | handler BindJSON / servicer 双重校验 → 400 INVALID_ARGUMENT |
| `limit` ∈ [1,100]（chunks default 50；messages default 100） | servicer 校验 → 400 |
| `chunk_type` ∈ {child, parent, doc_summary} | 400 |
| tenant 一律取 Auth 中间件 context（`instanceTenantID(c)`），忽略 body tenant_id | — |
| `UpdateKBRequest` 空 name/description = 不修改（COALESCE+NULLIF 语义） | — |
| session 必须属于 path 的 kb_id（#17/#18） | 归属不符 → 404（不泄露存在性） |

### 5.3 State Machine（#12 文档解析状态）

```
pending → parsing → indexing → ready
                ↘ failed
reparse 允许入口: failed（错误恢复主场景），以及 pending/parsing/indexing 之外的中间态（守卫仅拒绝 ready）
reparse 拒绝:     ready（FAILED_PRECONDITION → 409）
reparse 效果:     parse_status → pending, error_message/parsed_at → NULL, chunk_count → 0
KB 级守卫:        knowledge_bases.status = 'rebuilding' → 409（与 PUT config 触发的重建互斥）
```

（无新状态引入；DB CHECK 约束保持不变。）

### 5.4 Edge Cases

| 边界场景 | 处理 |
|---|---|
| UpdateKB 名称撞同租户既有 KB | 23505 → ALREADY_EXISTS → 409（§1.3 决策） |
| UpdateKB 空 name/空 description | COALESCE+NULLIF：保持原值（「空 = 不修改」契约语义） |
| UpdateKB 幂等重放（同 idempotency_key 重发） | 重放首次结果行（含撞名 409 场景——错误不落 async_tasks，重放不会回放 409） |
| 跨租户访问 kb_id/doc_id/session_id | RLS 行不可见 → NOT_FOUND（租户不可区分） |
| session 属于其他 KB（同租户） | `get_session` 归属校验 / DELETE WHERE kb_id 条件 → NOT_FOUND / 204 前置 404 语义（§6.1） |
| #18 重复删除 | 幂等 204 |
| #18 Redis DEL 失败 | best-effort，不阻断（24h TTL 过期） |
| #15 消息 source_chunks 为 `null` 字符串/空数组/非法 JSON | 过滤条件排除 `null`；空数组/非法 JSON 跳过该消息（不炸整页） |
| #17 user 消息的 source_chunks | NULL → REST sources 输出 null/缺省（不虚构空数组） |
| #11 文档无 chunks（pending/failed） | 空列表 200（非 404——文档存在即 200） |
| #11/#10 文档软删 | 软删过滤（get_document 同语义）→ 404 |
| proto int32 零值 vs REST nullable（page_number 等） | DB NULL → proto 0 → JSON 端可省略/输出 null；以现有 KBDocument 序列化惯例为准，`omitempty` 或指针二选一（不强制，B2 实现时对齐） |
| reparse 重复提交（同幂等键，任务 pending/completed） | 重放同一 AsyncTaskRef |
| reparse 任务已 failed 后同键重试 | 不重放（须换新键——契约 required uuid 由客户端生成，天然支持） |
| reparse 后 orchestrator 尚未消费（pending 窗口） | chunks 列表查询旧数据（parse_status=pending 提示）；无一致性破坏 |
| citations 分页期间新消息写入 | 键集游标（created_at DESC）天然免疫新增行注入 |
| chunks 页大小 > 100 | servicer 400 |

---

## 6. Error Handling

### 6.1 Error Taxonomy

Gateway 统一错误体 `{"code","message","request_id"}`；`mapGRPCError`（kb_grpc_client.go:314–347）已有映射：NOT_FOUND→404 / INVALID_ARGUMENT→400 / FAILED_PRECONDITION→409 / ALREADY_EXISTS→409 / UNAVAILABLE→503 / UNIMPLEMENTED→501 / PERMISSION_DENIED→403 / UNAUTHENTICATED→401 / DEADLINE_EXCEEDED→504 / default→500。

| HTTP | code | 触发条件 | 涉及接口 |
|---|---|---|---|
| 400 | BAD_REQUEST | idempotency_key 缺失/非 uuid；limit 越界；chunk_type 非法 | #5/#12 |
| 401 | UNAUTHORIZED | 网关认证失败 | 全部 |
| 403 | FORBIDDEN | 租户/权限拒绝 | 全部 |
| 404 | NOT_FOUND | KB/doc/session 不存在、跨租户（RLS 不可见）、session 归属不符、软删文档 | #5/#10/#11/#12/#17/#18（#18 仅 KB 不存在时） |
| 409 | ALREADY_EXISTS | UpdateKB 名称与同租户既有 KB 冲突（**本 SPEC 新增**） | #5 |
| 409 | CONFLICT | reparse：doc=ready（防覆盖）；KB=rebuilding；notify 幂等冲突场景沿用 | #12 |
| 500 | INTERNAL | 未预期异常（含未捕获 asyncpg 错误） | 全部 |
| 501 | NOT_IMPLEMENTED | kb-service 未实现 P1 RPC 时（B2 上线前 citations/sessions；UpdateKBPermissions 维持） | #15/#16（仅 B2 前） |
| 503 | UNAVAILABLE | kb-service 不可达 / gRPC client 未配置（handler nil 守卫） | 全部 |
| 504 | DEADLINE_EXCEEDED | gRPC callCtx（默认 5s）超时 | 全部 |

### 6.2 Retry Strategy

- **幂等接口**（#5/#12）：客户端可安全重试（同 idempotency_key 重放同一结果/同一 task_id）；failed 任务须换新键。
- **读接口**（#10/#11/#15/#16/#17）：无副作用，客户端可自由重试。
- **#18 DELETE**：幂等（204 重复），可安全重试。
- 消费侧（reparse）：outbox + async_tasks 重试/死信既有机制，本 SPEC 不改。
- gRPC client `callCtx` 5s 超时；Gateway 不做自动重试（保持现有 client 惯例）。

### 6.3 Failure Modes

| 依赖失败 | 行为 |
|---|---|
| PostgreSQL 不可达 | `_run_async` 异常 → INTERNAL(500)；pool 未就绪 → FAILED_PRECONDITION（GetKB 现有模式 grpc_server.py:281） |
| Redis 不可达 | 仅 #18 缓存删除受影响 → best-effort warning，DB 事务已提交（24h TTL 自然过期，与 append_message 降级一致） |
| NATS/dispatcher 延迟 | outbox 未发布事件保留（`published=false`），dispatcher 重试——reparse 任务停留在 pending，不影响 API 响应（202 已返回） |
| orchestrator 处理失败 | async_tasks → failed + error_message；文档 parse_status → failed；用户可再发 reparse（新键） |
| 生成物漂移 | `make validate-services` 零漂移门禁阻断 PR |

---

## 7. Security

### 7.1 Authentication & Authorization

- 认证：网关 Bearer（Services 现状——OpenAPI 未声明 security，靠网关中间件；契约基线 18+5 op 均登记 `operation_security` 豁免，未来补 security 声明须同步删条目）。
- 授权：**tenant 一律取 Auth 中间件 context**（`instanceTenantID(c)`），Gateway 注入 proto request，servicer/repository 不得信任 body tenant_id。
- 租户隔离：PostgreSQL RLS（`set_tenant_context`，RESTRICTIVE policy `tenant_id = current_setting('app.current_tenant_id')`），kb-service app role 非超级用户、非 bypassrls——**所有新查询必须在 RLS 事务内执行**。
- 越权防护：跨租户不可见（404）；session 跨 KB 枚举防护（归属校验，404 不泄露存在性）。

### 7.2 Input Validation

- idempotency_key：uuid 格式，handler BindJSON + servicer 双重校验。
- limit/cursor/chunk_type：白名单校验；cursor 格式非法 → 400 或按首页处理（与现有 list_kbs 惯例一致）。
- JSONB（source_chunks/custom_metadata）：展开失败跳过该条（#15）或 500（#17 序列化异常不应发生——DB 写入侧已约束）；不回传原始字符串给客户端。
- SQL：全参数化（asyncpg `$n`），无字符串拼接。

### 7.3 Data Protection

- 无新增敏感字段；custom_metadata/source_chunks 为业务数据，随租户 RLS 隔离。
- 错误体不含内部栈；`request_id` 便于审计关联。
- 删除（#18）为硬删，满足数据清除语义；Redis 副本 24h TTL 过期。

---

## 8. Performance

### 8.1 Expected Load

Console 单租户管理操作 + 问答回放读路径；非高频热点。分块明细（#11）可能单文档数百 chunk——分页（≤100/页）控制响应体。

### 8.2 Optimization Strategy

- 全部列表键集分页（禁 OFFSET）。
- #16 聚合单 SQL（GROUP BY + 关联子查询），避免 N+1。
- #15 以 message 为单位分页（不展开全量再分页）。
- 既有索引覆盖：`idx_kb_chunks_kb_doc(kb_id, doc_id)`、`idx_kb_messages_session(session_id, created_at)`、`idx_kb_docs_kb_id`、`idx_kb_messages_tenant(tenant_id, created_at)`。

### 8.3 Database Considerations

- #16 `GROUP BY s.id` + `(created_at, id) < (...)` 键集条件：现有 `idx_kb_messages_session` 支持 join 聚合；sessions 侧若有慢查询风险，可评估 `(kb_id, created_at DESC)` 复合索引——**当前量级不建**（YAGNI，§11.1 留观）。
- #17 复合排序 `ORDER BY created_at ASC, id ASC`：`idx_kb_messages_session(session_id, created_at)` 可用（id 仅 tie-break）。
- #15 join + 复合游标：`idx_kb_messages_tenant` / session_id 索引可用；RLS 叠加 tenant 过滤。
- N+1 防护：citations/sessions/messages 全单 SQL + Python 展开，无逐行查询。

---

## 9. Testing Strategy

### 9.1 Unit Tests（kb-service pytest——**不在任何 make 门禁内，须显式运行**）

**B1**：
- UpdateKB：成功（name+desc 同时改）/ NOT_FOUND / 跨租户 RLS 隔离 / 幂等重放 / 空字段不改（name 或 description 为空保持原值）/ **名称冲突 23505 → ALREADY_EXISTS**
- GetDocument 已有实现（#10 无 kb-service 测试新增，仅 Gateway 侧）

**B2**：
- chunks：分页 / chunk_type 过滤 / 文档不存在 404 / 软删文档 404 / 跨租户隔离
- sessions 列表：message_count / last_query / last_active_at 聚合正确、复合游标翻页
- session messages：user+assistant 完整回放顺序（created_at 同秒时 id tie-break）、sources/token 字段（user 消息 sources null）、跨 KB session 越权 404
- session delete：删除后 GetSessionMessages 404、消息行数清零、Redis DEL 被调用（mock SessionCache）、重复删除幂等
- citations：空 sources 返回空列表 / 多消息展开 / 跳过无 source 消息（含 'null' 字符串）/ 分页 / **uuid5 确定性**（同 (message, doc) 重放幂等且符合 format: uuid）/ message_id/session_id 透传 / 同 message×doc 多 chunk 取 score 最高
- **更新 P1 占位锁定测试**：`test_grpc_wiring.py:191–209`、`test_grpc_server.py:209–230` UNIMPLEMENTED 断言改为真实行为

**B3**：
- reparse：parse_status 回 pending / chunk_count 重置 0 / error_message+parsed_at 清 NULL / outbox_events 落一行（event_type='kb.reparse'）/ async_tasks 幂等记录
- 守卫：ready 拒绝 409 / KB rebuilding 拒绝 / 文档不存在 404 / 同键重放同 task_id / failed 任务换新键可重新发起
- 回归：现有 orchestrator 清理链路不被 reparse 破坏

### 9.2 Integration Tests（Gateway go test）

- 每批次路由注册断言（kb_resources_test.go 模式）：B1 两条、B2 三条、B3 一条
- fake client handler 单测：成功路径 + nil-client 503 守卫 + 错误映射（404/409/400/501）
- B2 序列化专项：custom_metadata / sources object 化（proto string → json.Unmarshal → object）
- B3：202/409/404 映射

### 9.3 Edge Case Tests

§5.4 表逐项覆盖（重点：名称冲突 409、幂等重放、跨租户/跨 KB 404、user 消息 sources null、uuid5 稳定性、非法 cursor）。

### 9.4 Acceptance Criteria Mapping

| 接口 | 验收项 | 测试类型 | 位置 |
|---|---|---|---|
| #5 | 200 更新生效 / 空字段不改 / 404 / 409 名称冲突 / 幂等重放 | pytest + go test | B1 §9.1 |
| #10 | 200 KBDocument / 404 / nil-client 503 | go test | B1 §9.2 |
| #11 | 分页 + chunk_type / 404（含软删）/ RLS 隔离 | pytest + go test | B2 |
| #15 | 展开/过滤/uuid5 稳定/分页/新字段 | pytest + go test | B2 |
| #16 | 聚合正确/翻页 | pytest | B2 |
| #17 | 回放顺序/sources 映射/越权 404 | pytest + go test | B2 |
| #18 | 204 幂等/行清零/Redis DEL/404（仅 KB 不存在） | pytest + go test | B2 |
| #12 | 202/守卫 409/状态重置/outbox 落库/幂等 | pytest + go test | B3 |
| 门禁 | `validate-services` 无新 diff / 生成物零漂移 | make | 每批次 |

**每批次验收命令**（kb-service pytest 是 make 盲区，必须显式）：

```bash
cd repo
make validate-services
make test
cd services/kb-service && python -m pytest
cd ../.. && make validate-architecture
git diff --check
# B3 额外：基线清理后重跑 validate-services-route-contract 确认无 stale 报错
```

---

## 10. Implementation Plan

### 10.1 Phases（批次 = PR，顺序固定：先契约后实现）

**B1（KB-API-B1）**：v1.yaml（+put/+get/+schema）→ proto（+UpdateKB）→ 生成物两侧 → kb-service（update_kb + UpdateKB servicer）→ Gateway（client + 2 handler + 2 路由）→ contract baseline +2 条 → 测试 → 验收命令。

**B2（KB-API-B2）**：v1.yaml（+3 paths +5 schema、KBCitation+2 字段）→ proto（+3 RPC +4 message、KBCitation+field 9/10）→ 生成物 → kb-service（3 repo + cache.delete_session + 4 servicer + 替换 p1 占位 + 改测试锁定）→ Gateway（3 client + 3 handler + 3 路由 + kbCitationJSON 补 2 字段）→ contract baseline +3 条 → 测试 → 验收命令。

**B3（KB-API-B3）**：proto（+ReparseDocument）→ 生成物 → kb-service（reset_for_reparse_in_tx + ReparseDocument servicer）→ Gateway（client + handler + 路由）→ **route baseline 删 reparse 条目（L56–61，勿动其他）** → 测试 → 验收命令 + stale 复核。

依赖：B2 的 citations/sessions handler 激活依赖 kb-service 实现（同批次）；B1/B2/B3 相互独立可并行开发，但建议串行合入（每批次一个 PR，main 受保护串行收口）。

### 10.2 Issue Mapping（建议 /to-issues 拆分）

实际拆分见 `repo/services/tasks/modules/issue/core/knowledge/issue-040 ~ issue-048`（9 个，先契约层后功能层；接口层并入契约——proto 即两服务间接口契约）：

| Issue | 类型 | 内容 | SPEC 章节 | 依赖 |
|---|---|---|---|---|
| #040 | 契约 | B1 契约+proto+生成物 | §4.2/§4.3 | — |
| #041 | 契约 | B2 契约+proto+生成物 | §4.2/§4.3 | — |
| #042 | 契约 | B3 proto+生成物 | §2.4/§4.2 | — |
| #043 | 功能 | B1 kb-service（update_kb + UpdateKB） | §5.1/§3.2 | #040 |
| #044 | 功能 | B1 Gateway + baseline | §4.2/§6.1 | #040，与 #043 同 PR |
| #045 | 功能 | B2 kb-service（3 repo + 4 servicer） | §5.1 | #041 |
| #046 | 功能 | B2 Gateway + baseline | §4.2 | #041，与 #045 同 PR |
| #047 | 功能 | B3 kb-service reparse servicer | §5.1/§5.3 | #042 |
| #048 | 功能 | B3 Gateway + route baseline 清理 | §10.1 | #042，与 #047 同 PR |

### 10.3 Incremental Delivery

- 无 feature flag：全部纯新增接口，风险天然隔离（既有接口零改动）。
- #15/#16 灰度路径特殊：Gateway handler 已上线（501 兜底），kb-service 实现合入即自动激活——B2 合入前线上可见 501，合入后立即可用。
- 每批次合入后跑全量验收命令；progress 文档闭环 4 件套（development-records/{批次ID}.md 新建、README 索引、CURRENT-SPRINT.md、ANI-06-开发计划.md，CLAUDE.md §6-3）。

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

1. #16 sessions 列表在 session 数量大时是否需要 `(kb_id, created_at DESC)` 复合索引——当前量级不建（YAGNI），上线后观察慢查询再定。
2. proto int32 零值 vs REST nullable 的序列化策略（`omitempty` vs 指针）：B2 实现时对齐现有 KBDocument 惯例，不强制统一。
3. chunks「文档位置序」（parent→child 顺序）需求：若未来出现，需在解析写入侧加序号列——不在本计划范围。

### 11.2 Technical Risks

| 风险 | 影响 | 缓解 |
|---|---|---|
| proto 生成物两侧漂移（kb-service 与 pkg/generated 不同批） | Gateway 编译失败 / 运行时不一致 | 每批次 buf.gen.yaml 重新生成两侧，禁手改，`make validate-services` 校验 |
| 新 op 漏登记 contract baseline | `make validate-services` 阻断 | §4.2 硬约束写死检查单；B1 两项/B2 三项 |
| B3 漏删 route baseline reparse 条目 / 误删 config 等条目 | stale 阻断 / 破坏其他域基线 | §10.1 明确仅删 L56–61；删后重跑 validate-services-route-contract |
| kb-service pytest 不在 make 门禁内 | 测试更新遗漏不被 CI 拦截 | 验收命令显式 `python -m pytest`（§9.4） |
| chunks id 字典序 ≠ 文档位置序 | 前端如按位置展示会乱序 | 契约不承诺位置序；§11.3-A3 声明；需求出现时加序号列 |
| citations 非法 JSON source_chunks | 展开异常 | 跳过该消息（§5.4）；写入侧 Query 流程已约束结构 |
| UpdateKB 23505 未捕获 | 500 内部错误暴露 | B1 测试显式覆盖名称冲突用例 |
| custom_metadata/sources 序列化漂移（proto string vs 契约 object） | SDK 类型不符 | Gateway 统一 json.Unmarshal 后输出（对齐契约，issue-018 既有瑕疵不在本计划扩散） |

### 11.3 Assumptions

- **A1** `KBCitation`/`KBSessionListResponse` 对应接口从未发布（一直 501），增强字段无兼容负担（已核验 Gateway 501 兜底现状）。
- **A2** outbox dispatcher 不按 event_type 过滤，`kb.reparse` 事件会被 `ani.tasks.kb.parse` 消费（Plan §6.3 核实；B3 实现时复核）。
- **A3** `write_chunks` 单事务批量插入 → 同文档所有 chunk 的 created_at 相同（PG `now()` 事务时间），故 id ASC 游标在当前写入模式下稳定。
- **A4** `parse_orchestrator.process_document` 的清理逻辑（delete_chunks_by_doc + Core 向量删除）对 reparse 重入安全（已核实与 DeleteDocument 同模式）。
- **A5** KB 名称更新不触发向量/索引重建（纯元数据），无需与 rebuilding 状态互斥——若产品后续要求改名触发重建，另立批次（config 路径）。

---

## 12. 主维护源与文档链

- 本 SPEC：`repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`
- 上游计划：`repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
- 契约权威：`repo/api/openapi/services/v1.yaml` + `repo/api/proto/kb/v1/kb_service.proto` + `repo/architecture/` 两个 baseline
- Core 实现指南：`repo/services/tasks/execution/CORE-HANDLER-IMPLEMENTATION-GUIDE.md`

文档链：Plan → **SPEC（本文档）** → `/to-issues`（拆分 Issue）→ `/goal` 或 `/loop-it`（实现）→ `/review-it` → `/note-it` → `/ship-it`。
