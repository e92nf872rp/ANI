# Development Records — RAG-REFACTOR-STEP-8A-FEATURE

## Issue #035: 受控切换同步 Query flag 可回滚 (步骤 8A 功能)

**日期:** 2026-08-21
**分支:** `refactor/architecture-compliance`
**类型:** core (feature)
**依赖:** #027 (接口) + #034 (单测+shadow+契约)

### 变更文件

| 文件 | 变更内容 |
|------|----------|
| `services/kb-service/app/core/config.py` | 新增 flag: `kb_query_use_new_path` (默认 False)、`kb_query_shadow_mode` (默认 False)、`rag_engine_grpc_addr`、`nats_parse_subject_v2`、`kb_parse_consumer_enabled` |
| `services/kb-service/app/api/grpc_server.py` | Query RPC flag 切换 (`_query_new_path` / 旧路径)、`_load_history` (LRANGE -limit -1)、`_shadow_compare`、fallback 工厂 + 单例、#034 审查修复 H1/H2/R1/R2 |
| `services/kb-service/app/services/query_orchestrator.py` | 新增 — `QueryOrchestrator` 实现 `QueryOrchestratorProtocol`: retrieve → 三道闸门 → Generate RPC → QueryResult |
| `services/kb-service/app/session/cache.py` | 新增 `list_recent_messages` 方法 (LRANGE key -limit -1, 最近 N 条, 按时间正序) |
| `services/kb-service/main.py` | 生产环境接线: 基于 flag 的工厂注入、parse consumer 启动、连接池扩容、readyz 上报、生命周期清理 |
| `services/kb-service/tests/test_query_orchestrator.py` | 新增 — 20 个测试 (闸门、NO_RESULT_ANSWER、多轮会话、token 计数、QueryResult 结构、协议合规、_load_history、flag 切换、shadow 模式、config flag) |

---

## 1. 设计决策

### 1.1 闸门 ③ 作为安全网保留 (尽管实际不可达)

**模糊点:** Plan §0.1 (第 1591 行) 指出新架构中 `RetrieveService` 在 orchestrator 看到 sources 之前已完成 dedup, 因此闸门 ③ (LLM 调用后 `if not sources`) 在闸门 ① 和 ② 通过后永远不会触发。规格未明确是否保留这段死代码。

**决策:** 在 `QueryOrchestrator.query()` 中 Generate RPC 调用之后保留闸门 ③, 返回 LLM 响应中的 `input_tokens`/`output_tokens` (非 0)。

**理由:**
- 完全匹配旧 `QAService` 控制流 (第 576-583 行), 为未来可能出现的 post-LLM source 过滤代码路径记录预期行为。
- Plan §0.1 第 1591 行明确接受此差异, 将闸门标注为安全网。
- 移除它会偏离已记录的旧路径语义, 而节省的代码量可忽略不计。

### 1.2 Shadow 对比使用 chunk_id Jaccard (而非 doc_id)

**模糊点:** Plan §0.2/§7.2 要求 "sources Jaccard > 90%", 但未指定 source 的身份标识字段。

**决策:** `_shadow_compare` 基于 `chunk_id` 集合计算 Jaccard, 而非 `doc_id` 集合。

**理由:**
- `doc_id` 粒度太粗: 同一文档的多个 chunk 无法区分, 即使实际检索到的 chunk 不同, Jaccard 也会被虚高。
- `chunk_id` 提供更细粒度的等价性度量, 符合 Plan 等价性矩阵的意图。
- 与 Issue #034 的 shadow 测试 (`test_query_shadow.py`) 保持一致, 同样使用 chunk_id。

### 1.3 按租户缓存 orchestrator (`self._orchestrators` 字典)

**模糊点:** Plan 步骤 8A 伪代码按请求构造 `QueryOrchestrator`。规格未说明是否缓存 orchestrator 或其 `RetrieveService`。

**决策:** `grpc_server._query_new_path` 在 `self._orchestrators` 字典中为每个 `tenant_id` 缓存一个 `QueryOrchestrator`。每个 orchestrator 持有一个租户级 `RetrieveService` (后者持有租户级 `CoreClient`)。

**理由:**
- 保持多租户隔离: 每个租户拥有独立的 `RetrieveService` + `CoreClient`, 携带 RLS 上下文。
- 避免每次 Query 请求都重建 `RetrieveService` (及其 `CoreClient` + gRPC channel) — gRPC channel 是昂贵的资源。
- fallback 路径 (`_default_retrieve_service`) 复用模块级 `_default_grpc_client_instance` 单例, 避免 channel 泄漏 / 连接风暴。

### 1.4 `_load_history` 使用新增的 `list_recent_messages` 方法 (而非修改 `list_messages`)

**模糊点:** Plan §8A 第 1610 行要求修改 `list_messages` 为 `LRANGE -limit -1`。但 `list_messages` 也被 kb-service 其他功能使用 (history fallback), 修改它可能影响旧路径。

**决策:** 新增一个独立方法 `SessionCache.list_recent_messages` (LRANGE key -limit -1), 而非修改现有的 `list_messages` (LRANGE 0 limit-1)。`_load_history` 调用 `list_recent_messages`。

**理由:**
- Plan §8A 第 1612 行时序约束: `list_messages` 的修改需在 flag=true 之后进行。通过新增独立方法, 旧路径 (flag=false) 在任何 flag 状态下都完全不受影响。
- `list_messages` 语义 (最老 N 条) 保留给现有调用方。
- `list_recent_messages` 语义 (最近 N 条, 按时间正序) 与旧 `ChatMemoryBuffer.get()` 的 token_limit 行为一致。

---

## 2. 偏差

### 2.1 `QueryOrchestrator.__init__` 不接收 `session_cache` 或 `db_pool`

**规格要求:** Plan 步骤 8A 伪代码 (第 1525 行): `def __init__(self, retrieve_service, rag_engine_grpc_client, session_cache, db_pool)`。

**实际实现:** `def __init__(self, *, retrieve_service, rag_engine_client)` — 仅 2 个依赖。历史加载 (`_load_history`) 由调用方 (`grpc_server._query_new_path`) 在调用 `orchestrator.query()` **之前**完成, `history` 作为参数传入。

**为何更好:**
- 职责分离: orchestrator 专注于 retrieve → 闸门 → Generate。历史加载 (Redis/DB) 是 I/O 关注点, 属于 servicer 而非 orchestrator。
- orchestrator 纯净且可测试: 测试直接注入 `history`, 无需 mock cache/pool。
- `contracts.py` 中的 `QueryOrchestratorProtocol` 已声明 `query(..., history: list[dict[str, str]])` 参数, 因此 orchestrator 正确实现了协议。

### 2.2 Shadow 模式在 `_query_new_path` 内通过 `asyncio.ensure_future` 触发 (而非独立的 `_query_shadow` 方法)

**规格要求:** Plan 步骤 8A 伪代码 (第 1514 行): `asyncio.ensure_future(self._query_shadow(...))` — 一个独立的 shadow 方法。

**实际实现:** Shadow 在 `_query_new_path` 内通过 `asyncio.ensure_future(self._shadow_compare(...))` 触发。方法命名为 `_shadow_compare` (而非 `_query_shadow`), 运行旧 REST 路径 + Jaccard 对比。

**为何可接受:** 功能等价 — fire-and-forget 异步任务, 永不影响主路径。命名更具描述性 (`_shadow_compare` vs `_query_shadow`)。

### 2.3 规格要求修改 `list_messages`; 我们新增了 `list_recent_messages`

**规格要求:** Plan §8A 第 1610/1617 行: "修改 list_messages 内部为 LRANGE key -limit -1"。

**实际实现:** 新增 `list_recent_messages` 方法, 而非修改 `list_messages`。

**为何必要:** Plan §8A 第 1612 行警告 `list_messages` 被其他功能使用, 修改它有风险。新增独立方法完全消除风险。详见设计决策 1.4。

---

## 3. 权衡

### 3.1 Orchestrator 缓存 vs 按请求构造

**可选方案:**
1. **按租户缓存 orchestrator** (选用) — 每个租户一个 `QueryOrchestrator`, 持有在 `self._orchestrators` 字典中。
2. **按请求构造** — 每次 Query 调用都新建 orchestrator。
3. **单一共享 orchestrator** — 所有租户共用一个 orchestrator。

**选用: 按租户缓存**, 因为:
- 按请求构造会重建 gRPC channel (开销大, 有连接风暴风险)。
- 单一共享 orchestrator 丢失租户级 `CoreClient` 隔离 (RLS 上下文)。
- 按租户缓存在隔离 (租户级 `RetrieveService`) 和资源效率 (channel 复用) 之间取得平衡。

### 3.2 Fallback 工厂单例 vs 始终注入

**可选方案:**
1. **模块级单例 fallback** (选用) — `_default_grpc_client_instance` 首次使用时创建; `_default_rag_engine_grpc_client()` 返回它。
2. **强制注入** — 无 fallback; 若 `main.py` 未注入工厂则崩溃。
3. **按调用构造** — 每次 fallback 调用都新建 `RagEngineGRPCClient`。

**选用: 模块级单例**, 因为:
- 生产环境 (`main.py`) 在 flag=true 时始终注入工厂, fallback 仅用于测试 / 开发。
- 按调用构造会泄漏 gRPC channel (每个 `RagEngineGRPCClient` 会打开一个 channel)。
- 强制注入会使 servicer 在无复杂设置的情况下不可测试 (测试使用 fallback)。

### 3.3 闸门 ③: 保留死代码 vs 移除

**可选方案:**
1. **保留作为安全网** (选用) — Generate 之后保留 `if not sources`, 返回 LLM tokens。
2. **移除** — 删除不可达分支, 依赖闸门 ①。

**选用: 保留**, 因为:
- 匹配旧路径控制流 (Plan §0.1 第 1591 行将此记录为可接受)。
- 未来 post-LLM source 过滤器可能使其可达; 闸门记录了预期行为。
- 代码成本可忽略 (4 行)。

---

## 4. 待确认问题

### 4.1 Orchestrator 字典无界增长

`self._orchestrators` 每个租户增加一个条目, 永不淘汰。对于少量租户没有问题, 但如果平台扩展到数千租户, 该字典可能无界增长。

**后续跟进:** 若租户数超过 ~1000, 考虑引入 LRU 淘汰策略或基于 TTL 的清理。目前每个租户的内存占用很小 (一个 orchestrator + 一个 RetrieveService + 一个 CoreClient)。

### 4.2 Shadow 对比仅记录日志 (无指标)

`_shadow_compare` 在 Jaccard < 0.9 时记录 warning 日志, 但未发射指标。在生产环境中, 需要一个指标 (如 `shadow_jaccard_low_total`) 用于告警。

**后续跟进:** 在未来的可观测性批次中为 shadow 对比失败添加 Prometheus 计数器。

### 4.3 `_load_history` DB 回退使用 `list_session_messages` (最老 N 条)

当 Redis 缓存为空时, `_load_history` 回退到 DB `list_session_messages`, 返回最老的 N 条消息 (按 `created_at ASC`)。这与 Redis 路径 (`list_recent_messages` 返回最近 N 条) 不一致。Plan (§8A 第 1610 行) 指出这是 best-effort 回退; 主路径是 Redis。

**后续跟进:** 考虑在 DB repository 中添加 `list_recent_session_messages` 以保持一致性。低优先级, 因为 Redis 是主路径且 DB 回退很少触发。

---

## 5. 验证命令

```bash
# kb-service 完整测试套件 (含新增 query_orchestrator 测试)
python -m pytest services/kb-service/tests/ -v --tb=short
# 结果: 208 passed (现有) + 20 个新 test_query_orchestrator 测试

# QueryOrchestrator 测试 (新增)
python -m pytest services/kb-service/tests/test_query_orchestrator.py -v --tb=short
# 结果: 20 passed

# rag-engine 测试 (未变更)
python -m pytest ai/rag-engine/tests/ -q --tb=no
# 结果: 284 passed, 8 skipped

# Go vector store 测试 (未变更)
go test ./pkg/adapters/runtime/... -run VectorStore -count=1 -v
# 结果: 18 passed

# 架构守卫
python scripts/validate_component_imports.py
# 结果: component import guard passed
```

---

## 6. 验收标准状态

| # | 标准 | 状态 |
|---|------|------|
| 1 | `config.py` 新增 `kb_query_use_new_path: bool = False` + `kb_query_shadow_mode: bool = False` | 完成 |
| 2 | `grpc_server.py` Query RPC 按 flag 选择路径 | 完成 |
| 3 | shadow 模式：主路径返回后 fire-and-forget 异步对比 | 完成 |
| 4 | 实现 `QueryOrchestrator`：实现 `QueryOrchestratorProtocol` 接口 | 完成 (协议合规测试通过) |
| 5 | 无结果闸门 ①：检索为空 → NO_RESULT_ANSWER + tokens=0, LLM 未调用 | 完成 (test_gate1) |
| 6 | 无结果闸门 ②：max_score < threshold → NO_RESULT_ANSWER + tokens=0, LLM 未调用 | 完成 (test_gate2) |
| 7 | 无结果闸门 ③：dedup 后 sources 为空 → NO_RESULT_ANSWER + tokens=LLM实际值 | 完成 (test_gate3 — 安全网, 实际不可达) |
| 8 | 多轮会话：history 含当前轮 user + Generate 末尾追加 question (user 出现两次) | 完成 (test_current_turn_user_appears_twice, test_second_turn_history_includes_first_turn) |
| 9 | `_load_history` 改用 `LRANGE key -limit -1` (最近 N 条) | 完成 (list_recent_messages, test_load_history_from_redis_cache) |
| 10 | `NO_RESULT_ANSWER` 与旧 qa_service.py 一致 | 完成 (test_no_result_answer_value) |
| 11 | token 计数：Generate RPC 用 response.usage | 完成 (test_tokens_from_generate_response, test_tokens_zero_when_llm_not_called) |
| 12 | flag=false：现有测试全通过 | 完成 (208 passed) |
| 13 | flag=true：新路径单测通过 | 完成 (20 tests passed) |
| 14 | 无结果测试：三道闸门 ①② tokens=0 LLM 未调用；③ tokens=LLM实际值 | 完成 |
| 15 | 多轮测试：第 2 轮 history 含第 1 轮 + 第 2 轮 user；Generate 末尾追加 question | 完成 |
| 16 | prompt 等价测试：system prompt / refine / CompactAndRefine | 由 #034 契约测试覆盖 (test_generate_rpc_contract) |

---

## 7. 审查后修复 (来自 #034 review-it, 延入 #035)

以下修复在 #034 审查阶段发现, 属于本批次未提交变更的一部分:

| ID | 文件 | 问题 | 修复 |
|----|------|------|------|
| **H2** | `grpc_server.py` | `logger` 未定义 — 运行时 NameError | 添加 `import logging` + 模块级 `logger` |
| **H1** | `grpc_server.py` | DeleteKB/DeleteDocument 使用派生的 `_vector_store_name(kb_id)` 而非持久化的 `vector_store_id` UUID | 改为 `kb_repo.get_kb()` → 读取 `vector_store_id` → 传 UUID 给 Core API |
| **R1** | `grpc_server.py` | `except CoreAPIError: pass` 静默吞掉错误, 未捕获 `httpx.RequestError` | 改为 `except (CoreAPIError, httpx.RequestError) as e:` + `logger.warning(...)` |
| **R2** | `grpc_server.py` | `_default_session_cache` 有冗余的局部 `import logging` + `logger` | 移除; 使用模块级 `logger` |
| **M2** | `core_api/client.py` | `filter_expr: dict[str, str] | None` — 协议定义 `str | None` | 类型改为 `str | None` |
| **M3** | `rag_engine/client.py` | `embed(self, texts: list[str])` — 协议定义 keyword-only `embed(*, texts:)` | 改为 `embed(self, *, texts: list[str])` |
| **R3** | `test_core_client.py` | `filter_expr={"doc_id": "abc"}` 与 M2 修复矛盾 | 改为 `filter_expr='doc_id == "abc"'` |
