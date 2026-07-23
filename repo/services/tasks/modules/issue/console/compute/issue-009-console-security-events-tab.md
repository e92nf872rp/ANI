# Console Sandbox 安全事件 Tab

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability.md

## Description

实现实例详情安全事件 Tab：仅 `kind=sandbox`，调用 `listInstanceSecurityEvents`，支持 severity 筛选。对齐 PRD US-014 与 SPEC §4.1.7、§9.4(US-014)。

## Scope
- Product line: console
- Code paths allowed: repo/frontends/console/src/features/instance-observability/

## Acceptance Criteria
- [ ] 新建 `SecurityEventsTab.tsx`，仅 `kind=sandbox` 渲染（其余 kind 无此 Tab）
- [ ] 调用 `coreApi.GET('/instances/{instance_id}/security-events', { query: { severity, limit } })`
- [ ] severity 筛选 `Select`：全部 / info / warning / critical
- [ ] 展示列：occurred_at、severity Tag、event_type、description
- [ ] severity Tag theme：critical→danger, warning→warning, info→primary
- [ ] 空事件展示 `Empty description="暂无安全事件"`
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
- §4.1.7 安全事件 API 调用契约
- §9.4(US-014) 测试矩阵

## UX Reference
- §4.7 安全事件 Tab 布局
- §5.6 安全事件组件映射
- §6.6 安全事件 Tab 状态
