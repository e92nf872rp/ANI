# 后端：可用租户列表 API

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/tenant/prd-new-boss-tenant-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/tenant/ux-boss-tenant-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md`

## Description
实现可用租户列表查询：`GET /api/v1/svc/tenant-admins/tenants`（对应 SPEC §4.1 / US-011 / FR-7）。返回 `status <> 'disabled'` 的租户列表，按 `created_at DESC` 排序，不分页。用于邀请管理员时选择目标租户。网关通过 gRPC 转发至 tenant-service `ListAvailableTenants` RPC；tenant-service 内部调用 Core 层 SDK（`ports.TenantSvcClient.ListAvailableTenants`）获取可用租户列表后返回，不直接操作数据库。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/ports/core_tenant.go`、`repo/services/tenant-service/internal/repo/adapters/core/tenant_svc_client.go`

## Acceptance Criteria
- [ ] `GET /tenant-admins/tenants` 无需分页参数，返回 `status <> 'disabled'` 的租户列表
- [ ] tenant-service `ListAvailableTenants` RPC 内部调用 Core SDK `ports.TenantSvcClient.ListAvailableTenants(ctx)` 获取可用租户列表，不直接操作数据库
- [ ] Core SDK 实现（`tenant_svc_client.go`）：调用 Core HTTP API `GET /admin/tenant-admins/available-tenants`，返回 `[]ports.BoundTenant`
- [ ] 响应 200 `{ items: [{ id, name, display_name, status }] }`，status ∈ active / frozen（不含 disabled）
- [ ] 只读端点，platform-admin / platform-ops / platform-readonly 可访问（对齐 SPEC §7.1）
- [ ] 不写审计（只读查询）
- [ ] 网关通过 gRPC 转发至 tenant-service `ListAvailableTenants` RPC，不直连 Core DB
- [ ] 集成测试 `TestHandler_ListAvailableTenants`：验证返回非 disabled 租户、排序、字段完整性（SPEC §9.4 US-011）

## Dependencies
#2, #3

## Type
backend

## Priority
high

## References
- SPEC: §5.1.11 ListAvailableTenants / §4.1 listAvailableTenantsForAdmin / §4.2 AvailableTenantListResponse / §9
- Plan: 租户管理plan v3.0 §5.4.4 / §6.3.17e
- PRD: US-011 / FR-7
