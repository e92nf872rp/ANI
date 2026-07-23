# VM 指标 Tab 快照卡片验证（依赖 #9 后端修复）

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability-completion.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability-completion.md

## Description

验证 `kind=vm` 实例指标 Tab 快照卡片展示 guest OS 真实 CPU/内存/网络数据。Console 端卡片渲染逻辑已通用就绪（`MetricsSnapshot.tsx` 对所有 kind 通用渲染 CPU/内存/网络卡片），本 issue 的剩余工作是确保 #9 完成后 VM 快照数据从 `kubevirt_vmi` 指标返回，而非 QEMU cgroup 数据。前端无新增组件，仅端到端验证。

## Scope
- Product line: console
- Code paths allowed: `repo/frontends/console/src/features/instance-observability/MetricsSnapshot.tsx`（仅验证，无改动）

## Acceptance Criteria

- [ ] #9 完成后，`kind=vm` 实例调用 `GET /api/v1/instances/{instance_id}/metrics` 时 CPU/内存/网络字段为非 null 值，且数据来源是 `kubevirt_vmi_*` 指标（不是 `container_*` cgroup 指标）
- [ ] VM 快照卡片通过 `MetricsSnapshot.tsx` 通用渲染，展示 CPU 利用率、内存 used/total、网络 RX/TX
- [ ] KubeVirt virt-handler 不可用时字段为 null，卡片显示「暂不可用」，不伪造 0（已实现，无新增工作）
- [ ] 非 VM kind 不走 VM 分支，卡片行为不回归（依赖 #9 的分支隔离）
- [ ] Typecheck/lint passes
- [ ] [UI] 匹配 UX §4.2 VM 快照卡片布局（4 个卡片：CPU、内存、网络 RX、网络 TX，无 GPU 卡片）
- [ ] [UI] 匹配 UX §6.1 指标 Tab - 快照卡片状态（VM kind）：idle/idle-partial-null/loading/error/forbidden/stale-refresh
- [ ] [UI] 匹配 UX §6.5 Edge States：VM 实例但 KubeVirt virt-handler 不可用 → 快照卡片字段 null 显示「暂不可用」
- [ ] 在浏览器中通过可用的 browser automation 工具或 MCP 验证 VM 实例指标 Tab 的 loading / partial-null / error 三态；若不可用则记录手动验证步骤

## Dependencies
#9 — GetMetrics VM 分支（VM 快照数据来源切换）

## Type
console

## Priority
high

## Labels
console

## Batch
B-8

## References
- SPEC: §2.3.3 VM 指标端到端数据流（US-012）
- UX: §4.2 VM 快照卡片布局、§6.1 快照卡片状态、§6.5 Edge States
- PRD: US-012
