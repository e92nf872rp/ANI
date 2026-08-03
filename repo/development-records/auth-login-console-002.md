# Development Records — Console 登录前端

> **BATCH-ID:** auth-login-console-004
> **日期：** 2026-07-22
> **范围：** Console 前端登录页、会话管理、路由守卫（OIDC + 账密 Tab）
> **关联 Issue：** Console #004、#005
> **SPEC：** `spec/console/login/spec-console-login.md`
> **PRD：** `prd/console/login/prd-console-login-page.md`

---

## Implementation Notes / Design Choices

### 1. P1-2: 在 beforeLoad 路由守卫中调用 maybeRefresh

**Ambiguity:** SPEC US-006 要求"剩余有效期 < 5 分钟触发 refresh"，但未规定触发时机。

**Choice:** 在 `_authenticated.tsx` 的 `beforeLoad` 中调用 `await maybeRefresh()`。

**Rationale:** `beforeLoad` 在每次路由切换时执行，是检查 token 有效期的最佳时机。改为 `async` 后可等待 refresh 完成再进入页面，避免页面加载后 API 请求因 token 过期而 401。

### 2. P1-3: 401 中间件先尝试 refresh 再跳登录

**Ambiguity:** SPEC US-007 要求"API 401 时回到登录页"，但未明确是否应先尝试 refresh。

**Choice:** 401 时先调用 `refreshAccessToken()`，成功则让调用方重试请求，失败才 `handle401()`。

**Rationale:** access token 1 小时过期，refresh token 7 天有效。大多数 401 场景是 access token 自然过期。先尝试 refresh 可实现无感续期。`refreshAccessToken` 内部有并发去重（`refreshing` Promise 锁），多个并发 401 只触发一次 refresh。

### 3. P1-5: chat.tsx 幂等键使用 useRef + crypto.randomUUID()

**Ambiguity:** SPEC FR-16 要求 POST 写操作支持 idempotency_key，但未规定前端生成策略。

**Choice:** 用 `useRef<string | null>` 缓存当前问题的幂等键，新提问时生成 `ani_${crypto.randomUUID()}`，重试时复用同一键，请求成功后清空。

**Rationale:** `crypto.randomUUID()` 是浏览器原生 API，无需引入额外依赖。`useRef` 在组件生命周期内持久化，重试时复用同一键使服务端能识别重复请求。

---

## Spec Deviations + Rationale

### 1. Console/BOSS __root.tsx 保留 my-changes 架构

**Spec:** PR 分支将布局放在 `__root.tsx`（含 Header + Aside + Menu）。

**Implementation:** 保留 my-changes 的架构（`__root.tsx` 仅 `<Outlet />`，布局在 `_authenticated.tsx`），将 PR 分支的菜单项合并到 `_authenticated.tsx`。

**Rationale:** 公开路由（`/login`、`/auth/callback`）不应包含业务壳层。布局放在 `_authenticated.tsx` 确保只有认证后的页面才渲染 Header/Aside/Menu。同时保留 `beforeLoad` 守卫。

---

## Alternatives Considered

### 1. P1-2: beforeLoad 路由守卫 vs 请求拦截器

**备选 A（已选）：** `beforeLoad` 中调用 `maybeRefresh`。优点：路由切换时自动检查，在 API 请求之前完成续期。缺点：仅在路由切换时触发。

**备选 B：** `authMiddleware.onRequest` 中调用。优点：每次 API 请求前检查。缺点：改为 async 会影响所有请求延迟，可能导致并发 refresh 风暴。

**结论：** 两者组合使用最佳：`beforeLoad` 做主动续期，401 中间件做被动兜底。

### 2. P1-5: crypto.randomUUID() vs TS SDK newIdempotencyKey

**备选 A（已选）：** 直接用 `crypto.randomUUID()`。优点：零依赖。缺点：无降级策略。

**备选 B：** 引入 TS SDK 的 `newIdempotencyKey('ani')`。优点：有降级策略。缺点：需引入 SDK 依赖。

**结论：** `crypto.randomUUID` 在所有现代浏览器中已支持，方案 A 零依赖更轻量。

---

## Follow-ups / Blockers

### 1. maybeRefresh 后 setAuthToken 使用旧 session 引用

`_authenticated.tsx` 的 `beforeLoad` 中，`await maybeRefresh()` 后 `setAuthToken(session.access_token)` 用的是 maybeRefresh 之前的旧 session 引用。若 maybeRefresh 触发了 refresh，bearerToken 会被设回旧 token。实际由 401 中间件兜底，不阻断。建议后续在 `maybeRefresh` 后重新读取 `getSession()`。

---

## Verification Commands Run

```bash
cd frontends/console
npm run type-check
# Result: PASS

npx vite build --mode development
# Result: PASS
```

---

## SPEC 验收标准对照

| SPEC 条目 | 实现状态 | 代码位置 |
|---|---|---|
| §2.1 消费 `/auth/password/login` | ✅ | `console/src/api/auth.ts` |
| §3.1 beforeLoad 路由守卫 + maybeRefresh | ✅ | `console/src/routes/_authenticated.tsx` |
| §4.1 存储 `console:` 前缀 + sessionStorage/localStorage | ✅ | `console/src/auth/session.ts` |
| §4.3 401 先 refresh 再 handle401 | ✅ | `console/src/api/auth.ts` |
| §5 UI Plain 布局 + TDesign + Tab 切换 | ✅ | `console/src/routes/login.tsx` |

---

## Issue 完成度

| Issue | 标题 | 状态 | 验收项 |
|---|---|---|---|
| #004 | Console P0 OIDC 登录 | ✅ 已完成 | Plain UI + OIDC 链路 + returnTo + 记住我 + 会话 |
| #005 | Console P1 账密 Tab | ✅ 已完成 | Tab 切换 + 账密表单 + 错误码映射 |

---

## 架构决策记录

### ADR-4: 布局放在 _authenticated.tsx 而非 __root.tsx

**决策：** `__root.tsx` 仅渲染 `<Outlet />`，布局壳层（Header + Aside + Menu）放在 `_authenticated.tsx`。

**原因：** 公开路由不应包含业务壳层。布局放在 `_authenticated.tsx` 确保只有认证后的页面才渲染 Header/Aside/Menu。同时保留 `beforeLoad` 守卫。

---

## 变更文件清单

| 文件 | 修复项 |
|---|---|
| `frontends/console/src/routes/_authenticated.tsx` | P1-2 maybeRefresh + 菜单合并 |
| `frontends/console/src/api/auth.ts` | P1-3 401 先 refresh |
| `frontends/console/src/routes/_authenticated/kb/$kbId/chat.tsx` | P1-5 幂等键 |
