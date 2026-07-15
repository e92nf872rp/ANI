# 云主机 VM

## 页面定位

`云主机 VM` 是 `Console / 算力与云资源 / 实例管理` 下的租户侧资源管理页面，用于帮助用户查看、创建、操作和排查当前权限范围内的 VM 实例。

本页是 `Console` 页面，不是 `BOSS` 的平台实例运营页。

## 文档管理规则

- 本文件是 `云主机 VM` 的主维护源
- `prototypes/ani-services-prototype-console.html` 只保留页面摘要、模块边界和详细材料入口
- `tasks/modules/prd/console/compute/prd-console-vm-management.md` 和 `tasks/modules/spec/console/compute/spec-console-vm-management.md` 作为阶段性产物保留，不替代本文件
- 后续如 VM 页面定义发生变化，优先修改本文件，再同步回看 HTML 摘要

## Core 层要求

- `VM` 资源对象、生命周期、Console/VNC 会话、操作历史都属于 `Core`
- 查询与动作接口必须使用 `/api/v1/*`
- 不允许把 VM 资源对象写入 `Services /api/v1/svc/*`
- 不允许继续使用旧的 `/api/v1/console/*` 路径
- 不要求前端显式传 `tenant_id` 或 `X-Tenant-Id`
- 租户边界必须从认证上下文或后端中间件获取
- 创建和生命周期动作必须带 `idempotency_key`
- 新增接口说明必须具备 `operationId`、中文 `summary`、`tags`、`security`、完整 `responses`
- 错误响应格式统一为 `{"code":"UPPER_SNAKE","message":"...","request_id":"..."}`
- VM 模块必须严格对齐现有 `Core v1.yaml` 已冻结的返回码：创建 `201`，生命周期动作 `200`，Console 会话 `200`

## 页面职责

- 展示当前租户下的 VM 列表与基础统计
- 展示单个 VM 的详情信息
- 提供创建 VM 入口
- 提供启动、停止、重启、变配、删除等生命周期动作
- 提供 Console/VNC 会话入口
- 提供操作历史和失败排障入口

## 页面结构

```text
云主机 VM
├── 顶部筛选区
│   ├── 状态
│   ├── 创建时间
│   ├── Provider
│   └── 关键字
├── 统计摘要
│   ├── 全部
│   ├── 运行中
│   ├── 已停止
│   ├── 异常
│   └── 最近有操作
├── VM 列表
│   ├── 基础信息列
│   ├── 规格与节点信息
│   ├── 状态与终端保护
│   └── 操作入口
├── VM 详情
│   ├── 概览
│   ├── SSH 与访问
│   ├── 卷与快照摘要
│   ├── 关联资源
│   └── 操作历史
└── 动作入口
    ├── 创建 VM
    ├── 生命周期动作
    └── Console / VNC
```

## 交互与使用规则

- 页面默认展示当前租户、当前权限范围内的 VM，不展示平台全量实例池
- 列表页优先承担查找和筛选职责，详情页承担排障和关联资源查看职责
- 对于网络、存储、镜像、安全组等能力，本页只展示当前关联关系和跳转入口
- 危险动作必须结合状态判断和确认提示
- 若 VM 开启 `termination_protection`，停止、删除、重建等危险动作必须提示冲突风险
- Console/VNC 入口只消费短期会话信息，不暴露 provider 长期凭据

## 数据来源与分层约束

### 数据来源划分

- `Core` 数据
  - VM 列表
  - VM 详情
  - VM 创建
  - VM 生命周期动作
  - VM Console/VNC 会话
  - VM 操作历史与操作详情
- `Services` 数据
  - 本页不承载 VM 资源对象，不定义新的 Services VM 资源契约

### 关键边界

- 本页可以展示卷、快照、SSH、节点、操作历史等与 VM 直接相关的摘要
- 本页不重新定义 VPC、子网、安全组、块存储、对象存储、镜像等底层资源契约
- 如需继续细化这些资源，应在对应模块单独维护
- 如后续需要生成 `Core v1.yaml` 草稿，应从本文件的接口冻结规则继续下沉，而不是回到 HTML 里重新整理

## 页面区块与数据来源映射

| 区块 | 主要来源层 | 数据口径说明 | 跳转目标 |
|---|---|---|---|
| 统计摘要 | Core | 基于当前租户可见的 `kind=vm` 实例聚合 | VM 列表 |
| VM 列表 | Core | 展示当前租户权限范围内的 VM | VM 详情 |
| VM 详情 | Core | 对齐 `InstanceRecord`，展示 VM 自身与直接关联摘要 | 操作历史 / 关联资源模块 |
| 创建 VM | Core | 使用现有 `POST /api/v1/instances` 契约，`kind=vm` | 创建抽屉 |
| 生命周期动作 | Core | 使用 `POST /api/v1/instances/{instance_id}/lifecycle` | 动作确认弹窗 |
| Console / VNC | Core | 使用短期会话，不暴露长期凭据 | 新窗口 / 控制台 |
| 操作历史 | Core | 展示操作类型、状态、失败原因和时间线 | 操作详情 |

## 模块区块详细说明

### 统计摘要

- 展示重点：全部、运行中、已停止、异常、最近有操作
- 主要来源层：Core
- 展示口径：当前租户下 `kind=vm` 的实例聚合
- 异常/空态：无 VM 时展示空态，不伪造统计值
- 跳转目标：VM 列表

### VM 列表

- 展示重点：名称、状态、规格、Provider、节点、终端保护、创建时间、操作入口
- 主要来源层：Core
- 展示口径：列表字段来自 `InstanceRecord`
- 异常/空态：若接口失败，列表区单独报错，不影响页面框架
- 跳转目标：VM 详情

### VM 详情

- 展示重点：基础信息、状态原因、SSH、卷、快照、关联资源、更新时间
- 主要来源层：Core
- 展示口径：只展示当前租户可见的实例详情
- 异常/空态：找不到实例时展示 `NOT_FOUND`
- 跳转目标：操作历史 / 对应资源模块

### 创建 VM

- 展示重点：名称、CPU、内存、boot image、SSH、自动启动、终端保护
- 主要来源层：Core
- 展示口径：对齐 `CreateInstanceRequest`，其中 `kind=vm`
- 异常/空态：提交失败时必须展示 `request_id`
- 跳转目标：创建结果对应的实例详情

### 生命周期动作

- 展示重点：启动、停止、重启、变配、重建、删除、快照、卷挂载/卸载、回滚
- 主要来源层：Core
- 展示口径：对齐 `InstanceLifecycleRequest`
- 异常/空态：冲突和终端保护需明确提示
- 跳转目标：操作历史 / 操作详情

### Console / VNC

- 展示重点：`console / vnc / novnc / serial`
- 主要来源层：Core
- 展示口径：只消费 `InstanceConsoleSession`
- 可用状态：默认仅对 `running` 状态实例开放；`starting`、`stopped`、`failed`、`deleting`、`deleted` 不开放申请入口
- 异常/空态：会话申请失败时展示错误信息，不重用旧会话
- 跳转目标：新窗口 / 终端容器

### 操作历史

- 展示重点：操作类型、状态、触发人、失败原因、时间线步骤
- 主要来源层：Core
- 展示口径：对齐 `InstanceOperation`
- 异常/空态：无记录时展示空态
- 跳转目标：单个操作详情

## 字段级定义

### 列表与详情基础字段

| 字段 | 说明 | 展示建议 |
|---|---|---|
| id | 实例 ID | 文本；详情页保留复制能力 |
| name | 实例名称 | 文本；列表可点击 |
| kind | 实例类型 | 本页固定展示 `vm` |
| state | 实例状态 | 标签展示 |
| state_reason | 机器可读状态原因码 | 详情页展示 |
| state_message | 人类可读状态描述 | 详情页展示 |
| provider | 实例 Provider | 文本 |
| endpoint | 实例访问地址 | 文本或链接 |
| node_name | 实例所在节点 | 文本；可跳转 |
| termination_protection | 危险操作保护开关 | 布尔标签 |
| created_at | 创建时间 | 时间 |
| updated_at | 更新时间 | 时间 |

### SSH、卷、快照字段

| 字段 | 说明 | 展示建议 |
|---|---|---|
| ssh.username | SSH 用户名 | 文本 |
| ssh.host | SSH 主机地址 | 文本 |
| ssh.port | SSH 端口 | 数字 |
| ssh.key_ref | SSH 密钥引用 | 文本；不展示密钥内容 |
| ssh.ready | 是否已可连接 | 布尔状态 |
| volumes[].name | 卷名称 | 文本 |
| volumes[].kind | 卷类型 | `root_disk / data_disk / shared_pvc / object_fuse / ephemeral` |
| volumes[].size_gib | 卷容量 | `GiB` |
| snapshots[].id | 快照 ID | 文本 |
| snapshots[].name | 快照名称 | 文本 |
| snapshots[].state | 快照状态 | 标签 |

### 创建与动作字段

| 字段 | 说明 | 展示建议 |
|---|---|---|
| idempotency_key | 幂等键 | 写操作必填 |
| cpu | CPU 规格 | 文本，如 `4` |
| memory | 内存规格 | 文本，如 `8Gi` |
| vm_config.boot_image | 启动镜像引用（推荐） | 文本；扁平 `boot_image` 为兼容别名 |
| vm_config.ssh_username | SSH 用户名（推荐） | 文本；扁平 `ssh_username` 为兼容别名 |
| vm_config.ssh_key_ref | SSH 密钥引用（推荐） | 文本；扁平 `ssh_key_ref` 为兼容别名 |
| action | 生命周期动作 | 枚举 |
| operation_id | 操作记录 ID | 文本；支持跳转 |

## 字段展示规则

| 场景 | 展示规则 | 说明 |
|---|---|---|
| 正常 | 展示状态、规格、更新时间和操作入口 | 页面默认状态 |
| 暂无数据 | 展示空态文案，不伪造实例记录 | 适用于新租户或空列表 |
| 局部异常 | 列表、详情、操作历史分别展示错误 | 不让整页不可用 |
| 状态冲突 | 保留动作入口但在提交后明确提示冲突 | 如终端保护、状态不允许 |
| 会话过期 | 提示重新申请 Console/VNC 会话 | 不复用旧 URL |

## 字段口径与单位

| 字段 | 口径建议 | 单位/格式 |
|---|---|---|
| cpu | 以 `Core` 返回为准 | 文本或核心数 |
| memory | 以 `Core` 返回为准 | `Gi / Mi` |
| created_at / updated_at | 实例元数据时间 | ISO 时间或本地格式化时间 |
| size_gib | 卷大小 | `GiB` |
| termination_protection | 以布尔值表达 | 开 / 关 |

## 状态阈值建议

| 字段/场景 | 默认提示规则 | 说明 |
|---|---|---|
| state=failed | 红色高亮 | 实例运行失败 |
| state=provisioning/starting/stopping | 进行中态 | 避免误判卡死 |
| termination_protection=true | 危险动作前提示 | 防止误删 |
| 操作历史 status=failed | 高亮失败原因 | 便于排障 |
| ssh.ready=false | 不展示可连接态 | 与“无 SSH 信息”区分 |

## 创建前置条件

| 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
|---|---|---|
| 用户登录与租户上下文 | 已认证 | `401 UNAUTHORIZED` / `403 FORBIDDEN` |
| `name` | 租户内唯一 | `409 CONFLICT` |
| `vm_config.boot_image` / 兼容别名 `boot_image` | 镜像引用有效 | `422 PRECONDITION_FAILED`（YAML 已举例 `IMAGE_NOT_FOUND`） |
| `vm_config.ssh_key_ref` / 兼容别名 `ssh_key_ref`（若填写） | 密钥引用存在 | `422 PRECONDITION_FAILED`（具体 `code` 待 Core 冻结；建议语义：SSH 密钥未找到） |
| 租户配额（若启用） | 未超限 | `422 PRECONDITION_FAILED`（具体 `code` 待 Core 冻结；建议语义：配额超限） |

说明：前置条件校验由后端在 `POST /api/v1/instances` 执行；前端在提交前应做基础必填校验，但不得替代服务端 422 判定。除 YAML `PreconditionFailed` 描述已举例者外，表中 `code` 仅为产品建议语义，不得当作已冻结契约。

## 操作可用性矩阵

基于 `InstanceRecord.state` 与 `InstanceLifecycleRequest.action`（`openapi/v1.yaml` 已冻结枚举）。

| 操作 | pending | provisioning | starting | running | stopping | stopped | failed | deleting | deleted |
|---|---|---|---|---|---|---|---|---|---|
| 启动 (`start`) | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ✅ | ✅ | ❌ 置灰 | ❌ 置灰 |
| 停止 (`stop`) | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ✅ | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 |
| 重启 (`restart`) | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ✅ | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 |
| 变配 (`resize`) | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ✅ | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 |
| 删除 (`delete`) | ✅ | ⚠️ 二次确认 | ⚠️ 二次确认 | ⚠️ 需先停止 | ❌ 置灰 | ✅ | ✅ | ❌ 置灰 | ❌ 置灰 |
| Console/VNC | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ✅ | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 | ❌ 置灰 |

补充规则：

- `termination_protection=true` 时，停止/删除/重建等危险动作返回 `409 CONFLICT`
- 当前状态不允许执行某动作时，生命周期接口返回 `422 PRECONDITION_FAILED`（YAML 已举例 `INSTANCE_STATE_INVALID`）
- Console 会话仅 `running` 可申请（见 `createInstanceConsoleSession` 契约）

## 接口冻结规则

### 只读接口

#### `GET /api/v1/instances?kind=vm`

| 项 | 值 |
|---|---|
| operationId | `listInstances` |
| summary | `查询实例列表` |
| tags | `["Instances"]` |
| security | `[{BearerAuth: []}, {ApiKeyAuth: []}]` |
| query params | `kind=vm`、`limit?`、`cursor?` |
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
| 关键字段 | `kind=vm`、`cpu`、`memory`、`vm_config`（`boot_image`/`ssh_username`/`ssh_key_ref`）、`termination_protection`；扁平 `boot_image`/`ssh_*` 仍为兼容别名 |
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
| success | `200 + InstanceLifecycleResponse` |
| error responses | `400 BAD_REQUEST`、`401 UNAUTHORIZED`、`403 FORBIDDEN`、`404 NOT_FOUND`、`409 CONFLICT` |

#### `POST /api/v1/instances/{instance_id}/console`

| 项 | 值 |
|---|---|
| operationId | `createInstanceConsoleSession` |
| summary | `申请 VM console/VNC session` |
| tags | `["Instances"]` |
| security | `[{BearerAuth: []}, {ApiKeyAuth: []}]` |
| path params | `instance_id` |
| requestBody | 可选；`protocol=console/vnc/novnc/serial` |
| 可用状态 | 仅 `running` |
| success | `200 + InstanceConsoleSession` |
| error responses | `400 BAD_REQUEST`、`401 UNAUTHORIZED`、`403 FORBIDDEN`、`404 NOT_FOUND`、`409 CONFLICT` |

### 通用约束

- Header 仅要求标准鉴权和 `X-Request-Id`
- 不显式要求 `X-Tenant-Id`
- 前端页面视图模型可使用 camelCase，但 API 路径参数、请求体和响应 JSON schema 以 snake_case 为准
- `POST /api/v1/instances` 与 `POST /api/v1/instances/{instance_id}/lifecycle` 必须带 `idempotency_key`
- `POST /api/v1/instances/{instance_id}/console` 的 `requestBody` 可选，但必须遵守 `running` 状态限制
- 标准错误码使用 `BAD_REQUEST`、`UNAUTHORIZED`、`FORBIDDEN`、`NOT_FOUND`、`CONFLICT`
- 本模块严格跟随现有 `Core v1.yaml` 返回码，不把 VM 写操作改写成 `202 + AsyncTask`

## 响应与错误示例

### 创建 VM 成功响应示例

```json
{
  "instance": {
    "id": "inst-vm-001",
    "tenant_id": "tenant-001",
    "name": "vm-demo-01",
    "kind": "vm",
    "state": "provisioning",
    "provider": "kubevirt",
    "endpoint": null,
    "termination_protection": true,
    "created_at": "2026-06-13T14:00:00+08:00",
    "updated_at": "2026-06-13T14:00:00+08:00"
  },
  "operation_id": "op-vm-001",
  "audit_id": "audit-vm-001"
}
```

### 生命周期动作成功响应示例

```json
{
  "instance": {
    "id": "inst-vm-001",
    "tenant_id": "tenant-001",
    "name": "vm-demo-01",
    "kind": "vm",
    "state": "starting",
    "provider": "kubevirt",
    "created_at": "2026-06-13T14:00:00+08:00",
    "updated_at": "2026-06-13T14:10:00+08:00"
  },
  "operation_id": "op-vm-002"
}
```

### 错误响应示例

```json
{
  "code": "CONFLICT",
  "message": "termination protection is enabled for this instance",
  "request_id": "req-20260613-101"
}
```

## 回填前置依赖

- 当前 VM 页面主路径已在 `Core v1.yaml` 中存在，后续重点是保持页面正文与现有 schema 对齐
- 若后续需要在 VM 页面补充监控摘要能力，应先确认是否复用 `GET /api/v1/observability/query`
- 若后续需要更细粒度的快照、卷、网络或镜像接口，应在对应 `Core` 资源域扩充，不在 VM 页面继续叠加

## 待确认项

- VM 页面首版是否需要直接展示监控图表，还是只保留监控入口
- 快照恢复动作是否继续留在 VM 页面，还是后续拆到独立恢复流程

## 当前阶段产物

- `tasks/modules/prd/console/compute/prd-console-vm-management.md`
- `tasks/modules/spec/console/compute/spec-console-vm-management.md`
- 上述材料用于追溯本轮设计过程；后续如正文与辅助材料存在差异，以本文件为准

## 回填验收标准

- HTML 中只保留摘要、边界和详细材料入口，不再堆叠完整契约正文
- 本文件可以独立回答页面定位、字段口径、接口冻结规则和 Core 边界问题
- 创建、生命周期动作、Console 会话的路径、返回码、错误结构与现有 `Core v1.yaml` 一致
- 不出现 `X-Tenant-Id` 必填、旧 `/api/v1/console/*` 路径、偏 BOSS 的平台实例池运营措辞
- 不继续把网络、存储、镜像、安全组的完整资源契约混写到 VM 模块正文中

## 非目标与边界

- 不做 BOSS 的全平台实例运营态势
- 不重写网络、存储、镜像、安全组、亲和组的完整资源契约
- 不定义新的 Services VM 资源契约
- 不改变现有 `Core v1.yaml` 已冻结的返回码和主路径

## 产品经理补充定义

### 目标用户与权限视角

- 租户管理员：查看全部 VM、创建实例、执行生命周期动作、申请 Console/VNC
- 业务成员：查看与自身权限范围一致的 VM，并在授权前提下操作实例
- 只读用户：仅查看列表、详情和操作历史，不显示创建与危险动作入口

### 默认视图与页面状态

- 默认落在 `统计摘要 + VM 列表`，详情页承担排障与关联资源查看
- 当租户无 VM 时，页面展示空态并提供创建入口
- 当单个实例详情或 Console 会话申请失败时，只影响当前实例，不阻断整页列表使用
- 当生命周期动作提交成功后，页面必须显式回流到实例详情或操作历史

### 核心任务流

1. 用户通过筛选和统计摘要定位目标 VM，进入详情查看状态原因和关联资源
2. 用户完成创建 VM，系统跳转到新实例详情并展示创建结果与操作历史入口
3. 用户从详情页触发生命周期动作或 Console/VNC，会话申请成功后在新窗口或终端容器中继续操作

### 跨模块协同

- 创建与详情中涉及的网络、安全组、卷、镜像等只保留跳转，不在本页重写底层契约
- GPU 相关可见信息回跳到 `GPU 算力管理`，存储关系回跳到 `存储管理`
- 首页或告警页可直接带着实例状态筛选回到本页

### 产品验收补充

- 用户必须能在列表、详情、操作历史三层结构中完成查找、处理和复核
- 创建、生命周期、Console/VNC 三类动作都必须给出明确的成功回流点
- 危险动作与终端保护冲突必须可见，不允许用户误判为“操作已成功”
- 页面必须把读操作异常与写操作冲突分开提示，避免统一文案掩盖原因
