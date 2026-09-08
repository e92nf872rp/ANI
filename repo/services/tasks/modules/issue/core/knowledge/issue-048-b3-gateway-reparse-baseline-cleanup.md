# B3 Gateway：Reparse handler + route baseline 清理

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`

## Description
作为 ani-gateway 开发者，我需要实现 `POST /knowledge-bases/{kb_id}/documents/{doc_id}/reparse` 的 client/handler/路由（REST 契约已声明），并清理 architecture route baseline 中 reparse 的 `spec_not_in_code` 条目（代码落地后 spec 与 code 一致，条目必须删除否则 stale 阻断门禁）。

## Scope
- Product line: core (Services / ani-gateway + architecture baseline)
- Code paths allowed: `repo/services/ani-gateway/`、`repo/architecture/services-route-baseline.yaml`（**仅删 reparse 条目**）
- 依赖 #042 的 pkg/generated pb（Go 侧）
- 禁止动 route baseline 的 config/rebuild/models 条目（L62–85，另立批次）

## Acceptance Criteria
- [ ] [SPEC §2.4] `kb_grpc_client.go`：接口 + 实现 + 测试 fake 新增 `ReparseDocument`
- [ ] [SPEC §2.4] `kb_resources.go` 新增 handler `reparseKnowledgeBaseDocument`：BindJSON（idempotency_key 必填 uuid，缺失 400）→ `client.ReparseDocument(...)` → 202 + `AsyncTask` JSON；错误走 `writeKBError`（404 doc 不存在 / 409 ready 或 rebuilding / 503 nil-client）；路由 `svc.POST("...")` 直调形式注册到 `/api/v1/svc` 组
- [ ] [SPEC §9.2] `kb_resources_test.go`：1 条路由注册断言 + fake 单测（202/404/409/400/503）
- [ ] [SPEC §10.1] `repo/architecture/services-route-baseline.yaml`：**删除 reparse 的 spec_not_in_code 条目（L56–61，唯一 kind 条目，path `/knowledge-bases/{kb_id}/documents/{doc_id}/reparse`）**；不得误删 config GET/PUT（L62–73）、rebuild POST（L74–79）、models GET（L80–85）条目
- [ ] [SPEC §9.4] 基线清理后重跑 `make validate-services-route-contract` 确认无 stale 报错
- [ ] [SPEC §9.4] `make validate-services` + `make validate-architecture` + `make test` + `git diff --check` 全部通过
- [ ] progress 文档闭环 4 件套（CLAUDE.md §6-3：development-records/{批次ID}.md 新建、README 索引、CURRENT-SPRINT.md、ANI-06-开发计划.md）

## Dependencies
#042 (B3 契约层 pb Go 侧)；与 #047（kb-service servicer）同一 PR 合入

## Type
core (feature)

## Priority
high

## Labels
core, ani-gateway, architecture-baseline

## Batch
KB-API-B3

## References
- SPEC: §4.1（route baseline L56–61）、§6.1（202/409/404 映射）、§10.1 B3 收尾清单、§11.2（漏删/误删风险）
- Plan: §9 B3 步骤 + 基线清理
