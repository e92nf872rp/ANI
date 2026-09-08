# [Console] UsageFilterBar——DateRangePicker + 预设视角 Tabs + group_by Segmented

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/console/services/metering/spec-console-metering-service.md`

## Description

创建筛选区组件：DateRangePicker（必填，校验 start < end）、预设视角 Tabs（theme="card"，5 P0 启用 + 2 P1 disabled + Tooltip）、group_by Segmented。筛选变更触发 debounce 自动查询，无查询按钮。

## Scope
- Product line: console
- Code paths allowed: `repo/frontends/console/src/features/usage/UsageFilterBar.tsx`

## Acceptance Criteria
- [ ] DateRangePicker: 必填，start ≥ end 时 inline 错误「结束时间必须晚于开始时间」
- [ ] Tabs: 5 P0 启用（GPU/CPU/Memory/Input/Output），2 P1 disabled + Tooltip「待 API 合入（P1）」
- [ ] Segmented: resource_type / az / day / hour 4 选项
- [ ] 无查询按钮（debounce auto-fetch）
- [ ] [UI] Matches UX §5.1 组件映射

## Dependencies
#7（Shell 组件）, #8（feature/usage 基础模块）

## Type
console

## Priority
high

## Labels
console

## Batch
TBD

## References
- SPEC: Console §5.1, §5.2, §5.3
- UX: §5.1, §6.1, §7.1
