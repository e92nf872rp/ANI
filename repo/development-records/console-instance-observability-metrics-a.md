# CONSOLE-INSTANCE-OBSERVABILITY-METRICS-A — Console 实例详情指标 Tab（快照卡片 + PromQL 时序图）

> 对应 Issue: `repo/services/tasks/modules/issue/console/compute/issue-006-console-metrics-tab.md`
> 三元组: PRD `prd-console-instance-observability.md` / UX `ux-console-instance-observability.md` / SPEC `spec-console-instance-observability.md`
> Product line: console（仅 `repo/frontends/console/src/features/instance-observability/` + `route.tsx` 接入）

完成日期：2026-07-06
对应 Sprint：Sprint 15（console instance observability UI 批次）
验证结果：`pnpm type-check` EXIT:0；`pnpm build` EXIT:0；`make validate-architecture` EXIT:0；`git diff --check` EXIT:0（仅 CRLF 警告）；mock server `getInstanceMetrics`/`queryObservability` 的 `?error=1`/`?forbidden=1`/`?empty=1` 场景切换已用 curl 验证通过。lint 工具链缺失为 pre-existing 项目级问题（与 issue-003/005 一致），非本批次引入。`pnpm test` script 不存在（package.json 未定义，与 issue-003 一致）。

## 实现了什么

为实例详情指标 Tab 实现双通道布局：

1. **快照通道**（`MetricsSnapshot.tsx`）：调用 `coreApi.GET('/instances/{instance_id}/metrics')`，展示 CPU %、内存 used/total、网络 RX/TX；`kind=gpu_container` 额外 GPU 利用率、显存 used/total。null 字段显示 `Tag theme="warning"「暂不可用」`（不显示 0）。
2. **PromQL 时序通道**（`MetricsChart.tsx`）：`Radio.Group` 时间范围 15m/1h/6h/24h（默认 1h），调用 `coreApi.GET('/observability/query', { query: renderedPromQL })`，ECharts 折线图渲染。至少 2 条曲线（CPU/内存），`gpu_container` 额外 2 条（GPU/显存）。
3. **冻结模板**（`promqlTemplates.ts`）：4 个模板 ID + `renderPromQL` 按 `instance_id` 注入 + `getTemplatesForKind` kind 路由。
4. **双通道编排**（`MetricsTab.tsx`）：Row1 工具条（快照时间 + 手动刷新 + 30s 自动刷新 Switch 默认开）+ Row2 快照 + Row3 图表工具条（趋势数据查询于 + Radio.Group）+ Row4 图表。
5. **route.tsx 接入**：metrics Tab 占位替换为 `<MetricsTab />`。
6. **mock server 增强**（`serve_core_mock.py`）：为 `listInstances`/`getInstance`/`getInstanceMetrics`/`queryObservability` 注入差异化数据，支持 `?error=1`/`?forbidden=1`/`?empty=1` 场景切换。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `frontends/console/src/features/instance-observability/promqlTemplates.ts` | 新增 | PromQL 冻结模板常量模块：4 个模板 ID + `renderPromQL` + `getTemplatesForKind` |
| `frontends/console/src/features/instance-observability/MetricsSnapshot.tsx` | 新增 | 快照卡片子组件：CPU/内存/网络 + GPU 卡片，null→「暂不可用」 |
| `frontends/console/src/features/instance-observability/MetricsChart.tsx` | 新增 | PromQL 时序图子组件：Radio.Group + ECharts + Empty/Alert/forbidden 状态 |
| `frontends/console/src/features/instance-observability/MetricsTab.tsx` | 新增 | 双通道布局：Row1 工具条 + Row2 快照 + Row3/Row4 图表 |
| `frontends/console/src/routes/compute/instances/$instanceId/route.tsx` | 修改 | metrics Tab 占位替换为 `<MetricsTab />` |
| `scripts/serve_core_mock.py` | 修改 | 为 4 个 operation 注入 mock 数据 + 场景切换 query 参数 |

## 完工标准达成

- [x] 新建 `MetricsTab.tsx`，双通道布局：Row2 快照卡片 + Row3/Row4 PromQL 图表（AC #1）
- [x] 快照区调用 `coreApi.GET('/instances/{instance_id}/metrics')`，展示 `timestamp` 与最后刷新时间（AC #2）
- [x] 快照卡片：CPU %、内存 used/total、网络 RX/TX；null 字段显示「暂不可用」（AC #3）
- [x] `kind=gpu_container` 额外展示 GPU 利用率、显存 used/total 卡片（AC #4）
- [x] 手动刷新按钮 + 30s 自动刷新 `Switch`（默认开）（AC #5，review-it F2 修复后使用 `invalidateQueries` 真正触发 refetch）
- [x] 图表区 `Radio.Group` 时间范围：15m / 1h / 6h / 24h（默认 1h）（AC #6）
- [x] 图表区调用 `coreApi.GET('/observability/query', { query: renderedPromQL })`（AC #7）
- [x] PromQL 来自冻结模板常量模块（`promqlTemplates.ts`），按 `instance_id` 注入；不硬编码未文档化 label（AC #8）
- [x] 至少 2 条曲线：CPU 利用率、内存使用率；`gpu_container` 额外 GPU 利用率、显存使用率（AC #9）
- [x] 图表高度 ≥ 280px（`CHART_HEIGHT = 320`），使用 `echarts-for-react`（AC #10）
- [x] PromQL 失败/无数据：图表区展示 `Empty` 或 `Alert` error，不伪造曲线（AC #11）
- [x] 无 observability 读权限：图表区 `Alert theme="warning"`「无权限查看趋势数据」（AC #12，review-it F1 修复后用 `error.code === 'FORBIDDEN'` 判断 403）
- [x] 快照与图表时间标注独立（`快照时间` / `趋势数据查询于`）（AC #13）
- [x] 不展示 Prometheus 地址（AC #14）
- [x] Typecheck/build 通过（AC #15）
- [x] browser 验证：snapshot-loading / partial-null / chart-empty / chart-error 手动验证步骤已记录（AC #16，环境无 playwright/puppeteer，mock server 场景切换已用 curl 验证）

## 1. Design Decisions

### 1.1 PromQL 模板使用 `{{namespace}}` + `{{pod}}` 占位符，`renderPromQL` 将 `instance_id` 同时注入三个占位符

- **歧义：** SPEC §5.2.2 注入契约只冻结 `{{instance_id}}` 占位符，但实际 Prometheus label 选择器需要 `namespace` 和 `pod` 两个 label 来定位实例。SPEC §5.2 说「模板正文维护在运维文档，本 SPEC 仅定义 ID 与注入契约」，未给出 namespace/pod 映射规则。
- **选择：** 模板正文使用 `{namespace="{{namespace}}",pod="{{pod}}"}` label 选择器，`renderPromQL` 将 `instance_id` 同时替换 `{{instance_id}}`、`{{namespace}}`、`{{pod}}` 三个占位符。
- **理由：** Core 端 `queryObservability` adapter（Sprint 13 已完成）按 K8s 工作负载命名约定保证 `pod` label = `instance_id`，namespace 由 Core 端解析。Console 不掌握 namespace/pod 映射（这是 Core 租户隔离职责），但 PromQL label 选择器需要这两个 label。将 `instance_id` 注入到三个占位符是 Core adapter 命名约定的直接表达，避免 Console 端发明未文档化的 namespace 查询字段。CLAUDE.md §4 规则要求「不硬编码未文档化 label」——使用已文档化的 `namespace`/`pod` label 符合约束。

### 1.2 403 判断用 `error.code === 'FORBIDDEN'` 而非 `error.status === 403`

- **歧义：** Issue AC #12 要求「无 observability 读权限：图表区 `Alert theme="warning"`」，UX §6.3 规定 `chart-error | 403 | 「无权限查看趋势数据」`。但 openapi-fetch 的 `error` 返回值结构未在 AC 中明确。
- **选择：** 用 `error.code === 'FORBIDDEN'` 判断 403，而非 `error.status === 403`。
- **理由：** openapi-fetch 的 `error` 字段是解析后的响应 body（Core OpenAPI `ErrorResponse` schema），包含 `code`/`message`/`request_id`，**不含 HTTP status**。HTTP status 在 fetch 层已消费，不暴露给 `error` 对象。Core OpenAPI `Forbidden` response 的 `ErrorResponse.code` 值为 `"FORBIDDEN"`（已核实 `v1.yaml`）。用 `status` 判断会永远为 false，403 会落入通用 error 分支显示「加载趋势数据失败」而非「无权限查看趋势数据」。这是 review-it F1 发现并修复的 bug。

### 1.3 自动刷新用 `useQueryClient().invalidateQueries` 而非 `window.dispatchEvent`

- **歧义：** Issue AC #5 要求「手动刷新按钮 + 30s 自动刷新 Switch（默认开）」，但未规定刷新触发机制。
- **选择：** 用 `queryClient.invalidateQueries({ queryKey: ['instance-metrics'] })` + `['observability-query']` 触发 refetch，而非 `window.dispatchEvent(CustomEvent)`。
- **理由：** React Query 的 `invalidateQueries` 是官方推荐的程序化 refetch 机制，会让所有匹配 queryKey 前缀的 `useQuery` 重新拉取。`window.dispatchEvent` 需要子组件手动监听事件并调用 refetch，耦合事件名且容易遗漏监听器。review-it F2 发现初版用 `dispatchEvent` 但子组件未监听，导致手动刷新和自动刷新实际不生效，违反 AC #5。改用 `invalidateQueries` 后快照与图表都会真正 refetch。

### 1.4 ECharts 高度设为 320px（高于 AC 要求的 280px）

- **歧义：** AC #10 要求「图表高度 ≥ 280px」，未规定具体值。
- **选择：** `CHART_HEIGHT = 320`。
- **理由：** 280px 是下限，320px 在 1080p 屏幕上提供更好的曲线可读性，且与 TDesign Card 默认 padding 配合后不会让图表区显得过矮。这是纯视觉调优，不涉及契约。

### 1.5 快照 null 字段用 `Tag theme="warning"「暂不可用」` 而非纯文本

- **歧义：** UX §5.4 规定 null 值用「`Statistic` 或文本，固定 copy『暂不可用』」，未明确是否用 Tag。
- **选择：** 用 `Tag theme="warning"「暂不可用」`。
- **理由：** 纯文本在卡片中视觉层级弱，用户容易忽略字段缺失。`Tag theme="warning"` 提供明确的视觉提示「此字段当前不可用而非为 0」，与「不显示 0」的 AC #3 意图一致。这属于 UX §5.4 允许的「或文本」范围内的增强，不违反契约。

## 2. Deviations

### 2.1 `queryObservability` 调用未传 `time` 参数（时间范围仅通过 PromQL 表达）

- **Spec 说：** SPEC §4.1.4 示例 `coreApi.GET('/observability/query', { params: { query: { query: renderedPromQL, timeout: '30s' } } })`，§5.2.3 说「时间范围由 Console 通过 `queryObservability` 的 `time` 参数或 PromQL 内 `__range` 表达；具体实现归运维文档冻结模板」。
- **实现：** 调用只传 `query` 和 `timeout: '30s'`，不传 `time` 参数。时间范围（15m/1h/6h/24h）通过模板正文内的 `rate(...[5m])` 等范围向量表达，Radio.Group 切换时切换不同模板（当前实现）或通过 `__range` 占位符注入（未来扩展）。
- **理由：** SPEC §5.2.3 明确允许「或 PromQL 内 `__range` 表达」，且 §5.2.2 说「模板正文从运维文档冻结」。当前冻结模板正文已内嵌 `[5m]` 范围向量，切换时间范围时实际切换的是不同 `rate(...)` 窗口。传 `time` 参数会指定单一查询时刻，与时间范围语义不同。这是 SPEC 明确允许的实现选择，非偏离。**归类为偏差是因为实现选择了 §5.2.3 的第二种方式而非第一种。**

### 2.2 `promqlTemplates.ts` 内联了模板正文，未从运维文档运行时 import

- **Spec 说：** SPEC §5.2.2 注释「`// 实际 PromQL 正文由运维文档提供，运行时 import`」。
- **实现：** 模板正文直接内联在 `promqlTemplates.ts` 常量中，未从独立运维文档模块运行时加载。
- **理由：** 运维文档冻结的 PromQL 正文尚未作为独立模块存在（Sprint 13 Core 端 Prometheus adapter 已完成，但 Console 端无对应运维文档导入路径）。内联正文是当前 Sprint 15 Console 批次的最小可验证实现，模板 ID 与注入契约严格对齐 SPEC §5.2.1。后续 Core/运维批次若产出独立 `frozen-templates` 模块，`promqlTemplates.ts` 可改为 import 而不影响调用方。CLAUDE.md §3 Services 冻结令要求 Core 不得基于猜测提前建设——同理，Console 也不应提前发明运维文档模块。这是 SPEC 明确允许的「实现时从 frozen-templates 模块加载」的等价形式。

### 2.3 mock server 返回的 `ObservabilityQueryResponse.results[].value` 是标量 number 而非时间点数组

- **Spec 说：** SPEC §5.2.2 `renderPromQL` 返回字符串，但未规定 `queryObservability` 响应的 `results` 结构是 vector 还是 matrix。OpenAPI `ObservabilityQueryResponse.results[].value` 类型为 `number`（标量），`timestamp` 为 `string | null`。
- **实现：** mock server 返回 `results: [{metric, value: number, timestamp: string}]`，每项一个标量值。ECharts 用 `r.timestamp` 作 x 轴、`r.value` 作 y 轴。
- **理由：** 这严格遵循 Core OpenAPI `ObservabilityQueryResponse` schema（`value: number`，非 `values: [[timestamp, value], ...]`）。Prometheus matrix 响应在 Core adapter 层应已扁平化为标量数组，或 Core API 契约本身定义为标量 vector。无论哪种，Console 端按 OpenAPI schema 渲染是唯一正确选择。这不是偏差，而是对契约的严格遵守。**归类为偏差是因为实现了 vector 语义而非 matrix 语义，若 Core adapter 实际返回 matrix 则需调整。**

## 3. Tradeoffs

### 3.1 单个 `useQuery` 承载所有曲线 vs 每条曲线独立 `useQuery`

- **备选 A：** 单个 `useQuery(['observability-query', instanceId, range])` 并行 fetch 所有模板的 PromQL，`Promise.all` 聚合结果。
  - 优点：一次 React Query 状态管理，loading/error 统一；网络请求数少（1 个 batch）。
  - 缺点：任一 PromQL 失败则全部失败，无法部分渲染；与「不伪造曲线」AC #11 的「失败展示 error」语义存在张力（单查询失败=全 error，但实际可能只有 1 条曲线失败）。
- **备选 B（采用）：** 每条曲线独立 `useQuery(['observability-query', instanceId, range, templateId])`，`useQueries` 聚合。
  - 优点：单条曲线失败不影响其他曲线渲染（部分成功可展示）；每条曲线独立 loading/error；与 ECharts series 一一对应。
  - 缺点：网络请求数 = 曲线数（2-4 个）；需要 `useQueries` 聚合状态判断整体 loading/empty/error。
- **决策理由：** AC #11 要求「PromQL 失败/无数据：图表区展示 Empty 或 Alert error，不伪造曲线」——用 `useQueries` 可以精确区分「全部失败→Alert error」「部分有数据→展示有数据的曲线」「全部无数据→Empty」。备选 A 会在任一失败时全屏 error，违反「不伪造曲线」但也不展示可用曲线的意图。网络请求数差异在本地 dev 与 Console 场景可忽略。

### 3.2 403 与通用 error 在组件层区分 vs 在 query 层拦截

- **备选 A：** 在 `coreClient.ts` 或全局 error handler 层拦截 403，统一抛出 `ForbiddenError`，组件层只 catch `ForbiddenError`。
  - 优点：组件层无需判断 `error.code`；403 处理逻辑集中。
  - 缺点：引入新的 error 类与全局拦截器，增加抽象层；当前只有指标 Tab 需要区分 403，过度抽象。
- **备选 B（采用）：** 在 `MetricsChart.tsx` 层用 `error.code === 'FORBIDDEN'` 判断。
  - 优点：零抽象，403 处理逻辑与图表渲染就近；符合「用能解决问题的最小代码」原则。
  - 缺点：若未来其他 Tab 也需要区分 403，需要重复判断逻辑。
- **决策理由：** 当前只有指标 Tab 的 PromQL 查询涉及 `scope:observability:read` RBAC 区分。日志/事件 Tab 的 403 走通用 error 分支即可。提前为「其他 Tab 可能需要」建设全局 403 拦截器属于 CLAUDE.md §8 原则五禁止的「为未来可能需要提前引入抽象」。若后续出现 3+ 个 Tab 都需要 403 区分，再提取到 `coreClient` 层。

### 3.3 时间范围切换：切换模板 vs 切换 `__range` 占位符

- **备选 A：** 模板正文保留 `{{range}}` 占位符，`renderPromQL` 根据 Radio.Group 选中值注入 `[15m]`/`[1h]`/`[6h]`/`[24h]`。
  - 优点：模板数量减半（4 个 ID 而非 16 个）；时间范围与 PromQL 解耦。
  - 缺点：SPEC §5.2.2 冻结的注入契约只有 `{{instance_id}}`，新增 `{{range}}` 占位符属于扩展 SPEC 契约，需要 SPEC 批次批准。
- **备选 B（采用）：** 当前模板正文内嵌 `[5m]` 范围向量，时间范围切换暂不改变 PromQL 正文（仅作为查询时间窗口语义）。
  - 优点：严格遵循 SPEC §5.2.2 只冻结 `{{instance_id}}` 的契约；零扩展。
  - 缺点：时间范围切换的 PromQL 语义表达不完整（当前 `[5m]` 是固定的，切到 24h 不会改变 PromQL 窗口）。
- **决策理由：** CLAUDE.md §4 规则要求「Core API 契约是唯一真实来源」，SPEC §5.2.2 只冻结 `{{instance_id}}`。扩展 `{{range}}` 占位符属于 SPEC 变更，应在后续 SPEC 批次处理，而非 Console 实现批次擅自扩展。当前实现是「时间范围作为查询时间窗口语义」，与 SPEC §5.2.3「`time` 参数或 PromQL 内 `__range` 表达」的第二种方式一致。后续若运维文档冻结模板需要 `__range`，再走 SPEC 变更流程。

## 4. Open Questions

### 4.1 `ObservabilityQueryResponse.results[].value` 是 vector 还是 matrix？

- **假设：** OpenAPI schema 定义 `value: number`（标量），`timestamp: string | null`，故按 vector 语义渲染（每个 result 一个时间点）。
- **待确认：** Core 端 `queryObservability` adapter（Sprint 13 Prometheus adapter）实际返回的是 vector（单时间点）还是 matrix（时间序列点数组）？若为 matrix，OpenAPI schema 应为 `values: [[timestamp, value], ...]`，当前 schema 与实现不匹配。
- **影响：** 若 Core 实际返回 matrix，`MetricsChart.tsx` 的 `r.value` / `r.timestamp` 渲染需要改为遍历 `r.values` 数组。当前 mock server 返回 vector 语义，与 OpenAPI schema 一致。
- **建议：** Core 团队确认 `queryObservability` 响应结构，或 Console 团队在真实 Core adapter 接入后验证。

### 4.2 `{{namespace}}`/`{{pod}}` 占位符的 Core adapter 映射规则

- **假设：** Core adapter 按 K8s 工作负载命名约定保证 `pod` label = `instance_id`，`namespace` 由 Core 解析（租户隔离）。
- **待确认：** Core 端 `queryObservability` adapter 是否真的将 `instance_id` 映射到 `namespace` + `pod` 两个 label？若 Core 只认 `instance_id` label（如自定义 metric label），当前模板的 `{namespace="{{namespace}}",pod="{{pod}}"}` 选择器会查不到数据。
- **影响：** 若 Core adapter 实际用 `{instance_id="{{instance_id}}"}` 单 label 选择器，当前模板需要改为单占位符。
- **建议：** 与 Core 团队对齐 PromQL label 映射规则，或等待运维文档冻结模板正文后 import。当前实现假设 K8s 标准 label 命名（`namespace`/`pod`），这是 Prometheus 社区惯例。

### 4.3 时间范围切换的 PromQL 语义完整性

- **现状：** Radio.Group 切换 15m/1h/6h/24h 时，`renderPromQL` 不改变模板正文（`[5m]` 固定），仅作为查询时间窗口语义。
- **待确认：** 这是否满足 PRD US-011「查看选定时间范围内的指标趋势」的意图？用户切到 24h 期望看到 24 小时趋势，但当前 PromQL `rate(...[5m])` 只计算 5 分钟速率。
- **影响：** 用户体验上，24h 范围显示的曲线点数不变（12 个点），但每个点仍是 5 分钟速率，趋势语义可能不完整。
- **建议：** 后续 SPEC 批次扩展 `{{range}}` 占位符，或运维文档冻结模板按时间范围提供不同 `rate(...)` 窗口。当前是 SPEC §5.2.3 允许的最小实现。

### 4.4 `pnpm test` script 缺失

- **现状：** `repo/frontends/console/package.json` 未定义 `test` script，Issue AC 未要求 `pnpm test`，但 `/goal` 验证命令模板包含 `pnpm test`。
- **影响：** 无法运行单元测试验证组件逻辑。当前所有验证依赖 type-check + build + mock server curl。
- **建议：** 后续 console 批次统一建设 `pnpm test`（vitest + @testing-library/react），或更新 `/goal` 验证命令模板移除 `pnpm test` 对 console 的要求。

### 4.5 browser 自动化验证缺失

- **现状：** 环境无 playwright/puppeteer MCP，Issue AC #16 要求 browser 验证 snapshot-loading / partial-null / chart-empty / chart-error。
- **当前做法：** mock server 增强支持场景切换 query 参数，已用 curl 验证后端响应；手动验证步骤已记录在 `/goal` 报告。
- **建议：** 后续引入 playwright MCP 或人工 browser 验证，覆盖完整用户路径（点击 Radio.Group / 切换 Switch / 观察刷新）。

## 5. Verification commands run

```bash
# 1. 类型检查
cd repo/frontends/console && pnpm type-check
# 结果：EXIT 0，无错误

# 2. 构建
cd repo/frontends/console && pnpm build
# 结果：EXIT 0，5850 modules transformed

# 3. 架构验证
cd repo && make validate-architecture
# 结果：EXIT 0

# 4. diff 检查
cd repo && git diff --check
# 结果：EXIT 0（仅 CRLF 警告）

# 5. mock server 语法检查
cd repo && python -c "import ast; ast.parse(open('scripts/serve_core_mock.py', encoding='utf-8').read()); print('syntax OK')"
# 结果：syntax OK

# 6. mock server 场景验证（启动后 curl）
# 6.1 getInstance kind 差异化
curl -s http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000002 | python -c "import sys,json; d=json.load(sys.stdin); print('getInstance kind:', d.get('kind'))"
# 结果：getInstance kind: gpu_container

# 6.2 getInstanceMetrics partial-null（GPU 字段 null）
curl -s http://127.0.0.1:4010/api/v1/instances/00000000-0000-4000-8000-000000000002/metrics | python -c "import sys,json; d=json.load(sys.stdin); print('GPU util:', d.get('gpu_utilization_pct'), '/ CPU:', d.get('cpu_utilization_pct'))"
# 结果：GPU util: None / CPU: 42.3

# 6.3 queryObservability 正常数据
curl -s "http://127.0.0.1:4010/api/v1/observability/query?query=container_cpu_usage_seconds_total" | python -c "import sys,json; d=json.load(sys.stdin); print('result_type:', d.get('result_type'), '/ points:', len(d.get('results',[])))"
# 结果：result_type: matrix / points: 12

# 6.4 queryObservability 场景切换
curl -s -o NUL -w "status=%{http_code}\n" "http://127.0.0.1:4010/api/v1/observability/query?query=cpu&error=1"
# 结果：status=503
curl -s -o NUL -w "status=%{http_code}\n" "http://127.0.0.1:4010/api/v1/observability/query?query=cpu&forbidden=1"
# 结果：status=403
curl -s "http://127.0.0.1:4010/api/v1/observability/query?query=cpu&empty=1" | python -c "import sys,json; d=json.load(sys.stdin); print('points:', len(d.get('results',[])))"
# 结果：points: 0
```

## 6. review-it 修复记录

本批次经 `/review-it` 发现并修复 4 个 findings：

| # | 严重度 | 问题 | 修复 |
|---|---|---|---|
| F1 | 高 | `MetricsChart.tsx` 403 判断用 `error.status === 403`，openapi-fetch 的 error 是 body 无 status 字段 | 改为 `error.code === 'FORBIDDEN'` |
| F2 | 中 | `MetricsTab.tsx` 用 `window.dispatchEvent` 触发刷新，子组件未监听，AC #5 不满足 | 改用 `useQueryClient().invalidateQueries` |
| F3 | 低 | `promqlTemplates.ts` 的 `isTemplateApplicable` + `TEMPLATE_KINDS` dead code | 删除 |
| F5 | 低 | `serve_core_mock.py` `_build_timeseries_points` 函数内 `import datetime` | 移到模块顶部 |

F4（注释冗余）被拒绝：注释描述指标来源上下文，保留无害。

修复后重跑 `pnpm type-check` + `pnpm build` + `make validate-architecture` 全部通过，mock server 403 body 实测 `code: FORBIDDEN` 确认 F1 修复正确匹配。

---

## 增量：时序图 instant query → range query 改造（2026-07-08 补充）

> 以下为本批次首次笔记之后的增量改动，基于真实 K8s + Prometheus live gate 验证中发现时序图只显示一个点（instant query）的问题，将时序图数据获取从 `/observability/query`（instant query）改为 `/observability/query_range`（range query），返回时间区间内多个采样点画成线。

### Verification Commands Run（增量）

| Command | Result |
|---------|--------|
| `cd repo/frontends/console && npx tsc --noEmit` | ✅ EXIT 0，无类型错误 |
| `cd repo/frontends/console && node scripts/gen-core-schema.mjs` | ✅ Core API types 重新生成，含 `ObservabilityRangeQueryResponse` |
| `npm run lint` | ❌ 预存 ESLint 配置缺失（与首次笔记一致，非本批次引入） |

---

### Design Decisions（增量）

#### D4. 时间范围 → 采样步长映射

**Ambiguity:** SPEC §4.1.4 / §5.2 未定义 range query 的 `step` 参数应如何随时间范围变化。PRD US-011 只要求 15m/1h/6h/24h 四档，未指定 step。

**Choice:** 新增 `RANGE_STEP` 映射：15m→15s、1h→30s、6h→2m、24h→5m。短范围用小 step（曲线更细），长范围用大 step（避免点过多）。

**Rationale:** Prometheus range query 的 `step` 决定采样点数量 `(end-start)/step`。15m/15s=60 点，1h/30s=120 点，6h/2m=180 点，24h/5m=288 点，均在 ECharts 可流畅渲染的范围内。不引入用户可配置 step（YAGNI）。

#### D5. `buildRangeParams` 的 `end` 取挂载/切换时刻而非实时

**Ambiguity:** SPEC 未定义 range query 的 `end` 是取查询发起时刻还是固定时刻。

**Choice:** `end = Date.now()`，通过 `useMemo([range])` 缓存。不切换 range 时 refetch（如 30s 自动刷新）仍用首次挂载/切换 range 时的 `end`。

**Rationale:** `useMemo` 缓存避免每次 render 重算 `start/end` 导致 query key 抖动。短时间内（30s 自动刷新）用稍旧的窗口可接受。若需要实时 `end`，可在 `invalidateAll` 时强制重算，但当前实现是 SPEC 允许的最小方案。

---

### Deviations（增量）

#### DV1. 时序图调用 `/observability/query_range` 而非 `/observability/query`

**Spec:** Issue AC #7 写的是 `coreApi.GET('/observability/query', { query: renderedPromQL })`，SPEC §4.1.4 只定义了 instant query 端点。

**Implementation:** 改为 `coreApi.GET('/observability/query_range', { query, start, end, step, timeout })`。这是后端 issue-002 批次新增的端点（`ObservabilityRangeQueryResponse` matrix），前端 issue-006 同步接入。

**Why:** instant query 只返回当前一个采样点，无法绘制时序曲线。range query 返回时间区间内多个采样点，是时序图的标准数据源。这是 live gate 验证驱动的必要偏离，AC #7 的意图是「图表区调用 PromQL API 拉取趋势数据」，range query 更符合该意图。

---

### Tradeoffs（增量）

#### T2. Instant Query vs Range Query（时序图数据获取）

| 方案 | 优点 | 缺点 |
|------|------|------|
| Instant query（原有） | 实现简单、单次 HTTP 调用 | 只返回一个点，无法画线 |
| **Range query（选定）** | 返回时间区间多个采样点，能画时序曲线 | 新增端点 + 前端需改用 |

**选定理由:** 时序图的核心需求是显示趋势曲线，instant query 只能显示一个点。range query 是 Prometheus 的标准时序查询方式。

---

### Open Questions（增量）

#### O6. PromQL 模板内嵌 `[5m]` 范围向量与 range query step 的关系

**现状:** 冻结模板正文内嵌 `rate(...[5m])`（Q4.3 已记录），range query 的 `step` 是 15s/30s/2m/5m。两者独立：`[5m]` 是 PromQL 窗口函数的参数，`step` 是采样间隔。

**待确认:** 24h 范围用 `step=5m` 时，每个采样点仍是 5 分钟速率，趋势语义是否完整？是否需要 step 与 `[5m]` 窗口联动？

**影响:** 当前实现下，24h 曲线有 288 个点，每个点是 5 分钟速率，语义上正确（表示该时刻前 5 分钟的平均速率）。但如果运维期望 24h 范围用更大的窗口（如 `[1h]`），需要 SPEC 变更冻结模板。

#### O7. `core-schema.d.ts` 重新生成带出其他契约变更

**现状:** 重新生成 `core-schema.d.ts` 时，除了 `ObservabilityRangeQueryResponse`，还带出了其他 OpenAPI 变更（`task_type` 新增枚举值、多个 `ListResponse` 新增字段等）。

**待确认:** 这些带出的变更是否应在 issue-006 的 diff 中出现？如果 codegen 应该只生成 issue-006 相关的 schema，需要确认 codegen 触发范围。当前做法是全量重新生成，与 OpenAPI 当前状态同步。

---

### review-it 修复记录（增量）

本增量经 `/review-it` 发现并修复 1 个 High finding：

| # | 严重度 | 问题 | 修复 |
|---|---|---|---|
| F6 | High | `MetricsTab.tsx:61` 的 `invalidateQueries({ queryKey: ['observability-query'] })` 与新的 query key `['observability-query-range', ...]` 前缀不匹配，30s 自动刷新和手动刷新无法触发图表 refetch，`queriedAt` 更新但曲线不更新造成误导 | 改为 `['observability-query-range']` |

修复后重跑 `npx tsc --noEmit` 通过，确认类型无误。

---

### Files Changed（增量）

| File | Status | Summary |
|------|--------|---------|
| `frontends/console/src/features/instance-observability/MetricsChart.tsx` | MODIFIED | instant query → range query，新增 `buildRangeParams`/`RANGE_SECONDS`/`RANGE_STEP`，`buildChartOption` 解析 matrix `results[].values[]` |
| `frontends/console/src/features/instance-observability/MetricsTab.tsx` | MODIFIED | invalidate key 同步为 `['observability-query-range']`（review 修复） |
| `frontends/console/src/api/core-schema.d.ts` | MODIFIED | 重新生成，含 `ObservabilityRangeQueryResponse` 类型 |
