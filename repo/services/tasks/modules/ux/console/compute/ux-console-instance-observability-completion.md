# UX: 实例可观测性补全（GPU 指标 / 日志持久化 / VM 指标）

> Interaction specification derived from: `repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md`
> Part of ani-workflow artifact triad — next: `/prd-to-spec`
> Generated: 2026-07-20 | Product: Console | UI stack: TDesign React + TanStack Router + TanStack Query + ECharts

---

## 1. Page Type

### 1.1 Classification

| Screen | Page type | In app shell? | Route |
|--------|-----------|---------------|-------|
| 实例详情 - 指标 Tab（VM 增量） | detail tab | yes | `/instances/$instanceId?tab=metrics` |
| 实例详情 - 指标 Tab（GPU 增量） | detail tab | yes | `/instances/$instanceId?tab=metrics` |
| 实例详情 - 日志 Tab（Loki 适配） | detail tab | yes | `/instances/$instanceId?tab=logs` |

本 UX 不新增页面、不新增路由。所有变更是对现有「实例详情 - 可观测性 Tab 组」的**增量行为调整**，复用现有路由、Tab 容器、App Shell。

### 1.2 Pattern Reference

复用现有 `repo/frontends/console/src/features/instance-observability/` 模式：
- `MetricsSnapshot.tsx` — 快照卡片组件（通用 + gpu_container 扩展）
- `MetricsChart.tsx` — 时序图组件（Radio.Group + ECharts）
- `LogsTab.tsx` — 日志列表组件（`useInfiniteQuery` + cursor 分页）
- `promqlTemplates.ts` — PromQL 冻结模板常量
- `observabilityTabsConfig.ts` — kind → 可见 Tab 映射

---

## 2. Information Architecture

### 2.1 Routes & Entry Points

| Route | Entry | Auth required |
|-------|-------|---------------|
| `/instances/$instanceId?tab=metrics` | 实例列表「查看」→ 详情页 → 指标 Tab；深链直接打开 | yes |
| `/instances/$instanceId?tab=logs` | 实例列表「查看」→ 详情页 → 日志 Tab；深链直接打开 | yes |

### 2.2 Navigation Relationship

不变。指标 Tab 和日志 Tab 在实例详情页内的 Tab 组中，位置由 `INSTANCE_OBSERVABILITY_TAB_CONFIG` 决定。VM kind 的 Tab 顺序为 `['logs', 'events', 'metrics', 'console']`，指标 Tab 为第 3 个。

### 2.3 PRD Coverage Map

| PRD item | Screen / section |
|----------|------------------|
| US-001 (handler 透传 Kind) | 后端改动，无 UI 对应（隐式影响 US-003、US-012） |
| US-002 (DCGM scrape) | 后端改动，无 UI 对应（隐式影响 US-003） |
| US-003 (GPU adapter 端到端) | 指标 Tab - 快照卡片 - GPU 卡片（依赖后端） |
| US-004 (LogStore port) | 后端改动，无 UI 对应（隐式影响 US-008） |
| US-005 (Loki adapter) | 后端改动，无 UI 对应（隐式影响 US-008） |
| US-006 (LogStore 注入) | 后端改动，无 UI 对应（隐式影响 US-008） |
| US-007 (Loki 部署 yaml) | 部署改动，无 UI 对应 |
| US-008 (KubeVirt scrape) | 后端改动，无 UI 对应（隐式影响 US-012） |
| US-009 (GetMetrics VM 分支) | 指标 Tab - 快照卡片 - VM 卡片（依赖后端） |
| US-010 (PromQL label 重写) | 后端改动，无 UI 对应（隐式影响 US-011） |
| US-011 (VM PromQL 模板) | 指标 Tab - 时序图 - VM 曲线 |
| US-012 (VM 快照卡片) | 指标 Tab - 快照卡片 - VM 卡片 |

### 2.4 UI 改动范围矩阵

| UI 组件 | gpu_container | vm | logs (Loki) |
|--------|---------------|-----|-------------|
| `MetricsSnapshot.tsx` | 无改动（已就绪） | 无改动（已通用） | — |
| `MetricsChart.tsx` | 无改动（已就绪） | 无改动（由模板列表驱动） | — |
| `promqlTemplates.ts` | 无改动（已就绪） | **新增 VM 模板 + getTemplatesForKind 分支** | — |
| `LogsTab.tsx` | — | — | 无改动（已就绪，cursor 透明） |
| `observabilityTabsConfig.ts` | 无改动 | 无改动（VM 已 `metricsSupported: true`） | 无改动 |

**关键发现：** Console UI 大部分已就绪，真正的 UX 增量仅在 `promqlTemplates.ts` 新增 VM 模板，其他组件行为通过数据驱动自动适配。

---

## 3. User Flow

### 3.1 Primary Flow — VM 用户查看指标

```text
用户进入实例详情页（kind=vm）
  → Tab 组渲染 ['logs','events','metrics','console']
  → 用户点击「指标」Tab
  → MetricsSnapshot 调 GET /instances/{id}/metrics（handler 传 Kind=vm）
  → 后端走 VM 分支，返回 kubevirt_vmi_* 数据
  → 快照卡片渲染 CPU 利用率 / 内存已用+总量 / 网络 RX / 网络 TX
  → null 字段显示「暂不可用」Tag
  → 同时 MetricsChart 调 GET /observability/query_range（VM 模板）
  → 时序图渲染 VM 专用 2 条曲线（CPU 利用率、内存使用率）
  → 用户切换时间范围 Radio.Group
  → 重新拉取对应 range 的曲线数据
```

### 3.2 Primary Flow — GPU 用户查看指标（修复后）

```text
用户进入实例详情页（kind=gpu_container）
  → 用户点击「指标」Tab
  → MetricsSnapshot 调 GET /instances/{id}/metrics（handler 传 Kind=gpu_container）
  → 后端走 GPU 分支（修复后触发），返回 DCGM 数据
  → 快照卡片渲染 CPU / 内存 / 网络 RX / 网络 TX + GPU 利用率 + GPU 显存
  → 同时 MetricsChart 渲染 4 条曲线（CPU、内存、GPU 利用率、GPU 显存使用率）
```

### 3.3 Primary Flow — 用户查看日志（Loki 部署后）

```text
用户进入实例详情页
  → 用户点击「日志」Tab
  → LogsTab 调 GET /instances/{id}/logs（cursor=undefined）
  → 后端 ListLogs 判断 logStore 是否注入
    ├── 已注入 LokiLogStore → 调 Loki query_range，返回持久化日志 + next_cursor
    └── 未注入 → fallback 到 K8s pod log API，返回实时日志 + next_cursor=nil
  → 前端展示首页 100 条日志
  → 若 next_cursor 非 nil，底部显示「加载更多」按钮
  → 用户点击「加载更多」
  → fetchNextPage 传 cursor=next_cursor，拉取下一页
  → 重复直到 next_cursor=nil，按钮隐藏
```

### 3.4 Secondary Flows

- **指标快照刷新**：用户在指标 Tab 停留 30s 后，React Query staleTime 到期，自动后台刷新快照（不显示 loading）
- **指标快照手动重试**：快照加载失败时，Alert 上「重试」按钮触发 `refetch()`
- **时序图切换时间范围**：Radio.Group 切换 15m/1h/6h/24h，触发对应 query 的 refetch
- **时序图单条曲线失败**：`useQueries` 隔离，单条失败不影响其他曲线；全部失败显示错误态
- **日志 403**：无权限查看日志，Alert theme="warning"
- **日志 cursor 失效**：Loki cursor 过期或无效，后端返回错误，前端显示 Alert + 重试

---

## 4. Layout Regions

### 4.1 指标 Tab 整体布局（不变）

```text
┌─────────────────────────────────────────────────────┐
│ [Tab 组：日志 | 事件 | 指标 | terminal/console]      │
├─────────────────────────────────────────────────────┤
│ 指标 Tab 内容                                        │
│ ┌─────────────────────────────────────────────────┐  │
│ │ Row2: 快照卡片区域                              │  │
│ │ 快照时间：2026-07-20 14:30:00                   │  │
│ │ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐            │  │
│ │ │ CPU  │ │ 内存 │ │ 网络 │ │ 网络 │  [+GPU?]  │  │
│ │ │ 利用 │ │ 使用 │ │ RX   │ │ TX   │            │  │
│ │ └──────┘ └──────┘ └──────┘ └──────┘            │  │
│ └─────────────────────────────────────────────────┘  │
│ ┌─────────────────────────────────────────────────┐  │
│ │ Row3: 时序图工具条                              │  │
│ │ [15m | 1h | 6h | 24h]    趋势数据查询于 14:30   │  │
│ ├─────────────────────────────────────────────────┤  │
│ │ Row4: ECharts 时序图（高度 320px）              │  │
│ │ ┌─────────────────────────────────────────────┐│  │
│ │ │ legend: CPU 利用率 / 内存使用率 [/ GPU...]   ││  │
│ │ │ 折线图 X 轴: 时间, Y 轴: 利用率(%)            ││  │
│ │ └─────────────────────────────────────────────┘│  │
│ └─────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────┘
```

### 4.2 VM 快照卡片布局（kind=vm，复用现有 Row）

```text
┌─────────────────────────────────────────────────────┐
│ 快照时间：2026-07-20 14:30:00                        │
├─────────────────────────────────────────────────────┤
│ ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌────────────┐ │
│ │ CPU 利用率  │ │ 内存使用    │ │ 网络接收    │ │ 网络发送    │ │
│ │            │ │            │ │ （RX）      │ │ （TX）      │ │
│ │   45.2 %   │ │ 已用：2.1GB│ │  1.2 MB    │ │  0.8 MB    │ │
│ │            │ │ 总量：4.0GB│ │            │ │            │ │
│ └────────────┘ └────────────┘ └────────────┘ └────────────┘ │
│                   (无 GPU 卡片)                              │
└─────────────────────────────────────────────────────┘
```

VM 内存卡片复用现有 `MemorySnapshot` 组件（双值：已用 + 总量），数据源改为 `kubevirt_vmi_memory_domain_bytes`（总量）和 `domain - usable`（已用）。

### 4.3 GPU 快照卡片布局（kind=gpu_container，已实现，无改动）

```text
┌─────────────────────────────────────────────────────┐
│ ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐ │
│ │ CPU    │ │ 内存   │ │ 网络   │ │ 网络   │ │ GPU    │ │ GPU    │ │
│ │ 利用率 │ │ 使用   │ │ RX     │ │ TX     │ │ 利用率 │ │ 显存   │ │
│ └────────┘ └────────┘ └────────┘ └────────┘ └────────┘ └────────┘ │
└─────────────────────────────────────────────────────┘
```

### 4.4 VM 时序图布局（kind=vm，2 条曲线）

```text
┌─────────────────────────────────────────────────────┐
│ [15m | 1h | 6h | 24h]      趋势数据查询于 14:30      │
├─────────────────────────────────────────────────────┤
│ legend: ■ CPU 利用率  ■ 内存使用率                   │
│ 100% ┤                                                │
│     │     ┌─── CPU 利用率                              │
│  50% ┤   /    \                                       │
│     │  /      \─── 内存使用率                          │
│   0% └─────────────────────────────────────────        │
│     14:00      14:15      14:30      14:45   15:00     │
└─────────────────────────────────────────────────────┘
```

VM kind 的时序图**只展示 2 条曲线**（CPU 利用率、内存使用率），不展示网络 RX/TX（依据用户决策，见 §8.3 OQ-1）。

### 4.5 Region 详细说明

| Screen | Region | Content | Notes |
|--------|--------|---------|-------|
| 指标 Tab - VM | Row2 快照区 | 4 个卡片：CPU 利用率、内存使用（已用+总量）、网络 RX、网络 TX | 无 GPU 卡片 |
| 指标 Tab - VM | Row3 工具条 | Radio.Group 15m/1h/6h/24h + 查询时间标注 | 与 container 一致 |
| 指标 Tab - VM | Row4 图表 | 2 条曲线（CPU 利用率、内存使用率） | 用户决策：不展示网络 |
| 指标 Tab - GPU | Row2 快照区 | 6 个卡片：CPU、内存、网络 RX、网络 TX、GPU 利用率、GPU 显存 | 已实现 |
| 指标 Tab - GPU | Row4 图表 | 4 条曲线（CPU、内存、GPU 利用率、GPU 显存使用率） | 已实现 |
| 日志 Tab - Loki | 日志列表 | 100 条/页 + 「加载更多」按钮 | cursor 透明驱动 |

---

## 5. Component Mapping

### 5.1 现有组件复用（无新增组件）

| UI element | TDesign 组件 | Props / variant | 数据源 | 改动 |
|------------|-------------|-----------------|--------|------|
| 快照卡片 - CPU | `Card` + 自定义 StatisticSnapshot | `bordered`, title="CPU 利用率" | `metrics.cpu_utilization_pct` | 无 |
| 快照卡片 - 内存 | `Card` + 自定义 MemorySnapshot | `bordered`, title="内存使用" | `metrics.memory_used_mb` / `memory_total_mb` | 无（VM 数据源在后端切换） |
| 快照卡片 - 网络 RX | `Card` + StatisticSnapshot | `bordered`, title="网络接收（RX）" | `metrics.network_rx_bytes` | 无 |
| 快照卡片 - 网络 TX | `Card` + StatisticSnapshot | `bordered`, title="网络发送（TX）" | `metrics.network_tx_bytes` | 无 |
| 快照卡片 - GPU 利用率 | `Card` + StatisticSnapshot | `bordered`, title="GPU 利用率" | `metrics.gpu_utilization_pct` | 无（已实现） |
| 快照卡片 - GPU 显存 | `Card` + MemorySnapshot | `bordered`, title="GPU 显存" | `metrics.gpu_memory_used_mb` / `gpu_memory_total_mb` | 无（已实现） |
| 「暂不可用」标识 | `Tag` | `theme="warning"`, `variant="light"` | null 字段判断 | 无 |
| 时序图工具条 | `Radio.Group` | `variant="default-filled"`, 4 个 Radio | 用户选择 | 无 |
| 时序图图表 | `ReactECharts`（echarts-for-react） | `height=320`, `notMerge`, `lazyUpdate` | PromQL 查询结果 | 无 |
| 日志列表 | `Table` | `loading`, `Empty`, `Alert` | `useInfiniteQuery` | 无 |
| 「加载更多」按钮 | `Button` | `theme="primary"`, `variant="outline"`, `loading={isFetchingNextPage}` | `hasNextPage` | 无 |
| 错误提示 | `Alert` | `theme="error"` + `operation` 重试按钮 | API error | 无 |

### 5.2 新增/改动的代码模块

| 模块 | 改动 | UX 对应 |
|------|------|---------|
| `promqlTemplates.ts` | 新增 `instance_vm_cpu_utilization` 模板 ID | §4.4 VM 时序图 CPU 曲线 |
| `promqlTemplates.ts` | 新增 `instance_vm_memory_utilization` 模板 ID | §4.4 VM 时序图 内存曲线 |
| `promqlTemplates.ts` | `getTemplatesForKind` 新增 `kind === 'vm'` 分支，返回 2 个 VM 模板 | §4.4 VM 仅 2 条曲线 |
| `promqlTemplates.ts` | `PROMQL_TEMPLATE_LABELS` 新增 VM 模板中文系列名 | §7.1 标签文案 |
| `promqlTemplates.ts` | `SERIES_COLORS`（在 MetricsChart）新增 VM 模板配色 | §4.4 曲线配色 |

### 5.3 TDesign Token 使用（延续现有）

| 用途 | Token | 说明 |
|------|-------|------|
| 文本占位色（「暂不可用」） | `--td-text-color-placeholder` | 已使用 |
| 主色（CPU 曲线） | `#0052D9`（`--td-brand-color`） | 已使用 |
| 成功色（内存曲线） | `#2BA471`（`--td-success-color`） | 已使用 |
| 危险色（GPU 利用率） | `#D54941`（`--td-error-color`） | 已使用 |
| 警告色（GPU 显存） | `#E37318`（`--td-warning-color`） | 已使用 |
| VM 曲线配色 | 待定（建议 `#8B5CF6` 紫色 或 `#00A870` 青绿） | SPEC 决定 |

---

## 6. State Design

### 6.1 指标 Tab - 快照卡片状态（VM kind）

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| idle | 加载成功，有数据 | 展示 4 个卡片（CPU、内存、网络 RX、网络 TX），数值正常显示 | `Card` + `StatisticSnapshot` / `MemorySnapshot` |
| idle - partial-null | 加载成功，部分字段 null | null 字段显示「暂不可用」Tag（`theme="warning"`） | `Tag` |
| loading | 首次加载 | `Skeleton animation="gradient"` | `Skeleton` |
| error | API 失败 | `Alert theme="error"` + 错误消息 + 请求 ID + 重试按钮 | `Alert` + `Button` |
| forbidden | 403 | `Alert theme="warning"`「无权限查看指标」 | `Alert` |
| stale-refresh | 30s 后后台刷新 | UI 不变，后台 refetch | — |

### 6.2 指标 Tab - 时序图状态（VM kind）

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| idle | 至少 1 条曲线有数据 | 展示 ECharts 图表 + 工具条 | `Radio.Group` + `ReactECharts` |
| loading | 首次加载 | `Loading text="加载趋势数据中…"`（高度 320px 居中） | `Loading` |
| empty | 所有曲线无数据 | `Empty description="所选时间范围暂无数据"` | `Empty` |
| error - all | 全部曲线失败 | `Alert theme="error"` + 错误消息 + 请求 ID + 重试 | `Alert` + `Button` |
| error - forbidden | 403 且无数据 | `Alert theme="warning"`「无权限查看趋势数据」 | `Alert` |
| error - partial | 部分曲线失败 | 只渲染成功的曲线，失败的曲线不显示 | `ReactECharts` |
| range-change | 切换时间范围 | 工具条 Radio 选中态变化，图表 loading 后刷新 | `Radio.Group` |

### 6.3 指标 Tab - 时序图状态（GPU kind，已实现，无改动）

| State | Trigger | UI behavior |
|-------|---------|-------------|
| idle | 4 条曲线有数据 | ECharts 渲染 4 条曲线 |
| error - partial | DCGM 不可用，container 指标可用 | 只渲染 CPU/内存/网络曲线，GPU 曲线失败不显示 |

### 6.4 日志 Tab 状态（Loki 适配后）

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| idle | 首页加载成功 | 展示 100 条日志 | `Table` |
| idle - has-more | `next_cursor` 非 nil | 底部显示「加载更多」按钮 | `Button` |
| idle - no-more | `next_cursor` 为 nil | 不显示「加载更多」按钮 | — |
| loading | 首页加载 | `Table loading` | `Table` |
| loading-more | fetchNextPage 中 | 「加载更多」按钮 `loading=true` | `Button` |
| empty | 无日志 | `Empty description="暂无日志"` | `Empty` |
| error | API 失败 | `Alert theme="error"` + 错误消息 + 请求 ID + 重试 | `Alert` + `Button` |
| forbidden | 403 | `Alert theme="warning"`「无权限查看日志」 | `Alert` |
| cursor-invalid | Loki cursor 过期 | 作为 error 处理，提示重试 | `Alert` |

**Loki 透明性：** 用户无法从 UI 直接区分「Loki 持久化日志」与「K8s pod 实时日志」。`next_cursor` 是否非 nil 自然决定是否可分页（依据用户决策，见 §8.4 假设 1）。

### 6.5 Edge States

| 场景 | UI 行为 |
|------|---------|
| VM 实例但 KubeVirt virt-handler 不可用 | 快照卡片字段 null 显示「暂不可用」，时序图 empty 状态 |
| GPU 实例但 DCGM exporter 不可用 | GPU 卡片显示「暂不可用」，CPU/内存/网络正常；时序图 GPU 曲线失败，其他正常 |
| Loki 部署但 MinIO 不可用 | Loki adapter 返回错误，日志 Tab 显示 error + 重试 |
| 未部署 Loki | 后端 fallback 到 K8s API，`next_cursor` 为 nil，无「加载更多」按钮 |
| 实例已删除 | API 返回 404，Tab 内容显示 error + 「实例不存在」 |

---

## 7. Copy & Feedback

### 7.1 Labels & Buttons

| Element | Copy (zh-CN) | Notes |
|---------|--------------|-------|
| Tab 标题 | 指标 / 日志 / 事件 / 终端 / 控制台 | 已实现 |
| 快照卡片 - CPU | CPU 利用率 | 已实现 |
| 快照卡片 - 内存 | 内存使用 | 已实现 |
| 快照卡片 - 网络 RX | 网络接收（RX） | 已实现 |
| 快照卡片 - 网络 TX | 网络发送（TX） | 已实现 |
| 快照卡片 - GPU 利用率 | GPU 利用率 | 已实现 |
| 快照卡片 - GPU 显存 | GPU 显存 | 已实现 |
| 内存卡片 - 已用 | 已用： | 已实现 |
| 内存卡片 - 总量 | 总量： | 已实现 |
| 时序图 - 时间范围 | 15m / 1h / 6h / 24h | 已实现 |
| 时序图 - 查询时间 | 趋势数据查询于 {time} | 已实现 |
| 时序图系列 - VM CPU | CPU 利用率 | **新增**（VM kind） |
| 时序图系列 - VM 内存 | 内存使用率 | **新增**（VM kind） |
| 日志 - 加载更多 | 加载更多 | 已实现 |
| 重试按钮 | 重试 | 已实现 |
| 「暂不可用」 | 暂不可用 | 已实现 |

### 7.2 Messages

| Scenario | Type | Copy |
|----------|------|------|
| 快照加载失败 | `Alert theme="error"` | 无法加载指标快照（+ 请求 ID） |
| 快照 403 | `Alert theme="warning"` | 无权限查看指标 |
| 趋势数据加载失败 | `Alert theme="error"` | 加载趋势数据失败（+ 请求 ID） |
| 趋势数据 403 | `Alert theme="warning"` | 无权限查看趋势数据 |
| 趋势数据无数据 | `Empty` | 所选时间范围暂无数据 |
| 趋势数据加载中 | `Loading` | 加载趋势数据中… |
| 日志加载失败 | `Alert theme="error"` | 无法加载日志（+ 请求 ID） |
| 日志 403 | `Alert theme="warning"` | 无权限查看日志 |
| 日志为空 | `Empty` | 暂无日志 |
| cursor 失效 | `Alert theme="error"` | 无法加载日志（作为普通 error 处理，不单独文案） |
| 实例不存在 | `Alert theme="error"` | 实例不存在或已删除 |

文案遵循 B2B 简洁风格，不使用营销语气。

### 7.3 Tooltip & 辅助说明

| Element | Copy | 触发 |
|---------|------|------|
| 快照时间标注 | 快照时间：{timestamp} | 始终展示 |
| UI 刷新时间 | （UI 刷新于 {timestamp}） | 有 `dataUpdatedAt` 时展示 |
| 请求 ID | （请求 ID：{request_id}） | error 时附在消息后 |

---

## 8. Boundaries & Non-Goals

### 8.1 In Scope (UX)

- 指标 Tab 在 `kind=vm` 时的快照卡片展示（复用现有组件，数据驱动）
- 指标 Tab 在 `kind=vm` 时的时序图 2 条曲线（CPU 利用率、内存使用率）
- 指标 Tab 在 `kind=gpu_container` 时的快照和时序图（已实现，无改动）
- 日志 Tab 在 Loki 部署后的 cursor 真分页（已实现，无改动）
- VM 内存卡片复用现有 `MemorySnapshot` 双值形式
- VM kind 的 PromQL 模板新增（`promqlTemplates.ts` 增量）

### 8.2 Explicitly Out of Scope

- 不新增页面、不新增路由、不新增 Tab
- 不展示「日志已持久化」标识（用户决策，见 §8.4 假设 1）
- 不展示 VM 网络时序曲线（用户决策，见 §8.3 OQ-1）
- 不展示 Prometheus 地址、Loki 地址、KubeVirt 地址
- 不在 UI 暴露 `INSTANCE_OBSERVABILITY_LOG_STORE` 环境变量状态
- 不展示 `record.Kind` 字段值
- 不在快照卡片展示 VMI name、namespace 等内部映射
- 不新增「刷新」按钮（依赖 React Query staleTime 自动刷新）
- 不支持自定义时间范围（仅 4 个预设：15m/1h/6h/24h）
- 不支持时序图导出 CSV/PNG
- 不支持日志全文搜索、日志级别过滤（延续原 PRD Non-Goals）
- Boss 平台大盘 UI（延续原 PRD Non-Goals）
- 节点池（`k8s_cluster` node pool）路径的 VM 指标（延续原 PRD Non-Goals）

### 8.3 Open UX Questions

| ID | 问题 | 影响 | 状态 |
|----|------|------|------|
| OQ-1 | **VM 时序图曲线数量与 PRD US-011 AC 一致性**：PRD US-011 AC 已修订为"VM 时序图只展示 2 条曲线（CPU 利用率、内存使用率），不展示网络 RX/TX 时序曲线"。UX 与 PRD 对齐 | 无（已对齐） | ✅ 已解决 |
| OQ-2 | VM 时序图曲线配色未定：建议复用 container 的 CPU/内存配色（蓝/绿），还是用新配色（紫/青）区分 VM 与 container？ | `promqlTemplates.ts` SERIES_COLORS 扩展 | SPEC |
| OQ-3 | VM 内存卡片的「已用」数值是否需要标注「= domain - usable」公式来源？当前 UX 决策是不标注，与 container 内存卡片保持一致 | 无（保持不标注） | — |
| OQ-4 | VM kind 的时序图 Y 轴范围：CPU 利用率 0-100%，内存使用率 0-100%？还是内存使用率也用 0-100%？当前 ECharts option 写死 `min:0, max:100`，VM 内存使用率作为百分比也适用 | 无（复用现有 option） | — |

### 8.4 Assumptions

1. **Loki 透明性假设**：用户无需从 UI 区分「Loki 持久化日志」与「K8s 实时日志」。`next_cursor` 非 nil 即可分页，nil 即停止分页，对用户自然透明。后端 `INSTANCE_OBSERVABILITY_LOG_STORE` 环境变量不暴露到 UI。
2. **VM 内存卡片数据源假设**：VM 内存卡片复用 `MemorySnapshot` 组件，后端 `getInstanceMetrics` 返回的 `memory_used_mb` 和 `memory_total_mb` 字段在 VM kind 时已对应 `kubevirt_vmi_memory_domain_bytes - kubevirt_vmi_memory_usable_bytes`（已用）和 `kubevirt_vmi_memory_domain_bytes`（总量）。前端无需感知公式差异，只消费字段。
3. **VM 网络快照卡片假设**：VM 快照卡片仍展示网络 RX/TX 4 个卡片（CPU、内存、网络 RX、网络 TX），与时序图仅 2 条曲线的策略不冲突 — 快照是瞬时值，时序图是趋势，两者独立。
4. **使用现有 Console Shell**：复用 `_authenticated` layout、App Shell、TanStack Router、TanStack Query，无新增 layout。
5. **ECharts 版本延续**：继续使用 `echarts-for-react`，不切换图表库。
6. **TDesign 版本延续**：继续使用现有 TDesign React 组件，不升级版本。
7. **深链行为**：`?tab=metrics` 深链对 VM kind 有效，进入后默认选中「指标」Tab。

---

## References

- PRD: `repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability-completion.md`
- 原 PRD: `repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md`
- 原 UX: `repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability.md`（如有）
- Module main doc: `repo/services/docs/console-modules/compute/container-observability.md`
- OpenAPI: `repo/api/openapi/v1.yaml`（不改，consume only）
- 现有代码：
  - `repo/frontends/console/src/features/instance-observability/MetricsSnapshot.tsx`
  - `repo/frontends/console/src/features/instance-observability/MetricsChart.tsx`
  - `repo/frontends/console/src/features/instance-observability/LogsTab.tsx`
  - `repo/frontends/console/src/features/instance-observability/promqlTemplates.ts`
  - `repo/frontends/console/src/features/instance-observability/observabilityTabsConfig.ts`
- UI 规范：
  - `UI规范/产品设计规范-设计原则-2.0.md`
  - `UI规范/产品设计规范-TDesign组件与Token-2.0.md`
  - `UI规范/产品设计规范-页面模板-2.0.md`
