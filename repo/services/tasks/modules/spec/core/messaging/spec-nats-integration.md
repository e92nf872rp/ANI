# SPEC: NATS 接入（MessageBus 健壮性 + 示例 consumer）

> Technical specification derived from:
> - PRD: `repo/services/tasks/modules/prd/core/messaging/prd-nats-integration.md`
> - UX: N/A — backend-only
> - Plan: `repo/services/tasks/modules/plan/plan-nats-integration-v2.md`（技术设计来源）
> Generated: 2026-07-29 | Product line: core | Commit: 待补

> Scope: ANI Core 基础设施 adapter/port/bootstrap 改造 + metering-service 示例 consumer（7a）
> OpenAPI authority: consume only — 本任务不涉及 OpenAPI 契约变更

---

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 规范 ANI NATS 消息总线 adapter 的健壮性修复与 port 契约扩展，使模型导入 task 流和 metering event 流共用同一健壮 adapter。核心改动：Subscribe 改业务层决定 Ack/Nak + panic recover 兜底；Publish 写入 EventEnvelope 元数据到 NATS Message Header；SubscribeOptions/Message 接口扩展可选字段；adapter 注入 logger；ANI_EVENTS stream 从 WorkQueuePolicy 改为 InterestPolicy；新增 metering 示例 consumer（7a，仅代码 + 单测，不做 main/bootstrap 接线 7b）；新增 task 流示例 consumer + 集成测试；新增 adapter 单测 + 集成测试。

### 1.2 PRD Reference

- Source: `repo/services/tasks/modules/prd/core/messaging/prd-nats-integration.md`
- Plan source: `repo/services/tasks/modules/plan/plan-nats-integration-v2.md`
- UX source: none（backend-only）
- User Stories covered: US-001 ~ US-010
- Functional Requirements covered: FR-1 ~ FR-31

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Ack/Nak 归属 | 业务层决定，adapter 只 panic 兜底 Nak | task 流需"抢租约失败→Ack 跳过"，event 流需"StartCollection 失败→Nack"，自动 Ack/Nak 无法表达两种语义 |
| Publish headers | EventEnvelope 元数据写 NATS Header | 消费侧通过 Headers() 重建租户上下文；outbox publisher 已传完整 envelope，自动受益 |
| AckWait/MaxDeliver | SubscribeOptions 可选字段，传 0 不配 | task 流传 0（死信走 DB attempt_count），event 流传 30s/10（毒丸兜底） |
| jetStream 接口 | adapter 内部接口包装，不暴露 ports | natsgo.JetStreamContext 是具体类型无法直接 mock；生产路径零开销 |
| logger 注入 | `*slog.Logger`，改 NewMessageBus 签名 | 记录 panic/error 路径；现有 ports.Logger 统一 port 不存在，用标准库 |
| ANI_EVENTS policy | InterestPolicy | event 要 fan-out 给多消费者（metering + 审计），WorkQueue 会让首个 Ack 删除消息 |
| 死信方案 | task 流 DB attempt_count；event 流 MaxDeliver 兜底 + 重启重建 | 不建死信 stream/table（计划 §7.2 理由：跨服务边界、违反最小抽象原则、无 DB 关联不可查） |
| 7a 范围 | consumer 代码 + 单测，不做 7b 接线 | metering-service 在另一分支，本分支无 main/bootstrap 入口 |
| 集成测试 | 手动验证项，不作为硬性门禁 | 依赖本地 docker-compose NATS 或 port-forward，CI 环境可能无 docker |

---

## 2. Architecture

### 2.1 System Context

```
┌─────────────────────────────────────────────────────────────┐
│                    ANI Core / Services                       │
│                                                              │
│  ┌──────────────┐   EventEnvelope    ┌──────────────────┐   │
│  │ task-service │ ─────────────────► │  NATS MessageBus  │   │
│  │ outbox       │   (with headers)   │  adapter          │   │
│  │ publisher    │                    │  (pkg/adapters/   │   │
│  └──────────────┘                    │   nats/)          │   │
│                                      │                    │   │
│  ┌──────────────┐   Subscribe        │  ┌──────────────┐  │   │
│  │ metering-    │ ◄──────────────── │  │ jetStream    │  │   │
│  │ service      │   Message+Headers │  │ (natsgo.JS)  │  │   │
│  │ eventconsumer│                    │  └──────────────┘  │   │
│  └──────────────┘                    └─────────┬──────────┘   │
│                                                │              │
│                                                ▼              │
│                                      ┌──────────────────┐   │
│                                      │  NATS JetStream   │   │
│                                      │  ANI_TASKS (WQ)   │   │
│                                      │  ANI_EVENTS (Int) │   │
│                                      └──────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 Component Design

**改动组件**：

| 组件 | 职责 | 改动 |
|------|------|------|
| `ports.MessageBus` | 消息总线能力抽象 | 扩展 SubscribeOptions/Message 接口 |
| `adapters/nats.MessageBus` | NATS JetStream 实现 | 修复 Publish/Subscribe，注入 logger |
| `adapters/nats.jetStream` | 内部接口包装（测试工具） | 新增，不暴露 ports |
| `adapters/nats.message` | NATS Msg 包装 | 新增 Headers() |
| `bootstrap/nats.ensureStreams` | stream 初始化 | ANI_EVENTS 改 InterestPolicy |
| `metering-service/eventconsumer.Consumer` | 示例 consumer | 新增（7a） |

**边界约束**：
- `jetStream` 接口仅存在于 `pkg/adapters/nats/` 内部，不暴露到 `pkg/ports/`
- consumer 在 `services/metering-service/internal/eventconsumer/`，不跨服务调用 Core 内部包
- consumer 只依赖 `ports.MessageBus`，不 import `pkg/adapters/nats`

### 2.3 Module Interactions

**Publish 链路（outbox publisher → adapter → NATS）**：
```
outbox_publisher.Publish(ctx, EventEnvelope{...}, PublishOptions{...})
  → adapter.Publish
    → 构造 natsgo.Header{tenant-id, aggregate-id, aggregate-type, event-type, occurred-at}
    → js.PublishMsg(&natsgo.Msg{Subject, Data, Header}, pubOpts...)
  → NATS JetStream ANI_EVENTS stream
```

**Subscribe 链路（adapter → handler）**：
```
adapter.Subscribe(ctx, opts, handler)
  → js.Subscribe/QueueSubscribe(handlerFunc, ManualAck, Durable, MaxAckPending, AckWait?, MaxDeliver?)
  → 消息到达 → handlerFunc(msg)
    → defer { recover → logger.Error + msg.Nak() }
    → handler(ctx, message{msg})
      → 业务层决定 msg.Ack/Nack
    → handler 返回 error → logger.Warn（不自动 Ack/Nak）
```

**Consumer 链路（metering example）**：
```
Consumer.Start(ctx)
  → bus.Subscribe(ctx, SubscribeOptions{Subject:"ani.events.instance.>", AckWait:30s, MaxDeliver:10, ...}, c.handle)
  → c.sub = sub

Consumer.handle(ctx, msg)
  → tenantID = msg.Headers()["tenant-id"][0]
  → json.Unmarshal(msg.Data(), &event)
    → 失败（毒丸）→ msg.Ack(ctx) + logger.Error
    → 成功 → logger.Info + msg.Ack(ctx)（示例阶段）
```

### 2.4 File Structure

```
repo/
├── pkg/
│   ├── ports/
│   │   └── message_bus.go                    [MODIFY: SubscribeOptions+AckWait/MaxDeliver, Message+Headers]
│   ├── adapters/
│   │   └── nats/
│   │       ├── message_bus.go                [MODIFY: logger 注入, Publish 写 headers, Subscribe 业务层 Ack/Nak + panic recover]
│   │       ├── message_bus_test.go           [NEW: 单元测试]
│   │       ├── integration_test.go            [NEW: 集成测试, //go:build integration]
│   │       └── jetstream_iface.go             [NEW: 内部 jetStream 接口]
│   └── bootstrap/
│       └── nats.go                           [MODIFY: ANI_EVENTS → InterestPolicy]
└── services/
    └── metering-service/
        └── internal/
            └── eventconsumer/
                ├── consumer.go               [NEW: 示例 consumer]
                ├── consumer_test.go          [NEW: consumer 单测]
                └── integration_test.go        [NEW: Consumer 端到端集成测试, //go:build integration]
    └── task-service/
        └── internal/
            └── taskconsumer/
                ├── consumer.go               [NEW: task 流示例 consumer]
                ├── consumer_test.go          [NEW: task consumer 单测]
                └── integration_test.go        [NEW: task 流集成测试, //go:build integration]
```

> 注：`services/metering-service/` 在本分支可能尚不存在，需创建目录结构。`services/task-service/` 已存在。7b 接线（main/bootstrap goroutine 注入）不在本 SPEC 范围。

---

## 3. Data Model

### 3.1 Schema Changes

无数据库 schema 变更。本任务为 adapter/port/internal 改造，不涉及持久化层。

### 3.2 Entity Definitions

**port 契约扩展（pkg/ports/message_bus.go）**：

```go
// SubscribeOptions 扩展（新增 AckWait/MaxDeliver，零值兼容）
type SubscribeOptions struct {
    Subject     string
    Consumer    string
    Queue       string
    MaxInflight int
    AckWait     time.Duration  // 新增：传 0 不配置
    MaxDeliver  int            // 新增：传 0 不配置
}

// Message 接口扩展（新增 Headers）
type Message interface {
    Subject() string
    Data() []byte
    Ack(ctx context.Context) error
    Nack(ctx context.Context) error
    Headers() map[string][]string  // 新增
}
```

**EventEnvelope 不变**（现有结构已满足需求，Publish 内部写 headers）。

**adapter 内部接口（pkg/adapters/nats/jetstream_iface.go）**：

```go
// jetStream 是 natsgo.JetStreamContext 的内部接口包装，仅用于 adapter 和单测。
// natsgo.JetStreamContext 天然满足此接口，生产路径零开销。
type jetStream interface {
    PublishMsg(msg *natsgo.Msg, opts ...natsgo.PubOpt) (*natsgo.PubAck, error)
    Subscribe(subj string, cb natsgo.MsgHandler, opts ...natsgo.SubOpt) (*natsgo.Subscription, error)
    QueueSubscribe(subj, queue string, cb natsgo.MsgHandler, opts ...natsgo.SubOpt) (*natsgo.Subscription, error)
    StreamInfo(stream string, opts ...natsgo.RequestOpt) (*natsgo.StreamInfo, error)
    AddStream(cfg *natsgo.StreamConfig, opts ...natsgo.RequestOpt) (*natsgo.StreamInfo, error)
}
```

**consumer 实体（services/metering-service/internal/eventconsumer/consumer.go）**：

```go
type Consumer struct {
    bus    ports.MessageBus
    logger *slog.Logger
    sub    ports.Subscription
}

// instanceEvent 示例用 payload 结构，真实结构后续 PR 定义
type instanceEvent struct {
    EventType  string `json:"event_type"`
    InstanceID string `json:"instance_id"`
}
```

### 3.3 Relationships

- `Consumer` 依赖 `ports.MessageBus`（不 import adapter）
- `adapters/nats.MessageBus` 依赖 `jetStream` 内部接口 + `*slog.Logger`
- `natsgo.JetStreamContext` 满足 `jetStream` 接口（隐式实现）

### 3.4 Migration Plan

无数据库迁移。唯一"迁移"是运行时 stream policy 变更：

| 步骤 | 操作 | 回滚 |
|------|------|------|
| 1 | 改 bootstrap/nats.go ANI_EVENTS → InterestPolicy | 改回 WorkQueuePolicy |
| 2 | 若 NATS 已有 WorkQueuePolicy 的 ANI_EVENTS | `nats stream rm ANI_EVENTS -f` 后重启 Core，ensureStreams 重建 |
| 3 | ANI_TASKS 不变 | 无需回滚 |

> 现状 `message_bus.go Subscribe` 从未被调用（计划 §11），改 ANI_EVENTS policy 无破坏性影响。

---

## 4. API Design

### 4.1 OpenAPI Change Plan (Core only)

| Change | operationId | Compatibility | idempotency_key |
|--------|-------------|---------------|-----------------|
| 无 | N/A | N/A | N/A |

本任务为基础设施 adapter/port/internal 改造，不涉及任何 OpenAPI 契约变更（Core v1.yaml、Services services/v1.yaml 均不动）。

### 4.2 Frozen Facts Table

| Item | Status |
|------|--------|
| Frozen Paths | N/A（不触碰 v1.yaml operationId） |
| Frozen Schemas | N/A（不触碰 v1.yaml schemas） |
| Frozen Response / Error codes | N/A |
| Non-Frozen Capabilities | adapter 内部实现、port 契约扩展（新增可选字段，零值兼容） |
| Known Risky Assumptions | metering-service 目录在本分支是否存在（见 §11.3） |

### 4.3 Internal API Contracts

**NewMessageBus 签名变更**（破坏性，需同步调用点）：

| Before | After |
|--------|-------|
| `NewMessageBus(js natsgo.JetStreamContext) *MessageBus` | `NewMessageBus(js natsgo.JetStreamContext, logger *slog.Logger) *MessageBus` |

调用点清单（基于代码扫描）：
- `pkg/bootstrap/deps.go:213` — `natsadapter.NewMessageBus(js)` → 需改为 `natsadapter.NewMessageBus(js, logger)`

### 4.4 Error Responses

adapter 层 error（非 HTTP）：

| Error | Condition | Message Pattern |
|-------|-----------|-----------------|
| `fmt.Errorf` | Publish subject 为空 | `message bus publish: subject required` |
| `fmt.Errorf` | Publish 失败 | `message bus publish: %w` |
| `fmt.Errorf` | Subscribe subject 为空 | `message bus subscribe: subject required` |
| `fmt.Errorf` | Subscribe 失败 | `message bus subscribe: %w` |

---

## 5. Business Logic

### 5.1 Core Algorithms

**Publish 算法**：
```
1. 校验 opts.Subject != ""，否则返回 error
2. 构造 natsgo.Header：
   - "tenant-id":      [event.TenantID]
   - "aggregate-id":   [event.AggregateID]
   - "aggregate-type": [event.AggregateType]
   - "event-type":     [event.EventType]
   - "occurred-at":    [event.OccurredAt.Format(time.RFC3339Nano)]
3. 构造 pubOpts = [natsgo.Context(ctx)]
4. 若 opts.Key != ""，append natsgo.MsgId(opts.Key)
5. 调 js.PublishMsg(&natsgo.Msg{Subject, Data: event.Payload, Header}, pubOpts...)
6. 失败返回 fmt.Errorf("message bus publish: %w", err)
```

**Subscribe 算法**：
```
1. 校验 opts.Subject != ""，否则返回 error
2. 构造 subOpts = [natsgo.ManualAck()]
3. 若 opts.Consumer != ""，append natsgo.Durable(opts.Consumer)
4. 若 opts.MaxInflight > 0，append natsgo.MaxAckPending(opts.MaxInflight)
5. 若 opts.AckWait > 0，append natsgo.AckWait(opts.AckWait)
6. 若 opts.MaxDeliver > 0，append natsgo.MaxDeliver(opts.MaxDeliver)
7. 定义 handlerFunc(msg):
   a. defer { recover → logger.Error("handler panic") + msg.Nak() }
   b. err := handler(ctx, message{msg})
   c. 若 err != nil → logger.Warn("handler returned error; ack/nack is handler's responsibility")
      （不自动 Ack/Nak）
8. 若 opts.Queue != "" → js.QueueSubscribe(...)，否则 js.Subscribe(...)
9. 返回 subscription{sub}
```

**Consumer.handle 算法**：
```
1. tenantID = msg.Headers()["tenant-id"][0]（若存在）
2. err := json.Unmarshal(msg.Data(), &event)
3. 若 err != nil（毒丸）：
   a. logger.Error("parse event failed, ack to skip poison message")
   b. return msg.Ack(ctx)
4. logger.Info("received event", ...)
5. [示例阶段] return msg.Ack(ctx)
   [真实逻辑] c.metering.StartCollection → 失败 return msg.Nack(ctx)，成功 return msg.Ack(ctx)
```

### 5.2 Validation Rules

- `PublishOptions.Subject` 非空（adapter 校验）
- `SubscribeOptions.Subject` 非空（adapter 校验）
- `AckWait == 0` 或 `> 0`（零值不透传 natsgo.AckWait）
- `MaxDeliver == 0` 或 `> 0`（零值不透传 natsgo.MaxDeliver）
- `MaxInflight == 0` 或 `> 0`（零值不透传 natsgo.MaxAckPending，现有逻辑保持）

### 5.3 State Machine

无状态机。消息生命周期由 NATS JetStream 管理：
- Publish → stream 持久化
- Subscribe → durable consumer 各自维护消费进度
- Ack → 消息从 stream 删除（WorkQueue）或标记已消费（Interest）
- Nak → 延迟重投
- AckWait 超时 → NATS 自动重投
- MaxDeliver 满 → 停投

### 5.4 Edge Cases

| 场景 | 处理 |
|------|------|
| handler 返回 nil 但没调 Ack | 消息卡到 AckWait 超时，NATS 自动重投（不丢失） |
| handler 返回 error 但调了 Ack | 消息已处理，adapter 不干预（logger.Warn 仅记录） |
| handler panic 在 Ack 之前 | recover → msg.Nak() → 重投 |
| handler panic 在 Ack 之后 | recover → msg.Nak() 被 NATS 忽略（消息已 Ack），不重复处理 |
| payload 解析失败（毒丸） | consumer 调 Ack 跳过 + 告警（重投无意义） |
| StartCollection 重复调用 | consumer 调 Ack 幂等跳过（不是错误） |
| StartCollection 业务失败 | consumer 调 Nak 延迟重投 |
| ANI_EVENTS 已存在为 WorkQueuePolicy | bootstrap 不自动改 policy（StreamInfo 成功即跳过），需手动 `nats stream rm ANI_EVENTS -f` 后重启 |
| metering-service 目录不存在 | 需创建目录结构（见 §11.3 假设） |

---

## 6. Error Handling

### 6.1 Error Taxonomy

| Error | 严重度 | 处理方 | 恢复策略 |
|-------|--------|--------|----------|
| Publish subject 为空 | 配置错误 | 调用方 | 修正调用方代码 |
| Publish NATS 失败 | 基础设施故障 | 调用方 | outbox publisher 事务不 commit，下次重试 |
| Subscribe subject 为空 | 配置错误 | 调用方 | 修正调用方代码 |
| Subscribe NATS 失败 | 基础设施故障 | 调用方 | consumer.Start 返回 error，上层决定重试 |
| handler 返回 error | 业务错误 | 业务层 | 业务层决定 Ack/Nak；若未调则 AckWait 超时重投 |
| handler panic | 未捕获异常 | adapter 兜底 | recover + Nak 重投 + logger.Error |
| payload 解析失败 | 毒丸消息 | consumer | Ack 跳过 + 告警 |
| StartCollection 失败 | 可恢复故障 | consumer | Nak 延迟重投 |

### 6.2 Retry Strategy

| 操作 | 可重试 | 退避 | 上限 |
|------|--------|------|------|
| Publish（NATS 失败） | 是 | 由 outbox publisher 事务控制 | 无显式上限（事务级） |
| Subscribe（NATS 失败） | 是 | consumer.Start 上层决定 | 上层决定 |
| 消息处理（handler Nak） | 是 | NATS AckWait 延迟重投 | MaxDeliver（metering=10） |
| 消息处理（handler 未调 Ack/Nak） | 是 | NATS AckWait 超时自动重投 | MaxDeliver |
| 毒丸消息 | 否 | Ack 跳过 | — |

### 6.3 Failure Modes

| 依赖失败 | 影响 | 降级 |
|----------|------|------|
| NATS 不可达 | Publish/Subscribe 失败 | outbox 事件累积在 DB（事务不 commit），consumer 不启动 |
| logger 为 nil | panic（防御性：NewMessageBus 应拒绝 nil logger） | — |
| metering-service 不存在 | 7a consumer 代码无法编译到服务 | 本分支只建 internal/eventconsumer 包，不影响 Core 编译 |

---

## 7. Security

### 7.1 Authentication & Authorization

无新增认证授权。adapter 复用现有 NATS 连接（bootstrap/nats.go 的 nats.Connect 无认证配置，本地/集群内通信）。

### 7.2 Input Validation

- `PublishOptions.Subject` / `SubscribeOptions.Subject` 非空校验（adapter 层）
- `AckWait/MaxDeliver/MaxInflight` 零值或正值，负值语义未定义（Go time.Duration 负值会透传给 natsgo，但不应出现负值调用方）
- NATS Header value 长度受 NATS 协议限制（默认 16KB），本任务元数据均为短字符串，无需额外限制

### 7.3 Data Protection

- `tenant-id` 写入 NATS Header，与 payload 同在 NATS 持久化层。NATS FileStorage 落盘，访问控制依赖 NATS 部署层（本任务不改 NATS 部署配置）
- 无敏感字段（密码/密钥）进入 Header；EventEnvelope.Payload 由业务层负责加密/脱敏

---

## 8. Performance

### 8.1 Expected Load

本任务不引入新负载。adapter 改造后：
- Publish：outbox publisher 批量发布，batch size 由现有配置控制
- Subscribe：metering consumer MaxInflight=16，单 consumer 并发处理 16 条消息

### 8.2 Optimization Strategy

- 无新缓存策略
- `message.Headers()` 直接类型转换 `map[string][]string(m.msg.Header)`，零拷贝
- `jetStream` 接口生产路径零开销（natsgo.JetStreamContext 隐式实现，无反射）

### 8.3 Database Considerations

无数据库变更。outbox publisher 的 DB 事务行为不变（adapter 改 headers 不影响 outbox 表结构）。

---

## 9. Testing Strategy

### 9.1 Unit Tests

**adapter 单测（pkg/adapters/nats/message_bus_test.go）**：

Mock 策略：用 `jetStream` 内部接口的 fake 实现 mock `natsgo.JetStreamContext`。`natsgo.Msg` 的 Ack/Nak 需用可观测的 fake（记录调用次数）。

| 测试场景 | 验证点 |
|----------|--------|
| Publish 成功 | fake 收到的 Msg.Header 含 5 个 key 且值匹配 EventEnvelope |
| Publish subject 为空 | 返回 error，fake 未被调用 |
| Subscribe subject 为空 | 返回 error，fake 未被调用 |
| handler 返回 error | adapter 仅 logger.Warn，msg.Ack/Nack 未被 adapter 调用 |
| handler 返回 nil | adapter 未自动调 Ack |
| handler 调 Ack 且返回 nil | Ack 仅被业务层调一次（adapter 不重复） |
| handler 调 Nack 且返回 error | Nak 仅被业务层调一次（adapter 不重复） |
| handler panic（Ack 前） | recover 触发，msg.Nak 被调，不崩溃 |
| handler panic（Ack 后） | recover 触发，msg.Nak 被调（NATS 忽略），不崩溃，不重复处理 |

**consumer 单测（services/metering-service/internal/eventconsumer/consumer_test.go）**：

Mock 策略：mock `ports.MessageBus`（Subscribe 返回 mock Subscription），mock `ports.Message`（可控 Headers/Data/Ack/Nack）。

| 测试场景 | 验证点 |
|----------|--------|
| Start 成功 | Subscribe 被调用，参数含 AckWait=30s/MaxDeliver=10 |
| handle 解析失败 | msg.Ack 被调（毒丸跳过） |
| handle 业务成功 | msg.Ack 被调 |
| handle 业务失败（真实逻辑接入后） | msg.Nack 被调 |

### 9.2 Integration Tests

**集成测试（pkg/adapters/nats/integration_test.go，//go:build integration）**：

前置：
- 本地 docker-compose NATS（`make deps`）或 port-forward 真实 k8s NATS（计划 §9.2.1）
- 测试前确保 ANI_EVENTS=InterestPolicy、ANI_TASKS=WorkQueuePolicy
- 测试后清理 stream

| 测试场景 | 验证点 |
|----------|--------|
| Publish+Subscribe 端到端 | consumer 收到事件，headers 5 个 key 匹配 |
| Ack/Nak 业务层决定 | handler 调 Ack → 消息不再重投；调 Nak → 重投 |
| panic recover | handler panic → 消息 Nak → 进程不崩溃 |
| Nak 延迟重投 | handler Nak → 延迟后重投 → 第二次 Ack |
| MaxDeliver 满后停投 | 持续 Nak → 到顶后 NATS 不再投递 |
| Interest fan-out | 两个 durable consumer 各自收到同一事件 |

运行命令：
```bash
# docker 路径
make deps
go test ./pkg/adapters/nats/ -v -run Integration -tags integration

# port-forward 路径（无 docker）
kubectl port-forward svc/ani-reconcile-ha-nats 4222:4222 -n ani-system
go test ./pkg/adapters/nats/ -v -run Integration -tags integration
```

**Consumer 端到端集成测试（services/metering-service/internal/eventconsumer/integration_test.go，//go:build integration）**：

前置：
- 真实 NATS（docker-compose 或远程 port-forward/直连，通过 `ANI_TEST_NATS_URL` 指定）
- 测试前确保 ANI_EVENTS=InterestPolicy
- 测试代码 import `pkg/adapters/nats` 构造真实 MessageBus 注入给 Consumer（测试代码不算生产依赖，架构校验跳过 `_test.go`）

| 测试场景 | 验证点 |
|----------|--------|
| Consumer 端到端 | adapter 发布 `instance.created` → Consumer 通过真实 NATS 收到 → 打印 `received instance event` + `recovered tenant context` 租户上下文重建日志 |
| Consumer 毒丸消息 | 发布非法 JSON payload → Consumer 解析失败 → Ack 跳过 + 打印 `parse event failed` error 日志 |

运行命令：
```bash
# 通过环境变量指定 NATS 地址
ANI_TEST_NATS_URL=nats://10.10.1.66:31062 \
  go test ./services/metering-service/internal/eventconsumer/ -v -run Integration -tags integration
```

**task 流示例 consumer 端到端集成测试（services/task-service/internal/taskconsumer/integration_test.go，//go:build integration）**：

前置：
- 真实 NATS（docker-compose 或远程直连，通过 `ANI_TEST_NATS_URL` 指定）
- 测试前确保 ANI_TASKS=WorkQueuePolicy
- 测试代码 import `pkg/adapters/nats` 构造真实 MessageBus 注入给 Consumer（测试代码不算生产依赖，架构校验跳过 `_test.go`）

| 测试场景 | 验证点 |
|----------|--------|
| task 端到端 | adapter 发布 `model.import` → Consumer 通过真实 NATS 收到 → 打印 `received task` + `recovered tenant context` 租户上下文重建日志 |
| WorkQueuePolicy 语义 | 消息被 Ack 后从 stream 移除（State.Msgs 归零），非 fan-out |
| task 毒丸消息 | 发布非法 JSON payload → Consumer 解析失败 → Ack 跳过 + 打印 `parse task failed` error 日志 |

运行命令：
```bash
# 通过环境变量指定 NATS 地址
ANI_TEST_NATS_URL=nats://10.10.1.66:31062 \
  go test ./services/task-service/internal/taskconsumer/ -v -run Integration -tags integration
```

### 9.3 Edge Case Tests

| 边界场景 | 测试类型 | 验证点 |
|----------|----------|--------|
| handler 返回 nil 不调 Ack | 单测 | adapter 不调 Ack，消息靠 AckWait 重投 |
| handler 返回 error 调了 Ack | 单测 | adapter 不重复 Ack |
| panic 在 Ack 之后 | 单测 | Nak 被调但不重复处理 |
| ANI_EVENTS 已存在为 WorkQueue | 集成 | StreamInfo 返回成功，不自动改 policy（需手动 rm） |
| metering-service 目录不存在 | 编译 | eventconsumer 包独立编译，不依赖 metering-service main |

### 9.4 Acceptance Criteria Mapping

| US/FR | Test | Type | Description |
|-------|------|------|-------------|
| US-001 / FR-1,2 | testSubscribeOptionsZeroValue | unit | 新字段零值兼容，现有调用方行为不变 |
| US-002 / FR-3,4,5 | testPublishWritesHeaders | unit | Publish 写 5 个 header key |
| US-002 / FR-3 | testNewMessageBusSignature | unit | NewMessageBus(js, logger) 编译通过 |
| US-003 / FR-6,7,8,9 | testSubscribeHandlerReturnError | unit | handler 返回 error 不自动 Ack/Nak |
| US-003 / FR-8 | testSubscribeHandlerPanic | unit | panic recover + Nak |
| US-003 / FR-9 | testSubscribeAckWaitMaxDeliverPassthrough | unit | AckWait/MaxDeliver > 0 时透传 |
| US-004 / FR-10 | testMessageHeaders | unit | Headers() 返回 map，nil 时返回 nil |
| US-004 / FR-11 | testJetStreamInterfaceNotExported | compile | jetstream_iface.go 不在 ports 包 |
| US-005 / FR-12,13 | testEnsureStreamsInterestPolicy | unit/compile | ANI_EVENTS=Interest, ANI_TASKS=WorkQueue |
| US-006 / FR-14,15,16,17,18 | testConsumerStartAndHandle | unit | Start/handle/毒丸 Ack/成功 Ack |
| US-007 / FR-19 | testAdapterUnitTests | unit | 全部 adapter 单测场景 |
| US-008 / FR-21,22,23 | testIntegrationScenarios | integration | 6 个集成场景 |
| US-009 / FR-24,25,26 | testIntegrationConsumerEndToEnd | integration | Consumer 端到端 + 毒丸消息 2 个场景 |
| US-010 / FR-27,28,29,30,31 | testIntegrationTaskConsumerEndToEnd | integration | task 端到端 + WorkQueuePolicy 语义 + 毒丸消息 3 个场景 |
| FR-20 | testConsumerUnitTests | unit | consumer 单测 3 场景 |

---

## 10. Implementation Plan

### 10.1 Phases

| Phase | 内容 | 依赖 |
|-------|------|------|
| P1 | port 契约扩展（US-001） | 无 |
| P2 | adapter 核心修复（US-002, US-003, US-004） | P1 |
| P3 | stream 配置修复（US-005） | 无（可与 P1/P2 并行） |
| P4 | 示例 consumer（US-006） | P1（依赖 port 扩展） |
| P5 | adapter 单测（US-007） | P2（依赖 adapter 修复） |
| P6 | 集成测试（US-008） | P2, P3（依赖 adapter + stream 修复） |
| P7 | 调用点更新 + 全量验证 | P2（NewMessageBus 签名变更） |

### 10.2 Issue Mapping

| Issue | US | SPEC Sections | Priority | Depends On |
|-------|----|---------------|----------|------------|
| #1 | US-001 | 3.2, 5.2 | high | — |
| #2 | US-005 | 3.4, 5.4 | high | — |
| #3 | US-002 | 2.4, 5.1, 4.3 | high | #1 |
| #4 | US-003 | 2.4, 5.1 | high | #1 |
| #5 | US-004 | 3.2, 5.1 | high | #1 |
| #6 | US-006 | 2.4, 5.1 | medium | #1 |
| #7 | US-007 | 9.1 | medium | #3, #4, #5 |
| #8 | US-008, US-009 | 9.2 | medium | #3, #4, #2 |
| #9 | 调用点更新 | 4.3 | high | #3 |
| #10 | 全量验证 | 9, 验收命令 | high | #1-#9 |
| #11 | US-010 | 9.2, 6.1 | medium | #1, #3, #4 |

### 10.3 Incremental Delivery

本任务一次性合入（无 feature flag）。增量性体现在：
- port 字段新增均为可选，现有调用方零值兼容，可先合 port 扩展不破坏编译
- adapter 修复可先合 Subscribe/Publish，再合 consumer
- 集成测试用 `//go:build integration` tag，默认不跑，手动触发

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

- metering-service 的 main/bootstrap 入口位置（7b 阶段定位，本 SPEC 不含 7b）
- `ports.Logger` 统一 port 是否后续同步建设（当前用 `log/slog`，PRD Open Questions 已记录）

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|-----------|
| `natsgo.JetStreamContext` 具体类型无法直接 mock | 单测无法进行 | adapter 内部定义 jetStream 接口包装（US-004） |
| NewMessageBus 签名变更是破坏性改动 | 编译失败 | 扫描全量调用点（当前仅 `deps.go:213`），同步更新 |
| ANI_EVENTS 已存在为 WorkQueuePolicy 时 bootstrap 不自动改 | 集成测试 stream policy 错误 | 文档标注手动 `nats stream rm ANI_EVENTS -f` 后重启 |
| metering-service 目录在本分支不存在 | US-006 consumer 代码无处放置 | 创建 `services/metering-service/internal/eventconsumer/` 目录（见 §11.3） |
| Ack/Nak 改业务层决定后旧 handler 不调 Ack/Nack | 消息卡到 AckWait 超时重投（不丢失） | 通知所有 handler 作者改调用方式；现状无 Subscribe 调用方 |
| 集成测试依赖 docker/port-forward | CI 可能无法跑 | 集成测试用 build tag 隔离，手动验证项不作为硬性门禁 |

### 11.3 Assumptions

- **[假设]** `services/metering-service/` 目录在本分支不存在（Glob 确认）。US-006 需创建 `services/metering-service/internal/eventconsumer/` 目录结构。若 metering-service 有独立 go.mod，eventconsumer 包需能独立编译（只 import `pkg/ports` + 标准库）。
- **[假设]** `pkg/bootstrap/deps.go:213` 是 `NewMessageBus` 唯一调用点（Grep 确认 `pkg` 和 `services` 范围）。若 cmd/ 或其他位置有调用点，需补充扫描。
- **[假设]** NATS Header key 用小写连字符（HTTP header 风格）与 NATS 协议兼容。nats.go 库 Header 类型是 `map[string][]string`，key 大小写敏感，需与消费侧读取 key 一致。
- **[假设]** `EventEnvelope.OccurredAt.Format(time.RFC3339Nano)` 可无损序列化/反序列化时间戳。集成测试应验证 round-trip。
- **[假设]** `natsgo.JetStreamContext` 天然满足 `jetStream` 接口（5 个方法签名匹配）。编译时隐式实现验证。

---

## 12. 验收命令

```bash
cd repo
make test                    # 单元测试通过（含 adapter 单测 + consumer 单测）
make validate-architecture    # 架构边界校验通过
git diff --check             # 无空白错误
```

集成测试（手动，不作为硬性门禁）：
```bash
# adapter 集成测试（docker 路径）
make deps
go test ./pkg/adapters/nats/ -v -run Integration -tags integration
go test ./services/metering-service/internal/eventconsumer/ -v

# Consumer 端到端集成测试（指定真实 NATS 地址）
ANI_TEST_NATS_URL=nats://10.10.1.66:31062 \
  go test ./services/metering-service/internal/eventconsumer/ -v -run Integration -tags integration

# task 流示例 consumer 端到端集成测试（指定真实 NATS 地址）
ANI_TEST_NATS_URL=nats://10.10.1.66:31062 \
  go test ./services/task-service/internal/taskconsumer/ -v -run Integration -tags integration

# port-forward 路径（无 docker）
kubectl port-forward svc/ani-reconcile-ha-nats 4222:4222 -n ani-system
go test ./pkg/adapters/nats/ -v -run Integration -tags integration
```

真实环境门禁（REAL-K8S-LAB）：
- 验证 ANI_EVENTS 在真实 NATS 为 InterestPolicy
- 验证 metering consumer 在真实环境订阅链路（7b 接线后）
