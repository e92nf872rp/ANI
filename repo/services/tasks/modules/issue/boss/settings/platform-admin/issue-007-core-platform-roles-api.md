# Core：平台角色权限查询 API（角色列表 + 账号权限）

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/settings/prd-new-boss-platform-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/settings/ux-new-boss-platform-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/settings/spec-new-boss-platform-admin.md`

## Description
在 Core 层实现**两个**平台权限查询端点（替代原单一 roles 端点设计）：

1. **平台角色列表（不分页）**：`GET /api/v1/admin/platform-users/roles` —— 查询 `roles` 表中 `tenant_id IS NULL AND name LIKE 'platform-%'` 的全部平台内置角色，**原样返回** `permissions` JSONB（`resource` / `actions` / `scope` 数组，不做四维映射），供 Services 经 Core SDK 调用（创建/改角色时的可变更角色下拉）。
2. **具体账号权限**：`GET /api/v1/admin/platform-users/{userId}/permissions` —— 提供 userId，JOIN `users`→`user_roles`→`roles` 查询该账号当前绑定的角色及其 `permissions` JSONB 原样数组。

两者共同替换 #2 中 `PlatformUserAdminStore.ListPlatformRoles` 与 `CorePlatformUserClient.ListPlatformRoles` 的 501 占位（后者新增 `GetPlatformUserPermissions` 方法）。

> 种子 resource 维度（#3 已实现）：`tenants`（租户）/ `resource_pool`（资源池）/ `metering`（计量）；「无权限」维度省略条目。SQL 按 `name LIKE 'platform-%'` 匹配而非硬编码三角色名 IN 列表——角色表是运行时权威数据源（auth-service V2 授权直读），未来新增 `platform-*` 前缀平台角色无需改代码。

## Scope
- Product line: core + services（Core 实现为主；Services 仅补 Core SDK client 封装）
- Code paths allowed: `repo/api/openapi/v1.yaml`、`repo/pkg/ports/platform_user_admin.go`、`repo/pkg/adapters/postgres/platform_user_admin.go`、`repo/services/ani-gateway/internal/router/`、`repo/services/ani-gateway/main.go`、`repo/sdks/core/`、`repo/services/platform-settings-service/internal/repo/adapters/core/platform_user_client.go`、`repo/services/platform-settings-service/internal/repo/ports/core_platform_user.go`

## Acceptance Criteria

### A. OpenAPI（Core `v1.yaml`）
- [ ] `GET /admin/platform-users/roles`（operationId: `listPlatformUserRoles`）：**无分页参数**；响应 schema `PlatformRoleListResponse`（items: PlatformRole[]）；RBAC scope `scope:users:read`
- [ ] `GET /admin/platform-users/{userId}/permissions`（operationId: `getPlatformUserPermissions`）：path 参数 `userId`（UUID）；响应 schema `PlatformUserPermissionsResponse`；RBAC scope `scope:users:read`；404 复用 `PlatformUserNotFound`（账号不存在/已软删除/非平台账号）
- [ ] schema `PlatformRole`：
  ```yaml
  PlatformRole:
    type: object
    properties:
      name: { type: string }
      label: { type: string, description: 中文展示名（Core 内置映射，不新增 DB 列） }
      description: { type: string }
      permissions:
        type: array
        description: "roles.permissions JSONB 原样（resource/actions/scope）"
        items: { type: object, additionalProperties: true }
  ```
- [ ] schema `PlatformUserPermissionsResponse`：
  ```yaml
  PlatformUserPermissionsResponse:
    type: object
    properties:
      user_id: { type: string, format: uuid }
      role: { type: string, description: "当前绑定角色名（platform-admin/ops/readonly）" }
      label: { type: string }
      description: { type: string }
      permissions:
        type: array
        description: "该角色 roles.permissions JSONB 原样"
        items: { type: object, additionalProperties: true }
  ```

### B. Core 端口 + PostgreSQL 适配器
- [ ] `PlatformUserAdminStore.ListPlatformRoles(ctx)` 实现（替换 #2 占位）：`WithPlatformTx` 只读查询
  `SELECT name, permissions FROM roles WHERE tenant_id IS NULL AND name LIKE 'platform-%' ORDER BY name`，permissions 原样解码，**不做映射**；label/description 按角色名内置中文映射
- [ ] `PlatformUserAdminStore` 新增 `GetPlatformUserPermissions(ctx, userID uuid.UUID)`：单次 JOIN 查询
  `users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.id=$1 AND u.tenant_id IS NULL AND u.is_deleted=FALSE`，返回 role name + permissions JSONB 原样；无行 → `ports.ErrPlatformUserNotFound`（哨兵复用 Get 语义）
- [ ] `PlatformRole` struct 保持 `Permissions []map[string]any`（#2 已定义，不动）；新增返回结构（如 `PlatformUserPermissions{ UserID, Role, Label, Description, Permissions }`）

### C. 网关 handler + 路由
- [ ] `platform_user_resources.go`（或 #4 已建文件）实现两个 GET handler，调 Store + 响应组装；404 映射 `PlatformUserNotFound` response
- [ ] `main.go` 路由注册（若 #4 尚未合入则在本 issue 一并注册）；静态段 `/roles`、`/{userId}/permissions` 注册顺序先于 `/{userId}`（避免路径参数吞掉静态段，按现有 router 先例）

### D. SDK + Services client
- [ ] `make gen-core-sdk`，Core SDK 新增 `listPlatformUserRoles` / `getPlatformUserPermissions` 两个 operation
- [ ] `CorePlatformUserClient.ListPlatformRoles` 实现（替换 #2 骨架），封装 `GET /admin/platform-users/roles`
- [ ] `CorePlatformUserClient` 新增 `GetPlatformUserPermissions(ctx, userID)`，封装 `GET /admin/platform-users/{userId}/permissions`（ports + adapter 同步补 DTO）

### E. 测试 + 门禁
- [ ] 单元测试 `TestPlatformUserAdminStore_ListPlatformRoles`：seed platform-admin/ops/readonly 三角色（`tenant-admin` 等 tenant 角色不出现在结果——验证 `platform-%` 前缀过滤）；每项 permissions 为数组且元素含 resource/actions/scope
- [ ] 单元测试 `TestPlatformUserAdminStore_GetPlatformUserPermissions`：正常返回角色+权限数组；不存在 userId → `ErrPlatformUserNotFound`；已软删除 → 同 404
- [ ] `make openapi-lint`、`make test`、`make validate-architecture` 通过

## Dependencies
#1 (OpenAPI 契约), #2 (端口+接口骨架), #3 (platform-ops/platform-readonly 角色种子)

## Type
backend

## Priority
high

## References
- SPEC: §3.1.2 平台运营角色种子（实现修正块） / §4.1 Core Endpoints / §4.2 Services `GET /platform-admins/roles` / §5.2.3 ListRoles
- UX: §3.1 创建-角色选择 / §3.2 改角色 / §4.3 权限 Tab（单账号权限视图改调新端点）
- 种子 resource：tenants / resource_pool / metering（见 #3；SPEC 草稿四维为 Services 静态展示层，与 DB 种子分属两层）
- auth-service [permissions.go](../../../../../services/auth-service/internal/service/permissions.go) `userPermissions`：`roles.permissions` 是 V2 授权运行时权威数据——本 API 原样透出即运行时真实授权

## Blocks
#8（Services：平台角色权限列表 `ListPlatformAdminRoles` → `ListPlatformRoles`；用户权限查询 `GetPlatformAdminPermissions` → `GetPlatformUserPermissions`；用户权限修改 `UpdatePlatformAdminRole` → `ChangeRole`）
