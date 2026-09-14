# 后端：创建平台账号 API

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/settings/prd-new-boss-platform-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/settings/ux-new-boss-platform-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/settings/spec-new-boss-platform-admin.md`

## Description
实现创建平台运营账号的 Services 层业务逻辑与网关转发：`POST /api/v1/svc/platform-admins`（对应 SPEC §4.2 / US-001）。platform-settings-service 通过 Core SDK 调 Core `POST /admin/platform-users`（Core 层负责 bcrypt 哈希 + INSERT），Services 层叠加审计写入。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/platform-settings-service/internal/service/`、`repo/services/platform-settings-service/internal/repo/adapters/core/`、`repo/services/platform-settings-service/internal/repo/adapters/postgres/`、`repo/services/ani-gateway/internal/router/`

## Acceptance Criteria
- [ ] `PlatformAdminService.Create` RPC 实现替换 501 占位：校验入参（email RFC 5322 / username 1-64 不含 ':' / display_name 1-128 / role 白名单 / password 复杂度）
- [ ] 调用 `CorePlatformUserClient.Create(email, username, display_name, role, password)` → Core SDK `POST /admin/platform-users`
- [ ] Core 返回 409 EMAIL_ALREADY_EXISTS → Services 透传；409 USERNAME_ALREADY_EXISTS → 透传
- [ ] 成功后写入审计 `platform_admin.create`（details 含 target_id + role），审计 best-effort（失败不阻断）
- [ ] `platform_user_client.go` 实现 `CorePlatformUserClient.Create`（替换 #2 骨架），封装 anisdk.Client 调用
- [ ] `platform_admin_resources.go` 实现 `POST /svc/platform-admins` 网关转发（gRPC → platform-settings-service，`PLATFORM_SETTINGS_SERVICE_ADDR` 缺省 `127.0.0.1:9106`）
- [ ] 入参含 `idempotency_key`（body），网关层幂等去重
- [ ] 响应 200 `{ id, message: "platform admin created" }`
- [ ] 明文密码不落日志/审计/响应
- [ ] 单元测试：调 Core SDK 成功/失败、审计写入（SPEC §9.2 TestPlatformAdminService_Create）
- [ ] 集成测试 `TestHandler_CreateFlow`：POST create → GET list 验证 → GET detail 验证全字段（SPEC §9.3）
- [ ] `make test` 通过

## Dependencies
#2 (接口+SDK), #4 (Core handler)

## Type
backend

## Priority
high

## References
- SPEC: §5.2.1 Create / §4.2 create schema / §6.1 errors / §9.2-9.3
- UX: §3.1 创建流程 / §4.2 向导
