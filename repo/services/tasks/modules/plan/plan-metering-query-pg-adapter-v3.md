# 计量查询 Postgres Adapter 实现方案 V3

> 范围:计量查询后端实现(从 PG 读取 `metering_usage_records` 表)
> 不含:token 写入持久化(token 写入另立批次)、instance 采集写入主体(已由 metering-service 实现,本批次仅顺路补 period 时区 `.UTC()`)
> 基线代码读取时间:2026-08-18(鉴权部分于 2026-08-25 按鉴权四批次合入后的代码基线修订)

> **V3 相对 V2**:基于评审复盘落地 14 项修订(时区契约、handler 输入校验、契约完整性、设计清理、文档措辞),详见 §9 变更记录 2026-08-26 三条目。V2 文档保持不动作为对照基线。

> **本文档依据(必读):**
>
> 1. 原始方案:plan-metering-query-pg-adapter.md(2026-08-18 基线,鉴权章节基于当时的 `scopeAllowedForPath` 现状编写)——本 V2 是该方案的完整继承,除鉴权相关章节外,其余内容(PgMeteringService、port 扩展、SQL 聚合、RLS bypass、装配注入)与原方案一致,实施时以本文档为准。
> 2. 鉴权依据:[plan-authz-policy-compat-contract-pilot-v4.md](plan-authz-policy-compat-contract-pilot-v4.md)(鉴权四批次 AUTHZ-POLICY-A/B0/B1/C 实施计划,已全部合入并验证)——本 V2 的 §3.6 鉴权改动基于该方案落地后的代码事实(V2 pilot 链路、pilot allowlist 冻结校验、generated/legacy 分流)重新设计。
>
> **V2 与原方案的唯一实质差异**:平台查询 `getPlatformMeteringUsage` 的鉴权方式。原方案 §3.6 计划在 `scopeAllowedForPath` 手工前缀清单中新增 `/api/v1/metering/usage/platform`;V2 改为方案 B——为该 operation 增加 `x-ani-authz` 并扩展 pilot allowlist 至 `{listQuotaMeta, getPlatformMeteringUsage}`,平台隔离由 V2 链路(policy boundary=platform)承载。该决策已由用户于 2026-08-25 拍板,理由见 §0 决策记录和 §6 风险表。

> **已有代码说明**：当前 `LocalMeteringService` 已将 token 用量报告写入进程内存 map
> （`local_metering_service.go` 中的 `reports` 和 `idempotency` 字段）。该 token 内存写入
> 逻辑是已有实现，本批次不做修改。本批次新增的 `PgMeteringService` 负责统一从
> PostgreSQL 的 `metering_usage_records` 表查询 instance 资源用量和 token 用量，两类数据
> 使用相同的查询接口和数据表，通过 `resource_type` 请求参数区分。当前 token 尚未写入
> PostgreSQL，因此 token 查询暂时返回空结果；这不改变查询统一走 PostgreSQL 的设计。

---

## 0. 决策记录(已与用户确认)

| 决策点 | 选择 | 理由 |
|---|---|---|
| **查询范围** | 统一查询(instance 资源 + token) | `PgMeteringService` 统一查询 `metering_usage_records`,通过 `resource_type` 区分类型。token 尚未写入 PG,因此当前 token 查询返回空结果 |
| **本批次只做查询** | 是 | token 写入环节未实现,本批次只实现查询 adapter,不做写入 |
| **平台查询** | 本批次实现 | 前端 BOSS 已在调用 `GET /metering/usage/platform`,需要落地后端 |
| **RLS bypass 方案** | 方案 A:复用 `ani_metering_writer` 角色 | 已有 BYPASSRLS 角色 + `GRANT SELECT`,零新增 migration、零新增 BYPASSRLS 角色、沿用 quota_runtime 范式新建连接池(metering 与 quota 是独立业务领域,按项目既有范式独立连接池)。该角色语义是"metering 数据管理角色",读写同属数据管理职责域,谁写谁读,不违反最小权限(见 §6 风险说明) |
| **时间过滤字段** | `period`(TEXT 字符串比较) | period 是采集周期标识(分钟对齐),语义准确。用 `to_char` 把前端 RFC3339 转成同格式字符串后比较,走 period 字符串排序 |
| **聚合位置** | SQL 层聚合 | `GROUP BY SUBSTR(period,1,N)` + `SUM(quantity)` 在 DB 算,只返回聚合结果,数据量小 |
| **period 比较区间** | 闭区间 `>=` / `<=`,后端不改 | period 是分钟对齐周期标识,闭区间语义自洽。`defaultTimeRange()` 传当前时刻,闭区间能查到当前分钟周期;DateRangePicker 精度到天时若需"含 end 日期全天",由前端将 end_time 传 end 日期 23:59:59,后端闭区间无需改动 |
| **`SET LOCAL ROLE` 失败处理** | 返回错误,不降级 | 角色不存在属部署问题(migration 未执行/权限配置错),降级会走 RLS 返回空或单租户数据,错误数据比报错更危险。与写入侧 `persistRecords` 的 `SET ROLE` 失败处理一致(直接 return err) |
| **平台查询鉴权(2026-08-25 修订)** | 方案 B:`x-ani-authz` + V2 pilot 扩展 | 鉴权四批次合入后,`scopeAllowedForPath` 手工前缀清单已被 V4 迁移方案认定为待消除技术债;`getPlatformMeteringUsage` handler 本批次落地,直接进入 V2 链路(boundary=platform 承载隔离),不修改 `scopeAllowedForPath`。pilot allowlist 由 `{listQuotaMeta}` 扩展为 `{listQuotaMeta, getPlatformMeteringUsage}` |

---

## 1. 问题陈述

### 1.1 当前状态(来自代码实测)

**查询链路:**
- `GET /api/v1/metering/usage` → [metering_resources.go:71](../../../../../repo/services/ani-gateway/internal/router/metering_resources.go#L71) `queryUsage` handler
- handler 调 `api.service.QueryUsage` → [local_metering_service.go:43](../../../../../repo/pkg/adapters/runtime/local_metering_service.go#L43)
- 当前 local adapter 从**内存 map** 聚合已有 token 报告,只能查 token(input/output/total),**查不到 instance 资源用量**
- 本批次接入 `PgMeteringService` 后,租户和平台查询统一从 `metering_usage_records` 查询 instance/token 数据
- local adapter `RealProvider: false`,保留作 dev/CI fallback

**平台查询:**
- `GET /api/v1/metering/usage/platform` → **handler 不存在**
- [router.go:58](../../../../../repo/services/ani-gateway/internal/router/router.go#L58) `registerMetering(v1)` 只注册了 2 个路由(usage + token-usage),没有 platform 路由
- OpenAPI 契约 [v1.yaml:7777](../../../../../repo/api/openapi/v1.yaml#L7777) 已声明 `getPlatformMeteringUsage`,但 handler 未落地
- 前端 BOSS [usePlatformUsageQuery.ts:39](../../../../../repo/frontends/boss/src/features/platform-metering/usePlatformUsageQuery.ts#L39) 已在调用此接口

**数据表:**
- `metering_usage_records` 表([20260731_001](../../../../../repo/deploy/migrations/20260731_001_metering_usage.sql))已建,通用计量表
- 写入侧已由 metering-service 实现([metering_collection_service.go:218](../../../../../repo/services/metering-service/internal/service/metering_collection_service.go#L218) `INSERT INTO metering_usage_records`)
- 写入的 `resource_type` 有 3 种:`instance_gpu_seconds` / `instance_cpu_seconds` / `instance_memory_gib_seconds`
- `period` 格式:`2026-08-18T10:05`(ISO 8601 分钟对齐字符串)
- `quantity` 存的是一个采集周期(默认 60s)内的增量量
- token 类型(`token_input`/`token_output`/`token_total`)暂未写入,但表设计支持

**装配:**
- `registerMetering(v1)` 无参签名,`newMeteringAPI()` 硬编码 `NewLocalMeteringService()`
- `RegisterOptions` 结构体([router.go:13](../../../../../repo/services/ani-gateway/internal/router/router.go#L13))无 `MeteringService` 字段
- `main.go` 无 metering runtime 装配

**鉴权:**
- 鉴权四批次(AUTHZ-POLICY-A/B0/B1/C)已合入并验证:`listQuotaMeta` 走 V2 pilot 链路,其余 operation 走 legacy middleware
- metering 三个 operation 在生成注册表中均为 `PolicySourceLegacy`([zz_generated_core_policies.go:857-877](../../../../../repo/services/ani-gateway/internal/authz/zz_generated_core_policies.go#L857-L877)),无 `x-ani-authz`
- `getPlatformMeteringUsage` 的平台隔离若走 legacy 链路,需修改 `scopeAllowedForPath` 手工前缀清单——该清单已被 V4 迁移方案(`gateway-openapi-authorization-policy-migration-v4.md` §3.1)认定为待消除技术债,禁止继续扩大
- 本批次采用方案 B:为 `getPlatformMeteringUsage` 增加 `x-ani-authz` 并扩展 pilot allowlist,平台隔离由 V2 链路(`ValidatePrincipal` + `CheckPermissionV2` + policy boundary)承载,不修改 `scopeAllowedForPath`(详见 §3.6)

### 1.2 为什么需要新 adapter

- `LocalMeteringService` 的 token 写入和 local fallback 查询均使用已有内存 map,查不到 PG 里的 instance 资源用量
- `metering_usage_records` 表已有数据(由 metering-service 采集写入),需要从 PG 读取
- 需要新增一个 postgres adapter 实现从 PG 查询,保留 local adapter 作 dev/CI fallback

### 1.3 为什么现在做

- 前端 BOSS 已在调用平台查询接口,后端未实现
- `metering_usage_records` 表已有 instance 资源数据,需要查询能力落地
- `LocalMeteringService` 只能查 token 内存数据,无法满足生产查询需求;新增 PG adapter 统一查询 instance 和 token 资源类型

---

## 2. 架构约束(强制遵守)

来源 [CLAUDE.md](../../../../../CLAUDE.md):

1. **port/adapter 边界**:`ports.MeteringService` 是产品能力抽象,新 adapter 必须 `var _ ports.MeteringService = (*XxxMeteringService)(nil)` 声明
2. **不直接依赖组件 SDK**:adapter 通过 `ports.MetadataStore` 执行 SQL,不直接 import pgx
3. **local profile ≠ 生产**:不改变 `local_metering_service.go` 的既有 token 内存写入逻辑,保留作 dev/CI fallback
4. **API 契约先行**:先修改 `v1.yaml` 移除租户侧不受支持的 `group_by=az`,再按契约实现 handler 和 adapter
5. **Karpathy 原则**:不引入预聚合表/物化视图(当前问题用不到)

---

## 3. 方案设计

### 3.1 总览

```
GET /api/v1/metering/usage
  → [Gateway middleware] legacy 链路(ValidateToken + scopeAllowedForPath,已有,scope=tenant)
  → api.service.QueryUsage
  → [新 adapter] PgMeteringService.QueryUsage
  → ports.MetadataStore.WithTenantTx  (set tenant context → RLS 生效)
  → SELECT ... FROM metering_usage_records WHERE period >= ... GROUP BY ...
  → 行映射为 ports.MeteringUsageRecord

GET /api/v1/metering/usage/platform  (新增 handler)
  → [Gateway middleware] V2 pilot 链路(ValidatePrincipal + CheckPermissionV2,
    policy boundary=platform 承载平台隔离,不依赖 scopeAllowedForPath,详见 §3.6)
  → api.service.QueryPlatformUsage
  → [新 adapter] PgMeteringService.QueryPlatformUsage
  → ports.MetadataStore.WithPlatformTx + SET LOCAL ROLE ani_metering_writer (BYPASSRLS)
  → SELECT ... FROM metering_usage_records (跨租户) GROUP BY ...
  → 行映射为 ports.MeteringUsageRecord(items[].tenant_id 必填)
```

### 3.2 Port 接口扩展

**改动文件:** [ports/metering.go](../../../../../repo/pkg/ports/metering.go)

当前 `MeteringService` 接口只有 `QueryUsage` + `ReportTokenUsage`,没有平台查询方法。需要新增 `QueryPlatformUsage`:

```go
type MeteringService interface {
    QueryUsage(ctx context.Context, request MeteringUsageQueryRequest) (MeteringUsageResult, error)
    QueryPlatformUsage(ctx context.Context, request MeteringUsageQueryRequest) (MeteringUsageResult, error)  // 新增
    ReportTokenUsage(ctx context.Context, request TokenUsageReportRequest) (TokenUsageReportRecord, error)
}
```

**`MeteringUsageQueryRequest` 新增字段:**

```go
type MeteringUsageQueryRequest struct {
    TenantID     string
    StartTime    time.Time
    EndTime      time.Time
    ResourceType MeteringResourceType
    GroupBy      string
    PlatformTenantID string  // 新增:平台查询时可选筛选单租户(v1.yaml:7485 tenant_id query)
}
```

**LocalMeteringService 兼容:**
- 新增 `QueryPlatformUsage` 方法,返回空 items + local dev profile(与当前行为一致,不影响 local fallback)

```go
// local_metering_service.go 新增(不改动现有方法)
// 平台查询在 local/dev 模式下返回空 items,不校验 TenantID(平台 token 可能无租户归属)
func (s *LocalMeteringService) QueryPlatformUsage(_ context.Context, _ ports.MeteringUsageQueryRequest) (ports.MeteringUsageResult, error) {
    return ports.MeteringUsageResult{Items: []ports.MeteringUsageRecord{}, DevProfile: meteringDevProfile()}, nil
}
```

### 3.3 新建 adapter `PgMeteringService`

**文件:** `repo/pkg/adapters/runtime/pg_metering_service.go`

```go
package runtime

import (
    "context"
    "fmt"
    "strings"

    "github.com/kubercloud/ani/pkg/ports"
)

// PgMeteringService 不持有 clock:查询路径(buildUsageQuery)不用时钟,时间由前端传入;
// ReportTokenUsage 委托 local 实例,用的是 local 自身 clock。
// 评审 #10 删除原方案 WithPgMeteringClock option 和 now 字段(死代码)。
type PgMeteringService struct {
    store ports.MetadataStore      // 租户上下文连接(受 RLS 约束)
    local *LocalMeteringService    // 仅委托已有的 token 内存写入实现
}

type PgMeteringServiceOption func(*PgMeteringService)

func NewPgMeteringService(store ports.MetadataStore, options ...PgMeteringServiceOption) *PgMeteringService {
    s := &PgMeteringService{
        store: store,
        local: NewLocalMeteringService(),
    }
    for _, opt := range options {
        opt(s)
    }
    return s
}

var _ ports.MeteringService = (*PgMeteringService)(nil)
```

#### 3.3.1 QueryUsage 实现(租户视角,读 DB)

```go
func (s *PgMeteringService) QueryUsage(ctx context.Context, request ports.MeteringUsageQueryRequest) (ports.MeteringUsageResult, error) {
    if s.store == nil {
        return ports.MeteringUsageResult{}, ports.ErrNotConfigured
    }
    if strings.TrimSpace(request.TenantID) == "" {
        return ports.MeteringUsageResult{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
    }

    var items []ports.MeteringUsageRecord
    err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
        query, args := buildUsageQuery(request, false)
        rows, err := tx.Query(ctx, query, args...)
        if err != nil {
            return fmt.Errorf("query metering usage: %w", err)
        }
        defer rows.Close()
        for rows.Next() {
            item, err := scanUsageRow(rows, request.TenantID)
            if err != nil {
                return err
            }
            items = append(items, item)
        }
        return rows.Err()
    })
    if err != nil {
        return ports.MeteringUsageResult{}, err
    }
    return ports.MeteringUsageResult{Items: items, DevProfile: pgMeteringDevProfile()}, nil
}
```

#### 3.3.2 QueryPlatformUsage 实现(平台视角,读 DB,跨租户)

```go
func (s *PgMeteringService) QueryPlatformUsage(ctx context.Context, request ports.MeteringUsageQueryRequest) (ports.MeteringUsageResult, error) {
    if s.store == nil {
        return ports.MeteringUsageResult{}, ports.ErrNotConfigured
    }

    var items []ports.MeteringUsageRecord
    err := s.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
        // 使用 SET LOCAL ROLE(事务级)切换到 ani_metering_writer(BYPASSRLS)绕过 RLS 跨租户读
        // ani_metering_writer 已有 GRANT SELECT(20260731_001 migration:42)
        // SET LOCAL ROLE 在 commit/rollback 时自动重置,无需显式 RESET ROLE,
        // 即使中途出错回滚,角色也不会泄漏到连接池
        if _, err := tx.Exec(ctx, "SET LOCAL ROLE ani_metering_writer"); err != nil {
            return fmt.Errorf("set local role ani_metering_writer: %w", err)
        }
        query, args := buildUsageQuery(request, true)
        rows, err := tx.Query(ctx, query, args...)
        if err != nil {
            return fmt.Errorf("query platform metering usage: %w", err)
        }
        defer rows.Close()
        for rows.Next() {
            item, err := scanUsageRowPlatform(rows)
            if err != nil {
                return err
            }
            items = append(items, item)
        }
        return rows.Err()
    })
    if err != nil {
        return ports.MeteringUsageResult{}, err
    }
    return ports.MeteringUsageResult{Items: items, DevProfile: pgMeteringDevProfile()}, nil
}
```

#### 3.3.3 ReportTokenUsage 实现(委托 local adapter)

本批次只做查询,`ReportTokenUsage` 委托给内部持有的 `LocalMeteringService`。token 内存写入是已有实现,本批次不改变;后续批次再考虑持久化到 PG:

```go
func (s *PgMeteringService) ReportTokenUsage(ctx context.Context, request ports.TokenUsageReportRequest) (ports.TokenUsageReportRecord, error) {
    // token 内存写入为已有实现,本批次不改变
    return s.local.ReportTokenUsage(ctx, request)
}
```

Gateway 只需装配一个 `PgMeteringService`,查询(instance 和 token)统一走 PG,token 写入继续走已有 local 内存实现,对 handler 透明。

#### 3.3.4 聚合查询 SQL 构建

```go
// buildUsageQuery 构建聚合查询 SQL 和参数。
// SQL 始终输出固定列,确保 scan 列数与 SQL 列数一致:
//   租户视角: resource_type, total_quantity, unit, period (4列)
//   平台视角: tenant_id, resource_type, total_quantity, unit, period (5列)
// period 列在无时间聚合(group_by 为空/resource_type/tenant_id)时输出 NULL::text 占位。
//
// isPlatform=true 时,SQL 包含 tenant_id 输出列(平台视角),WHERE 不依赖 RLS。
// isPlatform=false 时,SQL 不含 tenant_id 输出列,WHERE 依赖 RLS 自动过滤(租户视角)。
func buildUsageQuery(request ports.MeteringUsageQueryRequest, isPlatform bool) (string, []any) {
    // period 表达式:有时间聚合时取子串,无聚合时输出 NULL
    var periodExpr, periodGroupExpr string
    switch request.GroupBy {
    case "day":
        // period 格式 "2026-08-18T10:05" → 取前10字符 "2026-08-18"
        periodExpr = "SUBSTR(period, 1, 10)"
        periodGroupExpr = "SUBSTR(period, 1, 10)"
    case "hour":
        // 取前13字符 "2026-08-18T10"
        periodExpr = "SUBSTR(period, 1, 13)"
        periodGroupExpr = "SUBSTR(period, 1, 13)"
    default:
        // 空 / resource_type / tenant_id:不按时间聚合,period 输出 NULL,不参与 GROUP BY
        // 注意:平台视角下 tenant_id 始终参与 GROUP BY(见下方 groupByParts 构造),
        // 因此 group_by=tenant_id 与 group_by="" 产出的 SQL 完全相同,前端可传可不传
        periodExpr = "NULL::text"
        periodGroupExpr = ""
    }

    // WHERE 条件:period 字符串比较 + resource_type 筛选
    var whereParts []string
    args := []any{}
    argIdx := 1

    // period 字符串比较:把前端 RFC3339 时间用 to_char 转成和 period 相同格式(分钟对齐)
    // period 格式: "2026-08-18T10:05" → to_char 格式 "YYYY-MM-DDTHH24:MI"
    // 时区契约(评审 #3):timestamptz 的 to_char 默认按会话 TimeZone 渲染,Go 侧传 .UTC() 不改变该行为。
    // 必须显式 `AT TIME ZONE 'UTC'` 把 timestamptz 转为 timestamp(无时区类型),
    // to_char 才按字面值渲染,与会话 TimeZone 无关;配合写入侧 .UTC()(评审 #4)形成完整 UTC 契约。
    if !request.StartTime.IsZero() {
        whereParts = append(whereParts, fmt.Sprintf("period >= to_char($%d::timestamptz AT TIME ZONE 'UTC', 'YYYY-MM-DDTHH24:MI')", argIdx))
        args = append(args, request.StartTime.UTC())
        argIdx++
    }
    if !request.EndTime.IsZero() {
        whereParts = append(whereParts, fmt.Sprintf("period <= to_char($%d::timestamptz AT TIME ZONE 'UTC', 'YYYY-MM-DDTHH24:MI')", argIdx))
        args = append(args, request.EndTime.UTC())
        argIdx++
    }

    // resource_type 筛选:空表示返回全部
    if request.ResourceType != "" {
        whereParts = append(whereParts, fmt.Sprintf("resource_type = $%d", argIdx))
        args = append(args, string(request.ResourceType))
        argIdx++
    }

    // 平台视角:可选筛选单租户
    if isPlatform && request.PlatformTenantID != "" {
        whereParts = append(whereParts, fmt.Sprintf("tenant_id = $%d::uuid", argIdx))
        args = append(args, request.PlatformTenantID)
        argIdx++
    }

    whereClause := ""
    if len(whereParts) > 0 {
        whereClause = "WHERE " + strings.Join(whereParts, " AND ")
    }

    // SELECT 列:平台视角额外输出 tenant_id
    tenantSelect := ""
    if isPlatform {
        tenantSelect = "tenant_id::text AS tenant_id, "
    }

    // GROUP BY / ORDER BY 子句:始终包含 resource_type, unit;有时间聚合时加 periodExpr
    groupByParts := []string{"resource_type", "unit"}
    orderByParts := []string{"resource_type", "unit"}
    if isPlatform {
        groupByParts = append([]string{"tenant_id"}, groupByParts...)
        orderByParts = append([]string{"tenant_id"}, orderByParts...)
    }
    if periodGroupExpr != "" {
        groupByParts = append(groupByParts, periodGroupExpr)
        orderByParts = append(orderByParts, periodGroupExpr)
    }

    query := fmt.Sprintf(`
        SELECT
            %s
            resource_type,
            SUM(quantity) AS total_quantity,
            unit,
            %s AS period
        FROM metering_usage_records
        %s
        GROUP BY %s
        ORDER BY %s
    `,
        tenantSelect,
        periodExpr,
        whereClause,
        strings.Join(groupByParts, ", "),
        strings.Join(orderByParts, ", "),
    )

    return query, args
}

// scanUsageRow 租户视角行映射(tenant_id 从 request 取)
// 列顺序固定:resource_type, total_quantity, unit, period
func scanUsageRow(rows ports.Rows, tenantID string) (ports.MeteringUsageRecord, error) {
    var (
        rtType string
        total  float64
        unit   string
        period *string
    )
    if err := rows.Scan(&rtType, &total, &unit, &period); err != nil {
        return ports.MeteringUsageRecord{}, fmt.Errorf("scan metering usage row: %w", err)
    }
    item := ports.MeteringUsageRecord{
        TenantID:      tenantID,
        ResourceType:  ports.MeteringResourceType(rtType),
        TotalQuantity: total,
        Unit:          unit,
    }
    if period != nil {
        item.Period = *period
    }
    return item, nil
}

// scanUsageRowPlatform 平台视角行映射(tenant_id 从 SQL 结果取)
// 列顺序固定:tenant_id, resource_type, total_quantity, unit, period
// ResourceRef 在聚合查询中不返回(按 resource_type 分组,不按 resource_ref 分组),
// 由 ports.MeteringUsageRecord 定义但扫描时不填,调用方不应依赖该字段
func scanUsageRowPlatform(rows ports.Rows) (ports.MeteringUsageRecord, error) {
    var (
        tenantID string
        rtType   string
        total    float64
        unit     string
        period   *string
    )
    if err := rows.Scan(&tenantID, &rtType, &total, &unit, &period); err != nil {
        return ports.MeteringUsageRecord{}, fmt.Errorf("scan platform metering usage row: %w", err)
    }
    item := ports.MeteringUsageRecord{
        TenantID:      tenantID,
        ResourceType:  ports.MeteringResourceType(rtType),
        TotalQuantity: total,
        Unit:          unit,
    }
    if period != nil {
        item.Period = *period
    }
    return item, nil
}
```

**SQL 示例(租户视角,group_by=day):**

```sql
SELECT
    resource_type,
    SUM(quantity) AS total_quantity,
    unit,
    SUBSTR(period, 1, 10) AS period
FROM metering_usage_records
WHERE period >= to_char($1::timestamptz AT TIME ZONE 'UTC', 'YYYY-MM-DDTHH24:MI')
  AND period <= to_char($2::timestamptz AT TIME ZONE 'UTC', 'YYYY-MM-DDTHH24:MI')
  -- RLS 自动过滤:tenant_id = current_setting('app.current_tenant_id')::uuid
GROUP BY resource_type, unit, SUBSTR(period, 1, 10)
ORDER BY resource_type, unit, SUBSTR(period, 1, 10)
```

**SQL 示例(平台视角,group_by=day,无 tenant_id 筛选):**

```sql
SELECT
    tenant_id::text AS tenant_id,
    resource_type,
    SUM(quantity) AS total_quantity,
    unit,
    SUBSTR(period, 1, 10) AS period
FROM metering_usage_records
WHERE period >= to_char($1::timestamptz AT TIME ZONE 'UTC', 'YYYY-MM-DDTHH24:MI')
  AND period <= to_char($2::timestamptz AT TIME ZONE 'UTC', 'YYYY-MM-DDTHH24:MI')
GROUP BY tenant_id, resource_type, unit, SUBSTR(period, 1, 10)
ORDER BY tenant_id, resource_type, unit, SUBSTR(period, 1, 10)
```

**SQL 示例(租户视角,无 group_by):**

```sql
SELECT
    resource_type,
    SUM(quantity) AS total_quantity,
    unit,
    NULL::text AS period
FROM metering_usage_records
WHERE ...
GROUP BY resource_type, unit
ORDER BY resource_type, unit
```

#### 3.3.5 DevProfile 标记

```go
func pgMeteringDevProfile() ports.DevProfileInfo {
    return ports.DevProfileInfo{
        Mode:         "postgres",
        Provider:     "pg-metering-service",
        RealProvider: true,
        Reason:       "postgres-backed metering usage query from metering_usage_records",
    }
}
```

### 3.4 OpenAPI 契约修改

**改动文件:** `repo/api/openapi/v1.yaml`

`metering_usage_records` 表不存在 `az` 列,无法支持按可用区聚合。按照 API 契约先行原则,
本批次先修正租户侧 `GET /metering/usage` 的 `group_by` 枚举,再实现 handler 和 adapter。

修改前:

```yaml
- { name: group_by, in: query, schema: { type: string, enum: [resource_type, az, day, hour] } }
```

修改后:

```yaml
- { name: group_by, in: query, schema: { type: string, enum: [resource_type, day, hour] } }
```

该修改仅影响租户侧 `GET /metering/usage`。平台侧 `GET /metering/usage/platform` 的枚举保持不变:

```yaml
- { name: group_by, in: query, schema: { type: string, enum: [tenant_id, day, hour] } }
```

契约已移除 `az`,`queryUsage` 和 `queryPlatformUsage` 无需增加 `group_by=az` 的专门 handler 拦截逻辑。

**平台端点 tenant_id query 参数加 `format: uuid`(评审 #5):**

平台端点 `tenant_id` query 参数当前 schema 是 `{ type: string }`,无 `format: uuid`,前端传任意字符串时 PG `::uuid` cast 失败会返回 500。契约层加 `format: uuid` 与 handler 层 `uuid.Parse` 校验一致(见 §3.5.1)。

修改前:

```yaml
- { name: tenant_id, in: query, required: false, schema: { type: string }, description: "可选筛选单租户,须平台 RBAC 校验" }
```

修改后:

```yaml
- { name: tenant_id, in: query, required: false, schema: { type: string, format: uuid }, description: "可选筛选单租户,须平台 RBAC 校验" }
```

`format: uuid` 是声明性约束,OpenAPI 生成器不会自动校验(仍需 handler 拦截),但能给客户端明确的格式预期,并让 SDK 生成时携带 UUID 类型注释。

#### 3.4.1 兼容性与生成物同步

租户侧 `group_by` 枚举移除 `az` 属于 Core v1 契约收窄。虽然当前
`metering_usage_records` 无 `az` 列、该能力从未真实实现,仍需运行
`make validate-core-api-compatibility` 检查兼容性并保留本节所述修正理由;
若门禁报告不兼容,不得绕过门禁或用 handler 兜底掩盖契约差异。

**两个 metering 端点 responses 各补 503 声明(评审 #9):**

本批次 §3.5.4 给 `writeMeteringError` 加了 503 映射(`ErrNotConfigured`/`ErrUnavailable` → 503),v1.yaml 两个 metering 端点 responses 当前只声明 200/400/401/403,未同步补 503,违反"契约先行"。本批次既动这两个 operation,应同步补 503 responses 声明。

修改前(两个端点 responses 末尾):

```yaml
responses:
  "200": ...
  "400": { $ref: '#/components/responses/BadRequest' }
  "401": { $ref: '#/components/responses/Unauthorized' }
  "403": { $ref: '#/components/responses/Forbidden' }
```

修改后(两个端点 responses 末尾各补一行):

```yaml
responses:
  "200": ...
  "400": { $ref: '#/components/responses/BadRequest' }
  "401": { $ref: '#/components/responses/Unauthorized' }
  "403": { $ref: '#/components/responses/Forbidden' }
  "503": { $ref: '#/components/responses/ServiceUnavailable' }   # 新增(评审 #9)
```

`ServiceUnavailable` 组件 v1.yaml 已存在,多处端点已有 503 先例。503 是新增 response 声明,符合 Core v1 允许新增 response 字段规则,属**非破坏性变更**。

`v1.yaml` 是唯一真实来源。修改后必须通过现有生成流程同步以下派生物,不得手工编辑:

- `repo/frontends/console/src/api/core-schema.d.ts`
- `repo/frontends/boss/src/api/core-schema.d.ts`
- `repo/sdks/core/`
- `repo/docs/api/core.html`

此外,Console 用量页的手写分组选项也必须与契约同步,移除 `GROUP_BY_OPTIONS` 中的 `az`;
该项不会由 OpenAPI 类型生成自动更新:

- `repo/frontends/console/src/features/usage/constants.ts`
- `repo/frontends/console/src/features/usage/constants.test.ts`

生成命令:

```bash
cd repo
make gen-console-api
node frontends/boss/scripts/gen-core-schema.mjs
make gen-core-sdk
make gen-api-docs
```

### 3.5 Gateway handler 改动

**改动文件:** [metering_resources.go](../../../../../repo/services/ani-gateway/internal/router/metering_resources.go)

#### 3.5.1 新增 `queryPlatformUsage` handler

```go
func (api *meteringAPI) queryPlatformUsage(ctx context.Context, c *app.RequestContext) {
    // 复用 requireTimeRange(评审 #11):与 queryUsage handler 共用时间必填校验
    startTime, endTime, ok := requireTimeRange(c)
    if !ok {
        return
    }

    // 评审 #5 修复:tenant_id query 参数 UUID 格式校验
    // 契约 schema 已加 format: uuid(见 §3.4),handler 层 uuid.Parse 是真正落地校验
    platformTenantID := strings.TrimSpace(c.Query("tenant_id"))
    if platformTenantID != "" {
        if _, err := uuid.Parse(platformTenantID); err != nil {
            writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "tenant_id must be a valid UUID")
            return
        }
    }

    result, err := api.service.QueryPlatformUsage(ctx, ports.MeteringUsageQueryRequest{
        TenantID:         instanceTenantID(c),
        PlatformTenantID: platformTenantID,
        StartTime:        startTime,
        EndTime:          endTime,
        ResourceType:     ports.MeteringResourceType(strings.TrimSpace(c.Query("resource_type"))),
        GroupBy:          strings.TrimSpace(c.Query("group_by")),
    })
    if err != nil {
        writeMeteringError(c, err)
        return
    }
    c.JSON(http.StatusOK, meteringUsageFromResult(result))
}
```

依赖:`github.com/google/uuid`(项目已使用)。

#### 3.5.2 注册路由

```go
// 修改前:
func registerMetering(v1 *route.RouterGroup) {
    api := newMeteringAPI()
    v1.GET("/metering/usage", api.queryUsage)
    v1.POST("/metering/token-usage", api.reportTokenUsage)
}

// 修改后:
func registerMetering(v1 *route.RouterGroup, service ports.MeteringService) {
    api := newMeteringAPI(service)
    v1.GET("/metering/usage", api.queryUsage)
    v1.GET("/metering/usage/platform", api.queryPlatformUsage)  // 新增
    v1.POST("/metering/token-usage", api.reportTokenUsage)
}
```

#### 3.5.3 `newMeteringAPI` 支持注入

```go
// 修改前(硬编码 local):
func newMeteringAPI() *meteringAPI {
    return &meteringAPI{service: runtimeadapter.NewLocalMeteringService()}
}

// 修改后(支持注入,默认 local):
func newMeteringAPI(services ...ports.MeteringService) *meteringAPI {
    if len(services) > 0 && services[0] != nil {
        return &meteringAPI{service: services[0]}
    }
    return &meteringAPI{service: runtimeadapter.NewLocalMeteringService()}
}
```

**测试文件影响:** `newMeteringAPI` 改可变参后,Go 可变参吃零参,既有 `newMeteringAPI()` 调用无需改签名;但 `metering_resources_test.go` 仍需新增平台 handler 和 503 错误映射测试。

#### 3.5.4 更新 `writeMeteringError` 处理新错误类型

现有 [metering_resources.go:171-179](../../../../../repo/services/ani-gateway/internal/router/metering_resources.go#L171-L179) 的 `writeMeteringError` 只处理 `ErrInvalid` → 400,其余全部 → 500。需补充 `ErrNotConfigured`/`ErrUnavailable` → 503:

```go
// 修改前:
func writeMeteringError(c *app.RequestContext, err error) {
    switch {
    case errors.Is(err, ports.ErrInvalid):
        writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
    default:
        writeInstanceError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
    }
}

// 修改后:
func writeMeteringError(c *app.RequestContext, err error) {
    switch {
    case errors.Is(err, ports.ErrInvalid):
        writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
    case errors.Is(err, ports.ErrNotConfigured), errors.Is(err, ports.ErrUnavailable):
        writeInstanceError(c, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "metering service unavailable")
    default:
        writeInstanceError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
    }
}
```

#### 3.5.5 `queryUsage` handler 同步强制 start/end 必填(评审 #11)

**背景**:OpenAPI 契约 [v1.yaml:7748-7749](../../../../../repo/api/openapi/v1.yaml#L7748-L7749) 声明 `start_time`/`end_time` 为 `required: true`,但现有 [metering_resources.go:71-94](../../../../../repo/services/ani-gateway/internal/router/metering_resources.go#L71-L94) `queryUsage` handler 不强制——空时返回零值,接入 `PgMeteringService` 后会让 `buildUsageQuery` 跳过 WHERE 时间条件(§3.3.4 中 `if !request.StartTime.IsZero()` 判断),导致全表聚合,返回租户全部历史数据,违反契约且有 DoS 风险。

本批次 §3.5.1 `queryPlatformUsage` 已强制 required,原 `queryUsage` handler **必须同步**保持一致,否则平台/租户两侧行为不对称。

**改动**:抽公共函数 `requireTimeRange` 供两个 handler 共用,`queryUsage` handler 改为调用它:

```go
// requireTimeRange 校验 start_time 和 end_time 必填且格式正确(RFC3339)。
// 返回解析后的时间;任一缺失或格式错误返回 400。两个 metering handler 共用。
func requireTimeRange(c *app.RequestContext) (time.Time, time.Time, bool) {
    startTime, err := optionalRFC3339(c.Query("start_time"))
    if err != nil {
        writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "start_time must be RFC3339")
        return time.Time{}, time.Time{}, false
    }
    endTime, err := optionalRFC3339(c.Query("end_time"))
    if err != nil {
        writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "end_time must be RFC3339")
        return time.Time{}, time.Time{}, false
    }
    if startTime.IsZero() || endTime.IsZero() {
        writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "start_time and end_time are required")
        return time.Time{}, time.Time{}, false
    }
    return startTime, endTime, true
}

// queryUsage handler 改为复用 requireTimeRange
func (api *meteringAPI) queryUsage(ctx context.Context, c *app.RequestContext) {
    startTime, endTime, ok := requireTimeRange(c)
    if !ok {
        return
    }
    result, err := api.service.QueryUsage(ctx, ports.MeteringUsageQueryRequest{
        TenantID:     instanceTenantID(c),
        StartTime:    startTime,
        EndTime:      endTime,
        ResourceType: ports.MeteringResourceType(strings.TrimSpace(c.Query("resource_type"))),
        GroupBy:      strings.TrimSpace(c.Query("group_by")),
    })
    if err != nil {
        writeMeteringError(c, err)
        return
    }
    c.JSON(http.StatusOK, meteringUsageFromResult(result))
}
```

**行为变更说明**:这是对契约 `required: true` 的真正落实。原本空 start_time/end_time 会返回全表聚合结果,修复后返回 400。

- 现有调用方:Console 用量页([UsageFilterBar.tsx](../../../../../repo/frontends/console/src/features/usage/UsageFilterBar.tsx))和 BOSS 平台页([PlatformFilterBar.tsx](../../../../../repo/frontends/boss/src/features/platform-metering/PlatformFilterBar.tsx))实际是否传空 start/end 需实施前确认
- 若所有调用方都已传 start/end:本修改仅是把契约要求真正落地,无影响
- 若有调用方依赖旧行为:提前协调,本批次同步修正前端

### 3.6 鉴权改动:getPlatformMeteringUsage 进入 V2 pilot

> 背景:鉴权四批次已合入,Gateway 中间件链为 `ResolveAuthzPolicy → AuthenticatePrincipal → AuthorizePrincipal`(chain.go)。generated operation 经 `ValidatePrincipal` + `CheckPermissionV2` 授权,平台隔离由 policy `boundary` 承载;legacy operation 走旧 middleware(`ValidateToken` + `scopeAllowedForPath`)。V4 迁移方案已将 `scopeAllowedForPath` 手工前缀清单认定为待消除技术债,本批次不扩大它,改为让 `getPlatformMeteringUsage` 直接进入 V2 链路。

#### 3.6.1 v1.yaml 增加 `x-ani-authz`

**改动文件:** [v1.yaml](../../../../../repo/api/openapi/v1.yaml) `getPlatformMeteringUsage`(L7777)

```yaml
/metering/usage/platform:
  get:
    operationId: getPlatformMeteringUsage
    # security 继承全局声明,无需显式声明(评审 #14 删除冗余 security 块)
    x-ani-rbac-scope: "scope:metering:platform:read"   # 已有,保留
    x-ani-authz:                                        # 新增
      version: v1
      resource: metering
      action: read
      boundary: platform
      principal_kinds: [user]
```

字段依据(与 `listQuotaMeta` pilot 保持同一范式):

| 字段 | 值 | 依据 |
|---|---|---|
| `resource: metering` | 权限数据合法资源 | permissions_schema.sql 合法资源清单含 `metering`;platform-admin wildcard `{"resource":"*","actions":["*"],"scope":"platform"}` 直接命中 |
| `action: read` | 查询操作 | `CheckPermissionV2` 按权威 permissions 判定 `metering:read` |
| `boundary: platform` | 跨租户平台查询 | `DomainAllowsBoundary` 要求 credential domain 覆盖 platform;tenant domain 主体在 Gateway 和 auth-service 双层被判 `CREDENTIAL_DOMAIN_MISMATCH`(403),不进权限查询 |
| `principal_kinds: [user]` | BOSS 管理端用户 Bearer token | 与 quota-meta pilot 一致;API Key 命中 `security` alternative 后被 `AllowsPrincipalKind` 拒绝(403),与迁移期 API Key 不进平台路由的既有语义一致 |

#### 3.6.2 扩展 pilot allowlist

**改动文件:** [mode.go](../../../../../repo/services/ani-gateway/internal/authz/mode.go)

```go
// 修改前(mode.go:63-67):
// functionalMVPPilotOperations 冻结 Functional MVP 的 pilot 唯一集合。
// 只允许 listQuotaMeta;空集、额外项、拼写错误都必须启动失败。
var functionalMVPPilotOperations = map[string]struct{}{
	"listQuotaMeta": {},
}

// 修改后:计量平台查询进入 pilot,集合为 {listQuotaMeta, getPlatformMeteringUsage}
var functionalMVPPilotOperations = map[string]struct{}{
	"listQuotaMeta":           {},
	"getPlatformMeteringUsage": {},
}
```

同步修改 `Validate` 中的错误信息(L97):

```go
// 修改前:
return errors.New("functional MVP pilot operations must equal {listQuotaMeta}")
// 修改后:
return errors.New("functional MVP pilot operations must equal {listQuotaMeta, getPlatformMeteringUsage}")
```

**存量部署升级路径(评审 #12):**

本批次扩展 pilot 冻结集合后,所有现存 `GATEWAY_AUTHZ_PILOT_OPERATIONS=listQuotaMeta` 的部署**升级新代码后立即无法启动**——`Validate` 严格相等校验在监听前失败,Gateway 进程退出。这是 fail-closed 设计的有意破坏性变更。

发布协调要求:

- **升级前**:先改环境变量 `GATEWAY_AUTHZ_PILOT_OPERATIONS=listQuotaMeta,getPlatformMeteringUsage`,再 rollout 新镜像
- **回滚时**:先回滚镜像,再删除环境变量中的 `getPlatformMeteringUsage` 项
- 该项必须在发版公告和 Deployment rollout 文档中显式列出(详见 §6 风险表)

#### 3.6.3 重新生成 registry

```bash
cd repo
make gen-gateway-authz
```

生成后 `zz_generated_core_policies.go` 中该条目从 `PolicySourceLegacy` 变为:

```go
"GET /api/v1/metering/usage/platform": {
    Source:               PolicySourceGenerated,
    OperationID:          "getPlatformMeteringUsage",
    Method:               "GET",
    PathTemplate:         "/api/v1/metering/usage/platform",
    SecurityAlternatives: []SecurityRequirement{
        {AllOf: []OpenAPISecurityScheme{OpenAPISecurityBearer}},
        {AllOf: []OpenAPISecurityScheme{OpenAPISecurityAPIKey}},
    },
    Version: "v1", Resource: "metering", Action: "read",
    Boundary: BoundaryPlatform, PrincipalKinds: []PrincipalKind{PrincipalUser},
},
```

#### 3.6.4 部署配置

```text
GATEWAY_AUTHZ_POLICY_MODE=pilot
GATEWAY_AUTHZ_PILOT_OPERATIONS=listQuotaMeta,getPlatformMeteringUsage
ANI_AUTH_MODE=auth_service
```

`Validate` 在监听前校验 allowlist 与冻结集合严格相等:漏配/多配/拼写错误均启动失败(fail closed)。

#### 3.6.5 请求行为对照

| 请求 | 链路 | 结果 |
|---|---|---|
| BOSS 用户 platform Bearer token(platform-admin) | ValidatePrincipal → CheckPermissionV2 | 200,wildcard 命中 `metering:read` platform scope |
| tenant 用户 Bearer token | ValidatePrincipal → CheckPermissionV2 | 403 `CREDENTIAL_DOMAIN_MISMATCH`(tenant domain 不覆盖 platform boundary) |
| tenant API Key | ValidatePrincipal → Gateway policy 复核 | 403 `principal not allowed by operation policy`(`principal_kinds: [user]`) |
| 无凭证 | credentialFromRequest | 401 |
| auth-service 不可用 | ValidatePrincipal/CheckPermissionV2 RPC error | 503(V2 deny/error 不回退 legacy,与 quota-meta pilot 同语义) |

#### 3.6.6 明确不改

- `scopeAllowedForPath` 完全不改——不新增 metering 前缀,不扩大手工清单债务
- `getMeteringUsage` / `reportTokenUsage` 保持 `PolicySourceLegacy`:租户 scope 在旧链路天然放行,platform token 被 `return scope == "tenant"` 挡住,隔离语义已正确
- `InstallGeneratedPrincipalContext` 对 platform 主体不注入 tenant context,平台查询 handler 本就使用 `WithPlatformTx`,不依赖 tenant context,与 V2 链路天然兼容

**pilot 关闭 + postgres 开启组合行为说明(评审 #13):**

该组合是合法的部署组合(运维可能先开启 postgres adapter 验证数据,再开启 pilot 鉴权),但方案原 §3.6.6 未分析该组合下平台路由的行为。该组合下 `/api/v1/metering/usage/platform` 走 legacy 链路 `scopeAllowedForPath` default 分支:

- `scopeAllowedForPath` 对该路径返回 `scope == "tenant"`
- scope 层面放行 tenant token、拒绝 platform token(**方向反了**)
- 最终靠 `rbac.go` 的 `scope:metering:platform:read` 挡住 tenant token,fail-closed 仍成立
- platform token 在 scope 层就被错误拒绝(应被放行进 rbac),无法到达 `queryPlatformUsage` handler

**结论**:该组合下平台查询不可用,但 fail-closed 安全性成立。**建议生产环境同步开启 pilot 鉴权**,避免方向反了的中间状态;开发/调试场景可临时使用该组合验证 postgres adapter 数据正确性(详见 §5.3 live gate)。

### 3.7 Gateway 装配注入

#### 3.7.1 `RegisterOptions` 加字段

```go
// router.go
type RegisterOptions struct {
    // ... 现有字段
    MeteringService ports.MeteringService  // 新增
}
```

#### 3.7.2 `registerMetering` 传参

```go
// router.go RegisterWithOptions 内
registerMetering(v1, options.MeteringService)  // 修改:传参
```

#### 3.7.3 新建 `metering_runtime.go`

**文件:** `repo/services/ani-gateway/metering_runtime.go`(仿 [quota_runtime.go](../../../../../repo/services/ani-gateway/quota_runtime.go) 模式)

```go
package main

import (
    "context"
    "fmt"
    "os"
    "strings"

    runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
    "github.com/kubercloud/ani/pkg/bootstrap"
    "github.com/kubercloud/ani/pkg/ports"
)

func newGatewayMeteringService(ctx context.Context) (ports.MeteringService, func(), error) {
    mode := strings.TrimSpace(os.Getenv("METERING_PROVIDER_MODE"))
    switch mode {
    case "", "local":
        return runtimeadapter.NewLocalMeteringService(), nil, nil
    case "postgres":
        dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
        if dsn == "" {
            return nil, func() {}, fmt.Errorf("%w: DATABASE_URL is required for postgres metering", ports.ErrNotConfigured)
        }
        store, closeStore, err := bootstrap.ConnectMetadataStore(ctx, dsn)
        if err != nil {
            return nil, func() {}, err
        }
        if err := store.Ping(ctx); err != nil {
            closeStore()
            return nil, func() {}, fmt.Errorf("%w: metering store database unreachable: %w", ports.ErrUnavailable, err)
        }
        return runtimeadapter.NewPgMeteringService(store), closeStore, nil
    default:
        return nil, func() {}, fmt.Errorf("%w: unsupported METERING_PROVIDER_MODE %q", ports.ErrUnsupported, mode)
    }
}
```

#### 3.7.4 `main.go` 装配

```go
// main.go,在 quotaAdminService 装配附近新增
meteringService, closeMeteringRuntime, err := newGatewayMeteringService(runtimeCtx)
if err != nil {
    logger.Error("failed to configure metering service", "err", err)
    os.Exit(1)
}
if closeMeteringRuntime != nil {
    defer closeMeteringRuntime()
}

// 注入到 RegisterOptions
router.RegisterWithOptions(h, router.RegisterOptions{
    // ... 现有字段
    MeteringService: meteringService,  // 新增
})
```

#### 3.7.5 Provider 切换方式与环境变量

计量查询 Provider 由 **ANI Gateway** 在启动时通过 `METERING_PROVIDER_MODE` 选择,不是 metering-service 的采集 Provider 配置。该变量只决定 `GET /metering/usage` 和 `GET /metering/usage/platform` 使用内存查询 adapter 还是 PostgreSQL 查询 adapter。

| 场景 | `METERING_PROVIDER_MODE` | 配套变量 | 实际装配 |
|---|---|---|---|
| 本地开发 / CI | 未设置、空字符串或 `local` | 无 | `LocalMeteringService`,查询已有进程内 token 数据;平台查询返回空列表 |
| live gate / 生产查询 | `postgres` | 必须设置 `DATABASE_URL` | `PgMeteringService`,统一查询 `metering_usage_records` |

切换到 PostgreSQL Provider:

```text
METERING_PROVIDER_MODE=postgres
DATABASE_URL=postgres://<user>:<password>@<host>:5432/<database>
```

切回本地 Provider:

```text
METERING_PROVIDER_MODE=local
```

Kubernetes Deployment 示例(数据库连接串必须从 Secret 注入,不得写入可提交 YAML):

```yaml
env:
  - name: METERING_PROVIDER_MODE
    value: postgres
  - name: DATABASE_URL
    valueFrom:
      secretKeyRef:
        name: ani-gateway-database
        key: url
```

环境变量由 Gateway 启动装配阶段读取,修改后必须重新启动进程或执行 Deployment rollout,不支持运行时热切换。`postgres` 模式下 `DATABASE_URL` 缺失、连接失败或 `Ping` 失败时 Gateway 必须启动失败,不得静默降级到 `local`;未知的 `METERING_PROVIDER_MODE` 值返回 `ErrUnsupported` 并阻止 Gateway 启动。

切换完成后的最小验证:

1. 检查 Gateway rollout 成功且 `readyz` 通过。
2. 使用租户 token 请求 `GET /api/v1/metering/usage`,确认可查询 `metering_usage_records` 中该租户的 instance 用量。
3. 使用 platform token 请求 `GET /api/v1/metering/usage/platform`,确认跨租户结果包含 `tenant_id`。
4. 将 `METERING_PROVIDER_MODE` 切回 `local` 并重启后,确认响应恢复 local dev profile 语义。

**生产部署检查清单(评审 #8):**

注意:`METERING_PROVIDER_MODE` 独立于 quota PG 装配(quota 装配见 `quota_runtime.go`,不依赖该开关)。若生产已配 quota PG 但漏配 metering 开关,`mode=""` 走 local,计量查询静默走内存 local adapter(`RealProvider:false`),用户看不到真实数据但 Gateway 不报错。

- 若 `DATABASE_URL` 已配置(quota PG 已启用),`METERING_PROVIDER_MODE` **必须显式设为 `postgres`**,不得为空或 `local`,否则计量查询静默走内存 local adapter,数据不可见但 Gateway 不报错
- 该检查不由启动装配强制(quota 和 metering 装配路径独立),需由部署文档和运维流程保证
- 建议在 Deployment rollout 检查表中显式列出此项
- live gate 启动前必须确认 `METERING_PROVIDER_MODE=postgres` 且 `DATABASE_URL` 已配置(详见 §5.3)

---

## 4. 改动清单

| # | 文件 | 类型 | 说明 |
|---|---|---|---|
| 1 | `repo/api/openapi/v1.yaml` | 修改 | ① 租户侧 `GET /metering/usage` 的 `group_by` 枚举由 `[resource_type, az, day, hour]` 修改为 `[resource_type, day, hour]`,平台侧枚举保持不变;② `getPlatformMeteringUsage` 新增 `x-ani-authz`(resource=metering, action=read, boundary=platform, principal_kinds=[user],详见 §3.6.1);③ 平台端点 tenant_id query 参数 schema 加 `format: uuid`(评审 #5,详见 §3.4);④ 两个 metering 端点 responses 各补 `"503": { $ref: '#/components/responses/ServiceUnavailable' }`,与 §3.5.4 `writeMeteringError` 503 映射一致(评审 #9,详见 §3.4.1);⑤ 删除 v1.yaml:7783 平台端点 description 中"若带 tenant_id query 须二次 RBAC 校验"一句(实现无二次校验,契约不应说谎,评审 #14) |
| 2 | `repo/frontends/console/src/api/core-schema.d.ts` | 生成修改 | 从 `v1.yaml` 重新生成,租户侧 `group_by` 类型移除 `az` |
| 3 | `repo/frontends/boss/src/api/core-schema.d.ts` | 生成修改 | 从 `v1.yaml` 重新生成,租户侧 `group_by` 类型移除 `az` |
| 4 | `repo/sdks/core/` | 生成修改 | 从 Core OpenAPI 契约重新生成四语言 SDK |
| 5 | `repo/docs/api/core.html` | 生成修改 | 从 Core OpenAPI 契约重新生成静态 API 文档 |
| 6 | `repo/frontends/console/src/features/usage/constants.ts` | 修改 | 租户用量页 `GroupByOption` 和 `GROUP_BY_OPTIONS` 移除 `az` |
| 7 | `repo/frontends/console/src/features/usage/constants.test.ts` | 修改 | 更新分组选项数量、枚举和值断言,不再断言 `az` |
| 8 | `repo/pkg/ports/metering.go` | 修改 | `MeteringService` 接口新增 `QueryPlatformUsage` 方法;`MeteringUsageQueryRequest` 新增 `PlatformTenantID` 字段 |
| 9 | `repo/pkg/adapters/runtime/pg_metering_service.go` | 新增 | postgres adapter,统一从 `metering_usage_records` 查询 instance 和 token 用量,实现租户+平台双查询;token 写入仍委托已有内存实现;struct 不含 `now` 字段、不含 `WithPgMeteringClock` option(评审 #10 删除死代码,详见 §3.3) |
| 10 | `repo/pkg/adapters/runtime/local_metering_service.go` | 修改 | 新增 `QueryPlatformUsage` 方法(返回空 items,兼容接口);不改变已有 token 内存写入逻辑 |
| 11 | `repo/pkg/adapters/runtime/pg_metering_service_test.go` | 新增 | adapter 单元测试 |
| 12 | `repo/services/ani-gateway/metering_runtime.go` | 新增 | 装配注入,仿 quota_runtime.go |
| 13 | `repo/services/ani-gateway/internal/router/metering_resources.go` | 修改 | `newMeteringAPI` 支持注入 + 新增 `queryPlatformUsage` handler(含 tenant_id UUID 校验,评审 #5)+ 注册路由 + `writeMeteringError` 补充 503 映射 + 新增 `requireTimeRange` 公共函数 + 改写 `queryUsage` handler 强制 start/end 必填(评审 #11,详见 §3.5.5);契约已移除 `az`,无需 handler 拦截 |
| 14 | `repo/services/ani-gateway/internal/router/metering_resources_test.go` | 修改 | 保留现有测试,新增平台 handler 成功响应测试及 `ErrNotConfigured`/`ErrUnavailable` → 503 错误映射测试 |
| 15 | `repo/services/ani-gateway/internal/router/router.go` | 修改 | `registerMetering` 传参 + `RegisterOptions` 加 `MeteringService` 字段 |
| 16 | `repo/services/ani-gateway/internal/authz/mode.go` | 修改 | `functionalMVPPilotOperations` 扩展为 `{listQuotaMeta, getPlatformMeteringUsage}`,同步更新冻结注释和 `Validate` 错误信息(详见 §3.6.2) |
| 17 | `repo/services/ani-gateway/internal/authz/mode_test.go` | 修改 | 更新 pilot 集合严格相等断言、allowlist 漂移断言和错误信息匹配 |
| 18 | `repo/services/ani-gateway/internal/authz/zz_generated_core_policies.go` | 生成修改 | `make gen-gateway-authz` 重新生成,`getPlatformMeteringUsage` 条目变为 `PolicySourceGenerated` |
| 19 | `repo/services/ani-gateway/internal/middleware/pilot_test.go` | 修改 | 扩展 pilot 场景断言:平台 metering 路由命中 generated 链路,`ValidatePrincipal`/`CheckPermissionV2` 各恰好调用 1 次;租户 metering 路由仍走 legacy(`ValidateToken`/`CheckPermission`) |
| 20 | `repo/services/ani-gateway/main.go` | 修改 | 装配 meteringService,注入 RegisterOptions |
| 21 | `repo/pkg/adapters/metering/collectors.go` | 修改 | `CollectAll` 中 `time.Now().Format` 改为 `time.Now().UTC().Format`(评审 #4,确保 period 按 UTC 写入,详见 §6 风险表) |
| 22 | `repo/services/metering-service/internal/service/metering_collection_service.go` | 修改 | 同 #21,第 167 行 period 赋值改为 `.UTC()`(评审 #4,与查询侧 `AT TIME ZONE 'UTC'` 配套) |

**明确不改动:** `scopeAllowedForPath`(auth.go)——本批次不向手工前缀清单添加任何 metering 前缀(见 §3.6.6)。

**不改动:**
- `metering_usage_records` 表 — 已建,不改 schema
- `metering-service` 采集写入主体不动,仅顺路补 period 时区 `.UTC()`(属 #3 时区契约必要配套,见 §3.3.4 和 §6 风险表)

---

## 5. 验证标准

### 5.1 单元测试(adapter)

- `TestPgMeteringServiceQueryUsageGroupByDay` — group_by=day 聚合,验证 `SUBSTR(period,1,10)` 分组
- `TestPgMeteringServiceQueryUsageGroupByHour` — group_by=hour 聚合,验证 `SUBSTR(period,1,13)` 分组
- `TestPgMeteringServiceQueryUsageResourceTypeFilter` — resource_type 筛选
- `TestPgMeteringServiceQueryUsageTokenResourceTypesUsePostgres` — `token_input`/`token_output`/`token_total` 使用同一 PG 查询路径,当前无数据时返回空 items
- `TestPgMeteringServiceQueryUsagePeriodStringComparison` — period 字符串比较时间过滤
- `TestPgMeteringServiceQueryPlatformUsageAggregatesAllTenants` — 平台视角跨租户聚合
- `TestPgMeteringServiceQueryPlatformUsageRLSBypass` — SET LOCAL ROLE ani_metering_writer 后能查全部租户
- `TestPgMeteringServiceQueryUsageRLSIsolation` — 租户 store 受 RLS,租户 A 查不到租户 B 数据
- `TestPgMeteringServiceReportTokenUsageDelegatesToLocal` — ReportTokenUsage 委托给 local adapter

**Handler 输入校验测试(评审 #5/#11):**

- `TestQueryPlatformUsageHandlerRejectsInvalidTenantID` — 平台端点传入非 UUID 字符串的 tenant_id 时返回 400,错误码 `BAD_REQUEST`(评审 #5)
- `TestQueryUsageHandlerRejectsMissingStartTime` — 租户端点不传 start_time 返回 400(评审 #11)
- `TestQueryUsageHandlerRejectsMissingEndTime` — 租户端点不传 end_time 返回 400(评审 #11)
- `TestQueryUsageHandlerRejectsInvalidRFC3339` — 租户端点 start/end 格式错误返回 400(评审 #11)
- `TestQueryPlatformUsageHandlerRejectsMissingTimeRange` — 平台端点缺失 start/end 同样返回 400(与租户侧共用 `requireTimeRange` 的回归保障)

### 5.2 Gateway 集成测试

- `TestQueryPlatformUsageHandlerResponse` — 注入测试 service 后平台 handler 返回 200
- `TestWriteMeteringErrorMapsNotConfiguredTo503` — `ErrNotConfigured` 映射为 503
- `TestWriteMeteringErrorMapsUnavailableTo503` — `ErrUnavailable` 映射为 503

`mode_test.go` / `pilot_test.go` 增加以下 V2 pilot 鉴权用例(逐场景冻结 V2/legacy RPC 精确调用次数,防止只看 HTTP 状态而未实际经过目标链路):

- `TestMeteringPlatformPilotAllowsPlatformAdmin` — platform-admin Bearer token 访问 `/api/v1/metering/usage/platform` 返回 200;`ValidatePrincipal`、`CheckPermissionV2` 各恰好调用 1 次,legacy `ValidateToken`/`CheckPermission` 调用次数为 0
- `TestMeteringPlatformPilotRejectsTenantDomain` — tenant 用户 token 返回 403(`CREDENTIAL_DOMAIN_MISMATCH`),`CheckPermissionV2` 被调用但 permission store 查询次数为 0(错误身份域不进权限查询)
- `TestMeteringPlatformPilotRejectsAPIKey` — tenant API Key 返回 403(policy `principal_kinds: [user]` 拒绝)
- `TestMeteringPlatformPilotRejectsMissingCredential` — 无凭证返回 401
- `TestMeteringPlatformPilotFailsClosedWhenAuthServiceUnavailable` — auth-service 不可用返回 503,不回退 legacy(legacy RPC 调用次数为 0)
- `TestMeteringTenantRoutesStayLegacy` — `/api/v1/metering/usage` 与 `/api/v1/metering/token-usage` 仍走 legacy 链路(`ValidateToken`/`CheckPermission` 各 1 次,V2 RPC 调用次数为 0)
- `TestPilotAllowlistRequiresMeteringPlatformOperation` — `GATEWAY_AUTHZ_PILOT_OPERATIONS` 缺少 `getPlatformMeteringUsage` 时启动校验失败(fail closed)

### 5.3 live gate

**启动前配置确认(评审 #8):**

- 启动前确认 `METERING_PROVIDER_MODE=postgres` 且 `DATABASE_URL` 已配置;若仅 quota 启用 PG 而 metering 漏配开关(`mode=""`),查询会静默走 local,数据不可见但 Gateway 不报错(详见 §3.7.5 生产部署检查清单)

**启动验证:**

- `METERING_PROVIDER_MODE=postgres` + `DATABASE_URL=...` 启动
- `GATEWAY_AUTHZ_POLICY_MODE=pilot` + `GATEWAY_AUTHZ_PILOT_OPERATIONS=listQuotaMeta,getPlatformMeteringUsage` + `ANI_AUTH_MODE=auth_service` 启动(allowlist 漏配/错配必须启动失败)
- **存量部署升级路径(评审 #12)**:升级前先改环境变量 `GATEWAY_AUTHZ_PILOT_OPERATIONS=listQuotaMeta,getPlatformMeteringUsage`,再 rollout 新镜像;回滚时反向操作(详见 §3.6.2)
- GET 查询(租户 A instance_gpu_seconds)数据正确,走 legacy 链路
- GET 平台查询能聚合租户 A + 租户 B 的数据(验证 SET LOCAL ROLE bypass),走 V2 pilot 链路;BOSS platform token 返回 200,tenant token 返回 403
- auth-service 日志确认平台查询请求经过 `ValidatePrincipal`/`CheckPermissionV2`(每次请求各 1 次),租户查询经过 `ValidateToken`/`CheckPermission`
- period 字符串比较时间过滤准确
- group_by=day/hour 聚合结果正确
- `group_by=tenant_id` 返回结果与 `group_by=""` 一致(平台视角 tenant_id 始终参与分组)
- `resource_type=token_input` / `token_output` / `token_total` 查询统一访问 `metering_usage_records`,当前因 token 尚未持久化到 PG 而返回空 items(预期行为,前端 token 专页会显示空数据)

### 5.4 生成与架构校验

```bash
cd repo
make gen-console-api
node frontends/boss/scripts/gen-core-schema.mjs
make gen-core-sdk
make gen-api-docs
make gen-gateway-authz          # v1.yaml 新增 x-ani-authz 后重新生成 registry
make validate-openapi-spec
make validate-core-api-compatibility
make validate-sdk-alpha
make validate-doc-api
make validate-gateway-authz    # 生成物 drift 检查
python scripts/validate_core_gateway_authz_routes.py  # 注册平台路由后确认 route coverage(防止路径参数名漂移类问题)
cd frontends/console
npm test -- --run src/features/usage/constants.test.ts
cd ../..
make test
make validate-architecture
git diff --check
```

---

## 6. 风险与对策

| 风险 | 对策 |
|---|---|
| **平台查询用 writer 角色读,语义上"写角色读数据"** | 不构成问题。权限的真正分界是租户边界:租户查询走 RLS(ani_app_user),平台查询走 BYPASSRLS(ani_metering_writer)。该角色是"metering 数据管理角色",读写同属数据管理职责域,谁写谁读。migration 中该角色本就含 GRANT SELECT,说明设计时未排除读。若另建等权限的 reader 角色,反而制造两个权限等价的角色,徒增维护成本。后续仅在出现独立计量管理服务时再考虑拆分 |
| `SET LOCAL ROLE` 在事务结束后自动重置 | 使用 `SET LOCAL ROLE`(事务级)替代 `SET ROLE`(会话级),commit/rollback 自动重置角色,无需显式 `RESET ROLE`,即使出错回滚也不会泄漏到连接池 |
| period 字符串比较依赖格式严格对齐 | 前端传的 RFC3339 时间用 `to_char` 转成 `YYYY-MM-DDTHH24:MI` 格式,与 period 存储格式一致 |
| **period 时区契约(评审 #3/#4)** | 查询侧 `to_char($N::timestamptz AT TIME ZONE 'UTC', ...)` 显式按 UTC 渲染,与会话 TimeZone 无关(timestamptz 的 `to_char` 默认按会话 TZ 渲染,Go 侧传 `.UTC()` 不改变该行为)。配合写入侧 `collectors.go` 和 `metering_collection_service.go` 顺路补 `time.Now().UTC().Format(...)`,period 始终按 UTC 存储和比较。若仅修查询侧不修写入侧,采集容器 TZ≠UTC 时 period 存本地时间,与查询侧 UTC 比较错位 8 小时 |
| **租户侧 queryUsage 强制 required 是行为变更(评审 #11)** | 契约 `v1.yaml:7748-7749` 已声明 start_time/end_time 为 `required: true`,现有 `queryUsage` handler 未强制,空时返回零值导致 `buildUsageQuery` 跳过 WHERE 时间条件 → 全表聚合。本批次抽 `requireTimeRange` 公共函数,与 `queryPlatformUsage` 共用,真正落实契约。原本空 start/end 返回全表聚合结果,修复后返回 400。实施前确认 Console 用量页和 BOSS 平台页是否传空 start/end;若有调用方依赖旧行为,提前协调并同步修正前端 |
| `SUBSTR` 聚合无法走 period 列索引 | 当前数据量不大,全表扫描可接受。后续如需优化,可建表达式索引 `CREATE INDEX ON metering_usage_records (SUBSTR(period,1,10))` |
| `unit` 列在 GROUP BY 中 | 同一 resource_type 的 unit 固定,GROUP BY unit 不影响结果,但确保 SQL 正确性 |
| 现有测试调 `newMeteringAPI()` 无参 | 可变参吃零参,既有调用无需改签名;但本批次仍修改测试文件以覆盖平台 handler、pilot 鉴权和 503 映射 |
| LocalMeteringService 接口不兼容 | 新增 `QueryPlatformUsage` 方法返回空 items,保持接口兼容 |
| Core v1 租户侧 `group_by` 枚举收窄可能触发兼容性门禁 | `az` 从未由当前表结构真实支持,本批次将其作为契约残留修正;运行 `make validate-core-api-compatibility`,同步 SDK/前端类型/API 文档,保留修正理由,不得用 handler 兜底或绕过门禁 |
| **V2 deny/error 不回退 legacy,auth-service 故障时平台查询 503** | 接受。与 quota-meta pilot 同语义,是 V4 迁移的既定取舍;租户查询仍走 legacy,不受 auth-service V2 故障影响 |
| **推翻鉴权方案两个冻结决策(V4 §1.5"不实现第二试点"、pilot 集合冻结 `{listQuotaMeta}`)** | 已由用户 2026-08-25 拍板(方案 B,现在扩)。理由:`getPlatformMeteringUsage` handler 本批次才落地,符合"新落地路由走新机制"精神;避免向 `scopeAllowedForPath` 手工清单继续堆技术债。pilot 集合扩展后严格相等校验不变,仍 fail closed |
| **非 wildcard 平台角色(如未来 platform-ops/readonly)无 `metering:read` platform 权限会被 CheckPermissionV2 deny** | 当前 BOSS 使用 platform-admin(wildcard)不受影响;若后续新增非 wildcard 平台角色,需为其补充 `{"resource":"metering","actions":["read"],"scope":"platform"}` 权限数据,属权限数据治理范畴,不在本批次 |
| **`GATEWAY_AUTHZ_PILOT_OPERATIONS` 部署漂移(漏配 metering operation)** | `Validate` 严格相等校验在监听前失败,Gateway 无法启动(fail closed),不会静默降级到 legacy |
| **存量 pilot 部署升级即启动失败(评审 #12)** | 扩展 pilot 冻结集合后,所有现存 `GATEWAY_AUTHZ_PILOT_OPERATIONS=listQuotaMeta` 部署升级新代码即启动失败。**发布协调要求**:升级前必须同步将环境变量改为 `listQuotaMeta,getPlatformMeteringUsage`;在发版公告和 Deployment rollout 文档中显式列出此项(详见 §3.6.2 存量部署升级路径) |
| **quota PG 已配 + metering 漏配开关导致静默走 local(评审 #8)** | `METERING_PROVIDER_MODE` 独立于 quota 装配,漏配时 `mode=""` 走 local,查询静默返回内存数据。**对策**:§3.7.5 生产部署检查清单要求 `DATABASE_URL` 已配时 `METERING_PROVIDER_MODE` 必须显式设为 postgres,并在 §5.3 live gate 启动验证项中确认 |

---

## 7. 不做的事(明确排除)

1. **不改动 `metering_usage_records` 表 schema** — 已建,不改
2. **不实现 token 写入持久化** — 本批次只做查询,token 写入另立批次
3. **不改 local adapter 现有方法** — 只新增 `QueryPlatformUsage`,不改动 `QueryUsage`/`ReportTokenUsage`
4. **不新增 API 路径或 schema** — 本批次修改 `v1.yaml` 租户侧 `group_by` 枚举(移除不受支持的 `az`)并为既有 operation `getPlatformMeteringUsage` 追加 `x-ani-authz` 扩展;不新增路径、不修改既有字段类型和响应语义
5. **不新建 bypassrls 角色** — 复用 `ani_metering_writer`(数据管理角色,谁写谁读,无需等权限的 reader 角色)
6. **不引入预聚合表/物化视图** — 当前问题用不到
7. **不做 unit 的 Go 层映射** — SQL `GROUP BY unit` 直接返回
8. **不支持 `group_by=az`** — 通过 OpenAPI 契约移除该枚举值表达,handler 不增加专门拦截

---

## 8. 参考索引

| 文件 | 作用 |
|---|---|
| [local_metering_service.go](../../../../../repo/pkg/adapters/runtime/local_metering_service.go) | 内存 adapter(不改现有方法,语义对齐基准) |
| [ports/metering.go](../../../../../repo/pkg/ports/metering.go) | port 接口(新增 QueryPlatformUsage) |
| [ports/metadata.go](../../../../../repo/pkg/ports/metadata.go) | MetadataStore/MetadataTx/Rows 接口定义 |
| [20260731_001_metering_usage.sql](../../../../../repo/deploy/migrations/20260731_001_metering_usage.sql) | metering_usage_records 表定义 + RLS + ani_metering_writer 角色 |
| [metering_collection_service.go](../../../../../repo/services/metering-service/internal/service/metering_collection_service.go) | 采集写入实现(SET ROLE ani_metering_writer 范式参考) |
| [metering_resources.go](../../../../../repo/services/ani-gateway/internal/router/metering_resources.go) | metering handler(新增 queryPlatformUsage) |
| [mode.go](../../../../../repo/services/ani-gateway/internal/authz/mode.go) | pilot allowlist 冻结集合(扩展 getPlatformMeteringUsage) |
| [generated_authz.go](../../../../../repo/services/ani-gateway/internal/middleware/generated_authz.go) | V2 认证/授权入口(ValidatePrincipal/CheckPermissionV2) |
| [chain.go](../../../../../repo/services/ani-gateway/internal/middleware/chain.go) | 中间件链装配(policy resolver → 认证 → 授权) |
| [auth.go](../../../../../repo/services/ani-gateway/internal/middleware/auth.go) | legacy 认证链路(scopeAllowedForPath,本批次不改) |
| [quota_runtime.go](../../../../../repo/services/ani-gateway/quota_runtime.go) | PG runtime 装配范式参考 |
| [router.go](../../../../../repo/services/ani-gateway/internal/router/router.go) | RegisterOptions(新增 MeteringService 字段) |
| [main.go](../../../../../repo/services/ani-gateway/main.go) | 装配入口 |
| [v1.yaml](../../../../../repo/api/openapi/v1.yaml) | 租户侧 `group_by` 枚举移除 `az`;`getPlatformMeteringUsage` 新增 `x-ani-authz` |
| [v1.yaml:7777](../../../../../repo/api/openapi/v1.yaml#L7777) | getPlatformMeteringUsage 契约 |
| [Console core-schema.d.ts](../../../../../repo/frontends/console/src/api/core-schema.d.ts) | 从 Core OpenAPI 生成的 Console TypeScript 类型 |
| [BOSS core-schema.d.ts](../../../../../repo/frontends/boss/src/api/core-schema.d.ts) | 从 Core OpenAPI 生成的 BOSS TypeScript 类型 |
| [usage/constants.ts](../../../../../repo/frontends/console/src/features/usage/constants.ts) | Console 租户用量页分组选项,需与租户契约枚举保持一致 |
| [usePlatformUsageQuery.ts](../../../../../repo/frontends/boss/src/features/platform-metering/usePlatformUsageQuery.ts) | 前端 BOSS 平台查询 hook |

---

## 9. 变更记录

| 日期 | 变更 |
|---|---|
| 2026-08-18 | 方案初版:确定从 PostgreSQL 查询 `metering_usage_records`,统一支持 instance 资源和 token 用量查询;新增平台跨租户查询能力;复用 `ani_metering_writer` 角色绕过 RLS;采用 `period` 字符串时间过滤和 SQL 层聚合;新增 `PgMeteringService`、平台查询 handler、路由鉴权和 Gateway 装配方案。 |
| 2026-08-19 | 根据评审意见修订方案:① 第三章新增 `3.4 OpenAPI 契约修改`,明确租户侧 `group_by` 枚举移除 `az`,平台侧枚举保持不变;② 删除原 `3.4.5 group_by=az` handler 拦截方案,并将 Gateway、鉴权、装配章节顺延为 `3.5`、`3.6`、`3.7`;③ 第四章改动清单新增 `repo/api/openapi/v1.yaml` 并标明契约先行顺序;④ 增加已有 token 内存写入说明,明确 token 写入继续使用 `LocalMeteringService` 的既有内存实现,instance/token 查询统一由 `PgMeteringService` 查询 `metering_usage_records`,当前 token 尚未写入 PG 时返回空结果。 |
| 2026-08-19 | 全文一致性审查修订:① 平台查询总览统一调用 `QueryPlatformUsage`,RLS 切换统一为 `SET LOCAL ROLE`;② `metering_resources_test.go` 改为修改并补平台 handler/503 映射测试,`auth_test.go` 补 scope 隔离测试;③ 补齐 `v1.yaml` 变更后的 Console/BOSS 类型、Core SDK、静态 API 文档生成清单与验证门禁;④ 风险章节增加 Core v1 枚举收窄兼容性说明;⑤ token 查询类型补齐 `token_total`,删除顶部残留的 grill-me 文案。 |
| 2026-08-20 | 补充计量查询 Provider 切换说明:明确由 ANI Gateway 的 `METERING_PROVIDER_MODE` 在 `local` 与 `postgres` adapter 之间选择,列出 `DATABASE_URL` 依赖、Kubernetes Secret 注入示例、重启生效与 fail-closed 验证要求。 |
| 2026-08-25 | **本 V2 文档创建**(从原方案复制修订,原方案保持 2026-08-18 基线不动):平台鉴权四批次 AUTHZ-POLICY-A/B0/B1/C(`plan-authz-policy-compat-contract-pilot-v4.md`)已合入并验证后,对原方案做鉴权适配修订。① 原 §3.6"`scopeAllowedForPath` 新增平台前缀"方案作废——该手工清单已被 V4 迁移方案(`gateway-openapi-authorization-policy-migration-v4.md` §3.1)认定为待消除技术债,经评审改为方案 B;② 新 §3.6:`getPlatformMeteringUsage` 增加 `x-ani-authz`(resource=metering, action=read, boundary=platform, principal_kinds=[user]),pilot allowlist 扩展为 `{listQuotaMeta, getPlatformMeteringUsage}`(推翻 V4 §1.5"不实现第二试点"冻结,已由用户拍板),平台隔离由 V2 链路承载;③ 改动清单替换 auth.go/auth_test.go 为 mode.go/mode_test.go/zz_generated_core_policies.go/pilot_test.go;④ 测试改为逐场景 V2/legacy RPC 调用次数断言;⑤ live gate 与验收命令补 `gen-gateway-authz`/`validate-gateway-authz`/route coverage 门禁及 pilot 配置验证;⑥ §6 补 V2 无回退 503、冻结决策推翻、非 wildcard 角色权限数据三项风险说明。 |
| 2026-08-26 | **本 V3 文档创建**(从 V2 复制修订,V2 文档保持不动作为对照基线):根据评审复盘([review-metering-query-pg-adapter-v2-复盘.md](./review-metering-query-pg-adapter-v2-复盘.md))和两份修订方案落地修订,本条为 SQL 与 handler 输入校验部分,对应[时区与校验修订方案](./review-metering-query-pg-adapter-v2-修订方案-时区与校验.md)。① **评审 #3 时区契约**:§3.3.4 `buildUsageQuery` 两处 `to_char($N::timestamptz, ...)` 改为 `to_char($N::timestamptz AT TIME ZONE 'UTC', ...)`,显式按 UTC 渲染,与会话 TimeZone 无关,同步更新两处 SQL 示例;② **评审 #4 写入侧配套**:§4 改动清单新增 #21 `collectors.go` 和 #22 `metering_collection_service.go` 顺路补 `time.Now().UTC().Format(...)`,§0 决策表"不改动"项修订为"metering-service 采集写入主体不动,仅顺路补 period 时区 `.UTC()`";③ **评审 #5 tenant_id 校验**:§3.4 平台端点 tenant_id query 参数 schema 加 `format: uuid`,§3.5.1 `queryPlatformUsage` handler 用 `uuid.Parse` 校验,非法返回 400;④ **评审 #11 租户侧 required**:§3.5 新增 §3.5.5,抽 `requireTimeRange` 公共函数,`queryUsage` handler 同步强制 start/end 必填,§3.5.1 `queryPlatformUsage` 同步改用该函数;⑤ §5.1 测试列表新增 5 个 handler 输入校验测试用例;⑥ §6 风险表新增 2 行(period 时区契约/租户侧 required 行为变更)。 |
| 2026-08-26 | V3 文档创建续:契约完整性与设计清理部分,对应[契约与设计修订方案](./review-metering-query-pg-adapter-v2-修订方案-契约与设计.md)。① **评审 #9 503 入契约**:§3.4.1 新增 503 响应声明 yaml 示例,v1.yaml 两个 metering 端点 responses 各补 `"503": { $ref: '#/components/responses/ServiceUnavailable' }`,§4 改动清单第 1 条补 ④,属非破坏性变更;② **评审 #10 删死代码**:§3.3 删除 `PgMeteringService.now` 字段、`WithPgMeteringClock` option、`NewPgMeteringService` 中 `s.now` 设置逻辑,§4 改动清单第 9 条同步说明;③ **评审 #12 发布协调**:§3.6.2 新增"存量部署升级路径"段,§6 风险表新增"存量 pilot 部署升级即启动失败"行,§5.3 live gate 补存量部署升级路径;④ **评审 #14 契约小瑕疵**:§3.6.1 yaml 示例删冗余 `security:` 块,§4 改动清单第 1 条补 ⑤ 删 v1.yaml:7783 description "二次 RBAC 校验"句;⑤ **评审 #8 部署检查清单**:§3.7.5 末尾新增"生产部署检查清单"段,§6 风险表新增"quota PG 已配 + metering 漏配开关"行,§5.3 live gate 补启动前配置确认项;⑥ **评审 #13 pilot 关闭+postgres 组合**:§3.6.6 末尾新增行为说明段,明确该组合下平台路由走 legacy default 分支,fail-closed 仍成立但中间状态诡异,建议生产同步开启 pilot;⑦ §6 风险表新增 2 行(存量 pilot 升级失败/quota 漏配开关)。 |
| 2026-08-26 | V3 文档追加文档清理(基于仓库实证复盘 #7):⑬ **评审 #2 行号漂移**:§1.1 第 68 行 `v1.yaml:7469` 改为 `v1.yaml:7777`,与 §3.6.1/附录引用一致;⑭ **评审 #7 决策表措辞**:§0 决策表第 45 行"零新增连接池"改为"零新增 migration、零新增 BYPASSRLS 角色、沿用 quota_runtime 范式新建连接池(metering 与 quota 是独立业务领域,按项目既有范式独立连接池)",方案 §3.7.3 新建连接池设计不变。 |
