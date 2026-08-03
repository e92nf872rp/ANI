# Development Records — Core Auth 账密登录 API

> **BATCH-ID:** auth-login-core-001
> **日期：** 2026-07-22
> **范围：** Core Auth API（租户账密登录 + 平台账密登录 + 平台用户迁移）
> **关联 Issue：** Core #001、#002、#003
> **SPEC：** `spec/core/login/spec-core-login.md`
> **PRD：** `prd/core/login/prd-core-auth.md`

---

## Implementation Notes / Design Choices

### 1. P0-1: 调整 access token 与 refresh token 的签发顺序

**Ambiguity:** SPEC 未规定 `IssueAccessToken` 与 `InsertRefreshToken` 的执行顺序。

**Choice:** 调整为 `IssueAccessToken → generateRefreshToken → InsertRefreshToken`。`passwordLoginManager` 和 `platformLoginManager` 均同步调整。

**Rationale:** `IssueAccessToken` 是纯计算（RSA 签名），不涉及 DB，失败概率极低且无副作用。即使后续 `InsertRefreshToken` 失败，用户只是拿不到 refresh token，不会产生孤儿数据。相比引入事务包裹（需修改 `ports.PasswordLoginStore` 接口签名），调整顺序是最小改动且完全消除孤儿风险。

### 2. P2-1: RBAC 中间件检查 scope 而非仅检查 tenant_id

**Ambiguity:** SPEC US-015 要求"平台 token 与租户 token 隔离"，但未规定 RBAC 中间件如何区分。

**Choice:** 改为 `if tenantID == "" && scope != "platform"` 才拦截。放行后由 `CheckPermission` RPC 统一判定权限。

**Rationale:** 平台管理员的 JWT 中 `tid` 为空字符串是设计决策。RBAC 应基于身份域（scope）区分拦截策略，保持权限决策集中在 auth-service。

---

## Spec Deviations + Rationale

### 1. P1-1: SQL 添加显式 u.tenant_id IS NULL 约束

**Spec:** 原 SQL 仅通过 `EXISTS (SELECT 1 FROM user_roles ... WHERE r.tenant_id IS NULL)` 间接过滤平台用户。

**Implementation:** 添加 `AND u.tenant_id IS NULL` 显式约束。

**Rationale:** 防御性编程。显式添加 `u.tenant_id IS NULL` 可避免未来 schema 变更后产生安全漏洞。属于纵深防御，不影响现有查询结果。

---

## Alternatives Considered

### 1. P0-1: 调整签发顺序 vs 事务包裹

**备选 A（已选）：** 调整签发顺序。优点：零接口变更，最小改动。缺点：`InsertRefreshToken` 失败时用户拿不到 refresh token（可接受）。

**备选 B：** 事务包裹。优点：原子性更强。缺点：需修改 `ports.PasswordLoginStore` 和 `ports.PlatformLoginStore` 接口签名，影响所有 adapter 实现。且 `IssueAccessToken` 是纯计算不需要事务。

**结论：** 方案 A 以最小改动消除核心风险。

---

## Follow-ups / Blockers

### 1. Refresh Token 不轮换的安全风险

当前 `RefreshToken` RPC 不签发新 refresh token，同一 refresh token 在 7 天有效期内可无限次换 access token。若泄露，攻击者可长期获取新 access token。建议评估是否启用 refresh token rotation。当前缓解：refresh token 以 SHA256 哈希存库。

---

## Verification Commands Run

```bash
cd services/auth-service
go test ./internal/service/... -run "TestPasswordLogin|TestPlatformLogin|TestRefresh" -count=1 -timeout 60s
# Result: PASS (3.967s)

cd services/ani-gateway
go test ./internal/middleware/... -count=1 -timeout 60s
# Result: PASS (3.608s)
```

---

## SPEC 验收标准对照

| SPEC 条目 | 实现状态 | 代码位置 |
|---|---|---|
| §4.1 `POST /auth/password/login` | ✅ | `password_login.go:32` |
| §4.1 `POST /auth/platform/password/login` | ✅ | `platform_login.go:37` |
| §4.2 PasswordLoginRequest | ✅ | `password_login.go:32-40` |
| §4.2 PlatformPasswordLoginRequest | ✅ | `platform_login.go:37-44` |
| §4.3 400 BAD_REQUEST | ✅ | `password_login.go:38-40` |
| §4.3 401 INVALID_CREDENTIALS | ✅ | `password_login.go:54-61` |
| §4.3 404 TENANT_NOT_FOUND | ✅ | `password_login.go:47-49` |
| §5.1 租户登录算法 1-9 | ✅ | `password_login.go:32-93` |
| §5.2 平台登录算法 1-9 | ✅ | `platform_login.go:37-89` |
| §5.1 步骤 7 签发顺序 | ✅ | `password_login.go:71-82` |
| §6 bcrypt cost=12 | ✅ | `password_login.go:100` |
| §6 平台/租户 token scope 隔离 | ✅ | `platform_login.go:67` |
| §6 平台用户查询 tenant_id IS NULL | ✅ | `postgres/password_login.go` |
| §7.1 TestPasswordLogin_* | ✅ PASS | go test |
| §7.1 TestPlatformPasswordLogin_* | ✅ PASS | go test |

---

## Issue 完成度

| Issue | 标题 | 状态 | 验收项 |
|---|---|---|---|
| #001 | 平台用户迁移 (users 表扩展) | ✅ 已完成 | 迁移 `20260707_014_platform_users.sql` 已合入 |
| #002 | 租户账密登录 API | ✅ 已完成 | v1.yaml + passwordLoginManager + ani-gateway 路由 + 测试 |
| #003 | 平台账密登录 API | ✅ 已完成 | v1.yaml + platformLoginManager + RBAC scope + Token 隔离 |

---

## 架构决策记录

### ADR-1: 平台管理员复用 users 表（2026-07-15）

**决策：** 取消独立 `platform_users` 表，平台管理员复用 `users` 表，通过 `tenant_id IS NULL` 区分。

### ADR-2: 移除失败计数与账号锁定（2026-07-13）

**决策：** 从 v1.0.0 移除失败计数与账号锁定功能，登录链路不再依赖 Redis。

### ADR-3: username 统一使用 local: 前缀

**决策：** 平台/租户用户 username 统一使用 `local:<username>` 前缀，与 OIDC 用户的 `oidc:<sub>` 隔离。

---

## 变更文件清单

| 文件 | 修复项 |
|---|---|
| `services/auth-service/internal/service/password_login.go` | P0-1 签发顺序 |
| `services/auth-service/internal/service/platform_login.go` | P0-1 签发顺序 |
| `pkg/adapters/postgres/password_login.go` | P1-1 SQL 约束 |
| `services/ani-gateway/internal/middleware/rbac.go` | P2-1 RBAC scope |

---