# [契约] kb-service 新增 Retrieve RPC proto 定义 (步骤 10 契约)

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX: N/A — backend-only
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` (§10.1, 步骤 10)

## Description
作为 Services 层开发者，我需要先修改 kb-service gRPC 契约：在 `kb_service.proto` 中新增 `Retrieve` RPC（server streaming）定义和 `RetrieveRequest`/`RetrieveEvent` message 定义，并运行 `make gen-proto` 生成 stub。**本 issue 仅定义 proto 契约和生成 stub，不含任何 RPC 实现代码。**

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/proto/kb_service.proto` (proto 定义), `make gen-proto` 生成物

## Acceptance Criteria
- [ ] [Plan §10.1] `kb_service.proto` 新增 `Retrieve` RPC 定义：`rpc Retrieve(RetrieveRequest) returns (stream RetrieveEvent)`
- [ ] [Plan §10.1] `RetrieveRequest` message 定义（字段同 QueryRequest）
- [ ] [Plan §10.1] `RetrieveEvent` message 定义，使用 oneof：
  - `RetrieveTokenEvent token = 1`（`content` 字段）
  - `RetrieveSourcesEvent sources = 2`（`repeated SourceChunk sources`）
  - `RetrieveDoneEvent done = 3`（`input_tokens`、`output_tokens`、`session_id`）
  - `RetrieveErrorEvent error = 4`（`message` 字段）
- [ ] 运行 `make gen-proto` 重新生成 Go/Python stub
- [ ] 不包含任何 RPC 实现代码（`grpc_server.py` 中的 Retrieve 实现在功能 issue 中完成）
- [ ] 不包含 Gateway 改线代码（在功能 issue 中完成）

## Dependencies
None — 独立契约，可提前定义。

## Type
core (contract)

## Priority
high

## Labels
core, contract

## Batch
RAG-REFACTOR-STEP-10-CONTRACT

## References
- Plan: §10.1 (proto 设计)
- CLAUDE.md §4.1 (先改 API 契约再写实现)
