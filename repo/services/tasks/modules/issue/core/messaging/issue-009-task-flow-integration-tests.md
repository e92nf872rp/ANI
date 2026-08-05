# 新增 task 流示例 consumer + 集成测试（连真实 NATS）

## Document Links
- PRD: repo/services/tasks/modules/prd/core/messaging/prd-nats-integration.md
- UX: N/A
- SPEC: repo/services/tasks/modules/spec/core/messaging/spec-nats-integration.md

## Description
task 流（`ANI_TASKS` stream，WorkQueuePolicy）当前只有发送侧（`outbox_publisher.go`），没有接收侧消费方代码。真实 `lease_reconciler` 由承霖负责，尚未实现。

本 issue 补一个最简单的示例 task consumer + 集成测试，验证 task 流端到端链路：adapter 发布一条 `model.import` task 消息 → 示例 consumer 通过真实 NATS 收到 → 打印日志 → Ack。集成测试用 `//go:build integration` build tag 隔离，不阻塞默认 `make test`，通过 `go test ./services/task-service/internal/taskconsumer/ -v -run Integration -tags=integration` 手动运行。

示例 consumer 仅验证链路连通性，不包含真实 task 处理逻辑（lease 抢占、进度上报等由真实 `lease_reconciler` 实现）。

## Scope
- Product line: core
- Code paths allowed:
  - `repo/services/task-service/internal/taskconsumer/consumer.go`（新增示例 consumer）
  - `repo/services/task-service/internal/taskconsumer/consumer_test.go`（新增单测）
  - `repo/services/task-service/internal/taskconsumer/integration_test.go`（新增集成测试）

## Acceptance Criteria
- [ ] 新增 `services/task-service/internal/taskconsumer/consumer.go`：最简示例 consumer
  - 订阅 `ani.tasks.model.import`（WorkQueuePolicy，at-least-once）
  - consumer 名 `task-example`，queue 组 `task-workers`
  - AckWait=30s、MaxDeliver=10、MaxInflight=16
  - handle：从 headers 重建租户上下文（读 `tenant-id`），解析 payload，打印日志，Ack
  - payload 解析失败（毒丸）→ Ack 跳过 + error 日志
  - 业务成功 → 打印 `received task` 日志 + Ack
- [ ] 新增 `services/task-service/internal/taskconsumer/consumer_test.go`：mock MessageBus 单测
  - 覆盖 Start 成功（验证 Subscribe 参数）
  - 覆盖解析失败 Ack
  - 覆盖业务成功 Ack
- [ ] 新增 `services/task-service/internal/taskconsumer/integration_test.go`（`//go:build integration` build tag）
  - 前置：确保 `ANI_TASKS` stream 存在且为 WorkQueuePolicy
  - 覆盖 task 端到端：发布一条 `model.import` task → consumer 收到 → 打印 `received task` 日志
  - 验证 headers（tenant-id 匹配）
  - 验证 WorkQueuePolicy 语义：消息被消费后从 queue 移除（不再投递给第二个 consumer）
  - 覆盖毒丸消息：非法 JSON → Ack 跳过 + error 日志
  - 测试后清理（PurgeStream / 删 consumer）
  - 测试末尾打印 consumer 原始日志（`t.Log(logBuf.String())`）
- [ ] 集成测试通过 `go test ./services/task-service/internal/taskconsumer/ -v -run Integration -tags=integration`
- [ ] Typecheck/lint 通过

## Dependencies
#2, #3, #4 — 依赖 adapter 修复（Publish headers、Subscribe Ack/Nak）和 stream 配置（ANI_TASKS WorkQueuePolicy）

## Type
core

## Priority
medium

## Labels
core

## Batch
TBD

## References
- SPEC: §9.2（集成测试策略）、§9.4（验收标准映射）
- PRD: US-008（集成测试场景）、§5 Non-Goals（lease_reconciler 不在本任务范围）
- Plan: §9.2（集成测试场景定义）
- pkg/nats/messages.go: SubjectModelImport = "ani.tasks.model.import"
