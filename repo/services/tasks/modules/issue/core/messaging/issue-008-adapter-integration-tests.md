# 新增 adapter 集成测试（连本地 docker-compose NATS）

## Document Links
- PRD: repo/services/tasks/modules/prd/core/messaging/prd-nats-integration.md
- UX: N/A
- SPEC: repo/services/tasks/modules/spec/core/messaging/spec-nats-integration.md

## Description
用真实 NATS（docker-compose）验证 adapter 端到端链路，包括 InterestPolicy fan-out、AckWait/MaxDeliver 配置生效、panic recover 不崩溃、Nak 延迟重投等集成场景。集成测试用 `//go:build integration` build tag 隔离，不阻塞默认 `make test`，通过 `go test ./pkg/adapters/nats/ -v -run Integration -tags=integration` 手动运行。

集成测试为手动验证项，不作为硬性门禁（PRD §8 Success Metrics：3B 选项）。

另外补充 metering-service 示例 consumer 端到端集成测试：发布 `instance.created` 事件 → `eventconsumer.Consumer` 通过真实 NATS 收到 → 打印业务日志，验证 adapter + consumer 完整链路（US-006 与 US-008 联动的真实环境验证）。

## Scope
- Product line: core
- Code paths allowed:
  - `repo/pkg/adapters/nats/integration_test.go`
  - `repo/services/metering-service/internal/eventconsumer/integration_test.go`

## Acceptance Criteria
- [ ] 新增 `pkg/adapters/nats/integration_test.go`（`//go:build integration` build tag）
- [ ] 前置：测试前确保 `ANI_EVENTS` stream 存在且为 InterestPolicy，`ANI_TASKS` 为 WorkQueuePolicy
- [ ] 覆盖 Publish + Subscribe 端到端：发布一条 `instance.created` 事件，consumer 收到后验证 headers（tenant-id/aggregate-id/aggregate-type/event-type/occurred-at 均匹配）
- [ ] 覆盖 Ack/Nak 业务层决定：handler 自己调 Ack/Nak，adapter 不干预（验证消息状态符合业务层调用）
- [ ] 覆盖 panic recover：handler panic → 消息被 Nak → 进程不崩溃
- [ ] 覆盖 Nak 延迟重投：handler 调 Nak → 消息延迟重投 → 第二次 handler 调 Ack
- [ ] 覆盖 MaxDeliver 满后停投：handler 持续 Nak → 到顶后 NATS 不再投递
- [ ] 覆盖 Interest fan-out：两个 durable consumer 各自收到同一事件（验证 InterestPolicy 生效，非 WorkQueue 删除语义）
- [ ] 测试后清理 stream（避免污染后续测试）
- [ ] 集成测试通过 `go test ./pkg/adapters/nats/ -v -run Integration -tags=integration`
- [ ] 新增 `services/metering-service/internal/eventconsumer/integration_test.go`（`//go:build integration` build tag）
- [ ] 覆盖 Consumer 端到端：adapter 发布一条 `instance.created` 事件到 `ani.events.instance.>`，`eventconsumer.Consumer` 通过真实 NATS 收到并打印业务日志（`received instance event`），验证 adapter + consumer 完整链路
- [ ] consumer 集成测试通过 `go test ./services/metering-service/internal/eventconsumer/ -v -run Integration -tags=integration`
- [ ] Typecheck/lint 通过

## Dependencies
#2, #3, #4 — 依赖 adapter 修复（Publish headers、Subscribe Ack/Nak）和 stream 配置修复（InterestPolicy）

## Type
core

## Priority
medium

## Labels
core

## Batch
TBD

## References
- SPEC: §9.2（集成测试策略：6 场景）、§9.4（验收标准映射）
- PRD: US-008、FR-21、FR-22、FR-23
- Plan: §9.2（集成测试 6 场景定义）
