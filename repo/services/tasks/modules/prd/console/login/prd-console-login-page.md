# PRD: Console 登录与身份认证

> **来源：** 拆分自 `prd-console-login-page.md` v2.0
> **日期：** 2026-07-03
> **范围：** Console 前端登录页、会话管理、路由守卫

---

## 1. Overview

Console 控制台需要统一、可验收的登录与身份认证能力。租户用户通过 OIDC 或账密登录获得 `access_token` / `refresh_token`，并在会话过期、主动登出或 API 401 时被安全地引导回登录页。

**产品入口：**

```text
Console /login → 租户上下文（tenant slug + OIDC 或 账密）
IdP 页（站外） → 用户名 + 密码（OIDC 路径）
```

---

## 2. Goals

- Console 提供 **Plain 企业风** 登录页，支持 **OIDC** 与 **账密** 两种入口（Tab 切换），不暴露 IdP 内部术语
- 登录成功后 **`returnTo` 优先** 回跳；无 `returnTo` 时进 `/`
- 提供 **「记住我」** 勾选：未勾选 `sessionStorage`；勾选 `localStorage`（同 key 结构）
- 完整会话链路：**begin/callback → 门禁 → refresh → 401 → logout → 启动 hydrate**
- 全部 POST 写操作支持 `idempotency_key`

---

## 3. User Stories

### US-001: Console 登录页 Plain UI（OIDC 入口）

**Acceptance Criteria:**

- [ ] `/login` 使用 **Plain** 布局：居中卡片 ~400px、`KuberCloud ANI` 品牌、无 Confetti/gamified 动画
- [ ] 表单含：`tenant_name`（必填）、**「记住我」** `Checkbox`、主按钮「登录」
- [ ] 底部说明：「将跳转到企业身份提供商完成认证」（OIDC Tab 激活时）
- [ ] `tenant_name` 为空时行内校验，不发起 API
- [ ] 已登录访问 `/login` → 重定向 `returnTo` 或 `/`
- [ ] `npm run type-check` 通过
- [ ] 浏览器验证 `/login` Plain UI 与 loading 态

### US-002: Console OIDC Begin 与 IdP 跳转

**Acceptance Criteria:**

- [ ] OIDC Tab 点击「登录」调用 `POST /api/v1/auth/oidc/begin`，body 含 `tenant_name`、`redirect_uri`
- [ ] `redirect_uri` = `${origin}/auth/callback`，与 token 阶段一致
- [ ] 200：保存 `state` 至 sessionStorage；跳转 `authorization_url`
- [ ] 请求中按钮 `loading`，禁止重复提交
- [ ] 错误展示 `ErrorResponse.message`（`Message.error`）；识别 Core 错误码 `TENANT_NOT_FOUND`、`IDP_UNAVAILABLE`
- [ ] 浏览器验证成功跳转与 400 错误提示

### US-003: Console OIDC Callback 换 Token

**Acceptance Criteria:**

- [ ] `/auth/callback` 读取 `code`、`state`；校验与 sessionStorage `state` 一致
- [ ] `POST /api/v1/auth/token`；200 写入会话并 `setAuthToken`
- [ ] 成功：清除 OIDC `state`；重定向 **`returnTo`（若有效）否则 `/`**
- [ ] state 不匹配 / 缺参：错误卡 +「返回登录」链接
- [ ] 浏览器验证 callback 成功与失败分支

### US-004: 会话存储、「记住我」与 Hydrate

**Acceptance Criteria:**

- [ ] 会话模块统一管理 `access_token`、`refresh_token`、`expires_at`、`tenant_name`、`remember_me`
- [ ] **未勾选「记住我」：** 使用 `sessionStorage`
- [ ] **勾选「记住我」：** 使用 `localStorage`（同 schema）
- [ ] 应用启动 `hydrateSession`：未过期 token 自动 `setAuthToken`
- [ ] 登出清除**当前存储介质**下全部 auth 键
- [ ] 不硬编码 mock JWT 或 dev bypass token
- [ ] `npm run type-check` 通过

### US-005: 路由门禁与 returnTo

**Acceptance Criteria:**

- [ ] 白名单：`/login`、`/auth/callback` 无需 token
- [ ] 无 token 访问业务路由 → 保存 `returnTo`（path + search）→ `/login`
- [ ] 登录成功 **优先跳转 `returnTo`**（须为同源相对路径，防 open redirect）；无效则 `/`
- [ ] 已登录访问 `/login` → 重定向 `returnTo` 或 `/`
- [ ] 浏览器验证：未登录访问 `/models` → 登录后回 `/models`

### US-006: Token Refresh

**Acceptance Criteria:**

- [ ] 剩余有效期 < 5 分钟触发 `POST /api/v1/auth/refresh`
- [ ] 200：更新 `access_token`、`expires_at`；`setAuthToken`
- [ ] 401：清会话 + 保存 `returnTo` + `/login` + 提示「登录已过期」
- [ ] refresh 请求不触发 401 中间件死循环
- [ ] `npm run type-check` 通过

### US-007: API 401 统一处理

**Acceptance Criteria:**

- [ ] `coreApi` / `api` 响应 401：先尝试 `refreshAccessToken()`，失败则清会话、保存 `returnTo`、跳转 `/login`
- [ ] `/login`、`/auth/callback`、auth 端点本身除外
- [ ] 可选 `Message.warning`「登录已过期，请重新登录」
- [ ] 无无限重定向

### US-008: Console 登出

**Acceptance Criteria:**

- [ ] 顶栏或设置区「退出登录」
- [ ] 从 access token payload 读 `jti`；`POST /api/v1/auth/logout`
- [ ] 无论 logout API 成败，清本地会话与 middleware
- [ ] 重定向 `/login`
- [ ] 浏览器验证登出后无法访问 `/models`

### US-009: Console 账密登录 Tab（P1）

**Acceptance Criteria:**

- [ ] `/login` 提供 Tab：**企业登录（OIDC）** | **账号密码**
- [ ] 账密 Tab 字段：`tenant_name`、`username`、`password`、记住我；**无** OIDC 跳转说明文案
- [ ] 提交 `POST /api/v1/auth/password/login`（路径以 `v1.yaml` 冻结为准）
- [ ] 200：与 OIDC 相同方式保存 `TokenPair` + `returnTo` 回跳
- [ ] 401/422：展示统一错误（`INVALID_CREDENTIALS`、`TENANT_NOT_FOUND` 等）
- [ ] 账密请求 **必须** 走 TLS；前端不记录密码
- [ ] 浏览器验证账密成功/失败/记住我

### US-010: Console 登录错误与边界态

**Acceptance Criteria:**

- [ ] 登录页、callback 页具备 **loading / error** 态
- [ ] 覆盖：网络失败、租户不存在、IdP 不可用、state 不匹配、callback 缺参、token 交换失败
- [ ] 错误文案中文、B2B 语气；不展示 Dex/Volcano 等内部组件名
- [ ] 浏览器逐场景验证

### US-016: 全链路验收（Console P0）

**Acceptance Criteria:**

- [ ] T-001 未登录 `/models` → `/login` → 登录后回 `/models`
- [ ] T-002 OIDC 完整链路（或 dev profile 等价）换 token 成功
- [ ] T-003 刷新页面会话保持（记住我开/关各测一次）
- [ ] T-004 refresh 与 401 引导回登录
- [ ] T-005 登出后业务页不可访问
- [ ] T-006 Plain UI，无 gamified 资源加载
- [ ] `npm run type-check`、`npm run build` 通过

---

## 4. Functional Requirements

- **FR-1:** Console `/login` 必须为 Plain 企业风，品牌「KuberCloud ANI」
- **FR-2:** Console 必须支持 OIDC 登录链路（begin → IdP → callback → token）
- **FR-3:** Console 必须实现 `returnTo` 优先回跳（同源校验）
- **FR-4:** Console 必须提供「记住我」；控制 `sessionStorage` vs `localStorage`
- **FR-5:** Console 必须对业务路由执行未登录拦截（白名单仅 login/callback）
- **FR-6:** Console 必须实现 refresh、401 处理、登出、启动 hydrate
- **FR-7:** P1 Console 必须提供账密 Tab，消费 Core 账密 API
- **FR-9:** 所有 Auth UI 使用 TDesign（`Form`、`Input`、`Button`、`Checkbox`、`Tabs`、`Message`）
- **FR-10:** 生产构建不得包含 dev 登录入口或硬编码 token

---

## 5. Non-Goals

- 注册、忘记密码、邮箱验证、MFA
- LDAP / 租户 SSO 配置 UI
- 登录页品牌定制从平台配置读取 Logo/背景
- HttpOnly Cookie 会话模式
- gamified 登录动画（Confetti 等）

---

## 6. Design Considerations

### Console `/login` Plain 布局

```text
┌──────────────────────────────┐
│      KuberCloud ANI          │
│  ─────────────────────────   │
│  [ 企业登录 | 账号密码 ]      │  ← P1 起显示 Tab；P0 仅 OIDC 可隐藏 Tab
│  租户标识 *                   │
│  ☐ 记住我                     │
│  [ 登录 ]                     │
│  将跳转到企业身份提供商…       │  ← 仅 OIDC Tab
└──────────────────────────────┘
```

### 路由结构

| 公开路由 | 受保护 |
|----------|--------|
| `/login`, `/auth/callback` | `/_authenticated/*` |

---

## 7. 参考文档

- Core Auth PRD: `repo/services/tasks/modules/prd/core/prd-core-auth.md`
- UX · Console: `repo/services/tasks/modules/ux/console/tenant/ux-console-login-page.md`
- SPEC: `repo/services/tasks/modules/spec/console/tenant/spec-console-login-page.md`
- 主维护文档: `repo/services/docs/console-modules/tenant/login-page.md`
