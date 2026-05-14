# KuberCloud ANI · 开发计划

> 版本 V8.1 | 广州常青云科技有限公司 | 内部产品规划文件
> 最后更新：2026-05-15

---

## 零、状态快照（先读这里）

> 任何新开发者（人类或 AI）打开本文件，先看这一节，30 秒内定位现在的位置。
> 任务细节见 → [`repo/CURRENT-SPRINT.md`](repo/CURRENT-SPRINT.md)（每冲刺更新）

### 项目全局进度

```
总体进度：Phase 1 v1.0.0 开发中（2026-05-15）
交付目标：2026-09-30 ANI Core v1.0.0 + ANI Services P0
```

| 阶段 | 状态 | 完成时间 | 说明 |
|---|---|---|---|
| **M1 基础设施底座** | ✅ 已完成 | 2026-05 | INFRA/GPU/Runtime/Instance A-S 全链路 |
| **M2 Auth/Gateway** | ✅ 已完成 | 2026-05 | OIDC/JWT/RBAC/API Key 全流程 |
| **V8 架构重规划** | ✅ 已完成 | 2026-05-15 | Core/Services 分层、AWS 工程加固 |
| **Sprint 1（当前）** | 🔄 进行中 | 2026-05-15~05-31 | 操作语义底座 + Health + Idempotency |
| Sprint 2 | ⏳ 计划中 | 2026-06-01~06-15 | VM & Container 深度 |
| Sprint 3 | ⏳ 计划中 | 2026-06-16~06-30 | 网络/存储/向量 API |
| Sprint 4 ⭐ | ⏳ 计划中 | 2026-07-01~07-15 | **API 冻结 + SDK**（硬截止）|
| Sprint 5 | ⏳ 计划中 | 2026-07-16~07-31 | 控制器 + 加解密 + Sandbox |
| Sprint 6 | ⏳ 计划中 | 2026-08-01~08-15 | 平台支撑 + Services P0 开始 |
| Sprint 7 | ⏳ 计划中 | 2026-08-16~09-01 | Installer + 知识库 RAG |
| Sprint 8 | ⏳ 计划中 | 2026-09-01~09-15 | Console + BOSS |
| Sprint 9 | ⏳ 计划中 | 2026-09-16~09-25 | RC 加固 |
| Sprint 10 | ⏳ 计划中 | 2026-09-26~09-30 | v1.0.0 发布 |

### 当前冲刺：Sprint 1（2026-05-15 → 2026-05-31）

| 批次 | 优先级 | 状态 |
|---|---|---|
| M1-INSTANCE-T（操作语义）| P0 ⭐ | 🔄 进行中 |
| M1-HEALTH-A（健康检查端点）| P1 | ⏳ 待开始 |
| M1-IDEM-A（幂等性令牌集成）| P1 | ⏳ 待开始 |
| M2.2-AUTH-FINAL（Auth 收尾）| P1 | ⏳ 待开始 |

**→ 今天该做什么，看 [`repo/CURRENT-SPRINT.md`](repo/CURRENT-SPRINT.md)**

### 已完成批次完整记录

> 完整的已完成批次列表和进度记录在 CLAUDE.md（"开发阶段命名强制约定 → 第4条"）。
> 对应代码记录在 `repo/development-records/` 目录下（56个文件）。

主要已完成里程碑（仅列关键节点）：
- M1-INFRA-A/B/C/D/E/F — Kubernetes 基础设施、KubeOVN 网络、GPU 调度基线
- M1-GPU-A — 异构 GPU（NVIDIA/昇腾/海光）发现与调度契约
- M1-RUNTIME-A — WorkloadRuntime port（VM/容器/GPU/Sandbox/Batch Job 抽象）
- M1-INSTANCE-A ~ S — 实例全链路（计划→渲染→准入→审计→dry-run→apply→observe→持久化→服务层）
- M2.1-TASK-A/B/C — 异步任务/outbox/worker mutations
- M2.2-AUTH-A ~ K — Auth 服务完整实现（JWT/RBAC/OIDC/JWKS/API Key）
- V8 架构设计 — Core/Services 分层、API 工程约定（幂等性/控制平面分离等）
- AWS 工程加固 — /healthz /readyz schema、WorkloadReconcileController port、operations DB 表、permissions schema

### Phase 2 延期（v1.0.0 不实现，生意驱动迭代）

| 条目 | 理由 |
|---|---|
| M1-BM-A（裸金属/Metal3）| 需物理机环境，无 P0 依赖 |
| M1-K8S-A（K8s集群/vCluster）| 复杂度高，无 P0 依赖 |
| M1-DPU-A（DPU节点）| 需专用硬件 |
| SDK-JAVA-A（Java SDK）| 1天生成任务，按需做 |
| M1-SVC-EP-A（服务目录/DNS）| PaaS 依赖，Phase 2 |
| M1-NOTIFY-A（事件通知 API）| 非 P0 阻塞 |

---

## 一、核心约束

| 约束 | 说明 |
|---|---|
| **交付截止** | **2026 年 9 月 30 日**，第一个生产可用版本 |
| **首个正式版本** | **v1.0.0**，版本规则见 `ANI-12-版本管理策略.md` |
| **开发模式** | AI 开发为主，人工为辅——接口设计与架构决策由人主导，代码实现最大化借助 Claude Code / Cursor 等工具生成 |
| **技术路线** | 完全从零构建，最大化复用成熟开源组件，ANI 价值在于"编排"与"封装" |
| **开发语言** | Go（平台层）、Python（AI 应用层）、TypeScript（前端） |

**每个模块的 AI 辅助标准流程：**
1. 人工编写 OpenAPI Spec / Protobuf 定义（明确接口契约）
2. AI 生成 Server Stub、Client、单元测试骨架
3. 人工审查逻辑正确性和安全边界
4. AI 补充错误处理、日志、Metrics 等横切代码
5. 人工做集成测试和边界 case 验证

---

## 二、双周冲刺计划（V8.1 重规划，2026-05-15 更新）

> **规划原则：** 每个冲刺 2 周，有明确进入条件、交付清单、完工标准（验收命令）。
> 任何开发者（人类或 AI）打开本节，应能在 5 分钟内知道当前冲刺在做什么、下一步是什么。
> 当前冲刺详细上手指南见 → `repo/CURRENT-SPRINT.md`（每冲刺结束时更新）

---

### Phase 2 延期条目（已从 v1.0.0 范围移出）

以下条目在当前版本中**不实现**，显式推迟到 2026-09-30 之后的 Phase 2 迭代，届时由"生意"驱动优先级：

| 条目 | 理由 |
|---|---|
| **M1-BM-A**（裸金属 / Metal3 接入）| 无 Phase 1 Services P0 依赖；需物理机环境才能测试；独立 2-3 周工程量 |
| **M1-K8S-A**（K8s 集群管理 / vCluster）| 无 Phase 1 Services P0 依赖；复杂度高；Phase 2 按需引入 |
| **M1-DPU-A**（DPU 节点纳管）| 需专用 BlueField 硬件；Phase 2 |
| **SDK-JAVA-A**（Java SDK）| 一天生成任务，Phase 2 按需生成；不阻塞 Services 团队 |
| **M1-SVC-EP-A**（服务目录 / 内部 DNS）| PaaS 依赖；Phase 2 |
| **M1-NOTIFY-A**（事件通知 API）| 非 Services P0 阻塞；Phase 2 |

---

### Sprint 计划总览

```
           ANI Core 开发线                    ANI Services 开发线
           ─────────────────────              ──────────────────────
S1  05-15~05-31  操作语义 + Foundation
S2  06-01~06-15  VM & Container 深度
S3  06-16~06-30  Core API 面（网络/存储/向量）
S4  07-01~07-15  API 冻结 + 三语言 SDK        ← 解锁日：07-15
S5  07-16~07-31  后台控制器 + 加解密 + Sandbox  S_A  模型仓库 + 推理服务
S6  08-01~08-15  平台支撑完整化               S_A  知识库 RAG
S7  08-16~09-01  Installer + 集成测试          S_B  Console 核心页面
S8  09-01~09-15  Console 集成 + BOSS          S_B  BOSS 基础版
S9  09-16~09-25  RC 加固（只修 Bug）
S10 09-26~09-30  v1.0.0 发布
─────────────────────────────────────────────────────────────────────
09-30  ✅  ANI Core v1.0.0  +  ANI Services P0
```

---

### Sprint 1：2026-05-15 → 2026-05-31（当前冲刺）

**主题：操作语义底座 + Foundation**

**进入条件：** `make build && make test` 通过（已验证 ✅）

| 批次 | 内容 | 难度 | 预估 |
|---|---|---|---|
| **M1-INSTANCE-T** ⭐ | 横切操作语义：precheck/disabled-reason/operation_id/timeline/before-after spec diff | 高 | 5天 |
| **M1-HEALTH-A** | 所有服务加 /healthz（liveness）和 /readyz（readiness） | 低 | 1天 |
| **M1-IDEM-A** | 幂等性令牌 wire-up：CREATE/lifecycle 接口写入 DB，返回已有结果 | 中 | 3天 |
| **M2.2-AUTH-FINAL** | OIDC Dex 接入生产 + API Key scope 验证 + 集成测试补齐 | 中 | 3天 |

**完工标准：**
```bash
make test                        # 所有测试通过
curl http://localhost:8080/healthz   # → {"status":"ok"}
curl http://localhost:8080/readyz    # → {"status":"ok","checks":{...}}
# POST /instances 返回 operation_id
# 同 idempotency_key 二次 POST → 返回相同结果，不创建第二个实例
```

**本冲刺交付物：** `WorkloadOperation` 记录写入 DB、所有服务 health 端点、idempotency_key DB 去重

**解锁：** Sprint 2 的 VM/Container 深度 + Sprint 4 的 operation timeline Console 展示

---

### Sprint 2：2026-06-01 → 2026-06-15

**主题：VM & Container / GPU 容器生产深度**

**进入条件：** Sprint 1 完工标准通过；`workload_instance_operations` 表已建

| 批次 | 内容 | 难度 | 预估 |
|---|---|---|---|
| **M1-INSTANCE-U** | VM 生产级操作：终止保护/VNC console/快照/磁盘绑定/SSH 连接信息 | 高 | 5天 |
| **M1-INSTANCE-V** | Container 部署深度（副本/滚动更新/回滚/历史）；GPU 调度原因/利用率 | 高 | 5天 |

**完工标准：**
```bash
make test
# VM 实例可获取 VNC session URL
# Container 实例可触发 rollback 到上一版本
# GPU 容器状态包含 gpu_scheduling_reason 和 gpu_utilization
```

**解锁：** Console VM 详情页、Container 部署页面接真实 API

---

### Sprint 3：2026-06-16 → 2026-06-30

**主题：Core API 面扩充（网络 + 存储 + 向量 + Workload Identity）**

**进入条件：** Sprint 2 完工标准通过

| 批次 | 内容 | 难度 | 预估 |
|---|---|---|---|
| **M1-NETWORK-A** | VPC/子网/安全组/LB CRUD：真实 KubeOVN 子资源管理 | 中 | 4天 |
| **M1-STORAGE-A** | 块存储(volumes) + 文件存储(filesystems) + 对象存储(objects) CRUD | 中 | 4天 |
| **M1-VSTORE-A** | vector-stores 创建/删除/检索 API（Milvus adapter 已有，加 Gateway 路由）| 低 | 2天 |
| **M1-WKID-A** | Workload Identity P0：实例创建时生成 lifecycle-bound API key + 实例删除时 revoke | 中 | 2天 |

**完工标准：**
```bash
make test
# POST /api/v1/networks/vpcs → 201 Created
# POST /api/v1/volumes → 201 Created
# POST /api/v1/vector-stores → 201；POST /{id}/search → 200
# 新实例的 ANI_WORKLOAD_TOKEN 环境变量已注入 + 实例删除后自动 revoked
```

**解锁：** Sprint 4 的 API Spec 可以基于真实实现补全；Services 团队可测试 object/vector 接口

---

### Sprint 4：2026-07-01 → 2026-07-15 ⭐ API 冻结硬截止

**主题：API 契约冻结 + 三语言 SDK + Mock Server**

**进入条件：** Sprint 3 全部完工；v1.yaml 需扩充为全量 Core 路径

| 批次 | 内容 | 难度 | 预估 |
|---|---|---|---|
| **SPEC-CORE-A** | 将 Sprint 1-3 所有新路径补入 api/openapi/v1.yaml，含正确 schema + 分页 + idempotency | 中 | 3天 |
| **SPEC-SPLIT-A** | /models /inference-services /knowledge-bases 移至 api/openapi/services/v1.yaml | 低 | 1天 |
| **SDK-GO-A** | oapi-codegen 生成 Go SDK（sdks/ani-go/） | 低 | 1天 |
| **SDK-PY-A** | openapi-generator 生成 Python SDK（sdks/ani-python/） | 低 | 1天 |
| **SDK-TS-A** | openapi-typescript 生成 TypeScript Client（sdks/ani-typescript/） | 低 | 1天 |
| **MOCK-A** | Prism Mock Server 基于 v1.yaml 启动，覆盖所有 Core 路径 | 低 | 1天 |
| **DOC-API-A** | Swagger UI / Redoc 自动生成并部署 | 低 | 1天 |

**完工标准（2026-07-15 前必须达成）：**
```bash
# v1.yaml 全量路径覆盖，无 TODO stub
make gen-core-sdk        # 三个 SDK 目录生成完毕
prism mock api/openapi/v1.yaml --port 4010   # 所有路径返回 200 mock
# Services 团队用 ani-python SDK 调用 Mock Server 能获得正确响应类型
```

**本冲刺结束即宣告 Core API 冻结。之后 v1.yaml 只增不删（API 兼容性承诺）。**

**解锁：** ANI Services 团队正式解锁，开始用 Python SDK + Mock Server 开发

---

### Sprint 5：2026-07-16 → 2026-07-31

**主题：后台控制器 + 加解密 + Sandbox**

**进入条件：** API 冻结完成；Services 团队已接入 Mock Server 自行推进

| 批次 | 内容 | 难度 | 预估 |
|---|---|---|---|
| **M1-RECONCILE-A** | WorkloadReconcileController 实现（30s/5s 双速后台 goroutine）| 高 | 5天 |
| **M1-ENCRYPT-A** | SM4 密钥管理 + seal/unseal-token（Init Container 解密模型文件）| 中 | 3天 |
| **M1-SECRETS-A** | Secret CRUD + 实例绑定（创建实例时注入 env var）| 中 | 3天 |
| **M1-SANDBOX-A** | Kata Containers QEMU RuntimeClass 接入 + Sandbox 实例 create/exec | 高 | 5天 |

**完工标准：**
```bash
make test
# Reconcile Controller 可扫描 workload_instances 并更新状态，不依赖 API 调用
# POST /api/v1/encryption/seal → 返回 unseal-token
# POST /api/v1/instances {kind: "sandbox"} → 实例起来，exec 可运行命令
```

**解锁：** 模型文件加密工作流（Services 模型仓库依赖）；Agent Sandbox 运行时就绪

---

### Sprint 6：2026-08-01 → 2026-08-15

**主题：Core 平台支撑服务 + Services P0 核心功能同步推进**

**Core 任务（本小组）：**

| 批次 | 内容 | 难度 | 预估 |
|---|---|---|---|
| **M1-OBS-A** | PromQL 代理查询 + 基础告警规则 CRUD | 中 | 3天 |
| **M1-METER-A** | 实例用量计量 + Token 用量上报接口 | 中 | 2天 |
| **M1-REGISTRY-A** | Harbor API 封装（镜像推拉权限 + 安全扫描） | 中 | 2天 |
| **Core E2E** | 全链路集成测试回归（Sprint 1-5 所有功能） | 中 | 3天 |

**Services 任务（另一小组，从 07-15 起并行）：**

| 批次 | 内容 | 说明 |
|---|---|---|
| **SVC-MODEL-A** | 模型仓库：上传/版本/元数据/国密加解密/HuggingFace 导入 | 依赖 Core objects + encryption API |
| **SVC-INFER-A** | 推理服务：部署端点/状态/日志/OpenAI 兼容 `/v1/chat/completions` | 依赖 Core GPU container + instances API |

**完工标准：**
```bash
# Core
make test   # 全通，包含 E2E
# Services（另一小组验证）
# 模型文件上传 + 加密 → 成功
# 推理端点部署 + 调用 /v1/chat/completions → 得到 LLM 回答
```

---

### Sprint 7：2026-08-16 → 2026-09-01

**主题：ani-installer + 知识库 RAG + Console Alpha**

**Core 任务：**

| 批次 | 内容 | 难度 | 预估 |
|---|---|---|---|
| **Installer-A** | ani-installer：裸机/VM/已有K8s 三种模式 + GPU 驱动安装 + 内部 CA | 高 | 5天 |
| **Installer-B** | 离线安装包：镜像预拉取 + Helm Chart + 自动化脚本 | 中 | 3天 |
| **信创基线** | ARM64 构建验证 + 国产 GPU adapter 测试 | 中 | 2天 |

**Services 任务：**

| 批次 | 内容 | 说明 |
|---|---|---|
| **SVC-KB-A** | 知识库 RAG：文档上传→解析→向量化→混合检索→问答→来源引用 | 依赖 Core vector-stores + objects API |
| **SVC-CONSOLE-Alpha** | 实例列表/详情/操作页面接真实 Core API（替换 mock） | 大量前端工作 |

**完工标准：**
```bash
# 在一台全新机器上运行 ani-installer
# 15 分钟内完成安装，make test 在新环境跑通
# 上传 5 个 PDF → 建立知识库 → 问答得到含来源引用的回答
```

---

### Sprint 8：2026-09-01 → 2026-09-15

**主题：Console 全量接真实 API + BOSS 基础版**

**Services 任务：**

| 批次 | 内容 |
|---|---|
| **SVC-CONSOLE-Full** | 模型管理页 + 推理部署页 + 知识库管理页全部接真实 API |
| **BOSS-A** | BOSS 租户管理基础版（创建/配额/资源大盘）|
| **CLI-A** | ani CLI 命令覆盖 Core 主要资源 |

**Core 任务（收尾）：**

| 批次 | 内容 |
|---|---|
| **Core 安全审查** | Trivy 扫描 + API 权限边界验证 + RLS 多租户测试 |
| **性能基线** | API P99 < 200ms；推理首 Token < 2s（7B 模型 A100）|

**完工标准：**
```bash
# Console 所有主要页面无 mock 数据
# BOSS 管理员可创建租户 + 设置配额
# 安全扫描无高危漏洞
```

---

### Sprint 9：2026-09-16 → 2026-09-25

**主题：v1.0.0-rc 加固（只允许修 Bug，不加新功能）**

| 任务 | 说明 |
|---|---|
| v1.0.0-rc.1 发布 | 打 tag，制作 rc 构建 |
| 全量 E2E 回归 | Core + Services + Installer 三线联合验收 |
| Bug 修复 | 只修 P0（阻断交付）和 P1（严重功能缺陷）|
| Release Notes | 中英文版本说明 + 已知问题列表 |

**完工标准：**
```bash
git tag v1.0.0-rc.1
make test   # 全通
# 完整交付验收单检查通过（见下方）
```

---

### Sprint 10：2026-09-26 → 2026-09-30

**主题：v1.0.0 正式发布**

```bash
git tag v1.0.0
# 离线安装包发布
# 文档站发布
# 第一个标杆客户环境部署验收
```

---

### v1.0.0 交付验收单（9 月 30 日前必须全部打勾）

```
ANI Core：
  [ ] make build && make test 通过（含 E2E）
  [ ] /healthz + /readyz 所有服务可用
  [ ] VM / 容器 / GPU 容器 全生命周期（含 operation_id + 时间线）
  [ ] Sandbox 实例可 exec 命令
  [ ] VPC / 子网 / 安全组 CRUD
  [ ] 块存储 / 文件存储 / 对象存储 CRUD
  [ ] 向量存储 API
  [ ] 国密 SM4 加解密（seal/unseal）
  [ ] Secrets API + 实例绑定注入
  [ ] Workload Identity（lifecycle-bound token）
  [ ] WorkloadReconcileController 后台运行
  [ ] 镜像仓库 API（Harbor 封装）
  [ ] 用量计量 API
  [ ] 可观测性 API（PromQL 代理 + 告警）
  [ ] Core API 契约 v1.yaml 完整 + 兼容承诺生效
  [ ] Go SDK + Python SDK + TypeScript Client 发布
  [ ] ani-installer（3 种部署模式）+ 离线包
  [ ] 信创基线（ARM64 构建通过）

ANI Services P0：
  [ ] 模型仓库（上传/版本/加解密/HuggingFace 导入）
  [ ] 推理服务（端点部署 / OpenAI 兼容 API / 日志指标）
  [ ] 知识库（文档上传/解析/RAG 问答/来源引用）
  [ ] Console 核心页面（实例/模型/推理/知识库）接真实 API
  [ ] BOSS 基础版（租户管理/配额）

产品验证：
  [ ] 全新机器 30 分钟内完成离线安装
  [ ] Qwen2.5-7B 推理响应 < 2s（首 Token，A100）
  [ ] 知识库问答来源引用准确
  [ ] 多租户隔离测试通过（租户 A 无法读取租户 B 数据）
```

---

**版本里程碑：**
- 2026-05 到 2026-08：`v0.x.y` 或 `v1.0.0-alpha/beta.N` 标记内部构建
- 2026-09 Sprint 9：进入 `v1.0.0-rc.N`，只允许修复阻断交付的 Bug
- 2026-09-30：发布 `v1.0.0`

**关键不可推迟节点：**
- `2026-07-15`：Core API 契约冻结 ← Services 团队解锁，不可推迟
- `2026-09-15`：Core + Services P0 功能完成，进入集成联调
- `2026-09-30`：v1.0.0 交付

---

## 三、已完成模块归档（精简）

> **已完成的批次不在此处重复维护**，完整记录在两处：
> - **代码批次进度**：`CLAUDE.md` → "开发阶段命名强制约定" → 第4条（完整批次列表）
> - **详细技术记录**：`repo/development-records/*.md`（56个文件，每个批次一份）
>
> 本节只保留"为什么做"的设计意图，不重复已完成条目清单。

### 模块 1：基础设施底座（M1）✅

**目标：** 在 K8s 1.36 上搭建完整 AI 平台底座，让 GPU 资源可被统一调度。

**已完成（全部）：** M1-INFRA-A/B/C/D/E/F + M1-GPU-A + M1-RUNTIME-A + M1-INSTANCE-A~S + M1-E2E-A/B + ARCH-ADAPTER-A/B/C/GUARD-A

**当前实现对齐（2026-05-11）：**
- `M1-INFRA-A`：已新增基础设施代码化基线，覆盖 `ani-system` 命名空间、平台依赖配置占位、默认拒绝 NetworkPolicy、控制面 ServiceAccount、Helm umbrella chart values contract。
- `M1-INFRA-B`：已新增组件安装 profile 和基础设施依赖合同，覆盖 PostgreSQL/CloudNativePG、NATS JetStream、Redis、MinIO、Milvus、Harbor。
- `M1-INFRA-C`：已新增 KubeOVN 租户 VPC/Subnet 模板、租户默认拒绝 NetworkPolicy、Gateway-only ingress、AI Agent 沙箱 egress 限制模板。
- `ARCH-ADAPTER-A / M1-ARCH-A`：已完成开源组件松耦合适配器架构设计。
- `ARCH-ADAPTER-B`：已新增 `pkg/ports`、`pkg/adapters` 与 `bootstrap.Capabilities` 骨架，后续新代码必须优先使用 capability ports。
- `ARCH-ADAPTER-GUARD-A`：已新增组件 SDK 直接导入扫描与 coupling-level allowlist。
- `ARCH-ADAPTER-C`：已完成第一批低风险迁移，auth JWT blocklist 使用 `CacheStore`，task outbox publish 使用 `MessageBus`。
- `M1-INFRA-D`：已新增 cluster preflight validation profile，用于 attach-k8s/offline 环境预检 KubeOVN CRD、Secret、StorageClass 和 NetworkPolicy API。
- `ARCH-ADAPTER-C-2`：已将 pgx/metadata 直接依赖按 `bounded_direct` 分类，保留 PostgreSQL/RLS 核心路径稳定性。
- `M2.2-AUTH-E`：已为 JWT blocklist 增加 PostgreSQL 持久化兜底和 CacheStore 快路径。
- `M2.2-AUTH-F`：已新增 refresh token 持久化表、refresh token 校验、RS256 AccessToken 签发能力；Dex/OIDC callback 后续只需创建 refresh token 记录。
- `M2.2-AUTH-G`：已新增 OIDC 登录 begin/callback RPC 边界、state 缓存和授权 URL 构造；code exchange / ID token verifier 待下一阶段接入 Dex。
- `M2.2-AUTH-H`：已实现 OIDC code exchange、静态 RS256 ID token verifier、用户 upsert、角色映射、refresh token 创建和 TokenPair 签发。
- `M2.2-AUTH-I`：已实现 OIDC JWKS discovery 与 `kid` 公钥选择；静态 RS256 公钥继续作为离线环境 fallback。
- `M2.2-AUTH-J`：已实现 OIDC group 到 ANI role 的显式映射；默认只授予 `user`，高权限角色必须通过 `AUTH_OIDC_GROUP_ROLE_MAP_JSON` 白名单配置。
- `M1-INFRA-E`：已新增 GPU scheduling baseline，覆盖 GPU 节点标签契约、Volcano Queue、HAMi 设备插件资源契约、DCGM 观测指标契约与 GPU 预检模板。
- `M2.2-AUTH-K`：已新增 auth-service 集成测试剖面，覆盖 OIDC Begin/Complete、refresh token 续签和 ValidateToken 验证链路。
- `M1-INFRA-F`：已新增 GPU scheduling preflight/e2e hardening，覆盖可执行预检 Job、最小 RBAC、严格运行时开关、GPU smoke workload 模板和离线契约校验。
- `M1-GPU-A`：已新增异构 GPU 发现与调度契约，覆盖 NVIDIA/Huawei/Hygon、多型号、内核/驱动/运行时兼容、资源名映射和 `GPUInventory` port。
- `M1-RUNTIME-A`：已补齐平台实例运行时抽象，覆盖传统 VM、普通容器、GPU 容器、推理实例、Notebook、Agent Sandbox 和 Batch Job，并新增 `WorkloadRuntime` port。
- `M1-INSTANCE-A`：已补齐核心实例对象、全生命周期动作/状态、网络平面与存储附件契约；VM 与 Pod 可按业务需要共享 `tenant_vpc`，平台服务/存储/管理流量分别走 `foundation_mesh`、`storage`、`management`。
- `M1-INSTANCE-B`：已新增 `PlanningRuntime` 实例规划器，覆盖 VM/容器/GPU 容器创建前网络、存储、GPUInventory、生命周期状态机校验与计划态记录。
- `M1-INSTANCE-C`：已新增 Kubernetes/KubeVirt provider dry-run renderer，规划后输出 VM `VirtualMachine`、容器/GPU 容器 `Deployment`、批任务 `Job` manifest，不直接创建集群资源。
- `M1-INSTANCE-D`：已新增本地 admission guardrail，审查 dry-run manifest 类型、租户/实例标签、网络平面注解，并拒绝 hostNetwork/privileged 风险。
- `M1-INSTANCE-E`：已新增实例计划/渲染/准入结果持久化与审计，覆盖 `WorkloadPlanAuditStore`、`MetadataPlanAuditStore` 和 `instance_plan_audits` 租户 RLS 表。
- `M1-INSTANCE-F`：已新增 provider dry-run executor 边界，覆盖 `WorkloadProviderDryRun`、本地 provider/kind/apiVersion 校验和未来 Kubernetes `dryRun=All` 执行契约。
- `M1-INSTANCE-G`：已新增 provider apply/create 执行门控，覆盖 `WorkloadProviderApply`、默认关闭执行开关、审计 ID、用户/租户、权限证明、admission 和 dry-run 证据校验。
- `M1-INSTANCE-H`：已新增实例状态回写/生命周期 reconcile 契约，覆盖 `WorkloadStatusReconciler`、provider observation 标准化、phase 映射和 apply/audit/resource refs 关联校验。
- `M1-INSTANCE-I`：已新增 provider status reader 与实例创建编排 API，覆盖 `WorkloadProviderStatusReader`、`WorkloadInstanceOrchestrator` 和 create 链路端到端编排。
- `M1-INSTANCE-J`：已新增实例持久化/查询 API 契约，覆盖 `WorkloadInstanceStore`、`MetadataInstanceStore`、`workload_instances` 租户 RLS 表和 orchestrator 状态写入。
- `M1-INSTANCE-K`：已新增 Kubernetes/KubeVirt provider adapter 边界，覆盖 `KubernetesProviderAdapter`、`KubernetesProviderClient`、server-side dry-run、受控 apply 和 provider status observation。
- `M1-INSTANCE-L`：已新增实例服务 API 层，覆盖 `WorkloadInstanceService`、`LocalInstanceService`，对 VM、普通容器和 GPU 容器提供 Create/Get/List 入口。
- `M1-INSTANCE-M`：已新增实例生命周期与可视化运维 API，覆盖 Start/Stop/Restart/Resize/Delete 和 logs/events/metrics/terminal/exec ops 边界。
- `M1-E2E-A`：已新增 M1 端到端集成剖面，覆盖 VM、普通容器、GPU 容器的 create/lifecycle/query/ops 合同链路。
- `M1-INSTANCE-N`：已新增 Kubernetes provider 执行剖面，覆盖 `KubernetesProviderClient` server-side dry-run、受控 apply、observe 与 orchestrator 集成合同。
- `M1-INSTANCE-O`：已新增 adapter-owned `KubernetesRESTClient`，通过标准库 HTTP 实现 dryRun=All、server-side apply 和 Deployment/Job/KubeVirt VM observe。
- `M1-INSTANCE-P`：已新增 bootstrap/config provider wiring，支持 `WORKLOAD_PROVIDER=kubernetes_rest` 接入 `KubernetesRESTClient`，默认 local 且 apply 关闭。
- `M1-INSTANCE-Q`：已新增 Kubernetes lifecycle execution，覆盖 `WorkloadInstanceLifecycleExecutor`、`KubernetesLifecycleExecutor` 和 start/stop/restart/resize/delete provider 执行边界。
- `M1-INSTANCE-R`：已新增 Kubernetes visual ops execution，覆盖 `KubernetesInstanceOps` 和 logs/events/metrics/terminal/exec provider 执行边界。
- `M1-E2E-B`：已新增 M1 real provider integration regression profile，统一覆盖 Kubernetes REST provider create/observe/lifecycle/ops 链路。
- `DEMO-INSTANCE-CONSOLE-A`：已新增阶段性实例 Demo API 与 Console 页面，提前展示 VM、普通容器、GPU 容器 create/lifecycle/ops 体验；该变化属于展示层，不改变 M1 核心契约。
- `M1-INSTANCE-S`：已新增 VM console/VNC/serial remote ops session 边界，支持 KubeVirt、OpenStack、VMware 与主流公有云 console 协议映射；业务层只接收 session metadata，不直接调用 provider console API。
- `DEMO-INSTANCE-WORKSPACE-UI-A`：已将实例 Demo 重构为生产控制台候选设计，覆盖 VM、普通容器、GPU 容器的创建、生命周期、运维与独立控制台页面。
- `2026-05-12-demo-handoff`：已记录 Demo 暂停点、明日启动步骤、mock 展示边界和验证命令；mock 只用于演示信息架构和前端体验，不代表模型、知识库、用量、审计等生产 API 已完成。
- 阶段验证：M1/M2 前序代码阶段 `make gen-proto` 已通过；当前工作区 `make validate-infra`、`make test`、`make build` 已通过。
- 进度记录文件：`repo/development-records/m1-infra-a-baseline.md`。
- 进度记录文件：`repo/development-records/m1-infra-b-component-profiles.md`。
- 进度记录文件：`repo/development-records/m1-infra-c-network-isolation.md`。
- 进度记录文件：`repo/development-records/m1-arch-a-component-adapter-design.md`。
- 进度记录文件：`repo/development-records/arch-adapter-b-ports-adapters-skeleton.md`。
- 进度记录文件：`repo/development-records/arch-adapter-c-first-migration.md`。
- 进度记录文件：`repo/development-records/m1-infra-d-cluster-preflight.md`。
- 进度记录文件：`repo/development-records/arch-adapter-c-2-metadata-boundaries.md`。
- 进度记录文件：`repo/development-records/m2-2-auth-e-durable-token-blocklist.md`。
 - 进度记录文件：`repo/development-records/m2-2-auth-f-refresh-token-foundation.md`。
 - 进度记录文件：`repo/development-records/m2-2-auth-g-oidc-boundary.md`。
 - 进度记录文件：`repo/development-records/m2-2-auth-i-oidc-jwks.md`。
 - 进度记录文件：`repo/development-records/m2-2-auth-j-oidc-group-mapping.md`。
 - 进度记录文件：`repo/development-records/m2-2-auth-h-oidc-code-exchange.md`。
- 进度记录文件：`repo/development-records/m1-infra-e-gpu-scheduling-baseline.md`。
- 进度记录文件：`repo/development-records/m2-2-auth-k-auth-integration-profile.md`。
- 进度记录文件：`repo/development-records/m1-infra-f-gpu-preflight-e2e.md`。
- 进度记录文件：`repo/development-records/m1-gpu-a-heterogeneous-gpu-contract.md`。
- 进度记录文件：`repo/development-records/m1-runtime-a-workload-runtime.md`。
- 进度记录文件：`repo/development-records/m1-instance-a-instance-fabric.md`。
- 进度记录文件：`repo/development-records/m1-instance-b-planning-runtime.md`。
- 进度记录文件：`repo/development-records/m1-instance-c-provider-renderer.md`。
- 进度记录文件：`repo/development-records/m1-instance-d-admission-guardrail.md`。
- 进度记录文件：`repo/development-records/m1-instance-e-plan-audit.md`。
- 进度记录文件：`repo/development-records/m1-instance-f-provider-dry-run.md`。
- 进度记录文件：`repo/development-records/m1-instance-g-provider-apply-gate.md`。
- 进度记录文件：`repo/development-records/m1-instance-h-status-reconcile.md`。
- 进度记录文件：`repo/development-records/m1-instance-i-orchestrator.md`。
- 进度记录文件：`repo/development-records/m1-instance-j-instance-store.md`。
- 进度记录文件：`repo/development-records/m1-instance-k-provider-adapter.md`。
- 进度记录文件：`repo/development-records/m1-instance-l-instance-service.md`。
- 进度记录文件：`repo/development-records/m1-instance-m-lifecycle-ops.md`。
- 进度记录文件：`repo/development-records/m1-e2e-a-instance-profile.md`。
- 进度记录文件：`repo/development-records/m1-instance-n-kubernetes-provider-execution.md`。
- 进度记录文件：`repo/development-records/m1-instance-o-kubernetes-rest-client.md`。
- 进度记录文件：`repo/development-records/m1-instance-p-kubernetes-bootstrap-wiring.md`。
- 进度记录文件：`repo/development-records/m1-instance-q-kubernetes-lifecycle-execution.md`。
- 进度记录文件：`repo/development-records/m1-instance-r-kubernetes-ops-execution.md`。
- 进度记录文件：`repo/development-records/m1-e2e-b-real-provider-profile.md`。
- 后续开始衔接模块 3 模型管理平台，或补真实集群 smoke profile。
- 下一阶段规划已完成：`repo/development-records/2026-05-11-next-development-plan.md`。

#### 1.1 Kubernetes 集群

- [ ] **K8s 1.36 集群部署规范**
  - 节点规划：Master ×3（HA）、GPU 工作节点、存储节点
  - 安装方式：RKE2 1.36（比 kubeadm 更适合离线环境和安全加固）
  - 容器运行时：containerd 2.1+
  - 离线安装包制作：镜像预拉取 + 离线 Helm Chart 打包
  - **开源组件：** RKE2 1.36、containerd 2.1

- [ ] **KubeOVN 1.13+ 网络部署**
  - 多租户 VPC 规划（每客户独立 VPC，物理隔离）
  - NetworkPolicy 模板（租户隔离 + AI Agent 沙箱出口限制）
  - BGP 配置（与客户现有网络对接）
  - **开源组件：** KubeOVN 1.13+、OVN/OVS

#### 1.2 GPU 算力纳管

- [ ] **异构 GPU 发现与调度契约**
  - 支持同厂商多型号 GPU 分池、跨厂商 NVIDIA / 昇腾 / 海光混合集群
  - 识别内核、驱动、device plugin、RuntimeClass、资源名和显存能力差异
  - 通过 `GPUInventory` port 输出 GPUNodeClass、GPUDeviceClass 和调度决策
  - 处置策略：不兼容节点隔离、标签/污点标记、调度决策拒绝
  - **实现：** `M1-GPU-A`

- [ ] **NVIDIA GPU Operator**
  - DaemonSet 自动化下发 GPU 驱动和容器工具包
  - 支持：A10、A30、A100、H100 系列
  - **开源组件：** nvidia-gpu-operator latest

- [ ] **HAMi GPU 虚拟化**（核心差异化能力）
  - GPU 切片：MIG 模式（A100）+ vGPU 模式（其他卡型）
  - 多租户 GPU 配额隔离
  - 异构算力：昇腾 910B/C（信创关键，HAMi 唯一同时支持 NVIDIA+昇腾+海光的开源方案）
  - GPU 利用率实时采集（核心卖点，解决客户 GPU 买了不会用的问题）
  - **开源组件：** HAMi 2.4+
  - **自研：** HAMi K8s Operator 配置层（Go）

- [ ] **Volcano AI 批调度**
  - Gang Scheduling（多 Pod 协同任务，训练时必需）
  - 队列管理：推理队列（低延迟优先）/ 训练队列（资源复用）
  - 资源抢占策略
  - **开源组件：** Volcano 1.10+

- [ ] **GPU 资源看板**（第一个对外可见成果）
  - GPU 利用率、显存使用率、任务队列状态
  - 按节点 / 按租户 / 按任务维度聚合
  - **实现：** DCGM Exporter → Prometheus → Grafana Dashboard

#### 1.2.1 Workload Runtime / 实例抽象

- [ ] **传统 VM / 云主机实例**
  - 支持 KubeVirt 或客户已有云/虚拟化平台 adapter
  - 生命周期：创建、查询、停止/删除、状态收敛
  - VM 网络、镜像、云盘、SSH/VNC 等细节归 VM runtime adapter 管理

- [ ] **传统容器实例**
  - 基于 Kubernetes Pod / Deployment / Job adapter
  - 支持租户网络隔离、Gateway ingress、ServiceAccount、资源配额

- [ ] **GPU 容器实例**
  - 通过 `GPUInventory` 生成 nodeSelector、tolerations、resourceName、RuntimeClass、Volcano queue
  - 支持 NVIDIA、昇腾、海光以及 HAMi/vGPU/MIG 资源

- [ ] **上层专项实例**
  - 推理实例、Notebook、Agent Sandbox、Batch Job 都必须构建在 `WorkloadRuntime` 之上
  - 模型管理平台不得直接绕过运行时抽象创建 Pod、Deployment 或 KubeVirt VM
  - **实现：** `M1-RUNTIME-A`

#### 1.2.2 Instance Fabric / 网络与存储预置

- [ ] **实例对象与生命周期**
  - 所有 VM、普通容器、GPU 容器、推理、Notebook、Agent Sandbox、Batch Job 都是 ANI 一等实例对象
  - 生命周期动作：create / start / stop / restart / resize / delete
  - 生命周期状态：pending / provisioning / starting / running / stopping / stopped / failed / deleting / deleted
  - 2026-05-12 AWS 对标补强：当前 M1 实现属于最小可验证链路，正式产品需继续补齐状态原因、操作预检、操作时间线、停删改安全确认、日志/事件/指标、连接会话、快照/备份、扩缩容、回滚、GPU 调度原因、推理端点 autoscaling/流量策略等功能深度；详见 `repo/development-records/2026-05-12-aws-instance-lifecycle-reference.md`
  - 2026-05-12 实现拆解：先做 `M1-INSTANCE-T` 横切操作语义，再按 VM、容器/GPU、模型、推理、Notebook、Batch 和生产 Console 逐层补强；详见 `repo/development-records/2026-05-12-instance-lifecycle-implementation-plan.md`
  - 2026-05-12 P0 范围确认：v1.0.0 P0 实例类型限定为 VM、普通容器、GPU 容器和基础推理实例；Notebook、Batch/训练任务、Agent Sandbox 放入 P1/P2；快照、备份/恢复、克隆、灰度/回滚/高级 autoscaling 暂不进入 P0；详见 `repo/development-records/2026-05-12-p0-instance-scope-confirmation.md`

- [ ] **P0 操作语义底座**
  - 所有 P0 实例操作必须先支持 precheck、禁用原因、危险操作确认、`operation_id`、操作时间线、失败原因、建议处理、重试资格和审计记录
  - 这是后续 VM、容器、GPU 容器和推理实例补强的统一前置能力，避免每类实例重复实现操作反馈
  - **计划实现：** `M1-INSTANCE-T`

- [ ] **实例网络平面**
  - `tenant_vpc`：租户业务系统互通，VM 与 Pod 需要业务互通时共享此平面
  - `foundation_mesh`：平台服务互联平面，避免所有平台依赖嵌套进租户 VPC
  - `storage`：对象存储、PVC、模型缓存、数据集访问
  - `management`：控制面、健康检查、日志、指标、SSH/VNC proxy
  - `public_ingress`：通过 ANI Gateway 或 ingress adapter 显式暴露

- [ ] **实例存储附件**
  - `root_disk`、`data_disk`、`shared_pvc`、`object_fuse`、`ephemeral`
  - Runtime adapter 必须在调度前解析 StorageClass、Bucket/PVC、挂载模式和保留策略
  - 必需存储无法创建或挂载时必须提前失败，不得进入半创建状态
  - **实现：** `M1-INSTANCE-A`

- [ ] **实例规划器**
  - 在真实 provider adapter 创建资源前，统一校验实例对象、网络平面、存储附件、GPUInventory 依赖和生命周期动作
  - 默认 `PlanningRuntime` 不直接创建 Pod、Deployment、Job 或 KubeVirt VM，只生成计划态记录并提前失败
  - GPU 容器/推理实例在 GPUInventory 缺失或调度决策失败时必须拒绝创建
  - **实现：** `M1-INSTANCE-B`

- [ ] **Provider dry-run 渲染**
  - 将规划后的 VM 渲染为 KubeVirt `VirtualMachine`
  - 将普通容器/GPU 容器/Notebook/Sandbox/Inference 渲染为 Kubernetes `Deployment`
  - 将 Batch Job 渲染为 Kubernetes `Job`
  - 渲染结果必须保留网络平面、存储附件、GPU 调度和 `render-mode=dry-run` 注解
  - **实现：** `M1-INSTANCE-C`

- [ ] **Provider admission guardrail**
  - provider manifest 必须先通过本地 admission，再允许进入 server-side dry-run
  - 允许类型：KubeVirt `VirtualMachine`、Kubernetes `Deployment`、Kubernetes `Job`
  - 必须包含租户/实例标签、`render-mode=dry-run` 和网络平面注解
  - 禁止 `hostNetwork=true` 和 privileged container
  - **实现：** `M1-INSTANCE-D`

- [ ] **实例计划审计**
  - 在 provider server-side dry-run 或真实 create/apply 前持久化计划、渲染 manifest 和 admission 结果
  - 审计表必须启用租户 RLS
  - admission 被拒绝的请求也必须可审计
  - 未记录审计不得进入真实 provider 执行
  - **实现：** `M1-INSTANCE-E`

- [ ] **Provider dry-run executor**
  - 本地实现校验 provider/kind/apiVersion 映射，不创建资源
  - Kubernetes/KubeVirt 真实实现必须使用 server-side dry-run `dryRun=All`
  - admission 未通过不得进入 provider dry-run
  - mixed provider batch 必须拒绝
  - **实现：** `M1-INSTANCE-F`

- [ ] **Provider apply/create execution gate**
  - provider apply 默认关闭，执行开关未显式启用时必须 fail closed
  - 真实执行前必须校验 tenant/user/instance/audit id、权限证明、admission 结果和 provider dry-run 结果
  - 首批只允许 `create` 操作，后续生命周期动作需单独扩展白名单
  - 业务服务不得绕过 `WorkloadProviderApply` 直接 apply Kubernetes/KubeVirt/客户云资源
  - **实现：** `M1-INSTANCE-G`

- [ ] **实例状态回写与生命周期 reconcile**
  - provider 状态必须先标准化为 observation，再进入 `WorkloadStatusReconciler`
  - observation 必须关联 tenant、instance、audit id 和 apply resource refs
  - provider phase 必须映射为 ANI 标准 `WorkloadState`
  - 业务服务不得直接轮询 Kubernetes/KubeVirt/客户云状态 API
  - **实现：** `M1-INSTANCE-H`

- [ ] **Provider status reader 与实例编排 API**
  - provider 状态读取必须封装在 `WorkloadProviderStatusReader`
  - 业务服务创建实例必须通过 `WorkloadInstanceOrchestrator`
  - 编排链路必须按 plan/render/admission/audit/dry-run/apply/status/reconcile 顺序执行
  - 业务服务不得手动串联 provider manifest、dry-run、apply、status reader 或 reconcile 细节
  - **实现：** `M1-INSTANCE-I`

- [ ] **实例持久化与查询 API**
  - 实例状态必须写入 `workload_instances` 租户 RLS 表
  - 持久化记录必须关联 audit id、provider id、resource refs、网络和存储状态
  - 查询恢复必须通过 `WorkloadInstanceStore.Get/List`
  - 业务查询不得依赖 `PlanningRuntime` 内存状态
  - **实现：** `M1-INSTANCE-J`

- [ ] **Kubernetes/KubeVirt provider adapter**
  - Kubernetes/KubeVirt SDK 只能出现在 adapter 内部
  - server-side dry-run 必须使用 `dryRun=All`
  - apply 默认关闭，开启后仍需 admission、audit、permission proof 和 dry-run 证据
  - provider status 必须归一化为 `WorkloadProviderObservation`
  - **实现：** `M1-INSTANCE-K`

- [ ] **实例服务 API 层**
  - VM、普通容器和 GPU 容器创建必须通过 `WorkloadInstanceService.Create`
  - 查询必须通过 `WorkloadInstanceService.Get/List`
  - 服务层不得暴露 provider manifest、Kubernetes/KubeVirt SDK 对象或 provider-specific status
  - **实现：** `M1-INSTANCE-L`

- [ ] **实例生命周期与可视化运维 API**
  - VM、普通容器、GPU 容器必须支持 Start/Stop/Restart/Resize/Delete 服务入口
  - 容器可视化运维操作必须覆盖 logs/events/metrics/terminal/exec
  - ops 默认关闭，生产实现必须通过 adapter 进入 Kubernetes/KubeVirt API
  - 业务服务不得直接调用 Kubernetes logs/events/metrics/exec 或 KubeVirt console/VNC API
  - **实现：** `M1-INSTANCE-M`

- [ ] **M1 端到端集成剖面**
  - 覆盖 VM、普通容器、GPU 容器创建链路
  - 覆盖 Start/Stop/Restart/Resize 查询恢复链路
  - 覆盖容器 logs/terminal、GPU metrics/exec 运维操作合同
  - 默认离线本地剖面，生产剖面可替换为真实 `KubernetesProviderClient`
  - **实现：** `M1-E2E-A`

- [ ] **Kubernetes Provider 执行剖面**
  - 覆盖 `KubernetesProviderClient.ServerSideDryRun` 与 `dryRun=All`
  - 覆盖受控 `Apply`、`Observe`、resource refs、audit ID 和 permission proof
  - 真实 client-go/KubeVirt client 只能放在 adapter-owned package
  - 业务服务不得导入 Kubernetes/KubeVirt SDK 或 provider-specific 对象
  - **实现：** `M1-INSTANCE-N`

- [ ] **Kubernetes REST Client 实现**
  - adapter-owned `KubernetesRESTClient` 实现 `KubernetesProviderClient`
  - 标准库 HTTP 调用 Kubernetes API，覆盖 `dryRun=All` 和 server-side apply
  - 支持 Kubernetes Deployment、Kubernetes Job、KubeVirt VirtualMachine
  - Observe 输出标准 `WorkloadProviderObservation`
  - **实现：** `M1-INSTANCE-O`

- [ ] **Kubernetes Provider Bootstrap Wiring**
  - 默认使用 local provider，保持离线开发稳定
  - `WORKLOAD_PROVIDER=kubernetes_rest` 时启用 `KubernetesRESTClient`
  - `WORKLOAD_PROVIDER_APPLY_ENABLED` 默认关闭
  - 支持 `KUBERNETES_API_HOST`、`KUBERNETES_BEARER_TOKEN`、`KUBERNETES_PROVIDER_FIELD_MANAGER`
  - **实现：** `M1-INSTANCE-P`

- [ ] **Kubernetes Lifecycle Execution**
  - 新增 `WorkloadInstanceLifecycleExecutor` provider 执行边界
  - `WORKLOAD_LIFECYCLE_PROVIDER=kubernetes_rest` 时启用 `KubernetesLifecycleExecutor`
  - `WORKLOAD_LIFECYCLE_APPLY_ENABLED` 默认关闭
  - 覆盖 Start/Stop/Restart/Resize/Delete 的 provider 调用边界
  - **实现：** `M1-INSTANCE-Q`

- [ ] **Kubernetes Visual Ops Execution**
  - 新增 `KubernetesInstanceOps` provider 执行边界
  - `WORKLOAD_OPS_PROVIDER=kubernetes_rest` 时启用 Kubernetes ops adapter
  - `WORKLOAD_OPS_ENABLED` 默认关闭
  - 覆盖 logs/events/metrics/terminal/exec 的 provider 调用边界
  - **实现：** `M1-INSTANCE-R`

- [ ] **M1 Real Provider Integration Regression Profile**
  - 统一覆盖 Kubernetes REST provider create/observe/lifecycle/ops 链路
  - 使用 fake HTTP transport 验证真实 adapter 链路，不依赖真实集群
  - 确认 local/offline default 和 execution switches 仍保持安全
  - **实现：** `M1-E2E-B`

#### 1.3 存储底座

- [ ] **MinIO**（模型仓库和数据集的对象存储）
  - 多节点纠删码部署（≥4 节点）
  - 完全离线，不依赖外网
  - Bucket 规划：`ani-models`、`ani-datasets`、`ani-kb-docs`
  - **开源组件：** MinIO RELEASE.2025+

- [ ] **Milvus 向量数据库**
  - Milvus Operator 方式部署（K8s 原生）
  - 生产用 Cluster 模式，测试用 Standalone
  - **开源组件：** Milvus 2.5+

- [ ] **PostgreSQL 17**
  - CloudNativePG Operator 管理（主从 + PgBouncer 连接池）
  - 初始 Schema：租户表、模型元数据表、权限表、审计日志表
  - Row-Level Security（RLS）实现多租户数据隔离
  - **开源组件：** CloudNativePG 1.x、PostgreSQL 17

- [ ] **Harbor 容器镜像仓库**（独立部署，与 ANI 松耦合）
  - Helm Chart 独立部署，不依赖 ANI 其他组件
  - 集成 Trivy 漏洞扫描
  - ANI Gateway 新增 `harbor-proxy` 模块（Go）：转发 Console/BOSS 请求到 Harbor API，附加认证头，屏蔽 Harbor 内部地址
  - **开源组件：** Harbor 2.x

---

### 模块 2：ANI Gateway（统一 Web Server 层）（M1-M2）

**目标：** 所有消费者的唯一入口，从这里衍生出 REST API、SDK、CLI、运维 Skills。

> 这是整个架构最关键的模块，M1 第一周就要搭好骨架，后续所有模块都依赖它。

#### 2.1 Gateway 骨架（Go + Hertz）

- [ ] **项目初始化**
  ```
  ani-gateway/
  ├── cmd/gateway/          # 启动入口
  ├── internal/
  │   ├── handler/          # HTTP Handler
  │   ├── middleware/       # 中间件链
  │   ├── router/           # 路由注册
  │   └── service/          # 业务编排
  ├── pkg/
  │   ├── auth/             # JWT/OAuth
  │   ├── ratelimit/        # 限流
  │   ├── errors/           # 统一错误类型
  │   └── harbor/           # harbor-proxy 模块
  ├── api/openapi/          # OpenAPI 3.1 Spec（代码先于实现）
  └── api/proto/            # Protobuf 定义
  ```
  - **框架：** Hertz 0.9+（CloudWeGo，字节开源，日万亿级请求生产验证）

- [ ] **Middleware 链**（按顺序执行）
  1. TLS 终止 + RequestID 注入（全链路唯一 ID）
  2. JWT 认证（验证 + 解析租户/用户信息）
  3. RBAC 授权（OPA 策略检查）
  4. 令牌桶限流（按租户维度，防止单一客户耗尽 GPU 资源）
  5. 审计日志打点（异步写入，不阻塞主流程）
  6. 路由分发 → 对应 gRPC Service
  7. 统一错误响应：`{ code, message, request_id, details }`

- [ ] **OpenAPI 3.1 Spec-First 工作流**
  - 所有 API 的 Spec 定义先于代码，禁止反向
  - `make gen-api`：oapi-codegen 生成 Go Server/Client → buf 生成 Protobuf 代码 → grpc-gateway 生成 REST 转发层
  - 同一 Spec 同时生成：Go SDK、Python SDK、TypeScript SDK、API 文档站

- [ ] **SSE 流式输出**
  - `/v1/chat/completions` 流式接口（OpenAI 兼容格式）
  - Hertz SSE Handler 封装，客户端断线检测与资源释放

- [ ] **NATS JetStream 异步任务框架**
  - Subject 规划：`ani.tasks.model.*`、`ani.tasks.kb.*`、`ani.tasks.import.*`
  - 提交：`POST /api/v1/tasks` → `202 Accepted + { task_id }`
  - 查询：`GET /api/v1/tasks/{id}` → `{ status, progress, result }`
  - Webhook 回调：任务完成后主动推送到客户配置的 URL
  - **开源组件：** NATS JetStream 2.10+
  - **当前实现对齐（2026-05-11）：**
    - 当前仍属于 `模块 2 -> 2.1 Gateway 骨架 -> NATS JetStream 异步任务框架`，尚未进入 `模块 3：模型管理平台`。
    - 代码生成批次命名统一为 `M2.1-TASK-A/B/C`；历史记录里的 `Stage 3A/3B/3C` 只是旧的内部切片名，不代表 `ANI-06` 的模块 3。
    - `M2.1-TASK-A`：已实现最小 `task-service` 查询接口，支持 `GetTask`，读取前设置租户上下文以配合 PostgreSQL RLS。
    - `M2.1-TASK-B`：已实现 `transactional outbox` 仓储与 NATS outbox publisher，使用 `FOR UPDATE SKIP LOCKED` 拉取未发布事件。
    - `M2.1-TASK-C`：已实现 worker mutation RPC，包括任务领取、租约心跳、进度更新、失败、完成；已修正 `task_service.proto` 的租户/worker 安全上下文。
    - 进度记录文件：
      - `repo/development-records/m2-1-task-a-b-task-service-outbox.md`
      - `repo/development-records/m2-1-task-c-worker-mutations.md`
      - `repo/development-records/2026-05-11-handoff-codex-cloud.md`
    - 当前验证状态：`make gen-proto`、`make test`、`make build` 已通过。

#### 2.2 认证授权（Go）（M2）

**当前实现对齐（2026-05-11）：**
- `M2.2-AUTH-A`：已新增最小 `auth-service`，实现内部 `ValidateToken` 的 RS256 JWT 校验和基础 `CheckPermission` 角色规则。
- `M2.2-AUTH-B`：已将 ANI Gateway Auth/RBAC middleware 接入 auth-service gRPC，并保留 `ANI_AUTH_MODE=dev`。
- `M2.2-AUTH-C`：已实现 RLS-safe API Key 创建、列表、吊销和验证；API Key 原文内嵌 tenant UUID，查询前设置 PostgreSQL RLS tenant context，不引入 `BYPASSRLS`。
- 已保留明确边界：传统 `Login` 暂不实现；OIDC begin/callback 已支持 code exchange、JWKS / 静态公钥 ID token verifier 和显式 group-role 映射；`RefreshToken` 已通过持久化 refresh token 换发 AccessToken；`RevokeToken` 已通过 PostgreSQL + `CacheStore` 实现 JWT blocklist；API Key 管理已完成 RLS-safe lifecycle。
- 验证命令：`make gen-proto`、`make validate-infra`、`make test`、`make build` 已通过。
- 进度记录文件：`repo/development-records/m2-2-auth-a-auth-service-foundation.md`。
- 进度记录文件：`repo/development-records/m2-2-auth-b-gateway-auth-wiring.md`。
- 进度记录文件：`repo/development-records/m2-2-auth-c-api-keys.md`。
- 下一阶段候选：`M2.2-AUTH-D`，Dex/OIDC 登录集成或 API Key RLS 集成测试。
- 规划文件：`repo/development-records/2026-05-11-next-development-plan.md`。

- [ ] **Dex（OIDC IdP）**
  - 对接企业 AD/LDAP（客户现有用户体系，无需重建账号）
  - SAML 2.0 支持（金融/国央企常用）
  - **开源组件：** Dex latest

- [ ] **JWT 服务**
  - AccessToken（1 小时过期）+ RefreshToken（7 天）
  - Token 吊销：黑名单机制，Redis 存储
  - API Key 管理：长期 Token，供 CLI / SDK / 自动化脚本使用

- [ ] **RBAC 服务**
  - 角色：`platform-admin` / `tenant-admin` / `user` / `auditor`
  - 权限粒度：API 路径 + HTTP Method
  - 与 Dex 集成：从 OIDC Token 的 `groups` 字段提取角色

---

### 模块 3：模型管理平台（Go + Python）（M2）

**目标：** IT 管理员无需懂 AI，把模型文件变成一个可调用的内网 API。

#### 3.1 私有模型仓库（Go）

- [ ] **模型元数据服务**
  - 数据表：`models (id, name, version, format, size_bytes, status, is_encrypted, encrypt_algo, encrypt_hint, meta_json)`
  - API：`GET/POST /api/v1/models`、`GET /api/v1/models/{id}`、`DELETE /api/v1/models/{id}/versions/{ver}`
  - 版本管理：同一模型多版本并存，支持 tag（latest / stable）
  - 能力标签：文本生成、嵌入、语音识别、视觉理解等

- [ ] **模型文件上传**
  - 分片上传 + 断点续传（支持 >100GB 大文件）
  - 上传写入 MinIO `ani-models` Bucket
  - 格式支持：HuggingFace safetensors、GGUF
  - 完整性校验：SHA256 checksum 验证后才更新状态为 `ready`

- [ ] **内置模型预配置模板**
  - Qwen2.5-7B / 14B / 72B（通义千问）
  - DeepSeek-V3 / R1-7B / 32B（幻方）
  - GLM-4-9B（智谱 AI）
  - BGE-M3（BAAI 开源，知识库向量化必需）
  - Faster-Whisper（语音转写）
  - 每个模型预置推荐 GPU 型号、显存要求、并发建议值

#### 3.2 模型加解密（Go，国密优先）

> 企业自训练/微调的模型是核心资产。平台提供存储加密保护，密钥由用户完全持有，平台不保存。

- [ ] **加密算法支持层**
  - **默认算法：SM4-GCM**（国密分组密码，128-bit 密钥，认证加密防篡改）
  - **扩展支持：** ZUC（祖冲之序列密码，3GPP 国密标准）、SM1（硬件实现为主）
  - **国际兼容：** AES-256-GCM（备选，非国密场景）
  - 密钥派生：PBKDF2 + SM3（用户输入密码 → 派生加密密钥，杜绝明文密码直接使用）
  - **开源组件：** `github.com/tjfoc/gmsm`（Go 国密库，SM1/SM2/SM3/SM4 完整实现）

- [ ] **加密文件格式（`.anip` — ANI Protected）**
  ```
  [文件头 64 bytes]
    magic:      "ANIP" (4 bytes)
    version:    uint8
    algo:       uint8  (0x01=SM4, 0x02=ZUC, 0x03=AES256)
    salt:       32 bytes (PBKDF2 盐值)
    digest:     SM3 摘要 (32 bytes，用于完整性校验)
  [加密数据流，分块处理]
  ```

- [ ] **模型加密 CLI 工具**
  ```bash
  ani model encrypt ./qwen2.5-72b/ --algo sm4 --out qwen2.5-72b.anip
  ani model decrypt qwen2.5-72b.anip --out ./qwen2.5-72b-decrypted/
  ```
  - 流式分块加解密（512MB/chunk），不全量读入内存，支持超大模型文件
  - 加密过程显示进度条和预计剩余时间

- [ ] **推理时运行时解密**
  - `InferenceService` CRD 新增 `encryptionKeyRef`（引用 K8s Secret 存储的密钥）
  - 推理 Pod 启动流程：
    ```
    Init Container（Go 实现）:
      1. 从 K8s Secret 读取密钥
      2. 从 MinIO 下载 .anip 文件
      3. 流式解密到 emptyDir（tmpfs 内存盘）
    主容器（vLLM）:
      4. 从 emptyDir 加载明文模型
    Pod 销毁时:
      5. emptyDir 随 Pod 消失，明文和密钥均不落盘
    ```
  - 密钥传递：用户通过 Console/API 提交密钥 → 转存为 K8s Secret → Init Container 通过环境变量读取

- [ ] **微调模型加密发布**
  - 微调完成后可选"加密后发布到仓库"
  - 工作流：微调完成 → 加密 API → 加密文件写入 MinIO → 元数据标记 `is_encrypted=true`

#### 3.3 远程模型导入（Go + Python）

> 模型不预先打包进镜像，Pod 启动时从模型仓库动态拉取，实现镜像与模型彻底解耦。

- [ ] **HuggingFace 导入**
  - `POST /api/v1/models/import` `{ source: "huggingface", repo_id: "Qwen/Qwen2.5-72B-Instruct" }`
  - 异步执行，返回 `task_id`，客户端轮询或 Webhook 通知进度
  - Python 下载服务：`huggingface_hub` 库，支持 `HF_ENDPOINT` 配置（指向国内镜像站）
  - 断点续传：记录已下载 shard，中断后从断点继续，不重下
  - 下载专属 Pod 开放外网出口（KubeOVN NetworkPolicy），其他 Pod 保持内网隔离
  - **开源组件：** huggingface_hub latest

- [ ] **ModelScope 导入**
  - `POST /api/v1/models/import` `{ source: "modelscope", model_id: "qwen/Qwen2.5-72B-Instruct" }`
  - 使用 `modelscope` Python SDK
  - 共用 HuggingFace 的任务调度框架，逻辑一致
  - **开源组件：** modelscope latest

- [ ] **推理 Pod 模型动态加载**（Init Container 模式）
  ```
  vLLM 推理 Pod 启动时:
    Init Container（Go 单一二进制）:
      1. 检查节点 PVC 缓存是否已有该模型版本
      2. 如无缓存：调用模型仓库 API → 获取 MinIO presigned URL → 下载
      3. 如模型加密：执行解密（SM4/ZUC）
      4. 将模型文件 ready 信号写入共享 emptyDir
    主容器（vLLM）:
      5. 从 emptyDir / PVC 缓存路径加载模型启动
  ```
  - 节点 PVC 缓存：避免同一节点多次下载同一模型版本
  - 好处：vLLM 镜像仅含推理运行时，无模型文件，镜像体积小，版本切换无需重新构建镜像

#### 3.4 一键推理部署（Go Operator + Python）（M3）

- [ ] **InferenceService K8s Operator（Go）**
  ```yaml
  apiVersion: ani.kubercloud.io/v1
  kind: InferenceService
  metadata:
    name: qwen2.5-72b-prod
  spec:
    model: qwen2.5-72b:v2          # 模型仓库 ID
    replicas: 2                     # 副本数
    gpuType: A100                   # GPU 型号
    gpuCount: 4                     # 每副本 GPU 数量
    maxConcurrency: 8               # 最大并发请求数
    encryptionKeyRef:               # 仅加密模型需要
      secretName: model-key-qwen
      key: password
  ```
  - Controller 监听 CR，自动创建 vLLM Deployment + K8s Service + 自动注入 Init Container
  - 状态机：`Pending` → `Downloading` → `Decrypting` → `Deploying` → `Running` / `Failed`

- [ ] **vLLM 推理服务封装（Python）**
  - 启动参数模板（按 GPU 型号和模型大小自动推荐 `--tensor-parallel-size`、`--gpu-memory-utilization`）
  - 暴露标准 OpenAI 兼容接口：`/v1/chat/completions`、`/v1/embeddings`
  - **开源组件：** vLLM 0.6+

- [ ] **推理服务路由（Go，ANI Gateway 层）**
  - 路由规则：`/v1/chat/completions` + `X-Model-Name: qwen2.5-72b` → 转发至对应 vLLM Service
  - 超并发排队：超出 `maxConcurrency` 时排队等候（而非直接返回 429）
  - 负载均衡：多副本轮询
  - 调用审计：记录 request_id / 用户 / 模型 / prompt_tokens / completion_tokens / 延迟

---

### 模块 4：企业知识库问答（Go + Python）（M3）

**目标：** Phase 1 核心交付物，业务用户最直接感知的 AI 能力，决定客户续费。

#### 4.1 文档管理（Go）

- [ ] **文档上传 API**
  - 格式：PDF、Word(.docx)、Excel(.xlsx)、PPT(.pptx)、TXT、Markdown
  - 文件存入 MinIO `ani-kb-docs`，上传完成后发 NATS 消息触发解析任务

- [ ] **文档解析服务（Python）**
  - **开源组件：** Docling（IBM 开源，PDF 版面分析 + 表格识别 + OCR 最完整）
  - OCR：PaddleOCR（中文准确率高于 Tesseract）
  - 输出：结构化 Markdown，保留标题层级和表格
  - 扫描件 PDF 走 OCR 路径，数字 PDF 直接提取不走 OCR

#### 4.2 RAG 引擎（Python）

- [ ] **向量化服务**
  - 嵌入模型：BGE-M3（BAAI，中英文双语效果最佳，免费开源）
  - 切片策略：语义边界切分（chunk ≈ 512 token，不硬截断段落）
  - 写入 Milvus（Collection 按知识库 ID 隔离，租户间不互通）
  - **开源组件：** sentence-transformers、Milvus 2.5+

- [ ] **混合检索**
  - 语义检索：Milvus ANN 向量搜索（召回语义相关段落）
  - 关键词检索：PostgreSQL pg_trgm 全文搜索（召回精确关键词）
  - 融合重排：RRF（Reciprocal Rank Fusion）算法，两路召回合并去重排序
  - Top-K：默认召回 5 段，可按知识库配置覆盖

- [ ] **问答生成**
  - Prompt 模板：系统提示词 + 检索上下文 + 用户问题
  - 来源引用：每段答案附来源文档名 + 页码（从 Milvus metadata 提取）
  - 置信度过滤：相似度低于阈值时返回"未找到相关内容"，不编造答案
  - 多轮对话：保留最近 10 轮历史，支持追问

- [ ] **知识库管理 API（Go）**
  - `POST /api/v1/knowledge-bases` — 创建知识库
  - `POST /api/v1/knowledge-bases/{id}/documents` — 上传文档
  - `GET /api/v1/knowledge-bases/{id}/documents` — 文档列表及解析状态
  - `DELETE /api/v1/knowledge-bases/{id}/documents/{doc_id}` — 删除文档
  - `POST /api/v1/knowledge-bases/{id}/query` — 执行问答
  - 权限隔离：知识库归属租户，跨租户无法访问

---

### 模块 5：前端 Console（TypeScript + React）（M4）

**目标：** IT 管理员和业务部门用户的操作界面，30 分钟能学会用。

#### 5.1 工程搭建

- [ ] **Monorepo 初始化**（Console + BOSS 共一个仓库）
  - pnpm workspace + Turborepo 构建缓存
  - Vite 5 + React 18 + TypeScript 5
  - TDesign React 1.x（腾讯开源企业组件库，中文友好，有 Mobile 版）
  - TanStack Router（类型安全路由 + 代码分割）
  - TanStack Query（服务端数据缓存与同步）
  - Zustand（轻量客户端 UI 状态）
  - 从 OpenAPI Spec 自动生成 TypeScript SDK（openapi-typescript-codegen）

- [ ] **OIDC 鉴权流程**
  - 跳转 Dex → 回调处理 Token → AccessToken 无感刷新
  - 多租户切换（一个账号可属于多个租户）

#### 5.2 Console 主要页面

- [ ] **仪表盘（首页）**
  - GPU 资源卡片：总量 / 已用 / 空闲
  - 推理服务列表：运行中 / 部署中 / 异常（含快捷操作）
  - 知识库调用量 7 日趋势图

- [ ] **模型管理页**
  - 模型列表（名称、版本、状态、是否加密、GPU 占用）
  - 模型来源：本地上传（分片进度条）/ HuggingFace 导入 / ModelScope 导入
  - 一键部署弹窗（选 GPU 数量、并发数、是否需要输入解密密码）
  - 推理服务日志实时查看（SSE 流式）

- [ ] **知识库管理页**
  - 知识库列表 + 新建
  - 文档管理（上传、解析进度、删除）
  - 知识库问答测试界面（对话框，带来源引用高亮）

- [ ] **容器镜像仓库页**（封装 Harbor API，via harbor-proxy）
  - 项目（Project）列表与创建
  - 镜像仓库（Repository）列表、搜索
  - 镜像 Tag 列表、漏洞扫描结果查看（Trivy）
  - 拉取命令一键复制
  - 镜像删除（二次确认）
  - **不做：** Harbor 用户管理、LDAP 配置等运维操作（保留在 Harbor 原生 UI）

- [ ] **用量报表页**
  - 按时间段查询调用量
  - 按模型 / 知识库 / 用户维度统计
  - Token 消耗量 + GPU 计算时长

---

### 模块 6：前端 BOSS（TypeScript + React）（M4）

**目标：** 常青云内部运营和运维团队的后台，与 Console 同步全量开发。

与 Console 共享 Monorepo 脚手架、TDesign 组件库、API SDK。

- [ ] **多租户管理**
  - 租户列表（创建、查看、禁用、配额修改）
  - 租户管理员账号初始化 + 重置密码
  - 租户资源使用概览

- [ ] **资源配额管理**
  - 按租户分配 GPU 配额（最大并发数、最大 GPU 数量）
  - 配额使用率趋势图

- [ ] **计费与账单**
  - GPU 计算时长统计（按租户 / 按模型）
  - Token 消耗量统计
  - 账单报表 CSV 导出

- [ ] **平台健康大盘**
  - 嵌入 Grafana Dashboard（Grafana Embedding API）
  - 系统告警列表（来自 AlertManager，P0/P1 分级显示）
  - 节点状态列表（GPU 节点在线 / 离线 / 异常）

- [ ] **运维操作面板**（运维 Skills 触发界面）
  - 手动触发运维 Skills（模型回滚、知识库重新索引、推理扩容等）
  - Skills 执行历史 + 日志查看

- [ ] **镜像仓库运维管理**（BOSS 专属，封装 Harbor API）
  - Harbor 项目配额管理（按租户分配存储配额）
  - 全局漏洞扫描报告汇总
  - 垃圾回收任务触发 + 状态查看
  - Harbor 系统配置查看（只读）

- [ ] **工单与客户列表**
  - 客户基本信息管理
  - 简单工单记录（问题描述 + 处理状态）

---

### 模块 7：CLI 工具 `ani`（Go）（M4）

- [ ] **cobra + viper 骨架**
  - 全局配置：`~/.ani/config.yaml`
  - `ani config set endpoint https://ani.company.internal`
  - `ani config set api-key <key>`

- [ ] **第一版子命令集**
  ```bash
  # 模型管理
  ani model list
  ani model upload ./qwen2.5-72b/ --name qwen2.5-72b --version v1
  ani model encrypt ./qwen2.5-72b/ --algo sm4 --out qwen2.5-72b.anip
  ani model import --source huggingface --repo Qwen/Qwen2.5-72B-Instruct
  ani model import --source modelscope --model qwen/Qwen2.5-72B-Instruct
  ani model deploy qwen2.5-72b --gpu-count 4 --replicas 2
  ani model status qwen2.5-72b
  ani model logs qwen2.5-72b --follow

  # 知识库
  ani kb create --name "人事制度"
  ani kb upload <kb-id> ./docs/
  ani kb query <kb-id> "差旅报销标准是什么？"

  # 集群
  ani cluster gpu-status
  ani cluster nodes
  ```
  - CLI 完全复用 Go SDK，不重复实现 HTTP 调用逻辑

---

### 模块 8：可观测性（贯穿 M1-M5）

- [ ] **指标采集（Prometheus）**
  - DCGM Exporter：GPU 利用率、显存、温度、功耗
  - vLLM 内置 Prometheus 端点：QPS、TTFT、Token 速率
  - ANI Gateway 自定义 Metrics：请求量、P50/P99 延迟、错误率、每个租户调用量

- [ ] **Grafana 仪表板**（预置 3 套模板）
  - GPU 集群大盘
  - 推理服务大盘
  - 知识库服务大盘

- [ ] **分布式追踪（OpenTelemetry + Jaeger）**
  - ANI Gateway 自动注入 TraceID（与 RequestID 关联）
  - 所有 Go 微服务传递 Trace Context
  - 一个 request_id 可查到完整调用链（Gateway → Service → vLLM）

- [ ] **日志（Loki + Promtail）**
  - 结构化 JSON 日志
  - 按 tenant_id 过滤
  - 审计日志单独 Collection，追加写入，不可篡改

- [ ] **告警规则（AlertManager）**
  - GPU 温度 > 85°C → P1
  - 推理服务错误率 > 5% → P0（立即响应）
  - 磁盘剩余 < 20% → P1
  - API P99 延迟 > 2s → P1
  - 推理 TTFT > 10s → P1

---

## 四、Phase 2 开发点预览（2026-10 起）

### 文档智能处理
- [ ] 合同要素结构化提取（LLM + JSON Schema 输出）
- [ ] 批量文档处理（100 份并行，NATS 任务队列）
- [ ] 公文智能起草（公文格式模板 + LLM 生成）
- [ ] 文档摘要（可配置摘要长度）

### 会议智能
- [ ] Faster-Whisper 语音转写（Python）
- [ ] 发言人区分（Speaker Diarization，pyannote.audio）
- [ ] 会议纪要结构化生成（LLM）
- [ ] 企微 / 钉钉 Bot 集成（Webhook）

### 模型微调平台（轻量版）
- [ ] 数据标注界面（Q&A 对人工标注，前端）
- [ ] LLaMA-Factory 封装（LoRA 微调，Python）
- [ ] 微调任务管理（进度、日志、Eval 对比）
- [ ] 微调模型一键加密后发布为推理服务

### 等保合规强化
- [ ] 等保 2.0 三级合规架构完整文档（必需交付物）
- [ ] 数据脱敏中间件（NER 识别证件号、手机号，推理前自动屏蔽）
- [ ] Vault 集成（敏感配置统一管理）

---

## 五、开发依赖关键路径

```
M1（5月）
├── K8s 集群 + KubeOVN ──────────────────────────────────→ 所有 Pod 依赖此
├── MinIO + PostgreSQL + Milvus ─────────────────────────→ 模型仓库 / RAG 依赖此
├── Harbor 独立部署 ──────────────────────────────────────→ 镜像仓库页面依赖此
└── ANI Gateway 骨架 + Middleware 链 ────────────────────→ 所有 API 依赖此 ⭐

M2（6月）
├── Dex + JWT + RBAC ───────────────────────────────────→ 所有接口鉴权依赖此
├── 模型仓库 API（上传 + 元数据）──────────────────────→ 推理部署依赖此
├── 模型加解密（gmsm + .anip 格式 + CLI）────────────→ 加密推理依赖此
└── HuggingFace / ModelScope 导入 + Init Container ──→ 动态加载依赖此

M3（7月）
├── InferenceService Operator ──────────────────────────→ 模型部署起点 ⭐
├── vLLM 推理服务封装 ──────────────────────────────────→ 推理 API 依赖此
├── RAG 引擎（文档解析 + 向量化 + 混合检索 + 问答）→ 知识库问答 ⭐
└── 知识库管理 API ─────────────────────────────────────→ 前端依赖此

M4（8月）
├── Console 前端（Monorepo）────────────────────────────→ 依赖 M1-M3 全部 API
├── BOSS 前端（同上）──────────────────────────────────→ 依赖 M1-M3 全部 API
└── ani CLI（复用 Go SDK）──────────────────────────────→ SDK 依赖 Gateway Spec

M5（9月）
├── 可观测性完整闭环 ───────────────────────────────────→ 依赖各服务暴露 Metrics
├── 信创适配（UOS + ARM64 构建）────────────────────────→ 依赖 M1-M4 全部完成
└── 集成测试 + 性能基线 + 离线安装包 ──────────────────→ 最终交付验证
```

---

## 六、AI 辅助的关键加速点

| 模块 | 人工负责 | AI 生成 |
|---|---|---|
| ANI Gateway | OpenAPI Spec 定义、安全边界审查 | Handler 骨架、Middleware 实现、错误处理 |
| 模型加密 | 算法选型、密钥安全设计 | SM4-GCM 流式加解密完整实现（基于 gmsm） |
| RAG 引擎 | Prompt 模板调优、检索策略 | LangChain Pipeline 代码、向量化服务 |
| K8s Operator | CRD 设计、状态机 | controller-runtime Controller 实现 |
| 所有 CRUD API | Spec 定义、权限设计 | Server Stub、Client SDK、单元测试 |
| 前端页面 | 交互逻辑、信息架构 | TDesign 组件拼装、TanStack Query hooks |
| CLI 工具 | 命令设计、用户体验 | cobra 子命令实现、帮助文档 |

---

## 七、开源组件选型清单

所有组件均满足：① 生产级成熟度 ② 符合 Go/Python/TS 技术栈 ③ 支持完全离线部署 ④ 有信创替代路径

| 层级 | 组件 | 版本 | 选型理由 |
|---|---|---|---|
| 编排 | Kubernetes（RKE2） | 1.36 | 行业标准，RKE2 适合离线和安全加固 |
| 网络 | KubeOVN | 1.13+ | Go 实现，国内主导，原生 VPC 多租户 |
| 容器运行时 | containerd | 2.1+ | K8s 推荐标准运行时 |
| GPU | HAMi | 2.4+ | 唯一同时支持 NVIDIA+昇腾+海光 的开源方案 |
| GPU 调度 | Volcano | 1.10+ | K8s 原生 AI 批调度事实标准 |
| LLM 推理 | vLLM | 0.6+ | 最高吞吐量，OpenAI 兼容，社区最活跃 |
| 语音 | Faster-Whisper | latest | Whisper 最快推理实现 |
| 向量库 | Milvus | 2.5+ | 国内团队，K8s 原生，亿级向量 |
| 对象存储 | MinIO | 2025+ | S3 兼容，离线可用，信创可替换 |
| 关系数据库 | PostgreSQL 17 | 17 | 信创兼容（金仓 KingbaseES 兼容 PG 协议） |
| Web 框架 | Hertz | 0.9+ | 字节开源，高性能，gRPC 原生，生产验证 |
| 消息队列 | NATS JetStream | 2.10+ | 轻量，Go 原生，比 Kafka 运维简单 10 倍 |
| 认证 | Dex | latest | OIDC 标准，LDAP/SAML 双协议 |
| 监控 | Prometheus + Grafana | latest | K8s 原生监控行业标准 |
| 追踪 | OpenTelemetry + Jaeger | latest | 标准化 Trace，Go SDK 完善 |
| 日志 | Loki + Promtail | latest | 轻量，K8s 原生，比 ELK 省 60% 资源 |
| 安全 | OPA + Falco | latest | K8s 准入控制 + 运行时安全双保险 |
| TLS | cert-manager | latest | K8s 证书自动化标准 |
| 文档解析 | Docling | latest | IBM 开源，PDF/表格/OCR 最完整 |
| OCR | PaddleOCR | latest | 中文识别准确率最高 |
| RAG 框架 | LangChain | 0.3+ | Python RAG 生态最成熟 |
| 微调 | LLaMA-Factory | latest | 国产模型全覆盖，LoRA 标准实现 |
| 国密加密 | gmsm | latest | Go 国密唯一成熟实现（SM1/SM2/SM3/SM4） |
| 镜像仓库 | Harbor | 2.x | 企业级标准，独立部署，ANI 只做 API 封装 |
| HF 下载 | huggingface_hub | latest | 官方 Python SDK，支持断点续传 |
| MS 下载 | modelscope | latest | 魔搭官方 SDK，国内模型首选 |
| CLI | cobra + viper | latest | Go CLI 事实标准（kubectl 同款） |
| 前端框架 | React 18 + TDesign | 18 / 1.x | 企业组件库，中文友好，有 Mobile 版 |
| 构建工具 | Vite 5 | 5+ | 最快前端构建，HMR 秒级 |

---

---

## 八、V8 新增模块规划（已纳入冲刺计划）

> 以下模块在 Sprint 3~5（已纳入 Section 二冲刺计划）实现，本节保留详细技术规格供实现时参考。

本节记录 V8 架构重规划新增的开发模块，作为后续代码生成批次的完整任务清单。

### 模块 M1-SANDBOX：Sandbox 安全沙箱实例

**目标：** 为 Agent 工作负载提供专用隔离运行环境，对标 E2B，P0 基于 Kata Containers + QEMU。

**代码批次规划：**

- [ ] `M1-SANDBOX-A`：Kata Containers QEMU RuntimeClass 接入
  - 部署 Kata Containers daemonset + RuntimeClass `sandbox-kata`
  - `WorkloadRuntime` 新增 `kind=sandbox` 支持，部署走 Kata RuntimeClass
  - Sandbox 实例 spec 增加 `session_policy`（出口约束/最大时长/资源配额）
  - 验证：创建 Sandbox → exec 命令 → 查看日志 → 清理

- [ ] `M1-SANDBOX-B`（P1）：Kata + Firecracker 后端
  - 新增 RuntimeClass `sandbox-kata-fc`，启动时间目标 ~150ms
  - CPU-only 场景，不支持 GPU passthrough
  - bootstrap 支持按环境切换 RuntimeClass

- [ ] `M1-SANDBOX-C`：Sandbox 会话 API 扩展
  - `/instances/{id}/sessions/{sid}/exec`：执行命令，流式返回输出
  - `/instances/{id}/sessions/{sid}/files/*`：文件读写（GET/PUT）
  - `/instances/{id}/sessions/{sid}/pause`：暂停（保存状态）
  - `/instances/{id}/sessions/{sid}/resume`：恢复
  - `/instances/{id}/sessions/{sid}/snapshot`：会话快照（P1）

### 模块 M1-NETWORK-SVC：网络服务层 API

**目标：** 将现有 KubeOVN 基础设施模板提升为完整的服务层 CRUD API。

**代码批次规划：**

- [ ] `M1-NETWORK-A`：VPC + 子网 CRUD API
  - `POST/GET/DELETE /api/v1/networks/vpcs`
  - `POST/GET/DELETE /api/v1/networks/subnets`
  - VPC 创建自动关联 KubeOVN subnet CRD，RLS 租户隔离

- [ ] `M1-NETWORK-B`：安全组 + 路由表 API
  - `CRUD /api/v1/networks/security-groups`（出入站规则管理）
  - `CRUD /api/v1/networks/route-tables`（静态路由）

- [ ] `M1-NETWORK-C`：负载均衡 API
  - `CRUD /api/v1/networks/load-balancers`
  - 四层 LB（TCP/UDP）+ 七层 LB（HTTP/HTTPS）
  - 证书管理（cert-manager 集成）

### 模块 M1-STORAGE-SVC：存储服务层 API

**目标：** 将现有存储基础设施提升为完整的服务层 CRUD API（块/对象/文件/向量四类型）。

**代码批次规划：**

- [ ] `M1-STORAGE-A`：块存储 + 对象存储服务层 API
  - `CRUD /api/v1/volumes`（含挂载/卸载/快照）
  - `CRUD /api/v1/objects/buckets`（含预签名 URL 生成）

- [ ] `M1-STORAGE-B`：文件存储 API（新存储类型）
  - `POST/GET/DELETE /api/v1/filesystems`
  - `CRUD /api/v1/filesystems/{id}/mount-targets`（VPC 内 NFS 挂载点）
  - `GET /api/v1/filesystems/{id}/usage`
  - 底层：Rook-CephFS subvolume 或 NFS 导出

- [ ] `M1-VSTORE-A`：向量存储 API（新 Core API 域）
  - `POST/DELETE /api/v1/vector-stores`（映射 Milvus Collection）
  - `POST /api/v1/vector-stores/{id}/search`（语义检索）
  - Milvus SDK 封装在 adapter，business layer 不直接调用

### 模块 M1-BM：裸金属实例

**目标：** 基于 Metal3 + Ironic，为高性能 AI 推理节点提供零虚拟化开销的裸金属实例。

**代码批次规划：**

- [ ] `M1-BM-A`：BM 硬件库存管理
  - Metal3 BareMetalHost CRD 部署
  - `GET /api/v1/baremetal/hosts`（硬件库存：CPU/内存/磁盘/NIC/GPU 信息）
  - `POST /api/v1/baremetal/hosts`（注册 BM 主机，提供 BMC 地址/MAC/凭据）
  - `POST /api/v1/baremetal/hosts/{id}/power`（BMC 电源操作）

- [ ] `M1-BM-B`：裸金属实例 OS 部署
  - `WorkloadRuntime` 新增 `kind=bare-metal`
  - 实例创建触发 Metal3 OS provisioning（PXE + cloud-init）
  - 生命周期：available → provisioning → running → deprovisioning

- [ ] `M1-BM-C`（P1）：BM 节点加入 K8s
  - Metal3 + Cluster API 将 BM 主机变成 K8s Worker Node
  - 支持 GPU 驱动自动安装（NVIDIA GPU Operator）
  - BM 节点可被 `gpu-inventory` 识别并参与 GPU 调度

### 模块 M1-K8S-CLUSTER：K8s 集群管理 API

**目标：** 为租户提供完整的 K8s 集群生命周期管理 + 原生 API 代理，对标 AWS EKS / Rancher。

**代码批次规划：**

- [ ] `M1-K8S-A`：vCluster 生命周期 API
  - `POST /api/v1/k8s-clusters`（创建 vCluster，指定 K8s 版本/节点池/网络）
  - `GET /api/v1/k8s-clusters/{id}`（集群状态/版本/节点数）
  - `PUT /api/v1/k8s-clusters/{id}`（升级 K8s 版本/调整节点池）
  - `DELETE /api/v1/k8s-clusters/{id}`
  - `GET /api/v1/k8s-clusters/{id}/kubeconfig`（ANI 鉴权的租户 kubeconfig）

- [ ] `M1-K8S-B`：原生 K8s API 代理
  - `ANY /api/v1/k8s-clusters/{id}/api/*`（透明代理到 vCluster API Server）
  - ANI JWT 验证 → 路由到对应 vCluster → 返回原生 K8s 响应
  - 支持 kubectl、Helm、Argo CD 等原生工具链
  - 可观测：代理请求记录审计日志

- [ ] `M1-K8S-C`：节点池管理
  - `CRUD /api/v1/k8s-clusters/{id}/node-pools`
  - 支持扩缩容、GPU 节点池

- [ ] `M1-K8S-D`（P1）：Karmada 多集群联邦
  - `POST /api/v1/k8s-federation`（注册联邦，Karmada 控制面）
  - `CRUD /api/v1/k8s-federation/{id}/propagation-policies`
  - 支持跨集群工作负载分发

### 模块 M1-PLATFORM-SVC：平台支撑服务 API

**目标：** 补齐 PaaS 服务凭据注入、内部服务发现、计量等平台级能力缺口。

**代码批次规划：**

- [ ] `M1-ENCRYPT-A`：国密加解密 API
  - `CRUD /api/v1/encryption/keys`（SM4 密钥管理，轮换/吊销）
  - `POST /api/v1/encryption/seal`（加密对象存储路径）
  - `POST /api/v1/encryption/unseal-token`（生成解密令牌，Init Container 使用）

- [ ] `M1-SECRETS-A`：密钥管理 API
  - `CRUD /api/v1/secrets`（KV 对，仅元数据可读，明文不返回）
  - `POST /api/v1/secrets/{id}/bindings`（绑定到实例，注入为环境变量/文件挂载）
  - 底层：K8s Secret，ANI RBAC 多租户隔离

- [ ] `M1-REGISTRY-A`：镜像仓库 API
  - `CRUD /api/v1/registry/repositories`
  - `GET /api/v1/registry/repositories/{repo}/tags`
  - `POST /api/v1/registry/repositories/{repo}/permissions`
  - `GET /api/v1/registry/images/{image}/scan-result`（Trivy 扫描结果）

- [ ] `M1-METER-A`：用量计量 API
  - `GET /api/v1/metering/usage`（按租户/时间段/资源类型）
  - `GET /api/v1/metering/usage/summary`（汇总报表）
  - `POST /api/v1/metering/events`（Services 层上报用量，如 Token 数）

- [ ] `M1-OBS-A`：可观测性 API
  - `GET /api/v1/observability/metrics`（PromQL 代理查询，不暴露 Prometheus 地址）
  - `CRUD /api/v1/observability/alarms`（告警规则管理）
  - `POST /api/v1/observability/alarms/{id}/actions`（触发/静默告警）

- [ ] `M1-SVC-EP-A`：服务目录 / 内部 DNS API
  - `CRUD /api/v1/service-endpoints`
  - Services 层注册 PaaS 服务的稳定内部域名（如 `postgres.prod.ani.internal`）
  - 底层：CoreDNS 自定义 zone 动态管理

- [ ] `M1-NOTIFY-A`：事件通知 API
  - `CRUD /api/v1/notifications/subscriptions`（订阅事件：webhook/email/内部消息）
  - `GET /api/v1/notifications/events`（通知历史查询）

### 模块 M1-DPU：DPU 加速节点纳管

**目标：** 基于 NVIDIA DPF 实现 DPU K8s 原生管理，为高性能 AI 推理节点提供网络/存储卸载能力。

**代码批次规划：**

- [ ] `M1-DPU-A`（P2）：DPU 库存与能力查询
  - `GET /api/v1/dpu-inventory/nodes`（DPU 装备节点列表，含型号/固件/卸载能力）
  - `GET /api/v1/dpu-inventory/availability`（可用 DPU 加速能力查询）
  - NVIDIA DPF Operator 部署，DPU 节点标签约定

- [ ] `M1-DPU-B`（P2）：实例 DPU 加速规格支持
  - 实例 spec 扩展 `acceleration.dpu.offloads`（network-sdn/storage-nvmeof/security）
  - Kata RuntimeClass 与 DPU-backed OVN 集成
  - BM + DPU 组合配置模板

---

## 九、Phase 1 非功能验收标准

| 指标 | 要求 | 验证方式 |
|---|---|---|
| API P99 延迟 | < 200ms（不含推理） | k6 压测 |
| 知识库问答端到端 | < 3s | 自动化测试 |
| 推理首 Token（TTFT） | < 2s（7B 模型，A100） | vLLM Benchmark |
| 故障自愈 | Pod 崩溃后 < 60s 恢复 | 手动 kill Pod 验证 |
| 通信安全 | 所有外部 API 强制 TLS 1.3 | SSL Labs 扫描 |
| 审计覆盖 | 100%（每次推理调用可追溯） | 随机抽样查审计日志 |
| 断网运行 | 完全断外网后所有功能正常 | 断网测试用例 |
| 首次部署 | 离线安装包 < 2 小时完成 | 全新环境演练 |
| 信创适配 | 统信 UOS 20 + ARM64 构建通过 | CI 多架构构建 |
| 多租户隔离 | 租户 A 无法访问租户 B 数据 | 渗透测试用例 |
