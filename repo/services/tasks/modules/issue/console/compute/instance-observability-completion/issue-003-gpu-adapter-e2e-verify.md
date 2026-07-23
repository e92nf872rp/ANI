# 验证 GPU adapter 分支端到端（依赖 #1+#2）

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability-completion.md

## Description

验证 `kind=gpu_container` 实例调用 `GET /instances/{id}/metrics` 时，GPU 相关字段（利用率、显存 used/total）为非 null 值。依赖 #1 handler 传 Kind 和 #2 DCGM scrape 配置。adapter 现有 GPU 分支无需改动，本 issue 是端到端验证 + 集成测试补全。

## Scope
- Product line: core
- Code paths allowed: `repo/pkg/adapters/runtime/prometheus_instance_observability.go`（仅测试补充，不改分支逻辑）

## Acceptance Criteria

- [ ] `repo/pkg/adapters/runtime/prometheus_instance_observability.go` 现有 GPU 分支（`DCGM_FI_DEV_GPU_UTIL`、`DCGM_FI_DEV_FB_USED`、`DCGM_FI_DEV_FB_TOTAL`）依赖 #1 修复后触发，指标名无需改动
- [ ] `kind=gpu_container` 实例调用 `GET /api/v1/instances/{instance_id}/metrics` 时，GPU 相关字段（利用率、显存 used/total）为非 null 值（DCGM exporter 可用时）
- [ ] `kind != gpu_container` 实例调用同一接口时，GPU 字段为 null（分支不触发）
- [ ] Typecheck/lint passes
- [ ] 集成测试覆盖 `kind=gpu_container` 路径，断言 GPU 字段非 null

## Dependencies
- #1 — handler 传 Kind
- #2 — DCGM scrape 配置

## Type
core

## Priority
high

## Labels
core

## Batch
B-1

## References
- SPEC: §2.3.1 GPU 指标端到端数据流（US-003）
- UX: §3.2 Primary Flow — GPU 用户查看指标（修复后）
- PRD: US-003 / FR-3 / FR-4
