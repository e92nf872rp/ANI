# 任务B：NATS 接入（MessageBus 健壮性 + 示例 consumer）落地计划 v4

> 状态: 计划修订版（v3 基础上按 review 两点改进调整）
> 创建日期: 2026-08-03
> 负责人: kjs
> 前置文档: `plan-nats-integration-v3.md`（v3 已实现，本文为增量修订）

---

## 0. 修订背景（两点改进）

v3 已落地，review 后提出两点改进：

1. **Subscribe 删除 ctx 参数**：`message_bus.go` 的 `Subscribe(ctx context.Context, ...)` 中 ctx 在 v3 已确认不使用（handler 固定用 `context.Background()`，NATS 异步回调模型也不消费该 ctx），是死参数。删除 ctx 参数，保持接口简洁。
2. **ack/nak 返回值不再忽略**：`message_bus.go` 中三处 `_ = msg.Nak()` / `_ = msg.Ack()` 忽略了返回值，Ack/Nak 调用本身失败时（连接断开、JetStream 不可用）无任何日志。改为接住 error 并在非 nil 时打 Error 日志，后续如果噪音大再调低级别。

---

## 1. 与 v3 的差异总览

| 维度 | v3（现状） | v4（本次改进） |
|---|---|---|
| `Subscribe` 签名 | `Subscribe(ctx, opts, handler)` | `Subscribe(opts, handler)`，删除 ctx |
| `consumer.Start` 签名 | `Start(ctx)` | `Start()`，删除 ctx（唯一用途是传给 Subscribe） |
| ack/nak 返回值处理 | `_ = msg.Nak()` / `_ = msg.Ack()` 忽略 | 接住 error，非 nil 时打 Error 日志 |
| `TestHandlerBackgroundCtx` | 传入被取消的 ctx 验证 handler 收到 Background | **删除**：前提不存在了 |
| Subscribe ctx 注释 | "ctx 仅用于建立订阅的语义契约，当前未使用" | 删除注释（参数已删） |
| Publish / SubscribeOptions / Message 接口 | 不变 | 不变 |
| ANI_EVENTS=Interest / ANI_TASKS=WorkQueue | 不变 | 不变 |
| consumer handle 逻辑 | 不变 | 不变 |

---

## 2. 核心决策（已敲定）

| 决策点 | 结论 | 理由 |
|---|---|---|
| consumer Start(ctx) 是否删 ctx | **删** | Start 的 ctx 唯一用途是传给 Subscribe，删了 Subscribe ctx 后是死参数；Stop(ctx) 保留（Drain 需要超时控制） |
| TestHandlerBackgroundCtx 用例 | **删除** | 删 Subscribe ctx 后"传入被取消的 ctx"这个前提不存在了；handler 收 Background 是实现细节，靠集成测试覆盖 |
| ack/nak 失败日志级别 | **Error** | Ack/Nak 失败指向 NATS 连接断开/JetStream 不可用，消息可能丢或重复投递；后续噪音大再统一降到 Warn |
| 是否抽 ack/nak helper | **不抽** | 三处调用模式相似但场景不同（两处 Nak、一处 Ack），内联更直接可读；helper 只省 2-3 行，增加间接性不划算（Karpathy 原则二/五） |
| 文档四件套更新 | **本次不更新** | 等代码实现完再统一更新 nats-integration-a.md / README / CURRENT-SPRINT / ANI-06 |

---

## 3. 核心变更

### 3.1 Port 契约变更（pkg/ports/message_bus.go）

`MessageBus.Subscribe` 签名删除 ctx：

```go
type MessageBus interface {
    Publish(ctx context.Context, event EventEnvelope, opts PublishOptions) error
    Subscribe(opts SubscribeOptions, handler MessageHandler) (Subscription, error)
}
```

> Publish 保留 ctx（用于 PubOpt.Context(ctx) 控制 publish 超时/取消），Subscribe 删除 ctx。
> `Subscription.Drain(ctx)` 保留 ctx（Drain 是同步操作，ctx 控制超时合理）。

### 3.2 Adapter 变更（pkg/adapters/nats/message_bus.go）

#### 3.2.1 Subscribe 签名删除 ctx

```go
// Subscribe 订阅 subject。
// 消息处理独立于调用方上下文，handler 固定收到 context.Background()。
func (b *MessageBus) Subscribe(opts ports.SubscribeOptions, handler ports.MessageHandler) (ports.Subscription, error) {
    if opts.Subject == "" {
        return nil, fmt.Errorf("message bus subscribe: subject required")
    }
    // ... 原逻辑不变
}
```

#### 3.2.2 三处 ack/nak 失败打日志

**panic 兜底 Nak（原 L80-89）：**

```go
defer func() {
    if r := recover(); r != nil {
        if b.logger != nil {
            b.logger.Error("handler panic recovered, nacking for redelivery",
                "subject", msg.Subject, "panic", r)
        }
        if err := msg.Nak(); err != nil {
            if b.logger != nil {
                b.logger.Error("nack failed after panic recovery",
                    "subject", msg.Subject, "err", err)
            }
        }
    }
}()
```

**handler 返回 error → Nak（原 L92-100）：**

```go
if err := handler(context.Background(), pMsg); err != nil {
    if b.logger != nil {
        b.logger.Warn("handler returned error, nacking for redelivery",
            "subject", msg.Subject, "err", err)
    }
    if err := msg.Nak(); err != nil {
        if b.logger != nil {
            b.logger.Error("nack failed after handler error",
                "subject", msg.Subject, "err", err)
        }
    }
    return
}
if err := msg.Ack(); err != nil {
    if b.logger != nil {
        b.logger.Error("ack failed after handler success",
            "subject", msg.Subject, "err", err)
    }
}
```

关键点：
- 三处 ack/nak 调用从 `_ = msg.Nak()` / `_ = msg.Ack()` 改为 `if err := ...; err != nil { log }`
- 日志统一 Error 级别，内容区分场景（"nack failed after panic recovery" / "nack failed after handler error" / "ack failed after handler success"）
- 不抽 helper，内联三处

### 3.3 Consumer 变更

#### 3.3.1 eventconsumer/consumer.go

```go
// Start 订阅 ani.events.instance.>，配置 AckWait=30s、MaxDeliver=10、MaxInflight=16。
func (c *Consumer) Start() error {
    sub, err := c.bus.Subscribe(ports.SubscribeOptions{
        Subject:     "ani.events.instance.>",
        Consumer:    "metering-example",
        Queue:       "metering",
        MaxInflight: 16,
        AckWait:     30 * time.Second,
        MaxDeliver:  10,
    }, c.handle)
    if err != nil {
        return err
    }
    c.sub = sub
    return nil
}

// Stop 关闭订阅。保留 ctx（Drain 需要超时控制）。
func (c *Consumer) Stop(ctx context.Context) error {
    if c.sub != nil {
        return c.sub.Drain(ctx)
    }
    return nil
}
```

#### 3.3.2 taskconsumer/consumer.go

同样去掉 `Start` 的 ctx 参数，`Subscribe` 调用去掉 ctx。

---

## 4. 测试调整

### 4.1 adapter 单测（pkg/adapters/nats/message_bus_test.go）

#### Subscribe 调用签名调整

3 处 `bus.Subscribe(context.Background(), ...)` → `bus.Subscribe(...)`，去掉 ctx 参数。

#### TestHandlerBackgroundCtx 删除

删 Subscribe ctx 后，"传入被取消的 ctx 验证 handler 收到 Background" 这个前提不存在了。handler 收到 `context.Background()` 是 adapter 实现细节，靠集成测试覆盖。

#### mockMessageBus.Subscribe 签名调整（consumer 单测）

```go
// 原
func (m *mockMessageBus) Subscribe(ctx context.Context, opts ports.SubscribeOptions, handler ports.MessageHandler) (ports.Subscription, error)
// 改为
func (m *mockMessageBus) Subscribe(opts ports.SubscribeOptions, handler ports.MessageHandler) (ports.Subscription, error)
```

### 4.2 adapter 集成测试（pkg/adapters/nats/integration_test.go）

7 处 `env.bus.Subscribe(context.Background(), ...)` → `env.bus.Subscribe(...)`，去掉 ctx 参数。

### 4.3 consumer 单测（eventconsumer/consumer_test.go / taskconsumer/consumer_test.go）

- `mockMessageBus.Subscribe` 签名去掉 ctx（见 4.1）
- 2 处 `c.Start(ctx)` → `c.Start()`，去掉 ctx
- mockMessageBus 去掉 ctx 后 `import "context"` 可能变为未使用，按编译器提示处理

### 4.4 consumer 集成测试（eventconsumer/taskconsumer integration_test.go）

2 处 `consumer.Start(ctx)` → `consumer.Start()`，去掉 ctx。

### 4.5 测试用例汇总

| 文件 | 调整 |
|---|---|
| `pkg/adapters/nats/message_bus_test.go` | 3 处 Subscribe 去 ctx；删除 TestHandlerBackgroundCtx |
| `pkg/adapters/nats/integration_test.go` | 7 处 Subscribe 去 ctx |
| `eventconsumer/consumer_test.go` | mockMessageBus.Subscribe 去 ctx；2 处 Start 去 ctx |
| `eventconsumer/integration_test.go` | 2 处 Start 去 ctx |
| `taskconsumer/consumer_test.go` | 同 eventconsumer |
| `taskconsumer/integration_test.go` | 同 eventconsumer |

---

## 5. 不变项（与 v3 保持一致）

- handler 用 `context.Background()`（v3 改进，不动）
- 返回值驱动 ack/nak：`nil→Ack` / `error→Nak` / `panic→Nak`（v3 改进，不动）
- `ports.Message` 接口无 Ack/Nack（v3 改进，不动）
- 毒丸消息业务侧返回 nil 吞错误（v3 改进，不动）
- Publish 写 headers（v2 实现，不动）
- SubscribeOptions 的 AckWait/MaxDeliver（v2 实现，不动）
- ANI_EVENTS = InterestPolicy / ANI_TASKS = WorkQueuePolicy（不动）
- NewMessageBus(js, logger) 签名（不动）

---

## 6. 改动文件清单

| 文件 | 类型 | 改动 |
|---|---|---|
| `pkg/ports/message_bus.go` | 修改 | `MessageBus.Subscribe` 签名删除 ctx |
| `pkg/adapters/nats/message_bus.go` | 修改 | Subscribe 签名删 ctx；三处 ack/nak 接住 error 打 Error 日志 |
| `pkg/adapters/nats/message_bus_test.go` | 修改 | 3 处 Subscribe 去 ctx；删除 TestHandlerBackgroundCtx |
| `pkg/adapters/nats/integration_test.go` | 修改 | 7 处 Subscribe 去 ctx |
| `services/metering-service/internal/eventconsumer/consumer.go` | 修改 | Start 删 ctx；Subscribe 调用去 ctx |
| `services/metering-service/internal/eventconsumer/consumer_test.go` | 修改 | mockMessageBus.Subscribe 去 ctx；2 处 Start 去 ctx |
| `services/metering-service/internal/eventconsumer/integration_test.go` | 修改 | 2 处 Start 去 ctx |
| `services/task-service/internal/taskconsumer/consumer.go` | 修改 | 同 eventconsumer |
| `services/task-service/internal/taskconsumer/consumer_test.go` | 修改 | 同 eventconsumer |
| `services/task-service/internal/taskconsumer/integration_test.go` | 修改 | 同 eventconsumer |

---

## 7. 验收标准

```bash
cd repo

# 单元测试
go test ./pkg/adapters/nats/...
go test ./services/metering-service/internal/eventconsumer/...
go test ./services/task-service/internal/taskconsumer/...

# 架构边界守卫
python scripts/validate_component_imports.py --root .

# gofmt 格式
gofmt -l ./pkg/adapters/nats/ ./services/metering-service/internal/eventconsumer/ ./services/task-service/internal/taskconsumer/
```

集成测试手动运行（验证 adapter ack/nak 在真实 NATS 上等价）：
```bash
ANI_TEST_NATS_URL=nats://10.10.1.66:31062 \
  go test ./pkg/adapters/nats/ -v -run Integration -tags integration

ANI_TEST_NATS_URL=nats://10.10.1.66:31062 \
  go test ./services/metering-service/internal/eventconsumer/ -v -run Integration -tags integration

ANI_TEST_NATS_URL=nats://10.10.1.66:31062 \
  go test ./services/task-service/internal/taskconsumer/ -v -run Integration -tags integration
```

---

## 8. 风险与注意事项

| 风险 | 应对 |
|---|---|
| port 接口签名变更属跨层契约变更 | 编译期暴露所有调用点，逐一改；当前仅 2 个 consumer 调用 Subscribe |
| ack/nak 失败日志可能噪音大 | 初始 Error 级别，后续观察后统一降到 Warn 或 Debug |
| `context` import 在部分文件可能变为未使用 | 按编译器提示逐一处理（consumer_test.go 的 mockMessageBus 去掉 ctx 后可能不再需要 context import） |
| consumer Start 删 ctx 后调用方需同步 | 当前调用方只有测试，编译器会暴露 |
