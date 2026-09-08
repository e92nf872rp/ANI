# [Console] feature/usage 基础模块——constants / types / useDebouncedFilter

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/console/services/metering/spec-console-metering-service.md`

## Description

创建 `src/features/usage/` 基础模块：`constants.ts`（RESOURCE_TYPE_TABS 5 启用 + 2 disabled、GROUP_BY_OPTIONS）、`types.ts`（UsageFilter、UsageRow）、`useDebouncedFilter.ts`（300ms debounce hook）。

## Scope
- Product line: console
- Code paths allowed: `repo/frontends/console/src/features/usage/`

## Acceptance Criteria
- [ ] `RESOURCE_TYPE_TABS`: GPU/CPU/Memory/Input/Output 5 启用 + Storage/KB 2 disabled
- [ ] 配置中**不含** token_total Tab（FR-17）
- [ ] `GROUP_BY_OPTIONS`: resource_type / az / day / hour
- [ ] `UsageFilter` 接口: start_time, end_time, resource_type?, group_by?
- [ ] `useDebouncedFilter(filter, 300ms)`: 延迟后返回 debounced 值，取消旧值
- [ ] 单元测试: debounce 延迟、取消旧值、defaultTimeRange 近 30 天

## Dependencies
None

## Type
console

## Priority
high

## Labels
console

## Batch
TBD

## References
- SPEC: Console §2.4, §3.2, §5.1, §5.2
- UX: §5.1
