# Sprint 13 — codex goal 持续循环驱动（real provider / live gate 收敛）

> 工件归属：Sprint 13 执行驱动。规划地图见 [`sprint13-real-provider-readiness-plan.md`](sprint13-real-provider-readiness-plan.md)。
> 用法：把 **§1 编排提示** 整段粘进 codex goal 持续模式即可逐切片自动推进；它会读 **§2 切片队列**、跑 loop-safe 闭环、更新关联文档、提交分支，再进入下一个 pending 切片。
> 关键边界：Sprint 13 涉及真实服务器写操作，**不能无人值守**。本驱动把工作拆成两条轨道，循环只跑「可自动」轨道，真实写操作留人工门禁（见 §0）。

---

## 0. 两条轨道（硬边界）

| 轨道 | 内容 | 谁执行 |
|---|---|---|
| **A. loop-safe（codex 持续循环自动做）** | per-slice readiness 声明、real adapter 代码（port/handler 不改）、fake/mock 单测、live-gate **契约级**校验器 + fixtures、本地全套门禁、四件套文档闭环、提交分支 | codex goal 循环 |
| **B. human-gated（必须停下来等人工）** | 真实集群 apply / kubectl / helm、部署 MinIO/Milvus/Prometheus、在三台服务器跑真实 live gate 产出 evidence、把能力标 real-provider/runtime/production ready | 人工（先只读盘点 + 确认） |

循环每个切片只把它推进到 **「code+contract ready, LIVE PENDING」**；真实 evidence 与 real-provider 标记是 B 轨人工步骤。能力在 B 轨完成前一律保持 **Tier1 local profile**。

## 1. 编排提示（粘进 codex goal 持续运行）

```text
角色：ANI Core 平台工程师（生产级，严谨可落地）。你在 codex goal 持续模式推进 Sprint 13。

目标：逐个推进 Sprint 13 切片的 A 轨（loop-safe）工作，每个切片跑闭环→更新关联文档→提交分支→自动进入下一个 pending 切片。绝不触碰 B 轨（真实写操作）。

每切片开始按序加载：CLAUDE.md → ANI-DOCS-INDEX.md → repo/CURRENT-SPRINT.md →
ANI-06-开发计划.md（§0 + §真实底座组件引入强制门禁）→
repo/development-records/sprint13-real-provider-readiness-plan.md →
repo/development-records/sprint13-loop-execution-prompts.md（§0 边界 + §2 队列 + §3 切片事实）→
对应 per-slice readiness（无则先按 §153 五项创建）→ repo/api/openapi/v1.yaml 对应段。

分支：feature/sprint12-core-support。不碰 main、不推远端、不改 port 接口签名/handler、不改 /api/v1/svc。

每轮（一个切片）只做 A 轨 loop-safe 步骤：
1. 在 §2 切片队列选第一个 status=pending 的切片。
2. 无 per-slice readiness 文件就先按 §153 五项创建（当前状态/真实组件+版本/live gate 命令/evidence 路径/失败边界）；真实组件 API 不臆测，标注“执行前需在真实 lab 确认”。
3. 在既有 ports/adapters 边界写 real adapter（port 接口与 Gateway handler 一行不改）；组件 SDK 只在 adapter 内，并在 scripts/validate_component_imports 登记 allowlist+coupling_level+理由。
4. 写/补 adapter 单测（用 fake/mock，不依赖真实后端）；扩 live-gate 契约级校验器 + fixtures（本地可跑，不连真实集群）。契约差异先改 v1.yaml（只增可选字段）。
5. 跑并贴出输出，必须全绿：
   cd repo && make test && <该切片 domain 校验> && <该切片 live-gate 契约校验> && python scripts/validate_yaml.py api/openapi/v1.yaml && make validate-doc-entrypoints && git diff --check
6. 更新关联文档：新增/更新 development-records/{切片}.md、development-records/README.md、repo/CURRENT-SPRINT.md、ANI-06-开发计划.md §0、本文件 §2 队列 status→“code+contract ready, LIVE PENDING”、readiness-plan 矩阵。能力仍标 Tier1 local profile。
7. git commit 到 feature/sprint12-core-support（不 push）。
8. 进入下一个 pending 切片，重复。

绝对禁止（出现即停，转人工，绝不自动执行）：真实服务器/集群写操作、apply、kubectl/helm 变更、部署 MinIO/Milvus/Prometheus、跑真实 live gate、标 real-provider/runtime/production ready、把凭据或 IP 写进任何可提交文件/evidence/日志、改 main/push、改 port 签名或 handler、动 /api/v1/svc。

停止条件（命中即停并报告）：
- 某切片必须 B 轨（真实写/部署）才能继续 → 该切片 status 标“LIVE GATE 待人工执行”，停。
- 所有切片到达“code+contract ready, LIVE PENDING” → 报告 A 轨完成，等人工执行 B 轨。
- 任何门禁失败且无法在 A 轨内修复 → 停并报告。

每轮结束输出：切片名 / 做了什么 / 门禁结果 / 队列最新 status / 下一步。
```

## 2. 切片队列（循环按序选 pending；完成后改 status）

| # | 切片 | 真实组件（已选） | status |
|---|---|---|---|
| S01 | 网络路由 | Kube-OVN v1.15.8 | code+contract ready, LIVE PENDING（见 `sprint13-netroute-kubeovn-a-track.md`；readiness：`sprint13-netroute-kubeovn-readiness.md`） |
| S02 | K8s workloads | vCluster / K8s v1.36.1 | code+contract ready, LIVE PENDING（见 `sprint13-k8s-workloads-vcluster-a-track.md`；readiness：`sprint13-k8s-workloads-vcluster-readiness.md`） |
| S03 | 卷快照 / mount-targets | Rook-Ceph（CSI snapshot / RBD / NFS） | code+contract ready, LIVE PENDING（见 `sprint13-storage-rook-ceph-a-track.md`；readiness：`sprint13-storage-rook-ceph-readiness.md`） |
| S04 | GPU 清单 / 占用 | NVIDIA device-plugin / DCGM | code+contract ready, LIVE PENDING（见 `sprint13-gpu-inventory-dcgm-a-track.md`；readiness：`sprint13-gpu-inventory-dcgm-readiness.md`） |
| S05 | 对象存储 bucket/upload/download | MinIO（S3 兼容，预签名） | pending |
| S06 | 向量文档写入 | Milvus | pending |
| S07 | 实例观测 logs/metrics/events | Prometheus + kubelet / K8s API | pending |

排序原则：S01–S04 复用已部署组件（A 轨可全程自动）；S05–S07 的真实后端需 B 轨人工部署，但 A 轨仍可先把 real adapter 代码 + 契约 live gate + readiness + 单测写完并本地跑绿。

## 3. 每切片钉死事实（防幻觉）

| # | ports / 代码边界 | handler 文件 | domain 校验 | live-gate 契约校验 |
|---|---|---|---|---|
| S01 | `ports.NetworkService`（CreateRoute/ListRoutes）+ `NetworkProvider`/`KubeOVNNetworkProviderAdapter` | `network_resources.go` | `validate-network-alpha` | 扩 `validate-kubeovn-network-live-gate` 覆盖 route |
| S02 | `ports.K8sClusterService`（ListWorkloads）+ proxy 路径 | `k8s_cluster_resources.go` | k8s 契约校验 | 复用 vCluster live gate 契约 |
| S03 | `ports.StorageService`（Volume snapshot / mount-target）+ StorageProvider | `storage_resources.go` | `validate-storage-alpha` | 新增 storage snapshot/mount-target live-gate 契约 |
| S04 | `ports.GPUInventory` | `gpu_inventory_resources.go` | `validate-gpu-contracts` | 新增 gpu-inventory live-gate 契约（复用 Sprint5 GPU evidence 作前置事实） |
| S05 | `ports.ObjectStore` + `ports.StorageService` | `storage_resources.go` | `validate-storage-alpha` | 新增 object-store(MinIO) live-gate 契约 |
| S06 | `ports.VectorStore` + `ports.VectorStoreService` | `vector_store_resources.go` | `validate-vector-alpha` | 新增 vector(Milvus) live-gate 契约 |
| S07 | `ports.InstanceObservability` | `demo_instances.go` | `validate-demo-instances` | 新增 instance-observability(Prometheus) live-gate 契约 |

通用：handler 与 port 接口签名不改；新组件 SDK 只在 adapter 内并登记 allowlist；真实组件 API（如 Kube-OVN 静态路由表达、CSI snapshot CRD、Milvus collection schema）执行前在真实 lab 确认，不照搬假设字段。

## 4. 关联文档

- 执行地图与组件决策：[`sprint13-real-provider-readiness-plan.md`](sprint13-real-provider-readiness-plan.md)
- S01 就绪声明（样板）：[`sprint13-netroute-kubeovn-readiness.md`](sprint13-netroute-kubeovn-readiness.md)
- 当前冲刺入口：[`../CURRENT-SPRINT.md`](../CURRENT-SPRINT.md)
- 真实底座门禁：[`../../ANI-06-开发计划.md`](../../ANI-06-开发计划.md) §「真实底座组件引入强制门禁」
- 强制工程规则：[`../../CLAUDE.md`](../../CLAUDE.md)
