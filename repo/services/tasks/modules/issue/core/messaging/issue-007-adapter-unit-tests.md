# 新增 adapter 单元测试（fake/mock JetStream）

## Document Links
- PRD: repo/services/tasks/modules/prd/core/messaging/prd-nats-integration.md
- UX: N/A
- SPEC: repo/services/tasks/modules/spec/core/messaging/spec-nats-integration.md

## Description
用内部 jetStream 接口 mock 覆盖 adapter 的 Publish/Subscribe 健壮性路径。测试不依赖真实 NATS，使用 fake/mock JetStream 验证 adapter 行为：Publish headers 正确性、Subscribe 业务层 Ack/Nak 决策、panic recover 兜底。

## Scope
- Product line: core
- Code paths allowed: `repo/pkg/adapters/nats/message_bus_test.go`

## Acceptance Criteria
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

## Dependencies
#3, #4, #5 — 依赖 adapter 修复（Publish headers、Subscribe Ack/Nak、message.Headers + jetStream 接口）

## Type
core

## Priority
medium

## Labels
core

## Batch
TBD

## References
- SPEC: §9.1（单元测试策略）、§9.4（验收标准映射）
- PRD: US-007、FR-19
