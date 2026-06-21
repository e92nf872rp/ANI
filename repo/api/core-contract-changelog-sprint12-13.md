# ANI Core 接口契约变更说明 · Sprint 12–13

> 面向 **外部 Services 团队** 的接口契约同步文档。
> 目的：说明 Sprint 12、Sprint 13 期间 ANI Core 对外契约的**改动范围、原因和对接动作**，供 Services 客户端同步。
> 本文同时供人工阅读与 AI Agent 加载；机器可读摘要见文末「Machine-readable summary」。

---

## 0. 结论（30 秒速览）

| 契约文件 | 是否变更 | 说明 |
|---|---|---|
| `repo/api/openapi/v1.yaml`（**Core 对外 / 跨层控制面契约**） | **是** | 仅在 **Sprint 12** 变更。以 Sprint 11 收尾提交 `d52efda` 为基线，当前 diff 为 **+784 / -5**；其中 Sprint 12 主体提交 `6d052d3` 为 **+742 / -3**，后续对齐提交 `6d052d3..HEAD` 为 **+72 / -32**。Sprint 13 全部为真实 provider / live gate 收敛，**未改契约**。 |

**Services 团队需要做的事**：先确认新增的 19 个 Core operationId（§3.0）是否已纳入客户端调用面；其中需要改造调用假设的是两类 —— ID 字段类型放宽（§3.4）与卷快照创建改为异步任务（§3.5）。其余均为**向后兼容的新增端点、字段或枚举**，无需破坏性改造即可继续工作。

Core API 变更全部位于 Sprint 12 的 5 个提交：
`6d052d3` Core +19 operationId / +16 path 主体扩展 · `779f84e` align b1 support schemas · `dbd1fe8` add core netstore support endpoints · `b78ee4a` align netstore review closure contracts · `e9ae3ec` align objvec storage bucket contract docs。

---

## 1. 背景

- Sprint 12 目标是补齐 Core 对 Services 的「支撑型 handler」契约（observability / netstore / objvec 三组），让 Services 能基于 Core OpenAPI 完成端到端开发。
- Sprint 13 目标是把这些已闭合的 ports/adapters/router 边界接入**真实 provider 与 live gate**（Kube-OVN / vCluster / Rook-Ceph / NVIDIA / MinIO / Milvus / Prometheus）。Sprint 13 只动实现与门禁，**不动契约**——因此对 Services 客户端无契约层影响。
- 本文只复核 `repo/api/openapi/v1.yaml`（Core）在既有双方接口共识之后的变更；`repo/api/openapi/services/v1.yaml` 的 Services 业务契约扩充不在本文审计或确认范围内。Services 业务资源不回流 Core。

---

## 2. 受影响的端点与 Schema 一览

| 资源域 | 端点 | 类型 | 受影响 Schema |
|---|---|---|---|
| 实例可观测性 | `/instances/{instance_id}/logs`、`/events`、`/metrics`、`/security-events`、`/exec` | 新增端点 | InstanceLogListResponse、InstanceEventListResponse、InstanceMetrics、InstanceSecurityEventListResponse、InstanceExecSession、CreateInstanceExecSessionRequest |
| 网络路由 | `/networks/routes`（list/create） | 新增端点 | NetworkRoute、NetworkRouteListResponse、CreateNetworkRouteRequest |
| 卷快照 | `/volumes/{volume_id}/snapshots`（list/**create**） | 新增端点；create 响应在 Sprint 12 内改为异步 | VolumeSnapshotRecord、VolumeSnapshotListResponse、CreateVolumeSnapshotRequest、AsyncTask |
| 文件系统挂载点 | `/filesystems/{filesystem_id}/mount-targets` | 新增端点 | FilesystemMountTarget、FilesystemMountTargetListResponse |
| 对象存储桶 | `/buckets`（list/create） | 新增端点 | StorageBucketRecord、StorageBucketListResponse、CreateStorageBucketRequest |
| 对象上传/下载 | `/objects/upload`、`/objects/{object_id}/download` | 新增端点 | StorageObjectUploadRequest、StorageObjectUploadResponse、StorageObjectDownloadInfo |
| 向量文档写入 | `/vector-stores/{vector_store_id}/documents` | 新增端点 | VectorStoreDocumentInsertRequest、VectorStoreDocumentInsertResponse |
| K8s 工作负载 | `/k8s-clusters/{cluster_id}/workloads` | 新增端点 | K8sClusterWorkload、K8sClusterWorkloadListResponse |
| GPU 库存 | `/gpu-inventory`、`/gpu-inventory/occupancy` | 新增端点 | GPUInventoryRecord、GPUInventoryListResponse、GPUOccupancyStats |
| 沙箱模板 | `/sandbox-templates` | 新增端点 | SandboxTemplate、SandboxTemplateListResponse |

---

## 3. 变更明细

### 3.0 Sprint 12 主体新增操作与端点（新增 / 向后兼容）

Sprint 12 主体提交 `6d052d3` 从 Sprint 11 收尾基线 `d52efda` 上新增 **16 条 path / 19 个 operationId**。这些端点在 Sprint 13 继续接入真实 provider 与 live gate，但 Sprint 13 未再修改 OpenAPI 契约。

| operationId | 方法 | 路径 | 资源域 |
|---|---|---|---|
| `listInstanceLogs` | GET | `/instances/{instance_id}/logs` | 实例可观测性 |
| `listInstanceEvents` | GET | `/instances/{instance_id}/events` | 实例可观测性 |
| `getInstanceMetrics` | GET | `/instances/{instance_id}/metrics` | 实例可观测性 |
| `createInstanceExecSession` | POST | `/instances/{instance_id}/exec` | 实例可观测性 |
| `listInstanceSecurityEvents` | GET | `/instances/{instance_id}/security-events` | 实例可观测性 |
| `listNetworkRoutes` | GET | `/networks/routes` | 网络路由 |
| `createNetworkRoute` | POST | `/networks/routes` | 网络路由 |
| `listVolumeSnapshots` | GET | `/volumes/{volume_id}/snapshots` | 卷快照 |
| `createVolumeSnapshot` | POST | `/volumes/{volume_id}/snapshots` | 卷快照 |
| `listFilesystemMountTargets` | GET | `/filesystems/{filesystem_id}/mount-targets` | 文件系统挂载点 |
| `listStorageBuckets` | GET | `/buckets` | 对象存储桶 |
| `createStorageBucket` | POST | `/buckets` | 对象存储桶 |
| `uploadStorageObject` | POST | `/objects/upload` | 对象上传 |
| `downloadStorageObject` | GET | `/objects/{object_id}/download` | 对象下载 |
| `insertVectorStoreDocuments` | POST | `/vector-stores/{vector_store_id}/documents` | 向量文档写入 |
| `listK8sClusterWorkloads` | GET | `/k8s-clusters/{cluster_id}/workloads` | K8s 工作负载 |
| `listGPUInventory` | GET | `/gpu-inventory` | GPU 库存 |
| `getGPUOccupancy` | GET | `/gpu-inventory/occupancy` | GPU 占用 |
| `listSandboxTemplates` | GET | `/sandbox-templates` | 沙箱模板 |

同一范围还新增了 `PreconditionFailed` 标准响应组件（`code=PRECONDITION_FAILED`）。当前 Core 契约中引用该 422 响应的操作为：`createK8sCluster`、`searchVectorStore`、`insertVectorStoreDocuments`。

### 3.1 新增异步任务枚举值（新增 / 向后兼容）

`AsyncTask` schema 新增枚举值，配合卷快照异步化（见 §3.5）：

- `task_type` 新增 `volume.snapshot.create`
- `resource_type` 新增 `volume_snapshot`

**影响**：枚举为新增值，旧客户端不受影响；处理异步任务的客户端应能识别新值。

### 3.2 列表响应新增 `total` 字段（新增 / 向后兼容）

以下 list 响应新增 `total: integer`（部分被标为 `required`，由服务端保证返回）：

InstanceLogListResponse、InstanceEventListResponse、InstanceSecurityEventListResponse、NetworkRouteListResponse、VolumeSnapshotListResponse、FilesystemMountTargetListResponse、StorageBucketListResponse、SandboxTemplateListResponse、K8sClusterWorkloadListResponse、GPUInventoryListResponse。

其中 `K8sClusterWorkloadListResponse` 与 `GPUInventoryListResponse` 在 `6d052d3` 新增时已包含 `total` 属性，后续 Sprint 12 对齐提交将 `total` 纳入 `required`；`GPUInventoryListResponse` 后续还新增并要求返回 `dev_profile`。

**影响**：纯新增 response 字段，无需客户端改造；可选用于分页总数展示。

### 3.3 新增 `dev_profile` 溯源字段（新增 / 向后兼容，但需理解语义）

多个 record / list 响应新增 `dev_profile`，引用既有 schema `CoreDevProfileInfo`（该 schema 首次引入于 Sprint 3 提交 `bea9eb3`；下方定义仅供 Services 对接时参考）：

```yaml
CoreDevProfileInfo:
  required: [mode, provider, real_provider]
  properties:
    mode:          { type: string, enum: [local, real] }
    provider:      { type: string }
    real_provider: { type: boolean }
    reason:        { type: string, nullable: true }
```

含该字段的 schema 包括：InstanceLogListResponse、InstanceEventListResponse、InstanceMetrics、InstanceSecurityEventListResponse、InstanceExecSession、NetworkRoute、VolumeSnapshotRecord、FilesystemMountTarget、K8sClusterWorkload、GPUInventoryRecord/ListResponse、GPUOccupancyStats、SandboxTemplate/ListResponse。

**含义**：该字段标识返回数据来自**本地 dev profile（`mode=local`）还是真实 provider（`mode=real`, `real_provider=true`）**。Services 在判断「该能力是否已真实可用」时应读取 `dev_profile`，不要把 `mode=local` 的成功响应当作 production 真实链路。

**影响**：纯新增 response 字段，无需改造；建议在 UI / 日志中透出以区分联调与真实环境。

### 3.4 ⚠️ ID 字段类型放宽：`format: uuid` → `string`（语义变更，**Services 需关注**）

以下字段去掉 `format: uuid` 约束，改为普通 `string`：

| Schema | 字段 |
|---|---|
| NetworkRoute | `id`、`vpc_id`、`next_hop_id` |
| CreateNetworkRouteRequest | `vpc_id`、`next_hop_id` |
| VolumeSnapshotRecord | `id`、`volume_id` |
| FilesystemMountTarget | `id`、`filesystem_id`、`subnet_id` |

**原因**：Sprint 13 接入真实 provider 后，这些 ID 来自底层组件的**原生资源标识**（Kube-OVN 路由、Rook-Ceph/CSI 快照、文件系统挂载点），并非 Core 生成的 UUID，无法保证 UUID 格式。

**对接动作（必须）**：
- Services 客户端**不得再对上述 ID 做 UUID 格式校验或类型断言**；按不透明字符串（opaque string）处理。
- 如有本地数据库列定义为 `uuid` 类型存储这些字段，需改为变长字符串。
- 这是对 response/request 的**约束放宽**：旧的合法 UUID 仍然合法；但新返回值可能不是 UUID，旧的严格校验会误判。

### 3.5 ⚠️ 卷快照创建改为异步任务（**Services 必须改造此调用**）

`POST /volumes/{volume_id}/snapshots`（`createVolumeSnapshot`）的 `202` 响应：

- **变更前**：响应体直接返回 `VolumeSnapshotRecord`（同步拿到快照记录）。
- **变更后**：响应体返回 `AsyncTask`，并新增 `Location` 响应头（任务轮询 URL）。

**原因**：真实快照（Rook-Ceph/CSI）创建是耗时异步操作，无法在请求内同步完成；统一纳入 Core 既有异步任务模型（配合 §3.1 的 `volume.snapshot.create` / `volume_snapshot`）。

**对接动作（必须）**：
- 调用方提交后拿到的是 `AsyncTask`，需按 `Location` 轮询任务状态至 `completed`，再用 `GET /volumes/{volume_id}/snapshots` 读取快照记录。
- 仍需复用同一 `idempotency_key` 进行重试。

---

## 4. 兼容性判定汇总

| 变更 | 分类 | Services 是否需改造 |
|---|---|---|
| §3.1 AsyncTask 枚举新增 | 新增 / 兼容 | 否（处理任务者建议识别新值） |
| §3.2 list `total` 新增 | 新增 / 兼容 | 否 |
| §3.3 `dev_profile` 新增 | 新增 / 兼容 | 否（建议读取以区分 local/real） |
| §3.4 ID `uuid`→`string` | 约束放宽 / 语义变更 | **是**：移除 UUID 格式校验，按 opaque string 处理 |
| §3.5 卷快照创建改异步 | response 契约变更 | **是**：改为提交任务 + 轮询模型 |

---

## 5. 验证与真实来源

- Core 契约：[`repo/api/openapi/v1.yaml`](openapi/v1.yaml)
- Core 差异复核命令：`git diff d52efda..HEAD -- repo/api/openapi/v1.yaml`
- Core 主体扩展复核命令：`git diff d52efda..6d052d3 -- repo/api/openapi/v1.yaml`
- Core 后续对齐复核命令：`git diff 6d052d3..HEAD -- repo/api/openapi/v1.yaml`

---

## Machine-readable summary

```yaml
contract_change_report:
  scope: "ANI Core Sprint 12-13"
  generated_for: "external Services team contract sync"
  scope_note: "Only Core OpenAPI changes are reported. Services OpenAPI is intentionally excluded and must not be inferred from this changelog."
  files:
    core_openapi:
      path: repo/api/openapi/v1.yaml
      changed: true
      changed_in_sprint: 12        # Sprint 13 = no contract change
      diff_range: d52efda..HEAD
      net_lines_current: "+784/-5"
      main_expansion:
        commit: 6d052d3
        diff_range: d52efda..6d052d3
        net_lines: "+742/-3"
        paths_added: 16
        operations_added: 19
      followup_alignment:
        diff_range: 6d052d3..HEAD
        net_lines: "+72/-32"
      commits: [6d052d3, 779f84e, dbd1fe8, b78ee4a, e9ae3ec]
  changes:
    - id: sprint12-new-core-operations
      type: additive
      breaking: false
      paths_added: 16
      operations:
        - { operationId: listInstanceLogs, method: GET, path: "/instances/{instance_id}/logs" }
        - { operationId: listInstanceEvents, method: GET, path: "/instances/{instance_id}/events" }
        - { operationId: getInstanceMetrics, method: GET, path: "/instances/{instance_id}/metrics" }
        - { operationId: createInstanceExecSession, method: POST, path: "/instances/{instance_id}/exec" }
        - { operationId: listInstanceSecurityEvents, method: GET, path: "/instances/{instance_id}/security-events" }
        - { operationId: listNetworkRoutes, method: GET, path: "/networks/routes" }
        - { operationId: createNetworkRoute, method: POST, path: "/networks/routes" }
        - { operationId: listVolumeSnapshots, method: GET, path: "/volumes/{volume_id}/snapshots" }
        - { operationId: createVolumeSnapshot, method: POST, path: "/volumes/{volume_id}/snapshots" }
        - { operationId: listFilesystemMountTargets, method: GET, path: "/filesystems/{filesystem_id}/mount-targets" }
        - { operationId: listStorageBuckets, method: GET, path: "/buckets" }
        - { operationId: createStorageBucket, method: POST, path: "/buckets" }
        - { operationId: uploadStorageObject, method: POST, path: "/objects/upload" }
        - { operationId: downloadStorageObject, method: GET, path: "/objects/{object_id}/download" }
        - { operationId: insertVectorStoreDocuments, method: POST, path: "/vector-stores/{vector_store_id}/documents" }
        - { operationId: listK8sClusterWorkloads, method: GET, path: "/k8s-clusters/{cluster_id}/workloads" }
        - { operationId: listGPUInventory, method: GET, path: "/gpu-inventory" }
        - { operationId: getGPUOccupancy, method: GET, path: "/gpu-inventory/occupancy" }
        - { operationId: listSandboxTemplates, method: GET, path: "/sandbox-templates" }
      added_response_components: [PreconditionFailed]
      operations_with_precondition_failed: [createK8sCluster, searchVectorStore, insertVectorStoreDocuments]
      services_action: "Regenerate/update Core SDK clients and decide which newly available Core operations each Services module consumes."
    - id: async-task-enums
      type: additive
      breaking: false
      schema: AsyncTask
      detail:
        task_type_added: [volume.snapshot.create]
        resource_type_added: [volume_snapshot]
      services_action: none
    - id: list-total-field
      type: additive
      breaking: false
      field: total
      schemas: [InstanceLogListResponse, InstanceEventListResponse, InstanceSecurityEventListResponse,
                NetworkRouteListResponse, VolumeSnapshotListResponse, FilesystemMountTargetListResponse,
                StorageBucketListResponse, SandboxTemplateListResponse, K8sClusterWorkloadListResponse,
                GPUInventoryListResponse]
      services_action: none
    - id: dev-profile-field
      type: additive
      breaking: false
      field: dev_profile
      ref_schema: CoreDevProfileInfo
      ref_schema_status: "existing since Sprint 3 / bea9eb3"
      semantics: "mode=local|real distinguishes dev-profile success from real-provider success"
      services_action: "read to distinguish local vs real; do not treat mode=local as production"
    - id: id-format-loosened
      type: constraint_relaxation
      breaking: false   # relaxation; but strict client-side UUID validation will break
      semantic_change: true
      change: "format: uuid -> plain string"
      reason: "real provider native resource identifiers are not UUIDs"
      fields:
        NetworkRoute: [id, vpc_id, next_hop_id]
        CreateNetworkRouteRequest: [vpc_id, next_hop_id]
        VolumeSnapshotRecord: [id, volume_id]
        FilesystemMountTarget: [id, filesystem_id, subnet_id]
      services_action: "remove UUID format validation; treat as opaque string"
    - id: volume-snapshot-async
      type: response_contract_change
      breaking: true
      operationId: createVolumeSnapshot
      path: POST /volumes/{volume_id}/snapshots
      from: "202 body = VolumeSnapshotRecord"
      to: "202 body = AsyncTask + Location header (task poll URL)"
      reason: "real snapshot creation is long-running async (Rook-Ceph/CSI)"
      services_action: "submit -> poll task via Location until completed -> GET snapshots; reuse idempotency_key"
```
