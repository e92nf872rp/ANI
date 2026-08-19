# [BOSS] 2 P1 占位路由——storage-gbdays + kb-queries（api-not-ready Empty）

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/boss/services/metering/spec-boss-metering-service.md`

## Description

创建 2 个 P1 占位页路由。路由可进但内容区显示 api-not-ready Empty「待 API 合入（P1）」。不伪造数据。

## Scope
- Product line: boss
- Code paths allowed: `repo/frontends/boss/src/routes/metering/storage-gbdays.tsx`, `repo/frontends/boss/src/routes/metering/kb-queries.tsx`

## Acceptance Criteria
- [ ] 2 路由可进入
- [ ] 内容区: Empty「该指标待 API 合入（P1）」
- [ ] 不伪造数据（FR-12, NG-7）
- [ ] 面包屑: `平台计量与结算 / Storage-GBDays` 和 `平台计量与结算 / KB Queries`

## Dependencies
#21（PlatformMetricPage 专页模板 + 5 P0 专页路由）

## Type
boss

## Priority
medium

## Labels
boss

## Batch
TBD

## References
- SPEC: BOSS §5.3 P1 page disabled
- UX: §6.2 P1 tab/page disabled
- PRD: NG-7
