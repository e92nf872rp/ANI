# 后端：重发邀请 API

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/tenant/prd-new-boss-tenant-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/tenant/ux-boss-tenant-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md`

## Description
实现重发邀请后端逻辑：`POST /api/v1/svc/tenants/{tenantId}/admins/{userId}/invitation/resend`（对应 SPEC §4.1 / US-002）。重新生成 token、刷新过期时间、状态回归 inviting；终态不可重发。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/postgres/`

## Acceptance Criteria
- [ ] `POST .../admins/{userId}/invitation/resend`，需 `idempotency_key`（body）
- [ ] 仅对最新一条状态为 `inviting` 或 `expired` 的邀请允许重发；重发时重新生成 token（更新 token_hash）、刷新 `expire_at=now()+72h`、清空 accepted_at/rejected_at、状态回归 `inviting`
- [ ] tenantId+userId 组合无匹配记录 → 404 `TENANT_ADMIN_INVITATION_NOT_FOUND`
- [ ] 最新邀请已 accepted/rejected（终态）→ 409 `TENANT_INVITATION_SETTLED`（不可重发）
- [ ] 响应 `{ id, token, expire_at, message }`；token 仅本次返回一次
- [ ] 写审计 `tenant_admin.resend_invitation`
- [ ] 单元测试覆盖：inviting/expired 重发成功、终态 409、无记录 404（SPEC §9.1, §9.4 US-002）
- [ ] 集成测试 `TestHandler_ResendFlow`：验证新 token + expire_at + settling（SPEC §9.2）

## Dependencies
#2, #3

## Type
backend

## Priority
high

## References
- SPEC: §5.1.2 Resend / §4.2 resend schema / §6.1 errors / §9
- Plan: 租户管理plan v3.0 §6.3.17c
