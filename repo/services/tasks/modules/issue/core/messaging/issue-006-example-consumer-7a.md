# 新增示例 consumer（7a）业务层 Ack/Nak + 单测

## Document Links
- PRD: repo/services/tasks/modules/prd/core/messaging/prd-nats-integration.md
- UX: N/A
- SPEC: repo/services/tasks/modules/spec/core/messaging/spec-nats-integration.md

## Description
新增 metering 示例 consumer（7a），演示业务层 Ack/Nak 决策和 headers 租户上下文重建。本 Issue 仅含 consumer 代码 + 单测，不含 7b 启动接线（metering-service main/bootstrap goroutine 注入）和真实业务逻辑 StartCollection。

Consumer.Start 订阅 ani.events.instance.>，配置 AckWait=30s、MaxDeliver=10、MaxInflight=16、Consumer="metering-example"、Queue="metering"。handle 从 msg.Headers()["tenant-id"] 重建租户上下文。失败分类按计划 §6.3：可恢复故障→Nack，毒丸→Ack+告警，重复调用→Ack 幂等跳过。

注意：services/metering-service/ 在本分支可能不存在，需创建目录结构。

## Scope
- Product line: core
- Code paths allowed: `repo/services/metering-service/internal/eventconsumer/`

## Acceptance Criteria
- [ ] 新增 `services/metering-service/internal/eventconsumer/consumer.go`
- [ ] `Consumer.Start` 调 `bus.Subscribe` 订阅 `ani.events.instance.>`，配置 `AckWait=30s`、`MaxDeliver=10`、`MaxInflight=16`、`Consumer="metering-example"`、`Queue="metering"`
- [ ] `handle` 从 `msg.Headers()["tenant-id"]` 重建租户上下文
- [ ] payload 解析失败（毒丸）→ `msg.Ack(ctx)` 跳过 + error 日志
- [ ] 业务成功 → `msg.Ack(ctx)`（示例阶段：链路验证通过即 Ack）
- [ ] 失败分类与计划文档 §6.3 一致：可恢复故障→Nack，毒丸→Ack+告警，重复调用→Ack 幂等跳过
- [ ] `Consumer.Stop` 调 `sub.Drain(ctx)`
- [ ] 新增 `consumer_test.go`，mock `ports.MessageBus` 覆盖：Start 成功、解析失败 Ack、业务成功 Ack
- [ ] Typecheck/lint 通过

## Dependencies
#1 — 依赖 port 契约扩展（SubscribeOptions.AckWait/MaxDeliver、Message.Headers()）

## Type
core

## Priority
medium

## Labels
core

## Batch
TBD

## References
- SPEC: §2.4（文件结构）、§5.1（consumer 业务层 Ack/Nak 决策）
- PRD: US-006、FR-14、FR-15、FR-16、FR-17、FR-18、FR-20
- Plan: §6.3（失败分类）、§6.1（7a 范围）
