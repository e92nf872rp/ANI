# Console browser 验证收口

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability.md

## Description

对全部 9 种 kind 的实例详情观测 Tab 进行 browser 验证收口，确认 Tab 差异、状态、null 处理符合 PRD/UX。对齐 PRD US-007、US-015 与 SPEC §9.2、§9.4(US-007,015)。

## Scope
- Product line: console
- Code paths allowed: repo/frontends/console/src/features/instance-observability/

## Acceptance Criteria
- [ ] browser 验证 Tab 差异：
  - container：有终端；无控制台、无安全事件
  - gpu_container：有终端；无控制台、无安全事件
  - sandbox：有终端 + 安全事件
  - vm：有控制台；无终端
  - batch_job：无终端
  - notebook：无终端
  - k8s_cluster：无指标 Tab
  - bare_metal：无指标 Tab
  - dpu_node：无指标 Tab
- [ ] browser 验证日志 empty：Empty 非 error
- [ ] browser 验证指标 partial null：gpu_container GPU 卡片「暂不可用」
- [ ] browser 验证指标 chart empty：container PromQL 无数据 Empty
- [ ] browser 验证终端 disabled：container stopped 按钮 disabled
- [ ] browser 验证 exec 403：container Alert 无权限
- [ ] 指标 Tab 按 capability 矩阵隐藏/展示（unsupported kind 不渲染 Tab）
- [ ] 字段 null 时不隐藏整个指标 Tab，仅相关卡片「暂不可用」
- [ ] 全 kind typecheck/lint 通过

## Dependencies
#4（LogsTab）、#5（EventsTab）、#6（MetricsTab）、#7（TerminalTab）、#8（ConsoleTab）、#9（SecurityEventsTab）

## Type
console

## Priority
medium

## Labels
console

## Batch
TBD

## SPEC Reference
- §9.2 Browser 验证清单
- §9.4(US-007,015) 测试矩阵

## UX Reference
- §9 Browser Verification Checklist
