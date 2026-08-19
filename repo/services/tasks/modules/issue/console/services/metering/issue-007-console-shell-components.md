# [Console] Shell 组件——ConsolePage / ConsolePageHeader / ConsoleContentCard

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/console/services/metering/spec-console-metering-service.md`

## Description

创建 Console 前端通用 shell 组件 `ConsolePage`、`ConsolePageHeader`、`ConsoleContentCard`，供 usage 页及其他页面使用。当前 `src/components/` 目录不存在，需新建。

## Scope
- Product line: console
- Code paths allowed: `repo/frontends/console/src/components/shell/`

## Acceptance Criteria
- [ ] `ConsolePage` 提供页面级布局容器
- [ ] `ConsolePageHeader` 提供标题 + 描述区
- [ ] `ConsoleContentCard` 提供内容卡片容器
- [ ] TDesign 风格一致
- [ ] Typecheck 通过

## Dependencies
None — 可与 #1（OpenAPI）并行

## Type
console

## Priority
high

## Labels
console

## Batch
TBD

## References
- SPEC: Console §2.4
