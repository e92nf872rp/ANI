# UX: Metering 平台（Console 租户用量 + BOSS 平台计量）

> Interaction specification derived from: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`
> Part of ani-workflow artifact triad — next: `/prd-to-spec`
> Generated: 2026-07-09 | Product: **Console + BOSS** | UI stack: **TDesign React + TanStack Router + React Query + ECharts**
> **Patch:** 2026-07-09 — 对齐 PRD v1.4（钻取 API、token_total、单位展示、local 算力空态）
> Module main docs:
> - Console: `repo/services/docs/console-modules/tenant/usage-report.md`
> - BOSS: `repo/services/docs/boss-modules/tenant/tenant-usage-billing.md`
> - BOSS: `repo/services/docs/boss-modules/metering/README.md`

**范围：** Console 租户用量报表 P0 增强；BOSS 租户计费与用量聚合页 + 平台计量 7 专页交互。
**不含：** metering-service 后端、gRPC/NATS 实现、OpenAPI YAML 变更（→ SPEC）；US-001/US-005/US-006 无终端 UI。

---

## 1. Page Type

### 1.1 Classification

| Screen | Product | Page type | In app shell? | Route |
|--------|---------|-----------|---------------|-------|
| Console 租户用量报表 | Console | report / dashboard（模板 C + 图表） | 是 | `/_authenticated/usage` |
| BOSS 租户计费与用量 | BOSS | report / hub（跨租户聚合） | 是 | `/_authenticated/tenant/usage-billing`（建议） |
| BOSS 平台 GPU-Hours | BOSS | report（单指标专页） | 是 | `/_authenticated/metering/gpu-hours` |
| BOSS 平台 CPU-Hours | BOSS | report | 是 | `/_authenticated/metering/cpu-hours` |
| BOSS 平台 Memory-GBHours | BOSS | report | 是 | `/_authenticated/metering/memory-gbhours` |
| BOSS 平台 Input Tokens | BOSS | report | 是 | `/_authenticated/metering/input-tokens` |
| BOSS 平台 Output Tokens | BOSS | report | 是 | `/_authenticated/metering/output-tokens` |
| BOSS 平台 Storage-GBDays | BOSS | report（P1 占位） | 是 | `/_authenticated/metering/storage-gbdays` |
| BOSS 平台 KB Queries | BOSS | report（P1 占位） | 是 | `/_authenticated/metering/kb-queries` |
| 单租户钻取抽屉 | BOSS | drawer（只读明细） | — | 从 BOSS 排行行打开，无独立路由 |

> **Console 创建/编辑/删除：** 本特性 **无** CRUD；只读报表。
> **BOSS 专页：** P0 交付 **5 页**（GPU/CPU/Memory/Input/Output）；Storage/KB 路由占位 api-not-ready。7 页共享同一交互模板。

### 1.2 Pattern Reference

| 参考 | 说明 |
|------|------|
| `usage-report.md` | Console 区块、字段、状态口径 |
| `tenant-usage-billing.md` | BOSS 聚合页结构、钻取抽屉 |
| `platform-input-tokens.md` 等 7 专页 | 单指标排行 + 趋势 |
| `repo/frontends/console/src/routes/_authenticated/usage.tsx` | 当前最小实现（仅 Table）；本 UX 在其上增强 |
| `ux-console-network-management.md` | 多屏 UX 文档结构参考 |
| 页面模板 §2 通用骨架 | PageHeader + Filter Bar + Content |
| 页面模板 §6 模板 C | 摘要卡 / 视角切换 |

---

## 2. Information Architecture

### 2.1 Routes & Entry Points

| Route | Entry | Auth / scope |
|-------|-------|----------------|
| Console `/usage` | 侧栏「用量报表」；首页「用量趋势」链接 | JWT 租户 + `scope:metering:read` |
| BOSS `/tenant/usage-billing` | 侧栏「租户与客户 → 租户计费与用量」 | 平台 + `scope:metering:platform:read` |
| BOSS `/metering/*` | 侧栏「平台计量与结算」子项；聚合页「查看专页」 | 同上 |
| Console 首页 `/` | 「查看用量报表」深链 | 同上 |

### 2.2 Navigation Relationship

```text
Console
  └── 用量与计量（侧栏）
        └── 租户用量报表  →  /usage

BOSS
  └── 租户与客户
        └── 租户计费与用量  →  /tenant/usage-billing
  └── 平台计量与结算
        ├── 平台 GPU-Hours      →  /metering/gpu-hours
        ├── 平台 CPU-Hours      →  /metering/cpu-hours
        ├── …（其余 5 专页）
        └── （聚合页可跳转专页，专页面包屑可回聚合页）
```

**面包屑示例（BOSS 专页）：**

- `平台计量与结算 / Input Tokens`
- `租户与客户 / 租户计费与用量`（聚合页无第三级）

### 2.3 PRD Coverage Map

| PRD 项 | UX 屏幕 / 区域 |
|--------|----------------|
| US-001 inference 上报 | 无 UI（§8.2）；Console/BOSS 仅消费结果 |
| US-002 算力可读 | Console `/usage` 预设视角 + FR-12 横幅 |
| US-003 Console 租户报表 | §4.1 全文 |
| US-004 BOSS 跨租户 | §4.2 聚合页 + §4.3 专页 + 钻取抽屉 |
| US-005 kb 上报 | 无 UI；BOSS KB 专页 P1 前禁用 |
| US-006 运维可观测 | 无 UI（§8.2） |
| FR-4 / FR-12 | Console 查询与 dev 横幅 |
| FR-5 / FR-8 / FR-15 / FR-16 | BOSS platform API、钻取、四态 |
| FR-17 / FR-18 | token_total 展示、单位原样展示 |
| NG-1 / NG-7 | §8.2 不展示账单金额；Storage/KB 禁用 |

---

## 3. User Flow

### 3.1 Primary Flow — Console 租户查看用量

```text
1. 用户进入 /usage（已登录租户上下文）
2. 默认时间范围：近 30 天（DateRangePicker 可改）
3. 筛选变更 **300ms debounce 自动查询**（无单独「查询」按钮）
4. 可选：预设视角 Tab（GPU/CPU/Memory/Input/Output Tokens…）
   - P0 可用视角：YAML 已冻结 5 类 Tab（**无** 独立 Token Total Tab，见 FR-17）
   - 未选 resource_type 时，表格可展示 API 返回的 `token_total` 行
   - Storage / KB：Tab disabled + Tooltip「待 API」
5. 可选：group_by Segmented（resource_type | az | day | hour）
6. refetch → GET /metering/usage
7. 趋势图（ECharts）与明细 Table 同步渲染 items[]；**total_quantity + unit 原样展示**（FR-18）
8. 若响应含 dev_profile 且非 production → 页顶 Alert 横幅
9. items 为空 → Empty，不画全 0 折线（算力 Tab 在 local 下空态为预期）
```

### 3.2 Primary Flow — BOSS 平台跨租户分析

```text
1. 平台运营进入 /tenant/usage-billing
2. 若 platform API 未合入 → 页顶 Alert「平台计量 API 待上线」+ 禁用排行/趋势（api-not-ready 态）
3. API 就绪后：选时间范围 + 指标视角（映射 resource_type）；筛选 **debounce 自动查询**
4. 可选：租户 ID 筛选（Select，须后端 RBAC 校验）
5. group_by：day | hour | tenant_id（平台 API）
6. GET /metering/usage/platform → 排行 Table + 趋势图
7. 点击排行某行「查看明细」→ Drawer 打开
   - **强制 API：** GET /metering/usage/platform?tenant_id={行 tenant_id}&start_time=…&end_time=…
     + 继承当前 resource_type；Drawer 内 group_by 默认 day|hour
   - **禁止：** GET /metering/usage（租户 JWT）、JWT 轮询、impersonate 租户
   - 字段口径对齐 Console usage-report（resource_type, total_quantity, unit, period）
   - 若主查询 group_by=tenant_id 且行数据已含明细级 items → 可不再二次请求
8. 从聚合页链接进入某专页（如 Input Tokens）→ 固定 resource_type 的单指标深页
9. Storage/KB 专页路由可进，内容区 api-not-ready / Empty「待 API」（P0-C）
```

### 3.3 Secondary Flows

| 流程 | 行为 |
|------|------|
| 403 无权限 | Alert + 隐藏图表/表格；Console 与 BOSS 文案区分租户/平台 |
| 查询失败 | Alert +「重试」Button；保留用户已选时间/视角 |
| 从首页深链 | Console `/usage` 可带 query 预设时间（可选 P1） |
| BOSS → Console 口径对照 | Drawer 脚注：字段与 Console 一致；**API 为 platform path + tenant_id**（FR-16） |
| P1 Storage/KB 上线 | 解除 Tab disabled，专页从「待 API」变为正常四态 |

### 3.4 Flow Diagram

```mermaid
flowchart TB
  subgraph Console
    C1[/usage] --> C2[筛选 time + 视角 + group_by]
    C2 --> C3[GET /metering/usage]
    C3 --> C4[Chart + Table]
  end
  subgraph BOSS
    B1[/tenant/usage-billing] --> B2[GET /metering/usage/platform]
    B2 --> B3[Rank Table + Trend]
    B3 --> B4[Drawer: platform API + tenant_id]
    B1 --> B5[/metering/input-tokens 等专页]
  end
  subgraph Backend
    M[metering-service 上报] --> W[POST /metering/token-usage]
    W --> C3
    W --> B2
  end
```

---

## 4. Layout Regions

### 4.1 Console — `/usage`

```text
┌─────────────────────────────────────────────────────────────┐
│ PageHeader：租户用量报表                                      │
├─────────────────────────────────────────────────────────────┤
│ [Alert] dev_profile / 边界说明（FR-12，条件显示）              │
├─────────────────────────────────────────────────────────────┤
│ 筛选区：DateRangePicker | 预设视角 Tabs | group_by Segmented   │
│          [查询]（若 auto-fetch 则省略按钮）                     │
├─────────────────────────────────────────────────────────────┤
│ 趋势图区（ECharts 折线/柱，按 group_by 时间桶）                 │
├─────────────────────────────────────────────────────────────┤
│ 明细表格（Table：resource_type, total_quantity, unit, period）│
├─────────────────────────────────────────────────────────────┤
│ 边界说明 Collapse：不含账单/发票；写入 API 不对租户开放          │
└─────────────────────────────────────────────────────────────┘
```

| Region | Content | Notes |
|--------|---------|-------|
| Alert | `theme="warning"` dev 横幅 | `dev_profile` 存在且标记 local 时显示 |
| 筛选区 | 时间必填 | 校验 start < end |
| 预设视角 Tabs | UI 名 → resource_type | Storage/KB disabled |
| 趋势图 | ECharts | 与 Table 同 queryKey |
| 明细表 | Table | rowKey 建议 `resource_type+period` |
| 边界说明 | Text / Collapse | 静态文案 |

### 4.2 BOSS — `/tenant/usage-billing`（聚合页）

```text
┌─────────────────────────────────────────────────────────────┐
│ PageHeader：租户计费与用量                                    │
├─────────────────────────────────────────────────────────────┤
│ [Alert] API 未就绪 | dev_profile 横幅（互斥优先级见 §6）       │
├─────────────────────────────────────────────────────────────┤
│ 筛选：DateRange | 指标视角 | 租户 Select(optional) | group_by  │
├─────────────────────────────────────────────────────────────┤
│ KPI 条（可选）：全平台 total_quantity 汇总                      │
├─────────────────────────────────────────────────────────────┤
│ 租户排行 Table（tenant_id, total_quantity, unit, 操作）        │
├─────────────────────────────────────────────────────────────┤
│ 趋势图（按 day/hour/tenant_id）                               │
├─────────────────────────────────────────────────────────────┤
│ 快捷入口 Link：跳转各平台计量专页                               │
└─────────────────────────────────────────────────────────────┘
```

| Region | Content | Notes |
|--------|---------|-------|
| 排行表 | Table + 行操作「查看明细」 | 打开 Drawer |
| 钻取 Drawer | 单租户明细 Table/Chart | 宽度 `size="large"` |
| 专页入口 | Link 组 | 7 专页 |

### 4.3 BOSS — 平台计量专页（7 页同模板）

```text
┌─────────────────────────────────────────────────────────────┐
│ PageHeader：平台 {Input Tokens | GPU-Hours | …}               │
├─────────────────────────────────────────────────────────────┤
│ 筛选：DateRange | group_by(day|hour|tenant_id) | 租户筛选(可选)│
├─────────────────────────────────────────────────────────────┤
│ 平台 KPI：该 resource_type 全平台汇总                         │
├─────────────────────────────────────────────────────────────┤
│ 租户排行 Table（固定 resource_type）                          │
├─────────────────────────────────────────────────────────────┤
│ 趋势图                                                        │
├─────────────────────────────────────────────────────────────┤
│ 边界说明：POST token-usage 为写入侧，非本页查询                 │
└─────────────────────────────────────────────────────────────┘
```

**专页固定 query：** `resource_type` 写死，UI 不提供切换（与聚合页「指标视角」区分）。

---

## 5. Component Mapping

### 5.1 Console `/usage`

| UI element | TDesign / 项目组件 | Props / variant | Data / API |
|------------|-------------------|-----------------|------------|
| 页面壳 | `ConsolePage`, `ConsolePageHeader`, `ConsoleContentCard` | — | — |
| 筛选区 | DateRangePicker | `enableTimePicker`, 必填 | → `start_time`, `end_time`；变更 debounce 300ms 触发查询 |
| 预设视角 | `Tabs` | `theme="card"` | → `resource_type` query；**无** Token Total Tab |
| 分组维度 | `Radio.Group` / `Segmented` | 4 enum | → `group_by` |
| 查询按钮 | — | P0 **省略**；debounce auto-fetch | refetch on filter change |
| dev 横幅 | `Alert` | `theme="warning"`, `close` 可选 | `dev_profile` |
| 趋势图 | `ReactECharts` | 折线；loading 态 skeleton | `items[]` |
| 明细表 | `Table` | columns 见下 | `items[]` |
| 空态 | `Empty` | `description="当前时间范围内暂无用量"` | items.length=0 |
| 边界说明 | `Collapse` / `Typography.Text` | secondary | 静态 |

**Table columns（Console）：**

| title | colKey | 来源字段 |
|-------|--------|----------|
| 资源类型 | `resource_type` | API（可映射中文 label） |
| 用量 | `total_quantity` | API；**原样展示**，P0 不换算 |
| 单位 | `unit` | API；**原样展示**（Tab 文案如 GPU 算力 ≠ 强制 hours） |
| 统计周期 | `period` | API，可空显示 `—` |

**预设视角 → resource_type（P0 启用）：**

| Tab 文案 | resource_type |
|----------|---------------|
| GPU 算力 | `instance_gpu_seconds` |
| CPU 算力 | `instance_cpu_seconds` |
| 内存 | `instance_memory_gib_seconds` |
| Input Tokens | `token_input` |
| Output Tokens | `token_output` |
| （无 Tab） | `token_total` — 仅未筛 resource_type 时表格可出现 |
| 存储（P1） | `storage_gb_days` — **disabled** |
| 知识库查询（P1） | `kb_query_count` — **disabled** |

### 5.2 BOSS 聚合页 + 专页

| UI element | TDesign | Props / variant | Data / API |
|------------|---------|-----------------|------------|
| 页面壳 | BOSS 壳组件（与现有 BOSS 页一致） | — | — |
| API 未就绪 | `Alert` | `theme="warning"` | platform path 404/501 |
| 时间范围 | `DateRangePicker` | 必填 | start/end |
| 指标视角 | `Select` / `Tabs` | 聚合页可切换；专页 hidden | resource_type |
| 租户筛选 | `Select` | `filterable`, clearable | `tenant_id` query |
| group_by | `Select` | 含 `tenant_id` | platform API only |
| 排行表 | `Table` | sortable on quantity | `items[]` |
| 查看明细 | `Button` `variant="text"` | 行内 | 打开 Drawer |
| 钻取 Drawer | `Drawer` | `size="large"`, `footer=false` | **GET /metering/usage/platform?tenant_id=…** |
| Drawer 内表 | `Table` | 同 Console 列 | platform API 响应 items[] |
| 趋势图 | `ReactECharts` | 与 Console 同构 | `items[]` |
| 跳转专页 | `Link` | — | `/metering/...` |

**Table columns（BOSS 排行）：**

| title | colKey | 说明 |
|-------|--------|------|
| 租户 ID | `tenant_id` | platform 响应必填 |
| 资源类型 | `resource_type` | 专页固定 |
| 用量 | `total_quantity` | — |
| 单位 | `unit` | — |
| 周期 | `period` | group_by 时间桶时显示 |
| 操作 | — | 「查看明细」 |

---

## 6. State Design

### 6.1 Console `/usage`

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| idle / success | 200 + items>0 | 图表+表格正常 | Chart, Table |
| loading | query fetching | Table `loading`；Chart Skeleton | Table, Skeleton |
| empty | 200 + items=[] | Empty；**不**渲染假折线；算力 Tab 在 local 空态 **预期** | Empty |
| error | 4xx/5xx/network | Alert + 重试；保留筛选 | Alert, Button |
| forbidden | 403 | Alert「无权限查看用量」；隐藏数据区 | Alert |
| dev_profile | 200 + dev_profile 标记 | 页顶 Warning Alert 固定文案 | Alert |
| invalid range | start≥end | DateRange 下 inline 错误；禁用查询 | Form feedback |
| tab disabled | Storage/KB P0 | Tab disabled + Tooltip「待 API 合入」 | Tabs |

**FR-12 横幅文案（固定）：**
「当前为联调/开发环境数据，非生产真实计量；生产可用性待 live 验证。」

### 6.2 BOSS 聚合页 + 专页

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| api-not-ready | platform API 未实现 / 501 | 全页 Alert；排行/图表区 disabled 或隐藏 | Alert |
| loading | fetching | 同 Console | Table, Skeleton |
| empty | 200 + items=[] | Empty「当前条件下暂无租户用量」 | Empty |
| error | 请求失败 | Alert + 重试 | Alert, Button |
| forbidden | 403 | Alert「无平台计量查看权限」 | Alert |
| dev_profile | 同 Console | Warning 横幅 | Alert |
| drilldown loading | Drawer 内 platform 二次请求 | Drawer 内 Skeleton | Drawer |
| drilldown forbidden | platform tenant_id 403 | Drawer 内 Alert | Alert |
| P1 tab/page disabled | storage/kb | 专页路由可进但显示「待 API」Empty | Empty |

**状态优先级（BOSS 页顶 Alert 仅显示一条）：**
`api-not-ready` > `forbidden` > `error` > `dev_profile` warning

### 6.3 后端能力（无 UI，供联调预期）

| 能力 | 终端 UI |
|------|---------|
| metering-service 上报 / 重试 | 无 |
| SRE metrics | Grafana/运维台，非本 UX |

---

## 7. Copy & Feedback

### 7.1 Labels & Buttons

| Element | Copy (zh-CN) | Screen |
|---------|--------------|--------|
| Page title | 租户用量报表 | Console |
| Page title | 租户计费与用量 | BOSS 聚合 |
| Page title | 平台 {GPU-Hours / Input Tokens / …} | BOSS 专页 |
| 时间筛选 label | 统计时间范围 | 共用 |
| 预设视角 | GPU 算力 / CPU 算力 / 内存 / Input Tokens / Output Tokens | Console Tabs |
| group_by | 按资源类型 / 按可用区 / 按天 / 按小时 | Console |
| group_by（平台） | 按租户 / 按天 / 按小时 | BOSS |
| Secondary | 重试 | error 态 |
| Row action | 查看明细 | BOSS 排行 |
| Drawer title | 租户用量明细 · {tenant_id} | BOSS |

### 7.2 Messages

| Scenario | Type | Copy |
|----------|------|------|
| 查询成功 | — | 无 toast（报表页静默刷新） |
| 加载失败 | `Message.error` | 用量数据加载失败，请稍后重试 |
| 403 租户 | Alert | 您没有权限查看用量报表 |
| 403 平台 | Alert | 您没有权限查看平台计量数据 |
| 空数据 | Empty | 当前时间范围内暂无用量数据 |
| 时间非法 | inline | 结束时间必须晚于开始时间 |
| API 未就绪 | Alert | 平台计量接口尚未上线，暂无法展示跨租户排行 |
| Storage/KB disabled Tooltip | Tooltip | 该指标待 API 合入（P1） |
| 边界说明 | 静态 | 本页仅展示用量统计，不含账单金额、发票与结算 |
| 单位提示（可选 Tooltip） | 静态 | 视角名称（如 GPU 算力）为分类别名；数值单位以后端返回为准 |

---

## 8. Boundaries & Non-Goals

### 8.1 In Scope (UX)

- Console 租户用量：时间筛选、5 类 P0 视角 Tab、group_by、图表+表格、dev/empty/error/forbidden、debounce 自动查询
- BOSS 跨租户：聚合页 + **5 P0 专页** + Storage/KB 占位路由、钻取 Drawer（**platform API + tenant_id**）、api-not-ready 四态
- 预设视角与 `resource_type` 映射与 PRD §6.3 一致
- FR-12 dev 横幅；FR-16 钻取；FR-17 token_total（无 Tab）；FR-18 单位原样展示

### 8.2 Explicitly Out of Scope (UI)

- 账单金额、发票、对账、导出 CSV（Phase 2+）
- 租户调用 `POST /metering/token-usage`
- metering-service / inference 上报配置界面
- SRE 队列积压运维大盘（US-006）
- 按 `model_id` 筛选（Phase 3）
- JWT 轮询模拟多租户（BOSS 禁止）
- 钻取调用租户 `GET /metering/usage`（FR-16）
- 伪造全 0 图表或假 tenant 数据
- P0 前端 seconds→hours 等单位换算（FR-18）

### 8.3 Open UX Questions

- （v1.4 已全部收敛，见 §8.4）

### 8.4 Assumptions & Closed Decisions（v1.4）

| 决策 | 结论 |
|------|------|
| Console 筛选触发 | **debounce 300ms 自动查询**，无单独查询按钮 |
| BOSS 钻取 API | **GET /metering/usage/platform?tenant_id=…**；禁止租户 path |
| Drawer 二次请求 | 行数据不足时带 tenant_id 再请求；group_by=tenant_id 且行已含明细时可省略 |
| token_total | Console **无** Tab；表格在未筛 resource_type 时可展示 |
| 单位展示 | **原样**展示 API `unit` + `total_quantity` |
| local 算力 Tab | 空态为 **预期**（local 仅 Token） |
| BOSS P0 专页 | 聚合 + GPU/CPU/Memory/Input/Output **5 页**；Storage/KB 路由占位 api-not-ready |
| BOSS 路由 | 前缀以 `boss-modules` 为准，SPEC 落地时对齐实际 scaffold |
| Console 基线 | 已有 `/_authenticated/usage` + `useMeteringUsageQuery`；增量增强 |
| BOSS 设计 Token | 与 Console 共用 TDesign Token（UI规范 2.0） |
| Platform API 未合入 | BOSS 使用 api-not-ready，不 mock 排行 |
| resource_type 中文 | UI 映射表展示；sort/filter 用 API 枚举值 |

---

## 9. 文档链

| 文档 | 路径 |
|------|------|
| PRD | `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md`（**v1.4**） |
| UX（本文） | `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md` |
| SPEC | （next: `/prd-to-spec`） |
| Console 主维护 | `repo/services/docs/console-modules/tenant/usage-report.md` |
| BOSS 主维护 | `repo/services/docs/boss-modules/tenant/tenant-usage-billing.md` |
