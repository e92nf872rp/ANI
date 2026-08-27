# SPEC: GPU 规格与配额管理

> Technical specification derived from:
> - PRD: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
> - UX: `repo/services/tasks/modules/ux/core/gpu-spec-quota/ux-gpu-spec-quota.md`
> Generated: 2026-08-12 | Target branch: feature/gpu-spec-quota | Product line: core + console + boss

---

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 覆盖 GPU 规格与配额管理的全栈实现：Core API 契约扩展、Ports 接口定义、Adapters 层实现（GPUSpec CRD + Volcano 翻译 + Inventory 扩展 + Reconciler 改造）、Gateway Handler 路由、BOSS 前端改版、Console 前端改版及集成验收。实现"节点打标签 → BOSS 分配配额/预留 → Console 选规格创建实例 → TCC 预检 + Volcano 调度 + Confirm/Cancel"完整闭环。

### 1.2 PRD Reference

- Source: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
- UX source: `repo/services/tasks/modules/ux/core/gpu-spec-quota/ux-gpu-spec-quota.md`
- User Stories covered: US-001 ~ US-007
- Functional Requirements covered: FR-1 ~ FR-28

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| GPUSpec 持久化 | K8s CRD（GPUSpec CRD） | 规格是集群级静态配置，对齐节点标签，复用 volcano_queue_store.go CRD 操作模式 |
| Outbox 集成 | 复用已有 outbox_events 表 + OutboxRepo | 表已由 init_schema 创建，publisher 已在 task-service 实现；本批次定义 outboxWriter 接口 + 复用 OutboxRepo 写入 |
| 配额三层量 | total → allocated_gpu_count → used+reserved | TCC 方案不可改，只做字段映射；新增 resource_reservation_allocations 表存 allocated_gpu_count |
| WorkloadInstanceStoreTx | 小接口（仅 UpsertStatusTx） | 不破坏现有 6 个 mock，最小侵入 |
| HAMi 废弃 | 删除 isHAMINode/parseHAMIAnnotation/hami-scheduler 分支 | 统一用 volcano scheduler 和 volcano.sh/node-vgpu-register annotation |
| SPEC 文档 | 全量一个 SPEC | Core + Frontend 统一管理依赖关系 |

---

## 2. Architecture

### 2.1 System Context

```text
┌─────────────────────────────────────────────────────┐
│                    BOSS Frontend                      │
│  /ops/gpu-pool (KPI + 4 Tabs + Drawer)               │
└──────────────┬──────────────────────────┬───────────┘
               │ Core REST API              │ Services API
               │ /api/v1/*                  │ /api/v1/svc/*
               ▼                            ▼
┌──────────────────────────┐  ┌────────────────────────┐
│    ANI Gateway Handler    │  │   Services Gateway     │
│  (ani-gateway)            │  │  (model/kb service)    │
│  gpu_spec.go (NEW)        │  └────────────────────────┘
│  gpu_inventory.go (EXT)   │
│  instance.go (EXT)        │
│  quota_resources.go (EXT) │
└──────────┬───────────────┬┘
           │               │
           ▼               ▼
┌──────────────────────────────────────────────────────┐
│                   Ports Layer                          │
│  GPUSpecStore (NEW)       QuotaService (复用)          │
│  WorkloadInstanceStoreTx  QuotaStoreService (复用)    │
│  (NEW 小接口)             GPUInventory (EXT)           │
└──────────┬──────────────┬──────────────┬────────────┘
           │              │              │
           ▼              ▼              ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────────┐
│ K8s CRD      │ │ PostgreSQL   │ │ K8s API          │
│ GPUSpec CRD  │ │ resource_    │ │ Node labels      │
│ (NEW)        │ │ quota (已有) │ │ Pod resources    │
│              │ │ resource_    │ │ Volcano Queue CRD │
│              │ │ reservation_ │ │ (status.allocated │
│              │ │ allocations  │ │  + status.state) │
│              │ │ (NEW)        │ │                  │
└──────────────┘ └──────────────┘ └──────────────────┘

┌─────────────────────────────────────────────────────┐
│                  Console Frontend                     │
│  /compute/gpu-containers (列表+配额卡片+创建Dialog)    │
│  /settings/gpu-queues (扩展 allocated 列 + state 徽标列)│
└──────────────┬──────────────────────────┬───────────┘
               │ Core REST API              │ Services API
               │ /api/v1/*                  │ /api/v1/svc/*
```

### 2.2 Component Design

| Component | Responsibility | Location |
|-----------|---------------|----------|
| GPUSpec CRD | 集群级规格目录持久化 | `deploy/manifests/gpu-spec-a/00-gpuspec-crd.yaml` |
| CRDGPUSpecStore | K8s CRD 读写实现 GPUSpecStore | `pkg/adapters/runtime/crd_gpu_spec_store.go` |
| VolcanoResourceTranslator | spec_id → Pod 资源请求 + nodeSelector + queue annotation | `pkg/adapters/runtime/volcano_resource_translator.go` |
| GPUInventory(EXT) | 扩展 ListSpecAvailability + 节点标签派生 + parseVolcanoVGPUAnnotation | `pkg/adapters/runtime/kubernetes_gpu_inventory.go` |
| ReconcileController(EXT) | Confirm/Cancel 同事务 + 超时 + 双调释放 + 对账 | `pkg/adapters/runtime/reconcile_controller.go` |
| OutboxWriter | outbox_writer.go 小接口 + 复用 OutboxRepo 写入 | `pkg/adapters/runtime/outbox_writer.go` |
| InstanceStoreTx | WorkloadInstanceStoreTx 实现 UpsertStatusTx | `pkg/adapters/runtime/instance_store.go` |
| GPU Spec Handler | 规格目录 4 端点 REST handler | `services/ani-gateway/internal/router/gpu_spec_resources.go` |
| Quota Handler(EXT) | 扩展配额端点 + 预留端点 | `services/ani-gateway/internal/router/quota_resources.go` |
| BOSS GPU Pool(EXT) | 4 Tab + 配额/预留/规格 Drawer | `frontends/boss/src/routes/_authenticated/ops/gpu-pool.tsx` |
| Console Create Dialog(EXT) | 规格四态下拉 + 队列必选 | `frontends/console/src/routes/_authenticated/compute/gpu-containers/-create-dialog.tsx` |
| Console List(EXT) | 配额/预留卡片 | `frontends/console/src/routes/_authenticated/compute/gpu-containers/index.tsx` |
| Console Queue(EXT) | allocated 列 + state 徽标列 | `frontends/console/src/routes/_authenticated/settings/gpu-queues.tsx` |

### 2.3 Module Interactions

```text
[创建实例流程]
Console Dialog → POST /instances (spec_id)
  → instance handler → instanceSpecFromRequest (spec_id 解析)
  → LocalInstanceService.Create
    → LocalInstanceOrchestrator.Create
      → VolcanoResourceTranslator.Translate(spec_id) → Pod spec
      → QuotaService.TryManyTx (同事务预占)
      → WorkloadInstanceStoreTx.UpsertStatusTx (同事务写 DB)
      → WorkloadProviderApply.Apply (K8s)
    → ReconcileController.runOnce
      → reader.Observe (Pod 状态)
      → QuotaService.Confirm/Cancel (同事务)
      → WorkloadInstanceStoreTx.UpsertStatusTx

[删除实例流程]
Console → DELETE /instances/{id}
  → LocalInstanceService.applyLifecycle(Delete)
    → store.Get → transition(deleting)
    → ReconcileController
      → Cancel + Release 双调
      → WorkloadInstanceStoreTx.UpsertStatusTx(deleted)
```

### 2.4 File Structure

```
repo/
├── api/openapi/v1.yaml                              [MODIFY: 新增 schema + 端点]
├── deploy/
│   ├── manifests/gpu-spec-a/
│   │   └── 00-gpuspec-crd.yaml                      [NEW: GPUSpec CRD 定义]
│   └── migrations/
│       ├── 20260812000100_quota_tx_ids.sql            [NEW: workload_instances + quota_tx_ids]
│       └── 20260812000200_resource_reservation_allocations.sql [NEW: 预留账本表]
├── pkg/
│   ├── ports/
│   │   ├── gpu_spec.go                              [NEW: GPUSpecStore 接口]
│   │   ├── workload_runtime.go                      [MODIFY: + WorkloadInstanceStoreTx]
│   │   └── gpu_inventory.go                         [MODIFY: + ListSpecAvailability]
│   └── adapters/runtime/
│       ├── crd_gpu_spec_store.go                    [NEW: GPUSpecStore 实现]
│       ├── volcano_resource_translator.go           [NEW: Volcano 翻译]
│       ├── outbox_writer.go                         [NEW: outboxWriter 接口]
│       ├── kubernetes_gpu_inventory.go              [MODIFY: +ListSpecAvailability +parseVolcanoVGPUAnnotation -HAMi]
│       ├── reconcile_controller.go                  [MODIFY: +Confirm/Cancel同事务 +超时 +双调]
│       ├── instance_orchestrator.go                 [MODIFY: +Release +UpsertStatusTx(failed)]
│       ├── instance_store.go                        [MODIFY: +UpsertStatusTx 实现]
│       ├── demo_instances.go                        [MODIFY: +TryManyTx +Volcano翻译 +Cancel异常]
│       ├── volcano_queue_store.go                   [MODIFY: +Status字段(Allocated+State) +allocated/state映射]
│       └── instance_service.go                      [MODIFY: +spec_id 解析逻辑]
├── pkg/bootstrap/
│   ├── server.go                                    [MODIFY: +GPUQuotaEnabled +PROVISIONING_TIMEOUT_MIN]
│   └── deps.go                                      [MODIFY: +注入新 store]
├── services/ani-gateway/
│   ├── internal/router/
│   │   ├── gpu_spec_resources.go                    [NEW: 规格目录 4 端点]
│   │   ├── gpu_inventory_resources.go               [MODIFY: +PATCH 设备 + 扩展字段]
│   │   ├── reservation_resources.go                 [NEW: 预留端点 + /quotas/me + /reservations/me]
│   │   ├── router.go                                [MODIFY: +注册新路由]
│   │   └── instances.go                             [MODIFY: +spec_id 接入]
│   └── gpu_inventory_runtime.go                     [MODIFY: +GPUSpecStore 注入]
├── frontends/boss/src/
│   ├── routes/_authenticated/ops/
│   │   ├── gpu-pool.tsx                             [MODIFY: +Tab4规格 +配额Drawer +预留Drawer]
│   │   ├── gpu-pool-quota-drawer.tsx                [NEW: 配额/预留分配 Drawer]
│   │   └── gpu-spec-drawer.tsx                      [NEW: 规格管理 Drawer]
│   └── api/coreClient.ts                            [MODIFY: +quota/spec 调用]
└── frontends/console/src/
    ├── routes/_authenticated/compute/gpu-containers/
    │   ├── index.tsx                                [MODIFY: +配额/预留卡片]
    │   └── -create-dialog.tsx                       [MODIFY: →规格下拉四态+队列必选]
    ├── routes/_authenticated/settings/
    │   └── gpu-queues.tsx                            [MODIFY: +allocated列 +state徽标列]
    └── api/coreClient.ts                            [MODIFY: +quota/availability 调用]
```

---

## 3. Data Model

### 3.1 Schema Changes

#### 新增表：resource_reservation_allocations

```sql
-- 20260812000200_resource_reservation_allocations.sql
BEGIN;
CREATE TABLE IF NOT EXISTS resource_reservation_allocations (
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    allocated_gpu_count BIGINT NOT NULL DEFAULT 0 CHECK (allocated_gpu_count >= 0),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id)
);
COMMIT;
```

#### 扩展表：workload_instances + quota_tx_ids

```sql
-- 20260812000100_quota_tx_ids.sql
BEGIN;
ALTER TABLE workload_instances
    ADD COLUMN IF NOT EXISTS quota_tx_ids JSONB NOT NULL DEFAULT '[]';
COMMIT;
```

#### 已有表（复用，不修改 schema）

- `resource_quota`：`tenant_id, resource_type, total, used, reserved, updated_at`（TCC 核心表）
- `resource_quota_meta`：`resource_type, enabled, default_quota, display_name, unit, is_discrete`
- `resource_reservations`：`tx_id, tenant_id, resource_type, amount, expires_at, state`
- `outbox_events`：`id, aggregate_type, aggregate_id, event_type, tenant_id, payload, published, published_at, created_at`

### 3.2 Entity Definitions

#### GPUSpec CRD（K8s CRD）

```yaml
apiVersion: ani.kubercloud.io/v1
kind: GPUSpec
metadata:
  name: nvidia-a100-sxm4-80gb  # spec_id, 格式 {gpu_type}-{mem}-{shares}
  labels:
    ani.io/idempotency-key: <uuid>  # 对齐 plan.md §4.1
spec:
  gpu_type: "NVIDIA-A100-SXM4-80GB"  # 必须对齐节点标签 gpu-spec / gpu-sharing-spec 的值
  gpu_mode: "wholecard"               # wholecard | vgpu, 对齐节点标签 gpu-mode
  memory_total_mb: 80640              # 物理卡显存,用于校验
  shares: 1                            # 1=整卡, >1=vGPU; 从 gpu-sharing-policy 派生: quarter=4, half=2
  mb_per_share: 80640                  # 整卡=卡显存, vGPU=从 gpu-sharing-spec 派生
  compute_per_share: null              # 算力字段预留(本次不切,后续补)
  node_affinity:
    gpu_spec: "NVIDIA-A100-SXM4-80GB"           # 整卡时用 ani.kubercloud.io/gpu-spec 值
    gpu_sharing_spec: ""                        # vGPU 时用 ani.kubercloud.io/gpu-sharing-spec 值
    gpu_sharing_policy: ""                      # vGPU 时用 ani.kubercloud.io/gpu-sharing-policy 值
    gpu_mode: "wholecard"                        # 整卡/vGPU 隔离, ani.kubercloud.io/gpu-mode 值
  volcano_resources:
    # 整卡模式
    wholecard:
      nvidia.com/gpu: "{count}"                  # NVIDIA 整卡
      huawei.com/Ascend910: "{count}"            # 华为整卡
    # vGPU 模式
    vgpu:
      volcano.sh/vgpu-memory: "{mb_per_share}"   # 由 gpu-sharing-spec 算出,不能传空
      volcano.sh/vgpu-number: "{count}"
```

> **节点标签 key 规则**（对齐 plan.md §4.2）：
> - 整卡节点：`ani.kubercloud.io/gpu-spec` + `ani.kubercloud.io/gpu-mode: wholecard`
> - vGPU 节点：`ani.kubercloud.io/gpu-sharing-spec` + `ani.kubercloud.io/gpu-sharing-policy` + `ani.kubercloud.io/gpu-mode: vgpu`

#### Go 类型

```go
// pkg/ports/gpu_spec.go (NEW)
// Delete 签名带 idempotencyKey，对齐 v1.yaml DELETE /gpu-specs/{spec_id} 的 Idempotency-Key header
type GPUSpecStore interface {
    List(ctx context.Context) ([]GPUSpecCRD, error)
    Get(ctx context.Context, specID string) (GPUSpecCRD, error)
    Create(ctx context.Context, idempotencyKey string, spec GPUSpecCRD) (GPUSpecCRD, error)
    Delete(ctx context.Context, idempotencyKey string, specID string) error
}

// GPUSpecCRD 对齐 v1.yaml GPUSpec / GPUSpecSummary schema
type GPUSpecCRD struct {
    ID               string
    Name             string // Console 展示名称; 为空时由 spec_id 派生 (对齐 v1.yaml name 字段)
    GPUType          string
    GPUMode          string   // wholecard | vgpu
    MemoryTotalMB    int64
    Shares           int
    MBPerShare       int
    Available        bool     // 是否允许用于新实例创建 (对齐 v1.yaml available 字段)
    ComputePerShare   *int64  // 算力预留,本次 null
    NodeAffinity      GPUSpecNodeAffinity
    VolcanoResources  GPUSpecVolcanoResources
}

// GPUSpecNodeAffinity 对齐 plan.md §4.1/§4.2 节点标签 key
type GPUSpecNodeAffinity struct {
    GPUSpec          string // 整卡: ani.kubercloud.io/gpu-spec 值
    GPUSharingSpec   string // vGPU: ani.kubercloud.io/gpu-sharing-spec 值
    GPUSharingPolicy string // vGPU: ani.kubercloud.io/gpu-sharing-policy 值
    GPUMode          string // ani.kubercloud.io/gpu-mode 值 (wholecard | vgpu)
}

// GPUSpecVolcanoResources 嵌套 wholecard / vgpu 两种模式 (对齐 plan.md §4.1)
type GPUSpecVolcanoResources struct {
    Wholecard map[string]string // nvidia.com/gpu / huawei.com/Ascend910
    VGPU      map[string]string // volcano.sh/vgpu-memory / volcano.sh/vgpu-number
}

// GPUSpec sentinel errors (SPEC §6.1 error taxonomy)
var (
    ErrGPUSpecNotFound = errors.New("gpu spec not found")
    ErrGPUSpecConflict = errors.New("gpu spec already exists")
    ErrGPUSpecInUse    = errors.New("gpu spec in use by running instances")
)

// pkg/ports/workload_runtime.go (MODIFY: 新增小接口)
// UpsertStatusTx 接收完整 WorkloadInstanceRecord（含 QuotaTxIDs），而非仅状态枚举，
// 因为事务场景需在同事务内写入 quota_tx_ids + status
type WorkloadInstanceStoreTx interface {
    UpsertStatusTx(ctx context.Context, tx MetadataTx, record WorkloadInstanceRecord) error
}

// pkg/adapters/runtime/outbox_writer.go (NEW)
type outboxWriter interface {
    WriteTx(ctx context.Context, tx MetadataTx, event OutboxEvent) error
}

type OutboxEvent struct {
    AggregateType string
    AggregateID   string
    EventType     string
    TenantID      string
    Payload       []byte
}
```

### 3.3 Relationships

- `resource_reservation_allocations.tenant_id` → `tenants.id` (FK, CASCADE)
- `workload_instances.quota_tx_ids` → `resource_reservations.tx_id` (JSONB array of tx_id strings, 逻辑关联非 FK)
- `GPUSpec CRD.metadata.name` = spec_id，创建实例时通过 `spec_id` 引用
- `GPUSpec.spec.node_affinity` 对齐 K8s 节点标签：整卡用 `gpu-spec` + `gpu-mode`，vGPU 用 `gpu-sharing-spec` + `gpu-sharing-policy` + `gpu-mode`

### 3.4 Migration Plan

| 顺序 | 迁移文件 | 操作 | 回滚 |
|------|---------|------|------|
| 1 | `20260812000100_quota_tx_ids.sql` | ALTER TABLE workload_instances ADD COLUMN | DROP COLUMN quota_tx_ids |
| 2 | `20260812000200_resource_reservation_allocations.sql` | CREATE TABLE | DROP TABLE |

迁移使用 `IF NOT EXISTS` / `IF EXISTS` 保证幂等。迁移前需确保 `tenants` 表存在（已有）。

---

## 4. API Design

### 4.1 Frozen Facts Table（Core）

| Category | Item | Source | Status |
|----------|------|--------|--------|
| Frozen Path | `GET /gpu-inventory` | v1.yaml operationId `listGPUInventory` | 已冻结 |
| Frozen Path | `GET /gpu-inventory/occupancy` | v1.yaml operationId `getGPUOccupancy` | 已冻结 |
| Frozen Path | `GET /gpu-specs` | v1.yaml operationId `listGPUSpecs` | 已冻结 |
| Frozen Path | `GET /gpu-specs/{spec_id}` | v1.yaml operationId `getGPUSpec` | 已冻结 |
| Frozen Path | `GET /gpu-scheduling/queues` | v1.yaml operationId `listGPUSchedulingQueues` | 已冻结 |
| Frozen Path | `POST /admin/tenants/{tenant_id}/quota` | v1.yaml operationId `createTenantQuota` | 已冻结（已实现） |
| Frozen Path | `PUT /admin/tenants/{tenant_id}/quota` | v1.yaml operationId `updateTenantQuota` | 已冻结（已实现） |
| Frozen Path | `GET /admin/tenants/{tenant_id}/quota` | v1.yaml operationId `getTenantQuota` | 已冻结（已实现） |
| Frozen Path | `DELETE /admin/tenants/{tenant_id}/quota` | v1.yaml operationId `deleteTenantQuota` | 已冻结（已实现） |
| Frozen Path | `GET /admin/quota-meta` | v1.yaml operationId `listQuotaMeta` | 已冻结（已实现） |
| Frozen Schema | `GPUSpecSummary` | v1.yaml line 2980 | 已冻结（扩展可选字段：+gpu_mode/+node_affinity/+volcano_resources，向后兼容；GET /gpu-specs 与 POST /gpu-specs 读写闭环，详见 §4.4） |
| Frozen Schema | `GPUOccupancyStats` | v1.yaml line 2996 | 已冻结（需扩展 vgpu_count/wholecard_count） |
| Frozen Schema | `GPUSchedulingQueue` | v1.yaml line 3016 | 已冻结（需扩展 status：allocated + state） |
| Non-Frozen | `POST /gpu-specs` | 待补 | 新增 |
| Non-Frozen | `DELETE /gpu-specs/{spec_id}` | 待补 | 新增 |
| Non-Frozen | `GET /gpu-specs/availability` | 待补 | 新增 |
| Non-Frozen | `PATCH /gpu-inventory/{device_id}` | 待补 | 新增 |
| Non-Frozen | `GET /quotas/me` | 待补 | 新增 |
| Non-Frozen | `PUT /admin/tenants/{tenant_id}/reservations` | 待补 | 新增 |
| Non-Frozen | `GET /admin/tenants/{tenant_id}/reservations` | 待补 | 新增 |
| Non-Frozen | `GET /reservations/me` | 待补 | 新增 |

### 4.2 OpenAPI Change Plan (Core)

> **已有端点（不在本批次重复实现）**：`PUT /admin/tenants/{tenant_id}/quota`（operationId `updateTenantQuota`）已由佳生/Leader 实现，handler 在 `quota_resources.go`。本批次不碰配额管理 handler，只新增预留端点和 Console 自查端点。

| Change | operationId | Compatibility | idempotency_key | 本批次? |
|--------|-------------|---------------|-----------------|---------|
| 扩展 GPUNodeClass +gpu_mode/+gpu_spec/+gpu_sharing_spec/+gpu_sharing_policy | N/A (schema) | 可选字段，兼容 | N/A | 是 |
| 扩展 CreateGPUContainerInstanceConfig.gpu +spec_id | N/A (schema) | 可选字段，兼容 | N/A | 是 |
| 扩展 CreateInstanceRequest +network_config | N/A (schema) | 可选字段，兼容 | N/A | 是 |
| 扩展 GPUOccupancyStats +vgpu_count/+wholecard_count | N/A (schema) | 可选字段，兼容 | N/A | 是 |
| 扩展 GPUSchedulingQueue +status (allocated + state) | N/A (schema) | 可选字段，兼容 | N/A | 是 |
| 扩展 GPUSpecSummary +gpu_mode/+node_affinity/+volcano_resources | N/A (schema) | 可选字段，兼容（老客户端可忽略） | N/A | 是 |
| 新增 schema GPUSpec | N/A (schema) | 新增 schema（POST 创建后返回的完整视图） | N/A | 是 |
| 新增 POST /gpu-specs | createGPUSpec | 新增端点 | header Idempotency-Key | 是 |
| 新增 DELETE /gpu-specs/{spec_id} | deleteGPUSpec | 新增端点 | N/A | 是 |
| 新增 GET /gpu-specs/availability | listGPUSpecAvailability | 新增端点 | N/A | 是 |
| 新增 PATCH /gpu-inventory/{device_id} | updateGPUDeviceStatus | 新增端点 | N/A | 是 |
| 新增 GET /quotas/me | getMyQuota | 新增端点 | N/A | 是 |
| 新增 GET /quotas | listQuotas | 新增端点 | N/A | 是 |
| 新增 PUT /admin/tenants/{tenant_id}/reservations | putTenantReservations | 新增端点 | header Idempotency-Key | 是 |
| 新增 GET /admin/tenants/{tenant_id}/reservations | getTenantReservations | 新增端点 | N/A | 是 |
| 新增 GET /reservations/me | getMyReservations | 新增端点 | N/A | 是 |
| 已有 PUT /admin/tenants/{tenant_id}/quota | updateTenantQuota | 已实现 | header Idempotency-Key | 否（佳生已实现） |
| 已有 GET /admin/tenants/{tenant_id}/quota | getTenantQuota | 已实现 | N/A | 否（佳生已实现） |
| 已有 POST /admin/tenants/{tenant_id}/quota | createTenantQuota | 已实现 | header Idempotency-Key | 否（佳生已实现） |
| 已有 DELETE /admin/tenants/{tenant_id}/quota | deleteTenantQuota | 已实现 | header Idempotency-Key | 否（佳生已实现） |

### 4.3 Endpoints

| Method | Path | Description | Auth scope | Request | Response |
|--------|------|-------------|------------|---------|----------|
| GET | /gpu-specs | 列出规格目录 | gpu-spec:read | ?gpu_type=&available=&limit=&cursor= | GPUSpecListResponse |
| POST | /gpu-specs | 创建规格 | gpu-spec:write | GPUSpecCreateRequest + Idempotency-Key header | 201 GPUSpec |
| GET | /gpu-specs/{spec_id} | 查单个规格 | gpu-spec:read | path param | GPUSpecSummary |
| DELETE | /gpu-specs/{spec_id} | 删除规格 | gpu-spec:write | path param | 204 |
| GET | /gpu-specs/availability | 规格可用性 | gpu-spec:read | 无 | GPUSpecAvailabilityListResponse |
| PATCH | /gpu-inventory/{device_id} | 翻转设备状态 | gpu-inventory:write | {status: maintenance\|idle} | GPUInventoryRecord |
| GET | /quotas/me | 查自己配额 | quota:read (tenant) | 无 | QuotaView |
| GET | /quotas | BOSS 分页列表 | quota:read (platform) | ?limit=&cursor= | QuotaListResult |
| PUT | /admin/tenants/{tenant_id}/reservations | 设预留额度 | quota:write (platform) | ReservationPutRequest + Idempotency-Key | ReservationView |
| GET | /admin/tenants/{tenant_id}/reservations | 查租户预留 | quota:read (platform) | path param | ReservationView |
| GET | /reservations/me | 查自己预留 | quota:read (tenant) | 无 | ReservationView |

> **已有端点（不在本批次）**：`PUT/GET/POST/DELETE /admin/tenants/{tenant_id}/quota` + `GET /admin/quota-meta` 已由佳生/Leader 实现（handler 在 `quota_resources.go`），本批次不重复实现。

### 4.4 Request/Response Schemas

#### GPUSpecSummary 与 GPUSpec 的关系

本批次对 `GPUSpecSummary`（已冻结 schema）做向后兼容的**可选字段扩展**，并新增 `GPUSpec` schema，形成 GET/POST 读写闭环：

- `GPUSpecSummary`（扩展后）：GET /gpu-specs 列表、GET /gpu-specs/{spec_id} 单查的响应 schema。新增 `gpu_mode`/`node_affinity`/`volcano_resources` 三个**可选**字段，对齐写视图；老客户端可忽略，向后兼容。
- `GPUSpec`（新增）：POST /gpu-specs 创建成功后的响应 schema。字段集与 `GPUSpecSummary` 相同，但 `gpu_mode`/`node_affinity`/`volcano_resources` 为**必填**（创建后已落库，必返回完整视图）。
- 两者不合并：`GPUSpecSummary` 保持"读视图"定位（字段可空，容错），`GPUSpec` 保持"写视图"定位（字段必填，强约束）。`GPUSpec` 是 `GPUSpecSummary` 的"完整态"超集约束，而非新字段超集。

```yaml
GPUSpecSummary:  # 扩展（向后兼容，新增可选字段）
  type: object
  description: |
    Core 集群级 GPU 规格只读视图。spec_id 描述 GPU 资源形态，不代表租户配额；
    本契约不执行 quota check、acquire 或 release。
    gpu_mode/node_affinity/volcano_resources 为可选扩展字段，对齐 GPUSpec 写视图，
    使 GET /gpu-specs 与 POST /gpu-specs 读写闭环（向后兼容，老客户端可忽略）。
  required: [id, name, gpu_type, shares, mb_per_share, available]
  properties:
    id:              { type: string }
    name:            { type: string }
    gpu_type:        { type: string }
    gpu_mode:        { type: string, enum: [wholecard, vgpu] }          # 可选扩展
    memory_total_mb: { type: integer, minimum: 1, nullable: true }
    shares:          { type: integer, minimum: 1 }
    mb_per_share:    { type: integer, minimum: 1 }
    available:       { type: boolean }
    node_affinity:   { $ref: '#/components/schemas/GPUSpecNodeAffinity' }        # 可选扩展
    volcano_resources: { $ref: '#/components/schemas/GPUSpecVolcanoResources' } # 可选扩展

GPUSpec:  # 新增（POST 创建后返回的完整视图）
  type: object
  description: |
    GPU 规格完整视图（POST 创建后返回）。含节点亲和性和 Volcano 资源声明，
    用于管理端展示和实例创建准入校验。
  required: [id, name, gpu_type, gpu_mode, shares, mb_per_share, available]
  properties:
    id:              { type: string }
    name:            { type: string }
    gpu_type:        { type: string }
    gpu_mode:        { type: string, enum: [wholecard, vgpu] }          # 必填
    memory_total_mb: { type: integer, minimum: 1, nullable: true }
    shares:          { type: integer, minimum: 1 }
    mb_per_share:    { type: integer, minimum: 1 }
    available:       { type: boolean }
    node_affinity:     { $ref: '#/components/schemas/GPUSpecNodeAffinity' }      # 必填
    volcano_resources: { $ref: '#/components/schemas/GPUSpecVolcanoResources' } # 必填
```

#### GPUSpecCreateRequest

```yaml
GPUSpecCreateRequest:
  type: object
  required: [spec_id, gpu_type, gpu_mode, shares, mb_per_share]
  properties:
    spec_id:        { type: string, pattern: "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$" }
    gpu_type:       { type: string }
    gpu_mode:       { type: string, enum: [wholecard, vgpu] }
    shares:         { type: integer, minimum: 1 }
    mb_per_share:   { type: integer, minimum: 1 }
    memory_total_mb: { type: integer, minimum: 1, nullable: true }
```

#### GPUSpecAvailabilityResponse

```yaml
GPUSpecAvailability:
  type: object
  required: [spec_id, status, available_count, has_matching_nodes, has_idle_devices, device_idle_count]
  properties:
    spec_id:           { type: string }
    status:            { type: string, enum: [available, full, device_full, unavailable] }
    available_count:   { type: integer, minimum: 0 }
    has_matching_nodes: { type: boolean }
    has_idle_devices:  { type: boolean }
    device_idle_count: { type: integer, minimum: 0 }
    gpu_count:         { type: integer, minimum: 1 }  # 每份规格占用的卡数

GPUSpecAvailabilityListResponse:
  type: object
  required: [items, quota_remaining]
  properties:
    quota_remaining: { type: integer, description: "租户配额剩余（allocated - used - reserved），跨规格共享，仅在顶层返回一次" }
    items: { type: array, items: { $ref: '#/components/schemas/GPUSpecAvailability' } }
```

#### ReservationPutRequest / ReservationView

```yaml
ReservationPutRequest:
  type: object
  required: [allocated_gpu_count]
  properties:
    allocated_gpu_count: { type: integer, minimum: 0 }

ReservationView:
  type: object
  required: [tenant_id, allocated_gpu_count, used, reserved, available]
  properties:
    tenant_id:          { type: string, format: uuid }
    allocated_gpu_count: { type: integer }
    used:               { type: integer }
    reserved:           { type: integer }
    available:          { type: integer }  # allocated - used - reserved
    tightened:          { type: boolean }  # 下调被 clamp 时 true
```

#### GPUDeviceStatusUpdateRequest

```yaml
GPUDeviceStatusUpdateRequest:
  type: object
  required: [status]
  properties:
    status: { type: string, enum: [maintenance, idle] }
```

### 4.5 Error Responses

| Error Code | HTTP Status | Condition | User Message |
|------------|-------------|-----------|--------------|
| GPUSpecNotFound | 404 | spec_id 不存在 | 规格不存在 |
| GPUSpecInUse | 409 | 有实例引用该规格 | 该规格有运行中实例引用，无法删除 |
| GPUSpecConflict | 409 | spec_id 已存在 | 规格已存在 |
| GPUTypeNotInNodes | 422 | gpu_type 不存在于节点标签 | GPU 型号不存在于集群节点标签中 |
| QUOTA_EXCEEDED | 409 | used+reserved+request > total | 配额不足 |
| RESERVED_INSUFFICIENT | 409 | allocated-used-reserved < request | 预留额度不足 |
| RESERVATION_EXCEEDS_QUOTA | 422 | allocated_gpu_count > total | 预留额度不能超过配额上限 |
| DeviceNotFound | 404 | device_id 不存在 | 设备不存在 |
| Forbidden | 403 | 无权限 | 权限不足 |

---

## 5. Business Logic

### 5.1 Core Algorithms

#### 规格可用性计算（ListSpecAvailability）

```text
INPUT: tenant_id
1. 获取租户配额：quota = GetMy(tenant_id) → total, used, reserved
2. 获取预留额度：allocation = getReservation(tenant_id) → allocated_gpu_count
3. quota_remaining = allocated_gpu_count - used - reserved
4. 获取所有规格：specs = GPUSpecStore.List()
5. 获取节点列表：nodes = GPUInventory.ListNodeClasses()
6. FOR EACH spec IN specs:
     a. has_matching_nodes = EXISTS(node IN nodes WHERE
          node.labels["ani.kubercloud.io/gpu-mode"] == spec.gpu_mode
          AND (spec.gpu_mode == "wholecard"
               ? node.labels["ani.kubercloud.io/gpu-spec"] == spec.gpu_type
               : node.labels["ani.kubercloud.io/gpu-sharing-spec"] == spec.gpu_type))
     b. IF !has_matching_nodes:
          status = "unavailable"
          available_count = 0
        ELSE:
          i. device_total = sum(parseVolcanoVGPUAnnotation(node) FOR node IN matching_nodes)
          ii. device_used = sum(pod.gpu_resource_request FOR pod IN pods ON matching_nodes)
          iii. device_idle_count = device_total - device_used
          iv. IF quota_remaining <= 0:
                status = "full"
                available_count = 0
              ELIF device_idle_count <= 0:
                status = "device_full"
                available_count = 0
              ELSE:
                status = "available"
                available_count = min(quota_remaining, device_idle_count)
7. RETURN [{spec_id, status, available_count, quota_remaining, has_matching_nodes, has_idle_devices, device_idle_count, gpu_count}]
```

#### Volcano 资源翻译（VolcanoResourceTranslator.Translate）

```text
INPUT: spec_id, queue_name, count
1. spec = GPUSpecStore.Get(spec_id)
2. nodeSelector = buildNodeSelector(spec.NodeAffinity):
     // 公共标签：gpu-mode 物理隔离整卡节点和 vGPU 节点
     "ani.kubercloud.io/gpu-mode": spec.NodeAffinity.GPUMode
     // 整卡模式：匹配 gpu-spec 标签
     IF spec.GPUMode == "wholecard":
       "ani.kubercloud.io/gpu-spec": spec.NodeAffinity.GPUSpec
     // vGPU 模式：匹配 gpu-sharing-spec + gpu-sharing-policy 标签
     ELSE:
       "ani.kubercloud.io/gpu-sharing-spec": spec.NodeAffinity.GPUSharingSpec
       "ani.kubercloud.io/gpu-sharing-policy": spec.NodeAffinity.GPUSharingPolicy
3. schedulerName = "volcano"
4. resourceRequests = buildResources(spec.VolcanoResources, spec.GPUMode, count):
     IF spec.GPUMode == "wholecard":
       // 从 VolcanoResources.Wholecard map 选取资源
       // NVIDIA: nvidia.com/gpu: {count}
       // 华为:   huawei.com/Ascend910: {count}
       FOR k, v IN spec.VolcanoResources.Wholecard:
         resourceRequests[k] = format(v, count)
     ELSE:
       // 从 VolcanoResources.VGPU map 选取资源
       // volcano.sh/vgpu-memory: {mb_per_share}  (由 gpu-sharing-spec 算出,不能传空)
       // volcano.sh/vgpu-number: {count}
       FOR k, v IN spec.VolcanoResources.VGPU:
         resourceRequests[k] = format(v, count, spec.MBPerShare)
5. annotations:
     "scheduling.volcano.sh/queue-name": queue_name
6. RETURN {nodeSelector, schedulerName, resourceRequests, annotations}
```

> **关键**：nodeSelector 的 key 必须对齐 plan.md §4.2 节点标签方案：
> - 整卡用 `ani.kubercloud.io/gpu-spec`
> - vGPU 用 `ani.kubercloud.io/gpu-sharing-spec` + `ani.kubercloud.io/gpu-sharing-policy`
> - 公共用 `ani.kubercloud.io/gpu-mode` 物理隔离
> - **不能**用不存在的 `ani.kubercloud.io/gpu-type`
>
> **volcano_resources 结构**：嵌套 `wholecard` / `vgpu` 两个子 map（对齐 plan.md §4.1），不扁平化
> - 整卡：`nvidia.com/gpu` 或 `huawei.com/Ascend910`
> - vGPU：`volcano.sh/vgpu-memory`（由 Adapter 算出，不能传空）+ `volcano.sh/vgpu-number`
> - 公共用 `ani.kubercloud.io/gpu-mode` 物理隔离
> - **不能**用不存在的 `ani.kubercloud.io/gpu-type`

#### 创建实例三道闸校验

```text
INPUT: tenant_id, spec_id, request_count
1. quota = GetMy(tenant_id)
2. allocation = getReservation(tenant_id)
3. 闸 1: IF quota.used + quota.reserved + request_count > quota.total
         RETURN QUOTA_EXCEEDED
4. 闸 2: IF allocation.allocated_gpu_count - quota.used - quota.reserved < request_count
         RETURN RESERVED_INSUFFICIENT
5. 闸 3: TryManyTx(tx, [{gpu_count, request_count}])  // TCC 原子预占
         IF ErrQuotaExceeded: RETURN QUOTA_EXCEEDED
6. InsertPendingTx(tx, instance)  // 同事务写 DB
7. RETURN success
```

#### Reconciler Confirm/Cancel 同事务

```text
// 监听 Volcano Pod 状态
SWITCH pod.status.phase:
  CASE "Running":
    // pending→running: Confirm
    tx = store.BeginTenantTx(tenant_id)
    QuotaService.Confirm(tx, quota_tx_ids, instance_id)
    WorkloadInstanceStoreTx.UpsertStatusTx(tx, {state: "running"})
    tx.Commit()
  CASE "Failed":
    IF previous_state == "pending":
      // pending→failed: Cancel (释放 reserved)
      tx = store.BeginTenantTx(tenant_id)
      QuotaService.Cancel(tx, quota_tx_ids)
      WorkloadInstanceStoreTx.UpsertStatusTx(tx, {state: "failed"})
      tx.Commit()
    ELIF previous_state == "running":
      // running→failed: Release (释放 used)
      tx = store.BeginTenantTx(tenant_id)
      QuotaService.Release(tx, quota_tx_ids)
      WorkloadInstanceStoreTx.UpsertStatusTx(tx, {state: "failed"})
      tx.Commit()
  CASE "Pending" (超时):
    IF now - created_at > PROVISIONING_TIMEOUT_MIN:
      // 超时标 failed + Cancel
      tx = store.BeginTenantTx(tenant_id)
      QuotaService.Cancel(tx, quota_tx_ids)
      markProvisioningFailed(tx, instance_id)
      tx.Commit()
```

#### 删除实例 Cancel + Release 双调

```text
// 不依赖原态判定，覆盖 pending/running/failed
tx = store.BeginTenantTx(tenant_id)
QuotaService.Cancel(tx, quota_tx_ids)   // 释放 reserved（如果有的话，幂等）
QuotaService.Release(tx, quota_tx_ids)   // 释放 used（如果有的话，幂等）
WorkloadInstanceStoreTx.UpsertStatusTx(tx, {state: "deleted"})
outboxWriter.WriteTx(tx, {event_type: "instance.deleted", ...})
tx.Commit()
```

#### BOSS 下调 clamp 逻辑

> 以下描述已有端点 `PUT /admin/tenants/{tenant_id}/quota`（佳生已实现）的 clamp 行为，供本批次前端调用参考。本批次不重新实现此 handler。

```text
PUT /admin/tenants/{tenant_id}/quota (total) — 已有端点（佳生实现）:
1. new_total = request.total
2. current = GetTotalForUpdateTx(tx, tenant_id, "gpu_count")  // FOR UPDATE 锁行
3. actual_total = GREATEST(new_total, current.used + current.reserved)
4. UPDATE resource_quota SET total = actual_total WHERE tenant_id AND resource_type
5. tightened = (actual_total != new_total)
6. RETURN {total: actual_total, tightened}

PUT /admin/tenants/{tenant_id}/reservations (allocated_gpu_count):
1. new_alloc = request.allocated_gpu_count
2. quota = GetTotalForUpdateTx(tx, tenant_id, "gpu_count")
3. IF new_alloc > quota.total: RETURN RESERVATION_EXCEEDS_QUOTA
4. actual_alloc = GREATEST(new_alloc, quota.used + quota.reserved)
5. UPSERT resource_reservation_allocations SET allocated_gpu_count = actual_alloc
6. tightened = (actual_alloc != new_alloc)
7. RETURN {allocated_gpu_count: actual_alloc, tightened}
```

### 5.2 Validation Rules

- spec_id 格式 `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`（K8s name 约定）
- 创建规格时 gpu_type 必须存在于集群节点标签中（查 GPUInventory）
- 删除规格时查 workload_instances 无引用（state != deleted 的实例）
- allocated_gpu_count <= total（BOSS 设预留时校验）
- GPU_QUOTA_ENABLED=false 时跳过三道闸（配额旁路）
- 创建实例时 spec_id 必填（规格模式），queue_name 必填

### 5.3 State Machine

实例状态转换（扩展 reconciler）：

```text
pending ──Apply成功──→ provisioning ──Pod Running──→ running
   │                      │                          │
   │                      │──Pod Failed──→ failed    │──Pod Failed──→ failed
   │                      │                          │
   │──超时──→ failed       │──删除──→ deleting──→ deleted
   │
   └──删除──→ deleting ──Cancel+Release──→ deleted
```

### 5.4 Edge Cases

- **volcano.sh/vgpu-number 不随 Pod 递减**：不能读 allocatable 剩余，必须解析节点 annotation 得总量 + 查已调度 Pod 求差
- **旧节点标签迁移**：过渡期 inventory 优先读新 label（`gpu-spec`/`gpu-sharing-spec` + `gpu-mode`），回退读旧 label（`nvidia.com/gpu.product` / `ani.kubercloud.io/gpu-model`），后续统一只读新 label
- **并发创建超卖**：TCC SQL `WHERE reserved + used + $1 <= total` 原子防超卖
- **幂等 Confirm/Cancel/Release**：已 confirmed/cancelled/released 的 tx_id 再次调用为幂等 no-op
- **GPU_QUOTA_ENABLED=false**：TryManyTx/Confirm/Cancel/Release 全跳过，配额完全旁路

---

## 6. Error Handling

### 6.1 Error Taxonomy

| Error Code | HTTP Status | Condition | Retryable |
|------------|-------------|-----------|-----------|
| QUOTA_EXCEEDED | 409 | 配额上限不足 | 否（需释放或扩容） |
| RESERVED_INSUFFICIENT | 409 | 预留额度不足 | 否（需等待释放） |
| RESERVATION_EXCEEDS_QUOTA | 422 | 预留 > 配额上限 | 否（输入错误） |
| GPUSpecNotFound | 404 | 规格不存在 | 否 |
| GPUSpecInUse | 409 | 删除时有引用 | 否（需先删除实例） |
| GPUSpecConflict | 409 | spec_id 重复 | 否 |
| GPUTypeNotInNodes | 422 | gpu_type 不在节点标签 | 否 |
| DeviceNotFound | 404 | 设备不存在 | 否 |
| ErrQuotaResourceNotRegistered | 500 | resource_type 未注册 | 否（需初始化） |
| ErrReservationNotFound | 500 | tx_id 不存在 | 否 |

### 6.2 Retry Strategy

| Operation | Retryable | Backoff | Max Attempts |
|-----------|-----------|--------|-------------|
| 创建实例（429/503） | 是 | 指数退避 1s/2s/4s | 3 |
| Confirm/Cancel/Release | 否（幂等） | N/A | 1（幂等安全） |
| 规格创建（409 冲突） | 否 | N/A | 1 |
| 设备状态翻转（409） | 是 | 立即重试 | 2 |

### 6.3 Failure Modes

| 依赖失败 | 降级策略 |
|---------|---------|
| K8s API 不可达 | Inventory 返回 error，前端展示 Alert(error) + 重试 |
| PG 不可达 | Quota/TCC 全失败，实例创建失败，返回 500 |
| Volcano scheduler 未安装 | Pod 调度失败，reconciler 标 failed + Cancel |
| GPUSpec CRD 未安装 | GPUSpecStore 返回 error，规格目录不可用 |
| outbox_events 写入失败 | 同事务回滚，实例状态不更新 |

---

## 7. Security

### 7.1 Authentication & Authorization

| 端点 | Auth scope | 说明 |
|------|-----------|------|
| GET /gpu-specs | scope:gpu-spec:read | 租户可读 |
| POST /gpu-specs | scope:gpu-spec:write | 仅 BOSS 管理员 |
| GET /gpu-specs/{spec_id} | scope:gpu-spec:read | 租户可读 |
| DELETE /gpu-specs/{spec_id} | scope:gpu-spec:write | 仅 BOSS 管理员 |
| GET /gpu-specs/availability | scope:gpu-spec:read | 租户可读 |
| PATCH /gpu-inventory/{device_id} | scope:gpu-inventory:write | 仅 BOSS 管理员 |
| GET /quotas/me | scope:quota:read (tenant) | 租户查自己 |
| GET /quotas | scope:quota:read (platform) | 仅 BOSS 管理员 |
| PUT /admin/tenants/{id}/reservations | scope:quota:write (platform) | 仅 BOSS 管理员 |
| GET /admin/tenants/{id}/reservations | scope:quota:read (platform) | 仅 BOSS 管理员 |
| GET /reservations/me | scope:quota:read (tenant) | 租户查自己 |

> **已有端点（不在本批次）**：`PUT/POST/DELETE /admin/tenants/{id}/quota` + `GET /admin/tenants/{id}/quota` + `GET /admin/quota-meta` 已由佳生/Leader 实现，scope 分别为 `quota:write (platform)` / `quota:read (platform)`。

### 7.2 Input Validation

- spec_id: regex `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` + maxLength 63
- gpu_type: 非空，校验存在于节点标签
- shares: int >= 1
- mb_per_share: int >= 1
- allocated_gpu_count: int >= 0
- total: int >= 0
- device status: enum [maintenance, idle]
- idempotency_key: string minLength 1 maxLength 128

### 7.3 Data Protection

- 配额数据通过 PG RLS 租户隔离（已有）
- BOSS 管理操作走 WithPlatformTx（bypass RLS，platform admin scope）
- 租户自查走 WithTenantTx（RLS 自动过滤）
- 敏感字段无（配额数据不包含敏感信息）

---

## 8. Performance

### 8.1 Expected Load

| 操作 | 预估 QPS | 数据量 |
|------|---------|--------|
| GET /gpu-specs | 10/min | <100 规格 |
| GET /gpu-specs/availability | 50/min | <100 规格 + <1000 节点 |
| POST /instances (spec_id) | 10/min | — |
| Reconcile loop | 1/30s | <1000 非终态实例 |
| GET /quotas/me | 100/min | 1 租户 |

### 8.2 Optimization Strategy

- ListSpecAvailability 缓存节点列表（30s TTL），避免每次查 K8s
- 规格 CRD 数量少（<100），全量 List 无需分页
- reconciler 已有 backoff 机制（FailureBackoffSeconds=30）
- TCC SQL 单行 UPDATE + WHERE guard，无 N+1

### 8.3 Database Considerations

- `resource_reservation_allocations` 主键 tenant_id，单行读写
- `workload_instances.quota_tx_ids` JSONB 列，存储 tx_id 数组，不做 JOIN
- `resource_quota` 行锁 `FOR UPDATE`（GetTotalForUpdateTx 已有）
- `resource_reservations` 索引 `tx_id`（已有）

---

## 9. Testing Strategy

### 9.1 Unit Tests

| 模块 | 测试 | 覆盖 |
|------|------|------|
| CRDGPUSpecStore | TestCRDGPUSpecStore_CRUD | 规格目录 CRUD + 幂等 |
| VolcanoResourceTranslator | TestVolcanoTranslator_SpecToPod | spec_id → Pod 资源翻译 |
| VolcanoResourceTranslator | TestVolcanoTranslator_VGPUMemory | vGPU memory 自动计算 |
| GPUInventory | TestListSpecAvailability_FourStates | 四态标注逻辑 |
| GPUInventory | TestParseVolcanoVGPUAnnotation | 节点 annotation 解析 |
| Reconciler | TestReconcile_ConfirmCancel | Confirm/Cancel 同事务 |
| Reconciler | TestReconcile_ProvisioningTimeout | 超时标 failed |
| Reconciler | TestReconcile_DeleteDualRelease | 删除双调释放 |
| Reconciler | TestReconcile_ReconcileLoop | 对账循环（只告警） |
| InstanceStoreTx | TestUpsertStatusTx | 同事务状态更新 |
| QuotaService | TestTryManyTx_QuotaEnabled | GPU_QUOTA_ENABLED=true |
| QuotaService | TestTryManyTx_QuotaDisabled | GPU_QUOTA_ENABLED=false 旁路 |

### 9.2 Integration Tests

| 测试 | 覆盖 |
|------|------|
| TestCreateInstance_WithSpecId | spec_id 创建实例全链路 |
| TestCreateInstance_QuotaExceeded | 配额不足返回 409 |
| TestCreateInstance_ReservedInsufficient | 预留不足返回 409 |
| TestDeleteInstance_QuotaRelease | 删除后配额释放 |
| TestBOSSQuota_Clamp | BOSS 下调配额 clamp |
| TestBOSSReservation_Clamp | BOSS 下调预留 clamp |
| TestGPUSpecDelete_InUse | 有引用时禁止删除 |

### 9.3 Edge Case Tests

| 测试 | 场景 |
|------|------|
| TestConcurrentCreate_NoOversell | 4 卡预留不被 5 个 pending 超卖 |
| TestVolcanoVGPUAnnotation_Legacy | 旧 label 回退读取 |
| TestConfirmCancel_Idempotent | 重复 Confirm/Cancel 幂等 |
| TestProvisioningTimeout_Cancel | 超时后 Cancel 释放配额 |

### 9.4 Acceptance Criteria Mapping

| US/FR | Test | Type | Description |
|-------|------|------|-------------|
| US-001 | TestOpenAPI_Validate | integration | v1.yaml 校验通过 |
| US-002 | TestPorts_Build | unit | ports 编译通过 |
| US-003 | TestAdapters_All | unit | 全 adapter 单测通过 |
| US-004 | TestHandler_All | integration | 全 handler 路由通过 |
| US-005 | TestBOSS_Build | integration | BOSS tsc + vite build 通过 |
| US-006 | TestConsole_Build | integration | Console tsc + vite build 通过 |
| FR-1 | TestGPUSpecCRUD | unit | 规格 CRUD |
| FR-3 | TestGPUSpecDelete_InUse | unit | 有引用禁止删除 |
| FR-8 | TestBOSSQuota_Clamp | unit | 下调配额 clamp |
| FR-9 | TestBOSSReservation_Clamp | unit | 下调预留 clamp |
| FR-12 | TestListSpecAvailability | unit | 四态标注 |
| FR-15 | TestThreeGateCheck | unit | 三道闸校验 |
| FR-17 | TestReconcile_ConfirmCancel | unit | Confirm/Cancel 状态转换 |
| FR-18 | TestReconcile_DeleteDualRelease | unit | 删除双调释放 |
| FR-19 | TestReconcile_ProvisioningTimeout | unit | 超时 Cancel |
| FR-20 | TestQuotaEnabledSwitch | unit | 开关旁路 |
| FR-23 | TestHAMiRemoved | unit | HAMi 代码删除 |

---

## 10. Implementation Plan

### 10.1 Phases

| Phase | 内容 | 依赖 |
|-------|------|------|
| Phase 1 | Core API 契约（US-001） | 无 |
| Phase 2 | Ports 层（US-002） | Phase 1 |
| Phase 3 | Adapters 层（US-003） | Phase 2 |
| Phase 4 | Gateway Handler（US-004） | Phase 3 |
| Phase 5 | BOSS 前端（US-005） | Phase 4（API 可用） |
| Phase 6 | Console 前端（US-006） | Phase 4（API 可用） |
| Phase 7 | 集成验收（US-007） | Phase 5 + 6 |

### 10.2 Issue Mapping

| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| #1 Core API 契约 | 4.1, 4.2, 4.3 | high | — |
| #2 Ports 层 | 3.2, 2.4 | high | #1 |
| #3 Adapters 层 | 3.1, 5.1, 2.4 | high | #2 |
| #4 Gateway Handler | 4.3, 5.2 | high | #3 |
| #5 BOSS 前端 | UX 4.1-4.3, 5.1 | high | #4 |
| #6 Console 前端 | UX 4.4-4.6, 5.2 | high | #4 |
| #7 集成验收 | 9, 10 | high | #5, #6 |

### 10.3 Incremental Delivery

- GPU_QUOTA_ENABLED 默认 false（配额旁路），前端可先用规格选择模式（无配额校验）
- Phase 1-4 完成后设 GPU_QUOTA_ENABLED=true 启用配额全链路
- Phase 5-6 前端可并行开发（Phase 4 API 就绪后）
- 真实环境 live gate 后续 Sprint（本期 local profile 验证）

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

- 本地闭环边界：tenant_id 先用默认值（租户功能未完成），VPC/Subnet 用占位默认值
- outbox 集成测等佳生 adapter 合并后补（本期定义 outboxWriter 接口 + 复用 OutboxRepo 写入逻辑，publisher 已在 task-service 实现）
- 旧节点标签迁移：过渡期 inventory 优先读新 label 回退读旧 label，后续统一只读新 label

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|-----------|
| Volcano 未安装导致 Pod 调度失败 | 实例卡在 pending | reconciler 超时机制 + Events 文本匹配区分 queued vs failed |
| volcano.sh/vgpu-number 不递减导致空闲数计算错误 | 超卖或误报不可用 | 必须解析 annotation + 查 Pod 求差，不能读 allocatable |
| TCC 方案不可改 | 字段映射必须适配 | 本方案只做字段映射，不改 TCC 表结构/接口/流程 |
| 并发创建超卖 | 多租户抢占 | TCC SQL WHERE guard 原子防超卖 + 三道闸校验 |
| HAMi 代码删除影响现有节点 | 旧节点标签迁移 | 过渡期回退读旧 label，后续统一 |

### 11.3 Assumptions

- 节点 GPU 同型号，安装驱动后由平台自动打标签
- vGPU 集群级等分，切分规格通过节点标签表达
- BOSS 以"卡"为维度分配，一个整卡 = 1 卡，一个 vGPU = 1 卡
- 本期基于单集群、单卡种、等分假设
- outbox_events 表已存在（init_schema 已创建），OutboxRepo 已实现，publisher 已在 task-service 运行
- GPUSpec CRD 通过 K8s CRD 持久化，复用 volcano_queue_store.go CRD 操作模式
- GPU_QUOTA_ENABLED 默认 false，需手动开启
