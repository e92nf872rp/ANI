# [BOSS] PlatformRankTable + PlatformTrendChart + PlatformKPI

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/boss/services/metering/spec-boss-metering-service.md`

## Description

创建排行表格 + 趋势图 + KPI 卡片。RankTable: tenant_id, resource_type, total_quantity, unit, period, 操作列（查看明细）。TrendChart: ECharts 按 day/hour/tenant_id 渲染。KPI: 全平台 total_quantity 汇总。单位原样展示（FR-18）。

## Scope
- Product line: boss
- Code paths allowed: `repo/frontends/boss/src/features/platform-metering/`

## Acceptance Criteria
- [ ] RankTable 列: 租户ID, 资源类型, 用量(total_quantity), 单位(unit 原样), 周期, 操作
- [ ] sortable on total_quantity
- [ ] 行操作「查看明细」→ 打开 Drawer
- [ ] FR-18: 不做单位换算
- [ ] TrendChart: ECharts 按 group_by 时间桶
- [ ] KPI: 全平台 total_quantity 汇总；聚合页可用 token_total 查询（FR-17）
- [ ] [UI] Matches UX §5.2 Table columns

## Dependencies
#16（feature/platform-metering 基础模块）

## Type
boss

## Priority
high

## Labels
boss

## Batch
TBD

## References
- SPEC: BOSS §5.1, §5.4
- UX: §4.2, §4.3, §5.2
