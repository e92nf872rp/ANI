# Issue #13: BOSS GPU 资源池管理页

> **Priority:** medium
> **Depends On:** #1, #12
> **Product line:** BOSS
> **Document Links:**
> - UX: `repo/services/tasks/modules/prd/console/gpu-inventory/ux-boss-gpu-pool.md` §4, §5
> - SPEC: `repo/services/tasks/modules/spec/boss/gpu-inventory/spec-boss-gpu-pool.md` §5.1
> - Module: `repo/services/docs/boss-modules/ops/gpu-pool-management.md`

## Scope

- `repo/frontends/boss/src/routes/ops/gpu-pool.tsx`（MODIFY：从占位替换为完整页面）

## Description

BOSS GPU 资源池管理页：集群级 KPI + 型号分布 + Tabs(节点/异常/调度队列只读) + 租户排行占位。消费 `GET /gpu-inventory` + `GET /gpu-inventory/occupancy`（平台 scope）+ `GET /gpu-scheduling/queues`（只读）。

## Acceptance Criteria

- [x] 范围说明 `Alert theme="info"`（常驻）：「本页展示全平台 GPU 资源池。租户内资源请前往 Console「GPU 算力管理」。」
- [x] KPI 4 卡（总量 / 已分配 / 空闲 / 异常），集群级 occupancy
- [x] 型号分布（`by_gpu_type`）
- [x] Tabs：节点（聚合 inventory by node）/ 异常设备（filter fault|maintenance）/ 调度队列（只读全览，**无操作列**）
- [x] 节点 Table 列：node_name / GPU 总数 / 已用 / 空闲 / 异常数
- [x] 异常 Table 列：node_name / gpu_type / gpu_index / status
- [x] 队列 Table 列：name / workload_class / weight / reclaimable / 范围；**无操作列**
- [x] 租户排行 Section：P0 **仅占位** `Alert theme="info"`，**禁止**渲染空 Table 假数据
- [x] **禁止**前端循环调多租户 API 拼排行
- [x] 刷新按钮（`Button variant="outline"`）
- [x] loading：KPI Skeleton + Table loading
- [x] empty-cluster：total=0 时 KPI 为 0 + `Empty`「集群暂无 GPU 设备」
- [x] error：页顶 `Alert theme="error"` + 重试
- [x] forbidden：`403` 时 `Alert`「无权查看平台 GPU 资源池」
- [x] partial-data：occupancy OK + inventory fail → 分区 `Alert warning`
- [x] rank-placeholder：排行 Section 只显示 Alert
- [x] `pnpm type-check && pnpm build` 通过

## Validation

```bash
cd repo/frontends/boss
npx tsc --noEmit
npx vite build
```

## Development Record

### 变更文件

| 文件 | 类型 | 说明 |
|---|---|---|
| `repo/frontends/boss/src/routes/ops/gpu-pool.tsx` | MODIFY | 从占位替换为完整 GPU 资源池管理页 |

### 实现要点

- 消费 `coreApi.GET /gpu-inventory` + `/gpu-inventory/occupancy` + `/gpu-scheduling/queues`（3 个 useQuery）
- KPI 4 卡：总量 / 已分配 / 空闲 / 异常（来自 occupancy）
- 型号分布：`by_gpu_type` 数组渲染卡片
- Tabs 三页签：
  - 节点：聚合 inventory by `node_name`（total/in_use/available/fault 计数）
  - 异常设备：filter `status=fault|maintenance`，列 node_name/gpu_type/gpu_index/status + Tag
  - 调度队列：只读全览，**无操作列**，列 name/workload_class/weight/reclaimable/范围(平台默认/租户)
- 租户排行 Section：**仅占位** `Alert theme="info"`，不渲染空 Table
- 刷新按钮：`Button variant="outline"` + RefreshIcon，调用 3 个 query 的 refetch
- 状态处理：
  - loading：KPI `Skeleton` + Table `loading`
  - empty-cluster：occupancy.total=0 → KPI 全 0 + `Empty`
  - error：`Alert theme="error"` + 重试按钮
  - forbidden：403 → `Alert theme="error"`「无权查看平台 GPU 资源池」
  - partial-data：inventory fail + occupancy OK → `Alert warning`；反之亦然

### 验证结果

- `npx tsc --noEmit` → EXIT_CODE: 0
- `npx vite build` → built in 1m 47s，BUILD_EXIT: 0
- 注：`pnpm lint` 因 pre-existing eslint config 缺失无法运行（与 Issue #12 一致）
