# 配额服务 v2：TryTx / TryManyTx 新增外部事务变体

> 状态: 计划草案
> 创建日期: 2026-08-12
> 负责人: kjs
> 前置文档: `通用资源配额与计量落地方案-0812.md` §4.2、§5.1.1、§5.2.1；`plan-quota-service.md`（v1 已合并入 main）

***

## 1. 背景与任务边界

### 1.1 为什么需要 v2

v1（`plan-quota-service.md`）已合并入 main，实现了 `QuotaService`（Try / TryMany / Confirm / Cancel / Release）、`QuotaStoreService`、`QuotaAdminService` 三个 port 及其 PG adapter。

TCC 调用方（创建实例流程）现在需要**在创建实例同事务内做配额预占**，而不是 TryMany 自开事务。原因：

- v1 的 `Try` / `TryMany` **自开事务**（`WithTenantTx`），预占与实例落库是两个独立事务。若实例落库事务在预占提交后失败，预占变成孤儿，依赖 TTL worker 回收。
- 0812 方案要求：**锁 allocated → 锁 quota → 校验 → TryManyTx → InsertPendingTx 原子提交**，即预占和实例行在同一事务内，任一失败整体回滚，无悬挂预占。

因此需要新增接受外部 tx 的 `TryTx` / `TryManyTx` 两个方法。

### 1.2 本任务做什么

在 `QuotaService` interface 新增两个方法，并在 `PostgresQuota` adapter 中实现：

| 方法 | 签名 | 事务来源 | 用途 |
|---|---|---|---|
| `TryTx` | `(ctx, tx MetadataTx, req QuotaTryRequest) (QuotaReservation, error)` | **接收外部 tx** | 单维度预占，与业务行同事务 |
| `TryManyTx` | `(ctx, tx MetadataTx, reqs []QuotaTryRequest) ([]QuotaReservation, error)` | **接收外部 tx** | 多维度批量预占，与实例落库同事务 |

### 1.3 本任务不做什么

| 不做项 | 原因 |
|---|---|
| 改动 Confirm / Cancel / Release | 已在 v1 实现且已接收外部 tx，无需改动 |
| 改动 QuotaStoreService / QuotaAdminService | 与本任务无关 |
| 改动三张表 migration | 表结构不变，state 枚举不变 |
| 改动 Core API 契约 / handler / SDK | TryTx / TryManyTx 是 Core 内部 port 调用，不暴露 REST |
| TTL 孤儿预占回收 worker | 后续 PR |
| WorkloadInstanceStore.UpsertStatusTx | 后续 PR（0812 方案 PR-3） |

***

## 2. 交付物清单

| 文件 | 类型 | 说明 |
|---|---|---|
| `repo/pkg/ports/quota.go` | 修改 | `QuotaService` interface 新增 `TryTx` / `TryManyTx` 两个方法签名 |
| `repo/pkg/adapters/runtime/postgres_quota.go` | 修改 | 实现 `TryTx` / `TryManyTx`；复用已有 `tryInTx` |
| `repo/pkg/adapters/runtime/postgres_quota_test.go` | 修改 | 新增 TryTx / TryManyTx 单元测试 |
| `kjs-study/配额操作任务/plan-quota-service-v2.md` | 本文件 | 本任务方案 |

***

## 3. Port 契约改动

### 3.1 QuotaService interface 新增方法

文件：`repo/pkg/ports/quota.go`

在现有 `QuotaService` interface 中新增 `TryTx` 和 `TryManyTx`：

```go
type QuotaService interface {
    // --- v1 已有（自开事务） ---

    // 单维度预占。自开 WithTenantTx，单行原子 UPDATE + lazy init。
    Try(ctx context.Context, req QuotaTryRequest) (QuotaReservation, error)

    // 多维度批量预占。单事务内循环 tryInTx，任一失败则整体回滚，无悬挂预占。
    TryMany(ctx context.Context, reqs []QuotaTryRequest) ([]QuotaReservation, error)

    // --- v2 新增（接收外部 tx） ---

    // 单维度预占，接受外部 tx。供需要和业务行同事务的单维度预占场景。
    // 调用方负责开 WithTenantTx 并注入 TenantContext；本方法在传入的 tx 内执行预占逻辑。
    // 任一维度不足/未注册 → 返回 err，由调用方的外层事务统一回滚。
    TryTx(ctx context.Context, tx MetadataTx, req QuotaTryRequest) (QuotaReservation, error)

    // 多维度批量预占，接受外部 tx。供创建实例同事务调用：
    // 锁 allocated → 锁 quota → 校验 → TryManyTx → InsertPendingTx 原子提交。
    // 调用方负责开 WithTenantTx 并注入 TenantContext；本方法只负责在传入的 tx 内循环 tryInTx。
    // 任一维度不足/未注册 → 返回 err，由调用方的外层事务统一回滚。
    TryManyTx(ctx context.Context, tx MetadataTx, reqs []QuotaTryRequest) ([]QuotaReservation, error)

    // --- v1 已有（接收外部 tx，不变） ---

    // 预占转实扣。接收外部 tx，幂等。
    Confirm(ctx context.Context, tx MetadataTx, txIDs []string, resourceRef string) error

    // 释放预占。接收外部 tx，幂等。
    Cancel(ctx context.Context, tx MetadataTx, txIDs []string) error

    // 释放已实扣。接收外部 tx，幂等。
    Release(ctx context.Context, tx MetadataTx, txIDs []string) error
}
```

### 3.2 调用方契约

与 v1 的 Confirm / Cancel / Release 相同：

- 传入的 `ctx` **必须带 TenantContext**（用 `WithTenantTx` 开事务即自动注入，或手动 `Begin` + `SetDBTenant`）
- 若传入的 ctx 无 TenantContext，`SetDBTenant` 会 panic（`types.FromContext`），RLS 也会拒绝所有行
- `TryTx` / `TryManyTx` **不自己 Begin / Commit**，事务由调用方控制
- 失败时只返回 err，**不自己回滚**，由调用方的外层事务统一回滚

### 3.3 为什么不改 Confirm / Cancel / Release

它们在 v1 已经是接收外部 tx 的签名，无需改动。v2 只补齐 Try 侧的外部事务变体，使 Try-TryMany 侧也支持同事务调用。

***

## 4. Adapter 实现

### 4.1 核心复用：tryInTx 已就绪

v1 已经将单维度预占逻辑提取为 `tryInTx(ctx, tx, req)` 内部方法，Try 和 TryMany 共用。v2 的 `TryTx` / `TryManyTx` 直接复用 `tryInTx`，**无需新增任何 SQL**。

`tryInTx` 当前实现（`repo/pkg/adapters/runtime/postgres_quota.go`，已合并入 main）：

```go
func (q *PostgresQuota) tryInTx(ctx context.Context, tx ports.MetadataTx, req ports.QuotaTryRequest) (ports.QuotaReservation, error) {
    var res ports.QuotaReservation

    if req.Amount <= 0 {
        return res, ports.ErrInvalid
    }

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
        return res, err
    }
    if !enabled {
        return res, ports.ErrQuotaResourceNotRegistered
    }

    // 2. lazy init：无配置行则用 default_quota 建行
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
    if tag.RowsAffected == 0 {
        return res, ports.ErrQuotaExceeded
    }

    // 4. 插入预占流水
    err = tx.QueryRow(ctx, `
        INSERT INTO resource_reservations (tenant_id, resource_type, amount, expires_at)
        VALUES ($1, $2, $3, NOW() + INTERVAL '10 minutes')
        RETURNING tx_id::text, expires_at
    `, req.TenantID, req.ResourceType, req.Amount).Scan(&res.TxID, &res.ExpiresAt)
    return res, err
}
```

### 4.2 TryTx 实现

文件：`repo/pkg/adapters/runtime/postgres_quota.go`

```go
// TryTx 单维度预占，接受外部 tx。不自己开事务，在调用方传入的 tx 内执行预占逻辑。
// 调用方负责开 WithTenantTx 并注入 TenantContext；失败时只返回 err，由外层事务统一回滚。
func (q *PostgresQuota) TryTx(ctx context.Context, tx ports.MetadataTx, req ports.QuotaTryRequest) (ports.QuotaReservation, error) {
    return q.tryInTx(ctx, tx, req)
}
```

### 4.3 TryManyTx 实现

文件：`repo/pkg/adapters/runtime/postgres_quota.go`

```go
// TryManyTx 多维度批量预占，接受外部 tx。不自己开事务，在调用方传入的 tx 内循环 tryInTx。
// 调用方负责开 WithTenantTx 并注入 TenantContext；任一维度失败只返回 err，由外层事务统一回滚。
func (q *PostgresQuota) TryManyTx(ctx context.Context, tx ports.MetadataTx, reqs []ports.QuotaTryRequest) ([]ports.QuotaReservation, error) {
    if len(reqs) == 0 {
        return nil, nil
    }
    var reservations []ports.QuotaReservation
    for _, req := range reqs {
        res, err := q.tryInTx(ctx, tx, req)
        if err != nil {
            return nil, err // 不自己回滚，由调用方的外层事务统一回滚
        }
        reservations = append(reservations, res)
    }
    return reservations, nil
}
```

### 4.4 事务模型对比（v1 + v2 完整视图）

| 方法 | 事务来源 | 失败处理 | 与业务行同事务 |
|---|---|---|---|
| `Try` (v1) | **自开** `WithTenantTx` | 事务自动回滚 | 否 |
| `TryMany` (v1) | **自开** `WithTenantTx` | 事务自动回滚 | 否 |
| **`TryTx` (v2)** | **接收外部 tx** | 返回 err，由外层事务回滚 | **是** |
| **`TryManyTx` (v2)** | **接收外部 tx** | 返回 err，由外层事务回滚 | **是** |
| `Confirm` (v1) | 接收外部 tx | 返回 err，由外层事务回滚 | 是 |
| `Cancel` (v1) | 接收外部 tx | 返回 err，由外层事务回滚 | 是 |
| `Release` (v1) | 接收外部 tx | 返回 err,由外层事务回滚 | 是 |

### 4.5 编译期断言

`postgres_quota.go` 已有的三行编译期断言无需改动：

```go
var _ ports.QuotaService = (*PostgresQuota)(nil)
var _ ports.QuotaStoreService = (*PostgresQuota)(nil)
var _ ports.QuotaAdminService = (*PostgresQuota)(nil)
```

新增 `TryTx` / `TryManyTx` 后，第一行断言会自动覆盖新方法签名，编译时即可发现签名不匹配。

***

## 5. 测试策略

### 5.1 单元测试（fake/mock）

在 `repo/pkg/adapters/runtime/postgres_quota_test.go` 中新增以下测试场景：

**TryTx：**
- 成功：meta enabled、lazy init、预占成功，返回有效 `QuotaReservation`
- 失败：meta disabled → `ErrQuotaResourceNotRegistered`
- 失败：meta 不存在 → `ErrQuotaResourceNotRegistered`
- 失败：余量不足 → `ErrQuotaExceeded`
- 失败：amount <= 0 → `ErrInvalid`
- **不自己开事务**：验证传入的 tx 被直接使用，无额外 Begin/Commit 调用
- 失败时不回滚：验证返回 err 后不调用 tx.Rollback（回滚由外层负责）

**TryManyTx：**
- 成功：多维度全占成功，返回所有 reservations
- 原子性：第二维度不足 → 返回 err + nil reservations（已成功的预占由外层回滚）
- 空入参：`reqs = nil` 或 `len(reqs) == 0` → 返回 `nil, nil`
- 租户一致性：不强制校验 tenant_id 一致（与 v1 TryMany 不同，TryManyTx 由调用方保证）
- **不自己开事务**：验证传入的 tx 被直接使用

### 5.2 集成测试（连 PG）

在 `repo/pkg/adapters/runtime/integration_test.go`（如已有则追加）中新增：

| # | 场景 | 验证什么 |
|---|---|---|
| 1 | TryTx 在外部 WithTenantTx 内成功预占 | RLS 放行，reserved 增加，流水插入 |
| 2 | TryTx 外层事务回滚 → 预占回滚 | 回滚后 reserved 不变，无流水残留 |
| 3 | TryManyTx 在外部 WithTenantTx 内多维度预占成功 | 多维度 reserved 均增加 |
| 4 | TryManyTx 第二维度不足 → 外层回滚 | 所有维度 reserved 不变，无流水残留 |
| 5 | TryTx / TryManyTx + Confirm 同事务端到端 | 预占 → Confirm → used 增加、reserved 减少 |
| 6 | 并发 TryTx 不超卖 | 两个 tx 并发，PG 行锁串行化，第二个 ErrQuotaExceeded |
| 7 | RLS 隔离：租户 A 的 tx 无法 TryTx 租户 B 的配额 | RLS 拦截 |

### 5.3 验收命令

**单元测试（无需 PG）：**

```bash
cd repo
make test                          # 单元测试通过
make validate-architecture         # 架构边界
git diff --check                   # 无空白错误
```

**集成测试（连接真实 PG）：**

集成测试沿用 issue-011 的连接模式：`//go:build integration` build tag 隔离，通过 `ANI_TEST_ADMIN_DSN` / `ANI_TEST_TENANT_DSN` 环境变量指定 DSN，默认指向真实 PG 实例 `10.10.1.66:30945`，双角色（admin `ani` + tenant `ani_app_user`）验证 RLS。

```bash
cd repo

# 集成测试（需真实 PG 连接，build tag 隔离不阻塞默认 make test）
ANI_TEST_ADMIN_DSN="postgres://ani:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable" `
ANI_TEST_TENANT_DSN="postgres://ani_app_user:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable" `
go test ./pkg/adapters/runtime/ -v -run 'TestIntegrationQuota.*TryTx|TestIntegrationQuota.*TryManyTx' -tags integration -timeout 60s
```

> 连接参数与 `repo/development-records/quota-service.md` issue-011 集成测试一致：
> - admin DSN：`postgres://ani:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable`（superuser，走 `WithPlatformTx` bypass RLS）
> - tenant DSN：`postgres://ani_app_user:ani_dev_password@10.10.1.66:30945/ani?sslmode=disable`（普通角色，走 `WithTenantTx` 设 `app.current_tenant_id` 触发 RLS self）
> - `-tags integration`：build tag 隔离，默认 `make test` 不触发集成测试
> - `-timeout 60s`：真实 PG 连接超时保护

***

## 6. 风险与注意事项

| 风险 | 应对 |
|---|---|
| 调用方忘记注入 TenantContext | 与 v1 Confirm/Cancel/Release 相同契约：ctx 无 TenantContext → SetDBTenant panic。在方法文档注释中明确标注，不额外加运行时守卫 |
| TryManyTx 不校验 tenant_id 一致 | 与 TryMany（v1）不同：TryManyTx 是同事务内部调用，调用方已保证租户一致，不加冗余校验 |
| 外层事务未回滚导致悬挂预占 | 调用方必须用 `WithTenantTx` 包裹；若调用方手动 Begin 后忘记 Rollback，是调用方 bug。TTL worker 作为兜底 |
| tryInTx 已被 Try / TryMany 使用，改动影响面 | v2 **不改 tryInTx**，只新增两个调用方，零侵入 |
| 编译期断言覆盖 | `var _ ports.QuotaService = (*PostgresQuota)(nil)` 自动覆盖新方法，编译即校验 |

***

## 7. 实施顺序

1. **改 port**（§3.1）：`pkg/ports/quota.go` 的 `QuotaService` interface 新增 `TryTx` / `TryManyTx` 签名
2. **实现 adapter**（§4.2、§4.3）：`postgres_quota.go` 新增 `TryTx` / `TryManyTx`，复用 `tryInTx`
3. **单元测试**（§5.1）：fake/mock 测试
4. **集成测试**（§5.2）：连 PG 验证同事务回滚 + RLS
5. **全量验收**（§5.3）

***

## 8. 变更记录

| 日期 | 变更 |
|---|---|
| 2026-08-12 | 初版：基于 0812 方案 §5.1.1 / §5.2.1，新增 TryTx / TryManyTx 两个接收外部 tx 的预占变体 |
