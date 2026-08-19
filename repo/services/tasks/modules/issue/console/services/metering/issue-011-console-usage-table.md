# [Console] UsageTable——明细表格增强 + token_total 行展示

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/console/services/metering/spec-console-metering-service.md`

## Description

增强明细表格：4 列（resource_type, total_quantity, unit, period），原样展示 total_quantity + unit（FR-18 不做换算）。未筛 resource_type 时表格可展示 token_total 行（FR-17）。rowKey = resource_type+period。

## Scope
- Product line: console
- Code paths allowed: `repo/frontends/console/src/features/usage/UsageTable.tsx`

## Acceptance Criteria
- [ ] 4 列: 资源类型、用量（total_quantity 原样）、单位（unit 原样）、统计周期（period 可空显示 —）
- [ ] FR-18: 不做 seconds→hours 换算，原样展示
- [ ] FR-17: 未筛 resource_type 时可展示 token_total 行
- [ ] loading 态: Table `loading`
- [ ] rowKey: `resource_type+period`
- [ ] [UI] Matches UX §5.1 Table columns

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
- SPEC: Console §5.4
- UX: §5.1
