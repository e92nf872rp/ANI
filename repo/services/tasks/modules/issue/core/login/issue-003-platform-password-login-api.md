# Issue 003: 平台账密登录 API（v1.yaml + auth-service + ani-gateway）

## Document Links
- PRD: `repo/services/tasks/modules/prd/console/login/prd-console-login-page.md`
- UX: `repo/services/tasks/modules/prd/boss/login/ux-boss-login-page.md`（BOSS 前端消费，本 Issue 不实现前端）
- SPEC: `repo/services/tasks/modules/spec/core/login/spec-core-login.md`

## Description

实现平台管理员账密登录 API `POST /api/v1/auth/platform/password/login`：平台管理员输入 `username` + `password`（无 tenant_name），校验 `users` 表（`WHERE u.username='local:'+username AND EXISTS (user_roles→roles.name='platform-admin' AND roles.tenant_id IS NULL)`，平台管理员与租户用户同表同 `local:` 前缀，由 `user_roles` 角色绑定区分）后签发**平台 token**（claims: `tenant_id=null` + `roles`（来自 `user_roles` + `roles`，`roles.tenant_id IS NULL` 表示平台内置角色） + `scope=platform`）。平台 token 与租户 token 通过 `scope` claim 强制隔离，Gateway 中间件按 scope 路由白名单分流。

失败计数/锁定功能于 2026-07-13 从 v1.0.0 移除，平台登录链路不再依赖 Redis。防暴力破解由 bcrypt cost ≥ 12 + 限流 + 防枚举（INVALID_CREDENTIALS 不区分用户存在性）覆盖。

依赖 Issue #001（users 表扩展支持平台管理员）与 Issue #002（passwordLoginManager、errors_auth、bcrypt 校验、idempotency_key 基础设施可复用）。

**2026-07-15 决策**：取消独立 `platform_users` 表，平台管理员复用 `users` 表（`tenant_id IS NULL` 区分存储），角色通过 `user_roles` + `roles`（`roles.tenant_id IS NULL`）映射。**平台/租户用户 username 统一使用 `local:<username>` 前缀**，由 `user_roles` 角色绑定区分（平台管理员关联 `roles.name='platform-admin'`）。`platformLoginManager` 新增 `LoadRoles` 方法，与 `passwordLoginManager.LoadRoles` 对称（但查询 `roles.tenant_id IS NULL` 而非 `roles.tenant_id=$1`）。`LookupUser` 查询使用 `EXISTS platform-admin role` 谓词，与 `tenant_id` 存储约定解耦。

## Scope
- Product line: core
- Code paths allowed:
  - `repo/api/openapi/v1.yaml`（契约）
  - `repo/api/proto/auth/v1/auth_service.proto`（gRPC 契约）
  - `repo/services/auth-service/internal/service/`（实现）
  - `repo/services/auth-service/internal/config/`（配置）
  - `repo/services/ani-gateway/internal/router/`（路由 + middleware）

## Acceptance Criteria

### OpenAPI 契约

- [ ] `v1.yaml` 新增 `PlatformPasswordLoginRequest` schema：required `[username, password]`（无 tenant_name），可选 `idempotency_key`
- [ ] `v1.yaml` 新增 `POST /auth/platform/password/login` 路径，operationId `platformPasswordLogin`，tags `[Auth]`，security `[]`（公开端点）
- [ ] 该端点 200 响应复用 `TokenPairResponse` schema
- [ ] 错误响应：400 BadRequest、401 INVALID_CREDENTIALS、429 RATE_LIMIT_EXCEEDED（422 ACCOUNT_LOCKED 已于 2026-07-13 移除）
- [ ] `make validate-openapi` 通过（无破坏性变更）

### gRPC Proto 契约

- [ ] `auth_service.proto` 新增 `PlatformPasswordLogin(PlatformPasswordLoginRequest) returns (TokenPair)` RPC
- [ ] message `PlatformPasswordLoginRequest` 与 OpenAPI schema 一致

### auth-service 实现

- [ ] 新增 `repo/services/auth-service/internal/service/platform_login.go`：`platformLoginManager` 类型
- [ ] `platformLoginManager.Login(ctx, username, password)` 算法（SPEC §5.1 平台账密，2026-07-13 移除失败计数/锁定后）：
  1. 校验入参非空 → 否则 INVALID_CREDENTIALS
  2. 校验 username 不含 `:` 前缀 → 否则 BAD_REQUEST
  3. 查 `users WHERE u.username='local:'+username AND EXISTS (user_roles→roles.name='platform-admin' AND roles.tenant_id IS NULL)`（ErrNoRows / password_hash="" → INVALID_CREDENTIALS，防枚举）
  4. bcrypt.CompareHashAndPassword 失败 → INVALID_CREDENTIALS
  5. status='disabled' → INVALID_CREDENTIALS
  6. LoadRoles：`SELECT r.name FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=$1 AND r.tenant_id IS NULL`，空则默认 `["platform-admin"]`
  7. 成功 → 签发平台 token（tenant_id=null, scope=platform, roles=LoadRoles 结果）、INSERT refresh_tokens（tenant_id=NULL, user_id=users.id）、UPDATE users.last_login_at（best-effort）
- [ ] `JWTIssuer` 扩展支持 `scope=platform` claim（principal 为 `platformPrincipal`，无 tenant_id）
- [ ] 平台 refresh_token 存储：复用 `refresh_tokens` 表（tenant_id=NULL，user_id 引用 users.id），不新增独立表 — 由应用层 `platformLoginManager` / `passwordLoginManager` 保证 user_id ↔ tenant_id 一致性
- [ ] `auth_service.go` 新增 `PlatformPasswordLogin` RPC handler，委托 `platformLoginManager.Login`
- [ ] `cmd/server/main.go` 注入 `platformLoginManager` 到 `AuthService`

### ani-gateway 路由与隔离

- [ ] `repo/services/ani-gateway/internal/router/auth.go` 新增 `v1.POST("/auth/platform/password/login", api.platformPasswordLogin)` 路由
- [ ] 平台路由组独立（`/auth/platform/*` 前缀），与租户路由 `/auth/password/*` 物理隔离
- [ ] 错误码映射复用 `authHTTPError`，扩展平台端点专属错误处理
- [ ] idempotency_key middleware 支持

### Token 隔离中间件

- [ ] Bearer middleware 校验 `scope` claim：`scope=tenant` token 不能访问 `/auth/platform/*` 端点（除登录端点本身），`scope=platform` token 不能访问 `/auth/password/*` 等租户端点
- [ ] 违反 → 403 FORBIDDEN
- [ ] 集成测试覆盖双向串号场景

### 安全

- [ ] bcrypt cost=12
- [ ] 密码字段内存清零
- [ ] 密码不进入 access log

### 测试

- [ ] `TestPlatformPasswordLogin_Success`：正确平台账密签发 token，claims 含 scope=platform、tenant_id=null、roles=["platform-admin"]（或 user_roles + roles 映射的多角色）
- [ ] `TestPlatformPasswordLogin_InvalidCredentials`：错误密码 + 不存在用户均返回 INVALID_CREDENTIALS
- [ ] `TestPlatformPasswordLogin_DisabledUser`：status=disabled → INVALID_CREDENTIALS
- [ ] `TestPlatformPasswordLogin_NamespaceInjection`：平台 username 含前缀 → BAD_REQUEST
- [ ] `TestPlatformPasswordLogin_OIDCOnlyUserRejected`：平台 OIDC-only 用户 → INVALID_CREDENTIALS
- [ ] `TestPlatformPasswordLogin_BadRequestOnEmptyInputs`：空入参 → INVALID_CREDENTIALS
- [ ] `TestPlatformPasswordLogin_IdempotencyKey`：同 idempotency_key 重复请求返回同 TokenPair
- [ ] `TestPlatformLogin_TenantIsolation`：平台 token 不能访问租户 API、租户 token 不能访问平台 API
- [ ] `TestPlatformPasswordLogin_TenantUserRejected`：租户用户用平台端点登录 → INVALID_CREDENTIALS（`WHERE username='local:'+input AND EXISTS platform-admin role` 不匹配无 platform-admin 角色绑定的租户用户，角色绑定隔离）
- [ ] `TestTenantPasswordLogin_PlatformUserRejected`：平台用户用租户端点登录 → INVALID_CREDENTIALS（`WHERE tenant_id=$1 AND username=$2` 不匹配 `tenant_id IS NULL` 行）
- [ ] `make test` 通过
- [ ] `make validate-architecture` 通过
- [ ] `git diff --check` 通过

## Dependencies
Issue #001（users 表扩展支持平台管理员 + idx_refresh_tokens_user_id 索引）
Issue #002（passwordLoginManager、errors_auth、bcrypt、idempotency_key 基础设施）

## Type
core（API + 服务实现 + 网关路由 + token 隔离中间件）

## Priority
medium

## References
- SPEC: §4.1 OpenAPI Change Plan、§4.3 Schemas、§4.4 Error Responses、§5.1 Core Algorithms (PlatformPasswordLogin)、§7.1 Authentication & Authorization、§7.3 Data Protection（scope 隔离）、§9 Testing Strategy、§11.1 Unresolved Questions（platform_refresh_tokens 存储）
- PRD: US-015（仅账密部分）、FR-15、FR-16、FR-17
- UX: `login/ux-boss-login-page.md` §5 Component Mapping（前端消费，本 Issue 不实现）
- CLAUDE.md: §3 强制架构边界、§4 API 与 SDK 强制规则、§7 提交与版本

## Notes
- **Open Question**：平台 refresh_token 存储（复用 refresh_tokens 表 vs 独立 platform_refresh_tokens 表）— 已决策复用 `refresh_tokens` 表（tenant_id=NULL，user_id 引用 users.id）；迁移 20260707_014 已 DROP NOT NULL 与原 user_id FK，由应用层 `platformLoginManager` / `passwordLoginManager` 保证 user_id ↔ tenant_id 一致性
- 平台 token claims 必须含 `scope=platform`，Gateway middleware 按 scope 路由白名单强制分流是安全关键
- 平台管理员与租户用户复用 `users` 表，username 统一使用 `local:<username>` 前缀，由 `user_roles` 角色绑定区分（平台管理员关联 `roles.name='platform-admin'`）；`idx_users_platform_username` / `idx_users_platform_email`（partial UNIQUE INDEX `WHERE tenant_id IS NULL`）保证平台管理员全局唯一
- 平台管理员角色通过 `user_roles` + `roles`（`roles.tenant_id IS NULL` 表示平台内置角色）映射，与租户 RBAC 基础设施一致
- 本 Issue **不实现** 平台 OIDC 登录（US-015 中 OIDC 部分由其他团队负责，CLAUDE.md 边界外）
- 初始平台管理员账号创建方式（ani-installer bootstrap-admin vs 迁移脚本）属独立 Issue，不在本范围
- **失败计数/锁定**：2026-07-13 从 v1.0.0 移除；`auth:fail:platform:*` / `auth:lock:platform:*` Redis key 全部下线
