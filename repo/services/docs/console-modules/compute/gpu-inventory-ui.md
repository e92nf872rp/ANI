# GPU 算力 — 设备清单页

## 页面定位

GPU 算力管理下的**设备清单与占用**明细页（非独立 GPU CRUD 资源域）。

与首页 GPU 利用率区块共享数据源，本页提供完整列表与筛选。

## 文档管理规则

- 本文是 GPU 清单子页的主维护源
- 一级权威源：`ANI-main/repo/api/openapi/v1.yaml`
- Handler 实现契约：`tasks/execution/CORE-HANDLER-IMPLEMENTATION-GUIDE.md` §TASK-CORE-003

## Core 层要求

| 方法 | 路径 | operationId | 状态 |
|---|---|---|---|
| GET | `/api/v1/gpu-inventory` | `listGPUInventory` | ✅ 已冻结 |
| GET | `/api/v1/gpu-inventory/occupancy` | `getGPUOccupancy` | ✅ 已冻结 |
| GET | `/api/v1/observability/query` | `queryObservability` | ✅ 已冻结（DCGM 利用率） |

Query（list）：`gpu_type`、`status`（available/in_use/fault/maintenance）、`node_name`、`limit`、`cursor`。

Schema：`GPUInventoryRecord`、`GPUInventoryListResponse`、`GPUOccupancyStats`。

RBAC：`scope:gpu-inventory:read`。

**P0 路由变更**：本页从独立路由 `/gpu-inventory` 重构为 `/compute/gpu` 的 Tabs 子页（设备 Tab），见 `gpu-management.md`。

**occupancy 平台 scope**：`getGPUOccupancy` 支持平台角色 JWT 读取全域（BOSS 复用），租户用户读取租户域。

## 创建前置条件

| 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
|---|---|---|
| 用户登录 | 已认证 | `401 UNAUTHORIZED` |
| GPU 清单读权限 | `scope:gpu-inventory:read` | `403 FORBIDDEN` |

本页无 POST 创建接口。

## 操作可用性矩阵

| 操作 | 只读用户 | 运维角色 | 说明 |
|---|---|---|---|
| 查看设备清单 | 可用 | 可用 | `GET /gpu-inventory` |
| 查看占用摘要 | 可用 | 可用 | `GET /gpu-inventory/occupancy` |
| GPU 分配/回收 | 不可用 | 不可用 | YAML 未声明写路径 |

## 页面职责

- 设备列表 + 占用摘要；跳转 `kind=gpu_container` 实例
- **无** GPU 独立 create/delete API

## 接口冻结规则

### `GET /api/v1/gpu-inventory`

- 成功：`200 + GPUInventoryListResponse`
- 错误：`401`、`403`
- 租户边界：仅返回当前租户可见设备

### `GET /api/v1/gpu-inventory/occupancy`

- 成功：`200 + GPUOccupancySummary`
- 错误：`401`、`403`

## 待补边界

- GPU 单设备 metrics 独立路径 — YAML 未声明（勿写 `gpu-inventory/{id}/metrics` 为冻结契约，除非 Phase 3+ 扩 YAML）
- GPU 分配/回收写操作 — 待 Core 资源域规划

## 相关模块

- `gpu-management.md`（总览边界）
- `home/home-gpu-utilization.md`（首页摘要）
- TASK：`TASK-CORE-003`

## 验收标准

- [x] 路径与 Core v1.yaml 一致（`listGPUInventory` / `getGPUOccupancy`）
- [x] 不使用 deprecated Services GPU 路径
- [x] 正文不把 handler stub 写成已实现
- [x] P0 路由重构到 `/compute/gpu` Tabs 子页
- [x] occupancy 平台 scope 支持 BOSS 复用

## 相关阶段产物

- PRD: `tasks/modules/prd/console/gpu-inventory/prd-k8s-gpu-hami-volcano-scheduling.md`
- UX: `tasks/modules/prd/console/gpu-inventory/ux-console-gpu-scheduling.md`
- SPEC (Console): `tasks/modules/spec/console/gpu-inventory/spec-console-gpu-scheduling.md`
- SPEC (Core): `tasks/modules/spec/core/gpu-inventory/spec-core-gpu-scheduling.md`
