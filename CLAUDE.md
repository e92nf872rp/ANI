# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

---

## ⚡ 新开发者 / 新 AI 实例：5 分钟快速上手

> 如果你是刚打开这个项目的开发者（人类或 AI 工具），从这里开始。
> **不需要读完整个 CLAUDE.md**，按以下步骤在 5 分钟内进入工作状态。

### Step 1：验证环境（1 分钟）

```bash
cd repo
make build && make test
# 期望：83+ tests PASS，4 个服务二进制生成在 bin/
```

如果失败，先看 `repo/CURRENT-SPRINT.md` 的「环境启动」节。

### Step 2：了解当前任务（2 分钟）

打开 **`repo/CURRENT-SPRINT.md`** — 这是你最重要的工作文档：
- 当前在做哪个冲刺
- 今天该做什么（P0/P1 优先级）
- 怎么验证完成

### Step 3：理解核心架构（2 分钟）

按顺序读这 3 个文件，够了：

```
1. pkg/ports/workload_runtime.go          ← 13个核心接口，理解整体边界
2. pkg/adapters/runtime/instance_orchestrator.go  ← 编排链路实现
3. api/openapi/v1.yaml（头部注释）        ← API 契约结构
```

### Step 4：知道什么不能做

- 不直接调用 K8s/KubeVirt/MinIO/Milvus SDK → 通过 `pkg/ports/` 接口
- 不先写代码再写文档 → API 契约（`api/openapi/v1.yaml`）先行
- 不说"OpenAPI"（会与 OpenAI 混淆）→ 说"API 契约"
- Services 层代码禁止 import `pkg/` 或 `services/ani-gateway/` → 只调 REST API

### 关键文件地图

```
CLAUDE.md                           ← 你正在读的（完整约定）
repo/CURRENT-SPRINT.md              ← 当前冲刺上手指南（最常用）
ANI-02-产品功能设计.md              ← 产品功能全景
ANI-06-开发计划.md                  ← 冲刺计划 + Phase 2 延期条目
repo/pkg/ports/                     ← 所有能力接口定义
repo/api/openapi/v1.yaml            ← Core API 契约
repo/deploy/migrations/             ← 数据库 Schema（3 个 SQL）
```

---

## 项目背景

广州常青云科技有限公司旗下战略级新产品 **KuberCloud ANI**（KuberCloud AI-Native Infrastructure，中文名：AI专有云）的规划文档集，当前产品定义版本 **V8**。

**必读顺序（任何参与本项目的 Claude 实例，在开始任何任务前必须先读）：**

```
ANI-00  产品战略与开发哲学   ← 最高优先级，所有决策的出发点
ANI-01  客户画像与场景分析
ANI-02  产品功能设计
ANI-03  产品路线图
ANI-04  技术栈设计
ANI-05  系统架构设计
ANI-06  开发计划            ← 详细开发点拆解与月度里程碑
ANI-07  部署工程设计        ← Installer、多部署目标、Region/AZ、计量、白牌化
ANI-08  安全架构设计        ← 平台自身安全（第一层）+ 安全服务能力（第二层）
ANI-09  数据模型设计        ← PostgreSQL 全量表结构 / Milvus Collection / Redis Key / MinIO Bucket
ANI-10  GPT审查提示词集     ← 8组结构化审查提示词，用于 GPT 5.5 跨模型设计审查
ANI-11  代码实现规范        ← Repository接口、三条核心流程时序、Bootstrap模式、Go规范、前端路由
ANI-12  版本管理策略        ← SemVer、发布门禁、Git tag、升级包、兼容性边界
ANI-13  开源组件松耦合适配器架构 ← MinIO/Milvus/NATS/Redis/Harbor 等默认组件的 ports/adapters 强制边界
```

**代码仓库位置：** `repo/`（相对于本文档目录）

> **当前态 vs 目标态说明（V8 架构迁移中）：**
> - 以下目录标注了`【当前】`的是代码实际位置，`【目标】`的是 V8 规划最终位置。
> - `model-service`、`kb-service` 逻辑上属于 ANI Services 层，但代码暂存在 Core 仓库；禁止 Core 服务调用它们，只允许它们调用 Core API。

```
repo/
│
│  ── ANI Core（本小组负责）─────────────────────────────────────────────
├── services/                      # 【当前】Core 服务实际位置
│   ├── ani-gateway/               # Core REST API 网关（Hertz）
│   ├── auth-service/              # 身份与鉴权服务
│   ├── task-service/              # 异步任务服务
│   ├── metering-service/          # 用量计量服务
│   ├── model-service/             # ⚠️ 逻辑属 Services 层，暂存此处，禁止 Core 调用
│   └── kb-service/                # ⚠️ 逻辑属 Services 层，暂存此处，禁止 Core 调用
│
├── pkg/
│   ├── ports/                     # 能力接口定义（workload_runtime/gpu_inventory 等）
│   ├── adapters/                  # 开源组件适配器（nats/redis/objectstore 等）
│   └── bootstrap/                 # 能力装配与配置
│
├── api/
│   ├── openapi/
│   │   ├── v1.yaml                # ANI Core API 契约（唯一真实来源，先于代码）
│   │   │                          # servers[0].url = "https://{host}/api/v1"
│   │   │                          # 只包含基础设施资源：instances/networks/volumes 等
│   │   └── services/
│   │       └── v1.yaml            # ANI Services API 契约（另一小组维护）
│   │                              # servers[0].url = "https://{host}/api/v1/svc"
│   │                              # 包含：models/inference/knowledge-bases/paas 等
│   └── proto/                     # gRPC Protobuf 定义（Core 内部 RPC）
│
│  ── SDK 输出（从 API 契约自动生成）──────────────────────────────────────
├── sdks/                          # 【目标】SDK 统一输出目录
│   ├── ani-go/                    # Go SDK（oapi-codegen 生成）
│   ├── ani-python/                # Python SDK（openapi-generator 生成）
│   ├── ani-typescript/            # TypeScript API Client（openapi-typescript）
│   └── ani-java/                  # Java SDK（openapi-generator + OkHttp3）
│
│  ── ANI Services（另一小组负责，暂存于本 monorepo）────────────────────
│  （V8 目标：Services 代码迁移至独立仓库或 repo/ani-services/ 子目录）
│
│  ── 其余共享目录──────────────────────────────────────────────────────
├── frontends/{console,boss}/      # TypeScript，React 18 + TDesign
├── cli/ani/                       # Go，ani CLI
├── installer/ani-installer/       # Go，bubbletea TUI 安装程序
├── operators/                     # K8s Operators（inference-operator 等）
├── deploy/                        # Helm Chart / manifests / migrations
├── ai/                            # Python AI 服务（rag-engine 等）
├── Makefile                       # make deps / make dev-gateway / make test
└── .env.example                   # 环境变量模板，cp 为 .env
```

**API 前缀规范（强制）：**
- ANI Core API `servers[0].url` 必须为 `https://{host}/api/v1`，路径写在 spec 而非 Gateway 路由层
- ANI Services API `servers[0].url` 必须为 `https://{host}/api/v1/svc`
- Gateway 做透明转发，不做路径前缀变换；spec 是完整 URL 的唯一真实来源
- v2 升级时：新建 `api/openapi/v2.yaml`，`servers[0].url` 改为 `/api/v2`，v1 和 v2 并行

**SDK 生成规范（强制）：**
- `make gen-core-sdk`：从 `api/openapi/v1.yaml` 生成 Go + Python + TypeScript + Java 四套
- `make gen-services-sdk`：从 `api/openapi/services/v1.yaml` 生成 Python + TypeScript + Java 三套
- SDK 不能包含对方层的资源类型
- Services 团队只依赖 Core SDK，不 import Core 代码包

## 产品分层架构强制约束（V8，所有实例必须遵守）

### 两层产品定义

ANI 产品由两层构成，**层间只允许通过 REST API / SDK 通信，严禁跨层直接代码引用**：

```
┌──────────────────────────────────────────────────────────────────────────┐
│  ANI Services（另一小组开发，位于 repo/services/）                         │
│  IaaS控制台 · AI全生命周期平台 · AI-Native应用 · PaaS托管服务              │
│  只调用 ANI Core REST API 或 Go/Python SDK，禁止 import repo/core/ 代码   │
├──────────────────────────────────────────────────────────────────────────┤
│  ANI Core v1.0.0（本小组开发，位于 repo/core/）                            │
│  计算 · 存储 · 网络 · 身份 · 可观测 · 平台管理                             │
│  统一对外输出：REST API（api/openapi/v1.yaml）+ Go SDK + Python SDK + CLI │
└──────────────────────────────────────────────────────────────────────────┘
```

### ANI Core 能力边界（完整清单）

ANI Core 只实现以下基础设施能力，**不得包含任何业务应用逻辑**：

**计算（Compute）**

| 实例类型 | 底层技术 | 阶段 |
|---|---|---|
| VM（云主机） | KubeVirt + KVM | P0 |
| Container（容器） | Kubernetes Pod/Deployment | P0 |
| GPU Container（GPU容器） | K8s + HAMi + Volcano | P0 |
| Sandbox（安全沙箱） | **Kata Containers + QEMU（P0）**；Kata+Firecracker（P1） | P0 |
| Batch Job（批任务） | Kubernetes Job + Volcano | P0 |
| Notebook（开发环境） | K8s Pod + PVC（生命周期独立于应用状态） | P1 |
| K8s Cluster（租户K8s集群） | vCluster（共享物理集群）或 Metal3（独立物理集群） | P1 |
| Bare Metal（裸金属） | Metal3 + Ironic + BMC/Redfish | P1 |
| DPU Node（DPU加速节点） | NVIDIA DPF（DOCA Platform Framework） | P2 |

**Sandbox 技术选型约束（已确认）：**
- P0 默认：Kata Containers + QEMU 后端，RuntimeClass `sandbox-kata`，完整 Linux syscall + GPU Passthrough（NVIDIA GPU Operator 官方支持 `kata-qemu-nvidia-gpu`）
- P1 补充：Kata Containers + Firecracker 后端，RuntimeClass `sandbox-kata-fc`，~150ms 启动，CPU-only
- P2 关注：gVisor（`sandbox-gvisor`），轻量 CPU 密集场景
- **不选**：纯 Firecracker（非 K8s 原生），Wasm（Python 生态支持不完整）

**K8s 集群管理架构约束（已确认）：**
- 集群生命周期（创建/升级/删除）：ANI Core REST API 封装（OpenAPI）
- 工作负载操作（Pod/Service/Deployment 等原生 K8s 资源）：**原生 K8s API 透明代理**，不二次封装，确保 kubectl/Helm/Argo CD 原生工具链完全兼容
- 多租户隔离：每租户一个 vCluster（独立 API Server + etcd），运行在 ANI 物理 K8s 上，KubeOVN VPC 提供网络隔离
- 多集群管理：Karmada 封装，ANI Core 提供高层 propagation-policy API
- 支持能力：Pod、Service、Deployment、StatefulSet、DaemonSet、Job、CronJob、Ingress、CRD、RBAC、NodePool 等完整 K8s 功能与当前最新 K8s 版本一一对齐

**存储（Storage）**

| 类型 | 对应 Core API | 底层技术 | 对标 |
|---|---|---|---|
| 块存储（Block） | `/api/v1/volumes` | Rook-Ceph RBD / CSI | AWS EBS |
| 对象存储（Object） | `/api/v1/objects` | MinIO | AWS S3 |
| 文件存储（File） | `/api/v1/filesystems` | **Rook-CephFS / NFS** | AWS EFS |
| 向量存储（Vector） | `/api/v1/vector-stores` | Milvus | Pinecone |

**网络（Network）**：`/api/v1/networks/*`，含 VPC、子网、安全组、路由、负载均衡，底层 KubeOVN

**身份（Identity）**：`/api/v1/auth/*`，`/api/v1/users/*`，OIDC/SSO、RBAC、API Key、多租户

**硬件库存**：`/api/v1/gpu-inventory`（GPU）、`/api/v1/dpu-inventory`（DPU）、`/api/v1/baremetal/hosts`（BM）

**平台支撑**：`/api/v1/registry`（镜像仓库）、`/api/v1/encryption`（国密加解密）、`/api/v1/secrets`（密钥管理）、`/api/v1/metering`（用量计量）、`/api/v1/observability`（指标/告警）、`/api/v1/service-endpoints`（内部DNS/服务目录）、`/api/v1/notifications`（事件通知）、`/api/v1/audit`（审计）

### ANI Services 能力边界（完整清单）

ANI Services 覆盖四个域，**全部通过 ANI Core SDK/API 实现，禁止直接操作 K8s/KubeVirt/底层组件**：

| 服务域 | 主要服务 |
|---|---|
| **IaaS 云服务** | 云主机/容器/GPU实例控制台、VPC管理、块/对象/文件存储服务、镜像仓库服务、负载均衡服务、K8s集群服务 |
| **AI 全生命周期** | Notebook开发环境、数据集服务、实验追踪、训练任务、微调服务、模型仓库、推理部署、AI API网关、Pipeline编排 |
| **AI-Native 应用** | 知识库/RAG、Agent开发平台、Agent运行时、MCP工具市场、语音/文档/会议智能服务 |
| **PaaS 托管服务** | 托管数据库（PostgreSQL/MySQL/Redis/MongoDB）、托管消息队列（Kafka/RabbitMQ）、托管搜索（Elasticsearch）、托管函数计算、托管容器服务 |

### ANI Core API 契约管理强制规则

1. **`api/openapi/v1.yaml` 是 ANI Core 所有 REST API 的唯一真实来源**，团队内部统一称其为"**API 契约**"（避免与 OpenAI 混淆），不得称"OpenAI 接口"
2. Core API 契约的 `servers[0].url` **必须**为 `https://{host}/api/v1`；Services API 契约（`api/openapi/services/v1.yaml`）的 `servers[0].url` **必须**为 `https://{host}/api/v1/svc`；前缀写在 spec 里，Gateway 不做路径变换
3. 所有新 Core API 端点必须**先写入 `api/openapi/v1.yaml` 并通过评审**，再生成代码；禁止先写代码后补文档
4. `api/openapi/v1.yaml` 只能包含基础设施资源（instances/networks/volumes/auth/gpu-inventory 等）；**模型仓库（models）、推理服务（inference-services）、知识库（knowledge-bases）等业务资源**必须在 `api/openapi/services/v1.yaml` 中维护
5. API 契约冻结目标：**2026-07-15**，之后 Core API 只增不删，为 Services 团队提供稳定的开发基础
6. SDK 生成规范：`make gen-core-sdk` 生成 Go + Python + TypeScript + Java 四个 SDK；`make gen-services-sdk` 生成 Python + TypeScript + Java 三个 SDK；**SDK 不得包含对方层的资源类型**
7. Services 团队使用 Core SDK（`sdks/ani-python/`）和 Mock Server 调用 Core API，不得 import Core 代码包
8. 推理实例（Inference Instance）的**创建/生命周期/GPU调度属于 ANI Core**；模型加载/vLLM配置/端点路由属于 ANI Services
9. `repo/services/model-service/` 和 `repo/services/kb-service/` 逻辑属于 ANI Services：**禁止 Core 服务（ani-gateway/auth-service/task-service）调用它们；它们只能调用 Core API，不得 import Core 代码包**

### API 工程约定（AWS 最佳实践，V8 引入）

#### 约定1：API 向后兼容性承诺

所有 ANI Core API 的版本兼容规则（`api/openapi/v1.yaml` 在 v1 生命周期内必须遵守）：

**向后兼容（不触发 v2）：**
- 新增可选的 request 字段（客户端不传时有合理默认值）
- 新增 response 字段（客户端应容忍未知字段）
- 新增 API 端点
- 新增枚举值（客户端代码必须能容忍未知枚举，不得用 switch-without-default）

**破坏性变更（必须升到 v2，新建 api/openapi/v2.yaml）：**
- 删除或重命名 request/response 字段
- 修改字段类型（string→int 等）
- 删除 API 端点
- 修改 HTTP 方法（GET→POST）
- 修改错误码语义

**弃用流程：** 字段/端点弃用前至少 180 天，在 API 响应 Header 中加 `Deprecation: <date>` + `Sunset: <date>`

#### 约定2：所有 mutation API 必须支持幂等性令牌

所有 POST（创建）和有副作用的 PUT/PATCH（更新）必须接受 `idempotency_key` 字段（UUID）：
- Server 端对同一 `(tenant_id, idempotency_key)` 的请求，24 小时内返回相同响应（不重复执行）
- 客户端超时重试时使用相同 idempotency_key，保证不会重复创建资源
- `idempotency_key` 应由客户端生成（UUID v4），不由服务端生成
- 四语言 SDK 必须提供 `with_idempotency_key(key)` 辅助方法

#### 约定3：控制平面与数据平面必须分离

**数据平面崩溃不影响控制平面，控制平面崩溃不影响数据平面。**

具体要求：
- 运行中的 VM/容器/Sandbox 在 ani-gateway/auth-service 宕机时**必须继续运行**
- 必须有独立的 `WorkloadReconcileController` 后台服务，持续对齐 K8s 实际状态 → `workload_instances` DB，不依赖 API 请求触发
- `WorkloadReconcileController` 扫描间隔：正常 30s，实例进入非终态（provisioning/starting/stopping）后降至 5s 直到终态
- `WorkloadStatusReconciler` port 的在线调用（create/lifecycle 链路内）与后台 Controller 共享同一实现，但后者独立运行

#### 约定4：Proto 与 OpenAPI 主从关系

- **OpenAPI（api/openapi/v1.yaml）是对外 REST API 的唯一真实来源**，修改需经 API 评审
- **proto（api/proto/\*）是内部微服务 gRPC 的实现细节**，修改不需要 API 评审，但不得暴露给外部消费者
- proto 字段命名可以与 REST schema 不同，SDK 生成从 OpenAPI 走，不从 proto 走
- 凡是 proto 和 OpenAPI 描述同一资源时，出现不一致以 OpenAPI 为准

#### 约定5：ANI Services 各服务使用独立 base path

Services 层每个服务有自己的 base path，**不使用统一的 `/api/v1/svc/` 前缀**：

```
模型仓库服务：  https://{host}/models/v1/
推理服务：      https://{host}/inference/v1/
知识库服务：    https://{host}/kb/v1/
训练服务：      https://{host}/training/v1/
PaaS 服务：     https://{host}/paas/{service-name}/v1/
```

理由：Services 各服务可以独立版本演化（模型仓库 v2 不影响推理服务 v1），独立部署时可以配独立域名，不需要通过 Gateway 统一路由。

#### 约定6：Workload Identity（工作负载身份）

运行中的实例调用 ANI Core API **禁止使用长期静态 API Key**。替代方案：

**P0 最小实现（lifecycle-bound API Key）：**
- 实例创建时由 Core 生成一个 `scoped API Key`，权限范围限定为该租户内该实例的特定操作
- 绑定实例生命周期：实例删除时自动 revoke
- 通过 Secrets API 注入为实例环境变量 `ANI_WORKLOAD_TOKEN`，有效期与实例共存亡
- 此类 token 在 `api_keys` 表中有 `instance_id` 字段和 `scope` 字段标识，区别于普通 API Key

**P1 升级（IRSA 风格）：**
- 每个实例获得唯一 K8s ServiceAccount，通过 K8s OIDC 换取短期 ANI Token（1小时自动轮换）
- 实例内通过 `ANI_TOKEN_ENDPOINT` 环境变量发现 Token 端点，SDK 自动处理续约

---

### 可扩展性设计强制原则

本架构设计必须支持"未来功能增加不破坏现有边界"：

1. **新实例类型扩展**：通过在 `WorkloadRuntime` port 新增 kind 实现，不修改 orchestrator/audit/admission 链路
2. **新存储类型扩展**：通过新增 StorageProvider adapter 实现，不修改实例服务层
3. **新网络能力扩展**：通过新增 NetworkProvider adapter 实现
4. **新硬件加速类型**：通过新增 Hardware Inventory port + adapter 实现（参照 GPUInventory 模式）
5. **新 PaaS 服务**（Services 层）：通过调用 Core API 组合实现，不下沉到 Core
6. **多云/多集群**：通过 ClusterProvider adapter 扩展，不修改 WorkloadInstanceOrchestrator
7. 每次新能力接入前必须先在设计文档中定义 port 接口，评审通过后再实现 adapter

---

## 开发阶段命名强制约定

为避免产品计划阶段与 AI 代码生成批次混淆，所有 Claude/Codex/GPT 实例必须遵守：

1. `ANI-06` 中的 `模块 1/2/3...` 是产品开发计划的唯一模块编号来源。
2. 代码生成批次不得再使用 `Stage 3A/3B/3C` 这类容易误解为模块 3 的名称。
3. 代码生成批次必须使用可回溯命名：`M{模块号}.{小节号}-{主题}-{批次}`，例如 `M2.1-TASK-A`。
4. **已完成批次的完整记录位于 `repo/development-records/README.md`**（不在本文件中维护，避免 CLAUDE.md 过重）。
   当前整体进度快照在 `ANI-06-开发计划.md` Section 零。当前 Sprint 任务在 `repo/CURRENT-SPRINT.md`。
5. `Stage 3A/3B/3C` 只允许作为历史旧名出现，并必须注明”不代表模块 3”。
6. 每个批次完工后：在 `repo/development-records/` 新建对应记录文件，更新 `repo/development-records/README.md` 归档索引，更新 `ANI-06` Section 零的状态快照，更新 `repo/CURRENT-SPRINT.md` 的当前状态。

## 版本管理强制约定

所有发布、tag、镜像、Helm Chart、升级包和 Codex Cloud 交付分支必须遵守 `ANI-12-版本管理策略.md`：

1. ANI 采用 SemVer 2.0：`vMAJOR.MINOR.PATCH[-pre.N]`。
2. 首个正式版本为 `v1.0.0`，目标日期 `2026-09-30`，对应 Phase 1 POC 交付就绪。
3. 当前仍处于 `v0.x` 开发期，不得标记为 `v1.0.0` 或 `rc`。
4. 产品阶段、开发模块、代码生成批次与版本号互相独立：`模块 3`、`M2.1-TASK-C` 都不是 SemVer。
5. 任何 API/Proto/DB/CRD/Helm/安全模型的破坏性变更必须按 `ANI-12` 判断 MAJOR/MINOR/PATCH。

**三条核心原则（来自 ANI-00）：**
1. 产品完全从零构建，底层最大化复用成熟开源组件，ANI 的价值在于"编排"与"封装"
2. 全程利用 AI 大模型辅助编码加速开发，架构设计必须对 AI 辅助友好（Spec-First、强类型、单一职责）
3. 这是生产级平台，不是原型或玩具——稳定性、可扩展性、可观测性、安全性有明确的量化标准

**开源组件松耦合强制原则：**
- 除 Kubernetes API 外，业务服务不得直接依赖 MinIO、Milvus、NATS JetStream、Redis、Harbor、CloudNativePG 等开源组件 SDK。
- 业务服务必须依赖 ANI 自定义能力接口，默认组件只能出现在 adapter、bootstrap wiring、deploy profile 和集成测试中。
- `make test` 会执行 `make validate-architecture`，新增直接组件 SDK 导入必须先进入显式 allowlist 并标注迁移批次。
- 架构优先级是可用性/稳定性优先，其次性能可控性与扩展性；核心组件可采用 `bounded_direct`，但必须限定模块边界并记录理由。
- VM、普通容器、GPU 容器、Sandbox、Batch Job、Notebook、K8s 集群、裸金属（BM）、DPU 节点全部必须先经过 `WorkloadRuntime` 能力抽象；模型部署不得直接把 Pod/Deployment/KubeVirt 细节写入业务流程。推理实例的创建/生命周期同样走 `WorkloadRuntime`，模型加载和端点路由属于 ANI Services 不得下沉到 Core。
- 异构 GPU 发现、分类和调度必须先经过 `GPUInventory` 能力抽象；厂商差异只能出现在 adapter、deploy profile、preflight 或 bounded runtime module。
- 所有实例必须声明网络平面和存储附件：租户业务互通走 `tenant_vpc`，平台控制/服务互联走 `foundation_mesh`，存储走 `storage`，运维入口走 `management`，公网暴露走 `public_ingress`。
- Provider renderer 只能输出 dry-run/review manifest；真实 apply/create 必须在后续受控 adapter 中接入 server-side dry-run、审计和权限检查后才允许。
- 所有 provider manifest 必须先通过 `WorkloadAdmission`；缺少 dry-run 标记、租户/实例标签、网络平面注解，或包含 hostNetwork/privileged 风险时必须拒绝。
- 实例计划、渲染 manifest、准入结果必须通过 `WorkloadPlanAuditStore` 持久化到租户 RLS 审计表，未审计不得进入真实 provider dry-run/apply。
- Provider dry-run 必须通过 `WorkloadProviderDryRun`；本地实现只校验 provider/kind/apiVersion 映射，未来 Kubernetes/KubeVirt 实现必须使用 server-side dry-run，仍不得绕过审计。
- Provider apply/create 必须通过 `WorkloadProviderApply`；默认执行开关关闭，真实 adapter 必须校验用户、租户、权限证明、审计 ID、admission、dry-run 结果和操作白名单，禁止业务服务直接 apply provider 资源。
- Provider 状态回写必须通过 `WorkloadStatusReconciler`；业务服务不得直接轮询 Kubernetes/KubeVirt/客户云状态 API，provider observation 必须携带 tenant、instance、audit 和 resource refs 关联证据。
- Provider status reader 必须通过 `WorkloadProviderStatusReader` 封装，业务服务创建实例必须优先使用 `WorkloadInstanceOrchestrator`，不得手动串联 renderer/dry-run/apply/status/reconcile 细节。
- 实例查询和恢复必须通过 `WorkloadInstanceStore` 或其上层服务，不能依赖 `PlanningRuntime` 内存状态；持久化记录必须包含 tenant、instance、kind、state、audit ID、provider ID 和 resource refs。
- Kubernetes/KubeVirt 真实执行必须通过 `KubernetesProviderAdapter` 及其内部 `KubernetesProviderClient`；server-side dry-run 必须使用 `dryRun=All`，apply 默认关闭，业务服务不得导入 provider SDK。
- 真实 KubernetesProviderClient 接入前必须先通过 `M1-INSTANCE-N` 执行剖面；后续只能替换 adapter-owned client 实现，不得改变 orchestrator、audit、admission、dry-run、apply、status reconcile 链路。
- 当前真实 KubernetesProviderClient 第一版为 adapter-owned `KubernetesRESTClient`，只使用标准库 HTTP；后续替换为 client-go/KubeVirt client 时仍必须留在 adapter-owned package。
- bootstrap 默认使用 local provider；只有显式设置 `WORKLOAD_PROVIDER=kubernetes_rest` 且提供 `KUBERNETES_API_HOST` 时才接入 `KubernetesRESTClient`，`WORKLOAD_PROVIDER_APPLY_ENABLED` 默认关闭。
- 已存在实例的真实生命周期操作必须通过 `WorkloadInstanceLifecycleExecutor`；`WORKLOAD_LIFECYCLE_PROVIDER=kubernetes_rest` 且 `WORKLOAD_LIFECYCLE_APPLY_ENABLED=true` 时才允许调用 Kubernetes/KubeVirt 生命周期 API。
- 实例可视化运维真实执行必须通过 `WorkloadInstanceOps` adapter；`WORKLOAD_OPS_PROVIDER=kubernetes_rest` 且 `WORKLOAD_OPS_ENABLED=true` 时才允许调用 Kubernetes logs/events/metrics/exec API。
- M1 真实 provider 路径必须通过 `M1-E2E-B` 回归剖面，统一覆盖 create、observe、lifecycle 和 ops adapter 链路。
- VM、普通容器、GPU 容器的业务创建/查询入口必须通过 `WorkloadInstanceService` 或其上层 gRPC/HTTP 服务，不得绕过实例服务 API 直接调用 provider adapter。
- VM、普通容器、GPU 容器生命周期操作必须通过 `WorkloadInstanceService`；可视化运维操作必须通过 `WorkloadInstanceOps`，业务服务不得直接调用 Kubernetes logs/events/metrics/exec 或 KubeVirt console/VNC API。
- VM 远程控制台必须建模为 `WorkloadInstanceOps` 会话；VNC、serial console、SPICE/RDP、OpenStack/VMware/公有云 console URL 都只能由 adapter 映射，业务层只接收 session metadata、connect URL、协议和过期时间。
- Demo/mock 页面只允许用于演示信息架构和前端体验；不得反向改变 M1/M2/M3 产品计划、端口/适配器边界或后端完成度判断。VM、普通容器、GPU 容器真实行为必须继续通过 `WorkloadInstanceService` 与 `WorkloadInstanceOps`。
- 任何违反该原则的实现必须停下来先补架构设计或 adapter 边界。

---

## Karpathy 四条开发原则

> 来源：Andrej Karpathy 关于 LLM 辅助编程的核心建议，整理自 [forrestchang/andrej-karpathy-skills](https://github.com/forrestchang/andrej-karpathy-skills)

### 原则一：先思考，再编码
**不要假设。不要掩饰困惑。要揭示取舍。**

- 如果需求有歧义，明确说出来并询问，而不是悄悄选一种猜测实现
- 存在多种合理方案时，列出并说明各方案的取舍，由人决策
- 面对复杂问题，先陈述理解再动手
- 遇到不确定的地方，停下来问，而不是带着疑惑继续

### 原则二：用能解决问题的最小代码
**拒绝一切带有猜想的实现。**

- 不实现没被要求的功能，哪怕"感觉以后用得到"
- 不为一次性代码创建抽象层
- 不加"灵活性""可配置性"等未被要求的扩展点
- 200 行能写成 50 行的，重写

### 原则三：只触碰你必须改动的部分
**只清理你自己制造的脏。**

- 不顺手"优化"任务范围之外的代码、注释或格式
- 不重构没坏的东西
- 保持现有代码风格，即使你有不同偏好
- 发现死代码，提出来，不要自作主张删除

### 原则四：定义成功标准，循环迭代直到验证通过
**把任务转化为可验证的目标。**

- 每个任务开始前明确"什么状态算完成"
- 多步骤任务先列出简短计划和验证步骤
- 优先给 Claude 成功标准而非操作指令：不是"修复这个 bug"，而是"写一个能复现 bug 的测试，再修复它"

---
