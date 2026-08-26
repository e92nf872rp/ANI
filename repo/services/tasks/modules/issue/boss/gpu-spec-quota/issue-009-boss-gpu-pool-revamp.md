# BOSS GPU 资源池改版 — 4 Tab + KPI + 节点/设备/队列扩展

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
- UX: `repo/services/tasks/modules/ux/core/gpu-spec-quota/ux-gpu-spec-quota.md`
- SPEC: `repo/services/tasks/modules/spec/core/gpu-spec-quota/spec-gpu-spec-quota.md`

## Description

作为平台运维管理员，在 BOSS GPU 资源池页查看节点聚合视图（含配额分配入口）、异常设备管理（含维护/恢复操作）、调度队列状态（含 allocated 列 + state 徽标列），并引入 coreApi 客户端。

## Scope

- Product line: boss
- Code paths allowed: `repo/frontends/boss/src/routes/_authenticated/ops/gpu-pool.tsx`、`repo/frontends/boss/src/api/coreClient.ts`

## Acceptance Criteria

- [ ] 引入 coreApi（取租户列表 + 配额管理调用），`boss/src/api/coreClient.ts` 新增 quota/spec/inventory 相关调用
- [ ] GPU 资源池页改版（`gpu-pool.tsx`）：4 Tab 切换（节点聚合 | 异常设备 | 调度队列 | 规格目录），复用现有页面结构
- [ ] [UI] KPI 扩展：整卡/vGPU 节点数（不只是总量/已分配/空闲/异常）— UX §4.1 Tab 1 KPI Cards
- [ ] [UI] 节点聚合 Tab 扩展 gpu_mode/gpu_spec 列 + 新增"配额分配"入口 Button — UX §4.1 Tab 1
- [ ] [UI] 异常设备 Tab 新增操作列"标记维护"/"恢复空闲"（status=maintenance 时显示"恢复空闲"）— UX §4.1 Tab 2
- [ ] [UI] 调度队列 Tab 新增 allocated 列展示 Queue status.allocated + 新增 state 徽标列展示 Queue status.state（open=success / closed=default / unknown=default）— UX §4.1 Tab 3 + §5.1
- [ ] [UI] 状态标签：maintenance=warning, fault=danger, 空闲=success — UX §7.3
- [ ] `cd repo/frontends/boss && npx tsc --noEmit` 通过
- [ ] `cd repo/frontends/boss && npx vite build` 通过

## Dependencies

#8

## Type

boss

## Priority

high

## Labels

boss

## Batch

M2.1-TASK-A

## References

- SPEC: §4.3 (API endpoints consumed by BOSS)
- UX: §4.1 (BOSS GPU 资源池 4 Tab 布局, 各 Tab 区域内容), §7.3 (Status Tag copy)
