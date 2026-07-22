# Issue 002: 租户账密登录 API（v1.yaml + auth-service + ani-gateway）

## Document Links
- PRD: `repo/services/tasks/modules/prd/console/login/prd-console-login-page.md`
- UX: `repo/services/tasks/modules/prd/console/login/ux-console-login-page.md`（前端消费，本 Issue 不实现前端）
- SPEC: `repo/services/tasks/modules/spec/core/login/spec-core-login.md`

## Description

实现租户账号密码登录 API `POST /api/v1/auth/password/login`：用户输入 `tenant_name` + `username` + `password`，校验 `users` 表（`username` 命名空间为 `local:<username>`，与 OIDC 用户 `oidc:<sub>` 隔离）后签发 `TokenPair`（与现有 OIDC `TokenPairResponse` 一致）。

bcrypt cost≥12。错误码统一为 `INVALID_CREDENTIALS`、`TENANT_NOT_FOUND`，不区分用户存在性以防枚举。失败计数与账号锁定功能于 2026-07-13 从 v1.0.0 移除，登录链路不再依赖 Redis（仅 idempotency middleware 仍用 Redis，由其自身 fail-open 策略覆盖）。

依赖 Issue #001（users 表扩展支持平台管理员 + idx_refresh_tokens_user_id 索引；本 Issue 仅依赖 users 表既有 password_hash 列，迁移批次的索引为后续会话吊销功能预留）。本 Issue 是平台账密登录的基础依赖。

## Scope
- Product line: core
- Code paths allowed:
  - `repo/api/openapi/v1.yaml`（契约）
  - `repo/api/proto/auth/v1/auth_service.proto`（gRPC 契约）
  - `repo/services/auth-service/internal/service/`（实现）
  - `repo/services/auth-service/internal/config/`（配置）
  - `repo/services/ani-gateway/internal/router/`（路由）

## Acceptance Criteria

### OpenAPI 契约（必须先改）

- [ ] `v1.yaml` 新增 `PasswordLoginRequest` schema：required `[tenant_name, username, password]`，可选 `idempotency_key`
- [ ] `v1.yaml` 新增 `POST /auth/password/login` 路径，operationId `passwordLogin`，tags `[Auth]`，security `[]`（公开端点）
- [ ] 该端点 200 响应复用现有 `TokenPairResponse` schema（不新增 schema）
- [ ] 错误响应：400 BadRequest、401 INVALID_CREDENTIALS、404 TENANT_NOT_FOUND、429 RATE_LIMIT_EXCEEDED（422 ACCOUNT_LOCKED 已于 2026-07-13 移除）
- [ ] `make validate-openapi` 通过（无破坏性变更）

### gRPC Proto 契约

- [ ] `auth_service.proto` 已声明 `Login(LoginRequest) returns (TokenPair)` RPC（无需新增，已有）
- [ ] message `LoginRequest` 已含 `username`、`password`、`tenant_name` 字段（验证）
- [ ] 仅需将 `auth_service.go` 中 `Login` 的实现从 `Unimplemented` 替换为实际逻辑

### auth-service 实现

- [ ] 新增 `repo/services/auth-service/internal/service/password_login.go`：`passwordLoginManager` 类型
- [ ] `passwordLoginManager.Login(ctx, tenant_name, username, password)` 算法（SPEC §5.1 密码校验，2026-07-13 移除失败计数/锁定后）：
  1. 校验入参非空 → 否则 INVALID_CREDENTIALS
  2. 校验 username 不含 `:` 前缀（防命名空间注入）→ 否则 BAD_REQUEST
  3. 查租户 `SELECT id FROM tenants WHERE name=? AND status='active'`（ErrNoRows → TENANT_NOT_FOUND）
  4. 查用户 `SELECT id, password_hash, status FROM users WHERE tenant_id=? AND username='local:'+username`（ErrNoRows / password_hash="" → INVALID_CREDENTIALS，不区分存在性）
  5. bcrypt.CompareHashAndPassword 失败 → INVALID_CREDENTIALS
  6. status='disabled' → INVALID_CREDENTIALS
  7. 成功 → 生成 refresh_token（复用 `generateRefreshToken` + `hashRefreshToken`）、INSERT refresh_tokens、IssueAccessToken(1h)、UPDATE users.last_login_at（best-effort）
- [ ] 复用现有 `JWTIssuer.IssueAccessToken` 与 `refreshTokenStore`（不新增 issuer）
- [ ] `auth_service.go` 修改 `Login` RPC：从 `return nil, status.Error(codes.Unimplemented, ...)` 替换为 `return s.passwordLogin.Login(ctx, req)`
- [ ] `NewAuthService` 注入 `passwordLoginManager`
- [ ] `cmd/server/main.go` 无需改动（NewAuthService 内部注入）

### 配置项

- [ ] `repo/services/auth-service/internal/config/config.go`：无新增配置项（失败计数/锁定相关的 `PasswordMaxFailures` / `PasswordLockDuration` 已于 2026-07-13 移除）
- [ ] 配置项支持环境变量加载（与现有 JWT 配置一致）

### 错误码常量

- [ ] 新增 `repo/services/auth-service/internal/service/errors_auth.go`：定义错误码常量 `INVALID_CREDENTIALS`、`TENANT_NOT_FOUND`
- [ ] gRPC status code → HTTP code 映射函数（与 ani-gateway `authHTTPError` 对齐）

### ani-gateway 路由

- [ ] `repo/services/ani-gateway/internal/router/auth.go` 新增 `v1.POST("/auth/password/login", api.passwordLogin)` 路由
- [ ] handler 函数 `passwordLogin` 调用 gRPC `Login` 并映射错误码
- [ ] idempotency_key middleware 支持（复用现有 middleware）
- [ ] 密码字段不进入 access log（Hertz middleware body mask）

### 安全

- [ ] bcrypt cost=12
- [ ] 密码字段 `[]byte` 显式 zeroing 后释放
- [ ] 密码不进入 Gateway access log（按字段名 mask）
- [ ] idempotency_key 强制 `^[A-Za-z0-9_-]{1,128}$` 防注入

### 测试

- [ ] `TestPasswordLogin_Success`：正确账密签发 token，claims 含 tenant_id、user_id、roles
- [ ] `TestPasswordLogin_InvalidCredentials`：错误密码 + 不存在用户均返回 INVALID_CREDENTIALS（不区分）
- [ ] `TestPasswordLogin_TenantNotFound`：不存在租户返回 TENANT_NOT_FOUND
- [ ] `TestPasswordLogin_NamespaceInjection`：username 含 `local:`/`oidc:`/`platform:` 前缀 → BAD_REQUEST
- [ ] `TestPasswordLogin_DisabledUser`：status=disabled → INVALID_CREDENTIALS
- [ ] `TestPasswordLogin_IdempotencyKey`：同 idempotency_key 重复请求返回同 TokenPair
- [ ] `TestPasswordLogin_OIDCUserRejected`：OIDC 用户（password_hash=NULL）尝试账密登录 → INVALID_CREDENTIALS
- [ ] `TestPasswordLogin_BadRequestOnEmptyInputs`：空入参 → INVALID_CREDENTIALS
- [ ] `TestTenantPasswordLogin_PlatformUserRejected`：平台管理员（`users.tenant_id IS NULL`）用租户端点登录 → INVALID_CREDENTIALS（`WHERE tenant_id=$1 AND username=$2` 不匹配 `tenant_id IS NULL` 行）
- [ ] `make test` 通过
- [ ] `make validate-architecture` 通过
- [ ] `git diff --check` 通过

## Dependencies
Issue #001（users 表扩展支持平台管理员 + idx_refresh_tokens_user_id 索引；本 Issue 仅依赖 users 表既有 password_hash 列，但迁移批次的索引被 `RevokeAllForUser` 使用，建议先完成）

## Type
core（API 契约 + 服务实现 + 网关路由）

## Priority
high

## References
- SPEC: §4.1 OpenAPI Change Plan、§4.3 Schemas、§4.4 Error Responses、§5.1 Core Algorithms (PasswordLogin)、§5.2 Validation Rules、§5.4 Edge Cases、§7.2 Input Validation、§7.3 Data Protection、§9.1 Unit Tests、§9.2 Integration Tests
- PRD: US-012、FR-14、FR-16、FR-17
- UX: `login/ux-console-login-page.md` §5.2 账密 Tab Component Mapping、§6.1 States（前端消费，本 Issue 不实现）
- CLAUDE.md: §3 强制架构边界、§4 API 与 SDK 强制规则、§5 组件边界（ports/adapters）

## Notes
- **用户名命名空间**：`local:<username>` 前缀防 OIDC 用户与本地用户名碰撞，复用 `users.username` UNIQUE 约束
- **错误码统一**：INVALID_CREDENTIALS 不区分用户存在性，防枚举攻击
- **idempotency_key 缓存**：仅缓存成功结果，失败不缓存
- **失败计数/锁定**：2026-07-13 从 v1.0.0 移除；`auth:fail:*` / `auth:lock:*` Redis key 全部下线，登录链路不再依赖 Redis；防暴力破解由 bcrypt cost ≥ 12 + 限流 + 防枚举覆盖
- 本 Issue **不实现**：ChangePassword、ResetUserPassword、密码强度校验、密码历史防复用（后续 Issue）、平台账密登录（Issue #003）
- 本 Issue **不涉及** OIDC 现有端点（不改 `/auth/oidc/begin`、`/auth/token`、`/auth/refresh`、`/auth/logout`）
