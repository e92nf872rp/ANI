# 扩展 ports.SubscribeOptions 和 ports.Message 契约

## Document Links
- PRD: repo/services/tasks/modules/prd/core/messaging/prd-nats-integration.md
- UX: N/A
- SPEC: repo/services/tasks/modules/spec/core/messaging/spec-nats-integration.md

## Description
扩展 ANI Core 的消息总线 port 契约，让 consumer 能按需配置 AckWait/MaxDeliver 并读取消息 headers。新增字段均为可选，零值兼容现有调用方，不破坏现有行为。

本 Issue 是 NATS adapter 健壮性改造的前置基础：后续 adapter 修复（Publish headers、Subscribe 业务层 Ack/Nak）和示例 consumer 都依赖这些 port 字段扩展。

## Scope
- Product line: core
- Code paths allowed: `repo/pkg/ports/message_bus.go`

## Acceptance Criteria
- [ ] `ports.SubscribeOptions` 新增 `AckWait time.Duration` 和 `MaxDeliver int` 字段，均为可选（传 0 表示不配置）
- [ ] `ports.Message` 接口新增 `Headers() map[string][]string` 方法
- [ ] 现有 `SubscribeOptions`/`Message` 调用方不传新字段时行为不变（零值兼容）
- [ ] Typecheck/lint 通过

## Dependencies
None

## Type
core

## Priority
high

## Labels
core

## Batch
TBD

## References
- SPEC: §3.2（Port 契约扩展）、§5.2（ports.SubscribeOptions/Message 扩展）
- PRD: US-001、FR-1、FR-2
