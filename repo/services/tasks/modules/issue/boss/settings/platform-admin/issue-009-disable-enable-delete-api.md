# 后端：禁用 + 启用 + 删除 API

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/settings/prd-new-boss-platform-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/settings/ux-new-boss-platform-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/settings/spec-new-boss-platform-admin.md`

## Description
实现禁用（US-006）、启用与软删除平台账号的 Services 层业务逻辑与网关转发：`POST /api/v1/svc/platform-admins/{userId}/disable`、`POST .../enable`、`DELETE /api/v1/svc/platform-admins/{userId}`。platform-settings-service 通过 Core SDK 调 Core（最后管理员保护在 Core 层事务内原子校验），Services 叠加审计写入。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/platform-settings-service/internal/service/`、`repo/services/platform-settings-service/internal/repo/adapters/core/`、`repo/services/platform-settings-service/internal/repo/adapters/postgres/`、`repo/services/ani-gateway/internal/router/`

## Acceptance Criteria
- [ ] `PlatformAdminService.Disable` RPC 实现替换 501 占位：调用 `CorePlatformUserClient.SetStatus(userId, 'disabled')` → Core SDK `POST /admin/platform-users/{userId}/disable`
- [ ] Core 返回 422 LAST_PLATFORM_ADMIN → Services 透传
- [ ] 成功后写入审计 `platform_admin.disable`
- [ ] `PlatformAdminService.Enable` RPC 实现替换 501 占位：调用 `CorePlatformUserClient.SetStatus(userId, 'active')` → Core SDK `POST /admin/platform-users/{userId}/enable`
- [ ] 成功后写入审计 `platform_admin.enable`（启用无最后管理员保护）
- [ ] `PlatformAdminService.Delete` RPC 实现替换 501 占位：调用 `CorePlatformUserClient.SoftDelete(userId)` → Core SDK `DELETE /admin/platform-users/{userId}`
- [ ] Core 返回 422 LAST_PLATFORM_ADMIN → Services 透传
- [ ] 成功后写入审计 `platform_admin.delete`（软删除：is_deleted=TRUE + deleted_at=now() + status='disabled'）
- [ ] `platform_user_client.go` 实现 `CorePlatformUserClient.SetStatus` 和 `SoftDelete`（替换 #2 骨架）
- [ ] `platform_admin_resources.go` 实现 3 个端点网关转发（gRPC → platform-settings-service）
- [ ] 各端点入参含 `idempotency_key`（body）
- [ ] AuthZ: 仅 platform-admin
- [ ] 单元测试：Disable LAST_PLATFORM_ADMIN 透传（SPEC §9.2 TestPlatformAdminService_Disable_LastAdmin）
- [ ] 集成测试 `TestHandler_DisableEnableFlow`：POST disable → status=disabled → POST enable → status=active（SPEC §9.3）
- [ ] 集成测试 `TestHandler_DeleteFlow`：DELETE → GET detail 404（SPEC §9.3）
- [ ] 集成测试 `TestHandler_LastAdminProtection`：唯一 admin delete/disable → 422（SPEC §9.3）
- [ ] `make test` 通过

## Dependencies
#2 (接口+SDK), #4 (Core handler)

## Type
backend

## Priority
high

## References
- SPEC: §5.2.6 Disable/Enable/Delete / §5.1.6-5.1.7 Core 最后管理员保护 / §6.1 LAST_PLATFORM_ADMIN / §9.2-9.3
- UX: §3.2 禁用启用删除
