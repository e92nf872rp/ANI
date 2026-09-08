# 后端：邀请租户管理员 API

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/tenant/prd-new-boss-tenant-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/tenant/ux-boss-tenant-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md`

## Description
实现邀请管理员后端逻辑：`POST /api/v1/svc/tenants/{tenantId}/admins/invite`（对应 SPEC §4.1 / US-001）。在租户内按 email+username 匹配现有用户，生成一次性 token 并写入 `tenant_admin_invitation`，不改 users.status、不绑角色、写审计、触发通知占位。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/postgres/`、`repo/services/tenant-service/internal/repo/adapters/core/`

## Acceptance Criteria
- [ ] `POST .../admins/invite` 入参 email+username（共同判定），需 `idempotency_key`（body）
- [ ] 在指定租户（tenant_id=tenantId）内匹配现有用户，无匹配 → 404 `TENANT_ADMIN_NOT_FOUND`（不新建用户）
- [ ] 已是本租户 tenant-admin → 409 `TENANT_ADMIN_ALREADY_ADMIN`
- [ ] 该用户在本租户下已存在 status='inviting' 邀请 → 409 `TENANT_INVITATION_PENDING`（引导重发）
- [ ] 成功则 `INSERT tenant_admin_invitation`（status='inviting'、token_hash=SHA-256(token)、expire_at=now()+72h），**不改变角色、不改 users.status**
- [ ] token = crypto_random(32)，响应 `{ id, token, expire_at, message }`；token 仅本次返回一次，库中仅存 token_hash
- [ ] 写审计 `tenant_admin.invite`（details 含 target_id + token_hash）
- [ ] 单元测试覆盖：匹配成功 / 无匹配 404 / already admin 409 / pending 409 / token+expire_at / 审计（SPEC §9.1, §9.4 US-001）
- [ ] 集成测试 `TestHandler_InviteFlow`：POST invite → GET 列表验证 is_inviting=true + token 一次性（SPEC §9.2）

## Dependencies
#2, #3

## Type
backend

## Priority
high

## References
- SPEC: §5.1.1 Invite / §4.2 invite schema / §6.1 errors / §9
- Plan: 租户管理plan v3.0 §6.3.17b
