# [契约] Core API 扩展预计算向量与 content 字段 (步骤 1 契约)

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX: N/A — backend-only
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` (§1.4, 步骤 1)

## Description
作为 Core 层开发者，我需要先修改 Core API 契约：在向量存储 API 的 port struct 和 HTTP schema 中新增 `Vector` 和 `content` 字段定义，并通过生成脚本派生 v1.yaml 等文件。**本 issue 仅定义契约，不含任何实现代码。**

## Scope
- Product line: core (Core API / ani-gateway)
- Code paths allowed: `repo/pkg/ports/vector_store.go` (struct 定义), `repo/services/ani-gateway/internal/router/vector_store_resources.go` (HTTP body/response schema 定义), v1.yaml 生成脚本派生文件

## Acceptance Criteria
- [ ] [Plan §1.4-1] `pkg/ports/vector_store.go` — `VectorDocumentInput` 新增 `Vector []float32` 字段（`omitempty` 语义，为空时 Core 用伪向量，仅定义）
- [ ] [Plan §1.4-3] `pkg/ports/vector_store.go` — `VectorSearchHit` 新增 `Content string` 字段（仅定义）
- [ ] [Plan §1.4-2] `vector_store_resources.go` — `vectorDocumentInputBody` 新增 `vector []float32` JSON 字段（`json:"vector,omitempty"`，仅 schema 定义）
- [ ] [Plan §1.4-3] `vector_store_resources.go` — `vectorSearchHitResponse` 新增 `content string` JSON 字段（`json:"content"`，仅 schema 定义）
- [ ] [兼容性] `Vector` 和 `vector` 字段均为 `omitempty`，旧调用方不传时行为不变；`content` 为新增返回字段，旧调用方忽略
- [ ] v1.yaml 通过生成脚本派生更新（契约生成物）
- [ ] `make validate-architecture` 通过
- [ ] 不包含任何实现逻辑（`InsertDocuments` 优先用 vector 的逻辑、`searchVectorStore` 提取 content 的逻辑在功能 issue 中完成）

## Dependencies
None — 改造契约起点。

## Type
core (contract)

## Priority
high

## Labels
core, contract

## Batch
RAG-REFACTOR-STEP-1-CONTRACT

## References
- Plan: §1.4 (Core API 必须修改 3 项)
- CLAUDE.md §4.1 (先改 API 契约再写实现), §3.1 (Core 不含模型推理), §5.3 (Services 不直接依赖底层 SDK)
