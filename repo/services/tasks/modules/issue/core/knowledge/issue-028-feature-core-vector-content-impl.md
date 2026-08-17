# [功能] Core API 预计算向量与 content 实现 (步骤 1 功能)

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX: N/A — backend-only
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` (步骤 1)

## Description
作为 Core 层开发者，我需要完成 Core API 预计算向量存储和 content 返回的实现逻辑：`InsertDocuments` 优先使用传入向量、`SearchVectorStore` 返回 content，跑通全部测试。Core 不做 embedding 推理（遵守 CLAUDE.md §3.1）。

## Scope
- Product line: core (Core API / ani-gateway)
- Code paths allowed: `repo/pkg/adapters/runtime/vector_store_service.go`, `repo/services/ani-gateway/internal/router/vector_store_resources.go` (handler 逻辑)

## Acceptance Criteria
- [ ] [Plan 步骤1.1] `LocalVectorStoreService.InsertDocuments` 实现逻辑：`len(doc.Vector) > 0` 时用 `doc.Vector`（调用方预计算），否则用 `localDocumentVector` 伪向量
- [ ] [Plan §1.4-3] `searchVectorStore` handler 从 `result` 提取 `Content` 填入 `vectorSearchHitResponse`
- [ ] [兼容性] 不传 vector 时行为与旧调用方完全一致
- [ ] `go test ./pkg/adapters/runtime/... -run TestVectorStore` 通过
- [ ] 现有 Gateway vector store 测试全通过（不传 vector 时行为不变）
- [ ] 新增测试：传入预计算 vector 时正确存储；search 返回 content 字段

## Dependencies
#024 (Core 契约) — 实现依赖契约定义的 struct/schema。

## Type
core (feature)

## Priority
high

## Labels
core, feature

## Batch
RAG-REFACTOR-STEP-1-FEATURE

## References
- Plan: 步骤 1.1 (InsertDocuments 逻辑), §1.4-3 (searchVectorStore 返回 content)
