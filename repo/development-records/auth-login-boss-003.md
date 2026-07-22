# Development Records — BOSS 平台管理员登录

> **BATCH-ID:** auth-login-boss-006
> **日期：** 2026-07-22
> **范围：** BOSS 前端平台管理员登录页、会话管理（与 Console 隔离）
> **关联 Issue：** BOSS #006
> **SPEC：** `spec/boss/login/spec-boss-login.md`
> **PRD：** `prd/boss/login/prd-boss-login-page.md`

---

## Implementation Notes / Design Choices

### 1. P1-2: 在 beforeLoad 路由守卫中调用 maybeRefresh

**Ambiguity:** SPEC US-006 要求"剩余有效期 < 5 分钟触发 refresh"，但未规定触发时机。

**Choice:** 在 `_authenticated.tsx` 的 `beforeLoad` 中调用 `await maybeRefresh()`。

**Rationale:** 与 Console 同步接入，路由切换时自动检查 token 有效期。

### 2. P1-3: 401 中间件先尝试 refresh 再跳登录

**Choice:** 401 时先调用 `refreshAccessToken()`，成功则让调用方重试请求，失败才 `handle401()`。

**Rationale:** 与 Console 同步修改，实现无感续期。

---

## Spec Deviations + Rationale

### 1. P0-3: BOSS OIDC redirect_uri 添加 /boss 前缀

**Spec:** SPEC 6.3 路由结构中 BOSS callback 路径标注"或 `/boss/auth/callback` — SPEC 定"。

**Implementation:** BOSS 的 `login.tsx` 和 `callback.tsx` 中 redirect_uri 从 `${origin}/auth/callback` 改为 `${origin}/boss/auth/callback`。

**Rationale:** BOSS 的 `vite.config.ts` 配置了 `base: '/boss/'`，实际回调路径是 `/boss/auth/callback`。原拼接缺少 `/boss` 前缀导致 Dex 回调时 Vite Dev Server 找不到资源。

### 2. BOSS OIDC 登录暂不实现

**Spec:** PRD US-014 要求 BOSS 支持 OIDC 与账密两种登录。

**Implementation:** BOSS OIDC 登录暂不实现（用户明确指示）。当前 BOSS 仅支持账密登录。

**Rationale:** auth-service 的 `Begin` 方法强制要求 `tenant_name` 非空，BOSS 平台登录传空字符串会被拒绝。需 Core 侧扩展平台 OIDC 路径后再实现。当前不阻断账密登录路径。

---

## Follow-ups / Blockers

### 1. BOSS 平台管理员 OIDC 登录路径不可用

BOSS 前端 OIDC 登录传 `tenant_name: ''`，但 auth-service 的 `Begin` 方法强制要求非空。需 Core 侧扩展平台 OIDC 路径后再实现。当前搁置，不影响账密登录路径。

### 2. Dex redirect_uri 白名单需同步更新

BOSS 的 redirect_uri 已改为 `${origin}/boss/auth/callback`，Dex 配置的 `redirectURIs` 需同步添加 `http://localhost:5174/boss/auth/callback`，否则 Dex 会拒绝回调。需运维确认。

---

## Verification Commands Run

```bash
cd frontends/boss
npx vite build --mode development
# Result: PASS (29.64s, 5255 modules)
```

---

## SPEC 验收标准对照

| SPEC 条目 | 实现状态 | 代码位置 |
|---|---|---|
| §2.1 消费 `/auth/platform/password/login` | ✅ | `boss/src/api/auth.ts` |
| §2.3 平台 Token Claims (scope=platform) | ✅ | `platform_login.go:67` |
| §3.1 Vite base `/boss/` + redirect_uri 带前缀 | ✅ | `boss/src/routes/login.tsx` |
| §3.2 beforeLoad 路由守卫 + maybeRefresh | ✅ | `boss/src/routes/_authenticated.tsx` |
| §4.1 存储 `boss:` 前缀（与 Console 隔离） | ✅ | `boss/src/auth/session.ts` |
| §4.2 401 先 refresh 再 handle401 | ✅ | `boss/src/api/auth.ts` |
| §5 UI Plain 布局 + TDesign | ✅ | `boss/src/routes/login.tsx` |
| 平台 OIDC 登录 | ⏸ 暂不实现 | 用户明确指示搁置 |

---

## Issue 完成度

| Issue | 标题 | 状态 | 验收项 |
|---|---|---|---|
| #006 | BOSS P1 账密登录 | ✅ 已完成 | 平台账密登录 + 会话隔离 + redirect_uri 修复 |
| #006 | BOSS P1 OIDC 登录 | ⏸ 暂不实现 | auth-service Begin 方法需扩展平台路径 |

---

## 变更文件清单

| 文件 | 修复项 |
|---|---|
| `frontends/boss/src/routes/_authenticated.tsx` | P1-2 maybeRefresh + 菜单合并 |
| `frontends/boss/src/api/auth.ts` | P1-3 401 先 refresh |
| `frontends/boss/src/routes/login.tsx` | P0-3 redirect_uri |
| `frontends/boss/src/routes/auth/callback.tsx` | P0-3 redirect_uri |
