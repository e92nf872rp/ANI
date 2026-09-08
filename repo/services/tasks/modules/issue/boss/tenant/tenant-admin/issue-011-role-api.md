# 后端：修改角色 + 权限查询 API

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/tenant/prd-new-boss-tenant-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/tenant/ux-boss-tenant-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md`

## Description
实现修改角色 `PUT /api/v1/svc/tenants/{tenantId}/admins/{userId}/role`（US-005）与权限查询 `GET /tenants/{tenantId}/admins/{userId}/role`（US-009，同路径不同方法）。

修改角色入参为 `role_id`（uuid），后端校验该 role 的 `tenant_id` 必须与参数 `tenantId` 一致或为 null，且 role 名称不以 `platform-` 为前缀（平台角色不可分配）。获取当前管理员角色时，查询到的 role 的 `tenant_id` 需与参数 `tenantId` 一致或为 null；若 role 以 `platform-` 为前缀则为平台角色，不可经本端点查询。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/postgres/`、`repo/services/tenant-service/internal/repo/adapters/core/`

## Acceptance Criteria
- [ ] `PUT .../role` 入参 `role_id`（uuid），需 `idempotency_key`；写审计 `tenant_admin.change_role`（details 含 target_id / old_role / old_role_id / new_role / new_role_id）
- [ ] `PUT .../role` 校验 `role_id` 对应的 role：`tenant_id` 必须与参数 `tenantId` 一致或为 null，且 `name` 不以 `platform-` 为前缀；否则 → 422 `ROLE_CHANGE_INVALID`
- [ ] `PUT .../role` 响应 200 `{ id, message }`
- [ ] `GET .../role` 通过 Core SDK `GetRolePermissions` 查询，返回 `{ user_id, tenant_id, role_id, role, permissions[] }`（permissions 为 resource/action/scope JSONB 数组）；roles.permissions JSONB 直接返回
- [ ] `GET .../role` 校验查询到的 role：`tenant_id` 需与参数 `tenantId` 一致或为 null；若 role 以 `platform-` 为前缀则为平台角色，不可经本端点查询
- [ ] `GET .../role` **仅能查询租户成员权限**（role ∈ tenant-admin/user/auditor，tenant_id 非空）；平台账户（tenant_id=null）不可经本端点查询；通过 Core SDK 调用 Core API 获取
- [ ] 单元测试覆盖：合法 role_id 成功、非法 role_id 422、平台角色不可分配、租户成员权限、平台账号不可查（SPEC §9.1, §9.4 US-005/US-009）

## Dependencies
#2, #3

## Type
backend

## Priority
high

## References
- SPEC: §5.1.5 ChangeRole / §5.1.8 GetRolePermissions / §4.2 role schema / §6.1 / §9
- Plan: 租户管理plan v3.0 §6.3.17d ChangeRole / §5.4.6-5.4.7
