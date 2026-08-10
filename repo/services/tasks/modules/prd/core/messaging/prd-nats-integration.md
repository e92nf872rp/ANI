# PRD: NATS 接入（MessageBus 健壮性 + 示例 consumer）

> 来源计划: `repo/services/tasks/modules/plan/plan-nats-integration-v2.md` §1.1 第 1-7a 项
> 范围确认: 1D（完成 §1.1 第 1-7a 项，不含 7b 接线和崩溃恢复 PR-7）

---

## 1. Introduction/Overview

ANI 的 NATS 消息总线 adapter 当前存在多处健壮性缺陷：`Subscribe` 自动根据 handler 返回值决定 Ack/Nak（无法表达"抢租约失败要 Ack 跳过"等业务语义）、无 panic recover、`Publish` 丢弃 `EventEnvelope` 元数据、`Message` 接口无 Headers 访问、`ANI_EVENTS` stream 错配 WorkQueuePolicy（event 无法 fan-out 给多消费者）、adapter 无日志、NATS adapter 无任何测试。

本 PRD 落地 §1.1 第 1-7a 项：修复 adapter 健壮性、扩展 port 契约、修复 stream 配置、新增示例 consumer（7a，仅 consumer 代码 + 单测，不做 metering-service 接线 7b）。完成后模型导入 task 流和 metering event 流共用同一健壮 adapter，7a 示例 consumer 在本分支可独立完成、独立测试。

**设计原则**：adapter 只负责消息收发与 panic 兜底，Ack/Nak 归业务层决定；port 字段新增均为可选，不破坏现有调用方；真实环境门禁在 REAL-K8S-LAB 执行。

---

## 2. Goals

- 修复 `MessageBus.Subscribe`：Ack/Nak 改由业务层决定，保留 panic recover 兜底
- 修复 `MessageBus.Publish`：`EventEnvelope` 元数据（tenant-id/aggregate-id/aggregate-type/event-type/occurred-at）写入 NATS Message Header
- 扩展 `ports.SubscribeOptions`：新增 `AckWait`、`MaxDeliver` 可选字段（传 0 不配置）
- 扩展 `ports.Message` 接口：新增 `Headers()` 方法
- adapter 注入 `*slog.Logger`，记录 panic/error 路径
- 修复 `ANI_EVENTS` stream：WorkQueuePolicy → InterestPolicy
- 新增示例 consumer（7a）：`services/metering-service/internal/eventconsumer/consumer.go` + 单测，业务层 Ack/Nak + 日志验证链路
- 新增 adapter 单测 + 集成测试，覆盖 Publish headers、Subscribe Ack/Nak 业务层决定、panic recover、AckWait/MaxDeliver 配置
- 通过 `make test`、`make validate-architecture`、`git diff --check`
- 真实环境门禁在 REAL-K8S-LAB 验证（含本地 docker-compose NATS 集成测试作为可选手动项）

---

## 3. User Stories

### US-001: 扩展 ports.SubscribeOptions 和 ports.Message 契约
**Description:** 作为开发者，我需要扩展 port 契约字段，让 consumer 能按需配置 AckWait/MaxDeliver 并读取消息 headers。

**Acceptance Criteria:**
- [ ] `ports.SubscribeOptions` 新增 `AckWait time.Duration` 和 `MaxDeliver int` 字段，均为可选（传 0 表示不配置）
- [ ] `ports.Message` 接口新增 `Headers() map[string][]string` 方法
- [ ] 现有 `SubscribeOptions`/`Message` 调用方不传新字段时行为不变（零值兼容）
- [ ] Typecheck/lint 通过

### US-002: 修复 adapter.Publish 写入 NATS headers + 注入 logger
**Description:** 作为开发者，我需要 Publish 把 EventEnvelope 元数据写进 NATS Message Header，并注入 logger 以记录错误路径。

**Acceptance Criteria:**
- [ ] `MessageBus` 结构体新增 `logger *slog.Logger` 字段
- [ ] `NewMessageBus` 签名改为 `NewMessageBus(js natsgo.JetStreamContext, logger *slog.Logger)`，同步更新所有调用点（`pkg/bootstrap/nats.go` 等）
- [ ] `Publish` 把 `event.TenantID/AggregateID/AggregateType/EventType/OccurredAt` 写入 NATS Header，key 用小写连字符：`tenant-id`/`aggregate-id`/`aggregate-type`/`event-type`/`occurred-at`
- [ ] `Publish` 在 `opts.Subject == ""` 时返回明确 error
- [ ] Typecheck/lint 通过

### US-003: 修复 adapter.Subscribe 改业务层决定 Ack/Nak + panic recover
**Description:** 作为开发者，我需要 Subscribe 不再自动 Ack/Nak，交业务层决定，同时保留 panic recover 兜底。

**Acceptance Criteria:**
- [ ] adapter 不再根据 handler 返回值自动调 `msg.Ack/Nak`
- [ ] handler 返回 error 时 adapter 仅记录 warn 日志，不调 Ack/Nak（未调则由 NATS AckWait 超时自动重投）
- [ ] handler panic 时 recover 兜底调 `msg.Nak()` + 记录 error 日志，不崩溃
- [ ] panic 发生在业务层 Ack 之后时，兜底 Nak 被 NATS 忽略，不产生重复处理
- [ ] `SubscribeOptions.AckWait > 0` 时透传 `natsgo.AckWait`，`MaxDeliver > 0` 时透传 `natsgo.MaxDeliver`
- [ ] Typecheck/lint 通过

### US-004: 新增 message.Headers() 实现 + 内部 jetStream 接口包装
**Description:** 作为开发者，我需要 message 结构体实现 Headers()，并定义内部 jetStream 接口以便单测 mock。

**Acceptance Criteria:**
- [ ] `message.Headers()` 返回 `map[string][]string(m.msg.Header)`，`Header == nil` 时返回 nil
- [ ] 新增 `pkg/adapters/nats/jetstream_iface.go`，定义内部 `jetStream` 接口（PublishMsg/Subscribe/QueueSubscribe/StreamInfo/AddStream）
- [ ] `jetStream` 接口不暴露到 `pkg/ports/`，仅 adapter 内部使用
- [ ] `natsgo.JetStreamContext` 天然满足 `jetStream` 接口，生产路径零开销
- [ ] Typecheck/lint 通过

### US-005: 修复 ANI_EVENTS stream 配置为 InterestPolicy
**Description:** 作为开发者，我需要把 ANI_EVENTS stream 从 WorkQueuePolicy 改为 InterestPolicy，让 event 能 fan-out 给多消费者。

**Acceptance Criteria:**
- [ ] `pkg/bootstrap/nats.go` 中 `ANI_EVENTS` stream 的 `Retention` 改为 `natsgo.InterestPolicy`
- [ ] `ANI_TASKS` stream 保持 `natsgo.WorkQueuePolicy` 不变
- [ ] `make validate-architecture` 通过
- [ ] Typecheck/lint 通过

### US-006: 新增示例 consumer（7a）业务层 Ack/Nak + 单测
**Description:** 作为开发者，我需要新增 metering 示例 consumer，演示业务层 Ack/Nak 决策和 headers 租户上下文重建，并附单测。

**Acceptance Criteria:**
- [ ] 新增 `services/metering-service/internal/eventconsumer/consumer.go`
- [ ] `Consumer.Start` 调 `bus.Subscribe` 订阅 `ani.events.instance.>`，配置 `AckWait=30s`、`MaxDeliver=10`、`MaxInflight=16`、`Consumer="metering-example"`、`Queue="metering"`
- [ ] `handle` 从 `msg.Headers()["tenant-id"]` 重建租户上下文
- [ ] payload 解析失败（毒丸）→ `msg.Ack(ctx)` 跳过 + error 日志
- [ ] 业务成功 → `msg.Ack(ctx)`（示例阶段：链路验证通过即 Ack）
- [ ] 失败分类与计划文档 §6.3 一致：可恢复故障→Nack，毒丸→Ack+告警，重复调用→Ack 幂等跳过
- [ ] `Consumer.Stop` 调 `sub.Drain(ctx)`
- [ ] 新增 `consumer_test.go`，mock `ports.MessageBus` 覆盖：Start 成功、解析失败 Ack、业务成功 Ack
- [ ] Typecheck/lint 通过

### US-007: 新增 adapter 单元测试（fake/mock JetStream）
**Description:** 作为开发者，我需要用内部 jetStream 接口 mock 覆盖 adapter 的 Publish/Subscribe 健壮性路径。

**Acceptance Criteria:**
- [ ] 新增 `pkg/adapters/nats/message_bus_test.go`
- [ ] 覆盖 Publish 成功验证 headers 正确（5 个 key 均存在且值匹配）
- [ ] 覆盖 Publish subject 为空返回 error
- [ ] 覆盖 Subscribe subject 为空返回 error
- [ ] 覆盖 handler 返回 error：adapter 仅记日志，未调 msg.Ack/Nack
- [ ] 覆盖 handler 返回 nil：adapter 未自动调 Ack
- [ ] 覆盖 handler 自己调 Ack 且返回 nil：Ack 仅被业务层调一次
- [ ] 覆盖 handler 自己调 Nack 且返回 error：Nak 仅被业务层调一次
- [ ] 覆盖 handler panic（Ack 调用前）：recover + Nak 被调 + 不崩溃
- [ ] 覆盖 handler panic（Ack 调用后）：recover + Nak 被调但不重复处理
- [ ] Typecheck/lint 通过

### US-008: 新增 adapter 集成测试（连本地 docker-compose NATS）
**Description:** 作为开发者，我需要用真实 NATS（docker-compose）验证 adapter 端到端链路，包括 InterestPolicy fan-out、AckWait/MaxDeliver 配置生效、panic recover 不崩溃、Nak 延迟重投等集成场景。

**Acceptance Criteria:**
- [ ] 新增 `pkg/adapters/nats/integration_test.go`
- [ ] 前置：测试前确保 `ANI_EVENTS` stream 存在且为 InterestPolicy，`ANI_TASKS` 为 WorkQueuePolicy
- [ ] 覆盖 Publish + Subscribe 端到端：发布一条 `instance.created` 事件，consumer 收到后验证 headers（tenant-id/aggregate-id/aggregate-type/event-type/occurred-at 均匹配）
- [ ] 覆盖 Ack/Nak 业务层决定：handler 自己调 Ack/Nak，adapter 不干预（验证消息状态符合业务层调用）
- [ ] 覆盖 panic recover：handler panic → 消息被 Nak → 进程不崩溃
- [ ] 覆盖 Nak 延迟重投：handler 调 Nak → 消息延迟重投 → 第二次 handler 调 Ack
- [ ] 覆盖 MaxDeliver 满后停投：handler 持续 Nak → 到顶后 NATS 不再投递
- [ ] 覆盖 Interest fan-out：两个 durable consumer 各自收到同一事件（验证 InterestPolicy 生效，非 WorkQueue 删除语义）
- [ ] 测试后清理 stream（避免污染后续测试）
- [ ] 集成测试通过 `go test ./pkg/adapters/nats/ -v -run Integration`
- [ ] Typecheck/lint 通过

### US-009: 新增 Consumer 端到端集成测试（连真实 NATS）
**Description:** 作为开发者，我需要用真实 NATS 验证 adapter + Consumer 完整链路：adapter 发布 `instance.created` 事件 → `eventconsumer.Consumer` 通过真实 NATS 收到 → 打印业务日志，补齐单测（mock）无法覆盖的"Consumer 连上真 NATS 后真的能收到事件"这一环。

**Acceptance Criteria:**
- [ ] 新增 `services/metering-service/internal/eventconsumer/integration_test.go`（`//go:build integration` build tag）
- [ ] 覆盖 Consumer 端到端：adapter 发布一条 `instance.created` 事件到 `ani.events.instance.>`，`eventconsumer.Consumer` 通过真实 NATS 收到并打印业务日志（`received instance event`），验证 adapter + consumer 完整链路
- [ ] 覆盖 Consumer 毒丸消息：发布非法 JSON payload，Consumer 解析失败后 Ack 跳过并打印 `parse event failed` error 日志
- [ ] 集成测试通过 `go test ./services/metering-service/internal/eventconsumer/ -v -run Integration -tags integration`
- [ ] 集成测试用 `//go:build integration` build tag 隔离，不阻塞默认 `make test`
- [ ] Typecheck/lint 通过

### US-010: 新增 task 流示例 consumer + 集成测试（连真实 NATS）
**Description:** 作为开发者，我需要新增一个最简示例 task consumer，验证 task 流（`ANI_TASKS` / WorkQueuePolicy）的 adapter 端到端链路：adapter 发布 `model.import` task → `taskconsumer.Consumer` 通过真实 NATS 收到 → 打印业务日志。补齐 event 流已测但 task 流未测的缺口（真实 `lease_reconciler` 由他人负责且未完工，示例 consumer 仅验证链路连通性）。

**Acceptance Criteria:**
- [ ] 新增 `services/task-service/internal/taskconsumer/consumer.go`，订阅 `ani.tasks.model.import`，Queue=`task-workers`，AckWait=30s、MaxDeliver=10、MaxInflight=16
- [ ] `handle` 从 `msg.Headers()["tenant-id"]` 重建租户上下文，解析 payload，打印 `received task` 日志，成功 Ack
- [ ] payload 解析失败（毒丸）→ `msg.Ack(ctx)` 跳过 + error 日志
- [ ] 新增 `consumer_test.go`，mock `ports.MessageBus` 覆盖：Start 成功、解析失败 Ack、业务成功 Ack
- [ ] 新增 `services/task-service/internal/taskconsumer/integration_test.go`（`//go:build integration` build tag）
- [ ] 集成测试覆盖 task 端到端：adapter 发布 `model.import` → Consumer 通过真实 NATS 收到 → 打印 `received task` + `recovered tenant context` 日志
- [ ] 集成测试验证 WorkQueuePolicy 语义（消息被 Ack 后从 stream 移除，非 fan-out）
- [ ] 集成测试覆盖毒丸消息（非法 JSON → Ack 跳过 + error 日志）
- [ ] 集成测试覆盖 headers 5 key 匹配
- [ ] 测试后清理 stream（PurgeStream 按 subject 过滤）
- [ ] 集成测试通过 `go test ./services/task-service/internal/taskconsumer/ -v -run Integration -tags integration`
- [ ] Typecheck/lint 通过

---

## 4. Functional Requirements

- FR-1: The system must extend `ports.SubscribeOptions` with `AckWait time.Duration` and `MaxDeliver int` optional fields (zero value means not configured)
- FR-2: The system must extend `ports.Message` interface with `Headers() map[string][]string` method
- FR-3: The system must inject `*slog.Logger` into `MessageBus` and change `NewMessageBus` signature to `NewMessageBus(js natsgo.JetStreamContext, logger *slog.Logger)`
- FR-4: The system must write `EventEnvelope` metadata (`tenant-id`/`aggregate-id`/`aggregate-type`/`event-type`/`occurred-at`) into NATS Message Header in `Publish`
- FR-5: The system must return an explicit error when `PublishOptions.Subject` is empty
- FR-6: The system must not auto Ack/Nak in `Subscribe` based on handler return value; Ack/Nak is handler's responsibility
- FR-7: The system must log a warn message (not auto Ack/Nak) when handler returns error and did not call Ack/Nack
- FR-8: The system must recover from handler panic, call `msg.Nak()` as fallback, and log an error without crashing
- FR-9: The system must pass `AckWait` and `MaxDeliver` to `natsgo` sub options only when their values are greater than zero
- FR-10: The system must implement `message.Headers()` returning `map[string][]string` from NATS message header (nil when header is nil)
- FR-11: The system must define an internal `jetStream` interface in `pkg/adapters/nats/jetstream_iface.go` for unit test mocking, not exported to `pkg/ports/`
- FR-12: The system must change `ANI_EVENTS` stream retention from `WorkQueuePolicy` to `InterestPolicy` in `pkg/bootstrap/nats.go`
- FR-13: The system must keep `ANI_TASKS` stream retention as `WorkQueuePolicy` unchanged
- FR-14: The system must add a metering example consumer in `services/metering-service/internal/eventconsumer/consumer.go` subscribing to `ani.events.instance.>` with AckWait=30s, MaxDeliver=10, MaxInflight=16
- FR-15: The consumer must rebuild tenant context from `msg.Headers()["tenant-id"]`
- FR-16: The consumer must Ack poison messages (payload parse failure) and log an error
- FR-17: The consumer must Ack on business success in the example phase (link verification only)
- FR-18: The consumer must classify failures per plan §6.3: recoverable→Nack, poison→Ack+alert, duplicate→Ack idempotent skip
- FR-19: The system must add unit tests for the adapter covering Publish headers, Subscribe business-layer Ack/Nak, panic recover, and AckWait/MaxDeliver passthrough
- FR-20: The system must add unit tests for the consumer covering Start success, parse failure Ack, business success Ack
- FR-21: The system must add an integration test file `pkg/adapters/nats/integration_test.go` that connects to local docker-compose NATS and verifies end-to-end Publish+Subscribe with headers
- FR-22: The integration test must cover six scenarios per plan §9.2: end-to-end Publish+Subscribe headers, business-layer Ack/Nak, panic recover, Nak delayed redelivery, MaxDeliver stop, and InterestPolicy fan-out with two durable consumers
- FR-23: The integration test must ensure `ANI_EVENTS` is InterestPolicy and `ANI_TASKS` is WorkQueuePolicy before running, and clean up streams after
- FR-24: The system must add a Consumer end-to-end integration test `services/metering-service/internal/eventconsumer/integration_test.go` with `//go:build integration` build tag, verifying adapter Publish → Consumer receive → business log via real NATS
- FR-25: The Consumer integration test must cover poison message: invalid JSON payload → Consumer parse failure → Ack skip + `parse event failed` error log
- FR-26: The Consumer integration test must be isolated via `//go:build integration` build tag and not block default `make test`
- FR-27: The system must add a task example consumer in `services/task-service/internal/taskconsumer/consumer.go` subscribing to `ani.tasks.model.import` with Queue=`task-workers`, AckWait=30s, MaxDeliver=10, MaxInflight=16
- FR-28: The task consumer must rebuild tenant context from `msg.Headers()["tenant-id"]`, print `received task` log on success, and Ack
- FR-29: The task consumer must Ack poison messages (payload parse failure) and log an error
- FR-30: The system must add a task consumer integration test `services/task-service/internal/taskconsumer/integration_test.go` with `//go:build integration` build tag, verifying adapter Publish → Consumer receive → business log via real NATS, and WorkQueuePolicy semantics (message removed from stream after Ack)
- FR-31: The task consumer integration test must be isolated via `//go:build integration` build tag and not block default `make test`

---

## 5. Non-Goals (Out of Scope)

- 不做 7b 启动接线（metering-service main/bootstrap goroutine 注入），需合并目标分支后执行
- 不做崩溃恢复逻辑（PR-7：进程重启从 PG 重建 ticker）
- 不做 consumer 真实业务逻辑 `StartCollection`
- 不做审计 consumer
- 不做死信表/死信 stream（按需升级，见计划 §7、§10）
- 不做 `EnsureDeadLetterStream`
- 不做模型导入 task 的 `lease_reconciler` goroutine（承霖那边做）
- 不做 reconciler 写 outbox（嘉明负责）
- 不做 outbox publisher 接线（已实现，无需改）
- 不发明任何 API；OpenAPI 契约在本任务范围外（纯 adapter/port/internal 改造）

---

## 6. Design Considerations (Optional)

- NATS Header key 命名规范：小写连字符（HTTP header 风格）：`tenant-id`/`aggregate-id`/`aggregate-type`/`event-type`/`occurred-at`
- `EventEnvelope` 结构不变，`Publish` 内部把元数据写进 headers
- `SubscribeOptions.AckWait/MaxDeliver` 为可选字段，task 流可传 0（死信走 DB attempt_count），event 流传具体值（AckWait=30s, MaxDeliver=10）
- 死信方案：task 流走 DB `attempt_count`，event 流走 `MaxDeliver` 兜底 + 重启重建，详见计划 §7
- 崩溃恢复对齐：详见计划 §8（本任务不实现，仅保证 adapter `AckWait` 配置让 NATS ack timeout 重投可用）

---

## 7. Technical Considerations (Optional)

- `natsgo.JetStreamContext` 是具体类型不是接口，需在 adapter 内部定义 `jetStream` 接口包装以便单测 mock
- `NewMessageBus(js)` 改签名 `NewMessageBus(js, logger)` 需同步更新所有调用点
- outbox publisher 自动受益：现有 `outbox_publisher.go:91-101` 已传完整 `EventEnvelope`，adapter 改完后 publisher 发的事件也带 headers
- `ANI_EVENTS` 改 InterestPolicy 对现有订阅方无破坏性影响（现状 `message_bus.go` Subscribe 从未被调用）
- Ack/Nak 改业务层决定后，旧 handler 若只返回 error 不调 Ack/Nack，消息会因 AckWait 超时被 NATS 自动重投，不会丢失；需通知所有 handler 作者改调用方式
- logger 用 `log/slog`；`ports.Logger` 统一 port 可能不存在，示例 consumer 临时使用，后续真实消费逻辑接入后再调整
- 依赖关系：前置依赖无（纯改 adapter + port + stream 配置，独立可测）；与任务 A（配额表）可并行开发

---

## 8. Success Metrics

- `make test` 通过（含新增 adapter 单测和 consumer 单测）
- `make validate-architecture` 通过
- `git diff --check` 无空白错误
- REAL-K8S-LAB 真实环境门禁执行通过（4B 选项，真实底座组件验证）
- adapter 单测覆盖 Publish headers 5 个 key、Subscribe 业务层 Ack/Nak 6 种场景、panic recover 2 种场景
- 集成测试（本地 docker-compose NATS，手动验证项，不作为硬性门禁）：通过 `go test ./pkg/adapters/nats/ -v -run Integration`，覆盖 Publish+Subscribe 端到端 headers、Ack/Nak 业务层决定、panic recover、Nak 延迟重投、MaxDeliver 满后停投、Interest fan-out 6 个场景（计划 §9.2）
- Consumer 端到端集成测试（连真实 NATS，手动验证项，不作为硬性门禁）：通过 `go test ./services/metering-service/internal/eventconsumer/ -v -run Integration -tags integration`，覆盖 adapter 发布 `instance.created` → Consumer 通过真实 NATS 收到并打印业务日志、毒丸消息解析失败 Ack 跳过 2 个场景（US-009）
- task 流示例 consumer 端到端集成测试（连真实 NATS，手动验证项，不作为硬性门禁）：通过 `go test ./services/task-service/internal/taskconsumer/ -v -run Integration -tags integration`，覆盖 adapter 发布 `model.import` → Consumer 通过真实 NATS 收到并打印业务日志、WorkQueuePolicy 语义验证（Ack 后消息从 stream 移除）、毒丸消息解析失败 Ack 跳过 3 个场景（US-010）

---

## 9. Open Questions

- metering-service 的 main/bootstrap 入口位置需在 7b 阶段定位（本 PRD 不含 7b，记录待 7b 执行时确认）
- `ports.Logger` 统一 port 是否需要在本任务同步建设（当前先用 `log/slog`，后续真实消费逻辑接入后再评估）

---

## 10. ANI Boundaries

| Item | Value |
|------|-------|
| Product line | core |
| Code scope | `repo/pkg/ports/message_bus.go`、`repo/pkg/adapters/nats/`、`repo/pkg/bootstrap/nats.go`、`repo/services/metering-service/internal/eventconsumer/`、`repo/services/task-service/internal/taskconsumer/` |
| OpenAPI authority | consume only / N/A（本任务不涉及 OpenAPI 契约变更，纯 adapter/port/internal 改造） |
| Frozen exclusions | Core OpenAPI v1.yaml、Services API services/v1.yaml、outbox publisher、模型导入 task 流代码、lease_reconciler |
| idempotency_key | N/A（本任务为基础设施 adapter 改造，不新增有副作用的 POST/PUT/PATCH） |
| Module main doc | N/A（无 UI 模块主文档） |
