# 容器实例

## 页面定位

`容器实例` 是 `Console / 算力与云资源 / 实例管理` 下的租户侧资源管理页面，用于帮助用户查看、创建、操作和排查当前权限范围内的容器实例。

本页是 `Console` 页面，不是 `BOSS` 的平台容器集群运营页。

## 文档管理规则

- 本文件是 `容器实例` 的主维护源
- `prototypes/ani-services-prototype-console.html` 只保留页面摘要、模块边界和详细材料入口
- `tasks/modules/prd/console/compute/prd-console-container-instance-management.md` 和 `tasks/modules/spec/console/compute/spec-console-container-instance-management.md` 作为阶段性产物保留，不替代本文件
- 后续如容器实例页面定义发生变化，优先修改本文件，再同步回看 HTML 摘要

## Core 层要求

- `容器实例` 资源对象、生命周期、操作历史都属于 `Core`
- 查询与动作接口必须使用 `/api/v1/*`
- 当前容器实例不拥有独立 `/containers` 路径，而是复用 `/api/v1/instances*` 并通过 `kind=container` 区分
- 不允许把容器实例资源对象写入 `Services /api/v1/svc/*`
- 不允许继续使用旧的 `/api/v1/console/*`、旧的 `/api/v1/storage/*` 或 K8s 原生 API 作为本页正式契约
- 不要求前端显式传 `tenant_id` 或 `X-Tenant-Id`
- 租户边界必须从认证上下文或后端中间件获取
- 创建和生命周期动作必须带 `idempotency_key`
- 新增接口说明必须具备 `operationId`、中文 `summary`、`tags`、`security`、完整 `responses`
- 错误响应格式统一为 `{"code":"UPPER_SNAKE","message":"...","request_id":"..."}`
- 容器实例模块必须严格对齐现有 `Core v1.yaml` 已冻结的返回码：创建 `201`，生命周期动作 `200`
- 观测与终端能力详见 `container-observability.md`（Phase 2 YAML 已声明 logs/events/metrics/exec）
- 当前模块不把 `dashboard`、K8s 工作负载平台 IA 写成已冻结 Core 能力

## 页面职责

- 展示当前租户下的容器实例列表与基础统计
- 展示单个容器实例的详情信息
- 提供创建容器实例入口
- 提供基础生命周期动作入口
- 提供操作历史和失败排障入口
- 明确标出容器平台其他能力的待补边界

## 页面结构

```text
容器实例
├── 顶部筛选区
│   ├── 状态
│   ├── 创建时间
│   ├── Provider
│   └── 关键字
├── 统计摘要
│   ├── 全部
│   ├── 运行中
│   ├── 发布中
│   ├── 异常
│   └── 最近有操作
├── 容器实例列表
│   ├── 基础信息列
│   ├── 规格与镜像信息
│   ├── 副本与发布状态
│   └── 操作入口
├── 容器实例详情
│   ├── 概览
│   ├── 发布状态与历史
│   ├── 关联资源摘要
│   ├── Workload Identity 摘要
│   └── 操作历史
└── 动作入口
    ├── 创建容器实例
    └── 生命周期动作
```

## 交互与使用规则

- 页面默认展示当前租户、当前权限范围内的容器实例，不展示平台全量容器集群视角
- 列表页优先承担查找和筛选职责，详情页承担排障和发布状态查看职责
- 当前页面只承接统一实例中的 `container` 能力，不承接完整容器平台工作台
- 对于工作负载、服务发现、配置管理、存储、弹性伸缩、应用市场、微服务治理等能力，本页只展示待补边界或后续模块方向
- 生命周期危险动作必须结合状态判断和确认提示
- 当前页面不提供 VM 专属 Console/VNC，会话和容器 exec/terminal 也不属于本轮已冻结能力

## 数据来源与分层约束

### 数据来源划分

- `Core` 数据
  - 容器实例列表
  - 容器实例详情
  - 容器实例创建
  - 容器实例生命周期动作
  - 容器实例操作历史与操作详情
- `Services` 数据
  - 本页不承载容器实例资源对象，不定义新的 Services 容器实例资源契约

### 关键边界

- 本页只承接 `Core v1.yaml` 已冻结的统一实例资源对象和 `kind=container` 查询/写操作
- 本页可以展示卷、`resource_refs`、`workload_identity`、发布历史等与容器实例直接相关的摘要
- 本页不重新定义命名空间、Deployment、StatefulSet、DaemonSet、Job、CronJob、Service、Ingress、ConfigMap、Secret、PVC、PV、StorageClass、卷快照等底层资源契约
- 本页不把 `logs / events / metrics / terminal / exec / dashboard` 写成当前正式接口
- 如后续需要生成 `Core v1.yaml` 草稿，应从本文件的接口冻结规则继续下沉，而不是回到旧 HTML 里重新整理

## 页面区块与数据来源映射

| 区块 | 主要来源层 | 数据口径说明 | 跳转目标 |
|---|---|---|---|
| 统计摘要 | Core | 基于当前租户可见的 `kind=container` 实例聚合 | 容器实例列表 |
| 容器实例列表 | Core | 展示当前租户权限范围内的容器实例 | 容器实例详情 |
| 容器实例详情 | Core | 对齐 `InstanceRecord`，展示容器实例自身与直接关联摘要 | 操作历史 / 关联资源模块 |
| 创建容器实例 | Core | 使用现有 `POST /api/v1/instances` 契约，`kind=container` | 创建抽屉 |
| 生命周期动作 | Core | 使用 `POST /api/v1/instances/{instance_id}/lifecycle` | 动作确认弹窗 |
| 操作历史 | Core | 展示操作类型、状态、失败原因和时间线 | 操作详情 |
| 待补能力说明 | 规划项 | 展示未进入 `Core v1.yaml` 的容器平台能力边界 | 后续模块或待补说明 |

## 模块区块详细说明

### 统计摘要

- 展示重点：全部、运行中、发布中、异常、最近有操作
- 主要来源层：Core
- 展示口径：当前租户下 `kind=container` 的实例聚合
- 异常/空态：无容器实例时展示空态，不伪造统计值
- 跳转目标：容器实例列表

### 容器实例列表

- 展示重点：名称、状态、镜像、CPU、内存、副本、就绪副本、Provider、创建时间、操作入口
- 主要来源层：Core
- 展示口径：列表字段来自 `InstanceRecord`
- 异常/空态：若接口失败，列表区单独报错，不影响页面框架
- 跳转目标：容器实例详情

### 容器实例详情

- 展示重点：基础信息、状态原因、访问地址、副本、修订版本、rollout 状态、发布历史、卷摘要、关联资源、workload identity、更新时间
- 主要来源层：Core
- 展示口径：只展示当前租户可见的实例详情
- 异常/空态：找不到实例时展示 `NOT_FOUND`
- 跳转目标：操作历史 / 对应资源模块

### 创建容器实例

- 展示重点：名称、镜像、CPU、内存、副本、自动启动
- 主要来源层：Core
- 展示口径：对齐 `CreateInstanceRequest`，其中 `kind=container`
- 请求约束：前端内部生成并随请求携带 `idempotency_key`，但不作为用户可见展示字段
- 能力边界：当前只创建统一实例中的容器实例，不表示创建完整 K8s 工作负载编排资源
- 异常/空态：提交失败时必须展示 `request_id`
- 跳转目标：创建结果对应的实例详情

### 生命周期动作

- 展示重点：`start`、`stop`、`restart`、`delete`、`rollback`
- 主要来源层：Core
- 展示口径：对齐 `InstanceLifecycleRequest`
- 约束说明：`rollback` 通过 `revision` 指定目标修订版本
- 能力边界：当前不把 `snapshot`、`attach_volume`、`detach_volume`、`exec`、`terminal` 写成容器实例页面主动作
- 异常/空态：冲突和状态不允许需明确提示
- 跳转目标：操作历史 / 操作详情

### 操作历史

- 展示重点：操作类型、状态、触发人、失败原因、时间线步骤
- 主要来源层：Core
- 展示口径：对齐 `InstanceOperation`
- 异常/空态：无记录时展示空态
- 跳转目标：单个操作详情

### 待补能力说明

- 展示重点：`logs`、`events`、`metrics`、`terminal / exec`、`dashboard`、工作负载管理、服务发现、配置管理、存储管理、弹性伸缩、DevOps、应用市场、微服务治理
- 主要来源层：规划项
- 展示口径：当前仅保留规划说明，不形成正式接口契约
- 异常/空态：若后端无能力，入口保持禁用或仅展示说明
- 跳转目标：后续 `Core v1.yaml` 扩充或其他模块

## 字段级定义

### 列表与详情基础字段

| 字段 | 说明 | 展示建议 |
|---|---|---|
| id | 实例 ID | 文本；详情页保留复制能力 |
| name | 实例名称 | 文本；列表可点击 |
| kind | 实例类型 | 本页固定展示 `container` |
| state | 实例状态 | 标签展示 |
| state_reason | 机器可读状态原因码 | 详情页展示 |
| state_message | 人类可读状态描述 | 详情页展示 |
| provider | 实例 Provider | 文本 |
| endpoint | 实例访问地址 | 文本或链接 |
| node_name | 实例所在节点 | 文本；如有返回可展示 |
| created_at | 创建时间 | 时间 |
| updated_at | 更新时间 | 时间 |

### 容器与关联摘要字段

| 字段 | 说明 | 展示建议 |
|---|---|---|
| image | 运行镜像 | 文本 |
| cpu | CPU 规格 | 文本 |
| memory | 内存规格 | 文本 |
| container.replicas | 目标副本数 | 数字 |
| container.ready_replicas | 就绪副本数 | 数字 |
| container.revision | 当前修订版本 | 文本 |
| container.rollout_status | 发布状态 | 标签 |
| container.history[].revision | 历史修订版本 | 时间线 / 表格 |
| container.history[].image | 历史镜像 | 文本 |
| resource_refs | 关联资源引用 | 列表摘要 |
| workload_identity.key_prefix | 绑定凭据前缀 | 文本；不显示明文 |
| workload_identity.scopes | 绑定权限范围 | 标签列表 |
| volumes[].name | 卷名称 | 文本 |
| volumes[].kind | 卷类型 | `root_disk / data_disk / shared_pvc / object_fuse / ephemeral` |

## 字段展示规则

- 页面展示的容器实例字段必须直接映射现有 `Core v1.yaml` schema
- 可以在前端 view model 中转为 camelCase，但正文里的契约口径以 `Core v1.yaml` 为准
- 对 `tenant_id` 只视为后端返回字段，不视为前端必传字段
- `dev_profile` 属于可选开发态辅助字段，默认不作为首屏展示字段；如需展示，必须直接映射 Core 返回值
- `workload_identity` 只展示绑定摘要，不展示密钥明文
- 当前页面不把 `image`、`container.history` 扩写成独立镜像仓库或发布系统资源

## 字段口径与单位

| 字段 | 口径建议 | 单位/格式 |
|---|---|---|
| cpu | 以 `Core` 返回为准 | 文本或核心数 |
| memory | 以 `Core` 返回为准 | `Gi / Mi` |
| replicas / ready_replicas | 以 `Core` 返回为准 | 整数 |
| created_at / updated_at | 实例元数据时间 | ISO 时间或本地格式化时间 |

## 状态与能力口径

### 已冻结的 Core 能力

- `GET /api/v1/instances?kind=container`
- `GET /api/v1/instances/{instance_id}`
- `POST /api/v1/instances`
- `POST /api/v1/instances/{instance_id}/lifecycle`
- `GET /api/v1/instances/{instance_id}/operations`
- `GET /api/v1/instance-operations/{operation_id}`

### 待补 Core 契约能力

<!-- ADDED-TO-YAML: GET /api/v1/instances/{instance_id}/logs (Core v1.yaml, Phase 2 2026-06-17) -->
- 容器实例日志
- 容器实例事件 <!-- ADDED-TO-YAML: GET /api/v1/instances/{instance_id}/events (Core v1.yaml, Phase 2 2026-06-17) -->
- 容器实例指标 <!-- ADDED-TO-YAML: GET /api/v1/instances/{instance_id}/metrics (Core v1.yaml, Phase 2 2026-06-17) -->
- 容器实例终端 / exec <!-- ADDED-TO-YAML: POST /api/v1/instances/{instance_id}/exec (Core v1.yaml, Phase 2 2026-06-17) -->
- 容器实例 Dashboard
- 命名空间
- Deployment / StatefulSet / DaemonSet / Job / CronJob
- Service / Ingress
- ConfigMap / Secret
- PVC / PV / StorageClass / 卷快照
- HPA / VPA / KEDA
- Helm / DevOps / 微服务治理

## 创建前置条件

| 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
|---|---|---|
| 用户登录与租户上下文 | 已认证 | `401 UNAUTHORIZED` / `403 FORBIDDEN` |
| `name` | 租户内唯一 | `409 CONFLICT` |
| `kind=container` | 必须为 `container` | `400 BAD_REQUEST` |
| `image` | 镜像引用有效 | `422 PRECONDITION_FAILED`（YAML 已举例 `IMAGE_NOT_FOUND`） |
| `container_config.replicas` / 兼容别名 `replicas`（若填写） | ≥ 1 | `400 BAD_REQUEST` |

首版创建只承接 `image`、`cpu`、`memory`、`container_config.replicas`（或兼容别名 `replicas`）、`auto_start`。

### 生命周期动作前置条件（`POST /api/v1/instances/{instance_id}/lifecycle`）

- 调用方已通过标准鉴权
- 路径参数 `instance_id` 必须存在且当前租户可见
- 请求体必须包含 `action` 和 `idempotency_key`
- 若 `action=rollback`，目标实例应具备可回滚修订版本

说明：前置条件校验由后端在 `POST /api/v1/instances` 执行；具体 `code` 口径见 `../governance/module-delivery-workflow.md` §2.10。

## 操作可用性矩阵

基于 `InstanceRecord.state` 与 `InstanceLifecycleRequest.action`（`openapi/v1.yaml` 已冻结枚举）。

| 操作 | pending | provisioning | starting | running | stopping | stopped | failed | deleting | deleted |
|---|---|---|---|---|---|---|---|---|---|
| 启动 (`start`) | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ✅ | ✅ | ❌ 置灰 | ❌ 置灰 |
| 停止 (`stop`) | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ✅ | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 |
| 重启 (`restart`) | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ✅ | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 |
| 变配 (`resize`) | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ✅ | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 |
| 删除 (`delete`) | ✅ | ⚠️ 二次确认 | ⚠️ 二次确认 | ⚠️ 需先停止 | ❌ 置灰 | ✅ | ✅ | ❌ 置灰 | ❌ 置灰 |
| 回滚 (`rollback`) | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ✅ | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 |

补充规则：

- `termination_protection=true` 时，停止/删除等危险动作返回 `409 CONFLICT`
- 当前状态不允许执行某动作时，生命周期接口返回 `422 PRECONDITION_FAILED`（YAML 已举例 `INSTANCE_STATE_INVALID`）

## 冲突语义与当前契约边界

- `POST /api/v1/instances` 当前已冻结返回码包含 `409 CONFLICT`
- `POST /api/v1/instances/{instance_id}/lifecycle` 当前已冻结返回码包含 `409 CONFLICT`
- 当前页面应把 `409` 解释为状态冲突、重复创建冲突或目标 revision/action 当前不可执行
- 本轮不额外自造新的冲突错误码

## 接口冻结规则

### 只读接口

#### `GET /api/v1/instances?kind=container`

| 项 | 值 |
|---|---|
| operationId | `listInstances` |
| summary | `查询实例列表` |
| tags | `["Instances"]` |
| security | `[{BearerAuth: []}, {ApiKeyAuth: []}]` |
| query params | `kind=container`、`limit?`、`cursor?` |
| success | `200 + InstanceListResponse` |
| error responses | `401 UNAUTHORIZED`、`403 FORBIDDEN` |

#### `GET /api/v1/instances/{instance_id}`

| 项 | 值 |
|---|---|
| operationId | `getInstance` |
| summary | `查询实例详情` |
| tags | `["Instances"]` |
| security | `[{BearerAuth: []}, {ApiKeyAuth: []}]` |
| path params | `instance_id` |
| success | `200 + InstanceRecord` |
| error responses | `401 UNAUTHORIZED`、`403 FORBIDDEN`、`404 NOT_FOUND` |

#### `GET /api/v1/instances/{instance_id}/operations`

| 项 | 值 |
|---|---|
| operationId | `listInstanceOperations` |
| summary | `查询实例操作历史` |
| tags | `["Instances"]` |
| security | `[{BearerAuth: []}, {ApiKeyAuth: []}]` |
| path params | `instance_id` |
| query params | `limit?`、`cursor?` |
| success | `200 + allOf(CursorPage + items: InstanceOperation[])` |
| error responses | `401 UNAUTHORIZED`、`403 FORBIDDEN`、`404 NOT_FOUND` |

#### `GET /api/v1/instance-operations/{operation_id}`

| 项 | 值 |
|---|---|
| operationId | `getInstanceOperation` |
| summary | `查询单个操作详情` |
| tags | `["Instances"]` |
| security | `[{BearerAuth: []}, {ApiKeyAuth: []}]` |
| path params | `operation_id` |
| success | `200 + InstanceOperation` |
| error responses | `401 UNAUTHORIZED`、`403 FORBIDDEN`、`404 NOT_FOUND` |

### 写操作接口

#### `POST /api/v1/instances`

| 项 | 值 |
|---|---|
| operationId | `createInstance` |
| summary | `创建实例` |
| tags | `["Instances"]` |
| security | `[{BearerAuth: []}, {ApiKeyAuth: []}]` |
| requestBody.required | `name`、`kind`、`idempotency_key` |
| 关键字段 | `kind=container`、`image`、`cpu`、`memory`、`container_config.replicas`、`auto_start`；扁平 `replicas` 仍为兼容别名 |
| success | `201 + CreateInstanceResponse` |
| error responses | `400 BAD_REQUEST`、`401 UNAUTHORIZED`、`403 FORBIDDEN`、`409 CONFLICT` |

#### `POST /api/v1/instances/{instance_id}/lifecycle`

| 项 | 值 |
|---|---|
| operationId | `applyInstanceLifecycle` |
| summary | `执行实例生命周期操作` |
| tags | `["Instances"]` |
| security | `[{BearerAuth: []}, {ApiKeyAuth: []}]` |
| path params | `instance_id` |
| requestBody.required | `action`、`idempotency_key` |
| 页面收口动作 | `start`、`stop`、`restart`、`delete`、`rollback` |
| success | `200 + InstanceLifecycleResponse` |
| error responses | `400 BAD_REQUEST`、`401 UNAUTHORIZED`、`403 FORBIDDEN`、`404 NOT_FOUND`、`409 CONFLICT` |

### 通用约束

- Header 仅要求标准鉴权和 `X-Request-Id`
- 不显式要求 `X-Tenant-Id`
- 前端页面视图模型可使用 camelCase，但 API 路径参数、请求体和响应 JSON schema 以 snake_case 为准
- `POST /api/v1/instances` 与 `POST /api/v1/instances/{instance_id}/lifecycle` 必须带 `idempotency_key`
- 本模块严格跟随现有 `Core v1.yaml` 返回码，不把容器实例写操作改写成 `202 + AsyncTask`
- `POST /api/v1/instances/{instance_id}/console` 明确不属于本模块已冻结能力

## 响应与错误示例

### 创建容器实例成功响应示例

```json
{
  "instance": {
    "id": "inst-container-001",
    "tenant_id": "tenant-001",
    "name": "container-demo-01",
    "kind": "container",
    "state": "provisioning",
    "provider": "kubernetes_rest",
    "endpoint": "https://app.example.com",
    "container": {
      "replicas": 2,
      "ready_replicas": 0,
      "revision": null,
      "rollout_status": "pending",
      "history": []
    },
    "created_at": "2026-06-13T14:00:00+08:00",
    "updated_at": "2026-06-13T14:00:00+08:00"
  },
  "operation_id": "op-container-001",
  "audit_id": "audit-container-001"
}
```

### 错误返回示例

```json
{
  "code": "CONFLICT",
  "message": "container instance action conflicts with current state",
  "request_id": "req-20260613-301"
}
```

## 回填前置依赖

- 后续若增加工作负载、服务发现、配置、存储、弹性伸缩等容器平台资源，必须拆成独立模块维护
- 后续若要扩展更细粒度的发布历史、回滚策略或副本策略，也必须先确认 `InstanceRecord.container` 和 `InstanceOperation` schema 是否需要增补

## 待确认项

- 容器实例专属日志、指标、事件、终端是否进入下一轮 `Core v1.yaml` 扩充
- 副本调整后续是否应补充明确字段或动作
- 容器平台 Dashboard 是否进入独立模块

## 回填验收标准

- 正文明确区分已冻结能力与待补能力
- 正文不再出现旧 `/api/v1/console/*`、旧 `/api/v1/storage/*` 路径和 K8s 原生 API 请求头
- 正文不要求前端显式传 `tenant_id / X-Tenant-Id`
- 正文中的路径、返回码、请求字段与现有 `Core v1.yaml` 一致
- 正文不把 K8s 工作负载平台 IA 写成当前模块正式契约
- `PRD`、`SPEC`、HTML 摘要和本文件一致
- 本文件可以独立作为 `Console / 容器实例` 对齐 `Core` 的主维护材料

## 产品经理补充定义

### 目标用户与权限视角

- 租户管理员：查看、创建并操作 `kind=container` 的统一实例
- 业务成员：查看与自身工作负载相关的容器实例，并在授权前提下执行基础生命周期动作
- 只读用户：仅查看列表、详情、发布摘要与操作历史

### 默认视图与页面状态

- 首屏默认展示列表、统计摘要和发布状态，不额外拆出独立运维页
- 当租户暂无容器实例时，页面展示空态并提供创建入口
- 当发布摘要或操作历史加载失败时，只影响对应区块，列表与详情骨架继续可见
- 当动作提交成功后，页面必须引导用户查看最新 `operation` 或刷新实例详情

### 核心任务流

1. 用户从列表进入实例详情，查看 `replicas / ready_replicas / revision / rollout_status` 等运行摘要
2. 用户提交创建请求后，页面跳转到详情或最近操作，并持续展示统一实例的发布结果
3. 用户执行 `start / stop / restart / delete / rollback` 后，通过操作历史确认是否完成

### 跨模块协同

- 网络、存储和镜像等引用信息只保留跳转，不在本页新建子资源定义
- 若实例被平台首页、GPU 页或告警页引用，应带着实例 ID 或状态回跳到本页
- 未冻结的 `logs / events / exec / dashboard` 仅能作为待补说明，不形成真实跳转承诺

### 产品验收补充

- 用户必须能把“实例状态”与“发布状态”区分开来，不混用同一标签
- 页面必须让用户在创建后快速看到统一实例详情，而不是停留在无上下文的成功提示
- 生命周期动作结果必须能通过操作历史追踪，不能只依赖 toast 文案
- 本页不得把普通容器实例扩写为独立 `/containers` 资源域
