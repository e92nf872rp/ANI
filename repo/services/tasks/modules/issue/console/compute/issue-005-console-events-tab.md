# Console 事件 Tab

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability.md

## Description

实现实例详情事件 Tab：调用 `listInstanceEvents`，cursor 分页，展示 occurred_at/type/reason/message/count。对齐 PRD US-009 与 SPEC §4.1.2、§9.4(US-009)。

## Scope
- Product line: console
- Code paths allowed: repo/frontends/console/src/features/instance-observability/

## Acceptance Criteria
- [ ] 新建 `EventsTab.tsx`，调用 `coreApi.GET('/instances/{instance_id}/events', { query: { limit: 100, cursor } })`
- [ ] 展示列：occurred_at、type Tag、reason、message、count
- [ ] `type=Warning` → `Tag theme="warning"`；`Normal` → `Tag theme="default"`
- [ ] cursor 分页「加载更多」
- [ ] 空事件展示 `Empty description="暂无事件"`
- [ ] API 失败展示 `Alert theme="error"` + message + `request_id` + 重试
- [ ] loading 态：`Table loading`
- [ ] Typecheck/lint 通过
- [ ] browser 验证：loading / empty / error

## Dependencies
#3（Console 路由壳层 + InstanceContext）

## Type
console

## Priority
medium

## Labels
console

## Batch
TBD

## SPEC Reference
- §4.1.2 事件 API 调用契约
- §9.4(US-009) 测试矩阵

## UX Reference
- §4.3 事件 Tab 布局
- §5.3 事件 Tab 组件映射
- §6.2 事件 Tab 状态
