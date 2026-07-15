# Sandbox 实例

## 页面定位

`Sandbox 实例` 是 `Console / 算力与云资源 / 实例管理` 下的租户侧实例页面，用于帮助用户创建、查看和管理当前权限范围内的 Sandbox 实例。

当前页面必须复用统一实例资源 `kind=sandbox`，而不是定义独立 `Sandbox` Core 资源。

## 文档管理规则

- 本文件是 `Sandbox 实例` 的主维护源
- `prototypes/ani-services-prototype-console.html` 与 `prototypes/ani-services-prototype.html` 只保留页面摘要、模块边界和详细材料入口
- `tasks/modules/prd/console/compute/prd-console-sandbox-instance-management.md` 与 `tasks/modules/spec/console/compute/spec-console-sandbox-instance-management.md` 作为阶段性产物保留，不替代本文件

## Core 层要求

- Sandbox 实例能力属于 `Core / Instances`
- 当前模块不拥有独立 `/api/v1/sandboxes*` 路径
- 查询与写操作必须使用 `/api/v1/instances*`
- Sandbox 实例通过 `kind=sandbox` 区分
- 页面不要求前端显式传 `tenant_id`
- 写操作必须按现有 schema 提供 `idempotency_key`
- `CreateInstanceRequest` 推荐按 kind 使用 `vm_config` / `container_config` / `gpu_container_config` / `sandbox_config`；`sandbox_config` 与 `InstanceRecord.sandbox` 是 Sandbox 正式字段来源
- 扁平 `boot_image` / `ssh_*` / `replicas` / `gpu.*` 保留为 v1 兼容别名，与对应 `*_config` 冲突时返回 `400`
- 当前模块不把 `templates / security-events / metrics / terminal / security-overview` 写成已冻结能力

## 页面职责

- 提供 Sandbox 实例创建入口
- 提供 Sandbox 实例列表入口
- 展示单个 Sandbox 实例详情
- 提供基础生命周期动作入口
- 提供操作历史与操作详情入口
- 明确说明当前列表能力来自统一实例查询，而不是独立 Sandbox 资源组

## 当前能力边界说明

- 当前 `GET /api/v1/instances` 已支持按 `kind=sandbox` 进入统一实例列表范围
- 因此 **Sandbox 列表能力已通过统一实例资源冻结**，但这不等于已经存在独立 `/api/v1/sandboxes*` 资源组
- 当前页面可以承接“列表 -> 详情 -> 生命周期 -> 操作历史”的链路
- 旧原型中的独立 Sandbox 资源组、模板、安全事件、安全总览都必须降级为待补边界

## 创建前置条件

| 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
|---|---|---|
| 用户登录与租户上下文 | 已认证 | `401` / `403` |
| `name` | 租户内唯一 | `409 CONFLICT` |
| `sandbox_config.runtime_class` | 合法运行时类 | `400 BAD_REQUEST` |
| `image` | 镜像引用有效 | `422 PRECONDITION_FAILED`（YAML 已举例 `IMAGE_NOT_FOUND`） |

> **架构说明**：Sandbox 统一走 Core `/api/v1/instances*`（`kind=sandbox`）。`services/v1.yaml` 中 `/sandboxes*` 已废弃。

## 操作可用性矩阵

| 操作 | pending | provisioning | starting | running | stopping | stopped | failed | deleting |
|---|---|---|---|---|---|---|---|---|
| 启动 | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ | ✅ | ❌ |
| 停止 | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ |
| 重启 | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ |
| 删除 | ✅ | ⚠️ | ⚠️ | ⚠️ | ❌ | ✅ | ✅ | ❌ |
| 延长/暂停（待补） | — | — | — | — | — | — | — | — |

`running` 状态下「启动」置灰；状态冲突返回 `422 PRECONDITION_FAILED`（YAML 已举例 `INSTANCE_STATE_INVALID`）。延长存活、暂停/恢复等待 Core 生命周期动作扩展后再接入。

## 页面区块与数据来源映射

| 区块 | 主要来源层 | 数据口径说明 | 跳转目标 |
|---|---|---|---|
| 实例列表 | Core | `GET /api/v1/instances` + `kind=sandbox` | 实例详情 |
| 创建实例 | Core | `POST /api/v1/instances` + `kind=sandbox` | 创建结果 |
| 实例详情 | Core | `GET /api/v1/instances/{instance_id}` | 操作历史 |
| 生命周期动作 | Core | `POST /api/v1/instances/{instance_id}/lifecycle` | 操作详情 |
| 操作历史 | Core | `GET /api/v1/instances/{instance_id}/operations` | 单次操作详情 |
| 边界说明 | 规划项 | 模板、安全事件等能力不属于当前冻结范围 | 后续模块 |

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

### Sandbox 摘要字段

| 字段 | 说明 | 展示建议 |
|---|---|---|
| sandbox.runtime_class | 运行时类 | 文本 |
| sandbox.session_timeout | 会话超时时间 | 文本 |
| sandbox.network_egress_policy | 出口策略 | 标签 |
| sandbox.session_state | 会话状态 | 标签 |
| sandbox.dev_profile | 本地 profile 摘要 | 只读说明 |

## 已冻结的 Core 能力

- `GET /api/v1/instances` + `kind=sandbox`
- `POST /api/v1/instances`
- `GET /api/v1/instances/{instance_id}`
- `POST /api/v1/instances/{instance_id}/lifecycle`
- `GET /api/v1/instances/{instance_id}/operations`
- `GET /api/v1/instance-operations/{operation_id}`

## 列表规则

### `GET /api/v1/instances`

| 项 | 值 |
|---|---|
| operationId | `listInstances` |
| summary | `查询实例列表` |
| query 过滤 | `kind=sandbox` 或 `instance_type=sandbox` |
| success | `200 + InstanceListResponse` |
| 当前口径 | 统一实例列表已冻结（`listInstances.kind` 枚举含 `sandbox`），不扩写独立 `/api/v1/sandboxes*` 路径 |

## 创建规则

### `POST /api/v1/instances`

| 项 | 值 |
|---|---|
| operationId | `createInstance` |
| summary | `创建实例` |
| requestBody | `CreateInstanceRequest` |
| 必填关键字段 | `idempotency_key`、`name`、`kind=sandbox` |
| 常用字段 | `image`、`cpu`、`memory`、`sandbox_config.runtime_class`、`sandbox_config.session_timeout`、`sandbox_config.network_egress_policy` |
| success | `201 + CreateInstanceResponse` |
| error responses | `400 BAD_REQUEST`、`401 UNAUTHORIZED`、`403 FORBIDDEN`、`409 CONFLICT` |

## 生命周期规则

### `POST /api/v1/instances/{instance_id}/lifecycle`

| 项 | 值 |
|---|---|
| operationId | `applyInstanceLifecycle` |
| summary | `执行实例生命周期操作` |
| requestBody | `InstanceLifecycleRequest` |
| 当前页面承接动作 | `start`、`stop`、`restart`、`delete` |
| success | `200 + InstanceLifecycleResponse` |
| error responses | `400 BAD_REQUEST`、`401 UNAUTHORIZED`、`403 FORBIDDEN`、`404 NOT_FOUND`、`409 CONFLICT` |

## 待补 Core 契约能力

- `/api/v1/sandboxes*` 独立资源组 <!-- ALIGNED: → Core /api/v1/instances* kind=sandbox -->
- Sandbox 模板列表 — 详文 `sandbox-templates.md` <!-- ADDED-TO-YAML: GET /api/v1/sandbox-templates (Core v1.yaml, Phase 2 2026-06-17) -->
- Sandbox 安全事件 — 详文 `sandbox-templates.md` §安全事件 <!-- ADDED-TO-YAML: GET /api/v1/instances/{instance_id}/security-events (Core v1.yaml, Phase 2 2026-06-17) -->
- Sandbox 监控指标 <!-- ADDED-TO-YAML: GET /api/v1/instances/{instance_id}/metrics (Core v1.yaml, Phase 2 2026-06-17) -->
- Sandbox 安全总览
- Sandbox 延长存活 / 暂停 / 恢复专属动作 <!-- ADDED-TO-YAML: POST /api/v1/instances/{instance_id}/lifecycle 扩展 action (Core v1.yaml, Phase 2 2026-06-17) -->
- Sandbox 独立终端能力 <!-- ADDED-TO-YAML: POST /api/v1/instances/{instance_id}/exec (Core v1.yaml, Phase 2 2026-06-17) -->

## 验收标准

- 正文路径、字段和返回码与现有 `Core v1.yaml` 一致
- 不再把旧 `/sandboxes*` 路径写成正式契约
- 正文明确 Sandbox 列表来自统一实例查询，而不是独立 Sandbox 资源组
- HTML 摘要、PRD、SPEC 与本文件一致
- 本文件可以独立作为 `Console / Sandbox 实例` 的主维护材料

## 产品经理补充定义

### 目标用户与权限视角

- 租户管理员：创建、查看并操作 `kind=sandbox` 的统一实例
- 业务成员：查看 Sandbox 会话状态，并在授权前提下执行基础生命周期动作
- 只读用户：仅查看详情与操作历史，不展示危险动作入口

### 默认视图与页面状态

- 当前产品口径默认以“列表 -> 详情 / 创建后进入详情 / 从最近操作进入详情”为主，并明确列表来自统一实例查询
- 当尚无 Sandbox 实例时，页面以创建引导和最近操作为空态为主
- 会话状态与实例状态必须分开呈现，避免把会话过期误判为实例删除
- 当创建或动作冲突时，页面必须保留 `request_id` 以便回溯

### 核心任务流

1. 用户填写 `sandbox_config` 创建实例，并在成功后进入详情查看会话摘要
2. 用户从详情页执行 `start / stop / restart / delete`，动作完成后刷新详情和操作历史
3. 用户通过最近操作或操作详情回到实例，确认当前会话状态是否与预期一致

### 跨模块协同

- 与 `安全与身份概览`、网络与实例总览协同，仅展示引用关系和跳转入口
- 不把 Sandbox 模板、安全事件、监控总览、独立终端等 **未实现 handler** 的能力写成联动承诺（YAML 已声明路径见 `sandbox-templates.md`）
- 若首页需要展示 Sandbox 事项，只能回跳到当前详情或最近操作上下文

### 产品验收补充

- 当前文档必须明确“可列表、可创建、可详情、可动作、可追踪”，但不能暗示已存在独立 Sandbox 资源组
- 会话状态必须与 `sandbox` 摘要字段对齐，不自行扩展独立状态机
- 创建与动作失败都必须给出可追踪的错误反馈，不允许只展示模糊失败文案
- 本页不得引用任何 `/sandboxes*` 风格正式路径
