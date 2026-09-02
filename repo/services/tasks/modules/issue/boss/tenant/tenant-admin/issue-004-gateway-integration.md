# 网关接入：gRPC client + Core SDK TenantAdminSvcClient + 路由 + 错误映射

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/tenant/prd-new-boss-tenant-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/tenant/ux-boss-tenant-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md`

## Description
在 ani-gateway 接入租户管理员 REST → gRPC 转发链路：新增 tenant gRPC client、注册 `/api/v1/svc/tenants/{tenantId}/admins/*`、`/tenant-admins`、`/tenant-admins/tenants` 路由，幂等与错误码映射。所有端点均通过 gRPC 转发到 tenant-service（不直连 Core DB），tenant-service 内部通过 Core SDK 调用 Core API。当前网关无 tenant gRPC client / 转发链路（tenant_resources.go 为 stub），需新建。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/ani-gateway/internal/router/`、`repo/services/ani-gateway/internal/pkg/errors/`、`repo/services/ani-gateway/main.go`

## Acceptance Criteria

### gRPC client 转发链路
- [ ] 新建 `internal/router/tenant_admin_resources.go`：gRPC client 在 router 层持有（不放 middleware，对齐 `tenant_plans.go` 的 `tenantPlansAPI` + `newTenantPlansAPI` 模式），按 `TENANT_SERVICE_ADDR`（缺省 `127.0.0.1:9105`）读取，`grpc.NewClient` 建立共享 conn；每方法 `context.WithTimeout` 超时（5s）；conn 建立失败时字段为 nil，各 handler 做 nil 守卫兜底返回 502
- [ ] 注册全部租户管理员路径（对齐 #1 契约 & SPEC §4.1，含 `GET /tenant-admins/tenants` 可用租户列表），经 gRPC client 转发到 tenant-service
- [ ] 在 `internal/router/router.go` 的 `svc := h.Group("/api/v1/svc")` 下调用注册（对齐现有 `registerTenantPlans`）
- [ ] 错误码映射：gRPC 业务错误 → HTTP 状态 + `APIError{code,message,request_id,details}`（对齐 SPEC §6.1；复用 `internal/pkg/errors/errors.go` 的 RespondError）
- [ ] 幂等：POST/PUT 经现有 `idempotency.go` 中间件（header `Idempotency-Key` 或 body `idempotency_key`）；DELETE 不幂等
- [ ] RBAC：读端点（列表/详情/历史/可用租户列表）允许 platform-readonly；写端点与 GET role 不允许（对齐 SPEC §7.1）
- [ ] 集成/冒烟：各路由经网关转发可达 tenant-service，错误码能正确映射为 HTTP 状态

### Core SDK 集成（tenant-service 侧，非网关侧）
- [ ] Core SDK `TenantAdminSvcClient`（`core_tenant_admin.go`）在 tenant-service 侧集成，网关不直连 Core DB
- [ ] tenant-service 内部通过 `TenantAdminSvcClient` 调用 Core `/api/v1/admin/...` API 完成用户/角色/权限操作（GetUser / ListTenantAdmins / ChangeRole / GetRolePermissions / ListAssignableRoles / SetStatus / SoftDelete / ResetPassword）
- [ ] 网关 handler struct 仅持有 gRPC client（`TenantAdminServiceClient`），不持有 `UserAdminService`；网关 handler struct 不持有 `TenantAdminSvcClient`

## Dependencies
#2, #3

## Type
backend

## Priority
high

## References
- SPEC: §2.3 Module Interactions / §2.4 gateway files / §7.1 AuthZ / §6.3 Failure Modes
- Plan: 租户管理plan v3.0 §6.1（TENANT_SERVICE_ADDR 缺省 127.0.0.1:9105）
