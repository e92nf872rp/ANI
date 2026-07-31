# 任务B：NATS 接入（MessageBus 健壮性 + 示例 consumer）落地计划

> 状态: 计划修订版（融合承霖 task 模式 + metering event 模式）
> 创建日期: 2026-07-28
> 修订日期: 2026-07-29
> 负责人: kjs
> 前置文档:
> - `通用资源配额与计量落地方案.md` §1.2、§7 PR-6、§8 取舍 4
> - `plan0728.md`（承霖模型仓库方案，task 模式参考）
> 依赖: 任务A 先行（配额表/Port 先到位），本任务可并行开发

---

## 0. 修订背景

本任务要同时满足承霖 task 流（模型导入）和 kjs event 流（计量）两种消费模式的需求。
经讨论确认：**adapter 层改造对两种模式零冲突，区分只在于 stream retention policy 配置和业务层处理逻辑**。
详见 §1.4 设计决策摘要。

---

## 1. 任务边界

### 1.1 本任务做什么

1. **修复 `MessageBus.Subscribe`**：Ack/Nak 改由业务层决定（adapter 不再自动处理），保留 panic recover 兜底
2. **修复 `MessageBus.Publish`**：传递 `EventEnvelope` 元数据到 NATS Message Header
3. **扩展 `ports.SubscribeOptions`**：新增 AckWait/MaxDeliver 字段（可选，各 consumer 按需传值）
4. **扩展 `ports.Message` 接口**：新增 Headers 访问方法，供消费侧读元数据
5. **注入 logger**：adapter 新增 logger 字段，记录 panic/error 路径
6. **修复 stream 配置**：`ANI_EVENTS` 从 WorkQueuePolicy 改为 InterestPolicy（现状错配）
7. **示例 consumer**：
   - **7a**（本分支可做）：新增 `services/metering-service/internal/eventconsumer/consumer.go` + 单测，handler 业务层 Ack/Nak + 打印日志验证链路
   - **7b**（合并目标分支后做）：在 `services/metering-service` 的 main/bootstrap 启动时开 goroutine 订阅 `ani.events.instance.>`。metering-service 在另一分支，本分支看不到其入口，接线步骤需合并分支后执行

### 1.2 本任务不做什么

| 不做项 | 负责人/阶段 |
|---|---|
| 计量 consumer 的真实业务逻辑（StartCollection） | 后续 PR |
| 审计 consumer | 后续 PR |
| consumer 重启重建逻辑（进程重启从 PG 恢复 ticker） | 后续 PR（PR-7） |
| reconciler 写 outbox | 嘉明 |
| outbox publisher 接线 | 已实现，不需做 |
| 死信表/死信 stream | 不做，按需升级（见 §10） |
| 模型导入 task 的 `lease_reconciler` goroutine | 承霖那边做 |

### 1.3 依赖关系

- **前置依赖（本分支）**：无（纯改 adapter + port + stream 配置，独立可测）
- **接线依赖（7b）**：metering-service 在另一分支，本分支看不到其 main/bootstrap 入口。consumer 代码和单测（7a）可独立完成；接线步骤（7b）需合并含 metering-service 的目标分支后执行
- **与任务A关系**：A 先 B 后（按用户确认），但本任务开发可与 A 并行，因为本任务只改 NATS adapter，不碰配额表
- **与承霖方案关系**：本任务改 adapter，模型导入的 task 流（task_repo.go + outbox_publisher.go + messages.go 已实现）和本任务的 event 流共用同一 adapter，零冲突

### 1.4 设计决策摘要（与承霖方案对齐的结论）

**为什么 metering 不走 task 模式（async_tasks 表）？** metering 的生命周期跨 `instance.created` 和 `instance.deleted` 两条事件，没有单消息"完成"时刻——`StartCollection` 启动 ticker 后即返回，ticker 要跑到实例销毁才停，套 task 状态机会卡在 `running` 永远不完；metering ticker 是进程内 goroutine，无竞争者，`AcquireLease` 空转、`Heartbeat` 续命续给谁看；崩溃恢复靠查 `workload_instances WHERE state='running'` 直接重建，不需要租约过期机制，若套 task 模式反而要清理 `async_tasks` 里卡 `running` 的过期租约，是负担。因此 metering 走 event 流：不建 `async_tasks` 行、不抢租约、没有 pending→running→completed 状态机，handler 启动 ticker 后即返回（Ack 由业务层调用），ticker 生命周期由 `instance.deleted` 事件独立终止。

| 决策点 | task 流（模型导入） | event 流（metering） | adapter 怎么做 |
|---|---|---|---|
| Ack/Nak 归属 | 业务层（抢租约失败→Ack 跳过） | 业务层（StartCollection 失败→Nack） | adapter 不自动处理，只兜底 panic |
| Publish 写 headers | outbox publisher 已传 envelope，受益 | 从 headers 重建租户上下文 | 统一改 Publish 写 headers |
| panic recover | 需要 | 需要 | adapter 保留 |
| Message.Headers() | 不强需要但受益 | 需要 | 新增 |
| SubscribeOptions 加 AckWait/MaxDeliver | 传 0 不配 | 传具体值 | 新增（可选字段） |
| 死信方式 | DB 计数 `attempt_count` | MaxDeliver 兜底 + 重启重建 | adapter 不管死信 |
| stream policy | WorkQueuePolicy（保持） | InterestPolicy（要改） | 改 bootstrap |
| 崩溃恢复 | NATS ack timeout + 过期租约 reconcile + max_attempts | 重启重建 ticker（PR-7） | adapter 只保证 AckWait 配置让 NATS ack timeout 可用 |

---

## 2. 交付物清单

| 文件 | 类型 | 说明 |
|---|---|---|
| `pkg/ports/message_bus.go` | 修改 | 扩展 SubscribeOptions（AckWait/MaxDeliver）+ Message（Headers）|
| `pkg/adapters/nats/message_bus.go` | 修改 | 修复 Subscribe/Publish + 注入 logger + Ack/Nak 业务层决定 |
| `pkg/adapters/nats/message_bus_test.go` | 新增 | 单元测试（fake/mock JetStream） |
| `pkg/adapters/nats/integration_test.go` | 新增 | 集成测试（连本地 docker-compose NATS） |
| `pkg/adapters/nats/jetstream_iface.go` | 新增 | 内部 jetStream 接口包装（便于单测 mock） |
| `pkg/bootstrap/nats.go` | 修改 | `ANI_EVENTS` 改为 InterestPolicy |
| `services/metering-service/internal/eventconsumer/consumer.go` | 新增 | 示例 consumer（业务层 Ack/Nak） |
| `services/metering-service/internal/eventconsumer/consumer_test.go` | 新增 | consumer 单元测试 |
| `services/metering-service/internal/eventconsumer/integration_test.go` | 新增 | Consumer 端到端集成测试（连真实 NATS，`//go:build integration`） |
| `services/task-service/internal/taskconsumer/consumer.go` | 新增 | task 流示例 consumer（业务层 Ack/Nak） |
| `services/task-service/internal/taskconsumer/consumer_test.go` | 新增 | task consumer 单元测试 |
| `services/task-service/internal/taskconsumer/integration_test.go` | 新增 | task 流集成测试（连真实 NATS，`//go:build integration`） |

> 注：不新增 `EnsureDeadLetterStream` 相关文件（死信不走 NATS stream，见 §10）。

---

## 3. Port 契约扩展（pkg/ports/message_bus.go）

### 3.1 SubscribeOptions 扩展

```go
type SubscribeOptions struct {
    Subject     string
    Consumer    string
    Queue       string
    MaxInflight int
    AckWait     time.Duration  // 新增：Ack 超时，超时未 Ack 视为失败重投（NATS 自动），按需配置
    MaxDeliver  int            // 新增：最大投递次数，按需配置，满后 NATS 停投
}
```

> AckWait/MaxDeliver 均为可选字段，传 0 表示不配置：
> - task 流（模型导入）可传 0（死信走 DB attempt_count，不依赖 NATS MaxDeliver）
> - event 流（metering）传具体值（AckWait=30s, MaxDeliver=10，毒丸兜底）

### 3.2 Message 接口扩展

```go
type Message interface {
    Subject() string
    Data() []byte
    Ack(ctx context.Context) error
    Nack(ctx context.Context) error
    Headers() map[string][]string  // 新增：读取 NATS message headers
}
```

### 3.3 EventEnvelope 不变

现有 `EventEnvelope` 结构不变，`Publish` 在内部把 envelope 元数据写进 NATS headers。

---

## 4. Adapter 修复（pkg/adapters/nats/message_bus.go）

### 4.1 结构体（注入 logger）

```go
type MessageBus struct {
    js     jetStream        // 内部接口包装，便于单测 mock（见 4.6）
    logger *slog.Logger     // 用 log/slog，记录 panic/error 路径
}

func NewMessageBus(js natsgo.JetStreamContext, logger *slog.Logger) *MessageBus {
    return &MessageBus{js: js, logger: logger}
}
```

> logger 用 `log/slog`。现有 `NewMessageBus(js)` 签名改为 `NewMessageBus(js, logger)`，需同步更新所有调用点（`pkg/bootstrap/nats.go` 等）。

### 4.2 Publish 修复（传递 envelope 元数据）

```go
func (b *MessageBus) Publish(ctx context.Context, event ports.EventEnvelope, opts ports.PublishOptions) error {
    if opts.Subject == "" {
        return fmt.Errorf("message bus publish: subject required")
    }
    headers := natsgo.Header{
        "tenant-id":      []string{event.TenantID},
        "aggregate-id":   []string{event.AggregateID},
        "aggregate-type": []string{event.AggregateType},
        "event-type":     []string{event.EventType},
        "occurred-at":    []string{event.OccurredAt.Format(time.RFC3339Nano)},
    }
    pubOpts := []natsgo.PubOpt{
        natsgo.Context(ctx),
    }
    if opts.Key != "" {
        pubOpts = append(pubOpts, natsgo.MsgId(opts.Key))
    }
    _, err := b.js.PublishMsg(&natsgo.Msg{
        Subject: opts.Subject,
        Data:    event.Payload,
        Header:  headers,
    }, pubOpts...)
    if err != nil {
        return fmt.Errorf("message bus publish: %w", err)
    }
    return nil
}
```

> 关键：`EventEnvelope.TenantID/AggregateID/EventType/OccurredAt` 写进 NATS Message Header，消费侧通过 `Message.Headers()` 读取，重建租户上下文。
>
> **outbox publisher 自动受益**：[outbox_publisher.go:91-101](file:///e:/go/project/ANI/repo/services/task-service/internal/worker/outbox_publisher.go) 已经在传完整 `EventEnvelope`，现有 adapter 把元数据丢了，改完后 publisher 发的事件也带 headers。

### 4.3 Subscribe 修复（Ack/Nak 业务层决定 + panic recover）

**核心变更**：adapter 不再根据 handler 返回值自动 Ack/Nak，handler 自己显式调用 `msg.Ack/Nack`。adapter 只保留 panic recover 兜底。

```go
func (b *MessageBus) Subscribe(ctx context.Context, opts ports.SubscribeOptions, handler ports.MessageHandler) (ports.Subscription, error) {
    if opts.Subject == "" {
        return nil, fmt.Errorf("message bus subscribe: subject required")
    }
    subOpts := []natsgo.SubOpt{natsgo.ManualAck()}
    if opts.Consumer != "" {
        subOpts = append(subOpts, natsgo.Durable(opts.Consumer))
    }
    if opts.MaxInflight > 0 {
        subOpts = append(subOpts, natsgo.MaxAckPending(opts.MaxInflight))
    }
    if opts.AckWait > 0 {
        subOpts = append(subOpts, natsgo.AckWait(opts.AckWait))
    }
    if opts.MaxDeliver > 0 {
        subOpts = append(subOpts, natsgo.MaxDeliver(opts.MaxDeliver))
    }

    handlerFunc := func(msg *natsgo.Msg) {
        defer func() {
            if r := recover(); r != nil {
                b.logger.Error("handler panic",
                    "subject", msg.Subject,
                    "panic", r,
                )
                // panic 兜底：业务层决定 Ack/Nak，但 panic 是未捕获异常，
                // adapter 兜底 Nak 触发重投。若 panic 发生在业务层 Ack 之后，
                // 此 Nak 会被 NATS 忽略（消息已 Ack），不影响正确性。
                _ = msg.Nak()
            }
        }()
        // 不自动 Ack/Nak——交给 handler 业务层决定。
        // handler 返回值仅用于日志，与 Ack/Nak 无关：
        //   - handler 返回 nil 但没调 Ack → 消息卡到 AckWait 超时由 NATS 自动重投
        //   - handler 返回 error 但调了 Ack → 消息已处理，adapter 不干预
        if err := handler(ctx, message{msg: msg}); err != nil {
            b.logger.Warn("handler returned error; ack/nack is handler's responsibility — if neither was called, message will redeliver after AckWait",
                "subject", msg.Subject,
                "err", err,
            )
        }
    }

    var (
        sub *natsgo.Subscription
        err error
    )
    if opts.Queue != "" {
        sub, err = b.js.QueueSubscribe(opts.Subject, opts.Queue, handlerFunc, subOpts...)
    } else {
        sub, err = b.js.Subscribe(opts.Subject, handlerFunc, subOpts...)
    }
    if err != nil {
        return nil, fmt.Errorf("message bus subscribe: %w", err)
    }
    return subscription{sub: sub}, nil
}
```

> **为什么改业务层决定**：模型导入的 task 流需要"抢租约失败→Ack 跳过"语义（消息处理完了，只是我不处理），metering 的 event 流需要"StartCollection 失败→Nack 重投"。如果 adapter 自动根据 handler 返回值决定 Ack/Nak，这两个语义都表达不了。改成业务层决定后：
> - 模型导入 task：抢租约失败 → handler 调 `msg.Ack(ctx)` 跳过；业务失败 → 调 `msg.Nack(ctx)` 重投
> - metering event：StartCollection 成功 → 调 `msg.Ack(ctx)`；失败 → 调 `msg.Nack(ctx)` 重投；毒丸 → 调 `msg.Ack(ctx)` 跳过

### 4.4 message 结构体扩展（Headers）

```go
type message struct {
    msg *natsgo.Msg
}

func (m message) Subject() string { return m.msg.Subject }
func (m message) Data() []byte    { return m.msg.Data }

func (m message) Ack(context.Context) error  { return m.msg.Ack() }
func (m message) Nack(context.Context) error { return m.msg.Nak() }

func (m message) Headers() map[string][]string {
    if m.msg.Header == nil {
        return nil
    }
    return map[string][]string(m.msg.Header)
}
```

### 4.5 内部 jetStream 接口包装（便于单测 mock）

`natsgo.JetStreamContext` 是具体类型不是接口，无法直接 mock。在 adapter 内部定义接口包装：

```go
// pkg/adapters/nats/jetstream_iface.go
package nats

import (
    natsgo "github.com/nats-io/nats.go"
)

// jetStream 是 natsgo.JetStreamContext 的内部接口包装，仅用于 adapter 内部和单测 mock。
// natsgo.JetStreamContext 天然满足此接口，生产路径零开销。
type jetStream interface {
    PublishMsg(msg *natsgo.Msg, opts ...natsgo.PubOpt) (*natsgo.PubAck, error)
    Subscribe(subj string, cb natsgo.MsgHandler, opts ...natsgo.SubOpt) (*natsgo.Subscription, error)
    QueueSubscribe(subj, queue string, cb natsgo.MsgHandler, opts ...natsgo.SubOpt) (*natsgo.Subscription, error)
    StreamInfo(stream string, opts ...natsgo.RequestOpt) (*natsgo.StreamInfo, error)
    AddStream(cfg *natsgo.StreamConfig, opts ...natsgo.RequestOpt) (*natsgo.StreamInfo, error)
}
```

> 关键：这个接口**不暴露到 `pkg/ports/`**，只是 adapter 内部测试工具。生产代码传 `natsgo.JetStreamContext`（天然满足），单测传 fake 实现。

### 4.6 不再实现 EnsureDeadLetterStream

原方案的 `EnsureDeadLetterStream` 删除。死信不走 NATS 死信 stream，详见 §10。

---

## 5. Stream 配置修复（pkg/bootstrap/nats.go）

### 5.1 现状

[bootstrap/nats.go:56-88](file:///e:/go/project/ANI/repo/pkg/bootstrap/nats.go) 的 `ensureStreams` 现状：

| stream | subjects | 现状策略 | 问题 |
|---|---|---|---|
| `ANI_TASKS` | `ani.tasks.>` | WorkQueuePolicy | 正确（task 一消息一 worker） |
| `ANI_EVENTS` | `ani.events.>` | WorkQueuePolicy | **错配**——event 要 fan-out 给多消费者，WorkQueue 会让第一个 Ack 的消费者把消息从 stream 删除，其他消费者收不到 |

### 5.2 修复

```go
// ANI_TASKS 保持 WorkQueue：一消息一 worker，Ack 后删除
{Name: "ANI_TASKS", Subjects: []string{"ani.tasks.>"}, Retention: natsgo.WorkQueuePolicy}

// ANI_EVENTS 改为 Interest：fan-out，每个 durable consumer 各自保留消费进度
{Name: "ANI_EVENTS", Subjects: []string{"ani.events.>"}, Retention: natsgo.InterestPolicy}
```

### 5.3 为什么必须改

metering 和审计都要订阅 `ani.events.instance.created`：
- WorkQueuePolicy 下 metering Ack 后消息从 stream 删除 → 审计永远收不到
- InterestPolicy 下每个 durable consumer 各自有消费进度 → 互不干扰

这是 metering consumer 能正常工作的前提。

---

## 6. 示例 consumer（services/metering-service）

### 6.1 consumer 实现

```go
// services/metering-service/internal/eventconsumer/consumer.go
package eventconsumer

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"
    "time"

    "github.com/kubercloud/ani/pkg/ports"
)

// Consumer 是 metering 的事件消费者示例，走 event 模式（Interest）。
// 业务层自己决定 Ack/Nak，不依赖 adapter 自动处理。
type Consumer struct {
    bus    ports.MessageBus
    logger *slog.Logger
    sub    ports.Subscription
}

func New(bus ports.MessageBus, logger *slog.Logger) *Consumer {
    return &Consumer{bus: bus, logger: logger}
}

func (c *Consumer) Start(ctx context.Context) error {
    sub, err := c.bus.Subscribe(ctx, ports.SubscribeOptions{
        Subject:    "ani.events.instance.>",
        Consumer:   "metering-example",
        Queue:      "metering",
        MaxInflight: 16,
        AckWait:    30 * time.Second,  // handler 卡住/进程假死时 NATS 自动重投
        MaxDeliver: 10,                // 毒丸兜底，满后 NATS 停投
    }, c.handle)
    if err != nil {
        return fmt.Errorf("eventconsumer subscribe: %w", err)
    }
    c.sub = sub
    c.logger.Info("eventconsumer started", "subject", "ani.events.instance.>")
    return nil
}

// handle 是业务层 handler，自己决定 Ack/Nak。
// 返回的 error 仅用于 adapter 日志记录，不等于 Nak。
func (c *Consumer) handle(ctx context.Context, msg ports.Message) error {
    // 1. 从 headers 重建租户上下文
    headers := msg.Headers()
    tenantID := ""
    if h, ok := headers["tenant-id"]; ok && len(h) > 0 {
        tenantID = h[0]
    }

    // 2. 解析 payload
    var event instanceEvent
    if err := json.Unmarshal(msg.Data(), &event); err != nil {
        // 毒丸消息：payload 格式错，重投也没用 → Ack 跳过 + 告警
        c.logger.Error("parse event failed, ack to skip poison message",
            "subject", msg.Subject(),
            "tenant_id", tenantID,
            "err", err,
        )
        return msg.Ack(ctx)
    }

    // 3. 业务处理（示例：打印日志验证链路；真实逻辑 StartCollection 后续 PR）
    c.logger.Info("received event",
        "subject",   msg.Subject(),
        "tenant_id",  tenantID,
        "event_type", event.EventType,
        "instance",  event.InstanceID,
        "data_len",   len(msg.Data()),
    )
    // 真实逻辑：c.metering.StartCollection(ctx, CollectionSpec{...})
    // 失败时：return msg.Nack(ctx)  // 延迟重投
    // 成功时：return msg.Ack(ctx)

    // 示例阶段：链路验证通过就 Ack
    return msg.Ack(ctx)
}

func (c *Consumer) Stop(ctx context.Context) error {
    if c.sub == nil {
        return nil
    }
    return c.sub.Drain(ctx)
}

// instanceEvent 是示例用的事件 payload 结构，真实结构后续 PR 定义
type instanceEvent struct {
    EventType  string `json:"event_type"`
    InstanceID string `json:"instance_id"`
}
```

### 6.2 启动接线（7b，合并目标分支后执行）

metering-service 在另一分支，本分支看不到其 main/bootstrap 入口。consumer 代码和单测（7a）可在本分支独立完成；以下接线步骤需合并含 metering-service 的目标分支后执行。

在 `services/metering-service` 的 main/bootstrap 里启动：

```go
// 启动示例 consumer（后台 goroutine）
go func() {
    consumer := eventconsumer.New(messageBus, logger)
    if err := consumer.Start(ctx); err != nil {
        logger.Error("eventconsumer start failed", "err", err)
    }
}()
```

> 注意：需找到 metering-service 的实际 main/bootstrap 入口，在服务启动后、阻塞前的位置插入。不干扰现有 gRPC ingest + NATS 重试队列。

### 6.3 metering 失败处理分类（业务层决策）

| 失败场景 | handler 调用 | 理由 |
|---|---|---|
| StartCollection 业务失败（DB 暂时不可用等） | `msg.Nack(ctx)` | 可恢复故障，Nak 延迟重投 |
| 解析失败（payload 格式错） | `msg.Ack(ctx)` + 告警 | 毒丸消息，重投也没用 |
| StartCollection 重复调用（实例已有 ticker） | `msg.Ack(ctx)` | 幂等跳过，不是错误 |

---

## 7. 死信方案（不建死信 stream）

### 7.1 两种流的死信方式

| 流 | 死信方式 | 最终结局 | 兜底恢复 |
|---|---|---|---|
| task 流（模型导入） | DB 计数：`attempt_count >= max_attempts` → `status='dead_letter'` | worker 主动 Ack 删除消息，DB 永久保留死信行，可查可人工重试 | 需人工干预 |
| event 流（metering） | `MaxDeliver` 兜底，满后 NATS 停投，consumer 侧告警 | durable 下消息留 stream 但不再给该 consumer，无 DB 记录 | 重启重建 ticker（PR-7） |

### 7.2 为什么 metering 不建死信 stream/table

1. **不能用 `async_tasks` 表**：metering 是消费侧，`async_tasks` 是派发侧表，字段语义对不上（`OutboxSubject`/`OutboxPayload` 无值可填），且跨服务边界（metering-service 写 task-service 的表）
2. **不建 metering 独立死信表**：当前只有两个 consumer，提前建表违反 Karpathy 原则五"不得为了未来可能需要提前引入抽象"
3. **不建 NATS 死信 stream**：MaxDeliver 满后默认不自动转发到死信 subject（需额外配 Republish），且 metering 没有 DB 记录关联事件 ID，死信进 stream 后不可查不可干预，意义有限

### 7.3 metering 失败处理三层保证

```
第一层：Nak 延迟重投（可恢复故障自动重试）
第二层：MaxDeliver 兜底（毒丸消息到顶停投 + 告警）
第三层：重启重建 ticker（进程重启查 running 实例补建，PR-7）
```

### 7.4 升级路径（按需，不在本任务）

真出现"实例销毁前一次没采到"的线上问题，再建统一 `event_delivery_results` 表（放共享 repo，所有 consumer 共用 `consumer_name` 字段区分）。届时需要：
1. 新建 migration + repo
2. 扩展 Message 接口加 `MsgID() string`（从 NATS `Nats-Msg-Id` header 取，关联 `outbox_events.id`）
3. consumer 失败时调 repo 记录

---

## 8. 崩溃恢复（本任务不实现，记录设计对齐）

### 8.1 模型导入 task 流的崩溃恢复（三层兜底）

```
第一层：NATS JetStream ack timeout（秒级，AckWait 配置）
  worker 崩溃 → 来不及 Ack/Nak → AckWait 超时后 NATS 自动重投
  → 新 worker 收到 → AcquireLease 抢锁
  → 旧租约可能还没过期（300s）→ 抢失败 → Ack 跳过
  → 问题：旧租约没过期，任务被跳过无人处理

第二层：过期租约 reconcile（分钟级）
  model-service 新增 lease_reconciler goroutine，每 60s 调 GetExpiredLeases
  查 status='running' AND lease_until < NOW() 的任务
  → attempt_count < max_attempts → 重新写 outbox_events 触发重投
  → attempt_count >= max_attempts → status='dead_letter'
  → 新 worker 收到重投，旧租约已过期 → 抢成功 → 从头下载

第三层：max_attempts + dead_letter
  超过 3 次 → dead_letter_at → 不再重投
```

- 代码基础：[task_repo.go:345-380](file:///e:/go/project/ANI/repo/pkg/repo/task_repo.go) `GetExpiredLeases` 已实现
- 待写：`lease_reconciler` goroutine（承霖那边做，不在本任务）

### 8.2 metering event 流的崩溃恢复（重启重建）

```
进程重启 → 查 workload_instances WHERE state='running'
        → 对每个 running 实例重建 ticker
        → 不依赖 NATS 重投，不依赖租约，不依赖 async_tasks
```

- 理由：PG 是第一手真相源，ticker 是内存派生物，丢了从 DB 重建
- 三种崩溃场景全覆盖：
  - instance.created 还没处理就崩 → 重启查到 running 实例 → 建 ticker
  - StartCollection 处理中崩 → 同上
  - ticker 运行中崩 → 同上
- 代码基础：PR-7（后续 PR，不在本任务）

### 8.3 本任务对崩溃恢复的贡献

本任务不实现崩溃恢复，但保证 adapter 层的 `AckWait` 配置让 NATS ack timeout 重投可用。这对模型导入 task 流是第一层兜底，对 metering event 流用于"handler 卡住/进程假死"场景。

### 8.4 两种崩溃恢复的本质区别

| 维度 | 模型导入 task | metering event |
|---|---|---|
| 崩溃丢了什么 | 任务执行进度（下载到 80% 崩了） | 内存里的 ticker |
| 能从哪恢复 | 不能从进度恢复，要从头重跑任务 | 从 PG 查 running 实例重建 ticker |
| 恢复触发 | NATS 重投 + 租约过期 reconcile | 进程重启 |
| 需要新代码 | `lease_reconciler` goroutine | 重启重建逻辑（PR-7） |
| 进度丢失吗 | 丢（从头下载） | 不丢（ticker 重建继续采） |

---

## 9. 测试策略

### 9.1 单元测试（fake/mock）

**adapter 测试（pkg/adapters/nats/message_bus_test.go）：**

用 §4.5 的 `jetStream` 接口包装 mock `natsgo.JetStreamContext`，测试用 fake 实现。

测试场景：
- Publish 成功：验证 headers 正确设置（tenant-id/aggregate-id/event-type/occurred-at）
- Publish 失败：subject 为空 → error
- Subscribe subject 为空 → error
- Subscribe handler 返回 error：adapter 仅记录 warn 日志，不自动 Ack/Nak（验证 msg.Ack/Nack 未被 adapter 调用）
- Subscribe handler 返回 nil：adapter 不自动 Ack（验证 msg.Ack 未被 adapter 调用，由业务层负责）
- Subscribe handler 自己调 msg.Ack 且返回 nil：adapter 不重复调 Ack（验证 msg.Ack 只被业务层调一次）
- Subscribe handler 自己调 msg.Nack 且返回 error：adapter 不重复调 Nak（验证 msg.Nak 只被业务层调一次）
- Subscribe handler panic（Ack 调用前）：recover → msg.Nak 被调用（兜底）→ 不崩溃
- Subscribe handler panic（Ack 调用后）：recover → msg.Nak 被调用但被 NATS 忽略（消息已 Ack），不崩溃

**consumer 测试（services/metering-service/internal/eventconsumer/consumer_test.go）：**

mock `ports.MessageBus`，验证：
- Start 成功：Subscribe 被调用
- handle 解析失败：handler 调 `msg.Ack` 跳过（毒丸）
- handle 业务成功：handler 调 `msg.Ack`
- handle 业务失败：handler 调 `msg.Nack`（真实逻辑接入后）

### 9.2 集成测试（连本地 docker-compose NATS）

- 前置：`make deps` 启动 docker-compose（NATS 4222）
- 测试前确保 stream 存在（ANI_EVENTS 为 InterestPolicy）
- 测试场景：
  - Publish + Subscribe 端到端：发布一条 `instance.created` 事件，consumer 收到后验证 headers
  - Ack/Nak 业务层决定：handler 自己调 Ack/Nak，adapter 不干预
  - panic recover：handler panic → 消息 Nak → 不崩溃
  - Nak 延迟重投：handler 调 Nak → 消息延迟重投 → 第二次 Ack
  - MaxDeliver 满后停投：handler 持续 Nak → 到顶后 NATS 不再投递
  - Interest fan-out：两个 durable consumer 各自收到同一事件（验证 InterestPolicy 生效）
- 测试后清理 stream

#### 9.2.2 Consumer 端到端集成测试（连真实 NATS）

补齐单测（mock）无法覆盖的"Consumer 连上真 NATS 后真的能收到事件"这一环。测试文件：`services/metering-service/internal/eventconsumer/integration_test.go`（`//go:build integration`）。

- 前置：真实 NATS（docker-compose 或远程直连，通过 `ANI_TEST_NATS_URL` 指定），ANI_EVENTS 为 InterestPolicy
- 测试代码 import `pkg/adapters/nats` 构造真实 MessageBus 注入给 Consumer（测试代码不算生产依赖，架构校验跳过 `_test.go`）
- 测试场景：
  - Consumer 端到端：adapter 发布 `instance.created` → Consumer 通过真实 NATS 收到 → 打印 `received instance event` + `recovered tenant context` 租户上下文重建日志
  - Consumer 毒丸消息：发布非法 JSON payload → Consumer 解析失败 → Ack 跳过 + 打印 `parse event failed` error 日志
- 用 `//go:build integration` build tag 隔离，不阻塞默认 `make test`

#### 9.2.3 task 流示例 consumer 端到端集成测试（连真实 NATS）

补齐 event 流已测但 task 流未测的缺口。真实 `lease_reconciler` 由他人负责且未完工，新增最简示例 task consumer 仅验证 task 流 adapter 链路连通性。测试文件：`services/task-service/internal/taskconsumer/integration_test.go`（`//go:build integration`）。

- 前置：真实 NATS（docker-compose 或远程直连，通过 `ANI_TEST_NATS_URL` 指定），ANI_TASKS 为 WorkQueuePolicy
- 测试代码 import `pkg/adapters/nats` 构造真实 MessageBus 注入给 Consumer（测试代码不算生产依赖，架构校验跳过 `_test.go`）
- 测试场景：
  - task 端到端：adapter 发布 `model.import` → Consumer 通过真实 NATS 收到 → 打印 `received task` + `recovered tenant context` 租户上下文重建日志
  - WorkQueuePolicy 语义：消息被 Ack 后从 stream 移除（`StreamInfo.State.Msgs` 归零），非 fan-out
  - task 毒丸消息：发布非法 JSON payload → Consumer 解析失败 → Ack 跳过 + 打印 `parse task failed` error 日志
- 用 `//go:build integration` build tag 隔离，不阻塞默认 `make test`

#### 9.2.1 可选路径：无 docker 环境走服务器 port-forward

本地无 docker 时，可 port-forward 真实 k8s 里的 NATS（`ani-system` 命名空间的 `ani-reconcile-ha-nats`）到本地 4222，连接地址仍是 `localhost:4222`，集成测试代码无需改。

前置与 stream 建立：
1. 保持 port-forward 运行：`kubectl port-forward svc/ani-reconcile-ha-nats 4222:4222 -n ani-system`
2. **先改 `pkg/bootstrap/nats.go`（`ANI_EVENTS` 改 InterestPolicy，见 §5.2）再启动 Core 服务**——`ensureStreams` 会在启动时自动建 `ANI_TASKS`（WorkQueue）和 `ANI_EVENTS`（Interest），无需手动建 stream。
3. 若已用旧 bootstrap 启动过、NATS 里已有 WorkQueuePolicy 的 `ANI_EVENTS`，先删旧 stream 再重启 Core：`nats stream rm ANI_EVENTS -f`（`ANI_TASKS` 未改不用动）。

注意：
- 该 NATS 部署 `args: ["-js"]` 未配 `--store_dir`，数据落 `/tmp/nats`，pod 重启后 stream 和 durable consumer 进度丢失，重启 Core + consumer 即恢复，不影响本任务验证目标（见 §4.6、§8.2）。
- 仅用于集成测试验证，不修改 `reconcile-ha-live-deps.yaml`。

---

## 10. 验收标准

```bash
cd repo
make test                    # 单元测试通过
make validate-architecture   # 架构边界校验通过
git diff --check             # 无空白错误
```

集成测试手动运行：
```bash
make deps                    # 启动本地 NATS
go test ./pkg/adapters/nats/ -v -run Integration
go test ./services/metering-service/internal/eventconsumer/ -v

# Consumer 端到端集成测试（指定真实 NATS 地址）
ANI_TEST_NATS_URL=nats://10.10.1.66:31062 \
  go test ./services/metering-service/internal/eventconsumer/ -v -run Integration -tags integration

# task 流示例 consumer 端到端集成测试（指定真实 NATS 地址）
ANI_TEST_NATS_URL=nats://10.10.1.66:31062 \
  go test ./services/task-service/internal/taskconsumer/ -v -run Integration -tags integration
```

集成测试可选路径（无 docker，走服务器 port-forward，见 §9.2.1）：
```bash
# 1. 终端 1：保持 port-forward
kubectl port-forward svc/ani-reconcile-ha-nats 4222:4222 -n ani-system

# 2. 先改 bootstrap/nats.go 的 ANI_EVENTS 为 InterestPolicy，再启动 Core 服务
#    （ensureStreams 自动建 ANI_TASKS / ANI_EVENTS，无需手动建 stream）
#    若已建过错配的 WorkQueuePolicy ANI_EVENTS：nats stream rm ANI_EVENTS -f

# 3. 终端 2：跑集成测试（连接地址仍是 localhost:4222）
go test ./pkg/adapters/nats/ -v -run Integration
go test ./services/metering-service/internal/eventconsumer/ -v
```

---

## 11. 风险与注意事项

| 风险 | 应对 |
|---|---|
| `natsgo.JetStreamContext` 是具体类型不是接口 | 在 adapter 内部定义 `jetStream` 接口包装，便于单测 mock |
| `ports.Logger` 可能不存在统一 port | 用 `log/slog`，示例 consumer 是临时的，后续真实消费逻辑接入后再调整 |
| metering-service 的 main/bootstrap 入口位置 | 开发时先定位入口，确认插入点不干扰现有 gRPC/重试队列 |
| NATS headers 的 key 命名规范 | 用小写连字符（HTTP header 风格）：`tenant-id`/`aggregate-id`/`event-type`/`occurred-at` |
| 现有 `MessageBus` 调用方注入 `NewMessageBus(js)` 改签名 | 新增 logger 参数，需同步更新所有调用点（`pkg/bootstrap/nats.go` 等） |
| `ANI_EVENTS` 改 InterestPolicy 影响现有订阅方 | 现状无 Subscribe 调用方（message_bus.go Subscribe 从未被调用），改 policy 无破坏性影响 |
| Ack/Nak 改业务层决定后，旧 handler 若只返回 error 不调 Ack/Nack | 消息会因 AckWait 超时被 NATS 自动重投，不会丢失；但应通知所有 handler 作者改调用方式 |

---

## 12. 现有代码缺口与修复对照

| 缺口 | 现状 | 本任务修复 |
|---|---|---|
| `Subscribe` 丢弃 handler error | `_ = handler(ctx, message{msg: msg})` | 记录日志，Ack/Nak 交业务层 |
| `Subscribe` 无 panic recover | 无 defer/recover | panic → recover + Nak + log |
| `Subscribe` 自动 Ack/Nak | 成功 Ack，error Nak | 改为业务层决定，adapter 不自动处理 |
| `Subscribe` 无 AckWait/MaxDeliver | 未配置 | SubscribeOptions 扩展（可选字段） |
| `Publish` 丢弃 envelope 元数据 | 只发 `event.Payload` | 写进 NATS headers |
| `Message` 无 Headers 访问 | 只有 Subject/Data/Ack/Nack | 新增 Headers() 方法 |
| adapter 无 logger | 无日志输出 | 注入 logger |
| `ANI_EVENTS` stream 错配 WorkQueuePolicy | fan-out 事件被消费后删除 | 改为 InterestPolicy |
| NATS adapter 无任何测试 | 无 `*_test.go` | 新增单测 + 集成测试 |
