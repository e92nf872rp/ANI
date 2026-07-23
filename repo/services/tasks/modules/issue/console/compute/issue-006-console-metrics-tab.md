# Console 指标 Tab（快照卡片 + PromQL 时序图）

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability.md

## Description

实现实例详情指标 Tab 双通道：快照卡片（`getInstanceMetrics`）+ PromQL 时序折线图（`queryObservability` + 冻结模板）。对齐 PRD US-010、US-011、US-015 与 SPEC §4.1.3、§4.1.4、§5.2、§8.2、§9.4(US-010,011)。

## Scope
- Product line: console
- Code paths allowed: repo/frontends/console/src/features/instance-observability/

## Acceptance Criteria
- [ ] 新建 `MetricsTab.tsx`，双通道布局：Row2 快照卡片 + Row3/Row4 PromQL 图表
- [ ] 快照区调用 `coreApi.GET('/instances/{instance_id}/metrics')`，展示 `timestamp` 与最后刷新时间
- [ ] 快照卡片：CPU %、内存 used/total、网络 RX/TX；null 字段显示「暂不可用」（不显示 0）
- [ ] `kind=gpu_container` 额外展示 GPU 利用率、显存 used/total 卡片
- [ ] 手动刷新按钮 + 30s 自动刷新 `Switch`（默认开）
- [ ] 图表区 `Radio.Group` 时间范围：15m / 1h / 6h / 24h（默认 1h）
- [ ] 图表区调用 `coreApi.GET('/observability/query', { query: renderedPromQL })`，RBAC：`scope:observability:read`
- [ ] PromQL 来自冻结模板常量模块（`promqlTemplates.ts`），按 `instance_id` 注入；不硬编码未文档化 label
- [ ] 至少 2 条曲线：CPU 利用率、内存使用率；`gpu_container` 额外 GPU 利用率、显存使用率
- [ ] 图表高度 ≥ 280px，使用 `echarts-for-react`
- [ ] PromQL 失败/无数据：图表区展示 `Empty`「所选时间范围暂无数据」或 `Alert` error，**不伪造曲线**
- [ ] 无 observability 读权限：图表区 `Alert theme="warning"`「无权限查看趋势数据」
- [ ] 快照与图表时间标注独立（`快照时间` / `趋势数据查询于`）
- [ ] 不展示 Prometheus 地址
- [ ] Typecheck/lint 通过
- [ ] browser 验证：snapshot-loading / partial-null（gpu_container GPU「暂不可用」）/ chart-empty / chart-error

## Dependencies
#2（Core 多 exporter 聚合 adapter，提供完整快照数据）
#3（Console 路由壳层 + InstanceContext）

## Type
console

## Priority
high

## Labels
console

## Batch
TBD

## SPEC Reference
- §4.1.3 指标快照 API 调用契约
- §4.1.4 PromQL 时序 API 调用契约
- §5.2 PromQL 模板注入方案
- §8.2 Non-Frozen Capabilities（模板正文待运维文档）
- §9.4(US-010,011) 测试矩阵

## UX Reference
- §4.4 指标 Tab 双通道布局
- §5.4 指标 Tab 组件映射
- §6.3 指标 Tab 状态
- §7.2 Messages（暂不可用、无权限、无数据）
