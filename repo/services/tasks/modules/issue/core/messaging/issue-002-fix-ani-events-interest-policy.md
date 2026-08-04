# 修复 ANI_EVENTS stream 配置为 InterestPolicy

## Document Links
- PRD: repo/services/tasks/modules/prd/core/messaging/prd-nats-integration.md
- UX: N/A
- SPEC: repo/services/tasks/modules/spec/core/messaging/spec-nats-integration.md

## Description
把 ANI_EVENTS stream 从 WorkQueuePolicy 改为 InterestPolicy，让 event 能 fan-out 给多消费者。WorkQueuePolicy 的删除语义会导致同一事件只被一个消费者消费，不适合 event 流的多订阅者场景。ANI_TASKS stream 保持 WorkQueuePolicy 不变（task 流是点对点消费语义）。

本 Issue 可与 #1（port 契约扩展）并行开发，无依赖关系。

## Scope
- Product line: core
- Code paths allowed: `repo/pkg/bootstrap/nats.go`

## Acceptance Criteria
- [ ] `pkg/bootstrap/nats.go` 中 `ANI_EVENTS` stream 的 `Retention` 改为 `natsgo.InterestPolicy`
- [ ] `ANI_TASKS` stream 保持 `natsgo.WorkQueuePolicy` 不变
- [ ] `make validate-architecture` 通过
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
- SPEC: §3.4（Stream 配置修复）、§5.4
- PRD: US-005、FR-12、FR-13
