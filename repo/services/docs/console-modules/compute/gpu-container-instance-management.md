# GPU 容器实例

## 页面定位

`GPU 容器实例` 是 `Console / 算力与云资源 / 实例管理` 下的租户侧实例页面，用于帮助用户查看、创建和管理当前权限范围内的 GPU 容器实例。

当前页面必须复用统一实例资源 `kind=gpu_container`，而不是定义独立 `GPU 容器` Core 资源。

## 文档管理规则

- 本文件是 `GPU 容器实例` 的主维护源
- `prototypes/ani-services-prototype-console.html` 与 `prototypes/ani-services-prototype.html` 只保留页面摘要、模块边界和详细材料入口
- `tasks/modules/prd/console/compute/prd-console-gpu-container-instance-management.md` 与 `tasks/modules/spec/console/compute/spec-console-gpu-container-instance-management.md` 作为阶段性产物保留，不替代本文件

## Core 层要求

- GPU 容器实例能力属于 `Core / Instances`
- 当前模块不拥有独立 `/api/v1/gpu-containers*` 路径
- 查询与写操作必须使用 `/api/v1/instances*`
- GPU 容器实例通过 `kind=gpu_container` 区分
- 页面不要求前端显式传 `tenant_id`
- 写操作必须按现有 schema 提供 `idempotency_key`
- 错误响应格式统一为 `{"code":"UPPER_SNAKE","message":"...","request_id":"..."}`
- 当前模块不把 `logs / events / metrics / exec / dashboard / revisions` 写成已冻结能力

## 页面职责

- 展示 GPU 容器实例分页列表
- 展示单个 GPU 容器实例详情
- 提供创建入口
- 提供基础生命周期动作入口
- 提供操作历史与操作详情入口

## 页面区块与数据来源映射

| 区块 | 主要来源层 | 数据口径说明 | 跳转目标 |
|---|---|---|---|
| 实例列表 | Core | `GET /api/v1/instances?kind=gpu_container` | 实例详情 |
| 实例详情 | Core | `GET /api/v1/instances/{instance_id}` | 操作历史 |
| 创建实例 | Core | `POST /api/v1/instances` | 创建结果 |
| 生命周期动作 | Core | `POST /api/v1/instances/{instance_id}/lifecycle` | 操作详情 |
| 操作历史 | Core | `GET /api/v1/instances/{instance_id}/operations` | 单次操作详情 |
| 边界说明 | 规划项 | 监控、日志、exec、Dashboard 不属于当前冻结能力 | 后续模块 |

## 字段级定义

### 统一实例字段

| 字段 | 说明 | 展示建议 |
|---|---|---|
| id | 实例 ID | 文本 |
| name | 实例名称 | 文本 |
| state | 实例状态 | 标签 |
| image | 镜像 | 文本 |
| cpu | CPU 规格 | 文本 |
| memory | 内存规格 | 文本 |
| created_at | 创建时间 | 时间 |
| updated_at | 更新时间 | 时间 |

### 容器摘要字段

| 字段 | 说明 | 展示建议 |
|---|---|---|
| container.replicas | 副本数 | 数字 |
| container.ready_replicas | 就绪副本数 | 数字 |
| container.revision | 当前修订版本 | 文本 |
| container.rollout_status | 发布状态 | 标签 |
| container.history | 历史修订摘要 | 详情中的只读区 |

### GPU 摘要字段

| 字段 | 说明 | 展示建议 | 状态 |
|---|---|---|---|
| gpu.vendor | GPU 厂商 | 文本 | ✅ 已冻结 |
| gpu.model | GPU 型号 | 文本 | ✅ 已冻结 |
| gpu.count | GPU 数量 | 数字 | ✅ 已冻结 |
| gpu.queue_name | 调度队列名 | 文本（可跳转队列设置页） | P0 待补 |
| gpu.resource_name | 调度资源名 | `nvidia.com/gpu` / `nvidia.com/vgpu` | P0 待补 |
| gpu.scheduling_reason | 调度说明 / 失败原因 | 文本（失败时 Alert 高亮） | P0 待补 |
| gpu.utilization_percent | GPU 利用率摘要 | 百分比 | 待补 |

## 创建前置条件

| 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
|---|---|---|
| 用户登录与租户上下文 | 已认证 | `401` / `403` |
| `name` | 租户内唯一 | `409 CONFLICT` |
| `image` | 镜像引用有效 | `422 PRECONDITION_FAILED`（YAML 已举例 `IMAGE_NOT_FOUND`） |
| GPU 资源（若指定） | 调度可满足 | `422 InsufficientGPU`（具体 `code` 待 Core P0-① 冻结） |
| 调度队列（若指定 `queue_name`） | 队列存在且属于该租户 | `422 QueueNotFound`（待 Core P0-① 冻结） |
| GPU 节点兼容性 | 存在兼容 GPU 节点 | `422 GPUNodeIncompatible`（待 Core P0-① 冻结） |

> **架构说明**：GPU 容器实例统一走 Core `POST /api/v1/instances`（`kind=gpu_container`）。`services/v1.yaml` 中 `/gpu-containers*` 已废弃，不得在新代码中使用。
>
> **P0 调度字段**：创建请求可传 `queue_name`（指定调度队列）和 GPU 分配模式（整卡 `nvidia.com/gpu` / vGPU `nvidia.com/vgpu`）。Core `PlanScheduling()` 解析队列并输出 `GPUSchedulingDecision`，见 SPEC `spec-core-gpu-scheduling.md` §5.1。

## 操作可用性矩阵

| 操作 | pending | provisioning | starting | running | stopping | stopped | failed | deleting |
|---|---|---|---|---|---|---|---|---|
| 启动 | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ | ✅ | ❌ |
| 停止 | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ |
| 重启 | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ |
| 删除 | ✅ | ⚠️ | ⚠️ | ⚠️ 需先停 | ❌ | ✅ | ✅ | ❌ |
| 回滚 (`rollback`) | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ |

状态不允许时返回 `422 PRECONDITION_FAILED`（YAML 已举例 `INSTANCE_STATE_INVALID`）。

## 已冻结的 Core 能力

- `GET /api/v1/instances?kind=gpu_container`
- `GET /api/v1/instances/{instance_id}`
- `POST /api/v1/instances`
- `POST /api/v1/instances/{instance_id}/lifecycle`
- `GET /api/v1/instances/{instance_id}/operations`
- `GET /api/v1/instance-operations/{operation_id}`

## 创建规则

### `POST /api/v1/instances`

| 项 | 值 |
|---|---|
| operationId | `createInstance` |
| summary | `创建实例` |
| requestBody | `CreateInstanceRequest` |
| 必填关键字段 | `idempotency_key`、`name`、`kind=gpu_container` |
| 常用字段 | `image`、`cpu`、`memory`、`replicas`、`gpu.vendor`、`gpu.model`、`gpu.count` |
| success | `201 + CreateInstanceResponse` |
| error responses | `400 BAD_REQUEST`、`401 UNAUTHORIZED`、`403 FORBIDDEN`、`409 CONFLICT` |

## 生命周期规则

### `POST /api/v1/instances/{instance_id}/lifecycle`

| 项 | 值 |
|---|---|
| operationId | `applyInstanceLifecycle` |
| summary | `执行实例生命周期操作` |
| requestBody | `InstanceLifecycleRequest` |
| 当前页面承接动作 | `start`、`stop`、`restart`、`delete`、`rollback` |
| success | `200 + InstanceLifecycleResponse` |
| error responses | `400 BAD_REQUEST`、`401 UNAUTHORIZED`、`403 FORBIDDEN`、`404 NOT_FOUND`、`409 CONFLICT` |

## 待补 Core 契约能力

<!-- ADDED-TO-YAML: GET /api/v1/instances/{instance_id}/metrics (Core v1.yaml, Phase 2 2026-06-17) -->
- GPU 容器日志 <!-- ADDED-TO-YAML: GET /api/v1/instances/{instance_id}/logs (Core v1.yaml, Phase 2 2026-06-17) -->
- GPU 容器事件 <!-- ADDED-TO-YAML: GET /api/v1/instances/{instance_id}/events (Core v1.yaml, Phase 2 2026-06-17) -->
- GPU 容器监控
- GPU 容器终端 / exec <!-- ADDED-TO-YAML: POST /api/v1/instances/{instance_id}/exec (Core v1.yaml, Phase 2 2026-06-17) -->
- GPU 资源总览 Dashboard
- Revision 独立资源

## 验收标准

- 正文路径、字段和返回码与现有 `Core v1.yaml` 一致
- 不再把旧 `/gpu-containers*` 路径写成正式契约
- HTML 摘要、PRD、SPEC 与本文件一致
- 本文件可以独立作为 `Console / GPU 容器实例` 的主维护材料

## 产品经理补充定义

### 目标用户与权限视角

- 租户管理员：查看、创建并操作 `kind=gpu_container` 的统一实例
- 业务成员：查看自身 GPU 容器实例的发布与 GPU 摘要，并在授权前提下执行基础动作
- 只读用户：仅查看实例、GPU 摘要与操作历史

### 默认视图与页面状态

- 首屏默认展示实例列表、GPU 摘要字段和发布状态，不另外创建 GPU 容器专属资源域
- 当租户暂无 GPU 容器实例时，页面展示空态并保留去普通容器或 GPU 资源页的入口
- 当 GPU 摘要缺失时，仅该字段显示“暂不可用”，不推导为 0
- 当创建或生命周期动作提交成功后，页面必须引导用户查看实例详情和操作结果

### 核心任务流

1. 用户在列表中按 GPU 厂商、型号或状态筛选目标实例并进入详情
2. 用户创建 GPU 容器实例后，页面跳到详情查看 `container` 与 `gpu` 摘要字段
3. 用户完成生命周期动作后，通过操作历史和 GPU 资源变化确认结果

### 跨模块协同

- 与 `GPU 算力管理` 联动，用于解释实例对 GPU 的占用关系
- 与 `容器实例` 共享统一实例的基础结构，但保持 `kind=gpu_container` 的页面口径
- 未冻结的 GPU 监控、日志、事件、exec、Dashboard 仅作为待补边界保留

### 产品验收补充

- 页面必须能区分 GPU 容器与普通容器，不通过额外路径而通过字段与筛选表达差异
- GPU 摘要字段缺失时必须给出可读文案，不得伪造默认值
- 动作和错误反馈必须沿用统一实例的返回结构与 `request_id`
- 本页不得继续引用任何 `/gpu-containers*` 风格路径
