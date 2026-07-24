# SPEC: Core 向量文档级删除端点 (P0)

> Technical specification derived from:
> - PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-003)
> - UX: N/A — backend-only
> Generated: 2026-07-23 | Target branch: main | Product line: core

## 1. Summary

### 1.1 What This SPEC Covers
为 Core API 新增「按 filter 删除向量文档」端点 `DELETE /vector-stores/{vector_store_id}/documents`，支撑 kb-service 在知识库文档删除时清理 Milvus 中该文档的所有向量。仅 Core handler 与 Core OpenAPI 契约变更，不涉及业务逻辑。

### 1.2 PRD Reference
- Source: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX source: N/A — backend-only
- User Stories covered: US-003
- Functional Requirements covered: FR-13

### 1.3 Design Decisions Summary
| Decision | Choice | Rationale |
|----------|--------|-----------|
| HTTP method | `DELETE` | 语义为删除，与现有 `/vector-stores/{id}` 集合级 DELETE 对齐 |
| 过滤机制 | `filter` 查询参数（Milvus boolean expression 字符串） | Milvus 原生 filter 表达式，直接透传，避免 Core 解析业务字段；kb-service 以 `doc_id == "..."` 形式调用 |
| 响应 | `200 + VectorStoreDocumentDeleteResponse`（含 deleted_count） | 与现有 `insertVectorStoreDocuments` 的 202 语义区分：删除是同步立即完成 |
| 幂等性 | DELETE 天然幂等；不引入 `idempotency_key` | 重复删除同一 filter 不产生副作用；遵循 Core API 「有副作用的 PUT/PATCH/DELETE 视语义决定」约定，集合级 DELETE 未强制 idempotency_key |
| RBAC scope | `scope:vector-stores:write` | 与 `insertVectorStoreDocuments` 共享 write scope，文档级写入/删除属同一能力面 |
| 路径参数名 | `vector_store_id` | 与现有 `/vector-stores/{vector_store_id}` 路径参数命名一致 |

---

## 2. Architecture

### 2.1 System Context
本端点位于 Core API vector-store 能力域。kb-service `DeleteDocument` RPC 在软删 `kb_documents` 记录后，调用此端点清理 Milvus 集合 `kb_{kb_id}` 中 `doc_id == "{doc_id}"` 的所有向量（子块 + 文档摘要）。

```
kb-service DeleteDocument RPC
   │
   ▼
DELETE /api/v1/vector-stores/{vector_store_id}/documents?filter=doc_id=="..."
   │ (Core API, RBAC: scope:vector-stores:write)
   ▼
Core vector-store handler → Milvus delete by expr
```

### 2.2 Component Design
- **VectorStore document delete handler**（新增）：接收 `vector_store_id` + `filter`，调用 Milvus adapter 按表达式删除文档向量，返回删除数量。
- 复用现有 vector-store port/adapter，不新增 port。

### 2.3 Module Interactions
1. kb-service 软删 `kb_documents` 记录（status=deleted）
2. kb-service Core API client 调用 `DELETE /vector-stores/{vector_store_id}/documents?filter=...`
3. Core handler → Milvus adapter `delete(expr=filter, collection_name=kb_{id})`
4. Core 返回 `deleted_count`
5. kb-service 记录清理结果（best-effort，失败记录 warning 不阻断删除）

### 2.4 File Structure
```
repo/api/openapi/v1.yaml                          [MODIFY: 新增 DELETE documents 端点 + schema]
repo/internal/.../vectorstore/handler.go          [MODIFY: 新增 DeleteDocuments handler]
repo/internal/.../vectorstore/handler_test.go     [NEW 或 MODIFY: 测试]
repo/pkg/adapters/.../milvus/...                 [MODIFY: 若无 delete by expr 方法则新增]
```
> 具体路径以 `repo/services/tasks/execution/CORE-HANDLER-IMPLEMENTATION-GUIDE.md` 为准，实现时定位现有 vector-store handler 与 Milvus adapter。

---

## 3. Data Model

### 3.1 Schema Changes
无 Core 数据库 schema 变更。Milvus 集合 `kb_{kb_id}` 中按 `doc_id` 元数据字段过滤删除。

### 3.2 Entity Definitions
新增 OpenAPI schema `VectorStoreDocumentDeleteRequest`（query 参数，无 body）与 `VectorStoreDocumentDeleteResponse`。

### 3.3 Relationships
N/A — 无新增实体。

### 3.4 Migration Plan
无 DB migration。Core SDK 需重新生成（见 spec-services-kb-service.md US-007）。

---

## 4. API Design

### 4.1 Endpoints

| Method | Path | operationId | Description | Auth | Request | Response |
|--------|------|-------------|-------------|------|---------|----------|
| DELETE | `/api/v1/vector-stores/{vector_store_id}/documents` | `deleteVectorStoreDocuments` | 按 filter 删除向量文档 | RBAC `scope:vector-stores:write` | `filter` query param | `200 + VectorStoreDocumentDeleteResponse` |

### 4.2 Request/Response Schemas

**请求（路径 + 查询参数，无 body）：**

| 参数 | 位置 | 必填 | 类型 | 约束 | 说明 |
|------|------|------|------|------|------|
| `vector_store_id` | path | yes | string | — | 向量存储标识（kb-service 传入 `kb_{kb_id 去横杠}`） |
| `filter` | query | yes | string | minLength 1, maxLength 512 | Milvus boolean expression，如 `doc_id == "abc"` |

**响应 `VectorStoreDocumentDeleteResponse`：**

```yaml
VectorStoreDocumentDeleteResponse:
  type: object
  required: [deleted_count]
  properties:
    deleted_count:
      type: integer
      minimum: 0
      description: 实际删除的向量数（best-effort，Milvus 可能不精确）
```

### 4.3 Error Responses

| HTTP | code | Condition | User Message |
|------|------|-----------|--------------|
| 400 | `INVALID_FILTER` | filter 为空 / 非法表达式 | filter 表达式非法 |
| 401 | `UNAUTHORIZED` | 未认证 | — |
| 403 | `FORBIDDEN` | 无 `scope:vector-stores:write` | — |
| 404 | `VECTOR_STORE_NOT_FOUND` | vector_store_id 不存在 | 向量存储不存在 |
| 422 | `PRECONDITION_FAILED` | Milvus 集合未就绪 | 向量存储未就绪 |

### 4.4 Breaking Changes
无。新增端点 + 新增可选 response schema，符合 Core API v1 兼容性规则（新增端点/字段不属于破坏性变更）。

---

## 5. OpenAPI Change Plan (Core only)

| Change | operationId | Compatibility | idempotency_key |
|--------|-------------|---------------|-----------------|
| 新增 `DELETE /vector-stores/{vector_store_id}/documents` | `deleteVectorStoreDocuments` | additive（新端点） | 不要求（DELETE 天然幂等） |
| 新增 schema `VectorStoreDocumentDeleteResponse` | — | additive | — |

---

## 6. Business Logic

### 6.1 Core Algorithm
```
1. 校验 RBAC scope:vector-stores:write
2. 解析 vector_store_id（路径）、filter（查询）
3. 若 filter 为空 → 400 INVALID_FILTER
4. 调用 Milvus adapter: collection=vector_store_id, delete(expr=filter)
5. 返回 200 + { deleted_count: <result> }
```

### 6.2 Validation Rules
- `filter` 必填，非空
- `vector_store_id` 必填
- 不解析 filter 内容（透传 Milvus），由 Milvus 校验表达式合法性，非法 → 422

### 6.3 Edge Cases
- 集合不存在 → 404
- filter 匹配 0 条 → 200, deleted_count=0（幂等）
- Milvus 不可用 → 503（沿用现有 vector-store 错误处理）

---

## 7. Error Handling

### 7.1 Error Taxonomy
| Error Code | HTTP Status | Condition | User Message |
|------------|-------------|-----------|--------------|
| `INVALID_FILTER` | 400 | filter 缺失/空 | filter 表达式不能为空 |
| `VECTOR_STORE_NOT_FOUND` | 404 | 集合不存在 | 向量存储不存在 |
| `PRECONDITION_FAILED` | 422 | Milvus 表达式非法/集合未就绪 | — |
| `UNAVAILABLE` | 503 | Milvus 不可用 | 向量存储服务暂不可用 |

### 7.2 Retry Strategy
- kb-service 调用此端点失败时记录 warning，不阻断文档删除（文档元数据已软删，向量清理为 best-effort）
- 不引入自动重试（避免分布式事务复杂度）

### 7.3 Failure Modes
Milvus 不可用时返回 503；kb-service 侧降级处理（见 spec-services-kb-service.md）。

---

## 8. Security

### 8.1 Authentication & Authorization
- 认证：现有 Core API JWT 认证
- 授权：RBAC `scope:vector-stores:write`（与 `insertVectorStoreDocuments` 共享）
- 租户隔离：kb-service 以服务账号调用，filter 由 kb-service 构造（`doc_id == "..."`），不含跨租户字段

### 8.2 Input Validation
- `filter` maxLength 512 防止超长表达式
- 透传 Milvus，依赖 Milvus 表达式解析器防御注入（Milvus expr 不支持任意 SQL）

### 8.3 Data Protection
仅删除操作，无敏感数据返回。

---

## 9. Performance

### 9.1 Expected Load
- 频次：低（文档删除事件驱动）
- 单次删除量：单文档的子块数（数十至数百向量）

### 9.2 Optimization Strategy
- Milvus delete by expr 为批量操作，无需额外批处理
- deleted_count 为 best-effort，不强求精确

### 9.3 Database Considerations
无 Core DB 操作；Milvus 索引不受删除影响。

---

## 10. Testing Strategy

### 10.1 Unit Tests
- handler：filter 空 → 400；vector_store_id 缺失 → 400；正常 → 200 + deleted_count
- Milvus adapter：delete by expr 调用参数正确

### 10.2 Integration Tests
- 端到端：插入若干向量 → DELETE filter → 确认集合中无匹配向量
- 幂等：重复 DELETE 同 filter → 200, deleted_count=0

### 10.3 Edge Case Tests
- 集合不存在 → 404
- filter 匹配 0 条 → 200
- Milvus 断开 → 503

### 10.4 Acceptance Criteria Mapping
| US/FR | Test | Type | Description |
|-------|------|------|-------------|
| US-003 AC1 | test_delete_vector_documents | integration | DELETE /vector-stores/{id}/documents?filter=... 删除指定 doc 向量 |
| US-003 AC2 | test_openapi_has_endpoint | contract | v1.yaml 含 deleteVectorStoreDocuments |
| US-003 AC4 | make validate-architecture | gate | 架构校验通过 |
| US-003 AC5 | make test | gate | 测试全绿 |
| FR-13 | test_kb_delete_cleans_vectors | integration | kb-service 删文档 → Core 清理向量 |

---

## 11. Implementation Plan

### 11.1 Phases
1. 改 `repo/api/openapi/v1.yaml` 新增端点 + schema
2. 实现 Core handler + Milvus adapter delete by expr
3. 单元 + 集成测试
4. `make validate-architecture` + `make test`
5. 重新生成 Core SDK（与 spec-services-kb-service.md US-007 协同）

### 11.2 Issue Mapping
| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| US-003 | 4, 5, 6, 10 | high | — |

### 11.3 Incremental Delivery
端点独立可上线，kb-service 未集成前可被 mock/测试调用。

---

## 12. Open Questions & Risks

### 12.1 Unresolved Questions
- `deleted_count` 是否需要精确（Milvus delete 返回值语义待实现时确认）

### 12.2 Technical Risks
| Risk | Impact | Mitigation |
|------|--------|-----------|
| Milvus delete by expr 性能在大集合下退化 | low | 单文档向量量级小，无风险 |
| filter 透传安全性 | low | Milvus expr 不支持任意 SQL，且由 kb-service 构造 |

### 12.3 Assumptions
- Milvus adapter 已具备或可低成本新增 delete by expression 能力
- 现有 vector-store RBAC scope 体系可复用 write scope
