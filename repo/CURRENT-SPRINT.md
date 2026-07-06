# ANI · 当前冲刺上手指南

> 新开发者（人类或 AI 工具）的第一个入口文件。本文只描述当前真实执行状态；历史完成批次查 `repo/development-records/README.md`。

> **仓库范围：仅 ANI Core。** ANI Services 已冻结并移交外部产品团队，本仓库不再开发任何 Services 代码（旧 Services 骨架只读保留）。外部团队给出产品功能/交互/API 定义后，Core 只按 Core OpenAPI/SDK/CLI 缺口补齐基础设施支撑。
> **当前重心：Sprint 13 / Core real provider 与 live gate 收敛。** Sprint 12 已完成 Core「Services 支撑 Handler」A/B1/B2/B3 全部 19 个 handler + 2 个 422 的 Tier1 local profile 收口；当前只允许沿用这些 OpenAPI/ports/adapters/router 边界接入真实 provider 与 live gate。未跑通对应 live gate 前，不得标记 real-provider、runtime ready 或 production ready。RAG、Console、BOSS、model-service、kb-service、ai、operators、frontends 均不在本仓库执行范围内。
> **标准状态 marker：** 真实服务器只读验证已完成；Rook-Ceph 正式部署已完成。Sprint 11 执行环境：正式部署执行环境。

> **Sprint 13（当前活跃冲刺，2026-06-19 起）：** Core real provider 与 live gate 收敛。前置 Sprint 12 已闭合 19 个 Core handler + 2 个 422；Sprint 13 不重写 handler，不新增 Services 业务逻辑，而是在既有 `pkg/ports` / `pkg/adapters` / Gateway handler 边界接入真实组件，并形成可复跑 live gate 与 evidence JSON。计划见 [`development-records/sprint13-real-provider-readiness-plan.md`](development-records/sprint13-real-provider-readiness-plan.md)。

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
| **最后校准日期** | 2026-06-29 |

## 已完成：Gateway 元数据持久化（P0–P5）

| 切片 | 状态 | 证据 / gate |
|---|---|---|
| GATEWAY-METADATA-PERSISTENCE-P0 | 已完成（local/logic verified + PG 冒烟 PASS） | `gateway-metadata-persistence-p0.md`；`make validate-gateway-metadata-p0` |
| GATEWAY-METADATA-PERSISTENCE-P1 | 已完成（local/logic verified + PG 冒烟 PASS） | `gateway-metadata-persistence-p1.md`；`make validate-gateway-metadata-p1` |
| GATEWAY-METADATA-PERSISTENCE-P2 | 已完成（local/logic verified + PG 冒烟 PASS） | `gateway-metadata-persistence-p2.md`；`make validate-gateway-metadata-p2` |
| GATEWAY-METADATA-PERSISTENCE-P3 | 已完成（local/logic verified + PG 冒烟 PASS） | `gateway-metadata-persistence-p3.md`；`make validate-gateway-metadata-p3` |
| GATEWAY-METADATA-PERSISTENCE-P4 | 已完成（local/logic verified + PG 冒烟 PASS） | `gateway-metadata-persistence-p4.md`；Docker init + `PUT /branding` 写 PG |
| GATEWAY-METADATA-PERSISTENCE-P5 | 已完成（local/logic verified + PG 冒烟 PASS） | `gateway-metadata-persistence-p5.md`；Registry/Harbor 元数据写 PG；`make validate-gateway-metadata-p5` |
| GATEWAY-METADATA-PERSISTENCE-P6-A1 | 已完成（local/logic verified） | `gateway-metadata-persistence-p6-a1.md`；K8s 集群/节点池元数据写 PG；`make validate-gateway-metadata-p6-a1` |
| GATEWAY-METADATA-PERSISTENCE-P6-A2 | 已完成（local/logic verified） | `gateway-metadata-persistence-p6-a2.md`；Secret 元数据/绑定写 PG（无明文）；`make validate-gateway-metadata-p6-a2` |
| GATEWAY-METADATA-PERSISTENCE-P6-A3 | 已完成（local/logic verified） | `gateway-metadata-persistence-p6-a3.md`；branding OpenAPI + logo MinIO 上传；`make validate-gateway-metadata-p6-a3` |
| GATEWAY-METADATA-PERSISTENCE-P6-A4 | 已完成（local/logic verified） | `gateway-metadata-persistence-p6-a4.md`；Encryption key 元数据写 PG（无 key material）；`make validate-gateway-metadata-p6-a4` |
| GATEWAY-METADATA-PERSISTENCE-P6-B1 | 契约已闭合（live evidence 待 lab） | `gateway-metadata-persistence-p6-b1.md`；in-cluster Harbor gate；`make validate-gateway-metadata-p6-b1` |
| GATEWAY-METADATA-PERSISTENCE-P6-B2 | 契约已闭合（live evidence 待 lab） | `gateway-metadata-persistence-p6-b2.md`；Harbor artifact-track；`make validate-gateway-metadata-p6-b2` |
| GATEWAY-METADATA-PERSISTENCE-P6-B3 | 契约已闭合（live evidence 待 lab） | `gateway-metadata-persistence-p6-b3.md`；pull secret → K8s 注入；`make validate-gateway-metadata-p6-b3` |
| **聚合 gate** | P0–P5 + P6-A1–A4 已闭合 | `make validate-gateway-metadata`（顺序执行 P0–P3 + P5 + P6-A1–A4 sub-gate） |

边界：`DATABASE_URL` 未设置时保持内存模式；Redis 仍只做幂等/限流；不标 real-provider / production ready。数据库：`deploy/postgres/ani-dev-database-init.sql`（新 `make deps` 空卷）；已有卷 `make db-upgrade-gateway-metadata`。

### Gateway 元数据剩余项（P5 之后 backlog）

| 域 | 现状 | 建议后续 |
|---|---|---|
| K8s 集群 / 节点池业务元数据 | **P6-A1 已落 PG**（`k8s_clusters` / `k8s_cluster_node_pools`）；proxy 凭据仍 `k8s_cluster_proxy_targets` | 阶段 B：production-shaped Gateway |
| Encryption keys | **P6-A4 已落 PG**（`encryption_keys`；仅存元数据，无 key material / seal token） | 阶段 B |
| Secrets 业务元数据 | **P6-A2 已落 PG**（`secrets` / `secret_bindings`；仅存 key 名称与绑定，无明文） | 阶段 B |
| Branding / logo | **P6-A3 已闭合**（OpenAPI + MinIO 上传 + PG URL） | **P6-B1**：集群内 Gateway Harbor live gate（契约已闭合，evidence 待执行） |
| Harbor 真实 live gate | **本机 dev passed**（2026-06-30） | `validate-registry-harbor-live-gate`；`run_registry_harbor_live_gate.py`（B2 artifact / B3 pull-secret-k8s --dev） |
| Harbor in-cluster | **P6-B1 契约已闭合**（live 待 lab） | `run_registry_harbor_live_gate.py --track in-cluster`；NodePort `30080` |
| Registry repositories/artifacts 索引 | 不持久化（设计如此） | 如需离线列表再评估 PG 缓存层 |

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
| 镜像仓库 Harbor | Harbor v2.0 REST（2026-06-26） | `ports.ImageRegistry` / `harbor_image_registry.go` / `registry_runtime.go` | **SPRINT13-REGISTRY-HARBOR-A** + **SPRINT13-REGISTRY-HARBOR-LIVE-A** production-shaped live gate passed；**REGISTRY-MULTI-PROJECT-A** 租户多项目/自定义 name（Harbor `ani-{tenant}-{name}` 映射）；evidence 见 `sprint13-registry-harbor-live-result.md`；多项目 live 需复跑；不代表 full platform production ready |
| K8s REST 凭证解析 | kubeconfig + in-cluster 自动探测（2026-06-26） | `kubernetes_rest_config_resolve.go` / `kubernetes_env.go` / `kubernetes_runtime.go` / `NewKubernetesRESTClient` | **local dev 便利，非 production gate**（`SPRINT13-KUBERNETES-REST-CREDENTIAL-RESOLVER-A`；显式 `KUBERNETES_*` 仍优先；`KUBECONFIG` 自动加载仅供本地；不标 real-provider runtime/production ready） |
| Gateway 实例 workload 接线 | `WORKLOAD_PROVIDER=kubernetes_rest`（2026-06-30） | `workload_runtime.go` / `instance_workload_runtime.go` / `demo_instances.go` / `pkg/adapters/runtime` Secret+apply 修复 | **local/logic verified + 本机集群冒烟**（`GATEWAY-INSTANCE-WORKLOAD-RUNTIME-A`；gate：`validate-gateway-instance-workload-runtime`；对齐 M1-INSTANCE-P；**不新增 production-shaped live gate，不标 production ready**） |
| 实例网络选择 | Kube-OVN subnet annotation（2026-07-06） | `api/openapi/v1.yaml` / `demo_instances.go` / `dryrun_renderer.go` / `instance_store.go` | **local/logic verified + container live verified**（`INSTANCE-NETWORK-KUBEOVN-A`；`CreateInstanceRequest.network` 持久化到 `workload_instances`，Deployment/Job/KubeVirt template 注入 `ovn.kubernetes.io/logical_switch/ip_address`；真实 container Pod 在非重叠 `10.74.0.0/25` subnet 中 Running 且互 ping 通过；VM 为 manifest/template 单测覆盖，未标 VM live verified） |

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

- 本仓库只推进 ANI Core；Services/RAG/Console/BOSS/前端/推理/知识库业务均由外部团队负责。
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
make validate-gateway-metadata
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
