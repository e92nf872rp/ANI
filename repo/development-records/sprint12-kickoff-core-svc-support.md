# Sprint 12 Kickoff — Core「Services 支撑 Handler」实现（GAP 分析 + 批次规划）

> 批次 ID：`SPRINT12-KICKOFF-A`
> 类型：Feature batch（Sprint 启动 + GAP 分析记录）
> 范围：仅 ANI Core。Sprint 11（Core Real Deployment Validation）已闭环，转为历史回归门禁。
> 真实来源：本文为 Sprint 12 启动与 GAP 分析归档；当前任务态以 `repo/CURRENT-SPRINT.md` 为准。

---

## 1. 背景与目标

外部 Services 团队已更新设计文档与两个 YAML 契约（Core `api/openapi/v1.yaml`、Services `api/openapi/services/v1.yaml`）。本仓库只实现并支撑 **ANI Core**。

对真实 Core 代码与 `api/openapi/v1.yaml` 做逐操作 diff，得到精确缺口：

- Core `v1.yaml` 声明 **111 个操作**；网关 `services/ani-gateway/internal/router/*.go` 已实现其余路径。
- **缺口 = 19 个「YAML 已声明、网关未注册 handler」的 Core 操作 + 2 个 422 前置校验行为。**
- 该 19 与治理文档 `services/docs/console-modules/governance/YAML-EXPANSION-SUMMARY-2026-06-17.md` 的「Core +19」完全吻合，对应 Services 侧 `CORE-TEAM-TASKS.md` 的 TASK-CORE-003~012。
- 这 19 个全部是 Core 自有基础设施资源（实例 / GPU / 网络 / 存储 / 向量 / K8s），response schema 已在 `v1.yaml` 定义好，不依赖 Services 产品 HTML 字段对齐，可立即开发、不返工。

目标：用最小、可落地、生产级严谨的改动闭合这 19 个缺口（Tier 1 本地 profile 实现）。real-provider 提升（Tier 2）属受门禁约束的后续批次，本 Sprint 不做。

## 2. 强制约束（CLAUDE.md）

1. 只改 Core；禁止改动 `/api/v1/svc` 前缀下的 Services 骨架（模型 / 推理 / 知识库 / 租户等）。
2. 能力必须经 `pkg/ports/` + `pkg/adapters/`（§5）：handler 不写业务逻辑，只调 port；缺方法时先扩 port + local adapter，再写 handler。禁止 handler 直接 import 组件 SDK（`validate-architecture` 拦截）。
3. Tier 1 只交付 local profile（§6.6）：响应带 `dev_profile` 标记，不得标 real-provider / runtime-ready。
4. 所有 POST / 有副作用操作带 `idempotency_key`。
5. 先改契约再写实现；本轮 19 op + schema 已在 `v1.yaml`，无需新增。
6. 不预防性新增 guard（§6.9 冻结令）。

## 3. GAP — 复用 / 扩展映射（已核对真实 ports）

实现标准形态（参照 `network_resources.go` / `vector_store_resources.go`）：
`pkg/ports/X.go`（接口）→ `pkg/adapters/runtime/local_X.go`（local 实现 + `_test.go`）→ `services/ani-gateway/internal/router/X_resources.go`（handler：request/response struct、`dev_profile`、`ports.Err*`→HTTP 映射、`{items,total,next_cursor}` 列表形）。

| # | operationId | 落地 handler 文件 | Port 复用 / 扩展 | 绑定 schema |
|---|---|---|---|---|
| **B1 — 实例可观测 ×5（P1）** |
| 1 | listInstanceLogs | demo_instances.go | 新增只读观测能力（WorkloadRuntime 无 logs/events/metrics）；local 返回 dev_profile 数据 | InstanceLogListResponse |
| 2 | listInstanceEvents | demo_instances.go | 同上 | InstanceEventListResponse |
| 3 | getInstanceMetrics | demo_instances.go | 同上 | InstanceMetrics |
| 4 | listInstanceSecurityEvents | demo_instances.go | 同上（sandbox kind 为主） | InstanceSecurityEventListResponse |
| 5 | createInstanceExecSession | demo_instances.go | 复用 instance ops 思路，返回合成 WebSocket URL（不发长期凭据） | InstanceExecSession / CreateInstanceExecSessionRequest |
| **B1 — GPU 清单 ×3（P1）** |
| 6 | listGPUInventory | 新建 gpu_inventory_resources.go | 复用 `ports.GPUInventory.ListNodeClasses` | GPUInventory* |
| 7 | getGPUOccupancy | gpu_inventory_resources.go | 由 `ListNodeClasses` 派生，或新增只读 `Occupancy` | GPUOccupancy* |
| 8 | listSandboxTemplates | gpu_inventory_resources.go 或新文件 | sandbox_runtime 无 templates → 新增只读 catalog 方法（静态/local） | SandboxTemplate* |
| **B2 — 网络/存储/K8s ×6 + 2×422（P2）** |
| 9 | listNetworkRoutes | network_resources.go | `ports.NetworkService` 扩展 `ListRoutes` | NetworkRoute* |
| 10 | createNetworkRoute | network_resources.go | `ports.NetworkService` 扩展 `CreateRoute`（带 idempotency） | NetworkRoute |
| 11 | listVolumeSnapshots | storage_resources.go | `ports.StorageService` 扩展 `ListVolumeSnapshots` | VolumeSnapshot* |
| 12 | createVolumeSnapshot | storage_resources.go | `ports.StorageService` 扩展 `CreateVolumeSnapshot`（202 形态） | VolumeSnapshot / AsyncTask |
| 13 | listFilesystemMountTargets | storage_resources.go | `ports.StorageService` 扩展 `ListFilesystemMountTargets` | MountTarget* |
| 14 | listK8sClusterWorkloads | k8s_cluster_resources.go | `ports.K8sClusterService` 扩展 `ListWorkloads`；local 返回 dev_profile | K8sWorkload* |
| — | searchVectorStore **422** | vector_store_resources.go | store 非 ready → 422 `PRECONDITION_FAILED`（已有 handler 加前置分支） | ErrorResponse |
| — | createK8sCluster **422** | k8s_cluster_resources.go | 前置不满足 → 422（已有 handler 加前置分支） | ErrorResponse |
| **B3 — 对象存储/向量 ×5（P2/P3）** |
| 15 | listStorageBuckets | storage_resources.go | StorageService 扩展 bucket 只读 + `ports.ObjectStore.EnsureBucket` | StorageBucket* |
| 16 | createStorageBucket | storage_resources.go | 同上（带 idempotency） | StorageBucket |
| 17 | uploadStorageObject | storage_resources.go | 复用 `ports.ObjectStore.SignedUploadURL`（预签名 URL，非 multipart，200） | StorageObjectUploadRequest/Response |
| 18 | downloadStorageObject | storage_resources.go | 复用 `ports.ObjectStore.SignedDownloadURL` | StorageObjectDownloadInfo |
| 19 | insertVectorStoreDocuments | vector_store_resources.go | `ports.VectorStoreService` 扩展 `InsertDocuments`，复用 `ports.VectorStore.Upsert`（202 形态） | VectorDocument* / AsyncTask |

> 实现以 `v1.yaml` 对应 operationId 的 request/response schema 为唯一真相；response struct 字段必须与 schema 一一对应。

## 4. 批次划分（每批 = 1 个 Feature batch）

| 批次 | 范围 | 批次 ID | 优先级 |
|---|---|---|---|
| B1 | 实例可观测 ×5 + GPU 清单 ×3 | `CORE-SVC-SUPPORT-OBSERVABILITY-A` | P1 |
| B2 | 网络路由 ×2 + 卷快照 ×2 + mount-targets ×1 + K8s workloads ×1 + 2×422 | `CORE-SVC-SUPPORT-NETSTORE-A` | P2 |
| B3 | 对象存储 ×4 + 向量写入 ×1 | `CORE-SVC-SUPPORT-OBJVEC-A` | P2/P3 |

B1/B2/B3 之间无硬依赖，可并行；组内 port→adapter→handler→test 串行（TDD 先写测试）。

## 5. 每批要新建 / 修改的文件

- `pkg/ports/*.go`：按映射表扩展接口（仅缺失方法）。
- `pkg/adapters/runtime/local_*.go` + `*_test.go`：local 实现 + 单测。
- `services/ani-gateway/internal/router/*_resources.go`（+ 新建 `gpu_inventory_resources.go`）+ `*_test.go`。
- `services/ani-gateway/internal/router/router.go`：注册新 route group。
- 文档闭环（Feature batch 四件套）：`development-records/{批次}.md`、`development-records/README.md`、`repo/CURRENT-SPRINT.md`、`ANI-06-开发计划.md` §0。

## 6. 验证（每批必须全绿）

```bash
cd repo
make test                     # validate-architecture + validate-auth-contract + go/python
make validate-demo-instances validate-core-alpha validate-gpu-contracts   # B1
make validate-network-alpha validate-storage-alpha                         # B2
make validate-storage-alpha validate-vector-alpha                          # B3
python scripts/validate_yaml.py api/openapi/v1.yaml
git diff --check
```

curl 冒烟（gateway 本地 `:8080`）：

```bash
curl -H "X-API-Key: $TEST_KEY" "http://localhost:8080/api/v1/gpu-inventory"           # B1 → 200
curl -H "X-API-Key: $TEST_KEY" "http://localhost:8080/api/v1/instances/$ID/metrics"    # B1 → 200
curl -X POST -H "X-API-Key: $TEST_KEY" -d @route.json "http://localhost:8080/api/v1/networks/routes"   # B2 → 201
curl -X POST -H "X-API-Key: $TEST_KEY" -d @upload.json "http://localhost:8080/api/v1/objects/upload"   # B3 → 200
```

成功标准：每个新 operationId 返回 schema 规定的状态码与结构；2 个 422 用例（向量库非 ready / K8s 创建前置失败）返回 `PRECONDITION_FAILED`；`dev_profile` 字段存在；架构 / 认证契约校验不回归。

## 7. 如何加载文档并触发开发（Claude Code / Codex）

本仓库没有字面 `/goal` 工具。触发 = 「按 CLAUDE.md 加载顺序加载 + 一段锚定到批次的执行提示」。

加载顺序：`CLAUDE.md` → `ANI-DOCS-INDEX.md` → `repo/CURRENT-SPRINT.md` → `ANI-06-开发计划.md` §0 → 本文件 → `api/openapi/v1.yaml`（对应 operationId 段）+ 目标 `*_resources.go` + 对应 `pkg/ports/*.go`。

B1 可复制 kickoff prompt：

```
按 CLAUDE.md 加载顺序读取 CLAUDE.md → ANI-DOCS-INDEX.md → repo/CURRENT-SPRINT.md →
ANI-06 §0 → repo/development-records/sprint12-kickoff-core-svc-support.md。
执行批次 CORE-SVC-SUPPORT-OBSERVABILITY-A（B1）：实现 v1.yaml 中
listInstanceLogs / listInstanceEvents / getInstanceMetrics / listInstanceSecurityEvents /
createInstanceExecSession 与 listGPUInventory / getGPUOccupancy / listSandboxTemplates。
约束：只改 Core；能力经 pkg/ports + pkg/adapters；Tier1 local profile 带 dev_profile；
TDD 先写测试。完成后跑 make test + validate-demo-instances + validate-core-alpha +
validate-gpu-contracts + git diff --check，并更新 Feature batch 四件套文档。
```

B2 / B3 同构替换批次 ID、operationId 列表与领域校验命令即可。亦可用 skill 驱动：`everything-claude-code:prp-implement` 或 `superpowers:executing-plans`，把本文件作为输入。

## 8. 生产化路线（Tier 2 — real-provider，受 ANI-06「真实底座组件引入强制门禁」约束）

Tier 1（B1–B3）只交付 contract + local profile，按 ANI-06 §「真实底座组件引入强制门禁」**不得标记 production ready / runtime ready**。要达到「生产环境可落地部署 + ANI 完整功能落地」，每个域还需 Tier 2 批次（建议落在 Sprint 13）：real adapter 经 `pkg/adapters` 接真实组件，handler 与 port 接口不变；新增 / 复用 live gate，在三台物理服务器跑通并输出 evidence JSON。

| 域 | Tier 2 real adapter | 真实组件（已具备） | live gate |
|---|---|---|---|
| GPU 清单 | 真实 GPU 发现 | NVIDIA device plugin / DCGM / node labels | 新增 gpu-inventory live gate |
| 实例 logs/metrics/events | 真实运行时观测 | K8s API / kubelet / Prometheus | 复用 KubeVirt/K8s live lab |
| 对象存储 upload/download/buckets | 预签名 | MinIO | 新增 object-store live gate |
| 向量写入 | upsert | Milvus | 新增 vector live gate |
| K8s workloads | 经 cluster proxy 真实列举 | vCluster / K8s | 复用 Sprint 5 vCluster live gate |
| 卷快照 / mount-targets | RBD snapshot / NFS | Rook-Ceph（Sprint 11 已部署） | 复用 Sprint 11 storage live |
| 网络路由 | 路由真实创建/观察 | Kube-OVN | 复用 Sprint 5 Kube-OVN live gate |

每个 Tier 2 批次必须（ANI-06 §153）声明当前是 contract / local-profile / real-provider、依赖组件 + 版本、用什么命令或 evidence 证明跑通。

边界：本仓库交付完整 **Core** 生产功能；Services 业务（模型 / 推理 / 知识库 / 租户业务）由外部团队基于 Core API 实现，「完整 ANI」是跨团队合力。Tier 1 闭合契约 → Tier 2 接真实 provider 并过 live gate → 三台物理服务器集成验证 → 进入 v1.0.0 发布候选，是本仓库到「生产可落地」的完整链路。
