# [BOSS] TenantDrilldownDrawer——单租户钻取（FR-16 platform path）

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/boss/services/metering/spec-boss-metering-service.md`

## Description

创建钻取 Drawer（size="large", footer=false）。点击排行行「查看明细」打开，调用 `GET /metering/usage/platform?tenant_id={行ID}&...&group_by=day`（FR-16 禁止租户 path）。Drawer 内 Table + Chart，继承主查询 resource_type。

## Scope
- Product line: boss
- Code paths allowed: `repo/frontends/boss/src/features/platform-metering/TenantDrilldownDrawer.tsx`

## Acceptance Criteria
- [ ] Drawer size="large", footer=false
- [ ] 调用 `GET /metering/usage/platform?tenant_id=...`（FR-16，禁止 GET /metering/usage）
- [ ] 继承主查询 resource_type；group_by 默认 day
- [ ] drilldown loading: Drawer 内 Skeleton
- [ ] drilldown forbidden(403): Drawer 内 Alert「无权限查看该租户用量」
- [ ] 若主查询 group_by=tenant_id 且行已含明细 → 可省略二次请求
- [ ] [UI] Matches UX §3.2 钻取流程 + §6.2 drilldown 状态

## Dependencies
#18（PlatformRankTable + PlatformTrendChart + PlatformKPI）

## Type
boss

## Priority
high

## Labels
boss

## Batch
TBD

## References
- SPEC: BOSS §5.1 钻取流程, §5.3
- UX: §3.2, §6.2
- PRD: FR-16
