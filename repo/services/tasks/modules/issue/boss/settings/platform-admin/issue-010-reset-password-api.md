# 后端：重置密码 API

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/settings/prd-new-boss-platform-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/settings/ux-new-boss-platform-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/settings/spec-new-boss-platform-admin.md`

## Description
实现重置平台账号密码的 Services 层业务逻辑与网关转发：`POST /api/v1/svc/platform-admins/{userId}/reset-password`（对应 SPEC §4.2 / US-005）。platform-settings-service 通过 Core SDK 调 Core（Core 层负责 bcrypt 校验与旧不同 + GenerateFromPassword + UPDATE），Services 叠加审计写入。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/platform-settings-service/internal/service/`、`repo/services/platform-settings-service/internal/repo/adapters/core/`、`repo/services/platform-settings-service/internal/repo/adapters/postgres/`、`repo/services/ani-gateway/internal/router/`

## Acceptance Criteria
- [ ] `PlatformAdminService.ResetPassword` RPC 实现替换 501 占位：校验 new_password 复杂度（前端预校验，Core 强校验）
- [ ] 调用 `CorePlatformUserClient.ResetPassword(userId, new_password)` → Core SDK `POST /admin/platform-users/{userId}/reset-password`
- [ ] Core 返回 422 PASSWORD_SAME_AS_OLD → Services 透传
- [ ] Core 返回 400 VALIDATION_FAILED → Services 透传
- [ ] 成功后写入审计 `platform_admin.reset_password`（details 不含密码明文）
- [ ] `platform_user_client.go` 实现 `CorePlatformUserClient.ResetPassword`（替换 #2 骨架）
- [ ] `platform_admin_resources.go` 实现 `POST /svc/platform-admins/{userId}/reset-password` 网关转发（gRPC → platform-settings-service）
- [ ] 入参含 `idempotency_key`（body）+ `new_password`（8-64 字符，四类至少三类，与旧密码不同）
- [ ] 明文密码不落日志/审计/响应
- [ ] AuthZ: 仅 platform-admin
- [ ] 单元测试：Core SDK PASSWORD_SAME_AS_OLD 透传、审计写入（SPEC §9.2 TestPlatformAdminService_ResetPassword）
- [ ] 集成测试 `TestHandler_ResetPasswordFlow`：POST reset-password → 验证旧密码登录失败、新密码登录成功（SPEC §9.3）
- [ ] `make test` 通过

## Dependencies
#2 (接口+SDK), #4 (Core handler)

## Type
backend

## Priority
high

## References
- SPEC: §5.2.5 ResetPassword / §5.1.5 Core ResetPassword / §6.1 PASSWORD_SAME_AS_OLD / §9.2-9.3
- UX: §3.2 重置密码 / §4.4 重置密码 Dialog
