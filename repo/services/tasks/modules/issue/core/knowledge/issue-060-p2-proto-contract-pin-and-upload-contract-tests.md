# KB-SPLIT-P2 proto 契约固化：pin 版本统一分发 + 上传契约入契约测试

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/0001-split-kb-from-core.md`
- UX: N/A — backend-only
- SPEC: N/A

## Description
作为 kb-service 开发者，我需要收敛三份 proto 生成物复制（`app/generated/kb/v1/`、`app/generated/common/v1/`、`app/rag_engine/rag_pb2*`）：对上 proto（kb.v1 + common.v1）归 kb 仓库、停止散复制、改 pin 版本共享 proto 包；对 rag-engine proto（Parse/Embed/Generate/GenerateStream）与 rag-engine 共同 pin 同一版本；对上前端的上传契约（预签名签发 + complete 端点，D1 新增）写入契约测试；对 Core 无调用即无契约（D3）。

## Scope
- Product line: core (kb-service)
- Code paths allowed: `repo/services/kb-service/proto/`（或契约包位置）、`repo/services/kb-service/app/generated/`（收敛处理）、`repo/services/kb-service/tests/`（契约测试）、生成命令文档 `docs/`
- D2：身份相关契约测试仅覆盖"租户上下文传递正确性"，不针对具体 header 形态做过强断言

## Acceptance Criteria
- [ ] [ADR §五-阶段2-对上proto] kb.v1 + common.v1 归 kb 仓库；停止 `app/generated/` 散复制，改 pin 版本共享 proto 包；生成命令写进 docs（含 Python 版本锁定）
- [ ] [ADR §五-阶段2-对rag] rag proto（Parse/Embed/Generate/GenerateStream）与 rag-engine 共同 pin 同一版本；EmbedResponse 扁平 float 数组等字段约定列入契约测试
- [ ] [ADR §五-阶段2-上传契约] 预签名 PUT URL 签发 + complete 端点（HeadObject 校验 + 登记）契约测试：时效、key 规则、双视角 URL 语义固化
- [ ] [ADR §D2] 契约测试覆盖租户上下文传递正确性，不对具体 header 形态做强断言（D2 暂缓，零改动原则）
- [ ] [ADR §D3] 对 Core 不设契约测试（无调用即无契约）；文档登记 RFC 流程（未来新 Core 依赖时启用）
- [ ] [ADR §一-1.3] gRPC server 全量 RPC 面与 proto 一致（含 #052 新增 ListOperations/GetOperation）
- [ ] `make test` + `make validate-architecture` + `git diff --check` 通过
- [ ] progress 文档闭环 4 件套（CLAUDE.md §6-3）

## Dependencies
#052（新 RPC 入 proto）；#057 #059（向量/对象路径定型后契约固化）

## Type
core (feature)

## Priority
medium

## Labels
core, kb-service, proto, contract-testing, split, adr-0001

## Batch
KB-SPLIT-P2

## References
- ADR: §四 #7（proto 三份复制耦合点）、§五-阶段2、§D2（契约测试边界）、§D3（对 Core 无契约）
