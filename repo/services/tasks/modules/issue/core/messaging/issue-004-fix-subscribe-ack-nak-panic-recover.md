# 修复 adapter.Subscribe 改业务层决定 Ack/Nak + panic recover

## Document Links
- PRD: repo/services/tasks/modules/prd/core/messaging/prd-nats-integration.md
- UX: N/A
- SPEC: repo/services/tasks/modules/spec/core/messaging/spec-nats-integration.md

## Description
修复 adapter.Subscribe 不再自动根据 handler 返回值调 Ack/Nak，交业务层决定。handler 返回 error 时 adapter 仅记录 warn 日志（未调 Ack/Nak 则由 NATS AckWait 超时自动重投，不丢失）；handler panic 时 recover 兜底调 msg.Nak() + 记录 error 日志，不崩溃。

AckWait/MaxDeliver 在 SubscribeOptions 中为可选字段，值 > 0 时透传 natsgo 子选项，== 0 时不配置。

## Scope
- Product line: core
- Code paths allowed: `repo/pkg/adapters/nats/message_bus.go`

## Acceptance Criteria
- [ ] adapter 不再根据 handler 返回值自动调 `msg.Ack/Nak`
- [ ] handler 返回 error 时 adapter 仅记录 warn 日志，不调 Ack/Nak（未调则由 NATS AckWait 超时自动重投）
- [ ] handler panic 时 recover 兜底调 `msg.Nak()` + 记录 error 日志，不崩溃
- [ ] panic 发生在业务层 Ack 之后时，兜底 Nak 被 NATS 忽略，不产生重复处理
- [ ] `SubscribeOptions.AckWait > 0` 时透传 `natsgo.AckWait`，`MaxDeliver > 0` 时透传 `natsgo.MaxDeliver`
- [ ] Typecheck/lint 通过

## Dependencies
#1 — 依赖 port 契约扩展（SubscribeOptions.AckWait/MaxDeliver）

## Type
core

## Priority
high

## Labels
core

## Batch
TBD

## References
- SPEC: §2.4（文件结构）、§5.1（Subscribe 算法：业务层 Ack/Nak + panic recover）
- PRD: US-003、FR-6、FR-7、FR-8、FR-9
