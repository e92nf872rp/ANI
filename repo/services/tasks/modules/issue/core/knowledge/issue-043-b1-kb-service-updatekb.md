# B1 kb-service：update_kb repository + UpdateKB servicer

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`

## Description
作为 kb-service 开发者，我需要实现 UpdateKB gRPC servicer 与 update_kb repository 函数，支撑 Gateway 的 `PUT /knowledge-bases/{kb_id}`（#5）。实现模式与既有 CreateKB（幂等重放 + async_tasks）和 GetKB（RLS + NOT_FOUND）同构。名称撞同租户既有 KB 时返回 ALREADY_EXISTS（PG 23505，UNIQUE(tenant_id, name)）。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/` only（repositories/knowledge_base.py、api/grpc_server.py、tests/）
- 依赖 #040 已生成的 pb（UpdateKB RPC stub）

## Acceptance Criteria
- [ ] [SPEC §5.1] `app/repositories/knowledge_base.py` 新增模块级 async 函数 `update_kb(conn, *, tenant_id, kb_id, name, description)`：`COALESCE(NULLIF($name,''),name)` / `COALESCE(NULLIF($desc,''),description)` + `updated_at=now()`，WHERE id=$kb_id（事务内 `set_tenant_context` RLS），RETURNING 全列；无行返回 None
- [ ] [SPEC §5.1] `grpc_server.py` 新增 `UpdateKB` sync wrapper + `_run_async` 私有实现（与 `_create_kb` L152 同模式）：idempotency_key/kb_id 校验 → `async_task_repo.find_by_idempotency_key` 幂等重放 → `update_kb` → None 时 `context.abort(NOT_FOUND)` → asyncpg `UniqueViolationError`（SQLSTATE 23505）捕获转 `ALREADY_EXISTS` → 写 async_tasks 幂等记录（result=更新后行）→ `_kb_row_to_pb` 返回
- [ ] [SPEC §5.4] 空白 name/description 保持原值不修改（COALESCE+NULLIF 语义）
- [ ] [SPEC §6.1] 跨租户 kb_id 经 RLS 不可见 → NOT_FOUND
- [ ] [SPEC §9.1 B1] pytest 覆盖：成功（name+desc 同改）/ NOT_FOUND / 跨租户隔离 / 幂等重放 / 空字段不改 / **名称冲突 23505 → ALREADY_EXISTS**
- [ ] `python -m pytest`（kb-service 目录内）通过——注意 pytest 不在任何 make 门禁内，必须显式运行
- [ ] `make test` 通过

## Dependencies
#040 (B1 契约 + 生成物)

## Type
core (feature)

## Priority
high

## Labels
core, kb-service, grpc

## Batch
KB-API-B1

## References
- SPEC: §5.1 UpdateKB 算法、§3.1 knowledge_bases 表（UNIQUE(tenant_id,name)）、§6.1 错误分类（409 ALREADY_EXISTS）
- Plan: §5.1/#5、§10-1 B1 步骤 2
