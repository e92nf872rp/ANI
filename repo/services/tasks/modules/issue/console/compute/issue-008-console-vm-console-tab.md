# Console VM 控制台 Tab

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability.md

## Description

实现实例详情控制台 Tab：仅 `kind=vm`，通过 `createInstanceConsoleSession` 申请 console/VNC 会话，成功后新窗口打开。对齐 PRD US-013 与 SPEC §4.1.6、§5.4、§9.4(US-013)。

## Scope
- Product line: console
- Code paths allowed: repo/frontends/console/src/features/instance-observability/

## Acceptance Criteria
- [ ] 新建 `ConsoleTab.tsx`，仅 `kind=vm` 渲染（其余 kind 无此 Tab）
- [ ] 协议 `Select`：console / vnc / serial（默认 vnc）；novnc 若 API 支持则列入
- [ ] 仅 `state=running` 可点击；非 running 打开按钮 disabled
- [ ] 无 console 权限：`Alert theme="warning"`「无控制台权限」
- [ ] 调用 `coreApi.POST('/instances/{instance_id}/console', { body: { protocol } })` → 返回 `InstanceConsoleSession`
- [ ] 成功：`window.open(connect_url, '_blank', 'noopener,noreferrer')` + `Message.success`「已在新窗口打开控制台」
- [ ] `Alert theme="info"` 提示：将在新窗口打开会话，会话过期后请重新申请
- [ ] 失败：`Message.error`
- [ ] 打开中：Button loading
- [ ] Typecheck/lint 通过
- [ ] browser 验证：disabled / opening / opened / error

## Dependencies
#1（Core console handler 补全）
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
- §4.1.6 console API 调用契约
- §5.4 控制台组件
- §9.4(US-013) 测试矩阵

## UX Reference
- §4.6 控制台 Tab 布局
- §5.5 控制台组件映射
- §6.5 控制台 Tab 状态
- §7.2 Messages（已打开、失败）
