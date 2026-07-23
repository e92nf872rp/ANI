# GetMetrics 新增 VM 分支（查询 kubevirt_vmi_* 指标）

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability-completion.md

## Description

在 `PrometheusInstanceObservability.GetMetrics` 方法新增 `if request.Kind == ports.WorkloadKindVM` 分支，位于现有 GPU 分支之前。VM 分支查询 `kubevirt_vmi_cpu_usage_seconds_total`、`kubevirt_vmi_memory_resident_bytes`、`kubevirt_vmi_memory_domain_bytes`、`kubevirt_vmi_network_receive_bytes_total`、`kubevirt_vmi_network_transmit_bytes_total`。VM 指标 label 用 `name="<vmi-name>"` 精确匹配（VMI `metadata.name` = `record.Name`，无随机后缀）。内存使用率公式：`kubevirt_vmi_memory_domain_bytes - kubevirt_vmi_memory_usable_bytes`。

## Scope
- Product line: core
- Code paths allowed: `repo/pkg/adapters/runtime/prometheus_instance_observability.go`

## Acceptance Criteria

- [ ] `repo/pkg/adapters/runtime/prometheus_instance_observability.go` 的 `GetMetrics` 方法新增 `if request.Kind == ports.WorkloadKindVM` 分支，位于现有 GPU 分支之前
- [ ] VM 分支查询指标：`kubevirt_vmi_cpu_usage_seconds_total`（CPU，Counter，快照用 `rate(...[5m])`）、`kubevirt_vmi_memory_resident_bytes`（内存已用，Gauge）、`kubevirt_vmi_memory_domain_bytes`（内存总量，Gauge）、`kubevirt_vmi_network_receive_bytes_total` / `kubevirt_vmi_network_transmit_bytes_total`（网络，Counter，快照用 `rate(...[5m])`）
- [ ] VM 指标 label 用 `name="<vmi-name>"` 精确匹配，不用 `pod=~"..."` 正则
- [ ] VMI `metadata.name` 等于 `record.Name`（已确认无随机后缀），VM 指标用 `request.InstanceID` 作为 `name` label 值
- [ ] 内存使用率公式：`kubevirt_vmi_memory_domain_bytes - kubevirt_vmi_memory_usable_bytes`
- [ ] `kind != vm` 实例不受影响，仍走现有 container/GPU 分支
- [ ] Typecheck/lint passes
- [ ] 单元测试覆盖 VM 分支的 PromQL 构造和 label 匹配

## Dependencies
- #1 — handler 传 Kind
- #8 — KubeVirt scrape 配置（合入后才能端到端验证）

## Type
core

## Priority
high

## Labels
core

## Batch
B-5

## References
- SPEC: §5.2 GetMetrics VM 分支（US-009）
- UX: §3.1 Primary Flow — VM 用户查看指标、§4.2 VM 快照卡片布局
- PRD: US-009 / FR-15 / FR-16 / FR-17 / FR-18
