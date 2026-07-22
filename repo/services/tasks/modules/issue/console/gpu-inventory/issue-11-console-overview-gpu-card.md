# Issue #11: Console 概览页 GPU 利用率卡

> **Priority:** medium
> **Depends On:** #1, #7
> **Product line:** Console
> **Document Links:**
> - UX: `repo/services/tasks/modules/prd/console/gpu-inventory/ux-console-gpu-scheduling.md` §4.1
> - SPEC: `repo/services/tasks/modules/spec/console/gpu-inventory/spec-console-gpu-scheduling.md` §5.1
> - Module: `repo/services/docs/console-modules/home/home-gpu-utilization.md`

## Scope

- `repo/frontends/console/src/routes/index.tsx`（MODIFY：新增 GPU 利用率卡）

## Description

在 Console 概览页新增 GPU 利用率卡，复用 `GET /gpu-inventory/occupancy`。当前 `routes/index.tsx` 仅有占位注释 `{/* GPU 资源卡片、知识库调用量图表等后续实现 */}`。

## Acceptance Criteria

- [x] 新增 GPU 利用率卡，消费 `coreApi.GET /gpu-inventory/occupancy`
- [x] 卡片显示：总量 / 已分配 / 空闲 / 异常（`GPUOccupancyStats`）
- [x] 卡片点击跳转 `/compute/gpu`
- [x] 与现有推理服务卡布局兼容（`Row`/`Col` span=6）
- [x] DCGM 未就绪时显示「监控未就绪」Tag
- [x] loading：Skeleton
- [x] error：卡片降级显示「数据加载失败」
- [x] `pnpm type-check && pnpm lint && pnpm build` 通过

## Validation

```bash
cd repo/frontends/console
pnpm type-check
pnpm lint
pnpm build
```

## Development Record

### 变更文件

| 文件 | 类型 | 说明 |
|---|---|---|
| `repo/frontends/console/src/routes/index.tsx` | MODIFY | 新增 GPU 利用率卡，消费 `/gpu-inventory/occupancy` + DCGM 降级 Tag + Skeleton/error 态 |

### 实现要点

- 复用 `coreApi.GET /gpu-inventory/occupancy` 获取 `GPUOccupancyStats`（total/in_use/available/fault）
- DCGM 通过 `coreApi.GET /observability/query` PromQL `avg(DCGM_FI_DEV_GPU_UTIL{job="dcgm-exporter"})` 探测就绪状态；失败时显示「监控未就绪」Tag
- 卡片点击通过 `useNavigate` 跳转 `/compute/gpu`
- 与现有推理服务卡共用 `Row gutter={16}` + `Col span={6}` 布局
- 三态：loading → `Skeleton`；error →「数据加载失败」文案；正常 → 4 列 Statistic

### 验证结果

- `npx tsc --noEmit` → EXIT_CODE: 0
- `npx vite build` → built in 4m 28s，BUILD_EXIT: 0
- 注：`pnpm lint` 因 pre-existing eslint config 缺失无法运行（与 Issue #7-#10 一致）
