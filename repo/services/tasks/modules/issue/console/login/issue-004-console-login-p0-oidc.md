# Issue 004: Console 登录页 Plain UI + OIDC 全链路 + 会话/returnTo/记住我（P0）

## Document Links
- PRD: `repo/services/tasks/modules/prd/console/login/prd-console-login-page.md`
- UX: `repo/services/tasks/modules/prd/console/login/ux-console-login-page.md`
- SPEC: `repo/services/tasks/modules/spec/core/login/spec-core-login.md`

## Description

实现 Console 登录页 P0 全链路：**Plain 企业风** UI（移除 gamified/Confetti）、OIDC begin/callback、`returnTo` 优先回跳、「记住我」勾选控制存储介质、refresh、401、登出、启动 hydrate，**以及 TanStack Router 受保护路由重构**（将业务路由从 `__root.tsx` 壳层剥离到 `_authenticated/` 布局路由下，`beforeLoad` 强制门禁）。

本 Issue 仅依赖现有 Core OIDC 四端点（`/auth/oidc/begin`、`/auth/token`、`/auth/refresh`、`/auth/logout`），**不依赖** Issue #2/#3 的账密 API（属 P1，Issue #005 处理）。

按 CLAUDE.md「Services 已冻结并移交外部产品团队」执行：本 Issue 仅修改 `repo/frontends/console/`，**禁止**修改 Gateway、auth-service 或 `v1.yaml`。

**路由现状（必须重构）：** 当前 `repo/frontends/console/src/routes/__root.tsx` 将 `Header`/`Aside`/`Outlet` 直接挂在 root 路由上，业务路由（`/`、`/models`、`/kb`、`/usage`、`/settings`、`/registry`）与 `/login` 共享同一壳层，未登录可直接访问任何业务页 — 必须在本 Issue 中通过 TanStack Router 的 `_authenticated` pathless 布局路由 + `beforeLoad` 守卫修复。

## Scope
- Product line: console
- Code paths allowed: `repo/frontends/console/` only

## Acceptance Criteria

### US-001 Plain 登录卡 UI
- [ ] `/login` 使用 Plain 布局：`.auth-page` 全屏居中、`Card.auth-card` max-width 400px、`KuberCloud ANI` 标题（20px semibold，非「欢迎回来」）
- [ ] 表单字段：`tenant_name`（必填，`maxlength=128` `clearable`）、`Checkbox`「记住我」、主按钮「登录」`theme="primary" block loading`
- [ ] OIDC 说明文案：「将跳转到企业身份提供商完成认证」（UX §7.1）
- [ ] `tenant_name` 为空时行内校验「请输入租户标识」，不发起 API
- [ ] 已登录访问 `/login` → 重定向 `returnTo` 或 `/`
- [ ] 移除 `LoginConfetti`、`login-gamified.css`（UX §4.1 P0 差异）
- [ ] `pnpm type-check` 通过

### US-002 OIDC Begin 与 IdP 跳转
- [ ] 点击「登录」调用 `POST /api/v1/auth/oidc/begin`，body 含 `tenant_name`、`redirect_uri=${origin}/auth/callback`
- [ ] 200：保存 `state` 至 sessionStorage；`window.location.assign(authorization_url)`
- [ ] 请求中按钮 `loading`，Input disabled，禁止重复提交
- [ ] 错误展示 `Message.error`（`ErrorResponse.message`）；识别 Core 错误码 `TENANT_NOT_FOUND`「租户不存在，请检查租户标识」、`IDP_UNAVAILABLE`「身份服务暂不可用，请稍后重试」（UX §7.2）
- [ ] 网络失败：`Message.error`「网络异常，请稍后重试」

### US-003 OIDC Callback 换 Token
- [ ] `/auth/callback` 读取 `code`、`state`；校验与 sessionStorage `state` 一致
- [ ] `POST /api/v1/auth/token`；200 写入会话并 `setAuthToken`
- [ ] 成功：清除 OIDC `state`；重定向 `returnTo`（同源相对路径）或 `/`
- [ ] state 不匹配：错误卡「登录状态无效，请重新登录」+「返回登录」按钮
- [ ] callback 缺参：错误卡「登录回调参数不完整」+「返回登录」按钮
- [ ] token 交换失败：错误卡「登录验证失败，请重新登录」
- [ ] 全屏 `Loading`「正在完成登录...」进行中态

### US-004 会话存储、「记住我」与 Hydrate
- [ ] `auth/session` 模块统一管理 `access_token`、`refresh_token`、`expires_at`、`tenant_name`、`remember_me`
- [ ] **未勾选「记住我」：** 使用 `sessionStorage`
- [ ] **勾选「记住我」：** 使用 `localStorage`（同 schema）
- [ ] 应用启动 `hydrateSession`：未过期 token 自动 `setAuthToken`
- [ ] 登出清除**当前存储介质**下全部 auth 键；切换记住我后旧介质残留须清理
- [ ] 不硬编码 mock JWT 或 dev bypass token（dev 入口由 Issue #006 处理）
- [ ] `pnpm type-check` 通过

### US-005 路由门禁与 returnTo（TanStack Router `beforeLoad` + `_authenticated` 布局）

**当前状态：** `repo/frontends/console/src/routes/__root.tsx` 将 `Header`/`Aside`/`Outlet` 直接挂在 root，**未做受保护路由隔离**；业务路由（`/`、`/models`、`/kb`、`/usage`、`/settings`、`/registry`）与登录页共享同一布局壳层，未登录可直接访问任何业务页。本 Issue 必须重构路由树为「公开壳」与「受保护壳」两层。

- [ ] 新增 `routes/_authenticated.tsx`：TanStack Router `createFileRoute('/_authenticated')` 作为受保护布局，渲染 `Header`/`Aside`/`Outlet`（即现 `__root.tsx` 的壳层）；`__root.tsx` 改为只渲染 `<Outlet />`，不再含 Header/Aside
- [ ] `_authenticated.tsx` `beforeLoad`：从 sessionStorage/localStorage 读 token；无 token 或已过期 → `throw redirect({ to: '/login', search: { returnTo: 当前 path + search } })`；有 token → `setAuthToken(token)` 注入到 API middleware
- [ ] 业务路由文件全部从 `routes/xxx.tsx` 迁移到 `routes/_authenticated/xxx.tsx`（含 `index.tsx` → `_authenticated/index.tsx`，`models/`、`kb/`、`usage.tsx`、`settings/`、`registry.tsx` 全部下移一级）；`__root.tsx` 不再直接挂业务页
- [ ] `/login`、`/auth/callback` 保留在 `routes/` 根下（公开路由），**不**在 `_authenticated/` 目录；它们使用独立的无壳布局（`.auth-page` 全屏居中）
- [ ] 白名单：`/login`、`/auth/callback` 的 `beforeLoad` 不检查 token；已登录访问 `/login` → `throw redirect({ to: returnTo || '/' })`（`returnTo` 须为同源相对路径，防 open redirect：以 `/` 开头，禁止 `//`、`http:`、`//evil.com` 等外链）
- [ ] 无 token 访问受保护路由 → `beforeLoad` 保存 `returnTo`（path + search）→ 跳转 `/login?returnTo=...`
- [ ] 登录成功优先跳转 `returnTo`（须为同源相对路径）；无效或缺失则 `/`
- [ ] `returnTo` 一次性消费：跳转后从 URL query 与 sessionStorage 中清除
- [ ] `routeTree.gen.ts` 通过 `pnpm dev` / `pnpm build` 自动重新生成，不手改
- [ ] `pnpm type-check` 通过（`_authenticated` 类型正确）
- [ ] 浏览器验证：未登录访问 `/models` → 跳转 `/login?returnTo=%2Fmodels` → 登录后回 `/models`
- [ ] 浏览器验证：已登录访问 `/login` → 跳转 `returnTo` 或 `/`
- [ ] 浏览器验证：`/login`、`/auth/callback` 不渲染 Header/Aside（无壳层）

### US-006 Token Refresh
- [ ] 剩余有效期 < 5 分钟触发 `POST /api/v1/auth/refresh`
- [ ] 200：更新 `access_token`、`expires_at`；`setAuthToken`
- [ ] 401：清会话 + 保存 `returnTo` + `/login` + `Message.warning`「登录已过期，请重新登录」
- [ ] refresh 请求本身不触发 401 中间件死循环
- [ ] `pnpm type-check` 通过

### US-007 API 401 统一处理
- [ ] `coreApi` / `api` 响应 401：清会话、保存 `returnTo`、跳转 `/login`（`/login`、`/auth/callback`、auth 端点本身除外）
- [ ] 可选 `Message.warning`「登录已过期，请重新登录」
- [ ] 无无限重定向
- [ ] 401 中间件不拦截 `/auth/oidc/begin`、`/auth/token`、`/auth/refresh`、`/auth/password/login` 等登录端点（避免登录失败被误判为 401）

### US-008 Console 登出
- [ ] 顶栏或设置区「退出登录」`Button variant="outline"`
- [ ] 从 access token payload 读 `jti`；`POST /api/v1/auth/logout`
- [ ] 无论 logout API 成败，清本地会话与 middleware
- [ ] 重定向 `/login`

### US-010 登录错误与边界态
- [ ] 登录页、callback 页具备 loading / error 态（empty 不适用）
- [ ] 覆盖：网络失败、租户不存在、IdP 不可用、state 不匹配、callback 缺参、token 交换失败
- [ ] 错误文案中文、B2B 语气；不展示 Dex/Volcano/OIDC 协议名作为主文案
- [ ] 浏览器逐场景验证（可配合 mock 或 dev profile）

### US-016 全链路验收（P0）
- [ ] T-001 未登录 `/models` → `/login?returnTo=%2Fmodels` → 登录后回 `/models`
- [ ] T-002 OIDC 完整链路换 token 成功
- [ ] T-003 刷新页面会话保持（记住我开/关各测一次）
- [ ] T-004 refresh 与 401 引导回登录（无无限重定向）
- [ ] T-005 登出后业务页不可访问（`beforeLoad` 重新拦截）
- [ ] T-006 Plain UI，无 gamified 资源加载（Network 面板校验）
- [ ] T-007 未登录访问 `/login` 自身不重定向（公开路由）
- [ ] T-008 未登录访问 `/auth/callback` 不被门禁拦截（公开路由）
- [ ] T-009 已登录访问 `/login` → 重定向 `returnTo` 或 `/`
- [ ] T-010 受保护路由与公开路由壳层隔离：`/login` 无 Header/Aside，`/_authenticated/*` 有
- [ ] `pnpm type-check`、`pnpm lint`、`pnpm build` 通过

## Dependencies
- 无（P0 仅依赖现有 Core OIDC 四端点，已合并）

## Type
frontend

## Priority
high

## Labels
console

## Batch
LOGIN-FE-P0

## SPEC Reference
- SPEC §1.1：本仓库不实现前端，外部团队按 OpenAPI 契约消费
- Core OIDC 端点：`/auth/oidc/begin`、`/auth/token`、`/auth/refresh`、`/auth/logout`（v1.yaml 已声明）

## UX Reference
- UX §1.1：`/login` auth standalone，`/auth/callback` transient
- UX §4.1：Plain 登录卡布局（移除 gamified）
- UX §5.1：组件映射（Form/Input/Button/Checkbox/Message）
- UX §6.1：`/login` 状态机（idle/validating/loading/redirecting/error-*/already_authed）
- UX §6.2：`/auth/callback` 状态机
- UX §6.3：全局 401 / 登出
- UX §7.1：文案（KuberCloud ANI、记住我、登录、跳转中…、返回登录、退出登录）
- UX §7.2：错误消息映射（TENANT_NOT_FOUND、IDP_UNAVAILABLE、OIDC_EXCHANGE_FAILED、网络失败）

## 验收命令

```bash
cd repo/frontends/console
pnpm type-check
pnpm lint
pnpm test
pnpm build
```

UI 验收（浏览器）：
- 未登录访问 `/models` → 登录后回 `/models`
- OIDC 完整链路（或 dev profile 等价）
- 记住我开/关 → sessionStorage/localStorage 切换
- 401 / refresh 失败引导回登录
- 登出后业务页不可访问
- Network 面板：无 Confetti / gamified 资源请求

## Non-Goals

- 账密 Tab 与 `/auth/password/login` 消费（P1，Issue #005）
- BOSS 登录页（P1，Issue #006）
- Dev Profile 折叠区入口（P0 仅 `import.meta.env.DEV` 占位，完整 dev token 入口由 Issue #006 或后续）
- 注册、忘记密码、MFA、LDAP/SSO 配置 UI
- 登录页 Logo/背景从平台配置读取（P2）
- 修改 Gateway、auth-service、`v1.yaml`
