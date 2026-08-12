# SPEC: 配额服务落地（扣减 + 配置查询 + 租户生命周期管理）

> Technical specification derived from:
> - PRD: `repo/services/tasks/modules/prd/core/quota/prd-quota-service.md`
> - Plan: `repo/services/tasks/modules/plan/plan-quota-service.md`
> - UX: N/A — backend-only
> Generated: 2026-08-04 | Target branch: main | Commit: N/A

---

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 规范 ANI 平台配额服务的完整技术实现，包含三个 port interface（QuotaService 扣减 / QuotaStoreService 配置查询 / QuotaAdminService 租户生命周期管理）、一个 PG adapter struct 实现三个 interface、5 个 Core API 端点 + handler + 鉴权扩展 + SDK 生成 + 单元/集成测试。三部分操作同一组表（`resource_quota` / `resource_quota_meta` / `resource_reservations`），但调用方、事务模型、UPSERT 语义不同，因此拆成三个 port，在同一个任务里交付。

### 1.2 PRD Reference

- Source: `repo/services/tasks/modules/prd/core/quota/prd-quota-service.md`
- Plan source: `repo/services/tasks/modules/plan/plan-quota-service.md`
- UX source: N/A（纯后端 Core 服务，无 UI）
- User Stories covered: US-001 ~ US-012（共 12 个）
- Functional Requirements covered: FR-1 ~ FR-31（共 31 个）

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| 三个 port 分离 | QuotaService / QuotaStoreService / QuotaAdminService 分文件 | 调用方、操作语义、事务模型、UPSERT 策略都不同；接口隔离避免臃肿 |
| 一个 adapter struct 实现三个 interface | `PostgresQuota` struct 持有 `ports.MetadataStore` | 共享表结构和测试基础设施；编译期接口断言保证接口实现 |
| adapter 文件路径 | `pkg/adapters/runtime/postgres_quota.go` | 与现有 `runtime/` 目录下 adapter（metering、k8s_cluster 等）保持一致；不建 local 态（配额服务依赖真实 PG 事务和 RLS，local mock 无意义） |
| TCC 状态机 | reserved→confirmed(Cancel)/cancelled/confirmed→released | TCC 预占/实扣状态机保证并发不超卖和幂等 |
| Try 自开事务 | Try/TryMany 自开 `WithTenantTx` | 创建前无业务行可挂载事务 |
| Confirm/Cancel/Release 接外部 tx | 接收 `MetadataTx` 参数 | 与调用方 `UpsertStatus` + outbox 同事务原子 |
| RLS 双 policy | platform_bypass + self | `WithPlatformTx` 不设 tenant_id 走 bypass；`WithTenantTx` 设 tenant_id 走 self |
| 缩容策略 | `GREATEST($3, reserved + used)` SQL 层 clamp | 保证 total >= used+reserved 不违反 CHECK 约束，无需修改 migration |
| Put 不 clamp | UPSERT 覆盖 total，撞 CHECK 报错 | BOSS 运营场景失误应报错而非悄悄 clamp |
| CreateTenantQuota 幂等 | ON CONFLICT DO NOTHING | 已存在维度跳过，不阻断其余维度创建 |
| Confirm/Cancel/Release 幂等 | 状态机 WHERE 子句守卫 | WHERE state='reserved'/'confirmed' RETURNING 空 = 已处理，跳过 |
| 错误响应 | 新增专用 error responses（SPEC 补充，plan 未明确要求） | 将 plan §7.4 错误码表翻译为 OpenAPI 命名 responses，与现有 v1.yaml 中 VectorStoreNotFound/NetworkSecurityGroupRuleNotFound 等专用 responses 风格一致；引用 ErrorResponse schema 并通过 description 区分特定 code |

---

## 2. Architecture

### 2.1 System Context

配额服务位于 ANI Core 层，为多租户平台提供资源配额管理能力。调用链全景：

```
[未来 tenant-service] ──HTTP──> [ani-gateway /api/v1/admin/tenants/{tenant_id}/quota]
                                        │
                                        ├─ handler (quota_resources.go, Core-protected, scope=platform)
                                        │     │
                                        │     └─ ports.QuotaAdminService (接口)
                                        │           │
                                        │           └─ PostgresQuota (adapter)
                                        │                 │
                                        │                 └─ MetadataStore.WithPlatformTx
                                        │                       │
                                        │                       └─ PostgreSQL (resource_quota / resource_quota_meta)
                                        │
                                        ├─ [GET /api/v1/admin/quota-meta] ──> ports.QuotaAdminService.ListQuotaMeta
                                        │     └─ 只读 resource_quota_meta（enabled=true）
                                        │
                                        ├─ [嘉明 BOSS/Console handler] ──内部──> ports.QuotaStoreService (配置查询)
                                        │     │                                   ├─ Put    (UPSERT total)
                                        │     │                                   ├─ List   (分页查)
                                        │     │                                   ├─ GetMy  (自查, WithTenantTx)
                                        │     │                                   └─ GetTotalForUpdateTx (FOR UPDATE 锁行)
                                        │     │                                         └─ 嘉明 handler 内同 tx: 查 gpu_slices + 判断 + 写切片
                                        │     └─ PostgresQuota (同一个 adapter)
                                        │
                                        └─ [demo_instances.go] ──内部──> ports.QuotaService (扣减)
                                                                              └─ 同库 resource_quota/resource_reservations
```

三个 port 操作同一组表，但走不同 port 和不同事务模型，互不污染。Services 层（如 tenant-service）不得 import `pkg/ports`/`pkg/adapters`，只能通过 Core SDK 调 REST。

### 2.2 Component Design

| Component | Type | Responsibility |
|-----------|------|----------------|
| `pkg/ports/quota.go` | NEW port | 定义 `QuotaService`（扣减）+ `QuotaStoreService`（配置查询）interface + `ResourceType` 常量 + 相关类型 |
| `pkg/ports/quota_admin.go` | NEW port | 定义 `QuotaAdminService`（租户生命周期管理）interface + 相关类型 |
| `pkg/ports/errors.go` | MODIFY | 追加配额哨兵错误 |
| `pkg/adapters/runtime/postgres_quota.go` | NEW adapter | `PostgresQuota` struct 实现三个 interface，持有 `ports.MetadataStore` |
| `repo/services/ani-gateway/internal/router/quota_resources.go` | NEW handler | `quotaAPI` struct + 5 个路由注册 |
| `repo/services/ani-gateway/internal/middleware/auth.go` | MODIFY | 扩展 `scopeAllowedForPath` 放行 `/api/v1/admin/*` |
| `repo/services/ani-gateway/internal/router/router.go` | MODIFY | `RegisterOptions` 加 `QuotaAdminService` 字段 + 调用 `registerQuotaResources` |
| `repo/api/openapi/v1.yaml` | MODIFY | 新增 5 个端点 + schema + 专用错误 responses |
| `sdks/core/go/anisdk/client.go` | REGEN | `make gen-core-sdk` 自动生成 |

### 2.3 Module Interactions

**扣减流程（创建实例）**：
```
demo_instances.create
  → QuotaService.TryMany (自开 WithTenantTx)
    → tryInTx: 校验 meta → lazy init → 单行原子 UPDATE(WHERE 余量) → 插入流水
  → 返回 []QuotaReservation
```

**Confirm 流程（reconciler 发现 Running）**：
```
reconciler (嘉明)
  → WithTenantTx (自开事务)
    → QuotaService.Confirm(ctx, tx, txIDs, resourceRef) (接外部 tx)
      → 流水 reserved→confirmed (WHERE state='reserved' 守卫)
      → resource_quota reserved→used 转账
    → WorkloadInstanceStore.UpsertStatusTx (同一事务)
    → outbox 写入 (同一事务)
```

**管理流程（tenant-service 经 SDK）**：
```
tenant-service
  → Core SDK POST /admin/tenants/{id}/quota
    → ani-gateway handler (scope=platform)
      → QuotaAdminService.CreateTenantQuota (WithPlatformTx)
        → 校验租户存在 → 校验 meta → ON CONFLICT DO NOTHING → 回读
```

### 2.4 File Structure

```
repo/
├── pkg/
│   ├── ports/
│   │   ├── quota.go                              [NEW] QuotaService + QuotaStoreService + ResourceType + 类型
│   │   ├── quota_admin.go                        [NEW] QuotaAdminService + 类型
│   │   └── errors.go                             [MODIFY] 追加 5 个哨兵错误
│   └── adapters/
│       └── runtime/
│           ├── postgres_quota.go                  [NEW] PostgresQuota adapter (实现三个 interface)
│           ├── postgres_quota_test.go             [NEW] 扣减单元测试
│           ├── postgres_quota_store_test.go        [NEW] 配置查询单元测试
│           ├── postgres_quota_admin_test.go        [NEW] 管理单元测试
│           └── integration_test.go                [NEW] 集成测试 (//go:build integration)
├── api/
│   └── openapi/
│       └── v1.yaml                                [MODIFY] 新增 5 端点 + schema + error responses
├── services/
│   └── ani-gateway/
│       └── internal/
│           ├── middleware/
│           │   └── auth.go                        [MODIFY] scopeAllowedForPath 扩展
│           └── router/
│               ├── router.go                      [MODIFY] RegisterOptions + RegisterWithOptions
│               └── quota_resources.go             [NEW] 5 个 handler
└── sdks/
    └── core/
        └── go/
            └── anisdk/
                └── client.go                      [REGEN] make gen-core-sdk
```

---

## 3. Data Model

### 3.1 Schema Changes

本任务不创建三张表（由李宇 migration 负责），但 adapter 依赖以下表结构。此为参考 SQL（plan 附录 A），adapter 以此为准实现。

**resource_quota_meta**（配额元数据表，只读）：
```sql
CREATE TABLE resource_quota_meta (
    resource_type     TEXT PRIMARY KEY,
    display_name      TEXT NOT NULL,
    unit              TEXT NOT NULL,
    is_discrete       BOOLEAN NOT NULL DEFAULT TRUE,
    default_quota     BIGINT NOT NULL,
    collector_id      TEXT,
    description       TEXT,
    enabled           BOOLEAN NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- seed 8 维度：gpu_count/cpu_core/memory_gb/storage_gb/token_count/kb_query_count/member_count/inference_service_count
```

**resource_quota**（租户配额表，读写）：
```sql
CREATE TABLE resource_quota (
    tenant_id      UUID   NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    resource_type  TEXT   NOT NULL REFERENCES resource_quota_meta(resource_type),
    total          BIGINT NOT NULL,
    reserved       BIGINT NOT NULL DEFAULT 0,
    used           BIGINT NOT NULL DEFAULT 0,
    CHECK (total >= 0 AND reserved >= 0 AND used >= 0),
    CHECK (reserved + used <= total),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, resource_type)
);
-- RLS: 双 policy（platform_bypass + self）
```

**resource_reservations**（预占流水表，读写）：
```sql
CREATE TABLE resource_reservations (
    tx_id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    resource_type TEXT NOT NULL REFERENCES resource_quota_meta(resource_type),
    amount        BIGINT NOT NULL CHECK (amount > 0),
    state         TEXT NOT NULL DEFAULT 'reserved'
        CHECK (state IN ('reserved','confirmed','cancelled','expired','released')),
    resource_ref  TEXT,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- 索引：idx_res_state_expires (state, expires_at) WHERE state='reserved'
-- 索引：idx_res_tenant (tenant_id, state)
-- RLS: 双 policy（platform_bypass + self）
```

**RLS 双 policy（两张表均需）**：
```sql
-- 平台操作绕过 RLS：未设置 tenant_id 时放行所有行
CREATE POLICY resource_quota_platform_bypass
  ON resource_quota FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

-- 租户上下文：只能操作自己的行
CREATE POLICY resource_quota_self
  ON resource_quota FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
```

### 3.2 Entity Definitions

**ResourceType 常量**（`pkg/ports/quota.go`）：
```go
type ResourceType string

const (
    QuotaGPUCount              ResourceType = "gpu_count"
    QuotaCPUCore               ResourceType = "cpu_core"
    QuotaMemoryGB              ResourceType = "memory_gb"
    QuotaStorageGB             ResourceType = "storage_gb"
    QuotaTokenCount            ResourceType = "token_count"
    QuotaKBQueryCount          ResourceType = "kb_query_count"
    QuotaMemberCount           ResourceType = "member_count"
    QuotaInferenceServiceCount ResourceType = "inference_service_count"
)
```

**QuotaService 相关类型**（`pkg/ports/quota.go`）：
```go
type QuotaTryRequest struct {
    TenantID     string
    ResourceType ResourceType
    Amount       int64
}

type QuotaReservation struct {
    TxID      string
    ExpiresAt time.Time
}
```

**QuotaStoreService 相关类型**（`pkg/ports/quota.go`）：
```go
type QuotaView struct {
    TenantID string
    Total    map[ResourceType]int64
    Used     map[ResourceType]int64
    Reserved map[ResourceType]int64
}

type QuotaPutRequest struct {
    TenantID       string
    Total          map[ResourceType]int64
    IdempotencyKey string
}

type QuotaListRequest struct {
    TenantID string // 可选过滤，空表示全部
    Limit    int
    Cursor   string
}

type QuotaListResult struct {
    Items      []QuotaView
    Total      int
    NextCursor string
}
```

**QuotaAdminService 相关类型**（`pkg/ports/quota_admin.go`）：
```go
type QuotaItemInput struct {
    ResourceType ResourceType
    Total        int64 // <=0 表示未提供，取 default_quota
}

type QuotaItemUpdate struct {
    ResourceType ResourceType
    Total        int64
}

type QuotaMeta struct {
    ResourceType  ResourceType
    DisplayName   string
    Unit          string
    DefaultQuota  int64
    IsDiscrete    bool
}

type QuotaInfo struct {
    TenantID     string
    ResourceType  ResourceType
    Total        int64
    Reserved     int64
    Used         int64
    Tightened    bool      // PUT 缩容自动收紧标记
    Unit         string    // 来自 meta（GET 时 JOIN 返回）
    DisplayName  string    // 来自 meta（GET 时 JOIN 返回）
    IsDiscrete   bool      // 来自 meta（GET 时 JOIN 返回）
    UpdatedAt    time.Time
}
```

**哨兵错误**（`pkg/ports/errors.go` 追加）：
```go
var ErrQuotaExceeded              = errors.New("quota exceeded")
var ErrQuotaResourceNotRegistered = errors.New("quota resource type not registered")
var ErrQuotaIdempotencyConflict    = errors.New("quota idempotency key conflict")
var ErrQuotaNotFound              = errors.New("quota not found")
var ErrQuotaAlreadyExists         = errors.New("quota already exists")
// 注：ErrTenantNotFound 复用 errors.go 已有定义
```

### 3.3 Relationships

- `resource_quota.tenant_id` → `tenants(id)` (FK, ON DELETE CASCADE)
- `resource_quota.resource_type` → `resource_quota_meta.resource_type` (FK)
- `resource_reservations.tenant_id` → `tenants(id)` (FK, ON DELETE CASCADE)
- `resource_reservations.resource_type` → `resource_quota_meta.resource_type` (FK)
- `resource_reservations` 与 `resource_quota` 无直接 FK，通过 `(tenant_id, resource_type)` 逻辑关联

### 3.4 Migration Plan

- 三张表 migration 由李宇负责提交（不在本任务范围）
- adapter 代码以本 SPEC §3.1 表结构为准
- 李宇 migration 已含 8 维度 seed INSERT
- **关键确认项**：`resource_reservations.state` CHECK 必须含 5 态（`reserved/confirmed/cancelled/expired/released`），其中 `released` 不得遗漏
- **关键确认项**：`gen_random_uuid()` 依赖 pgcrypto extension，需确认 migration 是否启用

---

## 4. API Design

### 4.1 OpenAPI Change Plan (Core only)

| Change | operationId | Compatibility | idempotency_key |
|--------|-------------|---------------|-----------------|
| POST `/admin/tenants/{tenant_id}/quota` | createTenantQuota | 新增端点（兼容） | required |
| PUT `/admin/tenants/{tenant_id}/quota` | updateTenantQuota | 新增端点（兼容） | required |
| GET `/admin/tenants/{tenant_id}/quota` | getTenantQuota | 新增端点（兼容） | N/A |
| DELETE `/admin/tenants/{tenant_id}/quota` | deleteTenantQuota | 新增端点（兼容） | required |
| GET `/admin/quota-meta` | listQuotaMeta | 新增端点（兼容） | N/A |

### 4.2 Frozen Facts Table

| Item | Status | Detail |
|------|--------|--------|
| Frozen Paths | 已确认 | 5 个端点路径 + operationId 见 §4.1 |
| Frozen Schemas | 已确认 | QuotaCreateRequest/QuotaCreateItem/QuotaUpdateRequest/QuotaUpdateItem/Quota/QuotaItem/QuotaDeleteResponse/QuotaMetaListResponse/QuotaMeta |
| Frozen Response/Error codes | 已确认 | TENANT_NOT_FOUND(404)/QUOTA_NOT_FOUND(404)/QUOTA_ALREADY_EXISTS(409)/QUOTA_RESOURCE_NOT_REGISTERED(422)/VALIDATION_FAILED(400) |
| Non-Frozen Capabilities | 待补 | 无 |
| Known Risky Assumptions | 已标记 | 李宇 migration 表结构与方案 SQL 完全一致（见 §11.3） |

### 4.3 Endpoints

| Method | Path | Description | Auth scope | Request | Response |
|--------|------|-------------|------------|---------|----------|
| POST | `/admin/tenants/{tenant_id}/quota` | 批量初始化配额行 | platform | `QuotaCreateRequest` | `Quota` (200) |
| PUT | `/admin/tenants/{tenant_id}/quota` | 批量改 total | platform | `QuotaUpdateRequest` | `Quota` (200) |
| GET | `/admin/tenants/{tenant_id}/quota` | 查该租户所有维度 | platform | 无 | `Quota` (200) |
| DELETE | `/admin/tenants/{tenant_id}/quota` | 删除该租户所有配额 | platform | 无 | `QuotaDeleteResponse` (200) |
| GET | `/admin/quota-meta` | 查可用配额元数据 | platform | 无 | `QuotaMetaListResponse` (200) |

- `tenant_id` 从路径参数取（平台管理员指定目标租户）
- POST/PUT/DELETE 支持 `idempotency_key` header

### 4.4 Request/Response Schemas

**QuotaCreateRequest**（POST 请求）：
```yaml
QuotaCreateRequest:
  type: object
  required: [items]
  properties:
    items:
      type: array
      minItems: 1
      items:
        $ref: '#/components/schemas/QuotaCreateItem'

QuotaCreateItem:
  type: object
  required: [resource_type]
  properties:
    resource_type: { type: string, description: "配额维度标识" }
    total:
      type: integer
      format: int64
      minimum: 0
      description: "配额上限；未提供时取 resource_quota_meta.default_quota"
```

**QuotaUpdateRequest**（PUT 请求）：
```yaml
QuotaUpdateRequest:
  type: object
  required: [items]
  properties:
    items:
      type: array
      minItems: 1
      items:
        $ref: '#/components/schemas/QuotaUpdateItem'

QuotaUpdateItem:
  type: object
  required: [resource_type, total]
  properties:
    resource_type: { type: string }
    total:
      type: integer
      format: int64
      minimum: 0
      description: "新配额上限，允许低于当前 used（缩容）"
```

**Quota**（GET/POST/PUT 共用响应）：
```yaml
Quota:
  type: object
  required: [tenant_id, items]
  properties:
    tenant_id: { type: string, format: uuid }
    items:
      type: array
      items:
        $ref: '#/components/schemas/QuotaItem'

QuotaItem:
  type: object
  required: [resource_type, total, used, reserved]
  properties:
    resource_type: { type: string }
    total: { type: integer, format: int64, description: "配额上限" }
    used: { type: integer, format: int64, description: "已实扣" }
    reserved: { type: integer, format: int64, description: "已预占" }
    tightened: { type: boolean, description: "PUT 缩容自动收紧标记" }
    unit: { type: string, description: "单位（来自 meta，GET 时返回）" }
    display_name: { type: string, description: "展示名称（来自 meta，GET 时返回）" }
    is_discrete: { type: boolean, description: "是否离散计数" }
```

**QuotaDeleteResponse**（DELETE 响应）：
```yaml
QuotaDeleteResponse:
  type: object
  required: [tenant_id, message]
  properties:
    tenant_id: { type: string, format: uuid }
    message: { type: string, example: "quota deleted" }
```

**QuotaMetaListResponse**（GET quota-meta 响应）：
```yaml
QuotaMetaListResponse:
  type: object
  required: [items]
  properties:
    items:
      type: array
      items:
        $ref: '#/components/schemas/QuotaMeta'

QuotaMeta:
  type: object
  required: [resource_type, display_name, unit, default_quota, is_discrete]
  properties:
    resource_type: { type: string }
    display_name: { type: string }
    unit: { type: string }
    default_quota: { type: integer, format: int64 }
    is_discrete: { type: boolean }
```

### 4.5 Error Responses

> **来源说明**：plan §7.4 只以表格形式列出错误码（供 handler 哨兵→HTTP 映射用），未要求在 OpenAPI 定义命名 error responses。本节为 SPEC 补充——将 plan 错误码表翻译为 OpenAPI 命名 responses，与现有 `v1.yaml` 中 `VectorStoreNotFound`/`NetworkSecurityGroupRuleNotFound`/`InvalidFilter` 等专用 responses（均引用 `ErrorResponse` schema）风格一致。纯新增，不破坏现有契约。

新增专用 error responses（引用 `ErrorResponse` schema）：

```yaml
responses:
    TenantNotFound:
      description: 租户不存在（code=TENANT_NOT_FOUND）
      content:
        application/json:
          schema: { $ref: '#/components/schemas/ErrorResponse' }
    QuotaNotFound:
      description: 配额行不存在（code=QUOTA_NOT_FOUND）
      content:
        application/json:
          schema: { $ref: '#/components/schemas/ErrorResponse' }
    QuotaAlreadyExists:
      description: 配额行已存在（code=QUOTA_ALREADY_EXISTS）
      content:
        application/json:
          schema: { $ref: '#/components/schemas/ErrorResponse' }
    QuotaResourceNotRegistered:
      description: 资源类型未注册或已禁用（code=QUOTA_RESOURCE_NOT_REGISTERED）
      content:
        application/json:
          schema: { $ref: '#/components/schemas/ErrorResponse' }
    QuotaValidationFailed:
      description: 参数校验失败（code=VALIDATION_FAILED）
      content:
        application/json:
          schema: { $ref: '#/components/schemas/ErrorResponse' }
```

### 4.6 Breaking Changes

无破坏性变更。所有 5 个端点和 9 个 schema 均为新增，不删除/不修改现有端点和 schema。

---

## 5. Business Logic

### 5.1 Port Contracts

**QuotaService interface**（`pkg/ports/quota.go`）：
```go
type QuotaService interface {
    Try(ctx context.Context, req QuotaTryRequest) (QuotaReservation, error)
    TryMany(ctx context.Context, reqs []QuotaTryRequest) ([]QuotaReservation, error)
    Confirm(ctx context.Context, tx MetadataTx, txIDs []string, resourceRef string) error
    Cancel(ctx context.Context, tx MetadataTx, txIDs []string) error
    Release(ctx context.Context, tx MetadataTx, txIDs []string) error
}
```

**QuotaStoreService interface**（`pkg/ports/quota.go`）：
```go
type QuotaStoreService interface {
    Put(ctx context.Context, idempotencyKey string, req QuotaPutRequest) (QuotaView, error)
    List(ctx context.Context, req QuotaListRequest) (QuotaListResult, error)
    GetMy(ctx context.Context, tenantID string) (QuotaView, error)
    GetTotalForUpdateTx(ctx context.Context, tx MetadataTx, tenantID string, rt ResourceType) (int64, error)
}
```

**QuotaAdminService interface**（`pkg/ports/quota_admin.go`）：
```go
type QuotaAdminService interface {
    CreateTenantQuota(ctx context.Context, tenantID string, items []QuotaItemInput) ([]QuotaInfo, error)
    UpdateTenantQuota(ctx context.Context, tenantID string, items []QuotaItemUpdate) ([]QuotaInfo, error)
    GetTenantQuota(ctx context.Context, tenantID string) ([]QuotaInfo, error)
    DeleteTenantQuota(ctx context.Context, tenantID string) error
    ListQuotaMeta(ctx context.Context) ([]QuotaMeta, error)
}
```

### 5.2 Core Algorithms

**tryInTx（Try/TryMany 共用内部方法）**：
```
1. 校验 meta：SELECT enabled, default_quota FROM resource_quota_meta WHERE resource_type = $1
   - ErrNoRows → ErrQuotaResourceNotRegistered
   - enabled=false → ErrQuotaResourceNotRegistered
2. lazy init：INSERT INTO resource_quota ... ON CONFLICT DO NOTHING（并发首次安全）
3. 单行原子预占：UPDATE resource_quota SET reserved = reserved + $1
   WHERE tenant_id = $2 AND resource_type = $3 AND reserved + used + $1 <= total
   - RowsAffected=0 → ErrQuotaExceeded（余量不足）
4. 插入流水：INSERT INTO resource_reservations ... RETURNING tx_id, expires_at
```

**Try**：
```
自开 WithTenantTx(req.TenantID) → 调 tryInTx → 返回 QuotaReservation
```

**TryMany**：
```
1. 校验所有 req 的 tenant_id 一致
2. 自开 WithTenantTx(tenantID)
3. 单事务内循环 tryInTx
4. 任一失败 → 事务回滚 → 无悬挂预占
```

**Confirm**（接外部 tx，幂等）：
```
for each txID:
  1. UPDATE resource_reservations SET state='confirmed', resource_ref=$2
     WHERE tx_id=$1 AND state='reserved' RETURNING state
     - ErrNoRows → continue（已处理，幂等跳过）
  2. UPDATE resource_quota SET reserved-=r.amount, used+=r.amount
     FROM resource_reservations r WHERE r.tx_id=$1 AND r.state='confirmed'
```

**Cancel**（接外部 tx，幂等）：
```
for each txID:
  1. UPDATE resource_reservations SET state='cancelled'
     WHERE tx_id=$1 AND state='reserved' RETURNING state
     - ErrNoRows → continue（幂等跳过）
  2. UPDATE resource_quota SET reserved-=r.amount
     FROM resource_reservations r WHERE r.tx_id=$1 AND r.state='cancelled'
```

**Release**（接外部 tx，幂等）：
```
for each txID:
  1. UPDATE resource_reservations SET state='released'
     WHERE tx_id=$1 AND state='confirmed' RETURNING state
     - ErrNoRows → continue（幂等跳过）
  2. UPDATE resource_quota SET used-=r.amount
     FROM resource_reservations r WHERE r.tx_id=$1 AND r.state='released'
```

**Put**（UPSERT total，不 clamp）：
```
自开 WithPlatformTx:
  for each (rt, total) in req.Total:
    1. 校验 meta enabled
    2. INSERT INTO resource_quota ... ON CONFLICT DO UPDATE SET total=EXCLUDED.total
       （不 clamp，撞 CHECK 则报错透传）
  回读所有维度 → QuotaView
```

**List**（租户级 keyset 分页）：
```
if req.TenantID != "":
  直接调 GetMy → 返回单租户全部维度（不分页）
else:
  自开 WithPlatformTx:
    1. SELECT DISTINCT tenant_id FROM resource_quota WHERE cursor < tenant_id LIMIT limit+1
    2. hasMore = len(tenantIDs) > limit; 截断
    3. SELECT tenant_id, resource_type, total, reserved, used FROM resource_quota WHERE tenant_id IN (...)
    4. 按 tenant_id 聚合为 []QuotaView
    5. NextCursor = 末尾 tenant_id
```

**GetMy**（自查，RLS 自动过滤）：
```
自开 WithTenantTx(tenantID):
  SELECT resource_type, total, reserved, used FROM resource_quota WHERE tenant_id = $1
  → QuotaView
```

**GetTotalForUpdateTx**（锁行查 total，接外部 tx）：
```
SELECT total FROM resource_quota WHERE tenant_id=$1 AND resource_type=$2 FOR UPDATE
- ErrNoRows → ErrQuotaNotFound
```

**CreateTenantQuota**（批量新建）：
```
自开 WithPlatformTx:
  1. 校验租户存在：SELECT EXISTS(SELECT 1 FROM tenants WHERE id=$1) → ErrTenantNotFound
  2. for each item:
     a. 校验 meta enabled + 取 default_quota
     b. total <= 0 时取 default_quota
     c. INSERT INTO resource_quota ... ON CONFLICT DO NOTHING（已存在跳过）
  3. 回读 items 涉及维度 → []QuotaInfo
```

**UpdateTenantQuota**（批量改 total，缩容 clamp）：
```
自开 WithPlatformTx:
  for each item:
    1. 校验 meta enabled
    2. UPDATE resource_quota SET total = GREATEST($3, reserved + used)
       WHERE tenant_id=$1 AND resource_type=$2
       - RowsAffected=0 → ErrQuotaNotFound
  回读 → 计算 tightened 标记（回读 total > 请求 total 时 tightened=true）
```

**GetTenantQuota**（查询，JOIN meta）：
```
自开 WithPlatformTx:
  SELECT q.*, m.unit, m.display_name, m.is_discrete
  FROM resource_quota q JOIN resource_quota_meta m ON m.resource_type = q.resource_type
  WHERE q.tenant_id = $1 ORDER BY q.resource_type
  → []QuotaInfo
```

**DeleteTenantQuota**（整租户删除）：
```
自开 WithPlatformTx:
  1. 校验租户存在 → ErrTenantNotFound
  2. DELETE FROM resource_reservations WHERE tenant_id=$1
  3. DELETE FROM resource_quota WHERE tenant_id=$1
  （不守卫 used/reserved）
```

**ListQuotaMeta**（查可用维度）：
```
自开 WithPlatformTx:
  SELECT resource_type, display_name, unit, default_quota, is_discrete
  FROM resource_quota_meta WHERE enabled=true ORDER BY resource_type
  → []QuotaMeta
```

### 5.3 State Machine

流水状态机（`resource_reservations.state`）：

```
                    ┌──────────┐
                    │ reserved │ ← Try/TryMany
                    └────┬─────┘
                         │
           ┌─────────────┼─────────────┐
           ▼              ▼              ▼
     ┌──────────┐  ┌──────────┐  ┌─────────┐
     │confirmed │  │cancelled │  │ expired  │
     └────┬─────┘  └──────────┘  └─────────┘
          │           (终态)       (终态)
          ▼
     ┌──────────┐
     │ released │
     └──────────┘
        (终态)
```

| 转移 | 触发 | 守卫 | 账本变更 |
|------|------|------|----------|
| reserved → confirmed | Confirm | WHERE state='reserved' | reserved -= amount, used += amount |
| reserved → cancelled | Cancel | WHERE state='reserved' | reserved -= amount |
| reserved → expired | TTL worker | WHERE state='reserved' AND expires_at < NOW() | reserved -= amount |
| confirmed → released | Release | WHERE state='confirmed' | used -= amount |

`cancelled`、`expired`、`released` 均为终态，不再流转。

### 5.4 Transaction Models

| 方法 | 事务来源 | 原因 |
|------|----------|------|
| Try | 自开 `WithTenantTx` | 创建前无业务行可挂载 |
| TryMany | 自开 `WithTenantTx` | 同上，多维度在同一事务内循环 |
| Confirm | 接外部 tx | 与 UpsertStatus + outbox 同事务原子 |
| Cancel | 接外部 tx | 与 DELETE instance + outbox 同事务原子 |
| Release | 接外部 tx | 与 UpsertStatusTx(deleted) + outbox 同事务原子 |
| Put | 自开 `WithPlatformTx` | BOSS 运营操作，平台级 |
| List | 自开 `WithPlatformTx` | BOSS 跨租户查询 |
| GetMy | 自开 `WithTenantTx` | Console 自查，RLS 自动过滤 |
| GetTotalForUpdateTx | 接外部 tx | 与 gpu_slices 查询 + 写切片同事务原子 |
| CreateTenantQuota | 自开 `WithPlatformTx` | 平台管理员操作 |
| UpdateTenantQuota | 自开 `WithPlatformTx` | 同上 |
| GetTenantQuota | 自开 `WithPlatformTx` | 同上 |
| DeleteTenantQuota | 自开 `WithPlatformTx` | 同上 |
| ListQuotaMeta | 自开 `WithPlatformTx` | 只读 meta 表 |

### 5.5 Edge Cases

| 场景 | 处理 |
|------|------|
| Try 余量不足 | `UPDATE ... WHERE reserved + used + $1 <= total` → RowsAffected=0 → `ErrQuotaExceeded` |
| TryMany 部分失败 | 任一维度失败 → 事务回滚 → 无悬挂预占 |
| Confirm 重复调用 | WHERE state='reserved' 不匹配 → ErrNoRows → continue（幂等跳过） |
| Cancel 重复调用 | 同 Confirm |
| Release 重复调用 | WHERE state='confirmed' 不匹配 → ErrNoRows → continue（幂等跳过） |
| Release 对非 confirmed 流水 | WHERE state='confirmed' 不匹配 → 跳过，不改账本 |
| Put total < used+reserved | 撞 CHECK 约束 → 错误透传（不 clamp） |
| UpdateTenantQuota total < used | `GREATEST($3, reserved + used)` clamp 到 used+reserved，tightened=true |
| CreateTenantQuota 已存在维度 | ON CONFLICT DO NOTHING 跳过，其余正常创建 |
| DeleteTenantQuota used>0 | 仍可删除（不守卫，清理语义） |
| GetTenantQuota 租户不存在 | 返回空 items（handler 可映射 404） |
| GetTotalForUpdateTx 行不存在 | ErrQuotaNotFound |
| RLS 租户隔离 | 租户 A 操作租户 B 数据 → RLS 拒绝（0 行/INSERT 被拒） |
| RLS 平台 bypass | WithPlatformTx 不设 tenant_id → bypass 放行所有行 |

---

## 6. Error Handling

### 6.1 Error Taxonomy

| Error Code | HTTP Status | Condition | User Message |
|------------|-------------|-----------|--------------|
| `TENANT_NOT_FOUND` | 404 | 租户不存在（Create/Delete 时校验） | "tenant not found" |
| `QUOTA_NOT_FOUND` | 404 | 配额行不存在（Update/GetTotalForUpdateTx 时） | "quota not found" |
| `QUOTA_ALREADY_EXISTS` | 409 | 配额行已存在（Create 时，已存在的跳过） | "quota already exists" |
| `QUOTA_RESOURCE_NOT_REGISTERED` | 422 | resource_type 未注册或 enabled=false | "quota resource type not registered" |
| `VALIDATION_FAILED` | 400 | 参数校验失败（total 负数 / items 空 / resource_type 重复） | "validation failed" |
| `QUOTA_EXCEEDED` | N/A（内部） | 预占余量不足 | "quota exceeded" |
| `INTERNAL` | 500 | 其他未预期错误 | "internal error" |

### 6.2 Retry Strategy

| 操作 | 可重试 | 幂等机制 |
|------|--------|----------|
| Try | 否（不幂等） | 无（每次 Try 生成新 tx_id） |
| TryMany | 否（不幂等） | 无 |
| Confirm | 是 | WHERE state='reserved' 守卫 |
| Cancel | 是 | WHERE state='reserved' 守卫 |
| Release | 是 | WHERE state='confirmed' 守卫 |
| Put | 是 | UPSERT 覆盖语义天然幂等 |
| CreateTenantQuota | 是 | ON CONFLICT DO NOTHING |
| UpdateTenantQuota | 是 | GREATEST clamp 幂等 |
| DeleteTenantQuota | 是 | 重复删除 0 行，无副作用 |
| ListQuotaMeta | 是 | 只读查询 |

### 6.3 Failure Modes

| 依赖失败 | 行为 |
|----------|------|
| PG 连接断开 | 事务回滚，错误透传 |
| RLS 拒绝 | 租户操作其他租户数据 → 0 行/INSERT 被拒 → 对应方法返回 ErrNoRows 或错误 |
| CHECK 约束违反 | Put total < used+reserved → PG 返回 check violation → adapter 透传 |
| meta 表无 seed | CreateTenantQuota/Try 校验 meta → ErrNoRows → ErrQuotaResourceNotRegistered |

---

## 7. Security

### 7.1 Authentication & Authorization

| 端点 | scope | 鉴权机制 |
|------|-------|----------|
| POST/PUT/GET/DELETE `/admin/tenants/{id}/quota` | platform | `scopeAllowedForPath` 放行 `/api/v1/admin/*` 要求 platform scope |
| GET `/admin/quota-meta` | platform | 同上 |
| QuotaService（内部调用） | tenant（Try/TryMany）/ 无（Confirm/Cancel/Release） | `WithTenantTx` 自动注入 RLS 上下文 |
| QuotaStoreService（内部调用） | platform（Put/List）/ tenant（GetMy）/ 外部 tx（GetTotalForUpdateTx） | 调用方（嘉明 handler）自行控制 |

**鉴权扩展**（`middleware/auth.go`）：
```go
func scopeAllowedForPath(path, scope string) bool {
    if strings.HasPrefix(path, "/api/v1/auth/platform/") ||
       strings.HasPrefix(path, "/api/v1/platform/") ||
       strings.HasPrefix(path, "/api/v1/admin/") {
        return scope == "platform"
    }
    return scope == "tenant"
}
```

### 7.2 Input Validation

| 字段 | 规则 |
|------|------|
| `tenant_id` | 路径参数，UUID 格式 |
| `resource_type` | 必须在 meta 表已注册且 enabled=true |
| `total`（Create） | >= 0；<=0 时取 default_quota |
| `total`（Update） | >= 0；< used+reserved 时 GREATEST clamp |
| `items` | minItems=1 |
| `idempotency_key` | POST/PUT/DELETE 必填 |

### 7.3 Data Protection

- RLS 双 policy 保证租户隔离：`platform_bypass`（NULL tenant_id 放行）+ `self`（tenant_id 匹配）
- `WithTenantTx` 自动注入 `app.current_tenant_id`，RLS self policy 强制租户只能操作自己的行
- `WithPlatformTx` 不设 tenant_id，RLS platform_bypass 放行所有行
- `FOR UPDATE` 锁行串行化并发预留校验

---

## 8. Performance

### 8.1 Expected Load

- 配额操作为低频管理操作 + 中频扣减操作
- Try/TryMany：创建实例时触发，QPS 与实例创建频率一致
- Confirm/Cancel/Release：reconciler 触发，QPS 与实例状态变更频率一致
- 管理端点：tenant-service 调用，极低频
- 数据量：8 维度 × N 租户，每租户最多 8 行 resource_quota

### 8.2 Optimization Strategy

- `resource_quota` 主键 `(tenant_id, resource_type)` 天然索引
- `resource_reservations` 索引 `idx_res_state_expires`（state, expires_at WHERE state='reserved'）支持 TTL worker
- `resource_reservations` 索引 `idx_res_tenant`（tenant_id, state）支持租户维度查询
- List 分页用 keyset（cursor=tenant_id），避免 OFFSET 深翻页
- `FOR UPDATE` 行锁粒度：单行 resource_quota，锁时间短

### 8.3 Database Considerations

- `UPDATE resource_quota SET reserved = reserved + $1 WHERE ... AND reserved + used + $1 <= total` 单行原子操作，利用 PG 行锁串行化并发，无需显式 `SELECT FOR UPDATE`
- `GREATEST($3, reserved + used)` 在 SQL 层 clamp，避免应用层多次往返
- `ON CONFLICT DO NOTHING` / `ON CONFLICT DO UPDATE` 利用唯一约束快速判断
- 集成测试用 `//go:build integration` tag 隔离，不阻塞 `make test`

---

## 9. Testing Strategy

### 9.1 Unit Tests — 扣减（QuotaService）

文件：`pkg/adapters/runtime/postgres_quota_test.go`

参考 `plan_audit_store_test.go` 的 `fakeMetadataTx` 模式，定义 `fakeMetadataTx` 实现 `ports.MetadataTx`。

| 场景 | 验证 |
|------|------|
| Try 成功 | meta enabled、lazy init、预占成功 |
| Try 失败：meta disabled | → `ErrQuotaResourceNotRegistered` |
| Try 失败：余量不足 | → `ErrQuotaExceeded` |
| TryMany 成功 | 多维度全占成功 |
| TryMany 原子性 | 第二维度不足 → 第一维度预占回滚 |
| Confirm 幂等 | state=reserved→confirmed，重复→ErrNoRows→跳过 |
| Cancel 幂等 | state=reserved→cancelled，重复→跳过 |
| Release 幂等 | state=confirmed→released，重复→跳过 |
| Confirm 后 reserved 减少 | used 增加 |
| Cancel 后 reserved 减少 | |
| Release 后 used 减少 | |
| Release 对非 confirmed 流水 | ErrNoRows→跳过，不改账本 |

### 9.2 Unit Tests — 配置查询（QuotaStoreService）

文件：`pkg/adapters/runtime/postgres_quota_store_test.go`

| 场景 | 验证 |
|------|------|
| Put 新增 | UPSERT 建行成功 |
| Put 修改 | UPSERT 覆盖 total 成功 |
| Put 资源未注册 | → `ErrQuotaResourceNotRegistered` |
| Put total < used+reserved | 撞 CHECK 约束报错（不 clamp） |
| Put 多维度 | 全部成功 |
| List 无过滤 | 按租户级分页返回 |
| List tenant_id 过滤 | 直接返回指定租户全部维度 |
| List 分页 cursor 衔接 | 不漏不重 |
| List 空表 | 返回空 items、空 cursor |
| List 超过 limit | hasMore=true |
| GetMy | 返回当前租户多维度 map |
| GetTotalForUpdateTx 行存在 | 返回 total |
| GetTotalForUpdateTx 行不存在 | → `ErrQuotaNotFound` |

### 9.3 Unit Tests — 管理（QuotaAdminService）

文件：`pkg/adapters/runtime/postgres_quota_admin_test.go`

| 场景 | 验证 |
|------|------|
| CreateTenantQuota 批量新建 | 含 total 省略取 default_quota |
| CreateTenantQuota 租户不存在 | → `ErrTenantNotFound` |
| CreateTenantQuota 资源未注册 | → `ErrQuotaResourceNotRegistered` |
| CreateTenantQuota 已存在维度 | ON CONFLICT DO NOTHING 跳过 |
| CreateTenantQuota items 为空 | 校验错误 |
| UpdateTenantQuota 批量改 | 成功 |
| UpdateTenantQuota 行不存在 | → `ErrQuotaNotFound` |
| UpdateTenantQuota 资源未注册 | → `ErrQuotaResourceNotRegistered` |
| UpdateTenantQuota 缩容 | GREATEST clamp，tightened=true |
| UpdateTenantQuota total >= used+reserved | tightened=false |
| UpdateTenantQuota items 为空 | 校验错误 |
| GetTenantQuota | JOIN meta 正确解析 |
| GetTenantQuota 租户存在但无配额行 | 返回空 items |
| DeleteTenantQuota | 连同 resource_reservations 删除 |
| DeleteTenantQuota 租户不存在 | → `ErrTenantNotFound` |
| DeleteTenantQuota used>0 | 仍可删除（不守卫） |
| ListQuotaMeta | 返回 enabled=true 维度 |
| ListQuotaMeta enabled=false | 不返回 |
| ListQuotaMeta 空表 | 返回空 items |

### 9.4 Integration Tests

文件：`pkg/adapters/runtime/integration_test.go`（`//go:build integration` build tag）

前置：PG 实例可用（docker-compose 本地 PG），DSN 通过 `ANI_TEST_ADMIN_DSN`、`ANI_TEST_TENANT_DSN` 环境变量覆盖。

双角色连接：

| 连接 | 角色 | RLS 行为 | 用途 |
|------|------|----------|------|
| 管理员 | ani (superuser) | 绕过 RLS | 建表 + seed + 平台操作 + 验证 bypass |
| 租户 | ani_app_user (普通角色) | 受 RLS 约束 | Try/Confirm/Cancel/Release + 验证隔离 |

**扣减场景（验证 RLS 写权限）**：

| # | 操作 | 验证 |
|---|------|------|
| 1 | 租户 A Try | RLS 放行：INSERT + UPDATE 成功 |
| 2 | 租户 A GetMy | RLS 放行：返回自己配额 |
| 3 | 租户 A 查租户 B 配额 | RLS 拦截：0 行 |
| 4 | 租户 A Confirm | RLS 放行：reserved→confirmed |
| 5 | 租户 B Confirm 租户 A 的 txID | RLS 拦截：0 行 → 跳过 |
| 6 | 租户 A Cancel | RLS 放行 |
| 7 | 租户 A Release | RLS 放行 |
| 8 | 租户 A INSERT tenant_id='B' 流水 | RLS 拦截：INSERT 被拒 |
| 9 | 并发 Try 不超卖 | reserved 不超过 total |
| 10 | TryMany 端到端 | 占多维度→Confirm→验证 used |
| 11 | Confirm/Cancel/Release 幂等 | 重复不重复扣减 |
| 12 | Release 端到端 | TryMany→Confirm→Release→used 归零 |

**管理场景（验证 RLS bypass）**：

| # | 操作 | 验证 |
|---|------|------|
| 13 | Put | 平台 UPSERT 成功（bypass RLS） |
| 14 | List | RLS bypass：返回所有租户 |
| 15 | Delete | RLS bypass：全删成功 |
| 16 | CreateTenantQuota 批量新建 | 回读验证 total/used=0/reserved=0 |
| 17 | CreateTenantQuota 幂等 | ON CONFLICT DO NOTHING 不覆盖 |
| 18 | UpdateTenantQuota 改 total | tightened=false |
| 19 | UpdateTenantQuota 缩容 | GREATEST clamp，tightened=true，Try→ErrQuotaExceeded |
| 20 | GetTenantQuota JOIN meta | unit/display_name/is_discrete 正确 |
| 21 | DeleteTenantQuota | resource_quota + resource_reservations 均清空 |
| 22 | ListQuotaMeta | 返回 enabled=true 维度 |
| 23 | SDK 端到端 | SDK 调 5 端点 → DB 验证 |

### 9.5 Acceptance Criteria Mapping

| US/FR | Test | Type | Description |
|-------|------|------|-------------|
| US-001 / FR-1~5 | typecheck + lint | compile | Port 契约定义编译通过 |
| US-002 / FR-6~12 | postgres_quota_test.go | unit | 扣减 adapter 单元测试 |
| US-003 / FR-13~16 | postgres_quota_store_test.go | unit | 配置查询 adapter 单元测试 |
| US-004 / FR-17~21 | postgres_quota_admin_test.go | unit | 管理 adapter 单元测试 |
| US-005 / FR-22 | validate_yaml.py | contract | OpenAPI 契约校验 |
| US-006 / FR-23 | typecheck + lint | compile | handler 编译通过 |
| US-007 / FR-24~25 | typecheck + lint | compile | 鉴权扩展编译通过 |
| US-008 / FR-26 | make gen-core-sdk + git diff | contract | SDK 无漂移 |
| US-009 / FR-27 | postgres_quota_test.go | unit | 扣减单元测试覆盖 |
| US-010 / FR-28 | postgres_quota_store_test.go | unit | 配置查询单元测试覆盖 |
| US-011 / FR-29 | postgres_quota_admin_test.go | unit | 管理单元测试覆盖 |
| US-012 / FR-30~31 | integration_test.go | integration | 集成测试覆盖双角色 RLS |

---

## 10. Implementation Plan

### 10.1 Phases

| Phase | 内容 | 依赖 |
|-------|------|------|
| **0** | **先验证 RLS 前提**（对齐 plan §14 第 1 步）：写最小集成测试，确认 `WithPlatformTx`（不设 `app.current_tenant_id`）在双 policy（`platform_bypass` + `self`）下能看到 resource_quota 行；需 PG 实例 + 李宇 migration 已落地 | 李宇 migration 已落地 |
| 1 | 改 Core API 契约（v1.yaml 5 端点 + schema + error responses） | 无（可与 Phase 0 并行） |
| 2 | 新增 port（quota.go + quota_admin.go + errors.go 哨兵） | Phase 1 |
| 3 | 实现 adapter（postgres_quota.go 三个 interface） | **Phase 0**, Phase 2 |
| 4 | 实现 handler（quota_resources.go 5 个端点）+ 鉴权扩展（auth.go）+ router 接线 | Phase 1, 2 |
| 5 | 单元测试（扣减 + 配置查询 + 管理） | Phase 3 |
| 6 | 生成 SDK（make gen-core-sdk） | Phase 1 |
| 7 | 集成测试（连 PG 实例，双角色验证 RLS） | Phase 3 |
| 8 | 全量验收（make test / validate-architecture / validate-services / git diff --check） | All |

> **Phase 0 说明**：plan §13.1 虽标注"已解决，李宇确认"，但 plan §14 仍要求在写任何代码前先验证前提。§6 的 5 个管理方法全部依赖 `WithPlatformTx` 能看到行；若双 policy 在实际 PG 实例上不成立，Phase 3-7 作废。Phase 0 用最小集成测试确认前提，再进入 Phase 3 实现 adapter。Phase 1（改契约）不依赖 Phase 0，可并行推进。

### 10.2 Issue Mapping

| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| **#0** | **10.1 Phase 0, 3.1, 7.2** | **high (blocker)** | **李宇 migration 已落地** |
| #1 (US-005) | 4.1, 4.2, 4.3, 4.4, 4.5 | high | — |
| #2 (US-001) | 3.2, 5.1 | high | #1 |
| #3 (US-002) | 5.2 (tryInTx/Try/TryMany/Confirm/Cancel/Release) | high | **#0**, #2 |
| #4 (US-003) | 5.2 (Put/List/GetMy/GetTotalForUpdateTx) | high | **#0**, #2 |
| #5 (US-004) | 5.2 (Create/Update/Get/Delete/ListQuotaMeta) | high | **#0**, #2 |
| #6 (US-006, US-007) | 4.3, 7.1 | high | #1, #2 |
| #7 (US-008) | 4.1 | medium | #1 |
| #8 (US-009) | 9.1 | medium | #3 |
| #9 (US-010) | 9.2 | medium | #4 |
| #10 (US-011) | 9.3 | medium | #5 |
| #11 (US-012) | 9.4 | medium | #3, #4, #5 |
| #12 | 10.1 Phase 8 | high | All |

> **#0 前置 Issue 说明**：对应 plan §14 第 1 步"先验证 RLS 风险"。内容是写最小集成测试，确认 `WithPlatformTx` 在双 policy 下能看到 resource_quota 行。#3/#4/#5（adapter 实现）依赖 #0 通过，因为 adapter 的管理方法全部基于 `WithPlatformTx`；若 #0 失败，需改用别的事务模型，#3/#4/#5 阻塞。#1（改契约）不依赖 #0，可并行推进。

### 10.3 Incremental Delivery

- Phase 0 可先行启动（需 PG 实例 + 李宇 migration），与 Phase 1 并行
- Phase 1-2 可先行合并（契约 + port 定义），不触碰现有功能
- Phase 3-4 需一起合并（adapter + handler + 鉴权 + router 接线），Phase 3 需 Phase 0 通过
- Phase 5-7 测试可增量提交
- Phase 8 全量验收门禁

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

- 李宇的 migration 表结构是否与方案附录 A 的 SQL 完全一致？（adapter 以 SPEC §3.1 表结构为准，不一致则调整）
- `scopeAllowedForPath` 扩展后是否有其他 `/api/v1/admin/` 路由被误伤？（调研未见，需确认）
- 集成测试 PG 实例使用本地 docker-compose 还是远程 `10.10.1.66:30945`？

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|-----------|
| 李宇 migration 表结构与方案 SQL 不一致 | adapter SQL 执行失败 | adapter 代码以 SPEC §3.1 为准；李宇提交后核对，不一致则调整 |
| `gen_random_uuid()` 依赖 pgcrypto | INSERT resource_reservations 失败 | 确认李宇 migration 是否启用 pgcrypto extension |
| `resource_reservations.state` CHECK 遗漏 'released' | Release 的 UPDATE 被 CHECK 拒绝 | 通知李宇 migration 的 CHECK 约束需含 5 态 |
| `WithTenantTx` 的 RLS 上下文注入 | Confirm/Cancel/Release 调用方未用 WithTenantTx → RLS 拒绝 | 调用方契约文档：必须用 WithTenantTx 或手动 SetDBTenant |
| GetTotalForUpdateTx FOR UPDATE 死锁 | 嘉明 handler 事务内锁顺序不一致 → 死锁 | 嘉明 handler 保证锁顺序：先锁 resource_quota，再操作 gpu_slices |
| SDK 自动生成覆盖 | 改契约后未 gen-core-sdk → validate-sdk-beta 报漂移 | 契约改完后立即 make gen-core-sdk |
| tenants 表旧字段与 resource_quota.total 语义重叠 | 数据不一致 | 本任务不动 tenants 旧字段，后续 PR 统一迁移 |
| meta 表种子数据未就绪 | CreateTenantQuota default_quota 兜底失败 | 李宇 migration 已含 8 维度 INSERT 种子 |
| QuotaStoreService.Put 撞 CHECK 约束 | BOSS 运营 total < used+reserved → 报错 | 预期行为（运营失误应报错），adapter 透传，handler 映射 HTTP |
| 三个 port 共享 adapter 测试隔离 | 单元测试 mock 状态污染 | 分别测三个 interface 方法，避免共用 mock 状态 |

### 11.3 Assumptions

- 李宇 migration 表结构与 SPEC §3.1 完全一致（字段名、类型、约束、RLS policy）
- 李宇 migration 已启用 pgcrypto extension（`gen_random_uuid()` 可用）
- 李宇 migration 已含 8 维度 seed INSERT（`resource_quota_meta` 有数据）
- `tenants` 表已由之前的 migration 建好（`resource_quota.tenant_id` 外键可引用）
- `WithPlatformTx` 不设 `app.current_tenant_id`，RLS `platform_bypass` policy 放行所有行
- `WithTenantTx` 设 `app.current_tenant_id`，RLS `self` policy 只放行本租户行
- 现有代码库无 `/api/v1/admin/` 路由（`scopeAllowedForPath` 扩展无误伤）

---

## 12. ANI Boundaries

| Item | Value |
|------|-------|
| Product line | core |
| Code scope | `pkg/ports/`、`pkg/adapters/runtime/`、`repo/services/ani-gateway/internal/`、`repo/api/openapi/v1.yaml`、`sdks/core/go/anisdk/` |
| OpenAPI authority | Core change batch（新增 `/admin/tenants/{id}/quota` + `/admin/quota-meta` 端点） |
| Frozen exclusions | Services backend（Services 层不得 import pkg/ports/pkg/adapters，只能通过 SDK 调 Core） |
| idempotency_key | required on: POST/PUT/DELETE `/admin/tenants/{tenant_id}/quota` |
| Module main doc | N/A（纯后端 Core 服务，无 UI 模块主文档） |
| Architecture gate | `make validate-architecture`（ports/adapters 边界）、`make validate-services`（Services boundary + SDK 漂移 + OpenAPI 契约） |
| SDK gate | `make gen-core-sdk && git diff --check -- sdks/core` |
