# GPU-Scheduling-Issue-12: BOSS 前端骨架初始化

> **批次类型：** Feature batch
> **依赖：** 无
> **完成日期：** 2026-07-06
> **SPEC：** `spec-boss-gpu-pool.md` §2.4

---

## 1. 范围

从零创建 BOSS 运营后台前端项目骨架，复用 Console 技术栈（TDesign React + TanStack Router + openapi-fetch）。

### 变更文件

| 文件 | 类型 | 说明 |
|---|---|---|
| `repo/frontends/boss/package.json` | NEW | 依赖与 Console 一致 |
| `repo/frontends/boss/pnpm-workspace.yaml` | NEW | 允许 esbuild build |
| `repo/frontends/boss/vite.config.ts` | NEW | port 5174，proxy /api → localhost:8080 |
| `repo/frontends/boss/tsconfig.json` | NEW | 与 Console 一致 |
| `repo/frontends/boss/index.html` | NEW | BOSS 入口 |
| `repo/frontends/boss/src/main.tsx` | NEW | TanStack Router + QueryClient |
| `repo/frontends/boss/src/styles.css` | NEW | 基础样式 |
| `repo/frontends/boss/src/api/coreClient.ts` | NEW | openapi-fetch `/api/v1` |
| `repo/frontends/boss/src/api/core-schema.d.ts` | NEW | 从 v1.yaml 生成 |
| `repo/frontends/boss/scripts/gen-core-schema.mjs` | NEW | Core schema 生成脚本 |
| `repo/frontends/boss/src/routes/__root.tsx` | NEW | TDesign Layout + 侧栏「资源池与基础设施 → GPU 资源池管理」 |
| `repo/frontends/boss/src/routes/index.tsx` | NEW | 运营总览首页占位 |
| `repo/frontends/boss/src/routes/ops/gpu-pool.tsx` | NEW | GPU 资源池管理占位（Issue #13 实现） |
| `repo/frontends/boss/src/routeTree.gen.ts` | GEN | TanStack Router 自动生成 |

---

## 2. 实现要点

### 2.1 技术栈复用

BOSS 复用 Console 全部技术栈：
- TDesign React 1.10 + tdesign-icons-react
- TanStack Router 1.40 + TanStack Router Vite plugin
- TanStack Query 5.40
- openapi-fetch 0.12 + openapi-typescript 7.13
- Vite 5.3 + TypeScript 5.4

### 2.2 设计决策

- BOSS 不使用 Console 的旧 `App.tsx` 自定义路由模式，直接采用 TanStack Router 文件路由（`__root.tsx` + `routes/`）
- 侧栏菜单按 UX 文档：「资源池与基础设施 → GPU 资源池管理」（`/ops/gpu-pool`）
- `vite.config.ts` 使用 port 5174（Console 是 5173），避免开发时冲突
- `coreClient.ts` 与 Console 一致，`baseUrl: '/api/v1'`，BOSS 只消费平台管理员 scope 端点

### 2.3 路由结构

```text
__root.tsx       → Layout(Header + Aside + Content)
  ├── index.tsx  → / (运营总览占位)
  └── ops/
      └── gpu-pool.tsx → /ops/gpu-pool (GPU 资源池管理占位，Issue #13 实现)
```

---

## 3. 验收

### 3.1 10 项 AC 全部通过

| AC | 验证 |
|---|---|
| package.json + pnpm-workspace.yaml | ✅ 已创建 |
| vite.config.ts + tsconfig.json | ✅ 已创建 |
| index.html | ✅ 已创建 |
| src/main.tsx | ✅ TanStack Router 模式 |
| TDesign Layout 壳 | ✅ __root.tsx Header + Aside + Content |
| src/api/coreClient.ts | ✅ openapi-fetch baseUrl=/api/v1 |
| core-schema.d.ts 生成 | ✅ npx openapi-typescript 生成成功 |
| __root.tsx 侧栏入口 | ✅ 「资源池与基础设施 → GPU 资源池管理」 |
| src/styles.css | ✅ 已创建 |
| type-check + build 通过 | ✅ tsc EXIT_CODE:0 + vite build built in 1m 34s |

### 3.2 验证命令

```bash
cd repo/frontends/boss
npm install              # added 254 packages
npx tsc --noEmit         # EXIT_CODE: 0
npx vite build           # built in 1m 34s
```

### 3.3 已知限制

- Console 和 BOSS 都缺少 eslint 配置文件，`pnpm lint` 无法运行（pre-existing，非本批次引入）
- `node_modules` 和 `dist` 不提交 Git

---

## 4. 后续

Issue #13（BOSS GPU 资源池管理页）将在此骨架上实现 `/ops/gpu-pool` 页面的完整内容。
