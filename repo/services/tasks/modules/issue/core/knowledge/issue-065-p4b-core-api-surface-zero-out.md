# KB-SPLIT-P4B Core 调用面清零：core_api 模块删除 + NotifyDocumentUploaded RPC 删除

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/0001-split-kb-from-core.md`
- UX: N/A — backend-only
- SPEC: N/A

## Description
作为 kb-service 开发者，我需要执行 Core 契约面清零（D3：无调用即无契约）：删除 core_api 模块（vector-stores + objects + link 调用面，D1 后均无调用方）、删除 NotifyDocumentUploaded 跨服务通知 RPC（被 #058 预签名直传 + complete 端点替代）；kb 对 Core 仅剩租户上下文信任（D2 暂缓项维持现状）。

## Scope
- Product line: core (kb-service)
- Code paths allowed: `repo/services/kb-service/app/core_api/`（整模块删除）、`repo/services/kb-service/app/api/grpc_server.py`（删 NotifyDocumentUploaded RPC）、proto 定义同步
- Core 侧端点（vector-stores/objects/link）归属 Core 团队，kb 只发下线知会不删 Core 代码

## Acceptance Criteria
- [ ] [ADR §D1] `app/core_api/client.py` 整模块删除；grep kb-service 无 core_api 引用、无 Core REST base_url 配置
- [ ] [ADR §D1] NotifyDocumentUploaded RPC 删除（proto + servicer 实现 + 调用方）；上传链路唯一路径 = 预签名直传 + complete（#058）
- [ ] [ADR §D1] kb 对 Core 仅剩租户上下文信任（D2 范围内，X-Tenant-Id 等原样保留）；出向 Core HTTP 调用清零（网络层验证：kb 进程无 Core Service 出向连接配置）
- [ ] [ADR §D3] Core OpenAPI 契约与契约测试取消（不设 Core 契约项）；RFC 流程文档登记（未来新 Core 依赖时按 RFC 办理）
- [ ] [ADR §五-阶段0] 向 Core 团队发出端点下线知会（vector-stores / knowledge-base-link / 预计算向量插入不再被 kb 调用），回执落档
- [ ] proto 更新后 Go 侧 pkg/generated 与 Gateway 依赖同步确认（若 NotifyDocumentUploaded 在 Go 侧有引用则一并知会）
- [ ] `make test` + `make validate-architecture` + `git diff --check` 通过
- [ ] progress 文档闭环 4 件套（CLAUDE.md §6-3）

## Dependencies
#057 #058（向量/对象自管路径替代完成，core_api 调用方已清零后再删模块）

## Type
core (feature)

## Priority
high

## Labels
core, kb-service, core-api-removal, d1, d3, split, adr-0001

## Batch
KB-SPLIT-P4B

## References
- ADR: §D1（Core 依赖收缩结果：core_api 不带入）、§D3（无调用即无契约 + RFC 流程）、§四 #6、§五-阶段5（不带入清单）
