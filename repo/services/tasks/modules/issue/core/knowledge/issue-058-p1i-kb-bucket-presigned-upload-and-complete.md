# KB-SPLIT-P1I kb 自有 bucket：预签名 PUT 直传 + complete 端点 + 双视角 URL（D1 对象自管）

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/0001-split-kb-from-core.md`
- UX: N/A — backend-only
- SPEC: N/A

## Description
作为 kb-service 开发者，我需要新建 kb 自有 MinIO bucket 承载文档正本与解析产物（无存量移交），实现上传新链路：① 前端经 Gateway 请求 kb 签发预签名 PUT URL（对外视角 NodePort、时效受限、key 预定）→ ② 前端直传 MinIO → ③ 前端调 kb complete 端点，kb HeadObject 校验后登记 kb_documents。双视角 URL（NodePort 对外 / ClusterIP 对内）由 kb 自主签发，继承现有先例。NotifyDocumentUploaded 跨服务通知 RPC 不再存在（其删除在 #065）。

## Scope
- Product line: core (kb-service)
- Code paths allowed: `repo/services/kb-service/app/`（新建对象存储客户端模块、complete 端点实现）、`repo/services/kb-service/migrations/`（若 kb_documents 登记字段有变更）
- MinIO service account 凭据配置（Secret 化归 #061）
- 前端与 Gateway 的配合改造由 #049 知会排期，前端契约细节在 #060 契约固化

## Acceptance Criteria
- [ ] [ADR §D1-对象自管] kb 自有 bucket 新建（正本 + 解析产物共用；key 规则沿用：产物 `{kb_id}/{doc_id}/images/{uuid}.{ext}`），无存量移交事项
- [ ] [ADR §D1-对象自管] 预签名 PUT URL 签发端点：对外视角 NodePort 地址、时效受限、key 预定（`{kb_id}/{doc_id}` 正本 key 规则）
- [ ] [ADR §D1-对象自管] complete 端点：HeadObject 校验对象存在与大小 → 登记 kb_documents（pending 状态）→ 触发解析链路；不再依赖 NotifyDocumentUploaded RPC
- [ ] [ADR §一-1.6] 双视角 URL：对外 NodePort（上传）/ 对内 ClusterIP（解析时 kb 签发内网视角 URL 给 rag-engine）由 kb 自主签发
- [ ] [ADR §D1-对象自管] 时效与最小权限：预签名 URL 时效配置化、仅授 PUT 单操作权限（测试面在 #067 专项回归覆盖）
- [ ] 单测：签发→complete 登记流转；HeadObject 校验失败（对象不存在）拒绝登记
- [ ] `make test` + `make validate-architecture` + `git diff --check` 通过
- [ ] progress 文档闭环 4 件套（CLAUDE.md §6-3）

## Dependencies
#049（基线 + 前端排期知会已发出）；#053（complete 登记 kb_documents，B 层 DDL 基线就绪）

## Type
core (feature)

## Priority
high

## Labels
core, kb-service, minio, presigned-upload, split, d1, adr-0001

## Batch
KB-SPLIT-P1I

## References
- ADR: §D1（对象自管 + 预签名直传流程三步）、§一-1.2（文档状态机 pending→parsing）、§一-1.6（MinIO 双视角先例）
