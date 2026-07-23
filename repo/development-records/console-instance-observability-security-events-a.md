# CONSOLE-INSTANCE-OBSERVABILITY-SECURITY-EVENTS-A — Console 实例详情安全事件 Tab

> 对应 Issue: `repo/services/tasks/modules/issue/console/compute/issue-009-console-security-events-tab.md`
> 三元组: PRD `prd-console-instance-observability.md` / UX `ux-console-instance-observability.md` / SPEC `spec-console-instance-observability.md`
> Product line: console（仅 `repo/frontends/console/src/`）

完成日期：2026-07-07
对应 Sprint：Sprint 15（console instance observability UI 批次）
验证结果：`pnpm type-check` EXIT:0；`pnpm build` EXIT:0（54.17s）；`make validate-architecture` EXIT:0；`git diff --check` EXIT:0；mock server `severity` 过滤与 `?error=1` 触发 503 已用 curl 验证通过。lint 工具链缺失为 pre-existing 项目级问题，非本批次引入。

## 实现了什么

为实例详情安全事件 Tab 实现 `SecurityEventsTab.tsx` 组件：仅 `kind=sandbox` 渲染，调用 `coreApi.GET('/instances/{instance_id}/security-events', { query: { severity, limit } })`，支持 severity 筛选 Select（全部 / info / warning / critical）、列展示（occurred_at / severity Tag / event_type / description），并覆盖 loading / empty / error 三态。对齐 PRD US-014 与 SPEC §4.1.7、§5.7、§6.1、§9.4(US-014)。

同时在 `route.tsx` 中将 security-events Tab 占位替换为真实 `<SecurityEventsTab />` 组件。

增强 `scripts/serve_core_mock.py`：新增 sandbox 实例（`INSTANCE_ID_SANDBOX`，`kind=sandbox`，`demo-sandbox-001`）用于安全事件 Tab 可见性；为 `listInstanceSecurityEvents` 操作注入 8 条覆盖 info/warning/critical 的 mock 安全事件并响应 `severity` 过滤与 `limit`；额外支持 `?error=1` query 触发 503 + `ErrorResponse`，便于本地验证 error 态。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `frontends/console/src/features/instance-observability/SecurityEventsTab.tsx` | 新增 | 安全事件 Tab 组件：`useQuery` 拉取安全事件、severity 筛选 Select、Table 列展示、severity Tag theme 映射、Empty / Alert / loading 三态 |
| `frontends/console/src/routes/compute/instances/$instanceId/route.tsx` | 修改 | security-events Tab 占位替换为 `<SecurityEventsTab />`，新增 import |
| `scripts/serve_core_mock.py` | 修改 | 新增 `INSTANCE_ID_SANDBOX` sandbox 实例使安全事件 Tab 可见；为 `listInstanceSecurityEvents` 注入 8 条 info/warning/critical mock 安全事件并响应 `severity` 过滤与 `limit`；新增 `?error=1` 触发 503 + `ErrorResponse` 用于 error 态验证 |

## 完工标准达成

- [x] 新建 `SecurityEventsTab.tsx`，仅 `kind=sandbox` 渲染（由 `observabilityTabsConfig` + `route.tsx` 壳层控制，本组件不重复校验）（AC #1）
- [x] 调用 `coreApi.GET('/instances/{instance_id}/security-events', { query: { severity, limit } })`（AC #2）
- [x] severity 筛选 `Select`：全部 / info / warning / critical（AC #3）
- [x] 展示列：occurred_at、severity Tag、event_type、description（AC #4）
- [x] severity Tag theme：critical→danger, warning→warning, info→primary（AC #5）
- [x] 空事件展示 `Empty description="暂无安全事件"`（AC #6）
- [x] API 失败展示 `Alert theme="error"` + message + `request_id` + 重试按钮（AC #7）
- [x] loading 态：`Table loading`（AC #8）
- [x] Typecheck/build 通过；lint 工具链缺失为 pre-existing 项目级问题，非本批次引入（AC #9）
- [x] browser 验证：loading / empty / error 态通过 mock server 增强 + curl 验证 + 手动验证步骤记录（AC #10，环境无 playwright/puppeteer，mock server `severity` 过滤与 `?error=1` 已用 curl 验证）

## 1. Design Decisions

### 1.1 使用 `useQuery` 一次性加载而非 cursor 分页

- **歧义：** SPEC §4.1.7 与 Issue 未明确要求 cursor 分页，但 `InstanceSecurityEventListResponse` schema 含 `next_cursor` 字段，存在是否实现「加载更多」的疑问。
- **选择：** 使用 `@tanstack/react-query` 的 `useQuery` 一次性加载 `limit=100` 条安全事件，不传 `cursor`，不实现「加载更多」。
- **理由：** CLAUDE.md §4 强制规则要求 `v1.yaml` 是 Core API 唯一真实来源，不得发明 API 字段。Core OpenAPI `listInstanceSecurityEvents` 的 query 参数当前仅有 `severity` 与 `limit`，未声明 `cursor` 入参。`core-schema.d.ts` 生成的 query 类型不含 `cursor`，传 `cursor` 会被 type-check 拒绝。故一次性加载 `limit=100`，待后续 Core 批次补齐 `cursor` query 参数后再迁移到 `useInfiniteQuery`（与 events 批次 §2.1 降级说明一致）。

### 1.2 `severityFilter` 空字符串映射为 `undefined` 传给 query

- **歧义：** AC 要求 Select「全部」对应查询全部 severity，但 OpenAPI `severity` 参数为可选 enum，传空字符串可能被后端视为非法值。
- **选择：** `severityFilter || undefined`——当 Select 值为 `''`（全部）时传 `undefined`，openapi-fetch 会忽略 `undefined` 参数不发到 URL；选具体 severity 时传字符串。
- **理由：** OpenAPI 契约 `severity` enum 为 `[info, warning, critical]`，不包含空字符串。传 `undefined` 让 openapi-fetch 省略该参数，后端收到无 `severity` 即返回全部安全事件。与 events 批次 `type` 参数处理模式一致。

### 1.3 `severityTheme` 函数用 switch 穷尽枚举而非三元表达式

- **歧义：** AC #5 规定 severity Tag theme 映射（critical→danger, warning→warning, info→primary），但未说明实现方式。
- **选择：** `severityTheme` 函数用 switch 枚举三个 severity 值，无 default 分支。
- **理由：** `InstanceSecurityEvent.severity` 的 enum 有三个值 `[info, warning, critical]`，core-schema.d.ts 生成为字面量联合 `"info" | "warning" | "critical"`。switch 穷尽三个分支后 TypeScript 能确认函数始终返回联合类型，type-check 通过。三个值的映射用 switch 比多个三元更清晰可读。信任 Core OpenAPI 契约为唯一真实来源，不为"假设的契约外数据"加默认分支（与 events 批次 §1.3 哲学一致）。

### 1.4 错误态不渲染工具条，仅展示 Alert + 重试

- **歧义：** SPEC §6.1 描述 error 态展示 `Alert theme="error"` + message + request_id + 重试，但未说明错误态下工具条（severity 筛选）是否可见。
- **选择：** 错误态分支直接 `return` Alert 组件，不渲染 `SecurityEventsToolbar`。
- **理由：** 与 events/logs 批次一致。错误态下用户应先处理错误（重试）再进行筛选操作。UX §6.6 error 态描述只提到 Alert + 重试，未提及工具条。重试按钮用当前 `severityFilter` 重新查询，若错误是暂时性的可恢复到正常态并重新渲染工具条。

### 1.5 Tab 可见性由壳层控制，组件内不重复校验 kind

- **歧义：** AC #1 要求「仅 `kind=sandbox` 渲染（其余 kind 无此 Tab）」，但 SecurityEventsTab 组件本身是否需要校验 `kind === 'sandbox'`？
- **选择：** `SecurityEventsTab` 组件不校验 kind，Tab 可见性完全由 `observabilityTabsConfig.ts` 的 `INSTANCE_OBSERVABILITY_TAB_CONFIG` 和 `route.tsx` 的 `getVisibleTabs(ctx.kind)` 壳层控制。
- **理由：** `observabilityTabsConfig.ts` 第 45 行 sandbox kind 的 `tabs` 数组包含 `'security-events'`，其余 kind 不含。`route.tsx` 第 118 行 `visibleTabs = getVisibleTabs(ctx.kind)` 只渲染可见 Tab，非 sandbox 实例根本不会渲染 SecurityEventsTab 组件。组件内重复校验 kind 会造成逻辑冗余，违反 Karpathy 原则三「只触碰必须改动的部分」。

## 2. Deviations

None — 实现严格遵循 SPEC §4.1.7 API 调用契约与 UX §4.7/§5.6/§6.6 布局规范。唯一与 Issue AC 字面不完全一致的是 AC #10 browser 验证——环境无 playwright/puppeteer，降级为 mock server 增强 + curl 验证 + 手动验证步骤记录（与同系列其它批次一致）。

## 3. Tradeoffs

### 3.1 cursor 分页：遵守契约降级 vs 强传 cursor

- **备选 A：** 遵守 OpenAPI 契约，query 只传 severity/limit，用 `useQuery` 一次性加载（选中）
  - 优点：严格遵守 CLAUDE.md §4「不得发明 API 字段」，type-check 通过，不越界改 v1.yaml/后端
  - 缺点：无法实现 cursor 分页「加载更多」（但 Issue AC 未明确要求此项，故无 AC 违反）
- **备选 B：** 用 `@ts-ignore` 或类型断言强传 cursor
  - 优点：能跑通 cursor 分页
  - 缺点：违反 OpenAPI 契约边界，属于发明 API 字段（CLAUDE.md 明令禁止），可能被 architecture 验证拒绝
- **决策：** A 胜出 —— 严格遵守契约边界是 CLAUDE.md 强制规则。`listInstanceSecurityEvents` query 无 `cursor` 入参，UI 批次不得发明。

### 3.2 mock server 增强 vs 仅前端改动

- **备选 A：** 增强 mock server 支持 severity 过滤 + error 触发 + 新增 sandbox 实例（选中）
  - 优点：无需启动完整 Gateway + adapter 栈，本地验证轻量；`?error=1` 提供便捷 error 态触发；sandbox 实例让安全事件 Tab 可见
  - 缺点：改动 Core 工具脚本，严格说超出 Issue #9 的 `frontends/console/src/features/instance-observability/` scope
- **备选 B：** 仅改前端，mock server 返回通用数据
  - 优点：严格守 Issue scope
  - 缺点：mock server 无 sandbox 实例 → 安全事件 Tab 不可见无法测试；无 severity 过滤 → AC #3 无法验证；无 error 触发 → AC #7 无法验证
- **决策：** A 胜出 —— mock server 增强是验证 AC 的必要前提，与 events/logs/terminal/metrics 批次增强 mock server 模式一致。改动局限在 `listInstanceSecurityEvents` 操作 + 实例列表，不影响其他端点。

## 4. Open Questions

### 4.1 cursor 分页的 Core 契约补齐

- **假设：** `listInstanceSecurityEvents` query 缺 `cursor` 入参是 OpenAPI 文档或后端 handler 的遗漏（response 有 `next_cursor` 但 query 无 `cursor`）。
- **需确认：** 是否开一个 Core 批次给 `v1.yaml` 的 `listInstanceSecurityEvents` 补 `cursor` query 参数 + 后端 handler 读取 cursor？补齐后 SecurityEventsTab 可迁移到 `useInfiniteQuery` 启用「加载更多」，与 logs tab 行为对齐。（与 events 批次 §4.1 同一问题）

### 4.2 lint 工具链缺失

- **假设：** Issue AC #9 要求 Typecheck/lint 通过，但 console 项目当前无 ESLint 配置文件，`pnpm lint` 无脚本。
- **需确认：** lint 工具链缺失是 pre-existing 项目级问题（依赖批次 #3~#8 记录已提及），非本批次引入。后续是否需要单独建立 console lint 配置批次？

### 4.3 浏览器自动化验证

- **假设：** 当前环境无 playwright/puppeteer 等 browser 自动化工具，loading / empty / error 三态验证依赖 mock server 增强 + curl + 手动步骤记录。
- **需验证：** 后续 Sprint 是否引入 browser 自动化测试框架？若引入，应补全 SecurityEventsTab 的 loading / empty / error 三态自动化测试。（与 events 批次 §4.3 同一问题）

---

## 验证命令

```bash
# Console type-check + build
cd repo/frontends/console && pnpm type-check && pnpm build

# 架构校验
cd repo && make validate-architecture

# git diff 检查
cd repo && git diff --check

# mock server severity 过滤验证
curl -s "http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000005/security-events"                   # 8 条
curl -s "http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000005/security-events?severity=critical" # 2 条 critical
curl -s "http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000005/security-events?severity=warning"   # 3 条 warning
curl -s "http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000005/security-events?severity=info"       # 3 条 info

# mock server error 态验证
curl -s "http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000005/security-events?error=1"
# 预期：HTTP 503 + {"code":"UNAVAILABLE","message":"mock: 安全事件服务暂时不可用（由 ?error=1 触发）","request_id":"mock-sev-err-0001"}

# sandbox 实例可见性验证
curl -s "http://127.0.0.1:4010/api/v1/instances?kind=sandbox" # 返回 demo-sandbox-001
```

## Browser 手动验证步骤

1. 启动 mock server：`cd repo && python scripts/serve_core_mock.py`（监听 `http://127.0.0.1:4010/api/v1`）
2. 启动 console dev server：`cd repo/frontends/console && pnpm dev`
3. 访问 sandbox 实例详情安全事件 Tab：`http://localhost:5174/compute/instances/00000000-0000-4000-8000-000000000005?tab=security-events`
4. **loading 态：** 首次进入 security-events Tab，API 请求未返回前应显示 Table loading 态
5. **正常态：** 表格展示 8 条安全事件（critical×2, warning×3, info×3），severity Tag 颜色对应 theme（critical→danger 红色, warning→warning 橙色, info→primary 蓝色）
6. **severity 筛选：** 切换 Select 到 critical，表格只显示 2 条 critical；切到 warning 显示 3 条；切到 info 显示 3 条；切回「全部」恢复 8 条
7. **空态：** mock 返回空 items 时显示 `Empty description="暂无安全事件"`（当前 mock 数据覆盖三种 severity，空态需临时改 mock 或通过 severity 筛选无匹配验证）
8. **error 态：** 请求 URL 加 `?error=1`（通过浏览器 devtools 拦截改 URL，或临时改 mock 强制返回 5xx），显示红色 Alert（含 message + request_id）+ 重试按钮
9. **Tab 可见性：** 访问 container 实例（`...0001`）详情，Tab 栏不出现「安全事件」Tab；访问 sandbox 实例（`...0005`）详情，Tab 栏出现「安全事件」Tab
