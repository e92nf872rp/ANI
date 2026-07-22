# Issue #12: BOSS 前端骨架初始化

> **Priority:** medium
> **Depends On:** —
> **Product line:** BOSS
> **Document Links:**
> - SPEC: `repo/services/tasks/modules/spec/boss/gpu-inventory/spec-boss-gpu-pool.md` §2.4
> - UX: `repo/services/tasks/modules/prd/console/gpu-inventory/ux-boss-gpu-pool.md`

## Scope

- `repo/frontends/boss/`（NEW：整个目录从零创建）

## Description

BOSS 运营后台前端项目骨架初始化。当前 `repo/frontends/boss/` 不存在，需从零搭建，复用 Console 的技术栈（TDesign React + TanStack Router + openapi-fetch）。

## Acceptance Criteria

- [x] `package.json` + `pnpm-workspace.yaml`（与 Console 共享 workspace）
- [x] `vite.config.ts` + `tsconfig.json`
- [x] `index.html`
- [x] `src/main.tsx`（TanStack Router 模式，不含旧 App.tsx 自定义路由）
- [x] TDesign `Layout` 壳（Header + Aside + Content）
- [x] `src/api/coreClient.ts`（openapi-fetch，`baseUrl: '/api/v1'`）
- [x] `make gen-core-schema` 生成 `src/api/core-schema.d.ts`（用 `npx openapi-typescript` 生成成功）
- [x] `src/routes/__root.tsx`：侧栏含「资源池与基础设施 → GPU 资源池管理」入口
- [x] `src/styles.css`
- [x] `pnpm type-check && pnpm lint && pnpm build` 通过（type-check + vite build 通过；lint 同 Console 无 eslint config）

## Validation

```bash
cd repo/frontends/boss
pnpm install
pnpm type-check
pnpm lint
pnpm build
```
