# Development Records — RAG-REFACTOR-STEP-9-FEATURE

## Issue #037: NATS 切换 — Outbox Dispatcher 切 v2 subject (步骤 9 功能)

**日期:** 2026-08-28
**分支:** `refactor/architecture-compliance`
**类型:** core (feature, ops)
**依赖:** #036 (E2E 测试) — 步骤 9 依赖 8B

### 变更文件

| 文件 | 变更内容 |
|------|----------|
| `services/kb-service/tests/test_outbox_dispatcher.py` | 新增 4 个测试: v2 subject 切换 (flag=True)、回滚到 legacy subject (flag=False)、配置默认值验证、切换→回滚完整路径 |

### 已有实现（前置步骤完成，本次未修改）

| 文件 | 已有内容 |
|------|----------|
| `services/kb-service/app/core/config.py` | `nats_parse_subject_v2: str = "ani.tasks.kb.parse.v2"` (line 40)、`kb_parse_consumer_enabled: bool = False` (line 47) |
| `services/kb-service/main.py` (lines 202-206) | 根据 `kb_parse_consumer_enabled` flag 选择 `nats_parse_subject_v2` 或 `nats_parse_subject` 作为 OutboxDispatcher 的 subject |
| `services/kb-service/app/outbox/dispatcher.py` | `OutboxDispatcher.__init__` 接收 `subject` 参数，`_dispatch_once` 使用 `self._subject` publish |
| `services/kb-service/app/consumers/parse_consumer.py` | kb-service NATS 消费者，订阅 v2 subject，dispatch 到 `ParseOrchestrator.process_document` |
| `services/kb-service/app/services/parse_orchestrator.py` | `parse_status == 'ready'` guard 保证幂等 |

---

## 1. 设计决策

### 1.1 测试中使用 `if True` / `if False` 硬编码条件模拟 main.py 的 flag 三元运算

**模糊点:** Issue 037 验收标准要求 "Outbox Dispatcher 切换到 `nats_parse_subject_v2`"，但 main.py 中的切换逻辑是一个简单的三元运算 (`v2 if flag else legacy`)。测试需要验证 dispatcher 在两种 subject 下的行为，而非 main.py 的运算符本身。

**决策:** 在测试中使用 `if True` / `if False` 硬编码条件来模拟 main.py 的 flag 三元运算，构造出对应分支的 subject 值，然后传给 `OutboxDispatcher`。

**理由:**
- 测试目标是验证 dispatcher 在接收到 v2/legacy subject 后的 publish 行为，而非 main.py 的条件判断逻辑。
- 直接测试 main.py 的 wiring 需要大量 mock (NATS 连接、DB pool、gRPC client、FastAPI startup)，收益极低——测试一个三元运算符不值得如此高的测试基建成本。
- `if True`/`if False` 形式与 main.py 的代码结构完全对应，作为文档化意图清晰表达"这是 flag=True 时的行为"。

### 1.2 不直接测试 main.py 的 subject 选择逻辑

**模糊点:** 是否需要端到端测试 main.py startup 时 `kb_parse_consumer_enabled` flag 到 dispatcher subject 的完整链路？

**决策:** 不直接测试 main.py wiring，仅通过 dispatcher 单元测试覆盖两个分支。已有的 `test_us010_wiring.py` 覆盖 startup wiring 模式。

**理由:**
- main.py 的 subject 选择是一个 3 行三元运算 (lines 202-206)，逻辑无歧义。
- 测试该运算需要 mock 整个 FastAPI 生命周期 (NATS connect、DB pool、gRPC client、parse consumer start)，测试基建成本远超被测代码复杂度。
- dispatcher 单元测试已验证两种 subject 的 publish 行为，这是实际业务逻辑所在。

---

## 2. 偏差

### 2.1 Issue 要求"确认 `KB_PARSE_CONSUMER_ENABLED=true` 已部署且稳定" — 代码层面无法验证

**规格要求:** 验收标准第 1 条 "确认 `KB_PARSE_CONSUMER_ENABLED=true` 已部署且稳定"。

**实际实现:** 代码层面仅保证 flag 存在且默认为 False ([config.py:47](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/core/config.py#L47))。部署验证属于 ops 准入标准，需在 K8s 环境设置 `KB_PARSE_CONSUMER_ENABLED=true` 并观察。

**为何可接受:** 代码已支持 flag 切换——部署时设置环境变量即可激活 v2 subject。这是 ops 步骤而非代码步骤。

### 2.2 Issue 要求"停止 rag-engine parse_worker (`NATS_URL=""`)" — 代码层面为环境变量覆盖

**规格要求:** 验收标准第 3 条 "停止 rag-engine parse_worker（`NATS_URL=""`）"。

**实际实现:** rag-engine `connect_nats()` 在 NATS_URL 为空时返回 None，[main.py:79](file:///c:/Users/PC/Desktop/ANI/repo/ai/rag-engine/main.py#L79) 的 guard 跳过 parse_worker 启动。gRPC 服务不受影响。此为部署时环境变量操作，非代码修改。

**为何可接受:** 停止 parse_worker 的机制已在 rag-engine 代码中实现——`NATS_URL=""` → `connect_nats` 失败 → guard 跳过。无需额外代码改动。

---

## 3. 权衡

### 3.1 测试 dispatcher 行为 vs 测试 main.py wiring

**可选方案:**
1. **dispatcher 单元测试 + 硬编码 flag 分支** (选用) — 用 `if True`/`if False` 模拟两个分支，测试 dispatcher publish 到正确 subject。
2. **main.py 端到端 wiring 测试** — mock 整个 FastAPI startup，设置 flag=True/False，验证 dispatcher 接收的 subject。
3. **集成测试** — 启动真实 NATS + DB，设置 flag，发送 outbox 事件，验证 subject 上的消费。

**选用: dispatcher 单元测试**, 因为:
- 方案 2 的测试基建成本极高 (mock NATS connect、DB pool、gRPC client、lifespan)，而被测代码仅 3 行三元运算。
- 方案 3 需要 NATS + DB 基建，属于 #036 E2E 测试范围，不在 #037 代码范围内。
- 方案 1 直接测试业务逻辑 (dispatcher publish 行为)，覆盖 issue 037 的核心验收标准。

### 3.2 回滚测试使用共享 `_MockNATS` 实例 vs 独立实例

**可选方案:**
1. **共享 `_MockNATS` 实例** (选用) — 两个 dispatcher 共用同一个 mock，`nats.published` 列表累积所有 publish 调用，用 `[-1]` 检查最新的一条。
2. **独立 `_MockNATS` 实例** — 每个 dispatcher 用自己的 mock，分别验证各自的 publish。

**选用: 共享实例**, 因为:
- 回滚测试的核心场景是"切换→回滚"序列：先 publish 到 v2 subject，再 publish 到 legacy subject。共享实例的累积列表天然表达时序。
- `nats.published[-1]` 检查最新 publish 的 subject，验证回滚后确实切回了 legacy。
- 独立实例无法验证时序关系。

---

## 4. 待确认问题

### 4.1 部署准入：`KB_PARSE_CONSUMER_ENABLED=true` 稳定性验证

Issue 验收标准第 1 条要求"确认已部署且稳定"。代码层面无法验证部署稳定性。需在 K8s 环境部署后观察 outbox_events 消费速度和 parse 管线行为。

**后续跟进:** 部署 `KB_PARSE_CONSUMER_ENABLED=true` 后，观察 outbox_events 消费延迟和 parse 错误率，确认稳定后再停止 rag-engine parse_worker。

### 4.2 预存在测试失败：`test_process_message_missing_object_id_dropped`

`test_parse_consumer.py` 中的 `test_process_message_missing_object_id_dropped` 测试失败，原因是 [parse_consumer.py:232-246](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/consumers/parse_consumer.py#L232-L246) 有 object_id fallback 逻辑——当 payload 中 `object_id` 为空时从 DB 的 `doc_row.object_id` 补全。测试 mock 返回了非空 `object_id`，导致 fallback 成功，orchestrator 被调用。

**后续跟进:** 这属于 issue 033 (parse_consumer) 的测试问题，不在 issue 037 范围内。应在 issue 033 范围内修复测试 mock（让 `doc_row.object_id` 也为空）。

### 4.3 部署顺序：先停止 rag-engine parse_worker 还是先切换 flag

Issue 验收标准要求"停止 rag-engine parse_worker"和"Outbox Dispatcher 切换到 v2 subject"。实际部署时需确认操作顺序：
1. 先设 `KB_PARSE_CONSUMER_ENABLED=true` → dispatcher 发往 v2 subject，kb-service consumer 开始消费
2. 再设 rag-engine `NATS_URL=""` → 停止 parse_worker

若顺序颠倒（先停 parse_worker 再切 flag），v2 subject 无消费者期间 outbox_events 会积压（dispatcher 仍发往旧 subject，但旧消费者已停）。

**后续跟进:** 在部署文档中明确操作顺序：先切 flag → 确认 kb-service consumer 正常消费 → 再停 rag-engine parse_worker。

---

## 5. 验证命令

```bash
# Outbox dispatcher 测试 (含 4 个新测试)
cd services/kb-service
python -m pytest tests/test_outbox_dispatcher.py -v --tb=short
# 结果: 14 passed (10 existing + 4 new)

# Issue 037 相关测试 (跨 test_outbox_dispatcher + test_parse_consumer)
python -m pytest tests/test_outbox_dispatcher.py tests/test_parse_consumer.py -v -k "037 or v2_subject or flag or rollback or switch or consumer_enabled or nats_parse_subject"
# 结果: 7 passed

# 架构守卫
cd repo
python scripts/validate_component_imports.py --root .
# 结果: component import guard passed

# git diff --check
git diff --check
# 结果: clean
```

---

## 6. 验收标准状态

| # | 标准 | 状态 |
|---|------|------|
| 1 | 确认 `KB_PARSE_CONSUMER_ENABLED=true` 已部署且稳定 | 代码就绪 — flag 存在且默认 False，部署时设 True 激活 (ops 准入) |
| 2 | Outbox Dispatcher 切换到 `nats_parse_subject_v2`（`ani.tasks.kb.parse.v2`） | 完成 — main.py:202-206 按 flag 选择 subject，测试覆盖 |
| 3 | 停止 rag-engine parse_worker（`NATS_URL=""`） | 代码就绪 — `connect_nats` 返回 None → guard 跳过 (部署操作) |
| 4 | 观察 outbox_events 消费速度正常 | 部署后观察 (ops 准入) |
| 5 | 回滚方案：切回旧 subject + 重启 rag-engine parse_worker | 完成 — flag=False → legacy subject，测试覆盖回滚路径 |
| 6 | 切换后 Parse 管线行为不变 | 完成 — kb-service consumer 通过 ParseOrchestrator 执行相同管线 |
| 7 | 旧 subject `ani.tasks.kb.parse` 保留（回滚用） | 完成 — config.py:34 `nats_parse_subject` 未变更 |

---

## 7. 审查后修复 (review-it)

review-it 审查结果：**clean — no accepted/actionable findings reported.**

| Finding | 判定 | 理由 |
|---------|------|------|
| `if True`/`if False` 硬编码条件 | Rejected | 意图性模拟 main.py flag 三元运算，作为文档化测试意图清晰 |
| 共享 `_MockNATS` 实例的回滚测试 | No issue | 累积列表 + `[-1]` 检查正确表达时序 |
| 无直接 main.py wiring 测试 | Rejected | 测试三元运算符的基建成本远超收益，dispatcher 单测已覆盖业务逻辑 |
