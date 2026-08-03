# UX: 登录与身份认证（Console）

> Interaction specification derived from: [`prd-console-login-page.md`](../../prd/console/tenant/prd-console-login-page.md) v2.0  
> Part of ani-workflow artifact triad — next: `/prd-to-spec`  
> Generated: 2026-07-03 | Product: **Console** | UI stack: **TDesign React + TanStack Router**  
> Module main doc: [`login-page.md`](../../../../docs/console-modules/tenant/login-page.md)

**范围：** Console 租户侧登录 UI 与全局会话反馈；不含 IdP 站外页面、不含 Core handler 实现细节。

---

## 1. Page Type

### 1.1 Classification

| Screen | Page type | In app shell? | Route | 阶段 |
|--------|-----------|---------------|-------|------|
| 登录页 | auth（standalone） | 否 | `/login` | P0 |
| OIDC 回调 | auth（transient） | 否 | `/auth/callback` | P0 |
| Console 主应用 | app shell + content | 是 | `/_authenticated/*` | P0 |
| 开发登录（折叠区） | auth fragment | 否 | `/login` 页内 | P0（仅 DEV 构建） |
| 账密 Tab | auth form | 否 | `/login` Tab | P1 |

### 1.2 Pattern Reference

| 页面 | 参考 |
|------|------|
| `/login` | 页面模板 §2 无壳层；**Plain** `.auth-page` + `.auth-card`（~400px）；**禁止** gamified / Confetti |
| `/auth/callback` | 全屏居中 `Loading` → 失败 `Card` 错误卡 |
| 已登录壳 | 现有 `ConsoleTopBar` + `Layout` Aside/Content（`_authenticated.tsx`） |
| 对标 | 企业 B2B 登录卡；非营销落地页 |

---

## 2. Information Architecture

### 2.1 Routes & Entry Points

| Route | Entry | Auth required |
|-------|-------|---------------|
| `/login` | 未登录访问业务页重定向；直接 URL | 否（已登录 → `returnTo` 或 `/`） |
| `/auth/callback` | IdP 授权后浏览器重定向 | 否 |
| `/_authenticated/*` | 侧栏、书签、`returnTo` 回跳 | 是 |

**公开白名单：** `/login`、`/auth/callback`。

### 2.2 Navigation Relationship

```text
[未登录]
  业务 URL → 保存 returnTo → /login
  /login（OIDC）→ IdP（站外）→ /auth/callback → returnTo 或 /
  /login（账密 P1）→ 同页提交 → returnTo 或 /

[已登录]
  /login → returnTo 或 /
  Header「退出登录」→ /login
```

- `/login`、`/auth/callback`：**无**侧栏、无 Console 顶栏壳
- 品牌「KuberCloud ANI」：登录页标题 + 已登录 `ConsoleTopBar` 左侧

### 2.3 PRD Coverage Map

| PRD item | Screen / section |
|----------|------------------|
| US-001 | §4.1 Plain 登录卡 |
| US-002 | §3.1 OIDC begin + 跳转 |
| US-003 | §4.2 callback |
| US-004 | §5.4 记住我 + §6 会话态 |
| US-005 | §3.2 returnTo + §6.3 门禁 |
| US-006～007 | §6.3 全局（无独立页） |
| US-008 | §4.3 Header 登出 |
| US-009 | §4.1 账密 Tab（P1） |
| US-010 | §6 错误态全集 |
| US-013 | §4.4 开发登录折叠区 |
| US-016 | §6 + §7 验收口径 |

---

## 3. User Flow

### 3.1 Primary Flow — OIDC（P0）

```text
1. 用户打开 /login（或从 /models 被拦下，returnTo 已写入）
2. 输入 tenant_name；可选勾选「记住我」
3. 点击「登录」
   → 表单校验
   → POST /api/v1/auth/oidc/begin
   → 保存 state（sessionStorage，与 OIDC 流程绑定）
   → 按钮 loading → window.location 跳转 authorization_url（无动画庆祝）
4. 用户在 IdP 输入用户名/密码（Console 不展示）
5. IdP → /auth/callback?code=&state=
6. 全屏 Loading「正在完成登录...」
   → 校验 state → POST /api/v1/auth/token
   → 按「记住我」写入 sessionStorage 或 localStorage
   → Message.success → 跳转 returnTo（有效同源 path）否则 /
7. 进入 _authenticated 壳
```

### 3.2 Primary Flow — 账密（P1）

```text
1. /login 切换 Tab「账号密码」
2. 填写 tenant_name、username、password；可选「记住我」
3. 点击「登录」→ POST 账密 API（路径 SPEC 冻结）
4. 200：保存 TokenPair（存储介质同记住我）→ returnTo 或 /
5. 失败：Message.error + 保留表单（密码框清空）
```

### 3.3 Secondary Flows

| 流程 | 行为 |
|------|------|
| 已登录访问 `/login` | 路由重定向 `returnTo` 或 `/`，无表单 |
| begin 失败 | 停留 `/login`；`Message.error`；按 `code` 展示友好文案（§7.2） |
| callback state 不一致 | 错误卡 +「返回登录」 |
| 401 / refresh 失败 | `Message.warning`「登录已过期…」→ 保存 returnTo → `/login` |
| 登出 | Header「退出登录」→ 清双存储介质残留 → `/login` |
| 开发登录（DEV） | 展开折叠区 → 一键或简表提交 dev token API → 同 success 跳转 |

### 3.4 Flow Diagram

```mermaid
flowchart TD
  A[业务页] --> B{已登录?}
  B -->|否| C[/login]
  B -->|是| D[业务页]
  C --> E{Tab}
  E -->|企业登录 P0| F[OIDC begin]
  E -->|账号密码 P1| G[password login]
  F --> H[IdP 站外]
  H --> I[/auth/callback]
  G --> J{returnTo 或 /}
  I --> J
  D --> K[401 / 登出]
  K --> C
```

---

## 4. Layout Regions

### 4.1 `/login` — Plain（P0 规范态）

```text
┌─────────────────────────────────────────────┐
│        .auth-page 全屏居中 bg-page           │
│   ┌─────────────────────────────────┐       │
│   │  Card.auth-card  max-width 400px │       │
│   │  ─────────────────────────────  │       │
│   │  [Title] KuberCloud ANI         │       │
│   │  [Tabs] 企业登录 | 账号密码      │  P1   │
│   │  [Form] 租户标识 *               │       │
│   │  [Form] 用户名/密码 *            │  P1 账密 Tab only │
│   │  ☐ 记住我                       │       │
│   │  [Button] 登录 primary block    │       │
│   │  [Desc] IdP 说明一行 secondary   │  OIDC Tab only │
│   │  ─────────────────────────────  │       │
│   │  [Collapse] 开发登录（DEV only）  │       │
│   └─────────────────────────────────┘       │
└─────────────────────────────────────────────┘
```

| Region | Content | Notes |
|--------|---------|-------|
| 背景 | `--td-bg-color-page` | 无全屏营销图 |
| 标题 | `KuberCloud ANI` | `20px` semibold；**不用**「欢迎回来」 |
| Tabs | `Tabs` 两项 | **P0 隐藏**整栏，仅 OIDC 表单 |
| OIDC 说明 | secondary 14px | 「将跳转到企业身份提供商完成认证」 |
| 记住我 | `Checkbox` | Tab 切换时**保持勾选状态** |
| 主按钮 | 「登录」block primary | loading 时 disabled 全表单 |
| 开发区 | `Collapse` | 标题「开发登录」；生产构建**不渲染** |

**P0 与当前代码差异：** 移除 `LoginConfetti`、`login-gamified.css`；`beforeLoad` 已登录时跳转须消费 `returnTo`（非写死 `/`）。

### 4.2 `/auth/callback`

| State | Layout |
|-------|--------|
| loading | `.auth-page` + `Loading` text「正在完成登录...」 |
| error | `.auth-page` + `.auth-card`：标题「登录未完成」+ 正文 + block「返回登录」 |

### 4.3 `_authenticated` Header

| Region | Content |
|--------|---------|
| 左 | 「KuberCloud ANI」 |
| 右 | `Button variant="outline"`「退出登录」 |
| 下 | Aside Menu + Content Outlet |

### 4.4 开发登录折叠区（P0，DEV only）

```text
┌─ Collapse panel ─────────────────┐
│ 开发登录（仅本地联调）              │
│ [Button] 使用开发账号登录          │  或 tenant + 开发口令（SPEC 定）
│ 说明：生产环境不提供此入口          │
└──────────────────────────────────┘
```

---

## 5. Component Mapping

### 5.1 `/login` — OIDC（P0）

| UI element | TDesign | Props / variant | Data |
|------------|---------|-----------------|------|
| 页面容器 | `div.auth-page` | flex 居中；padding 24px | — |
| 卡片 | `Card` | `bordered`；class `auth-card`；max-width 400px | — |
| 标题 | `h1` | class `auth-card-title` | 静态文案 |
| 表单 | `Form` | `labelAlign="top"` `colon={false}` | `Form.useForm()` |
| 租户标识 | `FormItem` + `Input` | `name="tenant_name"` required；`maxlength={128}` `clearable` | OpenAPI `tenant_name` |
| 记住我 | `Checkbox` | `name="remember_me"` | 用户勾选 |
| 登录 | `Button` | `theme="primary"` `block` `loading` | 触发 OIDC begin |
| OIDC 说明 | `p` | class `auth-card-desc` | 仅 OIDC 视图 |
| API 错误 | `MessagePlugin.error` | toast | `ErrorResponse.message` |

### 5.2 `/login` — 账密 Tab（P1）

| UI element | TDesign | Props / variant | Data |
|------------|---------|-----------------|------|
| Tab 栏 | `Tabs` | `defaultValue="oidc"` | 企业登录 / 账号密码 |
| 用户名 | `FormItem` + `Input` | `name="username"`；账密 Tab 显示 | PRD 字段 |
| 密码 | `FormItem` + `Input` | `type="password"` `name="password"` | 不持久化 |
| 登录 | 同 5.1 | 账密 Tab 提交账密 API | — |

### 5.3 `/login` — 开发折叠（P0 DEV）

| UI element | TDesign | Notes |
|------------|---------|-------|
| 折叠 | `Collapse` / `Collapse.Panel` | 默认收起 |
| 开发按钮 | `Button` | `variant="outline"` 或 `theme="default"` |
| 提示 | `Alert` `theme="warning"` | panel 内一行 |

### 5.4 `/auth/callback`

| UI element | TDesign | Props / variant |
|------------|---------|-----------------|
| 加载 | `Loading` | `loading` `text="正在完成登录..."` |
| 错误卡 | `Card` + `Button` | block「返回登录」→ `/login` |
| 成功 | `MessagePlugin.success` | 「登录成功」后硬跳转 |

### 5.5 Header 登出

| UI element | TDesign | Props / variant |
|------------|---------|-----------------|
| 退出 | `Button` | `variant="outline"` class `console-header-logout` |

### 5.6 无独立页面行为（用户可感知结果）

| 行为 | 用户感知 |
|------|----------|
| 记住我未勾选 | 关标签后需重新登录 |
| 记住我勾选 | 关浏览器重开仍登录（未过期） |
| 定时 refresh | 无打断；失败走 401 流 |
| returnTo | 登录后回到拦截前页面 |

---

## 6. State Design

### 6.1 `/login`

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| idle | 进入页 | Plain 卡；P0 无 Tab；表单可编辑 | Card, Form |
| validating | tenant 空 | 行内「请输入租户标识」；不请求 API | Form rules |
| loading | begin / 账密 POST 中 | Button loading；Input disabled | Button |
| redirecting | OIDC begin 200 | Button「跳转中…」；**立即** `location.assign`（无 confetti 等待） | Button |
| error-tenant | `code=TENANT_NOT_FOUND` | `Message.error`「租户不存在，请检查租户标识」 | Message |
| error-idp | `code=IDP_UNAVAILABLE` | `Message.error`「身份服务暂不可用，请稍后重试」 | Message |
| error-credentials | P1 `INVALID_CREDENTIALS` | `Message.error`「用户名或密码错误」；清空密码框 | Message, Input |
| error-network | 网络失败 | `Message.error`「网络异常，请稍后重试」 | Message |
| error-generic | 其他 4xx/5xx | `Message.error` API `message` 或默认文案 | Message |
| already_authed | 有效 session | 重定向 `returnTo` 或 `/` | redirect |

### 6.2 `/auth/callback`

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| loading | 有 code+state | 全屏 Loading | Loading |
| error_missing_params | 缺参 | 错误卡「登录回调参数不完整」 | Card |
| error_state_mismatch | state 不一致 | 「登录状态无效，请重新登录」 | Card |
| error_token | token API 失败 | `OIDC_EXCHANGE_FAILED` 或 API message | Card |
| success | token 200 | success toast → returnTo 或 `/` | Message |

### 6.3 全局 / `_authenticated`

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| authed | 有效 token | 正常壳 | Layout |
| gate_redirect | 无 token 访业务 | 无业务 UI → `/login` | redirect |
| session_expired_401 | API 401 | warning toast → returnTo 保存 → `/login` | Message |
| logout | 点击退出 | → `/login` | Button |

### 6.4 Empty states

不适用（无列表）。Callback loading 即进行中占位。

---

## 7. Copy & Feedback

### 7.1 Labels & Buttons

| Element | Copy (zh-CN) | Notes |
|---------|--------------|-------|
| 登录页标题 | KuberCloud ANI | Plain 固定 |
| Tab · 企业登录 | 企业登录 | P1 |
| Tab · 账密 | 账号密码 | P1 |
| 租户标识 | 租户标识 | placeholder 同 label |
| 用户名 | 用户名 | P1 账密 Tab |
| 密码 | 密码 | `type="password"` |
| 记住我 | 记住我 | |
| 主 CTA | 登录 / 跳转中… | |
| OIDC 说明 | 将跳转到企业身份提供商完成认证 | 账密 Tab 隐藏 |
| 开发折叠标题 | 开发登录 | DEV only |
| Callback loading | 正在完成登录... | |
| 错误卡标题 | 登录未完成 | |
| 返回登录 | 返回登录 | |
| 登出 | 退出登录 | Header |

### 7.2 Messages

| Scenario | Type | Copy |
|----------|------|------|
| begin / 账密成功 | — | 账密无 toast，直接跳转；OIDC callback success「登录成功」 |
| TENANT_NOT_FOUND | `Message.error` | 租户不存在，请检查租户标识 |
| IDP_UNAVAILABLE | `Message.error` | 身份服务暂不可用，请稍后重试 |
| INVALID_CREDENTIALS | `Message.error` | 用户名或密码错误 |
| OIDC_EXCHANGE_FAILED | 错误卡 / Message | 登录验证失败，请重新登录 |
| 网络失败 | `Message.error` | 网络异常，请稍后重试 |
| 默认 begin 失败 | `Message.error` | 登录发起失败，请稍后重试 |
| tenant 必填 | Form inline | 请输入租户标识 |
| callback 缺参 | 错误卡 | 登录回调参数不完整 |
| state 无效 | 错误卡 | 登录状态无效，请重新登录 |
| 401 / refresh 失败 | `Message.warning` | 登录已过期，请重新登录 |

**禁止文案：** Dex、OIDC 协议名作为用户主文案；内部组件名。

---

## 8. Boundaries & Non-Goals

### 8.1 In Scope (UX)

- Plain 登录卡 + P1 双 Tab
- 记住我勾选与存储介质语义
- returnTo 优先回跳
- callback / 401 / 登出用户反馈
- DEV 折叠开发登录入口
- Header 登出

### 8.2 Explicitly Out of Scope (UI)

- gamified、Confetti、入场动画（PRD NG-7）
- 注册、忘记密码、MFA、LDAP/SSO 配置
- IdP 登录页 UI
- 登录页 Logo/背景从平台配置读取（P2）
- BOSS 登录页（见 BOSS UX 文档）
- Console 与 BOSS 共用 storage key（PRD NG-8）

### 8.3 Open UX Questions

- 无（returnTo、记住我、Plain UI 已由 PRD §11 确认）
- 账密 API 路径命名 → SPEC / `v1.yaml` 冻结后同步 §5.2

### 8.4 Assumptions

- P0 实现可隐藏 Tabs，仅 OIDC；P1 打开 Tabs 不改动布局结构
- `redirect_uri` = `${origin}/auth/callback`
- 已登录重定向与登录成功跳转**均**消费 `returnTo`（与 PRD US-005 一致）
- 开发登录 UI 仅 `import.meta.env.DEV` 且后端 dev profile 开启时显示
- 密码仅在账密 Tab 与 IdP 站外出现，OIDC Tab 无密码字段

---

## Document Links

| Artifact | Path |
|----------|------|
| PRD v2 | `repo/services/tasks/modules/prd/console/tenant/prd-console-login-page.md` |
| UX（本文） | `repo/services/tasks/modules/ux/console/tenant/ux-console-login-page.md` |
| BOSS UX | `repo/services/tasks/modules/ux/boss/settings/ux-boss-login-page.md` |
| SPEC（待修订） | `repo/services/tasks/modules/spec/console/tenant/spec-console-login-page.md` |
