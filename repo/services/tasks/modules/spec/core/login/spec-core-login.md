# SPEC: Core Auth 账号密码登录扩展

> **来源：** 拆分自 `spec-core-login.md` v2.0
> **日期：** 2026-07-06（修订 2026-07-13、2026-07-15）
> **范围：** Core API（`v1.yaml`）、auth-service handler、数据库迁移
> **边界：** Console/BOSS 前端不在本 SPEC 范围（见 `spec-console-login.md`、`spec-boss-login.md`）

---

## 1. Summary

本 SPEC 规范 ANI Core 层的账号密码登录功能：

- 租户账密登录 API（`POST /auth/password/login`）
- 平台管理员账密登录 API（`POST /auth/platform/password/login`）
- bcrypt 密码哈希策略（cost ≥ 12）

**不覆盖：**
- OIDC 登录链路（已由其他团队完成）
- Dev Profile 可测登录端点
- Console/BOSS 前端 UI（见 `spec-console-login.md`、`spec-boss-login.md`）

---

## 2. Architecture

### 2.1 System Context

```text
Console/BOSS 前端
  │ POST /api/v1/auth/password/login
  │ POST /api/v1/auth/platform/password/login
  ▼
ani-gateway (路由 + Bearer middleware + 错误码映射)
  │ gRPC
  ▼
auth-service
  ├── JWTIssuer / JWTValidator (现有)
  ├── passwordLoginManager   (新增)
  └── platformLoginManager   (新增, 仅账密)
  │
  ▼
PostgreSQL
  ├── users        (tenant_id IS NULL = 平台管理员)
  ├── user_roles
  ├── roles        (tenant_id IS NULL = 平台内置角色)
  └── refresh_tokens
```

### 2.2 Component Design

| 组件 | 职责 | 边界 |
|------|------|------|
| `passwordLoginManager` | 租户账密校验、签发 TokenPair | 复用 `JWTIssuer` / `refreshTokenStore`；不访问 Redis |
| `platformLoginManager` | 平台账密校验、签发平台 TokenPair | 查询 `users WHERE tenant_id IS NULL AND EXISTS(platform-admin role)` |

---

## 3. Data Model

### 3.1 Schema Changes

```sql
-- 20260707_014_platform_users.sql
-- users.tenant_id 改为 NULLABLE
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_tenant_id_fkey;
ALTER TABLE users ALTER COLUMN tenant_id DROP NOT NULL;

-- 调整 UNIQUE 约束
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_tenant_username   ON users(tenant_id, username);
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_platform_username ON users(username) WHERE tenant_id IS NULL;

-- 20260707_014_platform_refresh_tokens.sql
ALTER TABLE refresh_tokens ALTER COLUMN tenant_id DROP NOT NULL;
ALTER TABLE refresh_tokens DROP CONSTRAINT IF EXISTS refresh_tokens_user_id_fkey;
ALTER TABLE refresh_tokens ADD CONSTRAINT refresh_tokens_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
```

### 3.2 Entity Definitions

```go
type passwordLoginManager struct {
    store  ports.PasswordLoginStore
    issuer *JWTIssuer
    now    func() time.Time
}

type platformLoginManager struct {
    store  ports.PlatformLoginStore
    issuer *JWTIssuer
    now    func() time.Time
}

type platformPrincipal struct {
    UserID uuid.UUID
    Roles  []string
}
```

---

## 4. API Design

### 4.1 Endpoints

| Method | Path | Description | Auth | Request | Response |
|--------|------|-------------|------|---------|----------|
| POST | `/auth/password/login` | 租户账密登录 | 无 | `PasswordLoginRequest` | `TokenPairResponse` |
| POST | `/auth/platform/password/login` | 平台账密登录 | 无 | `PlatformPasswordLoginRequest` | `TokenPairResponse` |

### 4.2 Request Schemas

```yaml
PasswordLoginRequest:
  type: object
  required: [tenant_name, username, password]
  properties:
    tenant_name: { type: string, maxLength: 128 }
    username:    { type: string, maxLength: 128 }
    password:    { type: string, maxLength: 256 }
    idempotency_key: { type: string, maxLength: 128 }

PlatformPasswordLoginRequest:
  type: object
  required: [username, password]
  properties:
    username:    { type: string, maxLength: 128 }
    password:    { type: string, maxLength: 256 }
    idempotency_key: { type: string, maxLength: 128 }
```

### 4.3 Error Responses

| HTTP | code | 触发条件 |
|------|------|----------|
| 400 | `BAD_REQUEST` | 缺参 / username 含命名空间前缀 |
| 401 | `INVALID_CREDENTIALS` | 账密不匹配 / 用户不存在（不区分） |
| 403 | `FORBIDDEN` | 平台 token 访问租户端点 / 反之 |
| 404 | `TENANT_NOT_FOUND` | tenant_name 不存在或不 active |
| 429 | `RATE_LIMIT_EXCEEDED` | 登录端点超频 |

---

## 5. Business Logic

### 5.1 租户账密登录

```text
1. 校验入参非空 → INVALID_CREDENTIALS
2. 校验 username 不含 ':' → BAD_REQUEST
3. 查租户 → ErrNoRows → TENANT_NOT_FOUND
4. 查用户 WHERE tenant_id=? AND username='local:'+username → INVALID_CREDENTIALS
5. bcrypt.CompareHashAndPassword → INVALID_CREDENTIALS
6. status='disabled' → INVALID_CREDENTIALS
7. IssueAccessToken → generateRefreshToken → InsertRefreshToken
8. TouchLastLogin (best-effort)
9. 返回 TokenPairResponse
```

### 5.2 平台账密登录

```text
1. 校验入参非空 → INVALID_CREDENTIALS
2. 校验 username 不含 ':' → BAD_REQUEST
3. 查平台管理员 WHERE u.tenant_id IS NULL AND EXISTS(platform-admin role) → INVALID_CREDENTIALS
4. bcrypt.CompareHashAndPassword → INVALID_CREDENTIALS
5. status='disabled' → INVALID_CREDENTIALS
6. 加载平台角色 (roles.tenant_id IS NULL) → 空 → 默认 ["platform-admin"]
7. IssuePlatformAccessToken(scope=platform) → generateRefreshToken → InsertRefreshToken
8. TouchLastLogin (best-effort)
9. 返回 TokenPairResponse
```

---

## 6. Security

- bcrypt cost=12（约 250ms/次）
- 密码不进入 Gateway access log
- INVALID_CREDENTIALS 不区分用户存在性（防枚举）
- 平台/租户 token 通过 `scope` claim 强制隔离
- 平台用户查询带 `tenant_id IS NULL` 谓词，租户查询带 `tenant_id=$1` 谓词

---

## 7. Testing Strategy

### 7.1 Unit Tests

| Test | 覆盖 |
|------|------|
| `TestPasswordLogin_Success` | 正确账密签发 token |
| `TestPasswordLogin_InvalidCredentials` | 错误密码 + 不存在用户均返回 INVALID_CREDENTIALS |
| `TestPasswordLogin_TenantNotFound` | 不存在租户返回 TENANT_NOT_FOUND |
| `TestPasswordLogin_NamespaceInjection` | username 含前缀拒绝 |
| `TestPasswordLogin_DisabledUser` | status=disabled → INVALID_CREDENTIALS |
| `TestPasswordLogin_IdempotencyKey` | 同 idempotency_key 返回同 TokenPair |
| `TestPlatformPasswordLogin_Success` | 平台账密登录成功，scope=platform |
| `TestPlatformPasswordLogin_InvalidCredentials` | 平台账密失败 |
| `TestPlatformPasswordLogin_TenantUserRejected` | 租户用户用平台端点登录 → INVALID_CREDENTIALS |
| `TestPlatformPasswordLogin_TenantIsolation` | 平台 token 不能访问租户 API、反之亦然 |

---

## 8. Implementation Plan

| 批次 | 内容 | 依赖 |
|------|------|------|
| M13.2-A1 | 迁移 `20260707_014_platform_users.sql` | — |
| M13.2-A2 | v1.yaml 新增 `/auth/password/login` | A1 |
| M13.2-A3 | passwordLoginManager + bcrypt + idempotency_key | A1, A2 |
| M13.2-A4 | auth_service.go Login RPC 实现 | A3 |
| M13.2-A5 | ani-gateway `/auth/password/login` 路由 | A4 |
| M13.2-A6 | v1.yaml 新增 `/auth/platform/password/login` | A2 |
| M13.2-A7 | platformLoginManager + RBAC scope=platform | A6 |
| M13.2-A8 | auth_service.go PlatformPasswordLogin RPC + 路由 | A7 |
| M13.2-A9 | Token 隔离中间件 | A8 |
| M13.2-A10 | 全量测试 + live gate | A3-A9 |

---

## 9. ANI Boundaries

| Item | Value |
|------|-------|
| Product line | **core** only |
| Code scope | `repo/api/openapi/v1.yaml`、`repo/api/proto/auth/v1/`、`repo/services/auth-service/`、`repo/services/ani-gateway/`、`repo/deploy/migrations/` |
| OpenAPI authority | **必须先改** `v1.yaml` |
| Frozen exclusions | 不改 Services `v1.yaml`；不动 `repo/frontends/` |
| idempotency_key | 账密登录、平台账密登录必须支持 |

---

## 10. 参考文档

- Console 登录 SPEC: `repo/services/tasks/modules/spec/console/login/spec-console-login.md`
- BOSS 登录 SPEC: `repo/services/tasks/modules/spec/boss/settings/spec-boss-login.md`
- PRD (Core): `repo/services/tasks/modules/prd/core/prd-core-auth.md`
- PRD (Console): `repo/services/tasks/modules/prd/console/login/prd-console-login-page.md`
- PRD (BOSS): `repo/services/tasks/modules/prd/boss/settings/prd-boss-login-page.md`

---

## 变更记录

| 日期 | 说明 |
|------|------|
| 2026-07-06 | 初版：基于 PRD v2.0 US-012/015 生成 Core 账密登录 SPEC |
| 2026-07-13 | 移除失败计数与账号锁定功能（v1.0.0 不再支持） |
| 2026-07-15 | 取消独立 `platform_users` 表，平台管理员复用 `users` 表（`tenant_id IS NULL`） |
| 2026-07-22 | 拆分为纯 Core 后端 SPEC，前端消费契约移至 `spec-console-login.md`、`spec-boss-login.md` |
