# Issue 4: 链路打通 — 网关路由 + service→Core 骨架（历史骨架；业务已由 005+ 填满）

> **Status: DONE（以实现为准）**  
> 证据：`repo/development-records/tenant-list-issue-004-gateway-integration.md`  
> **最终形态：** 无独立 `TenantListService`；路由与 RPC 挂在 `TenantService` / `tenant_service.go`；Core store 为 `postgres_tenant.go` 的 `PostgresTenant`。

## Document Links
- PRD: [prd-new-boss-tenant-list.md](../../../../prd/boss/tenant/prd-new-boss-tenant-list.md)
- UX: [ux-boss-tenant-list.md](../../../../ux/boss/tenant/ux-boss-tenant-list.md)
- SPEC: [spec-new-boss-tenant-list.md](../../../../spec/boss/tenant/spec-new-boss-tenant-list.md)

## Description

打通 BOSS → Gateway → tenant-service（gRPC）→ Core：注册 Services 19 + Core admin 9 路由、错误映射、`tenantCallCtx`（含 `x-user-id` / `x-request-id`）、main 装配。业务逻辑由 Issue-005～014 实现；本 Issue 交付骨架时曾用 `ErrUnsupported`/`NOT_IMPLEMENTED`→501。

## Scope（最终落地路径）
- Product line: boss
- Code paths:
  - `repo/services/ani-gateway/internal/router/tenant_list_resources.go`
  - `repo/services/ani-gateway/internal/router/admin_tenant_resources.go`
  - `repo/services/tenant-service/main.go`
  - `repo/services/tenant-service/internal/service/tenant_service.go`（非 `tenant_list_service.go`）
  - `repo/services/tenant-service/internal/repo/adapters/core/tenant_svc_client.go`
  - `repo/services/tenant-service/internal/repo/adapters/postgres/tenant_store.go`
  - `repo/pkg/adapters/runtime/postgres_tenant.go`（非 `postgres_tenant_store.go`）

## Acceptance Criteria

### 网关 svc（tenant_list_resources.go）
- [x] 19 端点注册于 `/api/v1/svc`（相对 path 对齐 services/v1.yaml）
- [x] `tenantCallCtx` 注入 metadata（含 actor / request_id，供 lifecycle 归因）
- [x] `mapTenantListError`：gRPC→HTTP；未实现 → 501 `NOT_IMPLEMENTED`
- [x] 写操作幂等由 Gateway 中间件处理；proto 可带 `idempotency_key` 字段（create 等可不传入 gRPC）
- [x] RBAC：写 platform-admin/ops；读含 platform-readonly

### 网关 Core admin
- [x] 9 端点：create/list/update/freeze/unfreeze/disable/auth/lifecycle
- [x] lifecycle 写路径注入 `WithTenantLifecycleAttribution`（`X-ANI-Actor-User-ID` / `X-Request-ID`）
- [x] `writeAdminTenantError` 覆盖 TENANT_NAME_CONFLICT / TENANT_STATE_INVALID 等

### Core / service 骨架 → 现已填满
- [x] `PostgresTenant` 实现 ports.TenantService 扩展方法（业务在 005+）
- [x] `TenantService.Register` 注册全部 list RPC（含 TestTenantSso 仍可 stub→501，见 Issue-010）
- [x] main 装配：SSO loader/tester 可为 `nil`（TestTenantSso 留给 010）
- [x] 最小闭环：GetTenantDetail 可基于 Core getTenant

## Dependencies
Issue 1、2、3

## Type
backend

## Priority
high

## References
- SPEC: §2.1 / §2.3 / §2.4
- Record: `repo/development-records/tenant-list-issue-004-gateway-integration.md`
