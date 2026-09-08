# B3 kb-service：ReparseDocument servicer + reset_for_reparse_in_tx

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`

## Description
作为 kb-service 开发者，我需要实现 ReparseDocument gRPC servicer：复用 `_notify_document_uploaded`（L451–558）的 outbox + async_tasks 事务模式，重置文档状态后重发解析任务，返回 202 + AsyncTaskRef。parse_orchestrator 已内置重入清理（delete_chunks_by_doc + Core 向量删除），本 issue 零改动下游。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/` only（repositories/document.py、api/grpc_server.py、tests/）
- 依赖 #042 已生成的 pb（ReparseDocument RPC stub）
- 禁止触碰 parse_orchestrator/parse_consumer（零改动）；禁止修改 proto/生成物（契约层归 #042）

## Acceptance Criteria
- [ ] [SPEC §5.1] `repositories/document.py` 新增 `reset_for_reparse_in_tx(conn, *, tenant_id, kb_id, doc_id)`：`UPDATE kb_documents SET parse_status='pending', error_message=NULL, parsed_at=NULL, chunk_count=0`——**chunk_count 必须=0 非 NULL**（列 NOT NULL DEFAULT 0，置 NULL 违反约束事务必失败；既有 update_parse_status_in_tx 的 COALESCE 语义无法重置，故新增函数）
- [ ] [SPEC §5.1] `grpc_server.py` 新增 `ReparseDocument` sync wrapper + `_run_async` 私有实现（与 `_notify_document_uploaded` L451–558 同构）：
  - idempotency_key 非空校验 → INVALID_ARGUMENT
  - 幂等键=**客户端 request.idempotency_key**（不照抄 notify 合成键）；`find_by_idempotency_key` 命中且 status ∈ {pending, completed} → 重放同一 AsyncTaskRef；failed 任务须换新键
  - get_kb 前置：不存在 → NOT_FOUND；status='rebuilding' → FAILED_PRECONDITION
  - get_document 前置：不存在/软删 → NOT_FOUND；parse_status='ready' → FAILED_PRECONDITION（防误触发覆盖，无 force 字段）
  - 单事务：reset_for_reparse_in_tx + INSERT async_tasks（task_type='kb.reparse'，幂等键=客户端 idempotency_key）+ INSERT outbox_events（event_type='kb.reparse'，payload 与 notify 模板同构：doc_id/kb_id/storage_path/tenant_id/file_name/object_id/chunk_size——storage_path/file_name 取自 DB doc_row，非 request）
  - 返回 AsyncTaskRef
- [ ] [SPEC §9.1 B3] pytest 覆盖：parse_status 回 pending / chunk_count 重置 0 / error_message+parsed_at 清 NULL / outbox 落一行 event_type='kb.reparse' / async_tasks 幂等记录 / ready 拒绝 409 / KB rebuilding 拒绝 / doc 不存在 404 / 同键重放同 task_id / failed 换新键可重新发起 / orchestrator 清理链路回归不被破坏
- [ ] [SPEC §11.3-A2] 实现前复核 outbox dispatcher 不按 event_type 过滤（`kb.reparse` 事件可被 `ani.tasks.kb.parse` 消费）——若不成立，先在 issue 内记录并升级
- [ ] `python -m pytest`（kb-service 目录内）通过——pytest 不在 make 门禁内，必须显式运行
- [ ] `make test` 通过

## Dependencies
#042 (B3 契约层 proto + 生成物)；与 #048（Gateway）同一 PR 合入

## Type
core (feature)

## Priority
high

## Labels
core, kb-service, grpc, async-task

## Batch
KB-API-B3

## References
- SPEC: §5.1 reparse 算法、§5.3 状态机（ready 拒绝/效果）、§3.1 kb_documents（chunk_count 约束）、§11.3-A2/A4
- Plan: §5.2/#12、§6.3 outbox 复用论证、§8 B3 步骤
