# GPU 算力管理

## 页面定位

`GPU 算力管理` 是 `Console / 算力与云资源` 下的租户侧资源观察与排障页面，用于定义 GPU 资源总览、占用关系、异常定位和跨模块回跳关系。

本页是 `Console` 页面，不是 `BOSS` 的平台 GPU 运营页。

## 文档管理规则

- 本文件是 `GPU 算力管理` 的主维护源
- `prototypes/ani-services-prototype-console.html` 只保留页面摘要、模块边界和详细材料入口
- `tasks/modules/prd/console/compute/prd-console-gpu-management.md` 与 `tasks/modules/spec/console/compute/spec-console-gpu-management.md` 用于追溯本轮设计过程，不替代本文件
- 若正文与辅助材料冲突，以本文件为准，并回修辅助材料

## Core 层要求

- GPU 资源域归 `Core`
- 不允许把 GPU 资源对象写入 `Services /api/v1/svc/*`
- 不允许继续使用旧的 `/api/v1/console/*` 路径
- 不要求前端显式传 `tenant_id` 或 `X-Tenant-Id`
- 统一错误结构口径为 `{"code":"UPPER_SNAKE","message":"...","request_id":"..."}`
- 在 `Core` 未冻结独立 GPU 接口前，正文不得自造路径、schema、operationId、返回码

## 权威源结论

当前一级权威源以 `ANI-main/repo/api/openapi/v1.yaml` 为准，可确认事实如下：

- `GET /api/v1/gpu-inventory`（`listGPUInventory`）— ✅ 已冻结
- `GET /api/v1/gpu-inventory/occupancy`（`getGPUOccupancy`）— ✅ 已冻结
- `GET /api/v1/observability/query`（`queryObservability`）— ✅ 已冻结（DCGM 利用率 PromQL 代理）
- `POST /api/v1/instances`（`kind=gpu_container`）— ✅ 已冻结
- `InstanceRecord.gpu`（`vendor`/`model`/`count`）— ✅ 已冻结
- **P0 待补**：`GET/POST/GET/PATCH/DELETE /api/v1/gpu-scheduling/queues` — 待 Core P0-① 冻结
- **P0 待补**：`scope:gpu-scheduling:read` / `scope:gpu-scheduling:write` — 待 Core P0-① 冻结
- **P0 待补**：`InstanceRecord.gpu.queue_name` / `resource_name` / `scheduling_reason` — 待 Core P0-① 冻结
- **P0 待补**：DCGM PromQL 模板 `avg(DCGM_FI_DEV_GPU_UTIL{job="dcgm-exporter"})` — 待 Core P0-④ lab 验证

结论：

- GPU 清单/占用 API 已冻结，页面可正式对接
- 队列 CRUD API 为 P0 待补，先改 `v1.yaml` 再实现（SPEC `spec-core-gpu-scheduling.md` §4.1）
- 不存在 `GpuOverviewResponse` 等自造 schema，一律使用已冻结的 `GPUInventoryListResponse` / `GPUOccupancyStats`

## 页面职责

- 定义 `GPU 总览`、`型号分布`、`节点`、`设备`、`占用分布` 的页面结构
- 帮助用户理解 GPU 资源与 `VM`、`容器实例`、`GPU 容器实例` 的关系
- 提供异常定位入口和跨模块跳转路径
- 为后续 `Core` 冻结独立 GPU 接口提供稳定的产品基线

## 页面结构

```text
GPU 算力管理（路由 /compute/gpu）
├── 资源总览 KPI
│   ├── GPU 总量
│   ├── 已分配
│   ├── 空闲
│   ├── 平均利用率（DCGM PromQL 代理）
│   └── 异常设备数
├── GPU 型号分布
├── Tabs
│   ├── 节点（聚合 inventory by node）
│   ├── 设备（明细 GPUInventoryRecord）
│   └── 占用分布
├── 资源动作（待补）
│   ├── 分配 GPU
│   └── 回收 / 释放 GPU
└── 调度队列设置（跳转 /settings/gpu-queues）
```

**关联页面：**
- `GPU 容器实例`（`/compute/gpu-containers`）— 创建 GPU 容器 + 选择调度队列
- `调度队列设置`（`/settings/gpu-queues`）— 租户管理员 CRUD 队列
- `概览页 GPU 卡`（`/`）— 复用 occupancy 数据

## 数据来源与分层约束

### 当前已冻结的权威源

| 类型 | 来源 | 当前用途 |
|---|---|---|
| Core 路径 | `GET /api/v1/gpu-inventory`（`listGPUInventory`） | 设备列表 + 型号/节点筛选 |
| Core 路径 | `GET /api/v1/gpu-inventory/occupancy`（`getGPUOccupancy`） | KPI 总量/已分配/空闲/异常 |
| Core 路径 | `GET /api/v1/observability/query`（`queryObservability`） | DCGM 平均利用率 PromQL 代理 |
| Core schema | `GPUInventoryRecord` | 设备明细字段 |
| Core schema | `GPUOccupancyStats` | KPI + `by_gpu_type` 型号分布 |
| Core schema | `InstanceRecord.gpu` | GPU 容器摘要（vendor/model/count） |
| Core schema | `InstanceRecord.state_reason` | 实例创建失败原因（如 `InsufficientGPU`） |

### P0 待补能力

| 能力 | 当前状态 | 文档写法 |
|---|---|---|
| 队列 CRUD API | 待 Core P0-① 冻结 | 写「P0 待补」+ 引用 SPEC `spec-core-gpu-scheduling.md` §4 |
| `InstanceRecord.gpu.queue_name` | 待 Core P0-① 冻结 | 写「P0 待补」 |
| DCGM PromQL 模板 | 待 Core P0-④ lab 验证 | 写「P0 待补」 |
| GPU 分配 / 回收独立动作 | 未冻结 | 只写待补边界 |

### 关键边界

- 本页可以定义 GPU 页面如何展示，但不能反向证明对应 API 已经存在
- 本页允许跳转到实例类模块，但不重写实例资源契约
- 本页允许描述监控需求，但不把 `observability` 路径误写成 GPU inventory 正式接口

## 页面区块与数据口径

| 区块 | 页面目标 | 当前接口状态 | 跳转目标 |
|---|---|---|---|
| 资源总览 | 快速判断当前租户 GPU 是否紧张或异常 | 待 Core 补齐 | 节点列表 / 设备列表 |
| GPU 型号分布 | 理解厂商和型号层面的资源可用性 | 待 Core 补齐 | 节点列表 / 设备列表 |
| GPU 节点列表 | 查看节点级容量和状态 | 待 Core 补齐 | 节点详情 |
| GPU 设备列表 | 定位单卡异常和占用对象 | 待 Core 补齐 | 设备详情 / 占用对象详情 |
| 租户内占用分布 | 查看当前租户内部谁在占用 GPU | 待 Core 补齐 | `VM` / `容器实例` / `GPU 容器实例` |
| 资源动作 | 表达未来分配与回收入口位 | 待 Core 补齐 | 关联资源操作或后续正式动作 |

## 字段级定义

以下字段是页面表达目标，不等于当前已经存在同名冻结 schema。

### 资源总览

| 字段 | 说明 | 展示建议 |
|---|---|---|
| gpu_total | 当前租户可见 GPU 总量 | 整数或卡数 |
| gpu_allocated | 当前已分配 GPU 数量 | 显示已分配 / 总量 |
| gpu_idle | 当前空闲 GPU 数量 | 整数 |
| avg_utilization | 约定时间窗口内的平均利用率 | 百分比 |
| abnormal_device_count | 异常设备数 | 大于 0 时高亮 |
| refreshed_at | 数据更新时间 | 显示时间戳或“最近刷新” |

### 型号、节点和设备

| 字段 | 说明 | 展示建议 |
|---|---|---|
| vendor | 厂商 | NVIDIA / Ascend / Hygon 等 |
| model | 型号 | A100 / A10 / 910B 等 |
| node_name | 节点名 | 文本 |
| gpu_count | 节点 GPU 数量 | 整数 |
| device_id | 设备主标识 | 文本 |
| status | 资源状态 | `healthy / warning / unavailable / unknown` |
| alloc_state | 占用状态 | `allocated / free / releasing / unknown` |
| owner_type | 占用对象类型 | `instance / workload / job` |
| owner_id | 占用对象标识 | 文本；支持跳转 |
| utilization | GPU 利用率 | 百分比 |
| memory_used_ratio | 显存使用比例 | 百分比 |

### 占用分布和动作

| 字段 | 说明 | 展示建议 |
|---|---|---|
| scope_type | 占用维度 | `project / namespace / workload / instance` |
| scope_id | 占用对象标识 | 文本 |
| scope_name | 占用对象名称 | 文本 |
| gpu_allocated | 当前维度占用 GPU 数量 | 整数 |
| action_state | 动作区可用状态 | `待补` / `已冻结`，当前仅允许 `待补` |

## 字段展示规则

| 场景 | 展示规则 | 说明 |
|---|---|---|
| 正常 | 展示指标、更新时间和跳转入口 | 页面目标状态 |
| 暂无数据 | 展示空态，不伪造 `0 正常` | 适用于当前无资源 |
| 监控缺失 | 标记 `数据暂不可用` | 与真实值为 `0` 区分 |
| 能力未冻结 | 明示 `待 Core 补齐` | 禁止伪造接口与结果 |
| 资源异常 | 高亮异常节点、设备或占用关系 | 便于排障 |

## 字段口径与单位

| 字段 | 口径建议 | 单位/格式 |
|---|---|---|
| gpu_total / gpu_allocated / gpu_idle | 以当前租户可见资源为准，不跨租户汇总 | 整数 |
| avg_utilization / utilization | 表达为时间窗口聚合值，不写成逐秒实时值 | 百分比 |
| memory_used_ratio | 已用显存 / 总显存 | 百分比 |
| refreshed_at | 数据更新时间，不等于页面打开时间 | ISO 时间或格式化时间 |

## 状态与能力口径

| 类型 | 当前口径 |
|---|---|
| 资源归属 | 已冻结，归 `Core` |
| 页面结构 | 已冻结，可作为产品定义维护 |
| 独立 API 路径 | 未冻结 |
| 独立 schema | 未冻结 |
| 资源动作 | 未冻结 |

## 创建前置条件

| 项 | 说明 |
|---|---|
| 本页定位 | 页面定义阶段，**无**独立 GPU 资源 POST 路径 |
| 关联创建 | GPU 容器实例创建见 `gpu-container-instance-management.md`（`kind=gpu_container`） |
| GPU 清单 | `GET /api/v1/gpu-inventory` — 详文 `gpu-inventory-ui.md`（YAML 已声明；handler stub） |

### 页面访问

- 用户已登录并进入租户上下文
- 平台后续如需展示独立 GPU 资源视图，必须先由 `Core` 冻结接口
- 页面若引用实例或监控信息，只能引用当前已存在的正式 `Core` 路径

## 操作可用性矩阵

| 操作 | 当前状态 | 说明 |
|---|---|---|
| 查看页面结构和区块定义 | 可用 | 当前文档已收口 |
| 查看 GPU 总览 KPI | ✅ 已冻结 | `GET /gpu-inventory/occupancy`（`GPUOccupancyStats`） |
| 查看设备明细 | ✅ 已冻结 | `GET /gpu-inventory`（`GPUInventoryRecord`） |
| 查看 GPU 型号分布 | ✅ 已冻结 | `GPUOccupancyStats.by_gpu_type` |
| 查看 DCGM 平均利用率 | ✅ 已冻结 | `GET /observability/query`（PromQL 代理） |
| 查看调度队列列表 | P0 待补 | `GET /gpu-scheduling/queues`（待 Core P0-①） |
| 创建/编辑/删除调度队列 | P0 待补 | `/gpu-scheduling/queues` CRUD（待 Core P0-①） |
| 提交 GPU 分配 / 回收动作 | 待补 | 需 `Core` 冻结独立写接口 |

## 接口冻结规则

### 当前正式结论

- ✅ `GET /api/v1/gpu-inventory`（`listGPUInventory`）— 已冻结
- ✅ `GET /api/v1/gpu-inventory/occupancy`（`getGPUOccupancy`）— 已冻结
- ✅ `GET /api/v1/observability/query`（`queryObservability`）— 已冻结
- ✅ `POST /api/v1/instances`（`kind=gpu_container`）— 已冻结
- **P0 待补**：`/api/v1/gpu-scheduling/queues` CRUD 5 端点 — 待 Core P0-① 冻结
- 不得使用 `GpuOverviewResponse`、`GpuDistributionResponse` 等自造 schema
- 不得使用 `/api/v1/gpu-resources/*`、`/api/v1/gpu-allocations*`、`/api/v1/console/*`

### 可安全引用的关联能力

| 类型 | 路径或字段 | 说明 |
|---|---|---|
| GPU 清单只读 | `GET /api/v1/gpu-inventory` | YAML 已声明（`listGPUInventory`）；**不等于** GPU 页面独立 CRUD <!-- ADDED-TO-YAML: GET /api/v1/gpu-inventory (Core v1.yaml, Phase 2 2026-06-17) --> |
| 关联实例列表 | `/api/v1/instances` | 用于跳转到占用对象（含 `kind=gpu_container`） |
| 关联实例详情 | `/api/v1/instances/{instance_id}` | 用于查看具体实例状态与 `InstanceRecord.gpu` 摘要 |
| 监控联动参考 | `/api/v1/observability/query` | 仅作后续监控联动参考 |

## 响应与错误示例

### 错误结构口径示例

```json
{
  "code": "SERVICE_UNAVAILABLE",
  "message": "gpu metrics are temporarily unavailable",
  "request_id": "req-gpu-001"
}
```

说明：

- 该示例只用于统一错误口径表达
- 不代表当前 GPU 页面已存在正式调用接口

## 回填前置依赖

- 若要把总览、分布、节点、设备、占用分布写成正式接口，必须先在 `ANI-main/repo/api/openapi/v1.yaml` 中补齐路径和 schema
- 若要把分配或回收写成正式动作，必须先冻结成功返回码、错误码、请求字段和幂等要求
- 在上述权威源补齐前，HTML、PRD、SPEC 和本文件都只能把这些能力写成 `待补`

## 待确认项

- `Core` 后续是否会冻结独立 GPU inventory 路径
- 租户是否允许查看温度、功耗等低层设备指标
- GPU 分配/回收是独立资源动作，还是通过实例资源间接承接

## 当前阶段产物

- PRD: `tasks/modules/prd/console/gpu-inventory/prd-k8s-gpu-hami-volcano-scheduling.md`
- UX: `tasks/modules/prd/console/gpu-inventory/ux-console-gpu-scheduling.md`
- SPEC (Console): `tasks/modules/spec/console/gpu-inventory/spec-console-gpu-scheduling.md`
- SPEC (Core): `tasks/modules/spec/core/gpu-inventory/spec-core-gpu-scheduling.md`
- SPEC (BOSS): `tasks/modules/spec/boss/gpu-inventory/spec-boss-gpu-pool.md`
- 上述文件用于追溯本轮设计过程；后续以本文件为主维护源

## 回填验收标准

- HTML 只保留摘要、边界和材料入口
- 本文件可以独立回答页面定位、字段口径、待补边界和 Core 归属
- 正文中不再出现伪造的 GPU 独立路径、schema、operationId 或返回码
- 不出现 `X-Tenant-Id` 必填、旧 `/api/v1/console/*` 路径或平台运营口径

## 非目标与边界

- 不做 BOSS 的全平台 GPU 运营总览
- 不定义新的 Services GPU 资源契约
- 不定义底层调度算法、设备插件和平台资源池实现
- 不把未来 GPU API 设计误写成当前正式契约
