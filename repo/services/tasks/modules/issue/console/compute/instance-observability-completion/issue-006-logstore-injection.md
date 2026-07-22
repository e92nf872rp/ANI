# PrometheusInstanceObservability 注入 LogStore（带 fallback）

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability-completion.md

## Description

为 `PrometheusInstanceObservability` 新增可选 `logStore` 字段 + `SetLogStore` 方法，修改 ani-gateway runtime 根据环境变量 `INSTANCE_OBSERVABILITY_LOG_STORE` 选择实现（`loki` / `k8s` / 空）。`ListLogs` 逻辑：`logStore != nil` 时调 `logStore.QueryLogs`，nil 时 fallback 到现有 K8s pod log API。未配置环境变量时行为与现状完全一致（零回归）。依赖 #4 port 定义和 #5 Loki adapter 实现。

## Scope
- Product line: core
- Code paths allowed: `repo/pkg/adapters/runtime/prometheus_instance_observability.go`, `repo/services/ani-gateway/instance_observability_runtime.go`

## Acceptance Criteria

- [ ] `PrometheusInstanceObservability` 结构体新增 `logStore ports.LogStore` 字段（可选，nil 时 fallback）
- [ ] 新增 `SetLogStore(store ports.LogStore)` 方法，由 runtime 在创建时调用
- [ ] 修改 `repo/services/ani-gateway/instance_observability_runtime.go`，在 `newGatewayInstanceObservability` 中根据环境变量 `INSTANCE_OBSERVABILITY_LOG_STORE` 选择实现：`loki` → `LokiLogStore`；`elasticsearch` → 暂未实现走 fallback；空/`k8s`/`not_configured` → nil
- [ ] `ListLogs` 逻辑：`logStore != nil` 时调 `logStore.QueryLogs`，nil 时 fallback 到现有 K8s pod log API 逻辑
- [ ] 未设置 `INSTANCE_OBSERVABILITY_LOG_STORE` 时，`ListLogs` 行为与现状完全一致
- [ ] Typecheck/lint passes
- [ ] 单元测试覆盖 fallback 路径和注入路径

## Dependencies
- #4 — LogStore port 定义
- #5 — Loki adapter 实现

## Type
core

## Priority
high

## Labels
core

## Batch
B-3

## References
- SPEC: §3.2 PrometheusInstanceObservability 扩展、§5.3 ListLogs fallback、§5.4 runtime 注入（US-006）
- UX: §3.3 Primary Flow — 用户查看日志（Loki 部署后）、§6.4 日志 Tab 状态（Loki 适配后）
- PRD: US-006 / FR-6 / FR-7 / FR-8
