# 后端：列表 + 详情 API

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/settings/prd-new-boss-platform-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/settings/ux-new-boss-platform-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/settings/spec-new-boss-platform-admin.md`

## Description
实现平台运营账号列表查询与详情查询的 Services 层业务逻辑与网关转发：`GET /api/v1/svc/platform-admins`（US-002）与 `GET /api/v1/svc/platform-admins/{userId}`（US-003）。platform-settings-service 通过 Core SDK 调 Core 对应端点，只读不写审计。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/platform-settings-service/internal/service/`、`repo/services/platform-settings-service/internal/repo/adapters/core/`、`repo/services/ani-gateway/internal/router/`

## Acceptance Criteria
- [ ] `PlatformAdminService.List` RPC 实现替换 501 占位：调用 `CorePlatformUserClient.List(filter)` → Core SDK `GET /admin/platform-users`
- [ ] List 支持 query 参数：limit（default 20, max 100）/ cursor / role / status / source / search
- [ ] List 响应 CursorPage：items[]（每项含 id/username/display_name/role/status/source/last_login_at，**不含 email**）+ next_cursor
- [ ] source 推断：`oidc:` → third_party，`local:` → local
- [ ] `PlatformAdminService.GetDetail` RPC 实现替换 501 占位：调用 `CorePlatformUserClient.Get(userId)` → Core SDK `GET /admin/platform-users/{userId}`
- [ ] Detail 响应含全字段：id/email/username/display_name/role/status/source/last_login_at/created_at（不含 password_hash）
- [ ] `platform_user_client.go` 实现 `CorePlatformUserClient.List` 和 `Get`（替换 #2 骨架）
- [ ] `platform_admin_resources.go` 实现 `GET /svc/platform-admins` 和 `GET /svc/platform-admins/{userId}` 网关转发（gRPC → platform-settings-service）
- [ ] 只读操作，不写审计日志
- [ ] 列表无数据返回 items=[], next_cursor=""；详情不存在 → 404 PLATFORM_USER_NOT_FOUND 透传
- [ ] AuthZ: platform-admin/ops/readonly 均可访问（网关 RBAC）
- [ ] 单元测试：List 分页/过滤（SPEC §9.2）、Detail 全字段（SPEC §9.3 TestHandler_CreateFlow 详情断言）
- [ ] `make test` 通过

## Dependencies
#2 (接口+SDK), #4 (Core handler)

## Type
backend

## Priority
high

## References
- SPEC: §5.2.2 List/Get / §4.2 list+detail schema / §9.2-9.3
- UX: §3.1 查看 / §4.1 列表 / §4.3 详情
