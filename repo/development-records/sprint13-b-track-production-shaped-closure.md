# SPRINT13-B-TRACK-PRODUCTION-SHAPED-CLOSURE - S01-S04 production-shaped closure

> 记录类型：Sprint 13 B-track production-shaped closure
> 日期：2026-06-20
> 范围：仅 ANI Core S01-S04 production-shaped 代码、部署契约与门禁闭环；不改 Services，不推远端
> 状态：**production-shaped acceptance passed for S01-S04**；不代表 full platform production ready。

## 目标

把 S01-S04 从“只防止误标 production ready”推进到可生产验收的代码和门禁形态：

- Gateway 与 bootstrap Kubernetes REST provider 支持标准 in-cluster ServiceAccount token、CA bundle 与 `KUBERNETES_SERVICE_HOST/PORT`，生产部署不再依赖本机 kubeconfig 或 kubectl proxy。
- S01 network route 元数据从 in-memory-only 提升为 metadata store 持久化路径，provider-backed route apply/observe 后可落库。
- 新增 production-shaped Gateway RBAC/profile，固定最小 ServiceAccount、ClusterRole、ClusterRoleBinding 与 S01-S07 B 轨通过标准。
- S02/S03/S04 live gate 增加 `--production-shaped` 模式；启用后拒绝 localhost/127、本机 proxy、port-forward/dev gateway 证据，并写入 `production_shape.status=passed` + `proof_items`。
- `validate-sprint13-b-track-production-shape` 从边界 guard 升级为生产形态契约门禁：passed evidence 必须有正向 proof_items，且不得使用 lab/local/dev transport。

## 本次代码闭环

| 切片 | 修复内容 | 生产验收影响 |
|---|---|---|
| S01 网络路由 Kube-OVN | `NetworkResourceStore.UpsertRoute`、`MetadataNetworkStore.UpsertRoute`、`LocalNetworkService.CreateRoute` 持久化 pending/success/failed route；新增 `network_routes` migration | route provider 不再只有内存态，具备持久 route metadata reconciliation 基础 |
| S01 network provider closure | `LocalNetworkService` provider pipeline 从 route-only 提升为 VPC/Subnet/SecurityGroup/LoadBalancer/Route 通用 provider；`KubernetesRESTClient.ServerSideDryRun` 改为 server-side apply PATCH `dryRun=All`，避免 route 更新既有 VPC 时 POST create 409 | S01 production-shaped gate 现在强制经 ANI Gateway create/list 后再观测底层 Kube-OVN，禁止 kubectl-only evidence 标 passed |
| S01/S03/S04 Kubernetes provider | `KubernetesRESTClient` 支持 in-cluster ServiceAccount token/CA；Gateway network/storage/gpu runtime 与 `pkg/bootstrap` provider 装配均透传 service host/port/token/CA file | Gateway 与 bootstrap 能力装配路径均可用正式 ServiceAccount/RBAC 访问 Kubernetes API |
| S02 K8s workloads vCluster | `validate_vcluster_live_gate.py --production-shaped` 拒绝 `--proxy-server` 与本地 Gateway，要求非本地 metadata target server | 后续 S02 passed evidence 必须证明 per-cluster metadata target + TLS/token |
| S03 storage Rook-Ceph | `validate_storage_live_gate.py --production-shaped` 拒绝本地 Gateway，并写入 S03 production proof items | 后续 S03 passed evidence 必须经 production gateway / in-cluster RBAC 路径 |
| S04 GPU inventory/DCGM | `validate_gpu_inventory_live_gate.py --production-shaped` 拒绝 `--kubernetes-nodes-url` 与本地 DCGM/Prometheus URL | 后续 S04 passed evidence 必须经 in-cluster Kubernetes API 与集群 Service/Prometheus |
| S05-S07 标准 | `sprint13-production-shaped-gateway-profile.yaml` 写入 S05/S06/S07 `slice_proof_items` | 后续 B 轨沿用同一 production-shaped passed 标准 |

## Post-review hardening

深度复审发现：初版 closure 已覆盖 Gateway network/storage/gpu runtime，但 `pkg/bootstrap.Config` 与 `NewCapabilitiesWithConfig` 仍只向 Kubernetes REST provider 传递显式 `KUBERNETES_API_HOST` / bearer token / field manager。已在 `SPRINT13-B-TRACK-PRODUCTION-SHAPED-POST-REVIEW` 中补齐 bootstrap in-cluster ServiceAccount 装配路径，并让 S01 network、S03 storage、S04 GPU inventory 与 S07 Prometheus observability 共用同一显式 Kubernetes REST config helper。

同时，`KubernetesRESTClient` 不再直接读取 ambient process env；环境变量只在 Gateway/bootstrap 配置层读取，adapter 层保持显式 config，避免生产部署和 CI 测试因宿主环境变量产生隐式行为。

## 新增/更新门禁

```bash
make validate-sprint13-b-track-production-shape
```

该门禁现在同时检查：

- S01-S04 passed evidence 必须：
  - `transport_profile` 不含 lab/local/dev gateway/kubectl proxy/port-forward；
  - `missing_items` 为空；
  - `proof_items` 包含该切片要求的生产证明项。
- S01 passed evidence 还必须包含 Gateway VPC/Subnet/Route create status、Route list status、Gateway route id 与 route count，禁止 kubectl-only evidence。
- S02/S03/S04 passed evidence 还必须包含各自正向业务证据：S02 `proxy_status=200` / `workloads_status=200` / workload count / cleanup，S03 volume/snapshot/filesystem/mount-target lifecycle status / cleanup，S04 Core GPU inventory/occupancy status、GPU capacity、GPU node count 与 DCGM metric evidence。
- `deploy/real-k8s-lab/sprint13-production-shaped-gateway-profile.yaml` 必须覆盖 S01-S07 的 proof 标准。
- `deploy/real-k8s-lab/sprint13-production-shaped-gateway-rbac.yaml` 必须包含 Gateway ServiceAccount、ClusterRole、ClusterRoleBinding 和最小 Kubernetes/Kube-OVN/CSI/GPU 资源权限，且不得授予 wildcard resources。

## 重要边界

S01-S04 已在正式 Gateway + in-cluster ServiceAccount/RBAC + metadata target / cluster Service 路径重新执行对应 `--production-shaped` live gate，并产出新的非敏感 evidence JSON，四份 evidence 均为 `production_shape.status=passed`。

该结论只代表 **production-shaped acceptance passed**，不是 full platform production ready：当前 production-shaped Gateway 仍为 `ANI_AUTH_MODE=dev`，Auth/Dex production gate、正式镜像发布/升级、长期 SLA/soak、备份/恢复和故障注入仍需后续单独门禁。S01-S04 和平台聚合状态不能标记为 production ready；S05-S07 B 轨可以继续，但也只能按同一组件级 production-shaped acceptance 标准验收。
S01 也尚未证明 Gateway delete / provider delete 全生命周期；本轮 cleanup 是 live gate 对底层临时资源的受控清理。

Auth/Dex 边界复审见 `sprint13-s01-s04-production-readiness-review.md`。

## 验证

本批次提交前执行完整 Sprint 13 基线与 production-shaped 门禁；输出以提交记录为准。
