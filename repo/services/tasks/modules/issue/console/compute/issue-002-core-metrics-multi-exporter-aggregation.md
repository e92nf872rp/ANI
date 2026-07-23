# Core metrics 多 exporter 聚合 adapter

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability.md

## Description

实现 `getInstanceMetrics` 的多 exporter 聚合 adapter。当前 `PrometheusInstanceObservabilityService.GetMetrics` 仅查 `container_cpu_usage_seconds_total`（CPU），内存/网络/GPU 字段全部 nil。PRD US-003 AC #3+#4 要求 adapter 从多个 exporter 聚合（metrics.k8s.io 提供 CPU/内存/网络，DCGM 提供 GPU），SPEC §2.2.2 已定义方案但标记为「待补」，归属后续 Core 批次。本 Issue 落地该方案。

## Scope
- Product line: core
- Code paths allowed: repo/pkg/adapters/runtime/、repo/pkg/ports/

## Acceptance Criteria
- [ ] `PrometheusInstanceObservabilityService.GetMetrics` 聚合多个 exporter 结果填入 `InstanceMetricsRecord`：
  - CPU：`container_cpu_usage_seconds_total`（metrics.k8s.io）
  - 内存：`container_memory_working_set_bytes`（metrics.k8s.io）
  - 网络：`container_network_receive_bytes_total` / `container_network_transmit_bytes_total`（metrics.k8s.io）
  - GPU 利用率：DCGM exporter 指标（`DCGM_FI_DEV_GPU_UTIL`）
  - GPU 显存：DCGM exporter 指标（`DCGM_FI_DEV_FB_USED` / `DCGM_FI_DEV_FB_TOTAL`）
- [ ] `kind=gpu_container` 在 DCGM exporter 可用时填充 GPU 相关字段（`gpu_utilization_pct`、`gpu_memory_used_mb`、`gpu_memory_total_mb`）
- [ ] 非 `gpu_container` kind 的 GPU 字段为 `null`（指针 nil），**禁止**用 0 代替缺失
- [ ] 单个 exporter 不可用时不阻塞其他字段采集；已采集字段正常返回，不可采集字段为 `null`
- [ ] Gateway 不向调用方暴露 Prometheus / DCGM 地址（adapter 内部持有）
- [ ] 错误语义：`401` / `403` / `404`（与现有 getInstanceMetrics 一致）
- [ ] RBAC：`scope:instances:read`
- [ ] 单元测试覆盖：全字段聚合、gpu_container GPU 填充、非 gpu_container GPU nil、单 exporter 降级、null 字段保留
- [ ] `make test` 通过
- [ ] 不修改 OpenAPI `v1.yaml`（consume only）

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

## SPEC Reference
- §2.2.2 Core 端组件（多 exporter 聚合 adapter 待补）
- §2.3.1 Console 指标双通道数据流（adapter ← exporter(s)）
- §3.3.1 Frozen Schemas（`InstanceMetrics` 可空字段：cpu_utilization_pct, memory_*, gpu_*, network_*）
- §5.2.1 PromQL 模板表（GPU 系列使用 DCGM）
- §6.3 指标 Tab 状态（partial-null 处理）

## UX Reference
N/A（Core only；UI null 处理由 Console 指标 Tab Issue 承担）
