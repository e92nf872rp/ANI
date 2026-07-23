# Console 日志 Tab

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability.md

## Description

实现实例详情日志 Tab：调用 `listInstanceLogs`，cursor 分页，级别筛选，展示 timestamp/level/message/container/stream。对齐 PRD US-008 与 SPEC §4.1.1、§5.7、§6.1、§9.1。

## Scope
- Product line: console
- Code paths allowed: repo/frontends/console/src/features/instance-observability/

## Acceptance Criteria
- [ ] 新建 `LogsTab.tsx`，调用 `coreApi.GET('/instances/{instance_id}/logs', { query: { limit: 100, cursor, level } })`
- [ ] 默认 `limit=100`；支持「加载更多」cursor 分页（有 `next_cursor` 时显示按钮）
- [ ] 展示列：timestamp、level Tag、message（monospace，超长 ellipsis + tooltip）、container、stream
- [ ] 级别筛选 `Select`：全部 / debug / info / warn / error → query `level`
- [ ] level Tag theme：error→danger, warn→warning, info→primary, debug→default
- [ ] 空日志展示 `Empty description="暂无日志"`
- [ ] API 失败展示 `Alert theme="error"` + message + `request_id` + 重试按钮
- [ ] loading 态：`Table loading`
- [ ] 无 `next_cursor` 时隐藏「加载更多」
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
- §4.1.1 日志 API 调用契约
- §5.7 日志组件
- §6.1 日志 Tab 状态
- §9.1 Console 测试矩阵（US-008）

## UX Reference
- §4.2 日志 Tab 布局
- §5.2 日志 Tab 组件映射
- §6.1 日志 Tab 状态
- §7.1/7.2 Copy
