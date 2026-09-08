# 后端：重置密码 API

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/tenant/prd-new-boss-tenant-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/tenant/ux-boss-tenant-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md`

## Description
实现重置管理员密码：`POST /api/v1/svc/tenants/{tenantId}/admins/{userId}/reset-password`（对应 SPEC §4.1 / US-007）。调用方提供 new_password（明文 HTTPS），bcrypt(cost=12) 存储。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/postgres/`、`repo/services/tenant-service/internal/repo/adapters/core/`

## Acceptance Criteria
- [ ] `POST .../reset-password`，入参 new_password（明文 HTTPS），需 `idempotency_key`
- [ ] new_password 约束：8-64 字符，须包含大小写字母、数字、特殊字符四类中至少三类，且与旧密码不同
- [ ] bcrypt(cost=12)；明文不写入日志/审计/响应
- [ ] 与旧密码相同 → 422 `PASSWORD_SAME_AS_OLD`
- [ ] 复杂度不满足 → 400 `VALIDATION_FAILED`
- [ ] 软删除用户（is_deleted=true）不可重置 → 404 `TENANT_ADMIN_NOT_FOUND`；禁用态用户（status='disabled'）可重置
- [ ] 写审计 `tenant_admin.reset_password`
- [ ] 响应 200 `{ id, message }`
- [ ] 单元测试：成功、同旧密码 422、复杂度 400、软删除 404、禁用态可重置（SPEC §9.1, §9.4 US-007）

## Dependencies
#2, #3

## Type
backend

## Priority
high

## References
- SPEC: §5.1.7 ResetPassword / §4.2 reset-password schema / §6.1 / §7.2 / §9
- Plan: 租户管理plan v3.0 §5.4.8 / §6.3.17d
