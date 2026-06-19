# ANI · 当前冲刺上手指南

> 新开发者（人类或 AI 工具）的第一个入口文件。本文只描述当前真实执行状态；历史完成批次查 `repo/development-records/README.md`。

> **仓库范围：仅 ANI Core。** ANI Services 已冻结并移交外部产品团队，本仓库不再开发任何 Services 代码（旧 Services 骨架只读保留）。外部团队给出产品功能/交互/API 定义后，Core 只按 Core OpenAPI/SDK/CLI 缺口补齐基础设施支撑。
> **当前重心：Sprint 11 / Core Real Deployment Validation 正式部署完成。** 本阶段已把 Sprint 6-10 的 contract/local/release-prep 成果放到真实物理服务器上验证，并完成 Rook-Ceph 正式部署：`rook-ceph` CephCluster 为 `Ready/HEALTH_OK`，`ceph-rbd-ssd` pool 为 `Ready`，5 个 SSD OSD 运行，`ani-rbd-ssd` StorageClass 已上线，受控 RBD smoke test、KubeVirt VM RBD storage smoke 与逐节点 reboot resilience 已通过并清理临时资源。未执行手工挂载、`/etc/fstab` 修改、系统盘变更、默认 StorageClass 切换或已有 PVC 迁移。RAG、Console、BOSS、model-service、kb-service、ai、operators、frontends 均不在本仓库执行范围内。
> **标准状态 marker：** 真实服务器只读验证已完成；Rook-Ceph 正式部署已完成。Sprint 11 执行环境：正式部署执行环境。

> **Sprint 12（当前活跃冲刺，2026-06-19 起）：** Core「Services 支撑 Handler」实现。基于真实代码与 `api/openapi/v1.yaml` 的 GAP，闭合 19 个已声明未实现的 Core handler（实例可观测 / GPU 清单 / 网络路由 / 卷快照 / 对象存储 / 向量写入 / K8s workloads）+ 2 个 422，分 B1/B2/B3 三批；仅 ANI Core，Tier1 local profile。下方 Sprint 11 已闭环并转为历史回归门禁，其 marker 保留供历史门禁校验。完整 GAP 与触发方式见 [`development-records/sprint12-kickoff-core-svc-support.md`](development-records/sprint12-kickoff-core-svc-support.md)。

## 当前冲刺

| 字段 | 值 |
|---|---|
| **冲刺编号** | Sprint 11（Core Real Deployment Validation 正式部署完成） |
| **主题** | Core real deployment validation + Rook-Ceph VM-first block storage live deployment |
| **当前状态** | `SPRINT11-KICKOFF-A`、`CORE-STORAGE-DISK-RISK-A`、`CORE-REAL-DEPLOY-A`、`CORE-ROOK-CEPH-FORMAL-DEPLOYMENT-A`、`CORE-ROOK-CEPH-LIVE-DEPLOYMENT-A`、`CORE-ROOK-CEPH-VM-STORAGE-SMOKE-A`、`CORE-ROOK-CEPH-REBOOT-RESILIENCE-A`、`CORE-SAFE-COMPLETION-A`、`CORE-REAL-DEPLOY-DOC-CONSISTENCY-A`、`SPRINT11-SAFE-CLOSURE-A` 与 `CORE-HISTORICAL-DOC-MARKER-COMPAT-A` 已建立代码和文档闭环。当前完成真实服务器只读验证、磁盘/存储风险计划、Rook-Ceph 正式部署代码包、真实 Rook-Ceph live 部署、RBD PVC/Pod smoke test、KubeVirt VM RBD storage smoke、逐节点 reboot resilience、聚合门禁和历史文档 marker 兼容维护 |
| **执行环境** | Sprint 11 执行环境：正式部署执行环境。允许已审批的 Rook-Ceph operator、CSI operator、CSI-Addons CRD、CephCluster、OSD、CephBlockPool、StorageClass 部署、受控 RBD smoke test、KubeVirt VM RBD storage smoke 和逐节点 reboot resilience；禁止手工挂载、fstab 修改、系统盘变更、盘符对齐、并发重启、默认 StorageClass 切换和已有 PVC 迁移 |
| **已由代码/真实环境证明完成** | Sprint 11：三台物理服务器 read-only disk/K8s/KubeVirt/storage 状态已核查；`rook-ceph` CephCluster 为 `Ready/HEALTH_OK`；3 个 mon、1 个 mgr、5 个 OSD 运行；`ceph-rbd-ssd` pool 为 `Ready`；`ani-rbd-ssd` StorageClass 已上线；临时 RBD PVC/Pod smoke test 已通过并清理；临时 KubeVirt VM 已挂载 RBD Block PVC，guest 看到块设备并完成写入尝试；两个 worker 逐台重启后 VM/PVC 恢复，control-plane 最后重启后 API readyz、mon/mgr/OSD、Ceph 和 worker VM/PVC 观测恢复；临时 VM/PVC/PV/StorageClass 均已清理 |
| **生产化边界** | Sprint 11 已完成 VM 优先块存储的真实部署验证和逐节点 reboot resilience，但仍不是实际 v1.0.0 发布，不代表完整 production ready、备份/恢复演练、容量告警、故障注入、长期 soak、升级/回滚演练或业务迁移完成。后续破坏性磁盘操作、默认 StorageClass 切换、已有 PVC 迁移、HDD class 引入、并发重启或更大故障演练仍必须单独审批 |
| **关联历史门禁** | Sprint 5 REAL-K8S-LAB-A 和 live gate evidence；Sprint 7 installer/offline/CLI/regression gates；Sprint 8 release hardening/offline/CLI/doc gates；Sprint 9 RC readiness gates；Sprint 10 release-prep gates |
| **最后校准日期** | 2026-06-06 |

## Sprint 11 已完成切片

本节保留当前 Sprint 的可执行事实，完整历史清单以 `repo/development-records/README.md` 为唯一归档索引。

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
- Sprint 5 `REAL-K8S-LAB-A` / `make validate-real-k8s-profile` 仍作为真实底座历史门禁保留，覆盖 Kube-OVN、KubeVirt、vCluster 与 local profile / real-provider 边界。
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

Sprint 11 依赖的历史回归入口：

```bash
make validate-sprint10-release-prep
make validate-real-k8s-profile
```

> 涉及真实服务器写操作前，必须先重新执行只读盘点，并由人工确认具体设备 ID、预期影响和回滚方案。
