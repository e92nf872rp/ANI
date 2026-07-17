# KuberCloud ANI · 技术栈设计

> 版本 V1.1 | 广州常青云科技有限公司 | 内部产品规划文件
> 最后更新：2026-05-23（对齐 Core/Services 分层与真实底座验证口径）

> 本文描述技术选型基线，不作为当前开发阶段事实来源。当前阶段以 `ANI-DOCS-INDEX.md`、`ANI-06-开发计划.md` Section 零和 `repo/CURRENT-SPRINT.md` 为准；系统架构图以 `ANI-05-系统架构设计.md` 为准。

---

## 一、技术选型原则

1. **成熟开源优先**：能用社区已验证的开源组件解决的问题，不自研。自研只发生在竞争壁垒所在的业务逻辑层。
2. **与公司现有产品栈对齐**：公司已有 K8s 容器平台能力，ANI 在此基础上向上叠加 AI 层，不另起炉灶。
3. **信创可替换性**：底层组件选型时优先考虑是否有国产/信创替代路径（如 NVIDIA GPU → 昇腾 NPU 的抽象层设计）。
4. **语言统一，降低协作成本**：平台层统一用 Go，减少跨语言 RPC 开销和团队认知分裂。
5. **开源组件准入闭环**：默认组件必须具备健康的 GitHub 社区热度、稳定 release、丰富源码和文档、可观测运维能力、离线部署能力和替换路径；不得为了追新把维护不确定的新项目放入 P0 主链路。

---

## 二、整体技术栈全景

```
┌─────────────────────────────────────────────────────────────────┐
│  ANI Services / 应用层（Python + TypeScript + Go）                  │
│  模型服务 / 推理服务 / RAG / 文档解析 / 前端 Web UI                 │
├─────────────────────────────────────────────────────────────────┤
│  ANI Core 平台层（Go）                                             │
│  实例 / 网络 / 存储 / K8s集群 / 加密 / Secret / Auth/RBAC / 审计    │
├─────────────────────────────────────────────────────────────────┤
│  AI 推理服务（Python）                                              │
│  vLLM  ·  Triton Inference Server  ·  Whisper（语音）              │
├─────────────────────────────────────────────────────────────────┤
│  调度与编排层（Go — Kubernetes 生态）                               │
│  Kubernetes 目标验证基线  ·  Volcano  ·  HAMi  ·  GPU Operator     │
├─────────────────────────────────────────────────────────────────┤
│  网络层（Go — KubeOVN）                                            │
│  KubeOVN 1.13+  ·  OVN/OVS  ·  VPC 隔离  ·  网络策略              │
├─────────────────────────────────────────────────────────────────┤
│  存储层（复用公司现有产品）                                          │
│  Rook-Ceph  ·  MinIO（模型/数据集对象存储）  ·  Milvus（向量库）    │
├─────────────────────────────────────────────────────────────────┤
│  算力硬件层                                                        │
│  NVIDIA（A/H 系列）  ·  昇腾 NPU  ·  海光 DCU  ·  x86 / ARM        │
└─────────────────────────────────────────────────────────────────┘
```

---

## 三、各层技术选型详解

### 3.1 容器编排层 — Kubernetes 目标验证基线

**选型：Kubernetes 1.36 作为 Sprint 5 真实底座验证目标基线。**

K8s 目标版本的关键关注点：
- **Sidecar Container GA**：推理服务的辅助容器（日志、安全代理）生命周期管理更可靠
- **In-place Pod Resource Resizing GA**：无需重启 Pod 即可调整推理服务的 CPU/内存配额，减少服务中断
- **Dynamic Resource Allocation（DRA）Beta**：更灵活的 GPU 资源分配模型，支持细粒度 GPU 切片

**容器运行时：containerd 2.x**（废弃 dockershim，主流选择，资源占用更低）

**不选 OpenShift**：对 K8s 做了大量定制封装，信创适配难度更高，且锁定 Red Hat 生态。

### 3.2 网络层 — KubeOVN

**选型：KubeOVN 1.13+**

KubeOVN 是基于 OVN（Open Virtual Network）构建的 K8s 网络方案，国内团队（灵雀云）主导开发，控制面用 **Go** 编写，社区活跃，信创环境适配好。

**为什么选 KubeOVN 而不是 Calico/Cilium：**

| 对比维度 | KubeOVN | Calico | Cilium |
|---|---|---|---|
| VPC 租户隔离 | 原生支持 | 需要额外配置 | 需要额外配置 |
| 国内社区支持 | 强 | 弱 | 弱 |
| 信创硬件适配 | 有案例 | 一般 | 较弱 |
| 多租户网络策略 | 精细 | 一般 | 强（但复杂） |
| 学习成本 | 中 | 低 | 高 |

**核心使用场景：**
- 为不同企业客户/部门创建独立 VPC，实现网络层物理隔离
- AI Agent 沙箱执行环境的网络出口精细管控
- 多租户推理服务之间的流量隔离

### 3.3 GPU 调度层

**组件组合：**

| 组件 | 作用 | 语言 |
|---|---|---|
| **NVIDIA GPU Operator** | GPU 驱动、容器工具包的 K8s 自动化管理，DaemonSet 方式下发 | Go |
| **HAMi**（Heterogeneous AI Computing Virtualization Middleware）| GPU 共享与虚拟化切分（类 vGPU），支持 NVIDIA/昇腾/海光 | Go |
| **Volcano** | 批量 AI 训练任务调度（Gang Scheduling、抢占、队列管理） | Go |
| **Koordinator** | 在/离线任务混合调度，训练与推理共存时的资源协调 | Go |

**HAMi 选型理由：**
- 支持异构算力（NVIDIA + 华为昇腾 + 海光 DCU），满足信创要求
- 可将一张 A100 切分为多个虚拟 GPU 给不同租户，提升 GPU 利用率
- 纯 K8s 原生实现，不依赖底层驱动私有 API

### 3.4 推理服务层

**选型：vLLM**

```
客户端请求 → ANI 推理网关（Go）→ vLLM 推理集群（Python）→ 返回结果
```

- vLLM 是当前 LLM 推理吞吐量最高的开源引擎，支持 PagedAttention、连续批处理
- 原生支持 OpenAI 兼容 API（`/v1/chat/completions`），客户现有系统无缝切换
- 支持 Qwen2.5、DeepSeek-V3/R1、GLM-4 等国内主流模型
- **推理网关（自研，Go）**：负责鉴权、限流、模型路由、调用审计，轻薄层，不碰推理逻辑

**语音转写：Faster-Whisper**（Whisper 的高性能推理版本，Python，CPU/GPU 都能跑）

### 3.5 存储层

| 用途 | 选型 | 说明 |
|---|---|---|
| 模型文件存储 | **MinIO** | S3 兼容，支持断网离线部署，模型仓库底座 |
| 知识库文档存储 | **Rook-Ceph**（复用公司现有分布式存储产品） | 结构化文档存储 |
| 向量数据库（RAG） | **Milvus** | 国内团队开发，K8s 原生部署，支持亿级向量检索 |
| 关系型数据库 | **PostgreSQL 17**（K8s 上用 CloudNativePG Operator 管理） | 平台元数据、用户权限、审计日志 |

**MinIO 选型说明：** 支持完全离线部署，所有模型文件存储在客户内网，不依赖任何外部服务，满足数据主权要求。

**强制架构约束：默认选型不是不可替换边界。**

除 Kubernetes API 外，本节列出的开源组件均为默认适配器实现，不得直接成为业务服务层、外部 API 或内部服务契约的硬编码依赖：

- MinIO 必须通过 `ObjectStore` 能力访问。
- Milvus 必须通过 `VectorStore` 能力访问。
- NATS JetStream 必须通过 `MessageBus` 能力访问。
- Redis 必须通过 `CacheStore` 能力访问。
- Harbor 必须通过 `ImageRegistry` 能力访问。
- PostgreSQL/CloudNativePG 是 `MetadataStore` 默认实现；RLS 是 v1.0.0 的安全基线，但 SQL 实现仍必须与业务 service 层隔离。

详细规则见 `ANI-13-开源组件松耦合适配器架构.md`。

### 3.6 可观测性

| 组件 | 用途 |
|---|---|
| **Prometheus + Grafana** | GPU 利用率、推理 QPS、系统指标监控 |
| **OpenTelemetry Collector** | 统一采集 Trace/Metric/Log |
| **Loki** | 日志聚合（轻量，K8s 原生，比 ELK 资源占用小） |
| **Jaeger** | 分布式追踪，定位推理链路瓶颈 |

### 3.7 身份认证与安全

| 组件 | 用途 |
|---|---|
| **Dex**（或 Keycloak） | OIDC IdP，对接客户现有 AD/LDAP，统一 SSO |
| **cert-manager** | TLS 证书自动化管理 |
| **OPA（Open Policy Agent）** | K8s 准入控制，强制执行安全策略 |
| **Falco** | 运行时安全检测，发现容器内异常行为 |

---

## 四、开发语言策略

### Go — 平台层的主力语言

**覆盖范围：**
- 所有 K8s Operator 和 Controller（模型部署控制器、GPU 配额控制器）
- ANI 推理网关（鉴权、限流、路由、审计）
- 模型管理 API Server
- 知识库服务后端
- Auth/RBAC 服务
- CLI 工具（`ani` 命令行，类似 `kubectl`）

**选 Go 的理由：**
1. **K8s 生态原生语言**：controller-runtime、operator-sdk、client-go 全是 Go，开发 K8s Operator 用 Go 是事实标准
2. **编译为单一二进制**：部署简单，无运行时依赖，适合客户断网环境
3. **并发模型成熟**：goroutine 处理高并发 API 请求代价极低
4. **工程师市场**：相对 Rust，Go 工程师更好招

### Python — AI 应用层的必选语言

**覆盖范围：**
- vLLM 推理集群配置与扩展
- RAG 引擎（LangChain / LlamaIndex）
- 文档解析（Docling / PyMuPDF）
- 语音转写（Faster-Whisper）
- 模型微调脚本（基于 LLaMA-Factory 或 Swift）

**必须用 Python 的原因：**  
AI/ML 生态几乎全是 Python，不存在能替代的选项。Go/Rust 调用 Python 推理库的成本高于直接用 Python 写服务。

### TypeScript — 前端

- **Console**（用户控制台）& **BOSS**（运营运维后台）：React 18 + TanStack Router + TanStack Query + TDesign
- 从 API 契约自动生成 TypeScript SDK，前端禁止手写 API 调用 URL
- 移动端（Phase 3）：React Native + TDesign Mobile，与 Web 共享业务逻辑和类型定义
- 无 BFF 层（直接通过 ANI Gateway 的 REST API），避免引入额外维护成本

### Rust — 谨慎引入，仅用于具体瓶颈

**可能用到的场景（Phase 2/3 才考虑）：**
- 推理请求解析和序列化的热路径（如果 Go 网关的 JSON 解析成为瓶颈）
- 文档解析的高性能 WASM 模块

**当前阶段不引入 Rust 的原因：**
- Rust 工程师稀缺，招聘成本高
- 编译速度慢，迭代效率低于 Go
- Phase 1/2 的瓶颈在于功能完整性，不在性能极限
- K8s 生态无 Rust SDK 成熟支持

**结论：Go 是平台层唯一的主力语言，Rust 等到有明确性能瓶颈数据后再决策。**

---

## 五、关键开源组件版本锁定

**编排与网络**

| 组件 | 版本 | 说明 |
|---|---|---|
| Kubernetes | **1.36 目标基线** | Sprint 5 真实底座验证版本，后续以实际 lab profile 和部署验证记录为准 |
| KubeOVN | **1.13+** | 需与目标 K8s 基线兼容，最终以真实环境验证为准 |
| containerd | **2.1+** | 主流容器运行时 |
| Volcano | **1.10+** | AI 批调度 |
| HAMi | **2.4+** | 异构 GPU 虚拟化 |

**Web Server 与 API 层**

| 组件 | 版本 | 说明 |
|---|---|---|
| Hertz | **0.9+** | 统一 Web Server 框架（CloudWeGo） |
| grpc-gateway | **2.x** | Core 内部 REST ↔ gRPC 协议转译；不得替代 OpenAPI 作为 Core/Services 跨层控制面契约 |
| buf | **1.x** | Protobuf 管理与 lint |
| oapi-codegen | **2.x** | OpenAPI → Go Server/Client 代码生成 |
| NATS JetStream | **2.10+** | 异步任务队列 |
| cobra + viper | latest | CLI 框架（`ani` 命令行工具） |

**AI 推理与存储**

| 组件 | 版本 | 说明 |
|---|---|---|
| vLLM | **0.6+** | LLM 推理引擎 |
| Milvus | **2.5+** | 向量数据库 |
| MinIO | **RELEASE.2025+** | 对象存储（模型/数据集） |
| PostgreSQL | **17** | 关系型数据库（CloudNativePG Operator 管理） |

**前端**

| 组件 | 版本 | 说明 |
|---|---|---|
| React | **18+** | Console & BOSS 前端框架 |
| TanStack Router/Query | **1.x** | 路由与服务端状态管理 |
| TDesign | **1.x** | 企业级 UI 组件库（腾讯开源，中文友好） |
| Vite | **5+** | 前端构建工具 |
| React Native | **0.76+** | 移动端（Phase 3） |

**开发语言**

| 语言 | 版本 | 用途 |
|---|---|---|
| Go | **1.25+** | 平台层（API Gateway、微服务、Operator、CLI、SDK）；当前依赖安全修复要求 Go 1.25 |
| Python | **3.12+** | AI 层（推理、RAG、文档解析、微调） |
| TypeScript | **5.x** | 前端（Console、BOSS、移动端） |

---

## 六、信创适配路径

| 替换场景 | 原方案 | 信创替代 |
|---|---|---|
| GPU 加速卡 | NVIDIA A/H 系列 | 华为昇腾 910B/910C，HAMi 统一抽象层屏蔽差异 |
| 操作系统 | Ubuntu 22.04 | 统信 UOS 20 / 银河麒麟 V10 |
| CPU 架构 | x86_64 | 鲲鹏（ARM64）、飞腾（ARM64）—— Go/Python 均原生支持 ARM64 |
| 数据库 | PostgreSQL | 人大金仓 KingbaseES（PostgreSQL 兼容，替换成本低）|
| 对象存储 | MinIO | MinIO 本身可信创部署；备选：华为 OBS 私有化版 |

**关键约束：** Go 和 Python 都原生支持 ARM64 交叉编译，信创切换不需要修改业务代码，只需调整 CI 构建目标和镜像基础层。
