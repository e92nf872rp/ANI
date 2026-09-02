# [BOSS] PlatformMetricPage 专页模板 + 5 P0 专页路由

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/boss/services/metering/spec-boss-metering-service.md`

## Description

创建专页通用模板 `PlatformMetricPage` + 5 个 P0 专页路由（gpu-hours, cpu-hours, memory-gbhours, input-tokens, output-tokens）。专页固定 resource_type，UI 不提供指标切换。面包屑: `平台计量与结算 / {指标名}`。

## Scope
- Product line: boss
- Code paths allowed: `repo/frontends/boss/src/routes/metering/`, `repo/frontends/boss/src/features/platform-metering/PlatformMetricPage.tsx`

## Acceptance Criteria
- [ ] 5 路由文件: gpu-hours.tsx, cpu-hours.tsx, memory-gbhours.tsx, input-tokens.tsx, output-tokens.tsx
- [ ] 每页从 METRIC_PAGES 查找 config，固定 resource_type
- [ ] 专页不提供指标视角切换（与聚合页区分）
- [ ] 复用 RankTable + TrendChart + KPI 组件
- [ ] 面包屑: `平台计量与结算 / {title}`
- [ ] [UI] Matches UX §4.3 专页模板

## Dependencies
#17（PlatformFilterBar + Alerts）, #18（RankTable + Chart + KPI）

## Type
boss

## Priority
high

## Labels
boss

## Batch
TBD

## References
- SPEC: BOSS §5.1 专页流程
- UX: §4.3
