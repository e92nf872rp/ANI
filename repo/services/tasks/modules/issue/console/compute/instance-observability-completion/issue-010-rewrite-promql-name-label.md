# rewritePromQLLabels 扩展支持 name label（OQ-4 决策）

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability-completion.md

## Description

修改 `rewritePromQLLabels`，新增 `case "name":` 分支，用精确匹配 `name="record.Name"`（非正则）。这是 OQ-4 的 SPEC 决策：**扩展后端重写链路支持 `name` label**，而非前端占位符注入。

**理由**：
- 现有 `namespace`/`pod` label 重写链路已统一，VM 新增 `name` label 保持架构一致性
- 前端模板风格保持一致（都用 `{{namespace}}`、`{{instance_id}}` 占位符）
- 后端统一处理租户隔离和实例名注入，前端不感知重写逻辑

## Scope
- Product line: core
- Code paths allowed: `repo/pkg/adapters/runtime/prometheus_observability_service.go`

## Acceptance Criteria

- [ ] 修改 `repo/pkg/adapters/runtime/prometheus_observability_service.go` 的 `rewritePromQLLabels`，支持 `name` label 重写（当前只支持 `namespace` 和 `pod`）
- [ ] 新增 `case "name":` 分支，用精确匹配 `name="record.Name"`（非正则 `name=~"..."`）
- [ ] 现有 `container` / `gpu_container` 的 `pod` label 重写不回归
- [ ] Typecheck/lint passes
- [ ] 单元测试覆盖 `name` label 重写路径

## Dependencies
None

## Type
core

## Priority
high

## Labels
core

## Batch
B-6

## References
- SPEC: §5.8 rewritePromQLLabels 扩展、§11.1 OQ-4 已关闭（US-010）
- UX: N/A（后端 label 重写）
- PRD: US-010 / FR-19
