# B1 Gateway：UpdateKB + GetDocument handler + contract baseline

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`

## Description
作为 ani-gateway 开发者，我需要新增 `PUT /knowledge-bases/{kb_id}`（updateKnowledgeBase）与 `GET .../documents/{doc_id}`（getKnowledgeBaseDocument）的 gRPC client 方法、REST handler 与路由注册，使 B1 契约（#040）在网关侧可达。与 #043 合入后 B1 批次全链路贯通（一个 PR）。

## Scope
- Product line: core (Services / ani-gateway)
- Code paths allowed: `repo/services/ani-gateway/` only
- 依赖 #040 的 pkg/generated pb（Go 侧）

## Acceptance Criteria
- [ ] [SPEC §2.4] `kb_grpc_client.go`：`KBGRPCClient` 接口 + `kbGRPCClient` 实现 + 测试 fake 新增 `UpdateKB`、`GetDocument`（GetDocument 客户端方法已存在 L161–165，仅接口沿用）
- [ ] [SPEC §2.4] `kb_resources.go` 新增 2 个 handler：
  - `updateKnowledgeBase`：BindJSON `UpdateKnowledgeBaseRequest`（idempotency_key 必填 uuid，缺失 400）→ `client.UpdateKB(ctx, instanceTenantID(c), kbID, idemKey, name, desc)` → `kbToJSON` 200
  - `getKnowledgeBaseDocument`：`client.GetDocument(ctx, tenant, kbID, docID)` → `kbDocumentToJSON`（L512）200
  - 错误统一走 `writeKBError`；nil-client 守卫 503（现有模式）
- [ ] [SPEC §4.2] 路由注册：PUT `/knowledge-bases/:kb_id` + GET `/knowledge-bases/:kb_id/documents/:doc_id`，注册到 `/api/v1/svc` 组且 `svc.METHOD("...")` 直调形式（spec-split 门禁要求）
- [ ] [SPEC §9.2] `kb_resources_test.go`：路由注册断言（2 条）+ fake client handler 单测（成功路径 / nil-client 503 / 404 / 409 名称冲突 / 400 缺 idempotency_key）
- [ ] `make validate-services` 通过（route-contract 校验：#040 新增的 v1.yaml path 与本 issue 路由注册须**同一 PR** 落地）
- [ ] `make test` 通过

## Dependencies
#040 (B1 契约 + Go 侧生成物)；与 #043 同批次合入（KB-API-B1 同一 PR）

## Type
core (feature)

## Priority
high

## Labels
core, ani-gateway, grpc

## Batch
KB-API-B1

## References
- SPEC: §2.4 File Structure（kb_grpc_client.go/kb_resources.go B1 改动点）、§4.2 同批次硬约束、§6.1 错误映射
- Plan: §10-1 B1 步骤 4
