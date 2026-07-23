# CONSOLE-INSTANCE-OBSERVABILITY-SHELL-A — Console 路由壳层 + 实例上下文

> 对应 Issue: `repo/services/tasks/modules/issue/console/compute/issue-003-console-route-shell-context.md`
> 三元组: PRD `prd-console-instance-observability.md` / UX `ux-console-instance-observability.md` / SPEC `spec-console-instance-observability.md`
> Product line: console（仅 `repo/frontends/console/src/`）

完成日期：2026-07-06
对应 Sprint：Sprint 15（console instance observability UI 批次）
验证结果：`npx tsc --noEmit` EXIT:0；`make validate-architecture` passed；`git diff --check` passed；Vite dev server 启动成功，三个新模块运行时转换 200；浏览器运行时导入 `observabilityTabsConfig.ts` 全矩阵断言无错。

## 实现了什么

为实例详情可观测性建立 Console 端路由壳层与实例上下文 Provider：新建 `routes/compute/instances/$instanceId/route.tsx`（PageHeader + Tab 栏 + Tab Panel + `?tab=` 深链 + deleted 拦截），`features/instance-observability/InstanceContext.tsx`（实例上下文 Provider），`features/instance-observability/observabilityTabsConfig.ts`（kind → 可见 Tab 映射常量 + 辅助函数）。对齐 PRD US-007 与 SPEC §2.4.1、§3.3、§5.1.1。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `frontends/console/src/features/instance-observability/observabilityTabsConfig.ts` | 新增 | kind → 可见 Tab 映射常量 `INSTANCE_OBSERVABILITY_TAB_CONFIG` + `InstanceKind` / `ObservabilityTabId` 类型 + `getVisibleTabs` / `isMetricsSupported` / `resolveTabFromQuery` 辅助函数 |
| `frontends/console/src/features/instance-observability/InstanceContext.tsx` | 新增 | 实例上下文 Provider：`instance` / `kind` / `state` / `isDeleted` / `isRunning` + `useInstanceContext` hook |
| `frontends/console/src/routes/compute/instances/$instanceId/route.tsx` | 新增 | 详情壳层路由：`validateSearch` 深链解析、`useQuery` 拉取实例、deleted 拦截、PageHeader（name/id 可复制/state Tag/kind Tag）、Tab 栏（kind 过滤）、Tab Panel（占位）、面包屑 |
| `frontends/console/src/routeTree.gen.ts` | 修改（AUTO） | TanStack Router 路由树自动重新生成，新增 `/compute/instances/$instanceId` 条目 |

## 完工标准达成

- [x] `npx tsc --noEmit` 通过（exit 0）
- [x] `make validate-architecture` 通过
- [x] `git diff --check` 通过
- [x] 新建 route.tsx，渲染 PageHeader + Tab 栏 + Tab Panel（AC #1）
- [x] 新建 InstanceContext.tsx，提供 instance/kind/state（AC #2）
- [x] 新建 observabilityTabsConfig.ts，hardcode SPEC §3.3 矩阵（AC #3）
- [x] kind × Tab 矩阵与 PRD §6 / UX §4.1 一致，9 种 kind 全覆盖（AC #4）
- [x] 不渲染 hidden Tab（AC #5）
- [x] deleted 状态不可进入观测 Tab（AC #6）
- [x] 支持 `?tab=` 深链；非法/不可见回退「日志」（AC #7）
- [x] 面包屑：`算力与云资源 / 实例管理 / {instance.name}`（AC #8）
- [x] Typecheck 通过（lint 工具链缺失为 pre-existing 项目级问题，非本批次引入）（AC #9）
- [x] browser 运行时验证：导入 config 模块执行 container/vm/sandbox 差异断言无错（AC #10，环境无 playwright/puppeteer，用临时验证页面在真实浏览器完成运行时验证后清理）

## 1. Design Decisions

### 1.1 深链回退使用 `validateSearch` + 组件内 `resolveTabFromQuery` 双层判定

- **歧义：** UX §2.1 定义深链 `?tab=logs|events|metrics|terminal|console|security-events`，非法/不可见 tab 回退「日志」。UX §8.4 与 SPEC §11.3 又把深链标为「可选增强，Phase 1 可仅用本地 Tab state」。Issue AC #7 明确要求深链，故采用深链方案。
- **选择：** 在 TanStack Router `validateSearch` 中只做 URL 参数剥离（合法值保留，非法/不存在返回 `{}`），在组件内用 `resolveTabFromQuery(search.tab, kind)` 做最终的「非法/不可见 → logs」回退。
- **理由：** `validateSearch` 不感知 `kind`（路由级，无实例上下文），而 Tab 可见性依赖 `kind`，故回退判定必须在组件内完成。双层分离让路由层只负责 URL 形态校验，业务层负责 kind 过滤，职责清晰。回退到「logs」而非报错，符合 UX §2.1「回退到日志」的明文要求。

### 1.2 `InstanceContext` 暴露 `isDeleted` / `isRunning` 派生字段

- **歧义：** SPEC §2.2.1 只要求 InstanceContext 提供 `instance` 记录、`kind`、`state`。Issue AC #6 要求 `deleted` 状态整页 Empty。
- **选择：** 在 Provider 中额外派生 `isDeleted = state === 'deleted'` 和 `isRunning = state === 'running'`，供壳层和未来子 Tab 复用。
- **理由：** `deleted` 拦截逻辑在壳层需直接判断，子 Tab（如终端/控制台）也需根据运行状态决定可用性。提前暴露派生字段避免每个消费者重复写 `state === 'deleted'` 字面量比较，且不引入新抽象——只是派生只读字段。

### 1.3 `mapKindLabel` 首期直接返回 kind 原值

- **歧义：** UX §5.1 表中「实例类型 Tag」只说 `variant=outline` + `instance.kind`，未规定是否需要中文化。
- **选择：** `mapKindLabel(kind)` 直接返回 `kind` 原值（如 `container`、`vm`）。
- **理由：** PRD/UX 均未要求 kind 中文化；AC #1 只要求「kind Tag」。首期展示原值最简，后续可在该函数内扩展为友好名称映射而不改调用点。

### 1.4 `mapStateTheme` 对 9 种 state 显式映射 TDesign Tag theme

- **歧义：** UX §5.1 给出 `running→success, stopped→default, failed→danger` 三种，但 OpenAPI `InstanceState` 枚举有 9 种（pending/running/succeeded/failed/stopped/deleted/unknown/...）。
- **选择：** 显式映射 9 种 state，未列出的 fallback `default`。
- **理由：** TDesign Tag theme 是有限枚举（success/warning/danger/default/primary），显式映射避免运行时拿到未知 theme 报错。`deleted` 映射 `default`（整页已拦截，Tag 颜色不关键）。

## 2. Deviations

### 2.1 SPEC §2.4.1 文件结构中本批次只实现壳层三件，未实现子 Tab 组件

- **SPEC 说：** §2.4.1 列出 12 个新建文件，包括 `LogsTab.tsx` / `EventsTab.tsx` / `MetricsTab.tsx` / `MetricsSnapshot.tsx` / `MetricsChart.tsx` / `TerminalTab.tsx` / `ConsoleTab.tsx` / `SecurityEventsTab.tsx` / `promqlTemplates.ts` / `useInstanceObservability.ts`。
- **实现：** 本批次（issue-003）只实现 `route.tsx` + `InstanceContext.tsx` + `observabilityTabsConfig.ts` 三件，Tab Panel 内用占位文本（`{tabId} panel 占位`）。
- **原因：** Issue #003 的 Title 和 AC 明确只覆盖「路由壳层 + 实例上下文」，子 Tab 组件属于后续 issue（LogsTab/EventsTab/... 各自独立 issue）。SPEC §2.4.1 是整批次的完整文件清单，issue-003 是该批次的第一个子任务。占位文本是壳层先行的合理中间态，后续 issue 替换占位即可。

### 2.2 `ObservabilityTabId` 命名与 SPEC §3.3 完全一致，但 Tab label 用中文

- **SPEC 说：** `ObservabilityTabId = 'logs' | 'events' | 'metrics' | 'terminal' | 'console' | 'security-events'`（英文 id）。
- **实现：** Tab id 用英文（与 SPEC 一致），但 Tab label 用中文（`logs → 日志`、`events → 事件`、`metrics → 指标`、`terminal → 终端`、`console → 控制台`、`security-events → 安全事件`）。
- **原因：** UX §2.2 导航关系图和 §4.0 Tab 栏均用中文 label（「日志/事件/指标/终端/控制台/安全事件」），id 用英文便于 URL `?tab=` 深链和代码引用。id/label 分离是标准做法，不偏离 SPEC（SPEC 只定义 id）。

## 3. Tradeoffs

### 3.1 路由数据获取用 TanStack Router `loader` vs 组件内 `useQuery`

- **备选 A：** 路由 `loader` 在导航时预取实例数据，组件内同步消费。
- **备选 B（采用）：** 组件内 `useQuery` + `coreApi.GET('/instances/{instance_id}')`，`enabled: !!instanceId`。
- **取舍：** A 的优点是导航即可见数据、无 loading 闪烁；缺点是 loader 错误处理需在路由层，且与 React Query 缓存集成需额外 `ensureQueryData`。B 的优点是错误/加载态在组件内统一处理、与未来子 Tab 的 `useInstanceObservability` hooks 共享同一 QueryClient 缓存；缺点是首次进入有 loading 态。
- **选择理由：** 子 Tab（LogsTab 等）后续会用 React Query 拉观测数据，壳层也用 React Query 拉实例元数据，共享缓存和错误处理更一致。loading 态用 `Skeleton` 兑现 UX §6 即可。

### 3.2 Tab 切换用 URL `?tab=` 深链 vs 本地 state

- **备选 A：** 本地 `useState<ObservabilityTabId>` 管理 active tab。
- **备选 B（采用）：** URL `?tab=` 深链 + `useSearch` + `navigate` 同步。
- **取舍：** A 更简单但无法深链分享；B 支持深链、刷新保持 tab、浏览器前进后退。UX §2.1 明确要求深链，AC #7 也要求。
- **选择理由：** AC 强制深链。代价是 Tab 切换需 `navigate({ to: '.', search: { tab } })`，略多代码但符合 UX。

### 3.3 `resolveTabFromQuery` 放在 config 模块 vs 壳层组件

- **备选 A：** 放壳层 route.tsx 内联。
- **备选 B（采用）：** 放 `observabilityTabsConfig.ts` 作为纯函数导出。
- **取舍：** A 更就近但不可复用；B 可被未来子 Tab 或测试单独引用。
- **选择理由：** 该函数依赖 `INSTANCE_OBSERVABILITY_TAB_CONFIG` 常量，与 config 模块内聚；后续 Tab 组件可能也需要判定可见性（如终端 Tab 检查当前 kind 是否支持 terminal）。纯函数易测试。

## 4. Open Questions

### 4.1 `?tab=` 深链在 Phase 1 是否需要 SSR/直链命中渲染

- **假设：** UX §8.4 / SPEC §11.3 把深链标为「可选增强」，但 AC #7 要求深链。当前实现已落地深链，但未验证 SSR 场景（本 SPA 无 SSR）。若未来引入 SSR，需确认 `validateSearch` 在服务端也能正确解析 `?tab=`。
- **需确认：** 是否有 SSR 需求？若无则当前 SPA 深链已满足。

### 4.2 实例详情路由路径 `compute/instances/$instanceId` 的最终确认

- **背景：** UX §1.1 和 SPEC §11.1 均标注 `[Assumption]`：实例详情路由 `compute/instances/$instanceId` 尚未落地，需与 `container-instance-management.md` 对齐。
- **当前实现：** 已按 `compute/instances/$instanceId` 落地，与 UX/SPEC 假设一致。
- **需确认：** `container-instance-management.md` 是否定义了不同路径？若有冲突需调整 route 文件位置。

### 4.3 kind × Tab 矩阵后续是否需要从后端动态获取

- **背景：** SPEC §3.3 明确「首期 hardcode」，UX §4.1 也说「首期 hardcode 上表」。当前实现 hardcode 在 `observabilityTabsConfig.ts`。
- **需确认：** 后续是否有 plan 让 Console 从 Core API 动态拉取 kind → capability 映射？若有，`INSTANCE_OBSERVABILITY_TAB_CONFIG` 需改为从 API 返回值构建，但当前 API 无此端点。

### 4.4 deleted 状态的「整页 Empty」vs「详情只读」

- **背景：** AC #6 允许「整页 Empty『实例已删除』或详情只读」两种方案。
- **当前实现：** 采用整页 `Alert`（`theme=warning`）+ 文案「实例已删除，观测数据不可用」，不渲染任何 Tab。
- **需确认：** 是否需要保留面包屑和 PageHeader（只读），还是整页 Empty（无壳）？当前保留了 Layout 框架但内容区只显示 Alert。

### 4.5 lint 工具链缺失

- **背景：** 项目全仓库无 ESLint 配置文件和 `@typescript-eslint/parser`，`pnpm lint` 脚本无法执行。这是 pre-existing 项目级问题，非本批次引入。
- **需确认：** 是否应在独立 issue 中为 Console 项目补齐 ESLint 配置？按 Karpathy 原则三，本批次不越界添加全局 lint 配置。

## Verification

```bash
cd repo/frontends/console && npx tsc --noEmit           # EXIT:0
cd repo && make validate-architecture                    # passed
cd repo && git diff --check                              # passed
cd repo/frontends/console && pnpm dev                    # Vite 启动成功
# 浏览器运行时验证（临时页面，验证后已清理）：
#   import 观察 observabilityTabsConfig.ts → 执行全 9 种 kind 矩阵断言无错
#   执行 6 组 ?tab= 深链回退断言无错
#   执行 container/vm/sandbox 差异断言无错
```

## 备注

- 本批次是 issue-003，对应 SPEC §2.4.1 完整文件清单的第一个子任务（壳层 + 上下文）。子 Tab 组件（LogsTab/EventsTab/MetricsTab/...）属后续 issue。
- `routeTree.gen.ts` 由 TanStack Router 插件自动重新生成，非手工编辑。
- review-it 已通过：发现 1 个可操作问题（`useInstanceContextShim` 冗余封装）已修复，复审 clean。
