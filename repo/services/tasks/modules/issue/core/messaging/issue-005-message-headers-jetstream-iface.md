# 新增 message.Headers() 实现 + 内部 jetStream 接口包装

## Document Links
- PRD: repo/services/tasks/modules/prd/core/messaging/prd-nats-integration.md
- UX: N/A
- SPEC: repo/services/tasks/modules/spec/core/messaging/spec-nats-integration.md

## Description
实现 message 结构体的 Headers() 方法，并定义内部 jetStream 接口以便单测 mock。natsgo.JetStreamContext 是具体类型不是接口，需在 adapter 内部定义 jetStream 接口包装（仅 adapter 内部使用，不暴露到 pkg/ports/），natsgo.JetStreamContext 天然满足该接口，生产路径零开销。

## Scope
- Product line: core
- Code paths allowed: `repo/pkg/adapters/nats/message_bus.go`、`repo/pkg/adapters/nats/jetstream_iface.go`

## Acceptance Criteria
- [ ] `message.Headers()` 返回 `map[string][]string(m.msg.Header)`，`Header == nil` 时返回 nil
- [ ] 新增 `pkg/adapters/nats/jetstream_iface.go`，定义内部 `jetStream` 接口（PublishMsg/Subscribe/QueueSubscribe/StreamInfo/AddStream）
- [ ] `jetStream` 接口不暴露到 `pkg/ports/`，仅 adapter 内部使用
- [ ] `natsgo.JetStreamContext` 天然满足 `jetStream` 接口，生产路径零开销
- [ ] Typecheck/lint 通过

## Dependencies
#1 — 依赖 port 契约扩展（Message.Headers()）

## Type
core

## Priority
high

## Labels
core

## Batch
TBD

## References
- SPEC: §3.2（message.Headers() 实现）、§5.1（jetStream 内部接口）
- PRD: US-004、FR-10、FR-11
