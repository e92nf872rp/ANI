# Development Records — RAG-REFACTOR-STEP-6-FEATURE

## Issue #033: kb-service NATS consumer 默认关闭

**Date:** 2026-08-20
**Branch:** `refactor/architecture-compliance`
**Type:** core (feature)
**Dependencies:** #032 (parse_orchestrator)

### Changed Files

| File | Change |
|------|--------|
| `app/consumers/__init__.py` | New — package init |
| `app/consumers/parse_consumer.py` | New — NATS consumer dispatching to `ParseOrchestrator.process_document` |
| `app/core/config.py` | `kb_parse_consumer_enabled: bool = False`, `nats_parse_subject_v2: str`, `rag_engine_grpc_addr: str` |
| `main.py` | Flag-gated consumer startup + outbox subject switching + lifecycle cleanup + readyz |
| `tests/test_parse_consumer.py` | New — 20 tests (mock NATS + orchestrator + flag) |

---

## 1. Design Decisions

### 1.1 Consumer 委托幂等性给 orchestrator，不自行去重

**Ambiguity:** Plan 步骤 6 要求 "幂等处理：重复消息不重复解析"，但未指定在哪一层实现。

**Choice:** Consumer 不做幂等检查，直接将每条消息分发给 `ParseOrchestrator.process_document`。幂等性由 orchestrator 的 `parse_status == 'ready'` 检查实现（单一真值来源）。

**Rationale:**
- 幂等逻辑集中在 orchestrator 一处，避免 consumer 和 orchestrator 重复实现
- orchestrator 已有完整的 idempotency guard（`get_document` 检查 status + `pending→parsing` UPDATE 返回值检查）
- consumer 只负责消息分发，不参与业务逻辑决策

### 1.2 Consumer 直接调用 `doc_repo` / `kb_repo` 解析元数据

**Ambiguity:** Plan 未说明 `file_type` 和 `vector_store_id` 如何获取 — outbox payload 不包含这两个字段。

**Choice:** Consumer 在 `process_message` 中通过 `doc_repo.get_document()` 和 `kb_repo.get_kb()` 从数据库解析 `file_type`、`file_name`（fallback）和 `vector_store_id`，然后传给 orchestrator。

**Rationale:**
- `file_type` 写入 `kb_documents` 表由 `GetDocumentUploadURL` 设置，outbox payload 不携带
- `vector_store_id` 写入 `knowledge_bases` 表由 `CreateKB` 设置，outbox payload 不携带
- Consumer 必须解析这些字段才能调用 orchestrator 的 `process_document`（需要这两个参数）

### 1.3 `_ParseOrchestrator` Protocol 用于解耦

**Choice:** 定义 `@runtime_checkable Protocol` 描述 consumer 依赖的 orchestrator 接口子集，而非直接导入 `ParseOrchestrator` 类。

**Rationale:**
- Consumer 依赖接口而非实现，便于单元测试注入 mock
- `runtime_checkable` 允许 `isinstance` 检查（虽然当前未使用）
- 符合项目 ports/adapters 模式

### 1.4 连接池 `max_size` 从 5 扩展到 10

**Choice:** `_outbox_pool` 的 `max_size` 从 5 增加到 10。

**Rationale:**
- 原始 `max_size=5` 仅为 outbox dispatcher 设计（1 连接/轮询）
- 启用 consumer 后，`max_concurrency=4` 个并发消息每个需要 1 连接做元数据查询 + orchestrator 6 次顺序 `acquire()`
- 最坏情况 4 (consumer) + 1 (dispatcher) = 5，`max_size=10` 留余量

---

## 2. Deviations

### 2.1 `stop()` 捕获 `asyncio.TimeoutError`（改进 rag-engine 模式）

**Spec said:** Plan 步骤 6 未指定 `stop()` 的超时处理行为。

**Implemented:** `stop()` 使用 `asyncio.wait_for(..., timeout=timeout)` 并捕获 `asyncio.TimeoutError`，取消残留任务后执行 `_pending.clear()`。rag-engine `parse_worker.stop()` 未捕获此异常 — `TimeoutError` 会跳过 `_pending.clear()` 并传播到 lifespan。

**Why better:** 防止 shutdown 超时导致异常传播和状态泄漏。

### 2.2 Consumer 启动失败时不阻塞服务启动

**Spec said:** Plan 未指定 consumer 启动失败时的行为。

**Implemented:** `main.py` 在 `try/except` 中启动 consumer，失败时 `logger.exception` + `_parse_consumer = None`，服务继续启动。

**Why:** 与 NATS 连接和 Redis 连接的 best-effort 模式一致 — 降级运行优于完全不可用。

---

## 3. Tradeoffs

### 3.1 Core NATS push subscription vs JetStream durable consumer

**Alternatives:**
1. **Core NATS push subscription** (chosen) — `nc.subscribe(subject, cb=callback)`
2. **JetStream durable consumer** — 持久化 ack + redelivery

**Chosen: Core NATS**, because:
- 与 rag-engine `parse_worker` 完全一致的模式（对称性，便于回滚）
- Outbox dispatcher 已提供 at-least-once 语义（crash 后重新发布未 ack 的事件）
- JetStream 引入额外复杂度（durable name, ack 逻辑），且过渡架构后续会移除 NATS
- **Con:** 进程崩溃时 in-flight 消息丢失，但 outbox 重发弥补此缺口

### 3.2 Consumer 直接调 repo vs 通过 service 层

**Alternatives:**
1. **Consumer 直接调 `doc_repo` / `kb_repo`** (chosen)
2. **通过 service 层封装元数据解析**

**Chosen: 直接调 repo**, because:
- 项目现有模式 — orchestrator、dispatcher 都直接调 repo
- Consumer 是传输层组件，元数据解析不是业务逻辑
- 引入 service 层封装增加无意义的间接层

### 3.3 DB 查询冗余 — consumer 和 orchestrator 都调 `get_document`

**Known issue:** Consumer 调 `doc_repo.get_document()` 获取 `file_type`，orchestrator 内部也调 `get_document()` 做幂等检查。两次查询同一行。

**Why not fixed:**
- 修复需要修改 `ParseOrchestrator.process_document` 签名（让 orchestrator 从其 `get_document` 结果中解析 `file_type`/`vector_store_id`），属于 issue #032 范围
- 当前通过参数传入是正确的 workaround，功能正确，仅为性能微小损耗（1 次额外 SELECT）

---

## 4. Open Questions

### 4.1 Outbox dispatcher.py:159 注释过时

`dispatcher.py` line 159 注释写着 "NotifyDocumentUploaded only has {doc_id, kb_id, storage_path}"，但实际 outbox payload 包含 `{doc_id, kb_id, storage_path, tenant_id, file_name, object_id, chunk_size}`。注释过时但不影响功能（`tenant_id` merge 逻辑变为 no-op）。**后续应更新注释。**

### 4.2 self-publish/self-subscribe 背压

当 `kb_parse_consumer_enabled=True` 时，dispatcher 和 consumer 共享同一 NATS 连接，dispatcher 发布到 v2 subject，consumer 同进程订阅。若发布速率超过消费速率，消息在 nats-py 内部队列积累。`Semaphore(4)` 限制并发但不限制队列长度。**过渡架构限制，将在 NATS 移除计划（kb-nats-publish-closure-plan.md）中解决。**

### 4.3 集成测试缺口

无测试验证 dispatcher + consumer 共享同一 nats-py `Client` 的并发 pub/sub — 需要真实 NATS 实例，单元测试使用独立 mock。**生产 wiring（main.py:167-214）是唯一使用此模式的地方，当前未被集成测试覆盖。**

---

## 5. Verification Commands Run

```bash
# Unit tests
python -m pytest services/kb-service/tests/ -q
# Result: 202 passed

# Focused tests
python -m pytest services/kb-service/tests/test_parse_consumer.py tests/test_parse_orchestrator.py tests/test_outbox_dispatcher.py -q
# Result: 61 passed (20 parse_consumer + 26 parse_orchestrator + 15 outbox_dispatcher)

# Architecture guard
python scripts/validate_component_imports.py --root .
# Result: component import guard passed

# Whitespace check
git diff --check
# Result: clean

# E2E P0 API test (new architecture)
python scripts/test_e2e_p0_new_arch.py
# Result: 14/14 passed — ListKBs, CreateKB, GetKB, GetDocumentUploadURL,
#         MinIO upload, NotifyDocumentUploaded, parse (md: 34 chunks, txt: 4 chunks),
#         ListDocuments, Query (3 questions with correct answers + sources),
#         DeleteDocument, DeleteKB
```

## 6. Acceptance Criteria Status

| # | Criteria | Status |
|---|----------|--------|
| 1 | `app/consumers/parse_consumer.py` 消费 NATS parse 消息，调用 `ParseOrchestrator.process_document` | Done |
| 2 | `config.py` 新增 `kb_parse_consumer_enabled: bool = False` | Done |
| 3 | `config.py` 新增 `nats_parse_subject_v2: str = "ani.tasks.kb.parse.v2"` | Done |
| 4 | `main.py` 按 flag 启动 consumer | Done |
| 5 | Outbox Dispatcher 按 flag 切换 subject | Done |
| 6 | 旧 subject 不变，新增 v2 由 flag 切换 | Done |
| 7 | 幂等处理：重复消息不重复解析 | Done (delegated to orchestrator) |
| 8 | 新增 `test_parse_consumer.py` — mock NATS + orchestrator + flag | Done (20 tests) |
| 9 | flag=false 时 consumer 不启动，现有功能不受影响 | Done (202 tests pass) |
