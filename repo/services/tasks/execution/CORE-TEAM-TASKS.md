# CORE TEAM 开发任务清单

> **Phase 3 对齐**：2026-06-17  
> **Phase 4 范围**：**仅文档交付** — Handler 实现指南见 `tasks/execution/CORE-HANDLER-IMPLEMENTATION-GUIDE.md`（本清单不含代码实现任务）  
> **契约来源**：`docs/console-modules/governance/YAML-EXPANSION-SUMMARY-2026-06-17.md`（Core +19 ops，+2 个 422 验通）  
> **总任务数**：13 | 文档已就绪：13 | Handler 待 Core 团队实现

架构约定：

- GPU 容器与 Sandbox 统一走 Core `/api/v1/instances*`（`kind=gpu_container` / `kind=sandbox`）
- **不实现** `services/v1.yaml` 中已 deprecated 的 `/gpu-containers*`、`/sandboxes*`
- OpenAPI 已声明 ≠ handler 已实现；验收以 curl/集成测试为准
- **Handler 实现契约（文档）**：`tasks/execution/CORE-HANDLER-IMPLEMENTATION-GUIDE.md`

---

## 任务索引（Phase 2 新增映射）

| TASK | Phase 2 ops | 模块详文 |
|---|---|---|
| CORE-003 | gpu-inventory ×2 | `home/home-gpu-utilization.md`、`compute/gpu-management.md` |
| CORE-005 | instances 观测 ×5 | `compute/container-observability.md` |
| CORE-006 | sandbox-templates、security-events | `compute/sandbox-instance-management.md` |
| CORE-007 | networks/routes ×2 | `compute/network/route.md` |
| CORE-008 | volumes/snapshots ×2 | `compute/storage/block-storage-snapshot.md` |
| CORE-009 | buckets、upload、download | `compute/storage/object-storage-upload.md` |
| CORE-010 | filesystem mount-targets | `compute/storage/file-storage.md` |
| CORE-011 | vector documents + search 422 | `compute/storage/vector-store-write.md` |
| CORE-012 | k8s workloads + k8s 422 | `compute/k8s-cluster-management.md` |

---

## TASK-CORE-001

状态：[x] 已完成（Phase 4 Sprint 1，2026-06-17）  
接口：`GET /api/v1/instances?kind=sandbox`（`listInstances` `kind` 枚举）  
优先级：P1  
被依赖：Console Sandbox 列表页  
本任务依赖：无  
模块详文：`docs/console-modules/compute/sandbox-instance-management.md`

需修改：

- [ ] `ANI-main/repo/api/openapi/v1.yaml`（`kind` query 枚举含 `sandbox` — 文档已对齐，handler 验通）
- [x] Gateway / Core handler：`listInstances` 按 `kind` 过滤

验收：

```bash
curl -H "X-API-Key: $TEST_KEY" "http://localhost:8080/api/v1/instances?kind=sandbox"
# 期望 200 + items[]
```

---

## TASK-CORE-002

状态：[x] 已完成（Phase 4 Sprint 1，2026-06-17）  
接口：`POST /api/v1/instances`、`POST .../lifecycle` — **422 PreconditionFailed 验通**  
优先级：P1  
被依赖：VM / 容器 / GPU 容器 / Sandbox Console 动作置灰  
本任务依赖：无  
模块详文：`vm-management.md`、`container-instance-management.md`、`gpu-container-instance-management.md`、`sandbox-instance-management.md`

Handler 逻辑：

1. 校验实例状态是否允许 `action`
2. 不允许 → `422` + `PreconditionFailed`（YAML 已举例 `INSTANCE_STATE_INVALID`）
3. 镜像缺失 → `IMAGE_NOT_FOUND`（YAML 已举例）

验收：对已在 `running` 的实例再次 `start` 返回 422

---

## TASK-CORE-003

状态：[ ] 待开始  
接口：`GET /api/v1/gpu-inventory`、`GET /api/v1/gpu-inventory/occupancy`  
operationId：`listGPUInventory`、`getGPUOccupancy`  
优先级：P1  
被依赖：首页 GPU 利用率、GPU 算力页  
本任务依赖：无  
模块详文：`docs/console-modules/home/home-gpu-utilization.md`、`compute/gpu-management.md`

> Phase 2：OpenAPI + schema **已声明**；本 TASK = handler 实现。

验收：

```bash
curl -H "X-API-Key: $TEST_KEY" "http://localhost:8080/api/v1/gpu-inventory"
curl -H "X-API-Key: $TEST_KEY" "http://localhost:8080/api/v1/gpu-inventory/occupancy"
```

---

## TASK-CORE-004

状态：[ ] 待开始  
接口：`GET /api/v1/tasks/{task_id}`（AsyncTask stub 验通）  
优先级：P2  
被依赖：任务中心、202 响应轮询  
本任务依赖：无  
模块详文：`docs/console-modules/alerts/async-task-center.md`

待补（非 Phase 2 范围，单独 backlog）：

- `GET /api/v1/tasks` list + cursor — **YAML 未声明**

验收：`GET /tasks/{uuid}` 返回 200 + `AsyncTask` 或 404

---

## TASK-CORE-005

状态：[ ] 待开始  
接口：实例观测子路径（Phase 2 **+5**）  
优先级：P1  
被依赖：容器可观测性页、Sandbox/GPU 容器排障  
本任务依赖：TASK-CORE-001（实例存在）  
模块详文：`docs/console-modules/compute/container-observability.md`

| 方法 | 路径 | operationId |
|---|---|---|
| GET | `/api/v1/instances/{instance_id}/logs` | `listInstanceLogs` |
| GET | `/api/v1/instances/{instance_id}/events` | `listInstanceEvents` |
| GET | `/api/v1/instances/{instance_id}/metrics` | `getInstanceMetrics` |
| POST | `/api/v1/instances/{instance_id}/exec` | `createInstanceExecSession` |

验收：对 `kind=container` 实例，`/logs` 返回 200；exec 需 `scope:instances:exec`

---

## TASK-CORE-006

状态：[ ] 待开始  
接口：Sandbox 扩展（Phase 2 **+2**）  
优先级：P2  
本任务依赖：TASK-CORE-001  
模块详文：`docs/console-modules/compute/sandbox-instance-management.md`

| 方法 | 路径 | operationId |
|---|---|---|
| GET | `/api/v1/sandbox-templates` | `listSandboxTemplates` |
| GET | `/api/v1/instances/{instance_id}/security-events` | `listInstanceSecurityEvents` |

验收：`GET /sandbox-templates` 返回 200

---

## TASK-CORE-007

状态：[ ] 待开始  
接口：路由表（Phase 2 **+2**）  
优先级：P2  
模块详文：`docs/console-modules/compute/network/route.md`

| 方法 | 路径 | operationId |
|---|---|---|
| GET | `/api/v1/networks/routes` | `listNetworkRoutes` |
| POST | `/api/v1/networks/routes` | `createNetworkRoute` |

验收：`POST` 创建路由返回 201 + `NetworkRoute`

---

## TASK-CORE-008

状态：[ ] 待开始  
接口：卷快照（Phase 2 **+2**）  
优先级：P2  
模块详文：`docs/console-modules/compute/storage/block-storage-snapshot.md`

| 方法 | 路径 | operationId |
|---|---|---|
| GET | `/api/v1/volumes/{volume_id}/snapshots` | `listVolumeSnapshots` |
| POST | `/api/v1/volumes/{volume_id}/snapshots` | `createVolumeSnapshot` |

验收：`POST` 返回 202 + `VolumeSnapshot`

---

## TASK-CORE-009

状态：[ ] 待开始  
接口：对象存储桶与上传/下载（Phase 2 **+4**）  
优先级：P2  
模块详文：`docs/console-modules/compute/storage/object-storage-upload.md`

| 方法 | 路径 | operationId |
|---|---|---|
| GET | `/api/v1/buckets` | `listStorageBuckets` |
| POST | `/api/v1/buckets` | `createStorageBucket` |
| POST | `/api/v1/objects/upload` | `uploadStorageObject` |
| GET | `/api/v1/objects/{object_id}/download` | `downloadStorageObject` |

验收：upload 返回 202 + `AsyncTask`

---

## TASK-CORE-010

状态：[ ] 待开始  
接口：`GET /api/v1/filesystems/{filesystem_id}/mount-targets`  
operationId：`listFilesystemMountTargets`  
优先级：P3  
模块详文：`docs/console-modules/compute/storage/file-storage.md`

验收：200 + mount targets 列表

---

## TASK-CORE-011

状态：[ ] 待开始  
接口：向量写入 + search 422 验通  
优先级：P2  
模块详文：`docs/console-modules/compute/storage/vector-store-write.md`、`vector-storage.md`

| 项 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/vector-stores/{vector_store_id}/documents` | `insertVectorStoreDocuments` → 202 |
| 422 验通 | `POST .../search` | Phase 2 已挂 `PreconditionFailed` |

验收：向量库非 ready 时 search 返回 422

---

## TASK-CORE-012

状态：[ ] 待开始  
接口：K8s 工作负载 + 创建集群 422  
优先级：P2  
模块详文：`docs/console-modules/compute/k8s-cluster-management.md`

| 项 | 路径 | operationId |
|---|---|---|
| GET | `/api/v1/k8s-clusters/{cluster_id}/workloads` | `listK8sClusterWorkloads` |
| 422 验通 | `POST /api/v1/k8s-clusters` | `createK8sCluster` |

验收：workloads 200；创建集群前置失败 422

---

## TASK-CORE-013

状态：[ ] 待开始  
接口：Branding stub、Metering 验通、遗留 schema  
优先级：P3  
本任务依赖：无

参考：`docs/console-modules/governance/schema-completion-tracker.md`

含：Branding 5 条 stub、metering handler、volume 挂载冲突 409/422（待 YAML）

---

## TASK-CORE-014

状态：**部分完成**（任务列表/取消契约已定义、等待评审，handler 待实现；告警事件仍待定）
接口：P0 告警事件 + 异步任务列表
优先级：P0（排队中）  
本任务依赖：无  
模块详文：`alerts/alerts-pending-items.md`、`alerts/async-task-center.md`  
草案：`docs/console-modules/openapi-drafts/p0/openapi-p0-yaml-draft.md` §1–§2

| 项 | 建议路径 | operationId | 说明 |
|---|---|---|---|
| list | `GET /api/v1/observability/alerts` | `listObservabilityAlerts` | 告警事件只读；方案 A |
| list | `GET /api/v1/tasks` | `listTasks` | AsyncTask cursor 分页；契约已定义，handler 待实现 |
| cancel | `POST /api/v1/tasks/{task_id}/cancel` | `cancelTask` | pending 任务幂等取消；契约已定义，handler 待实现 |

验收：评审通过后 YAML 合入；handler 200 + schema 非空；Console 详文 `TODO-YAML` 更新

---

## TASK-CORE-015

状态：[ ] 待开始  
接口：Core secrets / encryption handler 验通 + 安全域 list 待补项  
优先级：P2  
本任务依赖：无  
模块详文：`security/secrets-management.md`、`security/crypto-sm.md`、`security/audit-log.md`

| 项 | 路径 | 说明 |
|---|---|---|
| handler | `/api/v1/secrets*` | YAML 已声明 |
| handler | `/api/v1/encryption/*` | YAML 已声明 |
| 待补 | audit events list | 见 `audit-log.md` |
| 详化草案 ✅ | api-key audit | `../openapi-drafts/phase3/openapi-phase3-api-key-audit-draft.md` |
| 详化草案 ✅ | netsec policies | `../openapi-drafts/phase3/openapi-phase3-netsec-policy-draft.md` |

---

## 刻意不实现

| 项 | 原因 |
|---|---|
| `/gpu-containers*`、`/sandboxes*`（Services） | deprecated |
| ~~`GET /api/v1/alerts`~~ | 见 TASK-CORE-014 草案（observability/alerts） |
| `listInstances?kind=batch_job` | query 枚举待扩展；见 `batch-job-instances.md` |

## 相关文件

- `tasks/execution/CORE-HANDLER-IMPLEMENTATION-GUIDE.md` — Handler 实现指南（Phase 4 文档）
- `docs/console-modules/governance/YAML-EXPANSION-SUMMARY-2026-06-17.md`
- `tasks/execution/TASK-DEPENDENCY-MAP.md`
- `docs/console-modules/governance/schema-completion-tracker.md`
