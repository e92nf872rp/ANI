# TENANT-ADMIN-ISSUE-005：可用租户列表 API — gRPC RPC + Core SDK 适配器 + 网关转发

> **批次类型：** Feature batch（BOSS 租户管理员管理 Issue #5）
> **完成日期：** 2026-08-25
> **Scope：** `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/core/`、`repo/services/tenant-service/internal/repo/ports/`、`repo/services/tenant-service/main.go`、`repo/services/ani-gateway/internal/router/`、`repo/pkg/ports/tenant.go`、`repo/pkg/adapters/runtime/postgres_tenant.go`
> **依赖：** #1 OpenAPI 契约、#2 接口/数据模型、#4 网关接入
> **Product line：** boss

## 交付内容

落地"查询可用租户列表"端到端链路：网关 gRPC 转发 → tenant-service `ListAvailableTenants` RPC → Core SDK 适配器 → ani-gateway Core handler → PG 查询。覆盖 SPEC §5.1.11 / US-011 / FR-7。

### 修改文件

| 文件 | 变更 |
|------|------|
| `tenant-service/internal/repo/ports/core_tenant.go` | `TenantSvcClient` 接口新增 `ListAvailableTenants(ctx) ([]BoundTenant, error)` 方法 |
| `tenant-service/internal/repo/adapters/core/tenant_svc_client.go` | 实现 `ListAvailableTenants`：调 Core `GET /admin/tenant-admins/available-tenants`，解析 `items` 数组为 `[]BoundTenant`（id 必须 UUID） |
| `tenant-service/internal/service/tenant_admin_service.go` | `TenantAdminService` 新增 `tenants ports.TenantSvcClient` 字段；`NewTenantAdminService` 签名加 `tenants` 参数；实现 `ListAvailableTenants` RPC |
| `tenant-service/main.go` | `NewTenantAdminService(coreTenantAdmins, coreTenants)` 注入 Core 租户客户端 |
| `pkg/ports/tenant.go` | `TenantService` 接口新增 `ListAvailableTenants(ctx) ([]TenantSummary, error)` 方法 |
| `pkg/adapters/runtime/postgres_tenant.go` | 实现 `ListAvailableTenants`：`SELECT id, name, display_name, status FROM tenants WHERE status <> 'disabled' ORDER BY created_at DESC` |
| `ani-gateway/internal/router/admin_tenant_resources.go` | 新增 `GET /admin/tenant-admins/available-tenants` 路由 + handler（Core 直连 `ports.TenantService`） |
| `tenant-service/internal/repo/adapters/core/sdk_client.go` | `init()` 设置 `http.DefaultClient.Timeout = 10s`（SDK 不接收 context，全局兜底） |

### 新增测试

| 测试 | 覆盖点 |
|------|--------|
| `TestTenantAdminService_ListAvailableTenants` | happy path：返回 active/frozen 租户、字段完整、disabled 不泄露 |
| `TestTenantAdminService_ListAvailableTenants_NilClient` | `s.tenants == nil` → `codes.Unavailable` |
| `TestHandler_ListAvailableTenants` | 网关层 happy path：gRPC 转发、JSON 字段映射、顺序保持 |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| `GET /tenant-admins/tenants` 无需分页参数，返回 `status <> 'disabled'` 租户列表 | RPC 无入参（`_ *ListAvailableTenantsRequest`）；Core SQL `WHERE status <> 'disabled'` | ✅ |
| tenant-service `ListAvailableTenants` RPC 调 Core SDK，不直接操作数据库 | `s.tenants.ListAvailableTenants(ctx)` → `c.sdk.Request("GET", ...)` | ✅ |
| Core SDK 实现 `WHERE status <> 'disabled' ORDER BY created_at DESC` | `postgres_tenant.go:84-89` SQL | ✅ |
| 响应 200 `{ items: [{ id, name, display_name, status }] }` | 网关 handler JSON 映射 4 字段；OpenAPI `TenantSummary` schema 对齐 | ✅ |
| 只读端点，不写审计 | RPC 无 `writeAudit*` 调用；注释标注"只读、无审计" | ✅ |
| 网关通过 gRPC 转发，不直连 Core DB | `tenant_admin_resources.go:88` 调 `api.admins.ListAvailableTenants`（gRPC client） | ✅ |
| 集成测试 `TestHandler_ListAvailableTenants` | 验证返回非 disabled、字段完整、顺序保持 | ✅ |

## Design Decisions

### D1：tenant-service 调 Core 用 HTTP SDK，不直连 DB

- **Ambiguity：** SPEC §2.3 描述 tenant-service "调用 Core 层 SDK 获取"，但 tenant-service 与 Core 控制面 DB 是否同进程存在歧义。
- **Choice：** tenant-service 经 Core Go SDK（`anisdk.Client`）HTTP 调 ani-gateway 的 `/admin/tenant-admins/available-tenants`，不直连 PG。
- **Rationale：** ① 对齐 SPEC §3.4 "Services 层不得 import `pkg/ports`/`pkg/adapters`，只能通过 Core SDK 调 REST"；② 保持 Core/Services 控制平面边界；③ 与配额套餐已落地的 Core 集成模式一致（`QuotaSvcClient`/`TenantPlanSvcClient` 均走 HTTP SDK）。

### D2：Core 端点实现在 ani-gateway，不在独立 Core 服务

- **Choice：** `GET /admin/tenant-admins/available-tenants` 的 handler 与 SQL 实现都在 ani-gateway 进程（`admin_tenant_resources.go` + `postgres_tenant.go`）。
- **Rationale：** ani-gateway 同时承担 Core API 与网关转发职责；Core 端点直接查控制面 PG（`WithPlatformTx` bypass RLS），与 `/admin/tenants/:tenant_id` 同一 `adminTenantAPI`。tenant-service 的 SDK 调用回环到 ani-gateway HTTP 端点。

### D3：`BoundTenant` 复用 `TenantPlanStore` 已有 DTO

- **Choice：** `ListAvailableTenants` 返回 `[]ports.BoundTenant`（已在 `tenant_plan_store.go` 定义），不新增 DTO。
- **Rationale：** `BoundTenant` 字段（ID/Name/DisplayName/Status）与 Core `TenantSummary` 4 字段完全对齐，邀请管理员选择器与套餐绑定租户列表用同一视图，避免重复定义。

### D4：SDK 不接收 context，用 `http.DefaultClient.Timeout` 兜底

- **Choice：** `init()` 设置 `http.DefaultClient.Timeout = 10s`，`ListAvailableTenants` 显式 `_ = ctx`。
- **Rationale：** `anisdk.Client.Request` 内部用 `http.NewRequest` + `http.DefaultClient.Do`，不接收 `context.Context`，无法注入 per-request deadline。全局 10s 兜底避免 Core 挂起时 goroutine 无限阻塞。网关侧 gRPC 调用有 5s 超时（`tenantCallTimeout`），会先于 Core HTTP 超时返回。

## Deviations

None — 实现遵循 SPEC §5.1.11 / Issue-005 AC 如文。

## Tradeoffs

### T1：HTTP 回环 vs DB 直连

- **备选 A（已选）：** tenant-service → HTTP SDK → ani-gateway `/admin/*` → PG
- **备选 B：** tenant-service 直连控制面 PG（绕过 ani-gateway）
- **A 优势：** 保持 Core/Services 边界；RLS/bypassRLS 由 Core 统一管理；对齐 SPEC
- **A 劣势：** 多一次 HTTP 跳，延迟略高；端口/鉴权需对齐；回环依赖 ani-gateway 在线
- **B 优势：** 延迟低，无回环
- **B 劣势：** 违反 SPEC §3.4 边界约束；tenant-service 需引入 `pgx` 直连控制面 DB，破坏分层
- **结论：** 选 A，符合架构约束

### T2：全局 `http.DefaultClient.Timeout` vs 自定义 Client

- **备选 A（已选）：** `init()` 改 `http.DefaultClient.Timeout`
- **备选 B：** 自建 `http.Client` 传给 SDK
- **A 劣势：** 全局污染，影响所有用 `http.DefaultClient` 的代码
- **B 优势：** 隔离
- **B 劣势：** SDK `Request` 硬编码用 `http.DefaultClient.Do`，无法注入
- **结论：** 选 A，SDK 限制下唯一可行方案；tenant-service 进程内无其他重度 HTTP 使用方，污染可接受

## Open Questions

None — 实现与 SPEC/Issue 对齐，测试全绿。

## 验证命令

```bash
cd repo/services/tenant-service
go build ./...
go test ./internal/... -run "TestTenantAdminService_ListAvailableTenants" -v
go test ./internal/...

cd repo/services/ani-gateway
go build ./...
go test ./internal/router/ -run "TestHandler_ListAvailableTenants" -v
```

## 边界声明

- 本 Issue 完成 `ListAvailableTenants` 端到端链路（RPC + Core 适配器 + 网关转发 + Core handler/SQL + 测试）。
- `TenantAdminService` 其余 12 个 RPC（邀请/重发/列表/详情/角色/密码/禁用启用删除/审计）仍返回 `UNIMPLEMENTED`，属后续 Issue 范围。
- `fakeTenantClient` 新增 `available` 字段与 `ListAvailableTenants` 方法，以兼容 `ports.TenantSvcClient` 接口扩展。
