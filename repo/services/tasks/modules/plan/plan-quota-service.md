# 任务：配额服务落地计划（扣减 + 配置查询 + 租户生命周期管理）

> 状态: 计划草案
> 创建日期: 2026-07-28（扣减部分）/ 2026-07-31（管理 CRUD 部分）/ 2026-08-03（QuotaStoreService 部分）
> 负责人: kjs
> 依赖: 三张表 migration 由李宇负责提交（本 plan 附带 SQL 作为参考，adapter 按此表结构实现）
> 前置文档: `通用资源配额与计量落地方案.md` §4.2、§5；`core-quota-port-contract.md`（嘉明契约）；`core-quota-api.md`（李宇需求）

***

## 1. 任务边界

### 1.1 本任务做什么

实现完整的配额服务，包含三部分能力，作为一个任务交付：

- **扣减（QuotaService）**：Try / TryMany / Confirm / Cancel / Release + PG adapter，对应方案 §7 的 PR-1b 核心。调用方：demo\_instances 创建实例、嘉明 reconciler
- **配置查询（QuotaStoreService）**：Put（UPSERT total）/ List（分页查）/ GetMy（自查）/ GetTotalForUpdateTx（FOR UPDATE 锁行查 total）+ PG adapter。调用方：嘉明 BOSS/Console handler、GPU 预留校验 handler
- **租户生命周期管理（QuotaAdminService）**：CreateTenantQuota / UpdateTenantQuota / GetTenantQuota / DeleteTenantQuota / ListQuotaMeta + PG adapter + Core API 契约 `/admin/tenants/{id}/quota` + `/admin/quota-meta` + handler + Core SDK 生成。调用方：李宇 tenant-service 经 SDK 调 REST

三部分操作同一组表（resource\_quota / resource\_quota\_meta / resource\_reservations），但语义、调用方、事务模型不同，因此拆成三个 port（见 §3.4 说明），在同一个任务里交付。一个 PG adapter struct 实现三个 interface。

### 1.2 本任务不做什么

| 不做项                                                          | 负责人                                                       |
| ------------------------------------------------------------ | --------------------------------------------------------- |
| 三张表 migration 文件提交                                           | 李宇                                                        |
| TTL 孤儿预占回收 worker                                            | 后续 PR                                                     |
| resource\_quota\_meta 的注册/启用/禁用/改 default\_quota（写入）         | 后续 PR（QuotaMetaService）                                   |
| reconciler 接线 Confirm/Cancel                                 | 嘉明                                                        |
| BOSS/Console 的 `/quotas`、`/quotas/me` handler 实现             | 待确认（本任务只实现 QuotaStoreService port + adapter，handler 调它） |
| GPU 预留校验 handler（调 GetTotalForUpdateTx + 查 gpu\_slices + 判断） | 嘉明（本任务只提供 GetTotalForUpdateTx 锁行查询）                       |
| GPUSliceStore（CountByTenantTx / AssignToTenantTx）            | 嘉明                                                        |
| WorkloadInstanceStore.UpsertStatusTx 扩展                      | 后续 PR                                                     |
| tenants 表旧字段迁移/废弃                                            | 后续 PR                                                     |
| 租户管理 service 的独立拆分落地                                         | 后续（本方案只保证调用链可用）                                           |

### 1.3 依赖关系

- **前置依赖**：`tenants` 表已由 migration 建好（`resource_quota.tenant_id` 外键引用 `tenants(id)`；§6.1/§6.4 用 `SELECT EXISTS(SELECT 1 FROM tenants WHERE id = $1)` 校验租户存在）。配额三张表（resource\_quota / resource\_quota\_meta / resource\_reservations）由李宇的 migration 创建
- **被依赖**：
  - 嘉明的 reconciler 依赖 `QuotaService`（Confirm/Cancel/Release）；嘉明的 BOSS/Console handler 依赖 `QuotaStoreService`（Put/List/GetMy）；嘉明的 GPU 预留校验依赖 `QuotaStoreService.GetTotalForUpdateTx`
  - demo\_instances 创建实例依赖 `QuotaService.TryMany`
  - 李宇 tenant-service 经 Core SDK 调 REST，依赖 `QuotaAdminService` + Core API 契约 + SDK

***

## 2. 交付物清单

| 文件                                                             | 类型   | 说明                                                                                                           |
| -------------------------------------------------------------- | ---- | ------------------------------------------------------------------------------------------------------------ |
| `pkg/ports/quota.go`                                           | 新增   | `QuotaService` interface（扣减）+ `QuotaStoreService` interface（配置查询）+ `ResourceType` 常量 + 相关类型定义                |
| `pkg/ports/quota_admin.go`                                     | 新增   | `QuotaAdminService` interface（租户生命周期管理）+ 相关类型定义                                                              |
| `pkg/ports/errors.go`                                          | 修改   | 追加配额哨兵错误                                                                                                     |
| `pkg/adapters/quota/postgres_quota.go`                         | 新增   | PG adapter 实现（一个 struct `PostgresQuota` 实现三个 interface：QuotaService + QuotaStoreService + QuotaAdminService） |
| `pkg/adapters/quota/postgres_quota_test.go`                    | 新增   | 扣减单元测试（fake/mock）                                                                                            |
| `pkg/adapters/quota/postgres_quota_store_test.go`              | 新增   | 配置查询单元测试（fake/mock）                                                                                          |
| `pkg/adapters/quota/postgres_quota_admin_test.go`              | 新增   | 管理单元测试                                                                                                       |
| `pkg/adapters/quota/integration_test.go`                       | 新增   | 集成测试（连本地 docker-compose PG，含扣减+配置查询+管理）                                                                      |
| `repo/api/openapi/v1.yaml`                                     | 修改   | 新增 `/admin/tenants/{id}/quota` + `/admin/quota-meta` schema + 端点（契约先行）                                      |
| `repo/services/ani-gateway/internal/router/quota_resources.go` | 新增   | Core API handler（5 个端点路由注册：POST/PUT/GET/DELETE `/admin/tenants/{tenant_id}/quota` + GET `/admin/quota-meta`） |
| `repo/services/ani-gateway/internal/middleware/auth.go`        | 修改   | 扩展 `scopeAllowedForPath` 放行 `/api/v1/admin/*`（含 `/admin/tenants/*` 与 `/admin/quota-meta`）                   |
| `repo/services/ani-gateway/internal/router/router.go`          | 修改   | `RegisterOptions` 加 `QuotaAdminService ports.QuotaAdminService` 字段；`RegisterWithOptions` 加 `registerQuotaResources(v1, options.QuotaAdminService)` 调用 |
| `sdks/core/go/anisdk/client.go`                                | 重新生成 | `make gen-core-sdk` 自动生成，新增 quotas operation                                                                      |
| `kjs-study/plan-quota-service.md`                              | 本文件  | 统一任务计划                                                                                                       |

***

## 3. Port 契约

### 3.1 QuotaService（扣减，pkg/ports/quota.go）

按方案 §4.2 原文：

```go
package ports

import (
    "context"
    "errors"
    "time"
)

type ResourceType string

const (
    QuotaGPUCount            ResourceType = "gpu_count"
    QuotaCPUCore             ResourceType = "cpu_core"
    QuotaMemoryGB            ResourceType = "memory_gb"
    QuotaStorageGB           ResourceType = "storage_gb"
    QuotaTokenCount          ResourceType = "token_count"
    QuotaKBQueryCount        ResourceType = "kb_query_count"
    QuotaMemberCount         ResourceType = "member_count"
    QuotaInferenceServiceCount ResourceType = "inference_service_count"
)

var ErrQuotaExceeded              = errors.New("quota exceeded")
var ErrQuotaResourceNotRegistered = errors.New("quota resource type not registered")

type QuotaTryRequest struct {
    TenantID     string
    ResourceType ResourceType
    Amount       int64
}

type QuotaReservation struct {
    TxID      string
    ExpiresAt time.Time
}

type QuotaService interface {
    // 单维度预占。自开 WithTenantTx，单行原子 UPDATE + lazy init(meta.default_quota)。
    Try(ctx context.Context, req QuotaTryRequest) (QuotaReservation, error)

    // 多维度批量预占（创建 GPU 实例占 gpu_card+vcpu+memory）。
    // 单事务内循环 Try，任一维度不足/未注册则事务回滚，无悬挂预占。
    TryMany(ctx context.Context, reqs []QuotaTryRequest) ([]QuotaReservation, error)

    // 预占转实扣。reconciler 在 WithTenantTx 内传入 tx。
    // 调用方契约：传入的 ctx 必须带 TenantContext（用 WithTenantTx 开事务即自动注入），
    // 否则 SetDBTenant 不执行 → app.current_tenant_id 为空 → RLS self policy 拒绝所有行。
    // 幂等：WHERE state='reserved' RETURNING 空 = 已确认，返回 nil。
    Confirm(ctx context.Context, tx MetadataTx, txIDs []string, resourceRef string) error

    // 释放预占。API 层 Apply 失败 / reconciler 失败分支调用。
    // 调用方契约：同 Confirm，ctx 必须带 TenantContext。
    // 幂等：同 Confirm。
    Cancel(ctx context.Context, tx MetadataTx, txIDs []string) error

    // 释放已实扣配额。删除实例时调用。
    // 接收外部 tx，与 UpsertStatusTx + outbox 同事务原子。
    // 调用方契约：同 Confirm，ctx 必须带 TenantContext。
    // 幂等：WHERE state='confirmed' RETURNING 空 = 已释放，返回 nil。
    Release(ctx context.Context, tx MetadataTx, txIDs []string) error
}
```

**关键设计决策（grilling 确认）：**

- Try/TryMany **自开事务**（`WithTenantTx`），因为创建前无业务行可挂载
- Confirm/Cancel/Release **纯接收外部 tx**，不自己 Begin/Commit，事务由调用方（嘉明 reconciler / 删除流程）控制，保证与 `UpsertStatus` + outbox 同事务原子
- Confirm/Cancel/Release/GetTotalForUpdateTx 接收外部 tx，**调用方必须用 `WithTenantTx` 开事务**（自动注入 TenantContext + SetDBTenant），或手动 `Begin` + `SetDBTenant`。若传入的 ctx 无 TenantContext，`SetDBTenant` 会 panic（`types.FromContext`），RLS 也会拒绝所有行
- Try **不幂等**（按方案原文），幂等只在 Confirm/Cancel/Release 用状态机保证

### 3.2 QuotaStoreService（配置查询，pkg/ports/quota.go）

对齐 `core-quota-port-contract.md`（嘉明契约）：4 个方法，服务 BOSS/Console 运营配额管理 + GPU 预留校验锁行查询。调用方（嘉明 handler）在 Core 内部直接调 port，不经 REST。

```go
package ports

// QuotaView 是 QuotaStoreService 查询返回的配额视图（多维度 map）
type QuotaView struct {
    TenantID string
    Total    map[ResourceType]int64
    Used     map[ResourceType]int64
    Reserved map[ResourceType]int64
}

// QuotaPutRequest 是 Put 的请求（多维度 UPSERT total）
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

// QuotaStoreService 配额配置读写 + 查询 + 预留校验锁行查询。
// 调用方：嘉明 BOSS handler（Put/List）、Console handler（GetMy）、GPU 预留校验 handler（GetTotalForUpdateTx）。
// 与 QuotaService（扣减状态机）和 QuotaAdminService（租户生命周期 CRUD）分离。
type QuotaStoreService interface {
    // 设置租户配额 total（BOSS 平台角色）。UPSERT 语义：不存在则建行，存在则覆盖 total。
    // 幂等键防重复。不 clamp（BOSS 运营场景不涉及缩容策略，撞 CHECK 则报错由 handler 处理）。
    // 对应 PUT /api/v1/quotas（嘉明实现 handler，本任务只实现 port + adapter）
    Put(ctx context.Context, idempotencyKey string, req QuotaPutRequest) (QuotaView, error)

    // 列租户配额（BOSS）。分页 + 可选 tenant_id 过滤。
    // 对应 GET /api/v1/quotas（嘉明实现 handler）
    List(ctx context.Context, req QuotaListRequest) (QuotaListResult, error)

    // 查当前租户配额（Console 自查 / 规格可用性聚合）。
    // 对应 GET /api/v1/quotas/me（嘉明实现 handler）
    GetMy(ctx context.Context, tenantID string) (QuotaView, error)

    // 预留校验锁行查询（GPU 预留场景）。
    // 接外部 tx，SELECT total ... FOR UPDATE 锁住 resource_quota 行，串行化并发预留校验。
    // 只返回 total 数值，不做判断；判断逻辑（比对 gpu_slices 计数）由嘉明 handler 完成。
    // adapter 不读 gpu_slices 表。
    GetTotalForUpdateTx(ctx context.Context, tx MetadataTx, tenantID string, rt ResourceType) (int64, error)
}
```

**新增哨兵错误**（`pkg/ports/errors.go` 追加）：

```go
var ErrQuotaIdempotencyConflict = errors.New("quota idempotency key conflict")
```

**关键设计决策（grilling 确认）：**

- `Put` 是 **UPSERT 覆盖**语义（不存在建行，存在覆盖 total），与 `QuotaAdminService.CreateTenantQuota`（ON CONFLICT DO NOTHING 跳过）和 `UpdateTenantQuota`（GREATEST clamp）语义不同，三者走不同 port 不冲突
- `Put` **不 clamp**：BOSS 运营场景不涉及缩容策略，若 total < used+reserved 撞 `CHECK (reserved + used <= total)` 约束则报错，由 handler 返回错误
- `GetTotalForUpdateTx` **接外部 tx + FOR UPDATE**：与 `QuotaService.Confirm/Cancel/Release` 同为接外部 tx 模式，但只查不改，不触碰流水状态机
- `QuotaStoreService` 的 4 个方法对应的 REST 端点（`/quotas`、`/quotas/me`）handler **由嘉明实现**，本任务只实现 port + adapter

### 3.3 QuotaAdminService（租户生命周期管理，pkg/ports/quota\_admin.go）

对齐 `core-quota-api.md` 实际需求：5 个端点（POST 批量新建 / PUT 批量改上限 / GET 查询 / DELETE 整租户删除 / GET 配额元数据），前 4 个路径统一在 `/admin/tenants/{tenant_id}/quota`，配额元数据查询走 `/admin/quota-meta`，由平台管理员（租户管理 service 经 SDK）调用。

```go
package ports

import (
    "context"
    "time"
)

// QuotaAdminService 管理租户配额上限与查询用量。
// 与 QuotaService（扣减）分离：扣减改 reserved/used + 流水状态机；管理只改 total + 查询，不触碰流水状态机。
// 两者操作同一组表，但调用方、事务模型不同。
type QuotaAdminService interface {
    // 批量新建配额：为指定租户初始化多个资源维度配额行（used/reserved 初始为 0）。
    // items.resource_type 必须在 resource_quota_meta 已注册且 enabled=true；
    // items.total 未提供（<=0 哨兵）时取 resource_quota_meta.default_quota。
    // 已存在的维度跳过（QUOTA_ALREADY_EXISTS 不阻断其余维度创建）。
    // 对应 POST /api/v1/admin/tenants/{tenant_id}/quota
    CreateTenantQuota(ctx context.Context, tenantID string, items []QuotaItemInput) ([]QuotaInfo, error)

    // 批量修改配额上限：只改 total，不影响 used/reserved。
    // 允许 total < used（缩容，已有资源继续运行，仅阻止后续新建 Try → ErrQuotaExceeded）。
    // 维度行不存在返回 ErrQuotaNotFound（需先调 CreateTenantQuota）。
    // 对应 PUT /api/v1/admin/tenants/{tenant_id}/quota
    UpdateTenantQuota(ctx context.Context, tenantID string, items []QuotaItemUpdate) ([]QuotaInfo, error)

    // 查询租户配额：列出该租户所有维度的 total/used/reserved + unit/display_name（JOIN meta）。
    // 对应 GET /api/v1/admin/tenants/{tenant_id}/quota
    GetTenantQuota(ctx context.Context, tenantID string) ([]QuotaInfo, error)

    // 删除租户所有配额：删除 resource_quota 该租户全部行 + resource_reservations 该租户全部流水。
    // 用于租户禁用/资源清理场景。由调用方保证此时无在用资源（建议先确认 used==0，但本方法不强制守卫）。
    // 对应 DELETE /api/v1/admin/tenants/{tenant_id}/quota
    DeleteTenantQuota(ctx context.Context, tenantID string) error

    // 查询可用配额元数据：列出 resource_quota_meta 中 enabled=true 的所有维度。
    // 用于创建租户/套餐时展示可选项（前端据此渲染配额维度表单）。
    // 对应 GET /api/v1/admin/quota-meta
    ListQuotaMeta(ctx context.Context) ([]QuotaMeta, error)
}

// QuotaItemInput 用于新建，total 可省略（0 表示取 default_quota）
type QuotaItemInput struct {
    ResourceType ResourceType
    Total        int64 // <=0 表示未提供，取 resource_quota_meta.default_quota
}

// QuotaItemUpdate 用于修改，total 必填
type QuotaItemUpdate struct {
    ResourceType ResourceType
    Total       int64
}

// QuotaMeta 是 resource_quota_meta 的只读视图（ListQuotaMeta 返回）
type QuotaMeta struct {
    ResourceType  ResourceType
    DisplayName   string
    Unit          string
    DefaultQuota  int64
    IsDiscrete    bool // 是否离散计数（true=整数，false=允许小数），用于校验新增/修改 total 的值类型
}

type QuotaInfo struct {
    TenantID     string
    ResourceType  ResourceType
    Total        int64
    Reserved     int64
    Used         int64
    Tightened    bool      // PUT 缩容自动收紧标记：请求 total < used+reserved 时收紧为 used+reserved，置 true（对齐李宇 API §2 Response）。GET 响应里为零值 false；handler 可选择保留或用 omitempty 省略（李宇 API §3 GET 响应未定义此字段，带不带均不影响契约）
    Unit         string    // 来自 resource_quota_meta（GET 时 JOIN 返回）
    DisplayName  string    // 来自 resource_quota_meta（GET 时 JOIN 返回）
    IsDiscrete   bool      // 来自 resource_quota_meta（GET 时 JOIN 返回，用于校验 total 值类型）
    UpdatedAt    time.Time
}
```

**新增哨兵错误**（`pkg/ports/errors.go` 追加）：

```go
var ErrQuotaNotFound          = errors.New("quota not found")
var ErrQuotaAlreadyExists     = errors.New("quota already exists")
// 注：ErrTenantNotFound 复用 errors.go 已有定义（ports.ErrTenantNotFound），不新增
```

**设计决策（对齐 core-quota-api.md）：**

- 路径统一在 `/admin/tenants/{tenant_id}/quota`，不再分租户自查 / 平台管理两套端点；全部由平台管理员（租户管理 service）调用
- 缩容策略（李宇确认）：若请求的 `total < 当前 used+reserved`，则将 `total` 赋值为 `used+reserved`（即缩容下限是当前已用量，不会低于已用量），不报错。实现用 `GREATEST($3, reserved + used)` 在 SQL 层 clamp，保证不违反 CHECK 约束
- DELETE 不做 `reserved+used==0` 守卫（core-quota-api.md §4 语义：租户禁用场景清理配额，由调用方保证无在用资源）
- `resource_quota_meta` 仍只读（校验 resource\_type 已注册 + 读 default\_quota 兜底），无 meta 写入

### 3.4 为什么分三个 port

| 维度        | QuotaService（扣减）                                    | QuotaStoreService（配置查询）                               | QuotaAdminService（租户生命周期管理）                               |
| --------- | --------------------------------------------------- | ----------------------------------------------------- | --------------------------------------------------------- |
| 操作        | reserved/used + resource\_reservations 流水状态机        | resource\_quota.total UPSERT + 查询 + FOR UPDATE 锁行     | resource\_quota.total 批量增改查删 + resource\_quota\_meta 只读校验 |
| 调用方       | demo\_instances、嘉明 reconciler                       | 嘉明 BOSS/Console handler、GPU 预留校验 handler              | 李宇 tenant-service 经 Core SDK 调 REST → handler             |
| 鉴权 scope  | tenant（Try/TryMany）+ 无（Confirm/Cancel/Release 内部调用） | 嘉明 handler 自行控制（BOSS=quota:write/read，Console=tenant） | platform/admin（`/admin/tenants/{id}/quota`）               |
| 事务模型      | Try 自开 WithTenantTx；Confirm/Cancel/Release 接外部 tx   | Put 自开 WithPlatformTx；GetTotalForUpdateTx 接外部 tx      | 全部用 WithPlatformTx                                        |
| API 暴露    | 不直接暴露                                               | `/quotas`、`/quotas/me`（嘉明实现 handler）                  | `/admin/tenants/{id}/quota` 4 端点 + `/admin/quota-meta` 1 端点（本任务实现 handler） |
| UPSERT 语义 | N/A                                                 | Put = 覆盖（不 clamp，撞 CHECK 报错）                          | Create = 跳过已存在；Update = GREATEST clamp                    |

三者调用方、操作语义、事务模型、UPSERT 策略都不同，放一个 interface 会臃肿并违反接口隔离；但三者操作同一组表，作为**一个任务**交付，一个 adapter struct 实现三个 interface，共享表结构和测试基础设施。

***

## 4. Adapter 实现 - 扣减（pkg/adapters/quota/postgres\_quota.go）

### 4.1 结构体

`PostgresQuotaService` 是 `QuotaService` 接口的 PG 实现，持有 `MetadataStore` 用于 Try/TryMany 自开事务：

```go
package quota

import (
    "github.com/kubercloud/ani/pkg/ports"
)

// PostgresQuota 一个 struct 实现三个 interface：
// - ports.QuotaService（扣减：Try/TryMany/Confirm/Cancel/Release）
// - ports.QuotaStoreService（配置查询：Put/List/GetMy/GetTotalForUpdateTx）
// - ports.QuotaAdminService（租户生命周期管理：Create/Update/Get/Delete）
type PostgresQuota struct {
    store ports.MetadataStore
}

func NewPostgresQuota(store ports.MetadataStore) *PostgresQuota {
    return &PostgresQuota{store: store}
}

// 编译期接口断言（三个）
var _ ports.QuotaService        = (*PostgresQuota)(nil)
var _ ports.QuotaStoreService   = (*PostgresQuota)(nil)
var _ ports.QuotaAdminService   = (*PostgresQuota)(nil)
```

> **注意**：后续代码中 `PostgresQuotaService` 的方法接收者统一改为 `*PostgresQuota`。方法签名不变，只是 struct 改名。

### 4.2 tryInTx（Try/TryMany 共用的内部方法）

提取为内部方法，TryMany 在同一事务里循环调用。返回 `(QuotaReservation, error)` 符合 Go 多返回值惯例：

```go
// tryInTx 在给定 tx 上执行单维度预占，返回 QuotaReservation。
// Try 和 TryMany 共用此方法，区别在于是否在同一事务内循环。
func (q *PostgresQuota) tryInTx(ctx context.Context, tx ports.MetadataTx, req ports.QuotaTryRequest) (ports.QuotaReservation, error) {
    var res ports.QuotaReservation

    // 1. 校验资源已注册且 enabled
    var enabled bool
    var defaultQuota int64
    err := tx.QueryRow(ctx, `
        SELECT enabled, default_quota FROM resource_quota_meta
        WHERE resource_type = $1
    `, req.ResourceType).Scan(&enabled, &defaultQuota)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return res, ports.ErrQuotaResourceNotRegistered
        }
        return res, err  // 网络错误、连接断开等透传，不掩盖
    }
    if !enabled {
        return res, ports.ErrQuotaResourceNotRegistered
    }

    // 2. lazy init：无配置行则用 default_quota 建行（并发首次 ON CONFLICT）
    if _, err := tx.Exec(ctx, `
        INSERT INTO resource_quota (tenant_id, resource_type, total)
        VALUES ($1, $2, $3)
        ON CONFLICT (tenant_id, resource_type) DO NOTHING
    `, req.TenantID, req.ResourceType, defaultQuota); err != nil {
        return res, err
    }

    // 3. 单行原子预占：WHERE 校验余量，行锁串行化并发
    tag, err := tx.Exec(ctx, `
        UPDATE resource_quota
        SET reserved = reserved + $1, updated_at = NOW()
        WHERE tenant_id = $2 AND resource_type = $3
          AND reserved + used + $1 <= total
    `, req.Amount, req.TenantID, req.ResourceType)
    if err != nil {
        return res, err
    }
    if tag.RowsAffected() == 0 {
        return res, ports.ErrQuotaExceeded  // 余量不足
    }

    // 4. 插入预占流水，拿回 tx_id 和 expires_at
    err = tx.QueryRow(ctx, `
        INSERT INTO resource_reservations (tenant_id, resource_type, amount, expires_at)
        VALUES ($1, $2, $3, NOW() + INTERVAL '10 minutes')
        RETURNING tx_id::text, expires_at
    `, req.TenantID, req.ResourceType, req.Amount).Scan(&res.TxID, &res.ExpiresAt)
    return res, err
}
```

**并发不超卖证明**：两个 Try 并发，PG 行锁让两条 `UPDATE resource_quota` 串行；第二条执行时 `reserved + used + amount > total` → `RowsAffected=0` → `ErrQuotaExceeded`。

### 4.3 Try（单维度预占，自开事务）

```go
// Try 单维度预占。自开 WithTenantTx，单行原子 UPDATE + lazy init。
func (q *PostgresQuota) Try(ctx context.Context, req ports.QuotaTryRequest) (ports.QuotaReservation, error) {
    var res ports.QuotaReservation
    err := q.store.WithTenantTx(ctx, req.TenantID, func(ctx context.Context, tx ports.MetadataTx) error {
        var err error
        res, err = q.tryInTx(ctx, tx, req)  // 直接赋值给外层 res
        return err
    })
    if err != nil {
        return ports.QuotaReservation{}, err
    }
    return res, nil
}
```

### 4.4 TryMany（多维度批量预占，单事务原子）

```go
// TryMany 多维度批量预占。单事务内循环 tryInTx，任一失败则整体回滚，无悬挂预占。
func (q *PostgresQuota) TryMany(ctx context.Context, reqs []ports.QuotaTryRequest) ([]ports.QuotaReservation, error) {
    if len(reqs) == 0 {
        return nil, nil
    }
    tenantID := reqs[0].TenantID

    var reservations []ports.QuotaReservation
    err := q.store.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx ports.MetadataTx) error {
        for _, req := range reqs {
            // 校验所有请求的 tenant_id 一致（多维度预占同一租户）
            if req.TenantID != tenantID {
                return fmt.Errorf("quota TryMany: all requests must have same tenant_id, got %s and %s", tenantID, req.TenantID)
            }
            res, err := q.tryInTx(ctx, tx, req)  // 直接拿返回值
            if err != nil {
                return err  // 任一失败 → return err → 整个事务回滚 → 已成功的预占一并回滚
            }
            reservations = append(reservations, res)
        }
        return nil
    })
    if err != nil {
        return nil, err
    }
    return reservations, nil
}
```

**原子性证明**：`WithTenantTx` 是一个事务，for 循环里任一 `tryInTx` 返回 error，整个 func 返回 error，事务回滚，之前循环里成功的 `UPDATE resource_quota` + `INSERT resource_reservations` 全部回滚，无悬挂预占。

### 4.5 Confirm（预占转实扣，接收外部 tx，幂等）

```go
// Confirm 预占转实扣。不自己开事务，用调用方传入的 tx。
// 幂等：WHERE state='reserved' RETURNING 空 = 已确认，返回 nil。
func (q *PostgresQuota) Confirm(ctx context.Context, tx ports.MetadataTx, txIDs []string, resourceRef string) error {
    for _, txID := range txIDs {
        // 1. 流水 reserved → confirmed，WHERE state='reserved' 守卫幂等
        var state string
        err := tx.QueryRow(ctx, `
            UPDATE resource_reservations
            SET state = 'confirmed', resource_ref = $2, updated_at = NOW()
            WHERE tx_id = $1 AND state = 'reserved'
            RETURNING state
        `, txID, resourceRef).Scan(&state)
        if errors.Is(err, pgx.ErrNoRows) {
            slog.Warn("quota Confirm: reservation not in reserved state, skipping",
                "tx_id", txID, "resource_ref", resourceRef,
                "reason", "already confirmed/cancelled or not found")
            continue  // 已 confirmed/cancelled → 幂等成功，跳过
        }
        if err != nil {
            return err
        }

        // 2. reserved → used 转账（同一事务，只对刚 confirmed 的行）
        if _, err := tx.Exec(ctx, `
            UPDATE resource_quota q
            SET reserved = reserved - r.amount,
                used = used + r.amount,
                updated_at = NOW()
            FROM resource_reservations r
            WHERE r.tx_id = $1 AND r.state = 'confirmed'
              AND q.tenant_id = r.tenant_id AND q.resource_type = r.resource_type
        `, txID); err != nil {
            return err
        }
    }
    return nil
}
```

**幂等证明**：NATS 重投或 reconciler 重入，重复 Confirm 同一个 txID。第二次执行时 `WHERE state='reserved'` 不匹配（已是 `confirmed`），返回 `pgx.ErrNoRows` → `continue` 跳过。转账的 `UPDATE resource_quota` 只在第一次 Confirm 把 state 从 reserved 改成 confirmed 时触发，第二次 state 已是 confirmed，第一步不匹配直接跳过，不会执行到第二步，所以 `used` 不重复加。

### 4.6 Cancel（释放预占，接收外部 tx，幂等）

```go
// Cancel 释放预占。不自己开事务，用调用方传入的 tx。
// 幂等：同 Confirm。
func (q *PostgresQuota) Cancel(ctx context.Context, tx ports.MetadataTx, txIDs []string) error {
    for _, txID := range txIDs {
        // 1. 流水 reserved → cancelled，WHERE state='reserved' 守卫幂等
        var state string
        err := tx.QueryRow(ctx, `
            UPDATE resource_reservations
            SET state = 'cancelled', updated_at = NOW()
            WHERE tx_id = $1 AND state = 'reserved'
            RETURNING state
        `, txID).Scan(&state)
        if errors.Is(err, pgx.ErrNoRows) {
            slog.Warn("quota Cancel: reservation not in reserved state, skipping",
                "tx_id", txID,
                "reason", "already cancelled/confirmed or not found")
            continue  // 已 cancelled/confirmed → 幂等跳过
        }
        if err != nil {
            return err
        }

        // 2. 释放 reserved（同一事务，只对刚 cancelled 的行）
        if _, err := tx.Exec(ctx, `
            UPDATE resource_quota q
            SET reserved = reserved - r.amount,
                updated_at = NOW()
            FROM resource_reservations r
            WHERE r.tx_id = $1 AND r.state = 'cancelled'
              AND q.tenant_id = r.tenant_id AND q.resource_type = r.resource_type
        `, txID); err != nil {
            return err
        }
    }
    return nil
}
```

**幂等证明**：同 Confirm，重复 Cancel 时 `WHERE state='reserved'` 不匹配（已是 `cancelled`），返回 ErrNoRows → continue，不会重复释放 reserved。

### 4.7 Release（释放已实扣，接收外部 tx，幂等）

```go
// Release 释放已实扣配额。不自己开事务，用调用方传入的 tx。
// 幂等：WHERE state='confirmed' RETURNING 空 = 已释放，返回 nil。
func (q *PostgresQuota) Release(ctx context.Context, tx ports.MetadataTx, txIDs []string) error {
    for _, txID := range txIDs {
        // 1. 流水 confirmed → released，WHERE state='confirmed' 守卫幂等
        var state string
        err := tx.QueryRow(ctx, `
            UPDATE resource_reservations
            SET state = 'released', updated_at = NOW()
            WHERE tx_id = $1 AND state = 'confirmed'
            RETURNING state
        `, txID).Scan(&state)
        if errors.Is(err, pgx.ErrNoRows) {
            slog.Warn("quota Release: reservation not in confirmed state, skipping",
                "tx_id", txID,
                "reason", "already released/cancelled or not found")
            continue  // 已 released/cancelled → 幂等跳过
        }
        if err != nil {
            return err
        }

        // 2. used 减回（同一事务，只对刚 released 的行）
        if _, err := tx.Exec(ctx, `
            UPDATE resource_quota q
            SET used = used - r.amount,
                updated_at = NOW()
            FROM resource_reservations r
            WHERE r.tx_id = $1 AND r.state = 'released'
              AND q.tenant_id = r.tenant_id AND q.resource_type = r.resource_type
        `, txID); err != nil {
            return err
        }
    }
    return nil
}
```

**幂等证明**：同 Confirm 对称。重复 Release 同一个 txID，第二次执行时 `WHERE state='confirmed'` 不匹配（已是 `released`），返回 `pgx.ErrNoRows` → continue。`used` 减回的 `UPDATE resource_quota` 只在第一次 Release 把 state 从 confirmed 改成 released 时触发，第二次直接跳过第一步，不会执行到第二步，所以 `used` 不重复减。

**与 Cancel 的区别**：Cancel 释放预占（`reserved -= amount`，`state=reserved → cancelled`）；Release 释放实扣（`used -= amount`，`state=confirmed → released`）。删除实例时实例通常已 Running（Confirm 已执行、used 已增加），Cancel 的 `WHERE state='reserved'` 守卫会直接跳过、无法释放 used，必须用 Release。

### 4.8 扣减事务模型对比

| 方法      | 事务来源                  | 原因                                               |
| ------- | --------------------- | ------------------------------------------------ |
| Try     | **自开** `WithTenantTx` | 创建前无业务行，自己开事务做预占                                 |
| TryMany | **自开** `WithTenantTx` | 同上，多维度在同一事务内循环                                   |
| Confirm | **接收外部 tx**           | 与 `UpsertStatus` + outbox 同事务原子                  |
| Cancel  | **接收外部 tx**           | 与 `DELETE instance` + outbox 同事务原子               |
| Release | **接收外部 tx**           | 与 `UpsertStatusTx`(state=deleted) + outbox 同事务原子 |

### 4.9 流水状态机

```
reserved → confirmed     (Confirm，reconciler 发现 Running)
reserved → cancelled     (Cancel，Apply 失败 / 异步失败)
reserved → expired        (TTL worker 回收孤儿预占)
confirmed → released     (Release，删除实例)
```

`cancelled`、`expired`、`released` 均为终态，不再流转。

***

## 5. Adapter 实现 - QuotaStoreService（配置查询）

> §4 的 `PostgresQuota` struct 同时实现 `QuotaStoreService` interface。以下方法接收者均为 `*PostgresQuota`。

### 5.1 Put（UPSERT total，自开 WithPlatformTx）

BOSS 平台设置租户配额上限。UPSERT 语义：不存在建行，存在覆盖 total。不 clamp（BOSS 运营场景不涉及缩容策略，撞 CHECK 则报错）。幂等键防重复。

```go
func (q *PostgresQuota) Put(ctx context.Context, idempotencyKey string, req ports.QuotaPutRequest) (ports.QuotaView, error) {
    var view ports.QuotaView
    view.TenantID = req.TenantID
    view.Total = make(map[ports.ResourceType]int64)
    view.Used = make(map[ports.ResourceType]int64)
    view.Reserved = make(map[ports.ResourceType]int64)

    err := q.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
        // Put 是 UPSERT 覆盖语义，对相同输入重复调用结果天然幂等。
        // IdempotencyKey 字段已定义在 QuotaPutRequest 中，adapter 层不做去重表（避免引入
        // idempotency_keys 表的额外复杂度）。幂等防重由调用方在 HTTP 层处理。

        for rt, total := range req.Total {
            // 校验资源类型已注册且 enabled
            var enabled bool
            err := tx.QueryRow(ctx, `
                SELECT enabled FROM resource_quota_meta WHERE resource_type = $1
            `, rt).Scan(&enabled)
            if err != nil {
                if errors.Is(err, pgx.ErrNoRows) {
                    return ports.ErrQuotaResourceNotRegistered
                }
                return err
            }
            if !enabled {
                return ports.ErrQuotaResourceNotRegistered
            }

            // UPSERT total，不 clamp（直接写，撞 CHECK 则报错）
            if _, err := tx.Exec(ctx, `
                INSERT INTO resource_quota (tenant_id, resource_type, total, reserved, used)
                VALUES ($1, $2, $3, 0, 0)
                ON CONFLICT (tenant_id, resource_type)
                DO UPDATE SET total = EXCLUDED.total, updated_at = NOW()
            `, req.TenantID, rt, total); err != nil {
                return err
            }
        }

        // 回读所有维度
        rows, err := tx.Query(ctx, `
            SELECT resource_type, total, reserved, used
            FROM resource_quota
            WHERE tenant_id = $1
        `, req.TenantID)
        if err != nil {
            return err
        }
        defer rows.Close()
        for rows.Next() {
            var rt string
            var total, reserved, used int64
            if err := rows.Scan(&rt, &total, &reserved, &used); err != nil {
                return err
            }
            view.Total[ports.ResourceType(rt)] = total
            view.Used[ports.ResourceType(rt)] = used
            view.Reserved[ports.ResourceType(rt)] = reserved
        }
        return rows.Err()
    })
    if err != nil {
        return ports.QuotaView{}, err
    }
    return view, nil
}
```

**说明**：`Put` 不做 `GREATEST` clamp。若 BOSS 运营把 total 改到低于 used+reserved，`ON CONFLICT DO UPDATE` 会撞 `CHECK (reserved + used <= total)` 约束，PG 返回 check violation 错误，adapter 透传，由嘉明 handler 映射为 HTTP 错误。这是 BOSS 场景的预期行为（运营失误应报错，不是悄悄 clamp）。

### 5.2 List（分页查，自开 WithPlatformTx）

按租户级分页：第一步查一页租户列表（keyset 分页，cursor = tenant_id），第二步查这些租户的全部维度填充 QuotaView。指定 tenant_id 时直接调 GetMy 不分页。

```go
func (q *PostgresQuota) List(ctx context.Context, req ports.QuotaListRequest) (ports.QuotaListResult, error) {
    var result ports.QuotaListResult

    // 指定了 tenant_id 就不分页，直接返回该租户全部维度
    if req.TenantID != "" {
        view, err := q.GetMy(ctx, req.TenantID)
        if err != nil {
            return result, err
        }
        result.Items = []ports.QuotaView{view}
        result.Total = 1
        return result, nil
    }

    // 无 tenant_id：按租户级 keyset 分页
    err := q.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
        limit := req.Limit
        if limit <= 0 || limit > 100 {
            limit = 50
        }

        // 第一步：查一页租户列表（DISTINCT tenant_id，keyset 分页，cursor = 上页最后一个 tenant_id）
        // 用原生 UUID 比较（$1::uuid），走 UUID 索引；::text 强转会破坏索引
        tenantRows, err := tx.Query(ctx, `
            SELECT DISTINCT tenant_id::text
            FROM resource_quota
            WHERE ($1 = '' OR tenant_id > $1::uuid)
            ORDER BY tenant_id
            LIMIT $2
        `, req.Cursor, limit+1)
        if err != nil {
            return err
        }

        var tenantIDs []string
        for tenantRows.Next() {
            var tid string
            if err := tenantRows.Scan(&tid); err != nil {
                tenantRows.Close()
                return err
            }
            tenantIDs = append(tenantIDs, tid)
        }
        if err := tenantRows.Err(); err != nil {
            tenantRows.Close()
            return err
        }
        tenantRows.Close()

        if len(tenantIDs) == 0 {
            return nil // 无数据
        }

        // 判断是否还有下一页
        hasMore := len(tenantIDs) > limit
        if hasMore {
            tenantIDs = tenantIDs[:limit] // 去掉多查的那一个
        }

        // 第二步：查这些租户的全部配额维度
        rows, err := tx.Query(ctx, `
            SELECT tenant_id::text, resource_type, total, reserved, used
            FROM resource_quota
            WHERE tenant_id::text = ANY($1)
            ORDER BY tenant_id::text, resource_type
        `, tenantIDs)
        if err != nil {
            return err
        }
        defer rows.Close()

        viewsByTenant := make(map[string]*ports.QuotaView)
        var order []string
        for rows.Next() {
            var tenantID, rt string
            var total, reserved, used int64
            if err := rows.Scan(&tenantID, &rt, &total, &reserved, &used); err != nil {
                return err
            }
            v, ok := viewsByTenant[tenantID]
            if !ok {
                v = &ports.QuotaView{
                    TenantID: tenantID,
                    Total:    make(map[ports.ResourceType]int64),
                    Used:     make(map[ports.ResourceType]int64),
                    Reserved: make(map[ports.ResourceType]int64),
                }
                viewsByTenant[tenantID] = v
                order = append(order, tenantID)
            }
            v.Total[ports.ResourceType(rt)] = total
            v.Used[ports.ResourceType(rt)] = used
            v.Reserved[ports.ResourceType(rt)] = reserved
        }
        if err := rows.Err(); err != nil {
            return err
        }

        for _, tid := range order {
            result.Items = append(result.Items, *viewsByTenant[tid])
        }
        result.Total = len(result.Items)

        // cursor = 上一页最后一个 tenant_id（纯字符串）
        if hasMore {
            result.NextCursor = tenantIDs[len(tenantIDs)-1]
        }
        return nil
    })
    return result, err
}
```

### 5.3 GetMy（自查，用 WithTenantTx）

Console 自查 / 规格可用性聚合。走 `WithTenantTx`，RLS 自动过滤只看本租户。

```go
func (q *PostgresQuota) GetMy(ctx context.Context, tenantID string) (ports.QuotaView, error) {
    var view ports.QuotaView
    view.TenantID = tenantID
    view.Total = make(map[ports.ResourceType]int64)
    view.Used = make(map[ports.ResourceType]int64)
    view.Reserved = make(map[ports.ResourceType]int64)

    err := q.store.WithTenantTx(ctx, tenantID, func(ctx context.Context, tx ports.MetadataTx) error {
        rows, err := tx.Query(ctx, `
            SELECT resource_type, total, reserved, used
            FROM resource_quota
            WHERE tenant_id = $1
        `, tenantID)
        if err != nil {
            return err
        }
        defer rows.Close()
        for rows.Next() {
            var rt string
            var total, reserved, used int64
            if err := rows.Scan(&rt, &total, &reserved, &used); err != nil {
                return err
            }
            view.Total[ports.ResourceType(rt)] = total
            view.Used[ports.ResourceType(rt)] = used
            view.Reserved[ports.ResourceType(rt)] = reserved
        }
        return rows.Err()
    })
    if err != nil {
        return ports.QuotaView{}, err
    }
    return view, nil
}
```

### 5.4 GetTotalForUpdateTx（锁行查 total，接外部 tx + FOR UPDATE）

GPU 预留校验专用。接外部 tx，`SELECT total ... FOR UPDATE` 锁住 resource\_quota 行，串行化并发预留。只返回 total，不做判断。

**调用方契约**：与 Confirm/Cancel/Release 相同，传入的 ctx 必须带 TenantContext（用 `WithTenantTx` 开事务即自动注入）。

```go
func (q *PostgresQuota) GetTotalForUpdateTx(ctx context.Context, tx ports.MetadataTx, tenantID string, rt ports.ResourceType) (int64, error) {
    var total int64
    err := tx.QueryRow(ctx, `
        SELECT total FROM resource_quota
        WHERE tenant_id = $1 AND resource_type = $2
        FOR UPDATE
    `, tenantID, rt).Scan(&total)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return 0, ports.ErrQuotaNotFound
        }
        return 0, err
    }
    return total, nil
}
```

**并发安全证明**：两个并发预留事务甲/乙都先调 `GetTotalForUpdateTx` 获取 `resource_quota` 行锁；甲锁住→查 gpu\_slices count→通过→写切片→提交释放锁；乙获得锁→count 已增加→超限→409。`FOR UPDATE` 锁住 resource\_quota 行，即使实际写的是 gpu\_slices，所有并发预留都必须先获取这个锁，串行化校验-写入序列。

**职责边界**：本方法只锁行查 total，不读 gpu\_slices 表，不做判断。查 gpu\_slices count + 比对判断 + 写切片由嘉明 handler 完成（见 §3.2 调用方场景）。

### 5.5 QuotaStoreService 事务模型对比

| 方法                  | 事务来源                    | 原因                                      |
| ------------------- | ----------------------- | --------------------------------------- |
| Put                 | **自开** `WithPlatformTx` | BOSS 运营操作，平台级，不属某租户                     |
| List                | **自开** `WithPlatformTx` | BOSS 跨租户查询                              |
| GetMy               | **自开** `WithTenantTx`   | Console 自查，RLS 自动过滤                     |
| GetTotalForUpdateTx | **接收外部 tx**             | 与嘉明 handler 的 gpu\_slices 查询 + 写切片同事务原子 |

***

## 6. Adapter 实现 - QuotaAdminService（租户生命周期管理）

> §4 的 `PostgresQuota` struct 同时实现 `QuotaAdminService` interface。以下方法接收者均为 `*PostgresQuota`。

### 6.1 CreateTenantQuota（批量新建，POST）

为指定租户初始化多个资源维度配额行。`total` 未提供时取 `resource_quota_meta.default_quota`；已存在的维度跳过（不阻断其余维度）。

```go
func (q *PostgresQuota) CreateTenantQuota(ctx context.Context, tenantID string, items []ports.QuotaItemInput) ([]ports.QuotaInfo, error) {
    var result []ports.QuotaInfo
    err := q.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
        // 校验租户存在（tenants 表，无 RLS 或由 WithPlatformTx 绕过）
        var exists bool
        if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenants WHERE id = $1)`, tenantID).Scan(&exists); err != nil {
            return err
        }
        if !exists {
            return ports.ErrTenantNotFound
        }

        // 逐维度处理：校验 meta 注册 → 取 default_quota 兜底 → INSERT ... ON CONFLICT DO NOTHING
        for _, it := range items {
            var enabled bool
            var defaultQuota int64
            err := tx.QueryRow(ctx, `
                SELECT enabled, default_quota FROM resource_quota_meta
                WHERE resource_type = $1
            `, it.ResourceType).Scan(&enabled, &defaultQuota)
            if err != nil {
                if errors.Is(err, pgx.ErrNoRows) {
                    return ports.ErrQuotaResourceNotRegistered
                }
                return err
            }
            if !enabled {
                return ports.ErrQuotaResourceNotRegistered
            }
            total := it.Total
            if total <= 0 {
                total = defaultQuota
            }

            // ON CONFLICT DO NOTHING：已存在的维度跳过，不报错、不覆盖
            if _, err := tx.Exec(ctx, `
                INSERT INTO resource_quota (tenant_id, resource_type, total, reserved, used)
                VALUES ($1, $2, $3, 0, 0)
                ON CONFLICT (tenant_id, resource_type) DO NOTHING
            `, tenantID, it.ResourceType, total); err != nil {
                return err
            }
        }

        // 回读本次 items 涉及的维度（不含该租户之前已有的其他维度）
        resourceTypes := make([]string, len(items))
        for i, it := range items {
            resourceTypes[i] = string(it.ResourceType)
        }
        rows, err := tx.Query(ctx, `
            SELECT tenant_id::text, resource_type, total, reserved, used, updated_at
            FROM resource_quota
            WHERE tenant_id = $1 AND resource_type = ANY($2)
            ORDER BY resource_type
        `, tenantID, resourceTypes)
        if err != nil {
            return err
        }
        defer rows.Close()
        for rows.Next() {
            var qi ports.QuotaInfo
            if err := rows.Scan(&qi.TenantID, &qi.ResourceType, &qi.Total, &qi.Reserved, &qi.Used, &qi.UpdatedAt); err != nil {
                return err
            }
            result = append(result, qi)
        }
        return rows.Err()
    })
    if err != nil {
        return nil, err
    }
    return result, nil
}
```

**幂等说明**：`ON CONFLICT DO NOTHING` 保证重复 POST 同一租户同一维度不会报错、不覆盖已有 total；调用方可通过返回结果对比判断哪些是新建、哪些已存在（HTTP 层若需 `QUOTA_ALREADY_EXISTS` 409，可在 handler 检查返回行数与请求 items 是否一致来决定是否带该码）。

### 6.2 UpdateTenantQuota（批量改上限，PUT）

只改 `total`，不影响 `used/reserved`。缩容策略（李宇确认）：若请求的 `total < 当前 used+reserved`，则将 `total` 赋值为 `used+reserved`（即缩容下限是当前已用量，不会低于已用量），不报错。维度行不存在返回 `ErrQuotaNotFound`。

```go
func (q *PostgresQuota) UpdateTenantQuota(ctx context.Context, tenantID string, items []ports.QuotaItemUpdate) ([]ports.QuotaInfo, error) {
    var result []ports.QuotaInfo
    err := q.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
        // 记录每个维度请求的 total，用于回读时计算 tightened 标记
        reqTotals := make(map[ports.ResourceType]int64, len(items))
        for _, it := range items {
            reqTotals[it.ResourceType] = it.Total
        }

        for _, it := range items {
            // 校验资源类型已注册且 enabled
            var enabled bool
            err := tx.QueryRow(ctx, `
                SELECT enabled FROM resource_quota_meta WHERE resource_type = $1
            `, it.ResourceType).Scan(&enabled)
            if err != nil {
                return ports.ErrQuotaResourceNotRegistered
            }
            if !enabled {
                return ports.ErrQuotaResourceNotRegistered
            }

            // 缩容 clamp：若请求的 total < 当前 used+reserved，则取 used+reserved
            // 利用 GREATEST 在 SQL 层取较大值，保证 total >= used+reserved，不违反 CHECK 约束
            tag, err := tx.Exec(ctx, `
                UPDATE resource_quota
                SET total = GREATEST($3, reserved + used), updated_at = NOW()
                WHERE tenant_id = $1 AND resource_type = $2
            `, tenantID, it.ResourceType, it.Total)
            if err != nil {
                return err
            }
            if tag.RowsAffected() == 0 {
                return ports.ErrQuotaNotFound  // 维度行不存在，需先调 CreateTenantQuota
            }
        }

        // 回读本次 items 涉及的维度（计算 tightened 标记）
        resourceTypes := make([]string, len(items))
        for i, it := range items {
            resourceTypes[i] = string(it.ResourceType)
        }
        rows, err := tx.Query(ctx, `
            SELECT tenant_id::text, resource_type, total, reserved, used, updated_at
            FROM resource_quota
            WHERE tenant_id = $1 AND resource_type = ANY($2)
            ORDER BY resource_type
        `, tenantID, resourceTypes)
        if err != nil {
            return err
        }
        defer rows.Close()
        for rows.Next() {
            var qi ports.QuotaInfo
            if err := rows.Scan(&qi.TenantID, &qi.ResourceType, &qi.Total, &qi.Reserved, &qi.Used, &qi.UpdatedAt); err != nil {
                return err
            }
            // tightened：回读的 total 大于请求的 total，说明被 GREATEST 收紧了
            if reqTotal, ok := reqTotals[qi.ResourceType]; ok && qi.Total > reqTotal {
                qi.Tightened = true
            }
            result = append(result, qi)
        }
        return rows.Err()
    })
    if err != nil {
        return nil, err
    }
    return result, nil
}
```

**缩容说明（李宇确认）**：`GREATEST($3, reserved + used)` 在 SQL 层 clamp，保证 `total >= used+reserved`，不会违反 `CHECK (reserved + used <= total)` 约束。即缩容下限为当前已用量，已有资源继续运行，仅后续 `Try` 因 `reserved + used + amount > total` 返回 `ErrQuotaExceeded` 阻止新建。**无需修改 CHECK 约束**。

### 6.3 GetTenantQuota（查询，GET）

查询指定租户所有维度配额，JOIN `resource_quota_meta` 获取 `unit`/`display_name`/`is_discrete`。

```go
func (q *PostgresQuota) GetTenantQuota(ctx context.Context, tenantID string) ([]ports.QuotaInfo, error) {
    var items []ports.QuotaInfo
    err := q.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
        rows, err := tx.Query(ctx, `
            SELECT q.tenant_id::text, q.resource_type, q.total, q.reserved, q.used, q.updated_at,
                   m.unit, m.display_name, m.is_discrete
            FROM resource_quota q
            JOIN resource_quota_meta m ON m.resource_type = q.resource_type
            WHERE q.tenant_id = $1
            ORDER BY q.resource_type
        `, tenantID)
        if err != nil {
            return err
        }
        defer rows.Close()
        for rows.Next() {
            var it ports.QuotaInfo
            if err := rows.Scan(&it.TenantID, &it.ResourceType, &it.Total, &it.Reserved, &it.Used, &it.UpdatedAt,
                &it.Unit, &it.DisplayName, &it.IsDiscrete); err != nil {
                return err
            }
            items = append(items, it)
        }
        return rows.Err()
    })
    return items, err
}
```

**tenant\_id 存在性校验**：若 tenant\_id 不存在，返回空 items；HTTP 层若需 404 `TENANT_NOT_FOUND`，可先单独 SELECT tenants 表校验，或由 handler 根据 `len(items)==0` 决定是否映射为 404（需区分"租户存在但无配额行"与"租户不存在"，建议在 adapter 显式校验租户存在并返回 `ErrTenantNotFound`）。

### 6.4 DeleteTenantQuota（整租户删除，DELETE）

删除该租户所有 `resource_quota` 行 + `resource_reservations` 流水。租户禁用/资源清理场景，不守卫 `reserved+used`。

```go
func (q *PostgresQuota) DeleteTenantQuota(ctx context.Context, tenantID string) error {
    return q.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
        // 校验租户存在
        var exists bool
        if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenants WHERE id = $1)`, tenantID).Scan(&exists); err != nil {
            return err
        }
        if !exists {
            return ports.ErrTenantNotFound
        }

        // 先删流水（外键 resource_reservations.tenant_id REFERENCES tenants(id) ON DELETE CASCADE，
        // 但 resource_quota 也有 ON DELETE CASCADE，所以删 tenants 行会级联；此处显式删两表更清晰，且不删 tenants）
        if _, err := tx.Exec(ctx, `DELETE FROM resource_reservations WHERE tenant_id = $1`, tenantID); err != nil {
            return err
        }
        if _, err := tx.Exec(ctx, `DELETE FROM resource_quota WHERE tenant_id = $1`, tenantID); err != nil {
            return err
        }
        return nil
    })
}
```

**说明**：core-quota-api.md §4 语义为租户禁用场景清理配额，不强制 `reserved+used==0` 守卫。若调用方需在 used>0 时拒绝删除，应在调用前自行检查 `GetTenantQuota`；本方法按"清理"语义直接删除。

### 6.5 ListQuotaMeta（查询可用配额元数据，GET quota-meta）

查询 `resource_quota_meta` 表中所有 `enabled=true` 的维度，用于创建租户/套餐时展示可选项。只读查询，无需鉴权租户上下文，用 `WithPlatformTx`。

```go
func (q *PostgresQuota) ListQuotaMeta(ctx context.Context) ([]ports.QuotaMeta, error) {
    var items []ports.QuotaMeta
    err := q.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
        rows, err := tx.Query(ctx, `
            SELECT resource_type, display_name, unit, default_quota, is_discrete
            FROM resource_quota_meta
            WHERE enabled = true
            ORDER BY resource_type
        `)
        if err != nil {
            return err
        }
        defer rows.Close()
        for rows.Next() {
            var m ports.QuotaMeta
            if err := rows.Scan(&m.ResourceType, &m.DisplayName, &m.Unit, &m.DefaultQuota, &m.IsDiscrete); err != nil {
                return err
            }
            items = append(items, m)
        }
        return rows.Err()
    })
    return items, err
}
```

**说明**：只读 meta 表，不触碰 resource\_quota / resource\_reservations。结果集固定为已注册维度（当前 8 个），无分页需求。

***

## 7. Core API 契约设计（repo/api/openapi/v1.yaml）

> 本节对齐 `core-quota-api.md` 实际需求。4 个租户配额端点统一在 `/admin/tenants/{tenant_id}/quota`，配额元数据查询走 `/admin/quota-meta`，由租户管理 service（平台管理员）经 Core SDK 调用。

### 7.1 设计原则

1. **契约先行**：先改 `v1.yaml`，再写 port/adapter/handler/SDK（CLAUDE.md §4 强制）
2. **路径统一**：4 个租户配额端点都在 `/admin/tenants/{tenant_id}/quota`，不再分自查/平台两套；配额元数据查询单独走 `/admin/quota-meta`
3. **幂等**：POST 用 `ON CONFLICT DO NOTHING`（已存在跳过）；PUT/DELETE 支持 `idempotency_key`（CLAUDE.md §4.5）
4. **不破坏现有契约**：只新增端点/schema/枚举，不删不改
5. **鉴权**：路径在 `/admin/tenants/...` 下，需扩展 `scopeAllowedForPath` 放行 admin scope（见 §9）

### 7.2 新增端点

| Method | Path                               | operationId       | 说明                                | 涉及表                                                  |
| ------ | ---------------------------------- | ----------------- | --------------------------------- | ---------------------------------------------------- |
| POST   | `/admin/tenants/{tenant_id}/quota` | createTenantQuota | 批量初始化配额行（total 可省略取 default）      | resource\_quota INSERT、resource\_quota\_meta SELECT  |
| PUT    | `/admin/tenants/{tenant_id}/quota` | updateTenantQuota | 批量改 total（不影响 used/reserved，允许缩容） | resource\_quota UPDATE                               |
| GET    | `/admin/tenants/{tenant_id}/quota` | getTenantQuota    | 查该租户所有维度（含 unit/display\_name/is\_discrete） | resource\_quota SELECT + resource\_quota\_meta JOIN  |
| DELETE | `/admin/tenants/{tenant_id}/quota` | deleteTenantQuota | 删除该租户所有配额行 + 流水                   | resource\_quota DELETE、resource\_reservations DELETE |
| GET    | `/admin/quota-meta`                | listQuotaMeta     | 查可用配额元数据（enabled=true 的维度，用于创建租户/套餐表单） | resource\_quota\_meta SELECT                          |

- `tenant_id` 从路径参数取（平台管理员指定目标租户）
- POST/PUT/DELETE 支持 `idempotency_key` header
- `GET /admin/quota-meta` 无路径参数、无请求体，纯只读查询

### 7.3 新增 schema（对齐 core-quota-api.md）

```yaml
# 请求 - 新建（POST）
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

# 请求 - 修改（PUT）
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

# 响应（GET/POST/PUT 共用）
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
    used: { type: integer, format: int64, description: "已实扣（已 Confirm）" }
    reserved: { type: integer, format: int64, description: "已预占（已 Try 未 Confirm/Cancel）" }
    tightened: { type: boolean, description: "PUT 缩容自动收紧标记（请求 total<used+reserved 时收紧为 used+reserved，置 true）；GET 响应中为零值 false，handler 可选择保留或用 omitempty 省略（带不带均不影响契约）" }
    unit: { type: string, description: "单位（来自 resource_quota_meta，GET 时返回）" }
    display_name: { type: string, description: "展示名称（来自 resource_quota_meta，GET 时返回）" }
    is_discrete: { type: boolean, description: "是否离散计数（来自 resource_quota_meta，GET 时返回，用于校验 total 值类型）" }

# DELETE 响应
QuotaDeleteResponse:
  type: object
  required: [tenant_id, message]
  properties:
    tenant_id: { type: string, format: uuid }
    message: { type: string, example: "quota deleted" }

# GET /admin/quota-meta 响应
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
    resource_type: { type: string, description: "配额维度标识" }
    display_name: { type: string, description: "展示名称" }
    unit: { type: string, description: "单位" }
    default_quota: { type: integer, format: int64, description: "默认上限（未提供 total 时兜底）" }
    is_discrete: { type: boolean, description: "是否离散计数（true=整数，false=允许小数），用于校验新增/修改 total 的值类型" }
```

### 7.4 错误码（对齐 core-quota-api.md §5）

| 错误码                               | HTTP | 说明                                             |
| --------------------------------- | ---- | ---------------------------------------------- |
| `TENANT_NOT_FOUND`                | 404  | 租户不存在                                          |
| `QUOTA_NOT_FOUND`                 | 404  | 配额行不存在（修改时）                                    |
| `QUOTA_ALREADY_EXISTS`            | 409  | 配额行已存在（新建时，已存在的跳过，其余正常创建）                      |
| `QUOTA_RESOURCE_NOT_REGISTERED`   | 422  | resource\_type 未注册或 enabled=false              |
| `VALIDATION_FAILED`               | 400  | 参数校验失败（total 负数 / items 空 / resource\_type 重复） |

***

## 8. Handler 实现（ani-gateway/internal/router/quota\_resources.go）

> 本节实现 QuotaAdminService 的 5 个端点：4 个 `/admin/tenants/{id}/quota` + 1 个 `/admin/quota-meta`。QuotaStoreService 的 `/quotas`、`/quotas/me` 端点 handler 由嘉明实现，不在本任务范围。

照搬 `demo_instances.go` 模式：handler 持有 `ports.QuotaAdminService` 接口，构造时注入 adapter。

### 8.1 结构体与路由注册

```go
package router

import (
    "github.com/cloudwego/hertz/pkg/app"
    "github.com/cloudwego/hertz/pkg/app/server/route"
    "github.com/kubercloud/ani/pkg/ports"
)

type quotaAPI struct {
    admin ports.QuotaAdminService
}

func registerQuotaResources(v1 *route.RouterGroup, admin ports.QuotaAdminService) {
    api := &quotaAPI{admin: admin}
    // 管理端点（scope=admin/platform，由 scopeAllowedForPath 守卫）
    // 路径对齐 core-quota-api.md：/admin/tenants/{tenant_id}/quota
    v1.POST("/admin/tenants/:tenant_id/quota", api.createTenantQuota)
    v1.PUT("/admin/tenants/:tenant_id/quota", api.updateTenantQuota)
    v1.GET("/admin/tenants/:tenant_id/quota", api.getTenantQuota)
    v1.DELETE("/admin/tenants/:tenant_id/quota", api.deleteTenantQuota)
    // 配额元数据查询（只读，无路径参数）
    v1.GET("/admin/quota-meta", api.listQuotaMeta)
}
```

> **接线说明**：`RegisterOptions` 需新增 `QuotaAdminService ports.QuotaAdminService` 字段，`RegisterWithOptions` 需新增 `registerQuotaResources(v1, options.QuotaAdminService)` 调用。

### 8.2 handler 方法要点

- `createTenantQuota`：解析 `QuotaCreateRequest` → `api.admin.CreateTenantQuota(ctx, c.Param("tenant_id"), items)` → 响应 `Quota`（200）或错误码
- `getTenantQuota`：`api.admin.GetTenantQuota(ctx, c.Param("tenant_id"))` → 响应 `Quota`（200）或 `TENANT_NOT_FOUND`（404）；`tightened` 字段为零值 false，建议用 `omitempty` 省略（李宇 API §3 GET 响应未定义此字段），保留也不影响契约
- `updateTenantQuota`：解析 `QuotaUpdateRequest` → `api.admin.UpdateTenantQuota` → 响应 `Quota`（200）或错误码；`tightened` 字段保留（PUT 响应需要，对齐李宇 API §2）
- `deleteTenantQuota`：`api.admin.DeleteTenantQuota(ctx, c.Param("tenant_id"))` → 响应 `QuotaDeleteResponse`（200）或 `TENANT_NOT_FOUND`（404）
- `listQuotaMeta`：`api.admin.ListQuotaMeta(ctx)` → 响应 `QuotaMetaListResponse`（200）；只读查询无业务错误，仅系统错误返回 500
- 错误统一用 `writeDemoError` 三段式 + `middleware.GetRequestID(c)`（与 demo\_instances 一致）

### 8.3 错误映射（adapter 哨兵 → HTTP）

| adapter 返回                      | HTTP | code                                                    |
| ------------------------------- | ---- | ------------------------------------------------------- |
| `ErrTenantNotFound`             | 404  | `TENANT_NOT_FOUND`                                      |
| `ErrQuotaNotFound`              | 404  | `QUOTA_NOT_FOUND`                                       |
| `ErrQuotaResourceNotRegistered` | 422  | `QUOTA_RESOURCE_NOT_REGISTERED`                         |
| `ErrQuotaAlreadyExists`         | 409  | `QUOTA_ALREADY_EXISTS`（POST 时，已存在的跳过，其余正常创建）            |
| 其他                              | 500  | `INTERNAL`                                              |

### 8.4 tenant\_id 取法

- 全部从路径参数 `c.Param("tenant_id")` 取（平台管理员指定目标租户，由 scope 鉴权保证可信）

***

## 9. 平台鉴权扩展（middleware/auth.go）

现有 `scopeAllowedForPath` 只对 `/api/v1/auth/platform/*` 放行 platform scope。需扩展：

```go
func scopeAllowedForPath(path, scope string) bool {
    // 平台/管理路由前缀：/auth/platform/*、/platform/*、/admin/*（含 /admin/tenants/*、/admin/quota-meta）
    if strings.HasPrefix(path, "/api/v1/auth/platform/") ||
       strings.HasPrefix(path, "/api/v1/platform/") ||
       strings.HasPrefix(path, "/api/v1/admin/") {
        return scope == "platform"
    }
    return scope == "tenant"
}
```

**风险提示**：此修改让 `/api/v1/admin/*` 路由要求 platform scope（覆盖 `/admin/tenants/*` 与 `/admin/quota-meta`）。需确认现有无其他 `/api/v1/admin/` 路由被误伤（调研未见此类路由，安全）。若后续有租户管理其他端点也走此前缀，会一并要求 platform scope，符合预期。

***

## 10. SDK 生成与 Services 调用链

### 10.1 Core SDK 生成

契约改完后执行（Makefile 已有 target）：

```bash
make gen-core-sdk    # 重新生成 sdks/core/go/anisdk/client.go
make validate-sdk-beta
```

生成后 Core SDK 的 `Operations` 切片会自动包含 createTenantQuota/updateTenantQuota/getTenantQuota/deleteTenantQuota/listQuotaMeta。

### 10.2 Services 侧租户管理调用模式

租户管理（未来独立 service）通过 Core SDK 调 Core REST：

```go
import anisdk "github.com/kubercloud/ani/sdks/core/go/anisdk"

// 管理员新建某租户配额（多维度批量）
client := anisdk.NewClient(coreBaseURL, platformToken)
resp, err := client.Request("POST",
    "/admin/tenants/"+tenantID+"/quota",
    anisdk.RequestOptions{
        Body: map[string]any{
            "items": []map[string]any{
                {"resource_type": "gpu_count", "total": 16},
                {"resource_type": "cpu_core", "total": 128},
                {"resource_type": "memory_gb"},  // total 省略，取 default_quota
            },
        },
    })

// 管理员修改某租户配额上限（批量）
resp, err = client.Request("PUT",
    "/admin/tenants/"+tenantID+"/quota",
    anisdk.RequestOptions{
        Body: map[string]any{
            "items": []map[string]any{
                {"resource_type": "gpu_count", "total": 32},
                {"resource_type": "storage_gb", "total": 4096},
            },
        },
    })

// 查询某租户配额
resp, err = client.Request("GET",
    "/admin/tenants/"+tenantID+"/quota", anisdk.RequestOptions{})

// 删除某租户所有配额（租户禁用场景）
resp, err = client.Request("DELETE",
    "/admin/tenants/"+tenantID+"/quota", anisdk.RequestOptions{})

// 查询可用配额元数据（创建租户/套餐时展示可选项）
resp, err = client.Request("GET",
    "/admin/quota-meta", anisdk.RequestOptions{})
```

**注意**：Services 层不得 import `pkg/ports`/`pkg/adapters`（`validate_services_boundary.py` 强制），只能通过 SDK 调 Core。ani-gateway 内 handler 是 Core-protected，可以 import `pkg/ports`。

### 10.3 调用链全景

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

三个 port（QuotaService 扣减 / QuotaStoreService 配置查询 / QuotaAdminService 租户生命周期管理）操作同一组表，但走不同 port 和不同事务模型，互不污染。一个 `PostgresQuota` adapter struct 实现三个 interface。

***

## 11. 测试策略

### 11.1 扣减单元测试（fake/mock）

仿照 `pkg/adapters/runtime/metering_db_store_test.go` 风格：

- 定义 `fakeMetadataTx` 实现 `ports.MetadataTx`，模拟 `QueryRow`/`Exec` 返回值
- 测试场景：
  - Try 成功：meta enabled、lazy init、预占成功
  - Try 失败：meta disabled → `ErrQuotaResourceNotRegistered`
  - Try 失败：余量不足 → `ErrQuotaExceeded`
  - TryMany 成功：多维度全占成功
  - TryMany 原子性：第二维度不足 → 第一维度预占回滚（验证 fake tx 的 rollback 调用）
  - Confirm 幂等：state=reserved → confirmed，重复 Confirm → ErrNoRows → 跳过
  - Cancel 幂等：state=reserved → cancelled，重复 Cancel → 跳过
  - Release 幂等：state=confirmed → released，重复 Release → ErrNoRows → 跳过
  - Confirm 后 reserved 减少、used 增加
  - Cancel 后 reserved 减少
  - Release 后 used 减少
  - Release 对非 confirmed 流水（reserved/cancelled）→ ErrNoRows → 跳过，不改账本

### 11.2 配置查询单元测试（QuotaStoreService）

- Put：
  - 新增（行不存在）→ UPSERT 建行成功
  - 修改（行存在）→ UPSERT 覆盖 total 成功
  - 资源未注册/enabled=false → `ErrQuotaResourceNotRegistered`
  - total < used+reserved → 撞 CHECK 约束报错（不 clamp，透传错误）
  - 多维度同时 PUT → 全部成功
- List：
  - 无过滤 → 按租户级分页返回，每页含完整多维度 QuotaView（不拆碎租户维度）
  - tenant\_id 过滤 → 直接返回指定租户全部维度（不分页）
  - 分页 cursor 衔接：第一页 NextCursor = 末尾 tenant\_id，第二页用该 cursor 正确衔接，不漏不重
  - 空表 → 返回空 items、空 cursor
  - 超过 limit 的一页 → hasMore=true，NextCursor 指向本页最后一个租户
- GetMy：
  - 返回当前租户多维度 map
  - RLS 隔离：只返回本租户数据
- GetTotalForUpdateTx：
  - 行存在 → 返回 total
  - 行不存在 → `ErrQuotaNotFound`
  - FOR UPDATE 锁行：并发两个 tx 调用，第二个阻塞等第一个提交

### 11.3 管理单元测试（fake/mock）

- CreateTenantQuota：
  - 批量新建成功（含 total 省略取 default\_quota）
  - 租户不存在 → `ErrTenantNotFound`
  - 资源未注册/enabled=false → `ErrQuotaResourceNotRegistered`
  - 已存在的维度 → `ON CONFLICT DO NOTHING` 跳过，其余正常创建
  - items 为空 → 校验错误
- UpdateTenantQuota：
  - 批量改 total 成功
  - 维度行不存在 → `ErrQuotaNotFound`
  - 资源未注册 → `ErrQuotaResourceNotRegistered`
  - total < used（缩容）→ 成功（不报错，已有资源继续运行），返回 `tightened=true` + 收紧后的 total
  - total >= used+reserved → 成功，返回 `tightened=false`
  - items 为空 → 校验错误
- GetTenantQuota：
  - 返回多行 + unit/display\_name/is\_discrete（JOIN meta）正确解析
  - 租户不存在 → 返回空 或 `ErrTenantNotFound`（按 adapter 实现选择）
  - 租户存在但无配额行 → 返回空 items
- DeleteTenantQuota：
  - 删除成功（连同 resource\_reservations 流水）
  - 租户不存在 → `ErrTenantNotFound`
  - used>0 时仍可删除（按 core-quota-api.md 语义，不守卫）
- ListQuotaMeta：
  - 返回 enabled=true 的维度列表（含 display\_name/unit/default\_quota/is\_discrete）
  - enabled=false 的维度不返回
  - 空表 → 返回空 items

### 11.4 集成测试（连 PG 实例，双角色验证 RLS）

- 前置：PG 实例可用（docker-compose 本地 PG 或远程 `10.10.1.66:30945`）
- **双角色连接**（复用现有角色，不新增 migration）：

| 连接 | DSN 默认值 | 角色 | RLS 行为 | 用途 |
|---|---|---|---|---|
| 管理员 | `postgres://ani:ani_dev_password@.../ani` | ani (superuser) | 绕过 RLS | 建表 + seed + 平台操作 + 验证 bypass |
| 租户 | `postgres://ani_app_user:ani_dev_password@.../ani` | ani_app_user (普通角色) | 受 RLS 约束 | Try/Confirm/Cancel/Release + 验证隔离 |

- DSN 通过环境变量覆盖：`ANI_TEST_ADMIN_DSN`、`ANI_TEST_TENANT_DSN`
- **Setup 顺序**：
  1. 用管理员连接建三张表 + RLS policy + seed meta
  2. 用管理员连接 `GRANT SELECT, INSERT, UPDATE, DELETE ON 三张表 TO ani_app`
  3. 构造管理员 `MetadataStore`（WithPlatformTx）和租户 `MetadataStore`（WithTenantTx）
  4. 跑测试
  5. 用管理员连接 `TRUNCATE` 清理
- **扣减场景（验证 RLS 写权限，关键）**：

| # | 操作 | 连接 + RLS 上下文 | 验证什么 |
|---|---|---|---|
| 1 | 租户 A Try 创建实例 | 租户连接 + SET tenant_id='A' | RLS 放行：INSERT resource_reservations + UPDATE resource_quota 成功 |
| 2 | 租户 A GetMy 查自己配额 | 租户连接 + SET tenant_id='A' | RLS 放行：返回自己的 total/used/reserved |
| 3 | 租户 A 查租户 B 的配额 | 租户连接 + SET tenant_id='A' | RLS 拦截：SELECT 0 行 |
| 4 | 租户 A Confirm 自己的 txID | 租户连接 + SET tenant_id='A' | RLS 放行：state reserved→confirmed，used 转账成功 |
| 5 | 租户 B Confirm 租户 A 的 txID | 租户连接 + SET tenant_id='B' | RLS 拦截：UPDATE 0 行 → ErrNoRows → 幂等跳过 |
| 6 | 租户 A Cancel 自己的 txID | 租户连接 + SET tenant_id='A' | RLS 放行：reserved 释放成功 |
| 7 | 租户 A Release 自己的 txID | 租户连接 + SET tenant_id='A' | RLS 放行：used 减回成功 |
| 8 | 租户 A 试图 INSERT tenant_id='B' 的流水 | 租户连接 + SET tenant_id='A' | RLS 拦截：WITH CHECK 不满足，INSERT 被拒 |
| 9 | 并发 Try 不超卖 | N 个租户连接 + SET 各自 tenant_id | reserved 不超过 total |
| 10 | TryMany 端到端 | 租户连接 + SET tenant_id='A' | 占多维度 → Confirm → 验证 used |
| 11 | Confirm/Cancel/Release 幂等 | 租户连接 + SET tenant_id='A' | 重复调用不重复扣减 |
| 12 | Release 端到端 | 租户连接 + SET tenant_id='A' | TryMany → Confirm → Release → used 归零 |

- **管理场景（验证 RLS bypass）**：

| # | 操作 | 连接 | 验证什么 |
|---|---|---|---|
| 13 | Put 设置租户 A 配额 | 管理员连接 | 平台 UPSERT 成功（bypass RLS） |
| 14 | List 所有租户 | 管理员连接 | RLS bypass：返回所有租户 |
| 15 | Delete 租户 A 配额 | 管理员连接 | RLS bypass：resource_quota + resource_reservations 全删成功 |
| 16 | CreateTenantQuota 批量新建 | 管理员连接 | 回读验证 total/used=0/reserved=0 |
| 17 | CreateTenantQuota 幂等 | 管理员连接 | 重复同一维度 → ON CONFLICT DO NOTHING，不覆盖 |
| 18 | UpdateTenantQuota 改 total | 管理员连接 | 回读验证 tightened=false |
| 19 | UpdateTenantQuota 缩容 | 管理员连接 | total < used → GREATEST clamp，tightened=true，Try 新建 → ErrQuotaExceeded |
| 20 | GetTenantQuota JOIN meta | 管理员连接 | unit/display_name/is_discrete 正确 |
| 21 | DeleteTenantQuota | 管理员连接 | resource_quota + resource_reservations 均清空 |
| 22 | ListQuotaMeta | 管理员连接 | 返回 enabled=true 维度列表，含 is_discrete |
| 23 | SDK 端到端 | 启动 ani-gateway | SDK 调 POST/PUT/GET/DELETE + GET quota-meta → DB 验证 |

- 测试后清理数据（TRUNCATE，用管理员连接）

### 11.5 契约与边界校验

```bash
make validate-services          # Services boundary gate
make validate-sdk-beta          # SDK 漂移校验
make validate-architecture       # 架构边界
```

***

## 12. 验收标准

```bash
cd repo
make test                       # 单元测试通过
make validate-architecture      # 架构边界
make validate-services          # Services boundary
make gen-core-sdk && git diff --check -- sdks/core   # SDK 无漂移
git diff --check                # 无空白错误
```

集成测试手动运行：

```bash
make deps                       # 启动本地 PG
go test ./pkg/adapters/quota/ -v -run Integration
```

***

## 13. 风险与注意事项

| 风险                                                           | 应对                                                                                                                                                                                                |
| ------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 李宇的 migration 表结构与方案 SQL 不一致                                 | adapter 代码以方案 §3 表结构为准；李宇提交后核对，不一致则调整 adapter                                                                                                                                                     |
| `WithTenantTx` 的 RLS 上下文注入                                   | Try/TryMany 通过 `WithTenantTx` 自动注入 `app.current_tenant_id`；Confirm/Cancel/Release 由调用方（reconciler / 删除流程）负责                                                                                       |
| `gen_random_uuid()` 依赖 pgcrypto                              | 确认李宇的 migration 是否启用了 pgcrypto extension                                                                                                                                                          |
| Release 依赖 `resource_reservations.state` 约束包含 `'released'`   | 通知李宇 migration 的 CHECK 约束需加 `'released'`，否则 Release 的 UPDATE 会被 CHECK 拒绝                                                                                                                          |
| tenants 表旧字段（max\_gpu\_count 等）与新 resource\_quota.total 语义重叠 | 本方案不动 tenants 表旧字段；旧字段视为历史遗留，后续 PR 统一迁移/废弃。resource\_quota 表为唯一配额真实来源                                                                                                                             |
| `scopeAllowedForPath` 扩展影响面                                  | 新增 `/api/v1/admin/` 前缀要求 platform scope（覆盖 `/admin/tenants/*` 与 `/admin/quota-meta`）；调研未见现有此路径路由，无误伤                                                                                   |
| meta 表种子数据未就绪                                                | CreateTenantQuota 的 default\_quota 兜底依赖 meta 种子；李宇 migration 已含 8 维度 INSERT 种子（gpu\_count/cpu\_core/memory\_gb/storage\_gb/token\_count/kb\_query\_count/member\_count/inference\_service\_count） |
| SDK 自动生成覆盖                                                   | `sdks/core/go/anisdk/client.go` 是 DO NOT EDIT 文件，改契约后必须 `make gen-core-sdk`，否则 `validate-sdk-beta` 报漂移                                                                                                 |
| Services 拆独立 service 后调用链切换                                  | 本方案已按 SDK 调 REST 设计，未来拆分只需在 tenant-service 里初始化 SDK client，无需改 Core                                                                                                                               |
| QuotaStoreService.Put 撞 CHECK 约束                             | Put 不 clamp，若 BOSS 运营把 total 改到低于 used+reserved，撞 `CHECK (reserved + used <= total)` 报错。这是预期行为（运营失误应报错），adapter 透传错误，嘉明 handler 映射 HTTP                                                           |
| GetTotalForUpdateTx 的 FOR UPDATE 死锁风险                        | FOR UPDATE 锁 resource\_quota 行串行化并发预留；若嘉明 handler 事务内还锁其他表（如 gpu\_slices），需注意锁顺序一致避免死锁。嘉明 handler 需保证锁顺序：先锁 resource\_quota，再操作 gpu\_slices                                                       |
| 三个 port 共享 adapter 的测试隔离                                     | 一个 `PostgresQuota` struct 实现三个 interface，单元测试需分别测三个 interface 的方法，避免共用 mock 状态污染                                                                                                                  |

### 13.1 RLS 与 WithPlatformTx 交互（已解决，李宇确认）

~~原风险~~：`WithPlatformTx` 不设 `app.current_tenant_id`，RLS policy `tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid` 导致 `tenant_id = NULL`，平台管理员看不到任何行。

**李宇最终方案（双 policy，已落地）**：`resource_quota` 和 `resource_reservations` 两张表均新增 `platform_bypass` policy：

```sql
-- 平台操作绕过 RLS：未设置 tenant_id 时放行所有行
CREATE POLICY resource_quota_platform_bypass
  ON resource_quota FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

-- 租户上下文只能看/操作自己的行（SELECT + INSERT + UPDATE + DELETE）
-- FOR ALL + USING = WITH CHECK，保证租户只能写自己的行、不能 INSERT 别人的行
CREATE POLICY resource_quota_self
  ON resource_quota FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
```

**工作原理**：

- `WithPlatformTx`（不设 `app.current_tenant_id`）→ `current_setting(..., true)` 返回 NULL → `IS NULL` 为 true → `platform_bypass` 放行所有行
- `WithTenantTx`（设了 `app.current_tenant_id`）→ 返回非 NULL → `platform_bypass` 不匹配 → 走 `self` 只看/操作自己的行

**对本任务的影响**：§6 的 5 个管理方法用 `WithPlatformTx` 即可正常工作，**无需新增** **`WithTenantTxByID`** **变体**，无需 BYPASSRLS 特权角色。adapter 代码不需为 RLS 做特殊处理。

### 13.2 缩容与 CHECK 约束冲突（已解决，李宇确认）

~~原风险~~：core-quota-api.md 允许 `total < used`，但 `CHECK (reserved + used <= total)` 会拒绝。

**李宇最终方案（不改 CHECK，应用层 clamp）**：

- 保留 `CHECK (reserved + used <= total)` 约束不变
- `UpdateTenantQuota` 用 `GREATEST($total, reserved + used)` 在 SQL 层 clamp，保证 `total >= used+reserved`
- 即缩容下限为当前已用量，不会低于 used+reserved，不违反 CHECK

**对本任务的影响**：§6.2 已按此方案实现（`SET total = GREATEST($3, reserved + used)`），**无需修改 migration 的 CHECK 约束**。

***

## 14. 实施顺序（建议）

1. **先验证 RLS 风险**（§13.1）：写最小集成测试，确认 `WithPlatformTx` 能否看到 resource\_quota 行
2. **改 Core API 契约**（§7）：`v1.yaml` 新增 `/admin/tenants/{id}/quota` + `/admin/quota-meta` 端点 + schema
3. **新增 port**（§3）：`pkg/ports/quota.go`（QuotaService + QuotaStoreService）+ `quota_admin.go`（QuotaAdminService）+ errors.go 哨兵
4. **实现 adapter**（§4-§6）：`postgres_quota.go`（一个 `PostgresQuota` struct 实现三个 interface）
   - §4 扣减（QuotaService）：Try/TryMany/Confirm/Cancel/Release
   - §5 配置查询（QuotaStoreService）：Put/List/GetMy/GetTotalForUpdateTx
   - §6 租户生命周期管理（QuotaAdminService）：Create/Update/Get/Delete/ListQuotaMeta
5. **实现 handler**（§8）+ 扩展鉴权（§9）
6. **单元测试**（§11.1 扣减、§11.2 配置查询、§11.3 管理）
7. **生成 SDK**（§10.1）：`make gen-core-sdk`
8. **集成测试**（§11.4）
9. **全量验收**（§12）

***

## 附录A：三张表 SQL（参考，由李宇提交，已对齐李宇最终方案）

### resource\_quota\_meta

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

-- 初始 seed（8 维度）
INSERT INTO resource_quota_meta (resource_type, display_name, unit, is_discrete, default_quota, collector_id, description) VALUES
  ('gpu_count',              'GPU 份数',     '份',    true, 8,        'prometheus_dcgm',    '单租户可持有的 GPU 份数上限'),
  ('cpu_core',               'CPU 核数',     '核',    true, 8,       'prometheus_kubelet', '单租户可占用的 CPU 核数上限'),
  ('memory_gb',              '内存 GB',      'gb',    true, 32,      'prometheus_kubelet', '单租户可占用的内存 GB 上限'),
  ('storage_gb',             '存储 GB',      'gb',    true, 64,     NULL,                  '单租户可占用的存储 GB 上限'),
  ('token_count',            'Token 数',     'token', true, 1000000, 'inference_token',     '单租户可消耗的 Token 总量上限'),
  ('kb_query_count',         'KB 查询次数',  '次',    true, 10000,  NULL,                  '单租户知识库查询次数上限'),
  ('member_count',           '成员上限',     '人',    true, 20,     NULL,                  '单租户可邀请的成员数量上限'),
  ('inference_service_count','推理服务上限', '个',    true, 10,     NULL,                  '单租户可创建的推理服务数量上限')
ON CONFLICT (resource_type) DO NOTHING;
```

### resource\_quota

```sql
CREATE TABLE resource_quota (
    tenant_id      UUID   NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    resource_type  TEXT   NOT NULL REFERENCES resource_quota_meta(resource_type),
    total          BIGINT NOT NULL,
    reserved       BIGINT NOT NULL DEFAULT 0,
    used           BIGINT NOT NULL DEFAULT 0,
    CHECK (total >= 0 AND reserved >= 0 AND used >= 0),
    CHECK (reserved + used <= total),  -- 保留；缩容时由应用层 GREATEST clamp 保证不违反
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, resource_type)
);

-- RLS: 双 policy（李宇最终方案）
-- 平台操作绕过 RLS：未设置 tenant_id 时放行所有行
ALTER TABLE resource_quota ENABLE ROW LEVEL SECURITY;
ALTER TABLE resource_quota FORCE ROW LEVEL SECURITY;
CREATE POLICY resource_quota_platform_bypass
  ON resource_quota FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

-- 租户上下文：只能操作自己的行（SELECT + INSERT + UPDATE + DELETE）
-- FOR ALL + USING = WITH CHECK，保证租户只能写自己的行、不能 INSERT 别人的行
CREATE POLICY resource_quota_self
  ON resource_quota FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
```

### resource\_reservations

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
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_res_state_expires
    ON resource_reservations(state, expires_at) WHERE state = 'reserved';
CREATE INDEX idx_res_tenant
    ON resource_reservations(tenant_id, state);

-- RLS: 双 policy（李宇最终方案）
ALTER TABLE resource_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE resource_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY resource_reservations_platform_bypass
  ON resource_reservations FOR ALL
  USING (current_setting('app.current_tenant_id', true) IS NULL);

-- 租户上下文：只能操作自己的行（SELECT + INSERT + UPDATE + DELETE）
CREATE POLICY resource_reservations_self
  ON resource_reservations FOR ALL
  USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
```

***

## 附录B：扣减与管理的关系

- 扣减（`QuotaService`）与管理（`QuotaAdminService`）操作同一组表，但：
  - 不同 port 文件、不同 interface、不同 adapter 文件
  - 扣减用 `WithTenantTx`（本租户）/ 接外部 tx；管理用 `WithPlatformTx`（平台管理员指定目标租户）
  - 两者无代码依赖，仅在 DB 层共享表结构
- demo\_instances.go 调 `QuotaService.TryMany` 创建实例时扣减；本方案不改动该路径
- 未来 reconciler 调 `Confirm/Cancel/Release` 时也只依赖扣减 `QuotaService`，与管理无关
- 租户管理（Services 层）通过 Core SDK 调 Core REST 的 5 个管理端点（`/admin/tenants/{tenant_id}/quota` 4 个 + `/admin/quota-meta` 1 个），走 `QuotaAdminService`

