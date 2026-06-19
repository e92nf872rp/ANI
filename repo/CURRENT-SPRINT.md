# ANI · 当前冲刺上手指南

> 新开发者（人类或 AI 工具）的第一个入口文件。本文只描述当前真实执行状态；历史完成批次查 `repo/development-records/README.md`。

> **仓库范围：仅 ANI Core。** ANI Services 已冻结并移交外部产品团队，本仓库不再开发任何 Services 代码（旧 Services 骨架只读保留）。外部团队给出产品功能/交互/API 定义后，Core 只按 Core OpenAPI/SDK/CLI 缺口补齐基础设施支撑。
> **当前重心：Sprint 13 / Core real provider 与 live gate 收敛。** Sprint 12 已完成 Core「Services 支撑 Handler」A/B1/B2/B3 全部 19 个 handler + 2 个 422 的 Tier1 local profile 收口；当前只允许沿用这些 OpenAPI/ports/adapters/router 边界接入真实 provider 与 live gate。未跑通对应 live gate 前，不得标记 real-provider、runtime ready 或 production ready。RAG、Console、BOSS、model-service、kb-service、ai、operators、frontends 均不在本仓库执行范围内。
> **标准状态 marker：** 真实服务器只读验证已完成；Rook-Ceph 正式部署已完成。Sprint 11 执行环境：正式部署执行环境。

> **Sprint 13（当前活跃冲刺，2026-06-19 起）：** Core real provider 与 live gate 收敛。前置 Sprint 12 已闭合 19 个 Core handler + 2 个 422；Sprint 13 不重写 handler，不新增 Services 业务逻辑，而是在既有 `pkg/ports` / `pkg/adapters` / Gateway handler 边界接入真实组件，并形成可复跑 live gate 与 evidence JSON。计划见 [`development-records/sprint13-real-provider-readiness-plan.md`](development-records/sprint13-real-provider-readiness-plan.md)。

## 当前冲刺

| 字段 | 值 |
|---|---|
| **冲刺编号** | Sprint 13（Core real provider 与 live gate 收敛） |
| **主题** | 将 Sprint 12 已闭合的 Core handler/ports/local adapters 接到真实组件，并建立可复跑 live gate 与 evidence JSON |
| **当前状态** | Sprint 12 进入条件已满足：A/B1/B2/B3 全部 19 个 Core handler + 2 个 422 已完成并收口；Sprint 13 当前为真实 provider/live gate 收敛阶段，S01 网络路由 Kube-OVN、S02 K8s workloads vCluster、S03 storage Rook-Ceph 与 S04 GPU NVIDIA device-plugin/DCGM 已到 code+contract ready / LIVE PENDING |
| **执行环境** | 真实 provider 批次必须先声明组件与版本、live gate 命令、evidence 输出路径和失败边界；涉及真实服务器写操作前必须重新只读盘点并取得人工确认 |
| **已由代码/真实环境证明完成** | Sprint 12 证明了 contract + Tier1 local profile：B1 实例观测/GPU/Sandbox、B2 网络/存储/K8s workloads + 2 个 422、B3 对象/向量写入均经 OpenAPI、ports/adapters、Gateway handler 和测试闭合。Sprint 11/Sprint 5 提供真实 K8s/Kube-OVN/KubeVirt/vCluster/Rook-Ceph/GPU/CAPK 等历史 live evidence，可作为 Sprint 13 provider gate 的基础。 |
| **生产化边界** | Sprint 13 未执行具体 live gate 前，Sprint 12 能力仍只可标 Tier1 local profile；不得把计划、local profile 或历史相邻 live evidence 直接等同于当前能力 runtime/production ready |
| **关联历史门禁** | Sprint 5 REAL-K8S-LAB-A 和 live gate evidence；Sprint 7 installer/offline/CLI/regression gates；Sprint 8 release hardening/offline/CLI/doc gates；Sprint 9 RC readiness gates；Sprint 10 release-prep gates |
| **最后校准日期** | 2026-06-19 |

## Sprint 13 当前任务

1. `SPRINT13-REAL-PROVIDER-READINESS-PLAN`：已建立 Sprint 12 handler/ports/local adapters 到真实 provider/live gate 的代码关联计划；该文档是 Sprint 13 的执行地图，不是完成记录。
2. 已完成 S01 网络路由 Kube-OVN、S02 K8s workloads vCluster、S03 storage Rook-Ceph 与 S04 GPU NVIDIA device-plugin/DCGM A 轨；下一批从 S05 对象存储 bucket/upload/download 开始，仍只推进 code+contract ready / LIVE PENDING。
3. 每个 provider slice 必须先补 real adapter 或 provider runtime 选择，再补 live gate 和 evidence JSON，再更新对应 development record；未跑通前只保持 planning/local-profile 状态。
4. 持续执行驱动：[`development-records/sprint13-loop-execution-prompts.md`](development-records/sprint13-loop-execution-prompts.md) 提供 codex goal 持续循环提示与切片队列（S01–S07）。两轨道：**A 轨 loop-safe**（readiness/real adapter 代码/fake 单测/契约级 live-gate/文档闭环/提交，可自动）；**B 轨 human-gated**（真实集群写、组件部署、真实 live gate evidence、real-provider 标记，必须人工先只读盘点 + 确认）。循环只跑 A 轨，把切片推进到「code+contract ready, LIVE PENDING」。

## Sprint 13 执行矩阵

| 候选切片 | 真实组件方向 | 代码边界 | 当前状态 |
|---|---|---|---|
| 实例观测 | Prometheus + kubelet / K8s API（已选 2026-06-19） | `ports.InstanceObservability`，Gateway handler 不绕过 port | 待拆分执行 |
| GPU 清单/占用 | NVIDIA device-plugin / DCGM / node labels | `ports.GPUInventory`，复用 Sprint 5 GPU evidence 作为前置事实 | **code+contract ready, LIVE PENDING**（`SPRINT13-GPU-INVENTORY-DCGM-A-TRACK`；`sprint13-gpu-inventory-dcgm-a-track.md`；readiness：`sprint13-gpu-inventory-dcgm-readiness.md`；gate：`validate-gpu-inventory-live-gate`） |
| Sandbox templates | Kata / runtimeClass / template catalog | `ports.SandboxTemplateCatalog` | 待拆分执行 |
| 网络路由 | Kube-OVN | `ports.NetworkService` / `runtime.NetworkService` / `network_resources.go` | **code+contract ready, LIVE PENDING**（`sprint13-netroute-kubeovn-a-track.md`；readiness：`sprint13-netroute-kubeovn-readiness.md`） |
| 卷快照与 mount-targets | Rook-Ceph RBD / CSI snapshot / NFS 或等价 filesystem backend | `ports.StorageService` / `runtime.LocalStorageService` / `storage_resources.go` | **code+contract ready, LIVE PENDING**（`SPRINT13-STORAGE-ROOK-CEPH-A-TRACK`；`sprint13-storage-rook-ceph-a-track.md`；readiness：`sprint13-storage-rook-ceph-readiness.md`；gate：`validate-storage-live-gate`） |
| K8s workloads | vCluster / Kubernetes API | `ports.K8sClusterService` / `local_k8s_cluster_service.go` / `k8s_cluster_resources.go` | **code+contract ready, LIVE PENDING**（`sprint13-k8s-workloads-vcluster-a-track.md`；readiness：`sprint13-k8s-workloads-vcluster-readiness.md`） |
| 对象存储 bucket/upload/download | MinIO（已选 2026-06-19，S3 兼容） | `ports.ObjectStore` + `ports.StorageService` / `storage_resources.go` | 待拆分执行 |
| 向量文档写入 | Milvus（已选 2026-06-19） | `ports.VectorStore` + `ports.VectorStoreService` / `vector_store_resources.go` | 待拆分执行 |

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
- Sprint 5 `REAL-K8S-LAB-A` / `make validate-real-k8s-profile` 仍作为真实底座历史门禁保留，覆盖 Kube-OVN、KubeVirt、vCluster 与 local profile / real-provider 边界；M1-NETWORK-LIVE-A / `validate-kubeovn-network-live-gate` 固定 Kube-OVN `Vpc/Subnet`、NetworkPolicy 和 Service/LB contract 门禁，Sprint 13 S01 已在此基础上补 route contract，但真实 route evidence 仍待人工 B 轨；M1-K8S-LIVE-A / `validate-vcluster-live-gate` 固定 vCluster Helm/kubeconfig、kubectl `/version` 和 Core live proxy contract 门禁，Sprint 13 S02 已在此基础上补 `core-workloads-list` contract，但真实 workload evidence 仍待人工 B 轨。
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
python scripts/validate_yaml.py api/openapi/v1.yaml
make validate-doc-entrypoints
git diff --check
```

Sprint 13 单批 real provider/live gate 还必须追加该批固定 live gate 命令和 evidence JSON 校验；未形成命令与 evidence 前，不得标记为 runtime ready。

Sprint 11 依赖的历史回归入口：

```bash
make validate-sprint10-release-prep
make validate-real-k8s-profile
```

> 涉及真实服务器写操作前，必须先重新执行只读盘点，并由人工确认具体设备 ID、预期影响和回滚方案。
