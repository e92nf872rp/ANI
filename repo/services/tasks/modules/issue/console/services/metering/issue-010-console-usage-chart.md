# [Console] UsageChart——ECharts 趋势图

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/console/services/metering/spec-console-metering-service.md`

## Description

创建 ECharts 趋势图组件，按 group_by 时间桶渲染折线/柱图。echarts-for-react 已安装但全代码库无使用，此为首例引入。loading 态显示 Skeleton，empty 态不渲染假折线。

## Scope
- Product line: console
- Code paths allowed: `repo/frontends/console/src/features/usage/UsageChart.tsx`

## Acceptance Criteria
- [ ] ECharts 折线/柱图渲染 items[]
- [ ] x 轴: period (day/hour) 或 resource_type；y 轴: total_quantity
- [ ] loading 态: Skeleton
- [ ] empty 态 (items=[]): 不渲染假折线
- [ ] 与 UsageTable 共享同一 queryKey
- [ ] Typecheck 通过

## Dependencies
#8（feature/usage 基础模块）

## Type
console

## Priority
high

## Labels
console

## Batch
TBD

## References
- SPEC: Console §5.1, §5.4
- UX: §4.1, §5.1
