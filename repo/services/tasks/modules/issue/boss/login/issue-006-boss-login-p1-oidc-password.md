# Issue 006: BOSS 平台管理员登录页 + OIDC/账密/会话链路（P1 前端 + 路由门禁）

## Document Links
- PRD: `repo/services/tasks/modules/prd/console/login/prd-console-login-page.md`
- UX: `repo/services/tasks/modules/prd/boss/login/ux-boss-login-page.md`
- SPEC: `repo/services/tasks/modules/spec/core/login/spec-core-login.md`

## Description

实现 BOSS（平台运营台）登录页 P1 全链路：**Plain 企业风** UI、双 Tab 登录（OIDC + 账密）、`returnTo` 优先回跳、「记住我」勾选控制存储介质、refresh、401、登出、启动 hydrate，**以及 TanStack Router 受保护路由**（`_authenticated` 布局 + `beforeLoad` 门禁）。

**BOSS 工程现状：** `repo/frontends/boss/` 不存在，本 Issue 含工程脚手架（package.json / vite / tsconfig / TanStack Router / TDesign React），与 Console 同栈、同 Token、同 `.auth-page` 样式，但**独立工程**，不直接 import Console 代码。

**与 Console 的关键差异与复用**（UX §4.1、§8.2）：
- 标题：「ANI 平台运营台」（非「KuberCloud ANI」）
- **无** `tenant_name` 字段（平台管理员无租户上下文）
- **会话 storage key 与 Console 隔离**（PRD NG-8 强制；防租户 token 与平台 token 互串）
- OIDC API：**与 Console 复用同一套 Core OIDC 端点**（`/auth/oidc/begin`、`/auth/token`、`/auth/refresh`、`/auth/logout`），不使用单独的 `/auth/platform/oidc/begin`
- 账密 API：走平台专属端点 `/auth/platform/password/login`（与 Console `/auth/password/login` 分离）
- token claims `scope=platform`，与 Console `scope=tenant` 隔离
- 页内可选 `Alert` info「本入口仅供平台管理员。租户用户请使用 Console。」

按 CLAUDE.md「Services 已冻结并移交外部产品团队」执行：本 Issue 仅修改 `repo/frontends/boss/`，**禁止**修改 Gateway、auth-service 或 `v1.yaml`。

## Scope
- Product line: boss
- Code paths allowed: `repo/frontends/boss/` only

## Acceptance Criteria

### A. 工程脚手架（BOSS 前端从零搭建）

- [ ] 新增 `repo/frontends/boss/package.json`：`name: @ani/boss`，scripts `dev`/`build`/`preview`/`type-check`/`lint`/`gen-api`，与 Console 同栈（Vite + React 18 + TanStack Router + TanStack Query + TDesign React + tdesign-icons-react + openapi-fetch + zustand）
- [ ] 新增 `repo/frontends/boss/vite.config.ts`：`@tanstack/router-plugin` 启用，`base: '/boss/'`（或 SPEC 决定的 BOSS 路由前缀；与 Console `/` 区分），dev server 端口与 Console 不同
- [ ] 新增 `repo/frontends/boss/tsconfig.json`、`tsconfig.node.json`：严格模式，与 Console 对齐
- [ ] 新增 `repo/frontends/boss/index.html`：root mount `#root`
- [ ] 新增 `repo/frontends/boss/src/main.tsx`：`createRouter()` + `RouterProvider` + TDesign `MessagePlugin` 全局挂载
- [ ] 新增 `repo/frontends/boss/eslint.config.js`（或 `.eslintrc`）与 Console 对齐
- [ ] `pnpm install` 在 `repo/frontends/boss/` 成功；`pnpm type-check` 通过

### B. API 客户端与会话模块（与 Console 隔离）

- [ ] 新增 `src/api/client.ts`：`openapi-fetch` 创建 `api` 实例，`baseUrl` 指向 Core API `${origin}/api/v1`
- [ ] 新增 `src/api/coreClient.ts`：与 Console 对齐（如需 Services API 再加 `servicesClient`）
- [ ] `pnpm gen-api` 生成 `src/api/schema.d.ts`（消费 `repo/api/openapi/v1.yaml`）+ `src/api/core-schema.d.ts`（如有 Services 需求）
- [ ] 新增 `src/api/auth.ts`：`setAuthToken(token)` 注入 Bearer middleware，与 Console 对齐但**独立模块**
- [ ] 新增 `src/auth/session.ts`：会话存储模块，**storage key 前缀 `boss:`**（如 `boss:access_token` / `boss:refresh_token` / `boss:expires_at` / `boss:remember_me` / `boss:oidc_state`），**禁止**与 Console 共用 `console:` 或无前缀 key
- [ ] `session.ts` 支持按 `remember_me` 切换 `sessionStorage` / `localStorage`（同 schema）
- [ ] `session.ts` 提供 `hydrateSession()` / `saveSession(pair, remember)` / `clearSession()` / `getSession()`
- [ ] `clearSession()` 同时清理 `sessionStorage` 与 `localStorage` 中 `boss:` 前缀的 auth 键（切换介质后旧介质残留清理）

### C. 路由树 + 受保护路由（TanStack Router `_authenticated`）

- [ ] 新增 `src/routes/__root.tsx`：`createRootRoute`，仅渲染 `<Outlet />`，**不**含 Header/Aside
- [ ] 新增 `src/routes/login.tsx`：`/login` 公开路由，Plain 登录页（无壳层）
- [ ] 新增 `src/routes/auth/callback.tsx`：`/auth/callback` 公开路由，处理 OIDC 回调（与 Console 复用同一 Core `/auth/token` 端点；state key 用 `boss:oidc_state` 与 Console 隔离防冲突）
- [ ] 新增 `src/routes/_authenticated.tsx`：`createFileRoute('/_authenticated')` 受保护布局，渲染 BOSS `Header`/`Aside`/`Outlet`
- [ ] `_authenticated.tsx beforeLoad`：从 storage 读 `boss:access_token` + `boss:expires_at`；无 token 或已过期 → `throw redirect({ to: '/login', search: { returnTo: 当前 path + search } })`；有效 → `setAuthToken(token)`
- [ ] 新增 `src/routes/_authenticated/index.tsx`：BOSS 首页（`/`），默认占位（如「运营总览」），SPEC 冻结路径前可占位
- [ ] `src/routes/routeTree.gen.ts` 由 `pnpm dev`/`pnpm build` 自动生成，不手改
- [ ] `/login` `beforeLoad`：已登录（有效平台 token）→ `throw redirect({ to: returnTo || '/' })`
- [ ] `returnTo` 校验：同源相对路径（以 `/` 开头，禁止 `//`、`http:`、`//evil.com`），一次性消费

### D. US-014 BOSS 登录页 Plain UI（双 Tab：OIDC + 账密）

- [ ] `/login` Plain 布局：`.auth-page` 全屏居中、`Card.auth-card` max-width 400px、标题「ANI 平台运营台」（UX §7.1）
- [ ] 页内 `Alert theme="info" closable`：「本入口仅供平台管理员。租户用户请使用 Console 登录。」（UX §4.1、§7.1 可选）
- [ ] `Tabs` 两项：「企业登录」（OIDC）|「账号密码」（账密）；两个 Tab 均可交互
- [ ] 「企业登录」Tab 字段：无 `tenant_name`（平台管理员无租户上下文），仅 `Checkbox`「记住我」+ 主按钮「登录」`theme="primary" block loading`
- [ ] 「企业登录」Tab 说明文案：「将跳转到企业身份提供商完成认证」（与 Console 同文案）
- [ ] 「账号密码」Tab 字段：`username`（必填，`maxlength=64` `clearable`）、`password`（`type="password"` 必填）、`Checkbox`「记住我」
- [ ] **无** `tenant_name` 字段（UX §4.1，两 Tab 均无）
- [ ] 主按钮「登录」`theme="primary" block loading`
- [ ] `username` / `password` 为空时行内校验，不发起 API
- [ ] Tab 切换时「记住我」状态保持（UX §4.1）
- [ ] 不展示 Dex/Volcano/OIDC 协议名作为主文案

### E. US-014 OIDC Tab 流程（与 Console 同 API，storage 隔离）

- [ ] 「企业登录」Tab 点击「登录」调用 `POST /api/v1/auth/oidc/begin`（与 Console 同一端点），body：`redirect_uri=${origin}/auth/callback`，**无** `tenant_name` 字段（平台管理员无租户上下文）
- [ ] 200：保存 `state` 至 sessionStorage（key `boss:oidc_state`，与 Console 隔离防冲突）；`window.location.assign(authorization_url)`
- [ ] 请求中按钮 `loading`，Input disabled，禁止重复提交
- [ ] `/auth/callback` 读取 `code`、`state`；校验与 sessionStorage `boss:oidc_state` 一致
- [ ] `POST /api/v1/auth/token`（与 Console 同一端点）；200 写入会话（`boss:` 前缀）并 `setAuthToken`
- [ ] 成功：清除 `boss:oidc_state`；重定向 `returnTo`（同源相对路径）或 `/`
- [ ] state 不匹配：错误卡「登录状态无效，请重新登录」+「返回登录」按钮
- [ ] callback 缺参：错误卡「登录回调参数不完整」+「返回登录」按钮
- [ ] token 交换失败：错误卡「登录验证失败，请重新登录」
- [ ] 全屏 `Loading`「正在完成登录...」进行中态
- [ ] 错误展示 `Message.error`（`ErrorResponse.message`）；识别 Core 错误码 `IDP_UNAVAILABLE`「身份服务暂不可用，请稍后重试」（UX §7.2）
- [ ] 网络失败：`Message.error`「网络异常，请稍后重试」

### F. US-014 账密登录提交

- [ ] 账密 Tab 提交 `POST /api/v1/auth/platform/password/login`，body：`username`、`password`，可选 `idempotency_key`
- [ ] 请求中按钮 `loading`、全表单 disabled，禁止重复提交
- [ ] 200：保存 `TokenPair`（存储介质由「记住我」决定，`boss:` 前缀 key）→ 跳转 `returnTo` 或 `/`
- [ ] 账密请求**必须**走 TLS（生产 https）；前端不记录密码到 storage / log
- [ ] 失败后清空 `password` 字段，保留 `username`
- [ ] Network 面板：密码字段不出现在请求 URL query（仅 body）

### G. 错误码映射（UX §7.2）

- [ ] `INVALID_CREDENTIALS` → `Message.error`「用户名或密码错误」；清空密码框
- [ ] 网络失败 → `Message.error`「网络异常，请稍后重试」
- [ ] 其他 4xx/5xx → `Message.error` API `message` 或默认文案「登录失败，请稍后重试」
- [ ] 不区分用户是否存在（防枚举，SPEC §5.1）

### H. 会话链路（与 Console 模式一致，storage 隔离）

- [ ] **启动 hydrate**：`main.tsx` 调 `hydrateSession()`，未过期 `boss:access_token` 自动 `setAuthToken`
- [ ] **Token Refresh**：剩余有效期 < 5 分钟触发 `POST /api/v1/auth/refresh`（复用 Core refresh 端点，body 用 `boss:refresh_token`）；200 更新 `boss:access_token` / `boss:expires_at`；401 清会话 + `returnTo` + `/login` + `Message.warning`「登录已过期，请重新登录」
- [ ] **API 401 统一处理**：`api` / `coreApi` 401 → 清 `boss:` 会话、保存 `returnTo`、跳转 `/login`（登录端点本身除外）；无无限重定向
- [ ] **401 中间件不拦截** `/auth/oidc/begin`、`/auth/token`、`/auth/platform/password/login`、`/auth/refresh` 等登录端点
- [ ] **登出**：BOSS Header 右侧「退出登录」`Button variant="outline"`；从 access token payload 读 `jti`；`POST /api/v1/auth/logout`；无论成败清 `boss:` 会话与 middleware；重定向 `/login`

### I. UI 状态机（UX §6.1）

- [ ] idle / validating / loading / error-credentials / error-network / error-generic / already_authed 7 态覆盖
- [ ] `already_authed`：已登录访问 `/login` → 直接重定向 `returnTo` 或 `/`
- [ ] callback 缺参 / state 不匹配 / token 失败 → 错误卡（UX §6.2 与 Console 同文案）

### J. 全链路验收

- [ ] T-001 未登录访问 BOSS `/` → `/login?returnTo=%2F` → 登录后回 `/`
- [ ] T-002 平台账密登录成功 → `returnTo` 或 `/`
- [ ] T-003 错误凭证 → `Message.error` + 密码框清空 + 保留 `username`
- [ ] T-004 记住我开/关 → `localStorage` / `sessionStorage` 切换（`boss:` 前缀）
- [ ] T-005 刷新页面会话保持（记住我开/关各测一次）
- [ ] T-006 refresh 与 401 引导回登录（无无限重定向）
- [ ] T-007 登出后 BOSS 业务页不可访问（`beforeLoad` 重新拦截）
- [ ] T-008 Plain UI，无 gamified 资源加载（Network 面板校验）
- [ ] T-009 未登录访问 `/login` 自身不重定向（公开路由）
- [ ] T-010 已登录访问 `/login` → 重定向 `returnTo` 或 `/`
- [ ] T-011 受保护路由与公开路由壳层隔离：`/login` 无 Header/Aside，`/_authenticated/*` 有
- [ ] T-012 **会话隔离**：同一浏览器同时打开 Console 与 BOSS 标签页，Console `console:access_token` 与 BOSS `boss:access_token` 互不污染（Network 面板 + Application Storage 校验）
- [ ] T-013 **OIDC state 隔离**：同一浏览器 Console 与 BOSS 同时发起 OIDC 登录，`console:oidc_state` 与 `boss:oidc_state` 互不冲突；任一端 callback 不误消费另一端 state
- [ ] T-014 平台 token 不能访问 Console 业务页（Console `_authenticated beforeLoad` 校验 `scope=tenant`；若 Console 尚未做 scope 校验，记录为 Issue #004 后续补丁）
- [ ] T-015 BOSS OIDC Tab 登录全链路（begin → IdP → callback → token → 跳转）
- [ ] `pnpm type-check`、`pnpm lint`、`pnpm test`、`pnpm build` 通过

## Dependencies
- Issue #003（Core 平台账密 API `POST /auth/platform/password/login`，PR 未合并 — 本 Issue 可先做 UI/路由/会话骨架，但端到端验收需等 #003 合并）
- 参考 Issue #004（Console 登录页 P0 模式：会话模块、`_authenticated` 布局、`returnTo`、401 处理、OIDC 全链路）— **复用模式但不复用代码**
- Core OIDC 四端点（`/auth/oidc/begin`、`/auth/token`、`/auth/refresh`、`/auth/logout`，v1.yaml 已声明）— 与 Console 共用

## Type
frontend

## Priority
medium

## Labels
boss

## Batch
LOGIN-FE-BOSS-P1

## SPEC Reference
- SPEC §4.1：`POST /auth/platform/password/login` request/response 契约（`username` + `password`，无 `tenant_name`）
- SPEC §6.1：错误码 `INVALID_CREDENTIALS`（不区分用户存在性防枚举）
- SPEC §7.1：平台 token claims `scope=platform`、`tenant_id=null`、`roles=["platform-admin"]`
- Core OIDC 端点：`/auth/oidc/begin`、`/auth/token`、`/auth/refresh`、`/auth/logout`（v1.yaml 已声明，与 Console 共用）

## UX Reference
- UX §1.1：BOSS `/login` auth standalone，P1
- UX §2.2：与 Console 会话 storage key 隔离
- UX §3.2：账密流程（无租户字段）
- UX §4.1：Plain 布局 + 「ANI 平台运营台」标题 + Alert info + 无 `tenant_name`
- UX §5：组件映射（Tabs/Form/Input password/Checkbox/Button/Alert/Message）
- UX §6.1：`/login` 状态机
- UX §6.2：`/auth/callback` 状态机
- UX §6.3：全局 401 / 登出
- UX §7.1：文案（ANI 平台运营台、企业登录、账号密码、记住我、登录、跳转中…、退出登录、Alert 文案）
- UX §7.2：错误消息映射
- UX §8.2：会话 storage key 与 Console 隔离

## 验收命令

```bash
cd repo/frontends/boss
pnpm install
pnpm gen-api
pnpm type-check
pnpm lint
pnpm test
pnpm build
```

UI 验收（浏览器）：
- 未登录访问 BOSS `/` → `/login?returnTo=%2F` → 登录后回 `/`
- 平台账密登录成功 / 失败 / 记住我切换
- BOSS OIDC 登录全链路（begin → IdP → callback → token → 跳转）
- 刷新会话保持、401 回登录、登出
- Network 面板：无 Confetti / gamified 资源
- 同浏览器 Console + BOSS 标签页共存：storage key `console:` 与 `boss:` 独立；OIDC state `console:oidc_state` 与 `boss:oidc_state` 独立
- 密码字段不在请求 URL query

## Non-Goals

- BOSS 业务路由（`/ops/*` 等运营页，独立 Issue）
- Dev Profile 折叠区入口（DEV-only，后续 Issue）
- 注册、忘记密码、MFA、LDAP/SSO 配置
- 登录页 Logo/背景从平台配置读取（P2）
- 修改 Gateway、auth-service、`v1.yaml`
- 与 Console 共享前端代码（BOSS 是独立工程，复用模式不复用代码）
- Console 侧 `scope` 校验（如 Console `_authenticated` 不校验 `scope=tenant`，记录为 Issue #004 后续补丁，本 Issue 不修 Console）

## Open Questions

- **BOSS 路由前缀**：`/` vs `/boss/`（vite `base` 与 dev server 路径）— SPEC 冻结前默认 `/boss/`（与 Console 同 origin 不同前缀），生产由 Gateway 路由
- **BOSS 默认首页路径**：`/` vs `/ops/overview`（UX §8.3 Open UX Question）— SPEC 冻结前默认 `/`，BOSS `index.tsx` 占位「运营总览」
- **OIDC `tenant_name` 缺省语义**：Core `/auth/oidc/begin` 当前 body 含 `tenant_name`，BOSS 平台管理员无租户上下文 — 需 Core 侧确认 `tenant_name` 缺省（或不传）时走平台 realm 的行为；本 Issue 前端按"不传 `tenant_name`"实现，若 Core 不支持需补 Core Issue
- **OIDC `redirect_uri` 与 state 隔离**：BOSS 与 Console 共用 `/auth/callback` 路径 — 通过 storage key 前缀（`boss:oidc_state` vs `console:oidc_state`）隔离 state，避免同浏览器两端同时登录时 state 互串；若 Core 端要求 `redirect_uri` 区分平台/租户，则 BOSS 用 `${origin}/auth/callback?scope=platform`（待 Core 确认）
