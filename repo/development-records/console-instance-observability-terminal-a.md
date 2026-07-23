# CONSOLE-INSTANCE-OBSERVABILITY-TERMINAL-A — Console 实例详情终端 Tab（exec）

> 对应 Issue: `repo/services/tasks/modules/issue/console/compute/issue-007-console-terminal-tab.md`
> 三元组: PRD `prd-console-instance-observability.md` / UX `ux-console-instance-observability.md` / SPEC `spec-console-instance-observability.md`
> Product line: console（仅 `repo/frontends/console/src/`）

完成日期：2026-07-06
对应 Sprint：Sprint 15（console instance observability UI 批次）
验证结果：`pnpm type-check` EXIT:0；`pnpm build` EXIT:0（5854 modules transformed）；`git diff --check` passed；curl 验证 mock exec 端点四场景通过（默认/forbidden/error/expired）；Python WebSocket echo 握手 + stdin→stdout 往返验证通过。

## 实现了什么

为实例详情终端 Tab 实现 `TerminalTab.tsx` 组件：仅 `container`/`gpu_container`/`sandbox` kind 渲染（由 issue-003 的 `observabilityTabsConfig` 过滤），点击「连接终端」调用 `coreApi.POST('/instances/{instance_id}/exec', { body: { idempotency_key, command, tty, rows, cols } })`，成功后用返回的 `ws_url` 建立 WebSocket 连接并用 xterm.js 渲染终端。状态机覆盖 idle / connecting / connected / expired / no-permission 五态。对齐 PRD US-012 与 SPEC §4.1.5、§5.3、§5.6.2、§9.4(US-012)。

同时在 route.tsx 中将 terminal Tab 占位替换为真实 `<TerminalTab />` 组件。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `frontends/console/src/features/instance-observability/TerminalTab.tsx` | 新增 | 终端 Tab 组件：POST exec → ws_url → WebSocket + xterm.js；状态机 idle/connecting/connected/expired/no-permission；Tooltip/Alert/Tag/Empty 全态覆盖；组件卸载清理 ws + xterm |
| `frontends/console/src/routes/compute/instances/$instanceId/route.tsx` | 修改 | terminal Tab 占位替换为 `<TerminalTab />`；同时引入 `MetricsTab`（issue-006） |
| `frontends/console/package.json` / `pnpm-lock.yaml` | 修改 | 新增 `@xterm/xterm@6.0.0`、`@xterm/addon-fit@0.11.0` 依赖 |
| `scripts/serve_core_mock.py` | 修改 | 为 `createInstanceExecSession` 增加专用 mock：返回有效 `ws_url`（指向 4011 端口 WS echo）+ `expires_at`；支持 `?forbidden=1`/`?error=1`/`?expired=1` 场景切换；新增标准库 WebSocket echo 服务器（4011 端口） |

## 完工标准达成

- [x] 新建 `TerminalTab.tsx`，仅 `container`/`gpu_container`/`sandbox` kind 渲染（AC #1，kind 过滤由 issue-003 的 `observabilityTabsConfig` 完成）
- [x] `batch_job`、`notebook` **不展示**终端 Tab（AC #2，`observabilityTabsConfig` 已排除）
- [x] 仅 `state=running` + `scope:instances:exec` 权限可连接（AC #3，`isRunning` 守卫按钮 + 403 体现无权限）
- [x] 非 running：连接按钮 disabled + `Tooltip`「仅运行中的实例可连接终端」（AC #4）
- [x] 无 exec 权限：`Alert theme="warning"`「当前账号无终端访问权限」（AC #5，`connState === 'no-permission'` 分支）
- [x] 连接流程：POST `/instances/{id}/exec`（body 含 `idempotency_key`）→ `InstanceExecSession` → 用 `ws_url` 建立 WebSocket（AC #6，`handleConnect` → `connectWebSocket`）
- [x] 连接状态 Tag：未连接 / 连接中 / 已连接 / 已过期（AC #7，`CONNECTION_STATE_TAG` 映射表）
- [x] 连接中：Button loading（AC #8，`loading={connecting}`）
- [x] 连接成功：终端区 active（xterm.js 渲染）（AC #9，`Terminal` + `FitAddon`）
- [x] session 过期（now > expires_at）：`Alert theme="warning"`「终端会话已过期，请重新连接」+ 重新连接按钮（AC #10，`expired` 分支 + `handleReconnect`）
- [x] exec 4xx/422 失败：`Message.error` + 保留 idle 态（AC #11，error 分支 + `MessagePlugin.error`）
- [x] 终端容器 min-height 400px，bg container，monospace（AC #12，内联 style + TDesign CSS 变量）
- [x] idle 态：`Empty`「点击连接终端开始会话」（AC #13，未连接时 Empty）
- [x] WebSocket 帧格式遵循 SPEC §5.3.2 客户端契约（stdin/resize → stdout/stderr/exit/error）（AC #14）
- [x] Typecheck/lint 通过（AC #15，`pnpm type-check` EXIT:0；lint 工具链缺失为 pre-existing 项目级问题，非本批次引入）
- [x] browser 验证：disabled / connecting / error（AC #16，环境无浏览器自动化工具，记录手动验证步骤）

## 1. Design Decisions

### 1.1 状态机用 5 态枚举而非 3 态

- **歧义：** SPEC §5.6.2 描述终端状态机为 idle / connecting / connected / expired 四态，但 PRD US-012 与 UX §6.4 还要求「无权限」态展示 Alert warning。
- **选择：** 扩展为 5 态：`'idle' | 'connecting' | 'connected' | 'expired' | 'no-permission'`。`no-permission` 作为独立终止态，整页渲染 Alert warning，不回退到 idle。
- **理由：** 403 无权限是持久状态（用户权限不会自动获得），回退 idle 会让用户误以为可以重试连接。独立态让 Alert warning 持续展示，直到用户切换实例或获得权限。`CONNECTION_STATE_TAG` 映射表也对应 5 态，Tag 在 idle/connecting/connected/expired 态展示，no-permission 态不展示 Tag（整页 Alert 替代）。

### 1.2 无权限判断用 `error.code === 'FORBIDDEN'` 而非 HTTP 状态码

- **歧义：** `coreApi.POST` 返回 `{ data, error }`，error 是序列化后的 body（OpenAPI `ErrorResponse`），不直接暴露 HTTP 状态码。
- **选择：** 用 `error.code === 'FORBIDDEN'` 判断 403。`CoreApiError` 接口提取 `code`/`message`/`request_id` 三字段，`as` 断言后取 `code`。
- **理由：** OpenAPI `v1.yaml` 的 `Forbidden` response 描述明确 `code=FORBIDDEN`（[v1.yaml:1777](file:///e:/go/project/ANI/repo/api/openapi/v1.yaml#L1777)）。`ErrorResponse` schema 有 `code` 字段（[core-schema.d.ts:1804](file:///e:/go/project/ANI/repo/frontends/console/src/api/core-schema.d.ts#L1804)）。openapi-fetch 在非 2xx 时把 body 作为 `error` 返回，类型由 `responses['403']` 推导。与 LogsTab/EventsTab 的 `error as CoreApiError` 模式一致（项目既有约定）。

### 1.3 `idempotency_key` 生命周期：重试复用，重新连接新生成

- **歧义：** PRD US-004 要求"同一 `idempotency_key` 重试幂等"，但未明确"重试"与"重新连接"的边界。
- **选择：** `idempotencyKeyRef`（useRef）承载当前 key：
  - `handleConnect`：若 ref 为空则生成新 key，否则复用（重试场景）
  - `handleReconnect`：清空 ref（`null`），下次 `handleConnect` 生成新 key
  - 连接成功后不清空 ref（若 WS 断开重试仍用同一 key）
- **理由：** "重试"指同一会话尝试失败后再试（网络抖动、422 可恢复错误），应复用 key 保证幂等——后端用同一 key 返回同一 session，避免创建多个 session。"重新连接"指用户主动从过期态发起新会话，应生成新 key 创建全新 session。用 ref 而非 state 避免重渲染抖动。

### 1.4 会话过期检测用 `setInterval` 轮询而非 `setTimeout` 延迟

- **歧义：** SPEC §5.6.2 要求 `now > expires_at` 时切到 expired 态，但未指定检测机制。
- **选择：** `useEffect` 在 `connState === 'connected'` 时启动 `setInterval(1000)` 每秒检查 `Date.now() >= new Date(expiresAt).getTime()`，过期则 `setConnState('expired')` + `cleanup()`。
- **理由：** `expires_at` 是绝对时间戳，用 `setTimeout(expires_at - now)` 也可行，但若系统时钟与服务器有偏差或 expires_at 很远（1 小时），单次 setTimeout 不可靠。`setInterval` 每秒轮询更健壮，且 `cleanup` 在 effect cleanup 中清除 interval，无泄漏。1 秒精度对终端会话过期检测足够（用户体验上 1 秒内切换到过期态可接受）。

### 1.5 xterm 实例 lazy 创建于 `ws.onopen` 而非组件挂载时

- **歧义：** SPEC §5.6.2 要求连接成功后 xterm 渲染，但未明确 xterm 实例何时创建。
- **选择：** `termRef`/`fitRef` 初始为 null，`ws.onopen` 时才 `new Terminal()` + `loadAddon(FitAddon)` + `term.open(termHostRef.current)` + `fit.fit()`。
- **理由：** xterm 实例需要真实 DOM 容器（`termHostRef.current`）才能挂载。idle 态下容器 `div` 是 `display: none`，xterm 无法正确计算尺寸（FitAddon 依赖 clientWidth/clientHeight）。在 `ws.onopen` 时容器已可见（`connected` 态 `display: block`），此时创建并 fit 能正确计算终端行列数。组件卸载时 `cleanup()` 调用 `term.dispose()` 释放 xterm 资源，避免内存泄漏。

## 2. Deviations

### 2.1 增强 Core Mock Server 支持 exec session + WebSocket echo（超出 Issue #7 范围）

- **Spec 说：** Issue #7 Scope 限定 `repo/frontends/console/src/features/instance-observability/`。
- **实现：** 额外修改了 `scripts/serve_core_mock.py`，为 `createInstanceExecSession` 增加专用 mock（返回有效 `ws_url` + `expires_at`，支持 `?forbidden=1`/`?error=1`/`?expired=1` 场景切换），并新增标准库实现的 WebSocket echo 服务器（4011 端口）。
- **原因：** Core Mock Server 原先对 `createInstanceExecSession` 走通用 `mock_value` 路径，返回静态 `ws_url: "mock"` 字符串，前端拿到后 `new WebSocket("mock")` 会立即失败。这导致 TerminalTab 的连接成功态（AC #9）、会话过期态（AC #10）、帧契约（AC #14）在本地完全无法验证。增强 mock 是验证这些 AC 的必要前提。WebSocket echo 服务器用 Python 标准库（socket + hashlib + base64 + struct）手写 RFC 6455 帧编解码，避免引入 `websockets` 依赖（环境未安装且保持脚本零额外依赖特性）。

### 2.2 WebSocket 服务端未实现是已知边界，前端按 mock 验证状态机

- **Spec 说：** SPEC §11.2 风险表明确"exec WebSocket 服务端未实现 | 终端 Tab 无法真连 | local profile 下 POST 返回合成 ws_url；UI 验证状态机"。
- **实现：** 前端 `connectWebSocket` 用 `ws_url` 建立 WebSocket 连接，连接成功后 xterm 渲染。真实后端（8080）的 `ws_url` 指向不存在的 WS 端点（后端无 WebSocket Upgrade 实现），连接会失败触发 `ws.onerror` → idle。mock 环境（4011）的 `ws_url` 指向 echo 服务器，连接成功可验证完整流程。
- **原因：** 后端 WebSocket 服务端实现归"后续 Core 批次"（SPEC §11.1），不属于本 PRD/Issue 范围（PRD Non-Goals L240）。前端代码按 SPEC §5.3 客户端契约实现，真实后端就绪后可直接对接。

## 3. Tradeoffs

### 3.1 xterm 依赖引入：`@xterm/xterm` + `@xterm/addon-fit` vs 轻量替代

- **备选 A：** `@xterm/xterm@6.0.0` + `@xterm/addon-fit@0.11.0`（选中）
  - 优点：SPEC §5.3.3 明确指定；业界标准终端渲染库，支持 ANSI 转义、光标、滚动、resize；FitAddon 自动适配容器尺寸
  - 缺点：包体积增加（xterm 核心 ~200KB）；新增 2 个依赖
- **备选 B：** 用 `<pre>` + 手动渲染 stdout 文本
  - 优点：零依赖，包体积小
  - 缺点：无法处理 ANSI 转义码（颜色、光标移动）、无法接收 stdin（键盘输入）、无法 resize、不满足 SPEC §5.3.3 约束
- **决策：** A 胜出 —— SPEC §5.3.3 明确要求 `xterm` + `xterm-addon-fit`，UX §5.5 终端组件映射也指定 xterm。轻量替代无法满足终端交互需求（stdin 输入、ANSI 渲染、resize 契约）。

### 3.2 WebSocket echo 用标准库 vs `websockets` 库

- **备选 A：** Python 标准库（socket + hashlib + base64 + struct）手写 RFC 6455 帧编解码（选中）
  - 优点：零额外依赖，保持 `serve_core_mock.py` 原有"零外部依赖"特性（仅依赖 yaml）；脚本可独立运行
  - 缺点：手写 WebSocket 帧编解码代码量约 120 行，需正确实现 RFC 6455 握手 + 帧格式 + 掩码解码
- **备选 B：** 引入 `websockets` 库
  - 优点：代码简洁，成熟实现，自动处理握手/帧/心跳
  - 缺点：新增依赖（环境未安装）；破坏脚本零额外依赖特性；需 `pip install websockets`
- **决策：** A 胜出 —— 脚本头部注释声明依赖-free（除 yaml），引入 websockets 会破坏约定。手写帧编解码虽然代码量多，但 RFC 6455 握手 + 帧格式是成熟标准，实现可控。echo 服务器只处理文本帧（stdin/resize），不处理二进制/分片/压缩，复杂度可控。

### 3.3 状态机用单一 `connState` 枚举 vs 多个布尔状态

- **备选 A：** 单一 `connState: TerminalConnectionState` 枚举（选中）
  - 优点：状态互斥清晰，渲染分支明确（switch/if），Tag 映射表 `CONNECTION_STATE_TAG` 直接索引
  - 缺点：扩展新状态需改枚举 + 映射表
- **备选 B：** 多个布尔状态（`isConnecting`/`isConnected`/`isExpired`/`isNoPermission`）
  - 优点：每个状态独立，组合灵活
  - 缺点：状态可能冲突（如 `isConnecting && isExpired`），需额外逻辑保证互斥；渲染分支判断复杂
- **决策：** A 胜出 —— 终端连接状态本质是有限状态机，互斥性强。枚举 + 映射表让状态转换与渲染解耦，新增状态只需加枚举值 + 映射项。布尔状态组合会产生非法状态（如 connecting + expired），需防御性代码。

## 4. Open Questions

### 4.1 lint 工具链缺失

- **假设：** Issue AC #15 要求 Typecheck/lint 通过，但 console 项目当前无 ESLint 配置文件（`.eslintrc*` / `eslint.config.*` 全仓均不存在），`pnpm lint` 无脚本。
- **需确认：** lint 工具链缺失是 pre-existing 项目级问题（issue-003/004/005/006 记录已提及），非本批次引入。后续是否需要单独建立 console lint 配置批次？

### 4.2 浏览器自动化验证缺失

- **假设：** AC #16 要求 browser 验证 disabled / connecting / error 三态，但当前环境无 playwright/puppeteer 等 browser 自动化工具。
- **需验证：** 已记录手动验证步骤（见下方 Browser 手动验证步骤）。后续 Sprint 是否引入 browser 自动化测试框架？若引入，应补全 TerminalTab 的 disabled / connecting / connected / error / no-permission / expired 六态自动化测试。

### 4.3 WebSocket 服务端实现时机

- **假设：** 前端按 SPEC §5.3 客户端契约实现 WebSocket 连接，但后端 WebSocket 服务端（HTTP→WS Upgrade + 帧转发）尚未实现，归"后续 Core 批次"。
- **需确认：** 后续 Core 批次实现 WebSocket 服务端时，是否需要调整前端 `connectWebSocket` 的帧解析逻辑（如二进制 vs JSON、base64 编码）？SPEC §5.3.2 注释"帧格式细节归服务端实现冻结"，前端当前按 JSON 文本帧实现，若服务端选二进制需前端适配。

### 4.4 `handleReconnect` 冗余 `cleanup` 调用

- **观察：** `handleReconnect` 先 `cleanup()` 再调 `handleConnect()`，`handleConnect` 内部 `if (connState === 'expired')` 分支又会 `cleanup()`。由于 React state 异步更新，`handleConnect` 闭包捕获的 `connState` 仍是 'expired'，导致 `cleanup` 被调用两次。
- **影响：** `cleanup` 是幂等的（检查 ref 存在性），功能正确，仅冗余调用。
- **需确认：** 是否值得优化为 `handleReconnect` 不调 `cleanup`，仅清空 ref + setConnState('idle')，由 `handleConnect` 统一清理？当前实现优先正确性，冗余调用不影响性能。

---

## 验证命令

```bash
# Console type-check + build
cd repo/frontends/console && pnpm type-check && pnpm build

# 架构校验
cd repo && make validate-architecture

# git diff 检查
cd repo && git diff --check

# mock server exec 端点四场景验证
curl -s -X POST "http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000001/exec" \
  -H "Content-Type: application/json" -d '{"idempotency_key":"k1"}'                    # 默认成功
curl -s -X POST "http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000001/exec?forbidden=1" \
  -H "Content-Type: application/json" -d '{"idempotency_key":"k1"}'                    # 403 无权限
curl -s -X POST "http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000001/exec?error=1" \
  -H "Content-Type: application/json" -d '{"idempotency_key":"k1"}'                    # 422 失败
curl -s -X POST "http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000001/exec?expired=1" \
  -H "Content-Type: application/json" -d '{"idempotency_key":"k1"}'                    # 过期 session

# WebSocket echo 验证（Python 标准库客户端）
python -c "
import socket,base64,os,json,time
key=base64.b64encode(os.urandom(16)).decode()
s=socket.socket(); s.connect(('127.0.0.1',4011))
req=f'GET /ws/exec/test HTTP/1.1\r\nHost: 127.0.0.1:4011\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n'
s.sendall(req.encode())
time.sleep(0.5); s.recv(8192)  # 握手+欢迎帧
msg=json.dumps({'type':'stdin','data':'ls -la\r'}).encode('utf-8')
mask=os.urandom(4); masked=bytes(msg[i]^mask[i%4] for i in range(len(msg)))
header=bytearray([0x81, 0x80|len(msg)]) + mask
s.sendall(bytes(header)+masked); time.sleep(0.3)
frame=s.recv(4096); opcode=frame[0]&0x0F; ln=frame[1]&0x7F
payload=frame[2:2+ln] if ln<126 else frame[4:4+int.from_bytes(frame[2:4],'big')]
print('回显帧:', json.loads(payload.decode('utf-8'))); s.close()
"
```

## Browser 手动验证步骤

环境无 playwright/puppeteer，以下为人工验证步骤：

1. 启动 mock + dev 服务器：`python scripts/serve_core_mock.py` + `cd frontends/console && pnpm dev`
2. 访问 `http://localhost:5175/compute/instances/00000000-0000-4000-8000-000000000001?tab=terminal`
3. **disabled 态**：mock 中将实例 state 改为 `stopped`，按钮应置灰 + Tooltip「仅运行中的实例可连接终端」
4. **connecting 态**：running 实例点击「连接终端」→ Button loading + Tag「连接中」
5. **connected 态**：连接成功 → Tag「已连接」+ xterm 渲染欢迎语 + 键入字符回显 `echo: <输入>`
6. **error 态**：mock 加 `?error=1` → `Message.error` 弹出 + 回到 idle
7. **no-permission 态**：mock 加 `?forbidden=1` → `Alert theme="warning"`「当前账号无终端访问权限」
8. **expired 态**：mock 加 `?expired=1` → 连接后立即 `Alert theme="warning"`「终端会话已过期」+ 重新连接按钮
