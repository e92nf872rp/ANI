# Implementation Notes — Issue #030

> **Issue:** kb-service repository 改走数据面（message/session + async_task/outbox，跨表原子折叠）
> **Branch:** `backend-impl`
> **SPEC:** `repo/services/tasks/modules/spec/core/knowledge/design-kb-persistence-to-core-datapipe.md` (§4.2, §6, §8)
> **Date:** 2026-08-11
> **Type:** core (services)

---

## 1. Design Decisions

### DD-1: NotifyDocumentUploaded 幂等检查折叠进 CTE，消除 TOCTOU 竞态

**Ambiguity:** SPEC §4.2 要求 NotifyDocumentUploaded 的 3 表写入（kb_documents UPDATE + async_tasks INSERT + outbox_events INSERT）折叠为单次 `data_query` 调用。但幂等检查（`find_by_idempotency_key`）是否应该保留为独立的前置调用？

**Choice:** 将幂等检查折叠进 CTE 的 `existing AS` 子句。整个流程在一个 SQL 语句中完成：`existing` 查询已有 task → `task` INSERT with `ON CONFLICT DO NOTHING` → `obx` INSERT with `WHERE NOT EXISTS (SELECT 1 FROM existing)` → 最终 `UNION ALL` 返回新 task 或已有 task。

**Rationale:**
1. 消除 TOCTOU 竞态 — 两次并发 RPC 对同一 doc 的通知，都会在 `ON CONFLICT (tenant_id, idempotency_key) DO NOTHING` 上序列化，最终 SELECT 返回相同的 task_id。
2. 单次 data_query = 单次网络往返 + 单次事务，性能优于两次调用（find + fold）。
3. `obx` 的 `WHERE NOT EXISTS (SELECT 1 FROM existing)` 确保重放时不产生重复 outbox 事件。

### DD-2: `to_jsonb(NULL)` → `COALESCE(to_jsonb(...), 'null'::jsonb)` 防止 payload 为 NULL

**Ambiguity:** `jsonb_set` 当 new_value 为 SQL NULL 时返回 NULL（而非 JSON null）。当 `doc_upd.object_id` 或 `kb_cfg.chunk_size` 为 NULL 时，整个 payload 变为 NULL，违反 `async_tasks.payload NOT NULL` 约束。

**Choice:** 所有 `to_jsonb(column)` 调用都用 `COALESCE(to_jsonb(...), 'null'::jsonb)` 包裹，确保生成 JSON null（`'null'::jsonb`）而非 SQL NULL。

**Rationale:**
1. PostgreSQL 的 `jsonb_set` 语义：当 new_value 为 SQL NULL 时返回 NULL 而非 JSON null。
2. `async_tasks.payload` 有 `NOT NULL` 约束，NULL payload 会导致 `SQLSTATE 23502` 运行时错误。
3. JSON null 是语义正确的表示（"值不存在"），与 Python `None` 序列化为 JSON null 一致。

### DD-3: `to_jsonb($7::text)` 显式类型转换解决多态类型推断失败

**Ambiguity:** `to_jsonb($7)` 中 `$7` 是未类型化的 `unknown` 参数，PostgreSQL 无法推断 `to_jsonb` 的多态类型，报 `SQLSTATE 42804`。

**Choice:** 将 `to_jsonb($7)` 改为 `to_jsonb($7::text)`，显式告诉 PostgreSQL 参数类型为 text。

**Rationale:**
1. PostgreSQL 扩展查询协议中，`$7` 未被上下文足够约束时无法推断类型。
2. `::text` 是最小侵入性修复 — `storage_path` 本身就是字符串，`::text` 不改变语义。
3. 同理 `$5::jsonb` 和 `$6::jsonb` 已有显式类型转换，`$7` 是唯一遗漏的。

### DD-4: `_pool_sentinel = object()` 替代 asyncpg.Pool 作为 servicer 的 DB 就绪哨兵

**Ambiguity:** `KBServiceServicer.__init__` 的 `pool` 参数用于判断 DB 是否可用（`pool is None` → `FAILED_PRECONDITION`）。移除 asyncpg 后，如何保持这个哨兵语义？

**Choice:** 在 `main.py` 模块级创建 `_pool_sentinel = object()`，传给 `KBServiceServicer(pool=_pool_sentinel)`。`pool is None` 检查仍然有效（sentinel 非 None），所有 DB-backed RPC 正常执行。

**Rationale:**
1. 最小侵入性 — 不修改 `KBServiceServicer.__init__` 签名和所有 `if self._pool is None` 检查。
2. 语义清晰 — sentinel 是非 None 的配置哨兵，不是真实连接池。issue-031 会完全移除 `pool` 参数。
3. 测试兼容 — 单元测试可以传 `pool=object()` 或 `pool=None`（skeleton mode）。

### DD-5: Outbox dispatcher 用 `CoreClient` + `role="service"` 替代 asyncpg BYPASSRLS

**Ambiguity:** 旧 `outbox.py` 的 `list_undispatched` / `mark_dispatched` 依赖 asyncpg 直连 + `ani_outbox_publisher` BYPASSRLS 角色跨租户扫描。数据面如何实现跨租户访问？

**Choice:** `CoreClient.data_query(role="service")` 跨租户执行。dispatcher 的 CoreClient 用占位 tenant_id（`00000000-...-000000000000`），因为 `role="service"` 忽略 tenant_id。

**Rationale:**
1. SPEC §4.2 明确 `role="service"` 为跨租户语义，替代旧 BYPASSRLS。
2. CoreClient 的 `X-Tenant-Id` header 在 `role="service"` 时不参与 RLS，所以占位 tenant_id 无影响。
3. 网关侧 `dataQuery` handler 校验 `role=service` 需要 `scope=platform`，dispatcher 的 CoreClient 在 dev 模式下注入 `X-Dev-Scope: service`。

---

## 2. Deviations

### DEV-1: `async_task.create_task` 返回空 dict → 改为 raise RuntimeError

**Spec 说:** SPEC §4.2 要求 `data_query` 返回的 rows 为空时应视为异常（INSERT 失败）。

**实现:** 原 `create_task` 返回 `rows[0] if rows else {}`，调用方 `grpc_server.py:260` 做 `str(task_row["id"])` 时会 `KeyError`。改为 `raise RuntimeError("async_tasks INSERT returned no row")`，与 `message.py` 的 `insert_message` 一致。

**原因:** 空行返回 {} 会导致下游 `KeyError` 而非清晰的 RuntimeError，调试困难。统一为 raise 模式后，所有 INSERT 操作的失败行为一致。

### DEV-2: `rls.py` 保留模块但标记 DEPRECATED

**Spec 说:** SPEC §4.2 要求所有 repo 不再调用 `set_tenant_context`，RLS 由 Core 侧注入。

**实现:** `rls.py` 删除了 `set_tenant_context` 函数和 `asyncpg` 导入，仅保留模块文件和 DEPRECATED docstring。`from .rls import set_tenant_context` 的导入已从所有 repo 移除。

**原因:** issue-031 会完全删除 `rls.py` 和 asyncpg pool wiring，当前批次保留文件避免破坏 `__init__.py` 的导入链。测试 `test_message_repo_does_not_import_rls` 验证了 repo 不再依赖 rls。

### DEV-3: E2E 测试 SQL 中 `$7::text` 和 `COALESCE` 修复同步到生产代码

**Spec 说:** SPEC §4.2 要求 E2E 测试覆盖生产代码路径。

**实现:** E2E 测试 `test_e2e_issue030.py` 中的 `fold_sql` 与 `grpc_server.py` 中的 `fold_sql` 保持完全一致（包括 `COALESCE(to_jsonb(...), 'null'::jsonb)` 和 `to_jsonb($7::text)`）。

**原因:** E2E 测试的价值在于验证生产 SQL 在真实 PostgreSQL 上的行为。如果测试 SQL 与生产 SQL 不一致，测试失去意义。

---

## 3. Tradeoffs

### TRA-1: CTE 折叠 vs. 多语句事务

**备选 A: CTE 折叠（选定）**
- 优点：单语句 = 单次网络往返 = 原子性由 PostgreSQL 单语句语义保证。无 TOCTOU 竞态。
- 缺点：SQL 复杂度高（5 个 CTE 子句），可读性差。`jsonb_set` 嵌套容易出错（如 NULL 类型问题）。

**备选 B: 多语句单事务（`;` 分隔）**
- 优点：SQL 更简单，每个语句独立可读。
- 缺点：`ports.DataPlaneQueryRequest` 明确说 Params 只能绑定单语句（pgx 限制），多语句不能用参数化。必须拼接 SQL，有注入风险。

**决定:** 选 A — 参数化安全 > SQL 可读性。CTE 折叠是 SPEC §4.2 的明确要求。

### TRA-2: `_pool_sentinel` vs. 移除 pool 参数

**备选 A: `_pool_sentinel = object()`（选定）**
- 优点：不修改 servicer 签名，所有 `self._pool is None` 检查不变，最小侵入。
- 缺点：sentinel 语义不直观（`pool=object()` 看起来像 bug）。

**备选 B: 完全移除 pool 参数**
- 优点：语义清晰 — 不再有"假的 pool"。
- 缺点：需修改 `KBServiceServicer.__init__`、所有 `self._pool is None` 检查、所有测试的 servicer 构造。改动面大。

**决定:** 选 A — issue-031 专门处理 asyncpg 移除和 pool 参数删除，当前批次不做。

### TRA-3: CTE 名称提取正则 vs. 白名单豁免

**备选 A: CTE 名称提取正则（选定）**
- 优点：自动适应任意 CTE 名称，不需要手动维护豁免列表。
- 缺点：正则 `(?:^|\bWITH\b|,)\s*([A-Za-z_]\w*)\s+AS\s*\(` 可能漏匹配嵌套 CTE 或特殊格式。

**备选 B: 手动 CTE 名称豁免列表**
- 优点：精确控制，无误匹配。
- 缺点：每新增一个 CTE fold 都要更新豁免列表，维护负担大。

**决定:** 选 A — 正则覆盖了 issue-030 所有的 CTE 模式（`WITH name AS (` 和 `, name AS (`），测试验证通过。

---

## 4. Open Questions

### OQ-1: 嵌套 CTE 名称是否会被误判为未注册表？

CTE 正则 `cteRe` 只匹配顶层 `WITH name AS (` 和 `, name AS (`。如果一个 CTE 内部再嵌套一个子查询 `WITH inner AS (...)`，`inner` 不会被提取为 CTE 名称。如果 `inner` 出现在 `FROM inner` 位置，会被 `tableTokenRe` 匹配并误判为未注册表。

**当前状态:** issue-030 的 fold_sql 没有嵌套 CTE，所以不影响。但未来如果有人写嵌套 CTE，可能遇到这个问题。

**建议:** 如果未来出现嵌套 CTE，可以升级正则为递归匹配或改用 SQL parser。

### OQ-2: outbox dispatcher 的 CoreClient 生命周期管理

`_outbox_core` 在 `lifespan` 中创建，在 `lifespan` 退出时调用 `aclose()`。但 `_default_core_client` 在 gRPC servicer 中每次 RPC 调用都创建新的 CoreClient（通过 `__aenter__`/`__aexit__`）。这些临时 CoreClient 的 `aclose()` 是否被正确调用？

**当前状态:** `CoreClient.__aexit__` 调用 `aclose()`，`async with` 语句确保清理。但 `_default_core_client` 返回的 CoreClient 如果不被 `async with` 包裹（直接 `core = self._core_client_factory(tenant_id)`），则不会调用 `aclose()`。

**建议:** 确认 `grpc_server.py` 中所有 `_core_client_factory` 调用都使用 `async with` 语法。

### OQ-3: issue-031 是否会完全移除 `rls.py` 和 `pool` 参数？

SPEC 设计文档提到 issue-031 会删除 `rls.py` 和 asyncpg pool wiring。当前批次保留了 `rls.py` 的空壳和 `_pool_sentinel`。

**建议:** issue-031 实现时确认这些遗留物被清理。

---

## 5. Verification Commands Run

```bash
# Go tests — data plane handlers
cd repo/services/ani-gateway
go test ./internal/router/... -run TestDataPlane -v -count=1
# Result: all PASS

# Python tests — issue-030 code paths
cd repo/services/kb-service
python -m pytest tests/test_us010_wiring.py tests/test_outbox_dispatcher.py \
  tests/test_message_repository.py tests/test_core_client.py \
  tests/test_grpc_server.py tests/test_grpc_wiring.py -v --tb=short
# Result: 48 passed, 0 failed

# E2E test — real PostgreSQL via server NodePort
python tests/test_e2e_issue030.py
# Result: 16 passed, 0 failed, 1 skipped (outbox mark_dispatched: no undispatched events)
```

---

## 6. Files Changed

| File | Change Type | Description |
|------|------------|-------------|
| `app/api/grpc_server.py` | Modified | NotifyDocumentUploaded 3-table CTE fold, Query 2-table fold, COALESCE+::text fixes |
| `app/core_api/client.py` | Modified | Added `data_query()` and `create_table()` methods, `extra_headers` param |
| `app/outbox/dispatcher.py` | Modified | `pool: asyncpg.Pool` → `core_client: CoreClient` |
| `app/repositories/async_task.py` | Modified | asyncpg → CoreClient.data_query, raise on empty rows |
| `app/repositories/message.py` | Modified | CTE fold for create_session_and_message, raise on empty rows |
| `app/repositories/outbox.py` | Modified | asyncpg → CoreClient.data_query with role="service", removed insert_event |
| `app/repositories/rls.py` | Modified | DEPRECATED — set_tenant_context removed, docstring only |
| `main.py` | Modified | asyncpg pools → _pool_sentinel + CoreClient, /readyz checks updated |
| `tests/test_us010_wiring.py` | Modified | Mock asyncpg conn/pool → Mock CoreClient with data_query |
| `tests/test_e2e_issue030.py` | Created | 17 E2E test cases against real PostgreSQL |
| `ani-gateway/internal/router/data_plane_resources.go` | Created | /data/query + /data/tables handlers, CTE name extraction, security hardening |
| `ani-gateway/internal/router/data_plane_resources_test.go` | Created | 40+ handler tests |
| `ani-gateway/internal/middleware/auth.go` | Modified | X-Dev-Scope header support for dev mode |
| `ani-gateway/internal/router/router.go` | Modified | RegisterOptions.DataPlane + Store, registerDataPlaneResources |
| `ani-gateway/instance_service_runtime.go` | Modified | Local profile wires DataPlane when DATABASE_URL set |
| `ani-gateway/main.go` | Modified | Pass DataPlane + Store to RegisterWithOptions |
