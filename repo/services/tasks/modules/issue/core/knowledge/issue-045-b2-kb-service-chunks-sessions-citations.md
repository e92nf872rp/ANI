# B2 kb-service：chunks / sessions / messages / citations 四个 servicer 实现

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/kb-api-completion-plan.md`
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-kb-api-completion.md`

## Description
作为 kb-service 开发者，我需要实现 B2 的 4 个 gRPC servicer（ListDocumentChunks/ListSessionMessages/DeleteSession/ReparseDocument 之外的 B2 部分）及 repository 函数，并把 `ListKBCitations`/`ListKBSessions` 从 p1_rpcs.py 占位替换为真实实现。citations 由 kb_messages.source_chunks JSONB 在 Python 侧展开（不建新表），全部走键集分页。

## Scope
- Product line: core (Services / kb-service)
- Code paths allowed: `repo/services/kb-service/` only（repositories/chunk.py、repositories/message.py、session/cache.py、api/grpc_server.py、api/p1_rpcs.py、tests/）
- 依赖 #041 生成的 pb
- **无 DDL**：全部基于既有表

## Acceptance Criteria
- [ ] [SPEC §5.1] `repositories/chunk.py` 新增 `list_chunks_by_doc_paged(conn, *, tenant_id, kb_id, doc_id, chunk_type, limit, cursor)`：`ORDER BY id ASC` 键集（`id > $cursor`），RLS 事务
- [ ] [SPEC §5.1] `repositories/message.py` 新增：
  - `list_sessions`（聚合 SQL：COUNT(message)/MAX(created_at)/last_query 关联子查询取最早 user 消息；`ORDER BY created_at DESC, id DESC` 复合键集）
  - `get_session`（归属校验：session.kb_id 必须等于 path kb_id，否则 None → 404）
  - `list_session_messages_paged`（`ORDER BY created_at ASC, id ASC` 复合键集——id 为随机 UUID 必须 tie-break）
  - `delete_session`（单事务：DELETE kb_messages → DELETE kb_sessions WHERE id AND kb_id；kb_messages.session_id ON DELETE CASCADE）
- [ ] [SPEC §5.1] `session/cache.py` SessionCache 新增 `delete_session`（Redis DEL，best-effort，失败仅 logger.warning 不阻断）
- [ ] [SPEC §5.1] `grpc_server.py` 新增 `ListDocumentChunks`（limit 1–100 校验 + chunk_type 白名单）/`GetSessionMessages`/`DeleteSession`（get_kb 门禁 → 单事务 → 提交后 best-effort 缓存删除 → session 不存在仍 204 幂等）servicer，sync wrapper + `_run_async` 模式
- [ ] [SPEC §5.1] citations 展开替换 p1 占位：分页查询 assistant 消息（source_chunks IS NOT NULL AND <> 'null'，JOIN kb_sessions WHERE kb_id）→ `json.loads` → 按 doc_id 分组取 score 最高 → `KBCitation.id = uuid.uuid5(NAMESPACE_URL, f"ani:kb:citation:{kb_id}:{message_id}:{doc_id}")`，message_id/session_id 透传
- [ ] [SPEC §2.4] `p1_rpcs.py` 删除 citations/sessions 占位（grpc_server.py L1238/L1241 委托点改为真实实现）；**UpdateKBPermissions 占位保留**
- [ ] [SPEC §9.1 B2] pytest 覆盖：分页/chunk_type 过滤/软删 404/跨租户隔离/sessions 聚合正确性/messages 回放顺序（同秒 id tie-break）/user 消息 sources null/跨 KB session 404/删除后 GetSessionMessages 404/重复删除幂等/Redis DEL 调用（mock）/citations 空列表/跳过 'null' 字符串/uuid5 确定性/同 message×doc 取 score 最高
- [ ] [SPEC §9.1] **更新 P1 占位锁定测试**：`test_grpc_wiring.py:191–209`、`test_grpc_server.py:209–230` 的 UNIMPLEMENTED 断言改为真实行为
- [ ] `python -m pytest`（kb-service 目录内）通过——pytest 不在 make 门禁内，必须显式运行
- [ ] `make test` 通过

## Dependencies
#041 (B2 契约 + 生成物)

## Type
core (feature)

## Priority
high

## Labels
core, kb-service, grpc

## Batch
KB-API-B2

## References
- SPEC: §5.1 游标分页表（4 种）/sessions 聚合 SQL/citations 展开/§5.4 边界场景、§6.1 错误分类
- Plan: §5.1/#11/#15/#16/#17/#18、§5.3、§7
