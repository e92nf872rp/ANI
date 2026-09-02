# [BOSS] feature/platform-metering 基础模块——constants / types / hooks

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/boss/services/metering/spec-boss-metering-service.md`

## Description

创建 `src/features/platform-metering/` 基础模块：`constants.ts`（METRIC_PAGES 5 P0 + 2 P1 配置）、`types.ts`（PlatformUsageFilter, PlatformUsageRow）、`usePlatformUsageQuery.ts`（平台查询 hook）、`useDebouncedFilter.ts`（300ms debounce）。

## Scope
- Product line: boss
- Code paths allowed: `repo/frontends/boss/src/features/platform-metering/`

## Acceptance Criteria
- [ ] `METRIC_PAGES`: 5 P0（GPU/CPU/Memory/Input/Output）+ 2 P1（Storage/KB, p0_enabled=false）
- [ ] `PlatformUsageFilter`: start_time, end_time, resource_type?, group_by?(tenant_id|day|hour), tenant_id?
- [ ] `usePlatformUsageQuery`: queryKey 构建 + coreApi.GET('/metering/usage/platform')
- [ ] `useDebouncedFilter`: 300ms 延迟
- [ ] 单元测试: METRIC_PAGES 配置、queryKey 构建

## Dependencies
#14（API 客户端 + 类型生成）

## Type
boss

## Priority
high

## Labels
boss

## Batch
TBD

## References
- SPEC: BOSS §3.2, §5.1
- UX: §4.3
