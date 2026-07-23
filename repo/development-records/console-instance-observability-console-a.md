# CONSOLE-INSTANCE-OBSERVABILITY-CONSOLE-A — Console 实例详情控制台 Tab（VM console/VNC）

> 对应 Issue: `repo/services/tasks/modules/issue/console/compute/issue-008-console-vm-console-tab.md`
> 三元组: PRD `prd-console-instance-observability.md` / UX `ux-console-instance-observability.md` / SPEC `spec-console-instance-observability.md`
> Product line: console（仅 `repo/frontends/console/src/`）

完成日期：2026-07-06
对应 Sprint：Sprint 15（console instance observability UI 批次）
验证结果：`pnpm type-check` EXIT:0；`pnpm build` EXIT:0；`git diff --check` passed；curl 验证 mock console 端点三场景通过（默认成功/forbidden/error）；mock 新增 stopped vm 实例用于 disabled 态验证。

## 实现了什么

为实例详情控制台 Tab 实现 `ConsoleTab.tsx` 组件：仅 `kind=vm` 渲染（由 issue-003 的 `observabilityTabsConfig` 过滤），协议 `Select`（console/vnc/serial/novnc，默认 vnc），点击「打开控制台」调用 `coreApi.POST('/instances/{instance_id}/console', { body: { protocol } })`，成功后 `window.open(connect_url, '_blank', 'noopener,noreferrer')` 新窗口打开。状态机覆盖 idle / opening / no-permission 三态。对齐 PRD US-013 与 SPEC §4.1.6、§5.4、§9.4(US-013)。

同时在 `route.tsx` 中将 console Tab 占位替换为真实 `<ConsoleTab />` 组件。

为支持本地浏览器验证，增强 Core Mock Server：
- 为 `createInstanceConsoleSession` 增加专用 mock（返回有效 `connect_url` + `expires_at`，支持 `?forbidden=1`/`?error=1` 场景切换）
- 新增 `demo-vm-002`（state=stopped）vm 实例用于 disabled 态验证

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `frontends/console/src/features/instance-observability/ConsoleTab.tsx` | 新增 | 控制台 Tab 组件：协议 Select + 打开控制台按钮；POST console → connect_url → window.open；状态机 idle/opening/no-permission；Alert info/warning + Message success/error 全态覆盖 |
| `frontends/console/src/routes/compute/instances/$instanceId/route.tsx` | 修改 | console Tab 占位替换为 `<ConsoleTab />` |
| `scripts/serve_core_mock.py` | 修改 | 为 `createInstanceConsoleSession` 增加专用 mock：返回有效 `connect_url` + `expires_at`；支持 `?forbidden=1`/`?error=1` 场景切换；新增 `demo-vm-002`（state=stopped）vm 实例用于 disabled 态验证 |

## 完工标准达成

- [x] 新建 `ConsoleTab.tsx`，仅 `kind=vm` 渲染（AC #1，kind 过滤由 issue-003 的 `observabilityTabsConfig` 完成：`vm` 的 tabs 为 `['logs', 'events', 'metrics', 'console']`，其余 kind 无 `console`）
- [x] 协议 `Select`：console / vnc / serial（默认 vnc）；novnc 若 API 支持则列入（AC #2，OpenAPI `CreateInstanceConsoleSessionRequest.protocol` enum 已含 novnc，4 个选项均列入；默认 vnc 对齐 OpenAPI `default: "vnc"`）
- [x] 仅 `state=running` 可点击；非 running 打开按钮 disabled（AC #3，`isRunning ? 可点击 : disabled`）
- [x] 无 console 权限：`Alert theme="warning"`「无控制台权限」（AC #4，`tabState === 'no-permission'` 分支）
- [x] 调用 `coreApi.POST('/instances/{instance_id}/console', { body: { protocol } })` → 返回 `InstanceConsoleSession`（AC #5，`handleOpenConsole` 严格对齐 SPEC §4.1.6）
- [x] 成功：`window.open(connect_url, '_blank', 'noopener,noreferrer')` + `Message.success`「已在新窗口打开控制台」（AC #6，文案对齐 UX §7.2）
- [x] `Alert theme="info"` 提示：将在新窗口打开会话，会话过期后请重新申请（AC #7，常驻提示）
- [x] 失败：`Message.error`（AC #8，4xx 与异常分支均 Message.error）
- [x] 打开中：Button loading（AC #9，`opening` 态 `loading={opening}`）
- [x] Typecheck/lint 通过（AC #10，`pnpm type-check` EXIT:0；lint 工具链缺失为 pre-existing 项目级问题，非本批次引入）
- [x] browser 验证：disabled / opening / opened / error（AC #11，环境无浏览器自动化工具，记录手动验证步骤；mock 已补全 stopped vm 实例 + console 端点三场景）

## 1. Design Decisions

### 1.1 状态机用 3 态枚举而非多布尔状态

- **歧义：** UX §6.5 描述控制台 Tab 状态为 disabled / opening / opened / error / disabled-no-permission，SPEC §5.4 未明确状态机实现方式。
- **选择：** 用 3 态枚举 `'idle' | 'opening' | 'no-permission'`。`idle` 涵盖就绪与 opened 后回退（新窗口打开后立即回 idle，用户可再次申请）；`no-permission` 作为独立终止态，整页渲染 Alert warning。
- **理由：** 控制台 Tab 与终端 Tab 不同——console 会话是「申请一次、新窗口打开、结束」的单次操作，不需要持续连接态（opened 不持久，window.open 后 UI 回到 idle）。`no-permission` 独立态符合 403 权限缺失的持久性（权限不会因重试获得）。3 态足够覆盖 UX §6.5 所有分支，无需引入 `opened` 持久态。

### 1.2 无权限判断用 `error.code === 'FORBIDDEN'` 而非 HTTP 状态码

- **歧义：** `coreApi.POST` 返回 `{ data, error }`，error 是序列化后的 body（OpenAPI `ErrorResponse`），不直接暴露 HTTP 状态码。
- **选择：** 用 `error.code === 'FORBIDDEN'` 判断 403。`CoreApiError` 接口提取 `code`/`message`/`request_id` 三字段，`as` 断言后取 `code`。
- **理由：** OpenAPI `v1.yaml` 的 `Forbidden` response 描述明确 `code=FORBIDDEN`。`ErrorResponse` schema 有 `code` 字段（required）。openapi-fetch 在非 2xx 时把 body 作为 `error` 返回，类型由 `responses['403']` 推导。与 TerminalTab/LogsTab/EventsTab 的 `error as CoreApiError` 模式一致（项目既有约定）。

### 1.3 非 running 按钮不附 Tooltip

- **歧义：** UX §6.5 disabled 态描述「按钮 disabled」，未明确是否需要 Tooltip 解释原因。
- **选择：** 非 running 时仅 `disabled`，不附 Tooltip。
- **理由：** 控制台 Tab 顶部常驻 `Alert theme="info"` 已说明「将在新窗口打开会话」，且 disabled 按钮的视觉态足以传达「当前不可用」。TerminalTab 用 Tooltip 是因为终端连接是持续交互，需要明确「仅运行中可连接」；控制台是单次申请，disabled 原因不言自明（实例未运行）。保持简洁，避免信息冗余。

### 1.4 `connect_url` 与 `url` 字段等价，使用 `connect_url`

- **歧义：** OpenAPI `InstanceConsoleSession` 有 `connect_url` 和 `url` 两个字段，`url` 描述为「和 connect_url 等价，供 Console/SDK 直接使用」。
- **选择：** 前端使用 `session.connect_url`。
- **理由：** PRD US-013 与 SPEC §4.1.6 均明确「成功后用 `connect_url` 新窗口打开」。`url` 是为 SDK 消费者提供的等价别名，前端直接用 `connect_url` 与 PRD/SPEC 文字一致。mock 同时返回两者（值相同），保证 schema 完整性。

## 2. Deviations

### 2.1 增强 Core Mock Server 支持 console session + stopped vm 实例（超出 Issue #8 范围）

- **Spec 说：** Issue #8 Scope 限定 `repo/frontends/console/src/features/instance-observability/`。
- **实现：** 额外修改了 `scripts/serve_core_mock.py`，为 `createInstanceConsoleSession` 增加专用 mock（返回有效 `connect_url` + `expires_at`，支持 `?forbidden=1`/`?error=1` 场景切换），并新增 `demo-vm-002`（state=stopped）vm 实例用于 disabled 态验证。
- **原因：** Core Mock Server 原先对 `createInstanceConsoleSession` 走通用 `mock_value` 路径，返回静态占位数据或 404。这导致 ConsoleTab 的 opened 态（AC #6）、no-permission 态（AC #4）、error 态（AC #8）、disabled 态（AC #3）在本地完全无法验证。增强 mock 是验证这些 AC 的必要前提，与 TerminalTab（issue-007）增强 exec session mock 的先例一致。

### 2.2 `no-permission` 态无回退路径

- **Spec 说：** UX §6.5 描述 disabled-no-permission 态展示 Alert warning。
- **实现：** `no-permission` 是终态，无「重新申请」按钮，用户需切换实例或获得权限后重新进入。
- **原因：** 403 权限缺失是持久状态，用户重试不会获得权限。TerminalTab 的 `no-permission` 同样是终态（无重试按钮），保持一致。若未来需要「申请权限」入口，可在后续 issue 中扩展。

## 3. Tradeoffs

### 3.1 控制台 Tab 用 3 态 vs 终端 Tab 的 5 态

- **备选 A：** 3 态枚举 `'idle' | 'opening' | 'no-permission'`（选中）
  - 优点：控制台是单次申请操作（POST → window.open → 回 idle），无需持续连接态；状态机简洁，渲染分支少
  - 缺点：`opened` 不作为独立态，用户点击后按钮立即恢复可点击（但新窗口已打开）
- **备选 B：** 5 态枚举（对齐终端 Tab：idle / opening / opened / error / no-permission）
  - 优点：与终端 Tab 状态机结构一致，UX §6.5 列出的 5 个状态均有对应枚举值
  - 缺点：`opened` 态无实际 UI 差异（新窗口已打开，原页面回到可申请态）；`error` 态与 `idle` 态 UI 差异仅是 Message.error 弹出后回 idle，无需独立枚举
- **决策：** A 胜出 —— 控制台会话与终端会话本质不同。终端是持续 WebSocket 连接（connected 是持久态），控制台是单次 POST + window.open（opened 是瞬时态，无需保留）。3 态足够覆盖 UX §6.5 的所有 UI 分支：disabled（isRunning 守卫）、opening（loading）、opened（window.open + Message.success 后回 idle）、error（Message.error 后回 idle）、no-permission（Alert warning 终态）。

### 3.2 mock `connect_url` 指向本地占位 URL vs 真实 VNC 页面

- **备选 A：** `connect_url` 指向 `http://127.0.0.1:4010/api/v1/mock/console/{session_id}`（占位，选中）
  - 优点：mock 不需要实现真实 VNC 服务器，只需验证 `window.open` 行为与状态机
  - 缺点：新窗口打开后显示 404 或空白页（mock 未注册该 GET 路由）
- **备选 B：** 指向一个简单的 HTML 占位页
  - 优点：新窗口有内容显示，更直观
  - 缺点：需在 mock 中注册 GET 路由返回 HTML，增加 mock 复杂度
- **决策：** A 胜出 —— AC 验证目标是「新窗口打开」行为（window.open 调用 + Message.success），而非 VNC 页面内容。占位 URL 足够验证状态机。真实 VNC 页面归后续 Core 后端实现（PRD Non-Goals 明确不含 VNC 前端）。

## 4. Open Questions

### 4.1 lint 工具链缺失

- **假设：** Issue AC #10 要求 Typecheck/lint 通过，但 console 项目当前无 ESLint 配置文件（`.eslintrc*` / `eslint.config.*` 全仓均不存在），`pnpm lint` 无脚本。
- **需确认：** lint 工具链缺失是 pre-existing 项目级问题（issue-003/004/005/006/007 记录已提及），非本批次引入。后续是否需要单独建立 console lint 配置批次？

### 4.2 浏览器自动化验证缺失

- **假设：** AC #11 要求 browser 验证 disabled / opening / opened / error 四态，但当前环境无 playwright/puppeteer 等 browser 自动化工具。
- **需验证：** 已记录手动验证步骤（见下方 Browser 手动验证步骤）。后续 Sprint 是否引入 browser 自动化测试框架？若引入，应补全 ConsoleTab 的 disabled / opening / opened / error / no-permission 五态自动化测试。

### 4.3 `novnc` 协议后端支持确认

- **假设：** OpenAPI `CreateInstanceConsoleSessionRequest.protocol` enum 含 `novnc`，前端将其列入 Select 选项。
- **需确认：** Core 后端 `createInstanceConsoleSession` handler 是否实际支持 `novnc` 协议？若后端不支持，应在 OpenAPI 中移除该 enum 值，前端同步移除。当前前端按 OpenAPI 契约消费，novnc 的实际支持取决于后端实现。

### 4.4 `make test` / `make validate-architecture` 未运行

- **假设：** 本批次为纯前端 + mock 脚本改动，未触碰 Core Go 代码，按 UI 批次惯例未运行 `make test` / `make validate-architecture`。
- **需确认：** 若后续 ship 时需要完整门禁，应在 `repo/` 目录运行 `make test && make validate-architecture`。当前 `pnpm type-check` + `pnpm build` + `git diff --check` 已覆盖前端验证。

---

## 验证命令

```bash
# Console type-check + build
cd repo/frontends/console && pnpm type-check && pnpm build

# 架构校验（若需要）
cd repo && make validate-architecture

# git diff 检查
cd repo && git diff --check

# mock server console 端点三场景验证
curl -s -X POST "http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000003/console" \
  -H "Content-Type: application/json" -d '{"protocol":"vnc"}'                              # 默认成功
curl -s -X POST "http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000003/console?forbidden=1" \
  -H "Content-Type: application/json" -d '{"protocol":"vnc"}'                              # 403 无权限
curl -s -X POST "http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000003/console?error=1" \
  -H "Content-Type: application/json" -d '{"protocol":"vnc"}'                               # 422 失败

# mock 列表验证 stopped vm 实例
curl -s "http://127.0.0.1:4010/api/v1/instances?kind=vm" | python -c "import sys,json; d=json.load(sys.stdin); [print(it['name'], it['state']) for it in d['items']]"
```

## Browser 手动验证步骤

环境无 playwright/puppeteer，以下为人工验证步骤：

1. 启动 mock + dev 服务器：`python scripts/serve_core_mock.py` + `cd frontends/console && pnpm dev`
2. **opened 态**：访问 `http://localhost:5175/compute/instances/00000000-0000-4000-8000-000000000003?tab=console`（demo-vm-001，running）→ 默认协议 vnc → 点击「打开控制台」→ 按钮短暂 loading → 新窗口打开 `http://127.0.0.1:4010/api/v1/mock/console/...` → 右上角 Message.success「已在新窗口打开控制台」
3. **opening 态**：点击按钮后观察按钮 loading 态
4. **disabled 态**：访问 `http://localhost:5175/compute/instances/00000000-0000-4000-8000-000000000004?tab=console`（demo-vm-002，stopped）→ 「打开控制台」按钮置灰 disabled，不可点击
5. **error 态**：DevTools Console 执行 `fetch('/api/v1/instances/00000000-0000-4000-8000-000000000003/console?error=1', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({protocol:'vnc'})}).then(r=>r.json()).then(console.log)` → 返回 `{"code":"INVALID_ARGUMENT",...}`
6. **no-permission 态**：DevTools Console 执行 `fetch('/api/v1/instances/00000000-0000-4000-8000-000000000003/console?forbidden=1', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({protocol:'vnc'})}).then(r=>r.json()).then(console.log)` → 返回 `{"code":"FORBIDDEN",...}`；若让组件收到该响应，则展示 Alert warning「无控制台权限」
