# Gateway handler 透传 record.Kind 到 GetMetrics

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability-completion.md

## Description

修改 getMetrics handler，在 InstanceObservationGetRequest 新增 `Kind: record.Kind` 字段，使 adapter 的 GPU/VM 分支在生产路径下能正确触发。这是 GPU 指标死分支修复和 VM 指标采集链路的共同前置改动。

当前问题：Gateway handler `getMetrics` 调用 adapter 时未透传 `record.Kind`，导致 `PrometheusInstanceObservability.GetMetrics` 的 GPU 分支恒不触发，`gpu_container` 实例的 GPU 利用率与显存指标在 Console 指标 Tab 中始终为 null。

## Scope
- Product line: core
- Code paths allowed: `repo/services/ani-gateway/internal/router/demo_instances.go`

## Acceptance Criteria

- [ ] 修改 `repo/services/ani-gateway/internal/router/demo_instances.go` 的 `getMetrics` 调用，在 `InstanceObservationGetRequest` 中新增 `Kind: record.Kind` 字段
- [ ] 修改后 `request.Kind` 在生产路径下等于 `record.Kind`（`container` / `gpu_container` / `vm` 等），不再是空字符串
- [ ] 现有 `container` kind 的指标行为不回归（分支逻辑不变）
- [ ] Typecheck/lint passes
- [ ] 单元测试覆盖 `kind=container`、`kind=gpu_container`、`kind=vm` 三种路径，断言传入 adapter 的 `request.Kind` 值

## Dependencies
None

## Type
core

## Priority
high

## Labels
core

## Batch
B-1

## References
- SPEC: §5.1 handler 传 Kind（US-001）
- UX: §2.3 PRD Coverage Map — US-001 隐式影响 US-003、US-012
- PRD: US-001 / FR-1
