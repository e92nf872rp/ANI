# Console 路由壳层 + 实例上下文

## Document Links
- PRD: repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md
- UX: repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability.md
- SPEC: repo/services/tasks/modules/spec/console/compute/spec-console-instance-observability.md

## Description

为实例详情可观测性建立 Console 端路由壳层与实例上下文 Provider。新建 `routes/compute/instances/$instanceId/route.tsx`（详情壳层：PageHeader + Tab 栏 + Tab Panel），`InstanceContext` Provider，以及 `observabilityTabsConfig`（kind → 可见 Tab 映射常量）。对齐 PRD US-007 与 SPEC §2.4.1、§3.3、§5.1.1。

## Scope
- Product line: console
- Code paths allowed: repo/frontends/console/src/

## Acceptance Criteria
- [ ] 新建 `routes/compute/instances/$instanceId/route.tsx`，渲染 PageHeader（name、id 可复制、state Tag、kind Tag）+ Tab 栏 + Tab Panel
- [ ] 新建 `features/instance-observability/InstanceContext.tsx`，提供 instance 记录、kind、state 给子 Tab
- [ ] 新建 `features/instance-observability/observabilityTabsConfig.ts`，kind → 可见 Tab 映射常量（首期 hardcode SPEC §4.1 矩阵）
- [ ] kind × Tab 可见性矩阵与 PRD §6 / UX §4.1 一致：
  - container：日志/事件/指标/终端
  - gpu_container：日志/事件/指标/终端
  - sandbox：日志/事件/指标/终端/安全事件
  - vm：日志/事件/指标/控制台
  - batch_job：日志/事件/指标
  - notebook：日志/事件/指标
  - k8s_cluster：日志/事件（**无**指标 Tab）
  - bare_metal：日志/事件（**无**指标 Tab）
  - dpu_node：日志/事件（**无**指标 Tab）
- [ ] 不渲染 hidden Tab（不展示空态 Tab）
- [ ] `deleted` 状态实例不可进入观测 Tab（整页 Empty「实例已删除」或详情只读）
- [ ] 支持 `?tab=logs|events|metrics|terminal|console|security-events` 深链；非法/不可见 tab 回退「日志」
- [ ] 面包屑：`算力与云资源 / 实例管理 / {instance.name}`
- [ ] Typecheck/lint 通过
- [ ] browser 验证：container、vm、sandbox 三种 kind 的 Tab 差异

## Dependencies
#1（Core console handler 可用，但不强阻塞——UI 可先基于现有 API 开发）

## Type
console

## Priority
high

## Labels
console

## Batch
TBD

## SPEC Reference
- §2.4.1 Console 端新建文件结构
- §3.3 Console 消费 API 客户端
- §5.1.1 壳层与导航组件

## UX Reference
- §1.1 页面分类（detail fragment，无独立 /observability 路由）
- §2.1 路由与入口
- §2.2 导航关系
- §4.0 详情页可观测性公共壳
- §4.1 Kind × Tab 可见性
- §5.1 壳层与导航组件映射
