# B2 Gateway：chunks / sessions handler + KBCitation 增强字段

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`

## Description
作为 ani-gateway 开发者，我需要实现 B2 的 Gateway 侧：3 个新 client 方法（ListDocumentChunks/GetSessionMessages/DeleteSession）、3 个新 handler + 路由（#11/#17/#18），并为既有 citations/sessions handler 补 KBCitation 增强字段映射（message_id/session_id）。citations/sessions handler 已存在（501 兜底），#045 合入后自动激活。

## Scope
- Product line: core (Services / ani-gateway)
- Code paths allowed: `repo/services/ani-gateway/` only
- 依赖 #041 的 pkg/generated pb（Go 侧）

## Acceptance Criteria
- [ ] [SPEC §2.4] `kb_grpc_client.go`：接口 + 实现 + 测试 fake 新增 `ListDocumentChunks`/`GetSessionMessages`/`DeleteSession`（ListKBCitations L262/ListKBSessions L272 已存在）
- [ ] [SPEC §2.4] `kb_resources.go` 新增 3 个 handler + 3 条路由（`svc.METHOD` 直调形式，`/api/v1/svc` 组）：
  - `listKnowledgeBaseDocumentChunks`：query 解析 limit/cursor/chunk_type → client → `KBChunkListResponse`；`custom_metadata`（proto string）`json.Unmarshal` 后输出 object
  - `listKnowledgeBaseSessionMessages`：query limit/cursor → client → messages；`sources`（proto string）Unmarshal 后输出数组，user 消息输出 null
  - `deleteKnowledgeBaseSession`：client → 204；错误走 `writeKBError`；nil-client 503 守卫
- [ ] [SPEC §4.3] 既有 `listKnowledgeBaseCitations`（kb_resources.go:350）/`listKnowledgeBaseSessions`（:377）handler：`kbCitationJSON`（L471）/`kbCitationToJSON`（L545）追加 `message_id`/`session_id` 映射（omitempty 空串不出）
- [ ] [SPEC §9.2] `kb_resources_test.go`：3 条路由注册断言 + fake client 单测（成功/404/400 limit 越界/503 nil-client）
- [ ] [SPEC §9.2] 序列化专项：custom_metadata/sources object 化验证（proto string → object，对齐契约）
- [ ] `make validate-services` 通过（#041 新增 path 与路由注册同一 PR）
- [ ] `make test` 通过

## Dependencies
#041 (B2 契约 + Go 生成物)；与 #045 同批次合入（KB-API-B2 同一 PR）

## Type
core (feature)

## Priority
high

## Labels
core, ani-gateway, grpc

## Batch
KB-API-B2

## References
- SPEC: §2.3 citations 激活路径（501 → 自动激活）、§2.4 B2 改动点、§3.2 JSONB 字段序列化
- Plan: §10-2 B2 步骤
