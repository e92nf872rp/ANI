# Issue 007: 实现 consumer（seenSeq 成功才推进 + 乱序过滤）

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/metering/prd-metering-consumer.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/metering/spec-metering-consumer.md`

## Description

实现 consumer 的 handleEvent，处理 InstanceLifecycleEvent。seenSeq 处理成功后才推进，避免 Nak 重投误判过期永久丢失。MaxInflight=1 串行消费保证顺序。

## Scope
- Product line: core
- Code paths allowed: `repo/services/metering-service/internal/`

## Acceptance Criteria
- [ ] 新增 `services/metering-service/internal/consumer.go`
- [ ] `Consumer` 结构持有 `metering ports.MeteringCollectionService`、`logger *slog.Logger`、`mu sync.Mutex`、`seenSeq map[string]uint64`
- [ ] `handleEvent` 从 `msg.Headers()["tenant-id"]` 读租户 ID，与 payload `tenant_id` 校验一致；不一致时返回 error（→ adapter Nak 重投）
- [ ] `handleEvent` 对 `json.Unmarshal` 失败的毒消息记 Error 日志后返回 nil（→ adapter Ack，不重投）
- [ ] `handleEvent` 乱序过滤：`event.EventSeq <= seenSeq[instance_id]` 时 Warn 日志并返回 nil（丢弃过期事件）
- [ ] `handleEvent` 路由：`new_status=="running"` → `StartCollection(buildSpec(...))`；`stopped/failed/deleted` → `StopCollection(instance_id)`；未知状态 → Warn 日志返回 nil
- [ ] `handleEvent` 处理失败时返回 error（→ adapter Nak 重投），**不推进 seenSeq**
- [ ] `handleEvent` 处理成功后才推进 seenSeq：`event.EventSeq > seenSeq[instance_id]` 时更新
- [ ] seenSeq 是进程内存态，重启归零（接受此边界，不持久化）
- [ ] 单测覆盖：成功路径推进 seenSeq、失败路径不推进、过期事件丢弃、毒消息 Ack 跳过、租户不匹配 Nak 重投、未知状态 Ack 跳过
- [ ] Typecheck/lint 通过

## Dependencies
- Issue #002（port 接口）
- Issue #004（meteringCollectionService）
- Issue #006（buildSpec）

## Type
core

## Priority
high

## Labels
core

## Batch
PR-M3

## SPEC Reference
- SPEC §5.1.4 Consumer handleEvent seenSeq 乱序过滤
- SPEC §6.1 Error Taxonomy
- PRD FR-30 ~ FR-34
