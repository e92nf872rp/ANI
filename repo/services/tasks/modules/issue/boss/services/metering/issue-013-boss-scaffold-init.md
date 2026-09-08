# [BOSS] 前端项目 scaffold 初始化

## Document Links
- PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
- UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- SPEC: `repo/services/tasks/modules/spec/boss/services/metering/spec-boss-metering-service.md`

## Description

从零创建 BOSS 前端项目 `repo/frontends/boss/`。技术栈与 Console 一致：Vite + TanStack Router + TDesign + React Query + openapi-fetch + ECharts。含 package.json、vite.config.ts、tsconfig.json、index.html、main.tsx 入口。

## Scope
- Product line: boss
- Code paths allowed: `repo/frontends/boss/`（全新）

## Acceptance Criteria
- [ ] `package.json` 依赖: Vite, @tanstack/react-router, @tanstack/react-query, tdesign-react, openapi-fetch, openapi-typescript, echarts, echarts-for-react
- [ ] `vite.config.ts`: TanStackRouterVite 插件 + @ 别名 + /api 代理
- [ ] `main.tsx`: QueryClientProvider + RouterProvider
- [ ] `npm install` + `npm run dev` 可启动
- [ ] `npm run build` 成功

## Dependencies
None

## Type
boss

## Priority
high

## Labels
boss

## Batch
TBD

## References
- SPEC: BOSS §2.4
