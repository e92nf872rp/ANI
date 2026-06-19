# Sprint 13 Real Provider Readiness Plan - Services 支撑 Handler 真实环境门禁

> 记录类型：Planning / readiness plan
> 适用范围：Sprint 13 当前真实 provider 与 live gate 执行地图
> 前置条件：Sprint 12 B1/B2/B3 的 Tier1 local profile handler 已全部完成，并已通过 `make test` 与对应 domain validators

## 目标

Sprint 12 只闭合 Core OpenAPI handler 与 local adapters，不声明 real-provider、runtime ready 或 production ready。Sprint 13 的目标是把 Sprint 12 已落地的 Core handler 接到真实底座组件，按 ANI-06「真实底座组件引入强制门禁」形成可复跑 live gate 与 evidence JSON。

## 组件选型决策（已定，2026-06-19）

| 切片 | 选定组件 | 备注 |
|---|---|---|
| 对象存储 bucket/upload/download | **MinIO**（S3 兼容，预签名 URL） | 替代待定项 |
| 向量文档写入 | **Milvus** | Milvus / Qdrant 决策已定为 Milvus |
| 实例观测 logs/metrics/events | **Prometheus** + kubelet / K8s API | 观测源已定 |

首切片（执行中）：**网络路由 Kube-OVN**，就绪声明见 [`sprint13-netroute-kubeovn-readiness.md`](sprint13-netroute-kubeovn-readiness.md)。

## 代码关联矩阵

| Sprint 12 批次 | 已落地代码入口 | Sprint 13 真实组件 | Sprint 13 需新增或复用的 live gate |
|---|---|---|---|
| B1 `CORE-SVC-SUPPORT-OBSERVABILITY-A` | `pkg/ports/instance_observability.go`、`pkg/adapters/runtime/local_instance_observability_service.go`、`services/ani-gateway/internal/router/demo_instances.go` | K8s API / kubelet logs / Prometheus 或等价观测源 | 新增 instance-observability live gate，至少覆盖 logs/events/metrics/security-events/exec session HTTP schema |
| B1 `CORE-SVC-SUPPORT-OBSERVABILITY-A` | `pkg/ports/gpu_inventory.go`、`pkg/adapters/runtime/local_gpu_inventory.go`、`services/ani-gateway/internal/router/gpu_inventory_resources.go` | NVIDIA device plugin / DCGM / node labels / existing GPU lab | 新增 gpu-inventory live gate，复用 Sprint 5 GPU 调度 evidence，校验 `/gpu-inventory` 与 `/gpu-inventory/occupancy` |
| B1 `CORE-SVC-SUPPORT-OBSERVABILITY-A` | `pkg/ports/sandbox_template_catalog.go`、`pkg/adapters/runtime/local_sandbox_template_catalog.go`、`services/ani-gateway/internal/router/gpu_inventory_resources.go` | Kata / sandbox runtime catalog 来源 | 新增 sandbox-template catalog live/readiness gate；未接真实 catalog 前只允许 local profile |
| B2 `CORE-SVC-SUPPORT-NETSTORE-A` | `pkg/ports/network_resources.go`、`pkg/adapters/runtime/network_service.go`、`services/ani-gateway/internal/router/network_resources.go` | Kube-OVN | 复用 / 扩展 `validate-kubeovn-network-live-gate`，覆盖 route list/create |
| B2 `CORE-SVC-SUPPORT-NETSTORE-A` | `pkg/ports/storage_resources.go`、`pkg/adapters/runtime/storage_service.go`、`services/ani-gateway/internal/router/storage_resources.go` | Rook-Ceph RBD / CSI snapshot / NFS 或等价 filesystem mount target | 复用 Sprint 11 storage live 结果并新增 snapshot/mount-target live gate |
| B2 `CORE-SVC-SUPPORT-NETSTORE-A` | `pkg/ports/k8s_clusters.go`、`pkg/adapters/runtime/local_k8s_cluster_service.go`、`services/ani-gateway/internal/router/k8s_cluster_resources.go` | vCluster / Kubernetes API | 复用 vCluster live gate，覆盖 `listK8sClusterWorkloads` |
| B3 `CORE-SVC-SUPPORT-OBJVEC-A` | `pkg/ports/storage_resources.go`、`pkg/ports/object_store.go`、`pkg/adapters/runtime/storage_service.go`、`services/ani-gateway/internal/router/storage_resources.go` | MinIO / object store presign provider | 新增 object-store live gate，覆盖 buckets、upload presign、download presign |
| B3 `CORE-SVC-SUPPORT-OBJVEC-A` | `pkg/ports/vector_store.go`、`pkg/adapters/runtime/vector_store_service.go`、`services/ani-gateway/internal/router/vector_store_resources.go` | Milvus / Qdrant 或选定向量后端 | 新增 vector insert live gate，覆盖 document insert async task 与 search readiness |

## Sprint 13 进入条件

1. Sprint 12 B1/B2/B3 均完成 Feature batch 四件套文档闭环。
2. `make test`、对应 domain validators、`python scripts/validate_yaml.py api/openapi/v1.yaml`、`git diff --check` 全部通过。
3. 每个要接真实 provider 的能力都先声明：当前状态、真实组件与版本、live gate 命令、evidence 输出路径、失败时不得声称的 ready 级别。

## 边界

- 本计划不是 Sprint 13 完成记录。
- 未跑通 live gate 前，Sprint 12 handler 只能标记为 Tier1 local profile。
- 不新增 Services 业务逻辑，不修改 `/api/v1/svc` 资源。
