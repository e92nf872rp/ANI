# Issue 005: 新增 Collector 接口 + 三个 Collector 实现 + CollectAll

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/metering/prd-metering-consumer.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/metering/spec-metering-consumer.md`

## Description

实现三个 Collector（GPU 占用时长/CPU Counter 增量/内存 Gauge 瞬时占用加权），三种语义不同质，不能用一刀切累加模型。提供 CollectAll 路由入口和 Resolve 函数。

## Scope
- Product line: core
- Code paths allowed: `repo/pkg/adapters/metering/`

## Acceptance Criteria
- [ ] 新增 `pkg/adapters/metering/collectors.go`
- [ ] 定义 `Collector` interface：`Collect(ctx, spec, period string) ([]ports.MeteringUsageRecord, error)`
- [ ] 实现 `DCGMGPUCollector`：`spec.GPUSpec == nil` 时返回 nil（跳过 GPU 维度，不写 0 错值）；否则产出 `TotalQuantity = float64(GPUSpec.Count) * float64(IntervalSec)`，unit=`gpu_second`
- [ ] GPU 占用时长不查 DCGM：计量语义是"持有时长"而非"利用率"，持有 2 张 GPU 运行 60s = 120 gpu_seconds
- [ ] 实现 `KubeletCPUCollector`：查询 Prometheus `container_cpu_usage_seconds_total` 的 `rate(...[60s])`，产出 `TotalQuantity = secs * float64(IntervalSec)`，unit=`cpu_second`
- [ ] 实现 `KubeletMemCollector`：查询 Prometheus `container_memory_working_set_bytes`，产出 `TotalQuantity = bytes / 1024^3 * float64(IntervalSec)`，unit=`gib_second`
- [ ] 实现 `Resolve(collectorID string) (Collector, bool)` 函数，路由 key：`dcgm_gpu`/`kubelet_cpu`/`kubelet_mem`
- [ ] 实现 `CollectAll(ctx, spec, logger)` 包级函数：生成分钟对齐 Period（`time.Now().Format("2006-01-02T15:04")`）→ 遍历 `spec.Dimensions` → 逐个 Resolve + Collect → 聚合返回
- [ ] `CollectAll` 在 unknown collector source 时 Warn 日志并跳过（不中断其余维度）
- [ ] `CollectAll` 在单维度 Collect 失败时 Error 日志并跳过（不中断其余维度）
- [ ] 三个 Collector 产出的记录均填充 `TenantID`/`ResourceRef`/`ResourceType`/`TotalQuantity`/`Unit`/`Period` 字段
- [ ] 单测覆盖三个 Collector 的 Collect 逻辑（含 GPU 卡数缺失跳过、Prometheus 查询 mock）
- [ ] Typecheck/lint 通过

## Dependencies
- Issue #002（port 接口 CollectionSpec/MeteringUsageRecord）

## Type
core

## Priority
high

## Labels
core

## Batch
PR-M2

## SPEC Reference
- SPEC §3.2 Entity Definitions（Adapter 层）
- SPEC §5.1.6 CollectAll 三维度路由 + 公式表
- PRD FR-16 ~ FR-22
