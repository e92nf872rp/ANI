# Issue 010: 集成测试（真实 NATS + 真实 DB + 真实 Prometheus）

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/metering/prd-metering-consumer.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/metering/spec-metering-consumer.md`

## Description

集成测试覆盖端到端链路，验证事件驱动采集、幂等、重建和 DeliverAll 回放的完整行为。使用真实 NATS JetStream + 真实 PG（含 migration）+ 真实 Prometheus。

## Scope
- Product line: core
- Code paths allowed: `repo/services/metering-service/internal/`（测试文件）

## Acceptance Criteria
- [ ] 集成测试启动真实 NATS JetStream + 真实 PG（含 migration）+ 真实 Prometheus
- [ ] 测试场景 1：发布 `instance.created`(running) 事件 → consumer 调 StartCollection → ticker 产出记录写入 `metering_usage_records`
- [ ] 测试场景 2：发布 `instance.stopped` 事件 → consumer 调 StopCollection → ticker 停止 → 短生命周期保底采集触发
- [ ] 测试场景 3：重复发布 `instance.created` 同一 instance → 进程内 map 幂等 no-op，DB 无重复行
- [ ] 测试场景 4：consumer 进程重启 → rebuilder 查 running 实例重建 ticker → DeliverAll 回放补齐崩溃窗口消息
- [ ] 测试场景 5：seenSeq 乱序过滤——先发 seq=5 再发 seq=3，seq=3 被丢弃
- [ ] 测试场景 6：seenSeq 失败重投——StartCollection 失败后 Nak 重投，seenSeq 未推进，重投后重新处理
- [ ] 测试场景 7：租户上下文不匹配 → Nak 重投
- [ ] 测试场景 8：毒消息（json 畸形）→ Ack 跳过
- [ ] 测试场景 9：DB UNIQUE 约束兜底——同实例同维度同周期重复 INSERT 时 `ON CONFLICT DO NOTHING`
- [ ] Typecheck/lint 通过

## Dependencies
- Issue #009（main.go 完整启动链路）

## Type
core

## Priority
medium

## Labels
core

## Batch
PR-M4

## SPEC Reference
- SPEC §9.2 Integration Tests
- PRD US-009
