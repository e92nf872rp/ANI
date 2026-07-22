# SPEC: K8s GPU 调度 — Core 后端（HAMi + Volcano）v1.0.0 P0

> Technical specification derived from:
> - PRD: [`prd-k8s-gpu-hami-volcano-scheduling.md`](../../../prd/console/gpu-inventory/prd-k8s-gpu-hami-volcano-scheduling.md)
> - UX (Console): [`ux-console-gpu-scheduling.md`](../../../prd/console/gpu-inventory/ux-console-gpu-scheduling.md) — 仅供 Core 参考，不含 UI 实现细节
> - UX (BOSS): [`ux-boss-gpu-pool.md`](../../../prd/console/gpu-inventory/ux-boss-gpu-pool.md) — 仅供 Core 参考，不含 UI 实现细节
> - Sibling SPEC: `spec-console-gpu-scheduling.md` / `spec-boss-gpu-pool.md`
> Generated: 2026-07-03 | Target branch: `feature/gpu-scheduling-p0-core` | Commit: TBD
>
> **Scope:** ANI Core 后端 — OpenAPI 契约、handler、adapter、ports、deploy manifest
> **Source of truth:** `repo/api/openapi/v1.yaml` — 先改契约，再实现 handler/adapter，再 lab 验证
> Guide: `repo/services/tasks/execution/CORE-HANDLER-IMPLEMENTATION-GUIDE.md`

---

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 覆盖 ANI Core P0 GPU 调度后端全链路：① OpenAPI 新增 GPU 调度队列 CRUD 5 端点 + 4 schema + 2 RBAC scope；② `GPUSchedulingQueueStore` port + Volcano Queue CRD adapter；③ `KubernetesGPUInventory.PlanScheduling()` 扩展（NVIDIA 整卡默认 + HAMi vGPU smoke）；④ 真实 lab 部署 HAMi + Volcano + DCGM Exporter 的 preflight 契约验证；⑤ GPU smoke A/B live gate。

### 1.2 PRD Reference

- Source: `repo/services/tasks/modules/prd/console/gpu-inventory/prd-k8s-gpu-hami-volcano-scheduling.md`
- User Stories covered: US-001, US-002, US-003, US-004, US-005, US-008, US-009
- Functional Requirements covered: FR-1 ~ FR-10, FR-14 ~ FR-15

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| OpenAPI 实施顺序 | 严格先改 v1.yaml → handler → adapter → lab | PRD §8.2 强制 |
| 队列数据持久化 | Volcano Queue CRD（纯 K8s 声明式） | PRD §8.1 架构边界；无 DB 双写 |
| BOSS P0 集群级数据源 | 复用 `GET /gpu-inventory/occupancy` 提升为平台 scope | 最小改动；不新增 aggregate API（NG-9） |
| DCGM 利用率查询 | 经 `GET /observability/query` PromQL 代理 | v1.yaml 已冻结 `queryObservability` |
| 实施顺序 | ① OpenAPI → ② adapter/handler → ③ PlanScheduling 扩展 → ④ lab 验证 | PRD §8.2 强制 |

---

## 2. Architecture

### 2.1 System Context

```text
┌─────────────────────────────────────────────────────┐
│  Console / BOSS（消费 Core API，不在此 SPEC 范围）      │
└───────────────┬─────────────────────────────────────┘
                │
        ┌───────▼───────────────────────────────────┐
        │  ANI Core API (Gateway → handlers)          │
        │  /gpu-inventory (已冻结)                     │
        │  /gpu-inventory/occupancy (已冻结，P0 加平台 scope) │
        │  /gpu-scheduling/queues (P0 新增)           │
        │  /instances (已冻结，P0 扩展 gpu 调度字段)    │
        │  /observability/query (已冻结，DCGM PromQL) │
        └───────┬───────────────────────────────────┘
                │
        ┌───────▼───────────────────────────────────┐
        │  pkg/ports/ + pkg/adapters/runtime/        │
        │  GPUInventory port (已存在)                 │
        │  GPUSchedulingQueueStore port (P0 新建)     │
        │  KubernetesGPUInventory (已存在，P0 扩展)    │
        │  VolcanoQueueStore adapter (P0 新建)        │
        │  PlanningRuntime (已存在，P0 对接 queue)    │
        │  InstanceOrchestrator (已存在，P0 无改动)   │
        └───────┬───────────────────────────────────┘
                │
        ┌───────▼───────────────────────────────────┐
        │  K8s + HAMi + Volcano + DCGM (底座)        │
        └───────────────────────────────────────────┘
```

### 2.2 Component Design

| 组件 | 位置 | 职责 | 状态 |
|------|------|------|------|
| `GPUInventory` port | `pkg/ports/gpu_inventory.go` | GPU 发现 + 调度规划接口 | ✅ 已存在 |
| `GPUSchedulingQueueStore` port | `pkg/ports/gpu_scheduling.go` | 队列 CRUD 接口 | **P0 新建** |
| `KubernetesGPUInventory` adapter | `pkg/adapters/runtime/kubernetes_gpu_inventory.go` | 真实 K8s 节点解析 + PlanScheduling | ✅ 已存在，需扩展 queue + vGPU |
| `LocalGPUInventory` adapter | `pkg/adapters/runtime/local_gpu_inventory.go` | dev profile 静态清单 | ✅ 已存在 |
| **`VolcanoQueueStore` adapter** | `pkg/adapters/runtime/volcano_queue_store.go` | 队列 CRUD → Volcano Queue CRD | **P0 新建** |
| `PlanningRuntime` | `pkg/adapters/runtime/planning.go` | 规划态 WorkloadRuntime | ✅ 已存在，需对接 queue |
| `LocalInstanceOrchestrator` | `pkg/adapters/runtime/instance_orchestrator.go` | 实例生命周期编排 | ✅ 已存在 |
| **GPU Scheduling handler** | `services/ani-gateway/internal/handlers/gpu_scheduling_handler.go` | `/gpu-scheduling/queues` 5 端点 | **P0 新建** |

### 2.3 Module Interactions

```text
Console/BOSS → coreApi.POST /instances (kind=gpu_container, queue_name, gpu分配模式)
  → InstanceOrchestrator.Create()
    → PlanningRuntime.plan()
      → KubernetesGPUInventory.PlanScheduling()
        → 过滤 GPU 节点（vendor/model/pool）
        → 检查 allocatable（nvidia.com/gpu 或 nvidia.com/vgpu）
        → 输出 GPUSchedulingDecision{schedulerName=volcano, queueName, resourceName}
    → WorkloadRenderer 渲染 manifest
    → WorkloadProviderApply 提交到 K8s（schedulerName=volcano）
  → 返回 InstanceRecord（含 gpu 摘要 + state_reason）
```

```text
Console → coreApi.POST /gpu-scheduling/queues
  → gpu_scheduling_handler.Create()
    → VolcanoQueueStore.Create()
      → 映射为 Volcano Queue CRD
      → kubectl apply / controller watch
    → 返回 GPUSchedulingQueue
```

```text
BOSS → coreApi.GET /gpu-inventory/occupancy (平台 scope，JWT 平台角色)
  → KubernetesGPUInventory 内部聚合（不传 tenant_id = 全域）
  → 返回 GPUOccupancyStats
```

### 2.4 File Structure

```text
repo/
├── api/openapi/
│   └── v1.yaml                              [MODIFY: 新增 /gpu-scheduling/queues 5 端点 + schema + scope]
├── pkg/
│   ├── ports/
│   │   ├── gpu_inventory.go                 [MODIFY: 扩展 GPUSchedulingRequest 加 QueueName/WorkloadClass]
│   │   └── gpu_scheduling.go                [NEW: GPUSchedulingQueueStore port + 类型]
│   └── adapters/runtime/
│       ├── kubernetes_gpu_inventory.go      [MODIFY: PlanScheduling 扩展 queue 解析 + vGPU]
│       ├── volcano_queue_store.go            [NEW: Volcano Queue CRD adapter]
│       └── volcano_queue_store_test.go      [NEW]
├── services/ani-gateway/
│   └── internal/handlers/
│       └── gpu_scheduling_handler.go        [NEW: 5 端点 handler]
│       └── gpu_scheduling_handler_test.go   [NEW]
├── deploy/
│   ├── manifests/m1-infra-e/                [已存在，不需改]
│   ├── manifests/m1-infra-f/                [已存在，不需改]
│   └── real-k8s-lab/
│       └── gpu-scheduling-live-gate.yaml   [NEW: 队列 CRUD live gate]
└── Makefile                                 [MODIFY: 新增 gpu-scheduling gate target]
```

---

## 3. Data Model

### 3.1 Schema Changes（OpenAPI v1.yaml 新增）

**P0 必须新增的 schema：**

```yaml
# ── GPU 调度队列 ──────────────────────────────────────────
GPUSchedulingQueue:
  type: object
  required: [id, name, weight, reclaimable, workload_class, is_platform_default, created_at, updated_at]
  properties:
    id:                  { type: string, format: uuid }
    name:                { type: string, description: "租户内唯一" }
    weight:              { type: integer, minimum: 1, default: 10 }
    reclaimable:         { type: boolean, default: false }
    workload_class:      { type: string, enum: [inference, training, batch] }
    project_id:          { type: string, format: uuid, nullable: true }
    is_platform_default: { type: boolean, description: "平台默认队列不可删除" }
    created_at:          { type: string, format: date-time }
    updated_at:          { type: string, format: date-time }

GPUSchedulingQueueListResponse:
  type: object
  required: [items]
  properties:
    items: { type: array, items: { $ref: '#/components/schemas/GPUSchedulingQueue' } }

GPUSchedulingQueueCreateRequest:
  type: object
  required: [name, weight, workload_class]
  properties:
    name:           { type: string, minLength: 1, maxLength: 63 }
    weight:         { type: integer, minimum: 1 }
    reclaimable:    { type: boolean, default: false }
    workload_class: { type: string, enum: [inference, training, batch] }
    project_id:     { type: string, format: uuid, nullable: true }

GPUSchedulingQueueUpdateRequest:
  type: object
  properties:
    weight:         { type: integer, minimum: 1 }
    reclaimable:    { type: boolean }
    workload_class: { type: string, enum: [inference, training, batch] }
    project_id:     { type: string, format: uuid, nullable: true }
```

**P0 必须确认/扩展的 InstanceRecord.gpu 字段（待补冻结）：**

```yaml
# InstanceRecord.gpu 扩展（待 P0-① 冻结确认）
InstanceGPUSummary:
  type: object
  properties:
    count:             { type: integer }  # 已存在
    vendor:            { type: string }   # 已存在
    model:             { type: string }   # 已存在
    queue_name:        { type: string, nullable: true }    # 待补
    resource_name:     { type: string, nullable: true }    # 待补：nvidia.com/gpu | nvidia.com/vgpu
    scheduling_reason: { type: string, nullable: true }    # 待补
```

### 3.2 Entity Definitions（Go ports）

```go
// pkg/ports/gpu_scheduling.go (NEW)

type WorkloadClass string

const (
    WorkloadClassInference WorkloadClass = "inference"
    WorkloadClassTraining  WorkloadClass = "training"
    WorkloadClassBatch     WorkloadClass = "batch"
)

type GPUSchedulingQueue struct {
    ID                string
    Name              string
    Weight            int
    Reclaimable        bool
    WorkloadClass     WorkloadClass
    ProjectID         string // 可空
    IsPlatformDefault bool
    CreatedAt         time.Time
    UpdatedAt         time.Time
}

type GPUSchedulingQueueCreateRequest struct {
    Name          string
    Weight        int
    Reclaimable   bool
    WorkloadClass WorkloadClass
    ProjectID     string // 可空
}

type GPUSchedulingQueueUpdateRequest struct {
    Weight        *int
    Reclaimable   *bool
    WorkloadClass *WorkloadClass
    ProjectID     *string
}

type GPUSchedulingQueueStore interface {
    List(ctx context.Context) ([]GPUSchedulingQueue, error)
    Get(ctx context.Context, id string) (GPUSchedulingQueue, error)
    Create(ctx context.Context, req GPUSchedulingQueueCreateRequest) (GPUSchedulingQueue, error)
    Update(ctx context.Context, id string, req GPUSchedulingQueueUpdateRequest) (GPUSchedulingQueue, error)
    Delete(ctx context.Context, id string) error
}
```

```go
// pkg/ports/gpu_inventory.go [MODIFY]

type GPUSchedulingRequest struct {
    TenantID             string
    WorkloadID           string
    PreferredVendors     []GPUVendor
    PreferredModels      []string
    RequiredMemoryMiB    int64
    RequiredCount        int
    VirtualizationModes  []GPUVirtualizationMode
    RequiredCapabilities []string
    Pool                 string
    // P0 新增
    QueueName     string         // 可空，空则按 WorkloadClass 选默认队列
    WorkloadClass WorkloadClass  // inference/training/batch
}
```

### 3.3 Relationships

- `GPUSchedulingQueue` 与 `Volcano Queue CRD` 1:1 映射（adapter 负责）
- `InstanceRecord.gpu.queue_name` 引用队列名（P0 在实例创建时关联）
- 平台默认队列 `ani-inference` / `ani-training` 由 `m1-infra-e` 预置 CRD，`is_platform_default=true`，API 不可删除

### 3.4 Migration Plan

**无数据库迁移**——队列数据持久化在 Volcano Queue CRD（K8s etcd），不在 PostgreSQL。

- 平台默认队列通过 `deploy/manifests/m1-infra-e/` 预置 Volcano Queue CRD
- 租户自定义队列由 adapter 动态创建 CRD
- 回滚：删除 CRD 即回滚队列

---

## 4. API Design

### 4.1 OpenAPI Change Plan（Core only，P0 必做）

| Change | operationId | Compatibility | idempotency_key | RBAC scope |
|--------|-------------|---------------|-----------------|------------|
| **新增** | `listGPUSchedulingQueues` | additive | N/A | `scope:gpu-scheduling:read` |
| **新增** | `createGPUSchedulingQueue` | additive | **required** | `scope:gpu-scheduling:write` |
| **新增** | `getGPUSchedulingQueue` | additive | N/A | `scope:gpu-scheduling:read` |
| **新增** | `updateGPUSchedulingQueue` | additive | optional | `scope:gpu-scheduling:write` |
| **新增** | `deleteGPUSchedulingQueue` | additive | optional | `scope:gpu-scheduling:write` |

**无破坏性变更**——全部为新增端点，不影响已有 API。

### 4.2 Endpoints

| Method | Path | Description | Auth | Request | Response |
|--------|------|-------------|------|---------|----------|
| GET | `/api/v1/gpu-scheduling/queues` | 列表（租户 scoped） | `scope:gpu-scheduling:read` | — | `200 GPUSchedulingQueueListResponse` |
| POST | `/api/v1/gpu-scheduling/queues` | 创建 | `scope:gpu-scheduling:write` | `GPUSchedulingQueueCreateRequest` + `Idempotency-Key` header | `201 GPUSchedulingQueue` |
| GET | `/api/v1/gpu-scheduling/queues/{id}` | 详情 | `scope:gpu-scheduling:read` | — | `200 GPUSchedulingQueue` |
| PATCH | `/api/v1/gpu-scheduling/queues/{id}` | 更新 | `scope:gpu-scheduling:write` | `GPUSchedulingQueueUpdateRequest` | `200 GPUSchedulingQueue` |
| DELETE | `/api/v1/gpu-scheduling/queues/{id}` | 删除（仅租户自定义） | `scope:gpu-scheduling:write` | — | `204` |

### 4.3 Error Responses

| Error Code | HTTP Status | Condition | User Message |
|------------|-------------|-----------|--------------|
| `QueueNameConflict` | 409 | 租户内队列名重复 | 队列名称已存在 |
| `QueueNotFound` | 404 | id 不存在或跨租户 | 队列不存在 |
| `PlatformDefaultProtected` | 403 | PATCH/DELETE 平台默认队列 | 平台默认队列不可修改或删除 |
| `InsufficientGPU` | 422 | 创建实例时 GPU 不足 | GPU 资源不足 |
| `GPUNodeIncompatible` | 422 | 无兼容 GPU 节点 | 无兼容 GPU 节点 |
| `QueueNotFound` (instance) | 422 | 创建实例时队列不存在 | 所选调度队列不存在 |

### 4.4 Breaking Changes

**无**——全部为新增端点和 schema。

---

## 5. Business Logic

### 5.1 Core Algorithms

**PlanScheduling() 扩展（KubernetesGPUInventory）：**

```text
INPUT: GPUSchedulingRequest{TenantID, WorkloadID, PreferredVendors, RequiredCount,
                            VirtualizationModes, Pool, QueueName, WorkloadClass}

1. 过滤 GPU 节点（vendor/model/pool/Ready）
2. 检查节点 allocatable：
   - 整卡模式 (VirtualizationNone): nvidia.com/gpu >= RequiredCount
   - vGPU 模式 (GPUVirtualizationVGPU): nvidia.com/vgpu >= RequiredCount
3. 若 QueueName 非空，校验队列存在且属于该租户（VolcanoQueueStore.List 过滤）
4. 若 QueueName 为空，按 WorkloadClass 选默认队列：
   - inference → ani-inference
   - training  → ani-training
   - batch     → ani-training（fallback）
5. 输出 GPUSchedulingDecision{
     SchedulerName: "volcano",
     QueueName:     <resolved>,
     ResourceName:  "nvidia.com/gpu" | "nvidia.com/vgpu",
     NodeSelector:  {ani.kubercloud.io/gpu-node: "true"},
     Reasons:       []  // 无可用节点时填充
   }
6. 无可用 GPU → return decision with Reasons → 上层返回 422
```

**Volcano Queue adapter CRUD：**

```text
Create:
  1. 校验 name 租户内唯一（List CRD 过滤 tenant label）
  2. 构造 Volcano Queue CRD:
     apiVersion: scheduling.volcano.sh/v1beta1
     kind: Queue
     metadata:
       name: <tenant_slug>-<name>
       labels:
         ani.kubercloud.io/tenant: <tenant_id>
         ani.kubercloud.io/workload-class: <workload_class>
     spec:
       weight: <weight>
       reclaimable: <reclaimable>
  3. apply CRD via K8s client
  4. 返回 GPUSchedulingQueue（含生成的 UUID）

Update:
  1. 查 CRD，若 is_platform_default=true → 返回 PlatformDefaultProtected
  2. patch CRD spec
  3. 返回更新后 record

Delete:
  1. 查 CRD，若 is_platform_default=true → 返回 PlatformDefaultProtected
  2. delete CRD
```

### 5.2 Validation Rules

- 队列名：`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`，1-63 字符（K8s 资源名规范）
- weight：正整数 >= 1
- 平台默认队列：不可 PATCH/DELETE
- 租户隔离：所有查询自动注入 `tenant_id` from JWT，禁止前端传 `tenant_id`
- 创建实例 `queue_name`：若非空必须在租户可见队列中

### 5.3 Edge Cases

- 昇腾/海光请求：P0 返回明确不支持，`Reasons: ["P1 未启用"]`
- MIG 请求：P0 返回明确不支持
- Volcano CRD 不可达：队列 CRUD 返回 `503 QueueStoreUnavailable`
- DCGM 未就绪：不影响 Core 后端，仅前端利用率 KPI 降级

---

## 6. Error Handling

### 6.1 Error Taxonomy

| Error Code | HTTP Status | Condition | User Message (zh-CN) |
|------------|-------------|-----------|----------------------|
| `InsufficientGPU` | 422 | allocatable 不足 | GPU 资源不足，当前无可用算力满足本次创建请求 |
| `QueueNotFound` | 422/404 | 队列不存在 | 所选调度队列不存在或已删除 |
| `GPUNodeIncompatible` | 422 | 无兼容节点 | 无兼容 GPU 节点，请调整型号偏好或调度队列 |
| `QueueNameConflict` | 409 | 名称重复 | 队列名称已存在 |
| `PlatformDefaultProtected` | 403 | 改默认队列 | 平台默认队列不可修改或删除 |
| `QueueStoreUnavailable` | 503 | Volcano CRD API 不可达 | 队列服务暂时不可用 |

### 6.2 Retry Strategy

- POST 创建队列：`idempotency_key` 保护，可安全重试
- POST 创建实例：不自动重试，返回 422 让上层处理

### 6.3 Failure Modes

| 依赖失败 | 降级行为 |
|---------|---------|
| Volcano CRD API 不可达 | 队列 CRUD 返回 503；inventory/occupancy 不受影响 |
| K8s API 不可达 | inventory/occupancy 返回 503 |
| DCGM Exporter 不可达 | 不影响 Core；observability/query 返回空或错误 |

---

## 7. Security

### 7.1 Authentication & Authorization

| Scope | 角色 | 能力 |
|-------|------|------|
| `scope:gpu-inventory:read` | 租户用户 | 读 GPU 清单/占用 |
| `scope:gpu-scheduling:read` | 租户管理员 + 项目成员 | 读队列列表 |
| `scope:gpu-scheduling:write` | **仅租户管理员** | CRUD 租户自定义队列 |
| 平台 GPU 角色（SPEC 冻结） | BOSS/运维 | 读全平台 occupancy；P0 只读队列 |

### 7.2 Input Validation

- 队列名 K8s 资源名规范
- `idempotency_key`：UUID v4 格式
- 租户隔离：所有查询自动注入 `tenant_id` from JWT

### 7.3 Data Protection

- 队列 CRD labels 含 `ani.kubercloud.io/tenant=<tenant_id>`，adapter 按 tenant 过滤
- BOSS 平台 scope：JWT 平台角色可读全域 inventory，不暴露 `by_tenant` 字段（P0）

---

## 8. Performance

### 8.1 Expected Load

| API | 预估 QPS | 数据量 |
|-----|---------|--------|
| `GET /gpu-inventory` | 5-10/租户/分钟 | 单租户 50-200 GPU 卡 |
| `GET /gpu-inventory/occupancy` | 10-20/租户/分钟 | 聚合统计 |
| `GET /gpu-scheduling/queues` | 5/租户/分钟 | 单租户 10-50 队列 |
| `POST /instances` (gpu_container) | 1-5/租户/分钟 | — |

### 8.2 Optimization Strategy

- inventory/occupancy：adapter 端 K8s informer L1 cache，30s TTL
- 队列列表：adapter 端 CRD list cache + tenant label 过滤
- 无 N+1 风险（无 DB）

---

## 9. Testing Strategy

### 9.1 Unit Tests

| 模块 | 测试范围 | Mock 策略 |
|------|---------|-----------|
| `VolcanoQueueStore` | CRUD + 租户隔离 + 默认队列保护 | fake K8s client |
| `KubernetesGPUInventory.PlanScheduling` | 整卡/vGPU/无可用/昇腾拒绝/queue 解析 | fake node list |
| `gpu_scheduling_handler` | 5 端点 + RBAC + idempotency | test server |

### 9.2 Integration Tests

- `make test`（全量 Go 单元测试）
- `make validate-architecture`（架构边界：无 HAMi/Volcano SDK 泄漏到 Gateway/Services）
- `make validate-infra`（m1-infra-e/f manifest 校验）
- `make validate-real-k8s-profile`（lab live gate）

### 9.3 Edge Case Tests

- 创建实例时队列不存在 → 422 `QueueNotFound`
- 创建实例时 GPU 不足 → 422 `InsufficientGPU`
- PATCH 平台默认队列 → 403 `PlatformDefaultProtected`
- 跨租户读队列 → 404（不泄露存在性）
- 昇腾/海光请求 → 明确不支持
- Volcano CRD 不可达 → 503

### 9.4 Acceptance Criteria Mapping

| US/FR | Test | Type | Description |
|-------|------|------|-------------|
| US-001 | preflight Job | integration | `m1-infra-e` + `m1-infra-f` apply + exit 0 |
| US-002 | smoke A + B | integration | `make validate-real-k8s-profile` + smoke Job |
| US-003 | PlanScheduling | unit | 整卡/vGPU/无可用/昇腾拒绝 |
| US-004 | 队列 CRUD | unit + integration | 5 端点 + RBAC + idempotency |
| US-005 | inventory/occupancy | integration | `listGPUInventory` + `getGPUOccupancy` 200 |
| US-008 | 实例创建失败 | unit | 422 + state_reason 回写 |
| US-009 | 昇腾/海光拒绝 | unit | PlanScheduling 返回不支持 |
| FR-1~3 | deploy + preflight | integration | `make validate-infra` |
| FR-7 | 队列 OpenAPI | contract | `v1.yaml` diff 含 5 端点 |
| FR-14 | DCGM | integration | preflight `ANI_GPU_REQUIRE_DCGM_SERVICE=true` |

---

## 10. Implementation Plan

### 10.1 Phases

| Phase | 内容 | 依赖 |
|-------|------|------|
| **P0-① OpenAPI 契约** | 改 `v1.yaml`：新增 5 端点 + 4 schema + 2 scope + InstanceGPU 扩展 | 无 |
| **P0-② Core adapter** | `ports/gpu_scheduling.go` + `volcano_queue_store.go` + handler | P0-① |
| **P0-③ PlanScheduling 扩展** | `kubernetes_gpu_inventory.go` 扩展 queue + vGPU | P0-① |
| **P0-④ Lab 验证** | HAMi + Volcano + DCGM 部署 + preflight + smoke | P0-②③ |
| **P0-⑤ Live gate** | 队列 CRUD + smoke live gate profile | P0-②③④ |

### 10.2 Issue Mapping

| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| #1: OpenAPI 新增队列 CRUD + InstanceGPU 扩展 | §3.1, §4.1, §4.2 | high | — |
| #2: Core Queue port + Volcano adapter + handler | §2.2, §3.2, §5.1 | high | #1 |
| #3: PlanScheduling 扩展（queue + vGPU） | §5.1, §5.3 | high | #1 |
| #4: Lab HAMi+Volcano+DCGM 部署 + preflight | §10.1 P0-④ | high | #2, #3 |
| #5: GPU smoke A+B live gate | §9.4 US-002 | high | #4 |
| #6: 队列 CRUD live gate | §10.1 P0-⑤ | medium | #2, #4 |

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

- `InstanceRecord.gpu.queue_name` 字段名未冻结：P0-① 需确认（当前假设 `gpu.queue_name`）
- DCGM PromQL 模板：SPEC 冻结为 `avg(DCGM_FI_DEV_GPU_UTIL{job="dcgm-exporter"})`，需 lab 验证 job label
- Volcano Queue CRD API 版本：当前假设 `scheduling.volcano.sh/v1beta1`，需 lab 验证

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|-----------|
| HAMi + Volcano lab 部署失败 | P0 阻塞 | 优先 P0-④ 部署；失败则降级 local profile |
| Volcano Queue CRD 版本不兼容 | adapter 写入失败 | 锁定 Volcano 1.10+；adapter 版本探测 |
| DCGM Exporter job label 不一致 | PromQL 查无数据 | lab 部署时冻结 job label |

### 11.3 Assumptions

- 平台默认队列由 `m1-infra-e` manifest 预置 Volcano Queue CRD，adapter 启动时读取并标记 `is_platform_default=true`
- BOSS P0 集群级 occupancy 复用 `GET /gpu-inventory/occupancy`，平台角色 JWT 可读全域（adapter 不注入 tenant filter）
- Volcano Queue CRD namespace 与 adapter 一致（`volcano-system` 或 tenant namespace）

---

## Frozen Facts Table

| 类别 | 事实 | 来源 | 状态 |
|------|------|------|------|
| 冻结路径 | `GET /gpu-inventory` (`listGPUInventory`) | v1.yaml L4019 | ✅ frozen |
| 冻结路径 | `GET /gpu-inventory/occupancy` (`getGPUOccupancy`) | v1.yaml L4041 | ✅ frozen |
| 冻结 Schema | `GPUInventoryRecord` | v1.yaml L1697 | ✅ frozen |
| 冻结 Schema | `GPUInventoryListResponse` | v1.yaml L1713 | ✅ frozen |
| 冻结 Schema | `GPUOccupancyStats` | v1.yaml L1723 | ✅ frozen |
| 冻结 RBAC | `scope:gpu-inventory:read` | v1.yaml L4025 | ✅ frozen |
| 冻结路径 | `GET /observability/query` (`queryObservability`) | v1.yaml | ✅ frozen |
| 冻结路径 | `POST /instances`（`kind=gpu_container`） | v1.yaml L2060 | ✅ frozen |
| 待补 | `GET/POST /gpu-scheduling/queues` | v1.yaml 无 | **待补 P0-①** |
| 待补 | `GET/PATCH/DELETE /gpu-scheduling/queues/{id}` | v1.yaml 无 | **待补 P0-①** |
| 待补 | `scope:gpu-scheduling:read` / `scope:gpu-scheduling:write` | v1.yaml 无 | **待补 P0-①** |
| 待补 | `GPUSchedulingQueue` schema | v1.yaml 无 | **待补 P0-①** |
| 待补 | `InstanceRecord.gpu.queue_name` / `resource_name` / `scheduling_reason` | v1.yaml 未冻结 | **待补 P0-①** |
| 待补 | DCGM PromQL 模板 | 无冻结证据 | **待补 P0-④** |
| 已存在 | `ports.GPUInventory` interface | `pkg/ports/gpu_inventory.go` | ✅ exists |
| 已存在 | `KubernetesGPUInventory` adapter | `pkg/adapters/runtime/` | ✅ exists |
| 已存在 | `LocalGPUInventory` (dev profile) | `pkg/adapters/runtime/` | ✅ exists |
| 已存在 | `PlanningRuntime` | `pkg/adapters/runtime/planning.go` | ✅ exists |
| 已存在 | `LocalInstanceOrchestrator` | `pkg/adapters/runtime/instance_orchestrator.go` | ✅ exists |
| 已存在 | `m1-infra-e` 合约 manifest | `deploy/manifests/` | ✅ exists |
| 已存在 | `m1-infra-f` preflight Job | `deploy/manifests/` | ✅ exists |
| 不存在 | `GPUSchedulingQueueStore` port | `pkg/ports/` | **P0 新建** |
| 不存在 | `VolcanoQueueStore` adapter | `pkg/adapters/runtime/` | **P0 新建** |
| 不存在 | GPU scheduling handler | `services/ani-gateway/internal/handlers/` | **P0 新建** |
