# TENANT-ADMIN-ISSUE-04：网关接入 — gRPC client + Core UserAdminService + 路由 + 错误映射

> **批次类型：** Feature batch（BOSS 租户管理员管理 Issue #4）
> **完成日期：** 2026-08-24
> **Scope：** `repo/services/ani-gateway/internal/router/`、`repo/services/ani-gateway/tenant_admin_runtime.go`（新建）、`repo/services/ani-gateway/main.go`
> **依赖：** #1 OpenAPI 契约、#2 接口/数据模型、#3 数据库迁移
> **Product line：** boss

## 交付内容

在 ani-gateway 落地租户管理员 REST → gRPC 转发 + Core DB 直连双路径接入，覆盖 SPEC §4.1 全部 12 个端点。

### 新增文件

| 文件 | 职责 |
|------|------|
| `tenant_admin_resources.go` | 路由注册、gRPC 转发 handler、Core 直连 handler、JSON mapper、错误映射 |
| `tenant_admin_runtime.go` | `newGatewayTenantAdminService`：DATABASE_URL → ConnectMetadataStore → NewPostgresTenantAdmin |

### 修改文件

| 文件 | 变更 |
|------|------|
| `main.go` | 调用 `newGatewayTenantAdminService` + 注入 `RegisterOptions.TenantAdminService` |
| `router.go` | `RegisterOptions` 新增 `TenantAdminService` 字段 + `registerTenantAdmins(svc, options.TenantAdminService)` |

### 双路径架构

```
tenantAdminAPI struct {
    admins      tenantv1.TenantAdminServiceClient  // gRPC → tenant-service
    tenantAdmin ports.TenantAdminService            // Core DB 直连
}
```

- **gRPC 路径（5 端点）：** `GET /tenant-admins`、`GET /tenants/:tenantId/admins/:userId`、`GET .../audit-logs`、`POST .../invite`、`POST .../invitation/resend` → `tenantv1.TenantAdminServiceClient`（`127.0.0.1:9105`，5s timeout）
- **Core 直连路径（7 端点）：** `PUT/GET .../role`、`GET .../changeable-roles`、`POST .../reset-password`、`POST .../disable`、`POST .../enable`、`DELETE .../admins/:userId` → `ports.TenantAdminService`（`PostgresTenantAdmin` 适配器，platform bypass RLS）

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| `tenant_admin_resources.go` gRPC client 持有模式对齐 `tenant_plans.go` | `dialTenantAdminGRPCClient` + `tenantAdminAPI` struct，nil 守卫返回 502 | ✅ |
| `TENANT_SERVICE_ADDR` 缺省 `127.0.0.1:9105` | 复用 `tenantServiceDefaultAddr` 常量 | ✅ |
| 每方法 5s `context.WithTimeout` | `tenantCallCtx` 共享 helper（`tenant_plans.go`） | ✅ |
| 注册全部 12 端点对齐 SPEC §4.1 | `registerTenantAdminsWithClient` 12 条路由 | ✅ |
| 在 `svc := h.Group("/api/v1/svc")` 下注册 | `router.go` line 118 | ✅ |
| gRPC 错误码 → HTTP + APIError | `mapTenantAdminGRPCError` 业务码前缀匹配 + gRPC code fallback | ✅ |
| Core 错误码 → HTTP + APIError | `mapTenantAdminCoreError` sentinel errors.Is | ✅ |
| 幂等：POST/PUT 经中间件；DELETE 不幂等 | 全局 `idempotency.go` 中间件覆盖；DELETE 无 body key 转发 | ✅ |
| RBAC：读端点含 readonly；写端点不含 | 依赖全局 `rbac.go`（粒度问题见 Deviations D1） | ⚠️ |
| `tenant_admin_runtime.go` 对齐配额 Core 集成模式 | `newGatewayTenantAdminService` → DATABASE_URL → ConnectMetadataStore → NewPostgresTenantAdmin | ✅ |
| `main.go` 注入 + `RegisterOptions` 字段 | `TenantAdminService: tenantAdminService` | ✅ |
| `UserAdminService` 10 方法覆盖 | `ports.TenantAdminService` 接口 10 方法（`PostgresTenantAdmin` 全部声明） | ✅ |
| 网关 Core 直连端点经 `TenantAdminService` 调 Core DB | 7 个 handler 调 `api.tenantAdmin.*` | ✅ |

## Design Decisions

### D1：双路径拆分——邀请经 gRPC，用户/角色经 Core 直连

- **Ambiguity：** SPEC §2.3 Module Interactions 描述了邀请/重发经 tenant-service gRPC、改角色/重置密码/禁用启用删除经 Core SDK UserSvcClient，但 SPEC §2.2 Component Design 中 `UserAdminService` 描述为"Core DB 直连 `ports.UserAdminService`"，两种表述（Core SDK API 调用 vs Core DB 直连）存在歧义。
- **Choice：** 采用 Core DB 直连（`ports.TenantAdminService` + `PostgresTenantAdmin` 适配器），不走 Core SDK HTTP API 中转。
- **Rationale：** ① 对齐配额套餐已落地的 Core 集成模式（`newGatewayQuotaStore` / `newGatewayTenantPlanService` 均为 DB 直连）；② 网关进程与 Core 控制面 DB 同属内部网络，DB 直连延迟低于 HTTP 自调；③ 避免 Core API 鉴权自调用环。SPEC §3.4 注释"运行时 CRUD 通过 Core SDK UserSvcClient 调 Core API"是理想架构，但当前阶段网关 DB 直连更实际。

### D2：gRPC 连接独立于配额套餐连接

- **Ambiguity：** SPEC 未说明 tenant-service gRPC 连接是否应共享。配额套餐已存在 `newTenantPlansAPI()` 创建独立 conn。
- **Choice：** `dialTenantAdminGRPCClient()` 独立创建 conn，不与 `tenantPlansAPI` 共享。
- **Rationale：** ① 路由注册函数签名独立（`registerTenantAdmins` vs `registerTenantPlans`），共享 conn 需重构注册顺序；② 两条链路功能正交，独立 conn 隔离故障；③ 当前为开发阶段，连接数优化优先级低。见 Tradeoffs T1 进一步讨论。

### D3：gRPC metadata 传播 `x-request-id` + `x-user-id`

- **Choice：** `tenantCallCtx` 共享 helper（`tenant_plans.go` line 589-596）在 gRPC outgoing context 注入 `x-request-id` 和 `x-user-id`。
- **Rationale：** tenant-service 写审计日志需关联网关请求 ID 和操作者 ID。这两个 metadata 从中间件上下文提取（`middleware.GetRequestID` / `middleware.GetUserID`），对用户不可见。

### D4：Core 直连 handler 不传 `idempotency_key` 到后端

- **Choice：** 7 个 Core 直连 handler 提取 `idempotency_key` body 字段但不传入 `ports.TenantAdminService` 方法（接口无该参数），仅依赖全局幂等中间件去重。
- **Rationale：** Core DB 直连为单进程调用，无网络重试风险，幂等中间件 Redis 缓存足够去重。gRPC 路径因网络重试需转发 key 到 tenant-service 做服务端幂等。两种策略匹配各自路径的风险模型。

### D5：`PostgresTenantAdmin` 骨架返回 `ErrUnsupported`

- **Choice：** `PostgresTenantAdmin` 适配器声明全部 10 方法但均返回 `ports.ErrUnsupported`，SQL 实现留给后续 Issue。
- **Rationale：** Issue-004 的 AC 聚焦网关接入链路（路由注册、gRPC 转发、错误映射、依赖注入），`PostgresTenantAdmin` 的 SQL 实现属于独立 Issue（对应 SPEC §10.2 Phase 4）。骨架先行保证编译通过、路由可达、错误链路验证完整。

## 验证命令

```bash
cd repo/services/ani-gateway
go build ./...
go test ./internal/router/ -run TestTenantAdmin -v
go vet ./...
```

## 边界声明

- 本 Issue 完成网关接入链路搭建（路由注册、gRPC 转发、Core 直连注入、错误映射、JSON 映射），不包含 `PostgresTenantAdmin` 的 SQL 实现（骨架返回 `ErrUnsupported`）。
- gRPC 转发端点的业务逻辑由 tenant-service 实现（Issue-005），网关仅负责转发与错误映射。
- 审计中间件的 DB 写入为 TODO 桩，不属于本 Issue 范围。
- RBAC 中间件为全局共享组件，本 Issue 未修改其资源推断逻辑。
