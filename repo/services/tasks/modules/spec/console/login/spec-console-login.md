# SPEC: Console 登录前端消费契约

> **来源：** 拆分自 `spec-core-login.md` v2.0
> **日期：** 2026-07-06
> **范围：** Console 前端登录页、会话管理、路由守卫（消费 Core Auth API 契约）

---

## 1. Summary

本 SPEC 规范 Console 前端登录功能对 Core Auth API 的消费契约，包括 OIDC 链路、账密登录 Tab、会话存储、路由守卫、token refresh、401 处理、登出。

**不覆盖：**
- Core 后端 handler 实现（见 `spec-core-login.md`）
- BOSS 前端登录（见 `spec-boss-login.md`）

---

## 2. API 消费契约

### 2.1 消费的 Core 端点

| Method | Path | 用途 | 阶段 |
|--------|------|------|------|
| POST | `/auth/oidc/begin` | OIDC 登录发起 | P0 |
| POST | `/auth/token` | OIDC callback 换 token | P0 |
| POST | `/auth/refresh` | 刷新 access token | P0 |
| POST | `/auth/logout` | 登出 | P0 |
| POST | `/auth/password/login` | 账密登录 | P1 |

### 2.2 请求/响应契约

```typescript
// OIDC Begin
POST /auth/oidc/begin
Body: { tenant_name: string, redirect_uri: string }
Response: { authorization_url: string, state: string }

// OIDC Token
POST /auth/token
Body: { code: string, state: string, redirect_uri: string }
Response: TokenPairResponse

// Password Login (P1)
POST /auth/password/login
Body: { tenant_name: string, username: string, password: string, idempotency_key?: string }
Response: TokenPairResponse

// TokenPairResponse
{ access_token: string, refresh_token: string, expires_in: int32, issued_at: timestamp }
```

### 2.3 错误码映射

| HTTP | code | 前端文案 |
|------|------|----------|
| 400 | `BAD_REQUEST` | 表单校验提示 |
| 401 | `INVALID_CREDENTIALS` | 用户名或密码错误 |
| 404 | `TENANT_NOT_FOUND` | 租户不存在，请检查租户标识 |
| 503 | `IDP_UNAVAILABLE` | 身份提供商暂不可用 |
| 401 | `OIDC_EXCHANGE_FAILED` | 登录回调失败，请重试 |
| 429 | `RATE_LIMIT_EXCEEDED` | 请稍后重试 |

---

## 3. 路由结构

| 公开路由 | 受保护路由 |
|----------|------------|
| `/login`, `/auth/callback` | `/_authenticated/*` |

### 3.1 路由守卫 beforeLoad

```typescript
export const Route = createFileRoute('/_authenticated')({
  beforeLoad: async ({ location }) => {
    const session = getSession()
    if (!session || !isSessionValid()) {
      saveReturnTo(location.pathname + location.searchStr)
      throw redirect({ to: '/login', search: { returnTo } })
    }
    await maybeRefresh()  // 剩余 < 5 分钟自动续期
    setAuthToken(session.access_token)
  },
  component: AuthenticatedLayout,
})
```

---

## 4. 会话管理

### 4.1 存储策略

| 场景 | 存储介质 | key 前缀 |
|------|----------|----------|
| 未勾选「记住我」 | `sessionStorage` | `console:` |
| 勾选「记住我」 | `localStorage` | `console:` |

### 4.2 会话字段

```typescript
interface Session {
  access_token: string
  refresh_token: string
  expires_at: number  // epoch ms
  tenant_name: string
  remember_me: boolean
}
```

### 4.3 401 处理

```typescript
// 401 中间件
if (response.status === 401 && !isAuthEndpoint) {
  const refreshed = await refreshAccessToken()  // 先尝试 refresh
  if (!refreshed) {
    handle401()  // 失败才清会话 + 跳登录
  }
}
```

---

## 5. UI 布局

```text
┌──────────────────────────────┐
│      KuberCloud ANI          │
│  ─────────────────────────   │
│  [ 企业登录 | 账号密码 ]      │  ← P1 起显示 Tab
│  租户标识 *                   │
│  ☐ 记住我                     │
│  [ 登录 ]                     │
│  将跳转到企业身份提供商…       │  ← 仅 OIDC Tab
└──────────────────────────────┘
```

使用 TDesign 组件：`Form`、`Input`、`Button`、`Checkbox`、`Tabs`、`Message`。

---

## 6. 参考文档

- Core Auth SPEC: `repo/services/tasks/modules/spec/core/login/spec-core-login.md`
- BOSS 登录 SPEC: `repo/services/tasks/modules/spec/boss/settings/spec-boss-login.md`
- PRD: `repo/services/tasks/modules/prd/console/login/prd-console-login-page.md`
- UX: `repo/services/tasks/modules/ux/console/tenant/ux-console-login-page.md`
