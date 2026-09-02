# Services OpenAPI 契约 v1.yaml

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/tenant/prd-new-boss-tenant-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/tenant/ux-boss-tenant-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md`

## Description
在 `repo/api/openapi/services/v1.yaml` 中补齐租户管理员管理全部端点契约（Services 层，前缀 `/api/v1/svc`）。本 Issue **只产出 v1.yaml 契约本身，不含任何衍生文件（schema.d.ts / pb 等由用户手动实现）、.go 实现 / SQL 迁移 / 前端页面**（对齐 plan PR-1）。

## Scope
- Product line: boss
- Code paths allowed: `repo/api/openapi/services/v1.yaml`（仅契约文件）
- 禁止：衍生文件生成（schema.d.ts / pb 等由用户手动实现，不在本 Issue 范围）、任何 .go 实现、SQL 迁移、前端页面

## Acceptance Criteria
- [ ] `services/v1.yaml` 新增以下路径 + schema + 错误码（对齐 SPEC §4.1/§4.2/§6.1）：
  - `POST /tenants/{tenantId}/admins/invite`、`POST /tenants/{tenantId}/admins/{userId}/invitation/resend`
  - `GET /tenant-admins`（跨租户列表）
  - `GET /tenants/{tenantId}/admins/{userId}`（详情）
  - `PUT /tenants/{tenantId}/admins/{userId}/role` 与 `GET /tenants/{tenantId}/admins/{userId}/role`（同路径不同方法）
  - `POST /tenants/{tenantId}/admins/{userId}/reset-password`
  - `POST /tenants/{tenantId}/admins/{userId}/disable`、`.../enable`
  - `DELETE /tenants/{tenantId}/admins/{userId}`
  - `GET /tenants/{tenantId}/admins/{userId}/audit-logs`
  - `GET /tenant-admins/tenants`（可用租户列表，邀请管理员选择器数据源）
- [ ] operationId 命名对齐 SPEC §4.1（inviteTenantAdmin / resendTenantAdminInvitation / listAllTenantAdmins / getTenantAdminDetail / updateTenantAdminRole / getTenantAdminRole / resetTenantAdminPassword / disableTenantAdmin / enableTenantAdmin / deleteTenantAdmin / listTenantAdminAuditLogs / listAvailableTenantsForAdmin）
- [ ] 各写操作（POST/PUT，含 resend/reset/disable/enable）请求体含 `idempotency_key`；`DELETE` 不携带幂等键
- [ ] schema 定义 `AdminWithTenant`（id/email/username/display_name/role/status/is_inviting/is_expired/source/last_login_at/created_at?/updated_at?/tenant{id,name,display_name}）、`InvitationResult`（id/token/expire_at/message）、`UserPermissions`（user_id/tenant_id/role_id/role/permissions[]，permissions 为 resource/action/scope JSONB 数组）、`CursorPage`（items+next_cursor）、`AvailableTenantListResponse`（items: [{id, name, display_name, status}]，status ∈ active/frozen，不含 disabled）
- [ ] 跨租户列表仅返回 role ∈ (tenant-admin) 或正在被邀请或邀请已过期的用户；`is_inviting` / `is_expired` 仅作标记
- [ ] 错误码覆盖 SPEC §6.1 全部（TENANT_ADMIN_NOT_FOUND / TENANT_ADMIN_ALREADY_ADMIN / TENANT_INVITATION_PENDING / TENANT_ADMIN_INVITATION_NOT_FOUND / TENANT_INVITATION_SETTLED / ROLE_CHANGE_INVALID / PASSWORD_SAME_AS_OLD / USER_STATE_INVALID / VALIDATION_FAILED / IDEMPOTENCY_CONFLICT / TENANT_NOT_FOUND）
- [ ] `make openapi-lint` 通过
- [ ] 衍生文件（`repo/frontends/boss/src/api/schema.d.ts`、pb 等）**由用户手动实现，不在本 Issue 验收范围**
- [ ] PR 描述含路径列表、schema 摘要、错误码表

## Dependencies
None

## Type
backend / docs

## Priority
high

## References
- SPEC: §2.1 Frozen Facts / §4.1 Endpoints / §4.2 Schemas / §4.3 Errors / §6.1 Error Taxonomy
- Plan: 租户管理plan v3.0 §12.1 (PR-1)
