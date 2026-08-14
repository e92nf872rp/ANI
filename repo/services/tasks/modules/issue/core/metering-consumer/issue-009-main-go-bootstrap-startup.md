# Issue 009: 新增 metering-service main.go（bootstrap 启动 + 先重建后订阅）

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/metering/prd-metering-consumer.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/metering/spec-metering-consumer.md`

## Description

新建 metering-service 进程入口，用 `bootstrap.MustConnect` 启动，按"先重建后订阅"协议执行。重建消除竞态窗口，DeliverAll 回放补齐崩溃窗口。

## Scope
- Product line: core
- Code paths allowed: `repo/services/metering-service/`

## Acceptance Criteria
- [ ] 新增 `services/metering-service/main.go`
- [ ] main.go 用 `bootstrap.MustConnect(cfg.Config)` 启动，获得 `*Deps`（DB/Ports.MessageBus/Ports.Metadata/Logger）
- [ ] 构造 `meteringCollectionService`（传入 `deps.DB, deps.Logger`）
- [ ] 构造 consumer（传入 `meteringSvc, deps.Ports.MessageBus, deps.Logger`）
- [ ] 构造 rebuilder（传入 `deps.Ports.Metadata, meteringSvc, deps.Logger`）
- [ ] 启动顺序：1) `rebuilder.Rebuild(ctx)` 重建 ticker → 2) `Subscribe` NATS → 3) `<-ctx.Done()` 常驻等待
- [ ] 重建失败不阻塞：日志告警后继续订阅（靠事件增量 + DeliverAll 兜底）
- [ ] Subscribe 失败时 `os.Exit(1)`
- [ ] 退出时 `defer sub.Drain(context.Background())`（Subscription 接口只有 Drain，无 Unsubscribe）
- [ ] Subscribe 配置：subject `ani.events.instance.>`、Consumer name `metering-consumer`、MaxInflight=1、DeliverAllPolicy、AckWait=30s、MaxDeliver=5
- [ ] 不设 Queue Group（单副本只有一个订阅，Queue 竞争语义无处发挥）
- [ ] Typecheck/lint 通过

## Dependencies
- Issue #003（go.mod + config）
- Issue #004（meteringCollectionService）
- Issue #007（consumer）
- Issue #008（rebuilder）

## Type
core

## Priority
high

## Labels
core

## Batch
PR-M3

## SPEC Reference
- SPEC §2.3 Module Interactions（启动流程）
- SPEC §4.3 NATS Subscribe 配置
- PRD FR-24 ~ FR-29
