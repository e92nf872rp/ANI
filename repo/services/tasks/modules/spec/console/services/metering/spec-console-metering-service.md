# SPEC: Console Metering（租户用量报表增强）

> Technical specification derived from:
> - PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md` (v1.4)
> - UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
> Generated: 2026-07-09 | Target branch: main | Commit: —
>
> **Product line:** console
> **Code scope:** `repo/frontends/console/` **only**
> Source of truth: consume OpenAPI — no backend changes in UI-only batch

---

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 覆盖 Console 租户用量报表页 `/usage` 的 P0 增强：

1. **筛选区**：DateRangePicker（必填）、预设视角 Tabs（5 类 P0 resource_type + 2 类 P1 disabled）、group_by Segmented
2. **debounce 自动查询**：筛选变更 300ms debounce 后自动触发 `GET /metering/usage`，无查询按钮
3. **ECharts 趋势图**：全新引入 echarts-for-react，按 group_by 时间桶渲染折线/柱图
4. **明细表格增强**：原样展示 `total_quantity` + `unit`（FR-18），支持 `token_total` 行展示（FR-17）
5. **dev 横幅**：`dev_profile` 存在时显示 Warning Alert（FR-12）
6. **状态机**：idle/success、loading、empty、error、forbidden、dev_profile、invalid range、tab disabled

### 1.2 PRD Reference

- Source: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md` (v1.4)
- UX source: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- User Stories covered: US-002（算力可读）、US-003（Console 租户报表）
- Functional Requirements covered: FR-4、FR-7、FR-12、FR-17、FR-18

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| 查询触发方式 | debounce 300ms 自动查询（无按钮） | UX §8.4 定稿 |
| API 客户端 | 复用 `coreApi` (openapi-fetch) | 现有模式；`coreApi.GET('/metering/usage')` |
| 图表库 | echarts-for-react（已安装） | package.json 已有依赖 |
| 预设视角 Tab | TDesign Tabs `theme="card"` | UX §5.1 |
| token_total 无 Tab | 表格在未筛 resource_type 时可展示 | FR-17 定稿 |
| 单位展示 | 原样展示 API `unit` + `total_quantity` | FR-18 定稿 |
| 查询 Hook | 内联 useQuery（现有模式） | 代码库无 queries/ 目录抽象 |
| 路由 | `/_authenticated/usage` | UX §1.1 路由 |

---

## 2. Architecture

### 2.1 System Context

```text
Console 前端 (/usage)
  ├── coreApi.GET('/metering/usage', { query })  -- openapi-fetch, 类型安全
  ├── React Query (useQuery)                     -- 缓存 + 重试 + 状态管理
  ├── ECharts (echarts-for-react)                -- 趋势图
  └── TDesign (Table, Tabs, Alert, Empty)        -- UI 组件
```

### 2.2 Component Design

| 组件 | 职责 | 状态 |
|------|------|------|
| `UsagePage` | 页面容器，组合筛选区 + 图表 + 表格 | **重写** `routes/usage.tsx` |
| `UsageFilterBar` | 筛选区（DateRangePicker + Tabs + Segmented） | [NEW] 子组件 |
| `UsageChart` | ECharts 趋势图 | [NEW] 子组件 |
| `UsageTable` | 明细表格 | [NEW] 子组件（从现有 Table 提取） |
| `useDebouncedFilter` | debounce 300ms filter hook | [NEW] |
| `RESOURCE_TYPE_TABS` | 预设视角配置常量 | [NEW] |

### 2.3 Module Interactions

```text
1. 用户进入 /usage
2. 默认时间范围：近 30 天
3. 用户改变筛选 → useDebouncedFilter (300ms)
4. debounce 触发 → useQuery({ queryKey, queryFn: coreApi.GET })
5. data 更新 → UsageChart + UsageTable 同步渲染
6. dev_profile 存在 → 页顶 Alert
7. items=[] → Empty
```

### 2.4 File Structure

```
repo/frontends/console/src/
├── routes/
│   └── _authenticated/
│       └── usage.tsx                         [REWRITE: 增强版本]
├── components/
│   └── shell/                                [NEW: 若不存在则创建]
│       ├── ConsolePage.tsx
│       ├── ConsolePageHeader.tsx
│       └── ConsoleContentCard.tsx
├── features/
│   └── usage/                                [NEW]
│       ├── UsageFilterBar.tsx
│       ├── UsageChart.tsx
│       ├── UsageTable.tsx
│       ├── constants.ts                      # RESOURCE_TYPE_TABS, GROUP_BY_OPTIONS
│       ├── types.ts                          # UsageRow, UsageFilter
│       └── useDebouncedFilter.ts
└── api/
    └── coreClient.ts                         [现有: 无需修改]
```

> **注意：** `_authenticated/usage.tsx` 当前引用了不存在的 `@/queries/coreResources` 和 `@/components/shell`。本 SPEC 选择**重写此文件**使其生效，同时创建所需的 shell 组件和 feature 模块。旧版 `routes/usage.tsx` 在路由切换后可移除。

---

## 3. Data Model

### 3.1 Schema Changes

无数据库变更。数据模型来自 OpenAPI 生成的类型（`core-schema.d.ts`）。

### 3.2 Entity Definitions

**UsageFilter（前端状态）：**

```typescript
// features/usage/types.ts
interface UsageFilter {
  start_time: string;      // ISO 8601 date-time
  end_time: string;        // ISO 8601 date-time
  resource_type?: string;  // 从 Tab 选择；undefined = 全部
  group_by?: 'resource_type' | 'az' | 'day' | 'hour';
}
```

**UsageRow（表格行类型，来自 OpenAPI）：**

```typescript
// 来自 core-schema.d.ts 的 MeteringUsageRecord
interface UsageRow {
  resource_type: string;
  total_quantity: number;
  unit: string;
  period?: string | null;
  tenant_id?: string | null;
}
```

### 3.3 Relationships

无。单租户视角，`tenant_id` 字段可忽略。

### 3.4 Migration Plan

无 DB 迁移（UI-only batch）。

---

## 4. API Design

### 4.1 Endpoints

| Method | Path | Description | Auth | Request | Response |
|--------|------|-------------|------|---------|----------|
| GET | `/metering/usage` | 租户用量查询 | JWT + `scope:metering:read` | `start_time*`, `end_time*`, `resource_type?`, `group_by?` | `MeteringUsageResponse` |

### 4.2 Request/Response Schemas

**请求参数：**

```typescript
// openapi-fetch 调用
coreApi.GET('/metering/usage', {
  params: {
    query: {
      start_time: filter.start_time,
      end_time: filter.end_time,
      resource_type: filter.resource_type,     // optional
      group_by: filter.group_by,               // optional
    }
  }
})
```

**响应类型（来自 `MeteringUsageResponse`）：**

```typescript
interface MeteringUsageResponse {
  items: MeteringUsageRecord[];
  total: number;
  dev_profile: CoreDevProfileInfo;
}

interface MeteringUsageRecord {
  resource_type: string;
  total_quantity: number;
  unit: string;
  period?: string | null;
  tenant_id?: string | null;
}

interface CoreDevProfileInfo {
  mode: string;
  provider: string;
  real_provider: boolean;
  reason: string;
}
```

### 4.3 Error Responses

| HTTP Status | 前端处理 |
|------------|---------|
| 400 | Alert + 「时间范围无效」 |
| 403 | Alert + 「您没有权限查看用量报表」 + 隐藏数据区 |
| 5xx / network | Alert + 「用量数据加载失败，请稍后重试」+ 重试按钮 |

### 4.4 Breaking Changes

无。前端消费现有端点，无 API 变更。

---

## 5. Business Logic

### 5.1 Core Algorithms

**debounce 自动查询流程：**

```text
1. 用户改变筛选条件 (DateRangePicker / Tabs / Segmented)
2. 更新 filter state
3. useDebouncedFilter(filter, 300ms) → debounce 后返回 debouncedFilter
4. useQuery({
     queryKey: ['metering', 'usage', debouncedFilter],
     queryFn: () => coreApi.GET('/metering/usage', { params: { query: debouncedFilter } })
              .then(({ data }) => data),
     enabled: isValidRange(debouncedFilter)
   })
5. data 变更 → UsageChart + UsageTable 重新渲染
```

**默认时间范围：**

```typescript
function defaultTimeRange(): { start_time: string; end_time: string } {
  const end = new Date();
  const start = new Date(end.getTime() - 30 * 24 * 60 * 60 * 1000); // 30 天
  return {
    start_time: start.toISOString(),
    end_time: end.toISOString(),
  };
}
```

**图表数据映射：**

```text
items[] → ECharts dataset
  - x 轴: period (group_by=day/hour 时) 或 resource_type (group_by=resource_type 时)
  - y 轴: total_quantity
  - 系列: resource_type (按维度拆分)
```

### 5.2 Validation Rules

| 规则 | 条件 | UI 反馈 |
|------|------|---------|
| 时间必填 | start_time / end_time 为空 | DateRangePicker inline 错误 |
| start < end | start_time ≥ end_time | inline 错误「结束时间必须晚于开始时间」 |
| resource_type 枚举 | Tab 限定 5 个 P0 值 | Tab disabled 阻止非法值 |

### 5.3 State Machine

| State | Trigger | UI behavior |
|-------|---------|------------|
| idle / success | 200 + items > 0 | Chart + Table 正常 |
| loading | query fetching | Table `loading`; Chart Skeleton |
| empty | 200 + items = [] | Empty「当前时间范围内暂无用量数据」; **不**渲染假折线 |
| error | 4xx / 5xx / network | Alert + 重试按钮; 保留筛选 |
| forbidden | 403 | Alert「您没有权限查看用量报表」; 隐藏数据区 |
| dev_profile | 200 + dev_profile.real_provider = false | 页顶 Warning Alert（固定文案 FR-12） |
| invalid range | start ≥ end | DateRange inline 错误; 禁用查询 |
| tab disabled | Storage / KB P0 | Tab disabled + Tooltip「待 API 合入（P1）」 |

**dev 横幅文案（固定，FR-12）：**
「当前为联调/开发环境数据，非生产真实计量；生产可用性待 live 验证。」

### 5.4 Edge Cases

| 场景 | 处理 |
|------|------|
| local profile 无算力数据 | items=[] → Empty；算力 Tab 空态为**预期**（PRD §7.3） |
| token_total 行（未筛 resource_type） | 表格可展示 `token_total` 行（FR-17）；**无**独立 Tab |
| 单位非预期（如 seconds 而非 hours） | 原样展示（FR-18）；P0 不做换算 |
| 筛选变更后查询失败 | 保留用户已选时间/视角；Alert + 重试 |

---

## 6. Error Handling

### 6.1 Error Taxonomy

| Error Code | UI 表现 | 用户消息 |
|------------|---------|---------|
| 400 INVALID_TIME_RANGE | DateRange inline | 结束时间必须晚于开始时间 |
| 403 FORBIDDEN | Alert + 隐藏数据区 | 您没有权限查看用量报表 |
| 5xx / network | Alert + 重试 | 用量数据加载失败，请稍后重试 |
| items = [] | Empty | 当前时间范围内暂无用量数据 |

### 6.2 Retry Strategy

- React Query 默认重试 3 次（5xx / network）
- 403 不重试
- 用户点击「重试」按钮 → `refetch()`

### 6.3 Failure Modes

| 依赖失败 | 行为 |
|---------|------|
| Core API 不可用 | Alert + 重试 |
| 网络超时 | React Query 超时 → error 态 |
| 响应格式异常 | openapi-fetch 类型校验 → 返回 undefined → error 态 |

---

## 7. Security

### 7.1 Authentication & Authorization

- JWT 由 `auth.ts` 中间件自动注入 Bearer header
- `scope:metering:read` 由后端 Gateway 校验
- 前端不做权限判断；403 由后端返回后展示 Alert

### 7.2 Input Validation

- 时间范围前端校验（start < end）
- resource_type 通过 Tab 限定可选值
- group_by 通过 Segmented 限定可选值

### 7.3 Data Protection

无敏感字段。计量数据非敏感。

---

## 8. Performance

### 8.1 Expected Load

| 指标 | 估计 |
|------|------|
| 单用户查询 | 低频（报表页，手动浏览） |
| 响应时间 | P95 < 3s（PRD §8） |
| 图表渲染 | < 500ms（ECharts 客户端） |

### 8.2 Optimization Strategy

- React Query 缓存：相同 queryKey 不重复请求
- debounce 300ms 避免频繁请求
- ECharts 按需渲染（items 为空时不渲染）

### 8.3 Database Considerations

无。前端不直接访问数据库。

---

## 9. Testing Strategy

### 9.1 Unit Tests

| 测试目标 | 范围 |
|---------|------|
| useDebouncedFilter | 300ms 延迟、取消旧值 |
| defaultTimeRange | 近 30 天计算 |
| isValidRange | start < end 校验 |
| RESOURCE_TYPE_TABS | 5 启用 + 2 disabled |
| token_total 无 Tab | 配置中不含 token_total Tab |

### 9.2 Integration Tests

| 测试 | 描述 |
|------|------|
| 筛选 → 查询 → 渲染 | 改变 DateRange → debounce → queryKey 变更 → refetch |
| empty 态 | items=[] → Empty 组件渲染 |
| error 态 | API 500 → Alert + 重试 |
| forbidden 态 | API 403 → Alert + 隐藏数据 |
| dev 横幅 | dev_profile.real_provider=false → Warning Alert |

### 9.3 Edge Case Tests

| 场景 | 期望 |
|------|------|
| local 算力 Tab | items=[] → Empty（预期，非缺陷） |
| 未筛 resource_type 返回 token_total | 表格展示 token_total 行 |
| 筛选变更后查询失败 | 保留旧筛选 + Alert |
| 单位为 seconds | 原样展示（不换算为 hours） |

### 9.4 Acceptance Criteria Mapping

| US/FR | Test | Type | Description |
|-------|------|------|-------------|
| US-002 | 算力 Tab 切换 | integration | 5 类 resource_type Tab 可切换 |
| US-002 | local 算力空态 | integration | 算力 Tab 在 local 下 Empty 为预期 |
| US-003 | 时间范围筛选 | integration | DateRangePicker 变更触发查询 |
| US-003 | group_by 切换 | integration | 4 种 group_by 可选 |
| US-003 | 三态区分 | integration | loading / empty / error 可区分 |
| FR-4 | 使用 GET /metering/usage | integration | 不绕路到 Services |
| FR-12 | dev 横幅 | integration | dev_profile 时显示固定文案 |
| FR-17 | token_total 无 Tab | unit | 配置不含 token_total Tab |
| FR-17 | token_total 表格行 | integration | 未筛时表格可展示 |
| FR-18 | 单位原样展示 | unit | 不做 seconds→hours 换算 |

---

## 10. Implementation Plan

### 10.1 Phases

| Phase | 范围 | 依赖 |
|-------|------|------|
| P0-B-1 | 创建 shell 组件（ConsolePage 等） | — |
| P0-B-2 | 创建 feature/usage 模块（constants, types, useDebouncedFilter） | — |
| P0-B-3 | UsageFilterBar（DateRange + Tabs + Segmented） | P0-B-1, P0-B-2 |
| P0-B-4 | UsageChart（ECharts 集成） | P0-B-2 |
| P0-B-5 | UsageTable（增强 + token_total） | P0-B-2 |
| P0-B-6 | 重写 _authenticated/usage.tsx 组合所有子组件 | P0-B-3～B-5 |
| P0-B-7 | 状态机实现（dev 横幅、empty、error、forbidden） | P0-B-6 |
| P0-B-8 | 移除旧版 routes/usage.tsx（路由切换后） | P0-B-6 |

### 10.2 Issue Mapping

| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| #1 shell 组件 | 2.4 | high | — |
| #2 feature/usage 基础模块 | 2.4, 3.2, 5.2 | high | — |
| #3 UsageFilterBar | 5.1, 5.2, 5.3 | high | #1, #2 |
| #4 UsageChart | 5.1, 5.4 | high | #2 |
| #5 UsageTable | 5.4, 5.3 | high | #2 |
| #6 页面组合 + 状态机 | 5.3, 6.1 | high | #3, #4, #5 |

### 10.3 Incremental Delivery

- **路由切换：** `_authenticated/usage.tsx` 重写完成后，更新 `routeTree.gen.ts`（Vite 插件自动生成）
- **旧版清理：** 确认新版生效后移除 `routes/usage.tsx` 和侧栏菜单项的旧链接

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

- shell 组件（ConsolePage / ConsolePageHeader / ConsoleContentCard）是否应提取为通用组件库，还是在 features 目录内创建？
- 旧版 `routes/usage.tsx` 移除后，侧栏菜单的路由配置是否需要同步更新？

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|-----------|
| ECharts 首次引入无现有模式 | 图表渲染性能未知 | 先在 UsageChart 中封装统一接口 |
| `_authenticated` 路由未注册 | 新版页面不生效 | 确认 Vite 插件重新生成 routeTree |
| debounce 与 React Query 缓存交互 | 重复请求 | queryKey 包含完整 filter，React Query 自动去重 |

### 11.3 Assumptions

- `coreApi` (openapi-fetch) 类型定义已包含 `/metering/usage` 路径
- `core-schema.d.ts` 已包含 `MeteringUsageResponse` / `MeteringUsageRecord` 类型
- ECharts 响应式布局在 Console 容器内正常工作
- TDesign Tabs / DateRangePicker / Segmented / Alert / Empty 组件可用
