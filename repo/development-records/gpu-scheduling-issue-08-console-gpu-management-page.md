# GPU-Scheduling-Issue-08: Console GPU 算力管理页

> **批次类型：** Feature batch
> **依赖：** #1, #7
> **完成日期：** 2026-07-06
> **SPEC：** `spec-console-gpu-scheduling.md` §5.1
> **UX：** `ux-console-gpu-scheduling.md` §4.2, §5.1

---

## 1. 范围

Console GPU 算力管理主页面：KPI 5 卡 + 型号分布 + Tabs(节点/设备/占用分布) + DCGM 利用率降级。

### 变更文件

| 文件 | 类型 | 说明 |
|---|---|---|
| `repo/frontends/console/src/routes/compute/gpu.tsx` | NEW | GPU 算力管理页（路由 `/compute/gpu`） |
| `repo/frontends/console/src/routes/__root.tsx` | MODIFY | 侧栏新增「算力与云资源」菜单组 |

---

## 2. 实现要点

### 2.1 页面结构

```
ConsolePage (shell)
  └── ConsolePageHeader (title + 刷新按钮)
  └── KPI Row (5 卡: 总量/已分配/空闲/平均利用率/异常)
  └── 型号分布 (ECharts 条形图, by_gpu_type)
  └── ConsoleContentCard
      └── Tabs
          ├── 节点 Tab (聚合 inventory by node_name)
          ├── 设备 Tab (明细 GPUInventoryRecord)
          └── 占用分布 Tab (by_gpu_type 列表)
```

### 2.2 数据源（消费冻结端点）

- `GET /gpu-inventory/occupancy` → KPI + 型号分布
- `GET /gpu-inventory` → 设备明细 + 节点聚合
- `GET /observability/query` (PromQL: `avg(DCGM_FI_DEV_GPU_UTIL)`) → 平均利用率

### 2.3 三态处理

| 状态 | 处理 |
|---|---|
| loading | KPI Skeleton + Table loading |
| empty | total=0 时 `Empty`「集群暂无 GPU 设备」 |
| error | 页顶 `Alert theme="error"` + 重试按钮 |
| forbidden (403) | `Alert`「无权查看」 |
| DCGM 降级 | query 失败时 KPI 卡显示「监控未就绪」Tag，不阻塞页面 |

### 2.4 侧栏菜单

`__root.tsx` 新增 `Menu.SubMenu value="compute"` 菜单组「算力与云资源」，含 `GPU 算力管理`入口。

---

## 3. 验收

### 3.1 12 项 AC 全部通过

| AC | 验证 |
|---|---|
| KPI 5 卡 | ✅ Statistic: 总量/已分配/空闲/平均利用率/异常 |
| 型号分布 | ✅ ECharts 条形图（stack: 总量/已用/空闲） |
| Tabs 三页 | ✅ 节点(聚合)/设备(明细)/占用分布 |
| DCGM PromQL | ✅ `coreApi.GET /observability/query` |
| DCGM 降级 | ✅ 失败时「监控未就绪」Tag |
| loading | ✅ Skeleton + Table loading |
| empty | ✅ Empty「集群暂无 GPU 设备」 |
| error | ✅ Alert theme="error" + 重试 |
| forbidden | ✅ 403 → Alert「无权查看」 |
| 侧栏菜单组 | ✅ 「算力与云资源」→ GPU 算力管理 |
| gen-core-schema | ✅ 已有 core-schema.d.ts（含 GPU 端点类型） |
| type-check + build | ✅ tsc EXIT_CODE:0 + vite build built in 4m 40s |

### 3.2 验证命令

```bash
cd repo/frontends/console
npx tsc --noEmit     # EXIT_CODE: 0
npx vite build      # built in 4m 40s
```

### 3.3 已知限制

- Console 项目无 eslint config，`pnpm lint` 无法运行（pre-existing）
- `instance_id` 链接因 `gpu-containers/$instanceId` 路由尚未创建（Issue #9），暂用文本展示
