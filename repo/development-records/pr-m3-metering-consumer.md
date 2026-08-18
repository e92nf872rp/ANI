# PR-M3 — Metering Consumer 主进程（consumer + rebuilder + buildSpec + main.go）

完成日期：2026-08-13（进行中，逐 Issue 追加）
对应 Sprint：以 repo/CURRENT-SPRINT.md 为准
批次类型：Feature batch（新增计量采集产品能力）

> **说明：** 本文件按 Issue 逐条追加实现笔记。批次全部完成后再一次性更新 README.md、CURRENT-SPRINT.md、ANI-06-开发计划.md。

---

## Issue 007: 实现 consumer（seenSeq 成功才推进 + 乱序过滤）

完成日期：2026-08-13
验证结果：`go build ./internal/...` 通过，`go vet ./internal/...` 通过，`go test ./internal/ -run TestHandleEvent -v -count=1` 11/11 PASS，`make validate-architecture` 通过，`git diff --check` 通过

### 实现了什么

新增 `Consumer` 结构和 `handleEvent` 方法，订阅 `InstanceLifecycleEvent` 事件，根据实例状态驱动 `MeteringCollectionService` 的 Start/Stop。核心是 seenSeq 乱序过滤机制：处理成功后才推进高水位，避免 Nak 重投时被误判为过期事件而永久丢失（V1 缺陷修复）。

### 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/metering-service/internal/consumer.go` | 新增 | Consumer 结构 + NewConsumer + handleEvent + safeLog |
| `services/metering-service/internal/consumer_test.go` | 新增 | 11 个单测覆盖全部 AC 场景 |

### Design Decisions

1. **safeLog 封装 logger nil 安全**
   - 模糊性：SPEC 未指定 logger 为 nil 时的行为，AC 要求 `logger *slog.Logger` 字段但单测不应强制注入。
   - 选择：封装 `safeLog(fn func(*slog.Logger))` 方法，logger 为 nil 时跳过日志输出。
   - 理由：单测只需验证业务逻辑（返回值、seenSeq 推进、mock 调用次数），无需注入 logger。生产环境 main.go 会注入真实 logger。避免每个调用点写 `if c.logger != nil` 重复代码。

2. **seenSeq 读取和推进分两次加锁，不合并为一个临界区**
   - 模糊性：SPEC §5.1.4 step 4 读 seenSeq、step 8 推进 seenSeq，中间隔着路由处理（可能耗时）。
   - 选择：step 4 加锁读取后立即解锁，step 8 处理成功后再加锁推进。
   - 理由：如果合并为一个临界区（读→处理→推进全程持锁），StartCollection/StopCollection 的网络 I/O 会阻塞所有其他实例的事件处理，违反 MaxInflight=1 串行消费的性能预期。分两次加锁使得处理期间不阻塞 seenSeq map 访问，依赖 MaxInflight=1 保证不会有同实例并发事件竞争。

3. **未知状态不推进 seenSeq**
   - 模糊性：SPEC §6.1 Error Taxonomy 表中未知状态的 seenSeq 推进列写"否"，但 §5.1.4 step 6 的 default 分支只说"return nil"未明确 seenSeq。
   - 选择：未知状态 return nil 且不推进 seenSeq。
   - 理由：未知状态意味着事件格式可能上游有变更，不推进 seenSeq 使得如果上游修正后重发同序号事件仍能被处理。测试 `TestHandleEventUnknownStatusAckSkip` 验证此行为。

### Deviations

None — 实现严格遵循 SPEC §5.1.4 的 7 步处理流程和 §6.1 Error Taxonomy 的 6 场景返回值/日志级别/seenSeq 推进行为。review-it 逐项核对确认完全一致。

### Tradeoffs

1. **seenSeq 读取分两次加锁 vs 合并为一个临界区**
   - 考虑过的替代方案：合并为单次加锁（读 seenSeq → 处理 → 推进 seenSeq 全程持锁）。
   - 优点：绝对防止并发竞争。
   - 缺点：处理期间持锁阻塞所有实例的事件处理，StartCollection 创建 ticker 的开销会放大延迟。
   - 选择理由：MaxInflight=1 保证同一时刻只有一个事件在处理，不存在同实例并发竞争。分两次加锁使处理期间不阻塞 map 访问，性能更优。推进时的 `if event.EventSeq > c.seenSeq[event.InstanceID]` 二次比较防止回退。

2. **毒消息 Ack 跳过 vs Nak 重投**
   - 考虑过的替代方案：毒消息也返回 error 触发 Nak 重投。
   - 优点：不丢消息。
   - 缺点：json 畴形消息永远无法解析成功，重投会耗尽 MaxDeliver=5 后进入 dead letter queue，浪费资源。
   - 选择理由：SPEC §6.1 明确毒消息 Ack 跳过。毒消息是上游生产者 bug，不应靠重试解决，Ack 跳过 + Error 日志即可追踪。

### Open Questions

None

### 验证命令

```bash
cd repo/services/metering-service
go build ./internal/...                                    # 通过
go vet ./internal/...                                      # 通过
go test ./internal/ -run TestHandleEvent -v -count=1       # 11/11 PASS

cd repo
make validate-architecture                                  # 通过
git diff --check                                            # 通过
```

> **注：** `make test` 中 `pkg/adapters/runtime` sandbox symlink 测试在 Windows 不兼容（既有失败，与本次改动无关）。

---

## Issue 008: 实现 rebuilder（直接查 DB + WithPlatformTx 绕 RLS）

完成日期：2026-08-13
验证结果：`go build ./internal/` 通过，`go vet ./internal/` 通过，`go test ./internal/ -v -count=1` 31/31 PASS（含 8 个 rebuilder 测试），`python scripts/validate_component_imports.py --root .` 通过，`git diff --check` 通过

### 实现了什么

新增 `Rebuilder` 结构和 `Rebuild` 方法，启动时跨租户查所有 running 实例并重建 ticker。用 `WithPlatformTx` 绕 RLS 查 `workload_instances WHERE state='running' ORDER BY updated_at ASC`，解析 `gpu_status` JSONB 获取 GPU 卡数，对每个实例调 `buildSpec` + `StartCollection`。单实例失败不阻塞。

### 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/metering-service/internal/rebuilder.go` | 新增 | Rebuilder 结构 + NewRebuilder + Rebuild + safeLog |
| `services/metering-service/internal/rebuilder_test.go` | 新增 | 8 个单测覆盖全部 AC 场景 |

### Design Decisions

1. **safeLog 复用 consumer.go 中的 nil 安全日志模式**
   - 模糊性：SPEC 未指定 logger 为 nil 时的行为，AC 要求 `logger *slog.Logger` 字段但单测不应强制注入。
   - 选择：封装 `safeLog(fn func(*slog.Logger))` 方法（与 consumer.go 同模式），logger 为 nil 时跳过日志输出。
   - 理由：单测只需验证业务逻辑（WithPlatformTx 调用、Query SQL、StartCollection 调用次数、错误传播），无需注入 logger。生产环境 main.go 会注入真实 logger。避免每个调用点写 `if r.logger != nil` 重复代码。

2. **rebuilder_test.go 独立定义 mock，不复用 consumer_test.go 的 mock**
   - 模糊性：consumer_test.go 已定义 `mockMetadataStore` 和 `mockMeteringCollectionService`，但字段结构不同（consumer mock 侧重 handleEvent 的事件路由，rebuilder mock 侧重 WithPlatformTx 回调和 Query 仿真）。
   - 选择：在 rebuilder_test.go 中独立定义 `mockMetadataStore` 和 `mockMeteringCollectionService`，字段和方法集精确匹配 rebuilder 的需求。
   - 理由：两个测试文件的 mock 侧重点不同——consumer mock 需要 `SetPlatformTxHandler` 语义，rebuilder mock 需要 `WithPlatformTxFunc` 直接回调。合并会引入不必要的字段和复杂度。Go 同包内不同 mock 类型名相同会编译冲突，独立文件定义各自的 mock 是 Go 测试惯例。

### Deviations

1. **count++ 无条件递增（与 Plan §6.2 和 SPEC §5.1.5 伪代码一致）**
   - SPEC/Plan 规定：Plan §6.2 第 570-574 行和 SPEC §5.1.5 伪代码中 `count++` 在 `if err != nil` 块之后无条件执行（无 `continue`），`count` 语义为"遍历的 running 实例总数"。
   - 初始实现偏差：首版实现错误地在 `if err != nil` 块内加了 `continue`，导致 `count++` 只有成功时才执行，`count` 退化为"成功重建的实例数"。
   - 修复：review-it 阶段用户指出后，移除 `continue`，`count++` 无条件执行，与 Plan/SPEC 一致。
   - 理由：`running_instances` 日志字段语义应为"遍历的 running 实例总数"，即使部分实例 StartCollection 失败也应计入总数。这样运维人员能从日志直接判断 DB 中 running 实例数量与成功重建数量的差异。

### Tradeoffs

1. **WithPlatformTx 内直接遍历 rows 逐个 StartCollection vs 先收集到 slice 再逐个处理**
   - 考虑过的替代方案：在 WithPlatformTx 回调内先 `rows.Scan` 收集到 `[]instanceInfo` slice，回调返回后在事务外逐个调 `StartCollection`。
   - 优点：StartCollection 在事务外执行，不占用数据库事务时间。
   - 缺点：多一次内存拷贝（slice 分配），且 SPEC §5.1.5 伪代码明确在 `WithPlatformTx` 回调内直接遍历 `rows` 并调 `StartCollection`。
   - 选择理由：遵循 SPEC 伪代码结构，减少中间数据结构。StartCollection 是内存操作（创建 ticker），不涉及数据库 I/O，在事务内调用不会延长事务持有时间。

### Open Questions

None

### 验证命令

```bash
cd repo/services/metering-service
go build ./internal/                                       # 通过
go vet ./internal/                                         # 通过
go test ./internal/ -v -count=1                            # 31/31 PASS（含 8 个 rebuilder 测试）

cd repo
python scripts/validate_component_imports.py --root .     # 通过
git diff --check                                           # 通过
```

### 测试覆盖明细

| 测试用例 | AC 覆盖 |
|---|---|
| `TestRebuildCallsWithPlatformTx` | WithPlatformTx 调用验证 |
| `TestRebuildStartsCollectionForRunningInstances` | running 实例建 ticker |
| `TestRebuildParsesGPUStatus` | gpu_status JSONB 解析（5 种边界：正常/缺失/null/空 JSON/损坏） |
| `TestRebuildSingleInstanceFailureDoesNotBlock` | 单实例失败不阻塞 |
| `TestRebuildWithPlatformTxError` | WithPlatformTx 错误传播 |
| `TestRebuildQueryError` | Query 错误传播 |
| `TestRebuildNoRunningInstances` | 无 running 实例（空结果） |
| `TestRebuildRowsErrPropagation` | rows.Err() 传播 |

---

## Issue 009: 新增 metering-service main.go（bootstrap 启动 + 先重建后订阅）

完成日期：2026-08-13
验证结果：`go build ./...` 通过，`go vet ./...` 通过，`go test ./...` 通过，`make validate-architecture` 通过，`git diff --check` 通过

### 实现了什么

新增 metering-service 进程入口 `main.go`，用 `bootstrap.MustConnect(cfg.Config)` 启动，按"先重建后订阅"协议执行：1) `rebuilder.Rebuild(ctx)` 重建 running 实例 ticker → 2) `Subscribe` NATS（DeliverAll + MaxInflight=1）→ 3) `<-ctx.Done()` 常驻等待。重建失败不阻塞，Subscribe 失败 `os.Exit(1)`，退出时 `defer sub.Drain`。同时在 `consumer.go` 中新增 `HandleEvent()` 导出方法适配 `ports.MessageHandler`。

### 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/metering-service/main.go` | 新增 | 进程入口，bootstrap 启动 + 先重建后订阅 |
| `services/metering-service/internal/consumer.go` | 修改 | 新增 `HandleEvent()` 导出方法，适配 `ports.MessageHandler` |

### Design Decisions

1. **`HandleEvent()` 导出适配方法**
   - 模糊性：SPEC §2.3 启动流程中 `Subscribe` 需要传入 `MessageHandler`（即 `func(ctx, Message) error`），但 `Consumer` 的处理方法是未导出的 `handleEvent`。`main.go` 在 `package main` 中无法直接引用 `internal` 包的未导出方法。
   - 选择：在 `consumer.go` 中新增 `HandleEvent()` 导出方法，返回 `c.handleEvent`（类型为 `ports.MessageHandler`）。
   - 理由：保持 `handleEvent` 的核心逻辑封装在 `internal` 包内不对外暴露，仅通过 `HandleEvent()` 暴露一个适配函数给 `main.go` 使用。遵循 Go 最小导出原则——`main.go` 只需要一个回调函数，不需要知道内部处理细节。

2. **`metering.CollectAll` 在 main.go 注入**
   - 模糊性：SPEC §2.3 启动流程写的是 `NewMeteringCollectionService(deps.DB, deps.Logger)`，但实际构造函数还需要 `CollectAllFunc` 参数（PR-M2 产物）。
   - 选择：在 main.go 中直接导入 `pkg/adapters/metering` 并传入 `metering.CollectAll`。
   - 理由：`CollectAll` 是 `pkg/adapters/metering` 中的包级函数，组装在 main.go 是标准的依赖注入位置。这使 `meteringCollectionService` 可测试（单测可注入 mock CollectAllFunc），同时 main.go 作为组合根（composition root）负责连接端口与适配器。

### Deviations

None — 实现完全遵循 SPEC §2.3 启动流程和 §4.3 NATS Subscribe 配置。SPEC 描述的启动顺序为：`config.Load()` → `bootstrap.MustConnect(cfg.Config)` → 构造三个组件 → `rebuilder.Rebuild(ctx)`（失败不阻塞）→ `Subscribe`（失败 `os.Exit(1)`）→ `<-ctx.Done()`（`defer sub.Drain`）。实现按此顺序逐行对应，无偏离。

### Tradeoffs

1. **`DeliverAllPolicy` 通过 durable consumer 默认行为实现**
   - 考虑过的替代方案：在 `SubscribeOptions` 中新增 `DeliverPolicy` 字段以显式设置 DeliverAll。
   - 优点：显式表达投递策略。
   - 缺点：需修改 `pkg/ports` 公共接口，影响面大，且 NATS durable consumer 不显式设置 `DeliverLatest`/`DeliverNone` 时默认即为 DeliverAll。
   - 选择理由：`SubscribeOptions` 的设计意图是只暴露 ANI 产品语义参数。DeliverAll 是 durable consumer 的默认行为，无需额外字段。遵循奥卡姆剃刀——不为假设的未来需求提前引入配置项。

2. **信号处理用 `signal.NotifyContext` 而非手动 channel**
   - 考虑过的替代方案：手动创建 `chan os.Signal` + `signal.Notify`。
   - 优点：更传统，兼容旧版 Go。
   - 缺点：更冗余，需手动管理信号 channel 和停止监听。
   - 选择理由：`signal.NotifyContext` 是 Go 1.16+ 标准方式，一行完成，ctx 取消后自动停止信号监听（通过 `defer stop()`），且与 `rebuilder.Rebuild(ctx)` 的 ctx 传递天然契合。

### Open Questions

None

### 验证命令

```bash
cd repo/services/metering-service
go build ./...                                               # 通过
go vet ./...                                                 # 通过
go test ./...                                                # 通过

cd repo
make validate-architecture                                   # 通过
git diff --check                                             # 通过
```

### AC 对照

| AC | 证据 |
|---|---|
| 新增 `services/metering-service/main.go` | 文件存在，70 行 |
| `bootstrap.MustConnect(cfg.Config)` 启动 | main.go 第 20 行 |
| 构造 `meteringCollectionService` | 第 26 行（注入 `metering.CollectAll`） |
| 构造 consumer | 第 29 行 |
| 构造 rebuilder | 第 30 行 |
| 启动顺序：Rebuild → Subscribe → `<-ctx.Done()` | 第 38/45/68 行 |
| 重建失败不阻塞 | 第 38-41 行 |
| Subscribe 失败 `os.Exit(1)` | 第 53-56 行 |
| `defer sub.Drain(context.Background())` | 第 57 行 |
| Subscribe 配置（subject/consumer/MaxInflight/AckWait/MaxDeliver） | 第 45-51 行 |
| DeliverAllPolicy | durable consumer 默认行为 |
| 不设 Queue Group | Queue 字段零值 |
| Typecheck/lint 通过 | go build/vet/test + make validate-architecture 全 pass |

---
