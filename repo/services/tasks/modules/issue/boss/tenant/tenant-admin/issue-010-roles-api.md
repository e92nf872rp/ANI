# 后端：查询可变角色列表 API

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/tenant/prd-new-boss-tenant-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/tenant/ux-boss-tenant-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md`

## Description
实现可变角色列表查询：`GET /api/v1/svc/tenants/{tenantId}/roles`（对应 SPEC §4.1 / §5.1.12 / US-012 / FR-8）。返回 `name` 前缀不为 `platform-` 且 `tenant_id` 为空或与传入 `tenantId` 相同的角色列表，不分页。用于修改管理员角色时选择目标角色。网关通过 gRPC 转发至 tenant-service `ListTenantRoles` RPC；tenant-service 内部调用 Core SDK 查询 `roles` 表获取可变角色列表后返回。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/core/`、`repo/services/ani-gateway/internal/router/`、`repo/api/proto/tenant/v1/`、`repo/pkg/generated/pb/tenant/v1/`

## Acceptance Criteria
- [ ] `GET /tenants/{tenantId}/roles` 无需分页参数，返回该租户可分配的角色列表
- [ ] 查询条件：`name NOT LIKE 'platform-%'` 且 `tenant_id IS NULL OR tenant_id = $tenantId`
- [ ] 网关通过 gRPC 转发至 tenant-service `ListTenantRoles` RPC，不直连 Core DB
- [ ] tenant-service `ListTenantRoles` RPC 内部调用 Core SDK `ListAssignableRoles` 查询 `roles` 表，不直接操作数据库
- [ ] 响应 200 `{ items: [{ id, name, tenant_id, permissions }] }`，不分页
- [ ] 只读端点，platform-admin / platform-ops / platform-readonly 可访问（对齐 SPEC §7.1）
- [ ] 不写审计（只读查询）
- [ ] 集成测试 `TestHandler_ListTenantRoles`：验证排除 platform- 前缀角色、tenant_id 过滤、字段完整性（SPEC §9.4 US-012）

## Dependencies
#2, #3

## Type
backend

## Priority
high

## References
- SPEC: §5.1.12 ListTenantRoles / §4.1 listTenantRoles / §4.2 roles schema / §7.1 / §9
- Plan: 租户管理plan v3.0 §4.1.4 查询可变角色列表
