# SPEC: kb-service 薄业务层 (P0)

> Technical specification derived from:
> - PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-001,002,004,005,007,008,009,010)
> - UX: N/A — backend-only
> Generated: 2026-07-23 | Target branch: main | Product line: core (Services / kb-service)

## 1. Summary

### 1.1 What This SPEC Covers
新建 `repo/services/kb-service/`（Python 3.11 + FastAPI + gRPC），承接 `kb_service.proto` 10 个 RPC + Phase A 新增 3 个 P1 RPC 声明，实现知识库/文档元数据 CRUD、两步式 pre-signed URL 上传、自有 outbox 派发、Redis 会话缓存、Core API client 与 rag-engine gRPC client。同时修复 Services OpenAPI 契约（US-001）、新增 reparse/config/rebuild/models 端点（US-002）、model proto ocr capability（US-004）、kb_chunks + pg_trgm 迁移（US-005）、SDK 重生成（US-007）。

### 1.2 PRD Reference
- Source: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX source: N/A — backend-only
- User Stories covered: US-001, US-002, US-004, US-005, US-007, US-008, US-009, US-010
- Functional Requirements covered: FR-1, FR-2, FR-3, FR-9, FR-14, FR-15, FR-17, FR-18

### 1.3 Design Decisions Summary
| Decision | Choice | Rationale |
|----------|--------|-----------|
| Outbox 派发 | kb-service 自有轮询派发器发布到 NATS `ani.tasks.kb.parse` | kb-service 完全自治，不依赖 task-service 进程；NotifyDocumentUploaded 同事务写 kb_documents+async_tasks+outbox_events |
| 技术栈 | Python 3.11 + FastAPI + grpcio + asyncpg + nats-py + redis-py | 与 rag-engine 同栈，复用 Python 生态；PRD §6 指定 |
| Core API 调用 | httpx async client（非 SDK） | kb-service 是 Services 层，通过 Core OpenAPI REST 调用，遵循 CLAUDE.md §3 跨层边界 |
| 向量清理 | 调 Core `DELETE /vector-stores/{id}/documents?filter=doc_id=="..."` | 复用 spec-core-kb-vector-delete.md 新增端点，不在 kb-service 直连 Milvus |
| 会话缓存 | Redis key `ani:prod:session:kb:{session_id}`，TTL 24h，LTRIM 20 | FR-9 强制 |
| idempotency_key | CreateKB / NotifyDocumentUploaded / Query 必填 | PRD §7 + §10 ANI Boundaries 强制 |

---

## 2. Architecture

### 2.1 System Context
kb-service 是薄业务层，不承载 AI 逻辑。对上经 ani-gateway（gRPC）暴露给 Console；对下通过 Core OpenAPI REST 调用 vector-store/object-store，通过 gRPC 调用 rag-engine Query，通过 NATS 派发解析任务给 rag-engine parse_worker。

```
Console → ani-gateway ─gRPC─→ kb-service ─┬─REST─→ Core (vector-stores / objects)
                                          ├─gRPC──→ rag-engine (Query)
                                          ├─NATS──→ rag-engine (parse_worker)
                                          └─Redis  (session cache)
                              rag-engine ─REST→ Core (objects download)
                                          └Milvus (direct, 见 rag-engine SPEC)
```

### 2.2 Component Design
- **api/grpc_server.py**：实现 `KBService` servicer，承接 10 个 RPC
- **repositories/**：4 张 KB 表 + kb_chunks 的数据访问（asyncpg + RLS 过滤）
- **core_api/client.py**：httpx async client，封装 vector-stores / objects 端点
- **rag_engine/client.py**：grpcio async client，调用 Query
- **outbox/dispatcher.py**：轮询 outbox_events 发布到 NATS
- **session/cache.py**：Redis 会话缓存
- **migrations/**：kb_chunks + pg_trgm 迁移脚本

### 2.3 Module Interactions
1. `CreateKB`：校验 idempotency_key → 写 `knowledge_bases` → Core `POST /vector-stores` 创建集合 → 返回
2. `GetDocumentUploadURL`：预生成 doc_id（UUID）+ 写 `kb_documents`(parse_status=pending) → Core `POST /objects/upload` 取 presigned PUT URL → 返回
3. `NotifyDocumentUploaded`：校验 checksum（Core HEAD object）→ 同事务写 `kb_documents` 更新 + `async_tasks` + `outbox_events` → 返回 AsyncTaskRef
4. outbox dispatcher 轮询未派发事件 → NATS `ani.tasks.kb.parse` → 标记已派发
5. `Query`：校验 idempotency_key → 写 `kb_messages` + Redis 会话 → gRPC 调 rag-engine Query → 返回
6. `DeleteDocument`：软删 `kb_documents` → Core `DELETE /vector-stores/{id}/documents?filter=doc_id=="..."`（best-effort）
7. `DeleteKB`：软删 `knowledge_bases` → Core `DELETE /vector-stores/{id}`

### 2.4 File Structure
```
repo/services/kb-service/
├── Dockerfile                       [NEW]
├── requirements.txt                 [NEW]
├── main.py                          [NEW: FastAPI + grpc server 启动]
├── app/
│   ├── api/grpc_server.py           [NEW: KBService servicer 10 RPC]
│   ├── api/p1_rpcs.py               [NEW: 3 个 P1 RPC 声明返回 UNIMPLEMENTED]
│   ├── repositories/
│   │   ├── knowledge_base.py        [NEW]
│   │   ├── document.py              [NEW]
│   │   ├── message.py               [NEW]
│   │   ├── async_task.py            [NEW]
│   │   ├── outbox.py                [NEW]
│   │   └── chunk.py                 [NEW: kb_chunks]
│   ├── core_api/
│   │   └── client.py                [NEW: httpx vector-stores/objects]
│   ├── rag_engine/
│   │   └── client.py                [NEW: grpcio Query]
│   ├── outbox/
│   │   └── dispatcher.py            [NEW: NATS 派发]
│   ├── session/
│   │   └── cache.py                 [NEW: Redis]
│   └── core/config.py               [NEW]
├── migrations/
│   ├── 001_pg_trgm_extension.sql    [NEW: US-005]
│   └── 002_kb_chunks.sql            [NEW: US-005]
└── tests/                           [NEW]

repo/api/proto/kb/v1/kb_service.proto          [MODIFY: US-001 字段统一 + US-004 ocr 注释 + P1 RPC 声明]
repo/api/openapi/services/v1.yaml              [MODIFY: US-001 修复 + US-002 新增端点]
repo/api/proto/model/v1/model_service.proto   [MODIFY: US-004 ocr capability 注释]
repo/services/tasks/modules/prd/...            [MODIFY: SDK 重生成 US-007]
```

---

## 3. Data Model

### 3.1 Schema Changes
现有 4 张 KB 表（`knowledge_bases` / `kb_documents` / `kb_messages` / `async_tasks` / `outbox_events`）已迁移就绪（PRD §7 依赖就绪）。本 SPEC 新增：

**US-005 新增 `kb_chunks` 表（与 plan.md §3.1 一致）：**

```sql
CREATE TABLE IF NOT EXISTS kb_chunks (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       UUID NOT NULL,
  kb_id           UUID NOT NULL,
  doc_id          UUID NOT NULL,
  parent_chunk_id UUID,
  chunk_type       TEXT NOT NULL CHECK (chunk_type IN ('child','parent','doc_summary')),
  content         TEXT NOT NULL,
  parent_content  TEXT,
  page_number     INT,
  content_type    TEXT,
  file_name       TEXT NOT NULL,
  token_count     INT,
  custom_metadata JSONB DEFAULT '{}'::jsonb,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_kb_chunks_kb_doc   ON kb_chunks(kb_id, doc_id);
CREATE INDEX IF NOT EXISTS idx_kb_chunks_parent   ON kb_chunks(parent_chunk_id);
CREATE INDEX IF NOT EXISTS idx_kb_chunks_type     ON kb_chunks(chunk_type);

CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_kb_chunks_content_trgm ON kb_chunks USING GIN (content gin_trgm_ops);
```

**US-001 `kb_documents` 字段修复：**
- `status` → `parse_status`（枚举 `pending | parsing | indexing | ready | failed`，对齐 proto）
- 新增 `custom_metadata JSONB DEFAULT '{}'::jsonb`

### 3.2 Entity Definitions
对齐 proto `KnowledgeBase` / `KBDocument` / `QueryRequest` / `QueryResponse` / `SourceChunk`（见 proto）。`KBChunk`（kb-service 侧只读，写入由 rag-engine 负责）：

```python
class KBChunk(BaseModel):
    id: UUID
    tenant_id: UUID
    kb_id: UUID
    doc_id: UUID
    parent_chunk_id: UUID | None
    chunk_type: Literal["child", "parent", "doc_summary"]
    content: str
    parent_content: str | None
    page_number: int | None
    content_type: str | None
    file_name: str
    token_count: int | None
    custom_metadata: dict
    created_at: datetime
```

### 3.3 Relationships
- `kb_chunks.kb_id` → `knowledge_bases.id`（软 FK，RLS 同租户）
- `kb_chunks.doc_id` → `kb_documents.id`
- `kb_chunks.parent_chunk_id` → `kb_chunks.id`（自引用）

### 3.4 Migration Plan
- US-005 迁移脚本可重复执行（`CREATE ... IF NOT EXISTS` / `CREATE EXTENSION IF NOT EXISTS`）
- 回滚：`DROP TABLE kb_chunks; DROP EXTENSION pg_trgm;`（仅 P0 阶段，无生产数据）
- kb_documents 字段重命名迁移：`ALTER TABLE kb_documents RENAME COLUMN status TO parse_status;` + `ALTER TYPE ... RENAME VALUE`（如使用枚举类型）

---

## 4. API Design

### 4.1 gRPC Endpoints（kb_service.proto，10 RPC + 3 P1 声明）

| RPC | Request | Response | idempotency_key | 说明 |
|-----|---------|----------|-----------------|------|
| CreateKB | CreateKBRequest | KnowledgeBase | required | 创建 KB + Core vector-store |
| GetKB | GetKBRequest | KnowledgeBase | — | 查询单个 |
| ListKBs | ListKBsRequest | ListKBsResponse | — | 游标分页 |
| DeleteKB | DeleteKBRequest | Empty | — | 软删 + Core delete vector-store |
| GetDocumentUploadURL | GetDocumentUploadURLRequest | GetDocumentUploadURLResponse | required | 两步式上传 step 1 |
| NotifyDocumentUploaded | NotifyDocumentUploadedRequest | AsyncTaskRef | required | 两步式上传 step 2 + outbox |
| GetDocument | GetDocumentRequest | KBDocument | — | — |
| ListDocuments | ListDocumentsRequest | ListDocumentsResponse | — | 按 parse_status 过滤 |
| DeleteDocument | DeleteDocumentRequest | Empty | — | 软删 + Core 清理向量 |
| Query | QueryRequest | QueryResponse | required | 同步问答，转发 rag-engine |
| ListKBCitations（P1） | — | — | — | UNIMPLEMENTED |
| ListKBSessions（P1） | — | — | — | UNIMPLEMENTED |
| UpdateKBPermissions（P1） | — | — | — | UNIMPLEMENTED |

### 4.2 Request/Response Schemas
见 `repo/api/proto/kb/v1/kb_service.proto`。US-001 修复点：
- `KBDocument.parse_status` 枚举 `pending | parsing | indexing | ready | failed`（proto 已正确，OpenAPI 待对齐）
- `GetDocumentUploadURLRequest` 已含 `idempotency_key`（proto 第 7 字段）
- `QueryRequest` 已含 `score_threshold` / `inference_service_name` / `idempotency_key`（proto 已正确，OpenAPI 待补齐）
- 新增 `custom_metadata`（JSONB）到 proto `GetDocumentUploadURLRequest` 与 `KBDocument`

### 4.3 Error Responses
gRPC status codes：

| gRPC Code | Condition | HTTP 映射（gateway） |
|-----------|-----------|----------------------|
| `NOT_FOUND` | KB/doc 不存在 | 404 |
| `INVALID_ARGUMENT` | 参数非法/idempotency_key 缺失 | 400 |
| `UNIMPLEMENTED` | P1 RPC | 501 |
| `INTERNAL` | 内部错误 | 500 |
| `UNAVAILABLE` | rag-engine/Core 不可用 | 503 |
| `FAILED_PRECONDITION` | KB 正在重建 | 409 kb.rebuilding |

### 4.4 Breaking Changes
US-001 为契约修复（字段重命名 `status`→`parse_status`、枚举值对齐）。proto 已是目标态；OpenAPI `services/v1.yaml` 需同步修复。由于 Services API 尚未正式发布，按 Services 受控 PR 处理，需 `make validate-services` 通过。

---

## 5. OpenAPI Change Plan (Services only)

**US-001 修复 `services/v1.yaml`：**

| Change | operationId | Compatibility |
|--------|-------------|--------------|
| `KBDocument.status` → `parse_status`，枚举 `pending/parsing/indexing/ready/failed` | — | breaking（Services 未发布，受控修复） |
| `KBDocument` 新增 `custom_metadata`(JSONB) | — | additive |
| `KBQueryRequest` 补齐 `score_threshold`/`inference_service_name`/`idempotency_key` | `queryKnowledgeBase` | additive（补齐必填/可选） |
| `uploadKnowledgeBaseDocument` 改两步式（GetDocumentUploadURL + NotifyDocumentUploaded） | `getDocumentUploadURL` / `notifyDocumentUploaded` | breaking（Services 未发布，受控修复） |

**US-002 新增 `services/v1.yaml` 端点（不写入 Core v1.yaml）：**

| Method | Path | operationId | Description | Success |
|--------|------|-------------|-------------|---------|
| POST | `/knowledge-bases/{kb_id}/documents/{doc_id}/reparse` | `reparseKnowledgeBaseDocument` | 重新解析 | 202 |
| GET | `/knowledge-bases/{kb_id}/config` | `getKnowledgeBaseConfig` | 读取 KB 配置 | 200 + KBConfig |
| PUT | `/knowledge-bases/{kb_id}/config` | `updateKnowledgeBaseConfig` | 更新 KB 配置（触发重建） | 200 + KnowledgeBase |
| POST | `/knowledge-bases/{kb_id}/rebuild` | `rebuildKnowledgeBase` | 全库重建 | 202 |
| GET | `/knowledge-bases/{kb_id}/models` | `listKnowledgeBaseModels` | 可用嵌入/推理模型 | 200 + ModelList |

**Frozen Facts Table:**

| 类别 | 内容 |
|------|------|
| Frozen Paths（Services） | `/knowledge-bases`、`/knowledge-bases/{kb_id}`、`/knowledge-bases/{kb_id}/documents`、`/knowledge-bases/{kb_id}/documents/{doc_id}`、`/knowledge-bases/{kb_id}/query`、`/knowledge-bases/{kb_id}/query/stream`、`/knowledge-bases/{kb_id}/citations`、`/knowledge-bases/{kb_id}/sessions`、`/knowledge-bases/{kb_id}/permissions` |
| Frozen Schemas | `KnowledgeBase`、`KBDocument`、`KBQueryRequest`、`KBQueryResponse`、`ErrorResponse` |
| Frozen Response/Error codes | 404 NotFound、400 BadRequest、401 Unauthorized、403 Forbidden、409 kb.rebuilding、422 doc.unsupported_type、413 doc.too_large、422 doc.checksum_mismatch、200 doc.parse_failed、503 inference.unavailable |
| Non-Frozen（待补） | `KBConfig`、`ModelList`、reparse/rebuild request schema（US-002 新增，本 SPEC 冻结为待补，实现时按 US-002 AC 写入 v1.yaml） |
| Known Risky Assumptions | 两步式上传改造为 breaking change；reparse/rebuild 触发逻辑依赖 rag-engine 重建能力（rag-engine SPEC 覆盖） |

---

## 6. Business Logic

### 6.1 Core Algorithms

**CreateKB：**
```
1. 校验 idempotency_key（缺失 → INVALID_ARGUMENT）
2. 查 async_tasks.idempotency_key 命中 → 返回已有结果（幂等）
3. INSERT knowledge_bases (id, tenant_id, name, ..., status=active)
4. Core POST /vector-stores {name: kb_{id 去横杠}, metric: COSINE, dim: <embedding_dim>}
5. 写 async_tasks(idempotency_key, result=kb)
6. 返回 KnowledgeBase
```

**NotifyDocumentUploaded（同事务 outbox）：**
```
1. 校验 idempotency_key
2. Core HEAD object 验证存在 + 校验 checksum_sha256（不符 → FAILED_PRECONDITION doc.checksum_mismatch）
3. BEGIN TX:
     UPDATE kb_documents SET parse_status='pending', updated_at=now WHERE id=doc_id
     INSERT async_tasks(id, idempotency_key, type='kb.parse', payload={doc_id,kb_id})
     INSERT outbox_events(id, aggregate='kb_documents', aggregate_id=doc_id, type='kb.parse', payload={doc_id,kb_id,storage_path})
   COMMIT
4. 返回 AsyncTaskRef { task_id, status='pending' }
```

**outbox dispatcher（独立协程）：**
```
loop:
  rows = SELECT * FROM outbox_events WHERE dispatched_at IS NULL ORDER BY created_at LIMIT 100
  for r in rows:
    nats.publish('ani.tasks.kb.parse', json(r.payload))
    UPDATE outbox_events SET dispatched_at=now WHERE id=r.id
  sleep(1s)
```

**Query（同步）：**
```
1. 校验 idempotency_key
2. session_id 空 → 生成 UUID
3. INSERT kb_messages(tenant_id, kb_id, session_id, role='user', content=question)
4. Redis: RPUSH ani:prod:session:kb:{session_id} <user_msg>; EXPIRE 24h; LTRIM 0 19
5. resp = rag_engine.Query(gRPC)
6. INSERT kb_messages(role='assistant', content=resp.answer, sources=resp.sources)
7. Redis: RPUSH <assistant_msg>; LTRIM 20
8. 返回 resp
```

**DeleteDocument：**
```
1. UPDATE kb_documents SET parse_status='deleted' WHERE id=doc_id（软删）
2. Core DELETE /vector-stores/{kb_{id}}/documents?filter=doc_id=="{doc_id}" （best-effort，失败 warning）
```

### 6.2 Validation Rules
- `idempotency_key`：CreateKB/NotifyDocumentUploaded/Query 必填，格式 UUID
- `file_type`：枚举 `pdf | docx | xlsx | pptx | md | txt`（US-001 补 pptx）
- `file_size_bytes`：≤ 100MB（UX §8.4 假设），超限 → 413 doc.too_large
- `question`：1-2000 字符
- `top_k`：1-20
- `score_threshold`：0.0-1.0

### 6.3 State Machine
**KB 状态：** `active → rebuilding → active`（rebuild 期间写操作 409 kb.rebuilding）
**文档 parse_status：** `pending → parsing → indexing → ready | failed`（failed 可 reparse 回 pending）

### 6.4 Edge Cases
- idempotency_key 重放：返回首次结果，不重复副作用
- KB rebuilding 期间上传/删除/重试：返回 409
- Core/object-store 不可用：503，不写 outbox（避免派发无效任务）
- rag-engine Query 超时：gRPC deadline，返回 503

---

## 7. Error Handling

### 7.1 Error Taxonomy
| Error Code | gRPC / HTTP | Condition | User Message |
|------------|-------------|-----------|--------------|
| `INVALID_ARGUMENT` | INVALID_ARGUMENT / 400 | 参数缺失/非法 | — |
| `NOT_FOUND` | NOT_FOUND / 404 | KB/doc 不存在 | — |
| `FAILED_PRECONDITION` | FAILED_PRECONDITION / 409 | KB rebuilding | 全库重建进行中 |
| `FAILED_PRECONDITION` | / 422 | doc.unsupported_type | 不支持的文档格式 |
| `RESOURCE_EXHAUSTED` | / 413 | doc.too_large | 文件大小超限 |
| `FAILED_PRECONDITION` | / 422 | doc.checksum_mismatch | 校验和不匹配 |
| `UNAVAILABLE` | UNAVAILABLE / 503 | Core/rag-engine 不可用 | 服务暂不可用 |

### 7.2 Retry Strategy
- idempotency_key 保证客户端可安全重试 CreateKB/NotifyDocumentUploaded/Query
- outbox 派发失败：下次轮询重试（at-least-once，rag-engine 需幂等）
- Core API 调用失败：不自动重试（返回错误给上游）

### 7.3 Failure Modes
- DB 不可用：gRPC INTERNAL
- NATS 不可用：outbox 堆积，不丢（持久化在 DB），恢复后继续派发
- Redis 不可用：Query 降级为仅写 DB（会话缓存 best-effort）

---

## 8. Security

### 8.1 Authentication & Authorization
- P0 无 KB 级权限校验（PRD Non-Goals，仅 proto/OpenAPI 声明 + 前端占位）
- 租户隔离：所有 repository 查询带 `tenant_id = current_tenant_id`（RLS）
- 服务间：kb-service 以服务账号调 Core API（RBAC scope:vector-stores:write/read）

### 8.2 Input Validation
- proto 字段校验 + Pydantic 二次校验
- `custom_metadata` JSONB 深度/大小限制（≤ 64KB）
- `filter` 透传 Core 由 Milvus 校验

### 8.3 Data Protection
- presigned URL 有效期 15 分钟（proto 注释）
- checksum_sha256 客户端计算，服务端校验
- 会话内容 Redis 存储，TTL 24h 自动清理

---

## 9. Performance

### 9.1 Expected Load
- KB CRUD：低频
- 文档上传：中频（每 KB 数十至数百文档）
- Query：中高频（取决于问答活跃度）

### 9.2 Optimization Strategy
- outbox 批量派发（100/批）
- Redis 会话缓存避免每次 Query 查 DB 历史
- List 端点游标分页

### 9.3 Database Considerations
- kb_chunks 索引覆盖 US-005（kb_doc/parent/type/trgm）
- Query 历史读 kb_messages 按 session_id 索引（已存在）

---

## 10. Testing Strategy

### 10.1 Unit Tests
- repositories：CRUD + RLS 过滤验证
- core_api client：mock httpx，断言请求路径/参数
- outbox dispatcher：mock NATS，断言发布 + 标记
- session cache：Redis mock，LTRIM/TTL 验证

### 10.2 Integration Tests
- CreateKB → Core vector-store 被调用
- 两步式上传完整链路（mock Core objects）
- NotifyDocumentUploaded → outbox → NATS（embedded NATS）
- Query → rag-engine mock → Redis 会话写入
- DeleteDocument → Core DELETE documents 被调用

### 10.3 Edge Case Tests
- idempotency_key 重放返回相同结果
- KB rebuilding 返回 409
- checksum mismatch 返回 422
- 文件超限返回 413

### 10.4 Acceptance Criteria Mapping
| US/FR | Test | Type | Description |
|-------|------|------|-------------|
| US-001 AC1 | test_openapi_field_alignment | contract | parse_status 枚举对齐 proto |
| US-001 AC2 | test_two_step_upload_contract | contract | 两步式端点存在 |
| US-001 AC3 | test_query_request_fields | contract | score_threshold/inference_service_name/idempotency_key |
| US-001 AC6 | make validate-services | gate | — |
| US-002 AC1-5 | test_new_endpoints | contract | reparse/config/rebuild/models 端点 |
| US-004 | test_proto_ocr_capability | contract | model proto ocr 注释 |
| US-005 AC1-5 | test_migrations | migration | kb_chunks + pg_trgm + 索引 + idempotent |
| US-007 AC1-4 | make validate-sdk-beta / validate-spec-split | gate | SDK 无漂移 |
| US-008 AC1-5 | test_grpc_server_10_rpcs | integration | 10 RPC + 3 P1 UNIMPLEMENTED |
| US-009 AC1-6 | test_repositories_and_clients | integration | repositories + Core/rag client |
| US-010 AC1-4 | test_outbox_and_session | integration | outbox 派发 + Redis 会话 |
| FR-1 | test_kb_crud | integration | 10 RPC 承接 |
| FR-2 | test_two_step_upload | integration | 两步式 |
| FR-3 | test_async_parse_chain | integration | outbox→NATS |
| FR-9 | test_session_cache | integration | Redis TTL/LTRIM |
| FR-14 | test_custom_metadata_inheritance | integration | JSONB 全链路 |
| FR-15 | test_rls_isolation | integration | 跨租户不可见 |

---

## 11. Implementation Plan

### 11.1 Phases
1. **契约层（US-001,002,004）**：改 proto + services/v1.yaml + model proto ocr；`make validate-services`
2. **迁移（US-005）**：kb_chunks + pg_trgm；`make test`
3. **SDK 重生成（US-007）**：基于 A1/A2/A3/A4 变更；`make validate-sdk-beta` / `validate-spec-split`
4. **骨架（US-008）**：kb-service 目录 + gRPC server 10 RPC + 3 P1 UNIMPLEMENTED
5. **数据层（US-009）**：repositories + Core API client + rag-engine client
6. **outbox + 会话（US-010）**：dispatcher + Redis cache

### 11.2 Issue Mapping
| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| US-001 | 4, 5 | high | — |
| US-002 | 4, 5 | high | US-001 |
| US-004 | 4 | medium | — |
| US-005 | 3 | high | — |
| US-007 | 11.1 | high | US-001,002,003,004 |
| US-008 | 2, 4 | high | US-001,005 |
| US-009 | 2, 6 | high | US-008 |
| US-010 | 2, 6 | high | US-009 |

### 11.3 Incremental Delivery
契约层先行（解锁前后端并行）；骨架可独立启动；outbox/会话依赖数据层。

---

## 12. Open Questions & Risks

### 12.1 Unresolved Questions
- `doc_count` 是否拆分「可检索文档数」与「总文档数」（PRD Open Question，P0 维持单一 doc_count）
- 全库重建期间拒绝写入的具体边界（PRD Open Question，P0：拒绝上传/删除/重试，允许查询）
- US-002 新增端点的 request/response schema 细节（本 SPEC 标记待补，实现时按 AC 写入 v1.yaml）

### 12.2 Technical Risks
| Risk | Impact | Mitigation |
|------|--------|-----------|
| Services OpenAPI breaking change（status→parse_status） | medium | Services 未正式发布，受控 PR + CODEOWNERS 共同审查 |
| outbox at-least-once 导致 rag-engine 重复处理 | medium | rag-engine 解析需按 doc_id 幂等（rag-engine SPEC 覆盖） |
| Core API 文档级 DELETE 不可用 | high | 依赖 spec-core-kb-vector-delete.md 先行落地 |

### 12.3 Assumptions
- 现有 4 张 KB 表 + async_tasks + outbox_events 已迁移就绪（PRD §7 依赖就绪状态）
- Core VectorStore API（集合级）Ready、Core ObjectStore API Ready、NATS Ready、PostgreSQL/Redis/MinIO Ready
- model-service ListModels Ready（无 capability 过滤）
- embedding_dim 可从 model-service 获取（CreateKB 时确定向量维度）
