# B1 契约层：UpdateKB / GetDocument 契约 + proto + 生成物

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`

## Description
作为 Services 契约维护者，我需要为 KB-API-B1 批次（#5 KB 更新、#10 文档详情）补齐 OpenAPI 契约与 gRPC proto，并同步两侧生成物。这是 B1 的前置契约层：新 operation 纯新增、无破坏性变更。

## Scope
- Product line: core (Services 契约层)
- Code paths allowed: `repo/api/openapi/services/v1.yaml`、`repo/api/proto/kb/v1/kb_service.proto`、`repo/services/kb-service/app/generated/`、`repo/pkg/generated/pb/kb/v1/`（仅生成）、`repo/sdks/services/`、`repo/docs/api/services.html`、`repo/frontends/console/src/api/schema.d.ts`（仅生成）
- 禁止手改生成物；禁止触碰 Core API `repo/api/openapi/v1.yaml`

## Acceptance Criteria
- [ ] [SPEC §4.3 #5] v1.yaml 既有 path `/knowledge-bases/{kb_id}`（L2166，现有 GET/DELETE）追加 **PUT** operation，operationId=`updateKnowledgeBase`，200 返回 `KnowledgeBase`，请求体 `UpdateKnowledgeBaseRequest`（idempotency_key required uuid / name / description nullable，空字段=不修改）
- [ ] [SPEC §4.3 #10] v1.yaml 既有 path `.../documents/{doc_id}`（L2265，现有 DELETE）追加 **GET** operation，operationId=`getKnowledgeBaseDocument`，200 返回既有 `KBDocument` schema
- [ ] [SPEC §4.2] 新增 2 个 operationId 同 PR 登记 `repo/architecture/services-contract-baseline.yaml` 的 `operation_security` 豁免（格式对齐 KB 域 L5–56 既有 18 条）
- [ ] [SPEC §2.4] proto 新增 `UpdateKB` RPC（request: tenant_id/kb_id/idempotency_key/name/description，returns KnowledgeBase）；`GetDocument` RPC 已存在（kb_service.proto:27），仅校验 request 字段满足 Gateway 需要（tenant_id/kb_id/doc_id）
- [ ] [SPEC §4.2] 运行 buf.gen.yaml 生成两侧 pb：`services/kb-service/app/generated/kb/v1/` + `pkg/generated/pb/kb/v1/`（两侧必须同一提交，禁手改）
- [ ] [SPEC §9.4] 生成物更新：SDK `sdks/services/`、`docs/api/services.html`、Console `schema.d.ts`（npm run gen-api）
- [ ] `make validate-services` 通过（零漂移门禁）
- [ ] `git diff --check` 通过

## Dependencies
None

## Type
core (contract)

## Priority
high

## Labels
core, contract, openapi, proto

## Batch
KB-API-B1

## References
- SPEC: §4.1 Frozen Facts（L2166/L2265 path 现状）、§4.2 OpenAPI Change Plan（同批次硬约束）、§4.3 Endpoints #5/#10
- Plan: §3 批次 B1、§5.1/#5 字段设计
