# 修复 adapter.Publish 写入 NATS headers + 注入 logger

## Document Links
- PRD: repo/services/tasks/modules/prd/core/messaging/prd-nats-integration.md
- UX: N/A
- SPEC: repo/services/tasks/modules/spec/core/messaging/spec-nats-integration.md

## Description
修复 adapter.Publish 把 EventEnvelope 元数据写进 NATS Message Header，并注入 logger 以记录错误路径。MessageBus 结构体新增 logger 字段，NewMessageBus 签名变更需同步更新所有调用点（repo/pkg/bootstrap/deps.go:213 等）。

Publish 写入的 header key 采用小写连字符（HTTP header 风格）：tenant-id/aggregate-id/aggregate-type/event-type/occurred-at。outbox publisher 已传完整 EventEnvelope，adapter 改完后自动受益。

## Scope
- Product line: core
- Code paths allowed: `repo/pkg/adapters/nats/message_bus.go`、`repo/pkg/bootstrap/deps.go`（调用点更新）

## Acceptance Criteria
- [ ] `MessageBus` 结构体新增 `logger *slog.Logger` 字段
- [ ] `NewMessageBus` 签名改为 `NewMessageBus(js natsgo.JetStreamContext, logger *slog.Logger)`，同步更新所有调用点（`pkg/bootstrap/deps.go:213` 等）
- [ ] `Publish` 把 `event.TenantID/AggregateID/AggregateType/EventType/OccurredAt` 写入 NATS Header，key 用小写连字符：`tenant-id`/`aggregate-id`/`aggregate-type`/`event-type`/`occurred-at`
- [ ] `Publish` 在 `opts.Subject == ""` 时返回明确 error
- [ ] Typecheck/lint 通过

## Dependencies
#1 — 依赖 port 契约扩展（SubscribeOptions/Message）

## Type
core

## Priority
high

## Labels
core

## Batch
TBD

## References
- SPEC: §2.4（文件结构）、§5.1（Publish headers 算法）、§4.3
- PRD: US-002、FR-3、FR-4、FR-5
