# 实例管理 — 技术方案

> **涉及平台**: Console(租户控制台) / ANI Core
> **承接关系**: 承接镜像仓库、网络、存储、GPU 规格和实例可观测性既有能力；本方案只设计实例创建、实例详情、生命周期与 Sandbox 子资源，不重复设计已完成模块。GPU 配额扣减本期只保留接入位置，后续独立增加。
> **闭环定义**: 用户选择镜像、网络、存储和规格 → 创建 VM / Container / GPU Container / Sandbox → 查询详情与观测信息 → 执行生命周期操作 → 删除或销毁实例。

***

## 一、背景与现状

### 1.1 当前原型实例入口

依据 `/root/design/【存储补充】ANI-原型/index.html` 和 `market-detail.md`，Console 计算实例包含:

- 云主机 VM: `/compute/instances/vm`
- 容器实例: `/compute/instances/container`
- GPU 容器实例: `/compute/instances/gpu-container`
- Sandbox 实例: `/compute/instances/sandbox`
- 批任务: `/compute/instances/batch`，P1
- 裸金属 / DPU: `/compute/instances/bare-metal`，P1

P0 只覆盖 VM、Container、GPU Container、Sandbox。Batch、Bare Metal、DPU 本次只保留 kind 和列表占位，不做完整创建与生命周期。

### 1.2 当前 Core 实例契约

依据 `repo/api/openapi/v1.yaml`，当前已有统一实例模型:

- `GET /instances`
- `POST /instances`
- `GET /instances/{instance_id}`
- `POST /instances/{instance_id}/lifecycle`
- `POST /instances/{instance_id}/console`
- `GET /instances/{instance_id}/logs`
- `GET /instances/{instance_id}/events`
- `GET /instances/{instance_id}/metrics`
- `POST /instances/{instance_id}/exec`
- `GET /instances/{instance_id}/security-events`
- `GET /instances/{instance_id}/operations`
- `GET /sandbox-templates`

现有模型已经具备统一 `InstanceRecord`、`CreateInstanceRequest`、VM/Container/GPU/Sandbox config、生命周期操作、日志/事件/指标/终端/Console 能力。因此本次不重建实例体系，只做兼容扩展。

### 1.3 已就绪基础设施

| 能力 | 状态 | 本方案处理方式 |
| --- | --- | --- |
| Registry / Harbor / image purpose | 已有后端与 live gate | 实例创建只引用 `image_id`，不重复设计镜像仓库 |
| Network / VPC / Subnet / SecurityGroup | 已有接口与真实 provider 方向 | 实例创建在对应 kind config 中引用 `InstanceNetworkConfig` |
| Storage / Volume / Filesystem / Snapshot | 已有接口与 Rook-Ceph 后端 | 实例创建只引用 `volume_id`、`filesystem_id`、mount config |
| GPU spec | 由 `/root/design/plan(2).md` 定义，当前 Core OpenAPI 尚未落地 | GPU Container 只引用 `spec_id`；规格契约是 Console 选规格的前置依赖 |
| GPU quota | 本期不实现 | 只预留准入扩展点，不扣减、不占用、不释放配额 |
| Instance Observability | 已有 logs/events/metrics/exec/console/security-events | 只补详情页字段和分页缺口 |
| WorkloadRuntime 边界 | `CLAUDE.md` 强制要求 | VM/Container/GPU/Sandbox 都必须经 `WorkloadRuntime` |

***

## 二、关键决策记录

| 问题 | 决策 | 理由 |
| --- | --- | --- |
| Q1 实例 API 形态 | 保留统一 `/instances`，不拆 `/vm-instances` 等类型端点 | 现有 Core 契约已经是统一实例模型；拆分会增加 SDK、Console、handler 重复 |
| Q2 类型差异如何表达 | 通用字段放 `InstanceRecord`，类型差异放 `vm_config` / `container_config` / `gpu_container_config` / `sandbox_config` | 保持 schema 可读，避免万能 map |
| Q3 镜像/网络/存储是否重复设计 | 不重复；实例只持有引用和创建时选择字段 | 避免和已完成模块产生双写契约 |
| Q4 GPU 规格如何关联 | GPU Container 只新增 `spec_id`，引用 Core `GPUSpec`；实例模块不创建或维护规格 | `/root/design/plan(2).md` 定义规格资源，本方案只消费 |
| Q5 Sandbox 操作是否走 lifecycle | Sandbox 专用资源用独立子接口，基础 pause/resume/destroy 可走 lifecycle | token、port、file、checkpoint、code-run 是明确子资源，放进 action bus 会降低契约质量 |
| Q6 生命周期接口是否保留 | 保留 `POST /instances/{id}/lifecycle` 并扩展 action enum | 兼容现有 API 与 SDK |
| Q7 详情页 tabs 如何落地 | 详情主数据在 `InstanceRecord`，观测数据继续走 logs/events/metrics/exec/console/security-events | 复用 Sprint 15 已完成的可观测性能力 |
| Q8 批任务/裸金属/DPU | P1，只保留 kind 与列表兼容，不做创建和 runtime | 原型标为 P1，当前实例闭环先聚焦四类 P0 |
| Q9 真实 provider 边界 | Gateway handler 不直接拼 K8s/KubeVirt 对象；通过 port/adapter | 符合 `CLAUDE.md` 组件边界 |
| Q10 幂等 | 所有 create/lifecycle/sandbox 副作用接口必须有 `idempotency_key` | 符合 Core API 强制规则 |
| Q11 列表分页缺口 | `events` 和 `security-events` 增加可选 `cursor` query | 现有响应有 `next_cursor`，但请求缺 cursor，Console 只能降级一次性加载 |
| Q12 删除语义 | delete 为生命周期 action，终态为 `deleted`，记录保留 | 保持审计与操作历史可追溯 |
| Q13 VM 批量创建 | P0 不新增 `count`；创建接口一次只创建一个实例 | 原型只有批量生命周期操作；现有 `CreateInstanceResponse` 也只返回单实例，避免引入部分成功语义 |
| Q14 通用与类型配置边界 | 顶层只保留所有 kind 真正共享字段；网络、磁盘、挂载和运行参数放到对应 `*_config` | 避免 `storage_config` 与 `vm_config/container_config` 双写 |
| Q15 实例响应模型 | `InstanceRecord` 增加稳定的 image/compute/network/access/attachment 摘要，类型详情继续放 kind-specific status | 原型列表与详情必须由一个明确响应模型承载 |
| Q16 NFS 挂载入口 | 增加 `attach_filesystem` / `detach_filesystem` lifecycle action，由 instance service 编排 Storage port | 原型 VM/Container/GPU Container P0 均有“挂载 NFS” |
| Q17 状态表达 | 顶层 `state` 保持统一状态机；排队、发布、暂停、过期等进入 kind-specific status 和 `state_reason` | 避免继续膨胀全局状态 enum，同时满足列表筛选 |
| Q18 Sandbox schema 命名 | 扩展现有 `SandboxConfig`，不新增 `CreateSandboxInstanceConfig` 同义类型 | 保持 v1 兼容和生成物稳定 |
| Q19 Sandbox 删除幂等 | DELETE 使用 `Idempotency-Key` header；其他副作用 POST 继续使用 body `idempotency_key` | 避免 DELETE body 的跨客户端兼容问题 |
| Q20 契约交付顺序 | 契约和生成物单独提交、推送并通过个人仓库 CI 后，才进入 ports/handler/adapter | 遵守 `ani-core-platform` API-first 强制门禁 |
| Q21 GPU 配额范围 | 本期不做配额扣减、占用和释放；只在实例准入编排中保留后续检查位置 | 先完成规格选配与真实调度，配额作为后续独立契约和事务批次 |

***

## 三、架构边界图

```text
┌─────────────────────────────────────────────────────────┐
│                    Console 计算实例页                    │
│  VM · Container · GPU Container · Sandbox               │
│  创建向导 · 列表 · 详情 Tabs · 生命周期操作              │
└──────────────────────────┬──────────────────────────────┘
                           │ Core API /api/v1
                           ▼
┌─────────────────────────────────────────────────────────┐
│                       ANI Core                          │
│  /instances 统一实例契约                                │
│  创建准入: image_id / kind config 中的网络与存储引用      │
│  GPU Container: spec_id → GPUSpec 解析 → GPU 调度        │
│  Sandbox: token / port / file / checkpoint / code-run    │
└──────────────────────────┬──────────────────────────────┘
                           │ ports
                           ▼
┌─────────────────────────────────────────────────────────┐
│                     WorkloadRuntime                     │
│  Create · Get · List · Lifecycle · Console · Exec        │
│  不暴露 Kubernetes SDK，不表达 provider 细节             │
└──────────────┬────────────────────┬─────────────────────┘
               │                    │
               ▼                    ▼
┌──────────────────────┐  ┌───────────────────────────────┐
│ adapters/runtime      │  │ 已有依赖模块                   │
│ KubeVirt VM           │  │ Registry / Network / Storage   │
│ Kubernetes workload   │  │ GPU spec / Observability        │
│ Sandbox runtime/Kata  │  │                               │
└──────────────────────┘  └───────────────────────────────┘
```

**关键边界**:

- Core API 只表达实例控制面意图。
- Gateway handler 不直接依赖 K8s SDK，不拼 provider 对象。
- 镜像、网络、存储和 GPU 规格只通过引用字段进入实例创建。
- GPU 配额接口只预留编排位置；本期创建和删除实例不修改任何配额计数。
- Services 业务资源不得进入 Core 实例契约。

### 3.1 与既有模块的关联设计

实例 service 负责按产品顺序编排，Gateway handler 只解析请求。关联资源的 CRUD 和真实状态仍由各自模块负责:

| 模块 | 实例输入 | 创建前校验 | 实例侧保存 | 失败与删除规则 |
| --- | --- | --- | --- | --- |
| Registry | `image_id` | 镜像存在、租户可见、purpose 与 kind 匹配、架构兼容 | 不可变 image ID、digest、展示摘要 | 镜像不可用返回 422；实例删除不删除镜像 |
| Network | kind config 中的 `vpc_id/subnet_id/security_group_ids` | 同租户、Subnet 属于 VPC、安全组可绑定、地址可分配 | 网络资源 ID、实际 private IP、endpoint 摘要 | apply 前失败不创建 workload；实例删除只释放实例 IP/端点，不删除 VPC/Subnet/安全组 |
| Storage | `volume_id/filesystem_id` 或 VM 新盘声明 | 同租户、卷状态和访问模式可挂载、filesystem 有可用 mount target | attachment 与真实 Storage task 引用 | 轮询 Storage task；删除按 `delete_with_instance` 和所有权规则处理 |
| GPU Spec | `gpu_container_config.gpu.spec_id` | `GET /gpu-specs/{spec_id}` 存在、available、gpu_type 与 inventory 匹配 | `spec_id` 和解析后的 `gpu_type/shares/mb_per_share` 快照 | 规格无效返回 422；实例删除不删除规格 |

推荐创建顺序:

1. 校验 tenant、幂等键和 kind config。
2. 解析 Registry 镜像并固定 digest，避免创建过程中 tag 漂移。
3. 校验 Network 引用并生成网络意图，但尚不直接创建 provider 对象。
4. 校验/创建 Storage 资源并等待必要的 Storage task。
5. GPU Container 解析 `spec_id` 并调用既有 GPU inventory 做可调度性检查。
6. 预留 `GPUQuotaAdmission.Check(tenant_id, spec_id, requested)` 编排位置；本期为未启用状态，不新增 port、数据表或计数更新。
7. 调用 `WorkloadRuntime` apply，记录 operation steps 和 provider reference。
8. 失败时按资源所有权补偿；不得删除用户预先存在的镜像、网络或存储资源。

***

## 四、数据模型扩展

### 4.1 CreateInstanceRequest 扩展

```yaml
CreateInstanceRequest:
  properties:
    idempotency_key: string
    name: string
    description: string
    kind: vm | container | gpu_container | sandbox
    labels: object
    image_id: string
    image_ref: string
    cpu: string
    memory: string
    vm_config:
      $ref: '#/components/schemas/CreateVMInstanceConfig'
    container_config:
      $ref: '#/components/schemas/CreateContainerInstanceConfig'
    gpu_container_config:
      $ref: '#/components/schemas/CreateGPUContainerInstanceConfig'
    sandbox_config:
      $ref: '#/components/schemas/SandboxConfig'
    auto_start: boolean
    termination_protection: boolean
```

说明:

- `image_id` 优先；`image_ref` 仅用于兼容外部镜像引用。
- P0 一次请求只创建一个实例；批量启动/停止/重启/删除由 Console 对多个实例逐个提交 lifecycle 请求并汇总结果。
- 网络、磁盘、挂载放入对应 kind config，禁止顶层和 kind config 同时定义同一语义。
- 旧字段不删除，只标记 deprecated。

### 4.2 InstanceNetworkConfig

```yaml
InstanceNetworkConfig:
  properties:
    vpc_id: string
    subnet_id: string
    security_group_ids:
      type: array
      items: string
    assign_private_ip: boolean
    private_ip: string
```

说明:

- VPC/Subnet/SecurityGroup CRUD 不在本方案内。
- 未传时 adapter 可按平台默认网络兜底，但返回中必须明确实际绑定值。

### 4.3 InstanceRecord 通用响应扩展

```yaml
InstanceRecord:
  properties:
    description: string
    labels:
      type: object
      additionalProperties: string
    image:
      $ref: '#/components/schemas/InstanceImageSummary'
    compute:
      $ref: '#/components/schemas/InstanceComputeSummary'
    network:
      $ref: '#/components/schemas/InstanceNetworkSummary'
    access:
      $ref: '#/components/schemas/InstanceAccessSummary'
    storage_attachments:
      type: array
      items:
        $ref: '#/components/schemas/InstanceStorageAttachment'
```

字段职责:

- `image`: `id`、`ref`、`name`、`tag`、`purpose`、`architecture`；不得返回 Registry 凭据。
- `compute`: `cpu`、`memory`、`spec_id`、`availability_zone`、`node_name`。
- `network`: `vpc_id/name`、`subnet_id/name`、`security_groups[]`、`private_ip`、`endpoints[]`、`load_balancer_refs[]`。
- `access`: SSH/Console/Exec 的可用性摘要；实际短期 URL 仍通过 console/exec session API 获取。
- `storage_attachments`: 当前卷和文件系统挂载摘要，包含 `resource_type`、`resource_id`、`name`、`mount_path`、`read_only`、`status`。
- `container`、`gpu`、`sandbox` 沿用 kind-specific status，不把发布、GPU 排队、Sandbox 双超时塞入通用字段。

列表过滤新增:

- 通用: `kind`、`state`、`keyword`、`created_after`、`created_before`、`limit`、`cursor`、`sort`。
- VM: `spec_id`、`node_name`。
- Container: `image_id`、`node_name`、`rollout_status`。
- GPU Container: `gpu_model`、`queue_name`、`scheduling_state`。
- Sandbox: `template_id`、`session_state`。

状态映射:

| 原型展示状态 | 顶层 `state` | kind-specific 字段 |
| --- | --- | --- |
| VM 重启中/变配中 | `starting` / `provisioning` | `state_reason=Restarting/Resizing` |
| Container 部署中/更新中 | `provisioning` | `container.rollout_status=progressing` |
| GPU 排队中 | `pending` | `gpu.scheduling_state=queued`、`scheduling_reason` |
| Sandbox 已暂停 | `stopped` | `sandbox.session_state=paused` |
| Sandbox 已过期 | `stopped` | `sandbox.session_state=expired`、`stop_reason=TTL_EXPIRED/IDLE_EXPIRED` |

### 4.4 VM 配置

```yaml
CreateVMInstanceConfig:
  properties:
    network:
      $ref: '#/components/schemas/InstanceNetworkConfig'
    os_type: linux | windows
    ssh_username: string
    ssh_key_ref: string
    password_secret_ref: string
    user_data: string
    system_disk:
      $ref: '#/components/schemas/InstanceDiskSpec'
    data_disks:
      type: array
      items:
        $ref: '#/components/schemas/InstanceDiskSpec'
    filesystem_mounts:
      type: array
      items:
        $ref: '#/components/schemas/InstanceFilesystemMount'
```

`InstanceDiskSpec` 必须区分两种模式:

- 引用已有卷: `volume_id`。
- 声明新盘: `name`、`size_gib`、`volume_type`、`storage_class`、`encrypted`、`delete_on_failure`、`delete_with_instance`。

两种模式互斥；系统盘必须声明新盘或引用可启动卷。顶层 `image_id` 是唯一推荐镜像 ID，现有 `vm_config.boot_image` 仅保留兼容。

通用引用 schema:

```yaml
InstanceVolumeMount:
  required: [volume_id, mount_path]
  properties:
    volume_id: string
    mount_path: string
    read_only: boolean

InstanceFilesystemMount:
  required: [filesystem_id, mount_path]
  properties:
    filesystem_id: string
    mount_path: string
    read_only: boolean

InstancePortSpec:
  required: [container_port]
  properties:
    name: string
    container_port: integer
    protocol: tcp | udp

InstanceEnvVar:
  required: [name]
  properties:
    name: string
    value: string
    secret_ref: string
```

`value` 与 `secret_ref` 互斥；敏感值必须使用 `secret_ref`，响应中不回显 secret 明文。

VM 详情补充:

- `ssh`
- `snapshots`
- `console`
- `disks`
- `network`
- `operations`

### 4.5 Container 配置

```yaml
CreateContainerInstanceConfig:
  properties:
    network:
      $ref: '#/components/schemas/InstanceNetworkConfig'
    replicas: integer
    ports:
      type: array
      items:
        $ref: '#/components/schemas/InstancePortSpec'
    env:
      type: array
      items:
        $ref: '#/components/schemas/InstanceEnvVar'
    secret_ids:
      type: array
      items: string
    volume_mounts:
      type: array
      items:
        $ref: '#/components/schemas/InstanceVolumeMount'
    workload_identity:
      $ref: '#/components/schemas/InstanceWorkloadIdentityConfig'
    filesystem_mounts:
      type: array
      items:
        $ref: '#/components/schemas/InstanceFilesystemMount'
```

### 4.6 GPU Container 配置

```yaml
CreateGPUContainerInstanceConfig:
  properties:
    network:
      $ref: '#/components/schemas/InstanceNetworkConfig'
    ports:
      type: array
      items:
        $ref: '#/components/schemas/InstancePortSpec'
    env:
      type: array
      items:
        $ref: '#/components/schemas/InstanceEnvVar'
    secret_ids:
      type: array
      items: string
    volume_mounts:
      type: array
      items:
        $ref: '#/components/schemas/InstanceVolumeMount'
    filesystem_mounts:
      type: array
      items:
        $ref: '#/components/schemas/InstanceFilesystemMount'
    replicas: integer
    gpu:
      properties:
        spec_id: string
        vendor: string
        model: string
        count: integer
        allocation_mode: dedicated | vgpu
        workload_class: inference | training | batch
```

说明:

- `spec_id` 是新规格模式，引用 Core `GPUSpec.id`。
- `vendor`、`model`、`count`、`allocation_mode` 保留兼容，标记 deprecated。
- 本期根据规格解析 GPU 类型、份数和显存，并交给 GPU inventory/scheduler；不执行租户配额扣减。

### 4.7 Sandbox 配置与状态

```yaml
SandboxConfig:
  properties:
    template_id: string
    runtime_class: string
    session_timeout: string
    idle_timeout: string
    on_timeout: pause | kill
    network_egress_policy: deny_all | allowlist | internet
    egress_allowlist:
      type: array
      items: string
    env:
      type: array
      items:
        $ref: '#/components/schemas/InstanceEnvVar'
    initial_ports:
      type: array
      items:
        $ref: '#/components/schemas/SandboxPortSpec'
```

`SandboxInstanceStatus` 扩展:

- `template_id`
- `remain_seconds`
- `idle_remain_seconds`
- `on_timeout`
- `network_egress_policy`
- `egress_allowlist`
- `ports`
- `env`
- `checkpoints`
- `files_summary`
- `session_state`
- `agent_ref`
- `stop_reason`
- `connectivity`

`SandboxTemplate` 同步补齐 `image_id`、`image_ref`、`default_cpu`、`default_memory`、`default_session_timeout`、`default_idle_timeout`、`default_egress_policy`、`default_ports`、`available`。模板不可用或引用镜像 purpose 非 `sandbox` 时禁止创建。

### 4.8 GPUSpec 引用契约

`spec_id` 不是实例模块内部生成的字符串，而是 `/root/design/plan(2).md` 定义的 Core 集群级 `GPUSpec` 资源 ID。最小规格视图:

```yaml
GPUSpecSummary:
  required: [id, name, gpu_type, shares, mb_per_share, available]
  properties:
    id: string
    name: string
    gpu_type: string
    memory_total_mb: integer
    shares: integer
    mb_per_share: integer
    available: boolean
```

关联接口由 GPU 规格契约批次提供:

- `GET /gpu-specs`: Console 创建 GPU Container 时列出可选规格。
- `GET /gpu-specs/{spec_id}`: instance service 创建前解析并校验规格。
- 规格创建、删除和切片维护不属于实例模块。

依赖规则:

- 若 GPU 规格契约尚未批准和实现，`spec_id` 只能作为可选兼容字段进入实例契约，Console 不得声称已支持规格下拉闭环。
- 传 `spec_id` 时以规格解析结果为准；同时传旧 `vendor/model/count/allocation_mode` 且不一致时返回 400，不得静默忽略冲突。
- 未传 `spec_id` 时暂时保留旧 GPU 字段路径，待规格模式完成迁移后再通过独立兼容性方案处理。
- `GPUSpec` 只描述资源形态，不代表租户拥有配额。本期只做规格存在性和可调度性检查。
- 后续配额批次启用时，在既有实例准入步骤中接入原子 acquire/release；不得修改 `spec_id` 字段语义。

***

## 五、接口清单(Core API v1.yaml)

### 5.1 复用并扩展现有接口

| # | 方法 | 路径 | 用途 |
| --- | --- | --- | --- |
| 1 | GET | `/instances` | 列实例，增加过滤和排序 |
| 2 | POST | `/instances` | 创建 VM / Container / GPU Container / Sandbox |
| 3 | GET | `/instances/{instance_id}` | 查实例详情 |
| 4 | POST | `/instances/{instance_id}/lifecycle` | 生命周期操作 |
| 5 | POST | `/instances/{instance_id}/console` | VM console / VNC / serial |
| 6 | POST | `/instances/{instance_id}/exec` | Container / GPU / Sandbox 终端 |
| 7 | GET | `/instances/{instance_id}/logs` | 实例日志 |
| 8 | GET | `/instances/{instance_id}/events` | 实例事件，新增 cursor query |
| 9 | GET | `/instances/{instance_id}/metrics` | 实例指标 |
| 10 | GET | `/instances/{instance_id}/security-events` | Sandbox 安全事件，新增 cursor query |
| 11 | GET | `/instances/{instance_id}/operations` | 操作历史 |
| 12 | GET | `/sandbox-templates` | Sandbox 模板列表 |

### 5.2 新增 Sandbox 子资源接口

| # | 方法 | 路径 | 用途 |
| --- | --- | --- | --- |
| 13 | POST | `/instances/{instance_id}/sandbox/tokens` | 签发短期访问 token |
| 14 | POST | `/instances/{instance_id}/sandbox/ports` | 暴露预览端口 |
| 15 | DELETE | `/instances/{instance_id}/sandbox/ports/{port}` | 关闭预览端口 |
| 16 | GET | `/instances/{instance_id}/sandbox/files` | 列文件 |
| 17 | POST | `/instances/{instance_id}/sandbox/files` | 上传或写入文件 |
| 18 | DELETE | `/instances/{instance_id}/sandbox/files` | 删除文件 |
| 19 | GET | `/instances/{instance_id}/sandbox/checkpoints` | 列 checkpoint |
| 20 | POST | `/instances/{instance_id}/sandbox/checkpoints` | 创建 checkpoint |
| 21 | POST | `/instances/{instance_id}/sandbox/checkpoints/{checkpoint_id}/restore` | 恢复 checkpoint |
| 22 | POST | `/instances/{instance_id}/sandbox/checkpoints/{checkpoint_id}/clone` | 从 checkpoint 克隆 |
| 23 | POST | `/instances/{instance_id}/sandbox/code-runs` | 运行 Python / JS 代码 |

所有 Sandbox 子资源接口先校验实例属于当前 tenant 且 `kind=sandbox`；否则分别返回 404 或 422。POST body 必须包含 `idempotency_key`；DELETE 使用 `Idempotency-Key` header。

#### 5.2.1 Token

```yaml
CreateSandboxTokenRequest:
  required: [idempotency_key]
  properties:
    idempotency_key: string
    expires_in: string       # 默认 15m，最大 1h
    scopes:
      type: array
      items: { enum: [connect, exec, files, ports] }

SandboxTokenResponse:
  required: [token, expires_at, scopes]
  properties:
    token: string            # 仅本次响应返回，日志/任务/审计不得记录
    expires_at: date-time
    scopes: string[]
```

仅 `running` Sandbox 可签发；暂停、过期、销毁中返回 422。相同幂等键回放同一逻辑结果，但 token 明文不可持久化后再次回显时，服务必须返回原始创建响应的安全缓存或明确采用一次性 token 重签策略，契约阶段必须固定其中一种。P0 选择“同 key 在 token 有效期内回放原响应，过期后返回 409 IdempotencyResultExpired”。

#### 5.2.2 预览端口

```yaml
CreateSandboxPortRequest:
  required: [idempotency_key, port]
  properties:
    idempotency_key: string
    port: integer             # 1..65535
    name: string
    protocol: tcp | http

SandboxPort:
  required: [port, protocol, status, preview_url]
  properties:
    port: integer
    name: string
    protocol: tcp | http
    status: opening | available | closing | failed
    preview_url: string
    expires_at: date-time
```

端口不得创建 Kubernetes Ingress 作为产品语义；adapter 通过 Sandbox runtime 的短期 preview 能力实现。重复开放同一端口返回现有结果；关闭不存在端口返回 404。

#### 5.2.3 文件

```yaml
SandboxFileListResponse:
  required: [items, total]
  properties:
    items:
      type: array
      items:
        properties:
          path: string
          kind: file | directory
          size_bytes: integer
          updated_at: date-time
    total: integer
    next_cursor: string

WriteSandboxFileRequest:
  required: [idempotency_key, path]
  properties:
    idempotency_key: string
    path: string
    content_base64: string
    upload_id: string
    overwrite: boolean
```

- `GET /sandbox/files` query: `path`、`limit`、`cursor`。
- `content_base64` 与 `upload_id` 互斥；P0 小文件可 inline，大文件通过短期上传会话。
- DELETE query 必须携带 `path`，禁止 `..`、NUL、绝对路径越界和工作区外 symlink。
- 返回 413 表示大小超限，409 表示目标存在且 `overwrite=false`。

#### 5.2.4 Checkpoint

```yaml
CreateSandboxCheckpointRequest:
  required: [idempotency_key, name]
  properties:
    idempotency_key: string
    name: string
    keep_memory: boolean

SandboxCheckpoint:
  required: [id, name, status, keep_memory, created_at]
  properties:
    id: string
    name: string
    status: creating | available | restoring | failed | deleted
    keep_memory: boolean
    created_at: date-time
    size_bytes: integer
    reason: string
```

列表支持 `limit/cursor`。restore 只作用于原 Sandbox；clone 返回新的 `CreateInstanceResponse`，并要求新实例名称及独立 `idempotency_key`。不支持内存 checkpoint 的 runtime 在 `keep_memory=true` 时返回 422。

#### 5.2.5 Code run

```yaml
CreateSandboxCodeRunRequest:
  required: [idempotency_key, language, code]
  properties:
    idempotency_key: string
    language: python | javascript
    code: string
    timeout_seconds: integer  # 1..300
    stdin: string

SandboxCodeRun:
  required: [id, status, language, created_at]
  properties:
    id: string
    status: accepted | running | succeeded | failed | timed_out
    language: python | javascript
    stdout: string
    stderr: string
    exit_code: integer
    created_at: date-time
    completed_at: date-time
```

创建返回 202 + `Location: /api/v1/tasks/{task_id}`；任务完成结果包含 `SandboxCodeRun`。stdout/stderr 必须有大小上限并标记 `truncated`，运行成功后刷新 idle timer。代码、stdin、stdout/stderr 不进入普通审计日志。

***

## 六、生命周期动作

### 6.1 通用 action

```yaml
InstanceLifecycleAction:
  enum:
    - start
    - stop
    - restart
    - resize
    - rebuild
    - delete
    - snapshot
    - attach_volume
    - detach_volume
    - attach_filesystem
    - detach_filesystem
    - rollback
    - scale
    - update_image
    - bind_secret
    - unbind_secret
    - change_security_groups
    - set_termination_protection
    - pause
    - resume
    - extend
    - touch_idle
```

### 6.2 action 适用矩阵

| action | VM | Container | GPU Container | Sandbox |
| --- | --- | --- | --- | --- |
| start/stop/restart | 是 | 是 | 是 | 否 |
| resize | 是 | 是 | 是 | 否 |
| rebuild | 是 | 否 | 否 | 否 |
| snapshot/rollback | 是 | 否 | 否 | checkpoint 走子资源 |
| attach/detach volume | 是 | 是 | 是 | 否 |
| attach/detach filesystem | 是 | 是 | 是 | 否 |
| scale | 否 | 是 | 是 | 否 |
| update_image | 否 | 是 | 是 | 否 |
| bind/unbind secret | 否 | 是 | 是 | 否 |
| change_security_groups | 是 | 是 | 是 | 否 |
| set_termination_protection | 是 | 是 | 是 | 否 |
| pause/resume/extend/touch_idle | 否 | 否 | 否 | 是 |
| delete | 是 | 是 | 是 | destroy 语义 |

说明:

- 不可用 action 返回 `422 PRECONDITION_FAILED`。
- 终止保护打开时，`delete`、危险 `rebuild` 等操作返回 `409 CONFLICT`。
- `resize` 对 VM 要求 stopped；Container/GPU Container 可按 runtime 能力决定滚动更新或要求 stopped，返回中暴露 `precheck_result`。

### 6.3 Lifecycle request payload

沿用现有 `InstanceLifecycleRequest`，新增可选字段，不引入无类型 `params` map:

| action | 必填字段 | 可选字段 |
| --- | --- | --- |
| `resize` | `cpu`、`memory` | — |
| `snapshot` | `snapshot_name` | `include_data_disks` |
| `rollback` | `revision` 或 `snapshot_id` | — |
| `attach_volume` | `volume_id`、`mount_path` | `read_only` |
| `detach_volume` | `volume_id` | — |
| `attach_filesystem` | `filesystem_id`、`mount_path` | `read_only` |
| `detach_filesystem` | `filesystem_id` | — |
| `scale` | `replicas` | — |
| `update_image` | `image_id` | `strategy` |
| `bind_secret` | `secret_id`、`binding_type` | `env_name`、`mount_path` |
| `unbind_secret` | `secret_id` | — |
| `change_security_groups` | `security_group_ids` | — |
| `set_termination_protection` | `enabled` | — |
| `extend` | `duration` | — |
| `touch_idle` | 无 | — |

跨 action 字段冲突返回 400。例如 `scale` 带 `volume_id`、`resize` 缺 cpu/memory、`update_image` 的镜像 purpose 不匹配均不得静默忽略。`strategy` P0 enum 为 `rolling`；GPU/Container 回滚继续使用 `revision`。

Lifecycle 保留 `200 + InstanceLifecycleResponse + operation_id` 作为兼容同步 profile 和快速元数据操作响应。实例创建、启停/重启、变配、重建、删除、快照/回滚、扩缩容、镜像更新以及 Sandbox pause/resume/extend 等长操作返回 `202 + InstanceAsyncTask + Location`。`InstanceAsyncTask` 继承 `AsyncTask`，并要求在返回 202 前已分配 `instance_id` 和 `operation_id`；任务中心通过 `GET /tasks` 聚合展示，实例详情继续通过 `GET /instance-operations/{operation_id}` 展示业务步骤。

`AsyncTask` 和 `InstanceOperation` 不相互替代：前者负责统一执行状态、进度、取消和任务中心列表，后者负责实例维度的前置检查、资源编排、失败原因与步骤时间线。与 Storage 关联的 operation steps 至少包含 `storage_precheck`、`storage_task_wait`、`runtime_apply`、`observe`，并记录真实 Storage task ID。attach/detach volume/filesystem 返回 `volume.mount`、`volume.unmount`、`filesystem.mount`、`filesystem.unmount` 等真实任务类型，不复制或伪造为 `instance.*`。

错误语义:

- 400: action payload 缺失、冲突或格式错误。
- 404: 实例或被引用资源不存在/不属于当前租户。
- 409: 终止保护、重复冲突、资源当前状态不允许。
- 422: kind 不支持该 action、provider/runtime 能力不足、镜像/网络/存储/GPU 准入失败。

***

## 七、创建实例流程改造

### 7.1 VM 创建

输入:

- `name` / `description` / `labels`
- `image_id`（推荐）或兼容字段 `image` / `vm_config.boot_image`
- `cpu` / `memory`
- `vm_config.network`
- `vm_config.system_disk`
- `vm_config.data_disks`
- `vm_config.filesystem_mounts`
- `vm_config.ssh_key_ref` 或 `vm_config.password_secret_ref`
- `vm_config.user_data`
- `auto_start`
- `termination_protection`

准入:

- 镜像存在且 purpose 支持 VM/system。
- 多个镜像字段同时出现但解析结果不一致时返回 400。
- 网络引用存在且属于租户。
- 存储类型与容量合法。
- SSH key 或 password secret 至少一种满足登录方式。

### 7.2 Container 创建

输入:

- `name`
- `image_id`
- `cpu` / `memory`
- `container_config.replicas`
- `container_config.network`
- `container_config.ports`
- `container_config.env`
- `container_config.secret_ids`
- `container_config.volume_mounts`
- `container_config.filesystem_mounts`

准入:

- 镜像存在且 purpose 支持 container。
- volume mount 引用存在。
- Secret 引用存在。
- replicas 合法。

### 7.3 GPU Container 创建

输入:

- Container 通用字段
- `gpu_container_config.gpu.spec_id`
- `gpu_container_config.network/ports/env/secret_ids/volume_mounts/filesystem_mounts`

准入:

- 镜像存在且 purpose 支持 gpu。
- `spec_id` 对应的 `GPUSpec` 存在且 available。
- 规格的 `gpu_type` 与真实 GPU inventory 一致。
- 调度计划可找到满足规格的设备或切片。
- 本期不查询或扣减租户 GPU quota。

重复项处理:

- GPU spec、slice 数据结构和 API 由 `/root/design/plan(2).md` 负责，本方案只做引用。
- GPU quota 方案继续保留在该独立计划中，但不进入本期实例实现和验收。

### 7.4 Sandbox 创建

输入:

- `name`
- `sandbox_config.template_id`
- `cpu` / `memory`
- `sandbox_config.session_timeout`
- `sandbox_config.idle_timeout`
- `sandbox_config.on_timeout`
- `sandbox_config.network_egress_policy`
- `sandbox_config.egress_allowlist`
- `sandbox_config.env`
- `sandbox_config.initial_ports`

准入:

- template 存在且可用。
- egress allowlist 域名合法。
- timeout 在平台允许范围内。
- runtime_class 可用。

***

## 八、实例与存储联动规则

本章只定义实例侧如何引用和编排存储能力，不重新设计 Storage API。块卷、文件系统、快照、mount target 的 CRUD 与异步任务语义由存储模块负责。

### 8.1 创建实例时的存储输入

| 实例类型 | 创建输入 | 处理规则 |
| --- | --- | --- |
| VM | `vm_config.system_disk` | 必填；用于创建或引用系统盘，最终在 VM runtime 中作为 boot/root disk 绑定 |
| VM | `vm_config.data_disks[]` | 可选；支持引用已有 `volume_id` 或声明新数据盘规格 |
| VM | `vm_config.filesystem_mounts[]` | 可选；仅引用已有 filesystem |
| Container | `container_config.volume_mounts[]` | 可选；仅挂载已有 volume/filesystem，不在 Container 创建中隐式创建新存储资源 |
| Container | `container_config.filesystem_mounts[]` | 可选；引用已有 filesystem |
| GPU Container | `gpu_container_config.volume_mounts[] / filesystem_mounts[]` | 同 Container；GPU 规格调度与 storage mount 是两个独立准入 |
| Sandbox | `sandbox_config.initial_ports` / `env` | P0 不支持块卷或文件系统挂载；文件能力走 Sandbox files 子资源 |

VM 新建系统盘和数据盘时，实例创建 orchestration 可以调用 Storage port 生成存储意图，但 Gateway handler 不直接调用 Rook-Ceph、CSI 或 Kubernetes SDK。

### 8.2 实例生命周期与 Storage API 的映射

| 实例动作 | Storage 动作 | 任务类型 | 说明 |
| --- | --- | --- | --- |
| `attach_volume` | `POST /volumes/{volume_id}/mount` | `volume.mount` | 绑定卷到 VM / Container / GPU Container |
| `detach_volume` | `POST /volumes/{volume_id}/unmount` | `volume.unmount` | 从实例卸载卷 |
| `attach_filesystem` | `POST /filesystems/{filesystem_id}/mount` | `filesystem.mount` | P0 lifecycle action；挂载前必须存在可用 mount target |
| `detach_filesystem` | `POST /filesystems/{filesystem_id}/unmount` | `filesystem.unmount` | P0 lifecycle action |
| VM 快照 | `POST /volumes/{volume_id}/snapshots` | `volume.snapshot.create` | VM 快照可组合实例元数据快照 + 系统盘/数据盘快照 |
| 从快照恢复数据盘 | `POST /volumes/{volume_id}/snapshots/{snapshot_id}/create-volume` | `volume.create_from_snapshot` | 先生成新卷，再按 attach 规则挂载 |

实例 API 可以提供 `attach_volume` / `detach_volume` 作为用户入口，但执行时必须复用 Storage port 语义，返回的异步任务不能伪装为实例内部任务。

### 8.3 失败回滚与幂等

- 创建 VM 时如系统盘创建成功但 VM runtime 创建失败，默认保留系统盘并把实例 operation 标为 failed，避免误删用户数据；Console 提供清理建议。
- 创建 VM 时如数据盘声明为 `delete_on_failure=true`，才允许失败回滚删除该数据盘；默认 false。
- `attach_volume` / `detach_volume` 重试必须复用同一个 `idempotency_key`，由 Storage API 回放同一任务结果。
- 删除实例默认只卸载卷，不删除独立数据盘；只有显式 `delete_with_instance=true` 的数据盘才可随实例删除。
- 文件系统 mount target 是网络侧共享入口，不随单个实例删除。
- 新盘创建、Storage task 等待、runtime apply 任一步失败都写入同一个 instance operation timeline；重试复用原始实例 idempotency key，并派生稳定的 Storage idempotency key，禁止每次重试生成新 key。
- `delete_with_instance` 仅允许作用于实例创建时新建且所有权归该实例的卷；引用已有卷时必须为 false。

### 8.4 详情页展示归属

实例详情页的“卷与快照 / 存储挂载”Tab 聚合以下数据:

- `InstanceRecord.volumes[]`: 实例当前绑定的卷摘要。
- Storage `GET /volumes/{volume_id}`: 卷容量、状态、`snapshots_count`、OS 初始化状态。
- Storage `GET /volumes/{volume_id}/snapshots`: 卷快照列表。
- Storage filesystem mount 信息: 文件系统挂载路径、实例绑定、mount target 状态。
- `GET /instances/{instance_id}/operations`: 实例侧操作历史。
- Storage async task: 任务列表显示真实 `task_type`，例如 `volume.mount`、`filesystem.unmount`，并通过 `Location: /api/v1/tasks/{task_id}` 轮询。
- Instance async task: 创建和运行时长操作显示 `instance.create`、`instance.resize` 等真实类型，并通过 `operation_id` 下钻实例操作时间线。

### 8.5 明确不做

- 不在实例 API 中重新定义 Volume / Filesystem / Snapshot schema。
- 不让实例 handler 直接调用 storage provider SDK。
- 不把文件系统 mount target 生命周期绑定到单个实例。
- P0 不做 Sandbox 块卷/文件系统挂载。

***

## 九、Ports/Adapters 变更清单

### 9.1 Ports 层

优先复用现有 `WorkloadRuntime`。只有现有接口无法表达以下能力时才扩展:

| port | 变更 |
| --- | --- |
| `WorkloadRuntime` | 扩展 create request 中的 network/storage/image/spec 引用字段 |
| `WorkloadRuntime` | 扩展 lifecycle action 与 precheck result |
| `SandboxRuntime` 或同等现有边界 | 如当前没有 Sandbox 子资源表达能力，则新增最小接口 |
| `InstanceObservability` | 不新增；仅补 events/security-events cursor 契约 |

### 9.2 Adapters 层

| adapter | 职责 |
| --- | --- |
| KubeVirt VM adapter | VM 创建、启动、停止、重启、resize、console、snapshot 引用 |
| Kubernetes workload adapter | Container/GPU Container 创建、scale、update image、exec |
| Sandbox runtime adapter | Sandbox pause/resume/extend/token/ports/files/checkpoints/code-runs |
| Local adapter | 保持 local profile 可测，不声明 real provider ready |

### 9.3 不允许的实现

- Gateway handler 直接 import Kubernetes SDK。
- Gateway handler 拼 KubeVirt VirtualMachine 或 Kubernetes Deployment。
- Instance service 直接调用 Harbor、Rook-Ceph、Kube-OVN SDK。
- 为未来 Batch/Bare Metal/DPU 提前创建未使用 port。

***

## 十、批次拆分与执行步骤

### 10.1 原型功能到契约的完成矩阵

| 原型区域 | P0 能力 | 契约落点 | 后端责任 | Console 责任 |
| --- | --- | --- | --- | --- |
| 实例总览 | 四类实例列表、搜索、筛选、排序、分页 | `GET /instances` query + `InstanceRecord` 摘要 | 租户隔离、稳定 cursor、kind/state 映射 | 统一列表、批量选择、状态与关键规格列 |
| VM 创建 | 镜像、规格、网络、登录、系统盘、数据盘、文件系统 | `CreateInstanceRequest.vm_config` | Registry/Network/Storage 准入与编排 | 分步创建向导和引用资源选择器 |
| VM 详情 | 概览、监控、网络、磁盘与快照、Console、操作记录 | `InstanceRecord` + metrics/events/console/operations + Storage API | 聚合引用摘要，不复制资源真相 | Tabs 按需加载，异步任务轮询 |
| Container 创建 | 镜像、CPU/内存、副本、端口、环境变量、Secret、卷/NFS | `container_config` | Deployment/Service、Secret、存储挂载编排 | 表单校验、敏感值不回显 |
| Container 详情 | 概览、Pod/副本、日志、事件、指标、终端、网络、存储 | 实例观测接口 + `InstanceRecord` | rollout 和 endpoint 状态归一化 | 终端、日志流和更新操作 |
| GPU Container 创建 | 镜像、规格 `spec_id`、网络、环境、存储 | `gpu_container_config` | GPUSpec 解析、GPU 调度、DCGM 关联 | 规格选择、排队/失败原因展示 |
| GPU Container 详情 | GPU 使用率、显存、调度状态、日志、终端 | metrics + GPU kind status | GPU 状态映射 | GPU 指标和队列状态 |
| Sandbox 创建 | 模板、CPU/内存、双超时、超时动作、出网策略、环境、端口 | `sandbox_config` | Kata/runtime 准入、超时状态机 | 模板选择和策略配置 |
| Sandbox 详情 | Token、端口预览、文件、Checkpoint、代码运行 | Sandbox 子资源 API | 短期凭据、路径安全、限流、审计、任务 | 独立 Tabs 和执行结果展示 |
| 通用生命周期 | start/stop/restart/delete、变配、挂载、Secret/安全组 | lifecycle request + DELETE header | 前置检查、幂等回放、operation/task 状态 | 单项和批量动作、部分失败汇总 |

矩阵中的每一行都必须同时有 OpenAPI schema、handler 测试、service/adapter 测试和 Console 状态处理；缺少任一层不得标记为原型闭环完成。

### 10.2 阶段 1: 契约评审与批准

**前置依赖 GPU-SPEC-CONTRACT-A**

该批次属于 GPU 规格模块，不合入实例契约批次，但必须在启用 Console 规格选择前完成:

| 必要能力 | 验收 |
| --- | --- |
| `GPUSpecSummary` schema | 字段至少覆盖 `id/name/gpu_type/shares/mb_per_share/available` |
| `GET /gpu-specs` | Console 可分页列出 available 规格 |
| `GET /gpu-specs/{spec_id}` | instance service 可解析规格，404 与 unavailable 可区分 |
| GPU inventory 对齐 | `gpu_type` 必须来自真实 inventory，不接受实例请求自由生成 |

实例契约可以先增加可选 `spec_id`，但在上述依赖完成前，GPU Container 仍走兼容旧字段路径，不能把规格模式标记为完成。

**批次 INSTANCE-CONTRACT-A: 通用实例与四类创建契约**

| 变更 | 文件 |
| --- | --- |
| 扩展 `CreateInstanceRequest`，保留兼容字段并标记 deprecated | `repo/api/openapi/v1.yaml` |
| 扩展 `InstanceRecord` 的 image/compute/network/access/storage 摘要 | `repo/api/openapi/v1.yaml` |
| 扩展 VM / Container / GPU / `SandboxConfig` | `repo/api/openapi/v1.yaml` |
| 增加列表过滤、排序、cursor 与状态映射字段 | `repo/api/openapi/v1.yaml` |
| 扩展 lifecycle enum、动作 payload、operation step 和错误语义 | `repo/api/openapi/v1.yaml` |
| 为 events/security-events 增加 cursor query | `repo/api/openapi/v1.yaml` |
| 刷新 Go/Python SDK、TS schema、静态 API docs | 生成物目录 |

验收:

```bash
cd repo
python scripts/validate_yaml.py api/openapi/v1.yaml
python scripts/gen_sdk_alpha.py
python scripts/generate_api_docs.py
node frontends/console/scripts/gen-core-schema.mjs
make validate-openapi-spec
make validate-core-api-compatibility
make validate-instance-contracts
make validate-instance-lifecycle-ops
git diff --check
```

**批次 INSTANCE-SANDBOX-CONTRACT-A: Sandbox 子资源契约**

| 变更 | 文件 |
| --- | --- |
| token、ports、files、checkpoints、code-runs 路径与 operationId | `repo/api/openapi/v1.yaml` |
| 每个接口的 request/response、security、错误和分页语义 | `repo/api/openapi/v1.yaml` |
| code-run 的 `202 + AsyncTask + Location` 语义 | `repo/api/openapi/v1.yaml` |
| DELETE 幂等键统一使用 `Idempotency-Key` header | `repo/api/openapi/v1.yaml` |
| 刷新 SDK、TS schema 和 API docs | 生成物目录 |

验收命令与 `INSTANCE-CONTRACT-A` 相同，并增加 Sandbox 路径/operationId 的契约聚焦测试。

**契约批准关卡**

1. 两个契约批次只包含 OpenAPI、契约测试、生成物和必要流程文档，不混入 handler/service/adapter 实现。
2. 本地契约门禁通过后，在用户指定的 `main` 分支使用显式路径 stage，按 Conventional Commits 提交并 push 到 `origin main`。
3. 等待个人仓库 GitHub Actions 全绿，再创建或更新上游契约 PR。
4. 契约 PR 合入或获得明确批准前，后续实现批次保持未开始状态。
5. 契约批准后冻结字段名、错误语义和幂等 transport；实现发现缺口时回到独立契约 PR，不在 handler 中私自扩展。

### 10.3 阶段 2: Ports、Service 与状态持久化

**批次 INSTANCE-PORTS-A: 通用运行时意图**

| 变更 | 文件 |
| --- | --- |
| 扩展 `WorkloadRuntime` 的创建、查询、生命周期和 precheck 意图 | `repo/pkg/ports/...` |
| 用 Core 资源 ID 表达 Registry/Network/Storage/GPU 引用 | `repo/pkg/ports/...` |
| 定义 operation/task 关联，不泄漏 provider 对象 | `repo/pkg/ports/...` |
| 增加编译期接口守卫和聚焦单测 | `repo/pkg/ports/...` |

**批次 INSTANCE-SANDBOX-PORTS-A: Sandbox 最小能力边界**

仅当现有 `WorkloadRuntime` 无法表达 token、文件、checkpoint 和 code-run 时新增 `SandboxRuntime`；接口按产品意图拆分，禁止包装完整 Kubernetes/Kata SDK。

**批次 INSTANCE-SERVICE-A: 编排、幂等与状态**

| 变更 | 验收重点 |
| --- | --- |
| 创建准入和 kind config 互斥校验 | 镜像 purpose、租户资源归属、规格和网络/存储引用 |
| 实例幂等记录 | 同 key 同请求回放；同 key 不同 payload 返回 409 |
| 生命周期 precheck 与 operation timeline | 400/404/409/422 可区分，步骤和失败原因可查询 |
| Storage task 关联 | 保存真实 task ID/type/resource，轮询完成后推进 operation |
| 删除所有权规则 | 独立数据盘保留，实例所有且显式标记的卷才删除 |
| Sandbox 双超时与短期资源 | session/idle 过期、token TTL、checkpoint/code-run 状态 |

验收:

```bash
cd repo
go build ./pkg/ports/...
go test ./pkg/ports/...
go test ./pkg/... -run "Test.*Instance|Test.*Sandbox" -count=1
make validate-architecture
git diff --check
```

### 10.4 阶段 3: Gateway Handler

**批次 INSTANCE-HANDLER-A: 通用实例接口**

| 变更 | 文件 |
| --- | --- |
| 创建实例 handler 解析新字段 | `services/ani-gateway/internal/router/...` |
| lifecycle handler 支持新 action | `services/ani-gateway/internal/router/...` |
| 列表过滤、详情摘要和 operation/task Location | `services/ani-gateway/internal/router/...` |
| tenant、idempotency、参数与错误映射 | `services/ani-gateway/internal/router/...` |
| OpenAPI request/response 一致性测试 | `services/ani-gateway/internal/router/...` |

**批次 INSTANCE-SANDBOX-HANDLER-A: Sandbox 子资源接口**

覆盖 token、ports、files、checkpoints、code-runs 的成功、鉴权、越权、路径穿越、过期、幂等和限流场景；handler 只做 HTTP 转换，不直接访问 Pod、容器文件系统或 runtime SDK。

验收:

```bash
cd repo
go build ./services/ani-gateway/...
go test ./services/ani-gateway/internal/router/... -run "TestInstance|TestSandbox" -count=1
make validate-architecture
git diff --check
```

### 10.5 阶段 4: Adapters 与跨模块编排

**批次 INSTANCE-VM-ADAPTER-A**

- KubeVirt VM create/start/stop/restart/resize/console/status。
- Kube-OVN 网络绑定和 KubeVirt volume/filesystem 渲染。
- 系统盘可启动性、登录凭据引用和 provider 错误归一化。

**批次 INSTANCE-WORKLOAD-ADAPTER-A**

- Container/GPU Container Deployment、Service、Secret、volume/NFS mount、scale、update image、exec。
- GPU `spec_id` 到 GPUSpec 和现有 GPU inventory/scheduling 边界的引用。
- rollout、Pod、GPU 排队和失败状态回写。

**批次 INSTANCE-SANDBOX-ADAPTER-A**

- Kata runtime class、pause/resume/destroy、短期 token、端口代理、文件、checkpoint、code-run。
- 出网策略必须通过既有网络策略边界实现；不得由 Gateway 直接修改 NetworkPolicy。

**批次 INSTANCE-ORCHESTRATION-A**

| 变更 | 文件 |
| --- | --- |
| Registry 镜像解析/purpose 校验/pull secret 引用 | `repo/pkg/adapters/...` 与现有 port |
| Network VPC/Subnet/SecurityGroup 引用和 endpoint 状态 | 现有 Network port/adapter |
| Storage 创建、mount/unmount、filesystem mount-target 与 task 等待 | 现有 Storage port/adapter |
| GPUSpec 解析、GPU 可调度性检查和状态回写 | 现有 GPU port/adapter |
| GPU 配额准入接入位置 | 仅保留 service 编排步骤，本期不新增实现 |
| 跨步骤补偿、稳定派生幂等键、operation timeline | instance service/orchestrator |

**批次 INSTANCE-LOCAL-PROFILE-A**

同步 local adapter 以覆盖契约、状态机和错误路径；只声明 local 可测，不作为真实 provider 证据。

验收:

```bash
cd repo
go test ./pkg/adapters/... -run "Test.*Instance|Test.*Sandbox" -count=1
make validate-instance-provider-adapter
make validate-instance-orchestrator
make validate-instance-service
make validate-architecture
git diff --check
```

### 10.6 阶段 5: Console

**批次 INSTANCE-CONSOLE-VM-A**

- 统一列表中的 VM 筛选、状态、批量生命周期。
- VM 创建向导和镜像/网络/卷/NFS 选择。
- VM 详情概览、监控、网络、磁盘/快照、Console、操作记录。

**批次 INSTANCE-CONSOLE-CONTAINER-A**

- Container 创建、列表、详情、日志、事件、指标、终端、scale、update image。
- GPU Container 共用基础表单，额外提供 `spec_id`、排队状态和 GPU 指标。

**批次 INSTANCE-CONSOLE-SANDBOX-A**

- Sandbox 创建、双超时状态、暂停/恢复/销毁。
- Token、端口、文件、Checkpoint、代码运行 Tabs。
- Token/secret 只在允许时短暂展示，禁止进入持久化 store 或日志。

**批次 INSTANCE-CONSOLE-COMMON-A**

- operation/task 轮询、`Location` 跟踪、超时和失败详情。
- 批量动作逐实例提交并展示成功/失败汇总。
- 401/403/404/409/422/429 和空态、加载态、过期态。
- 原型中四类实例的响应式布局与权限控制。

验收:

```bash
cd repo/frontends/console
npx tsc --noEmit
npx vite build
npx vitest run
```

### 10.7 阶段 6: 真实端到端

**批次 INSTANCE-E2E-LIVE-A: live gate 定义**

新增 `deploy/real-k8s-lab/instance-management-live-gate.yaml`、执行脚本、脚本单测、脱敏 evidence schema 和 `make validate-instance-management-live-gate`。该门禁必须调用真实 Core API，不允许用直接 `kubectl apply` 代替产品路径。

部署边界:

- `ani-gateway` 和实例控制服务按当前开发拓扑运行在本地，通过 adapter 配置访问集群后端。
- 集群只部署 KubeVirt、Kubernetes workload、Kata、Kube-OVN、CSI/Rook-Ceph、GPU/DCGM 等 adapter 对接组件和测试工作负载。
- 不把 Gateway、Registry、Network、Storage 等所有服务塞进同一个 Dockerfile，也不以单容器进程编排替代服务边界。
- 如需更新实现，只重建受影响服务镜像并更新其既有部署；先确认镜像仓库可推送、节点可拉取、网络/DNS/证书连通，再开始业务调试。

| 闭环 | 验收 |
| --- | --- |
| 基础设施预检 | Registry pull、Gateway 到集群 API、Kube-OVN、StorageClass/CSI、KubeVirt、Kata、GPU 节点与 DCGM 均 ready |
| VM | API 创建镜像/网络/系统盘/数据盘/NFS → running → console → stop/start/resize → 快照 → delete，验证卷保留/删除规则 |
| Container | API 创建网络/Secret/卷/NFS → rollout → logs/events/metrics/exec → scale/update image → delete |
| GPU Container | `spec_id` 解析 → 可调度性检查 → queued/running → GPU metrics → delete |
| Sandbox | 创建 → token → expose port → files → checkpoint/restore/clone → code-run task 轮询 → timeout/pause/resume → destroy |
| 负向 | 跨租户引用、错误镜像 purpose、重复幂等键 payload 冲突、非法状态动作、无 mount target NFS、无效或不可调度 GPUSpec |
| 清理 | 删除临时实例、网络引用、测试卷/快照/checkpoint/secret；保留 evidence，不保留凭据 |

执行脚本必须:

1. 为每次运行生成唯一且可追踪的 tenant/resource 前缀。
2. 使用临时 Bearer token，从环境变量读取，禁止写入命令行记录和 evidence。
3. 对每个 `202` 读取 `Location`，轮询真实 `AsyncTask` 到终态并校验 `task_type/resource_type`。
4. 同时验证 API 返回、provider 实际对象和最终资源状态，不能只看 HTTP 2xx。
5. 失败时记录脱敏阶段、错误码和可复现资源 ID；成功或失败都执行幂等 cleanup。
6. evidence 包含 commit SHA、镜像 digest、集群基线、时间、步骤结果和脱敏资源引用。

验收:

```bash
cd repo
make validate-instance-management-live-gate
make test
make validate-architecture
git diff --check
```

只有四类实例的对应闭环实际通过，才能分别声明该 kind 的 real-provider gate 通过；不得由其中一种实例外推其余类型，更不得外推 full platform production ready。

### 10.8 每个实现批次的提交与记录

每个 Feature batch 都必须:

1. 先完成聚焦测试，再运行当前批次门禁、`make test`、`make validate-architecture` 和 `git diff --check`。
2. 更新 `repo/development-records/{BATCH-ID}.md`、`repo/development-records/README.md`、`repo/CURRENT-SPRINT.md`、`ANI-06-开发计划.md`。
3. 提交前检查 `.github/workflows/ci.yml`，按 job 边界完成本地 CI 等价验证。
4. 使用显式路径 stage，避免把其他模块未提交改动混入。
5. 在用户确认后提交并 push；等待个人仓库 Actions 全绿后再进入下一发运关卡。

***

## 十一、闭环验收标准

| 闭环动作 | 验收标准 | 依赖 |
| --- | --- | --- |
| 选择镜像 | Console 创建实例可按用途选择镜像 | Registry |
| 选择网络 | 创建实例可绑定 VPC/Subnet/SecurityGroup | Network |
| 选择存储 | VM 可创建系统盘/数据盘；VM/Container/GPU Container 可挂载卷和已有 NFS filesystem | Storage |
| 选择 GPU 规格 | `/gpu-specs` 可查询，GPU Container 可传 `spec_id` 并解析为真实调度参数 | GPU spec contract |
| 创建实例 | real profile 返回 `202 + InstanceAsyncTask + Location`，且包含 instance_id/operation_id；兼容同步 profile 可返回 `CreateInstanceResponse` | WorkloadRuntime / Task |
| 查询详情 | 四类 P0 实例详情字段满足原型 tabs | Core API |
| 生命周期 | 支持 P0 action 矩阵 | WorkloadRuntime |
| 异步操作 | 所有 `202` 返回真实 `AsyncTask` 和 `Location`，Console 可轮询到终态 | Task/Operation |
| 任务中心 | `GET /tasks` 支持租户内 cursor 分页与状态/类型/时间/实例筛选；仅 pending 任务可幂等取消 | Task |
| 观测 | logs/events/metrics/exec/console/security-events 可用 | Observability |
| Sandbox 子资源 | token/ports/files/checkpoints/code-runs 可用 | Sandbox runtime |
| 真实后端 | VM、Container、GPU Container、Sandbox 分别通过自身 real-provider live gate | Cluster adapters |

***

## 十二、关键风险与对策

| 风险 | 对策 |
| --- | --- |
| 与 GPU spec 方案重复 | 本方案只引用 `spec_id`，不定义 GPU spec CRUD |
| 配额延期后被误认为已生效 | 契约、Console 和 live evidence 明确标注本期不做 quota check/acquire/release |
| 与网络/存储/镜像方案重复 | 本方案只引用资源 ID，不重复 CRUD |
| lifecycle action 过大 | Sandbox 子资源拆独立接口，lifecycle 只保留通用状态变化 |
| OpenAPI 兼容性风险 | 只新增可选字段、可选 query、端点和 enum，不删除旧字段 |
| 旧镜像/网络/存储字段形成双写 | 明确 canonical 字段与冲突校验；旧字段只兼容读取并标记 deprecated |
| 跨 Registry/Network/Storage/GPU 步骤部分成功 | operation timeline 记录每一步，使用稳定派生幂等键，并按资源所有权执行补偿 |
| Storage task 与实例 operation 状态脱节 | 持久化真实 task ID/type/resource，轮询 Storage task 终态后再推进实例状态 |
| Sandbox token、secret 或内网信息泄露 | 短 TTL、响应不持久化、日志脱敏、evidence 禁止记录凭据和完整内网端点 |
| NFS mount target 不可用导致实例卡住 | 创建和 attach 前执行 precheck，返回可区分的 409/422，不进入 provider apply |
| handler 绕过 port | architecture gate 必须覆盖 Gateway 不直接依赖 K8s SDK |
| Console 先于后端实现 | 前端批次排在契约、ports、handler、adapter 之后 |
| 单一 happy-path 冒充端到端完成 | live gate 同时校验 API、真实 provider 对象、任务终态、负向场景和 cleanup |
| 真实环境误声明 production ready | live gate 只声明当前闭环通过，不外推 full platform production ready |

***

## 十三、本次不做

- GPU spec / slice 的管理实现，以及 GPU quota API、数据模型和扣减逻辑。
- Registry 镜像仓库管理。
- VPC / Subnet / SecurityGroup CRUD。
- Volume / Filesystem / Snapshot CRUD。
- Loki / Prometheus / DCGM 采集底层改造。
- Batch Job 完整创建与调度。
- Bare Metal / DPU 完整生命周期。
- Services 业务资源。
- 新增后台 controller。
- full platform production ready 声明。

***

## 十四、确认后下一步

确认本设计后，按 `ani-core-platform` API-first 流程依次进入 `INSTANCE-CONTRACT-A` 和 `INSTANCE-SANDBOX-CONTRACT-A`:

1. 只修改 `repo/api/openapi/v1.yaml`、契约测试和必要生成物，不混入实现代码。
2. 运行 SDK/API docs/TS schema 生成命令和本地契约门禁。
3. 按用户要求在 `main` 分支提交并 push，等待个人仓库 GitHub Actions 通过。
4. 契约 PR 合入或明确批准后，再按本文批次顺序进入 ports/service、handler、adapter/orchestration、Console 与真实端到端。
