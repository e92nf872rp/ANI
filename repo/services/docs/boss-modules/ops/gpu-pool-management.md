# GPU 资源池管理

## 页面定位

`GPU 资源池管理` 是 BOSS **资源池与基础设施** 域下的 **全平台 GPU 资源池运营** 专页：查看 GPU 总览、型号分布、节点绑定、跨租户占用排行、利用率趋势与异常设备；支持跳转监控与运维 Skills。

Console 对照：[`gpu-management.md`](../../console-modules/compute/gpu-management.md)（单租户）。overview 摘要：[`gpu-pool-trend.md`](../overview/gpu-pool-trend.md)。监控专页：[`gpu-monitoring.md`](../health/gpu-monitoring.md)。

## 文档管理规则

- 本文是 `GPU 资源池管理` 的主维护源
- PRD/SPEC 为辅助
- 流程：ANI-14 + [`boss-full-depth-checklist.md`](../governance/boss-full-depth-checklist.md)

## Core 层要求

- `GET /api/v1/gpu-inventory`（`listGPUInventory`）、`GET /api/v1/gpu-inventory/occupancy`（`getGPUOccupancy`）— YAML **已声明**；BOSS **只读参考**
- `GET /api/v1/observability/query` — PromQL
- 平台 GPU 跨租户 list/排行 — **TODO-YAML**
- RBAC 租户路径：`scope:gpu-inventory:read`；平台 scope **TODO-YAML**
- 禁止 Services deprecated `/gpu-containers*` 作为 GPU 物理库存来源

## 页面职责

- 展示全平台 GPU 总览、型号分布、节点列表、租户占用 Top N
- 利用率趋势与异常设备（`unavailable` / `maintenance`）
- 跳转 GPU 监控、gpu-pool-trend、platform-gpu-hours、maint-skills（drain）
- 标明统计窗口；**不承担** 在本页完成 drain（Phase 2 → Skills）

## 页面结构

```text
GPU 资源池管理（路由 /ops/gpu-pool）
├── PageHeader: 全平台标题 + 刷新
├── Alert: 范围说明（常驻 info，指向 Console）
├── GPU 总览 KPI（集群级 occupancy）
│   ├── 总量
│   ├── 已分配
│   ├── 空闲
│   └── 异常设备
├── 型号分布（by_gpu_type）
├── Tabs
│   ├── 节点（聚合 inventory by node）
│   ├── 异常设备（filter fault|maintenance）
│   └── 调度队列（只读全览，P0 无 CRUD）
├── 租户占用排行（P0 仅占位 Alert，P1 替换为真实 Table）
└── 操作
    ├── 查看监控
    └── 触发 drain（Phase 2 → maint-skills）
```

**P0 关键约束（UX `ux-boss-gpu-pool.md` §8.2）：**
- 租户排行 **仅占位**，不渲染空 Table 假数据，不前端循环调多租户 API
- 调度队列 Tab **只读全览**，无新建/编辑/删除（P0 BOSS 不写队列）
- 数据源复用 `GET /gpu-inventory/occupancy` 平台 scope（不新增 aggregate API）

## 数据来源与分层约束

| 层 | 路径 | 用法 |
|---|---|---|
| Core | `GET /api/v1/gpu-inventory` | `GPUInventoryDevice`；租户 **只读参考** |
| Core | `GET /api/v1/gpu-inventory/occupancy` | `GPUOccupancySummary` |
| Core | `GET /api/v1/observability/query` | 利用率/温度 PromQL |
| Core | 平台 GPU pool list **TODO-YAML** | BOSS 正式契约 |
| Core | `GET /api/v1/instances?kind=gpu_container` | 占用关联参考 |

### 关键边界

- `GPUInventoryDevice.state`：`available` / `allocated` / `unavailable` / `maintenance`
- PromQL metric 名称 **待运维模板 YAML**
- 租户 inventory **≠** BOSS 平台正式 API

## 页面区块与数据来源映射

| 区块 | 来源 | 跳转 |
|---|---|---|
| 总览 KPI | occupancy + aggregate | — |
| 型号分布 | inventory 聚合 | gpu-monitoring |
| 节点列表 | inventory `node_id` 等 | node-status |
| 租户排行 | **TODO-YAML** | tenant-list |
| 利用率趋势 | PromQL | observability/query |
| 异常设备 | `state` 筛选 | gpu-monitoring |

## BOSS 与 Console 分工

| 维度 | BOSS | Console gpu-management |
|---|---|---|
| 范围 | 全平台 GPU 池运营 | 当前租户 GPU |
| 租户排行 | 跨租户 Top N | 无 |
| drain/维护 | Phase 2 Skills | 租户内操作 |

## 当前冻结事实

| 方法 | 路径 | operationId | RBAC |
|---|---|---|---|
| GET | `/api/v1/gpu-inventory` | `listGPUInventory` | `scope:gpu-inventory:read` |
| GET | `/api/v1/gpu-inventory/occupancy` | `getGPUOccupancy` | `scope:gpu-inventory:read` |
| GET | `/api/v1/observability/query` | `queryObservability` | `scope:observability:read` |

| 能力 | 状态 |
|---|---|
| 平台 GPU pool | **ADDED-TO-YAML** `listPlatformResourcePools` + trends/gpu |
| GPU drain 写操作 | Phase 2 → maint-skills |

## 字段级定义

| 字段 | 说明 |
|---|---|
| `gpu_total` | 设备总数 |
| `gpu_allocated` | 已分配 |
| `gpu_available` | 空闲 |
| `gpu_unavailable` | 异常 |
| `model_distribution` | 型号 → 数量 |
| `tenant_rank[]` | **待 YAML** |
| `avg_utilization_pct` | 窗口 % |
| `node_name` | 绑定节点 |
| `last_refreshed_at` | UI |

## 字段展示规则

| 场景 | 规则 |
|---|---|
| 正常 | 全区块渲染 |
| handler stub | 标注 OpenAPI ≠ 已实现 |
| 异常 GPU > 0 | 红标 + 置顶 |
| aggregate 未就绪 | 排行区「待 YAML」 |
| 403 | 无权限 |

## 字段口径与单位

| 字段 | 口径 | 单位/格式 |
|---|---|---|
| `memory_mb` | YAML 原始显存容量 | MB；UI 可换算 GiB |
| `utilization_pct` | 指定 `time_window` 内平均利用率 | %，保留 1 位小数 |
| `occupancy` | `GPUOccupancySummary` 汇总口径 | count / % |
| `device_count` | inventory 设备数 | integer |

## 状态与能力口径

### GPUInventoryDevice.state

| 状态 | 展示 |
|---|---|
| `available` | 空闲 |
| `allocated` | 已分配 |
| `unavailable` | 异常 |
| `maintenance` | 维护 |

## 创建前置条件

| 依赖 | 响应 |
|---|---|
| 平台读 RBAC（**TODO-YAML**） | `403` |
| `scope:gpu-inventory:read`（引用时） | `403` |
| PromQL 非空 | `400` |
| 未认证 | `401` |

## 操作可用性矩阵

| 操作 | 只读 | SRE | 管理员 |
|---|---|---|---|
| 查看 GPU 池 | ✅ | ✅ | ✅ |
| 跳转监控/计量 | ✅ | ✅ | ✅ |
| drain / 标记维护 | ❌ | Phase 2 | Phase 2 |
| 在本页改配额 | ❌ | ❌ | → tenant-quota |

## 删除前置校验

**N/A**（本页无 DELETE GPU 设备）

## 接口冻结规则

### `GET /api/v1/gpu-inventory`

- Query：`vendor`、`state`、`limit`、`cursor`
- `success`：`200` + `GPUInventoryListResponse`
- `errors`：`401`、`403`
- 当前 YAML 未声明 BadRequest response
- BOSS：**只读参考**

### `GET /api/v1/gpu-inventory/occupancy`

- `success`：`200` + `GPUOccupancySummary`
- `errors`：`401`、`403`

### `GET /api/v1/observability/query`

- `query` required；`errors`：`400`、`401`、`403`

### 平台 GPU pool list（待补）

<!-- TODO-YAML: GET /api/v1/gpu-inventory/platform 或等价 -->

## 使用规则

- 禁止跨租户 JWT 轮询
- 不得把 PromQL 伪造为 inventory list
- OpenAPI 已声明 ≠ handler 已实现

## 待补能力边界

- 平台 GPU pool aggregate — **ADDED-TO-YAML**
- curated PromQL — **TODO-YAML**
- gpu.drain Skill — Phase 2

## 响应示例

### inventory 引用（租户 · **非 BOSS 正式**）

```json
{
  "items": [
    {
      "device_id": "gpu-001",
      "model": "NVIDIA A100",
      "state": "allocated",
      "memory_mb": 81920,
      "node_id": "node-12"
    }
  ],
  "next_cursor": null
}
```

### 平台 pool aggregate 目标（**待 YAML**）

```json
{
  "gpu_total": 512,
  "gpu_allocated": 401,
  "top_tenants": [{ "tenant_id": "t-001", "gpu_allocated": 64 }],
  "last_refreshed_at": "2026-06-17T11:00:00Z"
}
```

## 错误示例

### 无 gpu-inventory 读权限

```json
{
  "code": "FORBIDDEN",
  "message": "permission denied: scope:gpu-inventory:read required",
  "request_id": "req-boss-gpm-403-001"
}
```

### PromQL 为空

```json
{
  "code": "BAD_REQUEST",
  "message": "query is required and must not be empty",
  "request_id": "req-boss-gpm-400-001"
}
```

## 相关模块

- [`platform-resource-pool.md`](platform-resource-pool.md)、[`gpu-pool-trend.md`](../overview/gpu-pool-trend.md)
- [`gpu-monitoring.md`](../health/gpu-monitoring.md)、[`maint-skills.md`](../health/maint-skills.md)

## 回填验收标准

- [x] 满配章节齐全
- [x] gpu-inventory 已声明 vs 平台 aggregate 分层
- [x] 400 + 403 错误示例
- [ ] 平台 aggregate YAML 合入后回写
- [x] PRD/SPEC/HTML 与本文同步

## 相关阶段产物

- PRD: `tasks/modules/prd/console/gpu-inventory/prd-k8s-gpu-hami-volcano-scheduling.md`
- UX (BOSS): `tasks/modules/prd/console/gpu-inventory/ux-boss-gpu-pool.md`
- SPEC (BOSS): `tasks/modules/spec/boss/gpu-inventory/spec-boss-gpu-pool.md`
- SPEC (Core): `tasks/modules/spec/core/gpu-inventory/spec-core-gpu-scheduling.md`
