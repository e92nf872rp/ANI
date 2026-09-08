# SPEC: BOSS Metering（平台计量页 + scaffold 初始化）

> Technical specification derived from:
> - PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md` (v1.4)
> - UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
> Generated: 2026-07-09 | Target branch: main | Commit: —
>
> **Product line:** boss
> **Code scope:** `repo/frontends/boss/` **only**
> Source of truth: consume OpenAPI — no backend changes in UI-only batch

---

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 覆盖 BOSS 平台计量的**全部前端建设**：

1. **BOSS 前端 scaffold 初始化**：Vite + TanStack Router + TDesign + React Query + openapi-fetch + ECharts 项目脚手架
2. **聚合页** `/tenant/usage-billing`：跨租户排行 + 趋势图 + KPI + 专页入口
3. **5 个 P0 专页** `/metering/{gpu-hours,cpu-hours,memory-gbhours,input-tokens,output-tokens}`：单指标排行 + 趋势
4. **2 个 P1 占位页** `/metering/{storage-gbdays,kb-queries}`：路由可进 + api-not-ready 态
5. **钻取 Drawer**：单租户明细，调用 `GET /metering/usage/platform?tenant_id=...`（FR-16）
6. **状态机**：api-not-ready、loading、empty、error、forbidden、dev_profile、drilldown loading/forbidden

### 1.2 PRD Reference

- Source: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md` (v1.4)
- UX source: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- User Stories covered: US-004（BOSS 平台跨租户）
- Functional Requirements covered: FR-5、FR-8（消费端）、FR-12、FR-15（消费端）、FR-16、FR-17、FR-18

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| 技术栈 | 与 Console 一致（Vite + TanStack Router + TDesign + React Query + openapi-fetch） | UX §8.4 共用 TDesign Token |
| API 客户端 | openapi-fetch `createClient<paths>()`，baseUrl `/api/v1` | 与 Console coreClient 一致 |
| 平台查询端点 | `GET /metering/usage/platform` | FR-8/FR-5 定稿 |
| 钻取 API | `GET /metering/usage/platform?tenant_id=...` | FR-16 定稿；禁止租户 path |
| api-not-ready 态 | 平台 API 404/501 时全页 Alert + 禁用数据区 | UX §6.2 |
| P0 专页数 | 5 页（GPU/CPU/Memory/Input/Output） | UX §1.1 |
| P1 占位页 | Storage/KB 路由可进 + api-not-ready Empty | UX §1.1 |
| token_total | 聚合页 KPI 可用 `resource_type=token_total` 查询 | FR-17 |
| 单位展示 | 原样展示 API `unit` + `total_quantity` | FR-18 |

---

## 2. Architecture

### 2.1 System Context

```text
BOSS 前端 (repo/frontends/boss/)
  ├── /tenant/usage-billing          聚合页（跨租户排行 + 趋势 + KPI + 专页入口）
  ├── /metering/gpu-hours            专页：GPU-Hours
  ├── /metering/cpu-hours            专页：CPU-Hours
  ├── /metering/memory-gbhours       专页：Memory-GBHours
  ├── /metering/input-tokens         专页：Input Tokens
  ├── /metering/output-tokens        专页：Output Tokens
  ├── /metering/storage-gbdays       占位页（P1 api-not-ready）
  ├── /metering/kb-queries           占位页（P1 api-not-ready）
  └── API: GET /api/v1/metering/usage/platform (scope:metering:platform:read)
```

### 2.2 Component Design

| 组件 | 职责 | 类型 |
|------|------|------|
| `BossApp` | 根布局（Header + Aside + Content + Menu） | [NEW] shell |
| `BossPage` / `BossPageHeader` / `BossContentCard` | 页面 shell 组件 | [NEW] shell |
| `PlatformUsagePage` | 聚合页容器 | [NEW] |
| `PlatformMetricPage` | 专页通用容器（7 页共享模板） | [NEW] |
| `PlatformRankTable` | 租户排行表格 | [NEW] |
| `PlatformTrendChart` | 趋势图（ECharts） | [NEW] |
| `PlatformKPI` | 平台 KPI 汇总卡 | [NEW] |
| `TenantDrilldownDrawer` | 单租户钻取抽屉 | [NEW] |
| `PlatformFilterBar` | 筛选区（DateRange + 指标视角 + 租户 Select + group_by） | [NEW] |
| `ApiNotReadyAlert` | API 未就绪 Alert | [NEW] |
| `DevProfileAlert` | dev 横幅 Alert | [NEW] |
| `usePlatformUsageQuery` | 平台查询 hook | [NEW] |

### 2.3 Module Interactions

**聚合页查询流程：**

```text
1. 进入 /tenant/usage-billing
2. 默认时间范围：近 30 天
3. 筛选 debounce 300ms → usePlatformUsageQuery
4. GET /metering/usage/platform?start_time=...&end_time=...&resource_type=...&group_by=tenant_id
5. 响应 → PlatformRankTable + PlatformTrendChart + PlatformKPI
6. 点击行「查看明细」→ TenantDrilldownDrawer
   → GET /metering/usage/platform?tenant_id={行ID}&start_time=...&end_time=...&group_by=day
```

**专页查询流程：**

```text
1. 进入 /metering/input-tokens（固定 resource_type=token_input）
2. 筛选 debounce 300ms → usePlatformUsageQuery（resource_type 写死）
3. GET /metering/usage/platform?...&resource_type=token_input&group_by=tenant_id
4. 响应 → PlatformRankTable + PlatformTrendChart + PlatformKPI
5. 专页无指标视角切换（resource_type 固定）
```

### 2.4 File Structure

```
repo/frontends/boss/                           [NEW: 整个项目]
├── package.json
├── vite.config.ts
├── tsconfig.json
├── index.html
├── scripts/
│   └── gen-core-schema.mjs                    # 从 v1.yaml 生成类型
├── src/
│   ├── main.tsx                               # 入口: QueryClientProvider + RouterProvider
│   ├── App.tsx                                # 旧版 SPA (若有)
│   ├── routeTree.gen.ts                       # TanStack Router 自动生成
│   ├── styles.css
│   ├── api/
│   │   ├── coreClient.ts                      # Core API 客户端 (/api/v1)
│   │   ├── core-schema.d.ts                   # 从 v1.yaml 生成
│   │   └── auth.ts                            # JWT 中间件
│   ├── components/
│   │   └── shell/
│   │       ├── BossPage.tsx
│   │       ├── BossPageHeader.tsx
│   │       └── BossContentCard.tsx
│   ├── features/
│   │   └── platform-metering/
│   │       ├── PlatformUsagePage.tsx          # 聚合页
│   │       ├── PlatformMetricPage.tsx         # 专页通用模板
│   │       ├── PlatformRankTable.tsx
│   │       ├── PlatformTrendChart.tsx
│   │       ├── PlatformKPI.tsx
│   │       ├── TenantDrilldownDrawer.tsx
│   │       ├── PlatformFilterBar.tsx
│   │       ├── ApiNotReadyAlert.tsx
│   │       ├── DevProfileAlert.tsx
│   │       ├── constants.ts                   # METRIC_PAGES, RESOURCE_TYPE_MAP
│   │       ├── types.ts                       # PlatformUsageFilter, PlatformUsageRow
│   │       ├── usePlatformUsageQuery.ts
│   │       └── useDebouncedFilter.ts
│   └── routes/
│       ├── __root.tsx                         # 根布局 (Header + Aside + Menu + Outlet)
│       ├── index.tsx                          # / (BOSS 仪表盘, 可占位)
│       ├── tenant/
│       │   └── usage-billing.tsx               # /tenant/usage-billing
│       └── metering/
│           ├── gpu-hours.tsx                   # /metering/gpu-hours
│           ├── cpu-hours.tsx                   # /metering/cpu-hours
│           ├── memory-gbhours.tsx              # /metering/memory-gbhours
│           ├── input-tokens.tsx                # /metering/input-tokens
│           ├── output-tokens.tsx               # /metering/output-tokens
│           ├── storage-gbdays.tsx              # /metering/storage-gbdays (P1 占位)
│           └── kb-queries.tsx                  # /metering/kb-queries (P1 占位)
├── .env.development
└── Dockerfile
```

---

## 3. Data Model

### 3.1 Schema Changes

无数据库变更（UI-only batch）。

### 3.2 Entity Definitions

**PlatformUsageFilter：**

```typescript
// features/platform-metering/types.ts
interface PlatformUsageFilter {
  start_time: string;
  end_time: string;
  resource_type?: string;      // 聚合页可选；专页固定
  group_by?: 'tenant_id' | 'day' | 'hour';
  tenant_id?: string;          // 钻取时指定
}
```

**PlatformUsageRow（表格行，来自 OpenAPI）：**

```typescript
interface PlatformUsageRow {
  tenant_id: string;           // 平台视角下必填
  resource_type: string;
  total_quantity: number;
  unit: string;
  period?: string | null;
}
```

**METRIC_PAGES 配置：**

```typescript
// features/platform-metering/constants.ts
interface MetricPageConfig {
  route: string;
  title: string;           // 页面标题
  resource_type: string;   // 固定值
  p0_enabled: boolean;     // false = P1 占位
}

const METRIC_PAGES: MetricPageConfig[] = [
  { route: '/metering/gpu-hours',      title: '平台 GPU-Hours',      resource_type: 'instance_gpu_seconds',       p0_enabled: true },
  { route: '/metering/cpu-hours',      title: '平台 CPU-Hours',      resource_type: 'instance_cpu_seconds',       p0_enabled: true },
  { route: '/metering/memory-gbhours', title: '平台 Memory-GBHours', resource_type: 'instance_memory_gib_seconds', p0_enabled: true },
  { route: '/metering/input-tokens',   title: '平台 Input Tokens',   resource_type: 'token_input',                p0_enabled: true },
  { route: '/metering/output-tokens',  title: '平台 Output Tokens',  resource_type: 'token_output',               p0_enabled: true },
  { route: '/metering/storage-gbdays', title: '平台 Storage-GBDays', resource_type: 'storage_gb_days',           p0_enabled: false },
  { route: '/metering/kb-queries',     title: '平台 KB Queries',     resource_type: 'kb_query_count',             p0_enabled: false },
];
```

### 3.3 Relationships

无。平台视角下 `tenant_id` 为必填字段。

### 3.4 Migration Plan

无 DB 迁移（UI-only batch）。

---

## 4. API Design

### 4.1 Endpoints

| Method | Path | Description | Auth | Request | Response |
|--------|------|-------------|------|---------|----------|
| GET | `/metering/usage/platform` | 平台跨租户用量查询 | `scope:metering:platform:read` | `start_time*`, `end_time*`, `resource_type?`, `group_by?`, `tenant_id?` | `MeteringUsageResponse`（`tenant_id` 必填） |

### 4.2 Request/Response Schemas

**聚合页查询：**

```typescript
coreApi.GET('/metering/usage/platform', {
  params: {
    query: {
      start_time: filter.start_time,
      end_time: filter.end_time,
      resource_type: filter.resource_type,
      group_by: 'tenant_id',     // 排行模式
    }
  }
})
```

**钻取查询：**

```typescript
coreApi.GET('/metering/usage/platform', {
  params: {
    query: {
      start_time: filter.start_time,
      end_time: filter.end_time,
      resource_type: filter.resource_type,  // 继承主查询
      tenant_id: rowTenantId,                 // 钻取指定租户
      group_by: 'day',                       // Drawer 内默认按天
    }
  }
})
```

### 4.3 Error Responses

| HTTP Status | 前端处理 |
|------------|---------|
| 404 / 501 | api-not-ready 态：全页 Alert + 禁用数据区 |
| 403 | forbidden 态：Alert「您没有权限查看平台计量数据」 |
| 400 | Alert + 时间范围无效 |
| 5xx / network | error 态：Alert + 重试 |

### 4.4 Breaking Changes

无。消费新端点，无 API 变更。

---

## 5. Business Logic

### 5.1 Core Algorithms

**聚合页查询流程：**

```text
1. 进入 /tenant/usage-billing
2. 默认时间范围：近 30 天
3. 检测平台 API 可用性（首次请求 404/501 → api-not-ready 态）
4. 筛选 debounce 300ms → usePlatformUsageQuery
5. GET /metering/usage/platform?...&group_by=tenant_id
6. 响应 → PlatformRankTable (排序 by total_quantity desc) + PlatformTrendChart + PlatformKPI
```

**专页查询流程：**

```text
1. 进入 /metering/{metric} → 从 METRIC_PAGES 查找 config
2. 若 config.p0_enabled = false → 渲染 api-not-ready Empty「待 API」
3. 固定 resource_type = config.resource_type
4. 筛选 debounce 300ms → usePlatformUsageQuery（resource_type 写死）
5. GET /metering/usage/platform?...&resource_type={fixed}&group_by=tenant_id
6. 响应 → PlatformRankTable + PlatformTrendChart + PlatformKPI
```

**钻取流程（FR-16）：**

```text
1. 用户点击排行表行「查看明细」
2. TenantDrilldownDrawer 打开（size="large"）
3. GET /metering/usage/platform?tenant_id={行ID}&start_time=...&end_time=...&resource_type=...&group_by=day
4. Drawer 内 Skeleton → 数据到达后渲染 Table + Chart
5. 若主查询 group_by=tenant_id 且行数据已含明细 → 可省略二次请求
6. 禁止: GET /metering/usage（租户 path）、JWT 轮询、impersonate
```

### 5.2 Validation Rules

| 规则 | 条件 | UI 反馈 |
|------|------|---------|
| 时间必填 | start_time / end_time 为空 | DateRangePicker inline 错误 |
| start < end | start_time ≥ end_time | inline 错误 |
| resource_type | 聚合页可选；专页固定 | 专页不提供切换 |
| group_by | 平台枚举 `[tenant_id, day, hour]` | Select 限定 |
| tenant_id query | 钻取时指定 | 后端 RBAC 二次校验 |

### 5.3 State Machine

| State | Trigger | UI behavior | Components |
|-------|---------|------------|------------|
| api-not-ready | 平台 API 404/501 | 全页 Alert；排行/图表区 disabled 或隐藏 | ApiNotReadyAlert |
| loading | fetching | Table `loading`; Chart Skeleton | Table, Skeleton |
| empty | 200 + items=[] | Empty「当前条件下暂无租户用量」 | Empty |
| error | 请求失败 | Alert + 重试 | Alert, Button |
| forbidden | 403 | Alert「您没有权限查看平台计量数据」 | Alert |
| dev_profile | 200 + dev_profile.real_provider=false | Warning 横幅 | DevProfileAlert |
| drilldown loading | Drawer 内 platform 二次请求 | Drawer 内 Skeleton | Drawer, Skeleton |
| drilldown forbidden | platform tenant_id 403 | Drawer 内 Alert | Alert |
| P1 page disabled | storage/kb | 专页路由可进但显示「待 API」Empty | Empty |

**状态优先级（页顶 Alert 仅显示一条）：**

```text
api-not-ready > forbidden > error > dev_profile
```

### 5.4 Edge Cases

| 场景 | 处理 |
|------|------|
| 平台 API 未合入（P0 初期） | api-not-ready 全页 Alert；不 mock 排行数据 |
| 平台 API 返回空 | Empty「当前条件下暂无租户用量」 |
| 钻取时租户无数据 | Drawer 内 Empty |
| 钻取 API 403 | Drawer 内 Alert「无权限查看该租户用量」 |
| token_total 聚合 KPI | 聚合页 KPI 可用 `resource_type=token_total` 查询（FR-17） |
| 专页固定 resource_type | UI 不提供指标视角切换（与聚合页区分） |
| 单位非预期 | 原样展示 API `unit` + `total_quantity`（FR-18） |

---

## 6. Error Handling

### 6.1 Error Taxonomy

| Error Code | UI 表现 | 用户消息 |
|------------|---------|---------|
| 404 / 501 | api-not-ready Alert | 平台计量接口尚未上线，暂无法展示跨租户排行 |
| 403 | Alert | 您没有权限查看平台计量数据 |
| 400 | Alert | 时间范围无效 |
| 5xx / network | Alert + 重试 | 用量数据加载失败，请稍后重试 |
| items=[] | Empty | 当前条件下暂无租户用量数据 |
| Storage/KB P1 | Empty | 该指标待 API 合入（P1） |
| 钻取 403 | Drawer 内 Alert | 无权限查看该租户用量 |

### 6.2 Retry Strategy

- React Query 默认重试 3 次（5xx / network）
- 403 / 404 / 501 不重试
- 用户点击「重试」按钮 → `refetch()`

### 6.3 Failure Modes

| 依赖失败 | 行为 |
|---------|------|
| 平台 API 未实现 | api-not-ready 态；不伪造数据 |
| Core API 不可用 | error 态 + 重试 |
| 钻取 API 失败 | Drawer 内 Skeleton → error Alert |
| 网络超时 | React Query 超时 → error 态 |

---

## 7. Security

### 7.1 Authentication & Authorization

- JWT 由 `auth.ts` 中间件自动注入 Bearer header
- `scope:metering:platform:read` 由后端 Gateway 校验
- 前端不做权限判断；403 由后端返回后展示 Alert
- 钻取 `tenant_id` query 由后端二次 RBAC 校验（FR-16）

### 7.2 Input Validation

- 时间范围前端校验（start < end）
- resource_type 在专页固定（不提供 UI 切换）
- group_by 通过 Select 限定平台枚举
- tenant_id 筛选（Select，clearable）

### 7.3 Data Protection

无敏感字段。计量数据非敏感。

---

## 8. Performance

### 8.1 Expected Load

| 指标 | 估计 |
|------|------|
| 平台查询 QPS | 低（BOSS 运营手动查看） |
| BOSS Top N 排行 | N ≥ 20 租户（PRD §8） |
| 图表渲染 | < 500ms（ECharts 客户端） |
| 钻取 Drawer 查询 | < 2s |

### 8.2 Optimization Strategy

- React Query 缓存：相同 queryKey 不重复请求
- debounce 300ms 避免频繁请求
- 排行在后端 `group_by=tenant_id` 聚合，避免前端逐租户轮询
- 钻取数据复用：若主查询行已含明细可省略二次请求

### 8.3 Database Considerations

无。前端不直接访问数据库。

---

## 9. Testing Strategy

### 9.1 Unit Tests

| 测试目标 | 范围 |
|---------|------|
| useDebouncedFilter | 300ms 延迟、取消旧值 |
| usePlatformUsageQuery | queryKey 构建、resource_type 固定/可选 |
| METRIC_PAGES | 5 P0 + 2 P1 配置 |
| 状态优先级 | api-not-ready > forbidden > error > dev_profile |
| 钻取 query 构建 | tenant_id + 继承 resource_type + group_by=day |

### 9.2 Integration Tests

| 测试 | 描述 |
|------|------|
| 聚合页查询 → 渲染 | 筛选 → debounce → query → RankTable + Chart + KPI |
| 专页查询（固定 resource_type） | 路由进入 → resource_type 写死 → query |
| 钻取 Drawer | 点击行 → Drawer 打开 → 二次 query → 渲染 |
| api-not-ready 态 | API 404/501 → 全页 Alert |
| empty 态 | items=[] → Empty |
| error 态 | API 500 → Alert + 重试 |
| forbidden 态 | API 403 → Alert |
| dev 横幅 | dev_profile.real_provider=false → Warning Alert |
| P1 占位页 | Storage/KB 路由进入 → api-not-ready Empty |

### 9.3 Edge Case Tests

| 场景 | 期望 |
|------|------|
| 钻取禁止租户 path | 使用 /metering/usage/platform?tenant_id=...（非 /metering/usage） |
| 钻取行数据已含明细 | 可省略二次请求 |
| token_total KPI | 聚合页 KPI 可用 token_total 查询 |
| 专页不提供指标切换 | resource_type 固定，无 Select |
| 排行 Top N ≥ 20 | 后端聚合返回 ≥ 20 租户 |

### 9.4 Acceptance Criteria Mapping

| US/FR | Test | Type | Description |
|-------|------|------|-------------|
| US-004 | 聚合页跨租户排行 | integration | GET /metering/usage/platform + group_by=tenant_id |
| US-004 | 5 P0 专页 | integration | 7 路由进入，5 页正常 |
| US-004 | 不轮询 JWT | integration | 单次平台 API 调用，无逐租户轮询 |
| US-004 | 钻取 FR-16 | integration | 钻取使用 platform path + tenant_id |
| US-004 | 四态 | integration | loading / empty / api-not-ready / error |
| FR-5 | 使用 platform path | integration | 不使用租户 GET /metering/usage |
| FR-12 | dev 横幅 | integration | dev_profile 时显示 |
| FR-16 | 钻取 API | unit | GET /metering/usage/platform?tenant_id=... |
| FR-17 | token_total KPI | integration | 聚合页 KPI 用 token_total |
| FR-18 | 单位原样展示 | unit | 不做换算 |

---

## 10. Implementation Plan

### 10.1 Phases

| Phase | 范围 | 依赖 |
|-------|------|------|
| P0-C-1 | BOSS 项目 scaffold（Vite + Router + TDesign + Query + openapi-fetch + ECharts） | — |
| P0-C-2 | API 客户端 + 类型生成（coreClient.ts + core-schema.d.ts） | P0-C-1 |
| P0-C-3 | shell 组件（BossPage / Header / ContentCard） + 根布局 | P0-C-1 |
| P0-C-4 | feature/platform-metering 基础模块（constants, types, hooks） | P0-C-2 |
| P0-C-5 | PlatformFilterBar + ApiNotReadyAlert + DevProfileAlert | P0-C-3, P0-C-4 |
| P0-C-6 | PlatformRankTable + PlatformTrendChart + PlatformKPI | P0-C-4 |
| P0-C-7 | TenantDrilldownDrawer | P0-C-6 |
| P0-C-8 | PlatformUsagePage（聚合页）组合 | P0-C-5, P0-C-6, P0-C-7 |
| P0-C-9 | PlatformMetricPage（专页模板）+ 5 P0 路由 | P0-C-5, P0-C-6 |
| P0-C-10 | 2 P1 占位路由 | P0-C-9 |
| P0-C-11 | 侧栏菜单 + 面包屑 | P0-C-3 |

### 10.2 Issue Mapping

| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| #1 BOSS scaffold | 2.4 | high | — |
| #2 API 客户端 + 类型 | 2.4, 4.1, 4.2 | high | #1 |
| #3 shell + 根布局 | 2.2, 2.4 | high | #1 |
| #4 feature 基础模块 | 3.2, 5.1, 5.2 | high | #2 |
| #5 筛选区 + Alert 组件 | 5.3, 6.1 | high | #3, #4 |
| #6 排行表 + 趋势图 + KPI | 5.1, 5.4 | high | #4 |
| #7 钻取 Drawer | 5.1, 5.3, 5.4 | high | #6 |
| #8 聚合页组合 | 2.3, 5.1 | high | #5, #6, #7 |
| #9 专页模板 + 5 路由 | 5.1, 5.3 | high | #5, #6 |
| #10 P1 占位路由 | 5.3, 6.1 | medium | #9 |

### 10.3 Incremental Delivery

- **scaffold 优先：** P0-C-1～C-3 先建立可运行的项目骨架
- **聚合页后于 scaffold：** P0-C-8 依赖全部组件就绪
- **api-not-ready 兼容：** P0 初期平台 API 未合入时，聚合页和专页均显示 api-not-ready 态，不阻塞前端开发
- **P1 占位：** Storage/KB 路由 P0 阶段即可创建，内容为 Empty「待 API」

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

- BOSS 前端的认证方式是否与 Console 完全一致（JWT Bearer token）？
- BOSS 路由前缀是否需要 `/_authenticated`（与 Console 一致）还是直接根路由？
- 平台 API 404 与 501 如何区分？前端是否需要统一处理为 api-not-ready？
- 面包屑组件是否需要新建，还是 TDesign Breadcrumb 即可？

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|-----------|
| 平台 API 未合入时前端无法联调 | P0-C 验收受阻 | api-not-ready 态先行；local profile 平台查询实现后可联调 |
| BOSS scaffold 从零开始 | 初始化工作量大 | 参照 Console scaffold 结构复制 |
| ECharts 首次引入无现有模式 | 图表渲染性能未知 | 封装 PlatformTrendChart 统一接口 |
| 钻取 API tenant_id 二次鉴权 | 越权风险 | 依赖后端 RBAC 校验；前端不信任 tenant_id 参数 |

### 11.3 Assumptions

- 平台 API `GET /metering/usage/platform` 在 Core FR-8 批次合入后可用
- local profile 将新增平台查询实现（内存全租户聚合），用于前端联调
- BOSS 前端与 Console 共用 TDesign Token（UI 规范 2.0）
- openapi-fetch + openapi-typescript 可从 v1.yaml 生成类型安全客户端
- TDesign 组件库提供 Drawer / Breadcrumb / Select / DateRangePicker 等所需组件
