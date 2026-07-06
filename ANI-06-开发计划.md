# KuberCloud ANI · 开发计划

> 版本 V8.3 | 广州常青云科技有限公司 | 内部产品规划文件
> 最后更新：2026-06-23
> 当前摘要：Sprint 12 Core handler/local profile 已闭环；Sprint 13 S01-S07 real provider live gate 均为 `production_shape.status=passed`。这只表示组件级 production-shaped acceptance passed，不等于 full platform production ready。
> Sprint 14 分支执行：`feature/sprint14-core-resilience-semantics` 已完成 R-P0-0 gateway shared store 前置批次、R-P0-1 gateway rate limit、R-P0-2 gateway idempotency replay、R-P0-3 adapter per-call timeout、R-P0-4 data-plane readyz health、R-P1-5 retry/circuit-breaker foundation、R-P1-6 resilience degradation 与 R-P2-7 multi-endpoint failover config；这些单批次仍按 local/logic verified 归档。SPRINT14-CORE-RESILIENCE-LIVE-GATE / validate-sprint14-resilience-live-gate / Sprint14 resilience live gate 已在 ani-sprint14-resilience 隔离 namespace 真实通过 P0 strong backend kill、P1 weak dependency degraded、P2 controller primary kill / follower failover，并归档脱敏 evidence；production-ready 范围仅限隔离 Sprint14 Core resilience fixture，不外推到现有 Sprint13 单副本后端或 full platform。

---

## 零、状态快照（先读这里）

> 任何新开发者（人类或 AI）打开本文件，先看这一节，30 秒内定位现在的位置。
> 任务细节见 → [`repo/CURRENT-SPRINT.md`](repo/CURRENT-SPRINT.md)（当前冲刺唯一执行入口）。
> 已完成批次不要堆在本文顶部，统一归档到 [`repo/development-records/README.md`](repo/development-records/README.md)。

### 文档职责

| 文档 | 职责 | 使用时机 |
|---|---|---|
| `CLAUDE.md` | AI/人类开发入口、稳定强制规则、架构红线、提交门禁；不维护批次流水账 | 每次开发会话启动时先读 |
| `ANI-06-开发计划.md` | 总路线、Services 解锁门禁、Sprint 边界、延期项 | 判断当前阶段和长期节奏 |
| `ANI-05-系统架构设计.md` | 系统架构图、Core/Services 模块边界、API/SDK/ports/adapters 结构 | 解释架构和新人理解全局时使用 |
| `repo/CURRENT-SPRINT.md` | 当前 Sprint 的执行清单、入口文件、验收命令 | 每次开始开发先读 |
| `repo/development-records/README.md` | 已完成批次索引 | 查历史实现和验证记录 |
| `repo/development-records/*.md` | 单批次闭环记录 | 需要追溯技术细节时再读 |

### 项目全局进度

```
仓库范围：仅 ANI Core。ANI Services 已冻结并移交外部产品团队，本仓库不再开发任何 Services 代码。
当前阶段：Phase 1 / Sprint 13 / Core real provider 与 live gate 收敛。
当前不是 Phase 2：Phase 2 指 2026-10 以后延期能力，不是下一次开发阶段。
交付目标：2026-09-30 ANI Core v1.0.0（Services P0 由外部团队负责）。
关键节奏：Services 团队维护业务产品/API 定义；Core 只按 Core OpenAPI REST API / Core SDK 补基础设施支撑，不反向开发 Services 业务。
当前重心：Sprint 13 从 Sprint 12 已闭合的 handler/ports/adapters/router 边界接入真实 provider 与 live gate。
生产化边界：S01-S07 均达到 production-shaped acceptance passed；full platform production ready 仍需正式镜像发布/升级、长期 SLA/soak、备份/恢复和故障注入等 release gate。Sprint14 resilience live gate 当前以 ani-sprint14-resilience 隔离 fixture 验证 backend kill、degradation 与 controller primary failover，不把现有 Sprint13 单副本后端误标为自身 HA。
Auth 边界：SPRINT13-AUTH-DEX-PRODUCTION-GATE / Auth/Dex production gate 已通过；production-shaped Gateway 使用 ANI_AUTH_MODE=auth_service。
当前执行入口：repo/CURRENT-SPRINT.md
详细计划：repo/development-records/sprint13-real-provider-readiness-plan.md
完整批次：repo/development-records/README.md

Sprint 12 摘要：
- CORE-SVC-SUPPORT-OBSERVABILITY-A：实例观测、GPU inventory/occupancy、Sandbox catalog；Tier1 local profile。
- CORE-SVC-SUPPORT-NETSTORE-A：网络路由、卷快照、filesystem mount-target、K8s workloads；Tier1 local profile。
- CORE-SVC-SUPPORT-OBJVEC-A：MinIO object-store pre-signed URL 与 vector document insert；Tier1 local profile。

Sprint 13 production-shaped live gate 摘要：
- S01 Kube-OVN network route：production_shape.status=passed。
- S02 vCluster workloads：production_shape.status=passed。
- S03 Rook-Ceph storage：SPRINT13-STORAGE-ROOK-CEPH-A-TRACK；validate-storage-live-gate；LIVE PENDING 仅作历史门禁兼容语境；production_shape.status=passed。
- S04 NVIDIA device-plugin / DCGM：SPRINT13-GPU-INVENTORY-DCGM-A-TRACK；validate-gpu-inventory-live-gate；LIVE PENDING 仅作历史门禁兼容语境；production_shape.status=passed。
- S05 MinIO object-store：SPRINT13-OBJECTSTORE-MINIO-A-TRACK；validate-object-store-live-gate；pre-signed URL；LIVE PENDING 仅作历史门禁兼容语境；production_shape.status=passed。
- S06 Milvus vector-store：SPRINT13-VECTOR-MILVUS-A-TRACK；validate-vector-store-live-gate；LIVE PENDING 仅作历史门禁兼容语境；production_shape.status=passed。
- S07 Prometheus + kubelet / K8s API observability：SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-A-TRACK；validate-instance-observability-live-gate；LIVE PENDING 仅作历史门禁兼容语境；production_shape.status=passed。
- S05-S07 B 轨可以继续：保留为历史兼容 token；截至 2026-06-21，S05/S06/S07 均已 passed。
- 镜像仓库 Harbor：SPRINT13-REGISTRY-HARBOR-A 代码对接 + **SPRINT13-REGISTRY-HARBOR-LIVE-A production-shaped live gate passed**（2026-06-30）；**REGISTRY-MULTI-PROJECT-A** 对齐租户多项目/自定义 name（Harbor 内部映射）；Harbor `docker.kubercon.local` @ 192.168.102.81；本机 Gateway `:8080` + `REGISTRY_PROVIDER=harbor`；`validate-registry-harbor-live-gate --production-shaped`；evidence 见 `sprint13-registry-harbor-live-result.md`；不代表 full platform production ready。
- Kubernetes REST 凭证解析：SPRINT13-KUBERNETES-REST-CREDENTIAL-RESOLVER-A；`ResolveKubernetesRESTClientConfig` + `LoadKubernetesRESTEnvFromOS` + Gateway `kubernetes_runtime.go`；本地可 `KUBECONFIG` 自动连集群；显式 `KUBERNETES_*` 仍覆盖 kubeconfig；**dev 便利批次，不标 production ready**。
- Gateway 实例 workload 接线：GATEWAY-INSTANCE-WORKLOAD-RUNTIME-A；`services/ani-gateway/workload_runtime.go` 将 `WORKLOAD_PROVIDER=kubernetes_rest` 注入 `/instances` orchestrator（对齐 M1-INSTANCE-P bootstrap）；`dev_profile`/`demo_notice` 反映 provider 配置；gate：`make validate-gateway-instance-workload-runtime`；**不新增 live gate，不标 production ready**。
- 实例网络选择接 Kube-OVN：INSTANCE-NETWORK-KUBEOVN-A；`CreateInstanceRequest.network` 支持 `vpc_id/subnet_id/private_ip`，写入 `workload_instances` 并返回 list/detail；Gateway 校验 tenant 下 subnet/vpc available 与 IPv4/CIDR/gateway；Deployment/Job pod template 与 KubeVirt VM template 注入 `ovn.kubernetes.io/logical_switch/ip_address`；真实 container Pod 在非重叠 `10.74.0.0/25` subnet 中 Running 且互 ping 通过。10.72 验证发现历史重叠 subnet 干扰，记录为后续 network provider 防重叠 CIDR 优化项；VM 仍为 manifest/template 单测覆盖，未标 VM live verified。

Sprint 14 Core resilience 分支完成状态：
- 分支：`feature/sprint14-core-resilience-semantics`。
- 目标：把 Core 运行期韧性从 local/logic verified 推进到隔离真实 lab 可验证，包括 P0 限流/幂等/超时/readyz、P1 重试/断路/strong-vs-weak 降级、P2 多端点配置与 controller primary failover。
- 完成：R-P0-0..R-P2-7 已实现并归档；`SPRINT14-CORE-RESILIENCE-LIVE-GATE` 已在 `ani-sprint14-resilience` 隔离 namespace 真实通过 P0 strong backend kill、P1 weak dependency degraded、P2 controller primary kill / follower failover。
- Evidence：`repo/development-records/live-evidence/sprint14-resilience-live-evidence.json`，已按规则脱敏；验证后隔离 namespace 已清理。
- 边界：production-ready 只限隔离 Sprint14 Core resilience fixture；不声明现有 Sprint13 单副本后端自身 HA，不声明 full platform production ready；PG 读副本路由、MinIO/Milvus 命名 circuit breaker policy 与后端生产 Operator 拓扑仍属后续 release/operator gate。

Gateway 元数据持久化（P0–P3，2026-06-30）：
- `GATEWAY-METADATA-PERSISTENCE-P0`：`DATABASE_URL` 接入 ani-gateway 共享 `MetadataStore`；实例/网络/存储 local profile 元数据写入 PostgreSQL（`workload_*` / `network_*` / `storage_*`）；未配置时保持内存模式；Redis 仍只做幂等/限流；migration `20260629_014`；sub-gate：`make validate-gateway-metadata-p0`；local/logic verified + PG 冒烟 PASS，不标 production ready。详见 `repo/development-records/gateway-metadata-persistence-p0.md`。
- `GATEWAY-METADATA-PERSISTENCE-P1`：branding / async_tasks / metering / vector_stores 元数据接 PG（`platform_branding`、`async_tasks`、`metering_token_reports`、`metering_records`、`vector_stores`）；migration `20260629_015`；gate：`make validate-gateway-metadata-p1`；真实 PG 冒烟 PASS，不标 production ready。详见 `repo/development-records/gateway-metadata-persistence-p1.md`。
- `GATEWAY-METADATA-PERSISTENCE-P2`：网络/存储 List/Get/Delete 读 PG（`network_*` / `storage_*` 既有表）；gate：`make validate-gateway-metadata-p2`；重启后 VPC/Volume GET 冒烟 PASS，不标 production ready。详见 `repo/development-records/gateway-metadata-persistence-p2.md`。
- `GATEWAY-METADATA-PERSISTENCE-P3`：bucket / volume snapshot / mount-target 元数据接 PG（`storage_buckets`、`volume_snapshots`、`filesystem_mount_targets`）；migration `20260629_016`；gate：`make validate-gateway-metadata-p3`；真实 PG 冒烟 PASS，不标 production ready。详见 `repo/development-records/gateway-metadata-persistence-p3.md`。
- `GATEWAY-METADATA-PERSISTENCE-P4`：DB 整合（`deploy/postgres/ani-dev-database-init.sql`、`gateway-metadata-schema.sql`、`make db-upgrade-gateway-metadata`）；`PUT /branding` 写 PG。详见 `repo/development-records/gateway-metadata-persistence-p4.md` 与 `deploy/postgres/README.md`。
- `GATEWAY-METADATA-PERSISTENCE-P5`：Registry 元数据接 PG（`registry_projects` / `registry_repository_permissions` / `registry_pull_secrets`）；local + Harbor `PersistingImageRegistry`；migration `20260629_017`；gate：`make validate-gateway-metadata-p5`；聚合 gate 扩展 P0–P5；PG 冒烟 PASS，不标 production ready。详见 `repo/development-records/gateway-metadata-persistence-p5.md`。
- `GATEWAY-METADATA-PERSISTENCE-P6-A1`：K8s 集群/节点池控制面元数据接 PG（`k8s_clusters` / `k8s_cluster_node_pools`）；migration `20260629_018`；gate：`make validate-gateway-metadata-p6-a1`；聚合 gate 含 P6-A1；local/logic verified，不标 production ready。详见 `repo/development-records/gateway-metadata-persistence-p6-a1.md`。
- `GATEWAY-METADATA-PERSISTENCE-P6-A2`：Secret 元数据/绑定接 PG（`secrets` / `secret_bindings`；无明文）；migration `20260630_019`；gate：`make validate-gateway-metadata-p6-a2`；聚合 gate 含 P6-A2；local/logic verified，不标 production ready。详见 `repo/development-records/gateway-metadata-persistence-p6-a2.md`。
- `GATEWAY-METADATA-PERSISTENCE-P6-A3`：branding OpenAPI 补齐 + logo 上传 MinIO + URL 写 PG；gate：`make validate-gateway-metadata-p6-a3`；聚合 gate 含 P6-A3；local/logic verified，不标 production ready。详见 `repo/development-records/gateway-metadata-persistence-p6-a3.md`。
- `GATEWAY-METADATA-PERSISTENCE-P6-A4`：Encryption key 元数据接 PG（`encryption_keys`；无 key material）；migration `20260630_020`；gate：`make validate-gateway-metadata-p6-a4`；聚合 gate 含 P6-A4；local/logic verified，不标 production ready。详见 `repo/development-records/gateway-metadata-persistence-p6-a4.md`。
- `GATEWAY-METADATA-PERSISTENCE-P6-B1`：in-cluster production-shaped Gateway Harbor live gate 契约 + runner；gate：`make validate-gateway-metadata-p6-b1`；in-cluster evidence 待 lab 执行，不标 production ready。详见 `repo/development-records/gateway-metadata-persistence-p6-b1.md`。
- `GATEWAY-METADATA-PERSISTENCE-P6-B2`：Harbor artifact-track live gate（docker push + artifacts + scan-result）；gate：`make validate-gateway-metadata-p6-b2`；live evidence 待 lab 执行，不标 production ready。详见 `repo/development-records/gateway-metadata-persistence-p6-b2.md`。
- `GATEWAY-METADATA-PERSISTENCE-P6-B3`：Harbor pull secret → Kubernetes imagePullSecret 注入（`pull-secret/kubernetes-apply` + SecretProviderApply）；gate：`make validate-gateway-metadata-p6-b3`；live evidence 待 lab 执行，不标 production ready。详见 `repo/development-records/gateway-metadata-persistence-p6-b3.md`。
- 聚合 gate：`make validate-gateway-metadata` 顺序执行 P0–P3 + P5 sub-gate，作为 Sprint 14 resilience 回归与本地验收入口。
```

| 阶段 | 状态 | 完成时间 | 说明 |
|---|---|---|---|
| **M1 基础设施底座** | ✅ 已完成 | 2026-05 | INFRA/GPU/Runtime/Instance A-S 全链路 |
| **M2 Auth/Gateway** | ✅ 已完成 | 2026-05 | OIDC/JWT/RBAC/API Key 全流程 |
| **V8 架构重规划** | ✅ 已完成 | 2026-05-15 | Core/Services 分层、AWS 工程加固 |
| **Sprint 1** | ✅ 已完成 | 2026-05-18 | 操作语义底座 + Health + Idempotency + Auth Final |
| **Sprint 2** | ✅ 已完成 | 2026-05-20 | VM & Container/GPU 深度 + **Core API Alpha Freeze** |
| Sprint 3 | ✅ 已完成 | 2026-05-20 | 网络/存储/向量 API + **SDK Alpha + Dev Profile Ready** |
| Sprint 4 | ✅ 已完成并归档 | 2026-05-21（开发验收完成）；计划窗口 2026-07-01~07-15 | API Beta 准备 + 四语言 SDK + Mock Server |
| Sprint 5 ⭐ | ✅ 真实验证完成 | 计划窗口 2026-07-16~07-31 | 三台物理服务器 K8s+Kube-OVN+KubeVirt bootstrap 完成，网络/VM/vCluster/Secret/HA/KMS-SM4/GPU 全部 live gate 真实执行并归档 evidence（逐项见 [当前真实底座环境状态](#当前真实底座环境状态)）；guard 系列见 `repo/development-records/guard-series/REAL-K8S-LAB-guard-index.md` |
| Sprint 6 ⭐ | ✅ 已完成 | 2026-06-03（提前完成）；计划窗口 2026-08-01~08-15 | Sandbox + 平台支撑；`M1-SANDBOX-A`、`M1-OBS-A`、`M1-METER-A`、`M1-REGISTRY-A` 与 `SPRINT6-CLOSURE-A` 已完成 |
| Sprint 7 ⭐ | ✅ Core-only 已完成 | 2026-06-04；计划窗口 2026-08-16~09-01 | `CORE-INSTALLER-A`、`CORE-OFFLINE-A`、`CORE-CLI-A`、`CORE-REGRESSION-A` 与 `SPRINT7-CLOSURE-A` 已完成；RAG/Console/Services 不在本仓库执行范围 |
| Sprint 8 ⭐ | ✅ Core-only 已完成 | 2026-06-04；计划窗口 2026-09-01~09-15 | `CORE-HARDEN-A`、`CORE-INSTALLER-LIVE-A`、`CORE-OFFLINE-PACK-A`、`CORE-CLI-B`、`CORE-DOC-CONSISTENCY-A` 与 `SPRINT8-CLOSURE-A` 已完成；Console/BOSS 不在本仓库范围 |
| Sprint 9 ⭐ | ✅ Core-only 已完成 | 2026-06-04；计划窗口 2026-09-16~09-25 | `CORE-RC-GATE-A`、`CORE-RELEASE-EVIDENCE-A`、`CORE-OFFLINE-CHECKSUM-A`、`CORE-CLI-VERSION-A`、`CORE-RC-DOC-CONSISTENCY-A` 与 `SPRINT9-CLOSURE-A` 已完成；这是 RC readiness，不是实际 RC cut |
| Sprint 10 ⭐ | ✅ Core-only 已完成 | 2026-06-04；计划窗口 2026-09-26~09-30 | `CORE-ARTIFACT-MANIFEST-A`、`CORE-VERSION-POLICY-A`、`CORE-FINAL-READINESS-A`、`CORE-CLI-RELEASE-METADATA-A`、`CORE-FINAL-DOC-CONSISTENCY-A` 与 `SPRINT10-CLOSURE-A` 已完成；这是 release-prep readiness，不是实际 v1.0.0 发布 |
| Sprint 11 ⭐ | ✅ Core Real Deployment Validation 正式部署完成；Rook-Ceph 正式部署已完成 | 2026-06-05 | Rook-Ceph CephCluster `Ready/HEALTH_OK`，5 个 SSD OSD，`ani-rbd-ssd` StorageClass、RBD smoke、KubeVirt VM RBD storage smoke、逐节点 reboot resilience 通过。批次清单见 `repo/development-records/README.md`；不是实际 v1.0.0 发布或完整 production ready |
| Sprint 12 ⭐ | ✅ Core-only 已完成 | 2026-06-19 | Core「Services 支撑 Handler」收口：19 个 handler + 2 个 422 均关联 `api/openapi/v1.yaml` operationId、`pkg/ports`、`pkg/adapters`、Gateway handler；契约改动见 [`repo/api/core-contract-changelog-sprint12-13.md`](repo/api/core-contract-changelog-sprint12-13.md)；仅 Tier1 local profile，不代表 runtime/production ready |
| Sprint 13 ⭐ | 🔄 收敛中（S01–S07 production-shaped gate passed） | 2026-06-19 起 | 真实 provider / live gate 收敛：S01 Kube-OVN、S02 vCluster、S03 Rook-Ceph（`SPRINT13-STORAGE-ROOK-CEPH-A-TRACK` / `validate-storage-live-gate`）、S04 NVIDIA device-plugin/DCGM（`SPRINT13-GPU-INVENTORY-DCGM-A-TRACK` / `validate-gpu-inventory-live-gate`）、S05 MinIO、S06 Milvus、S07 Prometheus observability 均 `production_shape.status=passed` 并归档 evidence。`SPRINT13-INSTANCE-OBSERVABILITY-PROMETHEUS-A-TRACK` / `validate-instance-observability-live-gate` 固定 Prometheus + kubelet contract；历史 LIVE PENDING token 仅作门禁兼容语境；计划见 `repo/development-records/sprint13-real-provider-readiness-plan.md`；production-shaped passed ≠ full platform production ready |
| Sprint 14 ⭐ | ✅ Core resilience feature branch complete | 2026-06-23 | Core 韧性与服务语义：R-P0-0..R-P2-7 已完成，覆盖 gateway shared store、限流、幂等重放、adapter per-call timeout、data-plane readyz、retry/circuit breaker foundation、strong/weak degradation、多端点 failover config；`SPRINT14-CORE-RESILIENCE-LIVE-GATE` / `validate-sprint14-resilience-live-gate` 已在隔离 namespace 跑通 P0/P1/P2 真实故障注入与 failover，并归档脱敏 evidence。production-ready 范围仅限隔离 Sprint14 Core resilience fixture |

### Core 与外部 Services 团队的协作门禁

ANI Services 已移交外部产品团队；本仓库只交付 ANI Core（基础设施底座 + Core OpenAPI/SDK/CLI），最终经外部 Services 落地给客户。Core 的成熟度按 Services 依赖反推，但 Services 的开发、排期与验收不在本仓库范围内。

| 日期 | 里程碑 | 本仓库（ANI Core）职责 |
|---|---|---|
| **2026-06-10 前后** | 外部团队产出产品功能/交互/API 定义 | 接收定义；据此规划 Core API/SDK 缺口补齐；在此之前不基于猜测预建 Services 业务能力 |
| **Sprint 5 收敛** | Core Real Path（真实 live gate） | 8 个真实 live gate 全部跑通并归档 evidence（见 CURRENT-SPRINT.md） |
| **持续** | Core API 兼容性 | Core API v1 不做破坏性变更；按外部定义只新增可选能力，循环收敛 |
| **2026-09-30** | ANI Core v1.0.0 | Core 主链路真实可用、SDK/CLI/部署文档就绪 |

**硬规则：** 凡外部 Services P0 场景依赖的 Core 能力，到约定 Runtime Ready 日期后不允许仍停留在 `contract`、`local-profile`、stub、mock success 或 `NOT_IMPLEMENTED`；必须由真实 live gate 证明。

**协作模式：** 外部团队改产品/接口定义 → Core 借 AI Coding 快速循环生成/调整代码与契约 → 真实环境验证 → 回环。Core/Services 跨层只走 Core OpenAPI REST API / Core SDK；Services 业务资源不回流 Core API。仓库内旧 Services 骨架（`repo/services/`、`repo/ai/`、`repo/operators/inference-operator/`、`repo/frontends/`）只读冻结，不再开发。

### 真实底座组件引入强制门禁

从 **Sprint 5** 开始，ANI Core 进入真实 provider 收敛阶段。以下规则为强制规则，不是建议：

1. **Sprint 1~4 允许以 API 契约、local profile、Mock Server 和 SDK 为主**，目标是先稳定产品能力边界、接口语义、状态机、权限、幂等、错误结构、SDK 和文档。
2. **Sprint 5 开始必须并行建设真实底座组件验证环境**，至少包含 K8s、Kube-OVN、KubeVirt、vCluster；涉及存储、对象、向量、加密和镜像仓库时，还必须逐步引入对应真实组件或等价测试实例。
3. **凡是需要证明“能和开源组件对接并运行”的能力，不得只靠 local profile 标完成**。local profile 只能标记为 `dev/local profile completed`，不能标记为 `real provider completed`、`production ready` 或 `runtime ready`。
4. **网络、VM、容器/GPU 容器、K8s 集群、K8s proxy、Secret 注入、存储挂载等能力，在 Sprint 5 之后必须具备真实组件门禁**，否则不得进入“真实主链路完成”或“可交付”状态。
5. **真实环境门禁必须形成固定命令或记录**。当前固定入口为 `REAL-K8S-LAB-A` 和 `make validate-real-k8s-profile`：默认校验门禁定义和文档闭环，三台云 VM 的 kubeconfig 就绪后使用 live 模式执行真实 kubectl 检查，并用 `--evidence-output` 归档 JSON 证据。未形成门禁前，只能称为“已开发契约与 local profile”。

真实底座引入顺序：

| 阶段 | 必须引入的真实底座 | 目的 | 未完成时不得声称 |
|---|---|---|---|
| Sprint 5 当前起 | K8s 测试集群 | 验证 Namespace、RBAC、ServiceAccount、API Server、StorageClass 等基础能力 | Core Real Path Beta |
| Sprint 5 当前起 | Kube-OVN | 验证 VPC/Subnet（`Vpc/Subnet`）、NetworkPolicy、Service/LB 等网络资源能真实创建和观察 | 网络真实 provider 完成 |
| Sprint 5 当前起 | KubeVirt | 验证 `M1-KUBEVIRT-LIVE-A` / KubeVirt VM 创建、启动、停止、删除、console/VNC 等能力能真实运行 | VM 真实 provider 完成 |
| Sprint 5 当前起 | vCluster | 验证 K8s 集群创建、kubeconfig、proxy 能真实访问租户集群 | K8s 集群服务完成 |
| Sprint 5~6 | MinIO / KMS或SM4实现 / K8s Secret | 验证对象存储、加解密、Secret 注入真实链路 | 模型仓库加密和凭据注入可交付 |
| Sprint 6~7 | Harbor / observability / metering 相关组件 | 验证镜像、监控、计量和平台支撑真实链路 | 平台支撑完成 |

因此，Sprint 5 之后每个涉及底座组件的批次必须同时说明三件事：当前是 `contract`、`local-profile` 还是 `real-provider`；依赖哪些真实组件和版本；用什么命令或记录证明已经跑通。

### 冲刺进度速览（明细见 CURRENT-SPRINT.md / dev-records）

> 完整批次清单和验收命令以 [`repo/CURRENT-SPRINT.md`](repo/CURRENT-SPRINT.md) 为唯一执行入口；已完成批次归档见 [`repo/development-records/README.md`](repo/development-records/README.md)。本节只保留 30 秒状态信号，不再复制批次明细。

| 冲刺 | 状态 | 一句话结论 |
|---|---|---|
| Sprint 3 | ✅ 已完成 | 网络/存储/向量 API + Workload Identity + 四语言 SDK Alpha + Core Dev Profile |
| Sprint 4 | ✅ 已完成 | API 分层收口 + Core API Beta 准备矩阵 + SDK helper + Mock Server + API 文档 |
| Sprint 5 ⭐ | ✅ 真实验证完成 | K8s 集群/proxy/upgrade/node-pool、KMS/SM4、Secrets、reconcile 的契约 + local profile + 代码边界 + live gate 全部完成，并在真实 lab 跑通归档 evidence。逐项 live gate 与 caveat 见 [当前真实底座环境状态](#当前真实底座环境状态)。 |
| Sprint 13 ⭐ | 🔄 收敛中 | S01-S07 real provider 均已 production-shaped gate passed；仍不等于 full platform production ready。 |
| Sprint 14 ⭐ | ✅ 分支完成 | Core resilience 三阶段 P0/P1/P2 已完成 aggregate live gate；代码、fixture、脱敏 evidence 与文档归档在 `feature/sprint14-core-resilience-semantics`。 |

**→ 继续入口：** 当前切片、验收命令、冻结目录见 [`repo/CURRENT-SPRINT.md`](repo/CURRENT-SPRINT.md)；破坏性磁盘操作、默认 StorageClass 切换、已有 PVC 迁移、HDD class 引入、并发重启或更大故障演练仍须单独审批。

### 当前真实底座环境状态

**底座：** 三台物理开发服务器已完成 Kubernetes `v1.36.1` + Kube-OVN `v1.15.8` + KubeVirt `v1.8.2` 最小部署；Kube-OVN CNI join subnet 迁移到 `172.30.0.0/16`，CNI/CoreDNS Ready，KubeVirt phase `Deployed`。

**已通过并归档 evidence 的 live gate：**

| live gate | 真实验证范围 | 当前 caveat（非生产化） |
|---|---|---|
| Kube-OVN network + external LB | network resource、external LoadBalancer IP 可达性 | external LB 用 live-gate helper 镜像/脚本兼容方案，非生产镜像供应链/Helm 部署 |
| KubeVirt VM + console/VNC | VM lifecycle；console/VNC 完成 HTTP `101` upgrade、`plain.kubevirt.io` 子协议回选、流字节验证 | — |
| vCluster Helm/kubeconfig/Core proxy | 创建、kubeconfig、Core proxy | Core proxy 经本机 kubectl proxy 转发，非生产 per-cluster metadata target/KMS token |
| vCluster upgrade | 升级真实执行 | — |
| Secret（含 VM guest 可见性） | env/file/VM 注入 | — |
| controller HA failover | 多副本 leader election failover | 最小依赖 + hostPath worker 二进制，非生产 Helm/Operator 控制面 |
| KMS/SM4 provider streaming | SM4-GCM 流式加解密 + objectstore round trip | live-gate fixture，非生产 KMS/对象存储/TLS/credential |
| GPU 调度 + CAPK node pool | 三节点调度依赖、VM worker create/scale | 证明 create/scale-ready，非 VM 内 GPU passthrough/vGPU |

**详细记录：** [bootstrap](repo/development-records/real-k8s-lab-k8s-kubeovn-kubevirt-bootstrap.md) · [network](repo/development-records/m1-network-live-c-kubeovn-real-lab-result.md) · [external-lb](repo/development-records/m1-network-live-d-kubeovn-external-lb-real-lab-result.md) · [vm](repo/development-records/m1-kubevirt-live-c-vm-real-lab-result.md) · [console-vnc](repo/development-records/m1-kubevirt-live-d-console-vnc-session-real-lab-result.md) · [vcluster](repo/development-records/m1-k8s-live-g-vcluster-real-lab-result.md) · [vcluster-upgrade](repo/development-records/m1-k8s-live-h-vcluster-upgrade-real-lab-result.md) · [gpu-scheduling](repo/development-records/m1-k8s-live-k-gpu-scheduling-real-lab-progress.md) · [node-pool](repo/development-records/m1-k8s-live-m-node-pool-capk-real-lab-result.md) · [secret](repo/development-records/m1-secrets-live-c-secret-real-lab-result.md) · [vm-secret](repo/development-records/m1-secrets-live-d-vm-secret-guest-real-lab-result.md) · [reconcile-ha](repo/development-records/m1-reconcile-live-c-ha-real-lab-result.md) · [kms-sm4](repo/development-records/m1-encrypt-live-c-kms-sm4-real-lab-result.md)

### 已完成批次完整记录

> 完整的已完成批次列表在 `repo/development-records/README.md`（唯一归档索引）。
> 详细技术记录在 `repo/development-records/*.md`。本文只保留关键里程碑，避免当前阶段被历史细节淹没。

主要已完成里程碑（仅列关键节点）：
- M1-INFRA-A/B/C/D/E/F — Kubernetes 基础设施、KubeOVN 网络、GPU 调度基线
- M1-GPU-A — 异构 GPU（NVIDIA/昇腾/海光）发现与调度契约
- M1-RUNTIME-A — WorkloadRuntime port（VM/容器/GPU/Sandbox/Batch Job 抽象）
- M1-INSTANCE-A ~ S — 实例全链路（计划→渲染→准入→审计→dry-run→apply→observe→持久化→服务层）
- M2.1-TASK-A/B/C — 异步任务/outbox/worker mutations
- M2.2-AUTH-A ~ K + M2.2-AUTH-FINAL — Auth 服务完整实现与生产收尾（JWT/RBAC/OIDC/JWKS/API Key/Dex smoke）
- V8 架构设计 — Core/Services 分层、API 工程约定（幂等性/控制平面分离等）
- AWS 工程加固 — /healthz /readyz schema、WorkloadReconcileController port、operations DB 表、permissions schema

### v1.0.0 后续延期项（不是当前下一阶段）

> ⚠️ **M1-K8S-A 已从延期列表移回 v1.0.0 范围（Sprint 5）**，理由见 Sprint 5 说明。
> 这里的延期项不是当前优先要做的任务；当前阶段是 Sprint 13 / Core real provider 与 live gate 收敛启动，Sprint 11 与 Sprint 12 已转为历史回归门禁，且不是实际 v1.0.0 发布。

| 条目 | 理由 |
|---|---|
| M1-BM-A（裸金属/Metal3）| 需物理机环境，无 P0 依赖 |
| M1-DPU-A（DPU节点）| 需专用硬件 |
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
1. 人工编写 OpenAPI 契约；如涉及 Core 内部 gRPC，再补 Protobuf 实现契约并保持对齐
2. AI 生成 Server Stub、Client、单元测试骨架
3. 人工审查逻辑正确性和安全边界
4. AI 补充错误处理、日志、Metrics 等横切代码
5. 人工做集成测试和边界 case 验证

---

## 二、双周冲刺计划（原始排期基线，2026-05-19 制定）

> ⚠️ **本节是 2026-05-19 制定的原始冲刺规划与验收标准参考。表中「计划窗口」是原始排期日期，实际执行已大幅提前。**
> **冲刺的真实状态、完成日期与当前重心，一律以「[零、状态快照](#零状态快照先读这里)」和 [`repo/CURRENT-SPRINT.md`](repo/CURRENT-SPRINT.md) 为准；本节仅供查阅每个冲刺的目标与验收命令，不作为进度真相。新人请先读 Section 零。**
>
> **规划原则：** 每个冲刺 2 周，有明确进入条件、交付清单、完工标准（验收命令）。

---

### Sprint 计划总览

> 状态列以「零、状态快照」为准；「计划窗口」为 2026-05-19 原始排期，实际完成日期见 Section 零。

| 冲刺 | 计划窗口（原始排期） | 主题 | 实际状态 |
|---|---|---|---|
| S1 | 05-15~05-31 | 操作语义底座 + Health + Auth 收尾 | ✅ 已完成 |
| S2 | 06-01~06-15 | VM/Container/GPU + Core API Alpha | ✅ 已完成 |
| S3 | 06-16~06-30 | 网络/存储/向量 + SDK Alpha | ✅ 已完成 |
| S4 | 07-01~07-15 | API Beta + SDK + Mock Server | ✅ 已完成 |
| S5 | 07-16~07-31 | 真实底座 live gate 收敛 | ✅ 已完成 |
| S6 | 08-01~08-15 | Sandbox + 平台支撑 local profile | ✅ 已完成 |
| S7 | 08-16~09-01 | Installer + 离线包 + Core CLI | ✅ Core-only 已完成 |
| S8 | 09-01~09-15 | Core 发布前加固 | ✅ Core-only 已完成 |
| S9 | 09-16~09-25 | RC 加固（只修 Bug） | ✅ Core-only 已完成（RC readiness，非实际 RC cut） |
| S10 | 09-26~09-30 | release-prep readiness | ✅ Core-only 已完成（非实际 v1.0.0 发布） |
| S11 | — | Core 真实部署验证 + Rook-Ceph 正式部署 | ✅ 已完成，转历史回归门禁 |
| S12 | — | Core「Services 支撑 Handler」补齐 | ✅ Core-only 已完成（Tier1 local profile） |
| S13 | 2026-06-19 起 | 真实 provider / live gate 收敛 | 🔄 收敛中：S01–S07 production-shaped gate passed |
| S14 | 2026-06-23 feature branch | Core 韧性与服务语义 | ✅ 分支完成：P0/P1/P2 aggregate live gate passed（隔离 fixture production-ready） |

> **v1.0.0 目标：** 2026-09-30 交付 ANI Core v1.0.0（当前未发布，不得标 v1.0.0/RC）；Services P0 由外部团队交付，不在本仓库开发。

### 代码依赖关键路径（实际代码状态驱动）

> 以下基于 2026-06-04 代码与文档状态，反映真实依赖而非愿景描述；当前仓库只推进 ANI Core。

```
当前代码实际状态：
  ✅ pkg/ports/ 与 pkg/adapters/runtime/ 已建立 ports/adapters 架构基础；具体数量以当前代码为准
  ✅ auth-service JWT/OIDC/RBAC 完整实现
  ✅ DB migrations 4个SQL，operations 表与 instance 深度字段已建
  ✅ /api/v1/instances Core Alpha path/schema/error/state/RBAC scope 已冻结，dev/local profile 可供 Services P0 依赖
  ✅ /api/v1/networks /volumes /objects /filesystems /vector-stores 已完成 Core dev/local profile；真实 provider 仍需 Sprint 5+ 收敛
  ⚠️  model-service 属于 ANI Services 早期逻辑，只读冻结，只能作为历史参考
  ⚠️  kb-service 当前为空目录，不能被文档描述成已实现知识库服务
  ⚠️  frontends/console 当前仍有历史单 API Client 使用痕迹；已冻结并移交外部产品/Services 团队
  ⚠️  K8s 集群 API local dev profile — 已有 create/get/list/delete + kubeconfig + proxy；vCluster Helm/kubeconfig/upgrade provider 代码边界、proxy forwarding adapter、本地 target resolver/store、metadata 持久化 store、Gateway router 注入接线、forwarding_static/forwarding_metadata runtime 选择和 vCluster live/upgrade contract 门禁已有；vCluster live Helm/kubeconfig/proxy、vCluster upgrade、三节点 GPU 调度与 CAPK node pool provider-backed create/scale 真实执行结果已完成；CAPK VM 内 GPU passthrough/vGPU 未在本轮声明完成
  ✅ Sandbox local profile 已完成；真实 Kata Containers provider 未完成，不得标记 production ready

关键依赖链（必须按顺序）：
  Sprint 1：WorkloadOperation 语义 + operation_id DB
       ↓ 解锁 Sprint 2（VM/Container 深度需要 operation_id 记录）
  Sprint 2：/instances handler stub → 真实实现（VM/Container/GPU）
       ↓ 解锁 Sprint 3（网络/存储需要 instances 关联）
  Sprint 3：/networks /volumes /objects /vector-stores handler → 真实
       ↓ 解锁 Sprint 4（API 契约能写全量路径）
  Sprint 3：Core Dev Profile Ready + SDK Alpha
       ↓ 解锁 Services 团队（06-30 前后）
  Sprint 4：API Beta 准备 + 四语言 SDK + Mock Server
       ↓ Services 团队基于稳定 SDK 持续开发
  Sprint 5：K8s/Kube-OVN/KubeVirt/vCluster/KMS/SM4/Secret/controller HA/GPU/CAPK node pool 真实 live gate 已完成并归档 evidence；guard series 已冻结，不再新增假设型 guard
       ↓
  Sprint 6：Sandbox、Observability、Metering、Registry 四个 Core API/local profile 已完成
       ↓
  Sprint 7：Core installer、离线包 manifest、Core CLI minimal behavior、Core regression profile 已完成
       ↓
  Sprint 8~10：Core 发布前加固、RC readiness、release-prep（均已完成，非实际 v1.0.0 发布）
       ↓
  Sprint 11：Core 真实部署验证 + Rook-Ceph 正式部署（已完成，转历史回归门禁）
       ↓
  Sprint 12：Core「Services 支撑 Handler」补齐（已完成 Tier1 local profile）
       ↓
  Sprint 13：真实 provider / live gate 收敛（S01–S07 production-shaped gate passed，当前重心）；RAG/Console/BOSS/Services 业务由外部团队负责
       ↓
  Sprint 14：Core 韧性与服务语义分支完成（P0/P1/P2 aggregate live gate passed，production-ready 范围仅限隔离 Sprint14 fixture）；待 PR/评审后进入主线状态
```

---

### Sprint 1：2026-05-15 → 2026-05-18（已完成）

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

**归档：** 详细完成记录见 `repo/development-records/README.md`；当时执行入口已切换到 `repo/CURRENT-SPRINT.md` 的 Sprint 3。

---

### Sprint 2：2026-05-19 提前启动 → 2026-05-20（已完成）

**主题：VM & Container / GPU 容器生产深度**

**进入条件：** Sprint 1 完工标准通过；`workload_instance_operations` 表已建；M2.2 Auth Final 已通过合同守卫和 Dex smoke。

**执行原则：** 先冻结 Services P0 依赖的 API 契约，再做 VM/Container 深度实现；每完成一个可验证切片，都要补测试并写入 `repo/development-records/`。

| 批次 | 内容 | 难度 | 预估 |
|---|---|---|---|
| **M1-INSTANCE-U** | VM 生产级操作：终止保护/VNC console/快照/磁盘绑定/SSH 连接信息 | 高 | 5天 |
| **M1-INSTANCE-V** | Container 部署深度（副本/滚动更新/回滚/历史）；GPU 调度原因/利用率 | 高 | 5天 |
| **SPEC-CORE-ALPHA** ⭐ | P0 Core path/schema/error/state/RBAC scope 冻结到 Alpha，覆盖 Services P0 依赖 | 中 | 2天 |

**完工标准：**
```bash
make test
# VM 实例可获取 VNC session URL
# Container 实例可触发 rollback 到上一版本
# GPU 容器状态包含 gpu_scheduling_reason 和 gpu_utilization
# api/openapi/v1.yaml 中 Services P0 依赖路径达到 Alpha Freeze，不允许后续 breaking change
```

**解锁：** Console VM 详情页、Container 部署页面接真实 API

---

### Sprint 3：2026-05-20 提前启动 → 2026-06-30（已完成）

**主题：Core API 面扩充（网络 + 存储 + 向量 + Workload Identity）**

**进入条件：** Sprint 2 完工标准通过

| 批次 | 内容 | 难度 | 预估 |
|---|---|---|---|
| **M1-NETWORK-A** | VPC/子网/安全组/LB CRUD：真实 KubeOVN 子资源管理 | 中 | 4天 |
| **M1-STORAGE-A** | 块存储(volumes) + 文件存储(filesystems) + 对象存储(objects) CRUD | 中 | 4天 |
| **M1-VSTORE-A** | vector-stores 创建/删除/检索 API（Milvus adapter 已有，加 Gateway 路由）| 低 | 2天 |
| **M1-WKID-A** | Workload Identity P0：实例创建时生成 lifecycle-bound API key + Secret 引用注入 + 实例删除时 revoke | 中 | ✅ 已完成 |
| **SDK-ALPHA-A** ⭐ | Go/Python/TypeScript/Java SDK Alpha：生成、import、compile smoke test | 中 | 2天 |
| **CORE-DEV-PROFILE-A（原 MOCK-DEV-A）** | ✅ 已完成：Core dev/local profile 一致性收口；本地成功响应显式暴露 `dev_profile`，并通过合同守卫防止 Services 业务 mock 与 Core P0 路径混淆 | 中 | 2天 |

**完工标准：**
```bash
make test
# POST /api/v1/networks/vpcs → 201 Created
# POST /api/v1/volumes → 201 Created
# POST /api/v1/vector-stores → 201；POST /{id}/search → 200
# 新实例的 ANI_WORKLOAD_TOKEN 环境变量已注入 + 实例删除后自动 revoked
make gen-core-sdk        # Go/Python/TypeScript/Java SDK 可生成
# Services 团队可用 SDK + Core dev profile 做端到端开发；Services 业务 mock 由 Services 团队自行建设
```

**解锁：** ANI Services 团队开始真实开发；Sprint 4 进入 API Beta 准备和 SDK 加固

---

### Sprint 4：2026-07-01 → 2026-07-15

**主题：API Beta 准备 + 四语言 SDK 加固 + Mock Server**

**进入条件：** Sprint 3 全部完工；Services P0 依赖已在 06-30 前解锁

| 批次 | 内容 | 难度 | 预估 |
|---|---|---|---|
| **SPEC-CORE-BETA** | 将 Sprint 1-3 所有新路径补齐到 Beta：schema、分页、idempotency、错误码、状态机、RBAC scope | 中 | 3天 |
| **SPEC-COMPAT-A** | 建立 Core API v1 兼容性基线，阻止误删 path/method/operationId/参数/响应/schema 字段 | 低 | 0.5天 |
| **SPEC-SPLIT-A** | /models /inference-services /knowledge-bases 移至 api/openapi/services/v1.yaml | 低 | 1天 |
| **SDK-GO-A** | oapi-codegen 生成 Go SDK（sdks/ani-go/） | 低 | 1天 |
| **SDK-PY-A** | openapi-generator 生成 Python SDK（sdks/ani-python/） | 低 | 1天 |
| **SDK-TS-A** | openapi-typescript 生成 TypeScript Client（sdks/ani-typescript/） | 低 | 1天 |
| **SDK-JAVA-A** | openapi-generator 生成 Java SDK（sdks/ani-java/，OkHttp3） | 低 | 1天 |
| **MOCK-A** | Prism Mock Server 基于 v1.yaml 启动，覆盖所有 Core 路径 | 低 | 1天 |
| **DOC-API-A** | Swagger UI / Redoc 自动生成并部署 | 低 | 1天 |

**完工标准（2026-07-15 前必须达成）：**
```bash
# v1.yaml 全量路径覆盖，Services P0 依赖路径无 TODO stub
make gen-core-sdk        # Go/Python/TypeScript/Java 四个 SDK 目录生成完毕
prism mock api/openapi/v1.yaml --port 4010   # 所有路径返回 200 mock
# Services 团队用 ani-python SDK 调用 Mock Server 能获得正确响应类型
```

**本冲刺结束即宣告 Core API Beta。之后 Services P0 依赖路径只允许兼容新增，不允许 breaking change。**

**解锁：** Services 团队基于稳定 SDK 持续开发；Core 进入真实 provider 深度和集成收口

---

### Sprint 5：2026-07-16 → 2026-07-31

**主题：K8s 集群管理 + 后台控制器 + 加解密**

> ⚠️ **M1-K8S-A 已恢复到 v1.0.0 范围**。理由：Services IaaS 域的"K8s集群服务"是客户最核心的 IaaS 需求之一；vCluster 实现有成熟路径，不需要专用硬件；Services 团队在 Sprint 5~6 开发模型仓库和推理服务时依赖稳定的 K8s 集群环境。

**进入条件：** API 冻结完成（Sprint 4）；Services 团队已用 Mock Server 自行开始 SVC-MODEL-A

| 批次 | 内容 | 难度 | 预估 | 解锁对象 |
|---|---|---|---|---|
| **M1-K8S-A/B/C/D/E/F/G + M1-K8S-LIVE-A/B/C/D/E/F/G/H/J/K/L/M + M1-K8S-PROXY-A/B/C/D/E/F + M1-NETWORK-LIVE-A/B/C/D + M1-KUBEVIRT-LIVE-A/B/C/D** ⭐ | 已完成 local profile：create/get/list/delete + kubeconfig + proxy + node-pools；已完成 vCluster Helm/kubeconfig/upgrade provider 代码边界、Cluster API/CAPK node pool provider 代码边界、真实 CAPI schema hardening 与 CAPK refs 配置能力、proxy forwarding adapter、target resolver/store、metadata 持久化、Gateway router 注入接线、forwarding_static/forwarding_metadata runtime 选择、`K8S_CLUSTER_NODE_POOL_PROVIDER_MODE=clusterapi_kubernetes_rest` 接线、vCluster live contract gate、vCluster live evidence JSON 输出和真实 lab Helm/kubeconfig/Core proxy live result、node pool live contract gate、node pool evidence JSON 输出与 CAPK create/scale real lab result、vCluster upgrade live contract gate、vCluster upgrade evidence JSON 输出和真实 lab upgrade live result、Kube-OVN network live contract gate、evidence JSON 输出和真实 lab resource/external LoadBalancer real result、KubeVirt VM live contract gate、evidence JSON 输出、真实 lab VM lifecycle live result 与 console/VNC WebSocket session result、三节点 GPU 调度真实验证；CAPK node pool 不代表 VM 内 GPU passthrough/vGPU 已完成；vCluster Core proxy 本次经本机 kubectl proxy 转发，不代表生产 per-cluster metadata target/KMS token 管理已完成；vCluster upgrade 本次目标版本为当前 chart 默认的 `v1.35.0`，不宣称跨小版本升级策略已生产化 | 高 | 6天 | Services K8s集群服务；租户 kubectl/Helm 工具链；网络真实 provider；VM 真实 provider |
| **M1-RECONCILE-A/B/C/D/E + LIVE-A/B/C** | 已完成基础闭环：WorkloadReconcileController adapter/capability + 默认关闭的 bootstrap opt-in 后台 goroutine + 目标级失败退避、计数快照、`/metrics` Prometheus text 指标导出、独立 worker 进程形态、metadata-backed leader election 代码边界、`validate-reconcile-ha-live-gate` contract 门禁、controller HA evidence JSON 输出和 REAL-K8S-LAB-A 多副本 live HA failover 真实执行结果；本次 live gate 使用最小依赖和 hostPath worker 二进制，不代表生产 Helm/Operator 化控制面部署已完成 | 高 | 4天 | 生产级状态一致性保证 |
| **M1-ENCRYPT-A/B/C/D + LIVE-A/B/C** | 已完成 local profile：encryption keys create/get/list/delete + seal + unseal-token + rotate + revoke；已完成 KMS/SM4 HTTP provider 代码边界、对象内容 SM4-GCM 流式加解密代码边界、`validate-kms-sm4-live-gate` contract 门禁、KMS/SM4 evidence JSON 输出和真实 lab Core provider + SM4-GCM streaming + objectstore round trip live result；本次使用 live-gate fixture，不代表生产 KMS/对象存储部署形态完成 | 中 | 3天 | Services 模型仓库加密功能 |
| **M1-SECRETS-A/B/C/D + LIVE-A/B/C/D** | 已完成 local profile：Secret CRUD + bindings；已完成 Kubernetes Secret provider 写入代码边界、容器/Job Secret binding env/file manifest 注入代码边界、VM Secret binding volume manifest 注入代码边界、`validate-secrets-live-gate` contract 门禁、Kubernetes Secret evidence JSON 输出、真实 lab Secret live result 和 KubeVirt VM guest 内读取 Secret volume 真实执行结果；覆盖 env/file/VM 注入检查的当前可验证范围 | 中 | 2天 | Services PaaS 凭据注入 |

> M1-SANDBOX-A 移到 Sprint 6，腾出时间给 K8S-A。Sandbox 不在 Services P0 关键路径上。

**完工标准：**
```bash
make test
# POST /api/v1/k8s-clusters → 创建 vCluster，状态变为 running
# GET /api/v1/k8s-clusters/{id}/kubeconfig → 返回可用 kubeconfig
# kubectl --kubeconfig=<returned> get pods -A → 正常返回
# Reconcile Controller 独立扫描 workload_instances 并更新状态（不依赖 API 调用）
# POST /api/v1/encryption/seal → 返回 unseal-token
```

**当前代码校准（2026-06-03）：** 上述 Sprint 5 real path live gate 已具备当前可验证证据。当前满足 `POST/GET/LIST/DELETE /api/v1/k8s-clusters`、`GET /api/v1/k8s-clusters/{id}/kubeconfig`、`POST /api/v1/k8s-clusters/{id}/proxy`、`POST /api/v1/k8s-clusters/{id}/upgrade`、`CRUD /api/v1/k8s-clusters/{id}/node-pools` 的 local dev profile，且已有 vCluster Helm/kubeconfig/upgrade provider 代码边界、Cluster API/CAPK node pool provider 代码边界、真实 CAPI schema hardening 与 CAPK refs 配置能力、`validate-vcluster-live-gate`、`validate-vcluster-upgrade-live-gate`、`validate-k8s-node-pool-live-gate` contract 门禁与 node pool evidence JSON 输出、vCluster Helm/kubeconfig/Core proxy 真实 lab live result、vCluster upgrade 真实 lab live result、CAPK node pool create/scale real lab result、`validate-kubeovn-network-live-gate` contract 门禁与 Kube-OVN network evidence JSON 输出、Kube-OVN network 真实 lab resource live result、Kube-OVN external LoadBalancer IP 可达性真实执行结果、`validate-kubevirt-vm-live-gate` contract 门禁与 KubeVirt VM evidence JSON 输出、KubeVirt VM lifecycle 真实 lab live result、KubeVirt console/VNC WebSocket session 真实执行结果、可注入 resolver 的 proxy forwarding adapter、本地 per-cluster target resolver/store、metadata 持久化 store、Gateway router 注入接线、forwarding_static/forwarding_metadata runtime 选择和 `K8S_CLUSTER_NODE_POOL_PROVIDER_MODE=clusterapi_kubernetes_rest` 接线；当前满足 `POST/GET/LIST/DELETE /api/v1/encryption/keys`、`POST /api/v1/encryption/seal`、`POST /api/v1/encryption/unseal-token` 的 local dev profile，并已有 KMS/SM4 HTTP provider 代码边界、Gateway `ENCRYPTION_PROVIDER_MODE=kms_sm4_http` runtime 选择、对象内容 SM4-GCM 流式加解密代码边界、`validate-kms-sm4-live-gate` contract 门禁、KMS/SM4 evidence JSON 输出和真实 lab Core provider + SM4-GCM streaming + objectstore round trip live result；当前满足 `POST/GET/LIST/DELETE /api/v1/secrets`、`POST /api/v1/secrets/{id}/bindings` 的 local dev profile，以及 Kubernetes Secret provider 写入代码边界、Gateway `SECRET_PROVIDER_MODE=kubernetes_rest` runtime 选择、容器/Job Secret binding env/file manifest 注入代码边界、VM Secret binding volume manifest 注入代码边界、`validate-secrets-live-gate` contract 门禁、Kubernetes Secret evidence JSON 输出、真实 lab Secret live result 和 KubeVirt VM guest 内读取 Secret volume 真实执行结果；WorkloadReconcileController 默认关闭的 bootstrap opt-in 后台运行剖面、目标级失败退避、计数快照、`/metrics` Prometheus text 指标导出、独立 worker 进程形态、metadata-backed leader election 代码边界、`validate-reconcile-ha-live-gate` contract 门禁、controller HA evidence JSON 输出和真实 lab controller HA failover live result 也已完成；Kube-OVN external LB 本次使用 live-gate helper 镜像/脚本兼容方案，不代表生产镜像供应链或 Helm/Operator 化部署完成；CAPK node pool 本次证明 VM worker create/scale-ready，不代表 VM 内 GPU passthrough/vGPU 已完成；vCluster Core proxy 本次经本机 kubectl proxy 转发，不代表生产 per-cluster metadata target/KMS token 管理已完成；vCluster upgrade 本次目标版本为当前 chart 默认的 `v1.35.0`，不宣称跨小版本升级策略已生产化；Secret provider 本次经本机 kubectl proxy 访问 Kubernetes API，不代表生产 Kubernetes API credential 管理已完成；Controller HA 本次使用最小 live gate 依赖和 hostPath worker 二进制，不代表生产 Helm/Operator 化控制面部署已完成；KMS/SM4 本次使用 live-gate fixture，不代表生产 KMS/对象存储、直连 TLS/credential 管理或平台化部署完成。

**解锁：** Services K8s集群服务；Services 模型加密功能；生产级状态一致性

---

### Sprint 6：2026-08-01 → 2026-08-15

**主题：Sandbox + 平台支撑 + Services P0 核心（模型仓库/推理服务）**

**Core 任务（本小组）：**

| 批次 | 内容 | 难度 | 预估 | 解锁对象 |
|---|---|---|---|---|
| **M1-SANDBOX-A** | Sandbox 实例类型（已完成 local profile；真实 Kata provider 待后续） | 高 | 5天 | Services Agent 运行时 |
| **M1-OBS-A** | PromQL 代理查询 + 基础告警规则 CRUD（已完成 local profile） | 中 | 3天 | Services 推理监控 |
| **M1-METER-A** | 实例用量 + Token 用量上报（已完成 local profile） | 中 | 2天 | Services 计费 |
| **M1-REGISTRY-A** | 镜像仓库 API（已完成 local profile；真实 Harbor/Trivy provider 待后续）| 中 | 2天 | Services 镜像仓库服务 |
| **Core E2E** | Sprint 1-5 全链路集成测试回归 | 中 | 3天 | RC 门控 |

**Services 任务（另一小组，已在 06-30 前后解锁，本 Sprint 进入真实开发加速期）：**

| 批次 | 依赖的 Core API | 内容 |
|---|---|---|
| **SVC-MODEL-A** | `/api/v1/objects`（S3）+ `/api/v1/encryption`（SM4）| 模型仓库：上传/版本/元数据/国密加解密/HuggingFace 导入 |
| **SVC-INFER-A** | `/api/v1/instances`（kind=gpu-container）+ `/api/v1/k8s-clusters`（vCluster 中部署 vLLM）| 推理服务：端点部署/状态/日志/OpenAI 兼容 API |

> **2026-06-04 校准**：kb-service 属于外部 Services 团队范围，本仓库 Sprint 7 不开发 SVC-KB-A；Core 只按外部定义补齐所需 Core API/SDK 能力。

**完工标准：**
```bash
# Core
make test  # 全通（含 E2E）
# POST /api/v1/instances {kind: "sandbox"} → 实例启动，exec 可运行 python 命令
# Services（另一小组验证）
# 模型文件上传 + SM4 加密 → 通过 Core objects API 写入对象存储
# 推理端点部署 + GET /v1/chat/completions → 得到 LLM 回答
```

---

### Sprint 7：2026-08-16 → 2026-09-01

**主题：ANI Core-only installer + 离线包 + Core CLI + 真实回归门禁**

> 2026-06-04 校准：Sprint 7 Core-only 代码开发已完成。旧路线图中的知识库 RAG、Console Alpha、model-service、kb-service、ai、operators 和 frontends 均不在本仓库执行范围；外部 Services 团队负责业务能力与前端。本仓库只按 Core OpenAPI/SDK 缺口补齐基础设施支撑能力。

**Core 任务：**

| 批次 | 内容 | 难度 | 预估 |
|---|---|---|---|
| **CORE-INSTALLER-A** | ✅ 已完成：ani-installer 三种 Core profile + validator；不宣称 production ready | 高 | 5天 |
| **CORE-OFFLINE-A** | ✅ 已完成：Core 镜像、Helm chart、脚本清单 manifest + validator；不把 Services 业务镜像纳入本仓库交付 | 中 | 3天 |
| **CORE-CLI-A** | ✅ 已完成：`ani` Core CLI 最小资源覆盖，复用 Core REST 契约，不新增 Services 命令 | 中 | 3天 |
| **CORE-REGRESSION-A** | ✅ 已完成：Sprint 7 regression profile 固定 installer/offline/CLI/history gates；不新增 `M1-REAL-LAB-*` guard | 中 | 2天 |

**Services / UI 任务：**

不在本仓库执行。知识库 RAG、Console Alpha、BOSS、model-service、kb-service、ai、operators 和 frontends 均由外部 Services/产品团队负责；本仓库不得基于猜测提前开发这些目录或把 Services 业务资源回流到 Core API。

**完工标准：**
```bash
# Core
make test
make validate-architecture
make validate-core-api-compatibility
make validate-doc-entrypoints
make validate-sdk-beta
make validate-sdk-mock-smoke
git diff --check
# 当前完成范围：contract/local validation + CLI minimal behavior
# 不宣称 15 分钟安装、离线包可交付、CLI 全资源覆盖或 production ready
```

---

### Sprint 8：2026-09-01 → 2026-09-15

**主题：ANI Core 收尾/发布前加固**

> 2026-06-04 校准：Console、BOSS、RAG、model-service、kb-service、ai、operators 和 frontends 均不在本仓库执行范围；如外部 Services/产品团队需要 Core 支撑，本仓库只通过 Core OpenAPI/SDK/CLI 补齐基础设施能力。

> 2026-06-04 收敛：Sprint 8 Core-only 代码开发已完成，当前结果为 contract/local validation，不代表真实安装、离线包签名交付、CLI 发布或 production ready。

**Core 任务：**

| 批次 | 内容 |
|---|---|
| **CORE-HARDEN-A** | ✅ 已完成：Core release hardening profile + validator |
| **CORE-INSTALLER-LIVE-A** | ✅ 已完成：installer live-readiness profile；不宣称真实安装完成 |
| **CORE-OFFLINE-PACK-A** | ✅ 已完成：offline package lock；不宣称离线包已签名交付 |
| **CORE-CLI-B** | ✅ 已完成：扩展 Core CLI 主要只读资源，继续拒绝 Services 业务资源 |
| **CORE-DOC-CONSISTENCY-A** | ✅ 已完成：代码、Makefile、入口文档和 development records 一致性 gate |

**完工标准：**
```bash
make test
make validate-architecture
make validate-core-api-compatibility
make validate-doc-entrypoints
make validate-sdk-beta
make validate-sdk-mock-smoke
make validate-core-installer
make validate-core-offline
make validate-core-cli
make validate-sprint7-core-regression
make validate-core-release-hardening
make validate-core-installer-live
make validate-core-offline-pack
make validate-core-doc-consistency
make validate-sprint8-core-release
git diff --check
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

### Sprint 10：计划窗口 2026-09-26 → 2026-09-30（实际 2026-06-04 Core-only 完成）

**原始主题：v1.0.0 发布窗口 → 实际为 release-prep readiness，`v1.0.0 尚未发布`（不得标 v1.0.0/RC）。**

```bash
# 实际完成：CORE-ARTIFACT-MANIFEST-A / CORE-VERSION-POLICY-A / CORE-FINAL-READINESS-A 等 release-prep 门禁
make validate-sprint10-release-prep
# 原始计划项（离线包发布、文档站发布、标杆客户验收）仍属 v1.0.0 发布时执行，当前未发布
```

> 以下 Sprint 11–14 为原始排期之后实际推进的冲刺；详细真实状态与 evidence 以「[零、状态快照](#零状态快照先读这里)」和 [`repo/CURRENT-SPRINT.md`](repo/CURRENT-SPRINT.md) 为准，本节只给摘要与验收入口。

### Sprint 11：Core 真实部署验证 + Rook-Ceph 正式部署（✅ 已完成，2026-06-05，转历史回归门禁）

**主题：** 三台物理服务器首次真实部署验证；Rook-Ceph 正式块存储部署（CephCluster `Ready/HEALTH_OK`、5 个 SSD OSD、`ani-rbd-ssd` StorageClass、RBD/VM smoke、逐节点 reboot resilience）。明细见 Section 零「当前真实底座环境状态」与 dev-records。

```bash
make validate-sprint11-real-deployment
make validate-sprint11-core-doc-consistency
```

### Sprint 12：Core「Services 支撑 Handler」补齐（✅ Core-only 已完成，2026-06-19，Tier1 local profile）

**主题：** 补齐 19 个 Core handler + 2 个 422（observability / netstore / objvec），逐个关联 OpenAPI operationId、`pkg/ports`、`pkg/adapters`、Gateway handler；仅 Tier1 local profile，不代表 runtime/production ready。契约改动见 [`repo/api/core-contract-changelog-sprint12-13.md`](repo/api/core-contract-changelog-sprint12-13.md)。

```bash
make validate-architecture
make test
```

### Sprint 13：真实 provider / live gate 收敛（🔄 收敛中：S01–S07 production-shaped gate passed）

**主题：** 在 Sprint 12 已闭合的 `pkg/ports` / `pkg/adapters` / Gateway handler 边界接入真实组件（S01 Kube-OVN、S02 vCluster、S03 Rook-Ceph、S04 NVIDIA device-plugin/DCGM、S05 MinIO、S06 Milvus、S07 Prometheus observability），形成可复跑 live gate 与 evidence JSON。`production-shaped acceptance passed` ≠ `full platform production ready`。计划见 [`repo/development-records/sprint13-real-provider-readiness-plan.md`](repo/development-records/sprint13-real-provider-readiness-plan.md)；当前进度以 [`repo/CURRENT-SPRINT.md`](repo/CURRENT-SPRINT.md) 为准。

```bash
make validate-sprint13-b-track-production-shape
```

### Sprint 14：Core 韧性与服务语义（✅ feature branch complete，2026-06-23）

**主题：** 在 Sprint 13 production-shaped provider 基础上补齐 Core 运行期韧性与服务语义。P0 覆盖 gateway shared store、限流、幂等重放、adapter per-call timeout、data-plane readyz；P1 覆盖 retry/circuit breaker foundation 与 strong/weak dependency degradation；P2 覆盖 Redis Sentinel/Cluster 配置、MinIO/Milvus endpoint list fallback 和 controller primary kill / follower failover 验证。

**关联记录：** 主计划见 [`repo/development-records/sprint14-core-resilience-plan.md`](repo/development-records/sprint14-core-resilience-plan.md)，批次索引见 [`repo/development-records/README.md`](repo/development-records/README.md)，真实 aggregate live gate 完成记录见 [`repo/development-records/r-sprint14-resilience-live-gate.md`](repo/development-records/r-sprint14-resilience-live-gate.md)，接口契约影响说明见 [`repo/api/core-contract-changelog-sprint14.md`](repo/api/core-contract-changelog-sprint14.md)。

**完成状态：** `feature/sprint14-core-resilience-semantics` 已完成 R-P0-0..R-P2-7，新增 `SPRINT14-CORE-RESILIENCE-LIVE-GATE` 并在 `ani-sprint14-resilience` 隔离 namespace 真实执行：

- P0：Redis strong backend kill → readyz fail / HTTP 503 → 恢复 `ok`。
- P1：MinIO/object-store weak backend kill → readyz `degraded` / HTTP 200 → 恢复 `ok`。
- P2：删除当前 reconcile worker primary pod → follower 接管 metadata-backed lease → 最终 readyz `ok`。

**Evidence 与边界：** evidence 位于 `repo/development-records/live-evidence/sprint14-resilience-live-evidence.json`，已脱敏；production-ready 范围仅限隔离 Sprint14 Core resilience fixture。该结论不把现有 Sprint13 单副本后端标为自身 HA，不替代 Redis/Postgres/MinIO/Milvus 生产 Operator 拓扑，不代表 full platform production ready。

```bash
make validate-sprint14-resilience-live-gate
python scripts/validate_yaml.py deploy/real-k8s-lab/sprint14-resilience-live-gate.yaml deploy/real-k8s-lab/sprint14-resilience-live-fixture.yaml
make test
make validate-architecture
make validate-doc-entrypoints
git diff --check
```

---

### ANI Services P0 临时范围定义（待 2026-06-15 至 2026-06-20 重置）

> 本节保留 2026-05-15 会话形成的 Services 初始范围，用于说明历史规划和 Core 依赖；它不再承担 ANI Services 交付边界的最终定稿职责。
> ANI Services 由另一小组开发，全部通过 ANI Core OpenAPI REST API / Core SDK 实现；不得直接调用 Core 内部 gRPC service 或底层组件 SDK。
> 2026-06-15 至 2026-06-20，Services 团队必须输出完整前端功能、Services 功能和接口定义；评审通过后，Repo 中旧 Services 逻辑按新定义删除或覆盖。
> 代码位置：`repo/services/`、`repo/ai/`、`repo/operators/inference-operator/`、`repo/frontends/console/` 中存在早期 Services 逻辑或骨架，均不得被 Core 调用，也不得当成最终 Services 边界。

#### 域A：IaaS 云服务（基于 Core instances/networks/volumes API）

| 服务 | v1.0.0 P0 范围 | 依赖 Core Sprint |
|---|---|---|
| 云主机/容器/GPU实例控制台 | 创建/生命周期/运维的 Console UI | Sprint 1~2 |
| **K8s 集群服务** | vCluster 创建/kubeconfig/升级/节点池/原生 API 代理；kubectl/Helm 兼容 | **Sprint 5（M1-K8S-A/B/C/D/E/F/G + M1-K8S-LIVE-A/B/C/D/E/F/G/H/J/K/L + M1-K8S-PROXY-A/B/C/D/E/F，当前完成 local CRUD+kubeconfig+proxy+upgrade+node-pools 切片、vCluster Helm/kubeconfig/upgrade provider 代码边界、Cluster API node pool provider 代码边界、真实 CAPI schema hardening、CAPK refs 配置能力、proxy forwarding adapter、target resolver/store、metadata 持久化、Gateway router 注入接线、forwarding_static/forwarding_metadata runtime 选择、vCluster Helm/kubeconfig/Core proxy 与 vCluster upgrade 真实 lab live result、三节点 GPU 调度真实验证、`K8S_CLUSTER_NODE_POOL_PROVIDER_MODE=clusterapi_kubernetes_rest` 接线、`validate-k8s-node-pool-live-gate` contract 门禁和 node pool evidence JSON 输出）** |
| VPC/子网/安全组管理 | CRUD Console UI | Sprint 3 |
| 块存储/文件存储/对象存储 | CRUD Console UI | Sprint 3 |
| 镜像仓库服务 | Harbor 镜像浏览/推拉权限 | Sprint 6（M1-REGISTRY-A）|

#### 域B：AI 全生命周期（对标 AWS SageMaker）

| 服务 | v1.0.0 P0 范围 | 依赖 Core Sprint | 代码现状 |
|---|---|---|---|
| **模型仓库** | 上传/版本/元数据/SM4加解密/HuggingFace导入 | Sprint 5（加解密；当前只完成 keys CRUD local 切片）| model-service 有实现，需划清边界 |
| **推理服务** | 端点部署/状态/日志/OpenAI 兼容 `/v1/chat/completions` | Sprint 4（API冻结）| 从零建 |
| Notebook | JupyterLab 托管（P1，v1.x）| — | 未建 |
| 训练/微调 | LoRA 微调（Phase 2）| — | 未建 |
| AI API 网关 | Token 计费/限流（P1）| Sprint 6（计量）| 未建 |

#### 域C：AI-Native 应用

| 服务 | v1.0.0 P0 范围 | 依赖 Core Sprint | 代码现状 |
|---|---|---|---|
| **知识库/RAG** | 文档上传→解析→向量化→混合检索→问答→来源引用 | Sprint 3（vector-stores）| **kb-service 完全空，从零建** |
| Agent 运行时 | 基础沙箱会话管理（P1）| Sprint 6（Sandbox）| 未建 |
| 文档智能/会议智能 | Phase 2 | — | 未建 |

#### 域D：PaaS 托管服务

| 服务 | v1.0.0 P0 范围 | 依赖 Core Sprint |
|---|---|---|
| 托管数据库/消息队列 | **Phase 2**，v1.0.0 不做 | — |
| 函数计算 | Phase 2 | — |

#### 重要边界说明（防止越界）

1. **旧 Services 逻辑不再定义目标边界**：model-service、空 kb-service、RAG 原型、推理 operator 骨架和当前前端单 API Client 都只能作为历史参考；6.15-6.20 Services 定义通过后，冲突部分删除或覆盖。
2. **ANI Services 只能调用 Core API/SDK**：对象存储、加解密、K8s、网络、存储、向量存储等基础能力必须经 Core OpenAPI REST API / Core SDK 使用，不得 import `pkg/ports/`、Core 内部包、直接调用 Core 内部 gRPC service 或绕过 Core 直接操作底层组件。
3. **Services API 单独维护**：`models`、`inference-services`、`knowledge-bases` 等业务资源只能维护在 `repo/api/openapi/services/v1.yaml`，不得回流到 Core `repo/api/openapi/v1.yaml`。

---

### v1.0.0 交付验收单（9 月 30 日前必须全部打勾）

```
ANI Core：
  [ ] make build && make test 通过（含 E2E）
  [ ] /healthz + /readyz 所有服务可用
  [ ] VM / 容器 / GPU 容器 全生命周期（含 operation_id + 时间线）
  [ ] **K8s 集群（vCluster）创建/kubeconfig/原生 API 代理**  ← 恢复 v1.0.0
  [ ] Sandbox 实例可 exec 命令
  [ ] VPC / 子网 / 安全组 CRUD
  [ ] 块存储 / 文件存储 / 对象存储 CRUD
  [ ] 向量存储 API
  [ ] 国密 SM4 加解密（seal/unseal）
  [x] Secrets API + 容器/Job/VM manifest 绑定注入代码边界，且 Secret live gate（含 VM guest Secret volume 可见性）已通过
  [x] Workload Identity（lifecycle-bound scoped API key P0）
  [x] WorkloadReconcileController 默认关闭的可配置后台运行
  [x] WorkloadReconcileController metadata-backed leader election 代码边界和 `M1-RECONCILE-LIVE-A/B/C` / `validate-reconcile-ha-live-gate` contract 门禁、evidence JSON 输出与 REAL-K8S-LAB-A 多副本 live HA failover 真实执行结果（退避、`/metrics` 指标、独立 worker、`control_plane_leases` 和 HA failover 检查步骤已覆盖；本次 live gate 不代表生产 Helm/Operator 化控制面部署已完成）
  [x] 镜像仓库 API local profile（Harbor/Trivy real provider 未完成）
  [x] 用量计量 API local profile（真实 metering/billing backend 未完成）
  [x] 可观测性 API local profile（PromQL 查询 + 告警规则；Prometheus/Alertmanager real provider 未完成）
  [x] Core API 契约 v1.yaml + 兼容性基线生效
  [x] Go SDK + Python SDK + TypeScript Client + Java SDK 生成与 SDK smoke gates
  [x] ani-installer 三种 profile contract + Core offline package manifest contract（真实安装、签名、客户现场交付未完成）
  [ ] 信创基线（ARM64 构建通过）

ANI Services P0（外部 Services/产品团队负责，不在本仓库开发）：
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
- `2026-06-10`：Core API Alpha Freeze ← Services P0 依赖接口开始稳定，不可推迟
- `2026-06-30`：Core Dev Profile Ready + SDK Alpha ← Services 团队正式解锁，不可推迟
- `2026-08-31`：Core Integration RC ← Services 依赖缺口清零，进入全项目联调
- `2026-09-15`：Core + Services Release Candidate，只允许修 bug、安全、部署、文档
- `2026-09-30`：v1.0.0 交付

---

## 三、功能规格参考（Services 团队 + 前端实现依据）

> **关于本节的阅读说明：**
>
> ANI-06 现在包含两套不同性质的内容，用途不同，请注意区分：
>
> | 内容 | 位置 | 用途 |
> |---|---|---|
> | 10个双周冲刺 | **Section 二** ← 主线 | 开发进度追踪（先读这里）|
> | 开发批次归档 | `repo/development-records/README.md` | 已完成工作的索引 |
> | **本节（Section 三）** | Section 三 | **功能规格参考**：描述产品应该做什么，供实现时查阅 |
>
> - `- [x]` 表示该功能已实现（代码存在）
> - `- [ ]` 表示该功能在当前或未来冲刺计划中（尚未实现）
> - **本节不用于追踪进度，进度在 Section 二**

---

### 模块 1：基础设施底座（M1）✅ 已完成

**目标：** 在 K8s 1.36 上搭建完整 AI 平台底座，让 GPU 资源可被统一调度。

**完成状态：** 全部完成。完整批次记录见 `repo/development-records/README.md`

**已实现能力（简写）：** M1-INFRA-A/B/C/D/E/F + M1-GPU-A + M1-RUNTIME-A + M1-INSTANCE-A~S + M1-E2E-A/B + ARCH-ADAPTER 系列

**已完成批次完整列表：** → `repo/development-records/README.md`

#### 1.1 Kubernetes 集群

- [ ] **K8s 1.36 集群部署规范**
  - 节点规划：Master ×3（HA）、GPU 工作节点、存储节点
  - 安装方式：上游原生 Kubernetes 1.36 bootstrap，保持 API、RuntimeClass、CSI、CNI、CRD 语义与开源社区同步
  - 容器运行时：containerd 2.1+
  - 离线安装包制作：镜像预拉取 + 离线 Helm Chart 打包
  - **开源组件：** Kubernetes 1.36、containerd 2.1

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
  - Services 模型与推理能力不得直接绕过运行时抽象创建 Pod、Deployment 或 KubeVirt VM
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
  - **首轮实现已完成：** `M1-INSTANCE-T`（operation_id、timeline、幂等回放、操作查询）；危险操作二次确认和生产级并发幂等继续在后续批次收敛

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

### 模块 2：ANI Gateway（统一 Web Server 层）✅ 已完成

**目标：** 所有消费者的唯一入口，从这里衍生出 REST API、SDK、CLI、运维 Skills。

**完成状态：** Gateway 骨架、Middleware 链、Auth wiring 全部完成。Sprint 4 补齐 SDK 生成。

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
  ├── api/openapi/          # API 契约（契约先于实现）
  └── api/proto/            # Protobuf 定义
  ```
  - **框架：** Hertz 0.9+（CloudWeGo，字节开源，日万亿级请求生产验证）

- [ ] **Middleware 链**（按顺序执行）
  1. TLS 终止 + RequestID 注入（全链路唯一 ID）
  2. JWT 认证（验证 + 解析租户/用户信息）
  3. RBAC 授权（OPA 策略检查）
  4. 令牌桶限流（按租户维度，防止单一客户耗尽 GPU 资源）
  5. 审计日志打点（异步写入，不阻塞主流程）
  6. 路由分发 → 对应 Core 内部 service（可用 gRPC 实现，但不暴露为 Services 绕过 OpenAPI 的跨层契约）
  7. 统一错误响应：`{ code, message, request_id, details }`

- [ ] **API 契约优先工作流**
  - 所有 API 的契约定义先于代码，禁止反向
  - `make gen-api`：OpenAPI 生成 REST Server/Client 与 SDK 类型；buf/Protobuf/grpc-gateway 只服务 Core 内部 gRPC 实现和协议转译，不替代 OpenAPI 作为 Core/Services 控制面真实来源
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
  - **已完成：** M2.1-TASK-A/B/C（task-service + outbox），详见 `repo/development-records/README.md`

#### 2.2 认证授权（Go）（M2）✅ 已完成

> 已完成：M2.2-AUTH-A~K + M2.2-AUTH-FINAL（JWT/OIDC/JWKS/RBAC/API Key/Gateway Auth REST/Dex smoke）。
> 本节只保留能力定义；完成细节见 `repo/development-records/README.md` 和 `repo/development-records/m2-2-auth-final-production-closeout.md`。

- [ ] **Dex（OIDC IdP）**
  - 对接企业 AD/LDAP（客户现有用户体系，无需重建账号）
  - SAML 2.0 支持（金融/国央企常用）
  - **开源组件：** Dex latest
  - **完成记录（2026-05-18）：** Dex-compatible OIDC 自动化验收、issuer 默认端点推导、JWKS/ID Token 护栏、redirect_uri/state/nonce 防护、Gateway Auth REST 表面和 API 契约守卫均已闭环；`make validate-auth-dex-smoke`、`make build`、`make test`、`make validate-architecture`、`git diff --check` 已通过。

- [ ] **JWT 服务**
  - AccessToken（1 小时过期）+ RefreshToken（7 天）
  - Token 吊销：黑名单机制，Redis 存储
  - API Key 管理：长期 Token，供 CLI / SDK / 自动化脚本使用
  - **完成记录（2026-05-18）：** API Key scope 规范化、service-account scope allow/deny、rate limit、name/expires_at/rate_limit_rpm 创建护栏已完成并有回归测试。

- [ ] **RBAC 服务**
  - 角色：`platform-admin` / `tenant-admin` / `user` / `auditor`
  - 权限粒度：API 路径 + HTTP Method
  - 与 Dex 集成：从 OIDC Token 的 `groups` 字段提取角色
  - **完成记录（2026-05-18）：** OIDC group→role 映射已支持 group DN/path 归一化和配置角色 trim/lowercase 归一化，并保持白名单角色约束。

---

### 模块 3：Services 首批 AI 能力切片 ⏳ ANI Services — Sprint 6 实现（SVC-MODEL-A）

> **归属：ANI Services 层**（另一小组负责，调用 Core API）
> Sprint 6 中 SVC-MODEL-A 实现核心功能（依赖 Sprint 5 的加解密 API）。

**目标：** IT 管理员无需懂 AI，把模型文件变成一个可调用的内网 API。注意：模型仓库只是 ANI Services 的首批 AI 能力切片，不代表 ANI Services 的完整范围；完整范围以 2026-06-15 至 2026-06-20 输出的 Services 功能与接口定义为准。

#### 3.1 私有模型仓库（Go）

- [ ] **模型元数据服务**
  - 数据表：`models (id, name, version, format, size_bytes, status, is_encrypted, encrypt_algo, encrypt_hint, meta_json)`
  - Services API：`GET/POST /api/v1/svc/models`、`GET /api/v1/svc/models/{id}`、`DELETE /api/v1/svc/models/{id}/versions/{ver}`
  - 版本管理：同一模型多版本并存，支持 tag（latest / stable）
  - 能力标签：文本生成、嵌入、语音识别、视觉理解等

- [ ] **模型文件上传**
  - 分片上传 + 断点续传（支持 >100GB 大文件）
  - 通过 Core objects API/SDK 写入对象存储；底层 MinIO/S3 细节不得泄漏到 Services 业务代码
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
> 2026-06-04 边界：本节描述 Services 侧模型资产保护的历史/外部需求，不是当前 ANI Core CLI 任务。Core 已完成 KMS/SM4 provider streaming live gate 和 Core encryption API/SDK 边界；`ani model ...` 命令不在本仓库 Sprint 7 CLI 范围。

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
      2. 通过 Core objects API/SDK 获取模型对象读取地址并下载 .anip 文件
      3. 流式解密到 emptyDir（tmpfs 内存盘）
    主容器（vLLM）:
      4. 从 emptyDir 加载明文模型
    Pod 销毁时:
      5. emptyDir 随 Pod 消失，明文和密钥均不落盘
    ```
  - 密钥传递：用户通过 Console/API 提交密钥 → 转存为 K8s Secret → Init Container 通过环境变量读取

- [ ] **微调模型加密发布**
  - 微调完成后可选"加密后发布到仓库"
  - 工作流：微调完成 → 加密 API → 通过 Core objects API 写入加密文件 → 元数据标记 `is_encrypted=true`

#### 3.3 远程模型导入（Go + Python）

> 模型不预先打包进镜像，Pod 启动时从模型仓库动态拉取，实现镜像与模型彻底解耦。

- [ ] **HuggingFace 导入**
  - `POST /api/v1/svc/models/import` `{ source: "huggingface", repo_id: "Qwen/Qwen2.5-72B-Instruct" }`
  - 异步执行，返回 `task_id`，客户端轮询或 Webhook 通知进度
  - Python 下载服务：`huggingface_hub` 库，支持 `HF_ENDPOINT` 配置（指向国内镜像站）
  - 断点续传：记录已下载 shard，中断后从断点继续，不重下
  - 下载专属 Pod 开放外网出口（KubeOVN NetworkPolicy），其他 Pod 保持内网隔离
  - **开源组件：** huggingface_hub latest

- [ ] **ModelScope 导入**
  - `POST /api/v1/svc/models/import` `{ source: "modelscope", model_id: "qwen/Qwen2.5-72B-Instruct" }`
  - 使用 `modelscope` Python SDK
  - 共用 HuggingFace 的任务调度框架，逻辑一致
  - **开源组件：** modelscope latest

- [ ] **推理 Pod 模型动态加载**（Init Container 模式）
  ```
  vLLM 推理 Pod 启动时:
    Init Container（Go 单一二进制）:
      1. 检查节点 PVC 缓存是否已有该模型版本
      2. 如无缓存：调用模型仓库 API → 通过 Core objects API/SDK 获取对象读取地址 → 下载
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

### 模块 4：企业知识库问答 ⏳ ANI Services — Sprint 6~7 实现

> **归属：ANI Services 层**（另一小组负责，调用 Core vector-stores + objects API）
> 2026-06-04 校准：SVC-KB-A 不在本仓库 Sprint 7 执行范围；外部 Services 团队负责业务实现，本仓库只维护 Core API/SDK 支撑边界。

**目标：** Phase 1 核心交付物，业务用户最直接感知的 AI 能力，决定客户续费。

#### 4.1 文档管理（Go）

- [ ] **文档上传 API**
  - 格式：PDF、Word(.docx)、Excel(.xlsx)、PPT(.pptx)、TXT、Markdown
  - 文件通过 Core objects API/SDK 写入知识库文档对象空间，上传完成后由 Services 自有任务机制触发解析任务

- [ ] **文档解析服务（Python）**
  - **开源组件：** Docling（IBM 开源，PDF 版面分析 + 表格识别 + OCR 最完整）
  - OCR：PaddleOCR（中文准确率高于 Tesseract）
  - 输出：结构化 Markdown，保留标题层级和表格
  - 扫描件 PDF 走 OCR 路径，数字 PDF 直接提取不走 OCR

#### 4.2 RAG 引擎（Python）

- [ ] **向量化服务**
  - 嵌入模型：BGE-M3（BAAI，中英文双语效果最佳，免费开源）
  - 切片策略：语义边界切分（chunk ≈ 512 token，不硬截断段落）
  - 通过 Core vector-stores API/SDK 写入向量集合（底层 Milvus 细节封装在 Core adapter 内）
  - **开源组件：** sentence-transformers、Milvus 2.5+

- [ ] **混合检索**
  - 语义检索：通过 Core vector-stores search API 执行向量召回
  - 关键词检索：PostgreSQL pg_trgm 全文搜索（召回精确关键词）
  - 融合重排：RRF（Reciprocal Rank Fusion）算法，两路召回合并去重排序
  - Top-K：默认召回 5 段，可按知识库配置覆盖

- [ ] **问答生成**
  - Prompt 模板：系统提示词 + 检索上下文 + 用户问题
  - 来源引用：每段答案附来源文档名 + 页码（从向量检索 metadata 提取）
  - 置信度过滤：相似度低于阈值时返回"未找到相关内容"，不编造答案
  - 多轮对话：保留最近 10 轮历史，支持追问

- [ ] **知识库管理 API（Go）**
  - `POST /api/v1/svc/knowledge-bases` — 创建知识库
  - `POST /api/v1/svc/knowledge-bases/{id}/documents` — 上传文档
  - `GET /api/v1/svc/knowledge-bases/{id}/documents` — 文档列表及解析状态
  - `DELETE /api/v1/svc/knowledge-bases/{id}/documents/{doc_id}` — 删除文档
  - `POST /api/v1/svc/knowledge-bases/{id}/query` — 执行问答
  - 权限隔离：知识库归属租户，跨租户无法访问

---

### 模块 5：前端 Console ⏳ ANI Services — Sprint 7~8 实现

> **归属：ANI Services 层（前端）**，Sprint 7 Console Alpha，Sprint 8 全量。
> 依赖 Core API Alpha / Dev Profile 解锁后逐步替换 mock；Sprint 4 后进入稳定 SDK 和 API Beta 收口。

**目标：** IT 管理员和业务部门用户的操作界面，30 分钟能学会用。

#### 5.1 工程搭建

- [ ] **Monorepo 初始化**（Console + BOSS 共一个仓库）
  - pnpm workspace + Turborepo 构建缓存
  - Vite 5 + React 18 + TypeScript 5
  - TDesign React 1.x（腾讯开源企业组件库，中文友好，有 Mobile 版）
  - TanStack Router（类型安全路由 + 代码分割）
  - TanStack Query（服务端数据缓存与同步）
  - Zustand（轻量客户端 UI 状态）
  - 从 API 契约自动生成 TypeScript SDK（openapi-typescript-codegen）

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

### 模块 6：前端 BOSS ⏳ ANI Services — Sprint 8 实现

> **归属：ANI Services 层（BOSS 前端）**，Sprint 8 中 BOSS-A 实现基础版。

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

### 模块 7：CLI 工具 `ani` ✅ Sprint 7 minimal contract 已完成

> 当前仓库只维护 ANI Core CLI。Sprint 7 已新增 `repo/cli/ani` 最小实现，支持 Core REST base URL、bearer token、Core 资源只读请求和 Services 业务资源拒绝；不代表全资源覆盖或发布包。

- [x] **Sprint 7 最小子命令集**
  ```bash
  ani instances list
  ani k8s-clusters list
  ani secrets list
  ani registry-projects list
  ani metering-usage get
  ```
  - 已通过 `make validate-core-cli` 和 `make build-cli`
  - `model`、`kb`、`inference` 等 Services 业务命令不在本仓库实现

---

### 模块 8：可观测性（M1-OBS-A local profile 已完成，真实组件待后续）

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
| ANI Gateway | API 契约定义、安全边界审查 | Handler 骨架、Middleware 实现、错误处理 |
| 模型加密 | 算法选型、密钥安全设计 | SM4-GCM 流式加解密完整实现（基于 gmsm） |
| RAG 引擎 | Prompt 模板调优、检索策略 | LangChain Pipeline 代码、向量化服务 |
| K8s Operator | CRD 设计、状态机 | controller-runtime Controller 实现 |
| 所有 CRUD API | Spec 定义、权限设计 | Server Stub、Client SDK、单元测试 |
| 前端页面 | 交互逻辑、信息架构 | TDesign 组件拼装、TanStack Query hooks |
| CLI 工具 | 命令设计、用户体验 | cobra 子命令实现、帮助文档 |

---

## 七、开源组件选型清单

所有组件均满足：① 生产级成熟度 ② 符合 Go/Python/TS 技术栈 ③ 支持完全离线部署 ④ 有信创替代路径 ⑤ GitHub 社区热度、源码和文档质量足以支撑人类与 AI 协同开发、修 bug、运维和可替换路径。

组件选型不是追新，也不是只看 stars。每个 P0 默认组件都必须能回答：

- 社区是否足够成熟：GitHub stars、forks、contributors、release 频率、issue/PR 响应是否健康。
- 源码和文档是否足够 AI 可读：架构清晰、API 文档完整、运维文档丰富，便于 AI 生成测试、排障脚本和修复补丁。
- 是否方便运营运维：metrics、logs、health check、backup/restore、upgrade/rollback、离线部署是否可落地。
- 是否松耦合可替换：License、协议、数据迁移、替代组件、adapter 边界和回滚方式是否清楚。
- 是否避免新项目踩坑：新开源项目、维护者不稳定或文档薄弱的组件不得进入 P0 主链路，除非经过架构负责人批准并给出退出方案。

| 层级 | 组件 | 版本 | 选型理由 |
|---|---|---|---|
| 编排 | 上游原生 Kubernetes | 1.36 | 行业标准，与开源社区 API/RuntimeClass/CSI/CNI/CRD 语义同步，不绑定特定发行版 |
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

- [x] `M1-SANDBOX-A`：Sandbox 实例类型 local profile
  - Core OpenAPI `/instances` 支持 `kind`/`instance_type=sandbox`、`sandbox_config` 和 `sandbox` 响应摘要
  - `WorkloadRuntime` / `SandboxRuntime` 支持 `kind=sandbox` 产品意图边界
  - local adapter 返回 pending/running 状态机和 `dev_profile.real_provider=false`
  - 真实 Kata Containers daemonset、RuntimeClass 部署和 live gate 待后续 provider 批次证明

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

- [x] `M1-NETWORK-A`：VPC + 子网 + 安全组 + LB Core API 主链路
  - `POST/GET/DELETE /api/v1/networks/vpcs`
  - `POST/GET/DELETE /api/v1/networks/subnets`
  - `POST/GET/DELETE /api/v1/networks/security-groups`
  - `POST/GET/DELETE /api/v1/networks/load-balancers`
  - 已完成 dev/local profile、持久化边界、KubeOVN provider 渲染、dry-run/apply gate、状态读取和状态回写

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

- [x] `M1-STORAGE-A` 首个切片：块存储 + 文件存储 + 对象元数据 Core API dev profile
  - `CRUD /api/v1/volumes`
  - `CRUD /api/v1/filesystems`
  - `CRUD /api/v1/objects`
  - 已完成 API 契约、Gateway dev/local profile、租户隔离和合同守卫
  - 已完成 `StorageResourceStore`、metadata adapter、RLS 迁移和持久化单元测试
  - 已完成 `StorageProviderRenderer`、PVC manifest、objectstore metadata intent 和渲染单元测试
  - 已完成 `StorageProviderDryRun` / `StorageProviderApply`、Kubernetes PVC server-side dry-run、默认关闭 apply gate 和 objectstore 执行边界保留
  - 已完成 `StorageProviderStatusReader` / `StorageStatusReconciler`、PVC 状态读取、state/reason 映射和 metadata 回写

- [ ] `M1-STORAGE-B`：文件存储 API（新存储类型）
  - `POST/GET/DELETE /api/v1/filesystems`
  - `CRUD /api/v1/filesystems/{id}/mount-targets`（VPC 内 NFS 挂载点）
  - `GET /api/v1/filesystems/{id}/usage`
  - 底层：Rook-CephFS subvolume 或 NFS 导出

- [x] `M1-VSTORE-A`：向量存储 API（新 Core API 域）
  - `POST/DELETE /api/v1/vector-stores`（映射 Milvus Collection）
  - `POST /api/v1/vector-stores/{id}/search`（语义检索）
  - Milvus SDK 封装在 adapter，business layer 不直接调用
  - 已完成 Core API 契约、Gateway dev/local profile、租户隔离、search 响应结构和合同守卫

- [x] `SDK-ALPHA-A`：四语言 SDK Alpha 生成与 smoke
  - Core SDK 从 `api/openapi/v1.yaml` 生成
  - Services SDK 从 `api/openapi/services/v1.yaml` 生成
  - 已完成 Go/Python/TypeScript/Java 生成物、Core/Services 分层隔离和 `make validate-sdk-alpha`
  - Java smoke 在有 JDK 的环境执行 compile/run；当前本机缺少 Java Runtime 时降级为 source smoke

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

**目标：** 为租户提供完整的 K8s 集群生命周期管理 + 原生 API 代理，对标 AWS EKS / 原生 Kubernetes 管理体验。

**代码批次规划：**

- [ ] `M1-K8S-A`：vCluster 生命周期 API
  - [x] `POST /api/v1/k8s-clusters`（当前默认 local dev profile 创建；vCluster Helm provider 代码边界已完成，vCluster Helm/kubeconfig/Core proxy live gate 已在真实 lab 通过）
  - [x] `GET /api/v1/k8s-clusters/{id}`（当前返回 local profile 状态/版本）
  - [x] `GET /api/v1/k8s-clusters`（当前返回租户隔离的 local profile 列表）
  - [x] `DELETE /api/v1/k8s-clusters/{id}`（当前标记为 deleting，不是真实删除 vCluster）
  - [x] `POST /api/v1/k8s-clusters/{id}/upgrade`（升级 K8s 版本；当前完成 local 幂等版本更新、vCluster Helm `controlPlane.distro.k8s.version` upgrade intent 代码边界和 `M1-K8S-LIVE-C` / `validate-vcluster-upgrade-live-gate` contract 门禁；live 升级真实执行结果待后续）
  - `PUT /api/v1/k8s-clusters/{id}`（其它集群配置调整；节点池已拆为 `/node-pools` 子资源）
  - [x] `GET /api/v1/k8s-clusters/{id}/kubeconfig`（默认返回 local dev profile 模拟 kubeconfig；`vcluster_helm` provider mode 已具备 `vcluster connect --print` 代码边界，live kubectl 可用性验证待后续）
  - [x] `POST /api/v1/k8s-clusters/{id}/proxy`（当前为 Core 管控面 proxy local profile，不真实转发）

- [x] `M1-K8S-B / M1-K8S-PROXY-A/B/C/D/E/F / M1-K8S-LIVE-G`：原生 K8s API 代理（local profile、proxy forwarding adapter、本地 target resolver/store、metadata 持久化、Gateway router 注入接线和 forwarding_static/forwarding_metadata runtime 选择已完成；Core live proxy `/version` 已经通过本机 kubectl proxy 转发到 live vCluster API）
  - [x] `POST /api/v1/k8s-clusters/{id}/proxy` 契约 + local profile（method/path/query/body）
  - [x] runtime adapter 转发到 resolver 指向的 vCluster/K8s API Server
  - [x] Gateway router 可注入 forwarding-capable `ports.K8sClusterService`
  - [x] Gateway main 可通过 `K8S_CLUSTER_PROXY_MODE=forwarding_static` 选择 forwarding adapter
  - [x] Gateway main 可通过 `K8S_CLUSTER_PROXY_MODE=forwarding_metadata` 接入 per-cluster metadata resolver
  - ANI JWT 验证 → 路由到对应 vCluster → 返回原生 K8s 响应
  - 支持 kubectl、Helm、Argo CD 等原生工具链
  - 可观测：代理请求记录审计日志

- [ ] `M1-K8S-C / M1-K8S-F / M1-K8S-G / M1-K8S-LIVE-B`：节点池管理
  - [x] `CRUD /api/v1/k8s-clusters/{id}/node-pools` local profile
  - [x] 支持节点数、实例规格和 GPU intent 字段
  - [x] Cluster API `MachineDeployment` node pool provider 代码边界、真实 CAPI schema hardening、CAPK refs 配置能力和 Gateway `K8S_CLUSTER_NODE_POOL_PROVIDER_MODE=clusterapi_kubernetes_rest` 接线
  - [x] `M1-K8S-LIVE-B` / `validate-k8s-node-pool-live-gate` contract 门禁覆盖 Core node pool create/update、Cluster API `MachineDeployment` 观测和 GPU workload 调度验证步骤，并已支持 `--evidence-output` 归档 JSON 证据
  - [ ] 真实 provider 节点池扩缩容和 GPU 节点池 live 调度验证

- [ ] `M1-K8S-D`（P1）：Karmada 多集群联邦
  - `POST /api/v1/k8s-federation`（注册联邦，Karmada 控制面）
  - `CRUD /api/v1/k8s-federation/{id}/propagation-policies`
  - 支持跨集群工作负载分发

### 模块 M1-PLATFORM-SVC：平台支撑服务 API

**目标：** 补齐 PaaS 服务凭据注入、内部服务发现、计量等平台级能力缺口。

**代码批次规划：**

- [ ] `M1-ENCRYPT-A/B/C/D`：国密加解密 API
  - [x] `CRUD /api/v1/encryption/keys`（当前为 key metadata local dev profile，并已有 KMS/SM4 HTTP provider 代码边界和 live-gate fixture 真实验证）
  - [x] `POST /api/v1/encryption/seal`（当前返回 local dev profile sealed object URI 和 unseal token）
  - [x] `POST /api/v1/encryption/unseal-token`（当前生成 local dev profile 解密令牌，Init Container 真实集成待后续）
  - [x] `POST /api/v1/encryption/keys/{key_id}/rotate`（当前为 local dev profile，并已有 KMS/SM4 HTTP provider 代码边界；本轮 live gate 覆盖 key/seal/token 与 streaming/objectstore round trip，不代表生产 KMS rotation 验收）
  - [x] `POST /api/v1/encryption/keys/{key_id}/revoke`（当前为 local dev profile，revoked key 不再允许 seal 或 unseal-token）
  - [x] KMS/SM4 HTTP provider 代码边界（`ENCRYPTION_PROVIDER_MODE=kms_sm4_http`）
  - [x] 对象内容 SM4-GCM 流式加解密代码边界（reader/writer port + 本地 SM4-GCM chunk seal/open）
  - [x] `M1-ENCRYPT-LIVE-A/B` / `validate-kms-sm4-live-gate` contract 门禁与 evidence JSON 输出
  - [x] KMS/SM4 live-gate fixture 下的 Core provider、SM4-GCM streaming 和对象存储 round trip 端到端验收

- [ ] `M1-SECRETS-A/B/C/D + LIVE-A`：密钥管理 API
  - [x] `CRUD /api/v1/secrets`（当前为 local dev profile，KV 值只在 adapter 内部保存，响应不返回明文）
  - [x] Kubernetes Secret provider 写入代码边界（`SECRET_PROVIDER_MODE=kubernetes_rest`，live 写入验证待 REAL-K8S-LAB-A）
  - [x] `POST /api/v1/secrets/{id}/bindings`（当前为绑定意图记录，容器/Job manifest env/file 注入代码边界已完成；live 注入验证和 VM 注入待后续）
  - [x] `M1-SECRETS-LIVE-A/B/C/D` / `validate-secrets-live-gate` contract 门禁、evidence JSON 输出、真实 lab Secret live result 和 KubeVirt VM guest Secret volume 真实可见性结果
  - 底层：K8s Secret，ANI RBAC 多租户隔离

- [x] `M1-REGISTRY-A`：镜像仓库 API local profile
  - `GET /api/v1/registry/projects`
  - `GET /api/v1/registry/projects/{project}/repositories`
  - `GET /api/v1/registry/projects/{project}/repositories/{repository}/artifacts`
  - `POST /api/v1/registry/projects/{project}/repositories/{repository}/permissions`
  - `GET /api/v1/registry/images/scan-result?image=...`（local scan result）
  - local adapter 支持租户项目、seeded repository/artifact、权限幂等和 `dev_profile.real_provider=false`
  - 真实 Harbor/Trivy provider、镜像推拉凭证和扫描报告回读待后续 provider/live gate 证明

- [x] `M1-METER-A`：用量计量 API local profile
  - `GET /api/v1/metering/usage`（按租户/时间段/资源类型查询 local usage 聚合）
  - `POST /api/v1/metering/token-usage`（Services/控制面上报模型 Token 用量）
  - local adapter 支持 token input/output/total 聚合、幂等去重和 `dev_profile.real_provider=false`
  - 真实计量后端、账单系统和实例 CPU/GPU/内存采集待后续 provider/live gate 证明

- [x] `M1-OBS-A`：可观测性 API local profile
  - `GET /api/v1/observability/query`（PromQL 代理查询，不暴露 Prometheus 地址）
  - `CRUD /api/v1/observability/alert-rules`（告警规则管理）
  - local adapter 支持 PromQL query 空结果、告警规则 CRUD/idempotency 和 `dev_profile.real_provider=false`
  - 真实 Prometheus/Alertmanager provider 和告警动作待后续 provider/live gate 证明

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

<!-- 历史回归门禁校验器兼容标记（请勿删除；对应 dev-records 历史批次与 make validate-* 门禁） -->
**历史回归门禁 token（校验器兼容，勿删）：** SPEC-SPLIT-A、SPEC-CORE-BETA、SPEC-COMPAT-A、SDK-BETA-A、SDK-BETA-B、SDK-BETA-C、SDK-BETA-D、SDK-MOCK-SMOKE-A、SDK-MOCK-SMOKE-B、SDK-MOCK-SMOKE-C、SDK-MOCK-SMOKE-D、MOCK-A、DOC-API-A、SPRINT4-CLOSURE-A（`make validate-sprint4-closure`）；Sprint 11 / Core Real Deployment Validation 正式部署完成；真实服务器只读验证已完成；Rook-Ceph 正式部署已完成；Sprint 11 执行环境：正式部署执行环境。
