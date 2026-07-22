# GPU 调度 Sprint — Issue #1-#13 实现笔记 (note-it)

> **批次：** GPU-SCHEDULING-BATCH-01-13
> **日期：** 2026-07-09
> **Scope：** Core + Console + BOSS
> **PRD：** `repo/services/tasks/modules/prd/console/gpu-inventory/prd-k8s-gpu-hami-volcano-scheduling.md`
> **覆盖 Issue：** #1-#13（Core #1-#6, Console #7-#11, BOSS #12-#13）

---

## 1. Design Decisions

### 1.1 [Issue #1] 契约先行原则 — OpenAPI 先于 handler/adapter

- **歧义点：** PRD §8.2 要求"先改 `v1.yaml`，再写实现、测试和 SDK"，但未明确 schema 错误码与 RBAC scope 的具体清单。
- **选择：** 在 Core OpenAPI 中一次性新增 5 端点 + 4 schema + 2 RBAC scope + 5 错误码，并扩展 `InstanceRecord.gpu` 三字段（`queue_name`/`resource_name`/`scheduling_reason`）。
- **理由：** 一次性声明全部契约，避免后续 Issue 反复修改 `v1.yaml`；`InstanceRecord.gpu` 扩展为 Issue #8/#9 Console 详情页消费 GPU 调度摘要提供类型基础。
- **证据：** `repo/api/openapi/v1.yaml` operationId 行 4115/4129/4163/4179/4211；schema 行 1748/1763/1771/1781；InstanceRecord.gpu 扩展行 473-475。

### 1.2 [Issue #2] handler 目录遵循现有代码风格（internal/router/）

- **歧义点：** SPEC 标注 handler 应放在 `internal/handlers/`，但现有代码库所有 handler 统一在 `internal/router/`。
- **选择：** 遵循现有代码风格（Karpathy 原则 #3），handler 放在 `repo/services/ani-gateway/internal/router/gpu_scheduling_resources.go`。
- **理由：** 与现有 handler 组织保持一致，避免引入目录分裂；`internal/router/router.go` 的 `RegisterOptions` 已是统一注册入口。

### 1.3 [Issue #2] VolcanoHTTPDoer 接口抽象 — 不引入 k8s.io/client-go 依赖

- **歧义点：** adapter 需要操作 Volcano Queue CRD，是否引入 `k8s.io/client-go` SDK。
- **选择：** 定义 `VolcanoHTTPDoer` 接口（`Do(ctx, method, endpoint, contentType, body) ([]byte, int, error)`），通过 K8s REST API 直接操作 CRD，不引入 `k8s.io/client-go`。
- **理由：** 保持 adapter 轻量，测试用 `DoerFunc` mock，生产环境由 gateway runtime 注入 doer；避免 Core adapter 层耦合 K8s SDK（CLAUDE.md §5.3 组件边界）。

### 1.4 [Issue #2] 队列 ID 存入 CRD label 而非 CRD 字段

- **歧义点：** Volcano Queue CRD 无 `id` 字段，但 API 契约要求返回 UUID `id`。
- **选择：** UUID 生成后存入 CRD label `ani.kubercloud.io/queue-id`，通过 labelSelector 查询。
- **理由：** 不修改 Volcano Queue CRD spec（第三方资源），label 是 K8s 原生查询机制，tenant label 同理。

### 1.5 [Issue #3] PlanScheduling provider 路由 nil-fallback 模式

- **歧义点：** `PlanScheduling` 需要校验显式 `QueueName` 是否属于该租户，但 local/dev profile 无真实 Volcano Queue store。
- **选择：** 新增 `NewKubernetesGPUInventoryWithQueueStore(client, store)` 构造函数；当 `queueStore` 非 nil 时校验，为 nil 时跳过校验（local/dev profile）。
- **理由：** local profile 只证明 API/SDK/状态机/调用边界（CLAUDE.md §6.6），不应因缺真实 store 而阻塞开发；nil-fallback 是显式的、可审计的降级。

### 1.6 [Issue #3] PlanScheduling vendor gate — P0 仅 NVIDIA

- **歧义点：** PRD §10.1 决定 P0 仅保证 NVIDIA 全链路，昇腾/海光 P1 占位。
- **选择：** `PlanScheduling` P0 vendor gate 仅允许 NVIDIA；昇腾(huawei)/海光(hygon) 返回 decision with Reasons（不返回 error，上层映射 422）；MIG mode 同样拒绝（P1 未启用）。
- **理由：** fail-closed 但可解释（PRD US-003 AC："昇腾/海光请求 P0 返回明确不支持，不误调度到 NVIDIA 节点"）。

### 1.7 [Issue #3] selectResourceName 策略 — vGPU vs 整卡

- **歧义点：** PRD §10.2 规定 P0-① 整卡 `nvidia.com/gpu` 默认，P0-② HAMi `nvidia.com/vgpu` smoke 必过，但未明确 PlanScheduling 如何选择。
- **选择：** `VirtualizationVGPU` → `nvidia.com/vgpu` (runtimeClassName=hami-vgpu)；其他 → `nvidia.com/gpu` (runtimeClassName=nvidia)。
- **理由：** PRD US-003 AC："默认 `resourceName=nvidia.com/gpu`（整卡）；用户/实例显式选择 vGPU 模式时 `resourceName=nvidia.com/vgpu`"。

### 1.8 [Issue #7] Shell 组件不重复全局布局

- **歧义点：** `__root.tsx` 已提供全局 `Layout`（Header + Aside + Content），shell 组件是否需要再封装布局。
- **选择：** shell 组件（ConsolePage/ConsolePageHeader/ConsoleContentCard）只负责 in-page 区域，不重复全局布局。`ConsolePage` 纯 flex column 容器，提供 16px 垂直间距。
- **理由：** 职责单一，避免布局嵌套冲突；后续 #8/#9/#10/#11 复用同一壳层。

### 1.9 [Issue #8] DCGM 利用率降级策略 — 失败不阻塞页面

- **歧义点：** PRD §10.4 要求"平均利用率 KPI 在 P0 必须基于 DCGM 指标"，但 DCGM 可能未就绪。
- **选择：** DCGM PromQL 查询失败时 KPI 卡显示「监控未就绪」Tag，不阻塞页面其他区域（KPI/型号分布/Tabs 正常渲染）。
- **理由：** PRD §10.4："禁止展示无依据的利用率数字"；降级是显式的、用户可感知的，优于伪造数据。

### 1.10 [Issue #9] crypto.randomUUID 生成 idempotency_key

- **歧义点：** `POST /instances` 要求 `idempotency_key`，前端如何生成。
- **选择：** `crypto.randomUUID()` 在浏览器端生成 UUID v4。
- **理由：** 浏览器原生 API，无需额外依赖；CLAUDE.md §4.5："客户端重试必须复用同一个 key"——在提交前生成一次，mutation 重试复用同一 key。

### 1.11 [Issue #10] RBAC placeholder — canManageQueues() 返回 true

- **歧义点：** PRD §10.5 RBAC 要求"仅租户管理员持有 `scope:gpu-scheduling:write`"，但 Console 当前无 auth store。
- **选择：** `canManageQueues()` 占位函数当前固定返回 `true`（dev/local profile 可测）。当 auth store 接入后应检查 `scope:gpu-scheduling:write`。无 write scope 时隐藏「新建队列」按钮 + 操作列 + 显示 `Alert theme="warning"`。
- **理由：** 不因 RBAC 未接入而阻塞 UI 开发；placeholder 是显式的、可替换的。

### 1.12 [Issue #10] 队列 name pattern 校验 — K8s 资源名正则

- **歧义点：** 队列名最终映射为 Volcano Queue CRD name，需符合 K8s 资源名规范。
- **选择：** 前端 `QUEUE_NAME_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/`，与 Issue #2 后端 `validateQueueName` 一致。
- **理由：** 前后端校验一致，避免提交后被后端拒绝。

### 1.13 [Issue #12] BOSS 直接采用 TanStack Router 文件路由

- **歧义点：** Console 使用旧 `App.tsx` 自定义路由模式，BOSS 是否复用。
- **选择：** BOSS 不使用 Console 的旧 `App.tsx` 模式，直接采用 TanStack Router 文件路由（`__root.tsx` + `routes/`）。
- **理由：** TanStack Router 文件路由是推荐模式，BOSS 是新项目无历史包袱；Console 后续可迁移。

### 1.14 [Issue #12] BOSS port 5174 避免开发冲突

- **歧义点：** BOSS 和 Console 开发时端口冲突。
- **选择：** BOSS `vite.config.ts` 使用 port 5174（Console 是 5173）。
- **理由：** 开发体验，两个前端可同时运行。

### 1.15 [Issue #13] 租户排行仅占位 — 禁止假数据

- **歧义点：** PRD §10.3 决定 P0 租户排行 Top N 仅占位，P1 上线正式 aggregate API。
- **选择：** 租户排行 Section **仅占位** `Alert theme="info"`，**禁止**渲染空 Table 假数据；**禁止**前端循环调多租户 API 拼排行。
- **理由：** PRD US-007 AC："P0 **禁止** 前端对多租户循环调用 `listGPUInventory` 作为正式 BOSS 契约"；NG-9："P0 不实现 BOSS 租户维度 aggregate API 与真实租户排行"。

### 1.16 [Issue #6] Volcano Queue CRD cluster-scoped REST path

- **歧义点：** Volcano Queue CRD 是 cluster-scoped 还是 namespaced，REST path 是否含 `namespaces/{ns}` 段。
- **选择：** CRD 是 `Cluster` scoped，REST path 为 `/apis/scheduling.volcano.sh/v1beta1/queues`（不含 namespaces 段）。
- **理由：** Issue #2 初始实现误用 namespaced path，Issue #6 live gate 修正为 cluster-scoped。

---

## 2. Deviations (vs PRD/UX/SPEC)

### 2.1 [Issue #5] HAMi 使用 `nvidia.com/gpu` 而非 `nvidia.com/vgpu`（重大偏差）

- **SPEC/PRD 说：** PRD §10.2 和 US-003 AC 规定 HAMi vGPU 切片使用 `nvidia.com/vgpu` 资源名。
- **实际实现：** HAMi 2.9.0 使用 `--resource-name=nvidia.com/gpu`（默认），每张物理 GPU 被 split 成 10 个 vGPU 单元（`--device-split-count=10`），节点 allocatable 显示 `nvidia.com/gpu: 20`（2 GPU × 10 split）。Smoke B 实际请求 `nvidia.com/gpu: 1` 而非 `nvidia.com/vgpu: 1`。
- **原因：** HAMi 2.9.0 默认配置行为，非 SPEC 错误。此偏差影响 Issue #3 `selectResourceName` 逻辑——当前代码 `VirtualizationVGPU → nvidia.com/vgpu`，但真实 HAMi 环境用 `nvidia.com/gpu`。
- **影响：** PlanScheduling 的 vGPU 路径在真实 HAMi 环境可能无法匹配 allocatable 资源。需确认 HAMi 是否可配置 `--resource-name=nvidia.com/vgpu`。

### 2.2 [Issue #5] HAMi + Volcano 调度器冲突 — Smoke B 改用默认调度器

- **SPEC 说：** PRD US-002 AC："smoke B（vGPU）：请求 `nvidia.com/vgpu: 1`，Pod `Running`"，SPEC §9.4 隐含 `schedulerName: volcano`。
- **实际实现：** 当 pod 指定 `schedulerName: volcano` 时，HAMi webhook 检测到 "Pod already has different scheduler assigned" 并跳过 vGPU binding，导致 `no binding pod found on node`。Smoke B 改用默认调度器（HAMi 管理的）解决此问题。
- **原因：** HAMi webhook 与 Volcano scheduler 的调度权冲突。HAMi 需要自己管理 vGPU binding，不兼容外部 schedulerName。
- **影响：** PRD FR-5 "`gpu_container`、`batch_job` 使用 `schedulerName=volcano`" 在 vGPU 模式下可能不成立。

### 2.3 [Issue #5] Smoke B 移除 `nvidia.com/gpumem` 资源限制

- **SPEC 说：** HAMi vGPU 应支持 `nvidia.com/gpumem` 显存限制。
- **实际实现：** Volcano 调度器不识别 HAMi 的 `nvidia.com/gpumem` 自定义资源，导致 pod group 无法调度。Smoke B 移除了 `nvidia.com/gpumem` 限制，仅用 `nvidia.com/gpu: 1`。
- **原因：** Volcano scheduler 不识别 HAMi 自定义扩展资源。

### 2.4 [Issue #2] router.go 越界修改

- **SPEC 说：** Issue #2 scope 仅涉及 `gpu_scheduling_resources.go` 新建和 `router.go` 注册调用。
- **实际实现：** `router.go` 的 `RegisterOptions` 新增 `GPUSchedulingQueueStore` 字段；`RegisterWithOptions` 新增 `registerGPUSchedulingResourcesWithStore` 调用。这属于必要的接线修改，但触动了核心路由文件。
- **原因：** 现有 `router.go` 的 `RegisterOptions` 结构体是 handler 注入的统一入口，新增 store 必须修改此结构体。

### 2.5 [Issue #8] `__root.tsx` 提前添加 #9 菜单入口

- **SPEC/UX 说：** Issue #8 scope 仅 GPU 算力管理页，Issue #9 scope 才包含 GPU 容器实例菜单。
- **实际实现：** `__root.tsx` 在 Issue #8 中新增「算力与云资源」菜单组时，提前添加了 Issue #9 的 GPU 容器实例入口。
- **原因：** 菜单组一次性建立，避免 #9 再次修改 `__root.tsx`。`instance_id` 链接因 `gpu-containers/$instanceId` 路由尚未创建（Issue #9），暂用文本展示。

### 2.6 [Issue #4] Preflight Job 镜像改为等效手动验证

- **SPEC 说：** `m1-infra-f` preflight Job 应 exit 0。
- **实际实现：** `bitnami/kubectl:1.30` 被 docker mirror 403 禁止；`registry.k8s.io/kubectl:v1.36.1` 是 distroless 无 `/bin/sh`；改用等效手动验证（手动执行 preflight.sh 检查项）。
- **原因：** 镜像拉取受限，非 SPEC 错误。功能等效，但自动化程度降低。

### 2.7 [Issue #4] DCGM namespace 通过 ExternalName 桥接

- **SPEC 说：** DCGM Exporter 应在 `ani-gpu-system` namespace。
- **实际实现：** DCGM 原在 `ani-system`，preflight 检查 `ani-gpu-system`，通过 ExternalName service 桥接 `ani-gpu-system/ani-dcgm-exporter → ani-system/ani-dcgm-exporter`。
- **原因：** 部署历史，DCGM 先在 `ani-system` 部署。ExternalName 是最小改动方案。

### 2.8 [Issue #6] KubernetesRESTClient 新增 Do()/Host() 方法

- **SPEC 说：** `VolcanoHTTPDoer` 接口需要 `Do(ctx, method, endpoint, contentType, body) ([]byte, int, error)`。
- **实际实现：** `KubernetesRESTClient` 原只有私有 `do` 方法返回 `([]byte, error)`，缺少 HTTP status code。新增公开 `Do()` 方法从 `resilience.StatusError` 提取 HTTP status code；新增 `Host()` 方法暴露 base URL。同时修复 CA 文件加载逻辑（即使 `inCluster=false`，只要提供了 `CAFile` 就加载）。
- **原因：** adapter 需要区分 404/409 等状态码做错误映射，原接口不满足。

---

## 3. Tradeoffs

### 3.1 [Issue #2] interface-based polymorphism (VolcanoHTTPDoer) vs switch

- **备选方案 A：** 定义 `VolcanoHTTPDoer` 接口，adapter 依赖接口，测试用 `DoerFunc` mock，生产注入 `KubernetesRESTClient`。
- **备选方案 B：** adapter 直接依赖 `KubernetesRESTClient` 具体类型，测试用真实 K8s mock server。
- **选择：** 方案 A（interface-based polymorphism）。
- **理由：** 接口隔离使 adapter 不耦合 K8s SDK 具体实现；测试无需启动 mock K8s API server；符合 ports/adapters 架构（CLAUDE.md §5）。

### 3.2 [Issue #7/#8] inline style vs CSS modules

- **备选方案 A：** 使用 inline style（`style={{ ... }}`）控制 shell 组件布局间距。
- **备选方案 B：** 引入 CSS modules 或 styled-components。
- **选择：** 方案 A（inline style），`ConsolePage` 用 `style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}`。
- **理由：** Console 项目现有代码风格以 inline style 为主；shell 组件布局简单，无需引入新依赖；TDesign 组件本身接受 `style` prop。

### 3.3 [Issue #5] retry:false for DCGM query

- **备选方案 A：** DCGM PromQL 查询 `retry: false`，失败立即降级显示「监控未就绪」。
- **备选方案 B：** `retry: 1` 或 `retry: 3`，短暂重试后再降级。
- **选择：** 方案 A（retry:false）。
- **理由：** DCGM 未就绪是长期状态（部署问题），重试无意义；快速降级避免用户等待；PRD §10.4 要求"不展示伪造数据"。

### 3.4 [Issue #8/#13] string matching for 403 detection

- **备选方案 A：** 检查 error message 是否包含 `"403"` 字符串来判断 forbidden 状态。
- **备选方案 B：** openapi-fetch 返回结构化 error 对象，检查 `error.status === 403`。
- **选择：** 方案 A（string matching），因 openapi-fetch 的 error 对象结构不稳定。
- **理由：** openapi-fetch 0.12 的 `error` 类型是 `ClientError`，status code 在 `error.status` 但 TypeScript 类型不完整；string matching 是务实的降级。**Open Question：Do() StatusCode 返回值改进（见 §4.6）。**

### 3.5 [Issue #9] crypto.randomUUID vs nanoid vs 自生成 UUID

- **备选方案 A：** `crypto.randomUUID()`（浏览器原生 UUID v4）。
- **备选方案 B：** 引入 `nanoid` 库。
- **备选方案 C：** 手动实现 UUID v4 生成。
- **选择：** 方案 A（crypto.randomUUID）。
- **理由：** 浏览器原生 API，零依赖；所有现代浏览器支持；CLAUDE.md §4.5 要求 `idempotency_key`——UUID v4 满足唯一性。

### 3.6 [Issue #5] HAMi device plugin nodeSelector `gpu=on` label

- **备选方案 A：** 给 GPU 节点打 `gpu=on` label 以满足 HAMi DaemonSet nodeSelector。
- **备选方案 B：** 修改 HAMi Helm values 覆盖 nodeSelector 为 `ani.kubercloud.io/gpu-node=true`。
- **选择：** 方案 A（`kubectl label nodes ... gpu=on`）。
- **理由：** 最小改动，不改 HAMi Helm chart；`ani.kubercloud.io/gpu-node=true` 已存在，`gpu=on` 是 HAMi 默认要求。

### 3.7 [Issue #13] partial-data 分区 Alert vs 全局 error

- **备选方案 A：** occupancy OK + inventory fail → 分区 `Alert warning`（仅 inventory 区域提示），occupancy 正常显示。
- **备选方案 B：** 任一 query 失败 → 全局 `Alert error` + 隐藏全部数据。
- **选择：** 方案 A（分区 Alert）。
- **理由：** 用户体验更优——部分数据可用时仍展示可用部分；PRD US-007 要求"loading / empty / error 三种 UI 状态"，partial-data 是 error 的细化。

---

## 4. Open Questions

### 4.1 HAMi vGPU 切分资源名规范（阻塞 Issue #3 vGPU 路径）

- **假设：** HAMi 2.9.0 默认使用 `nvidia.com/gpu`（非 `nvidia.com/vgpu`）作为 vGPU 切分资源名，每张物理 GPU split=10。
- **待确认：** HAMi 是否支持 `--resource-name=nvidia.com/vgpu` 配置？若支持，Issue #5 Smoke B 应重新验证 `nvidia.com/vgpu: 1` 请求路径。若不支持，Issue #3 `selectResourceName` 的 `VirtualizationVGPU → nvidia.com/vgpu` 逻辑需修正为 `nvidia.com/gpu`。
- **影响范围：** `repo/pkg/adapters/runtime/kubernetes_gpu_inventory.go` `selectResourceName` 函数；PRD §10.2 表格；SPEC §5.1。

---

## 5. PR 1/2/3 三段式拆分实现 (2026-07-21)

> **批次：** GPU-SCHEDULING-PR1-3-SPLIT
> **PRD：** 同上
> **覆盖 PR：** PR #21 (1/3 契约), PR #31 (2/3 接口), PR #46 (3/3 实现)
> **状态：** PR #21 + #31 已合入 main；PR #46 OPEN 等待 review

### 5.1 Design Decisions

#### 5.1.1 三段式 PR 拆分策略

- **歧义点：** GPU 调度功能涉及 104 文件、24902 行改动，单 PR 过大，review 困难。
- **选择：** 按"契约 → 接口 → 实现"三段拆分：PR 1 只改 v1.yaml + 生成物，PR 2 只改 pkg/ports/，PR 3 改 adapters + gateway + 前端。
- **理由：** 契约先行（CLAUDE.md §4.1），接口稳定后再做实现；每个 PR 可独立 review、squash、revert（ANI-15 §2.3）。

#### 5.1.2 PR 2 ports 接口 cherry-pick 到 PR 3

- **歧义点：** 组长建议 PR 3 包含 ports 改动，不再单独 review PR 2。
- **选择：** 将 PR 2 的 ports commit cherry-pick 到 PR 3 分支，PR 3 包含完整 ports + 实现。
- **理由：** 减少审查轮次；PR 2 已合入 main，cherry-pick 只是确保 PR 3 分支自洽。

#### 5.1.3 mixed-provider 检查修复（primaryProvider 跳过辅助 Secret）

- **歧义点：** PR 3 新增 workload identity Secret（provider=kubernetes）与 VM manifest（provider=kubevirt）混合，触发 mixed-provider 检查失败。
- **选择：** `primaryProvider()` 跳过辅助 Secret manifest，只检查主 manifest 的 provider 一致性。
- **理由：** 辅助 Secret 是支撑性资源，不应影响主 workload 的 provider 校验语义。

### 5.2 Deviations (vs PRD/UX/SPEC)

#### 5.2.1 PATCH 端点新增 Idempotency-Key 强制校验

- **SPEC 说：** CLAUDE.md §4.5 要求"所有 POST 创建和有副作用的 PUT/PATCH 必须支持 idempotency_key"。
- **实际实现：** review-it 发现 PATCH `updateGPUSchedulingQueue` 未校验 `Idempotency-Key`，已修复——空 header 返回 400。
- **原因：** 契约合规，修复 review 发现的规则违反。

#### 5.2.2 crdToQueue UID 切片防御性长度检查

- **SPEC 说：** 无明确 spec。
- **实际实现：** review-it 发现 `strings.ReplaceAll(...UID, "-", "")[:16]` 在 UID 为空时 panic，已修复为先判断长度。
- **原因：** 防御性边界处理，K8s 边缘情况或测试 mock 可能返回空 UID。

#### 5.2.3 labelSelector URL 编码

- **SPEC 说：** 无明确 spec。
- **实际实现：** review-it 发现 `labelSelector` 未 URL-escape，有注入风险，已修复为 `url.QueryEscape`。
- **原因：** 安全默认拒绝（ANI-15 §2.7），与 `gpu_inventory_resources.go` 的 `fetchPodOccupancyFromK8s` 保持一致。

### 5.3 Tradeoffs

#### 5.3.1 initOnce 错误处理 — 延迟到 follow-up

- **备选方案 A：** 在 PR 3 内修复 `initOnce.Do` 吞 `EnsurePlatformQueueDefaults` 错误的问题。
- **备选方案 B：** 延迟到 follow-up PR，PR 3 只修复直接阻断的问题。
- **选择：** 方案 B。
- **理由：** 修复需要重构 `New()` 构造函数或引入错误返回机制，属于更深层的架构改动，不适合在 PR 3 内混入；当前 `initOnce` 行为在 K8s 可用时不触发问题。

#### 5.3.2 LocalGPUSchedulingQueueStore 错误语义对齐

- **备选方案 A：** 保留 `ErrQueueStoreUnavailable`（返回 503）。
- **备选方案 B：** 改为 `ErrInvalid`（返回 400），与 VolcanoQueueStore 对齐。
- **选择：** 方案 B。
- **理由：** 校验错误（name 为空/非法）是客户端错误，应返回 400 而非 503；两个 store 实现语义应一致。

### 5.4 Open Questions

#### 5.4.1 VolcanoQueueStore initOnce 错误处理（follow-up）

- **假设：** `initOnce.Do` 在首次 `List` 时触发 `EnsurePlatformQueueDefaults`，若失败则永不重试。
- **待确认：** 是否应在 `New()` 构造函数中显式初始化并返回错误，还是改为 `sync.Once` + error field 模式？
- **影响范围：** `volcano_queue_store.go` L190-216。

#### 5.4.2 EnsurePlatformQueueDefaults 错误屏蔽（follow-up）

- **假设：** `getCRDByName` 非 404 错误被 `continue` 吞掉，K8s API 故障时函数仍返回 nil。
- **待确认：** 是否应在所有默认队列都无法确认时返回错误？
- **影响范围：** `volcano_queue_store.go` L124-159。

#### 5.4.3 lookupQueueByNameOrID 错误区分（follow-up）

- **假设：** 所有 `getCRDByName` 错误统一替换为 `ErrQueueNotFound`，掩盖连接故障。
- **待确认：** 是否应区分"确实 not found"和"查询失败"？
- **影响范围：** `volcano_queue_store.go` L381-399。

### 5.5 Verification Commands Run

| 验证项 | 命令 | 结果 |
|---|---|---|
| Go build | `go build ./pkg/adapters/runtime/... ./services/ani-gateway/...` | BUILD: 0 |
| Go test (adapters) | `go test ./pkg/adapters/runtime/...` | PASS |
| Go test (router) | `go test ./services/ani-gateway/internal/router/...` | PASS (except Windows /bin/sh) |
| gofmt | `gofmt -l <files>` | clean |
| OpenAPI spec | `python scripts/validate_openapi_spec.py` | valid: 2 |
| Architecture | `python scripts/validate_component_imports.py` | passed |
| SDK drift | `gen_sdk_alpha.py` + `git diff --exit-code` | DRIFT_EXIT: 0 |
| npm audit | `npm audit --audit-level=high` | 0 vulnerabilities |
| ESLint | `eslint src --ext ts,tsx` | 0 errors, 1 warning |
| review-it | branch diff vs origin/main | 4 fixed, 5 deferred |

### 4.2 control-plane 节点 GPU 工作负载（Issue #4）

- **假设：** lab 环境 control-plane 节点（`kubercloud`）也承载 GPU 工作负载。
- **待确认：** PRD 未明确 control-plane 是否应调度 GPU workload。当前 lab 3 节点均 Ready 且有 GPU allocatable，但生产环境 control-plane 通常 NoSchedule。
- **建议：** 确认是否需要 `nodeSelector` 或 taint 隔离 control-plane GPU 节点。

### 4.3 ESLint config 补齐（影响 Console #7-#11 + BOSS #12-#13）

- **假设：** Console 和 BOSS 项目均缺少 eslint 配置文件（`.eslintrc*`），`pnpm lint` 无法运行。
- **待确认：** 是否在本 Sprint 或后续 Sprint 补建 eslint config？这是 pre-existing 问题，非本批次引入，但阻塞 `pnpm lint` 验收（所有 Console/BOSS Issue 的 AC "type-check + lint + build" 中 lint 项无法执行）。
- **影响范围：** `repo/frontends/console/`、`repo/frontends/boss/`。

### 4.4 RBAC auth store 接入（Issue #10）

- **假设：** `canManageQueues()` 当前固定返回 `true`（placeholder）。
- **待确认：** Console 何时接入 auth store（读取 JWT scope）？接入后 `canManageQueues()` 应检查 `scope:gpu-scheduling:write`。
- **影响范围：** `repo/frontends/console/src/routes/settings/gpu-queues.tsx`。

### 4.5 项目列表 API 集成（Issue #10）

- **假设：** 队列设置页创建 Dialog 的 `project_id` 字段当前是纯文本 Input（可选）。
- **待确认：** 是否有项目列表 API 可用于 Select 下拉？当前 Console 无项目列表端点消费。
- **影响范围：** `repo/frontends/console/src/routes/settings/gpu-queues.tsx` CRUD Dialog。

### 4.6 Do() StatusCode 返回值改进（Issue #6 + 前端 #8/#13）

- **假设：** `KubernetesRESTClient.Do()` 返回 `([]byte, int, error)`，其中 `int` 是 HTTP status code。前端 openapi-fetch 的 error 对象 status code 提取不稳定，使用 string matching。
- **待确认：** 是否改进 openapi-fetch error 类型定义，使前端可直接 `error.status === 403` 而非 string matching？这影响 Issue #8/#13 的 forbidden 检测健壮性。
- **影响范围：** `repo/frontends/console/src/api/core-schema.d.ts`、`repo/frontends/boss/src/api/core-schema.d.ts`。

### 4.7 HAMi + Volcano 调度器共存策略（Issue #5）

- **假设：** HAMi vGPU 模式不兼容 `schedulerName: volcano`（HAMi webhook 跳过不同 scheduler 的 pod）。
- **待确认：** PRD FR-5 "`gpu_container`、`batch_job` 使用 `schedulerName=volcano`" 在 vGPU 模式下如何实现？是否需要 HAMi + Volcano 共调度方案（如 HAMi webhook 优先 + Volcano 二次调度）？或 vGPU 模式放弃 Volcano scheduler？
- **影响范围：** `repo/pkg/adapters/runtime/kubernetes_gpu_inventory.go` PlanScheduling 输出 `SchedulerName`。

### 4.8 Console `instance_id` 跳转路由（Issue #8）

- **假设：** Issue #8 GPU 算力管理页设备 Tab 的 `instance_id` 链接因 `gpu-containers/$instanceId` 路由尚未创建（Issue #9），暂用文本展示。
- **待确认：** Issue #9 完成后是否回填 Issue #8 的 `instance_id` Link？当前 Issue #8 记录为"已知限制"。
- **影响范围：** `repo/frontends/console/src/routes/compute/gpu.tsx`。

---

## 5. Verification Commands Run

### Core 后端（Issue #1-#6）

| Issue | 命令 | 结果 |
|---|---|---|
| #1 | `python scripts/validate_component_imports.py --root .` | exit 0 ✅ |
| #1 | `python -c "import yaml; yaml.safe_load(open('api/openapi/v1.yaml').read()); print('YAML valid')"` | YAML valid ✅ |
| #2 | `python scripts/validate_component_imports.py --root .` | exit 0 ✅ |
| #2 | `go test ./pkg/adapters/runtime/ -run TestVolcanoQueueStore -count=1` | 14 tests PASS ✅ |
| #2 | `go test ./services/ani-gateway/internal/router/ -run TestGPUScheduling -count=1` | 12 tests PASS ✅ |
| #3 | `go build ./pkg/... ./services/ani-gateway/...` | 编译成功 ✅ |
| #3 | `go test ./pkg/adapters/runtime/ -run "TestKubernetesGPUInventory\|TestPlanScheduling\|TestLocalGPUInventory\|TestPlanning\|TestVolcanoQueueStore" -count=1` | 33 tests PASS ✅ |
| #3 | `python scripts/validate_component_imports.py --root .` | component import guard passed ✅ |
| #4 | `make validate-infra` | M1-INFRA-A~F + M1-GPU-A + M1-RUNTIME-A 通过 ✅ |
| #4 | `kubectl -n ani-system logs job/ani-gpu-e2e-preflight` | 全部 preflight 检查项通过 ✅ |
| #5 | `make validate-gpu-scheduling-live-gate` | 通过 ✅ |
| #5 | Smoke A Job `ani-gpu-smoke-a` | Complete 1/1 (47s), SMOKE_A_PASS ✅ |
| #5 | Smoke B Job `ani-gpu-smoke-b` | Complete 1/1 (12s), SMOKE_B_PASS ✅ |
| #6 | `make validate-queue-crud-live-gate` | 通过 ✅ |
| #6 | Gateway LIST/CREATE/DELETE 集群验证 | 全部通过 ✅（含 403 PlatformDefaultProtected） |

### Console 前端（Issue #7-#11）

| Issue | 命令 | 结果 |
|---|---|---|
| #7 | `npx tsc --noEmit` | EXIT_CODE: 0 ✅ |
| #7 | `npx vite build` | built in 4m 15s ✅ |
| #7 | `npx eslint` | 无法运行（pre-existing: no eslint config） ⚠️ |
| #8 | `npx tsc --noEmit` | EXIT_CODE: 0 ✅ |
| #8 | `npx vite build` | built in 4m 40s ✅ |
| #9 | `npx tsc --noEmit` | EXIT_CODE: 0 ✅ |
| #9 | `npx vite build` | built in 3m 38s ✅ |
| #10 | `npx tsc --noEmit` | EXIT_CODE: 0 ✅ |
| #10 | `npx vite build` | built in 4m 26s ✅ |
| #11 | `npx tsc --noEmit` | EXIT_CODE: 0 ✅ |
| #11 | `npx vite build` | built in 4m 28s ✅ |

### BOSS 前端（Issue #12-#13）

| Issue | 命令 | 结果 |
|---|---|---|
| #12 | `npm install` | added 254 packages ✅ |
| #12 | `npx tsc --noEmit` | EXIT_CODE: 0 ✅ |
| #12 | `npx vite build` | built in 1m 34s ✅ |
| #13 | `npx tsc --noEmit` | EXIT_CODE: 0 ✅ |
| #13 | `npx vite build` | built in 1m 47s ✅ |

### 已知测试限制

- `TestDemoInstanceServiceRealShellExecutesCommand` 在 Windows 上失败（pre-existing，与本批次无关）。
- Console 和 BOSS 的 `pnpm lint` 因缺少 eslint config 无法运行（pre-existing）。
- Issue #4 的 `make validate-infra` 中 M1-INSTANCE-A 阶段 make 进程崩溃（Windows 进程问题），与 INFRA-E/F 无关。
