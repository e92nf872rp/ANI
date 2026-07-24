# Core 新增向量文档级删除端点

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-003)
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-core-kb-vector-delete.md` (§4, §5, §6)

## Description
作为平台开发者，我需要 Core 提供按 doc_id 过滤的向量文档删除能力，支撑知识库文档删除时清理 Milvus 向量。

## Scope
- Product line: core
- Code paths allowed: `repo/api/openapi/v1.yaml`, `repo/internal/.../vectorstore/`, `repo/pkg/adapters/.../milvus/` only

## Acceptance Criteria
- [ ] Core OpenAPI 新增 `DELETE /vector-stores/{id}/documents?filter=...`（按 filter 删除文档向量）
- [ ] 端点写入 `repo/api/openapi/v1.yaml`
- [ ] Core handler 实现该端点（RBAC `scope:vector-stores:write`）
- [ ] [SPEC] 实现 `deleteVectorStoreDocuments`：filter 空 → 400 `INVALID_FILTER`；集合不存在 → 404；Milvus delete by expr 调用正确（SPEC §6.1）
- [ ] [SPEC] 响应 `200 + VectorStoreDocumentDeleteResponse{deleted_count}`（SPEC §4.2）
- [ ] [SPEC] 错误码对齐：404 `VECTOR_STORE_NOT_FOUND` / 422 `PRECONDITION_FAILED` / 503 `UNAVAILABLE`（SPEC §4.3）
- [ ] `make validate-architecture` 通过
- [ ] `make test` 通过

## Dependencies
None — Core-only endpoint, can run in parallel with #1.

## Type
core

## Priority
high

## Labels
core

## Batch
M2.1-TASK-A

## References
- SPEC: spec-core-kb-vector-delete §4, §5, §6
