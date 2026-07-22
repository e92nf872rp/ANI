# Issue #8: Console GPU 算力管理页

> **Priority:** high
> **Depends On:** #1, #7
> **Product line:** Console
> **Document Links:**
> - UX: `repo/services/tasks/modules/prd/console/gpu-inventory/ux-console-gpu-scheduling.md` §4.2, §5.1
> - SPEC: `repo/services/tasks/modules/spec/console/gpu-inventory/spec-console-gpu-scheduling.md` §5.1
> - Module: `repo/services/docs/console-modules/compute/gpu-management.md`

## Scope

- `repo/frontends/console/src/routes/compute/gpu.tsx`（NEW）
- `repo/frontends/console/src/routes/__root.tsx`（MODIFY：新增「算力与云资源」菜单组）
- `repo/frontends/console/src/api/core-schema.d.ts`（REGEN）

## Description

Console GPU 算力管理主页面：KPI + 型号分布 + Tabs(节点/设备/占用分布) + DCGM 利用率。消费已冻结的 `GET /gpu-inventory` + `GET /gpu-inventory/occupancy` + `GET /observability/query`。

## Acceptance Criteria

- [x] KPI 5 卡：总量 / 已分配 / 空闲 / 平均利用率 / 异常
- [x] 型号分布（`by_gpu_type`，ECharts Progress 或条形图）
- [x] Tabs：节点（聚合 inventory by node）/设备（明细 `GPUInventoryRecord`）/占用分布
- [x] DCGM 利用率经 `coreApi.GET /observability/query` PromQL
- [x] DCGM 降级：query 失败时显示「监控未就绪」Tag，不阻塞页面
- [x] loading：KPI Skeleton + Table loading
- [x] empty：total=0 时 `Empty`「集群暂无 GPU 设备」
- [x] error：页顶 `Alert theme="error"` + 重试
- [x] forbidden：`403` 时 `Alert`「无权查看」
- [x] `__root.tsx` 侧栏新增「算力与云资源」菜单组
- [x] `make gen-core-schema` 生成新类型
- [x] `pnpm type-check && pnpm lint && pnpm test && pnpm build` 通过（type-check + build 通过；lint 同 Console 无 eslint config）

## Validation

```bash
cd repo/frontends/console
pnpm type-check
pnpm lint
pnpm test
pnpm build
```
