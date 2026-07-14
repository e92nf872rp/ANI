# KuberCloud ANI · 产品功能设计

> 版本 V8.4 | 广州常青云科技有限公司 | 内部产品规划文件
> 最后更新：2026-05-23（对齐 Sprint 5 与 Core/Services 架构边界）

> 当前执行状态以 `ANI-DOCS-INDEX.md`、`ANI-06-开发计划.md` Section 零和 `repo/CURRENT-SPRINT.md` 为准。本文定义产品功能边界，不作为当前任务清单。

---

## 一、产品整体架构

### 1.1 两层产品架构

ANI 由**基础设施平台层（ANI Core）**和**云服务与功能编排层（ANI Services）**两层构成。ANI 目标定义决定开发规划：Core 通过 OpenAPI REST 契约、Core SDK 和 CLI 为 Services 提供稳定基础设施控制面；Services 面向用户实现 ANI 的云服务、前端交互、AI 应用和功能编排。

ANI 底座使用**上游原生 Kubernetes**，并通过慎重选择的开源组件进行封装和编排，最终提供 IaaS、PaaS、AI 全生命周期、AI-Native 应用等多种云服务形态。ANI 要同时支撑传统业务应用、AI 增强型应用与 AI 原生（AI-Native）应用三种工作负载形态，而不是只做 IaaS 或只做模型工程。

```
┌──────────────────────────────────────────────────────────────────────────┐
│                        ANI Services（云服务层）                            │
│                                                                            │
│  ┌─────────────────┐  ┌──────────────────────┐  ┌────────────────────┐  │
│  │  IaaS 云服务     │  │  AI 全生命周期平台    │  │  AI-Native 应用    │  │
│  │  云主机/容器/GPU │  │  训练/微调/推理/知识库│  │  知识库问答/Agent  │  │
│  │  K8s集群/存储/网络│  │  Notebook/模型仓库   │  │  MCP/语音/文档     │  │
│  └─────────────────┘  └──────────────────────┘  └────────────────────┘  │
│                                                                            │
│  ┌──────────────────────────────────────────────────────────────────┐    │
│  │                    PaaS 托管服务                                   │    │
│  │   托管数据库 · 托管消息队列 · 托管搜索 · 函数计算 · 托管容器服务    │    │
│  └──────────────────────────────────────────────────────────────────┘    │
│                                                                            │
│        层间控制面契约：ANI Core OpenAPI REST API + Core SDK + ani CLI      │
│           （api/openapi/v1.yaml 是唯一真实来源，团队内称"API 契约"）       │
├──────────────────────────────────────────────────────────────────────────┤
│                        ANI Core（基础设施平台层）                           │
│                                                                            │
│  ┌──────────────────────────────────────────────────────────────────┐    │
│  │  计算层                                                            │    │
│  │  VM · Container · GPU Container · Sandbox · Batch Job            │    │
│  │  K8s Cluster(vCluster P0) · Bare Metal/Notebook/DPU(P1+)        │    │
│  ├──────────────────────────────────────────────────────────────────┤    │
│  │  存储层                                                            │    │
│  │  块存储（EBS类）· 对象存储（S3类）· 文件存储（EFS类）· 向量存储    │    │
│  ├──────────────────────────────────────────────────────────────────┤    │
│  │  网络层                                                            │    │
│  │  VPC · 子网 · 安全组 · 路由 · 负载均衡 · SDN（KubeOVN）           │    │
│  ├──────────────────────────────────────────────────────────────────┤    │
│  │  身份与安全层                                                      │    │
│  │  Auth · RBAC · OIDC/SSO · API Key · 多租户 · 国密加解密 · 密钥管理│    │
│  ├──────────────────────────────────────────────────────────────────┤    │
│  │  平台支撑层                                                        │    │
│  │  镜像仓库 · 用量计量 · 可观测性 · 服务目录/内部DNS · 事件通知      │    │
│  ├──────────────────────────────────────────────────────────────────┤    │
│  │  硬件纳管层                                                        │    │
│  │  GPU 异构纳管（NVIDIA/昇腾/海光）· DPU 纳管（BlueField）· BM 纳管 │    │
│  └──────────────────────────────────────────────────────────────────┘    │
│                                                                            │
│  底层开源组件（通过 ports/adapters 松耦合封装）：                           │
│  Kubernetes · KubeVirt · KubeOVN · Kata Containers · Metal3               │
│  HAMi · Volcano · NVIDIA GPU Operator · NVIDIA DPF                        │
│  MinIO · Rook-Ceph · Milvus · PostgreSQL · NATS · Redis · Harbor          │
└──────────────────────────────────────────────────────────────────────────┘
```

### 1.2 层间边界原则

1. ANI Services **只能**通过 `api/openapi/v1.yaml` 定义的 Core OpenAPI REST API 或生成的 Core SDK 调用 ANI Core
2. ANI Services **禁止** import ANI Core 代码包、直接调用 Core 内部 gRPC service、直接操作 K8s API、直接连接底层数据库
3. ANI Core **不包含**任何模型推理逻辑、RAG 逻辑、PaaS 业务逻辑
4. 新能力扩展：新实例类型通过 port/adapter 扩展；新云服务在 Services 层通过 Core API 组合实现
5. Services API 的真实来源是 `repo/api/openapi/services/v1.yaml`；当前首批切片已扩展到模型、推理、知识库及后续 Services 管理面资源。Services 受控并行 PR 必须 API-first，并通过共同审查和边界门禁迁移旧 Services 逻辑。

### 1.3 设计原则

- **编排而非重造**：底层最大化复用成熟开源组件，ANI 价值在于编排与封装
- **原生 Kubernetes 底座**：Kubernetes 版本、API、RuntimeClass、CSI、CNI、CRD 语义与开源社区同步，不绑定特定 Kubernetes 发行版
- **成熟开源准入**：默认开源组件必须有健康 GitHub 社区、稳定 release、丰富源码和文档、可观测运维能力、离线部署能力和可替换路径；GitHub stars 是信号，不是单独决策依据
- **API-First**：所有接口先写 API 契约再生成代码，Services 依赖契约而非实现
- **可扩展设计**：每类能力（计算/存储/网络/硬件）都有扩展点，未来增加新类型不破坏现有边界
- **多租户原生**：隔离在设计阶段内建，不是事后补丁；网络/存储/控制面三层均有租户边界
- **生产级标准**：稳定性、可扩展性、可观测性、安全性是硬性要求，不是锦上添花

---

## 二、ANI Core — 基础设施平台层

### 2.1 计算层 — 实例全类型

#### 2.1.1 实例类型完整定义

| 实例类型 | 底层技术 | 核心场景 | 优先级 |
|---|---|---|---|
| **VM（云主机）** | KubeVirt + KVM | 通用虚拟机，兼容传统工作负载 | P0 |
| **Container（容器实例）** | K8s Pod/Deployment | 微服务、Web 服务，含 K8s 集群可视化管理 | P0 |
| **GPU Container（GPU容器）** | K8s + HAMi + Volcano | AI 训练/推理算力，GPU 虚拟化切片 | P0 |
| **Sandbox（安全沙箱）** | Kata Containers（QEMU P0 / Firecracker P1） | Agent 代码执行、不可信负载隔离 | P0 |
| **Batch Job（批任务）** | K8s Job + Volcano 队列调度 | 离线批计算、数据处理 | P0 |
| **Notebook（开发环境）** | K8s Pod + 持久化 PVC | 开发调试，应用态与存储态分离 | P1 |
| **K8s Cluster（租户K8s集群）** | vCluster（P0）/ Metal3（P1）| 租户自主管理 K8s，与最新 K8s 版本一一对齐 | P0 |
| **Bare Metal（裸金属）** | Metal3 + Ironic + BMC/Redfish | 零虚拟化开销，高性能 AI 推理节点 | P1 |
| **DPU Node（DPU加速节点）** | NVIDIA DPF（DOCA Platform Framework） | 网络/存储/安全卸载，主机 CPU 100% 用于业务 | P2 |

#### 2.1.2 K8s 集群管理设计

K8s 集群管理采用**三层混合架构**：

```
Layer 1：集群生命周期           → ANI Core OpenAPI REST API（创建/升级/删除）
  POST /api/v1/k8s-clusters
  GET  /api/v1/k8s-clusters/{id}/kubeconfig   ← ANI 鉴权的 kubeconfig

Layer 2：工作负载操作           → 原生 K8s API 透明代理（不封装）
  ANY  /api/v1/k8s-clusters/{id}/api/*
  kubectl / Helm / Argo CD / K9s 等原生工具链 100% 兼容
  支持与最新 K8s 版本一一对齐的全部资源类型：
    Pod · Service · Deployment · StatefulSet · DaemonSet
    Job · CronJob · Ingress · CRD · RBAC · HPA · NetworkPolicy
    NodePool · PVC · ConfigMap · Secret · 及未来新增资源类型

Layer 3：多集群管理（Phase 2）  → Karmada 封装 + ANI 高层 API
  POST /api/v1/k8s-federation/{id}/propagation-policies
```

**多租户隔离方案：**
- 标准方案（v1.0.0）：每租户一个 vCluster（独立 API Server + etcd，运行在 ANI 物理集群上），KubeOVN VPC 提供网络物理隔离
- 高隔离方案（高价值客户）：Metal3 + Cluster API 提供独立物理 K8s 集群
- 观测数据：Metrics/Logs 经 ANI 可观测层聚合，租户只看自己的，管理员可看全局

**为什么不全部用 OpenAPI 封装 K8s：** K8s 有 50+ 资源类型 + CRD 无限扩展，且每年发布 3 个大版本，封装层无法保持同步，且会导致 kubectl/Helm/GitOps 等业界标准工具链失效。

#### 2.1.3 Sandbox 技术选型（已确认）

| 方案 | RuntimeClass | 启动时间 | GPU | 适用场景 | 阶段 |
|---|---|---|---|---|---|
| Kata + QEMU | `sandbox-kata` | ~1s | GPU Passthrough ✅ | Agent 通用沙箱，模型推理场景 | **P0** |
| Kata + Firecracker | `sandbox-kata-fc` | ~150ms | 无 | CPU-only 快速沙箱，代码解释器 | P1 |
| gVisor | `sandbox-gvisor` | ~1s | CUDA（部分） | 高密度轻量沙箱 | P2 |

Sandbox 对标 E2B 的产品语义：每个 Agent 任务/会话对应一个 Sandbox 实例，支持文件读写、命令执行、网络出口管控、会话暂停/恢复（最长 24 小时）。

#### 2.1.4 裸金属（BM）实例

基于 Metal3（CNCF 项目）+ Ironic 实现 K8s 原生裸金属管理：
- 硬件发现：通过 BMC（IPMI/Redfish）自动采集 CPU/内存/磁盘/网卡规格
- 系统部署：PXE/iPXE/Virtual Media 引导，cloud-init 初始化
- 生命周期：available → provisioning → running → deprovisioning → available
- 与 Cluster API 集成：BM 节点可直接加入 K8s 集群作为 Worker Node

#### 2.1.5 DPU 加速节点

基于 NVIDIA DPF（DOCA Platform Framework）实现 DPU K8s 原生管理：
- 卸载能力：OVN-K8s 网络处理（主机 CPU 零占用）、NVMe-oF 存储 I/O、TLS 加密/防火墙
- 效果：相同硬件配置，AI 推理节点吞吐提升 20-40%
- BM + DPU 组合是 AI 推理节点的黄金配置
- 实例创建时通过 `acceleration.dpu.offloads` 声明所需卸载能力

#### 2.1.6 GPU 算力纳管

- 支持 NVIDIA A/H 系列、昇腾 NPU、海光 DCU 统一纳管（通过 `GPUInventory` port 抽象）
- GPU 虚拟化：MIG 模式（A100/H100）、vGPU 模式
- 调度策略：推理任务优先低延迟 GPU（A10/A30），训练任务调度高显存 GPU（A100/H100）
- Volcano 队列优先级：确保生产推理不被训练任务抢占

#### 2.1.7 实例通用生命周期与操作语义

所有实例类型共享统一的生命周期语义（P0）：

**P0 生命周期动作：** 创建（含规划/渲染/准入/审计/dry-run/apply）、查询（状态/健康/事件）、启动、停止、重启、变配（CPU/内存/GPU/副本数）、删除

**P1/P2 生命周期动作：** 克隆、快照、备份/恢复、迁移、休眠/挂起

**P0 运维操作：**

| 操作 | VM | Container | GPU容器 | Sandbox | Batch | K8s集群 | BM |
|---|---|---|---|---|---|---|---|
| 日志 | ✅ | ✅ | ✅ | ✅ | ✅ | 代理至集群 | ✅ |
| 事件 | ✅ | ✅ | ✅ | ✅ | ✅ | 代理至集群 | ✅ |
| 指标 | ✅ | ✅ | ✅+GPU指标 | ✅ | ✅ | 集群级指标 | ✅+硬件 |
| exec/终端 | P1 | ✅ | ✅ | ✅(会话) | N/A | kubectl | P1 |
| VNC/Console | ✅ | N/A | N/A | ✅ | N/A | N/A | ✅(BMC) |
| 审计 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |

**操作反馈统一要求（P0 强制）：**
- 每个操作前执行 precheck（权限/配额/状态/provider能力）
- 不可用时展示禁用原因
- 高风险操作（停止/删除/变配）必须展示确认弹窗和影响说明
- 后端接受后返回 `operation_id`，前端展示操作时间线
- 失败时展示原因、建议和是否可重试
- 所有操作写入审计日志（不可篡改）

---

### 2.2 存储层 — 四类型统一 API

| 类型 | Core API 路径 | 底层技术 | 对标 |
|---|---|---|---|
| **块存储** | `/api/v1/volumes` | Rook-Ceph RBD + CSI | AWS EBS |
| **对象存储** | `/api/v1/objects` | MinIO（S3 兼容） | AWS S3 |
| **文件存储** | `/api/v1/filesystems` | Rook-CephFS / NFS | AWS EFS |
| **向量存储** | `/api/v1/vector-stores` | Milvus | — |

**文件存储：** 共享 POSIX 文件系统，支持多实例并发挂载；NFS 挂载点在指定 VPC 子网创建；支持快照和容量扩展；实例通过 `storage_attachments[type=filesystem]` 挂载。

**向量存储：** 创建/删除向量集合，向量检索 API（`ANI Services 知识库服务`消费此能力）；通过 Milvus adapter 封装，业务层不直接接触 Milvus SDK。

---

### 2.3 网络层

基于 KubeOVN 1.13+ 实现 VPC 级租户网络隔离：

```
/api/v1/networks/
├── vpcs/                ← VPC 创建/管理（每租户独立 VPC）
├── subnets/             ← 子网划分
├── security-groups/     ← 安全组规则（白名单出入站）
├── route-tables/        ← 路由策略
└── load-balancers/      ← 负载均衡（四层/七层）
```

**网络平面（实例必须声明）：**
- `tenant_vpc`：租户业务互通
- `foundation_mesh`：平台控制面/服务互联
- `storage`：存储访问专用
- `management`：Sandbox 出口管控/运维入口
- `public_ingress`：公网暴露（需安全审计）

---

### 2.4 身份与安全层

```
/api/v1/auth/           ← 登录/Token/OIDC/刷新/吊销
/api/v1/users/          ← 用户/角色/API Key 管理
/api/v1/tenants/        ← 租户管理（资源配额/用量）
/api/v1/secrets/        ← 密钥管理（KV + 实例绑定注入）← PaaS 凭据注入
/api/v1/encryption/     ← 国密加解密（SM4 密钥管理 + seal/unseal）
```

**多租户安全：** 所有数据表使用 PostgreSQL RLS，fail-closed 原则；vCluster 提供 K8s 控制面隔离；KubeOVN VPC 提供网络平面隔离。

**国密加解密：** SM4/ZUC 对称加密，离线加密后上传；推理时 Init Container 通过 `unseal-token` 内存解密，密钥不落盘，不经外网。

---

### 2.5 平台支撑层

| 能力 | Core API | 说明 |
|---|---|---|
| 镜像仓库 | `/api/v1/registry` | Harbor 封装，含镜像安全扫描 |
| 用量计量 | `/api/v1/metering` | 按租户/时间/资源类型统计，支持 Token 用量上报 |
| 可观测性 | `/api/v1/observability` | PromQL 代理查询 + 告警规则管理 |
| 服务目录 | `/api/v1/service-endpoints` | PaaS 服务内部 DNS 注册（`redis.prod.ani.internal`）|
| 事件通知 | `/api/v1/notifications` | Webhook/Email/内部消息订阅 |
| 审计日志 | `/api/v1/audit` | 操作审计，不可篡改，支持导出（等保合规）|

---

### 2.6 ANI Core 完整 API 范围（v1.0.0 目标）

下表描述 Core API 目标范围和当前成熟度。`contract` 表示 OpenAPI/路由或类型契约存在；`local-profile` 表示本地 dev profile 已能返回一致状态机与错误语义；`real-provider` 表示已接入真实底座组件并通过真实环境门禁。不要把 `local-profile` 误写成生产就绪。

```
/api/v1/
├── auth/                    身份与令牌                    local-profile / 持续真实集成
├── users/                   用户与 API Key                contract / 持续完善
├── tenants/                 租户与配额                    contract / 持续完善
├── instances/               所有计算实例（9 种类型）        local-profile / real-provider 收敛中
├── k8s-clusters/            K8s 集群生命周期 + API 代理    local-profile；真实 vCluster provider 待完成
├── k8s-federation/          Karmada 多集群（Phase 2）     ⏳ 预留
├── networks/vpcs            VPC                           local-profile / Kube-OVN real-provider 待完成
├── networks/subnets         子网                          local-profile / Kube-OVN real-provider 待完成
├── networks/security-groups 安全组                        local-profile / Kube-OVN real-provider 待完成
├── networks/load-balancers  负载均衡                      local-profile / real ingress/LB 待完成
├── volumes/                 块存储                        local-profile / CSI real-provider 待完成
├── filesystems/             文件存储（CephFS/NFS）         contract/local-profile；真实 provider 待完成
├── objects/                 对象存储                      local-profile；真实 MinIO/S3 provider 待完成
├── vector-stores/           向量存储（Milvus）             local-profile/search contract；真实 Milvus provider 待完成
├── baremetal/hosts          BM 硬件库存                   规划/P1+
├── gpu-inventory/           GPU 节点库存                  contract/adapter 边界
├── dpu-inventory/           DPU 节点库存                  规划/P2
├── registry/                镜像仓库                      规划/待 contract
├── encryption/              国密加解密                    local-profile；真实 KMS/SM4 provider 待完成
├── secrets/                 密钥管理与绑定                 local-profile；真实 K8s Secret 注入待完成
├── metering/                用量计量                      usage contract / 持续完善
├── observability/           指标查询与告警                 规划/待 contract
├── service-endpoints/       内部 DNS / 服务目录            规划/待 contract
├── notifications/           事件通知                      规划/待 contract
└── audit/                   审计日志                      contract/持续完善
```

**成熟度说明：** 当前缺口不再用粗粒度状态词混写。Core 文档必须区分 contract、local-profile、real-provider；Services 文档必须区分“当前首批 API 切片”和“2026-06-15 至 2026-06-20 后确认的完整 Services 功能与接口”。

---

## 三、ANI Services — 云服务与功能编排层

> 本节定义 ANI Services 的产品目标范围，不等于当前 Repo 中早期 Services 代码的最终边界。所有服务均由 Services 团队开发，通过 ANI Core OpenAPI REST API / Core SDK 实现，禁止直接操作底层基础设施或直接调用 Core 内部 gRPC service。当前 Services 采用受控并行 PR：以 `repo/api/openapi/services/v1.yaml` 为 API-first 来源，通过共同审查和 Services boundary gate 迁移陈旧逻辑，而不是由 Core 基于猜测实现或覆盖 Services 业务。

### 域A：IaaS 云服务

| 服务 | 核心功能 | 对标 |
|---|---|---|
| 云主机服务 | VM 创建/管理/镜像/快照/弹性扩缩 | AWS EC2 |
| 容器实例服务 | 容器 CRUD/滚动更新/回滚/副本管理 | AWS ECS/Fargate |
| GPU 实例服务 | GPU 容器调度/切片/利用率展示 | AWS P 系列 |
| Sandbox 服务 | Agent 沙箱会话/代码执行/文件读写/浏览器控制 | E2B |
| K8s 集群服务 | vCluster 管理/kubeconfig/节点池/插件 | AWS EKS / 原生 Kubernetes 管理体验 |
| 裸金属服务 | BM 实例申请/OS镜像/生命周期 | AWS Bare Metal |
| VPC 网络服务 | VPC/子网/安全组/路由/对等连接 | AWS VPC |
| 块存储服务 | 云盘创建/挂载/快照/扩容 | AWS EBS |
| 文件存储服务 | 共享文件系统/挂载点/快照 | AWS EFS |
| 对象存储服务 | Bucket 管理/权限/生命周期策略 | AWS S3 Console |
| 负载均衡服务 | LB 创建/监听/健康检查/证书 | AWS ALB/NLB |
| 镜像仓库服务 | 容器镜像托管/版本/安全扫描/权限 | AWS ECR / Harbor |

### 域B：AI 全生命周期平台

对标 AWS SageMaker，覆盖从数据到模型上线的完整工作流：

| 服务 | 核心功能 | 对标 |
|---|---|---|
| **Notebook 服务** | JupyterLab/VSCode 托管，持久化工作区，应用态与存储态分离 | SageMaker Studio |
| **数据集服务** | 数据集上传/版本/标注任务/血缘追踪 | SageMaker Data Wrangler |
| **实验追踪服务** | 训练指标/参数/对比/可视化（MLflow 封装） | SageMaker Experiments |
| **训练任务服务** | 单机/分布式训练，checkpoint，超参搜索 | SageMaker Training |
| **微调服务** | SFT/LoRA/RLHF/DPO，数据准备→训练→评估闭环 | SageMaker Fine-tuning |
| **模型评估服务** | Benchmark 测试/对比/指标报告 | SageMaker Clarify |
| **模型仓库服务** | 模型版本/元数据/格式转换/国密加解密/导入（HuggingFace/ModelScope/离线包） | SageMaker Model Registry |
| **推理部署服务** | 模型一键部署为推理端点，自动扩缩，蓝绿发布，OpenAI 兼容 API | SageMaker Endpoints |
| **AI API 网关** | 统一推理入口/限流/租户隔离/Token 计费/路由 | AWS Bedrock API GW |
| **Pipeline 服务** | 训练→评估→注册→部署 DAG 工作流编排 | SageMaker Pipelines |

**推理服务 P0 范围（v1.0.0）：**
- 选择模型版本 + GPU 规格 → 创建 endpoint → 查看状态
- 日志/事件/指标（QPS/延迟/错误率/Token用量）
- 启动/停止/重启/变配/删除
- 基础调用测试（OpenAI 兼容 `/v1/chat/completions`，支持流式输出 SSE）
- 内置支持：Qwen2.5、DeepSeek-V3/R1、GLM-4 等主流国内模型

### 域C：AI-Native 应用服务

| 服务 | 核心功能 | 对标 |
|---|---|---|
| **知识库服务** | 文档上传/解析/向量化/RAG问答/来源引用/知识库权限隔离 | AWS Bedrock KB / Dify |
| **Agent 开发平台** | 可视化 Agent 编排/工具注册/测试调试 | Dify / Coze |
| **Agent 运行时服务** | Agent 会话管理/工具执行/记忆/状态持久化/沙箱隔离 | E2B + LangGraph |
| **MCP 工具市场** | MCP Server 注册/发现/权限控制/版本管理 | — |
| **语音服务** | ASR（语音转文字）/ TTS（文字转语音）/ 实时转写 | AWS Transcribe |
| **文档智能服务** | OCR/版式解析/表格提取/合规审查/公文起草/摘要 | AWS Textract |
| **会议智能服务** | 录音转写/发言人区分/纪要生成/待办提取 | AWS Transcribe+Bedrock |

**知识库 P0 范围（v1.0.0）：**
- 文档格式：PDF/Word/Excel/PPT/Markdown/扫描件
- 处理流程：上传 → 自动解析 → 向量化 → 建立知识库
- 检索：混合检索（向量语义 + 关键词），来源引用（文档名+页码）
- 安全：知识库权限隔离，不同部门只能访问授权知识库
- 接入：Web 端 + API（兼容 RAG 标准接口）

### 域D：PaaS 托管服务

全部基于 K8s Operator + ANI Core 实例/存储/网络实现：

| 服务 | 核心功能 | 底层 Operator | 对标 |
|---|---|---|---|
| **托管 PostgreSQL** | 全托管，自动备份/扩缩/故障转移 | CloudNativePG | AWS RDS |
| **托管 MySQL/MongoDB** | 全托管，备份/扩缩/监控 | Percona Operator | AWS RDS |
| **托管 Redis** | 全托管，主从/集群/备份 | Redis Operator | AWS ElastiCache |
| **托管 Kafka** | 全托管，Topic管理/消费组/监控 | Strimzi | AWS MSK |
| **托管 RabbitMQ** | 全托管，Queue/Exchange 管理 | RabbitMQ Operator | AWS MQ |
| **托管 Elasticsearch** | 全托管，索引/搜索/监控 | ECK Operator | AWS OpenSearch |
| **托管函数计算** | Serverless Function，按调用付费 | OpenFaaS / Knative | AWS Lambda |
| **托管容器服务** | 无需管理 K8s，直接跑容器 | — | AWS Fargate |

**PaaS 凭据注入流程：**
```
创建托管数据库
  → Core secrets/ API 生成连接凭据
  → Core service-endpoints/ API 注册内部 DNS（redis.prod.ani.internal）
  → 租户绑定实例时凭据自动注入为环境变量
```

### 域E：平台运营支撑

| 服务 | 核心功能 | 对标 |
|---|---|---|
| **用量与计费服务** | 按租户/项目统计资源用量，Token 计费，出账单 | AWS Cost Explorer |
| **告警与通知服务** | 阈值告警/事件通知/Webhook 推送 | AWS CloudWatch Alarms |
| **审计中心** | 操作审计/合规报表/数据导出（等保 2.0 三级） | AWS CloudTrail |
| **BOSS 运营平台** | 租户管理/配额管理/平台健康大盘/白牌化 | — |

---

## 四、集成接口

| 接口类型 | 说明 |
|---|---|
| **ANI Core API 契约** | `api/openapi/v1.yaml`，基础设施资源接口（`/api/v1/` 前缀写在 spec 里），唯一真实来源 |
| **ANI Services API 契约** | `api/openapi/services/v1.yaml`，云服务层接口（`/api/v1/svc/` 前缀），另一小组维护 |
| **Go SDK** | 从 Core API 契约自动生成，`github.com/kubercloud/ani-go` |
| **Python SDK** | 从 Core API 契约自动生成，`pip install kubercloud-ani` |
| **TypeScript API Client** | 类型安全的 HTTP 客户端（openapi-typescript + openapi-fetch），`@kubercloud/ani-client` |
| **Java SDK** | 从 Core API 契约自动生成（OkHttp3），`com.kubercloud:ani-java`，面向企业系统集成 |
| **OpenAI 兼容 API** | 推理服务提供 `/v1/chat/completions`，已有 OpenAI SDK 系统无需修改代码 |
| **ani CLI** | 命令行工具，覆盖 Core + Services 核心操作 |
| **Webhook** | 任务完成/告警触发/实例状态变更等事件主动推送 |
| **SSO 集成** | SAML 2.0 / OAuth 2.0 / OIDC，与企业现有身份系统（AD/LDAP）打通 |
| **企业微信/钉钉 Bot** | 知识库问答可直接在聊天工具内使用 |

---

## 五、v1.0.0 P0 交付范围（2026-09-30）

### 5.0 成熟度与交付承诺

ANI Core Phase 1 采用 **宽语义、稳契约、硬目标成熟度** 的策略：

- **横向能力必须宽**：计算、存储、网络、安全、认证、鉴权、审计、计量、SDK、CLI、Installer 等能力按长期平台底座完整规划，避免未来 ANI Services 新场景反向撕裂 Core API。
- **接口契约必须早**：凡是 ANI Services P0 场景依赖的 Core 能力，必须在 Services 开发启动前完成 API/Schema/错误码/状态机/RBAC scope/SDK 示例的冻结。
- **实现成熟度必须硬**：`contract` 不是交付完成，只是起点。任何阻塞 Services P0 的能力，到对应 deadline 前必须达到 `real-path`。

成熟度定义：

| 成熟度 | 含义 | 交付承诺 |
|---|---|---|
| `contract` | API/Schema/Port 已定义，SDK 可生成 | 不能作为 Services P0 最终依赖 |
| `local-profile` | 有本地/mock adapter，语义与真实 API 一致 | 可解锁前端/Services 早期开发 |
| `real-provider` | 接入真实 K8s/KubeVirt/存储/网络 provider，在测试环境跑通 | 可进入联调 |
| `real-path` | 端到端可用，含权限、审计、幂等、operation timeline、状态查询、错误语义和基础测试 | 可用于 Phase 1 POC 交付 |
| `production` | 增加 HA、升级、备份恢复、安全验收、运维文档和容量验证 | 可作为长期生产承诺 |

**强制规则：** 凡是 ANI Services Phase 1 P0 场景依赖的 ANI Core 能力，不允许在 2026-09-30 前停留在 `contract`、`local-profile`、stub、mock success 或 `NOT_IMPLEMENTED`。

### ANI Core v1.0.0 必须交付

| 能力域 | P0 必须交付 | 2026-09-30 目标成熟度 | Services 解锁要求 |
|---|---|---|---|
| 计算实例 | VM / Container / GPU Container / Batch Job | `real-path` | 2026-06-10 API Alpha；2026-06-30 dev profile；2026-07-31 real path |
| Sandbox | Sandbox 资源模型、RuntimeClass、隔离策略、实例入口 | `real-provider`，若被 Agent 场景启用则必须 `real-path` | 2026-06-20 contract；2026-08-15 real-provider |
| K8s 集群 | vCluster 模式、原生 API 代理、基本 RBAC、多租户隔离 | `real-provider` | 2026-06-20 contract；2026-08-15 real-provider |
| 裸金属 | BM 硬件库存管理 + Redfish/IPMI 基础模型 | `contract`，若客户 POC 明确依赖则升级为 `real-provider` | 2026-06-20 contract |
| 存储 | 块存储、对象存储、文件存储、实例挂载 | 阻塞实例链路的块/对象能力为 `real-path`，其余至少 `contract` | 2026-06-10 API Alpha；2026-07-31 real path |
| 向量存储 | 创建集合 + 检索 API，供 Services 知识库消费 | `real-path` | 2026-06-20 API Alpha；2026-08-15 real path |
| 网络 | VPC、子网、安全组、基础 LB | VPC/子网/安全组为 `real-path`，复杂 LB 为 `real-provider` | 2026-06-10 API Alpha；2026-07-31 real path |
| 身份 | Auth + RBAC + OIDC + API Key + 多租户 RLS | `real-path` | 已完成 M2.2 Auth Final（2026-05-18）；后续只随 Services 依赖补合同守卫 |
| 加解密 | 国密 SM4 密钥管理 + 模型 seal/unseal-token | `real-path` | 2026-06-20 API Alpha；2026-08-15 real path |
| 密钥管理 | Secrets CRUD + 实例绑定注入 | `real-path` | 2026-06-20 API Alpha；2026-08-15 real path |
| 硬件库存 | GPU 库存、BM 库存、DPU 库存基础模型 | GPU 为 `real-path`，BM/DPU 至少 `contract` | 2026-06-20 contract；2026-07-31 GPU real path |
| 镜像仓库 | Harbor 封装，基础推拉权限 | `real-provider` | 2026-06-30 unblock；2026-08-15 real-provider |
| 计量 | 实例用量统计 + Token 用量上报 | `real-path` for P0 报表 | 2026-06-30 API Alpha；2026-08-31 real path |
| 可观测 | 指标查询代理 + 基础告警规则 | `real-provider` | 2026-06-30 unblock；2026-08-31 real-provider |
| 平台支撑 | service-endpoints、notifications 基础能力 | 阻塞 Services 的 service-endpoints 为 `real-path`，notifications 至少 `contract` | 2026-06-30 API Alpha |
| SDK | Go / Python / TypeScript / Java SDK | `real-path` | 2026-06-20 SDK Alpha；2026-06-30 import/compile smoke test |
| CLI | ani 覆盖 Core P0 资源核心命令 | `real-provider` | 2026-08-31 CLI Alpha |
| Installer | 裸机/VM/已有K8s 三种部署模式，离线包 | 交付主路径 `real-path`，非主路径 `real-provider` | 2026-08-31 integration RC |

### ANI Services P0 必须交付

| 服务 | P0 必须交付 |
|---|---|
| 模型仓库 | 上传/版本/元数据/国密加解密/HuggingFace/ModelScope 导入 |
| 推理服务 | 部署端点/状态查询/日志/OpenAI 兼容调用测试 |
| 知识库 | 文档上传/解析/向量化/RAG 问答/来源引用 |
| IaaS 控制台 | 主要实例类型（VM/Container/GPU/Sandbox）+ 存储 + 网络 Console |

### Core 对 Services 的解锁矩阵

| Services P0 场景 | 依赖 ANI Core 能力 | Core Interface Ready | Core Runtime Ready | 阻塞级别 |
|---|---|---:|---:|---|
| IaaS 控制台创建 VM | instances、volumes、networks、security-groups、secrets、operations、auth/rbac | 2026-06-10 | 2026-07-31 | P0 阻塞 |
| 模型一键部署 | gpu-inventory、gpu_container instance、objects、secrets、operations、metering | 2026-06-10 | 2026-07-31 | P0 阻塞 |
| 知识库文档上传与检索 | objects、vector-stores、tasks、audit、metering | 2026-06-20 | 2026-08-15 | P0 阻塞 |
| 用量报表 | metering、audit、metrics | 2026-06-30 | 2026-08-31 | P0 阻塞 |
| API Key 与租户权限 | auth、api-keys、rbac、tenant RLS | 已完成（2026-05-18）| 已完成（2026-05-18）| 已解除阻塞，后续只做回归守卫 |

**Core Interface Ready** 表示 API、SDK、状态机、错误码、RBAC scope、示例和 dev profile 足以让 Services 开始真实开发。
**Core Runtime Ready** 表示真实 provider 路径达到目标成熟度，可进入 Services 联调和客户 POC 验收。

---

## 六、未来扩展路径（Phase 2+）

以下能力已在架构设计中预留扩展点，Phase 2+ 可按需接入，不会破坏 v1.0.0 边界：

| 扩展方向 | 实现路径 |
|---|---|
| Kata + Firecracker Sandbox（P1）| 新增 RuntimeClass adapter，不改 WorkloadRuntime port |
| 新 GPU 厂商（海光二代、摩尔线程等）| 新增 GPUInventory adapter，不改调度层 |
| DPU 卸载服务（P2）| 新增 DPUProvider adapter + dpu-inventory API |
| Karmada 多集群（Phase 2）| 新增 k8s-federation API + Karmada adapter |
| VM 快照/迁移（P1）| 新增生命周期动作，复用现有 WorkloadRuntime port |
| PaaS Operator 扩展 | Services 层新增 Operator，Core 只需保证 secrets + service-endpoints 稳定 |
| 信创适配（UOS/麒麟/鲲鹏/飞腾）| 新增 RuntimeClass + 硬件 profile，不改业务逻辑 |
| 多 Region/AZ（Phase 2）| 新增 ClusterProvider adapter，WorkloadInstanceOrchestrator 不变 |
| Karmada 跨集群调度（Phase 2）| 新增 federation propagation-policy，不改单集群 instance 链路 |
| 等保三级合规文档 | 基于已有审计/RBAC/RLS 能力补充合规报告 |

---

## 七、产品边界（不做的事）

| 不做 | 原因 |
|---|---|
| 自研大语言模型 | 依托 Qwen/DeepSeek 等开源模型，不重复投入 |
| 模型从零预训练 | 需要超算级集群和海量数据，超出定位范围 |
| 公有云 AI API 中转代理 | 客户核心诉求是数据不出网，与此相悖 |
| 全部 K8s API 的 OpenAPI 封装 | K8s API 宽泛、快速迭代，只做生命周期封装 + 原生 API 代理 |
| ANI Core 内实现业务逻辑 | 模型推理配置/RAG 策略/PaaS 业务规则属于 Services 层 |
| 直接向客户暴露 Milvus/Redis 原始 API | 通过 Core vector-stores/secrets API 封装，保持松耦合 |
