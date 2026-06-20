# SPRINT13-B-TRACK-PRODUCTION-SHAPE-REVIEW - S01-S04 production-shaped boundary guard

> 记录类型：Sprint 13 B-track production-shaped review / guard
> 日期：2026-06-20
> 范围：仅 ANI Core Sprint 13 S01-S04 已通过 B 轨 real-provider evidence 的生产形态边界审查
> 状态：**production-shaped acceptance passed for S01-S04**；不得把该结论等同 full platform production ready。

## 结论

S01-S04 均已通过各自 real-provider live gate；本批次最初为防止误标生产可用，新增 `validate-sprint13-b-track-production-shape` 门禁，要求 evidence 显式记录 production_shape 边界。

后续 `SPRINT13-B-TRACK-PRODUCTION-SHAPED-CLOSURE` 已把该门禁升级为 production-shaped acceptance standard：Kubernetes REST client 支持 in-cluster ServiceAccount token/CA，Gateway network/storage/gpu runtime 透传 in-cluster 配置，S01 route metadata 已持久化，S02/S03/S04 live gate 增加 `--production-shaped` 模式，并新增 `deploy/real-k8s-lab/sprint13-production-shaped-gateway-profile.yaml` / `sprint13-production-shaped-gateway-rbac.yaml` 作为 S01-S07 B 轨 passed 标准。S01-S04 现已重新执行 production-shaped live gate，四份 evidence 均为 `production_shape.status=passed`。

## S01-S04 审查矩阵

| 切片 | 已证明 | production_shape 当前状态 | 生产形态仍缺 |
|---|---|---|---|
| S01 网络路由 Kube-OVN | Gateway VPC/Subnet/Route create/list + Kube-OVN 底层观测 + cleanup 通过 | `passed` / `production_gateway_in_cluster_serviceaccount` | 组件 production-shaped 已闭合；full production 仍受 Auth/Dex production gate 阻断 |
| S02 K8s workloads vCluster | Gateway provider create vCluster + metadata target TLS + workload list observe + cleanup 通过 | `passed` / `metadata_target_tls` | 组件 production-shaped 已闭合；full production 仍受 Auth/Dex production gate 阻断 |
| S03 storage Rook-Ceph | Gateway volume/snapshot/filesystem/mount-target lifecycle + cleanup 通过 | `passed` / `production_gateway_in_cluster_serviceaccount` | 组件 production-shaped 已闭合；full production 仍受 Auth/Dex production gate 阻断 |
| S04 GPU inventory/DCGM | Gateway `/gpu-inventory` 与 `/gpu-inventory/occupancy` + Kubernetes NodeList + DCGM metrics 通过 | `passed` / `in_cluster_kubernetes_api_and_cluster_metrics_service` | 组件 production-shaped 已闭合；full production 仍受 Auth/Dex production gate 阻断 |

## 新增强制门禁

```bash
cd repo && make validate-sprint13-b-track-production-shape
```

门禁检查：

- S01-S04 historical evidence JSON 必须保留 real-provider gate `status=passed`。
- S01-S04 evidence JSON 必须包含 `production_shape`。
- `production_shape.status=pending` 时必须列出 `missing_items`。
- 若未来标 `production_shape.status=passed`，不得使用 lab/local/kubectl proxy/port-forward/dev gateway 传输形态，`missing_items` 必须为空，且 `proof_items` 必须包含该切片 required proof。
- 对应 live-result 文档必须同时写明 `Production-shaped gate`、`production_shape` 和不代表 `production ready`。
- Production profile/RBAC 必须存在并覆盖 S01-S07 proof 标准。

## 生产形态通过条件

当前 S01-S04 已通过 production-shaped passed。后续若要把平台聚合状态升级为 production ready，必须重新产出 Auth/Dex production gate 证据，不得复用当前组件 evidence：

- Gateway 使用正式部署路径，不使用本机 `127.0.0.1` dev gateway 作为生产证据。
- Kubernetes API 通过正式 ServiceAccount/RBAC、in-cluster API 或受控 API endpoint 访问，不使用本机 `kubectl proxy` 作为生产证据。
- DCGM/Prometheus 使用集群 Service 或正式 Prometheus query，不使用本机 port-forward 作为生产证据。
- 凭据、token、kubeconfig、服务器 IP、Pod IP 不写入 evidence 或文档。
- 当前 production-shaped Gateway 仍为 `ANI_AUTH_MODE=dev`，Auth/Dex production gate 未通过前，S01-S04 和平台聚合状态不能标记为 production ready。
- S05-S07 B 轨可以继续，但只能按组件 production-shaped acceptance 标准验收。

## 验证

```text
python scripts/validate_sprint13_b_track_production_shape_test.py
....
Ran 4 tests
OK
```

完整提交前仍需跑 Sprint 13 基线与文档门禁。
