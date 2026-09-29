# KB-SPLIT-P5B Gateway 与依赖方最终切换：路由指向独立 kb-service + task 域对接

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/0001-split-kb-from-core.md`
- UX: N/A — backend-only
- SPEC: N/A

## Description
作为 ani-gateway 开发者，我需要把 Gateway 的 KB 资源路由（kb_resources.go 经 kb_grpc_client.go，含 SSE 流转换 kb_sse.go）从指向 monorepo 内 kb-service 切换到独立部署的 kb-service；task 资源路由按 D4 处置口径完成对接知会（Gateway task 路由依赖 async_tasks，任务展示归 task 域，届时接 task-service）；前端上传链路（预签名直传）切换确认。

## Scope
- Product line: core (ani-gateway + kb-service 部署)
- Code paths allowed: `repo/services/ani-gateway/internal/router/kb_resources.go`、`repo/services/ani-gateway/internal/clients/kb_grpc_client.go`（或实际位置）、`kb_sse.go`、Gateway 配置（kb-service gRPC 地址指向）
- Gateway 纯 gRPC 代理原则不变（不直连 kb 表）

## Acceptance Criteria
- [ ] [ADR §一-1.6] kb_grpc_client.go 目标地址切换至独立 kb-service（gRPC :50053）；kb_resources.go 全部 KB 路由经新指向可用
- [ ] [ADR §一-1.6] kb_sse.go SSE 流转换验证：Query 流式（StreamToken/Sources/Done/NoResult 事件面）端到端正常
- [ ] [ADR §一-1.6] Gateway task 路由处置落档：`task_resources.go` 依赖 async_tasks——按 D4 归 task 域；本 issue 只发对接知会与登记（kb 不再写共享 async_tasks 已由 #051 完成），不改 task 路由（演进期 task-service 就绪后接，ADR §五-阶段6）
- [ ] [ADR §D1] 前端上传链路切换确认：上传走 Gateway → kb 签发预签名 → 前端直传 MinIO（NodePort）→ Gateway → kb complete；与 #049 前端排期知会闭环
- [ ] [ADR §五-阶段4] 切换后在 #066 环境复跑 KB 全链路验收（Gateway → kb → 真实依赖）确认路由切换无回归
- [ ] `make test` + `make validate-architecture` + `make validate-services-route-contract` + `git diff --check` 通过
- [ ] progress 文档闭环 4 件套（CLAUDE.md §6-3）

## Dependencies
#068（独立仓库与部署就绪后切换）

## Type
infra (feature)

## Priority
high

## Labels
core, ani-gateway, kb-service, routing, split, adr-0001

## Batch
KB-SPLIT-P5B

## References
- ADR: §一-1.6（Gateway 纯 gRPC 代理 + task 路由 D4 处置）、§D1（前端直传链路）、§五-阶段6（task-service 就绪后演进）
