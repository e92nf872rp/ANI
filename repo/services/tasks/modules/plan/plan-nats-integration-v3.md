# 任务B：NATS 接入（MessageBus 健壮性 + 示例 consumer）落地计划 v3

> 状态: 计划修订版（v2 基础上按 review 两点改进调整）
> 创建日期: 2026-07-28
> 修订日期: 2026-08-03
> 负责人: kjs
> 前置文档: `plan-nats-integration-v2.md`（v2 已实现，本文为增量修订）

---

## 0. 修订背景（两点改进）

v2 已落地，review 后提出两点改进：

1. **消息处理上下文独立化**：[message_bus.go:85](file:///e:/go/project/ANI/repo/pkg/adapters/nats/message_bus.go) 的 `handler(ctx, pMsg)` 改为 `handler(context.Background(), pMsg)`。每条消息的处理不再绑定 `Subscribe` 调用方的 ctx，避免订阅 ctx 取消时正在处理的消息被中途打断。
2. **Ack/Nak 收回 adapter，业务侧用返回值表达意图**：不再由业务 handler 显式调 `msg.Ack/Nack`，改由 adapter 根据 handler 返回值统一 ack/nak。业务侧通过 `返回 nil = 已处理（Ack）`、`返回 error = 需重投（Nak）` 来兼容，其他语义差异由业务侧自行映射。

> v2 的 §4.3「Ack/Nak 业务层决定」整体反转：adapter 重新接管 ack/nak，业务侧不再触碰 Message 的 Ack/Nack。

---

## 1. 与 v2 的差异总览

| 维度 | v2（现状） | v3（本次改进） |
|---|---|---|
| 消息处理 ctx | `handler(ctx, pMsg)` 绑定订阅 ctx | `handler(context.Background(), pMsg)` 独立上下文 |
| Ack/Nak 归属 | 业务层显式调 `msg.Ack/Nack` | adapter 根据 handler 返回值统一 ack/nak |
| `ports.Message` 接口 | 含 `Ack(ctx)/Nack(ctx)` | **去掉 `Ack/Nack`**，业务侧无该能力 |
| handler 返回值语义 | 仅用于日志，与 ack/nak 无关 | `nil→Ack`、`error→Nak`，返回值即 ack/nak 意图 |
| panic 兜底 | recover → Nak | 不变 |
| 业务侧兼容 | handler 内调 Ack/Nack | handler 用返回值表达，去掉所有 Ack/Nack 调用 |
| Publish 写 headers | 已实现 | 不变 |
| SubscribeOptions | 已含 AckWait/MaxDeliver | 不变 |
| ANI_EVENTS=Interest | 已改 | 不变 |
| Subscribe 的 ctx 参数 | 透传给 handler | 保留签名不用（仅建立订阅语义），加注释说明 |

---

## 2. 核心决策（已敲定）

| 决策点 | 结论 | 理由 |
|---|---|---|
| error 二义性 | 纯返回值，业务吞错误 | adapter 只认 `nil→Ack / error→Nak`；毒丸/跳过/幂等场景由业务侧吞错误返回 nil |
| 吞错误观测性 | 两层记日志 | 业务侧记 error 日志（跳过原因）+ adapter 侧记 warn 日志（Nak 重投），跳过和重投都有观测 |
| Message.Ack/Nack | 方案 A，从接口去掉 | 编译期禁止业务调 ack/nak，彻底落实"不让业务 ack/nak" |
| 单测追踪 ack/nak | 选项 2，不追踪 | adapter 直接调 `msg.Ack()/msg.Nak()`，无内部接口；ack/nak 正确性靠集成测试覆盖；符合 Karpathy 原则五 |
| Subscribe ctx 参数 | 保留不用 | 签名零破坏，注释说明"仅用于建立订阅语义"；接口实现方法的未用参数 lint 默认豁免 |

---

## 3. 核心变更

### 3.1 Port 契约变更（pkg/ports/message_bus.go）

`Message` 接口去掉 `Ack/Nack`，业务侧不再具备 ack/nak 能力：

```go
type Message interface {
    Subject() string
    Data() []byte
    Headers() map[string][]string
    // 去掉 Ack(ctx)/Nack(ctx)：ack/nak 由 adapter 根据 handler 返回值统一处理
}

// MessageHandler 语义变更：
//   返回 nil   → adapter 调 Ack（消息已处理）
//   返回 error → adapter 调 Nak（消息需重投）
//   panic      → adapter recover 后调 Nak（兜底重投）
//
// 业务侧兼容契约（重要）：
//   - 处理成功 / 毒丸跳过 / 幂等跳过 → 返回 nil（业务侧自行记 error 日志后吞掉错误）
//   - 可恢复失败（DB 不可用等）       → 返回 error（adapter 自动 Nak 重投）
//   - 业务侧需自行判断"该跳过"还是"该重投"，吞错误时必须记日志便于排查
type MessageHandler func(context.Context, Message) error
```

> `MessageBus.Subscribe` 签名保留 `ctx context.Context`（不破坏 port 契约和调用方），但 ctx 不再透传给 handler——handler 固定用 `context.Background()`。Subscribe 的 ctx 仅用于建立订阅的语义契约，当前 NATS 异步回调模型未使用该 ctx。

### 3.2 Adapter 变更（pkg/adapters/nats/message_bus.go）

#### 3.2.1 handlerFunc 改写（对应 L70-91）

```go
handlerFunc := func(msg *natsgo.Msg) {
    var pMsg ports.Message
    if b.msgFactory != nil {
        pMsg = b.msgFactory(msg)
    } else {
        pMsg = message{msg: msg}
    }
    defer func() {
        if r := recover(); r != nil {
            if b.logger != nil {
                b.logger.Error("handler panic recovered, nacking for redelivery",
                    "subject", msg.Subject, "panic", r)
            }
            // panic 兜底：消息状态未知，Nak 触发重投
            _ = msg.Nak()
        }
    }()
    // 每条消息独立上下文：不绑定 Subscribe 调用方 ctx，
    // 避免订阅 ctx 取消时正在处理的消息被中断；业务侧需 timeout 自行 WithTimeout
    if err := handler(context.Background(), pMsg); err != nil {
        if b.logger != nil {
            b.logger.Warn("handler returned error, nacking for redelivery",
                "subject", msg.Subject, "err", err)
        }
        _ = msg.Nak()
        return
    }
    _ = msg.Ack()
}
```

关键点：
- **L85 改为 `context.Background()`**：满足改进一
- **返回 nil → `msg.Ack()`，返回 error → `msg.Nak()`**：满足改进二
- panic recover 兜底 Nak 不变
- adapter 直接调底层 `msg.Ack()/msg.Nak()`，不引入内部接口（决策：单测不追踪 ack/nak）

#### 3.2.2 message struct 调整

`Ack/Nack` 方法从 `ports.Message` 接口移除后，`message` struct 不再需要实现这两个方法作为接口实现。adapter 通过底层 `*natsgo.Msg` 直接调 `msg.Ack()/msg.Nak()`（见 3.2.1）。

```go
type message struct {
    msg *natsgo.Msg
}

func (m message) Subject() string              { return m.msg.Subject }
func (m message) Data() []byte                  { return m.msg.Data }
func (m message) Headers() map[string][]string  { return map[string][]string(m.msg.Header) }
// 不再有 Ack/Nack 方法——ack/nak 由 adapter 通过底层 *natsgo.Msg 调用
```

#### 3.2.3 Subscribe 的 ctx 注释

```go
// Subscribe 订阅 subject。
// ctx 仅用于建立订阅的语义契约，当前 NATS 异步回调模型未使用该 ctx；
// handler 固定收到 context.Background()，消息处理不受订阅 ctx 生命周期影响。
func (b *MessageBus) Subscribe(ctx context.Context, opts ports.SubscribeOptions, handler ports.MessageHandler) (ports.Subscription, error) {
```

---

## 4. 业务侧兼容契约（"其他问题让业务侧来兼容"）

业务侧的兼容契约：**handler 返回值即 ack/nak 意图**。

### 4.1 兼容映射表

| 业务场景 | v2 做法 | v3 做法（兼容） | handler 返回值 | 业务侧日志 |
|---|---|---|---|---|
| 处理成功 | `msg.Ack(ctx)` | 去掉调用 | `return nil` | 可选 Info |
| 毒丸消息（解析失败） | `msg.Ack(ctx)` 跳过 | 去掉调用 | `return nil` | **必须 Error**（跳过原因） |
| 幂等跳过（抢租约失败/重复） | `msg.Ack(ctx)` 跳过 | 去掉调用 | `return nil` | **必须 Error 或 Warn**（跳过原因） |
| 可恢复失败（DB 不可用等） | `msg.Nack(ctx)` | 去掉调用 | `return errors.New(...)` | 可选 Error |
| panic | adapter recover → Nak | 不变 | （未返回） | adapter 记 Error |

### 4.2 观测性两层保证

- **业务侧**：吞错误返回 nil 时**必须记 error/warn 日志**（含 subject、err、跳过原因），否则"跳过"场景完全静默，出问题无法排查
- **adapter 侧**：handler 返回 error 时记 warn 日志（含 subject、err），Nak 重投有记录
- 两层日志互补：跳过看业务日志，重投看 adapter 日志

### 4.3 业务侧守纪律要点

- **毒丸吞错误反直觉**：解析失败本该 `return err`，v3 要求业务写 `log; return nil` 吞掉。新人易顺手 `return err` 导致毒丸无限重投到 MaxDeliver。**consumer 文件头注释必须写明这条契约**。
- **"该跳过" vs "该重投"由业务判断**：唯一键冲突（该跳过 Ack）和 DB 连接失败（该重投 Nak）都是 error，v3 要求业务侧自己区分——前者吞掉返回 nil，后者原样返回 error。adapter 不负责区分。

---

## 5. 需要改的文件与改动点

### 5.1 eventconsumer/consumer.go（[consumer.go:62-87](file:///e:/go/project/ANI/repo/services/metering-service/internal/eventconsumer/consumer.go)）

```go
// handle 是消息处理回调：从 headers 重建租户上下文，解析 payload，按返回值表达 ack/nak 意图。
// 返回 nil → adapter Ack；返回 error → adapter Nak 重投。
// 毒丸/跳过场景必须记 error 日志后返回 nil（吞错误），不可 return err 否则毒丸无限重投。
func (c *Consumer) handle(ctx context.Context, msg ports.Message) error {
    tenantID := msg.Headers()["tenant-id"]
    if len(tenantID) > 0 {
        c.safeLog(func(l *slog.Logger) { l.InfoContext(ctx, "recovered tenant context", "tenant-id", tenantID[0]) })
    }
    var event instanceEvent
    if err := json.Unmarshal(msg.Data(), &event); err != nil {
        // 毒丸消息：记 error 日志后返回 nil，让 adapter Ack 跳过
        c.safeLog(func(l *slog.Logger) {
            l.ErrorContext(ctx, "parse event failed (poison message), ack to skip", "err", err)
        })
        return nil  // 原为 _ = msg.Ack(ctx); return nil
    }
    c.safeLog(func(l *slog.Logger) {
        l.InfoContext(ctx, "received instance event", "event_type", event.EventType, "instance_id", event.InstanceID)
    })
    // 示例阶段：返回 nil 让 adapter Ack
    return nil  // 原为 _ = msg.Ack(ctx); return nil
    // 真实逻辑接入后：
    //   if err := c.metering.StartCollection(ctx, ...); err != nil {
    //       return err  // 可恢复失败 → adapter Nak 重投
    //   }
    //   return nil     // 成功 → adapter Ack
}
```

### 5.2 taskconsumer/consumer.go（[consumer.go:63-88](file:///e:/go/project/ANI/repo/services/task-service/internal/taskconsumer/consumer.go)）

同样去掉两处 `_ = msg.Ack(ctx)`，改为 `return nil`。毒丸返回 nil（记 error 日志），成功返回 nil，业务失败返回 error。

### 5.3 注释更新

两个 consumer 文件头注释里"业务层 Ack/Nak 决策"改为"业务层用返回值表达 ack/nak 意图，adapter 统一执行；毒丸/跳过场景必须记日志后返回 nil"。

---

## 6. 测试调整

### 6.1 adapter 单测（pkg/adapters/nats/message_bus_test.go）

#### fakeMessage 调整

`ports.Message` 去掉 Ack/Nack 后，fakeMessage 不再需要实现 Ack/Nack 满足接口；决策不追踪 ack/nak，故 fakeMessage 彻底去掉 ack/nack 相关字段和方法：

```go
type fakeMessage struct {
    subject string
    data    []byte
    headers map[string][]string
}

func (f *fakeMessage) Subject() string                    { return f.subject }
func (f *fakeMessage) Data() []byte                       { return f.data }
func (f *fakeMessage) Headers() map[string][]string        { return f.headers }
// 不再有 Ack/Nack/ackCalled/nackCalled/WasAcked/WasNacked
```

#### 测试用例调整

| v2 用例 | v3 调整 |
|---|---|
| TestHandlerErrorNoAutoAck（返回 error 不自动 ack/nak） | **删除**：v3 返回 error 必然 Nak，无需单测验证（集成测试覆盖） |
| TestHandlerNilNoAutoAck（返回 nil 不自动 ack） | **删除**：v3 返回 nil 必然 Ack，无需单测验证 |
| TestHandlerOwnAck（业务自己调 Ack） | **删除**：业务不再调 Ack |
| TestHandlerOwnNack（业务自己调 Nak） | **删除**：业务不再调 Nack |
| TestHandlerPanicBeforeAck | **保留**：panic → recover → 不崩溃（不验证 Nak 调用，只验证不崩溃） |
| TestHandlerPanicAfterAck | **删除**：业务不再调 Ack，panic 一定在 Ack 前，合并到 PanicBeforeAck |
| TestPublishSuccess | 不变 |
| TestPublishEmptySubject | 不变 |
| TestSubscribeEmptySubject | 不变 |
| 新增 TestHandlerBackgroundCtx（可选） | 验证 handler 收到的 ctx 是 `context.Background()`：注入检测 ctx 的 handler，断言 `ctx == context.Background()` 或 ctx 未被取消 |

### 6.2 consumer 单测（eventconsumer/consumer_test.go / taskconsumer/consumer_test.go）

#### mockMessage 调整

去掉 Ack/Nack 方法及相关字段：

```go
type mockMessage struct {
    headers map[string][]string
    data    []byte
    // 去掉 ackCalled/nackCalled/ackErr/nackErr
}
func (m *mockMessage) Subject() string              { return "" }
func (m *mockMessage) Data() []byte                 { return m.data }
func (m *mockMessage) Headers() map[string][]string { return m.headers }
// 不再有 Ack/Nack
```

#### 用例调整

| v2 用例 | v3 调整 |
|---|---|
| TestConsumerStart | 不变（验证 Subscribe 参数） |
| TestConsumerHandlePoisonMessage | 断言 `err == nil`（原 `ackCalled == 1`） |
| TestConsumerHandleSuccess | 断言 `err == nil`（原 `ackCalled == 1`） |
| TestConsumerStop / StopWithoutStart | 不变 |

### 6.3 adapter 集成测试（pkg/adapters/nats/integration_test.go）

handler 内的显式 Ack/Nack 调用改为返回值：

| 场景 | v2 handler | v3 handler |
|---|---|---|
| TestIntegrationPublishSubscribeHeaders | `return msg.Ack(ctx)` | `return nil` |
| TestIntegrationAckBusinessDecision | `return msg.Ack(ctx)` | `return nil` |
| TestIntegrationPanicRecover | `panic(...)` / `return msg.Ack(ctx)` | `panic(...)` / `return nil` |
| TestIntegrationNakDelayedRedelivery | `return msg.Nack(ctx)` / `return msg.Ack(ctx)` | `return errors.New("retry")` / `return nil` |
| TestIntegrationMaxDeliverStop | `return msg.Nack(ctx)` | `return errors.New("always fail")` |
| TestIntegrationInterestFanout | `return msg.Ack(ctx)` | `return nil` |

集成测试断言不变（仍验证投递次数、不崩溃、MaxDeliver 停投、fan-out），因为 adapter 在真实 NATS 上的 ack/nak 行为与 v2 等价。**这是 ack/nak 正确性的主要验证载体**（决策：单测不追踪，靠集成测试覆盖）。

### 6.4 consumer 集成测试（eventconsumer/taskconsumer integration_test.go）

**不动**：测试通过日志断言验证链路（`received instance event`、`recovered tenant context`、`parse event failed` 等），handler 内部返回值变化不影响日志输出。日志断言全部保留。

---

## 7. 不变项（与 v2 保持一致）

- Publish 写 headers（已实现，不动）
- SubscribeOptions 的 AckWait/MaxDeliver（已实现，不动）
- ANI_EVENTS = InterestPolicy（已改，不动）
- ANI_TASKS = WorkQueuePolicy（不动）
- panic recover 兜底 Nak（逻辑不变，仅 ctx 改 Background）
- 死信方案（不建死信 stream，MaxDeliver 兜底）
- NewMessageBus(js, logger) 签名（不动）
- 崩溃恢复设计（不在本任务）

---

## 8. 验收标准

```bash
cd repo
make test                    # 单元测试通过（含改后的 adapter/consumer 单测）
make validate-architecture   # 架构边界校验通过
git diff --check             # 无空白错误
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

## 9. 风险与注意事项

| 风险 | 应对 |
|---|---|
| `context.Background()` 丢失订阅 ctx 的 trace/超时 | 业务侧需 timeout 时自行 `context.WithTimeout(context.Background(), ...)`；现有 consumer 用 Headers 重建租户，不依赖 ctx |
| 业务侧"正常跳过"需映射为返回 nil | 兼容清单 §4 已列明，业务侧内部记日志后返回 nil |
| 业务侧"可恢复失败"需映射为返回 error | 业务侧把 DB 不可用等包装为 error 返回，adapter 自动 Nak |
| 毒丸吞错误反直觉，新人易写错 | consumer 文件头注释必须写明契约；code review 重点检查毒丸路径是否返回 nil |
| "该跳过" vs "该重投"业务侧自行判断 | 唯一键冲突（跳过）vs DB 连接失败（重投）由业务区分，adapter 不负责 |
| 去掉 Message.Ack/Nack 后旧 handler 误调 | 编译期拦截（方法不存在），编译失败即暴露所有调用点，逐一改 |
| 跳过场景静默无记录 | 业务侧必须记 error/warn 日志（§4.2 两层日志保证），否则出问题无法排查 |
| Subscribe 的 ctx 未使用 | 接口实现方法的未用参数 lint 默认豁免；注释说明"仅用于建立订阅语义" |

---

## 10. 改动文件清单

| 文件 | 类型 | 改动 |
|---|---|---|
| `pkg/ports/message_bus.go` | 修改 | Message 去掉 Ack/Nack；MessageHandler 注释更新语义和兼容契约 |
| `pkg/adapters/nats/message_bus.go` | 修改 | L85 改 Background；handlerFunc 改返回值驱动 ack/nak；message struct 去掉 Ack/Nack；Subscribe ctx 加注释 |
| `pkg/adapters/nats/message_bus_test.go` | 修改 | fakeMessage 去 Ack/Nack；用例删除/保留/新增按 §6.1 |
| `pkg/adapters/nats/integration_test.go` | 修改 | handler 显式 Ack/Nack 改返回值 |
| `services/metering-service/internal/eventconsumer/consumer.go` | 修改 | handle 去掉 msg.Ack，改返回值；注释更新 |
| `services/metering-service/internal/eventconsumer/consumer_test.go` | 修改 | mockMessage 去 Ack/Nack；断言改 err |
| `services/metering-service/internal/eventconsumer/integration_test.go` | 不动 | 日志断言不变 |
| `services/task-service/internal/taskconsumer/consumer.go` | 修改 | 同 eventconsumer |
| `services/task-service/internal/taskconsumer/consumer_test.go` | 修改 | 同 eventconsumer |
| `services/task-service/internal/taskconsumer/integration_test.go` | 不动 | 日志断言不变 |
