# B3 契约层：ReparseDocument proto + 生成物

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`

## Description
作为 Services 契约维护者，我需要为 KB-API-B3 批次补齐 ReparseDocument 的 gRPC proto 契约并同步两侧生成物。REST 契约已声明于 v1.yaml:2379–2385（`reparseKnowledgeBaseDocument`），contract baseline 豁免已登记（services-contract-baseline.yaml L44–46）——本 issue **零 OpenAPI 改动、零 baseline 改动**，纯 proto 契约层。

## Scope
- Product line: core (Services 契约层)
- Code paths allowed: `repo/api/proto/kb/v1/kb_service.proto`（仅加 ReparseDocument）、`repo/services/kb-service/app/generated/`、`repo/pkg/generated/pb/kb/v1/`（仅生成，禁手改）
- 禁止修改 OpenAPI v1.yaml（契约已声明）；禁止修改 services-contract-baseline.yaml（豁免已登记）；禁止任何 kb-service / Gateway 实现代码

## Acceptance Criteria
- [ ] [SPEC §2.4] proto `service` 块（kb_service.proto L52 UpdateKBPermissions 之后、L53 `}` 之前）新增 RPC：`rpc ReparseDocument(ReparseDocumentRequest) returns (common.v1.AsyncTaskRef);`，附注释说明 202 异步任务语义（复用 outbox → `ani.tasks.kb.parse`）
- [ ] [SPEC §2.4] 新增 message `ReparseDocumentRequest`（对齐 NotifyDocumentUploadedRequest L105–110 模式 + 幂等键）：tenant_id=1 / kb_id=2 / doc_id=3 / idempotency_key=4（client-supplied uuid，必填）
- [ ] [SPEC §4.2] 运行 buf.gen.yaml 生成两侧 pb：`services/kb-service/app/generated/kb/v1/` + `pkg/generated/pb/kb/v1/`（两侧必须同一提交，禁手改）
- [ ] [SPEC §4.1] OpenAPI v1.yaml 与 services-contract-baseline.yaml **无任何 diff**（reparse 已声明/已登记，属冻结事实）
- [ ] `make validate-services` 通过（零漂移门禁）
- [ ] `git diff --check` 通过

## Dependencies
None（与 B1/B2 批次相互独立；SDK/docs/schema.d.ts 无需再生成——OpenAPI 未变更，生成物零漂移）

## Type
core (contract)

## Priority
high

## Labels
core, contract, proto, kb

## Batch
KB-API-B3

## References
- SPEC: §2.4 gRPC 契约面、§4.1 Frozen Facts（v1.yaml:2379–2385 / baseline L44–46）、§4.2 生成物硬约束
- Plan: §5.2/#12、§6.3 outbox 复用论证、§8 B3 步骤
