# PRD: BOSS 平台管理员登录

> **来源：** 拆分自 `prd-console-login-page.md` v2.0
> **日期：** 2026-07-03
> **范围：** BOSS 前端登录页、会话管理（与 Console 隔离）

---

## 1. Overview

BOSS 运营台需要独立的平台管理员登录入口，与租户 Console 隔离。平台管理员通过 OIDC 或账密登录获得 `access_token` / `refresh_token`（`scope=platform`），存储 key 与 Console 隔离防串会话。

**产品入口：**

```text
BOSS /login → 平台管理员上下文（平台 IdP / 账密，与租户隔离）
```

---

## 2. Goals

- BOSS 提供独立 **平台管理员登录页**，消费 Core Auth 平台 realm API
- **Plain** 风与 Console 一致，文案区分「平台运营台」
- **不含** 租户 slug 字段（或可选「平台域」— SPEC 定）
- 支持 `returnTo`、记住我、refresh、401、登出
- 存储 key 与 Console **隔离** 防串会话

---

## 3. User Stories

### US-014: BOSS 平台管理员登录页（P1）

**Description:** 作为平台管理员，我希望在 BOSS 独立入口登录，以便管理全平台资源。

**Acceptance Criteria:**

- [ ] BOSS 路由 `/login`（或 SPEC 冻结路径）；**Plain** 风与 Console 一致，文案区分「平台运营台」
- [ ] **不含** 租户 slug 字段（或可选「平台域」— SPEC 定）；提供 OIDC 与账密（与 Core 平台 realm API 对齐）
- [ ] 登录成功默认落地 BOSS 首页（如 `/` 或 `/ops/overview`，SPEC 冻结）
- [ ] 支持 `returnTo`、记住我、refresh、401、登出（复用会话模块模式，**存储 key 与 Console 隔离** 防串会话）
- [ ] 未登录访问 BOSS 业务页 → `/login`
- [ ] 浏览器验证 BOSS 登录三态

---

## 4. Functional Requirements

- **FR-8:** P1 BOSS 必须提供独立登录页与会话隔离存储
- **FR-9:** 所有 Auth UI 使用 TDesign（`Form`、`Input`、`Button`、`Checkbox`、`Tabs`、`Message`）
- **FR-10:** 生产构建不得包含 dev 登录入口或硬编码 token

---

## 5. Non-Goals

- BOSS 与 Console 共用同一浏览器的同一存储 key（**必须隔离**）
- 注册、忘记密码、邮箱验证、MFA
- HttpOnly Cookie 会话模式

---

## 6. Design Considerations

### BOSS `/login` Plain 布局

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

### 登录方式对照

| 方式 | BOSS | 密码输入位置 |
|------|------|--------------|
| OIDC | ✅ P1 | IdP 站外 |
| 账密 | ✅ P1 | 产品登录页 |

### 路由结构

| 公开路由 | 受保护 |
|----------|--------|
| `/login`, `/auth/callback`（或 `/boss/auth/callback` — SPEC 定） | BOSS 业务树 |

### Vite base 配置

BOSS 配置 `base: '/boss/'`，所有资源路径和 `redirect_uri` 需带 `/boss` 前缀。

---

## 7. 参考文档

- Core Auth PRD: `repo/services/tasks/modules/prd/core/prd-core-auth.md`
- Console 登录 PRD: `repo/services/tasks/modules/prd/console/login/prd-console-login-page.md`
- UX · BOSS: `repo/services/tasks/modules/ux/boss/settings/ux-boss-login-page.md`
