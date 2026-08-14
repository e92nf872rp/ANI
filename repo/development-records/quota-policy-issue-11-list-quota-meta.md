# QUOTA-POLICY-ISSUE-11：查询配额元数据 API — 网关 + service 透传 Core

> **批次类型：** Feature batch（BOSS 租户配额套餐功能流 Issue #11）
> **完成日期：** 2026-08-11
> **Scope：** `repo/api/proto/tenant/v1/tenant_plan.proto`、`repo/api/openapi/services/v1.yaml`、`repo/services/ani-gateway/internal/router/tenant_plans.go`、`repo/services/tenant-service/internal/service/tenant_plan_service.go`
> **依赖：** #2 gRPC 接口与 ports、#4 网关接入、#8 QuotaSvcClient.ListQuotaMeta（已实现）
> **Product line：** boss

## 交付内容

实现 `ListQuotaMeta`（GET /quota-meta）端到端链路：proto RPC 定义 + 网关 handler + service 透传 Core。复用 issue-008 已实现的 `QuotaSvcClient.ListQuotaMeta`，仅做对外暴露。

### Proto 定义

- 新增 `ListQuotaMeta` RPC + `ListQuotaMetaRequest`（空）+ `ListQuotaMetaResponse{ items[] }` + `QuotaMetaItem` message。
- `QuotaMetaItem` 5 字段：resource_type / display_name / unit / default_quota / is_discrete（无 enabled，Core 仅返回启用维度）。

### 网关 handler

- `GET /quota-meta` 无入参，调 gRPC `ListQuotaMeta`，手动映射为 snake_case JSON。
- nil 守卫 + request_id 透传 + 错误映射（Core 不可用 → 502）。

### Service 层

- 调 `core.ListQuotaMeta(ctx)` 拉取启用维度。
- `ErrCoreUnavailable` → `codes.Unavailable` + `GRPC_CLIENT_UNAVAILABLE` 前缀。
- 映射为 `QuotaMetaItem` 5 字段透传。

### Core 客户端（issue-008 已实现，复用）

- HTTP GET `/admin/quota-meta`，解析 5 字段，空 resource_type 过滤。
- 非 2xx / 网络错误 / 解析错误统一包装 `ErrCoreUnavailable`。
- 无本地缓存，每次远程调用。

### OpenAPI

- `GET /quota-meta` 端点 + `QuotaMetaItem` / `QuotaMetaListResponse` schema。
- 响应码 200 + 401 + 403 + 502。

### 测试覆盖

| 测试 | 覆盖 | 结果 |
|---|---|---|
| `TestTenantPlanService_ListQuotaMeta` | 正常透传 + 5 字段映射 + calls=1 | ✅ |
| `TestTenantPlanService_ListQuotaMeta_CoreUnavailable` | Core 不可用 → Unavailable | ✅ |
| `TestQuotaSvcClient_ListQuotaMeta_Fetch` | Core HTTP 正常 fetch | ✅ |
| `TestQuotaSvcClient_ListQuotaMeta_Unavailable` | Core HTTP 不可用 | ✅ |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| GET /quota-meta 端点 | 网关路由 + OpenAPI 定义 | ✅ |
| 返回 5 字段 | resource_type/display_name/unit/default_quota/is_discrete | ✅ |
| proto RPC 定义 | ListQuotaMeta + ListQuotaMetaRequest/Response + QuotaMetaItem | ✅ |
| OpenAPI schema | QuotaMetaItem + QuotaMetaListResponse | ✅ |
| 网关 handler | listQuotaMeta + 路由注册 | ✅ |
| 复用 QuotaSvcClient | issue-008 已实现，service 直接调 | ✅ |
| Core 不可用 → 502 | codes.Unavailable → mapTenantPlanError | ✅ |
| 无缓存 | 每次远程调用，测试验证 calls=1 | ✅ |
| 编译 | `go build` → EXIT=0 | ✅ |
| 测试 | `go test -run QuotaMeta` → 4/4 PASS | ✅ |
| review-it | clean，无 actionable findings | ✅ |

## 验证命令

```bash
cd repo
go build ./services/ani-gateway/... ./services/tenant-service/...
go test ./services/tenant-service/internal/... -run "QuotaMeta" -v
```

## 边界声明

- 本 Issue 仅做透传暴露，Core 客户端实现复用 issue-008。
- `QuotaMetaItem` 不含 `enabled` 字段——Core API 仅返回启用维度，前端只看启用项。
- 无入参、无分页、无过滤——Core 返回全量启用维度。

## Open Questions

None — 实现简洁，按 issue spec 透传 Core 结果，无偏离。
