# B2 契约层：chunks / sessions / messages / citations 契约 + proto + 生成物

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`

## Description
作为 Services 契约维护者，我需要为 KB-API-B2 批次（#11 分块明细、#16 会话列表、#17 会话消息、#18 会话删除、#15 引用溯源增强）补齐 OpenAPI 契约与 gRPC proto。citations/sessions 两个 operation 契约已存在，本批次新增 4 个 operation + 5 个 schema + KBCitation 增强字段。

## Scope
- Product line: core (Services 契约层)
- Code paths allowed: `repo/api/openapi/services/v1.yaml`、`repo/api/proto/kb/v1/kb_service.proto`、生成物目录（仅生成）
- 禁止手改生成物；禁止触碰 Core API `repo/api/openapi/v1.yaml`

## Acceptance Criteria
- [ ] [SPEC §4.3 #11] 新 path `GET /knowledge-bases/{kb_id}/documents/{doc_id}/chunks`，operationId=`listKnowledgeBaseDocumentChunks`，200 `KBChunkListResponse`；query: limit（default 50，1–100）/cursor/chunk_type（enum child|parent|doc_summary）
- [ ] [SPEC §4.3 #17] 新 path `GET /knowledge-bases/{kb_id}/sessions/{session_id}/messages`，operationId=`listKnowledgeBaseSessionMessages`，200 `KBSessionMessageListResponse`；query: limit（default 100，max 100）/cursor
- [ ] [SPEC §4.3 #18] 新 path `DELETE /knowledge-bases/{kb_id}/sessions/{session_id}`，operationId=`deleteKnowledgeBaseSession`，204（幂等）
- [ ] [SPEC §3.2] 新 schema：`KBChunk`（id/doc_id/kb_id/parent_chunk_id nullable/chunk_type enum/content/parent_content nullable/page_number nullable int/content_type/token_count/file_name/custom_metadata object nullable/created_at）、`KBChunkListResponse`、`KBSessionMessage`（id/session_id/role enum user|assistant/content/sources array of KBSourceChunk nullable/input_tokens/output_tokens/duration_ms nullable/created_at）、`KBSessionMessageListResponse`、`KBSourceChunk`（doc_id/file_name/page nullable/content/score nullable）——字段与 `kb_chunks`/`kb_messages` 真实列一一对应（SPEC §3.2 映射表）
- [ ] [SPEC §4.3 #15] 既有 `KBCitation`（v1.yaml:837–848）追加 `message_id`/`session_id`（string, format uuid, nullable）——该接口从未发布（一直 501），无兼容负担
- [ ] [SPEC §4.2] 3 个新 operationId 同 PR 登记 `services-contract-baseline.yaml` 的 `operation_security` 豁免
- [ ] [SPEC §2.4] proto 新增：`ListDocumentChunks`/`GetSessionMessages`/`DeleteSession` RPC（ListKBCitations/ListKBSessions 已声明 L48/L50，KBCitation message L251 追加 field 9/10）；请求复用 `common.v1.CursorPageRequest`（common.proto:18）
- [ ] [SPEC §4.2] buf 生成两侧 pb + SDK/docs/schema.d.ts 生成物同步（禁手改）
- [ ] **范围纪律**：不回溯改造 `KBQueryResponse`（sources 保持内联对象）
- [ ] `make validate-services` 通过（零漂移）

## Dependencies
None（与 B1/B3 批次相互独立，可并行）

## Type
core (contract)

## Priority
high

## Labels
core, contract, openapi, proto

## Batch
KB-API-B2

## References
- SPEC: §3.2 REST↔DB 列映射、§4.1 Frozen/Non-Frozen（5 个 operation 现状）、§4.2/§4.3
- Plan: §5.1/#11/#16/#17、§5.3 会话 SQL
