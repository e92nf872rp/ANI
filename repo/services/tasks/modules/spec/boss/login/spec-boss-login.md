# SPEC: BOSS 登录前端消费契约

> **来源：** 拆分自 `spec-core-login.md` v2.0
> **日期：** 2026-07-06
> **范围：** BOSS 前端平台管理员登录页、会话管理（与 Console 隔离）

---

## 1. Summary

本 SPEC 规范 BOSS 前端平台管理员登录功能对 Core Auth API 的消费契约，包括平台账密登录、会话存储（与 Console key 隔离）、路由守卫、token refresh、401 处理、登出。

**不覆盖：**
- Core 后端 handler 实现（见 `spec-core-login.md`）
- Console 前端登录（见 `spec-console-login.md`）
- 平台 OIDC 登录（暂不实现）

---

## 2. API 消费契约

### 2.1 消费的 Core 端点

| Method | Path | 用途 | 阶段 |
|--------|------|------|------|
| POST | `/auth/platform/password/login` | 平台账密登录 | P1 |
| POST | `/auth/refresh` | 刷新 access token | P1 |
| POST | `/auth/logout` | 登出 | P1 |

### 2.2 请求/响应契约

```typescript
// Platform Password Login
POST /auth/platform/password/login
Body: { username: string, password: string, idempotency_key?: string }
Response: TokenPairResponse

// TokenPairResponse
{ access_token: string, refresh_token: string, expires_in: int32, issued_at: timestamp }
```

### 2.3 平台 Token Claims

```text
tenant_id: null
roles: ["platform-admin", ...]
scope: "platform"
```

### 2.4 错误码映射

| HTTP | code | 前端文案 |
|------|------|----------|
| 400 | `BAD_REQUEST` | 表单校验提示 |
| 401 | `INVALID_CREDENTIALS` | 用户名或密码错误 |
| 429 | `RATE_LIMIT_EXCEEDED` | 请稍后重试 |

---

## 3. 路由结构

| 公开路由 | 受保护路由 |
|----------|------------|
| `/login`, `/auth/callback` | `/_authenticated/*` |

### 3.1 Vite base 配置

BOSS 配置 `base: '/boss/'`，所有资源路径和 `redirect_uri` 需带 `/boss` 前缀。

### 3.2 路由守卫

```typescript
export const Route = createFileRoute('/_authenticated')({
  beforeLoad: async ({ location }) => {
    const session = getSession()
    if (!session || !isSessionValid()) {
      saveReturnTo(location.pathname + location.searchStr)
      throw redirect({ to: '/login', search: { returnTo } })
    }
    await maybeRefresh()
    setAuthToken(session.access_token)
  },
  component: AuthenticatedLayout,
})
```

---

## 4. 会话管理

### 4.1 存储策略（与 Console 隔离）

| 场景 | 存储介质 | key 前缀 |
|------|----------|----------|
| 未勾选「记住我」 | `sessionStorage` | `boss:` |
| 勾选「记住我」 | `localStorage` | `boss:` |

**关键：** key 前缀 `boss:` 与 Console 的 `console:` 隔离，防止同一浏览器串会话。

### 4.2 401 处理

与 Console 相同：先 `refreshAccessToken()`，失败才 `handle401()`。`currentPath()` 需剥离 `/boss/` 前缀。

---

## 5. UI 布局

```text
┌──────────────────────────────┐
│    KuberCloud ANI BOSS       │
│  ─────────────────────────   │
│  [ 企业登录 | 账号密码 ]      │
│  ☐ 记住我                     │
│  [ 登录 ]                     │
│  本入口仅供平台管理员          │
└──────────────────────────────┘
```

使用 TDesign 组件：`Form`、`Input`、`Button`、`Checkbox`、`Tabs`、`Message`。

---

## 6. 参考文档

- Core Auth SPEC: `repo/services/tasks/modules/spec/core/login/spec-core-login.md`
- Console 登录 SPEC: `repo/services/tasks/modules/spec/console/login/spec-console-login.md`
- PRD: `repo/services/tasks/modules/prd/boss/settings/prd-boss-login-page.md`
- UX: `repo/services/tasks/modules/ux/boss/settings/ux-boss-login-page.md`
