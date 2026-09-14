# OpenAPI 契约 v1.yaml（Core + Services）

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/settings/prd-new-boss-platform-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/settings/ux-new-boss-platform-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/settings/spec-new-boss-platform-admin.md`

## Description
在 Core `repo/api/openapi/v1.yaml` 和 Services `repo/api/openapi/services/v1.yaml` 中补齐平台运营账号管理全部端点契约。Core 层前缀 `/api/v1/admin/platform-users/*`，Services 层前缀 `/api/v1/svc/platform-admins/*`。本 Issue **只产出 v1.yaml 契约本身，不含 SDK 生成、衍生文件（schema.d.ts / pb 等）、.go 实现 / SQL 迁移 / 前端页面**。

## Scope
- Product line: core + services
- Code paths allowed: `repo/api/openapi/v1.yaml`、`repo/api/openapi/services/v1.yaml`（仅契约文件）
- 禁止：SDK 生成、衍生文件生成、任何 .go 实现、SQL 迁移、前端页面

## Acceptance Criteria
- [ ] Core `v1.yaml` 新增以下路径 + schema + 错误码（对齐 SPEC §4.1）：
  - `POST /admin/platform-users`（createPlatformUser）
  - `GET /admin/platform-users`（listPlatformUsers）
  - `GET /admin/platform-users/{userId}`（getPlatformUser）
  - `PUT /admin/platform-users/{userId}/role`（updatePlatformUserRole）
  - `POST /admin/platform-users/{userId}/reset-password`（resetPlatformUserPassword）
  - `POST /admin/platform-users/{userId}/disable`（disablePlatformUser）
  - `POST /admin/platform-users/{userId}/enable`（enablePlatformUser）
  - `DELETE /admin/platform-users/{userId}`（deletePlatformUser）
- [ ] Core schema 定义 `PlatformUser`（id/email/username/display_name/role/status/source/last_login_at/created_at）、`PlatformUserCreateRequest`（email/username/display_name/role/password）、`PlatformUserListResponse`（CursorPage）、`CursorPage`（items+next_cursor）
- [ ] Core 错误码覆盖 SPEC §6.1（EMAIL_ALREADY_EXISTS / USERNAME_ALREADY_EXISTS / PLATFORM_USER_NOT_FOUND / ROLE_NOT_FOUND / LAST_PLATFORM_ADMIN / PASSWORD_SAME_AS_OLD / VALIDATION_FAILED）
- [ ] Services `v1.yaml` 新增以下路径 + schema + 错误码（对齐 SPEC §4.2）：
  - `POST /svc/platform-admins`（createPlatformAdmin）
  - `GET /svc/platform-admins`（listPlatformAdmins）
  - `GET /svc/platform-admins/roles`（listPlatformAdminRoles）
  - `GET /svc/platform-admins/{userId}`（getPlatformAdmin）
  - `PUT /svc/platform-admins/{userId}/role`（updatePlatformAdminRole）
  - `POST /svc/platform-admins/{userId}/reset-password`（resetPlatformAdminPassword）
  - `POST /svc/platform-admins/{userId}/disable`（disablePlatformAdmin）
  - `POST /svc/platform-admins/{userId}/enable`（enablePlatformAdmin）
  - `DELETE /svc/platform-admins/{userId}`（deletePlatformAdmin）
  - `GET /svc/platform-admins/{userId}/audit-logs`（listPlatformAdminAuditLogs）
- [ ] Services schema 定义 `PlatformAdminListItem`（id/username/display_name/role/status/source/last_login_at，不含 email）、`PlatformAdminDetail`（含 email/created_at）、`PlatformRole`（name/label/description/permissions 为 roles.permissions JSONB 原样数组）、`PlatformAdminCreateRequest`
- [ ] 各写操作（POST/PUT/DELETE）请求体含 `idempotency_key`
- [ ] Services 错误码覆盖 SPEC §6.1 全部（LAST_PLATFORM_ADMIN / EMAIL_ALREADY_EXISTS / USERNAME_ALREADY_EXISTS / PLATFORM_USER_NOT_FOUND / PASSWORD_SAME_AS_OLD / ROLE_CHANGE_INVALID / VALIDATION_FAILED / FORBIDDEN / IDEMPOTENCY_CONFLICT）
- [ ] `make openapi-lint` 通过
- [ ] 衍生文件（schema.d.ts / pb / SDK 等）**不在本 Issue 验收范围**
- [ ] PR 描述含 Core + Services 路径列表、schema 摘要、错误码表

## Dependencies
None

## Type
backend / docs

## Priority
high

## References
- SPEC: §2.2 Frozen Facts / §4.0 OpenAPI Change Plan / §4.1 Core Endpoints / §4.2 Services Endpoints / §4.3 Schemas / §6.1 Error Taxonomy
