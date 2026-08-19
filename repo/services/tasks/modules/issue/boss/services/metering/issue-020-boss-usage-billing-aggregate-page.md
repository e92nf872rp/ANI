# [BOSS] PlatformUsagePage 聚合页组合——/tenant/usage-billing

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/boss/services/metering/spec-boss-metering-service.md`

## Description

组合聚合页 `routes/tenant/usage-billing.tsx`：PlatformFilterBar + PlatformKPI + PlatformRankTable + PlatformTrendChart + 专页入口 Link 组。状态优先级: api-not-ready > forbidden > error > dev_profile。

## Scope
- Product line: boss
- Code paths allowed: `repo/frontends/boss/src/routes/tenant/usage-billing.tsx`

## Acceptance Criteria
- [ ] 组合 FilterBar + KPI + RankTable + TrendChart + 专页入口
- [ ] 默认时间范围: 近 30 天
- [ ] 调用 `GET /metering/usage/platform` + `group_by=tenant_id` 排行
- [ ] api-not-ready 态: 全页 Alert + 禁用数据区
- [ ] 状态优先级: api-not-ready > forbidden > error > dev_profile
- [ ] 专页入口 Link 组: 7 专页跳转
- [ ] [UI] Matches UX §4.2 聚合页布局
- [ ] 不轮询 JWT（PRD US-004）

## Dependencies
#17（PlatformFilterBar + Alerts）, #18（RankTable + Chart + KPI）, #19（TenantDrilldownDrawer）

## Type
boss

## Priority
high

## Labels
boss

## Batch
TBD

## References
- SPEC: BOSS §2.3, §5.1
- UX: §4.2, §6.2
