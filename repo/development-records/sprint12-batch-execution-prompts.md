# Sprint 12 — B1/B2/B3 批次执行提示词（人工 / AI 可直接粘贴）

> 工件归属：`SPRINT12-KICKOFF-A` 的执行提示词配套文件。
> 规划与 GAP 全文：[`sprint12-kickoff-core-svc-support.md`](sprint12-kickoff-core-svc-support.md)。
> 契约真相：[`../api/openapi/v1.yaml`](../api/openapi/v1.yaml)（对应 operationId 段）。
> 用法：选一个批次，把对应 ```text``` 代码块整段粘贴给 Claude Code / Codex 即可执行；每段已自包含，无需二次确认。

---

## 0. 前置事实（已核对真实代码，禁止重新臆测）

执行前必须把以下事实当作既定前提，**不要再去“猜”或重新发明**：

1. **本仓库只做 ANI Core。** 禁止改动 `/api/v1/svc` 前缀下的 Services 骨架（模型 / 推理 / 知识库 / 租户业务）。
2. **缺口已精确锁定 = 19 个已声明未实现 handler + 2 个 422。** Core `v1.yaml` 声明 111 op，网关 `services/ani-gateway/internal/router/*.go` 已实现其余；不要去“补全”已存在的 handler。
3. **相关 port 大多已存在**：`pkg/ports/gpu_inventory.go`（`ListNodeClasses` 等）、`object_store.go`（`SignedUploadURL`/`SignedDownloadURL`/`EnsureBucket`）、`workload_runtime.go`、`vector_store.go`（`Upsert`）、`storage_resources.go`、`network_resources.go`、`sandbox_runtime.go`。**优先复用；只有确实缺方法时才扩接口。**
4. **handler 标准形态**：参照 `services/ani-gateway/internal/router/network_resources.go` 与 `vector_store_resources.go` —— struct holds `ports.XService`（由 `runtimeadapter.NewLocalXService()` 注入）、request/response struct、响应带 `dev_profile`（`localCoreDevProfile(...)`）、列表返回 `{items,total,next_cursor}`、`ports.Err*`→HTTP（NotFound/Conflict/Invalid/Unsupported）映射、`demoTenantID(c)` 取租户。
5. **`uploadStorageObject` / `downloadStorageObject` 是预签名 URL 操作，不是 multipart 文件流**，直接复用 `ports.ObjectStore.SignedUploadURL/SignedDownloadURL`，返回 200。
6. **GPU 清单与 sandbox 模板 handler 当前不存在**，需新建 `gpu_inventory_resources.go` 并在 `router.go` 注册新 route group。
7. **本 Sprint 全部是 Tier 1 = local profile**：响应带 `dev_profile`，**严禁标 production ready / runtime ready**（ANI-06「真实底座组件引入强制门禁」）。真实 provider 是 Tier 2，不在本 Sprint。
8. response struct 字段必须与 `v1.yaml` 对应 schema **一一对应**；schema 名见每批表格。

## 1. 通用约束（三批共有）

- 分支：在 `feature/sprint12-core-support` 上工作，不碰 `main`，不推远端，完成等人工 review。
- 架构：能力必须经 `pkg/ports` + `pkg/adapters`；**handler 只调 port，禁止在 handler 直接 import 组件 SDK**（`validate-architecture` / `validate_component_imports.py` 会拦）。
- 幂等：所有 POST / 有副作用操作带 `idempotency_key`。
- RBAC：保留路由 `x-ani-rbac-scope` 语义（exec 用 `scope:instances:exec`）。
- TDD：先写 `pkg/adapters/runtime` local 实现单测 + `internal/router` handler 测试（红），再写实现（绿）。
- 不预防性新增 guard（CLAUDE.md §6.9 冻结令）。
- 文档闭环（Feature batch 四件套）：新增 `development-records/{批次}.md`、更新 `development-records/README.md`、`repo/CURRENT-SPRINT.md`、`ANI-06-开发计划.md` §0。

---

## 2. B1 — `CORE-SVC-SUPPORT-OBSERVABILITY-A`（P1）

```text
角色：ANI Core 平台工程师。ANI 是生产级基础设施平台，代码必须严谨、可落地交付。

加载（按序，不跳过）：CLAUDE.md → ANI-DOCS-INDEX.md → repo/CURRENT-SPRINT.md →
ANI-06-开发计划.md（§0 + §真实底座组件引入强制门禁）→
repo/development-records/sprint12-kickoff-core-svc-support.md →
repo/development-records/sprint12-batch-execution-prompts.md（§0 前置事实 + §1 通用约束）→
repo/api/openapi/v1.yaml（对应 operationId 段）。

分支：feature/sprint12-core-support，不碰 main、不推远端。

批次 CORE-SVC-SUPPORT-OBSERVABILITY-A，实现这 8 个 Core operationId 的网关 handler：
- listInstanceLogs        → schema InstanceLogListResponse          → handler demo_instances.go
- listInstanceEvents      → schema InstanceEventListResponse        → handler demo_instances.go
- getInstanceMetrics      → schema InstanceMetrics                  → handler demo_instances.go
- listInstanceSecurityEvents → schema InstanceSecurityEventListResponse → handler demo_instances.go
- createInstanceExecSession → schema InstanceExecSession / CreateInstanceExecSessionRequest → handler demo_instances.go（返回合成 WebSocket URL，不发长期凭据）
- listGPUInventory        → 复用 ports.GPUInventory.ListNodeClasses → 新建 handler gpu_inventory_resources.go，并在 router.go 注册
- getGPUOccupancy         → 由 ListNodeClasses 派生，或新增只读 Occupancy 方法 → gpu_inventory_resources.go
- listSandboxTemplates    → sandbox 无 templates，新增只读 catalog 方法（静态/local）→ gpu_inventory_resources.go

约束（务必遵守，细节见加载文档）：只改 Core；能力经 pkg/ports+pkg/adapters，缺方法才扩接口；
实例 logs/events/metrics/security-events 当前无 port 方法，需新增只读观测能力 + local adapter（返回 dev_profile 数据）；
Tier1 local profile，响应带 dev_profile，严禁标 production/runtime ready；response 字段与 v1.yaml schema 一一对应；
列表 {items,total,next_cursor}；ports.Err*→HTTP 沿用 network_resources.go/vector_store_resources.go；POST 带 idempotency_key；
保留 x-ani-rbac-scope；TDD 先写测试。参照模板：network_resources.go、vector_store_resources.go。

完成判定（必须全绿并贴出输出）：
cd repo && make test && make validate-demo-instances validate-core-alpha validate-gpu-contracts && python scripts/validate_yaml.py api/openapi/v1.yaml && git diff --check
再对每个新路由 curl 冒烟（如 GET /api/v1/gpu-inventory、GET /api/v1/instances/<id>/metrics）符合 schema 状态码。

收尾：新增 repo/development-records/core-svc-support-observability-a.md，更新 development-records/README.md、
repo/CURRENT-SPRINT.md、ANI-06-开发计划.md §0；全部提交到 feature/sprint12-core-support。
```

---

## 3. B2 — `CORE-SVC-SUPPORT-NETSTORE-A`（P2）

```text
角色：ANI Core 平台工程师。ANI 是生产级基础设施平台，代码必须严谨、可落地交付。

加载（按序，不跳过）：CLAUDE.md → ANI-DOCS-INDEX.md → repo/CURRENT-SPRINT.md →
ANI-06-开发计划.md（§0 + §真实底座组件引入强制门禁）→
repo/development-records/sprint12-kickoff-core-svc-support.md →
repo/development-records/sprint12-batch-execution-prompts.md（§0 前置事实 + §1 通用约束）→
repo/api/openapi/v1.yaml（对应 operationId 段）。

分支：feature/sprint12-core-support，不碰 main、不推远端。

批次 CORE-SVC-SUPPORT-NETSTORE-A，实现 6 个 operationId + 2 个 422：
- listNetworkRoutes   → 扩展 ports.NetworkService.ListRoutes + local adapter → handler network_resources.go → schema NetworkRoute*
- createNetworkRoute  → 扩展 ports.NetworkService.CreateRoute（带 idempotency） → network_resources.go → schema NetworkRoute
- listVolumeSnapshots → 扩展 ports.StorageService.ListVolumeSnapshots → handler storage_resources.go → schema VolumeSnapshot*
- createVolumeSnapshot→ 扩展 ports.StorageService.CreateVolumeSnapshot（202 形态） → storage_resources.go → schema VolumeSnapshot / AsyncTask
- listFilesystemMountTargets → 扩展 ports.StorageService.ListFilesystemMountTargets → storage_resources.go → schema MountTarget*
- listK8sClusterWorkloads → 扩展 ports.K8sClusterService.ListWorkloads（local 返回 dev_profile） → handler k8s_cluster_resources.go → schema K8sWorkload*
422 行为（已有 handler 加前置分支，不新建路由）：
- searchVectorStore：向量库非 ready → 422 PRECONDITION_FAILED（vector_store_resources.go）
- createK8sCluster：前置不满足 → 422 PRECONDITION_FAILED（k8s_cluster_resources.go）

约束：只改 Core；能力经 pkg/ports+pkg/adapters；Tier1 local profile 带 dev_profile，严禁标 production/runtime ready；
response 字段与 v1.yaml schema 一一对应；列表 {items,total,next_cursor}；ports.Err*→HTTP 沿用既有写法；
POST 带 idempotency_key；保留 x-ani-rbac-scope；TDD 先写测试。参照模板：network_resources.go。

完成判定（必须全绿并贴出输出）：
cd repo && make test && make validate-network-alpha validate-storage-alpha && python scripts/validate_yaml.py api/openapi/v1.yaml && git diff --check
再 curl 冒烟（如 POST /api/v1/networks/routes → 201；向量库非 ready 时 POST /api/v1/vector-stores/<id>/search → 422）。

收尾：新增 repo/development-records/core-svc-support-netstore-a.md，更新 development-records/README.md、
repo/CURRENT-SPRINT.md、ANI-06-开发计划.md §0；全部提交到 feature/sprint12-core-support。
```

---

## 4. B3 — `CORE-SVC-SUPPORT-OBJVEC-A`（P2/P3）

```text
角色：ANI Core 平台工程师。ANI 是生产级基础设施平台，代码必须严谨、可落地交付。

加载（按序，不跳过）：CLAUDE.md → ANI-DOCS-INDEX.md → repo/CURRENT-SPRINT.md →
ANI-06-开发计划.md（§0 + §真实底座组件引入强制门禁）→
repo/development-records/sprint12-kickoff-core-svc-support.md →
repo/development-records/sprint12-batch-execution-prompts.md（§0 前置事实 + §1 通用约束）→
repo/api/openapi/v1.yaml（对应 operationId 段）。

分支：feature/sprint12-core-support，不碰 main、不推远端。

批次 CORE-SVC-SUPPORT-OBJVEC-A，实现 5 个 operationId（全部落在 storage_resources.go / vector_store_resources.go）：
- listStorageBuckets   → StorageService 扩展 bucket 只读 + 复用 ports.ObjectStore.EnsureBucket → schema StorageBucket*
- createStorageBucket  → 同上（带 idempotency） → schema StorageBucket
- uploadStorageObject  → 复用 ports.ObjectStore.SignedUploadURL（预签名 URL，非 multipart，返回 200） → schema StorageObjectUploadRequest/Response
- downloadStorageObject→ 复用 ports.ObjectStore.SignedDownloadURL（预签名 URL，返回 200） → schema StorageObjectDownloadInfo
- insertVectorStoreDocuments → VectorStoreService 扩展 InsertDocuments，复用 ports.VectorStore.Upsert（202 形态） → schema VectorDocument* / AsyncTask

约束：只改 Core；能力经 pkg/ports+pkg/adapters；upload/download 不要写成 multipart，用预签名 URL；
Tier1 local profile 带 dev_profile，严禁标 production/runtime ready；response 字段与 v1.yaml schema 一一对应；
列表 {items,total,next_cursor}；ports.Err*→HTTP 沿用既有写法；POST 带 idempotency_key；保留 x-ani-rbac-scope；TDD 先写测试。
参照模板：storage_resources.go、vector_store_resources.go。

完成判定（必须全绿并贴出输出）：
cd repo && make test && make validate-storage-alpha validate-vector-alpha && python scripts/validate_yaml.py api/openapi/v1.yaml && git diff --check
再 curl 冒烟（如 POST /api/v1/objects/upload → 200 预签名 URL；POST /api/v1/vector-stores/<id>/documents → 202）。

收尾：新增 repo/development-records/core-svc-support-objvec-a.md，更新 development-records/README.md、
repo/CURRENT-SPRINT.md、ANI-06-开发计划.md §0；全部提交到 feature/sprint12-core-support。
```

---

## 5. 关联文档

- 规划 + GAP 全文：[`sprint12-kickoff-core-svc-support.md`](sprint12-kickoff-core-svc-support.md)（§3 复用/扩展映射、§8 Tier 2 生产化路线）。
- 当前冲刺入口：[`../CURRENT-SPRINT.md`](../CURRENT-SPRINT.md)。
- 全局状态 + 真实底座门禁：[`../../ANI-06-开发计划.md`](../../ANI-06-开发计划.md) §0 与「真实底座组件引入强制门禁」。
- 强制工程规则：[`../../CLAUDE.md`](../../CLAUDE.md)。
- Core 契约：[`../api/openapi/v1.yaml`](../api/openapi/v1.yaml)。
- handler 模板：`../services/ani-gateway/internal/router/network_resources.go`、`vector_store_resources.go`。
- 亦可 skill 驱动：`everything-claude-code:prp-implement` 或 `superpowers:executing-plans`，把对应批次代码块作为输入。
