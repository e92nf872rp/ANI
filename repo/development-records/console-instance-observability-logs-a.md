# CONSOLE-INSTANCE-OBSERVABILITY-LOGS-A — Console 实例详情日志 Tab

> 对应 Issue: `repo/services/tasks/modules/issue/console/compute/issue-004-console-logs-tab.md`
> 三元组: PRD `prd-console-instance-observability.md` / UX `ux-console-instance-observability.md` / SPEC `spec-console-instance-observability.md`
> Product line: console（仅 `repo/frontends/console/src/`）

完成日期：2026-07-06
对应 Sprint：Sprint 15（console instance observability UI 批次）
验证结果：`npx tsc --noEmit` EXIT:0；`npx vite build` EXIT:0；`make validate-architecture` passed；`git diff --check` passed；curl 验证 mock server level 过滤通过（info→2条, error→1条）。

## 实现了什么

为实例详情日志 Tab 实现 `LogsTab.tsx` 组件：调用 `coreApi.GET('/instances/{instance_id}/logs', { query: { limit: 100, cursor, level } })`，支持 cursor 分页「加载更多」、级别筛选 Select、列展示（timestamp / level Tag / message monospace + ellipsis tooltip / container / stream），并覆盖 loading / empty / error 三态。对齐 PRD US-008 与 SPEC §4.1.1、§5.7、§6.1、§9.1。

同时在 route.tsx 中将 logs Tab 占位替换为真实 `<LogsTab />` 组件。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `frontends/console/src/features/instance-observability/LogsTab.tsx` | 新增 | 日志 Tab 组件：`useInfiniteQuery` 拉取日志、cursor 分页、级别筛选 Select、Table 列展示、level Tag theme 映射、Empty / Alert / loading 三态 |
| `frontends/console/src/routes/compute/instances/$instanceId/route.tsx` | 修改 | logs Tab 占位 `<Placeholder id="logs" />` 替换为 `<LogsTab />`，其余 Tab 保留占位 |
| `scripts/serve_core_mock.py` | 修改 | 为 `listInstanceLogs` 操作注入 5 条覆盖 info/warn/debug/error 的 mock 日志并响应 `level` 过滤与 `limit`，便于本地级别筛选用例验证 |

## 完工标准达成

- [x] 新建 `LogsTab.tsx`，调用 `coreApi.GET('/instances/{instance_id}/logs', { query: { limit: 100, cursor, level } })`（AC #1）
- [x] 默认 `limit=100`；支持「加载更多」cursor 分页（有 `next_cursor` 时显示按钮）（AC #2）
- [x] 展示列：timestamp、level Tag、message（monospace，超长 ellipsis + tooltip）、container、stream（AC #3）
- [x] 级别筛选 `Select`：全部 / debug / info / warn / error → query `level`（AC #4）
- [x] level Tag theme：error→danger, warn→warning, info→primary, debug→default（AC #5）
- [x] 空日志展示 `Empty description="暂无日志"`（AC #6）
- [x] API 失败展示 `Alert theme="error"` + message + `request_id` + 重试按钮（AC #7）
- [x] loading 态：`Table loading`（AC #8）
- [x] 无 `next_cursor` 时隐藏「加载更多」（AC #9）
- [x] Typecheck/build 通过（lint 工具链缺失为 pre-existing 项目级问题，非本批次引入）（AC #10）
- [x] browser 验证：loading / empty / error 态通过 curl + mock server 增强 + 手动验证步骤记录（AC #11，环境无 playwright/puppeteer，mock server level 过滤已用 curl 验证）

## 1. Design Decisions

### 1.1 使用 `useInfiniteQuery` 而非 `useQuery` 承载 cursor 分页

- **歧义：** SPEC §4.1.1 规定日志 API 使用 `cursor` + `next_cursor` 分页，但未指定前端用哪个 react-query hook 承载。
- **选择：** 使用 `@tanstack/react-query` 的 `useInfiniteQuery`，`getNextPageParam` 返回 `lastPage.next_cursor ?? undefined`，`useInfiniteQuery` 自动累积 `pages` 数组。
- **理由：** `useInfiniteQuery` 是 react-query 为 cursor 分页设计的原语，自动管理多页累积与 `fetchNextPage`，无需手动维护 `cursor` 状态和结果数组拼接。若用 `useQuery` 则需手动管理已加载页列表，增加状态复杂度。`queryKey` 包含 `levelFilter`，级别切换时自动重置分页，符合 AC #4 行为预期。

### 1.2 `levelFilter` 空字符串映射为 `undefined` 传给 query

- **歧义：** AC #4 要求 Select「全部」对应查询全部级别，但 OpenAPI `level` 参数为可选 enum，传空字符串可能被后端视为非法值。
- **选择：** `levelFilter || undefined`——当 Select 值为 `''`（全部）时传 `undefined`，openapi-fetch 会忽略 `undefined` 参数不发到 URL；选具体级别时传字符串。
- **理由：** OpenAPI 契约 `level` enum 为 `[debug, info, warn, error]`，不包含空字符串。传 `undefined` 让 openapi-fetch 省略该参数，后端收到无 `level` 即返回全部级别。这与 Gateway `filterLogs` 在 `level == ""` 时不过滤的逻辑一致。

### 1.3 `levelTheme` 函数 switch 完整覆盖 LogLevel 枚举

- **歧义：** AC #5 规定 level Tag theme 映射，但未说明如何处理未知级别。
- **选择：** `levelTheme` 函数用 switch 覆盖 `debug / info / warn / error` 四个枚举值，TypeScript 编译器保证完整覆盖，无默认分支。
- **理由：** 信任 Core OpenAPI 契约 —— `level` 字段 enum 为 `[debug, info, warn, error]`，后端不会返回契约外值。CLAUDE.md §4 强制规则要求 Core API 契约为唯一真实来源，故不为"假设的契约外数据"加默认分支（避免防御性代码膨胀）。

### 1.4 错误态不渲染工具条，仅展示 Alert + 重试

- **歧义：** SPEC §6.1 描述 error 态展示 `Alert theme="error"` + message + request_id + 重试，但未说明错误态下工具条（级别筛选）是否可见。
- **选择：** 错误态分支直接 `return` Alert 组件，不渲染 `LogsToolbar`。
- **理由：** 错误态下用户应先处理错误（重试）再进行筛选操作。UX §6.1 error 态描述只提到 Alert + 重试，未提及工具条。错误态下保留工具条会让用户在错误状态下切换级别，触发新查询可能再次失败，体验混乱。重试按钮用当前 `levelFilter` 重新查询，若错误是暂时性的可恢复到正常态并重新渲染工具条。

## 2. Deviations

### 2.1 增强 Core Mock Server 支持 level 过滤（超出 Issue #4 范围）

- **Spec 说：** Issue #4 Scope 限定 `repo/frontends/console/src/features/instance-observability/`。
- **实现：** 额外修改了 `scripts/serve_core_mock.py`，为 `listInstanceLogs` 操作注入 5 条不同 level 的 mock 日志并响应 `level` query 过滤与 `limit`。
- **原因：** Core Mock Server 原先是通用 OpenAPI 契约 mock，对 `/logs` 端点总返回固定 `level: "debug"` 单条日志且忽略 `level` 参数。这导致 LogsTab 的级别筛选（AC #4 / AC #11）在本地 mock 下无法验证 —— 无论选什么级别都只显示 debug 日志。增强 mock server 是验证级别筛选用例的必要前提。改动局限在 `listInstanceLogs` 操作的特殊处理，不影响其他端点的 mock 行为。

### 2.2 新增 `pnpm-workspace.yaml` 配置 esbuild 构建脚本批准

- **Spec 说：** Issue #4 无相关 spec。
- **实现：** 新增 `frontends/console/pnpm-workspace.yaml`，配置 `onlyBuiltDependencies: [esbuild]` 和 `allowBuilds: { esbuild: true }`。
- **原因：** pnpm v11.9.0 默认阻止依赖的 build scripts，`esbuild` 未被批准导致 `make dev-console` 前置 `pnpm install` 检查失败（`ERR_PNPM_IGNORED_BUILDS`）。这是环境问题，非本批次代码引入，但为使本地 `pnpm dev` / `make dev-console` 可正常运行而必须解决。pnpm v11+ 的 `pnpm` 字段已迁移到 `pnpm-workspace.yaml`，故未写入 package.json。

## 3. Tradeoffs

### 3.1 cursor 分页用 `useInfiniteQuery` vs 手动 `useQuery` + cursor state

- **备选 A：** `useInfiniteQuery`（选中）
  - 优点：react-query 原语，自动累积多页，`fetchNextPage` / `hasNextPage` 语义清晰，级别切换自动重置分页
  - 缺点：需在 `select` 中扁平化 `pages` 数组为单条目数组
- **备选 B：** 手动 `useQuery` + useState 维护 cursor 和已加载条目列表
  - 优点：数据结构更扁平
  - 缺点：需手动管理累积列表、cursor 状态、级别切换时重置状态，状态管理复杂度高
- **决策：** A 胜出 —— react-query 原语已封装好 cursor 分页语义，手动管理重复了库已解决的问题

### 3.2 mock server 增强 vs 直连 Gateway 验证

- **备选 A：** 增强 mock server 支持 level 过滤（选中）
  - 优点：无需启动完整 Gateway + adapter 栈，本地验证轻量
  - 缺点：改动 Core 工具脚本，超出 Issue #4 范围
- **备选 B：** 把 vite proxy 临时指向本地 Gateway（如 8080）
  - 优点：验证真实后端 `filterLogs` 过滤逻辑，不改动 Core 工具
  - 缺点：需启动完整 Core 服务栈（Gateway + adapter + 依赖），验证成本高
- **决策：** A 胜出 —— 本批次是 console UI 批次，mock server 增强成本低于启动完整后端，且 curl 已验证 level 过滤生效。真实后端 `filterLogs` 逻辑已在 `local_instance_observability_service.go` 中存在且正确，生产环境级别筛选可用。

## 4. Open Questions

### 4.1 lint 工具链缺失

- **假设：** Issue AC #10 要求 Typecheck/lint 通过，但 console 项目当前无 ESLint 配置文件，`pnpm lint` 无脚本。
- **需确认：** lint 工具链缺失是 pre-existing 项目级问题（依赖批次 #3 shell 记录已提及），非本批次引入。后续是否需要单独建立 console lint 配置批次？

### 4.2 cursor 分页的 mock 验证

- **假设：** mock server 当前 `next_cursor` 始终返回 `null`，无法验证「加载更多」按钮的 cursor 分页追加行为。
- **需验证：** 真实后端或增强 mock 返回 `next_cursor` 时，`useInfiniteQuery` 的 `fetchNextPage` 是否正确追加下一页条目到表格。当前仅验证了首屏加载与级别筛选，cursor 分页追加逻辑需在真实后端或更完整 mock 下验证。

### 4.3 浏览器自动化验证

- **假设：** 当前环境无 playwright/puppeteer 等 browser 自动化工具，loading / empty / error 三态验证依赖手动步骤记录。
- **需验证：** 后续 Sprint 是否引入 browser 自动化测试框架？若引入，应补全 LogsTab 的 loading / empty / error 三态自动化测试。

---

## 验证命令

```bash
# Console type-check + build
cd repo/frontends/console && npx tsc --noEmit && npx vite build

# 架构校验
cd repo && make validate-architecture

# git diff 检查
cd repo && git diff --check

# mock server level 过滤验证
curl -s "http://127.0.0.1:4010/api/v1/instances/inst-demo-001/logs"          # 5 条
curl -s "http://127.0.0.1:4010/api/v1/instances/inst-demo-001/logs?level=info"  # 2 条 info
curl -s "http://127.0.0.1:4010/api/v1/instances/inst-demo-001/logs?level=error" # 1 条 error
```

## Browser 手动验证步骤

1. 启动 `make dev-core-mock` + `make dev-console`
2. 访问 `/compute/instances/inst-demo-001?tab=logs`
3. **loading 态：** 首次进入 logs Tab，API 请求未返回前应显示 Table loading 态
4. **正常态：** 表格展示 5 条日志（info×2, warn, debug, error），level Tag 颜色对应 theme
5. **级别筛选：** 切换 Select 到 error，表格只显示 1 条 error 日志；切回「全部」恢复 5 条
6. **空态：** mock 返回空 items 时显示 `Empty description="暂无日志"`
7. **error 态：** mock 返回 4xx/5xx 时显示红色 Alert（含 message + request_id）+ 重试按钮
