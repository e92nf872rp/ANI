# Issue 005: Console 登录页账密 Tab（P1）

## Document Links
- PRD: `repo/services/tasks/modules/prd/console/login/prd-console-login-page.md`
- UX: `repo/services/tasks/modules/prd/console/login/ux-console-login-page.md`
- SPEC: `repo/services/tasks/modules/spec/core/login/spec-core-login.md`

## Description

在 Console `/login` 页新增 **账号密码** Tab，消费 Issue #002 已合并的 Core 账密 API `POST /api/v1/auth/password/login`。Tab 切换时保持「记住我」勾选状态、共享 `returnTo` 回跳逻辑、共享会话模块；不展示 OIDC 跳转说明文案。

按 CLAUDE.md「Services 已冻结并移交外部产品团队」执行：本 Issue 仅修改 `repo/frontends/console/`，**禁止**修改 Gateway、auth-service 或 `v1.yaml`。

## Scope
- Product line: console
- Code paths allowed: `repo/frontends/console/` only

## Acceptance Criteria

### US-009 账密 Tab UI
- [ ] `/login` 展示 `Tabs` 两项：「企业登录」(OIDC) | 「账号密码」(账密)
- [ ] 账密 Tab 字段：`tenant_name`、`username`、`password`、`Checkbox`「记住我」；**无** OIDC 跳转说明文案（UX §4.1）
- [ ] 密码 `Input type="password"`，不持久化（UX §5.2）
- [ ] Tab 切换时「记住我」勾选状态保持（UX §4.1 notes）
- [ ] `tenant_name` / `username` / `password` 为空时行内校验，不发起 API

### US-009 账密登录提交
- [ ] 账密 Tab 提交 `POST /api/v1/auth/password/login`，body：`tenant_name`、`username`、`password`，可选 `idempotency_key`
- [ ] 请求中按钮 `loading`，全表单 disabled，禁止重复提交
- [ ] 200：与 OIDC 相同方式保存 `TokenPair`（存储介质由「记住我」决定）→ 跳转 `returnTo` 或 `/`
- [ ] 账密请求 **必须** 走 TLS（生产 https）；前端不记录密码到 localStorage / sessionStorage / log
- [ ] 失败后清空 `password` 字段，保留 `tenant_name` / `username`

### US-009 错误码映射（UX §7.2）
- [ ] `INVALID_CREDENTIALS` → `Message.error`「用户名或密码错误」；清空密码框
- [ ] `TENANT_NOT_FOUND` → `Message.error`「租户不存在，请检查租户标识」
- [ ] 网络失败 → `Message.error`「网络异常，请稍后重试」
- [ ] 其他 4xx/5xx → `Message.error` API `message` 或默认文案「登录失败，请稍后重试」
- [ ] 不区分用户是否存在（防枚举，按 SPEC §5.1）

### 全链路一致
- [ ] 账密登录成功后 `hydrateSession`、refresh、401、登出、returnTo 与 OIDC Tab 完全一致（复用 Issue #004 会话模块与 `_authenticated` 布局）
- [ ] 账密 token 与 OIDC token 走同一 `setAuthToken` / `coreApi` 拦截器
- [ ] 账密 Tab 提交成功后通过 `throw redirect({ to: returnTo || '/' })` 跳转，与 Issue #004 的 `_authenticated.tsx beforeLoad` 共享同一 returnTo 校验与消费逻辑
- [ ] 账密 Tab 的 `beforeLoad` 与 OIDC Tab 共用 `_authenticated.tsx`（已在 Issue #004 实现），不重复实现门禁

### UI 状态机（UX §6.1）
- [ ] idle / validating / loading / error-credentials / error-tenant / error-network / error-generic / already_authed 8 态覆盖
- [ ] `already_authed`：已登录访问 `/login` 账密 Tab 也直接重定向 `returnTo` 或 `/`

### 验收
- [ ] `pnpm type-check`、`pnpm lint`、`pnpm test`、`pnpm build` 通过
- [ ] 浏览器验证：账密成功/失败/记住我/returnTo 回跳
- [ ] Network 面板：密码字段不出现在请求 URL query（仅 body）

## Dependencies
- Issue #004（Console 登录页 P0 Plain UI + 会话链路）
- Issue #002（Core 账密 API `/auth/password/login`，已合并 PR #35）

## Type
frontend

## Priority
high

## Labels
console

## Batch
LOGIN-FE-P1

## SPEC Reference
- SPEC §4.1：`POST /auth/password/login` request/response 契约
- SPEC §6.1：错误码 `INVALID_CREDENTIALS`、`TENANT_NOT_FOUND`（不区分用户存在性防枚举）

## UX Reference
- UX §4.1：`/login` Plain + P1 双 Tab 布局
- UX §5.1：组件映射（Tabs/Form/Input password/Checkbox/Button）
- UX §5.2：账密 Tab 组件映射（username/password 字段）
- UX §6.1：`/login` 状态机（error-credentials 等）
- UX §7.1：Tab 文案「企业登录」「账号密码」
- UX §7.2：错误消息映射

## 验收命令

```bash
cd repo/frontends/console
pnpm type-check
pnpm lint
pnpm test
pnpm build
```

UI 验收（浏览器）：
- 账密登录成功 → `returnTo` 或 `/`
- 错误凭证 → `Message.error` + 密码框清空 + 保留 tenant/username
- 租户不存在 → `Message.error`
- 记住我开/关 → sessionStorage/localStorage 切换
- Tab 切换 → 「记住我」状态保持
- 401 / refresh / 登出与 OIDC Tab 一致

## Non-Goals

- 平台账密登录（BOSS 范围，Issue #006）
- Dev Profile 折叠区入口
- 密码自助修改、忘记密码、MFA
- 注册 UI
- 修改 Gateway、auth-service、`v1.yaml`
