# ANI · 当前冲刺上手指南

> 新开发者（人类或 AI 工具）的第一个入口文件。本文只描述当前真实执行状态；历史完成批次查 `repo/development-records/README.md`。

> **仓库范围：ANI Core + 受控 Services PR。** ANI Core 继续负责基础设施平台底座；Services 受控并行 PR 阶段已经启动，不再按旧冻结规则处理。Services PR 统一运行 `make validate-services`，覆盖 CODEOWNERS 共同审查要求之外的 API split、Services boundary gate、OpenAPI/Gateway route contract、语义契约、生成物漂移、模块检查和 `make validate-architecture`。
> **当前重心：Sprint 13 / Core real provider 与 live gate 收敛。** Core Sprint 13/14 既有事实继续有效：Sprint 12 已完成 Core「Services 支撑 Handler」A/B1/B2/B3 全部 19 个 handler + 2 个 422 的 Tier1 local profile 收口；Sprint 13 S01-S07 production-shaped live gate 事实保留；Sprint14 resilience 结论仅限隔离 fixture。未跑通对应 live gate 前，不得标记 real-provider、runtime ready 或 production ready。Services PR 可在主责目录推进业务实现，但不得绕过 Core OpenAPI REST API / Core SDK、Core review 或现有架构门禁。
> **标准状态 marker：** 真实服务器只读验证已完成；Rook-Ceph 正式部署已完成。Sprint 11 执行环境：正式部署执行环境。

> **Sprint 13（当前活跃冲刺，2026-06-19 起）：** Core real provider 与 live gate 收敛。前置 Sprint 12 已闭合 19 个 Core handler + 2 个 422；Sprint 13 不重写 Core handler，不把 Services 业务资源回流 Core API，而是在既有 `pkg/ports` / `pkg/adapters` / Gateway handler 边界接入真实组件，并形成可复跑 live gate 与 evidence JSON。历史冻结原因和历史结论仍保留在旧批次记录中，但不是当前 PR 规则。计划见 [`development-records/sprint13-real-provider-readiness-plan.md`](development-records/sprint13-real-provider-readiness-plan.md)。

> **Sprint 14 计划与分支状态：** Sprint 14 Core 韧性与服务语义计划见 [`development-records/sprint14-core-resilience-plan.md`](development-records/sprint14-core-resilience-plan.md)（限流/幂等重放/超时/readyz/重试断路/降级/failover）。配套交付 Services 的前端加速设计：[`development-records/frontend-acceleration-design-for-services.md`](development-records/frontend-acceleration-design-for-services.md)。当前主线入口仍保留 Sprint 13 production-shaped 边界；`feature/sprint14-core-resilience-semantics` 已完成 Sprint14 aggregate live gate，待 PR/评审后再进入主线状态。
> **Sprint 14 分支执行记录：** `feature/sprint14-core-resilience-semantics` 已完成 R-P0-0 gateway shared store 前置批次、R-P0-1 gateway rate limit、R-P0-2 gateway idempotency replay、R-P0-3 adapter per-call timeout、R-P0-4 data-plane readyz health、R-P1-5 retry/circuit-breaker foundation、R-P1-6 resilience degradation 与 R-P2-7 multi-endpoint failover config，见 [`development-records/r-p0-0-gateway-shared-store.md`](development-records/r-p0-0-gateway-shared-store.md)、[`development-records/r-p0-1-gateway-rate-limit.md`](development-records/r-p0-1-gateway-rate-limit.md)、[`development-records/r-p0-2-gateway-idempotency-replay.md`](development-records/r-p0-2-gateway-idempotency-replay.md)、[`development-records/r-p0-3-adapter-resilience-timeout.md`](development-records/r-p0-3-adapter-resilience-timeout.md)、[`development-records/r-p0-4-readyz-dataplane-health.md`](development-records/r-p0-4-readyz-dataplane-health.md)、[`development-records/r-p1-5-retry-circuit-breaker.md`](development-records/r-p1-5-retry-circuit-breaker.md)、[`development-records/r-p1-6-resilience-degradation.md`](development-records/r-p1-6-resilience-degradation.md)、[`development-records/r-p2-7-multi-endpoint-failover-config.md`](development-records/r-p2-7-multi-endpoint-failover-config.md)。R-P0-0..R-P2-7 单批次仍保持 local/logic verified 边界；其生产就绪结论由 `SPRINT14-CORE-RESILIENCE-LIVE-GATE` / `validate-sprint14-resilience-live-gate` / Sprint14 resilience live gate 补齐：已在 `ani-sprint14-resilience` 隔离 namespace 真实执行 P0 strong backend kill、P1 weak dependency degraded、P2 controller primary kill / follower failover，并归档脱敏 evidence。该 production-ready 范围仅限隔离 Sprint14 Core resilience fixture；不把现有 Sprint13 单副本后端或 full platform 标为 production ready。

## 当前冲刺

| 字段 | 值 |
|---|---|
| **冲刺编号** | Sprint 13（Core real provider 与 live gate 收敛） |
| **主题** | 将 Sprint 12 已闭合的 Core handler/ports/local adapters 接到真实组件，并建立可复跑 live gate 与 evidence JSON |
| **当前状态** | Sprint 12 已完成 19 个 Core handler + 2 个 422 的 Tier1 local profile；Sprint 13 S01-S07 均已归档 production_shape.status=passed evidence；历史 LIVE PENDING token 仅作门禁兼容语境 |
| **生产化边界** | Sprint 13 只达到 production-shaped acceptance passed；不等于 full platform production ready。正式镜像发布/升级、长期 SLA/soak、备份/恢复和故障注入仍需后续 release gate |
| **Auth 边界** | SPRINT13-AUTH-DEX-PRODUCTION-GATE / Auth/Dex production gate 已通过；production-shaped Gateway 固定 ANI_AUTH_MODE=auth_service |
| **执行入口** | `development-records/sprint13-real-provider-readiness-plan.md`、`development-records/README.md`、本文件验收命令 |
| **执行环境** | 真实 provider 写操作前必须重新只读盘点并取得人工确认；evidence 不得包含凭据、服务器 IP 或完整内网端点 |
| **最后校准日期** | 2026-07-15 |

## Sprint 13 当前任务

| 切片 | 状态 | 证据 / gate |
|---|---|---|
| S01 网络路由 Kube-OVN | production-shaped gate passed | `sprint13-netroute-kubeovn-live-result.md`；`validate-sprint13-b-track-production-shape` |
| S02 K8s workloads vCluster | production-shaped gate passed | `sprint13-k8s-workloads-vcluster-live-result.md`；metadata target TLS proof |
| S03 storage Rook-Ceph | production-shaped gate passed | `sprint13-storage-rook-ceph-live-result.md`；snapshot/mount-target proof |
| S04 GPU NVIDIA device-plugin/DCGM | production-shaped gate passed | `sprint13-gpu-inventory-dcgm-live-result.md`；DCGM metrics proof |
| S05 object-store MinIO | production-shaped gate passed | `SPRINT13-OBJECTSTORE-MINIO-A-TRACK`；`validate-object-store-live-gate`；pre-signed URL；LIVE PENDING 仅作历史兼容 |
| S06 vector Milvus | production-shaped gate passed | `SPRINT13-VECTOR-MILVUS-A-TRACK`；`validate-vector-store-live-gate`；LIVE PENDING 仅作历史兼容 |
| S07 instance observability Prometheus | production-shaped gate passed | `SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-A-TRACK`；`validate-instance-observability-live-gate`；Prometheus + kubelet；LIVE PENDING 仅作历史兼容 |

闭环规则：每个 provider slice 必须具备 real adapter/provider runtime、live gate、非敏感 evidence JSON、development record 和全局 production-shape guard。S05-S07 B 轨可以继续 作为历史兼容 token 保留；截至 2026-06-21，S05/S06/S07 均已 passed。

## 账密登录模块（2026-07）

> 独立于 Sprint 13/14 的账密登录功能开发流。覆盖 Core Auth API（租户账密 + 平台账密）、Console 前端（OIDC + 账密 Tab）、BOSS 前端（平台账密登录）。

| Issue | 描述 | 状态 | 证据 |
|---|---|---|---|
| Core #001 | 平台用户迁移（users 表扩展 tenant_id NULLABLE） | ✅ 已完成 | `development-records/auth-login-core-001.md` |
| Core #002 | 租户账密登录 API | ✅ 已完成 | `development-records/auth-login-core-001.md` |
| Core #003 | 平台账密登录 API | ✅ 已完成 | `development-records/auth-login-core-001.md` |
| Console #004 | Console P0 OIDC 登录 | ✅ 已完成 | `development-records/auth-login-console-004.md` |
| Console #005 | Console P1 账密 Tab | ✅ 已完成 | `development-records/auth-login-console-004.md` |
| BOSS #006 | BOSS P1 账密登录 | ✅ 已完成 | `development-records/auth-login-boss-006.md` |
| BOSS #006 | BOSS P1 OIDC 登录 | ⏸ 暂不实现 | auth-service Begin 方法需扩展平台路径 |

**代码审查修复（review-it）：** P0-1 签发顺序、P0-3 BOSS redirect_uri、P1-1 SQL 约束、P1-2 maybeRefresh、P1-3 401 先 refresh、P1-5 幂等键、P2-1 RBAC scope。测试验证：auth-service PASS、ani-gateway middleware PASS、BOSS vite build PASS。

**PRD/SPEC 文档体系：** 已按产品线拆分为 Console/BOSS/Core 三份 PRD 和三份 SPEC，分别放置在 `prd/{console,boss,core}/login/` 和 `spec/{console,boss,core}/login/` 目录。

## GPU 调度功能流（skill 流水线推进中，2026-07）

> 独立于 Sprint 13/14 real provider 收敛的 GPU 调度功能开发流，通过 `/prd-to-spec` → `/to-issues` → `/goal` skill 流水线推进。共 13 个 Issue，覆盖 Core OpenAPI 契约、Queue adapter/handler、Console 前端组件和 BOSS 前端页面。

| Issue | 描述 | 状态 | 证据 |
|---|---|---|---|
| #1 | OpenAPI 新增队列 CRUD + InstanceGPU 扩展 | ✅ 已完成 | `development-records/gpu-scheduling-issue-01-openapi-queue-crud.md`；8 项 AC 全部验证通过 |
| #2 | Core Queue adapter + handler | ✅ 已完成 | `development-records/gpu-scheduling-issue-02-queue-adapter-handler.md`；9 项 AC 全部验证通过；26 个单测通过 |
| #3 | Plan/scheduling extend | ✅ 已完成 | `development-records/gpu-scheduling-issue-03-plan-scheduling-extend.md`；10 项 AC 全部验证通过；13 个新单测通过 |
| #4 | Lab HAMi/Volcano/DCGM | ✅ 已完成 | `development-records/gpu-scheduling-issue-04-lab-hami-volcano-dcgm.md`；9 项 AC 全部验证通过；Volcano 1.15.0 + HAMi 2.9.0 + DCGM 在 3 节点集群部署成功 |
| #5 | GPU smoke live gate | ✅ 已完成 | `development-records/gpu-scheduling-issue-05-gpu-smoke-live-gate.md`；4 项 AC 全部验证通过；Smoke A (volcano+整卡) + Smoke B (HAMi vGPU) 均调度成功 |
| #6 | Queue CRUD live gate | ✅ 已完成 | `development-records/gpu-scheduling-issue-06-queue-crud-live-gate.md`；7 项 AC 全部验证通过；5 端点通过真实 Volcano CRD 验证 + 平台默认 403 + 跨租户 404 |
| #7 | Console Shell 组件 | ✅ 已完成 | `development-records/gpu-scheduling-issue-07-console-shell-components.md`；6 项 AC 全部验证通过；tsc + vite build 通过 |
| #8 | Console GPU 算力管理页 | ✅ 已完成 | `development-records/gpu-scheduling-issue-08-console-gpu-management-page.md`；12 项 AC 全部验证通过；tsc + vite build 通过 |
| #9 | Console GPU 容器实例 | ✅ 已完成 | `development-records/gpu-scheduling-issue-09-console-gpu-container-instance.md`；14 项 AC 全部验证通过；tsc + vite build 通过 |
| #10 | Console 队列设置页 | ✅ 已完成 | `development-records/gpu-scheduling-issue-10-console-queue-settings-page.md`；14 项 AC 全部验证通过；tsc + vite build 通过 |
| #11 | Console 概览 GPU 卡片 | ✅ 已完成 | `development-records/gpu-scheduling-issue-11-console-overview-gpu-card.md`；8 项 AC 全部验证通过；tsc + vite build 通过 |
| #12 | BOSS 前端骨架 | ✅ 已完成 | `development-records/gpu-scheduling-issue-12-boss-frontend-skeleton.md`；10 项 AC 全部验证通过；tsc + vite build 通过 |
| #13 | BOSS GPU 资源池页 | ✅ 已完成 | `development-records/gpu-scheduling-issue-13-boss-gpu-pool-page.md`；16 项 AC 全部验证通过；tsc + vite build 通过 |

### GPU 调度三段式 PR 拆分（2026-07-21）

| PR | 内容 | 状态 | 说明 |
|---|---|---|---|
| PR #21 (1/3) | v1.yaml 契约 + SDK/API docs/TS schema 生成物 | ✅ 已合入 main | `feat/core): add GPU scheduling queue CRUD contract to v1.yaml` |
| PR #31 (2/3) | pkg/ports 接口（GPUSchedulingQueueStore + GPUInventory 扩展） | ✅ 已合入 main | `feat(core): add GPU scheduling queue interface to pkg/ports` |
| PR #46 (3/3) | adapters + gateway + 前端 + manifests 实现 | 🟡 OPEN 等待 review | review-it 修复 4 项（UID panic/PATCH 幂等/URL 编码/错误语义）；5 项 follow-up 延迟；笔记 `gpu-scheduling-batch-01-13-note-it.md §5` |

Issue 清单：`repo/services/tasks/issues/issue-01-openapi-queue-crud.md` ~ `issue-13-boss-gpu-pool-page.md`

## Registry Console Flow（2026-07-22）

| 批次 | 状态 | 说明 |
|---|---|---|
| CORE-REGISTRY-CONSOLE-FLOW-CONTRACT-A | 契约/Console schema 已完成 | 按 7.22 原型“暂不考虑 BOSS 和权限”边界，Core v1 新增 `RegistryImage.purpose`、`/registry/images?purpose=`、四类算力引用 enum 与 createInstance 镜像门禁 422 语义；仅契约，不含 handler/adapter/Console 页面实现 |
| CORE-REGISTRY-CONSOLE-FLOW-CORE-A | Core 镜像仓库后端实现已完成 | RegistryImage purpose 贯通 port/adapter/router，`/registry/images?purpose=` 支持过滤；不含 instances、Console、BOSS 或权限实现 |


## Sprint 13 执行矩阵

| 候选切片 | 真实组件方向 | 代码边界 | 当前状态 |
|---|---|---|---|
| 实例观测 | Prometheus + kubelet / K8s API（已选 2026-06-19） | `ports.InstanceObservability`，Gateway handler 不绕过 port | **production-shaped gate passed**（`SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-A-TRACK`；B 轨 live result `sprint13-instance-observability-prometheus-live-result.md`；evidence：`live-evidence/sprint13-instance-observability-prometheus-live-evidence.json`；readiness：`sprint13-instance-observability-prometheus-readiness.md`；gate：`validate-instance-observability-live-gate`；历史 LIVE PENDING token 仅作门禁兼容语境；不代表 full platform production ready） |
| GPU 清单/占用 | NVIDIA device-plugin / DCGM / node labels | `ports.GPUInventory`，复用 Sprint 5 GPU evidence 作为前置事实 | **production-shaped gate passed**（A 轨 `SPRINT13-GPU-INVENTORY-DCGM-A-TRACK`；B 轨 live result `sprint13-gpu-inventory-dcgm-live-result.md`；Gateway/bootstrap `GPU_INVENTORY_PROVIDER=kubernetes_rest` 均支持 in-cluster ServiceAccount；readiness：`sprint13-gpu-inventory-dcgm-readiness.md`；gate：`validate-gpu-inventory-live-gate --production-shaped`；production guard：`validate-sprint13-b-track-production-shape`；evidence：`development-records/live-evidence/sprint13-gpu-inventory-dcgm-live-evidence.json`；不代表 full platform production ready） |
| Sandbox templates | Kata / runtimeClass / template catalog | `ports.SandboxTemplateCatalog` | 待拆分执行 |
| 网络路由 | Kube-OVN | `ports.NetworkService` / `runtime.NetworkService` / `network_resources.go` / `pkg/bootstrap/deps.go` / Gateway network runtime | **production-shaped gate passed**（Gateway `POST/GET /networks/routes` create/list + in-cluster ServiceAccount/RBAC + Kube-OVN bottom observation 已通过；production guard：`validate-sprint13-b-track-production-shape`；evidence：`development-records/live-evidence/sprint13-netroute-kubeovn-live-evidence.json`；result：`sprint13-netroute-kubeovn-live-result.md`；不代表 full platform production ready） |
| 卷快照与 mount-targets | Rook-Ceph RBD / CSI snapshot / NFS 或等价 filesystem backend | `ports.StorageService` / `runtime.LocalStorageService` / `storage_resources.go` / `pkg/bootstrap/deps.go` / `storage_runtime.go` | **production-shaped gate passed**（A 轨 `SPRINT13-STORAGE-ROOK-CEPH-A-TRACK`；Gateway/bootstrap `STORAGE_PROVIDER=kubernetes_rest` 均支持 in-cluster ServiceAccount；gate：`validate-storage-live-gate --production-shaped`；production guard：`validate-sprint13-b-track-production-shape`；live result：`sprint13-storage-rook-ceph-live-result.md`；evidence：`development-records/live-evidence/sprint13-storage-rook-ceph-live-evidence.json`；不代表 full platform production ready） |
| K8s workloads | vCluster / Kubernetes API | `ports.K8sClusterService` / `local_k8s_cluster_service.go` / `k8s_cluster_resources.go` | **production-shaped gate passed**（`validate-vcluster-live-gate --production-shaped` 已固定 metadata target TLS passed 标准；`sprint13-k8s-workloads-vcluster-live-result.md`；production guard：`validate-sprint13-b-track-production-shape`；evidence：`development-records/live-evidence/sprint13-k8s-workloads-vcluster-live-evidence.json`；不代表 full platform production ready） |
| 对象存储 bucket/upload/download | MinIO（已选 2026-06-19，S3 兼容 pre-signed URL） | `ports.ObjectStore` + `ports.StorageService` / `storage_resources.go` | **production-shaped gate passed**（`SPRINT13-OBJECTSTORE-MINIO-A-TRACK`；result：`sprint13-objectstore-minio-live-result.md`；evidence：`live-evidence/sprint13-objectstore-minio-live-evidence.json`；gate：`validate-object-store-live-gate`） |
| 向量文档写入 | Milvus（已选 2026-06-19） | `ports.VectorStore` + `ports.VectorStoreService` / `vector_store_resources.go` | **production-shaped gate passed**（`SPRINT13-VECTOR-MILVUS-A-TRACK`；B 轨 live result `sprint13-vector-milvus-live-result.md`；evidence：`live-evidence/sprint13-vector-milvus-live-evidence.json`；readiness：`sprint13-vector-milvus-readiness.md`；gate：`validate-vector-store-live-gate`；历史 LIVE PENDING token 仅作门禁兼容语境；不代表 full platform production ready） |

## Sprint 15：Console Instance Observability（✅ 已完成，2026-07-08）

> 本节记录统一实例可观测性 PRD（`repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md`）对应的 11 个 issue 执行完成事实。该 PRD 覆盖 Core 端 handler 补齐、Console UI 6 个 Tab 组件实现和 Gateway real K8s provider 链路接入，对应 9 种计算实例 kind（vm/container/gpu_container/sandbox/batch_job/notebook/k8s_cluster/bare_metal/dpu_node）的日志、事件、指标、终端/console 和安全事件能力。各 issue 的实现与验证细节见 `repo/development-records/` 对应批次记录。

### Core 端实现（Issue #001 / #002 / #011）

| 批次 | Issue | 内容摘要 | 状态 |
|---|---|---|---|
| CORE-CONSOLE-SESSION-HANDLER-A | #001 | VM console session handler 补全：新增 `CreateConsoleSession` port 方法 + Local/Prometheus adapter 实现 + 5 个 HTTP 测试；protocol 默认值在 adapter 层填充，白名单在 handler 层校验；`connect_url` 与 `url` 等价 | ✅ 已完成（2026-07-03） |
| CORE-INSTANCE-METRICS-MULTI-EXPORTER-A | #002 | 多 exporter 聚合 adapter + range query 端点：通过 `InstanceObservationGetRequest.Kind` 路由 GPU 采集；逐字段降级（`if err == nil` 守卫）；新增 `GET /observability/query_range` 返回 matrix 时序采样点；PromQL label 重写（namespace/pod 映射）；NaN/Inf 过滤；正则 pod matcher 兼容 Deployment hash 后缀 | ✅ 已完成（2026-07-06，增量 2026-07-08） |
| GATEWAY-INSTANCE-CREATE-REAL-K8S-PROVIDER-A | #011 | Gateway 实例创建链路接入 real K8s provider：新增 `bootstrap.ConnectInstanceService` helper；`instance_service_runtime.go` 按 `WORKLOAD_PROVIDER` env 切换；lazy re-observe（非终态实例 Get/List 时触发 K8s 状态同步）；Workload Identity Secret manifest 生成；auth.go 注入 `types.TenantContext`；Secret 脱敏；`make validate-architecture` 通过 | ✅ 已完成（2026-07-08） |

### Console UI 端实现（Issue #003 - #010）

| 批次 | Issue | 内容摘要 | 状态 |
|---|---|---|---|
| CONSOLE-INSTANCE-OBSERVABILITY-SHELL-A | #003 | 路由壳层 + 实例上下文：新建 `route.tsx`（PageHeader + Tab 栏 + Tab Panel + `?tab=` 深链 + deleted 拦截）、`InstanceContext.tsx`、`observabilityTabsConfig.ts`；深链回退双层判定；`InstanceContext` 暴露 `isDeleted`/`isRunning` 派生字段 | ✅ 已完成（2026-07-06） |
| CONSOLE-INSTANCE-OBSERVABILITY-LOGS-A | #004 | 日志 Tab：`LogsTab.tsx` 使用 `useInfiniteQuery` cursor 分页 + 级别筛选 Select + Table 列展示；`levelFilter` 空字符串映射为 `undefined` | ✅ 已完成（2026-07-06） |
| CONSOLE-INSTANCE-OBSERVABILITY-EVENTS-A | #005 | 事件 Tab：`EventsTab.tsx` 使用 `useQuery` 一次性加载 limit=100 + 类型筛选 Select；cursor 分页因 Core OpenAPI `listInstanceEvents` query 缺 `cursor` 入参而降级为一次性加载 | ✅ 已完成（2026-07-06） |
| CONSOLE-INSTANCE-OBSERVABILITY-METRICS-A | #006 | 指标 Tab（双通道）：`MetricsTab.tsx` 双通道布局、`MetricsSnapshot.tsx` 快照卡片、`MetricsChart.tsx` PromQL 时序图、`promqlTemplates.ts` 冻结模板；403 判断 `error.code==='FORBIDDEN'`；自动刷新用 `invalidateQueries`；后改为 range query（`/observability/query_range`） | ✅ 已完成（2026-07-06，增量 2026-07-08） |
| CONSOLE-INSTANCE-OBSERVABILITY-TERMINAL-A | #007 | 终端 Tab（exec）：`TerminalTab.tsx` — POST exec → ws_url → WebSocket + xterm.js；5 态状态机；`idempotency_key` 生命周期（重试复用，重连新生成）；xterm lazy 创建于 `ws.onopen` | ✅ 已完成（2026-07-06） |
| CONSOLE-INSTANCE-OBSERVABILITY-CONSOLE-A | #008 | 控制台 Tab（VM console/VNC）：`ConsoleTab.tsx` — 协议 Select + POST console → connect_url → window.open；3 态状态机 | ✅ 已完成（2026-07-06） |
| CONSOLE-INSTANCE-OBSERVABILITY-SECURITY-EVENTS-A | #009 | 安全事件 Tab（仅 sandbox）：`SecurityEventsTab.tsx` — severity 筛选 Select + Table 列展示；cursor 分页同样 blocked-by-core | ✅ 已完成（2026-07-07） |
| CONSOLE-INSTANCE-OBSERVABILITY-BROWSER-VERIFICATION-A | #010 | 验证收口批次（verification-only，无代码改动）：9 条 AC 逐条代码审查映射到组件源码行号；验证 SPEC §1.1 五项 Core 端实现落地情况 | ✅ 已完成（2026-07-07） |

### 关键设计决策与已知边界

- **Kind × Tab 矩阵**：container/gpu_container→logs,events,metrics,terminal；sandbox→+security-events；vm→logs,events,metrics,console；batch_job/notebook→logs,events,metrics；k8s_cluster/bare_metal/dpu_node→logs,events（无 metrics）
- **指标双通道**：快照（`getInstanceMetrics` ← adapter ← exporter）+ 时序（`/observability/query_range` PromQL 代理返回 matrix）
- **多 exporter 聚合**：metrics.k8s.io（CPU/内存/网络）+ DCGM（GPU/显存），仅 `gpu_container` 采集 GPU
- **cursor 分页 blocked-by-core**：`listInstanceEvents` 和 `listInstanceSecurityEvents` query 缺 `cursor` 入参（response 有 `next_cursor`），遵守契约不发明字段，降级为一次性加载
- **后端 WebSocket exec 服务端未实现**：SPEC §11.2 已知边界，归后续 Core 批次
- **Issue #011 lazy re-observe**：非终态实例在 Get/List 时触发 K8s 状态同步，避免引入后台 controller

## Instance Observability Completion 增量补全（2026-07，PR4 分支）

> 本节记录 `feat/instance-observability-pr4` 分支对 Sprint 15 实例可观测性的增量补全工作，对应 SPEC `spec-console-instance-observability-completion.md` 的 16 个设计决策（D-1~D-16）、12 个 User Story（US-001~US-012）和 8 个批次（B-1~B-8）。覆盖 LogStore port 抽象、Loki 日志持久化、Prometheus GPU/VM 指标采集、PromQL label 重写扩展和 VM 前端模板。各批次实现与验证细节见 `repo/development-records/instance-observability-completion-*.md`。

### 批次执行状态

| 批次 | Issue | 内容摘要 | 状态 |
|---|---|---|---|
| INSTANCE-OBSERVABILITY-COMPLETION-B1-HANDLER-PASS-KIND | #001 | Gateway `getMetrics` handler 透传 `record.Kind` 到 `InstanceObservationGetRequest`，修复 GPU 分支死分支问题；`metricsKindSpy` 端到端覆盖 container/gpu_container/vm 三种路径 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B2-PROMETHEUS-DCGM-SCRAPE | #002 | Prometheus ConfigMap 新增 `dcgm-exporter` scrape job，使 `DCGM_FI_DEV_GPU_UTIL` 等 GPU 指标可被采集 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B3-GPU-ADAPTER-E2E-VERIFY | #003 | GPU adapter 端到端集成测试 + live gate 缺陷修复：真实 DCGM 不暴露 `FB_TOTAL`，改用 `FB_FREE+FB_USED`；同步更新 SPEC D-2 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B3-LOGSTORE-PORT | #004 | 新增 `pkg/ports/log_store.go` 定义日志持久化存储 port 抽象（`LogStore` interface + `LogQueryRequest`/`LogQueryResult`） | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B3-LOKI-LOG-STORE-ADAPTER | #005 | `LokiLogStore` adapter 实现：LogQL 查询 + cursor 分页 + level 解析；15 个单元测试覆盖 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B4-LOKI-FLUENT-BIT-DEPLOY | #007 | Loki 3.6.0 + Fluent Bit 3.2.0 DaemonSet 部署示例（485 行，10 资源）；三节点 live 验证全通过 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B5-LOKI-RANGE-CA-FIX | live gate 收尾 | 修复 Loki pod 正则匹配、K8s CA 加载和 metrics 时序图无数据三个阻断性缺陷 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B5-PROMETHEUS-KUBEVIRT-SCRAPE | #008 | Prometheus ConfigMap 新增 `kubevirt-virt-handler` scrape job 采集 `kubevirt_vmi_*` 指标 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B3-LOGSTORE-INJECTION | #006 | `PrometheusInstanceObservability` 新增可选 `logStore` 字段 + `SetLogStore` 方法；Gateway runtime 按 `INSTANCE_OBSERVABILITY_LOG_STORE` env 切换实现 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B5-GETMETRICS-VM-BRANCH | #009 | `GetMetrics` 新增 VM 分支：查询 6 个 `kubevirt_vmi_*` 指标；`name` label 精确匹配；`MemoryUsedMB` 用 PRD FR-17 公式 `domain_bytes - usable_bytes` | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B6-REWRITE-PROMQL-NAME-LABEL | #010 | `rewritePromQLLabels` 扩展支持 `name` label（OQ-4 决策 D-13）；`name` 用精确匹配非正则 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B7-VM-PROMQL-TEMPLATES | #011 | 前端 `promqlTemplates.ts` 新增 VM kind 冻结 PromQL 模板（CPU 利用率、内存使用率）；VM 配色复用 container 蓝绿 | ✅ 已完成 |
| INSTANCE-OBSERVABILITY-COMPLETION-B8-VM-SNAPSHOT-VERIFY | #012 | VM 指标 Tab 快照卡片验证（纯验证批次）：确认 `getMetricsForVM` 查询 `kubevirt_vmi_*` 指标、null 字段显示「暂不可用」不伪造 0 | ✅ 已完成 |

### 关键设计决策与已知边界

- **LogStore port 抽象**：新增 `ports.LogStore` 单方法 interface（`QueryLogs`），复用现有 `InstanceLogEntry`，Cursor 为 opaque string
- **Loki 方向偏离 SPEC**：实现使用 `direction=backward` + cursor→end（RFC3339↔Unix 纳秒），偏离 SPEC §3.3/§5.5 的 `forward`+cursor→start；继承 B5 live gate 修复语义，待 SPEC 同步
- **Loki pod 正则匹配**：SPEC 指定精确 `{pod="<instance_id>"}`，实现用 `{pod=~"^<instance>(-.*)?$"}` 正则匹配兼容 ReplicaSet hash
- **Level 推断**：SPEC 未规定，实现新增 `inferLogLevel` 从 message 推断 level，兼容 Fluent-Bit 采集的 nginx/stdout 日志无 level 字段的情况
- **VM `resident_bytes` 查询但不赋值**：SPEC AC2/FR-15 要求"必须查询"，实现查询但只取 Timestamp，值丢弃（record 无对应字段）；`MemoryUsedMB` 用 PRD FR-17 公式 `domain_bytes - usable_bytes`
- **GPU 显存公式**：live gate 复现真实 DCGM 不暴露 `DCGM_FI_DEV_FB_TOTAL`，改用 `FB_FREE + FB_USED`，单位为 MiB 非 bytes；已同步更新 SPEC D-2
- **OQ-4 决策**：`rewritePromQLLabels` 扩展支持 `name` label，VM 用精确匹配（VMI `metadata.name` 无随机后缀）
- **VM 端到端 live 验证待补**：当前系统无 VM，单元测试用 mock HTTP server 验证，依赖 KubeVirt scrape 配置部署 + VM 运行后补齐
- **MinIO emptyDir 非持久化风险**：Loki 部署示例使用 MinIO S3 后端，emptyDir 重启后数据丢失，已在 yaml 头部标注
- **Local mock GPU 返回 0 而非 nil**：`LocalInstanceObservabilityService` 对非 `gpu_container` kind 返回 GPU 字段为 `&0.0` 而非 nil，与 port 注释"缺失不等于 0"原则不一致，属已知边界

## Sprint 12 已完成切片

1. `SPRINT12-KICKOFF-A`：Sprint 12 启动 + GAP 分析归档，规划 19 个 Core handler 缺口 + 2 个 422，分 B1/B2/B3 三批；仅 ANI Core，Tier1 local profile。
2. `CORE-SVC-SUPPORT-OBSERVABILITY-A`：B1 handler 已完成。新增实例可观测只读 port/local adapter，接入 `/instances/{instance_id}/logs`、`/events`、`/metrics`、`/security-events` 和 `POST /exec`；新增 GPU inventory local adapter 与 `gpu_inventory_resources.go`，注册 `/gpu-inventory`、`/gpu-inventory/occupancy`、`/sandbox-templates`；响应带 `dev_profile`，不声明 production/runtime ready。
3. `CORE-SVC-SUPPORT-NETSTORE-A`：B2 handler 已完成并复审收口。扩展 network/storage/K8s ports 与 local adapters，接入 `/networks/routes`、`/volumes/{volume_id}/snapshots`、`/filesystems/{filesystem_id}/mount-targets`、`/k8s-clusters/{cluster_id}/workloads`；`createVolumeSnapshot` 的 202 响应按全局约定返回 `AsyncTask`；向量库非 ready 与 K8s 创建前置不满足返回 `422 PRECONDITION_FAILED`；响应带 `dev_profile`，不声明 production/runtime ready。
4. `CORE-SVC-SUPPORT-OBJVEC-A`：B3 handler 已完成。扩展 storage/vector ports 与 local adapters，接入 `/buckets`、`/objects/upload`、`/objects/{object_id}/download`、`/vector-stores/{vector_store_id}/documents`；对象 upload/download 返回预签名 URL，不走 multipart；vector document insert 返回 202；不声明 production/runtime ready。
5. `SPRINT12-CLOSURE-A`：Sprint 12 收口完成，进入 Sprint 13 real provider/live gate 收敛。

## Sprint 11 已完成切片

本节保留 Sprint 11 的历史回归事实，完整历史清单以 `repo/development-records/README.md` 为唯一归档索引。

1. `SPRINT11-KICKOFF-A`：入口文档切换到 Sprint 11 / Core Real Deployment Validation；明确只做 ANI Core，先跑真实服务器只读验证和风险评估。
2. `CORE-STORAGE-DISK-RISK-A`：新增 `deploy/real-k8s-lab/sprint11-storage-disk-plan.yaml` 和 validator，记录三台物理机系统盘、数据盘、稳定 `/dev/disk/by-id` 映射、Rook-Ceph 风险策略。策略明确禁止依赖 `/dev/sdX` 顺序，禁止为“盘符对齐”调整启动盘或控制器枚举。
3. `CORE-REAL-DEPLOY-A`：新增 `deploy/real-k8s-lab/sprint11-core-real-deployment.yaml` 和 validator，聚合 Sprint 10 release-prep、REAL-K8S-LAB profile、K8s/KubeVirt/storage 只读验证和 Sprint 11 文档一致性门禁。
4. `CORE-ROOK-CEPH-FORMAL-DEPLOYMENT-A`：新增 `deploy/real-k8s-lab/sprint11-rook-ceph-formal-deployment.yaml` 和 validator，交付 Rook-Ceph `CephCluster`、`CephBlockPool`、`StorageClass` 正式部署代码包；只使用 `/dev/disk/by-id` SSD 候选盘，排除 HDD，不自动设为默认 StorageClass。
5. `CORE-SAFE-COMPLETION-A`：新增 `deploy/real-k8s-lab/sprint11-core-safe-completion.yaml` 和 validator，按上游 Kubernetes/Rook-Ceph 最佳实践固定安全完成条件：只读验证、持久设备 ID、raw unmounted OSD 策略、fail-closed、人工审批前禁止写操作。
6. `CORE-REAL-DEPLOY-DOC-CONSISTENCY-A`：新增 Sprint 11 文档一致性 gate，校验 `ANI-DOCS-INDEX.md`、`ANI-06-开发计划.md`、`repo/CURRENT-SPRINT.md`、`repo/README.md`、Makefile targets 和 development records 索引。
7. `CORE-ROOK-CEPH-LIVE-DEPLOYMENT-A`：正式部署 Rook `v1.20.0`、Ceph `v19.2.3`、CSI operator、CSI-Addons CRD、CephCluster、`ceph-rbd-ssd` pool 和 `ani-rbd-ssd` StorageClass；5 个 SSD OSD 运行；RBD PVC/Pod smoke test 通过并删除临时资源。
8. `CORE-ROOK-CEPH-VM-STORAGE-SMOKE-A`：启动临时 KubeVirt VM 挂载 Rook-Ceph RBD Block PVC；PVC/PV Bound，VMI Running/Ready，guest 看到 `/dev/vdb` 并完成块设备写入尝试；临时 VM/PVC/PV/StorageClass 已删除。
9. `CORE-ROOK-CEPH-REBOOT-RESILIENCE-A`：按 worker-first、control-plane-last 顺序逐台重启三台节点；两个 worker 的 VM/PVC 恢复通过，control-plane 重启后 API readyz、mon/mgr/OSD、Ceph 和 worker VM/PVC 观测恢复；未并发重启。
10. `SPRINT11-SAFE-CLOSURE-A`：Sprint 11 最终安全闭环已更新为“部署前安全证据 + 部署后 live result + VM storage smoke result + reboot resilience result”记录；不是实际 v1.0.0 发布或完整 production ready。
11. `CORE-HISTORICAL-DOC-MARKER-COMPAT-A`：修复 Sprint 8/9/10 Core 历史文档一致性 validator 的 marker 逻辑，使其接受当前入口文档中的历史门禁/已完成归档表达，同时继续拒绝 stale current marker；不新增 Services 或 Core API path。
12. `ANI-14-PHASE4-BATCH1-A`：Phase 4 第一批 handler 骨架完成：新建 8 个 handler 文件（55 条路由），修改 stubs.go/router.go；Models/InferenceServices/KnowledgeBases/GpuContainers/Sandboxes/Tenant/Branding/Tasks 全部从 501→200；build/test/architecture 通过。

## 真实环境结论

- Kubernetes 三节点 Ready，版本 `v1.36.1`；KubeVirt phase `Deployed`。
- `rook-ceph` CephCluster 已部署完成，状态 `Ready/HEALTH_OK`；3 个 mon、1 个 mgr、5 个 OSD 运行。
- `ceph-rbd-ssd` pool 为 `Ready`；`ani-rbd-ssd` StorageClass 已上线，`Retain`、`WaitForFirstConsumer`、非默认 StorageClass。
- 受控 RBD smoke test 使用临时 `Delete` StorageClass，PVC 绑定、Pod 挂载、写读 marker 成功；临时 Pod/PVC/StorageClass/PV 已删除。
- 受控 KubeVirt VM RBD storage smoke 使用临时 `Delete` StorageClass 和 Block PVC，VMI 达到 `Running/Ready`，guest 看到 RBD block device 并完成写入尝试；临时 VM/PVC/PV/StorageClass 已删除。
- 逐节点 reboot resilience 已执行：两个 worker 先后重启并验证同一 VM/PVC 恢复；control-plane 最后重启并验证 API readyz、mon/mgr/OSD、Ceph 和 worker VM/PVC 观测恢复；未并发重启。
- ANI1 系统盘观测为 `sdb`，数据 SSD 为 `sda`；ANI2 系统盘观测为 `sdc`，数据 SSD 为 `sda`/`sdb`；ANI3 系统盘观测为 `sdd`，数据 SSD 为 `sda`/`sdb`，另有一块 HDD 为 `sdc`。
- Linux `/dev/sdX` 不是稳定设备身份，不能作为 Rook-Ceph OSD 或 fstab 自动化选择依据。后续必须使用 `/dev/disk/by-id`、WWN、序列号或 UUID/PARTUUID。
- 对 Rook-Ceph，初始 VM 优先存储池建议只使用未挂载、无文件系统签名的 SSD raw devices；ANI3 HDD 初期应排除或单独建低速 class，不要混入 VM 优先 SSD pool。

## 当前事实边界

- Core 保护范围只推进 ANI Core；Services/RAG/Console/BOSS/前端/推理/知识库业务在 Services 主责目录以受控 PR 推进，触碰 Core 保护范围时按 CODEOWNERS 共同审查。
- Sprint 11 未新增 Core OpenAPI path，Core API v1 兼容性基线保持有效。
- Sprint 11 没有新增 `M1-REAL-LAB-*` guard。
- 本阶段未执行手工 `wipefs`、`sgdisk`、`mkfs`、`mount`、`/etc/fstab` 修改、系统盘变更、默认 StorageClass 切换或已有 PVC 迁移；Rook-Ceph 按审批后的 manifest 自动完成 OSD prepare 和 OSD 认领。生产化 reboot resilience 已按审批逐台重启三台节点，未并发重启。
- “盘符对齐”只可作为人工阅读清单里的 slot 命名，不可作为自动化操作目标；真实自动化必须使用持久设备 ID。
- Sprint 11 最终安全完成遵循上游 Kubernetes/Rook-Ceph 最佳实践：先只读盘点，再用稳定设备 ID 建模，最后在人工审批后才允许任何状态变更。

## 历史回归门禁

- Sprint 8 Core-only 代码开发已完成，并继续作为 release hardening、installer live-readiness、offline pack、CLI-B 和文档一致性历史门禁保留。
- Sprint 9 Core-only 代码开发已完成，并继续作为 RC readiness、release evidence、offline checksum、CLI version 和文档一致性历史门禁保留。
- Sprint 10 Core-only 代码开发已完成，并继续作为 artifact manifest、version policy、final readiness、CLI release metadata 和文档一致性历史门禁保留；Sprint 10 不是实际 v1.0.0 发布。
- Sprint 8/9/10 历史文档一致性门禁接受当前 Sprint 11 入口文档中的历史门禁/已完成归档表达，不要求入口文档保留旧 Sprint 的当前态短语。
- Sprint 5 `REAL-K8S-LAB-A` / `make validate-real-k8s-profile` 仍作为真实底座历史门禁保留，覆盖 Kube-OVN、KubeVirt、vCluster 与 local profile / real-provider 边界；M1-NETWORK-LIVE-A / `validate-kubeovn-network-live-gate` 固定 Kube-OVN `Vpc/Subnet`、NetworkPolicy 和 Service/LB contract 门禁，Sprint 13 S01 已在此基础上补 route contract 并通过真实 route evidence；M1-K8S-LIVE-A / `validate-vcluster-live-gate` 固定 vCluster Helm/kubeconfig、kubectl `/version` 和 Core live proxy contract 门禁，Sprint 13 S02 已在此基础上补 `core-workloads-list` 并通过真实 workload evidence。
- Sprint 11 聚合门禁依赖 Sprint 10 release-prep，不重新打开这些历史 Sprint 的开发范围。

## 文档入口边界

- `CLAUDE.md` 只维护稳定强制规则、读取顺序、架构边界、提交门禁和 Karpathy 五条开发原则。
- 当前 Sprint 的详细完成项、未完成项、验收命令、下一步和真实底座边界以本文为准。
- 批次实现细节只写入 `repo/development-records/*.md`，不得把每日开发流水账或 API path 长列表写回 `CLAUDE.md`。
- 修改入口文档后必须运行 `make validate-doc-entrypoints`。

## 验收命令

```bash
make validate-sprint11-storage-disk-plan
make validate-sprint11-core-real-deployment
make validate-sprint11-rook-ceph-formal-deployment
make validate-sprint11-rook-ceph-live-deployment-result
make validate-sprint11-rook-ceph-vm-storage-smoke
make validate-sprint11-rook-ceph-reboot-resilience
make validate-sprint11-safe-completion
make validate-sprint11-core-doc-consistency
make validate-sprint11-real-deployment
python scripts/validate_yaml.py deploy/real-k8s-lab/sprint11-core-real-deployment.yaml deploy/real-k8s-lab/sprint11-storage-disk-plan.yaml deploy/real-k8s-lab/sprint11-rook-ceph-formal-deployment.yaml deploy/real-k8s-lab/sprint11-rook-ceph-live-deployment-result.yaml deploy/real-k8s-lab/sprint11-rook-ceph-vm-storage-smoke-result.yaml deploy/real-k8s-lab/sprint11-rook-ceph-reboot-resilience-result.yaml deploy/real-k8s-lab/sprint11-core-safe-completion.yaml
make validate-doc-entrypoints
git diff --check
```

Sprint 13 基线回归入口：

```bash
make test
make validate-demo-instances validate-core-alpha validate-gpu-contracts
make validate-network-alpha validate-storage-alpha validate-vector-alpha
make validate-spec-split validate-core-beta validate-core-api-compatibility
make validate-sdk-beta validate-mock-a validate-doc-api validate-sdk-mock-smoke validate-sprint4-closure
make validate-instance-observability-live-gate
python scripts/validate_yaml.py api/openapi/v1.yaml
make validate-doc-entrypoints
git diff --check
```

Sprint 13 单批 real provider/live gate 还必须追加该批固定 live gate 命令和 evidence JSON 校验；未形成命令与 evidence 前，不得标记为 runtime ready。
S01-S07 B 轨还必须追加 `make validate-sprint13-b-track-production-shape`，确保 production-shaped evidence 未通过前不能误标 production ready；S05-S07 已复用同一 proof_items 标准，历史 LIVE PENDING token 仅作门禁兼容语境。

Sprint 14 resilience feature branch 回归入口：

关联记录：[`development-records/sprint14-core-resilience-plan.md`](development-records/sprint14-core-resilience-plan.md)、[`development-records/r-sprint14-resilience-live-gate.md`](development-records/r-sprint14-resilience-live-gate.md)、[`development-records/live-evidence/sprint14-resilience-live-evidence.json`](development-records/live-evidence/sprint14-resilience-live-evidence.json)、[`api/core-contract-changelog-sprint14.md`](api/core-contract-changelog-sprint14.md)。

```bash
make validate-sprint14-resilience-live-gate
python scripts/validate_yaml.py deploy/real-k8s-lab/sprint14-resilience-live-gate.yaml deploy/real-k8s-lab/sprint14-resilience-live-fixture.yaml
make test
make validate-architecture
make validate-doc-entrypoints
git diff --check
```

Sprint 14 live proof 已归档到 `development-records/live-evidence/sprint14-resilience-live-evidence.json`；production-ready 范围仅限 `ani-sprint14-resilience` 隔离 fixture。真实 live gate 复跑需要人工重新批准故障注入目标、影响和回滚方案。

Sprint 11 依赖的历史回归入口：

```bash
make validate-sprint10-release-prep
make validate-real-k8s-profile
```

> 涉及真实服务器写操作前，必须先重新执行只读盘点，并由人工确认具体设备 ID、预期影响和回滚方案。

<!-- 历史回归门禁校验器兼容标记（请勿删除；对应 dev-records 历史批次与 make validate-* 门禁） -->
**历史回归门禁 token（校验器兼容，勿删）：** Sprint 4 回归门禁 SPEC-SPLIT-A、SPEC-CORE-BETA、SPEC-COMPAT-A、SDK-BETA-A、SDK-BETA-B、SDK-BETA-C、SDK-BETA-D、SDK-MOCK-SMOKE-A、SDK-MOCK-SMOKE-B、SDK-MOCK-SMOKE-C、SDK-MOCK-SMOKE-D、MOCK-A、DOC-API-A（`make validate-doc-api`）、SPRINT4-CLOSURE-A（`make validate-sprint4-closure`），矩阵见 `api/core-beta-readiness.yaml`；Sprint 11 / Core Real Deployment Validation 正式部署完成；真实服务器只读验证已完成；Rook-Ceph 正式部署已完成；Sprint 11 执行环境：正式部署执行环境。
