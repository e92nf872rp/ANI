# 接口与数据模型设计

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/tenant/prd-new-boss-tenant-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/tenant/ux-boss-tenant-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md`

## Description
设计租户管理员模块的 gRPC 接口与数据模型：定义 `tenant_admin_service.proto`（service RPC 与数据模型）、生成 pb；在 tenant-service `internal/repo/ports/tenant_admin_store.go` 定义 Store 接口与领域模型（TenantAdminInvitation / AdminWithTenant / InvitationFlag / AuditLogListItem 等），在 `core_tenant_admin.go` 定义 TenantAdminSvcClient 接口（封装 Core API）。本 Issue 只产出接口/模型设计，不实现具体业务逻辑（对齐 plan PR-2，handler 占位返回 501）。

## Scope
- Product line: boss
- Code paths allowed: `repo/api/proto/tenant/v1/`、`repo/pkg/generated/pb/tenant/v1/`、`repo/services/tenant-service/internal/repo/ports/`（含 `core_tenant_admin.go`、`core_tenant.go`）、`repo/services/tenant-service/internal/repo/adapters/`（骨架）

## Acceptance Criteria
- [ ] `repo/api/proto/tenant/v1/tenant_admin_service.proto` 定义 TenantAdminService 全部 RPC（对应 SPEC §4.1 十三个端点：InviteTenantAdmin / ResendTenantAdminInvitation / ListAllTenantAdmins / GetTenantAdminDetail / UpdateTenantAdminRole / GetTenantAdminRole / ResetTenantAdminPassword / DisableTenantAdmin / EnableTenantAdmin / DeleteTenantAdmin / ListTenantAdminAuditLogs / ListAvailableTenants / ListTenantRoles）与消息类型（含 `ListAvailableTenantsRequest` / `ListAvailableTenantsResponse` / `AvailableTenant` / `ListTenantRolesRequest` / `ListTenantRolesResponse` / `TenantAssignableRole`）
- [ ] `repo/services/tenant-service/internal/repo/ports/tenant_admin_store.go` 定义 TenantAdminStore 接口与领域模型（对齐 SPEC §3.2）：TenantAdminInvitation / AdminWithTenant / TenantRef / TenantAdminListFilter / ListResult / InviteInput / InvitationResult / UserPermissions / InvitationFlag / AssignableRole / AuditLogListItem / TenantAdminAuditLogFilter / TenantAdminAuditLogListResult。**Store 仅操作 tenant_admin_invitation / audit_logs 表，不直接操作 users / user_roles / roles / tenants 表**
- [ ] `repo/services/tenant-service/internal/repo/ports/core_tenant_admin.go` 定义 TenantAdminSvcClient 接口（封装 Core `/api/v1/admin/...` API）：MatchUser / IsAlreadyAdmin / GetUser / BatchGetUsers / GetAdminDetail / ListTenantAdmins / ChangeRole / GetRolePermissions / ListAssignableRoles / SetStatus / SoftDelete / ResetPassword
- [ ] `repo/services/tenant-service/internal/repo/ports/core_tenant.go` 定义 TenantSvcClient 接口（封装 Core 租户查询 API）：GetTenant / ListAvailableTenants
- [ ] TenantAdminStore 方法签名覆盖邀请、重发、邀请标记、操作历史（HasPendingInvitation / InsertInvitation / GetLatestInvitation / UpdateInvitation / ListInvitationFlags / GetInvitationFlags / ListAuditLogs）；TenantAdminSvcClient 方法签名覆盖跨租户列表、详情、改角色、权限查询、可分配角色查询、重置、禁用/启用/删除（MatchUser / IsAlreadyAdmin / GetUser / BatchGetUsers / GetAdminDetail / ListTenantAdmins / ChangeRole / GetRolePermissions / ListAssignableRoles / SetStatus / SoftDelete / ResetPassword）
- [ ] 领域模型与 SPEC §4.2 契约字段一致（is_inviting / is_expired / created_at?/updated_at? / tenant 对象，不含 password_hash）
- [ ] adapters 骨架：`internal/repo/adapters/postgres/tenant_admin_store.go` 提供方法签名占位，handler 返回 `501 Not Implemented`
- [ ] `make test` 通过（含编译 + 现有测试）
- [ ] PR 描述含接口签名摘要

## Dependencies
#1 (OpenAPI 契约)

## Type
backend

## Priority
high

## References
- SPEC: §2.2 Component Design / §2.4 File Structure / §3.2 Entity Definitions
- Plan: 租户管理plan v3.0 §12.2 (PR-2)
