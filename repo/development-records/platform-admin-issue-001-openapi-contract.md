# PLATFORM-ADMIN-ISSUE-01：OpenAPI 契约 v1.yaml（Core + Services 平台运营账号）

> **批次类型：** Feature batch（BOSS 平台运营账号功能流 Issue #1）
> **完成日期：** 2026-08-20（commit `7b5a68d`，后续 `cacee93` 修正、`dfb3f4b` 精简）
> **Scope：** `repo/api/openapi/v1.yaml`、`repo/api/openapi/services/v1.yaml`
> **依赖：** 无（功能流第一个 Issue）
> **Product line：** boss
> **本地 issue：** `repo/services/tasks/modules/issue/boss/settings/platform-admin/issue-001-openapi-contract.md`

## 交付内容

在 Core `v1.yaml` 与 Services `services/v1.yaml` 中补齐平台运营账号管理全部端点契约。Core 层前缀 `/api/v1/admin/platform-users/*`（8 端点），Services 层前缀 `/api/v1/svc/platform-admins/*`（10 端点，含 roles 与 audit-logs）。

### Core 端点（8 个，tags: [PlatformUsers]）

| Method | Path | operationId | 幂等键 |
|---|---|---|---|
| POST | `/admin/platform-users` | `createPlatformUser` | body |
| GET | `/admin/platform-users` | `listPlatformUsers` | — |
| GET | `/admin/platform-users/{userId}` | `getPlatformUser` | — |
| PUT | `/admin/platform-users/{userId}/role` | `updatePlatformUserRole` | body |
| POST | `/admin/platform-users/{userId}/reset-password` | `resetPlatformUserPassword` | body |
| POST | `/admin/platform-users/{userId}/disable` | `disablePlatformUser` | body |
| POST | `/admin/platform-users/{userId}/enable` | `enablePlatformUser` | body |
| DELETE | `/admin/platform-users/{userId}` | `deletePlatformUser` | body |

- 写操作统一复用 `UserMutationResult`（`{id, message}`）；RBAC scope 标注 `x-ani-rbac-scope: scope:users:read|write`。
- 列表 query：`limit/cursor/role/status/source/search`；`source=local|oidc` 入参，响应映射 `third_party`（username 前缀 `oidc:`/`local:` 推断）。
- 复用既有租户用户错误响应族，新增三个专用 response：`PlatformUserNotFound`(404)、`LastPlatformAdmin`(422)、`PasswordSameAsOld`(422)；`EMAIL_ALREADY_EXISTS`/`USERNAME_ALREADY_EXISTS`/`ROLE_NOT_FOUND`/`IDEMPOTENCY_CONFLICT` 走通用 `Conflict`/`NotFound` response 的 description 枚举。

### Core 新增 Schema（6 个）

`PlatformUser`（9 字段，含 display_name/last_login_at nullable）、`PlatformUserCreateRequest`（password 8-64 四类至少三类）、`PlatformUserListResponse`、`PlatformUserRoleUpdateRequest`、`PlatformUserResetPasswordRequest`、`PlatformUserIdempotentRequest`。

### Services 端点（10 个，tags: [PlatformAdmins]）

| Method | Path | operationId | 权限（summary 注明） |
|---|---|---|---|
| POST | `/platform-admins` | `createPlatformAdmin` | platform-admin |
| GET | `/platform-admins` | `listPlatformAdmins` | 三角色皆可 |
| GET | `/platform-admins/roles` | `listPlatformAdminRoles` | 三角色皆可 |
| GET | `/platform-admins/{userId}` | `getPlatformAdmin` | 三角色皆可 |
| PUT | `/platform-admins/{userId}/role` | `updatePlatformAdminRole` | platform-admin |
| POST | `/platform-admins/{userId}/reset-password` | `resetPlatformAdminPassword` | platform-admin |
| POST | `/platform-admins/{userId}/disable` | `disablePlatformAdmin` | platform-admin |
| POST | `/platform-admins/{userId}/enable` | `enablePlatformAdmin` | platform-admin |
| DELETE | `/platform-admins/{userId}` | `deletePlatformAdmin` | platform-admin |
| GET | `/platform-admins/{userId}/audit-logs` | `listPlatformAdminAuditLogs` | 三角色皆可 |

- 写操作复用 `IdempotentResult` / `IdempotentOnlyRequest` / `ResetPasswordRequest`（租户管理员批次已有），未重复造 schema。
- `PlatformRole.permissions` 四维矩阵：`tenant_ops/resource_pool/platform_user/audit_export` × `read/write/none`，与 SPEC §3.2.3 静态映射一致。

### Services 新增 Schema（9 个）

`PlatformAdminListItem`（无 email）、`PlatformAdminDetail`（含 email/created_at）、`PlatformAdminListResponse`、`PlatformAdminCreateRequest`、`PlatformAdminRoleUpdateRequest`、`PlatformRole`、`PlatformRoleListResponse`、`PlatformAdminAuditLog`、`PlatformAdminAuditLogListResponse`。

## Design Decisions

1. **错误码承载方式**：Core 侧新业务码不逐个建 response 组件，`EMAIL_ALREADY_EXISTS`/`USERNAME_ALREADY_EXISTS` 复用 `Conflict`、`ROLE_NOT_FOUND` 复用 `NotFound`，仅以 response description 枚举表达；只有语义独特的 `PlatformUserNotFound`/`LastPlatformAdmin`/`PasswordSameAsOld` 建独立组件。理由：避免 response 组件爆炸，SDK 消费方按 `ErrorResponse.code` 判断而非组件名。
2. **Services 写请求体复用**：`IdempotentOnlyRequest`/`ResetPasswordRequest` 从租户管理员模块复用，不建 `PlatformAdminIdempotentRequest` 平行副本。理由：语义完全一致（幂等键 UUID + 网关 header 回落），重复定义徒增 SDK 面。
3. **权限表达放 summary**：Services 端点未用 `x-ani-rbac-scope` 扩展字段（Core 有），而是在 summary 文字注明「需 platform-admin / platform-ops / platform-readonly」。理由：Services 层 AuthZ 由 ani-gateway RBAC 中间件按角色名校验，不经 scope 元数据驱动；SPEC §4.2 也是按角色而非 scope 列权限。
4. **`source` 双值映射**：入参 `local|oidc`、出参 `local|third_party`。理由：query 过滤沿 Core 表 username 前缀物理事实（`oidc:`），响应对外统一 UX 域词汇（third_party），与租户管理员模块先例一致。

## Deviations

| Spec / Issue | 实现 | 原因 |
|---|---|---|
| Issue AC「`make openapi-lint` 通过」 | `npx yaml-lint api/openapi/v1.yaml api/openapi/services/v1.yaml`（PASS） | Makefile 无 `openapi-lint` 目标（`make lint` 仅 go/python/ts）；与 quota-policy-issue-01 同一替代方案 |
| SPEC §4.0 schemas 行「`CursorPage`（复用）」 | 未定义通用 `CursorPage`，各列表直接建 `PlatformUserListResponse`/`PlatformAdminListResponse`（items + next_cursor nullable） | Core v1.yaml 无既有 `CursorPage` 组件可复用（分页约定在 info.description 文字层面）；逐资源 response 更利于 SDK 强类型 |
| SPEC §4.3 响应示例 `{ "id": "uuid", "message": "..." }`（内联形状） | Core 用既有 `UserMutationResult`、Services 用既有 `IdempotentResult` 组件承载 | 形状一致；复用组件保持 OpenAPI 单一来源 |
| SPEC §6.1 `ROLE_NOT_FOUND` 404 表述为独立码 | Core create 的 404 response 引用通用 `NotFound`，description 列 `ROLE_NOT_FOUND`；update role 404 列 `PLATFORM_USER_NOT_FOUND / ROLE_NOT_FOUND` | 同 Design Decision 1 |
| Issue AC「衍生文件不在本 Issue 验收范围」 | 同一 commit `7b5a68d` 一并交付了 SDK 重生成（core+services × go/python/ts/java）、`docs/api/*.html`、Console `schema.d.ts` | 实际按「契约+生成物」单 commit 交付；issue-002（SDK 生成）范围被前置合入，边界在文档层面而非 git 层面切分 |
| 初始版本各端点带 `security: [{BearerAuth}, {ApiKeyAuth}]` | commit `dfb3f4b`（08-25）移除 PlatformAdmins 全部分组的冗余 security 声明 | 全局 default 已覆盖；逐端点重复声明徒增维护成本，精简规范文档 |

## Tradeoffs

- **通用 vs 专用错误 response**：选混合（3 专用 + 通用承载），代价是 `EMAIL_ALREADY_EXISTS` 无法在 SDK 层面静态区分（只能靠运行时 code 判断），换取组件数可控。
- **同 commit 合并 SDK 生成 vs 严格按 issue 切分**：选合并。代价是 issue-001/002 无法按 commit 独立回滚；收益是契约与生成物永远同步，不会出现「契约已合、SDK 未刷」的中间态。
- **Services 端点不加 `x-ani-rbac-scope`**：代价是 OpenAPI 机器不可读权限；收益是不引入与网关实际校验逻辑（角色名）不一致的第二套元数据。

## Open Questions

1. **`npx yaml-lint` 是弱校验**（仅 YAML 语法，不做 OpenAPI schema 校验/引用完整性）。是否值得引入 `@redocly/cli lint` 或 Spectral 作为 `make openapi-lint` 正式目标？quota 与 platform-admin 两个功能流都绕过了该 AC。
2. **`ROLE_NOT_FOUND` 在 Core create 与 Services create 两层都出现**（404 description），后续 handler 实现时 Services 层是否透传 Core 码即可，无需二次校验角色枚举？建议在 issue-005+（platform-settings-service handler）实现时确认。
3. **`dfb3f4b` 只删了 PlatformAdmins 分组的 security 声明**；文件内其他分组（如 line 1442+ 的 models 等）仍带逐端点 security。若「精简」是普适原则，其他分组是否要跟进清理？
4. Issue-003（roles 种子迁移）尚未落地时，`role=platform-ops/platform-readonly` 的枚举已写入契约——创建接口在 seed 缺失时会 404 `ROLE_NOT_FOUND`。实现顺序上 issue-003 需先于端到端联调。

## Verification

```bash
cd repo
npx yaml-lint api/openapi/v1.yaml api/openapi/services/v1.yaml   # PASS
# 端点核对：grep 'admin/platform-users' v1.yaml → 8 operationId 全就位
#            grep 'platform-admins' services/v1.yaml → 10 operationId 全就位
# SDK 元数据：sdks/core/sdk-metadata.json 含 8 个 PlatformUser* operationId + 6 schema
#            sdks/services/sdk-metadata.json 含 10 个 PlatformAdmin* operationId
# git: 7b5a68d（契约+SDK+文档，19 files）→ cacee93（AdminWithTenant 去 mfa_required，无关本模块）→ dfb3f4b（去冗余 security，-10 行）
```

## 边界声明

- 本批次仅 OpenAPI 契约（+同 commit 的 SDK/文档生成物），不含 handler / store / gRPC / 迁移 / 前端页面实现。
- Core handler 与迁移属 issue-003/004；platform-settings-service 骨架属 issue-002；网关接入属 issue-005 起。
- 不声明 runtime ready 或 production ready。
