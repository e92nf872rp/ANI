# 后端：管理员详情 API

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/tenant/prd-new-boss-tenant-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/tenant/ux-boss-tenant-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md`

## Description
实现管理员详情查询：`GET /api/v1/svc/tenants/{tenantId}/admins/{userId}`（对应 SPEC §4.1 / US-004）。返回该用户所有用户信息（不含 password_hash）+ is_inviting / is_expired 标记 + 租户对象。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/postgres/`、`repo/services/tenant-service/internal/repo/adapters/core/`

## Acceptance Criteria
- [ ] `GET /tenants/{tenantId}/admins/{userId}` 返回 id/username/email/display_name/role/status/source/last_login_at/created_at/updated_at/is_inviting/is_expired + tenant 对象（id/name/display_name）
- [ ] **不含 password_hash**、无顶层 tenant_id 冗余
- [ ] is_inviting：该租户下存在 `tenant_admin_invitation.status='inviting'` 为 true，否则 false；is_expired：该租户下存在 `tenant_admin_invitation.status='expired'` 为 true，否则 false；仅作标记
- [ ] source 推断：username `oidc:` → third_party，`local:` → local（对齐 SPEC §5.1.9）
- [ ] role 由 user_roles+roles 解析（tenant-admin / user / auditor）
- [ ] 只读端点不写审计（对齐 SPEC §6.1 读端点 readonly 可见）
- [ ] 单元/集成测试 `TestHandler_Detail`：验证全字段（含 is_inviting / created_at）（SPEC §9.2, §9.4 US-004）

## Dependencies
#2, #3

## Type
backend

## Priority
high

## References
- SPEC: §5.1.9 GetAdminDetail / §4.2 detail schema / §9
- Plan: 租户管理plan v3.0 §5.4.4 / §6.3.17d
