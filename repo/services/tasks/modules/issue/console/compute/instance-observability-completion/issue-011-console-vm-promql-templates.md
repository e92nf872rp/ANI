# Console VM 指标 PromQL 模板与时序图（2 条曲线）

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability-completion.md

## Description

在 `promqlTemplates.ts` 新增 VM kind 的冻结 PromQL 模板（`instance_vm_cpu_utilization`、`instance_vm_memory_utilization`），使用 `name` label 而非 `pod`。`getTemplatesForKind` 对 `kind=vm` 返回 VM 模板列表（2 条曲线：CPU 利用率、内存使用率），不展示网络 RX/TX 时序曲线（网络数据仅在快照卡片中展示）。`MetricsChart.tsx` 的 `SERIES_COLORS` 新增 VM 模板配色（复用 container 蓝绿 `#0052D9`/`#2BA471`，语义一致）。依赖 #9 VM 分支后端修复和 #10 name label 重写扩展。

## Scope
- Product line: console
- Code paths allowed: `repo/frontends/console/src/features/instance-observability/promqlTemplates.ts`, `repo/frontends/console/src/features/instance-observability/MetricsChart.tsx`

## Acceptance Criteria

- [ ] `repo/frontends/console/src/features/instance-observability/promqlTemplates.ts` 新增 VM kind 的冻结 PromQL 模板，使用 `name` label 而非 `pod`
- [ ] VM CPU 利用率模板：`rate(kubevirt_vmi_cpu_usage_seconds_total{namespace="{{namespace}}",name="{{instance_id}}"}[5m])`
- [ ] VM 内存使用率模板：`(kubevirt_vmi_memory_domain_bytes{namespace="{{namespace}}",name="{{instance_id}}"} - kubevirt_vmi_memory_usable_bytes{namespace="{{namespace}}",name="{{instance_id}}"}) / kubevirt_vmi_memory_domain_bytes{namespace="{{namespace}}",name="{{instance_id}}"}`
- [ ] `getTemplatesForKind` 对 `kind=vm` 返回 VM 模板列表（2 条曲线：CPU 利用率、内存使用率），而非现有 container 模板
- [ ] VM kind 时序图不展示网络 RX/TX 曲线（网络数据仅在快照卡片中展示）
- [ ] 非 VM kind 不展示 VM 模板曲线
- [ ] `MetricsChart.tsx` 的 `SERIES_COLORS` 新增 `instance_vm_cpu_utilization: #0052D9`（蓝）、`instance_vm_memory_utilization: #2BA471`（绿），复用 container 配色
- [ ] `PROMQL_TEMPLATE_LABELS` 新增 VM 模板中文系列名（CPU 利用率、内存使用率）
- [ ] Typecheck/lint passes
- [ ] [UI] 匹配 UX §4.4 VM 时序图布局（2 条曲线：CPU 利用率、内存使用率）
- [ ] [UI] 匹配 UX §6.2 指标 Tab - 时序图状态（VM kind）：loading/empty/error-all/error-partial/range-change
- [ ] [UI] 匹配 UX §7.1 标签文案：时序图系列 VM CPU = "CPU 利用率"、VM 内存 = "内存使用率"
- [ ] 在浏览器中通过可用的 browser automation 工具或 MCP 验证 loading / empty / error 三态；若不可用则记录手动验证步骤

## Dependencies
- #9 — VM 分支后端修复
- #10 — name label 重写扩展

## Type
console

## Priority
high

## Labels
console

## Batch
B-7

## References
- SPEC: §5.6 PromQL 模板冻结表、§5.7 getTemplatesForKind 扩展、§5.9 SERIES_COLORS 扩展（US-011）
- UX: §4.4 VM 时序图布局（2 条曲线）、§5.2 新增/改动的代码模块、§6.2 时序图状态、§7.1 标签文案
- PRD: US-011 / FR-19
