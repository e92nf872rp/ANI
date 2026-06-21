# ANI Core 接口契约变更说明 · Sprint 12–13

> 面向 **外部 Services 团队** 的接口契约同步文档。
> 目的：说明 Sprint 12、Sprint 13 期间 ANI Core 对外契约的**改动范围、原因和对接动作**，供 Services 客户端同步。
> 本文同时供人工阅读与 AI Agent 加载；机器可读摘要见文末「Machine-readable summary」。

---

## 0. 结论（30 秒速览）

| 契约文件 | 是否变更 | 说明 |
|---|---|---|
| `repo/api/openapi/v1.yaml`（**Core 对外 / 跨层控制面契约**） | **是** | 仅在 **Sprint 12** 变更（+72 / −32 行）；Sprint 13 全部为真实 provider / live gate 收敛，**未改契约**。 |
| `repo/api/openapi/services/v1.yaml`（**Services 业务契约**） | **否** | 自 Sprint 12 起 **零变更**。Services 资源（models / inference-services / knowledge-bases 等）仍由外部团队在该文件维护，Core 未回流。 |

**Services 团队需要做的事**：只有两类改动会影响调用 Core 的 Services 客户端 —— ID 字段类型放宽（§3.4）与卷快照创建改为异步任务（§3.5）。其余均为**向后兼容的新增字段/枚举**，无需改造即可继续工作。

变更全部位于 Sprint 12 的 4 个提交：
`779f84e` align b1 support schemas · `dbd1fe8` add core netstore support endpoints · `b78ee4a` align netstore review closure contracts · `e9ae3ec` align objvec storage bucket contract docs。

---

## 1. 背景

- Sprint 12 目标是补齐 Core 对 Services 的「支撑型 handler」契约（observability / netstore / objvec 三组），让 Services 能基于 Core OpenAPI 完成端到端开发。
- Sprint 13 目标是把这些已闭合的 ports/adapters/router 边界接入**真实 provider 与 live gate**（Kube-OVN / vCluster / Rook-Ceph / NVIDIA / MinIO / Milvus / Prometheus）。Sprint 13 只动实现与门禁，**不动契约**——因此对 Services 客户端无契约层影响。
- 跨层契约真实来源仍是 `repo/api/openapi/v1.yaml`（Core）与 `repo/api/openapi/services/v1.yaml`（Services）。Services 业务资源不回流 Core。

---

## 2. 受影响的端点与 Schema 一览

| 资源域 | 端点 | 受影响 Schema |
|---|---|---|
| 实例可观测性 | `/instances/{instance_id}/logs`、`/events`、`/metrics`、`/security-events`、`/exec` | InstanceLogListResponse、InstanceEventListResponse、InstanceMetrics、InstanceSecurityEventListResponse、InstanceExecSession |
| 网络路由 | `/networks/routes`（list/create） | NetworkRoute、NetworkRouteListResponse、CreateNetworkRouteRequest |
| 卷快照 | `/volumes/{volume_id}/snapshots`（list/**create**） | VolumeSnapshotRecord、VolumeSnapshotListResponse、AsyncTask |
| 文件系统挂载点 | `/filesystems/{filesystem_id}/mount-targets` | FilesystemMountTarget、FilesystemMountTargetListResponse |
| 对象存储桶 | `/buckets` | StorageBucketListResponse |
| K8s 工作负载 | `/k8s-clusters/{cluster_id}/workloads` | K8sClusterWorkload、K8sClusterWorkloadListResponse |
| GPU 库存 | `/gpu-inventory`、`/gpu-inventory/occupancy` | GPUInventoryRecord、GPUInventoryListResponse、GPUOccupancyStats |
| 沙箱模板 | `/sandbox-templates` | SandboxTemplate、SandboxTemplateListResponse |

---

## 3. 变更明细

### 3.1 新增异步任务枚举值（新增 / 向后兼容）

`AsyncTask` schema 新增枚举值，配合卷快照异步化（见 §3.5）：

- `task_type` 新增 `volume.snapshot.create`
- `resource_type` 新增 `volume_snapshot`

**影响**：枚举为新增值，旧客户端不受影响；处理异步任务的客户端应能识别新值。

### 3.2 列表响应新增 `total` 字段（新增 / 向后兼容）

以下 list 响应新增 `total: integer`（部分被标为 `required`，由服务端保证返回）：

InstanceLogListResponse、InstanceEventListResponse、InstanceSecurityEventListResponse、NetworkRouteListResponse、VolumeSnapshotListResponse、FilesystemMountTargetListResponse、StorageBucketListResponse、SandboxTemplateListResponse、GPUInventoryListResponse（原已含 `total`，本次改为 required）。

**影响**：纯新增 response 字段，无需客户端改造；可选用于分页总数展示。

### 3.3 新增 `dev_profile` 溯源字段（新增 / 向后兼容，但需理解语义）

多个 record / list 响应新增 `dev_profile`，引用新 schema：

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
- Services 契约：[`repo/api/openapi/services/v1.yaml`](openapi/services/v1.yaml)（本期未变更）
- 差异复核命令：`git diff 6d052d3..HEAD -- repo/api/openapi/v1.yaml`
- Services 契约未变更复核：`git log a49dc2a..HEAD -- repo/api/openapi/services/v1.yaml`（输出为空）

---

## Machine-readable summary

```yaml
contract_change_report:
  scope: "ANI Core Sprint 12-13"
  generated_for: "external Services team contract sync"
  files:
    core_openapi:
      path: repo/api/openapi/v1.yaml
      changed: true
      changed_in_sprint: 12        # Sprint 13 = no contract change
      diff_range: 6d052d3..HEAD
      net_lines: "+72/-32"
      commits: [779f84e, dbd1fe8, b78ee4a, e9ae3ec]
    services_openapi:
      path: repo/api/openapi/services/v1.yaml
      changed: false
  changes:
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
                StorageBucketListResponse, SandboxTemplateListResponse, GPUInventoryListResponse]
      services_action: none
    - id: dev-profile-field
      type: additive
      breaking: false
      field: dev_profile
      ref_schema: CoreDevProfileInfo
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
