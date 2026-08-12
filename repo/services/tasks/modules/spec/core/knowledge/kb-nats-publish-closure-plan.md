# kb-service 消息通道（NATS）发布侧收口方案

> 关联：`repo/development-records/kb-architecture-compliance-plan.md` 改动 2（kb-service 消息通道 NATS 收口）第一阶段
> 目标：kb-service 的 outbox dispatcher 从直连 NATS（nats-py）改为经 Core 事件 API（`POST /events`）发布；移除 `nats_url` 直连与 `nats-py` 依赖。
> 范围：**只收 kb-service 发布侧**；rag-engine 消费侧不在本次（留到 kb-architecture-compliance-plan 改动 4-3 与数据面一并收口）。
> 状态：方案稿（未实现）
> 审查依据：CLAUDE.md §3（Services 只能经 Core OpenAPI/SDK 调用 Core）、§5.3（业务服务禁止直连 NATS JetStream SDK）

---

## 1. 现状

### 1.1 直连点

| 位置 | 文件 | 行为 |
|---|---|---|
| NATS 建连 | [main.py#L131-L145](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/main.py#L131-L145) `_build_nats_client` | `from nats import connect; await nats_connect(settings.nats_url, name="kb-service-outbox")` |
| 发布 | [dispatcher.py#L209](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/outbox/dispatcher.py#L209) | `await self._nats.publish(self._subject, payload_str.encode("utf-8"))` 到 `ani.tasks.kb.parse` |
| 懒重连 | [dispatcher.py#L176-L188](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/outbox/dispatcher.py#L176-L188) | `nats_connect` 回调，NATS 宕机时自愈 |
| 配置 | [config.py#L33-L34](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/core/config.py#L33-L34) | `nats_url: str = "nats://localhost:4222"`、`nats_parse_subject: str = "ani.tasks.kb.parse"` |
| 依赖 | [requirements.txt#L13](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/requirements.txt#L13) | `nats-py==2.9.0` |

### 1.2 outbox 链路（本次保留不动）

```
NotifyDocumentUploaded (gRPC)
  └─ /data/query 原子写 outbox_events (grpc_server.py#L521-L536)
       └─ outbox_events(published=FALSE)
            └─ OutboxDispatcher 轮询 (dispatcher.py)
                 ├─ list_undispatched  (role=service, 跨租户)
                 ├─ nats.publish(ani.tasks.kb.parse, payload)  ← 本次替换这一跳
                 └─ mark_dispatched_batch (role=service)
```

- 事件写入：[grpc_server.py#L521-L536](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/api/grpc_server.py#L521-L536) `INSERT INTO outbox_events`，走 Core `/data/query`，原子随业务写提交。
- 轮询/标记：[outbox.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/repositories/outbox.py) `list_undispatched` / `mark_dispatched_batch`，走 Core 数据面 `role=service` 跨租户。
- 消费侧：rag-engine [parse_worker.py](file:///c:/Users/PC/Desktop/ANI/repo/ai/rag-engine/app/workers/parse_worker.py) 仍直连 NATS 订阅 `ani.tasks.kb.parse`，本次不动。

### 1.3 架构违规

CLAUDE.md §3 要求 Services 只能经 Core OpenAPI/SDK 调用 Core；§5.3 要求业务服务禁止直接依赖 NATS JetStream SDK。当前 kb-service 在 `main.py` 直接 `nats.connect`、在 `dispatcher.py` 直接 `nats.publish`，属明确违规，必须收口。

---

## 2. Core 侧现状（可复用能力）

### 2.1 已具备（Go 侧，进程内）

- **MessageBus 端口**：[message_bus.go](file:///c:/Users/PC/Desktop/ANI/repo/pkg/ports/message_bus.go) `Publish(ctx, EventEnvelope, PublishOptions)` / `Subscribe`。
- **NATS adapter**：[pkg/adapters/nats/message_bus.go](file:///c:/Users/PC/Desktop/ANI/repo/pkg/adapters/nats/message_bus.go) 实现 `MessageBus`，把 `EventEnvelope` 元数据写入 NATS Header，统一 ack/nak。
- **连接与 Stream 编排**：[bootstrap/nats.go](file:///c:/Users/PC/Desktop/ANI/repo/pkg/bootstrap/nats.go) `connectNATS` + `ensureStreams`（`ANI_TASKS` 走 WorkQueuePolicy，`ANI_EVENTS` 走 InterestPolicy）。
- **依赖装配**：[bootstrap/deps.go#L256](file:///c:/Users/PC/Desktop/ANI/repo/pkg/bootstrap/deps.go#L256) `MessageBus: natsadapter.NewMessageBus(js, ...)` 注入到 `Capabilities`。
- **Subject 常量**：[pkg/nats/messages.go#L21-L27](file:///c:/Users/PC/Desktop/ANI/repo/pkg/nats/messages.go#L21-L27) `SubjectKBParse = "ani.tasks.kb.parse"` 等。

### 2.2 缺失（本次需新增）

Core OpenAPI（`repo/api/openapi/v1.yaml`）**没有"发布事件到消息总线"的 HTTP 端点**。现有端点只有：
- `/data/query`、`/data/tables`（数据面，kb-service 已用于 outbox 读写）
- `/notifications/email/*`（邮件通知，与消息总线无关）
- `/instances/{id}/events`（实例事件查询，非发布）

→ 需新增 `POST /events` 端点，让 Services 经 HTTP 发布事件，Core handler 转交 `MessageBus.Publish`。

---

## 3. 方案

### 3.1 总体

```
kb-service OutboxDispatcher
  └─ CoreClient.publish_event(subject, payload, tenant_id, idempotency_key)
       └─ POST /events  (经 ani-gateway /api/v1)
            └─ Core handler: MessageBus.Publish(EventEnvelope, PublishOptions{Subject})
                 └─ NATS JetStream publish → ani.tasks.kb.parse
                      └─ rag-engine parse_worker 订阅（本次不动）
```

- **outbox 韧性保留**：dispatcher 仍轮询 outbox_events、批量 mark_dispatched；只把最后一跳从 `nats-py` 换成 Core HTTP。
- **at-least-once 不变**：Core `POST /events` 返回非 2xx 时事件不 mark，下一轮重试；rag-engine 幂等性由 `idempotency_key=event_id` 保证。
- **消费侧零影响**：subject 与 payload 格式不变，rag-engine 仍订阅 `ani.tasks.kb.parse`。

### 3.2 Core 层改动（先改契约）

#### 3.2.1 `repo/api/openapi/v1.yaml` — 新增 `POST /events`

```yaml
  /events:
    post:
      operationId: publishEvent
      tags: [Events]
      summary: Publish an event to the message bus
      description: |
        Publishes an event to the Core-managed message bus (NATS JetStream).
        Services MUST use this endpoint instead of connecting to NATS
        directly (CLAUDE.md §3, §5.3). Subject is constrained to the
        canonical allowlist defined in pkg/nats/messages.go.
      parameters:
        - $ref: '#/components/parameters/IdempotencyKeyQuery'
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/PublishEventRequest' }
      responses:
        '202':
          description: Accepted
          content:
            application/json:
              schema: { $ref: '#/components/schemas/PublishEventResponse' }
        '422': { $ref: '#/components/responses/UnprocessableEntity' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
      x-ani-rbac-scope: events:publish
```

Schema：

```yaml
  PublishEventRequest:
    type: object
    required: [subject, payload, tenant_id]
    properties:
      subject:
        type: string
        enum:
          - ani.tasks.kb.parse
          - ani.tasks.kb.index
          - ani.tasks.inference.deploy
          - ani.tasks.inference.delete
          - ani.tasks.model.import
          - ani.events.task.completed
        description: |
          Canonical subject allowlist (pkg/nats/messages.go constants).
          Unknown subjects are rejected with 422.
      payload:
        type: object
        description: Event payload as a JSON object; struct shape per subject.
      tenant_id:
        type: string
        format: uuid
      aggregate_id:
        type: string
        format: uuid
      aggregate_type:
        type: string
      event_type:
        type: string
      idempotency_key:
        type: string
        description: Deduplication key; clients MUST reuse on retry.

  PublishEventResponse:
    type: object
    properties:
      accepted: { type: boolean }
      subject: { type: string }
```

- **subject 用枚举 allowlist**，参照 [messages.go#L21-L27](file:///c:/Users/PC/Desktop/ANI/repo/pkg/nats/messages.go#L21-L27)；未知 subject 返回 422。
- RBAC scope：`events:publish`。
- `idempotency_key` 同时走 query param（Core 现有幂等规范）与 body（便于 Services 客户端）。

#### 3.2.2 Core Gateway handler

- 注入已装配的 `ports.MessageBus`（[deps.go#L256](file:///c:/Users/PC/Desktop/ANI/repo/pkg/bootstrap/deps.go#L256) 已有）。
- `publishEvent` handler：
  1. 校验 subject ∈ allowlist（OpenAPI enum 已约束，handler 做防御性二次校验）。
  2. 组装 `ports.EventEnvelope{TenantID, AggregateID, AggregateType, EventType, Payload, OccurredAt: now}`。
  3. 组装 `ports.PublishOptions{Subject, Key: idempotency_key}`。
  4. 调 `bus.Publish(ctx, envelope, opts)`。
  5. 成功 → `202 Accepted`；NATS 不可达 → `503`（kb-service dispatcher 保留事件、下一轮重试）；subject 不合法 → `422`。

#### 3.2.3 SDK 重新生成

- 跑 `make gen-core-sdk`，确保 Python/Go/TypeScript/Java SDK 暴露 `publishEvent`。
- 过 `make validate-architecture`。

### 3.3 kb-service 层改动

#### 3.3.1 [client.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/core_api/client.py) — 新增 `publish_event`

在 data plane 段（`create_table` 之后）追加：

```python
async def publish_event(
    self,
    *,
    subject: str,
    payload: dict[str, Any],
    tenant_id: str | None = None,
    idempotency_key: str | None = None,
    aggregate_id: str | None = None,
    aggregate_type: str | None = None,
    event_type: str | None = None,
) -> dict[str, Any]:
    """POST /events — publish an event via the Core message bus.

    The Core handler maps subject→NATS JetStream publish using the
    canonical payload structs (pkg/nats/messages.go). Subject is
    constrained to the allowlist in the OpenAPI schema; unknown
    subjects are rejected by Core with 422.

    On 503 (NATS unavailable at Core), the caller (outbox dispatcher)
    must NOT mark the event as published — it will be retried on the
    next poll (at-least-once, SPEC §7.3).
    """
    body: dict[str, Any] = {
        "subject": subject,
        "payload": payload,
        "tenant_id": tenant_id or self._tenant_id,
    }
    if idempotency_key:
        body["idempotency_key"] = idempotency_key
    if aggregate_id:
        body["aggregate_id"] = aggregate_id
    if aggregate_type:
        body["aggregate_type"] = aggregate_type
    if event_type:
        body["event_type"] = event_type
    resp = await self._client.post("/events", json=body)
    if resp.status_code not in (200, 202):
        raise _to_error(resp, "publishEvent")
    return resp.json()
```

#### 3.3.2 [dispatcher.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/outbox/dispatcher.py) — 换最后一跳

**`__init__`（第 67-87 行）**：删除 `nats_client`、`nats_connect` 参数与字段：

```python
def __init__(
    self,
    *,
    core_client: CoreClient,
    subject: str = "ani.tasks.kb.parse",
    batch_size: int = DEFAULT_BATCH_SIZE,
    poll_interval: float = DEFAULT_POLL_INTERVAL_SECONDS,
) -> None:
    self._core = core_client
    self._subject = subject
    self._batch_size = batch_size
    self._poll_interval = poll_interval
    self._task: asyncio.Task | None = None
    self._stopped = False
    self._consecutive_failures = 0
    self._last_error_logged = 0.0
```

**`_dispatch_once`（第 156-222 行）**：替换为：

```python
async def _dispatch_once(self) -> int:
    """One poll iteration: list undispatched, publish each via Core event API,
    mark in batch.

    Publishes each event via CoreClient.publish_event (POST /events) first
    (at-least-once), then marks all published events in a single batched
    UPDATE using the data plane (role="service", cross-tenant, SPEC §4.2).
    A crash between publish and mark leaves events un-dispatched →
    republished next poll; the rag-engine parse_worker MUST be idempotent
    on doc_id (module docstring, rag-engine SPEC).
    """
    rows = await outbox_repo.list_undispatched(self._core, limit=self._batch_size)
    if not rows:
        return 0
    published_ids: list[int] = []
    for row in rows:
        event_id = int(row["id"])
        payload = row.get("payload")
        if isinstance(payload, str):
            import json as _json
            payload_dict = _json.loads(payload) if payload else {}
        elif isinstance(payload, dict):
            payload_dict = payload
        else:
            payload_dict = {}
        # Merge tenant_id into the published payload so downstream
        # consumers (rag-engine parse_worker) can perform RLS-scoped writes.
        if "tenant_id" not in payload_dict and row.get("tenant_id"):
            payload_dict["tenant_id"] = str(row["tenant_id"])
        await self._core.publish_event(
            subject=self._subject,
            payload=payload_dict,
            tenant_id=str(row["tenant_id"]) if row.get("tenant_id") else None,
            aggregate_id=str(row["aggregate_id"]) if row.get("aggregate_id") else None,
            aggregate_type=row.get("aggregate_type"),
            event_type=row.get("event_type"),
            idempotency_key=str(event_id),
        )
        published_ids.append(event_id)
    await outbox_repo.mark_dispatched_batch(self._core, event_ids=published_ids)
    return len(published_ids)
```

**删除**：
- 第 97-102 行 `nats_client` property。
- 第 114-119 行 `stop()` 里的 `self._nats.drain()` 段。
- 第 176-188 行 lazy (re)connect 分支。
- 第 211-217 行 except drop NATS handle 段。

**`stop()` 简化为**：

```python
async def stop(self, timeout: float | None = 5.0) -> None:
    self._stopped = True
    if self._task is not None and not self._task.done():
        self._task.cancel()
        try:
            await asyncio.wait_for(self._task, timeout=timeout)
        except (asyncio.CancelledError, asyncio.TimeoutError):
            pass
        self._task = None
```

**文件 docstring（第 1-28 行）**：把 "publish to NATS" 改为 "publish via Core event API (POST /events)"；删除 NATS 相关注释（第 14-22 行关于 nats.publish 的描述），保留 at-least-once / SPEC §7.3 韧性描述。

#### 3.3.3 [main.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/main.py) — 删除 NATS 编排

**删除**：
- 第 58 行 `_nats_client = None`。
- 第 131-145 行 `_build_nats_client` 函数。
- `lifespan` 第 231 行 `_nats_client = await _build_nats_client()`。
- 第 235-237 行 `_nats_connect` 闭包。
- 第 266-276 行 NATS drain 段。

**`lifespan` 里 `OutboxDispatcher` 构造（第 239-244 行）改为**：

```python
_outbox_dispatcher = OutboxDispatcher(
    core_client=_outbox_core,
    subject=settings.nats_parse_subject,
)
_outbox_dispatcher.start()
logger.info("outbox dispatcher started (subject=%s)", settings.nats_parse_subject)
```

- `readyz` 当前 `ready` 字典无 NATS 字段，无需改；日志若提及 NATS 连接状态需去掉。
- `lifespan` 全局声明（第 211 行）去掉 `_nats_client`。

#### 3.3.4 [config.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/core/config.py)

- 删第 33 行 `nats_url: str = "nats://localhost:4222"`。
- 保留第 34 行 `nats_parse_subject: str = "ani.tasks.kb.parse"`（作为 subject 参数传给 Core 端点，值不变）。
- 第 32 行注释 `# NATS (outbox dispatch) — maps to env NATS_URL` 改为 `# Event publish subject (outbox dispatch via Core POST /events)`。

#### 3.3.5 [requirements.txt](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/requirements.txt)

- 删第 13 行 `nats-py==2.9.0`。

#### 3.3.6 `.env.example`

- 删 `NATS_URL=nats://localhost:4222` 行（若 kb-service 专用；若被 rag-engine 共用则保留并标注 rag-engine 仍需）。
- 本次只收 kb-service，rag-engine 仍需 `NATS_URL`，故 `.env.example` 里 `NATS_URL` 保留，仅在 kb-service 的 `config.py` 中删除该字段。

#### 3.3.7 测试

- **dispatcher 单测**：
  - `nats_client` mock 替换为对 `CoreClient.publish_event` 的 mock（AsyncMock）。
  - 断言 `publish_event` 调用参数含 `subject="ani.tasks.kb.parse"`、`payload` 含 `tenant_id`、`idempotency_key=str(event_id)`。
  - 断言 `publish_event` 抛异常时事件不被 `mark_dispatched_batch` 标记（at-least-once）。
  - 断言 `publish_event` 成功后 `mark_dispatched_batch` 被调用。
- **main.py lifespan 测试**：
  - 移除对 `_build_nats_client` 的期望与 NATS drain 期望。
  - 断言 `OutboxDispatcher` 构造参数不含 `nats_client`/`nats_connect`。
- 过 `make test`、`make validate-architecture`、`git diff --check`。

### 3.4 消费侧（不在本次，留痕）

- rag-engine [parse_worker.py](file:///c:/Users/PC/Desktop/ANI/repo/ai/rag-engine/app/workers/parse_worker.py) 仍直连 NATS 订阅 `ani.tasks.kb.parse`，本次不动。
- 后续与 kb-architecture-compliance-plan 改动 4-3（parse_worker 跨界直写 kb_chunks）+ 改动 1（数据面）协同收口时，新增 Core `GET /events/consume` + `POST /events/subscriptions` 端点，把 rag-engine 消费侧也收口到 Core 事件通道。

---

## 4. 验收标准

1. kb-service 进程内无 `nats-py` import：`grep -rn "import nats\|from nats" app/ main.py` 无结果。
2. `requirements.txt` 无 `nats-py`。
3. dispatcher 发布路径：`OutboxDispatcher._dispatch_once` → `CoreClient.publish_event` → `POST /events` → Core `MessageBus.Publish` → NATS JetStream。
4. outbox 韧性不变：Core 返回 503 时事件不 mark，下一轮重试；`idempotency_key=event_id` 保证消费端幂等。
5. `config.py` 无 `nats_url`；`nats_parse_subject` 保留作为 subject 参数。
6. `make validate-services`、`make validate-architecture`、kb-service 单测全绿。
7. `git diff --check` 无空白错误。

---

## 5. 文档闭环（Feature batch 四文件）

按 CLAUDE.md §6.3，本批次属 Feature batch（新增 Core API 端点 + kb-service 发布路径变更），完成时需更新：

1. `repo/development-records/{批次名}.md` — 本文件（或追加实现记录）。
2. `repo/development-records/README.md` — 批次索引追加一行。
3. `repo/CURRENT-SPRINT.md` — 当前 Sprint 进度更新。
4. `ANI-06-开发计划.md` — Section 零或当前 Sprint 条目更新。

另外在 `kb-architecture-compliance-plan.md` 改动 2 标注"发布侧已落地，消费侧待改动 4-3"。

---

## 6. 风险与取舍

| 项 | 说明 | 缓解 |
|---|---|---|
| 多一跳 HTTP | 每条发布从 `nats-py` 直发变为 kb-service→gateway→NATS | outbox 批量轮询已降低频率；HTTP 跳数与现有 `/data/query` 一致；NATS adapter 在 Core 进程内，无额外网络跳 |
| Core 端点新增成本 | 需走"先改契约再改实现"流程，涉及 `v1.yaml`、handler、SDK 生成物 | 这是 Feature batch 标准流程；`MessageBus` 端口与 adapter 已具备，handler 只做映射 |
| 消费侧暂未收口 | rag-engine 仍直连 NATS 订阅 | 本次范围明确只收发布侧；消费侧收口需与 parse_worker 跨界直写、数据面协同，属改动 4-3，单独立项 |
| subject allowlist 维护 | 新增 subject 需改 OpenAPI enum + 重新生成 SDK | 参照 [messages.go](file:///c:/Users/PC/Desktop/ANI/repo/pkg/nats/messages.go) 常量集，单一真实来源；新增 subject 本就该走契约变更 |

---

## 7. 执行顺序

1. **Core 先行**：`v1.yaml` 新增 `POST /events` + schema → Core handler 实现（注入 `MessageBus`）→ SDK 重新生成 → `make validate-architecture`。
2. **kb-service 跟进**：`client.py` 加 `publish_event` → `dispatcher.py` 换发布路径 → `main.py` 删 NATS 编排 → `config.py` 删 `nats_url` → `requirements.txt` 删 `nats-py` → 测试改造。
3. **联调**：kb-service 启动 → `NotifyDocumentUploaded` 触发 → outbox_events 写入 → dispatcher 轮询 → `POST /events` → rag-engine 收到 `ani.tasks.kb.parse`（验证消费端无影响）。
4. **验收**：执行第 4 节全部标准。
5. **文档闭环**：执行第 5 节四文件更新。
