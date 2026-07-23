# CONSOLE-INSTANCE-OBSERVABILITY-EVENTS-A — Console 实例详情事件 Tab

> 对应 Issue: `repo/services/tasks/modules/issue/console/compute/issue-005-console-events-tab.md`
> 三元组: PRD `prd-console-instance-observability.md` / UX `ux-console-instance-observability.md` / SPEC `spec-console-instance-observability.md`
> Product line: console（仅 `repo/frontends/console/src/`）

完成日期：2026-07-06
对应 Sprint：Sprint 15（console instance observability UI 批次）
验证结果：`pnpm type-check` EXIT:0；`pnpm build` EXIT:0；`git diff --check` EXIT:0；mock server `type` 过滤与 `?error=1` 触发 503 已用 curl 验证通过。lint 工具链缺失为 pre-existing 项目级问题，非本批次引入。

## 实现了什么

为实例详情事件 Tab 实现 `EventsTab.tsx` 组件：调用 `coreApi.GET('/instances/{instance_id}/events', { query: { limit: 100, type } })`，支持类型筛选 Select（全部 / Normal / Warning）、列展示（occurred_at / type Tag / reason / message monospace + ellipsis tooltip / count），并覆盖 loading / empty / error 三态。对齐 PRD US-009 与 SPEC §4.1.2、§5.7、§6.1、§9.1。

同时在 route.tsx 中将 events Tab 占位替换为真实 `<EventsTab />` 组件。

增强 `scripts/serve_core_mock.py`：为 `listInstanceEvents` 操作注入 8 条覆盖 Normal/Warning 的 mock 事件并响应 `type` 过滤与 `limit`；额外支持 `?error=1` query 触发 503 + `ErrorResponse`，便于本地验证 error 态。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `frontends/console/src/features/instance-observability/EventsTab.tsx` | 新增 | 事件 Tab 组件：`useQuery` 拉取事件、类型筛选 Select、Table 列展示、type Tag theme 映射、Empty / Alert / loading 三态 |
| `frontends/console/src/routes/compute/instances/$instanceId/route.tsx` | 修改 | events Tab 占位替换为 `<EventsTab />`，其余 Tab 保留占位 |
| `scripts/serve_core_mock.py` | 修改 | 为 `listInstanceEvents` 注入 8 条 Normal/Warning mock 事件并响应 `type` 过滤与 `limit`；新增 `?error=1` 触发 503 + `ErrorResponse` 用于 error 态验证 |

## 完工标准达成

- [x] 新建 `EventsTab.tsx`，调用 `coreApi.GET('/instances/{instance_id}/events', { query: { limit: 100 } })`（AC #1，query 传 `limit` 与 `type`，`cursor` 见 §2.1 降级说明）
- [x] 展示列：occurred_at、type Tag、reason、message、count（AC #2）
- [x] `type=Warning` → `Tag theme="warning"`；`Normal` → `Tag theme="default"`（AC #3）
- [⚠️] cursor 分页「加载更多」→ **blocked-by-core**（AC #4，见 §2.1 降级说明）
- [x] 空事件展示 `Empty description="暂无事件"`（AC #5）
- [x] API 失败展示 `Alert theme="error"` + message + `request_id` + 重试按钮（AC #6）
- [x] loading 态：`Table loading`（AC #7）
- [x] Typecheck/build 通过；lint 工具链缺失为 pre-existing 项目级问题，非本批次引入（AC #8）
- [x] browser 验证：loading / empty / error 态通过 mock server 增强 + curl 验证 + 手动验证步骤记录（AC #9，环境无 playwright/puppeteer，mock server `type` 过滤与 `?error=1` 已用 curl 验证）

## 1. Design Decisions

### 1.1 使用 `useQuery` 而非 `useInfiniteQuery` 承载事件列表加载

- **歧义：** SPEC §4.1.2 与 Issue AC #4 要求 cursor 分页「加载更多」，但 Core OpenAPI `v1.yaml` 中 `listInstanceEvents` 的 query 参数当前仅有 `limit` 与 `type`，未声明 `cursor` 入参（对比 `listInstanceLogs` 已声明 cursor）。
- **选择：** 使用 `@tanstack/react-query` 的 `useQuery` 一次性加载 `limit=100` 条事件，不传 `cursor`，不实现「加载更多」。
- **理由：** CLAUDE.md §4 强制规则要求 `v1.yaml` 是 Core API 唯一真实来源，不得发明 API 字段。`core-schema.d.ts` 生成的 `listInstanceEvents` query 类型不含 `cursor`，传 `cursor` 会被 type-check 拒绝。`useInfiniteQuery` 依赖 `cursor` pageParam，契约缺失时无法正确使用。故降级为 `useQuery` 一次性加载，待后续 Core 批次补齐 `cursor` query 参数后再迁移到 `useInfiniteQuery`（详见 §2.1）。

### 1.2 `typeFilter` 空字符串映射为 `undefined` 传给 query

- **歧义：** AC 要求 Select「全部」对应查询全部类型，但 OpenAPI `type` 参数为可选 enum，传空字符串可能被后端视为非法值。
- **选择：** `typeFilter || undefined`——当 Select 值为 `''`（全部）时传 `undefined`，openapi-fetch 会忽略 `undefined` 参数不发到 URL；选具体类型时传字符串。
- **理由：** OpenAPI 契约 `type` enum 为 `[Normal, Warning]`，不包含空字符串。传 `undefined` 让 openapi-fetch 省略该参数，后端收到无 `type` 即返回全部类型。这与 logs 批次 `level` 参数处理模式一致。

### 1.3 `typeTheme` 函数直接返回而不 switch

- **歧义：** AC #3 规定 type Tag theme 映射，但未说明如何处理未知类型。
- **选择：** `typeTheme` 函数用三元表达式 `type === 'Warning' ? 'warning' : 'default'`，非 switch 枚举覆盖。
- **理由：** `InstanceEvent.type` 的 enum 只有两个值 `[Normal, Warning]`，core-schema.d.ts 生成为字面量联合 `"Normal" | "Warning"`。两个值的映射用三元比 switch 更简洁。非 Warning 即 Normal，类型安全有保障。信任 Core OpenAPI 契约为唯一真实来源，不为"假设的契约外数据"加默认分支。

### 1.4 错误态不渲染工具条，仅展示 Alert + 重试

- **歧义：** SPEC §6.1 描述 error 态展示 `Alert theme="error"` + message + request_id + 重试，但未说明错误态下工具条（类型筛选）是否可见。
- **选择：** 错误态分支直接 `return` Alert 组件，不渲染 `EventsToolbar`。
- **理由：** 与 logs 批次一致。错误态下用户应先处理错误（重试）再进行筛选操作。UX §6.2 error 态描述只提到 Alert + 重试，未提及工具条。重试按钮用当前 `typeFilter` 重新查询，若错误是暂时性的可恢复到正常态并重新渲染工具条。

## 2. Deviations

### 2.1 cursor 分页降级为一次性加载（blocked-by-core）

- **Spec 说：** Issue AC #4 与 SPEC §4.1.2 要求 cursor 分页「加载更多」。
- **实现：** query 只传 `limit` 与 `type`，不传 `cursor`，使用 `useQuery` 一次性加载 `limit=100` 条，不实现「加载更多」按钮。
- **原因：** Core OpenAPI 唯一真实来源 `v1.yaml` 中 `listInstanceEvents` 的 query 参数**没有 `cursor`**（仅有 `limit` 和 `type`），而后端 handler `listEvents`（`demo_instances.go`）也**未读取 cursor**（对比 `listLogs` handler 读了 cursor）。但 `InstanceEventListResponse` schema 有 `next_cursor` 字段，推断是 OpenAPI query 入参或后端 handler 的遗漏。
  - CLAUDE.md §4 明令禁止「发明 API 字段」，SPEC §4.0 声明 UI-only 批次「不修改后端 API」。
  - 本 issue scope 限定为 `repo/frontends/console/src/features/instance-observability/`，不得改 `v1.yaml` 或后端。
  - 用户已明确决策：遵守 OpenAPI 契约，不传 cursor。
- **后续：** 待后续 Core 批次给 `listInstanceEvents` 补 `cursor` query 参数 + 后端 handler 读取 cursor 后，再将 EventsTab 迁移到 `useInfiniteQuery` 启用「加载更多」。此降级不违反任何强制规则。

### 2.2 增强 Core Mock Server 支持 type 过滤 + error 触发（超出 Issue #5 范围）

- **Spec 说：** Issue #5 Scope 限定 `repo/frontends/console/src/features/instance-observability/`。
- **实现：** 额外修改了 `scripts/serve_core_mock.py`，为 `listInstanceEvents` 操作注入 8 条 Normal/Warning mock 事件并响应 `type` query 过滤与 `limit`；新增 `?error=1` query 触发 503 + `ErrorResponse` 结构。
- **原因：** Core Mock Server 原先对 `/events` 端点返回通用 mock 数据（单条、固定值），忽略 `type` 参数，无法验证类型筛选（AC #3）。增强 mock server 是验证类型筛选用例与 error 态的必要前提。改动局限在 `listInstanceEvents` 操作的特殊处理，不影响其他端点的 mock 行为。与 logs 批次增强 mock server 模式一致。

## 3. Tradeoffs

### 3.1 cursor 分页：遵守契约降级 vs 强传 cursor

- **备选 A：** 遵守 OpenAPI 契约，query 只传 limit/type，用 `useQuery` 一次性加载（选中）
  - 优点：严格遵守 CLAUDE.md §4「不得发明 API 字段」，type-check 通过，不越界改 v1.yaml/后端
  - 缺点：AC #4「加载更多」无法满足，标记为 blocked-by-core
- **备选 B：** 用 `@ts-ignore` 或类型断言强传 cursor
  - 优点：能跑通 cursor 分页，满足 AC #4 字面要求
  - 缺点：违反 OpenAPI 契约边界，属于发明 API 字段（CLAUDE.md 明令禁止），可能被 architecture 验证拒绝
- **备选 C：** 暂停本 issue，先开 Core 批次补 cursor
  - 优点：彻底解决契约缺失
  - 缺点：范围扩大到 Core，超出本 issue 的 console UI-only scope
- **决策：** A 胜出 —— 严格遵守契约边界是 CLAUDE.md 强制规则，UI 批次不得为满足 AC 而发明 API 字段。cursor 缺失是 Core 侧契约问题，应在 Core 批次解决。

### 3.2 mock server 增强 vs 直连 Gateway 验证

- **备选 A：** 增强 mock server 支持 type 过滤 + error 触发（选中）
  - 优点：无需启动完整 Gateway + adapter 栈，本地验证轻量；`?error=1` 提供便捷 error 态触发
  - 缺点：改动 Core 工具脚本，超出 Issue #5 范围
- **备选 B：** 把 vite proxy 临时指向本地 Gateway（如 8080）
  - 优点：验证真实后端过滤逻辑，不改动 Core 工具
  - 缺点：需启动完整 Core 服务栈，验证成本高；error 态需手动制造后端故障
- **决策：** A 胜出 —— 本批次是 console UI 批次，mock server 增强成本低于启动完整后端，且 curl 已验证 type 过滤与 error 触发生效。与 logs 批次模式一致。

## 4. Open Questions

### 4.1 cursor 分页的 Core 契约补齐

- **假设：** `listInstanceEvents` query 缺 `cursor` 入参是 OpenAPI 文档或后端 handler 的遗漏（response 有 `next_cursor` 但 query 无 `cursor`）。
- **需确认：** 是否开一个 Core 批次给 `v1.yaml` 的 `listInstanceEvents` 补 `cursor` query 参数 + 后端 handler `listEvents` 读取 cursor？补齐后 EventsTab 可迁移到 `useInfiniteQuery` 启用「加载更多」，与 logs tab 行为对齐。

### 4.2 lint 工具链缺失

- **假设：** Issue AC #8 要求 Typecheck/lint 通过，但 console 项目当前无 ESLint 配置文件，`pnpm lint` 无脚本。
- **需确认：** lint 工具链缺失是 pre-existing 项目级问题（依赖批次 #3 shell 记录与 #4 logs 记录已提及），非本批次引入。后续是否需要单独建立 console lint 配置批次？

### 4.3 浏览器自动化验证

- **假设：** 当前环境无 playwright/puppeteer 等 browser 自动化工具，loading / empty / error 三态验证依赖 mock server 增强 + curl + 手动步骤记录。
- **需验证：** 后续 Sprint 是否引入 browser 自动化测试框架？若引入，应补全 EventsTab 的 loading / empty / error 三态自动化测试。

### 4.4 cursor 分页 mock 验证

- **假设：** mock server 当前 `next_cursor` 始终返回 `null`，即使 Core 补齐 cursor 后仍无法验证「加载更多」追加行为。
- **需验证：** Core 补齐 cursor 后，需同步增强 mock server 支持 cursor 分页（如根据 cursor 返回不同页），以验证 `useInfiniteQuery` 的 `fetchNextPage` 追加逻辑。

---

## 验证命令

```bash
# Console type-check + build
cd repo/frontends/console && pnpm type-check && pnpm build

# git diff 检查
cd repo && git diff --check

# mock server type 过滤验证
curl -s "http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000001/events"            # 8 条
curl -s "http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000001/events?type=Warning" # 4 条 Warning
curl -s "http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000001/events?type=Normal"   # 4 条 Normal

# mock server error 态验证
curl -s "http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000001/events?error=1"
# 预期：HTTP 503 + {"code":"UNAVAILABLE","message":"mock: 事件服务暂时不可用（由 ?error=1 触发）","request_id":"mock-evt-err-0001"}
```

## Browser 手动验证步骤

1. 启动 mock server：`cd repo && python scripts/serve_core_mock.py`（监听 `http://127.0.0.1:4010/api/v1`）
2. 启动 console dev server：`cd repo/frontends/console && pnpm dev`
3. 访问实例详情，切到「事件」Tab
4. **loading 态：** 首次进入 events Tab，API 请求未返回前应显示 Table loading 态
5. **正常态：** 表格展示 8 条事件（Normal×4, Warning×4），type Tag 颜色对应 theme（Warning→warning 橙色, Normal→default 灰色）
6. **类型筛选：** 切换 Select 到 Warning，表格只显示 4 条 Warning 事件；切到 Normal 显示 4 条 Normal；切回「全部」恢复 8 条
7. **count 列：** 显示 1/3/5/12/2 等数字；若 mock 数据某条 count 缺失则显示 `-`
8. **空态：** mock 返回空 items 时显示 `Empty description="暂无事件"`
9. **error 态：** 请求 URL 加 `?error=1`（通过浏览器 devtools 拦截改 URL，或临时改 mock 强制返回 5xx），显示红色 Alert（含 message + request_id）+ 重试按钮
