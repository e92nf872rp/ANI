# KB 接口补全实施计划（plan.md）

> 生成日期：2026-09-01
> 范围：ANI Services 层 `knowledge-bases` 8 个 REST 接口的「契约 → proto → kb-service → Gateway」全链路落地。
> 依据：2026-09-01 实施方案（4 项设计决策已确认）；接口编号沿用 gap 分析表（#5/#10/#11/#12/#15/#16/#17/#18）。
> 强制规则来源：CLAUDE.md（分层边界 / API 工程约定 / 开发验证闭环 / Karpathy 五条原则）。

***

## 1. 目标与范围

补齐 KB 产品面 8 个缺口接口，全部落在 Services 层（不触碰 Core API `repo/api/openapi/v1.yaml`）：

| #  | 域   | Method | Path                                 | operationId                      | 优先级 | 现状摘要                                                                              |
| -- | --- | ------ | ------------------------------------ | -------------------------------- | --- | --------------------------------------------------------------------------------- |
| 5  | 主对象 | PUT    | `/knowledge-bases/{kb_id}`           | updateKnowledgeBase              | 🔴  | 契约/proto/实现全缺                                                                     |
| 10 | 文档  | GET    | `.../documents/{doc_id}`             | getKnowledgeBaseDocument         | 🔴  | path 已有 DELETE；gRPC `GetDocument` 与 Gateway client 已就绪，只差契约 GET + Gateway handler |
| 11 | 文档  | GET    | `.../documents/{doc_id}/chunks`      | listKnowledgeBaseDocumentChunks  | 🔴  | 契约/proto/实现全缺（US-020 验收项）                                                         |
| 12 | 文档  | POST   | `.../documents/{doc_id}/reparse`     | reparseKnowledgeBaseDocument     | 🟡  | 契约已声明（202 + AsyncTask + 409），route baseline 已登记，proto/实现缺                         |
| 15 | 问答  | GET    | `.../citations`                      | listKnowledgeBaseCitations       | 🟡  | 契约/Gateway 路由已就绪（现 501），kb-service P1 占位 UNIMPLEMENTED                            |
| 16 | 问答  | GET    | `.../sessions`                       | listKnowledgeBaseSessions        | 🟡  | 同上                                                                                |
| 17 | 问答  | GET    | `.../sessions/{session_id}/messages` | listKnowledgeBaseSessionMessages | 🔴  | 契约/proto/实现全缺                                                                     |
| 18 | 问答  | DELETE | `.../sessions/{session_id}`          | deleteKnowledgeBaseSession       | 🔴  | 契约/proto/实现全缺                                                                     |

不在本计划范围：`config`/`rebuild`/`models` 三组路径（另立批次）；`UpdateKBPermissions`（P1 占位维持）。

## 2. 已确认设计决策

| 决策点            | 结论                                                                                                               |
| -------------- | ---------------------------------------------------------------------------------------------------------------- |
| KB 名称/描述更新位置   | 新增 `PUT /knowledge-bases/{kb_id}` 主对象更新，与 `PUT .../config`（chunk\_size 等，触发重建）语义分离                               |
| 会话删除           | DB 硬删（kb\_messages + kb\_sessions 单事务）+ Redis `DEL ani:prod:session:kb:{session_id}`（best-effort）；不存在也返回 204（幂等） |
| citations 数据来源 | 从 `kb_messages.source_chunks`（JSONB，role='assistant'）展开为行，不新建 citation 表                                         |
| reparse 任务下发   | 复用 outbox + async\_tasks 事务模式（与 NotifyDocumentUploaded 同构），返回 202 + AsyncTaskRef                                 |

## 3. 实施链路与顺序约束

每个接口的固定改动链路（顺序不可颠倒，先契约后实现）：

```
1. repo/api/openapi/services/v1.yaml          契约唯一真实来源
2. repo/api/proto/kb/v1/kb_service.proto       gRPC 契约
3. 生成物重新生成                               kb-service/app/generated/kb/v1 与 pkg/generated/pb/kb/v1 两侧同步
4. repo/services/kb-service/app/               repository（RLS）+ servicer（gRPC 专用事件循环 pool）
5. repo/services/ani-gateway/                  kb_grpc_client.go（接口+实现）+ kb_resources.go（handler+路由）
6. repo/architecture/services-route-baseline.yaml   路由差异登记/清理（新 path 路由同批次落地，实现后删除对应条目）
7. repo/architecture/services-contract-baseline.yaml 语义契约豁免登记（新增 operationId 须同步登记，见下）
8. SDK / API docs / Console schema.d.ts 重新生成（零漂移门禁）
```

**硬约束**：

- 新增 v1.yaml path 必须与 Gateway 路由注册**同一批次**落地，否则 `validate-services-route-contract` 出现未登记 diff 直接阻断 PR。
- Gateway 路由必须写成 `svc.METHOD("...")` 直调形式（路由校验器正则限定），且只注册到 `/api/v1/svc` 组（spec-split 门禁禁止注册到 Core 组）。
- **新 operationId 须同步登记** **`services-contract-baseline.yaml`**：KB 域现有 18 个 op 均未声明 security，全部登记 `operation_security` 豁免（services-contract-baseline.yaml:5-58）。新增 op 若不声明 security 又不登记 → `new Services contract violation: {op_id}/operation_security`，`make validate-services` 直接失败；反之未来若为某 op 补 security，须同步删条目（stale 条目同样阻断 PR）。
- 所有有副作用的 PUT/POST 必须要求 `idempotency_key`（CLAUDE.md §4-5）。
- tenant 一律取自 Auth 中间件 context（`instanceTenantID(c)`），忽略请求 body 中的 tenant\_id。

**批次划分**（每批次一个 PR，按依赖排序）：

| 批次 | 内容                                                   | 批次 ID（建议） |
| -- | ---------------------------------------------------- | --------- |
| B1 | #5 KB PUT 更新 + #10 文档详情 GET                          | KB-API-B1 |
| B2 | #11 分块明细 + #16 会话列表 + #17 会话消息 + #18 会话删除 + #15 引用溯源 | KB-API-B2 |
| B3 | #12 reparse                                          | KB-API-B3 |

***

## 4. 批次 B1：KB 主对象更新 + 文档详情

### 4.1 契约层（services/v1.yaml）

**#5 updateKnowledgeBase** — 在 `/knowledge-bases/{kb_id}` path（现有 get/delete，v1.yaml:2166 起）追加 `put`：

```yaml
put:
  operationId: updateKnowledgeBase
  summary: 更新知识库基础信息（名称与描述）
  tags: [KnowledgeBases]
  parameters:
    - { name: kb_id, in: path, required: true, schema: { type: string, format: uuid } }
  requestBody:
    required: true
    content:
      application/json:
        schema: { $ref: '#/components/schemas/UpdateKnowledgeBaseRequest' }
  responses:
    "200":
      description: 更新后的知识库
      content:
        application/json:
          schema: { $ref: '#/components/schemas/KnowledgeBase' }
    "400": { $ref: '#/components/responses/BadRequest' }
    "401": { $ref: '#/components/responses/Unauthorized' }
    "403": { $ref: '#/components/responses/Forbidden' }
    "404": { $ref: '#/components/responses/NotFound' }
```

新增 schema：

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

**#10 getKnowledgeBaseDocument** — 在 `/knowledge-bases/{kb_id}/documents/{doc_id}` path（现只有 delete，v1.yaml:2265 起）追加 `get`，响应复用现有 `KBDocument` schema（已含 parse\_status/error\_message/chunk\_count/custom\_metadata），错误响应 `404 NotFound`。

### 4.2 proto 层

新增（`GetDocument` 已存在，#10 不需要改 proto）：

```protobuf
// UpdateKB updates KB name/description only (metadata; no rebuild).
rpc UpdateKB(UpdateKBRequest) returns (KnowledgeBase);

message UpdateKBRequest {
  string tenant_id    = 1;
  string kb_id       = 2;
  string idempotency_key = 3;
  string name        = 4;   // empty = unchanged
  string description = 5;   // empty = unchanged
}
```

按 `repo/api/proto/buf.gen.yaml` 重新生成两处生成物（kb-service 与 pkg/generated）。

### 4.3 kb-service

- [ ] `app/repositories/knowledge_base.py` 新增 `update_kb(conn, *, tenant_id, kb_id, name, description) -> dict | None`：
  ```sql
  UPDATE knowledge_bases
  SET name        = COALESCE(NULLIF($3, ''), name),
      description = COALESCE(NULLIF($4, ''), description),
      updated_at  = now()
  WHERE id = $2            -- RLS 由 set_tenant_context 保证租户隔离
  RETURNING <全列>
  ```
  （沿用现有方法模式：模块级 async 函数，事务内 `rls.set_tenant_context`。）
- [ ] `app/api/grpc_server.py` servicer 新增 `UpdateKB`（sync 薄壳 + `_run_async` 提交 gRPC 专用事件循环，与 `GetKB` 同模式）：
  1. 校验 `idempotency_key`/`kb_id` 必填（INVALID\_ARGUMENT）
  2. `async_task_repo.find_by_idempotency_key` 幂等重放检查
  3. `update_kb` 返回 None → NOT\_FOUND
  4. 写 async\_tasks 幂等记录（result=更新后行，与 CreateKB 同模式）
  5. 返回 KnowledgeBase（行 → proto 的映射复用现有 `_kb_row_to_pb` 等辅助，grpc\_server.py:1357）
- [ ] **#10 无需改动**：`GetDocument` 已实现（grpc\_server.py:567 起）。

### 4.4 Gateway

- [ ] `kb_grpc_client.go`：`KBGRPCClient` 接口 + 实现 + 测试 fake 各加 `UpdateKB(ctx, tenantID, kbID, idemKey, name, description)`（tenant 写入 proto request；`callCtx` 默认 5s）。
- [ ] `kb_resources.go`：
  - [ ] handler `updateKnowledgeBase`：nil-client 守卫(503) → BindJSON（校验 idempotency\_key 必填）→ client 调用 → `writeKBError` → `kbToJSON` 输出
  - [ ] handler `getKnowledgeBaseDocument`：nil 守卫 → `a.client.GetDocument(ctx, instanceTenantID(c), kbID, docID)`（client 方法已存在）→ `kbDocumentToJSON`
  - [ ] 路由注册（`registerKnowledgeBasesWithClient`）：
    ```go
    svc.PUT("/knowledge-bases/:kb_id", a.updateKnowledgeBase)
    svc.GET("/knowledge-bases/:kb_id/documents/:doc_id", a.getKnowledgeBaseDocument)
    ```

### 4.5 基线维护

- **路由基线**：两条路径均已存在（无 spec\_not\_in\_code 条目），新增 operation 同批次注册路由即可，无需动 `services-route-baseline.yaml`。
- **契约基线（必做）**：`services-contract-baseline.yaml` 新增 2 条豁免，与现有 KB 域条目同格式：
  ```yaml
  - operation_id: updateKnowledgeBase
    rule: operation_security
    reason: 当前 Services OpenAPI 尚未声明顶层或操作级认证要求。
  - operation_id: getKnowledgeBaseDocument
    rule: operation_security
    reason: 当前 Services OpenAPI 尚未声明顶层或操作级认证要求。
  ```
  不登记则 `make validate-services` 的 services-contract 校验直接失败（见 §3 硬约束）。

### 4.6 测试

- [ ] kb-service pytest：UpdateKB 成功 / NOT\_FOUND / 跨租户 RLS 隔离 / 幂等重放 / 空字段不改（name 或 description 为空保持原值）
- [ ] Gateway go test：2 条路由注册断言（kb\_resources\_test.go 模式）+ fake client handler 单测
- [ ] 契约自测：`validate_services_route_contract` 无新 diff

### 4.7 批次验收

```bash
cd repo
make validate-services   # 含 route contract / 语义契约 / SDK+docs 零漂移（不含 kb-service pytest）
make test                # Go 服务测试 + rag-engine 语法检查（不含 kb-service pytest）
cd services/kb-service && python -m pytest   # kb-service 测试须显式运行（不在任何 make 目标内）
cd ../.. && make validate-architecture
git diff --check
```

> **门禁盲区提示**：`make test` 的 `test-python` 仅 `compileall ai/rag-engine`、`GO_PACKAGES` 不含 Python 服务，`validate-services` 亦不跑 kb-service 测试——各批次 §4.6/§5.6/§6.6 列的 pytest 用例**不会被所列 make 门禁执行**，必须显式调用。

***

## 5. 批次 B2：分块明细 + 会话三件套 + 引用溯源

### 5.1 契约层（services/v1.yaml）

**#11 新增 path** `/knowledge-bases/{kb_id}/documents/{doc_id}/chunks`（GET）：

- parameters：`kb_id`/`doc_id`（path）；`limit`（query，default 50，1–100）；`cursor`（query）；`chunk_type`（query，可选过滤 `child|parent|doc_summary`）
- 响应：200 → `KBChunkListResponse`；`401/403/404`

新增 schema（列名对齐 `kb_chunks` 真实表结构）：

```yaml
KBChunk:
  type: object
  required: [id, doc_id, kb_id, chunk_type, content, file_name, created_at]
  properties:
    id:              { type: string, format: uuid }
    doc_id:          { type: string, format: uuid }
    kb_id:           { type: string, format: uuid }
    parent_chunk_id: { type: string, format: uuid, nullable: true }
    chunk_type:      { type: string, enum: [child, parent, doc_summary] }
    content:         { type: string }
    parent_content:  { type: string, nullable: true }
    page_number:     { type: integer, nullable: true }
    content_type:    { type: string }
    file_name:       { type: string, description: "所属文档文件名（kb_chunks.file_name，NOT NULL 冗余列）" }
    token_count:     { type: integer }
    custom_metadata: { type: object }
    created_at:      { type: string, format: date-time }

KBChunkListResponse:
  type: object
  required: [items]
  properties:
    items:       { type: array, items: { $ref: '#/components/schemas/KBChunk' } }
    next_cursor: { type: string, nullable: true }
```

**#17 新增 path** `/knowledge-bases/{kb_id}/sessions/{session_id}/messages`（GET）：

- parameters：`kb_id`/`session_id`（path）；`limit`（query，default 100，max 100，对齐 proto `CursorPageRequest` 上限与 KB 域惯例）；`cursor`（query，`created_at|id` 复合键升序游标，与 §5.3 排序设计一致——kb\_messages.id 为随机 UUID，纯 id 游标不成立）
- 响应：200 → `KBSessionMessageListResponse`；`401/403/404`

**#18 新增 path** `/knowledge-bases/{kb_id}/sessions/{session_id}`（DELETE）：

- 响应：`204`（幂等：不存在也 204）；`401/403/404`（404 仅当 KB 不存在；session 不存在返回 204）

新增 schema：

```yaml
KBSessionMessage:
  type: object
  required: [id, session_id, role, content, created_at]
  properties:
    id:            { type: string, format: uuid }
    session_id:    { type: string, format: uuid }
    role:          { type: string, enum: [user, assistant] }
    content:       { type: string }
    sources:       { type: array, items: { $ref: '#/components/schemas/KBSourceChunk' }, nullable: true }
    input_tokens:  { type: integer, nullable: true }
    output_tokens: { type: integer, nullable: true }
    duration_ms:   { type: integer, nullable: true }
    created_at:    { type: string, format: date-time }

KBSessionMessageListResponse:
  type: object
  required: [items]
  properties:
    items:       { type: array, items: { $ref: '#/components/schemas/KBSessionMessage' } }
    next_cursor: { type: string, nullable: true }
```

- [ ] 检查 v1.yaml components 是否已有可复用的 `SourceChunk`/`KBSourceChunk` 公共 schema（`KBQueryResponse` 内可能为内联定义）；无则新增：
  ```yaml
  KBSourceChunk:
    type: object
    properties:
      doc_id:    { type: string, format: uuid }
      file_name: { type: string }
      page:      { type: integer, nullable: true }
      content:   { type: string }
      score:     { type: number, nullable: true }
  ```

**#15/#16 契约需一处增强**：`KBCitationListResponse`/`KBSessionListResponse`（v1.yaml:837–873）已存在，但 `KBCitation` 缺「回答定位」字段——溯源视图（哪些回答引用了哪些文档）无法定位到具体回答，需补 `message_id`/`session_id`（该接口从未发布，无兼容性负担）：

```yaml
KBCitation:   # 在现有 schema（v1.yaml:837-848）上追加两个属性
  properties:
    # ... 现有 id/kb_id/doc_id/file_name/page/content/score/created_at 保持不变 ...
    message_id: { type: string, format: uuid, nullable: true, description: "引用该文档的回答消息 id（定位/跳转用）" }
    session_id: { type: string, format: uuid, nullable: true, description: "回答所属会话 id" }
```

同步修改 proto `KBCitation`（见 §5.2）与 Gateway `kbCitationToJSON`（见 §5.4）。

### 5.2 proto 层

```protobuf
rpc ListDocumentChunks(ListDocumentChunksRequest) returns (ListDocumentChunksResponse);
rpc GetSessionMessages(GetSessionMessagesRequest) returns (GetSessionMessagesResponse);
rpc DeleteSession(DeleteSessionRequest) returns (google.protobuf.Empty);

message KBChunk {
  string id = 1;  string doc_id = 2;  string kb_id = 3;
  string parent_chunk_id = 4;
  string chunk_type = 5;            // child | parent | doc_summary
  string content = 6;  string parent_content = 7;
  int32  page_number = 8;  string content_type = 9;  int32 token_count = 10;
  string custom_metadata = 11;      // JSONB string
  google.protobuf.Timestamp created_at = 12;
  string file_name = 13;            // kb_chunks.file_name (NOT NULL)
}
message ListDocumentChunksRequest {
  string tenant_id = 1;  string kb_id = 2;  string doc_id = 3;
  string chunk_type = 4;  common.v1.CursorPageRequest page = 5;
}
message ListDocumentChunksResponse {
  repeated KBChunk items = 1;  string next_cursor = 2;
}

message KBSessionMessage {
  string id = 1;  string session_id = 2;  string role = 3;  string content = 4;
  string source_chunks = 5;   // JSONB string
  int32  input_tokens = 6;  int32 output_tokens = 7;  int64 duration_ms = 8;
  google.protobuf.Timestamp created_at = 9;
}
message GetSessionMessagesRequest {
  string tenant_id = 1;  string kb_id = 2;  string session_id = 3;
  common.v1.CursorPageRequest page = 4;
}
message GetSessionMessagesResponse {
  repeated KBSessionMessage items = 1;  string next_cursor = 2;
}
message DeleteSessionRequest {
  string tenant_id = 1;  string kb_id = 2;  string session_id = 3;
}
```

（#15/#16 的 `ListKBCitations`/`ListKBSessions` RPC 已声明；但 `KBCitation` message 需追加回答定位字段：）

```protobuf
message KBCitation {   // 在现有 message（kb_service.proto:251-260）上追加
  // ... 现有 id/kb_id/doc_id/file_name/page/content/score/created_at（field 1-8）保持不变 ...
  string message_id = 9;   // 引用该文档的回答消息 id；空 = 未知
  string session_id = 10;  // 回答所属会话 id
}
```

### 5.3 kb-service

**repository 新增**：

- [ ] `app/repositories/chunk.py`：`list_chunks_by_doc_paged(conn, *, tenant_id, kb_id, doc_id, chunk_type, cursor, limit)` — 现有 `list_chunks_by_doc` 无分页；新增 `chunk_type` 可选过滤 + id 升序游标（`id > cursor ORDER BY id ASC`，与 `list_kbs` 模式一致）
- [ ] `app/repositories/message.py`：
  - `list_sessions(conn, *, tenant_id, kb_id, cursor_ts, cursor_id, limit)`（#16）：
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
    游标为复合键 `{created_at_iso}|{session_id}`；首页不传 cursor。id 为随机 UUID，故排序键必须含 created\_at。
  - `get_session(conn, *, tenant_id, kb_id, session_id)` — 归属校验（session 必须属于该 kb\_id；RLS 保证租户隔离），防跨 KB 枚举 session\_id
  - `list_session_messages_paged(conn, *, tenant_id, session_id, cursor, limit)`（#17）— **`ORDER BY created_at ASC, id ASC`** **复合排序**（kb\_messages.id 为 `gen_random_uuid()` 随机值，init\_schema.sql:357——纯 id ASC 不等于时间序，回放会乱序；user/assistant 消息虽分属不同事务 created\_at 可区分，但必须叠加 id 做 tie-break；cursor 与 sessions/citations 同为 `created_at|id` 复合键集游标，方向为 ASC）
  - `delete_session(conn, *, tenant_id, kb_id, session_id) -> bool`（#18）— 单事务内 `DELETE FROM kb_messages WHERE session_id` + `DELETE FROM kb_sessions WHERE session_id AND kb_id`（需在调用者事务中执行，`*_in_tx` 命名与现有模式对齐）
- [ ] `app/session/cache.py`：`SessionCache` 新增 `async def delete_session(self, *, session_id)`（keyword-only，与 `append_message`/`list_messages` 签名惯例一致）— `DEL ani:prod:session:kb:{session_id}`（`KEY_PREFIX`，cache.py:28）；失败仅 `logger.warning`（best-effort，24h TTL 自然过期，与 `append_message` 降级模式一致）

**servicer 新增/替换**：

- [ ] `ListDocumentChunks`（#11）：`get_kb` 门禁（NOT\_FOUND）→ `get_document`（含软删过滤，NOT\_FOUND）→ `list_chunks_by_doc_paged` → KBChunk 映射
- [ ] `GetSessionMessages`（#17）：`get_kb` 门禁 → `get_session` 归属校验（session 不属于该 KB → NOT\_FOUND）→ `list_session_messages_paged`；`source_chunks` JSON 字符串原样透传
- [ ] `DeleteSession`（#18）：`get_kb` 门禁 → 单事务 `delete_session`（消息+会话）；session 不存在**仍返回 Empty**（幂等 204 语义，KB 不存在才 NOT\_FOUND）→ 事务提交后 best-effort `cache.delete_session`
- [ ] `ListKBCitations`（#15，替换 `app/api/p1_rpcs.py` 占位并从 grpc\_server.py 委托点接入真实实现）：
  ```sql
  SELECT m.id, m.session_id, m.created_at, m.source_chunks
  FROM kb_messages m
  JOIN kb_sessions s ON s.id = m.session_id
  WHERE s.kb_id = $1 AND m.role = 'assistant'
    AND m.source_chunks IS NOT NULL AND m.source_chunks <> 'null'
    [AND (m.created_at, m.id) < ($cursor_ts, $cursor_id)]
  ORDER BY m.created_at DESC, m.id DESC
  LIMIT $limit
  ```
  Python 侧展开 `source_chunks` JSON → 每条 source 生成一个 `KBCitation`（message\_id/session\_id/doc\_id/file\_name/page/content/score；`created_at` = message 时间）。**id 生成**：`uuid.uuid5(NAMESPACE_URL, f"ani:kb:citation:{kb_id}:{message_id}:{doc_id}")` 确定性生成——uuid 合规（契约 `format: uuid`）、同一 (回答， 文档) 幂等、分页重放稳定。同一回答引用同一文档多个 chunk 时取 score 最高一条（citation 按 message×doc 粒度出）。分页以 message 为单位，`next_cursor` = 最后一条消息的复合游标。
- [ ] `ListKBSessions`（#16，替换占位）→ `list_sessions` 聚合结果映射 `KBSession`
- [ ] **更新锁定 UNIMPLEMENTED 的既有测试**（`tests/test_grpc_wiring.py:191-209`、`tests/test_grpc_server.py:209-230` 相关断言改为真实行为）

### 5.4 Gateway

- [ ] `kb_grpc_client.go`：接口 + 实现 + fake 加 `ListDocumentChunks`/`GetSessionMessages`/`DeleteSession`（tenant 注入 proto request，`callCtx` 5s）
- [ ] `kb_resources.go`：
  - [ ] handler `listKnowledgeBaseDocumentChunks`（queryInt limit + cursor + chunk\_type）→ `chunkToJSON`（`custom_metadata`：proto string → `json.Unmarshal` → object 输出）
  - [ ] handler `listKnowledgeBaseSessionMessages` → `sessionMessageToJSON`（`source_chunks` string → Unmarshal → `sources` 数组；DB 列名 `source_chunks` 映射为 REST 字段 `sources`）
  - [ ] handler `deleteKnowledgeBaseSession` → 成功 `c.Status(204)`
  - [ ] 路由注册：
    ```go
    svc.GET("/knowledge-bases/:kb_id/documents/:doc_id/chunks", a.listKnowledgeBaseDocumentChunks)
    svc.GET("/knowledge-bases/:kb_id/sessions/:session_id/messages", a.listKnowledgeBaseSessionMessages)
    svc.DELETE("/knowledge-bases/:kb_id/sessions/:session_id", a.deleteKnowledgeBaseSession)
    ```
  - [ ] `citations`/`sessions` 列表 handler 已存在（现 501 兜底），kb-service 实现上线后自动激活；但 `kbCitationJSON` 结构体（kb\_resources.go:471 起）与 `kbCitationToJSON`（:545 起）需补 `message_id`/`session_id` 两个字段的映射（omitempty，空串不出）
  - [ ] `kbCitationJSON` 若 `Page` 为 `int32` 零值默认输出 0，注意与契约 `nullable` 对齐（可加 `omitempty` 或改指针类型；以现有 `KBSessionMessage` 序列化惯例为准，不强制）

### 5.5 基线维护

- **路由基线**：#11/#17/#18 三条新 path 的路由同批次注册（§5.4），无既有 spec\_not\_in\_code 条目，无需动 `services-route-baseline.yaml`；**不得误删**该文件中其他 KB 条目（config/rebuild/models，不在本计划范围）。
- **契约基线（必做）**：`services-contract-baseline.yaml` 新增 3 条豁免（同 §4.5 格式）：
  ```yaml
  - operation_id: listKnowledgeBaseDocumentChunks
    rule: operation_security
    reason: 当前 Services OpenAPI 尚未声明顶层或操作级认证要求。
  - operation_id: listKnowledgeBaseSessionMessages
    rule: operation_security
    reason: 当前 Services OpenAPI 尚未声明顶层或操作级认证要求。
  - operation_id: deleteKnowledgeBaseSession
    rule: operation_security
    reason: 当前 Services OpenAPI 尚未声明顶层或操作级认证要求。
  ```
  （#15/#16 的 `listKnowledgeBaseCitations`/`listKnowledgeBaseSessions` 已在 baseline:35-40，无需重复登记。）

### 5.6 测试

- [ ] kb-service pytest：
  - chunks：分页/chunk\_type 过滤/文档不存在 404/软删文档 404/跨租户隔离
  - sessions 列表：message\_count/last\_query/last\_active\_at 聚合正确、复合游标翻页
  - session messages：user+assistant 完整回放顺序、sources/token 字段（注：仅 assistant 消息带 source\_chunks，user 消息该列为 NULL——序列化时 sources 输出 null/缺省）、跨 KB session 越权 404
  - session delete：删除后 GetSessionMessages 404、消息行数清零、Redis DEL 被调用（mock SessionCache）、重复删除幂等
  - citations：空 sources 返回空列表、多消息展开、跳过无 source 消息、分页；**id 为 uuid5 确定性生成**——同 (message, doc) 重放幂等且符合契约 `format: uuid`；`message_id`/`session_id` 正确透传
- [ ] Gateway go test：3 条新路由注册断言 + handler 单测（含 `custom_metadata`/`sources` object 化序列化）

### 5.7 批次验收

同 B1（§4.7，含 kb-service pytest 显式调用）。

***

## 6. 批次 B3：reparse 重新解析

### 6.1 契约层

无改动（`reparse` path 已声明，v1.yaml:2379–2407；`ReparseDocumentRequest` schema 已含 idempotency\_key）。

### 6.2 proto 层

```protobuf
// ReparseDocument re-queues a document for parsing: parse_status → pending,
// existing chunks/vectors are overwritten by the orchestrator.
rpc ReparseDocument(ReparseDocumentRequest) returns (common.v1.AsyncTaskRef);

message ReparseDocumentRequest {
  string tenant_id = 1;  string kb_id = 2;  string doc_id = 3;
  string idempotency_key = 4;
}
```

### 6.3 kb-service

- [ ] servicer `ReparseDocument`（复用 NotifyDocumentUploaded 原子 outbox 事务模式，grpc\_server.py:497–558 同构）：
  1. 校验 `idempotency_key` 必填（INVALID\_ARGUMENT）
  2. 幂等键 = 客户端 `request.idempotency_key`（契约 required 字段；与 CreateKB「客户端键优先」惯例一致——grpc\_server.py:191。notify 用固定合成键是因为其 proto 无 idempotency\_key 字段——grpc\_server.py:473-477——reparse proto 有该字段，不得照抄）；`find_by_idempotency_key` 命中 → 重放返回同一 AsyncTaskRef（重放条件与 notify 同构：status 为 pending/completed；任务 failed 后重试须换新键）
  3. `get_kb`：不存在 → NOT\_FOUND；`status='rebuilding'` → FAILED\_PRECONDITION（Gateway 映射 409 kb.rebuilding）
  4. `get_document`：不存在/软删 → NOT\_FOUND；`parse_status='ready'` → FAILED\_PRECONDITION（防止误触发覆盖；不加 force 字段，YAGNI）；返回的 doc\_row 同时为第 5 步 payload 提供 `storage_path`/`file_name`（notify 这两个字段来自 request，reparse 无 request 来源，须取自 doc\_row；notify 的 file\_name 恒为空串，reparse 可取真实值）
  5. 单事务（`conn.transaction()`，RLS 在事务内 set）：
     - `UPDATE kb_documents SET parse_status='pending', error_message=NULL, chunk_count=0, parsed_at=NULL`（**chunk\_count 置 0 而非 NULL**——该列 `INT NOT NULL DEFAULT 0`，置 NULL 违反约束事务必失败，init\_schema.sql:337；error\_message/parsed\_at 可空可置 NULL。不硬复用 `update_parse_status_in_tx`——其 COALESCE 语义无法把 chunk\_count 重置为 0/parsed\_at 清 NULL，需新增 `reset_for_reparse_in_tx` 或扩展该方法）
     - `INSERT async_tasks`（task\_type=`kb.reparse`，幂等键 = 客户端 idempotency\_key；v1.yaml AsyncTask.task\_type 为自由 string 无 enum 限制）
     - `INSERT outbox_events`（event\_type=`kb.reparse`（表内记录语义用）；payload 与 notify 模板同构：doc\_id/kb\_id/storage\_path/tenant\_id/file\_name/object\_id/chunk\_size——**dispatcher 不按 event\_type 过滤**，任意事件都会发布到 `ani.tasks.kb.parse` subject 被 parse\_consumer 消费，payload 实际被读取的仅 6 字段：doc\_id/kb\_id/object\_id/tenant\_id/file\_name/chunk\_size，其余为兼容性冗余，与 notify 现状一致）
  6. 返回 AsyncTaskRef

> 已核实（2026-09-01 代码审查）：`parse_orchestrator.process_document` **已内置** reparse 幂等清理——`delete_chunks_by_doc` 清 kb\_chunks 行（parse\_orchestrator.py:315-321）+ best-effort `core.delete_vector_store_documents(filter='doc_id == "..."')` 清 Core 向量（parse\_orchestrator.py:322-332），与 DeleteDocument 同模式。**无需新增任何 orchestrator 改动**。

### 6.4 Gateway

- [ ] `kb_grpc_client.go`：`ReparseDocument(ctx, tenantID, kbID, docID, idemKey)`（返回 AsyncTaskRef）
- [ ] `kb_resources.go`：handler `reparseKnowledgeBaseDocument`（BindJSON 校验 idempotency\_key → 202 + AsyncTask JSON，复用现有 AsyncTaskRef 序列化辅助）；路由注册：
  ```go
  svc.POST("/knowledge-bases/:kb_id/documents/:doc_id/reparse", a.reparseKnowledgeBaseDocument)
  ```
- [ ] 409 语义：`mapGRPCError` 现有 `FAILED_PRECONDITION→409` 已覆盖；handler 把 doc/KB 标识填入 409 响应体供前端展示

### 6.5 基线维护（本批次必做）

- [ ] **路由基线**：删除 `repo/architecture/services-route-baseline.yaml` 中 reparse 的 `spec_not_in_code` 条目（当前 56–61 行）——不删则 stale 条目阻断 PR；**不得误删 config/rebuild/models 等其他条目**（不在本计划范围）。
- [ ] **契约基线**：无新增（`reparseKnowledgeBaseDocument` 已在 `services-contract-baseline.yaml:44-46` 登记，且本批次契约层无改动）。

### 6.6 测试

- [ ] kb-service pytest：reparse 后 parse\_status 回 pending、chunk\_count 重置为 0、error\_message/parsed\_at 清 NULL、outbox\_events 落一行（event\_type=`kb.reparse`）、async\_tasks 幂等记录；ready 拒绝 409；KB rebuilding 拒绝；文档不存在 404；同 idempotency\_key 重放同 task\_id、failed 任务换新键可重新发起；（回归确认）现有 orchestrator 清理链路不被 reparse 破坏
- [ ] Gateway go test：路由注册断言 + 202/409/404 映射单测

### 6.7 批次验收

同 B1（§4.7，含 kb-service pytest 显式调用），另加基线清理后重跑 `validate-services-route-contract` 确认无 stale 报错。

***

## 7. 全局门禁与生成物（每批次 PR 检查单）

```bash
cd repo
make validate-services      # Services boundary + API split + route contract + 语义契约 + SDK/docs 生成物零漂移
make test                   # Go 服务测试 + rag-engine 语法检查（不含 kb-service pytest）
cd services/kb-service && python -m pytest   # 显式运行（make 门禁盲区）
cd ../.. && make validate-architecture
git diff --check
```

生成物重新生成（`validate-services` 内置执行并校验零漂移）：

- `scripts/gen_sdk_alpha.py` → `sdks/services`（Services SDK 不得含 Core 资源）
- `scripts/generate_api_docs.py` → `docs/api/services.html`
- Console `npm run gen-api` → `frontends/console/src/api/schema.d.ts`（零漂移）

## 8. 进度文档闭环（每批次，Feature batch 规则）

每个批次完成时同步更新 4 个文件（CLAUDE.md §6-3）：

| 文件                                    | 更新内容                    |
| ------------------------------------- | ----------------------- |
| `repo/development-records/{批次 ID}.md` | 新建：实现细节、验证证据、涉及接口清单     |
| `repo/development-records/README.md`  | 追加批次索引行                 |
| `repo/CURRENT-SPRINT.md`              | 当前 Sprint 完成项 + 下一步     |
| `ANI-06-开发计划.md`                      | Section 零 + 当前 Sprint 段 |

PR 纪律遵循 `ANI-15-GitHub-协作规范与提交纪律.md`（开发和 CI 并行，main 受保护串行收口；触碰 `ani-gateway` 等 Services 根目录需 CODEOWNERS 共同审查）。

## 9. 接口改动量速查表

| #  | 接口             | v1.yaml                                | proto         | kb-service                 | Gateway                        | 主要工作量    |
| -- | -------------- | -------------------------------------- | ------------- | -------------------------- | ------------------------------ | -------- |
| 5  | PUT /kb\_id    | +put +schema                           | +UpdateKB     | +repo.update\_kb +RPC      | +client +handler               | 薄接口，全新   |
| 10 | GET doc        | +get operation                         | 已有            | **已有**                     | +handler（client 已有）            | 最小       |
| 11 | GET chunks     | +path +2 schema                        | +RPC +KBChunk | +分页查询                      | +client +handler               | 中（新数据模型） |
| 12 | POST reparse   | 已声明                                    | +RPC          | +outbox 事务（复用现有清理链路）       | +client +handler +基线删除         | 小-中      |
| 15 | GET citations  | 已声明（+message\_id/session\_id 增强见 §5.1） | +field 9/10   | +source\_chunks 展开（替换占位）   | 激活（已有）+kbCitationJSON 补 2 字段映射 | 小-中      |
| 16 | GET sessions   | 已声明                                    | 已有            | +聚合查询（替换占位）                | 激活（已有）                         | 小        |
| 17 | GET messages   | +path +2 schema                        | +RPC +message | +归属校验 +分页                  | +client +handler               | 中        |
| 18 | DELETE session | +path                                  | +RPC          | +硬删 +cache.delete\_session | +client +handler               | 小        |

## 10. 风险与注意事项

1. **proto 生成物两侧同步**：`kb-service/app/generated/kb/v1` 与 `pkg/generated/pb/kb/v1`（Gateway 依赖）必须同一批次重新生成，禁止手改生成物。
2. **已知契约瑕疵（custom\_metadata）**：proto 为 string(JSONB)、v1.yaml 声明为 object（issue-018 记录）。本计划新 handler（chunks 的 custom\_metadata、messages 的 sources）统一 `json.Unmarshal` 后以 object 输出，对齐契约；**既有 KBDocument 序列化行为不在本计划改动**（避免扩散），如需修正另开 micro-batch。
3. **P1 占位测试锁定**：`test_grpc_wiring.py`/`test_grpc_server.py` 中断言 UNIMPLEMENTED 的用例必须随实现同步更新。**注意**：kb-service pytest 不在任何 make 门禁内（`make test` 的 `test-python` 仅 compileall rag-engine），遗漏更新不会被 CI 拦截，必须在批次验收时显式运行（§4.7）。
4. **游标设计**：sessions/citations/messages 的 id 均为随机 UUID（`gen_random_uuid()`），排序必须用 `created_at` 复合键（`created_at|id`）；messages 回放为 ASC、sessions/citations 列表为 DESC。**chunks 单列 id 游标仅在其排序稳定的前提下成立**——`write_chunks` 单事务批量插入时所有行 created\_at 相同（PG `now()` 为事务时间），故 chunks 排序/游标用 id ASC 是当前写入模式下唯一稳定方案（chunk\_id 由 rag-engine `uuid.uuid4()` 生成、外部传入），但排序语义为「id 字典序」而非「文档内位置序」；若未来需要按文档位置序输出（parent→child 顺序），需在解析写入侧引入序号列，不在本计划范围。
5. **路由注册形态**：必须 `svc.METHOD("...")` 直调（校验器正则限定）；新 path 若只进 v1.yaml 不注册 Gateway 路由，route contract 直接阻断——契约与路由必须同批次。
6. **契约基线登记（services-contract-baseline.yaml）**：KB 域现有 18 个 op 均登记 `operation_security` 豁免，新增 5 个 operationId 必须同批次补登记（B1 两个：§4.5；B2 三个：§5.5），否则 `make validate-services` 必失败；未来若为 KB 域统一补 security 声明，须同步删条目（stale 同样阻断）。
7. **reparse 复用现有幂等清理链路**：`parse_orchestrator` 已内置 kb\_chunks 行清理 + best-effort Core 向量清理（按 doc\_id filter，parse\_orchestrator.py:315-332），无需为 reparse 新增清理逻辑；servicer 只负责状态回退 + outbox 投递。
8. **Redis 删除 best-effort**：`delete_session` 失败不阻断删除（24h TTL 自然过期），与现有缓存降级策略一致。
9. **范围纪律**：不动 `config`/`rebuild`/`models` 三组路径的 baseline 条目；不实现 `UpdateKBPermissions`（维持 P1 占位）；不给 reparse 加 force 参数（YAGNI）。

