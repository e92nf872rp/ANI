# 后端：禁用 / 启用 / 删除 API

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/tenant/prd-new-boss-tenant-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/tenant/ux-boss-tenant-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md`

## Description
实现管理员账号状态域：`POST .../disable`、`POST .../enable`、`DELETE .../admins/{userId}`（对应 SPEC §4.1 / US-008）。含软删除。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/postgres/`、`repo/services/tenant-service/internal/repo/adapters/core/`

## Acceptance Criteria
- [ ] `POST .../disable` → users.status='disabled'；需 `idempotency_key`；写审计 `tenant_admin.disable`；不可重复禁用（Core DB 层校验状态未变化 → 409 `USER_STATE_INVALID`）
- [ ] `POST .../enable` → users.status='active'；需 `idempotency_key`；写审计 `tenant_admin.enable`；不可重复启用（Core DB 层校验状态未变化 → 409 `USER_STATE_INVALID`）
- [ ] `DELETE .../admins/{userId}` 软删除（is_deleted=TRUE + deleted_at，不改 status）；**不幂等，不需 Idempotency-Key**；写审计 `tenant_admin.delete`
- [ ] 禁用/启用/删除均响应 `{ id, message }`
- [ ] 单元测试：禁用/启用/删除、软删除（SPEC §9.1 TestTenantAdminService_DisableEnableDelete）

## Dependencies
#2, #3

## Type
backend

## Priority
high

## References
- SPEC: §5.1.6 Disable/Enable/Delete / §4.2 disable\|enable schema / §4.2 DELETE / §6.1 / §9
- Plan: 租户管理plan v3.0 §5.4.9-5.4.11 / §6.3.17d
