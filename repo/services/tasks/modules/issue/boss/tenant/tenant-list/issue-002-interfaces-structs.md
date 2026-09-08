# Issue 2: 接口与数据模型 — proto messages + ports（历史：只写接口）

> **Status: DONE（以实现为准）**  
> 证据：`repo/development-records/tenant-list-issue-002-interfaces-structs.md`  
> **最终形态说明：** 无独立 `TenantListService`；list 相关 **19 RPC** 挂在既有 `TenantService`（`tenant_plan.proto`）；`tenant_list_service.proto` **仅 messages**。

## Document Links
- PRD: [prd-new-boss-tenant-list.md](../../../../prd/boss/tenant/prd-new-boss-tenant-list.md)
- UX: [ux-boss-tenant-list.md](../../../../ux/boss/tenant/ux-boss-tenant-list.md)
- SPEC: [spec-new-boss-tenant-list.md](../../../../spec/boss/tenant/spec-new-boss-tenant-list.md)

## Description

定义租户列表管理的接口与数据模型：gRPC messages、Core `ports.TenantService` 扩展、tenant-service `TenantSvcClient` / store / SSO 端口。历史上本 Issue「只写接口不写实现」；后续 Issue-004+ 已填满实现——**本文以当前仓库形态为准，不再要求独立 `TenantListService`。**

> pb.go 由人工 `buf generate`。

## Scope（最终落地路径）
- Product line: boss
- Code paths:
  - `repo/api/proto/tenant/v1/tenant_list_service.proto`（**messages only**）
  - `repo/api/proto/tenant/v1/tenant_plan.proto`（`service TenantService` 上挂 19 个 list RPC）
  - `repo/pkg/ports/tenant.go`
  - `repo/services/tenant-service/internal/repo/ports/core_tenant.go`
  - `repo/services/tenant-service/internal/repo/ports/tenant_store.go`（配额变更等，非独立 `QuotaChangeStore` 文件名）
  - `repo/services/tenant-service/internal/repo/ports/sso.go`（非 `sso_test.go`）
  - `repo/services/tenant-service/internal/repo/ports/errors.go`（按需）

## Acceptance Criteria

### proto
- [x] `tenant_list_service.proto`：messages（TenantListItem / TenantDetail / TenantAuth* / SsoTestResult / QuotaChange* / TenantLifecycleEntry / TenantAuditLogEntry / TenantScopedAdmin 等）
- [x] `tenant_plan.proto` 的 `TenantService`：**19 RPC** — ListAvailablePlans、CreateTenant、ListTenants、GetTenantDetail、UpdateTenant、FreezeTenant、UnfreezeTenant、DisableTenant、GetTenantAuth、UpdateTenantSso、TestTenantSso、UpdateTenantMfa、GetTenantQuota、SubmitQuotaChangeRequest、ListQuotaChangeRequests、ReviewQuotaChangeRequest、ListTenantLifecycle、ListTenantAuditLogs、ListTenantAdmins
- [x] package / go_package 对齐既有约定
- [x] 不在本 Issue 强制提交 pb.go

### Core ports（pkg/ports/tenant.go）
- [x] `Tenant` 扩展：ContactEmail、FrozenAt、DisabledAt、UserCount、AdminCount、Auth（`TenantAuthSummary` 仅两布尔）
- [x] 枚举分离（不可混用）：
  - `TenantStatus`：`active | frozen | disabled`
  - `TenantLifecycleAction`：`create | freeze | unfreeze | disable`（`tenant_lifecycle.action`）
- [x] 实体：`TenantAuth`、`TenantLifecycleEntry`、`CreateTenantInput`、`UpdateTenantInput`、`TenantAuthPatch`、`ListTenantsFilter`、`TenantLifecycleFilter`
- [x] `TenantService` +9：CreateTenant、ListTenants、UpdateTenant、Freeze/Unfreeze/Disable、GetTenantAuth、UpdateTenantAuth、ListTenantLifecycle
- [x] 错误哨兵：ErrTenantNameConflict、ErrTenantStateInvalid、ErrTenantNotFound 等
- [x] lifecycle 归因：**不在** `CreateTenantInput` / Freeze 等方法签名传 user_id；由 Gateway → headers → Core ctx（`WithTenantLifecycleAttribution`）注入

### tenant-service ports
- [x] `TenantSvcClient` 扩展 9 方法（入参出参用 service ports 实体）
- [x] `TenantStore`：配额变更 InsertPending* / List* / SetStatus*（跨请求同维 pending 允许；同 request 同维靠 PK）
- [x] `SsoConfigLoader` / `OidcDiscoveryTester`（`sso.go`）；实现可延后到 Issue-010

## Dependencies
Issue 1

## Type
backend

## Priority
high

## References
- SPEC: §2.4 / §3.2
- Record: `repo/development-records/tenant-list-issue-002-interfaces-structs.md`
