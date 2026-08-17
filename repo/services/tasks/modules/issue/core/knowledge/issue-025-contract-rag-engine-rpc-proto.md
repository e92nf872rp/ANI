# [契约] rag-engine 新增 Parse/Embed/Generate/GenerateStream RPC proto 定义 (步骤 2 契约)

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX: N/A — backend-only
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` (§2.1, 步骤 2)

## Description
作为 AI 层开发者，我需要先修改 rag-engine gRPC 契约：在 `rag.proto` 中新增 Parse/Embed/Generate/GenerateStream 四个 RPC 定义和所有 message 定义，并运行 `make gen-proto` 生成 Python stub。**本 issue 仅定义 proto 契约和生成 stub，不含任何 RPC 实现代码。**

## Scope
- Product line: core (Services / rag-engine)
- Code paths allowed: `repo/ai/rag-engine/app/grpc/rag.proto` (proto 定义), `make gen-proto` 生成物

## Acceptance Criteria
- [ ] [Plan §2.1] `rag.proto` 新增 `Parse` RPC 定义：`rpc Parse(ParseRequest) returns (ParseResponse)`
- [ ] [Plan §2.1] `rag.proto` 新增 `Embed` RPC 定义：`rpc Embed(EmbedRequest) returns (EmbedResponse)`
- [ ] [Plan §2.1] `rag.proto` 新增 `Generate` RPC 定义：`rpc Generate(GenerateRequest) returns (GenerateResponse)`
- [ ] [Plan §2.1] `rag.proto` 新增 `GenerateStream` RPC 定义：`rpc GenerateStream(GenerateRequest) returns (stream GenerateToken)`
- [ ] [Plan §2.1] `ParseRequest` message：`download_url`、`file_name`、`file_type`、`chunk_size` 字段
- [ ] [Plan §2.1] `ParsedChunk` message：`chunk_id`、`content`、`content_type`、`page_number`、`parent_content`、`parent_chunk_id`、`chunk_type`、`metadata_json`、`image_bytes`、`image_format` 字段
- [ ] [Plan §2.1] `ParseResponse` message：`repeated ParsedChunk chunks`
- [ ] [Plan §2.1] `EmbedRequest` message：`repeated string texts`
- [ ] [Plan §2.1] `EmbedResponse` message：`repeated float vectors_flat`（一维展平数组）+ `int32 dimension` + `int32 count`
- [ ] [Plan §2.1] `GenerateRequest` message：`question`、`session_id`、`repeated SourceChunk context`、`inference_service_name`、`max_tokens`、`repeated ChatMessage history`
- [ ] [Plan §2.1] `ChatMessage` message：`role`、`content` 字段
- [ ] [Plan §2.1] `GenerateResponse` message：`answer`、`input_tokens`、`output_tokens`、`session_id`
- [ ] [Plan §2.1] `GenerateToken` message：`content`、`done`、`input_tokens`、`output_tokens`
- [ ] [Plan §2.1] `SourceChunk` 新增 `chunk_id` 字段（向后兼容，旧 Query RPC 不受影响）
- [ ] [Plan §2.5] 旧 `Query` RPC 定义保留不变（标注 deprecated 注释）
- [ ] 运行 `make gen-proto` 重新生成 Python stub（`rag_pb2.py` / `rag_pb2_grpc.py`）
- [ ] 不包含任何 RPC 实现代码（`server.py` 中的 RPC 实现在功能 issue 中完成）

## Dependencies
None — 与 #024 可并行。

## Type
core (contract)

## Priority
high

## Labels
core, contract

## Batch
RAG-REFACTOR-STEP-2-CONTRACT

## References
- Plan: §2.1 (Proto 设计), §2.5 (旧 Query 保留)
- CLAUDE.md §4.1 (proto 先行)
