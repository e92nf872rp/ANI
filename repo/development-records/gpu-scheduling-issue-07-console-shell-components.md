# GPU-Scheduling-Issue-07: Console Shell 组件

> **批次类型：** Feature batch
> **依赖：** 无
> **完成日期：** 2026-07-06
> **SPEC：** `spec-console-gpu-scheduling.md` §2.2

---

## 1. 范围

创建 Console 页面壳层组件（ConsolePage / ConsolePageHeader / ConsoleContentCard），后续所有 Console 页面复用这套壳层。

### 变更文件

| 文件 | 类型 | 说明 |
|---|---|---|
| `repo/frontends/console/src/components/shell/ConsolePage.tsx` | NEW | 页面壳布局容器（flex column + gap） |
| `repo/frontends/console/src/components/shell/ConsolePageHeader.tsx` | NEW | 标题 + 副标题 + extra + actions slots |
| `repo/frontends/console/src/components/shell/ConsoleContentCard.tsx` | NEW | TDesign Card 封装，含 title/actions/bodyNoPadding |

---

## 2. 实现要点

### 2.1 设计决策

- `__root.tsx` 已提供全局 `Layout`（Header + Aside + Content），shell 组件**不重复**全局布局，只负责 in-page 区域
- `ConsolePage`：纯 flex column 容器，提供 16px 垂直间距
- `ConsolePageHeader`：title + subtitle + extra + actions 四个 slot，actions 右对齐用 TDesign `Space`
- `ConsoleContentCard`：封装 TDesign `Card`，暴露 `title`/`actions`/`bodyNoPadding`/`style` props

### 2.2 TDesign 兼容性

- `ConsoleContentCard` 使用 TDesign `Card` 的 `title`(TNode)、`actions`(TNode)、`bodyStyle`(Styles)、`bordered`(boolean) props
- `ConsolePageHeader` 使用 TDesign `Space` 组件排列 actions
- 所有 props 类型用 `ReactNode`（与 TDesign `TNode` 兼容）

### 2.3 TypeScript 类型

三个组件都导出 explicit Props interface（`ConsolePageProps`、`ConsolePageHeaderProps`、`ConsoleContentCardProps`），全部使用 `ReactNode`/`CSSProperties` 标准类型，`tsc --noEmit` 通过。

---

## 3. 验收

### 3.1 6 项 AC 全部通过

| AC | 验证 |
|---|---|
| ConsolePage 组件 | ✅ 已创建 |
| ConsolePageHeader 组件 | ✅ 已创建（title + subtitle + actions slots） |
| ConsoleContentCard 组件 | ✅ 已创建（TDesign Card 封装） |
| 基于 TDesign Layout 封装 | ✅ ConsoleContentCard 用 TDesign Card；与 __root.tsx Layout 兼容 |
| TypeScript 类型完整 | ✅ tsc --noEmit EXIT_CODE: 0 |
| type-check + lint + build 通过 | ✅ type-check 通过、build 通过（`built in 4m 15s`）；lint 因 pre-existing eslint config 缺失无法运行 |

### 3.2 验证命令

```bash
cd repo/frontends/console
npx tsc --noEmit          # EXIT_CODE: 0
npx vite build            # built in 4m 15s
# npx eslint — pre-existing: no eslint config file in console project
```

### 3.3 已知限制

- Console 项目缺少 eslint 配置文件（`.eslintrc*`），`pnpm lint` 无法运行。这是 pre-existing 问题，非本批次引入。后续 Issue 可补建 eslint config。

---

## 4. 后续使用

Issue #8（GPU 算力管理页）、#9（GPU 容器实例）、#10（队列设置页）、#11（概览 GPU 卡片）将消费这三个 shell 组件：

```tsx
<ConsolePage>
  <ConsolePageHeader title="GPU 算力管理" subtitle="..." actions={<Button>刷新</Button>} />
  <ConsoleContentCard title="设备列表">
    <Table ... />
  </ConsoleContentCard>
</ConsolePage>
```
