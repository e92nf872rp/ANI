# Issue #7: Console Shell 组件（ConsolePage/Header/ContentCard）

> **Priority:** high
> **Depends On:** —
> **Product line:** Console
> **Document Links:**
> - SPEC: `repo/services/tasks/modules/spec/console/gpu-inventory/spec-console-gpu-scheduling.md` §2.2
> - UX: `repo/services/tasks/modules/prd/console/gpu-inventory/ux-console-gpu-scheduling.md`

## Scope

- `repo/frontends/console/src/components/shell/ConsolePage.tsx`（NEW）
- `repo/frontends/console/src/components/shell/ConsolePageHeader.tsx`（NEW）
- `repo/frontends/console/src/components/shell/ConsoleContentCard.tsx`（NEW）

## Description

创建 Console 页面壳层组件，当前代码库不存在这些组件。基于 TDesign `Layout` 封装，与 `__root.tsx` 已有 Layout 兼容。后续所有 Console 页面都复用这套壳层。

## Acceptance Criteria

- [x] `ConsolePage` 组件（页面壳布局容器）
- [x] `ConsolePageHeader` 组件（标题 + 副标题 + 操作区 slots）
- [x] `ConsoleContentCard` 组件（内容卡片容器）
- [x] 基于 TDesign `Layout` 封装，与 `__root.tsx` 已有 Layout 兼容
- [x] TypeScript 类型完整
- [x] `pnpm type-check && pnpm lint && pnpm build` 通过（type-check + build 通过；lint 因 pre-existing eslint config 缺失无法运行，非本批次引入）

## Validation

```bash
cd repo/frontends/console
pnpm type-check
pnpm lint
pnpm build
```
