# [功能] SSE 切换 — kb-service.Retrieve + Gateway 改线 (步骤 10 功能)

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX: N/A — backend-only
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` (§10.2-§10.4, 步骤 10)

## Description
作为 Services 层开发者，我需要实现 kb-service `Retrieve` RPC 和 Gateway 改线，将 SSE 从 Gateway 直连 rag-engine + vLLM 改为 Gateway → kb-service.Retrieve（gRPC stream），由 kb-service 编排检索 + 流式生成。flag 控制，可回滚；降级行为保持。跑通全部测试。

## Scope
- Product line: core (Services / kb-service + ani-gateway)
- Code paths allowed: `repo/services/kb-service/app/api/grpc_server.py`, `repo/services/ani-gateway/internal/router/kb_sse.go`, `repo/services/ani-gateway/internal/router/kb_grpc_client.go`

## Acceptance Criteria
- [ ] [Plan §10.2] kb-service `Retrieve` RPC 实现：编排 retrieve → sources event → GenerateStream → token events → done event
- [ ] [Plan §10.2] session 管理与消息持久化与 Query RPC 一致（先持久化 user 到 DB+Redis，再 load_history 含当前轮 user，再调 GenerateStream，最后持久化 assistant）
- [ ] [Plan §10.2] 无结果三道闸门与 Query RPC 一致
- [ ] [Plan §10.3] Gateway `kb_sse.go` 改为 gRPC stream 透传：flag=true → `kbClient.Retrieve`；flag=false → 旧路径 `streamQuerySSELegacy`
- [ ] [Plan §10.3] `kb_grpc_client.go` 新增 `Retrieve` 方法
- [ ] [Plan §10.3] 新增 `KB_SSE_USE_NEW_PATH` flag（默认 false）
- [ ] [Plan §0.1] 事件序列不变：token* → sources → done
- [ ] [Plan §0.1] SSE 降级：kb-service 不可用时返回空流
- [ ] [Plan §10.4] 可选简化方案：若 gRPC stream 透传延迟过高，kb-service.Retrieve 只做检索 + session 管理，Gateway 直连 vLLM 流式生成（由 E2E 延迟测试决定）
- [ ] Gateway 单测 `kb_sse_test.go` 适配
- [ ] E2E `run_e2e_sse_test.py` 事件序列不变
- [ ] flag=false 时旧路径行为不变

## Dependencies
#026 (kb-service Retrieve 契约) + #036 (E2E 测试) — 实现依赖 proto 契约 + E2E 验证基础。与 #037 可并行。

## Type
core (feature)

## Priority
high

## Labels
core, feature, gateway

## Batch
RAG-REFACTOR-STEP-10-FEATURE

## References
- Plan: §10.2 (kb-service Retrieve), §10.3 (Gateway 改线), §10.4 (可选简化), §0.1 (SSE 等价性)
