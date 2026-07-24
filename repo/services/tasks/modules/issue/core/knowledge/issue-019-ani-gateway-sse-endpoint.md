# ani-gateway SSE 端点

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-017)
- UX: `repo/services/tasks/modules/ux/console/knowledge/ux-console-knowledge-base-platform.md` (§6.4 streaming states — indirect)
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-ani-gateway-kb.md` (§2, §4.3, §5)

## Description
作为网关开发者，我需要实现 SSE 流式问答端点。

## Scope
- Product line: core (Services / ani-gateway)
- Code paths allowed: `repo/services/ani-gateway/internal/router/kb_sse.go`, `kb_grpc_client.go` only

## Acceptance Criteria
- [ ] [SPEC] SSE 端点 `GET /api/v1/svc/knowledge-bases/{kb_id}/query/stream` 在 gateway 持有（SPEC §4.3）
- [ ] [SPEC] 调 rag-engine 检索 + prompt → vLLM streaming → token 透传（SPEC §5.1 SSE handler 算法）
- [ ] [SPEC] 末尾发送 sources 事件（SPEC §4.3 事件序列：token*→sources→done）
- [ ] [SPEC] SSE 错误处理：400/401/404 流前返回 JSON；流中 error 事件（SPEC §4.3 错误处理, §6.1）
- [ ] [SPEC] 实现 SSE 事件协议四事件：`token`/`sources`/`done`/`error`（SPEC §4.3 事件类型表）
- [ ] [SPEC] 客户端断开 → 取消 vLLM stream（context cancel，SPEC §5.4）
- [ ] `make test` 通过

## Dependencies
#14 (rag-engine Query RPC) — per SPEC §10.2 (US-017 depends on spec-services-rag-engine US-014,015).

## Type
core (services)

## Priority
high

## Labels
core, services

## Batch
M2.1-TASK-B

## References
- SPEC: spec-services-ani-gateway-kb §4.3, §5.1 (SSE handler), §6.1
