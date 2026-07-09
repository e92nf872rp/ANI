# PRD: K8s GPU 调度（HAMi + Volcano）v1.0.0 P0

> 版本：v1.0（P0 决策已收口）  
> 生成日期：2026-07-03  
> 确认选项：1C（Core+Console+BOSS）/ 2A（P0 最小闭环）/ 3C（NVIDIA P0，异构 P1）/ 4B（可配置队列）/ 5C（lab 门禁 + Console/BOSS 展示）  
> **已定（虚拟化）：** P0 先整卡 `nvidia.com/gpu` 打通链；P0 内必完成 HAMi `nvidia.com/vgpu` smoke；MIG 不纳入 P0（P1，依赖 A100/H100）  
> **已定（队列）：** P0 必须新增 Core OpenAPI 队列 CRUD；Console「设置 → GPU 调度队列」支持新建/编辑/删除（4B 完整）  
> **已定（BOSS aggregate）：** P0 接受临时方案—集群级 occupancy + 节点/异常；租户排行 Top N 占位，正式平台 aggregate API 放 P1（选项 1）  
> **已定（DCGM）：** P0 强制 `ANI_GPU_REQUIRE_DCGM_SERVICE=true`；lab 部署 DCGM Exporter；preflight 与 GPU 利用率展示一并验收（选项 B）  
> **已定（队列 RBAC）：** 租户管理员 `write`；项目成员 `read`；平台默认队列全员只读、BOSS/运维维护（选项 A）  
> 关联模块：Console [`gpu-management.md`](../console-modules/compute/gpu-management.md)、BOSS [`gpu-pool-management.md`](../boss-modules/ops/gpu-pool-management.md)  
> 文档包入口：本目录 [`README.md`](./README.md)  
> 下一步建议：`/prd-to-spec` → `/to-issues`

---

## 1. Introduction / Overview

ANI 需要在 Kubernetes 上交付 **可私有化部署的 GPU 算力调度能力**：底层由 **HAMi**（GPU 虚拟化/切分）与 **Volcano**（AI 批调度、队列、Gang Scheduling）支撑；产品层通过 **ANI Core** 统一抽象，租户与平台管理员通过 **Console / BOSS** 观察与管理 GPU 资源，**用户不应感知** HAMi、Volcano、device plugin 等底座组件。

本需求对标行业通用做法（阿里云 PAI 的 Quota/训推抢占、火山引擎 ML 平台的资源组/队列），但以 **专有云双控制台（Console 租户 + BOSS 平台）** 和 **更细粒度设备视图（节点/单卡）** 为 ANI 差异化表达。

### 当前项目现状

| 已有 | 尚未 |
|---|---|
| `GPUInventory` port、`M1-GPU-A` / `M1-INFRA-E/F` 部署契约 | HAMi、Volcano 真实 lab 安装 |
| OpenAPI 声明 `listGPUInventory`、`getGPUOccupancy` | 上述 handler 真正实现 |
| 真实 lab：NVIDIA driver + device plugin + GPU smoke（`M1-K8S-LIVE-K`） | vGPU 切片、Volcano 队列 smoke、**DCGM Exporter** |
| Console/BOSS GPU 模块主维护文档 | 页面可拉取真实 API 数据 |

### 产品三层模型（用户视角）

```text
┌─────────────────────────────────────────┐
│  Console / BOSS（用户看到的）              │
│  GPU 算力管理、资源池、创建 GPU 容器、队列设置 │
└─────────────────┬───────────────────────┘
                  │
┌─────────────────▼───────────────────────┐
│  ANI Core（前台翻译）                      │
│  GPUInventory、WorkloadRuntime、实例 API   │
└─────────────────┬───────────────────────┘
                  │
┌─────────────────▼───────────────────────┐
│  K8s + HAMi + Volcano（底座，用户不可见）   │
└─────────────────────────────────────────┘
```

---

## 2. Goals

- 在真实物理 lab 安装并验证 **HAMi 2.4+**、**Volcano 1.10+**，preflight Job 与 GPU smoke workload 全部通过
- Core 通过 `GPUInventory.PlanScheduling()` 将工作负载意图映射为 K8s 调度参数（`schedulerName=volcano`、queue、resourceName、nodeSelector 等）
- 支持 **平台默认队列 + 租户/项目级可配置 Volcano 队列**（权重、是否可回收、工作负载类型）；**P0 经 Core OpenAPI CRUD 暴露，Console 可自助配置**
- **P0 仅保证 NVIDIA** 全链路；昇腾 / 海光在 P1 占位，不阻塞 P0 验收
- Console 租户侧、BOSS 平台侧可展示 `listGPUInventory` / `getGPUOccupancy` 数据（只读）
- 创建 `gpu_container` 时，调度决策失败 **fail-closed** 拒绝创建，并返回可理解原因
- 不向 Services 或前端业务层暴露 HAMi SDK / Volcano CRD 细节
- **P0 强制 DCGM：** lab 部署 DCGM Exporter，preflight 启用 `ANI_GPU_REQUIRE_DCGM_SERVICE=true`
- **P0 GPU 虚拟化分两阶段验收：** ① 整卡 `nvidia.com/gpu` 打通 Volcano + `gpu_container` + Console/BOSS 页面；② 同里程碑内完成 HAMi `nvidia.com/vgpu` smoke（同卡多 Pod 切片）；**MIG 不纳入 P0**

---

## 3. 页面与信息架构（产品形态）

> 本节补足「页面长什么样」，便于产品、设计、开发建立共同画面。详细交互见后续 `/prd-to-ux`。

### 3.1 Console（租户视角）

```text
Console
├── 首页
│   └── GPU 利用率（摘要卡片）→ 跳转 GPU 算力管理
├── 算力与云资源
│   ├── GPU 算力管理          ← 主读页（inventory + occupancy）
│   ├── GPU 容器实例          ← 创建/使用 GPU（调度入口）
│   └── 批任务（P0 可选联动 Volcano 队列）
└── 设置
    └── GPU 调度队列          ← 4B：租户/项目队列配置
```

**GPU 算力管理（主页面）自上而下：**

```text
┌────────────────────────────────────────────────────────────┐
│ GPU 算力管理                          [刷新]  更新于 HH:MM  │
├────────────────────────────────────────────────────────────┤
│ [GPU总量] [已分配] [空闲] [平均利用率] [异常设备]  ← KPI 卡片 │
├────────────────────────────────────────────────────────────┤
│ GPU 型号分布（条形/饼图）                                    │
├────────────────────────────────────────────────────────────┤
│ Tab: [ 节点 ] [ 设备 ] [ 占用分布 ]                          │
│  节点/设备表：节点名、型号、状态、占用实例、利用率              │
│  点击占用实例 → 跳转 GPU 容器实例详情                         │
└────────────────────────────────────────────────────────────┘
```

**创建 GPU 容器（调度入口）：**

```text
名称、GPU 数量、型号偏好（可选）
GPU 分配模式：(•) 整卡（默认）  ( ) vGPU 切片（HAMi，P0 可选显式选择）
工作负载类型：( ) 推理  ( ) 训练     → 默认队列
调度队列：[ 项目-训练队 ▼ ]            → 4B 可配置
[ 创建 ]  失败时：GPU 不足 / 队列不存在 / 无兼容节点
```

**P0 默认：** 创建表单默认「整卡」；选 vGPU 时背后请求 `nvidia.com/vgpu`。用户界面不展示 HAMi/MIG 术语。

**P0 不做：** 页面上「分配 GPU」「回收 GPU」写操作按钮（待 Core 冻结写 API）。

**GPU 调度队列（设置页，P0 完整 CRUD）：**

```text
设置 → GPU 调度队列
┌────────────────────────────────────────────────────────────┐
│ GPU 调度队列                            [ + 新建队列 ]      │
├────────────────────────────────────────────────────────────┤
│ 平台默认（只读，不可删除）                                   │
│  • ani-inference   推理  weight 10  不可回收               │
│  • ani-training    训练  weight 5   可回收                 │
├────────────────────────────────────────────────────────────┤
│ 我的队列                                                    │
│  队列名          │ 类型 │ 权重 │ 可回收 │ 项目    │ 操作      │
│  proj-a-infer   │ 推理 │  12  │  否   │ proj-a │ 编辑 删除 │
└────────────────────────────────────────────────────────────┘
新建/编辑 Dialog：名称、workload_class、weight、reclaimable、project（可选）
→ 调用 POST/PATCH /api/v1/gpu-scheduling/queues（禁止 ConfigMap/CLI 作为 P0 主路径）
```

### 3.2 BOSS（平台视角）

```text
BOSS
└── 资源池与基础设施
    └── GPU 资源池管理    ← P0：全平台 KPI（集群级）+ 节点 + 异常
                            P1：租户排行 Top N（待平台 aggregate API）
```

与 Console 布局类似。**P0（已定）：** 展示集群级 occupancy 摘要、型号分布、节点列表、异常设备；**租户占用排行** 区块显示占位文案「租户维度排行待平台 API（P1）」，**不得**自造 aggregate 路径或前端循环调租户 API 冒充正式契约。

```text
┌────────────────────────────────────────────────────────────┐
│ GPU 资源池管理（全平台）                    [刷新]           │
├────────────────────────────────────────────────────────────┤
│ [GPU总量] [已分配] [空闲] [异常设备]  ← P0 集群级 KPI       │
├────────────────────────────────────────────────────────────┤
│ 型号分布 │ 节点列表 │ 异常设备列表                          │
├────────────────────────────────────────────────────────────┤
│ 租户占用排行 Top 10                                        │
│  ℹ 租户维度排行待平台 API（P1）                             │
│  （P0 不展示虚假排行数据）                                   │
└────────────────────────────────────────────────────────────┘
```

### 3.3 行业概念映射（便于对齐认知）

| ANI 产品概念 | 阿里云 PAI（近似） | 火山引擎 ML（近似） |
|---|---|---|
| BOSS GPU 资源池 | Quota 树 / 资源组总览 | 资源组 |
| Console GPU 队列设置 | 子级 Quota / 工作空间绑定 | 队列创建与管理 |
| 创建 GPU 容器选队列 | DLC/EAS 选 Quota | 提交任务选队列 ID |
| 推理优先于训练 | 训推一体抢占 | 非闲时任务优先于闲时任务 |
| GPU 算力管理只读页 | 配额与用量监控 | 队列剩余与设备视图 |

---

## 4. User Stories

### US-001: 真实 lab 部署 HAMi 与 Volcano

**Description:** 作为平台 SRE，我希望在三台物理 K8s lab 上安装 HAMi 与 Volcano，以便 GPU 虚拟化与批调度可在真实环境验证。

**Acceptance Criteria:**

- [ ] Volcano controller + scheduler 在 `volcano-system` 运行且 Ready
- [ ] HAMi 相关组件在 GPU 节点 Running
- [ ] GPU 节点上报 `nvidia.com/gpu` allocatable（整卡路径）
- [ ] 安装 HAMi 后节点上报 `nvidia.com/vgpu` allocatable（切片路径）
- [ ] **DCGM Exporter** 在 `ani-gpu-system`（或 deploy profile 约定 namespace）部署且 Service 可达
- [ ] `kubectl apply` `m1-infra-e` + `m1-infra-f` 后，preflight Job 退出码 0
- [ ] `ANI_GPU_REQUIRE_HAMI_ALLOCATABLE=true` 时 preflight 仍通过
- [ ] **`ANI_GPU_REQUIRE_DCGM_SERVICE=true` 时 preflight 仍通过**（P0 强制，已定）
- [ ] 部署记录写入 `repo/development-records/`，含版本号与 evidence JSON

---

### US-002: GPU scheduling preflight 与 smoke 门禁

**Description:** 作为 Core 开发者，我希望有固定命令验证 GPU 调度契约，以便 CI 与 lab 可重复验收。

**Acceptance Criteria:**

- [ ] `make validate-infra` 通过
- [ ] **smoke A（整卡）：** `schedulerName: volcano`，请求 `nvidia.com/gpu: 1`，Pod `Running`，容器内 `nvidia-smi` 成功
- [ ] **smoke B（vGPU）：** 请求 `nvidia.com/vgpu: 1`，Pod `Running`；同一张物理卡上 **至少 2 个** vGPU Pod 可同时 `Running`（HAMi 切片验收）
- [ ] preflight Job 在 P0 lab 默认启用 `ANI_GPU_REQUIRE_DCGM_SERVICE=true`（与 §10.4 一致）
- [ ] Makefile 暴露 GPU live gate target；`make test`、`make validate-architecture` 通过

---

### US-003: GPUInventory 调度决策（NVIDIA P0）

**Description:** 作为 Core API 调用方，我希望创建 `gpu_container` 时 Core 自动完成 GPU 调度规划。

**Acceptance Criteria:**

- [ ] `PlanScheduling()` 输出 `nodeSelector`、`tolerations`、`resourceName`、`schedulerName=volcano`、`queueName`
- [ ] **默认** `resourceName=nvidia.com/gpu`（整卡）；用户/实例显式选择 vGPU 模式时 `resourceName=nvidia.com/vgpu`
- [ ] P0 不生成 MIG 规格资源名；收到 MIG 请求返回明确「P1 未启用」
- [ ] 推理类默认高优先级队列；训练类默认可 reclaim 队列
- [ ] 无可用 GPU 时返回明确 `reasons[]`，创建返回 `422`（GPU 不可用语义）
- [ ] `WorkloadRuntime` adapter 写 manifest，不 import HAMi SDK
- [ ] 昇腾/海光请求 P0 返回明确不支持，不误调度到 NVIDIA 节点

---

### US-004: 可配置 Volcano 队列（Core OpenAPI CRUD，P0 必做）

**Description:** 作为租户管理员，我希望通过 Core API 为项目配置 GPU 调度队列与权重，以便训练与推理按业务优先级隔离。

**Acceptance Criteria:**

- [ ] 平台保留默认队列：`ani-inference`（weight=10, reclaimable=false）、`ani-training`（weight=5, reclaimable=true）；**不可经 API 删除**
- [ ] **P0 必须先改** `repo/api/openapi/v1.yaml`，新增队列资源与下列端点（实现前不得自造路径）：
  - `GET /api/v1/gpu-scheduling/queues` — 列表（租户 scoped）
  - `POST /api/v1/gpu-scheduling/queues` — 创建（`idempotency_key` 必填）
  - `GET /api/v1/gpu-scheduling/queues/{id}` — 详情
  - `PATCH /api/v1/gpu-scheduling/queues/{id}` — 更新
  - `DELETE /api/v1/gpu-scheduling/queues/{id}` — 删除（仅租户自定义队列；默认队列返回 `403` 或等价语义）
- [ ] 请求/响应字段至少含：`id`、`name`、`weight`、`reclaimable`、`workload_class`（inference/training/batch）、`project_id`（可选）、`created_at`、`updated_at`
- [ ] Core adapter 将 API 变更幂等映射为 Volcano `Queue` CRD
- [ ] 创建 `gpu_container` / `batch_job` 可指定 `queue_name`；未指定时用 workload_class 对应租户默认或平台默认队列
- [ ] 租户 A 不可读写租户 B 队列
- [ ] **RBAC（已定 §10.5）：** OpenAPI 冻结 `scope:gpu-scheduling:read`、`scope:gpu-scheduling:write`；**仅租户管理员** 持有 `write`；**项目成员** 仅 `read`
- [ ] 平台默认队列 `ani-inference` / `ani-training`：**全员只读**；租户侧 `PATCH/DELETE` 返回 `403`；**仅 BOSS/运维** 维护默认模板（P0 BOSS 只读全览）
- [ ] **P0 不以 ConfigMap + admin CLI 替代上述 API**（CLI 可作为运维补充，不是 Console 主路径）

---

### US-004a: Console GPU 调度队列页（租户管理员 CRUD）

**Description:** 作为**租户管理员**，我希望在 Console 自助管理 GPU 调度队列；**项目成员**仅查看与选用。

**Acceptance Criteria:**

- [ ] 「设置 → GPU 调度队列」调用 `GET/POST/PATCH/DELETE /api/v1/gpu-scheduling/queues`（不自造 API）
- [ ] **租户管理员**（`scope:gpu-scheduling:write`）：平台默认队列区块只读；「我的队列」支持新建、编辑、删除（删除前 `Popconfirm`）
- [ ] **项目成员**（仅 `scope:gpu-scheduling:read`）：可看队列列表与默认队列；**不显示**「+ 新建队列」及行内编辑/删除；`POST/PATCH/DELETE` 返回 `403` 时展示友好提示
- [ ] 新建/编辑表单字段与 OpenAPI schema 一致；提交成功 `Message.success` 并刷新列表
- [ ] 具备 loading / empty / error 三种 UI 状态
- [ ] 保存成功后，创建 GPU 容器表单的「调度队列」下拉可选项包含新队列（**项目成员**同样可选队列，但不能建队）

---

### US-005: listGPUInventory / getGPUOccupancy 实现

**Description:** 作为 Console / BOSS 前端，我希望通过 Core API 读取 GPU 设备清单与占用统计。

**Acceptance Criteria:**

- [ ] `GET /api/v1/gpu-inventory` 返回 `200 + GPUInventoryListResponse`，schema 符合 `v1.yaml`
- [ ] `GET /api/v1/gpu-inventory/occupancy` 返回 `200 + GPUOccupancyStats`
- [ ] `GPUInventoryRecord.status` 使用 OpenAPI 枚举：`available | in_use | fault | maintenance`
- [ ] Console JWT 仅本租户可见设备；RBAC：`scope:gpu-inventory:read`

---

### US-006: Console GPU 算力管理页展示

**Description:** 作为租户用户，我希望在 Console 查看 GPU 总量、占用与设备列表。

**Acceptance Criteria:**

- [ ] 调用 `listGPUInventory`、`getGPUOccupancy`（不自造路径）
- [ ] 首屏 KPI 与 `GPUOccupancyStats` 字段对齐
- [ ] **平均利用率** KPI 在 DCGM 就绪时展示真实百分比（经 `GET /api/v1/observability/query` PromQL 或 SPEC 冻结的等价只读源）；DCGM 未就绪时不得展示伪造数据，应显示明确「监控未就绪」状态
- [ ] 设备列表含 `node_name`、`gpu_type`、`gpu_index`、`status`、`instance_id`（若占用）
- [ ] 具备 loading / empty / error 三种 UI 状态
- [ ] 口径限定当前租户

---

### US-007: BOSS GPU 资源池页展示（P0 临时聚合）

**Description:** 作为平台管理员，我希望在 BOSS 查看全平台 GPU 占用摘要；租户维度排行待 P1 平台 API。

**Acceptance Criteria:**

- [ ] P0 展示 **集群级** occupancy 摘要（total / in_use / available / fault），数据来源为 Core 已声明接口的 **平台管理员可读范围** 或集群级 inventory 聚合（实现细节见 SPEC；**禁止**自造 OpenAPI 路径）
- [ ] P0 展示型号分布、节点列表、异常设备（可与租户 scoped `listGPUInventory` 的集群视图或等价只读源对齐，须在 SPEC 写明）
- [ ] **租户占用排行 Top N：** P0 仅展示占位区块 + 文案「租户维度排行待平台 API（P1）」；**不展示编造排行数据**
- [ ] P0 **禁止** 前端对多租户循环调用 `listGPUInventory` 作为正式 BOSS 契约
- [ ] 与 Console 租户视角差异在页面文案中明确
- [ ] 具备 loading / empty / error 三种 UI 状态

**P1（Out of Scope for P0）：** 正式平台 aggregate API（如 admin scope `listGPUInventory` / `getGPUOccupancy` 含 `by_tenant`）上线后，替换占位区块为真实排行。

---

### US-008: GPU 容器创建与调度原因回写

**Description:** 作为租户用户，我希望创建 GPU 容器失败时看到可理解的调度原因。

**Acceptance Criteria:**

- [ ] `POST /api/v1/instances`（`kind=gpu_container`）调度失败返回统一错误结构
- [ ] 成功实例 `InstanceRecord.gpu` 摘要含 GPU 数、resourceName、queue_name（若 schema 支持）
- [ ] `state_reason` 可承载 `InsufficientGPU`、`QueueNotFound`、`GPUNodeIncompatible`
- [ ] POST 携带 `idempotency_key`

---

### US-009: 昇腾 / 海光 P1 分期

**Description:** 作为产品负责人，我希望 P0 不阻塞于信创 GPU，但保留 P1 扩展路径。

**Acceptance Criteria:**

- [ ] P0 preflight 不强制 Ascend/DCU allocatable
- [ ] 对 huawei/hygon 请求返回明确 P1 未启用
- [ ] 引用 `M1-GPU-A`、`deploy/manifests/m1-gpu-a/` 异构契约文档

---

## 5. Functional Requirements

### 5.1 基础设施

- **FR-1:** deploy profile `gpu-scheduling.yaml` 声明 HAMi、Volcano、NVIDIA GPU Operator、DCGM 为 external provider
- **FR-2:** 提供 `m1-infra-e` manifest 与 `m1-infra-f` preflight Job
- **FR-3:** 真实 lab 执行 preflight + smoke 并归档 evidence

### 5.2 Core

- **FR-4:** 业务层仅通过 `GPUInventory`、`WorkloadRuntime` 表达调度意图
- **FR-5:** `gpu_container`、`batch_job` 使用 `schedulerName=volcano`
- **FR-6:** 平台默认队列 `ani-inference`、`ani-training` 必须存在
- **FR-7:** P0 必须在 `v1.yaml` 定义并实现 `/api/v1/gpu-scheduling/queues` CRUD；映射 Volcano `Queue` CRD；POST/PATCH/DELETE 支持 `idempotency_key`；RBAC 按 §10.5 强制执行
- **FR-8:** 调度决策失败时拒绝创建 GPU 工作负载
- **FR-9:** 实现 `listGPUInventory`、`getGPUOccupancy`
- **FR-10:** P0 保证 NVIDIA 整卡（`nvidia.com/gpu`）为默认路径，且同里程碑内完成 HAMi vGPU（`nvidia.com/vgpu`）smoke；MIG 不纳入 P0

### 5.3 Console / BOSS

- **FR-11:** Console GPU 页消费 Core `/api/v1/gpu-inventory*`；队列设置页消费 `/api/v1/gpu-scheduling/queues*`
- **FR-12:** BOSS GPU 池页 P0 展示集群级 occupancy、节点与异常设备；租户排行 P0 占位；**P0 不新增**平台 aggregate OpenAPI（P1）；不得自造契约或前端多租户循环聚合
- **FR-13:** 前端不传 `tenant_id`；上下文由 JWT 注入

### 5.4 观测

- **FR-14:** P0 部署 DCGM Exporter；暴露 `DCGM_FI_DEV_GPU_UTIL`、`DCGM_FI_DEV_FB_USED`；preflight **强制** `ANI_GPU_REQUIRE_DCGM_SERVICE=true`
- **FR-15:** `fault` 设备在 inventory 标记且不参与新调度

---

## 6. Non-Goals (Out of Scope)

- **NG-1:** P0 不实现昇腾、海光真实调度
- **NG-1a:** P0 不实现 NVIDIA MIG（P1；依赖 A100/H100 环境，当前 lab 为 RTX 4090 不适用 MIG）
- **NG-2:** 不实现 CAPK VM 内 GPU passthrough / vGPU
- **NG-3:** 不实现 Services 业务 API（models、inference-services 等）
- **NG-4:** 不向 Console/Services 暴露 Volcano Job CRD、HAMi 内部 API
- **NG-5:** P0 不做 GPU 分配/回收/释放 UI 写操作
- **NG-6:** 不自研 Gang Scheduling；委托 Volcano
- **NG-7:** 不修改 `repo/api/openapi/services/v1.yaml`
- **NG-8:** 不在 Gateway handler 内安装 HAMi/Volcano
- **NG-9:** P0 不实现 BOSS 租户维度 aggregate API 与真实租户排行（P1）
- **NG-10:** P0 不实现**项目级**队列 RBAC（项目管理员仅管本项目队列）；放 P1

---

## 7. Design Considerations

### 7.1 队列可配置规则（4B）

- 平台默认队列不可删除
- 租户队列命名建议：`{tenant_slug}-{project}-{class}`，避免集群级冲突
- 推理 workload 默认绑定不可 reclaim 队列；训练 workload 默认可 reclaim
- Console：**设置 → GPU 调度队列** — 租户管理员 P0 完整 CRUD（US-004a）；项目成员只读
- BOSS：**平台默认队列模板** 只读全览 + 运维维护（不进租户 Console 写默认队）

### 7.2 P0 GPU 虚拟化策略（已定）

| 阶段 | 资源名 | 用途 | P0 验收 |
|---|---|---|---|
| **P0-① 默认路径** | `nvidia.com/gpu` | 整卡调度；打通 Volcano、实例创建、inventory 页面 | smoke A 必过 |
| **P0-② HAMi 切片** | `nvidia.com/vgpu` | HAMi 虚拟化；提高利用率、多租户共享 | smoke B 必过（同卡 ≥2 Pod） |
| **P1** | MIG 规格 | 仅 A100/H100 硬件隔离切分 | 不纳入本 PRD P0 |

**产品默认：** 创建 `gpu_container` 默认整卡；可选切换 vGPU。界面不出现 HAMi、MIG、device plugin 字样。

**与当前 lab：** 三台物理机为 RTX 4090，支持整卡 + HAMi vGPU；**不支持 MIG**。

### 7.3 Console vs BOSS

| 维度 | Console | BOSS |
|---|---|---|
| 范围 | 当前租户 | 全平台 |
| 租户排行 | 无 | P0 占位；P1 正式 aggregate API |
| 队列配置 | 租户管理员 write；项目成员 read | 默认模板只读全览；运维维护 |
| 主维护 doc | `console-modules/compute/gpu-management.md` | `boss-modules/ops/gpu-pool-management.md` |

---

## 8. Technical Considerations

### 8.1 架构边界（强制）

- HAMi = 默认 GPU 虚拟化 adapter，不是 OpenAPI 资源
- Volcano = 默认批调度 adapter，不是任务系统 API
- K8s `resources.requests/limits` 仍是调度边界
- POST 创建与有副作用 PUT/PATCH 必须支持 `idempotency_key`

### 8.2 P0 必须新增的 Core OpenAPI

| 能力 | 路径 | P0 |
|---|---|---|
| **队列配置 CRUD** | `GET/POST /api/v1/gpu-scheduling/queues`、`GET/PATCH/DELETE /api/v1/gpu-scheduling/queues/{id}` | **必做**（已定）；先于 handler 改 `v1.yaml` |
| **BOSS 平台 aggregate** | 如 `GET /api/v1/admin/gpu-inventory/occupancy` 或 platform scope | **P1**（已定）；P0 不新增 |

**实施顺序（强制）：** ① 改 `repo/api/openapi/v1.yaml` → ② handler/adapter → ③ Console 队列页 → ④ lab 验证 Volcano Queue 同步。

### 8.3 验收命令（P0）

```bash
cd repo
make validate-infra
make validate-real-k8s-profile
kubectl apply -f deploy/manifests/m1-infra-e
kubectl apply -f deploy/manifests/m1-infra-f
kubectl -n ani-system logs job/ani-gpu-e2e-preflight
make test
make validate-architecture
```

### 8.4 组件版本基线

| 组件 | 版本 |
|---|---|
| HAMi | 2.4+ |
| Volcano | 1.10+ |
| Kubernetes | v1.36.1（当前 lab） |

---

## 9. Success Metrics

| 指标 | 目标 |
|---|---|
| 真实 lab preflight | 100% 通过（含 `ANI_GPU_REQUIRE_HAMI_ALLOCATABLE=true` 与 **`ANI_GPU_REQUIRE_DCGM_SERVICE=true`**） |
| GPU smoke A（整卡） | Volcano + `nvidia.com/gpu` + `nvidia-smi` 成功 |
| GPU smoke B（vGPU） | Volcano + `nvidia.com/vgpu` + 同卡 ≥2 Pod Running |
| API | `listGPUInventory` / `getGPUOccupancy` / **队列 CRUD** 可用 |
| 调度失败 | 100% 可解释 error code |
| Console / BOSS | Console occupancy + **利用率（DCGM）** 可展示；BOSS 集群级 KPI + 占位排行，三态 UI 可验证 |
| 架构 | 无 HAMi/Volcano SDK 泄漏到 Gateway/Services |

---

## 10. 已定决策

### 10.1 GPU 虚拟化（2026-07-03 确认）

- **P0-①：** 先支持整卡 `nvidia.com/gpu`，打通 Volcano + `gpu_container` + Console/BOSS 页面
- **P0-②：** 同里程碑内必须完成 HAMi `nvidia.com/vgpu` smoke（含同卡多 Pod）
- **MIG：** 不纳入 P0；放 P1，且依赖 A100/H100 环境

### 10.2 队列 CRUD（2026-07-03 确认，选项 2）

- **P0 必须** 在 Core OpenAPI 新增 `/api/v1/gpu-scheduling/queues` 完整 CRUD（先改 `v1.yaml`，再实现 handler）
- **Console**「设置 → GPU 调度队列」P0 支持新建/编辑/删除租户自定义队列
- **平台默认队列** `ani-inference` / `ani-training` 只读，不可 API 删除
- **P0 不以** ConfigMap + admin CLI 作为租户配置主路径（运维 CLI 仅作补充）

### 10.3 BOSS 平台 aggregate（2026-07-03 确认，选项 1）

- **P0 接受临时方案：** BOSS 展示 **集群级** occupancy、型号分布、节点列表、异常设备
- **P0 租户排行 Top N：** 仅占位 + 文案「租户维度排行待平台 API（P1）」；不展示虚假数据
- **P0 禁止：** 前端多租户循环调 `listGPUInventory` 冒充正式 BOSS 契约；禁止自造 aggregate OpenAPI 路径
- **P1：** 新增正式平台 aggregate API，上线真实租户排行

### 10.4 DCGM strict（2026-07-03 确认，选项 B）

- **P0 强制** preflight 启用 `ANI_GPU_REQUIRE_DCGM_SERVICE=true`；DCGM Exporter Service 未就绪则 **P0 门禁失败**
- **P0 lab 必须部署** DCGM Exporter（`deploy/profile` 约定 namespace，与 `m1-infra-e` 契约一致）
- **Console GPU 算力管理**「平均利用率」在 P0 必须基于 DCGM 指标（经 `observability/query` 或 SPEC 冻结数据源）；禁止展示无依据的利用率数字
- **指标基线：** `DCGM_FI_DEV_GPU_UTIL`、`DCGM_FI_DEV_FB_USED`（PromQL 模板在 SPEC 冻结，PRD 不写死未声明 metric 名以外的字段）

### 10.5 队列 RBAC（2026-07-03 确认，选项 A）

| 角色 | Scope | Console 能力 |
|---|---|---|
| **租户管理员** | `scope:gpu-scheduling:read` + `scope:gpu-scheduling:write` | CRUD「我的队列」；默认队列只读 |
| **项目成员** | `scope:gpu-scheduling:read` | 查看队列列表；创建 GPU 容器/批任务时**选用**队列；无新建/编辑/删除 |
| **BOSS / 平台运维** | 平台角色（SPEC 冻结） | 维护平台默认队列模板；P0 BOSS **只读**全平台队列列表 |
| **平台默认队列** | — | `ani-inference` / `ani-training` 租户侧 **只读**；不可 `PATCH/DELETE` |

**P1：** 项目级分权（项目管理员仅 CRUD 绑定 `project_id` 的队列）。

## 11. Open Questions

*P0 范围相关 Open Questions 已全部收口（§10.1–§10.5）。后续变更走 PRD 修订或 P1 附录。*

---

## 12. ANI Boundaries

| Item | Value |
|---|---|
| **Product line** | core + console + boss |
| **Code scope** | Core: `pkg/ports/gpu_inventory.go`、`pkg/adapters/`、`deploy/manifests/m1-infra-e/`、`m1-infra-f/`、`api/openapi/v1.yaml`；Console: `repo/frontends/console/`（若外部团队实施则以 spec 为准）；BOSS: 平台 GPU 页 |
| **OpenAPI authority** | 消费：`listGPUInventory`、`getGPUOccupancy`；**P0 必须新增**：`/api/v1/gpu-scheduling/queues` CRUD；RBAC：`scope:gpu-scheduling:read` / `scope:gpu-scheduling:write`（§10.5） |
| **Frozen exclusions** | 不修改 Services OpenAPI；不开发 `repo/services/model-service/` 等业务；Gateway 不内嵌插件安装 |
| **idempotency_key** | `POST /api/v1/instances`（gpu_container）、`POST /api/v1/gpu-scheduling/queues`、`PATCH/DELETE` 队列 |
| **Module main doc** | Console: `repo/services/docs/console-modules/compute/gpu-management.md`；BOSS: `repo/services/docs/boss-modules/ops/gpu-pool-management.md` |

---

## 13. 文档关系

| 文档 | 路径 | 职责 |
|---|---|---|
| 本 PRD | `repo/services/docs/gpu-hami/prd-k8s-gpu-hami-volcano-scheduling.md` | 需求与范围（本文件） |
| 文档包索引 | `repo/services/docs/gpu-hami/README.md` | 研发阅读顺序与速览 |
| Console 主维护 | `../console-modules/compute/gpu-management.md` | 页面职责与字段口径 |
| BOSS 主维护 | `../boss-modules/ops/gpu-pool-management.md` | 平台页职责 |
| UX · Console | `repo/services/docs/gpu-hami/ux-console-gpu-scheduling.md` | Console 页面交互说明 |
| UX · BOSS | `repo/services/docs/gpu-hami/ux-boss-gpu-pool.md` | BOSS 页面交互说明 |
| SPEC（待生成） | `tasks/modules/spec/...` | 技术实现 |

---

## 变更记录

| 日期 | 说明 |
|---|---|
| 2026-07-03 | 初稿：基于会话确认选项 1C/2A/3C/4B/5C，补充 §3 页面形态与行业映射 |
| 2026-07-03 | 已定 §10.1：P0 整卡默认 + HAMi vGPU smoke 必过；MIG → P1 |
| 2026-07-03 | 已定 §10.2：P0 队列 OpenAPI CRUD 必做 + Console 自助配置（选项 2） |
| 2026-07-03 | 已定 §10.3：BOSS P0 集群级视图 + 租户排行占位；aggregate API → P1（选项 1） |
| 2026-07-03 | 已定 §10.4：P0 强制 DCGM strict + 利用率 KPI 验收（选项 B） |
| 2026-07-03 | 已定 §10.5：队列 RBAC 方案 A；P0 Open Questions 全部收口 |
| 2026-07-03 | 已生成 Console/BOSS UX 交互说明（§13 链接） |
| 2026-07-03 | 文档包迁至 `repo/services/docs/gpu-hami/`（研发交付） |
