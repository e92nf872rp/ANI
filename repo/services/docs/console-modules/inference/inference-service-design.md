# ANI 推理服务完整设计方案

> 模块：ANI Services · 推理服务
> 状态：目标设计，尚未表示已实现或 production ready
> 日期：2026-08-13
> Services 权威契约：`repo/api/openapi/services/v1.yaml`
> Core 跨层权威契约：`repo/api/openapi/v1.yaml`

本文只定义推理服务自身：产品资源、控制面、可靠协调、Core 工作负载适配和推理引擎。模型服务、统一入口、调用网关、模型路由、计量和底层基础设施的内部方案不属于本文；它们不能反向改变推理服务主资源语义。本文不是 OpenAPI 的替代品；正式字段、路径、返回码和错误响应始终以合入后的对应 OpenAPI 为准。

---

## 1. 执行结论

ANI 将推理服务实现为独立的 Services 业务资源：

- 用户管理的是模型端点，不是 VM、容器或 GPU 实例。
- `inference-service` 是推理服务资源、状态机和操作的唯一权威。
- Services 只通过 Core OpenAPI / Core SDK 申请基础设施，禁止调用 Core Go port、内部 gRPC 或 Kubernetes API。
- Core 新增通用的、仅服务身份可用的 `platform-workloads` 契约；它表达平台工作负载，不包含模型、vLLM、推理策略等 Services 语义。
- 推理引擎通过 ClusterIP 暴露集群内 `runtime_endpoint`，仅供控制面健康检查和受控测试使用，不对集群外开放。
- P0 不建设调用网关，不发布稳定公网 `invocation_url`，也不实现按模型选择后端的路由；相关响应字段作为兼容保留字段返回 null。
- `inference-service + PostgreSQL operation/reconciler` 是唯一 Services 控制面权威；旧 inference gRPC、InferenceService CRD 和 operator 不再参与 P0 runtime/status 收敛。
- P0 的资源与 placement 创建后不可变；只允许修改独立副本数。跨节点 GPU 固定 `replicas=1`，避免在 P0 引入动态 per-group PodGroup controller。
- 所有异步动作由 PostgreSQL 权威状态、持久操作表和 reconciler 驱动，进程重启后必须继续收敛。
- P0 同时交付三种 runtime shape：CPU Deployment、单节点 GPU Deployment、跨节点 GPU LeaderWorkerSet；跨节点使用版本化 TP/PP execution plan 和 Volcano gang scheduling，不另建推理服务资源类型。

### 1.1 推荐架构

```text
API Caller
    │ /api/v1/svc/inference-services*
    ▼
┌──────────────────────────────────────┐
│          inference-service           │
│ resource / operation / reconcile /   │
│ reconciler / engine health           │
└───────┬──────────────┬───────────────┘
        │              │
        │ version      │ Core OpenAPI
        ▼              ▼
 ModelCatalog      PlatformWorkload
    port               port
        │              │
        ▼              ▼
 catalog adapter   Core platform-workloads
                       │
                       ▼
                  ClusterIP Service
                       │
                       ▼
                  vLLM runtime
              （仅集群内健康检查/测试）
```

### 1.2 为什么不直接复用租户实例

Core `/instances` 是租户 IaaS 产品，实例列表、详情和生命周期都表达“用户拥有一台机器”。推理服务是 PaaS 端点，用户只关心模型、容量、健康状态和调用地址。

将推理 Pod 注册成普通实例会带来三个问题：

1. 推理内部负载进入租户实例列表，破坏产品语义。
2. Services 需要知道实例类型、存储、网络和 provider 细节，边界逐步泄漏。
3. 推理副本、滚动伸缩与实例生命周期无法稳定一一对应。

因此使用独立的通用 `platform-workloads` Core 契约，而不是给 `/instances` 增加隐藏标签后依赖查询过滤。

---

## 2. 目标、范围与非目标

### 2.1 P0 目标

P0 必须形成以下可复跑闭环：

1. 当前租户选择一个 `ready` 的不可变模型版本。
2. 创建推理服务并立即获得 `202 + InferenceService`。
3. 服务从 `pending` 收敛到 `deploying`，最终进入 `running` 或带明确原因进入 `failed`。
4. `running` 表示集群内 runtime 已就绪且真实推理请求通过；内部工作负载不出现在 `/instances`。
5. 控制面通过受信的 `runtime_endpoint` 完成集群内调用测试，但不向租户返回该地址。
6. 日志、状态原因、期望副本和就绪副本可观测。
7. 副本伸缩、停止、启动、重启和删除均支持幂等与进程重启恢复。
8. CPU、单节点 GPU 和跨节点 GPU 使用同一资源与状态机，由内部 execution plan 选择 Deployment 或 LeaderWorkerSet。
9. `running` 只在当前 generation 的全部期望副本/group 就绪且真实推理 smoke 通过后成立。

### 2.2 P0 包含

- 推理服务列表、创建、详情、副本伸缩和删除。
- 启动、停止和重启。
- PostgreSQL 持久化、操作队列、幂等和 reconciler。
- 经 Core `platform-workloads` 创建 Deployment、Service、存储挂载和 GPU 请求。
- vLLM OpenAI-compatible Chat 服务。
- 日志、健康检查、基础请求指标和调用测试。
- 现有访问策略路径仅保留契约兼容，不属于 P0 实现与 ready 条件。
- CPU、单卡、单节点多卡 GPU。
- 跨节点 LeaderWorkerSet、vLLM 分布式 TP/PP 和 Volcano PodGroup gang scheduling。

### 2.3 P0 不包含

- 自动扩缩容、灰度、蓝绿、流量拆分和多版本路由。
- Prompt 缓存、语义缓存、Prompt Guard 和内容审核。
- 公有云模型 provider 聚合。
- 训练、微调、RAG、OCR 和知识库业务。
- 把完整 Kubernetes、Volcano、LWS 或 vLLM 参数暴露给 API 客户端。
- 公网/租户调用网关、稳定 `invocation_url`、按模型路由、网关鉴权、限流和计量。
- 资源类型、CPU/内存、accelerator 或 placement 的在线变更与自动回滚。
- 跨节点多个 LWS group；P0 每个跨节点服务固定一个 group。

### 2.4 P1 扩展

P1 可在不改变主资源身份的前提下增加：

- 动态选择 TP/PP/DP/EP 的高级优化和异构 GPU 拓扑。
- 跨节点多个 LWS group、弹性扩缩、故障域感知放置和多集群推理。
- 自动扩缩容与更完整的流量策略。
- Embeddings/TEI 等其他引擎类型。

---

## 3. 强制边界

### 3.1 产品与代码归属

| 能力 | 权威归属 | 禁止事项 |
|---|---|---|
| 推理服务资源、状态机、操作 | ANI Services / `inference-service` | 写入 Core API 或 Core domain |
| 模型及不可变版本 | `ModelCatalog` dependency port | Core 解析模型业务状态 |
| 平台工作负载 | ANI Core | 出现 vLLM、模型版本、推理策略等字段 |
| GPU 发现与调度 | ANI Core / `GPUInventory` | Services 直接调用 device-plugin/Volcano |
| K8s、存储、网络和 Secret | ANI Core adapters | Services import K8s/MinIO/Harbor SDK |
| runtime/status 收敛 | `inference-service` reconciler | Operator/CRD 与 Services 双写状态 |

### 3.2 跨层调用规则

- `inference-service → Core`：只使用由 `repo/api/openapi/v1.yaml` 生成的 Core SDK。
- `inference-service → ModelCatalog`：只使用本方案定义的 dependency port，不读取依赖方数据库，也不把依赖方内部方案带入推理服务。
- 任意 API ingress → `inference-service`：只使用 Services API；入口组件不得直接读写推理表。
- `inference-service → Kubernetes/Volcano/LWS/Harbor/MinIO`：禁止。
- Core 返回的 provider ID、Pod 名和集群内地址仅作内部运行引用，不进入租户资源契约。
- 旧 `InferenceServiceRPC`、生成物和 `InferenceService` CRD 只能作为待迁移资产，不能成为 Services 绕过 Core OpenAPI 的实现路径。
- Services OpenAPI 中未绑定任何 path 的旧 `InferenceEndpoint/CreateInferenceEndpointRequest` schema 不是第二种产品资源；契约 PR 应按兼容策略移除或标记 deprecated，禁止新增引用。

### 3.3 服务身份

`platform-workloads` 不是普通租户 API。调用必须同时具备：

- 已认证的平台服务身份；
- 当前租户上下文；
- `platform-workloads:read` 或 `platform-workloads:write` scope；
- 可审计的 `request_id` 和调用服务标识。

Core OpenAPI 中所有相关 operation 必须标记 `x-ani-exposure: internal`。部署层不得通过租户或公网 Ingress 发布这些路径；`inference-service` 只能通过集群内部 Core endpoint 调用。普通租户 JWT/API Key 即使从内部网络访问并知道路径也必须返回 `403`，跨租户资源仍按产品安全约定返回 `404`。

---

## 4. 组件职责

### 4.1 `inference-service`

负责：

- InferenceService CRUD 与租户唯一性。
- 模型版本解析和 ready 前置校验。
- 幂等记录、操作持久化和 request hash 冲突检测。
- desired state、generation 和状态机。
- 统一资源规格规范化、模型 execution profile 选择和 engine 参数生成。
- 调用 Core SDK 创建、观察、伸缩和删除平台工作负载。
- 引擎健康检查与内部 endpoint 验证。
- 周期 reconciler、失败重试和孤儿回收。

不负责：

- 生成原生 Kubernetes/Volcano/LWS 对象。
- 保存明文模型密钥或用户 API Key。
- 承载生产推理请求正文。
- 实现模型下载、对象存储或 GPU 调度 provider。
- 创建、读取或更新 InferenceService CRD，也不接受 operator 回写业务状态。

### 4.2 Core `platform-workloads`

Core 提供 provider-neutral 的平台工作负载能力：

- 规格准入和能力检查。
- 通过 `WorkloadRuntime`、`GPUInventory`、renderer/admission/audit/dry-run/apply 边界落地。
- 按通用 topology 创建 Deployment 或 LeaderWorkerSet，并管理 Service、PodGroup、网络、存储、Secret binding 和 GPU 资源请求。
- 返回规范化状态、内部 endpoint、就绪副本和失败原因。
- 提供生命周期、日志和删除。

Core 不理解模型 ready、served model、OpenAI、RPM 或推理业务状态。

### 4.3 `ModelCatalog`

- 这是由 inference-service 定义并使用的依赖端口；本文只冻结推理服务所需输入输出语义，不指定依赖方产品或内部实现。
- 根据 `model_version_id` 返回当前租户不可变版本。
- 确认模型及版本可部署，返回 capabilities、format、size 和 Core object/artifact reference。
- 返回 checksum/digest、runtime compatibility 和推荐 engine profile；创建时形成不可变快照，后续 catalog 变化不改写已有服务。
- 加密模型只返回 Key reference，不返回明文密钥。
- 模型后续新版本不改变已经部署服务所钉死的版本。

### 4.4 vLLM runtime

- P0 Chat 引擎固定使用经过平台验证的版本化镜像，不使用 `latest`。
- GPU 与 CPU 使用不同镜像和资源模板。
- 监听内部端口 8000，提供 `/health` 和 OpenAI-compatible API。
- 只创建 ClusterIP，不暴露 NodePort/LoadBalancer/Ingress；NetworkPolicy 仅允许 inference-service 健康检查/测试和必要的平台观测流量访问。

---

## 5. Core `platform-workloads` 目标契约

> 本节是待进入 `repo/api/openapi/v1.yaml` 的设计输入，不表示路径当前已经存在。必须先提交并批准 Core 契约 PR，再实现 handler、port/adapter 接线和 SDK。

### 5.1 路径

| 方法 | 路径 | 语义 | 成功响应 |
|---|---|---|---|
| GET | `/api/v1/platform-workload-capabilities` | 获取通用 runtime shape 与 gang 能力 | `200 + PlatformWorkloadCapabilities` |
| POST | `/api/v1/platform-workloads` | 创建平台内部工作负载 | `202 + AsyncTask` |
| GET | `/api/v1/platform-workloads/{workload_id}` | 获取规范化状态 | `200 + PlatformWorkload` |
| PATCH | `/api/v1/platform-workloads/{workload_id}` | P0 只修改副本数 | `202 + AsyncTask` |
| POST | `/api/v1/platform-workloads/{workload_id}/lifecycle` | start/stop/restart | `202 + AsyncTask` |
| DELETE | `/api/v1/platform-workloads/{workload_id}` | 删除工作负载 | `202 + AsyncTask` |
| GET | `/api/v1/platform-workloads/{workload_id}/logs` | 读取规范化日志 | `200 + WorkloadLogListResponse` |

所有 POST 和有副作用的 PATCH 都必须包含 `idempotency_key`。新建的 DELETE 路径统一要求 `Idempotency-Key` header。所有 `202` 遵循 Core 既有规则返回 `AsyncTask`；创建任务的 `resource_id` 在接收请求时即确定，调用方用它查询 `PlatformWorkload`。

Core `AsyncTask.task_type` 同步增量加入 `platform_workload.create/scale/start/stop/restart/delete`，`resource_type` 加入 `platform_workload`。这是新资源的兼容性扩展，不复用 `inference.deploy`，避免 Core 接管 Services 业务任务语义。

Core lifecycle 语义必须固定：`stop` 删除 provider 侧 Deployment/LWS、Service 和 PodGroup 并释放计算资源，但保留 PlatformWorkload 数据记录、ID 与 applied spec；`start` 在同一 workload ID 下重建 provider runtime；`delete` 才删除业务可见的 PlatformWorkload，并通过内部 tombstone 完成最终回收。这样 Deployment 与 LWS 使用同一停止语义，不依赖把副本或 group 缩到零。

能力响应保持 provider-neutral，至少包含 `supported_topology_modes`、LWS/Volcano controller readiness、可用 accelerator `spec_id` 以及每种规格的单节点最大可准入数量。它只用于生成候选 execution plan；最终创建准入仍由 Core 原子校验，调用方不能把一次能力查询当成资源预留。

P0 必须做实时容量准入，但不在 inference-service 内另造租户 CPU/GPU 硬配额系统。若 Core 后续提供正式 quota enforcement，PlatformWorkload 与其他 Core workload 使用同一准入结果；在该契约合入前，本文不伪造 quota 字段或 `QUOTA_EXCEEDED` 成功路径，也不让外部配额方案改变 InferenceService 资源语义。

### 5.2 创建请求

```yaml
idempotency_key: uuid
name: string
workload_class: inference
runtime_kind: container
image_ref: registry artifact reference
command: [string]
args: [string]
replicas: integer
resources:
  cpu: string
  memory: string
  accelerator:                # CPU 规格省略整个对象，不传 null 或 type=cpu
    spec_id: string            # Core GPUSpec ID
    count: integer
topology:
  mode: single_node|leader_worker
  profile_id: string
  profile_version: string
  leader:                      # 仅 leader_worker
    resources:
      cpu: string
      memory: string
      accelerator:
        spec_id: string
        count: integer
  workers:                     # 仅 leader_worker
    count: integer
    resources:
      cpu: string
      memory: string
      accelerator:
        spec_id: string
        count: integer
scheduling:
  queue_class: inference
  gang: boolean                # leader_worker 时固定 true；minMember/resources 由 Core 派生
network:
  exposure: cluster_internal
  ports:
    - name: http
      port: 8000
artifacts:
  - object_ref: string
    mount_path: /models
secret_bindings:
  - secret_ref: string
    mount_path: string
health_check:
  protocol: http
  path: /health
  port_name: http
metadata:
  owner_ref: string             # 调用方提供的 opaque correlation ref
  labels: map[string]string
```

Core 契约只表达通用 workload intent。`topology.profile_id/version` 引用双方冻结的通用 pod 拓扑版本，不包含模型 ID、vLLM 参数或推理业务配置；TP/PP/Ray 等引擎参数由 inference-service 的 engine profile 生成启动参数。单节点模式使用顶层 `resources`；leader-worker 模式必须使用 role resources，顶层 resources 仅保存 group 汇总快照，不参与 Pod request 渲染。

调用方不得填写 PodGroup `minMember/minResources`、Volcano queue 名、schedulerName 或 LWS 原生字段。Core 根据 role 数量与 requests 派生一个 group 的 gang 约束，并验证 queue class。P0 `leader_worker` 只接受 `replicas=1`；收到更大值返回 `422 UNSUPPORTED_TOPOLOGY`。后续若支持多个 group，必须先增加按 group reconcile PodGroup 的 Core controller，不能由静态 renderer 伪造。

`image_ref` 必须是批准镜像仓库中的 digest；`command/args` 必须匹配 `workload_class + topology.profile_id/version` 的 admission policy。Core 拒绝 tag/`latest`、保留 label 覆盖、跨租户 secret/artifact 和未批准命令，避免把该接口变成任意容器执行面。`owner_ref` 对 Core 是不解析的关联值；provider 资源的真正 owner 始终是 Core `PlatformWorkload`。

### 5.3 响应字段

```yaml
id: uuid
tenant_id: uuid
state: pending|provisioning|running|starting|stopping|stopped|failed|deleting|deleted
generation: integer
observed_generation: integer
desired_replicas: integer
ready_replicas: integer
runtime_shape: deployment|leader_worker_set
topology_profile_id: string
topology_profile_version: string
internal_endpoint: uri|null
reason: string|null
message: string|null
created_at: date-time
updated_at: date-time
```

`internal_endpoint` 只对授权服务身份返回。它永远不得被 `/instances`、Services API 客户端或租户 SDK 列表暴露。

`desired_replicas/ready_replicas` 的单位必须一致：Deployment 模式表示 Pod 副本，LeaderWorkerSet 模式表示 leader-worker group。Core 直接使用 LWS group 级 `status.readyReplicas`，不得把 leader/worker Pod 数相加后返回。

### 5.4 隔离保证

- `GET /instances` 只查询租户实例资源，不查询 `platform_workloads` 表或 owner 类型。
- 两类资源使用不同 API、store 查询接口和 RBAC scope，不能靠 label 过滤实现隔离。
- Core provider 资源必须带 `ani.owner_type`、`ani.owner_id`、`ani.tenant_id` 等保留标签；调用方不能覆盖保留键。
- Services 关联标签使用独立命名空间，例如 `services.ani.io/inference-service-id`；Core 只校验格式和透传，不解释其业务含义。
- 删除租户时，Core 可按 tenant + owner 执行平台工作负载级联回收，但不把资源投影为实例。

---

## 6. Services InferenceService 目标契约

> 已存在的路径继续兼容。新增字段和路径必须先修改 `repo/api/openapi/services/v1.yaml`、生成物与语义门禁，并按 API-first 独立契约 PR 合入后再实现。

### 6.1 路径矩阵

| 方法 | 路径 | 状态 | 成功响应 |
|---|---|---|---|
| GET | `/api/v1/svc/inference-services` | 已有 | `200 + {items}` |
| POST | `/api/v1/svc/inference-services` | 已有 | `202 + InferenceService` |
| GET | `/api/v1/svc/inference-services/{service_id}` | 已有 | `200 + InferenceService` |
| PATCH | `/api/v1/svc/inference-services/{service_id}` | 待补契约 | `202 + AsyncTask` |
| DELETE | `/api/v1/svc/inference-services/{service_id}` | 已有 | `202 + AsyncTask` |
| POST | `/api/v1/svc/inference-services/{service_id}/lifecycle` | 待补契约 | `202 + AsyncTask` |
| GET | `/api/v1/svc/inference-operations/{operation_id}` | 待补契约 | `200 + AsyncTask` |
| GET | `/api/v1/svc/inference-services/{service_id}/logs` | 已有 | `200 + InferenceServiceLogListResponse` |
| POST | `/api/v1/svc/inference-services/{service_id}/test` | 已有 | `200 + InferenceTestResponse` |
| GET | `/api/v1/svc/inference-services/{service_id}/policies` | P1 待新增 | `200 + InferenceServicePolicies` |
| PUT | `/api/v1/svc/inference-services/{service_id}/policies` | 已声明、P1 实现 | `200 + InferenceServicePolicies` |

列表后续以可选 `limit`、`cursor`、`status`、`model_version_id` 查询参数增量演进，不改变现有无参数调用。

现有 DELETE 没有幂等键字段，v1 不通过新增必填参数破坏兼容性。服务以 `service_id + desired_state=deleted` 去重：删除进行中重复请求返回同一任务；删除完成后按现有资源语义返回 404。其他新增写操作继续显式要求 `idempotency_key`。

Services `AsyncTask.task_type` 使用 `inference_service.create/scale/start/stop/restart/delete`，`resource_type` 为 `inference_service`。创建接口为保持现有契约仍返回 InferenceService，但响应新增 `current_operation_id`；其他异步动作返回 AsyncTask，并可通过 inference-operations 查询。operation 查询必须校验 tenant + service ownership，不能把 Core task 暴露给租户。

### 6.2 创建请求

目标请求：

```yaml
idempotency_key: uuid          # 必填
name: string                   # 必填，租户内活动资源唯一
model: string                  # v1 现有必填兼容字段，传稳定版本 UUID
model_version_id: uuid         # 新增可选强类型字段，与 model 指向同一版本
image_id: string               # 可选，镜像仓库 Registry 镜像 ID；与 image_ref 至少填一个，同时传优先 image_id
image_ref: string              # 可选，用户直接输入的镜像引用；与 image_id 至少填一个
served_model_name: string      # 可选，默认使用 name；创建后不可变，供 vLLM 请求 model 字段使用
replicas: integer              # 默认 1，P0 >= 1
resources:                     # 统一资源规格
  cpu: string                 # 必填，每个单节点 Pod 或跨节点 group 的 CPU 预算
  memory: string              # 必填，每个单节点 Pod 或跨节点 group 的内存预算
  accelerator:                # 可选；省略表示规格不含 accelerator
    spec_id: string           # Core GPUSpec ID；不接受 cpu/null 作为类型
    count_per_replica: integer # 一个模型副本所需 accelerator 总数
placement_mode: string         # auto|single_node|multi_node，默认 auto
```

兼容规则：

- 为避免改变现有 required/generated SDK，v1 继续要求 `model`；新客户端同时传 `model=<version UUID>` 与 `model_version_id`。
- `image_id` 与 `image_ref` 都是可选创建字段，两者至少填一个：`image_id` 从镜像仓库选择，`image_ref` 由用户直接输入；同时传入时优先 `image_id`。创建前固定 digest。进程环境中的平台默认引擎镜像不是创建路径权威来源。
- 两者都缺时返回 `400 INVALID_ARGUMENT`；选定或输入的镜像无法解析为 digest 时返回 `422 IMAGE_UNAVAILABLE`。
- 旧客户端只传 `model` 时，服务必须立即解析并落库 `model_version_id`；旧 `name:version` 仅作为兼容输入。
- 两者同时存在时必须指向同一版本，否则返回 `409 IDEMPOTENCY_CONFLICT` 或 `400 INVALID_ARGUMENT`，取决于是否发生在幂等重放。
- 响应继续保留 `model` 作为展示快照，调度与幂等指纹只使用 `model_version_id`。
- `served_model_name` 默认为服务 name，必须符合 vLLM model name 约束并在租户活动服务内唯一；它不代表公网路由已存在。
- 新客户端只提交统一 `resources`；推理服务不再要求调用方声明 `cpu/gpu` workload 类型。
- `resources.accelerator` 省略时，execution plan 使用 CPU Deployment；对象存在时通过生成的 Core SDK 调用 Core OpenAPI 的 GPUSpec/能力契约解析 `spec_id`，并按 count 申请 accelerator。Services 禁止直接调用 Core Go `GPUSpecService` port。规格不存在、不可用或不匹配时失败，禁止静默改写规格。
- 现有 OpenAPI 的 `gpu_type`、`gpu_count_per_pod` 仅作为 v1 兼容输入保留并标记 deprecated。adapter 将其规范化为 `resources.accelerator`；`gpu_type` 缺失时不能因为 `gpu_count_per_pod` 的历史默认值推断为 GPU。
- 现有 `max_concurrency` 作为 deprecated 兼容输入保留但 P0 不执行；新客户端不得发送。后续必须在 runtime capacity 与 gateway limit 之间选择单一语义后再启用。
- 兼容字段与 `resources` 同时传入时必须表达同一规格，否则返回 `400 INVALID_ARGUMENT`。
- `placement_mode=auto` 时，由规范化资源规格、Core 能力和模型 execution profile 决定单节点 Deployment 或 LWS。
- `placement_mode=single_node` 但单节点容量不足，或 `multi_node` 前置条件不满足时返回 422，不得改变用户请求的 placement。

P0 规范化与 shape 决策必须覆盖以下表格：

| 输入 | 规范化结果 | 结果 |
|---|---|---|
| 无 `resources.accelerator`，`placement_mode=auto/single_node` | CPU/内存规格 | CPU Deployment |
| 无 `resources.accelerator`，`placement_mode=multi_node` | 非法组合 | `400 INVALID_ARGUMENT`；P0 不支持分布式 CPU replica |
| accelerator 存在，但 spec_id 为空或 count `< 1` | 非法规格 | `400 INVALID_ARGUMENT` |
| spec_id 不存在/不可用于新部署 | 无效平台规格 | `422 ACCELERATOR_SPEC_UNAVAILABLE` |
| accelerator 存在，模型不支持该 spec/count | 不兼容 | `422 MODEL_INCOMPATIBLE` |
| accelerator + `single_node` | 固定单节点 | 可容纳则 GPU Deployment，否则 `422 INSUFFICIENT_CAPACITY` |
| accelerator + `multi_node` 且 replicas=1 | 固定跨节点 | LWS 前置满足则 LeaderWorkerSet，否则 `422 UNSUPPORTED_TOPOLOGY` |
| accelerator + `multi_node` 且 replicas>1 | P0 不支持 | `422 UNSUPPORTED_TOPOLOGY` |
| accelerator + `auto` | Core capability + execution profile | 优先单节点；只有 replicas=1 时可回落到 LWS，均不可行则返回对应 422 |
| 旧请求缺少 `gpu_type`，仅出现 SDK 默认 `gpu_count_per_pod=1` | 无 accelerator | CPU Deployment，禁止误判 GPU |
| 旧请求含 `gpu_type`，未传新 `resources/placement_mode` | accelerator + single_node | 保持旧客户端单节点语义，不自动升级为 LWS |
| 新旧字段同时存在但不一致 | 冲突 | `400 INVALID_ARGUMENT` |
| `image_id` 与 `image_ref` 都缺失 | 非法输入 | `400 INVALID_ARGUMENT` |
| 选定或输入的镜像无法解析为 digest | 镜像不可用 | `422 IMAGE_UNAVAILABLE` |

虽然 schema 使用通用名 `accelerator`，P0 allowlist 只接受已通过 live gate 的整卡 GPU `spec_id`；不接受 vGPU/MIG，也不表示 TPU、NPU 或其他 accelerator 已受支持。所有规范化结果与 GPUSpec 不可变快照都进入 request hash、desired spec 和审计快照，避免规格目录变化或重试后得到不同 execution plan。

对 leader-worker execution plan，role CPU/内存之和不得超过顶层 group 预算，所有 role GPU 数量之和必须等于 `count_per_replica`，并满足 `TP × PP = count_per_replica`。不满足时在调用 Core 前返回 `400 INVALID_ARGUMENT`；Core 仍重复校验，防止绕过 Services。

### 6.3 InferenceService 响应

现有字段保持兼容，并增量补充：

| 字段 | 含义 |
|---|---|
| `id` | 推理服务 UUID |
| `name` | 租户内活动资源唯一名称 |
| `model` | 模型名称/版本展示快照 |
| `model_version_id` | 实际部署的不可变版本 |
| `image_id` | 创建时从镜像仓库选定的 Registry 镜像 ID；手填 `image_ref` 时可为缺省 |
| `image_ref` | 创建时解析并冻结的 digest 引用；只读 |
| `served_model_name` | OpenAI 请求中的稳定 `model` 值，租户内唯一 |
| `replicas` | 期望独立服务副本数 |
| `ready_replicas` | 当前健康副本数 |
| `resources` | 规范化资源规格快照：CPU、内存和可选 accelerator |
| `gpu_type` | v1 deprecated 兼容投影；新客户端不得依赖 null 判断 CPU |
| `gpu_count_per_pod` | v1 deprecated 兼容投影；不再是规格权威字段 |
| `placement_mode` | `auto/single_node/multi_node` |
| `max_concurrency` | v1 deprecated 兼容投影；P0 不执行、不宣称生效 |
| `status` | `pending/deploying/running/stopping/stopped/failed` |
| `status_reason` | 稳定机器可读原因码，可空 |
| `status_message` | 脱敏的人类可读说明，可空 |
| `generation` | 最新期望规格代次 |
| `observed_generation` | runtime 已收敛代次 |
| `current_operation_id` | 当前或最近一次 runtime operation，可空 |
| `invocation_url` | 稳定平台调用 URL；P0 未建设网关，固定为 null |
| `endpoint_url` | v1 兼容保留字段；P0 固定为 null，禁止返回集群内地址 |
| `created_at` | 创建时间 |
| `updated_at` | 最近资源变化时间 |

`runtime_endpoint`、Core workload ID、Pod/Service 名、namespace、provider ID 不进入此响应。

### 6.4 PATCH

PATCH 只允许修改：

- `replicas`

请求必须带 `idempotency_key`。未变化请求按同一结果幂等返回。P0 仅 `running` 可缩放独立副本；修改 replicas 时 `generation + 1`、状态转为 `deploying`，全部期望副本健康后完成。`placement_mode=multi_node` 固定 replicas=1，PATCH 其他值返回 `422 UNSUPPORTED_TOPOLOGY`。

伸缩失败不能只留下含糊的 `failed`：operation 保留 `before_spec/target_spec`。服务在事务中把 `desired_spec` 恢复为 `applied_spec`、再递增一次 generation，并把该代次记入 `rollback_generation`；worker 使用由 operation ID + rollback generation 派生的幂等键要求 Core 恢复 `before_spec.replicas`。回滚成功后服务恢复为旧规格的 `running`，该 scale operation 仍以 `failed` 结束并记录 `SCALE_ROLLED_BACK`；回滚也失败时服务进入 `failed`，原因固定为 `ROLLBACK_FAILED`，由 reconciler 继续按当前 `desired_spec` 收敛或等待人工处置。只有目标规格全部 ready 且 smoke 通过，才允许覆盖 `applied_spec`。

模型版本、推理镜像、resources、placement 和 engine profile 都不是 P0 PATCH 字段。需要切换时新建服务，避免在没有网关流量切换与完整 applied-spec rollback 前制造高风险原地变更。

### 6.5 生命周期

```yaml
idempotency_key: uuid
action: start|stop|restart
```

| action | 允许状态 | 目标语义 |
|---|---|---|
| `start` | `stopped` | 创建/启动工作负载，健康检查和真实推理测试通过后 running |
| `stop` | `pending`、`running`、`deploying` | 取消未完成创建，或删除 provider runtime、释放计算资源并清除内部 endpoint；保留 Core PlatformWorkload 记录（若已创建）和已应用规格 |
| `restart` | `running` | 重启现有 generation，不修改规格 |

### 6.6 后置策略兼容

现有 policies OpenAPI 已声明但 Gateway 尚未注册真实 handler。P0 不实现、也不返回“已生效”的虚假成功；契约 PR 必须为该路径补充稳定的 `501 FEATURE_NOT_AVAILABLE`，直到后续网关方案冻结。`max_concurrency` 同样不进入 P0 新请求；若未来映射 vLLM `max-num-seqs`，它属于 runtime generation，若映射网关限流，则属于网关策略，两者不能复用一个含糊字段。

---

## 7. 内部数据模型

### 7.1 `inference_services`

| 列 | 约束与用途 |
|---|---|
| `id` | UUID 主键 |
| `tenant_id` | 非空，所有查询首要隔离键 |
| `name` | 活动资源范围内 `(tenant_id, name)` 唯一 |
| `model_version_id` | 非空，不可变 |
| `model_display_snapshot` | 创建时名称/版本快照 |
| `served_model_name` | `(tenant_id, served_model_name)` 活动范围唯一 |
| `desired_spec` | 当前期望规范化规格，含 GPUSpec/engine profile 快照 |
| `applied_spec` | 最近被 Core 完整观测且通过 smoke 的规格 |
| `status` | 当前 Services 状态 |
| `status_reason/message` | 稳定码与脱敏说明 |
| `generation` | 每次期望规格/desired state 变化递增 |
| `observed_generation` | 最近已收敛代次 |
| `desired_state` | `running/stopped/deleted` |
| `runtime_ref` | Core platform workload ID，内部字段 |
| `runtime_endpoint` | Core 返回的集群内 URL，内部字段 |
| `invocation_url` | 兼容保留；P0 固定 null |
| `ready_replicas` | 最近观测值 |
| `created_at/updated_at/deleted_at` | 生命周期时间 |

删除使用内部 tombstone 支持重试和审计；对租户 GET/List 在删除完成后表现为 404/不可见。

### 7.2 `inference_operations`

操作表同时承担持久队列和幂等记录：

| 列 | 用途 |
|---|---|
| `id` | operation/task UUID |
| `tenant_id/service_id` | 隔离与资源关联 |
| `type` | create/scale/start/stop/restart/delete |
| `idempotency_key` | 逻辑操作幂等键 |
| `request_hash` | 规范化请求指纹 |
| `target_generation` | 本操作收敛的 generation |
| `rollback_generation` | scale 补偿回滚使用的 generation，可空 |
| `before_spec/target_spec` | 操作前已应用规格与目标规格快照 |
| `state` | pending/running/completed/failed/cancelled/dead_letter，与 Services AsyncTask 一致 |
| `attempt` | 重试次数 |
| `next_attempt_at` | 指数退避时间 |
| `lease_owner/lease_until` | 多副本 worker 互斥租约 |
| `result_snapshot` | 首次成功响应或 task 投影 |
| `error_code/message` | 最终失败信息 |
| `created_at/updated_at` | 审计时间 |

唯一约束为 `(tenant_id, operation_scope, idempotency_key)`。相同 key + 相同 request hash 返回首次结果；相同 key + 不同 hash 返回 `409 IDEMPOTENCY_CONFLICT`。

### 7.3 不新增独立消息中间件

P0 使用 PostgreSQL 持久操作表配合 `FOR UPDATE SKIP LOCKED`/租约 worker 即可形成最小可靠闭环，不为推理服务单独引入 NATS/Kafka。若后续平台已有统一可靠事件总线，可在不改变资源语义的情况下替换投递实现。

---

## 8. 状态机与操作语义

### 8.1 状态机

```text
create accepted
      │
      ▼
   pending ── worker claimed ──► deploying ── workload ready + health/test ─► running
      │                              │                                              │
      └── stop ──► stopping ──► stopped                                             │
                                     └──────── terminal error ───────────────────► failed

running ── stop/delete accepted ──► stopping ── workload stopped ──► stopped
   │                                      └──── workload deleted ──► deleted(internal) → GET 404
   ├── patch ──► deploying ──► running
   └── restart ──► deploying ──► running

stopped ── start ──► deploying ──► running
```

### 8.2 状态不变量

- `running` 必须同时满足：Core workload running、`ready_replicas == desired_replicas`、每个当前 generation 的独立副本/group 健康、受控集群内真实推理 smoke 通过、`observed_generation == generation`。
- P0 的 `invocation_url` 和兼容字段 `endpoint_url` 在所有状态均返回 null；`runtime_endpoint` 只存内部数据库，不进入租户响应。
- `failed` 必须有 `status_reason`；不得只保存自由文本。
- `stopped` 时不保留占用计算资源的 provider runtime。P0 固定由 Core 删除 Deployment/LWS、Service 和 PodGroup，但保留 PlatformWorkload 记录、ID 与已应用规格；`start` 在同一 Core identity 下重新创建 provider runtime。`stop` 成功时 `runtime_endpoint=null`、`ready_replicas=0`，不得依赖 LWS 是否接受零 group 来表达停止。
- `deleted` 仅内部保存，租户资源状态 enum 不新增 deleted。

### 8.3 操作矩阵

| 操作 | pending | deploying | running | stopping | stopped | failed |
|---|---|---|---|---|---|---|
| 查看 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| test | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ |
| PATCH | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ |
| stop | ✅，取消未完成创建 | ✅，取消未完成部署 | ✅ | 幂等返回原任务 | 幂等成功 | ❌ |
| start | ❌ | ❌ | 幂等成功 | ❌ | ✅ | ❌ |
| restart | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ |
| delete | ✅ | ✅ | ✅ | ✅，抢占 stop | ✅ | ✅ |

### 8.4 generation、互斥与抢占规则

- 每个改变 desired spec/state 的动作都在数据库事务中锁定资源行并递增 generation。
- 一个 service 同一时间只允许一个对当前 generation 生效的 runtime operation。
- 普通 `scale/start/restart` 若遇到活动 operation，返回 `409 OPERATION_IN_PROGRESS` 并携带活动 task ID，不排队制造隐式顺序。
- `stop` 和 `delete` 是高优先级 desired-state 操作：允许抢占活动的 `create/scale/start/restart`。同一事务必须写入新 generation、新 operation，并把旧 operation 标记 `cancelled`；`delete` 还可抢占 `stop`，且删除期望不可被后续非删除操作反转。
- 若旧 Core task 已提交且无法取消，worker 不假设底层取消成功。旧 worker/回调完成时先比较 `target_generation`；generation 已过期则只记录审计结果，不得覆盖资源状态。当前 generation 的 reconciler 继续向 Core 下发 stop/delete 直至收敛。
- 对同一 stop/delete 的幂等重放返回原 task；不同幂等键但 desired state 已一致时返回当前高优先级 task，不重复递增 generation。
- 所有 worker 状态写入使用 generation CAS；不得用旧观测、旧 endpoint 或旧 ready 数覆盖新期望。

---

## 9. 可靠协调与故障恢复

### 9.1 创建链路

```text
1. API ingress 完成认证并传递 tenant/user/request context；inference-service 再执行资源级授权。
2. inference-service 校验请求、幂等键和名称唯一性。
3. 通过 ModelCatalog 解析不可变版本并校验 ready/capabilities；解析 Core GPUSpec 与能力，冻结 engine/execution profile 和规范化 request hash。
4. 同一 PG 事务写 InferenceService(pending, desired_spec) + Operation(pending, target_spec)。
5. API 立即返回 202，不等待镜像拉取或模型加载。
6. worker 获取 operation lease，将资源置为 deploying。
7. 使用 service_id + generation 派生 Core idempotency_key，调用 platform-workloads。
8. 从 Core AsyncTask.resource_id 立即持久化 runtime_ref；随后周期查询 PlatformWorkload 状态。
9. Core ready 后从受信响应取得 runtime_endpoint，执行 /health。
10. 使用受控客户端向 `runtime_endpoint` 发送带固定上限的真实推理请求，验证模型加载与返回内容，而不只检查 HTTP health。
11. 仅当 ready_replicas=desired_replicas 时，原子更新 applied_spec、observed_generation、ready_replicas 和 running；`invocation_url` 保持 null。
```

### 9.2 崩溃窗口处理

| 故障窗口 | 恢复方式 |
|---|---|
| DB 提交后、调用 Core 前崩溃 | lease 到期，另一 worker 重新领取 operation |
| Core 已创建、runtime_ref 未写入 | 重放相同 Core idempotency key，得到同一 AsyncTask/resource_id |
| workload ready、健康/推理测试未完成 | reconciler 重新执行受控检查，成功后继续状态事务 |
| 健康/推理测试已通过、状态未写 running | 根据相同 generation 幂等复查并继续状态事务 |
| 进程在删除中崩溃 | desired_state=deleted，reconciler 继续删除 workload 和内部引用 |
| Core 暂时不可用 | operation 退避重试；资源保留明确 reason，不伪造成功 |

### 9.3 重试分类

- 可重试：网络超时、Core 5xx、暂时未调度、镜像拉取暂态、health 尚未 ready。
- 不可重试：模型不兼容、无效/不可用 accelerator spec、镜像不存在/未授权、未批准 engine profile、保留字段冲突。
- 有界等待：资源不足可保持 `deploying` 并报告 `INSUFFICIENT_CAPACITY`；达到部署超时后进入 `failed`，但保留可诊断信息。
- 自动重试不得创建新 generation，也不得生成新幂等键。

### 9.4 失败清理与伸缩回滚

- create/start/restart 在不可重试错误或部署超时后，先保存规范化 reason、operation 审计和可检索日志引用，再要求 Core 删除 provider runtime、Service 和 PodGroup，避免失败服务长期占用 GPU；PlatformWorkload 记录可保留供诊断，`runtime_endpoint` 必须清空。
- scale 失败按 6.4 的 `applied_spec` 补偿流程回滚。回滚期间服务保持 `deploying`，不能短暂宣称新规格 `running`；回滚完成后服务可以是旧规格 `running`，但原 scale task 明确为 `failed/SCALE_ROLLED_BACK`。
- delete 永远优先于失败清理和回滚。观察到 `desired_state=deleted` 后，不再发起恢复旧规格的补偿动作，只继续最终回收。
- 日志保留期、tombstone 保留期和审计保留期由平台统一数据保留配置决定；它们不构成继续保留计算资源的理由。

### 9.5 reconciler

reconciler 周期扫描非终态、租约过期、generation 未收敛和删除 tombstone：

- 以数据库 desired state 为权威观察 Core。
- 修复状态漂移和过期 internal endpoint。
- 对存在 runtime_ref 的资源绝不盲目重新创建。
- 通过 Core owner labels 查找确定性 orphan，仅回收 ANI 明确拥有且超过安全窗口的资源。
- 不通过 List Kubernetes Pod 做业务判断。

---

## 10. 运行时规格

### 10.1 P0 CPU

- 统一资源规格中不含 `accelerator` 时生成 CPU execution plan；不再使用 `gpu_type=null` 作为业务语义。
- Core planning 必须把 inference 是否需要 GPU 改为由 accelerator request 决定；不得继续通过 `kind=inference` 强制进入 GPUInventory。
- 使用版本化 `vllm-openai-cpu` 镜像和 CPU 支持模型 allowlist。
- 准入必须校验 CPU 架构、指令集、内存、共享内存和模型格式。
- CPU/内存是平台规格或模型推荐结果的一部分；不能只用默认容器 requests。
- CPU live gate 单独记录吞吐、TTFT 和资源占用，不把功能可用等同于满足 GPU 性能目标。

### 10.2 P0 单节点 GPU

- `resources.accelerator.spec_id/count_per_replica` 表示一个模型副本需要的整卡 GPU 规格与总数；单节点 execution plan 要求它们位于同一节点。
- `replicas` 表示相互独立、可负载均衡的模型副本。
- Core `GPUInventory` 校验型号、数量和同节点可调度性。调度走 Volcano（`schedulerName=volcano`）；契约仍提交 `queue_class: inference`，实现不绑定名为 `ani-inference` 的 Queue CR。
- 单节点多卡时，Core 根据已验证模板设置 vLLM tensor parallel size；Services 不接受任意 TP 参数。
- 明确申请 GPU 而资源不足时返回/收敛为 GPU 容量错误，禁止转 CPU。

### 10.3 P0 跨节点 GPU：LeaderWorkerSet

跨节点统一使用 LeaderWorkerSet，不把多个普通 Deployment 拼成一个分布式副本。P0 一个 InferenceService 只允许一个 LWS group（`replicas=1`）；每组包含一个 leader 和若干 worker。LWS 负责组拓扑、稳定身份和组滚动，vLLM Ray 负责模型并行，Volcano PodGroup 负责整组 gang scheduling。

创建前必须生成内部、版本化 execution plan，至少包含：

- leader/worker 数以及各 role 的 CPU、内存和 GPU spec/count；
- TP、PP 及其乘积校验，满足 `TP × PP = resources.accelerator.count_per_replica`；
- 固定 `backend=ray`：leader 启动 Ray head，等待完整 group 后启动 vLLM API；worker 使用 `LWS_LEADER_ADDRESS` 加入 Ray；
- 固定 Ray control/client 端口、NetworkPolicy、启动超时和版本一致性；
- Core 从 role resources 派生 PodGroup `minMember/minResources`，队列类别固定 inference；
- 模型路径一致性、NCCL/RDMA 和网络拓扑前置条件。
- profile 内冻结启动超时、health probe 超时/重试和 smoke 的最大输入/输出 token；客户端不能覆盖这些安全上限。不同模型大小可以选择不同 profile，但同一 operation 重试不得重新选 profile。

runtime shape 选择规则：

```text
resources.accelerator 不存在
  → CPU Deployment

resources.accelerator 存在 + placement_mode = single_node
  → GPU Deployment；单节点必须容纳 count_per_replica

resources.accelerator 存在 + placement_mode = multi_node
  → replicas 必须为 1；LeaderWorkerSet + 一个 Volcano PodGroup

resources.accelerator 存在 + placement_mode = auto
  → 单节点可满足且模型 profile 允许：GPU Deployment
  → 否则且 replicas=1：LeaderWorkerSet + 一个 Volcano PodGroup
  → 否则：UNSUPPORTED_TOPOLOGY
```

Core 根据 execution plan 渲染 LeaderWorkerSet、一个 PodGroup 和仅选择 leader/API Pod 的 ClusterIP Service。leader/worker Pod 必须携带同一 PodGroup 绑定与 Volcano scheduler，`minMember` 等于完整 group Pod 数。若 LWS CRD/controller、Volcano queue/controller、Ray、模型共享路径或网络条件不满足，返回 `422 UNSUPPORTED_TOPOLOGY`，不得静默拆成普通 Deployment，也不得改为 CPU。

P0 固定 execution profile，不允许客户端直接填写 worker 数、TP、PP、Ray 参数、PodGroup 或 schedulerName。profile 由模型版本、GPUSpec、GPU 总数和集群能力确定，并将 profile ID/version、vLLM/Ray/Python/CUDA/NCCL 镜像 digest 保存到 operation 审计中。

### 10.4 模型挂载

- inference-service 从 `ModelCatalog` 获得不可变 `object_ref` 和可选 `secret_ref`。
- 请求 Core 通过 artifact/storage/secret binding 挂载到 `/models`。
- Services 不拼 PVC、Secret、pre-signed URL 或 init container manifest。
- Core 负责短期下载凭证注入和 Secret 脱敏；日志不得输出 URL query、token 或密钥。
- 模型加载完成前 workload 不得 ready。

---

## 11. 集群内端点与网关后置边界

### 11.1 两类端点

| 字段 | 可见性 | 例子 | 权威来源 |
|---|---|---|---|
| `runtime_endpoint` | 仅服务内部 | `http://...svc:8000` | Core platform workload |
| `invocation_url` | P0 兼容保留 | null | 后续调用网关 |

`endpoint_url` 在 v1 作为兼容保留字段，P0 同样返回 null。任何集群 DNS、namespace、Pod IP 或 provider endpoint 都不得通过 Services API 返回。

### 11.2 P0 访问方式

- Core 为 runtime 创建 ClusterIP Service，并将受信的 `runtime_endpoint` 返回给 inference-service。
- inference-service 使用该地址执行 `/health` 和有界真实推理测试。
- 普通租户、集群外客户端和 Services REST 响应均不能取得或直接访问该地址。
- P0 不创建 Ingress、Gateway API route、NodePort 或 LoadBalancer。

### 11.3 后续网关集成边界（非 P0）

后续网关需要完成稳定公网地址、认证鉴权、按 tenant + model 选择目标服务、限流、计量和流量治理。该方案冻结前，推理服务不预设网关产品、路由存储或发布接口；只保证未来可通过服务身份查询受信的 service ID、generation、served model 和内部 endpoint，不允许网关写推理服务数据库或直接修改资源状态。

网关接入后是否引入独立发布 port、如何处理 revision 和撤销顺序，应在网关方案中单独设计和验收，不属于当前 P0 完成条件。

### 11.4 受控调用测试

`POST .../test` 只允许 `running`：

- 必须通过内部 `runtime_ref` 重新观察并取得当前 generation 的受信 runtime endpoint。
- 服务端根据 service ID 绑定 `served_model_name`，不接受调用方提供任意 URL。
- `timeout_seconds` 只限制本次控制面测试。
- 响应正文按契约限制大小并脱敏；超大生成结果不写审计日志。
- `idempotency_key` 防止 API 客户端网络重试重复执行推理。P0 固定语义为：同一 tenant/service/key/request hash 在 5 分钟窗口内只执行一次，保存加密且受大小上限约束的结果快照并向重放请求返回；窗口后按新调用处理。相同 key 不同 request hash 返回 409。快照到期即清除，不进入长期审计。

---

## 12. 安全与多租户

### 12.1 控制面

- tenant ID 只从认证上下文获取，不接受请求体覆盖。
- 所有 repository 查询必须带 tenant ID；跨租户返回 404。
- 写操作校验 RBAC 和幂等键。

### 12.2 集群内 runtime 边界

- `/test` 使用 Services 控制面认证，tenant ID 只取认证上下文，不信任请求中的租户 header。
- runtime Service 采用 NetworkPolicy，只允许 inference-service health/test、必要的平台观测组件和 LWS 组内通信访问。
- 不暴露 runtime endpoint，防止绕过鉴权、限流、审计和计量。
- 测试接口只能使用 Core 返回并绑定到当前 service/generation 的 endpoint，禁止用户提供 upstream，避免 SSRF。

### 12.3 Secret 与模型安全

- 数据库只保存 secret reference，不保存明文 API Key、模型解密密钥或对象临时凭证。
- 工作负载使用短期身份/Secret binding；权限限定到一个模型版本。
- 加密模型的解密材料只在 runtime 启动边界注入，不进入命令行、环境转储或用户日志。
- 删除服务时撤销对应临时凭证；删除模型版本时必须先检查活动推理服务引用。

### 12.4 审计与隐私

控制面审计记录 actor、tenant、service、operation、request ID、结果和耗时。P0 的受控测试记录 service、model、principal reference、状态码、tokens 和延迟，不包含 prompt/output 原文；生产调用计量随网关方案后置。

---

## 13. 可观测性

### 13.1 资源状态

必须提供：

- `status/status_reason/status_message`
- generation/observed_generation
- desired/ready replicas
- 最近 operation ID、类型、状态和时间
- Core workload 规范化 reason，不暴露 provider 原始对象全文

### 13.2 日志

`GET .../logs` 通过 Core workload logs 或统一 LogStore 获取：

- 支持 cursor、limit、level。
- 默认按时间倒序，并在契约中固定 cursor 语义。
- 多副本日志项应包含稳定 replica/container 标识，但不返回 Pod UID 等无产品价值字段。
- Secret、Authorization、pre-signed query、prompt/output 按规则脱敏。

### 13.3 指标

控制面指标：

- operation latency、reconcile attempts、失败数、pending/deploying 时长；
- Core API latency/error、health probe latency、受控推理测试 latency/error；
- 每状态服务数与 ready replica 数。

runtime 内部暴露、由平台观测系统采集的推理指标：

- request count、active requests、429/5xx、TTFT、端到端 latency；
- prompt/completion tokens；
- 按 tenant/service/model/engine 维度聚合，禁止把凭据明文作为 label。

Core 工作负载使用保留 labels：

```text
ani.owner_type=platform_workload
ani.owner_id=<workload_id>
ani.workload_class=inference
ani.tenant_id=<tenant_id>
services.ani.io/inference-service-id=<inference_service_id>
services.ani.io/model-version-id=<model_version_id>
```

高基数字段不得直接成为 Prometheus label；必要时通过日志或审计查询。

### 13.4 事件

P0 不伪造未冻结的 `.../events` API。详情页可先展示最近 operation。需要统一事件页时，先决定复用 Core operation/events 还是新增 Services 路径，再走契约 PR。

---

## 14. 错误模型

统一响应：

```json
{"code":"UPPER_SNAKE","message":"脱敏说明","request_id":"req-..."}
```

建议冻结的主要语义：

| HTTP | code | 场景 |
|---|---|---|
| 400 | `INVALID_ARGUMENT` | 字段、组合或模型引用格式非法 |
| 401 | `UNAUTHORIZED` | 未认证 |
| 403 | `FORBIDDEN` | 无控制面或 platform workload scope |
| 404 | `NOT_FOUND` | 服务不存在或跨租户 |
| 409 | `NAME_CONFLICT` | 活动资源名称重复 |
| 409 | `IDEMPOTENCY_CONFLICT` | 同 key 不同请求体 |
| 409 | `OPERATION_IN_PROGRESS` | 存在冲突活动操作 |
| 422 | `MODEL_NOT_READY` | 模型版本未 ready |
| 422 | `MODEL_INCOMPATIBLE` | format/capability 不受当前引擎支持 |
| 422 | `ACCELERATOR_SPEC_UNAVAILABLE` | GPUSpec 不存在、不可用于新部署或不匹配 inventory |
| 422 | `INSUFFICIENT_CAPACITY` | 同步创建准入确认当前无可行容量 |
| 422 | `UNSUPPORTED_TOPOLOGY` | LWS、gang、分布式执行或网络拓扑前置条件不满足 |
| 422 | `INVALID_STATE_TRANSITION` | 当前状态不允许动作 |
| 501 | `FEATURE_NOT_AVAILABLE` | 已声明但明确后置的网关策略能力 |
| 502 | `RUNTIME_ERROR` | 引擎响应异常 |
| 503 | `DEPENDENCY_UNAVAILABLE` | Core 或 ModelCatalog 暂不可用 |
| 504 | `RUNTIME_TIMEOUT` | test 或运行时调用超时 |

只有合入 OpenAPI 的错误码才是正式契约。HTTP 422 只用于 API 接受请求前能够确定的准入失败；一旦创建已返回 202，后续资源竞争、调度超时或运行失败必须写入 AsyncTask + InferenceService `status_reason`，不能事后伪造成 HTTP 响应。内部 provider 错误必须映射并脱敏，不能把 Kubernetes/registry 响应直接返回给用户。

---

## 15. 目录与内部接口

### 15.1 目标目录

```text
repo/services/inference-service/
  cmd/inference-service/
  internal/http/            # Services OpenAPI handlers
  internal/domain/          # resource/state/transition rules
  internal/service/         # use cases + transaction boundary
  internal/repository/      # PG resource/operation stores
  internal/reconcile/       # lease worker + desired-state convergence
  internal/runtime/         # InferenceRuntime interface
  internal/runtime/core/    # generated Core SDK adapter only
  internal/catalog/         # ModelCatalog port + adapter
  internal/engine/          # versioned vLLM CPU/GPU templates
  internal/health/          # bounded trusted endpoint probe
```

不在 Services 下复制 Core `ports/adapters`。`InferenceRuntime` 是 Services 内部 anti-corruption interface，默认实现只包装生成的 Core SDK。

### 15.2 `InferenceRuntime`

```go
type InferenceRuntime interface {
    Create(ctx context.Context, req CreateRuntimeRequest) (Runtime, error)
    Get(ctx context.Context, runtimeID string) (Runtime, error)
    Update(ctx context.Context, runtimeID string, req UpdateRuntimeRequest) (Runtime, error)
    ApplyLifecycle(ctx context.Context, runtimeID string, action LifecycleAction, key string) (Task, error)
    Delete(ctx context.Context, runtimeID string, key string) (Task, error)
    Logs(ctx context.Context, runtimeID string, query LogQuery) (LogPage, error)
}
```

接口只表达 inference-service 所需意图；不得透出 Kubernetes object、clientset 或完整 Core SDK。

---

## 16. API-first 与实施顺序

每一阶段都必须遵循“契约 PR 先合入，再实现”，不能把跨层契约和实现混在一个未批准批次。

### 阶段 A：Core 通用契约

1. 在 `repo/api/openapi/v1.yaml` 冻结 `platform-workloads` 路径、schema、服务身份和错误语义。
2. 刷新 Core SDK/API docs/compatibility 生成物。
3. 运行 Core 契约门禁并提交独立 PR。
4. 契约批准后实现 Core handler → port 接线 → adapters → store → 测试。
5. 修正 `WorkloadKindInference` 强制 GPU 的现状，使 GPU 准入由 accelerator 是否存在决定。
6. 验证平台工作负载不会进入 `/instances`。

### 阶段 B：Services 契约

1. 在 `repo/api/openapi/services/v1.yaml` 增量加入 model version、resources、状态诊断、scale PATCH、lifecycle、operation query 和 policies 501 语义。
2. 清理未绑定 path 的 `InferenceEndpoint/CreateInferenceEndpointRequest` 遗留 schema；若兼容门禁不允许直接删除，先标记 deprecated、停止生成新业务引用，并在下一主版本删除。
3. 要求现有 API ingress 的过渡 stub 由 ANI Gateway 委托 inference-service 内部 `InferenceControl` gRPC，并对齐已有 202/AsyncTask 契约。
4. 刷新 Services SDK/API docs 并运行 `make validate-services`。
5. 独立契约 PR 合入后再实现业务代码。

### 阶段 B.1：旧控制面退役

- 将 `api/proto/inference/v1/inference_service.proto` 及生成物标记 deprecated，禁止新增调用者；`GetEndpointURL/UpdateStatus` 不进入新链路。
- `operators/inference-operator` 与 InferenceService CRD 不部署到 P0 环境；若已有测试资源，先盘点 owner，再迁移或清理。
- 删除 Gateway → 旧 `inference.v1.InferenceServiceRPC`（`GetEndpointURL` / `UpdateStatus`）和 operator → status 回写的任何运行接线；不得复活该旧 proto。
- 产品 HTTP 入口只在 ANI Gateway：`/api/v1/svc/inference-services*`。Gateway 通过新的内部 `inference.control.v1.InferenceControl` gRPC 委托 inference-service；inference-service 不再对外暴露产品 HTTP。
- 加架构门禁：Gateway 不得 import `services/inference-service` 业务包，不得创建 InferenceService CRD；跨层只允许 `pkg/generated/pb/inference/control/v1`。
- 如果旧资源已在真实环境运行，必须编写一次性、幂等迁移计划；本文不允许新旧控制器同时接管同一服务。

### 阶段 C：可靠控制面

- 建 inference-service、PG schema、repository、operation lease worker 和 reconciler。
- 完成 fake Core、fake ModelCatalog 下的全状态机闭环。
- API ingress 改为 Gateway HTTP → `InferenceControl` gRPC 真实委托，不再返回空 stub。

### 阶段 D：Core runtime 与 CPU live gate

- 接 Core platform-workloads real provider。
- 固定 vLLM CPU 镜像、模型 allowlist 和资源基线。
- 完成创建、重启恢复、日志、test、副本伸缩、停启和删除 live evidence。

### 阶段 E：单节点 GPU live gate

- 接 GPUInventory 与 Volcano scheduler/PodGroup；不接名为 `ani-inference` 的 Queue。
- 验证单卡与单节点多卡模板、模型挂载、健康、集群内真实推理测试和删除回收。
- 没有 GPU evidence 时只声明 CPU runtime ready，不外推 GPU ready。

### 阶段 F：集群内调用与安全边界验收

- 使用受控 `/test` 证明真实调用可以到达当前 generation 的 runtime。
- 验证 runtime 只有 ClusterIP，NetworkPolicy 禁止未授权 namespace/Pod 和集群外访问。
- 停止/删除后内部 endpoint 失效且测试不能继续调用；公网网关、模型路由、鉴权、限流和生产计量全部后置。

### 阶段 G：跨节点 LWS live gate（P0）

- 先冻结 execution plan/Core 能力契约。
- 实现 LWS/Volcano adapter、vLLM 分布式 profile 和真实多节点 gate。
- 不修改 InferenceService 主资源身份；`invocation_url` 继续为 null。

阶段 G 属于 P0 完成条件；阶段 D/E 通过只代表 CPU/单节点切片 ready，不能提前声明推理服务 P0 完成。

---

## 17. 测试与验收

### 17.1 单元测试

- 状态转换表和非法转换。
- model/model_version 兼容解析。
- 请求规范化、request hash 和幂等冲突。
- GPUSpec 快照、CPU/GPU 可选 planning 和旧字段默认值兼容。
- generation CAS、operation lease 和过期接管。
- stop/delete 抢占矩阵、旧 generation 回调丢弃和 delete-wins 规则。
- scale `before_spec/target_spec/rollback_generation` 补偿状态转换。
- Core 状态到 Services 状态映射。
- 错误映射与信息脱敏。

### 17.2 契约测试

- OpenAPI operationId、path、schema、响应码与 Services API 注册表一致。
- 创建保持 `202 + InferenceService/current_operation_id`；scale、lifecycle、删除为 `202 + AsyncTask`，不允许现有 stub 的 200/204。
- 所有有副作用操作要求幂等键。
- Services operation 可按 tenant 查询，Core task 永不直接暴露给租户。
- `runtime_endpoint/runtime_ref` 不出现在 Services SDK/schema。
- 普通租户无法调用 Core platform-workloads。
- `/instances` 永不返回平台推理 workload。
- inference-service 不依赖旧 inference proto/CRD/operator。
- SDK/前端不新增 `InferenceEndpoint` 或 `gpu_profile` 引用，产品资源只使用 InferenceService。

### 17.3 集成测试

- PG + fake Core + fake ModelCatalog 完整创建收敛。
- API 进程与 worker 分别重启后 operation 继续。
- Core create 成功但响应丢失时重放不重复创建。
- CPU inference 不调用 GPUInventory；GPU inference 必须使用冻结 GPUSpec。
- health 通过但真实推理测试失败时不得 running。
- 部分副本就绪时保持 deploying，只有 ready_replicas=desired_replicas 才 running。
- 删除过程中 Core 失败，恢复后最终回收。
- 部署中 stop 能取消 create；stopping 中 delete 能抢占 stop，旧任务完成不能复活 runtime。
- scale 失败且回滚成功时服务以旧规格 running、task 为 failed/SCALE_ROLLED_BACK；双重失败进入 ROLLBACK_FAILED。
- create/start/restart 终态失败后 provider runtime 和 accelerator 最终释放，诊断记录仍可查询。
- 两个 worker 竞争同一 operation 只有一个执行副作用。
- tenant A 无法查看、调用或删除 tenant B 服务。

### 17.4 集群内访问边界测试

- ClusterIP 和集群 DNS 不进入 Services API/SDK/schema。
- 未授权 namespace/Pod 不能访问 runtime Service，集群外不存在可达入口。
- `/test` 不能覆盖 upstream，只能解析当前 tenant/service/generation 的受信 endpoint。
- runtime 5xx/timeout 被规范化为调用失败事件，不修改资源 desired state。
- 停止/删除后 `/test` 立即拒绝，prompt/output 不进入默认日志。

### 17.5 CPU live gate

必须真实执行：

1. ready 小模型创建为 CPU InferenceService。
2. 观察 `pending → deploying → running`。
3. `/instances` 无对应记录，Core platform workload 可由服务身份查询。
4. 受控 `/test` 通过集群内 endpoint 得到真实模型响应，租户响应中的 endpoint 字段仍为 null。
5. inference-service 重启后，runtime endpoint 可由 PG 引用和 Core observation 恢复。
6. 副本伸缩、停止、启动、重启、日志和删除全部闭环。
7. 删除后 ClusterIP/runtime 不可达，Core provider 资源无孤儿。

### 17.6 单节点 GPU live gate

在 CPU gate 基础上追加：

- 指定 GPU 型号/数量被 Core 正确准入和调度。
- vLLM 实际使用请求的 GPU，指标可观测。
- 资源不足给出稳定原因，不降级 CPU。
- 单节点多卡 tensor parallel 与模型结果真实通过。
- 节点/Pod 重启后的恢复边界有 evidence。

### 17.7 跨节点 LWS live gate

- `placement_mode=multi_node` 创建 LeaderWorkerSet，而不是多个独立 Deployment。
- `replicas>1` 在 P0 fail closed；不会创建多个 group 或多个 PodGroup。
- LWS 每组 leader/worker 数、GPU 分配与冻结 execution profile 一致。
- Volcano PodGroup 的 `minMember/minResources` 覆盖整组，资源不足时没有半组运行。
- leader Ray head、worker Ray join、完整 group barrier 和 vLLM TP/PP 跨节点真实响应通过，模型路径与软件版本在所有 Pod 一致。
- ClusterIP 只选择当前 group 的 leader/API Pod，不把 Ray worker 加入 HTTP upstream。
- kill leader、kill worker 和逐节点恢复后，服务状态、LWS group 与集群内测试最终收敛。
- LWS controller、Volcano 或分布式网络不可用时 fail closed，并返回稳定诊断。
- 删除后 LWS、PodGroup、Service、模型挂载和临时凭证无孤儿。

### 17.8 故障注入

- 杀死 operation worker。
- inference-service rollout。
- 临时阻断 Core API。
- runtime health 失败。
- 模型挂载/镜像拉取失败。

每项必须验证资源状态、operation 状态、重试次数、用户错误和最终清理，而不仅是 Pod 是否重新启动。

### 17.9 仓库门禁

按实际改动范围至少执行：

```bash
cd repo
make validate-openapi-spec
make validate-core-api-compatibility   # Core 契约相关
make validate-services                 # Services 契约/handler/生成物相关
make test
make validate-architecture
git diff --check
```

真实 provider 声明还必须增加专用 inference live gate 和脱敏 evidence；local/mock 测试不得标记 runtime ready 或 production ready。

---

## 18. 完成定义

### 18.1 Contract ready

- Core 与 Services 契约分别通过独立 PR 评审。
- SDK、Services API 注册、错误语义和文档一致。
- 没有未声明路由或响应码漂移。

### 18.2 Control-plane ready

- 所有资源和操作状态持久化。
- 多副本/重启/超时下幂等与最终收敛通过。
- runtime 永不进入租户实例视图。

### 18.3 CPU/GPU runtime ready

- 对应真实环境 live gate 和 evidence 分别通过。
- CPU、单节点 GPU、跨节点 LWS 分别记录 ready 结论；三者全部通过才满足推理服务 P0 runtime ready。

### 18.4 Cluster-internal inference ready

- Services API 创建到集群内真实推理测试完整通过。
- tenant isolation、operation query、logs、metrics、audit、delete cleanup 均有证据。
- 已明确支持的模型/镜像/硬件矩阵经过验证。

该结论不代表公网调用 ready；稳定 URL、模型路由、网关鉴权、限流和生产调用计量必须在后续网关方案中独立验收。

### 18.5 Production ready

除以上条件外，还需要容量压测、升级/回滚、备份恢复、HA、故障注入、soak、安全评审和正式镜像供应链门禁。本文与 P0 live gate 均不自动声明 full platform production ready。

---

## 19. 当前仓库事实与下一步

截至本文日期，仓库事实是：

- Services OpenAPI 已有 list/create/get/delete/logs/test/policies 路径。
- Services OpenAPI 还残留未绑定 path、却被生成到 SDK/前端 schema 的 `InferenceEndpoint/CreateInferenceEndpointRequest`；它必须按阶段 B 收敛，不能发展成第二套资源。
- Services OpenAPI 尚未声明 PATCH 与 lifecycle。
- 当前 API ingress 存在推理资源过渡 stub，创建/删除响应尚未对齐 OpenAPI，test/policies 尚未完成真实委托。
- Core Go `WorkloadRuntime` 已有 `kind=inference`，但跨层 Core OpenAPI 尚无合法的平台内部工作负载路径。
- 当前 `PlanningRuntime.requiresGPU()` 把所有 inference 强制判为 GPU，CPU inference 尚不可用；这是阶段 A 必须修复并测试的 P0 缺口。
- 当前 runtime renderer 仍以 Deployment 为主，尚无可验收的 LWS/PodGroup adapter；这是必须在 P0 补齐的实现缺口，不构成把跨节点推理降到 P1 的依据。
- 仓库已有旧 inference gRPC、生成物、InferenceService CRD 与 operator 骨架，它们表达 Gateway/Operator 控制面，与本文唯一 Services reconciler 决策冲突；必须按阶段 B.1 退役，不能并行接线。

因此下一项不是直接创建 vLLM Deployment，而是：

1. 先把本方案转成 Core `platform-workloads` 契约设计与独立契约 PR。
2. 再完成 Services InferenceService 增量契约 PR。
3. 契约批准后先退役旧控制面运行接线，再按阶段 C–G 分批实现和验证；阶段 G 是 P0 runtime ready 的必要条件。

任何实现若需要 Services 直接访问 Kubernetes，或需要把内部推理负载作为普通 `/instances` 返回，都应立即停止并回到契约边界修正。
