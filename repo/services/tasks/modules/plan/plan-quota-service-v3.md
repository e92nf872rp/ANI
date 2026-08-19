# 配额服务 v3：Core 配额 Upsert 端点

> 状态: 计划草案
> 创建日期: 2026-08-18
> 负责人: kjs
> 前置文档: `plan-quota-service.md`（v1 已合并入 main）、`plan-quota-service-v2.md`（v2 已合并入 main）、`配额core层upsert端点设计.md`（接口定义）

***

## 1. 背景与任务边界

### 1.1 为什么需要 v3

v1 实现了 `QuotaAdminService` 的 5 个管理方法（Create/Update/Get/Delete/ListQuotaMeta）及对应 REST 端点。其中：

| 端点 | 已存在维度 | 不存在维度 |
|------|-----------|-----------|
| `POST /admin/tenants/{tenant_id}/quota`（createTenantQuota） | `ON CONFLICT DO NOTHING` 跳过 | INSERT |
| `PUT /admin/tenants/{tenant_id}/quota`（updateTenantQuota） | UPDATE total（GREATEST clamp） | 返回 `QUOTA_NOT_FOUND`（404） |

两者均不支持"存在则更新、不存在则新建"的原子 upsert 语义。

tenant-service 的 `applyTenantQuotaItems`（绑定套餐或同步限额）在为租户写入多个维度配额时，由于不知道哪些维度已存在，当前必须：

1. **GetQuota** — 查询租户已有配额维度
2. **分流** — 已有维度放入 `putItems`，缺失维度放入 `createItems`
3. **PutQuota** — 更新已有维度
4. **CreateQuota** — 新建缺失维度

步骤 3 和 4 是两次独立 HTTP 调用，对应两个独立 DB 事务。如果 PutQuota 成功但 CreateQuota 失败：

- 已 Put 的维度 total 已被修改，**无法自动回滚**（Core 端无事务引用可传递）
- Services 层只能 best-effort 补偿回滚：用 GetQuota 拿到的旧 total 再调 PutQuota 恢复
- 补偿本身也可能失败，导致数据不一致
- 额外增加 1 次 GetQuota + 1 次补偿 PutQuota 的网络开销

### 1.2 本任务做什么

在 `QuotaAdminService` interface 新增一个方法，并在 `PostgresQuota` adapter 中实现，同时新增对应 REST 端点：

| 方法 | 签名 | 事务来源 | 用途 |
|---|---|---|---|
| `UpsertTenantQuota` | `(ctx, tenantID string, items []QuotaItemInput) ([]QuotaInfo, error)` | **自开** `WithPlatformTx` | 批量 upsert：已存在维度更新 total，不存在维度新建行，单事务原子 |

**端点：**

```
PUT /api/v1/admin/tenants/{tenant_id}/quota/upsert
```

单次请求内所有维度在同一 DB 事务中原子完成，消除 Services 层的 Get + 分流 + 双调用 + 补偿回滚。

### 1.3 本任务不做什么

| 不做项 | 原因 |
|---|---|
| 改动 Create / Update / Get / Delete / ListQuotaMeta | 已在 v1 实现，保持不变 |
| 改动 QuotaService / QuotaStoreService | 与本任务无关 |
| 改动三张表 migration | 表结构不变，复用 `INSERT ... ON CONFLICT DO UPDATE` |
| 改动 tenant-service 的 `QuotaSvcClient` | tenant-service 侧适配由后续 PR 负责（新增 `UpsertQuota` 方法 + 替换 Get+分流+双调用） |
| 改动 Confirm / Cancel / Release / Try / TryMany / TryTx / TryManyTx | 与本任务无关 |

***

## 2. 交付物清单

| 文件 | 类型 | 说明 |
|---|---|---|
| `repo/pkg/ports/errors.go` | 修改 | 新增 `ErrQuotaUpdateUncertain` 和 `ErrMetadataPlatformTxCommit` 哨兵错误 |
| `repo/pkg/ports/quota_admin.go` | 修改 | `QuotaAdminService` interface 新增 `UpsertTenantQuota` 方法签名 |
| `repo/pkg/adapters/postgres/metadata_store.go` | 修改 | `WithPlatformTx` 在 Commit 失败时包装 `ErrMetadataPlatformTxCommit`，供上层用 `errors.Is` 判定 |
| `repo/pkg/adapters/runtime/postgres_quota.go` | 修改 | 实现 `UpsertTenantQuota`（`ON CONFLICT DO UPDATE` + GREATEST clamp + 回读 tightened + commit 错误捕获） |
| `repo/pkg/adapters/runtime/postgres_quota_admin_test.go` | 修改 | 新增 UpsertTenantQuota 单元测试 |
| `repo/pkg/adapters/runtime/integration_test.go` | 修改 | 新增 UpsertTenantQuota 集成测试（连 PG） |
| `repo/api/openapi/v1.yaml` | 修改 | 新增 `PUT /admin/tenants/{tenant_id}/quota/upsert` 路径定义 + 请求/响应 schema + `QuotaUpdateUncertain` 错误响应 |
| `repo/services/ani-gateway/internal/router/quota_resources.go` | 修改 | 新增 upsert 路由 handler + `writeQuotaUpsertError` 专用错误响应方法 |
| `repo/services/ani-gateway/internal/router/quota_resources_test.go` | 修改 | 新增 upsert handler 错误映射测试 |
| `sdks/core/go/anisdk/client.go` | 重新生成 | `make gen-core-sdk` 自动生成，新增 upsertTenantQuota operation |
| `kjs-study/配额操作任务/plan-quota-service-v3.md` | 本文件 | 本任务方案 |

***

## 3. Port 契约改动

### 3.1 新增哨兵错误

文件：`repo/pkg/ports/errors.go`

在 Quota sentinel errors 区块新增：

```go
// Quota sentinel errors.
ErrQuotaExceeded              = errors.New("quota exceeded")
ErrQuotaResourceNotRegistered = errors.New("quota resource type not registered")
ErrQuotaIdempotencyConflict   = errors.New("quota idempotency key conflict")
ErrQuotaNotFound              = errors.New("quota not found")
ErrQuotaAlreadyExists         = errors.New("quota already exists")
ErrQuotaUpdateUncertain       = errors.New("quota update uncertain: transaction commit status unknown") // 新增
ErrReservationNotFound        = errors.New("resource reservation not found")
```

> `ErrQuotaUpdateUncertain` 表示事务提交阶段失败（DB 宕机/连接断开），DB 事务状态不确定。与事务内校验/写入失败（已确定回滚）区分，调用方收到此错误不得自动重试。

同时在通用 Metadata transaction sentinel errors 区块新增：

```go
// Metadata transaction sentinel errors.
ErrMetadataPlatformTxCommit = errors.New("metadata platform tx commit")
```

> `ErrMetadataPlatformTxCommit` 只用于标记 `MetadataStore.WithPlatformTx` 已完成事务内业务逻辑、但 `Commit()` 阶段失败。上层 adapter 必须通过 `errors.Is(err, ports.ErrMetadataPlatformTxCommit)` 判定提交阶段失败，不得匹配 `err.Error()` 字符串。

### 3.2 QuotaAdminService interface 新增方法

文件：`repo/pkg/ports/quota_admin.go`

在现有 `QuotaAdminService` interface 中新增 `UpsertTenantQuota`：

```go
type QuotaAdminService interface {
    CreateTenantQuota(ctx context.Context, tenantID string, items []QuotaItemInput) ([]QuotaInfo, error)
    UpdateTenantQuota(ctx context.Context, tenantID string, items []QuotaItemUpdate) ([]QuotaInfo, error)
    GetTenantQuota(ctx context.Context, tenantID string) ([]QuotaInfo, error)
    DeleteTenantQuota(ctx context.Context, tenantID string) error
    ListQuotaMeta(ctx context.Context) ([]QuotaMeta, error)

    // UpsertTenantQuota 批量 upsert 租户配额：已存在维度更新 total，不存在维度新建，单事务原子完成。
    UpsertTenantQuota(ctx context.Context, tenantID string, items []QuotaItemInput) ([]QuotaInfo, error)
}
```

### 3.3 复用已有类型

`UpsertTenantQuota` 的入参复用 v1 已有的 `QuotaItemInput`，出参复用 `QuotaInfo`（含 `Tightened` 标记）。不新增任何类型定义。upsert 中 `total < 0` 视为非法，返回 `VALIDATION_FAILED`；`total == 0` 视为未提供，取 `default_quota`。

```go
// QuotaItemInput（v1 已有，无需改动）
type QuotaItemInput struct {
    ResourceType ResourceType
    Total        int64 // 0 means not provided; use default_quota; negative is invalid
}
```

### 3.4 与现有方法的语义区别

| 对比项 | `CreateTenantQuota`（POST） | `UpdateTenantQuota`（PUT） | `UpsertTenantQuota`（新增） |
|--------|---------------------------|---------------------------|---------------------------|
| 已存在维度 | 跳过（DO NOTHING） | UPDATE total（GREATEST clamp） | UPDATE total（GREATEST clamp） |
| 不存在维度 | INSERT | **QUOTA_NOT_FOUND 错误** | INSERT |
| 事务范围 | 单事务 | 单事务 | 单事务 |
| total 省略取 default | 是 | 否（total 必填） | 是 |
| 跨维度原子性 | 部分成功（已存在跳过不回滚） | 任一失败整体回滚 | 任一失败整体回滚 |

> `UpsertTenantQuota` 的"任一失败整体回滚"语义与 `UpdateTenantQuota` 一致，因为 upsert 场景下调用方（tenant-service）期望要么全部维度成功，要么全部不变，不存在"部分成功"的中间态。这与 `CreateTenantQuota` 的部分成功语义不同（Create 的"已存在跳过"是预期行为，不算失败）。

***

## 4. Adapter 实现

### 4.1 核心复用：已有 helper

v1 已在 `postgres_quota.go` 中提取以下 helper，v3 直接复用，**无需新增任何辅助方法**：

| Helper | 用途 | v3 复用点 |
|---|---|---|
| `requireTenantExists(ctx, tx, tenantID)` | 校验租户存在，不存在返回 `ErrTenantNotFound` | upsert 前置校验 |
| `getMetaDefault(ctx, tx, rt)` | 校验维度已注册且 enabled，返回 default_quota | 每维度 meta 校验 + total 兜底 |
| `quotaInfoByTypes(ctx, tx, tenantID, types)` | 回读指定维度集合，JOIN meta 返回 `[]QuotaInfo` | upsert 后回读 + 计算 tightened |

### 4.2 UpsertTenantQuota 实现

文件：`repo/pkg/adapters/runtime/postgres_quota.go`

```go
// UpsertTenantQuota 批量 upsert 租户配额（平台管理员）。自开 WithPlatformTx (bypass RLS)。
// 已存在的维度更新 total，不存在的维度新建行，所有维度在同一事务内原子完成。
// 每维度：校验 total 非负 → 校验 meta enabled → total==0 取 default_quota →
// INSERT ... ON CONFLICT (tenant_id, resource_type) DO UPDATE SET
//   total = GREATEST(EXCLUDED.total, resource_quota.reserved + resource_quota.used)
// → 回读计算 tightened 标记（回读 total > 请求 total 时 tightened=true）。
// 任一维度校验/写入失败 → 整体回滚（返回 ErrInvalid/ErrTenantNotFound/ErrQuotaResourceNotRegistered）。
// 提交阶段失败（DB 宕机/连接断开）→ 返回 ErrQuotaUpdateUncertain，调用方不得自动重试。
func (q *PostgresQuota) UpsertTenantQuota(ctx context.Context, tenantID string, items []ports.QuotaItemInput) ([]ports.QuotaInfo, error) {
    if len(items) == 0 {
        return nil, ports.ErrInvalid
    }

    // 校验 items 中 resource_type 不重复（同一批次对同一维度 upsert 两次属于调用方 bug）
    seen := make(map[ports.ResourceType]struct{}, len(items))
    for _, it := range items {
        if _, ok := seen[it.ResourceType]; ok {
            return nil, ports.ErrInvalid
        }
        seen[it.ResourceType] = struct{}{}
    }

    // 记录每维度请求的 total（default 兜底后的值），用于回读时计算 tightened 标记
    reqTotals := make(map[ports.ResourceType]int64, len(items))
    var err error
    infos := make([]ports.QuotaInfo, 0, len(items))

    err = q.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
        if err := q.requireTenantExists(ctx, tx, tenantID); err != nil {
            return err
        }

        for i, item := range items {
            if item.Total < 0 {
                return fmt.Errorf("%w: total 不能为负数，items[%d].total=%d", ports.ErrInvalid, i, item.Total)
            }

            _, defaultQuota, err := q.getMetaDefault(ctx, tx, item.ResourceType)
            if err != nil {
                return err
            }

            // total==0 视为未提供，取 default_quota
            total := item.Total
            if total == 0 {
                total = defaultQuota
            }
            reqTotals[item.ResourceType] = total

            // INSERT ... ON CONFLICT DO UPDATE：不存在则新建，存在则更新 total
            // 缩容 clamp：GREATEST(EXCLUDED.total, reserved + used) 保证 total >= used+reserved，
            // 不违反 CHECK (reserved + used <= total) 约束。
            // 新建行 reserved=0, used=0，GREATEST(total, 0) = total，clamp 不生效。
            if _, err := tx.Exec(ctx, `
                INSERT INTO resource_quota (tenant_id, resource_type, total, reserved, used)
                VALUES ($1, $2, $3, 0, 0)
                ON CONFLICT (tenant_id, resource_type)
                DO UPDATE SET total = GREATEST(EXCLUDED.total, resource_quota.reserved + resource_quota.used),
                              updated_at = NOW()
            `, tenantID, item.ResourceType, total); err != nil {
                return err
            }
        }

        // 回读 items 涉及维度，带 meta 信息
        types := make([]ports.ResourceType, len(items))
        for i, item := range items {
            types[i] = item.ResourceType
        }
        infos, err = q.quotaInfoByTypes(ctx, tx, tenantID, types)
        return err
    })
    if err != nil {
        // 区分 commit 阶段失败与事务内失败。
        // commit 失败时 DB 事务状态不确定，需上抛 ErrQuotaUpdateUncertain。
        if errors.Is(err, ports.ErrMetadataPlatformTxCommit) {
            return nil, ports.ErrQuotaUpdateUncertain
        }
        return nil, err
    }

    // tightened = 回读 total > 请求 total（GREATEST clamp 生效）
    // 仅对已存在维度（reserved+used > 请求 total）会触发；新建维度 reserved=0, used=0 → tightened=false
    for i := range infos {
        if req, ok := reqTotals[infos[i].ResourceType]; ok && infos[i].Total > req {
            infos[i].Tightened = true
        }
    }
    return infos, nil
}
```

> **Commit 阶段判定**：不要用 `strings.Contains(err.Error(), "commit")`。`WithPlatformTx` 的 Commit 失败应由上层 `MetadataStore` 包装特定哨兵错误，adapter 用 `errors.Is` 判定，避免依赖错误字符串。
>
> ```go
> // MetadataStore.WithPlatformTx
> if err := tx.Commit(ctx); err != nil {
>     return fmt.Errorf("%w: %w", ports.ErrMetadataPlatformTxCommit, err)
> }
> ```
>
> `PostgresQuota.UpsertTenantQuota` 收到 `WithPlatformTx` 返回值后直接判断：
>
> ```go
> if errors.Is(err, ports.ErrMetadataPlatformTxCommit) {
>     return nil, ports.ErrQuotaUpdateUncertain
> }
> ```

### 4.3 SQL 语义详解

```sql
INSERT INTO resource_quota (tenant_id, resource_type, total, reserved, used)
VALUES ($1, $2, $3, 0, 0)
ON CONFLICT (tenant_id, resource_type)
DO UPDATE SET total = GREATEST(EXCLUDED.total, resource_quota.reserved + resource_quota.used),
              updated_at = NOW()
```

- **INSERT 路径（维度行不存在）**：直接插入 `(tenant_id, resource_type, total, 0, 0)`。新行 reserved=0, used=0，CHECK 约束 `reserved + used <= total` → `0 <= total` 恒成立（total >= 0）。
- **ON CONFLICT DO UPDATE 路径（维度行已存在）**：更新 `total = GREATEST(EXCLUDED.total, reserved + used)`。
  - `EXCLUDED.total` 是请求值（default 兜底后的值）
  - `resource_quota.reserved + resource_quota.used` 是当前已占用量（行锁保护下的实时值）
  - `GREATEST` 取较大值，保证 `total >= reserved + used`，不违反 CHECK 约束
  - 缩容下限为当前已用量，已有资源继续运行，后续 `Try` 因 `reserved + used + amount > total` 返回 `ErrQuotaExceeded` 阻止新建

### 4.4 与 UpdateTenantQuota 的 SQL 对比

| 对比项 | `UpdateTenantQuota`（v1） | `UpsertTenantQuota`（v3） |
|--------|--------------------------|--------------------------|
| SQL 语句 | `UPDATE resource_quota SET total = GREATEST($3, reserved + used) WHERE ...` | `INSERT ... ON CONFLICT DO UPDATE SET total = GREATEST(EXCLUDED.total, reserved + used)` |
| 不存在维度 | `RowsAffected=0` → `ErrQuotaNotFound` | INSERT 新建行 |
| 已存在维度 | UPDATE clamp | ON CONFLICT DO UPDATE clamp |
| clamp 逻辑 | 相同（GREATEST(request, reserved+used)） | 相同 |

### 4.5 事务模型

| 方法 | 事务来源 | 失败处理 | 与 v1 一致 |
|---|---|---|---|
| `UpsertTenantQuota` | **自开** `WithPlatformTx` | 任一维度失败 → 事务回滚 → 所有维度不变 | 是（与 Create/Update 一致） |

### 4.6 编译期断言

`postgres_quota.go` 已有的三行编译期断言无需改动：

```go
var _ ports.QuotaService = (*PostgresQuota)(nil)
var _ ports.QuotaStoreService = (*PostgresQuota)(nil)
var _ ports.QuotaAdminService = (*PostgresQuota)(nil)
```

新增 `UpsertTenantQuota` 后，第三行断言会自动覆盖新方法签名，编译时即可发现签名不匹配。

***

## 5. Core API 契约改动（repo/api/openapi/v1.yaml）

### 5.1 设计原则

1. **契约先行**：先改 `v1.yaml`，再写 port/adapter/handler/SDK（CLAUDE.md §4 强制）
2. **只新增端点，不破坏现有契约**：现有 `PUT /admin/tenants/{tenant_id}/quota`（updateTenantQuota）保持不变，新增 `PUT /admin/tenants/{tenant_id}/quota/upsert`（upsertTenantQuota）
3. **幂等**：支持可选 `Idempotency-Key` header（CLAUDE.md §4.5）。v3 不新增请求级幂等存储；upsert 是 set-total 语义，相同请求重复执行后的最终 DB 状态一致
4. **鉴权**：路径在 `/admin/tenants/...` 下，已被 v1 扩展的 `scopeAllowedForPath` 放行（要求 platform scope），无需再改鉴权中间件

### 5.2 新增端点

在 `v1.yaml` 的 `/admin/tenants/{tenant_id}/quota` 路径块之后新增：

```yaml
  /admin/tenants/{tenant_id}/quota/upsert:
    put:
      operationId: upsertTenantQuota
      summary: 批量 Upsert 租户配额
      description: |
        批量 Upsert 指定租户多个维度的配额上限：已存在的维度更新 total，不存在的维度新建行。
        单次请求内所有维度在同一 DB 事务中原子完成，任一维度失败则整体回滚。
        - items.resource_type 必须在 resource_quota_meta 已注册且 enabled=true
        - items.total 未提供或为 0 时取 resource_quota_meta.default_quota；负数返回 VALIDATION_FAILED
        - 缩容时用 GREATEST(total, used+reserved) clamp 到 used+reserved，
          并在返回的 items 中将 tightened 置 true（新建维度不会触发收紧）
      tags: [QuotaAdmin]
      x-ani-rbac-scope: "scope:quota:write"
      parameters:
        - { name: tenant_id, in: path, required: true, schema: { type: string, format: uuid } }
        - { name: Idempotency-Key, in: header, required: false, schema: { type: string, minLength: 1, maxLength: 128 }, description: "客户端生成；同一 tenant_id 下 24 小时内去重" }
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/QuotaUpsertRequest' }
      responses:
        "200":
          description: Upsert 结果（含 tightened 标记）
          content:
            application/json:
              schema: { $ref: '#/components/schemas/Quota' }
        "400": { $ref: '#/components/responses/QuotaValidationFailed' }
        "401": { $ref: '#/components/responses/Unauthorized' }
        "403": { $ref: '#/components/responses/Forbidden' }
        "404": { $ref: '#/components/responses/TenantNotFound' }
        "422": { $ref: '#/components/responses/QuotaResourceNotRegistered' }
        "511": { $ref: '#/components/responses/QuotaUpdateUncertain' }
```

> **路径不冲突**：现有 `PUT /admin/tenants/{tenant_id}/quota` 与新增 `PUT /admin/tenants/{tenant_id}/quota/upsert` 是不同路径，Hertz 路由器按精确匹配分发，无冲突。

### 5.3 新增 schema

```yaml
    # ── 配额 Upsert（Core API v1，平台级配额管理）──────────────────────────
    QuotaUpsertRequest:
      type: object
      description: "批量 Upsert 租户配额请求（PUT /admin/tenants/{tenant_id}/quota/upsert）"
      required: [items]
      properties:
        items:
          type: array
          minItems: 1
          items:
            $ref: '#/components/schemas/QuotaUpsertItem'

    QuotaUpsertItem:
      type: object
      required: [resource_type]
      properties:
        resource_type: { type: string, description: "配额维度标识" }
        total:
          type: integer
          format: int64
          minimum: 0
          description: "配额上限值（>= 0）；未提供或为 0 时取 resource_quota_meta.default_quota"
```

> **复用响应 schema**：响应体复用已有的 `Quota` schema（含 `QuotaItem`，带 `tightened`/`unit`/`display_name`/`is_discrete` 字段），与 Create/Update 的响应结构一致。

### 5.3.1 新增错误响应 schema

```yaml
    # ── 配额 Upsert 提交不确定错误（Core API v1）──────────────────────────
    QuotaUpdateUncertain:
      description: |
        配额更新失败，无法确认事务提交状态（DB 宕机/连接断开等极端场景）。
        Services 层收到此错误后不得自动重试，应记录告警并触发人工核对流程。
        code=QUOTA_UPDATE_UNCERTAIN
      content:
        application/json:
          schema: { $ref: '#/components/schemas/ErrorResponse' }
```

### 5.4 错误码与失败响应 message 规范

| 失败类别 | HTTP | code | message 前缀 | 状态保证 |
|--------|------|------|---------------|----------|
| 租户不存在 | 404 | `TENANT_NOT_FOUND` | `租户不存在` | 未进入配额写入事务 |
| 事务内校验/写入失败 | 400 / 422 | `VALIDATION_FAILED` / `QUOTA_RESOURCE_NOT_REGISTERED` | `配额更新失败，已回滚。` | 所有维度均未被修改，租户配额保持请求前状态 |
| commit 阶段失败 | 511 | `QUOTA_UPDATE_UNCERTAIN` | `配额更新失败，无法确认事务状态，可能已部分提交，请联系管理员人工核对租户配额` | Core 无法确认事务是否提交，状态不确定 |

> 必须按 `配额core层upsert端点设计.md` 的失败语义返回明确 message：事务内失败返回“配额更新失败，已回滚”，commit 阶段失败返回“配额更新失败，无法确认事务状态...”。
> `QUOTA_NOT_FOUND`（404）不适用于 upsert（upsert 不区分"存在/不存在"，全部成功）。

响应示例：

```json
{
  "code": "QUOTA_RESOURCE_NOT_REGISTERED",
  "message": "配额更新失败，已回滚。resource_type 'gpu_count' 未在 resource_quota_meta 中注册或已禁用",
  "tenant_id": "uuid"
}
```

```json
{
  "code": "VALIDATION_FAILED",
  "message": "配额更新失败，已回滚。total 不能为负数，items[1].total=-1",
  "tenant_id": "uuid"
}
```

```json
{
  "code": "QUOTA_UPDATE_UNCERTAIN",
  "message": "配额更新失败，无法确认事务状态，可能已部分提交，请联系管理员人工核对租户配额",
  "tenant_id": "uuid"
}
```

***

## 6. Handler 实现（ani-gateway/internal/router/quota_resources.go）

### 6.1 路由注册

在 `registerQuotaResources` 中新增一行路由注册：

```go
func registerQuotaResources(v1 *route.RouterGroup, admin ports.QuotaAdminService) {
    api := quotaAPI{admin: admin}
    v1.POST("/admin/tenants/:tenant_id/quota", api.createTenantQuota)
    v1.PUT("/admin/tenants/:tenant_id/quota", api.updateTenantQuota)
    v1.GET("/admin/tenants/:tenant_id/quota", api.getTenantQuota)
    v1.DELETE("/admin/tenants/:tenant_id/quota", api.deleteTenantQuota)
    v1.GET("/admin/quota-meta", api.listQuotaMeta)
    v1.PUT("/admin/tenants/:tenant_id/quota/upsert", api.upsertTenantQuota) // 新增
}
```

### 6.2 请求结构

upsert 单独定义请求结构，避免与 create 语义耦合；`total` 使用 `*int64` 表示 JSON 字段可选：

```go
// quotaUpsertRequest 批量 upsert 租户配额请求。
type quotaUpsertRequest struct {
    Items []quotaUpsertItem `json:"items"`
}

type quotaUpsertItem struct {
    ResourceType ports.ResourceType `json:"resource_type"`
    Total        *int64             `json:"total,omitempty"`
}
```

> `total` 未提供时为 nil，handler 转换时将未提供映射为 0；显式传 0 也转换为 0。adapter 按 `QuotaItemInput` 语义统一处理：`total == 0` 取 `resource_quota_meta.default_quota`，`total < 0` 返回 `VALIDATION_FAILED`。

### 6.3 handler 方法

```go
// upsertTenantQuota 批量 Upsert 租户配额：已存在维度更新 total，不存在维度新建。
func (api *quotaAPI) upsertTenantQuota(ctx context.Context, c *app.RequestContext) {
    tenantID := c.Param("tenant_id")
    var req quotaUpsertRequest
    if err := c.BindJSON(&req); err != nil {
        writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid quota upsert request")
        return
    }
    items := make([]ports.QuotaItemInput, 0, len(req.Items))
    for _, it := range req.Items {
        var total int64
        if it.Total != nil {
            total = *it.Total
        }
        items = append(items, ports.QuotaItemInput{
            ResourceType: it.ResourceType,
            Total:        total,
        })
    }
    info, err := api.admin.UpsertTenantQuota(ctx, tenantID, items)
    if err != nil {
        writeQuotaUpsertError(c, tenantID, err)
        return
    }
    c.JSON(http.StatusOK, quotaResponse{TenantID: tenantID, Items: toQuotaItems(info)})
}
```

### 6.4 错误映射

`UpsertTenantQuota` 不能完全复用现有 `writeQuotaError` 的 `err.Error()` message，需要为本端点返回新版需求规定的明确中文失败语义。adapter 返回的包装错误需要携带中文上下文，handler 只负责添加固定前缀。

| adapter 返回 | HTTP | code | message |
|---|---|---|---|
| `ErrInvalid` | 400 | `VALIDATION_FAILED` | `配额更新失败，已回滚。` + 具体验证错误 |
| `ErrTenantNotFound` | 404 | `TENANT_NOT_FOUND` | `租户不存在: tenant_id=<tenant_id>` |
| `ErrQuotaResourceNotRegistered` | 422 | `QUOTA_RESOURCE_NOT_REGISTERED` | `配额更新失败，已回滚。resource_type '<resource_type>' 未在 resource_quota_meta 中注册或已禁用` |
| `ErrQuotaUpdateUncertain` | 511 | `QUOTA_UPDATE_UNCERTAIN` | `配额更新失败，无法确认事务状态，可能已部分提交，请联系管理员人工核对租户配额` |
| 其他 | 500 | `INTERNAL` | `internal server error` |

> `ErrQuotaNotFound` 和 `ErrQuotaAlreadyExists` 不会从 `UpsertTenantQuota` 返回（upsert 不区分存在性），但 `writeQuotaError` 已包含这些分支，无需改动。

建议为 upsert 单独封装 `writeQuotaUpsertError`，避免改变 Create/Update/Get/Delete 现有错误 message：

```go
func writeQuotaUpsertError(c *app.RequestContext, tenantID string, err error) {
    switch {
    case errors.Is(err, ports.ErrInvalid):
        writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "配额更新失败，已回滚。"+quotaErrorDetail(err, ports.ErrInvalid))
    case errors.Is(err, ports.ErrTenantNotFound):
        writeDemoError(c, http.StatusNotFound, "TENANT_NOT_FOUND", "租户不存在: tenant_id="+tenantID)
    case errors.Is(err, ports.ErrQuotaResourceNotRegistered):
        writeDemoError(c, http.StatusUnprocessableEntity, "QUOTA_RESOURCE_NOT_REGISTERED", "配额更新失败，已回滚。"+quotaErrorDetail(err, ports.ErrQuotaResourceNotRegistered))
    case errors.Is(err, ports.ErrQuotaUpdateUncertain):
        writeDemoError(c, http.StatusNetworkAuthenticationRequired, "QUOTA_UPDATE_UNCERTAIN", "配额更新失败，无法确认事务状态，可能已部分提交，请联系管理员人工核对租户配额")
    default:
        writeDemoError(c, http.StatusInternalServerError, "INTERNAL", "internal server error")
    }
}

func quotaErrorDetail(err error, sentinel error) string {
    detail := strings.TrimPrefix(err.Error(), sentinel.Error()+": ")
    if detail == err.Error() {
        return err.Error()
    }
    return detail
}
```

> `http.StatusNetworkAuthenticationRequired` = 511，Go 标准库已定义。

***

## 7. SDK 生成

契约改完后执行：

```bash
make gen-core-sdk    # 重新生成 sdks/core/go/anisdk/client.go
make validate-sdk-beta
```

生成后 Core SDK 的 `Operations` 切片会自动包含 `upsertTenantQuota`。

tenant-service 后续 PR 可在 `QuotaSvcClient` 接口新增 `UpsertQuota` 方法，通过 SDK 调用 `PUT /admin/tenants/{tenant_id}/quota/upsert`，替换现有的 Get + 分流 + Put + Create + 补偿回滚流程。

***

## 8. 测试策略

### 8.1 单元测试（fake/mock）

在 `repo/pkg/adapters/runtime/postgres_quota_admin_test.go` 中新增以下测试场景，复用已有的 `quotaFakeTx` / `quotaFakeStore` / `adminInfoRow` / `adminMetaRow` / `tenantExistsRow` 测试基础设施：

**UpsertTenantQuota：**

| # | 场景 | 验证什么 |
|---|---|---|
| 1 | 全部新建成功 | 两个维度均不存在 → INSERT 路径，回读 total=请求值，tightened=false |
| 2 | 全部更新成功 | 两个维度已存在 → ON CONFLICT DO UPDATE 路径，回读 total=请求值（total >= used+reserved），tightened=false |
| 3 | 混合（一新一旧） | 一个新建 + 一个更新，同一事务内完成，回读两个维度正确 |
| 4 | total 省略取 default | 请求 total=0 → 取 default_quota，回读 total=default_quota |
| 5 | 缩容 clamp + tightened | 请求 total=100，已有 used=150 → GREATEST clamp 到 150，回读 total=150，tightened=true |
| 6 | 缩容不触发 tightened | 请求 total=200，已有 used=50 → 200 >= 50，clamp 不生效，tightened=false |
| 7 | 租户不存在 | `requireTenantExists` 短路 → `ErrTenantNotFound` |
| 8 | 维度未注册 | `getMetaDefault` → `ErrQuotaResourceNotRegistered` |
| 9 | 维度 enabled=false | `getMetaDefault` → `ErrQuotaResourceNotRegistered` |
| 10 | items 为空 | `ErrInvalid`（不进入事务） |
| 11 | items 中 resource_type 重复 | `ErrInvalid`（不进入事务） |
| 12 | 任一维度失败整体回滚 | 第二维度 meta 未注册 → 返回 err，事务回滚（验证 fake tx 未提交） |
| 13 | SQL 语句校验 | 验证执行了 `INSERT INTO resource_quota` 且包含 `ON CONFLICT` 和 `GREATEST` |
| 14 | commit 阶段失败 → ErrQuotaUpdateUncertain | fake store 的 `WithPlatformTx` 返回包装 `ErrMetadataPlatformTxCommit` 的错误 → adapter 返回 `ErrQuotaUpdateUncertain`（非原 err） |
| 15 | 事务内失败不误判为 commit 失败 | `getMetaDefault` 返回 `ErrQuotaResourceNotRegistered` → adapter 原样上抛，**不**包装为 `ErrQuotaUpdateUncertain` |

**测试示例（场景 5：缩容 clamp + tightened）：**

```go
func TestPostgresQuotaAdminUpsertTenantQuotaTightened(t *testing.T) {
    tx := &quotaFakeTx{}
    now := time.Unix(100, 0)
    // 流程 QueryRow 顺序：租户 EXISTS → 维度 meta 校验
    tx.enqueueRows(
        tenantExistsRow(true),
        adminMetaRow(true, int64(100)),
    )
    // 回读：请求 total=100，但 used=150 → SQL clamp 到 150，回读 total=150
    tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
        adminInfoRow(testTenantID, string(ports.QuotaGPUCount), 150, 0, 150, "张", "GPU 数量", true, now),
    }})
    store := &quotaFakeStore{tx: tx}
    q := NewPostgresQuota(store)

    infos, err := q.UpsertTenantQuota(context.Background(), testTenantID, []ports.QuotaItemInput{
        {ResourceType: ports.QuotaGPUCount, Total: 100},
    })
    if err != nil {
        t.Fatalf("UpsertTenantQuota() error = %v", err)
    }
    if len(infos) != 1 {
        t.Fatalf("UpsertTenantQuota() len = %d, want 1", len(infos))
    }
    if !infos[0].Tightened {
        t.Fatalf("UpsertTenantQuota() tightened 应为 true（缩容被 clamp）")
    }
    if infos[0].Total != 150 {
        t.Fatalf("UpsertTenantQuota() total = %d, want 150（收紧后的 total）", infos[0].Total)
    }
    if !hasExec(tx, "ON CONFLICT") || !hasExec(tx, "GREATEST") {
        t.Fatalf("UpsertTenantQuota() 应使用 ON CONFLICT DO UPDATE + GREATEST clamp")
    }
}
```

### 8.2 集成测试（连 PG）

在 `repo/pkg/adapters/runtime/integration_test.go`（已有则追加）中新增，沿用 issue-011 的连接模式（`//go:build integration` build tag 隔离，双角色验证 RLS）：

| # | 场景 | 验证什么 |
|---|---|---|
| 1 | 全部新建 | 空租户 upsert 多维度 → 行被创建，reserved=0, used=0, total=请求值 |
| 2 | 全部更新 | 已有维度 upsert 新 total → total 被更新，reserved/used 不变 |
| 3 | 混合新建+更新 | 一新一旧同一请求 → 两维度均正确 |
| 4 | total 省略取 default | total=0 → 取 meta.default_quota |
| 5 | 缩容 clamp | total < used+reserved → GREATEST clamp，tightened=true |
| 6 | 原子性 | 第二维度未注册 → 整体回滚，第一维度不变 |
| 7 | RLS bypass | 管理员连接（WithPlatformTx）可 upsert 任意租户 |
| 8 | 并发 upsert | 两个并发 upsert 同一维度 → 行锁串行化，无数据损坏 |
| 9 | 重复请求幂等性 | 相同 upsert 请求重复执行 → 最终 DB 状态一致（v3 不新增请求级 replay 存储） |
| 10 | SDK 端到端 | 启动 ani-gateway，SDK 调 PUT upsert → DB 验证 |
| 11 | commit 阶段失败 → 511 | 模拟 DB 连接断开（测试中关闭 PG 连接池或注入 commit 失败）→ 返回 511 `QUOTA_UPDATE_UNCERTAIN` |

**验收命令：**

单元测试（无需 PG）：

```bash
cd repo
make test                          # 单元测试通过
make validate-architecture         # 架构边界
git diff --check                   # 无空白错误
```

集成测试（连接真实 PG）：

```bash
cd repo

ANI_TEST_ADMIN_DSN="$ANI_TEST_ADMIN_DSN" `
ANI_TEST_TENANT_DSN="$ANI_TEST_TENANT_DSN" `
go test ./pkg/adapters/runtime/ -v -run 'TestIntegrationQuota.*Upsert' -tags integration -timeout 60s
```

> 连接参数与 `repo/development-records/quota-service.md` issue-011 集成测试一致。

***

## 9. 风险与注意事项

| 风险 | 应对 |
|---|---|
| `ON CONFLICT DO UPDATE` 中引用 `resource_quota.reserved` 的列名 | SQL 中 `resource_quota.reserved` 和 `resource_quota.used` 引用的是冲突行（已存在行）的当前值，PG 语法支持；`EXCLUDED.total` 引用的是 INSERT 试图插入的值。两者均正确 |
| 新建行 reserved=0, used=0 时 GREATEST 是否误 clamp | `GREATEST(total, 0+0) = GREATEST(total, 0) = total`（total >= 0），clamp 不生效，新建行 total = 请求值，正确 |
| CHECK 约束 `reserved + used <= total` 在 ON CONFLICT DO UPDATE 时是否校验 | PG 在 ON CONFLICT DO UPDATE 后会校验 CHECK 约束。GREATEST 保证 `total >= reserved+used`，不违反 |
| items 中 resource_type 重复 | adapter 层前置校验（map 去重），返回 `ErrInvalid`，不进入事务 |
| 与 UpdateTenantQuota 的 SQL clamp 一致性 | 两者都用 `GREATEST(request, reserved+used)`，clamp 逻辑完全一致，区别仅在 INSERT vs UPDATE 路径选择 |
| 路由冲突 | `PUT /admin/tenants/:tenant_id/quota` 与 `PUT /admin/tenants/:tenant_id/quota/upsert` 是不同路径，Hertz 精确匹配无冲突 |
| 鉴权 | 路径在 `/api/v1/admin/` 下，v1 已扩展 `scopeAllowedForPath` 要求 platform scope，无需再改鉴权中间件 |
| SDK 自动生成覆盖 | `sdks/core/go/anisdk/client.go` 是 DO NOT EDIT 文件，改契约后必须 `make gen-core-sdk`，否则 `validate-sdk-beta` 报漂移 |
| 编译期断言覆盖 | `var _ ports.QuotaAdminService = (*PostgresQuota)(nil)` 自动覆盖新方法，编译即校验 |
| tenant-service 侧适配不在本任务范围 | 本任务只做 Core 侧端点 + adapter；tenant-service 的 `QuotaSvcClient.UpsertQuota` + 替换 Get+分流+双调用由后续 PR 负责 |
| commit 阶段错误误判 | `WithPlatformTx` 的 Commit 错误必须包装 `ports.ErrMetadataPlatformTxCommit`；adapter 使用 `errors.Is` 判定，避免依赖错误字符串 |
| `ErrQuotaUpdateUncertain` 是新增哨兵错误 | 需在 `repo/pkg/ports/errors.go` 新增；handler 的 `writeQuotaError` 需追加 511 映射分支；不影响现有错误码语义 |
| HTTP 511 语义 | 511 Network Authentication Required（RFC 6585）原义是代理认证，此处复用为"事务状态不确定"。语义偏移但 HTTP 标准无更合适的状态码（5xx 均为服务端错误，511 可表示"需外部介入"）。OpenAPI description 中明确说明其含义 |

***

## 10. 实施顺序

1. **改 Core API 契约**（§5）：`v1.yaml` 新增 `PUT /admin/tenants/{tenant_id}/quota/upsert` 路径 + `QuotaUpsertRequest` / `QuotaUpsertItem` schema
2. **改 port**（§3）：`pkg/ports/quota_admin.go` 的 `QuotaAdminService` interface 新增 `UpsertTenantQuota` 签名
3. **实现 adapter**（§4.2）：`postgres_quota.go` 新增 `UpsertTenantQuota`，复用 `requireTenantExists` / `getMetaDefault` / `quotaInfoByTypes`
4. **实现 handler**（§6）：`quota_resources.go` 新增 `upsertTenantQuota` handler + 路由注册
5. **单元测试**（§8.1）：fake/mock 测试
6. **生成 SDK**（§7）：`make gen-core-sdk`
7. **集成测试**（§8.2）：连 PG 验证 upsert + clamp + RLS + 原子性
8. **全量验收**（§8.2 验收命令）

***

## 11. 变更记录

| 日期 | 变更 |
|---|---|
| 2026-08-18 | 初版：基于 `配额core层upsert端点设计.md`，新增 `UpsertTenantQuota` 方法 + `PUT /admin/tenants/{tenant_id}/quota/upsert` 端点，复用 v1 的 GREATEST clamp + quotaInfoByTypes，消除 Services 层 Get+分流+双调用+补偿回滚 |
| 2026-08-18 | v3.1：根据 `配额core层upsert端点设计.md` 更新——(1) `Idempotency-Key` 改为可选；(2) 新增 `ErrQuotaUpdateUncertain` 哨兵错误 + HTTP 511 响应，区分事务内失败（已回滚）与 commit 阶段失败（状态不确定）；(3) `WithPlatformTx` 包装 `ErrMetadataPlatformTxCommit`，adapter 用 `errors.Is` 捕获 commit 失败，不做字符串匹配；(4) handler 新增 `writeQuotaUpsertError` 返回明确中文失败 message；(5) 测试补充 commit 失败场景（单元 #14/#15 + 集成 #11） |
