# 后端：平台角色权限列表 + 用户权限查询 + 修改角色 API

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/settings/prd-new-boss-platform-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/settings/ux-new-boss-platform-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/settings/spec-new-boss-platform-admin.md`

## Description
实现平台运营账号权限相关的 Services 层业务逻辑（US-007 权限矩阵 / US-004 改角色 / UX §4.3 权限 Tab），对齐 **当前 Core 已落地契约**（#7 + Core `PUT .../role`）：

| 能力 | Services 对外 | Core SDK 调用 | 审计 |
|------|---------------|---------------|------|
| **1. 平台角色权限列表** | `GET /api/v1/svc/platform-admins/roles` | `CorePlatformUserClient.ListPlatformRoles` → `GET /admin/platform-users/roles` | 否 |
| **2. 用户权限查询** | `GET /api/v1/svc/platform-admins/{userId}/permissions` | `CorePlatformUserClient.GetPlatformUserPermissions` → `GET /admin/platform-users/{userId}/permissions` | 否 |
| **3. 用户权限修改（改角色）** | `PUT /api/v1/svc/platform-admins/{userId}/role` | `CorePlatformUserClient.ChangeRole` → `PUT /admin/platform-users/{userId}/role` | 是 `platform_admin.change_role` |

> **实现对齐（相对旧稿修正）：**
> 1. **不再写死三角色枚举**：目标 `role` 合法性由 Core `roles` 表校验（`tenant_id IS NULL AND name LIKE 'platform-%'`）；未知角色 → Core `ROLE_NOT_FOUND`（404）透传，**不在 Services 做** `role ∈ {platform-admin, platform-ops, platform-readonly}` → `ROLE_CHANGE_INVALID`。
> 2. **权限原样透出**：`PlatformRole.permissions` / `PlatformUserPermissions.permissions` 为 `roles.permissions` JSONB 数组（`resource` / `actions` / `scope`），**不做** SPEC 草稿四维静态映射（`tenant_ops` / `resource_pool` / `platform_user` / `audit_export`）。
> 3. **schema 对齐 Core**：`PlatformRole` 仅 `name` + `permissions`（Core 契约无 `label` / `description`）；Services OpenAPI / proto / 网关 JSON 同步收敛，去掉旧四维 `PlatformRolePermissions` 过渡分支。
> 4. **Core client 已就绪**：`ListPlatformRoles` / `GetPlatformUserPermissions` / `ChangeRole` 已由 #4/#7 落地，本 issue **只填 RPC 业务体 + 契约补齐单账号权限端点 + 网关转发/JSON 对齐**，不再重写 client 骨架。

## Scope
- Product line: boss（Services 业务；契约/proto/网关为支撑）
- Code paths allowed:
  - `repo/api/openapi/services/v1.yaml`（补 `GET /platform-admins/{userId}/permissions`；`PlatformRole` 对齐 Core）
  - `repo/api/proto/platform_settings/v1/platform_admin_service.proto`（补 `GetPlatformAdminPermissions` RPC；`PlatformRole.permissions` 为 `repeated Struct`）
  - `repo/pkg/generated/pb/platform_settings/v1/`（`make proto`）
  - `repo/services/platform-settings-service/internal/service/`
  - `repo/services/ani-gateway/internal/router/platform_admin_resources.go`（补 permissions 路由；`platformRoleJSON` 改为 permissions 数组）
- 不触碰：Core OpenAPI / `pkg/ports` / `pkg/adapters/runtime` / Core SDK（#7 已齐）；`users`/`roles`/`user_roles` 表 SQL

## Acceptance Criteria

### A. 契约补齐（Services OpenAPI + proto）
- [ ] Services `v1.yaml` 新增 `GET /platform-admins/{userId}/permissions`（operationId: `getPlatformAdminPermissions`）：path `userId`（UUID）；响应 schema `PlatformAdminPermissionsResponse`（`user_id` / `role` / `permissions` 数组，对齐 Core `PlatformUserPermissionsResponse`）；404 `PLATFORM_USER_NOT_FOUND`
- [ ] Services `PlatformRole`：`required: [name, permissions]`；去掉必填 `label` / `description`（可删字段或改 optional，与 Core 一致）
- [ ] `GET /platform-admins/roles` 响应继续用 `PlatformRoleListResponse`（items 为上述 `PlatformRole`）
- [ ] proto 新增 `rpc GetPlatformAdminPermissions(GetPlatformAdminPermissionsRequest) returns (PlatformAdminPermissions)`；消息含 `user_id` / `role` / `repeated Struct permissions`
- [ ] proto `PlatformRole.permissions` 为 `repeated google.protobuf.Struct`（替换旧四维 message，若生成物仍残留则本 issue 一并收敛）
- [ ] `make proto` + `make openapi-lint` 通过；route baseline / Services boundary 同步（若新增 path 触发）

### B. 平台角色权限列表（ListRoles）
- [ ] `PlatformAdminService.ListPlatformAdminRoles` 替换 `unimplemented()`：调用 `coreClient.ListPlatformRoles()`，映射为 proto `PlatformRole{name, permissions}`，**不写审计**
- [ ] 响应 items[] 每项仅含 `name` + `permissions`（JSONB 原样数组）；空表合法返回 `items=[]`
- [ ] 网关 `listPlatformAdminRoles` / `platformRoleJSON`：`permissions` 序列化为 **JSON 数组**（不再拼四维 map）；与 OpenAPI 一致
- [ ] AuthZ：platform-admin / ops / readonly 可读（沿用现有 `/svc/platform-admins/*` RBAC）

### C. 用户权限查询（GetPermissions）
- [ ] `PlatformAdminService.GetPlatformAdminPermissions` 实现：校验 `user_id` UUID → `coreClient.GetPlatformUserPermissions(userID)` → 映射 `user_id` / `role` / `permissions`；**不写审计**
- [ ] Core `PLATFORM_USER_NOT_FOUND`（账号不存在 / 已软删除 / 非平台账号 / 无平台角色绑定）→ 透传 404
- [ ] 网关注册 `GET /platform-admins/:userId/permissions` → 新 RPC；注册顺序在 `GET /:userId` 之后、与其他 `/:userId/*` 子路径并列（Hertz 参数段兼容现有先例）
- [ ] AuthZ：与 ListRoles 相同（只读三角色）

### D. 用户权限修改 / 改角色（ChangeRole）
- [ ] `PlatformAdminService.UpdatePlatformAdminRole` 替换 `unimplemented()`：
  1. 校验 `user_id` UUID、`role` 非空（Services 仅做必填/格式校验，**不白名单三角色**）
  2. （可选但推荐）改前 `coreClient.Get(userID)` 取 `old_role`，供审计；不存在 → 404
  3. `coreClient.ChangeRole(userID, role)` → Core `PUT /admin/platform-users/{userId}/role`
  4. 成功写入审计 `platform_admin.change_role`（details 含 `target_id` + `old_role` + `new_role`）；审计 best-effort（失败不阻断）
- [ ] 错误透传：`PLATFORM_USER_NOT_FOUND` / `ROLE_NOT_FOUND` → 404；`VALIDATION_FAILED` → 400；`IDEMPOTENCY_CONFLICT` → 409；Core 若返回 `ROLE_CHANGE_INVALID` 则 422 透传（当前 Core ChangeRole 主路径为 `ROLE_NOT_FOUND`，不强制 Services 本地制造该码）
- [ ] 入参含 `idempotency_key`（body）；网关既有幂等中间件 + `updatePlatformAdminRole` 转发已就绪（#4），本 issue 确保 gRPC 透传完整
- [ ] AuthZ：仅 platform-admin 可写（沿用网关 RBAC）

### E. 测试 + 门禁
- [ ] 单元测试 `TestPlatformAdminService_ListPlatformAdminRoles`：fake Core 返回 ≥1 角色 + permissions 数组断言（含 `resource`/`actions`/`scope`）
- [ ] 单元测试 `TestPlatformAdminService_GetPlatformAdminPermissions`：成功返回 role+permissions；NotFound 透传
- [ ] 单元测试 `TestPlatformAdminService_UpdatePlatformAdminRole`：调 Core ChangeRole + 审计 `change_role`（含 old/new）；ROLE_NOT_FOUND / PLATFORM_USER_NOT_FOUND
- [ ] 网关单测：`GET .../roles` permissions 为数组；`GET .../{id}/permissions` 转发；`PUT .../role` 参数拼装
- [ ] （可选）集成 `TestHandler_ChangeRoleFlow`：PUT role → GET permissions / GET detail 验证新角色
- [ ] `make test`、`make validate-architecture` 通过

## Dependencies
#2 (接口+SDK), #4 (Services 链路/网关既有 roles+role 转发), #7 (Core 角色列表 + 账号权限查询 API)

## Type
backend

## Priority
high

## References
- Core 契约（当前实现）：`GET /admin/platform-users/roles`、`GET /admin/platform-users/{userId}/permissions`、`PUT /admin/platform-users/{userId}/role`
- Core schema：`PlatformRole`（name + permissions）、`PlatformUserPermissionsResponse`（user_id + role + permissions）
- SPEC: §5.2.3 ListRoles / §5.2.4 ChangeRole / §4.2 / §9.2-9.3（静态四维映射以本 issue「实现对齐」为准废弃）
- UX: §3.1 创建-角色选择 / §3.2 改角色 / §4.3 权限 Tab（单账号权限视图调 GetPermissions）
- auth-service V2：`roles.permissions` 为运行时权威授权数据——列表/查询原样透出即真实授权面
