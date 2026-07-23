# SPEC: 统一实例可观测性（Core + Console）

> Technical specification derived from:
> - PRD: `repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md`
> - UX: `repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability.md`
> - 模块主文档: `repo/services/docs/console-modules/compute/container-observability.md`（全 kind 扩展版，待覆盖）
> Generated: 2026-07-03 | Product line: **console + core** | Code scope: `repo/frontends/console/` + Core handler/adapter 补全

> Scope: `repo/frontends/console/`（Console UI）+ Core Gateway handler/adapter（console session 补全 + adapter 扩展说明）
> Source of truth: consume `repo/api/openapi/v1.yaml` — UI-only 批次不修改后端 API；Core 批次仅补全已声明 handler，不新增 OpenAPI 路径

---

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 规定「统一实例可观测性」的完整技术实现，覆盖两段交付：

1. **Core 端**：7 个已声明 endpoint 的 handler 契约确认与补全。Sprint 12 已完成 logs/events/metrics/security-events/exec 的 local adapter + handler（`CORE-SVC-SUPPORT-OBSERVABILITY-A`），Sprint 13 已完成 Prometheus real adapter（`S07 instance observability`）。本 SPEC 需补全 `createInstanceConsoleSession`（VM console）handler，并定义 adapter 多 exporter 聚合方案、kind→metrics capability 映射、PromQL 模板注入方案与 exec WebSocket 客户端协议。
2. **Console 端**：为全部 9 种计算实例 kind 提供统一观测 Tab 框架（日志/事件/指标/终端/控制台/安全事件），按 kind 显示或隐藏，指标 Tab 采用双通道（快照 + PromQL 时序），终端 Tab 走 exec WebSocket，控制台 Tab 走 VM console session。

### 1.2 PRD Reference

- Source: `repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md`
- UX source: `repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability.md`
- User Stories covered: US-001 ～ US-015（全 15 个）
- Functional Requirements covered: FR-1 ～ FR-13（全 13 个）
- 产品决策: OQ-1 ～ OQ-5（已关闭）

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| 指标双通道 | 快照走 `getInstanceMetrics`，时序走 `queryObservability` PromQL 代理 | OQ-1 决策；不向 Console 暴露 Prometheus 地址 |
| PromQL 模板注入 | Console 引用 SPEC 冻结模板 ID，运行时注入 `instance_id`；不硬编码 label | PRD FR-7、UX §8.4 假设 |
| exec 细节 | PRD 只要求「能连上」；xterm/鉴权头/TTY resize 在本 SPEC 定义客户端协议契约 | OQ-2 决策 |
| batch_job/notebook exec | 不展示 exec Tab | OQ-3 决策、FR-9 |
| metrics unsupported kind | 隐藏指标 Tab（不渲染空态 Tab） | OQ-4 决策、FR-12 |
| GPU 字段缺失 | adapter 扩展从其它 exporter 补全；UI 用「暂不可用」代替 0 | OQ-5 决策、FR-13 |
| Console 路由 | 实例详情 Tab 嵌入 `__root.tsx` Layout，新建 `routes/compute/instances/` | UX §1.1、§2.2 |
| API 客户端 | 实例观测走 `coreApi`（Core API `/api/v1`）；不引入 Services API | 现有双客户端架构 + PRD 边界 |
| 图表库 | `echarts-for-react`（已在 `package.json`） | UX §1.2 |
| 类型生成 | `npm run gen-api` 重新生成 `core-schema.d.ts` | 现有约定 |
| Core handler 现状 | logs/events/metrics/security-events/exec 已 Sprint 12/13 完成；console handler 需补全 | CURRENT-SPRINT.md |

---

## 2. Architecture

### 2.1 System Context

```text
┌─────────────────────────────────────────────────────────────────┐
│ Console (repo/frontends/console/)                               │
│  实例详情 Tab 组                                                  │
│  ┌────────┬────────┬────────┬─────────┬─────────┬─────────────┐  │
│  │ 日志   │ 事件   │ 指标   │ 终端    │ 控制台  │ 安全事件    │  │
│  │ Tab    │ Tab    │ Tab    │ Tab     │ Tab     │ Tab         │  │
│  └───┬────┴───┬────┴───┬────┴───┬─────┴───┬─────┴──────┬──────┘  │
│      │        │        │        │         │            │         │
│   coreApi.GET/POST (openapi-fetch, baseUrl=/api/v1)              │
└──────┬──────────┬──────────┬──────────┬──────────┬──────────┬───┘
       │          │          │          │          │          │
       ▼          ▼          ▼          ▼          ▼          ▼
┌─────────────────────────────────────────────────────────────────┐
│ Core Gateway (repo/services/ani-gateway/)                       │
│  /api/v1/instances/{id}/logs|events|metrics|exec|console|        │
│  /api/v1/instances/{id}/security-events                         │
│  /api/v1/observability/query                                    │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │ demoInstanceAPI (handler)                                │   │
│  │  ports.InstanceObservability (port)                      │   │
│  └────────────────────────────┬─────────────────────────────┘   │
└──────────────────────────────┬──────────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│ Adapter 层 (repo/pkg/adapters/runtime/)                         │
│  ├─ LocalInstanceObservabilityService (合成数据，dev profile)    │
│  ├─ PrometheusInstanceObservabilityService (real，Sprint 13)    │
│  └─ [待补] 多 exporter 聚合 adapter (metrics.k8s.io + DCGM)     │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 Component Design

#### 2.2.1 Console 端组件（新建）

| 组件 | 职责 | 边界 |
|------|------|------|
| `InstanceDetailLayout` | 实例详情壳层：PageHeader + Tab 栏 + Tab Panel | 路由组件，承担 kind 过滤 |
| `ObservabilityTabsConfig` | kind → 可见 Tab 映射的常量模块 | 静态配置，被多个 Tab 组件引用 |
| `LogsTab` | 日志列表 + cursor 分页 + 级别筛选 | 调用 `listInstanceLogs` |
| `EventsTab` | 事件列表 + cursor 分页 | 调用 `listInstanceEvents` |
| `MetricsTab` | 双通道：快照卡片 + PromQL 时序图 | 调用 `getInstanceMetrics` + `queryObservability` |
| `TerminalTab` | exec WebSocket 连接与会话 | 调用 `createInstanceExecSession` + ws |
| `ConsoleTab` | VM console session 申请 + 新窗口打开 | 调用 `createInstanceConsoleSession` |
| `SecurityEventsTab` | Sandbox 安全事件列表 + severity 筛选 | 调用 `listInstanceSecurityEvents` |
| `promqlTemplates` | PromQL 冻结模板 ID → 模板字符串的常量模块 | 引用本 SPEC §5.2 表 |
| `InstanceContext` | 详情页上下文（instance 记录、kind、state） | React Context，被 Tab 共享 |

#### 2.2.2 Core 端组件（补全）

| 组件 | 职责 | 现状 |
|------|------|------|
| `demoInstanceAPI.listConsole`（待补） | `POST /instances/{id}/console` handler | OpenAPI 已声明，handler 未确认实现 |
| `InstanceObservability.CreateConsoleSession`（待补） | port 接口扩展 | 现 port 仅有 5 个方法 |
| `LocalInstanceObservabilityService.CreateConsoleSession`（待补） | local adapter 实现 | 合成 console session 数据 |
| 多 exporter 聚合 adapter（待补） | `getInstanceMetrics` 聚合 metrics.k8s.io + DCGM | SPEC 定义方案，实现归后续 Core 批次 |
| Prometheus real adapter | Sprint 13 已完成 | 不在本 SPEC 范围 |

### 2.3 Module Interactions

#### 2.3.1 Console 指标双通道数据流

```text
MetricsTab
├── 快照区
│   └── useQuery(['instance','metrics',instanceId])
│       └── coreApi.GET('/instances/{id}/metrics')
│           └── Core adapter ← exporter(s) (metrics.k8s.io / DCGM)
│           └── 返回 InstanceMetrics（null 字段保留）
│
└── 时序区
    └── useQuery(['observability','query',templateId,instanceId,range])
        └── coreApi.GET('/observability/query', { params: { query: renderedPromQL } })
            └── Core Prometheus 代理
            └── 返回 ObservabilityQueryResponse
```

#### 2.3.2 Console 终端 Tab 连接流

```text
TerminalTab
├── useMutation(createInstanceExecSession)
│   └── coreApi.POST('/instances/{id}/exec', { body: { idempotency_key, ... } })
│       └── 返回 InstanceExecSession { ws_url, expires_at }
│
└── WebSocket(ws_url)  ← 连接成功后建立
    └── xterm.js 渲染终端（细节见 §5.3）
```

#### 2.3.3 Core handler 调用链

```text
HTTP → demoInstanceAPI.listXxx
       └── api.observability.ListXxx(ctx, ports.InstanceObservationListRequest{...})
           └── LocalInstanceObservabilityService / PrometheusInstanceObservabilityService
           └── 返回 ports.InstanceXxxListResult（含 DevProfile）
       └── demoInstanceXxxFromResult() 转换为 HTTP response
       └── c.JSON(200, response)
```

### 2.4 File Structure

#### 2.4.1 Console 端新建文件

```text
repo/frontends/console/src/
├── routes/
│   └── compute/
│       └── instances/
│           ├── $instanceId/
│           │   └── route.tsx              [NEW] 实例详情路由壳层（PageHeader + Tabs）
│           └── index.tsx                [NEW] 实例列表（仅占位，列表逻辑归实例管理 SPEC）
├── features/
│   └── instance-observability/
│       ├── InstanceContext.tsx          [NEW] 实例上下文 Provider
│       ├── observabilityTabsConfig.ts   [NEW] kind → 可见 Tab 映射常量
│       ├── LogsTab.tsx                  [NEW]
│       ├── EventsTab.tsx                [NEW]
│       ├── MetricsTab.tsx               [NEW]
│       ├── MetricsSnapshot.tsx          [NEW] 快照卡片子组件
│       ├── MetricsChart.tsx             [NEW] ECharts 时序图子组件
│       ├── TerminalTab.tsx              [NEW]
│       ├── ConsoleTab.tsx               [NEW]
│       ├── SecurityEventsTab.tsx        [NEW]
│       ├── promqlTemplates.ts           [NEW] PromQL 冻结模板常量
│       └── useInstanceObservability.ts  [NEW] React Query hooks 封装
└── routeTree.gen.ts                     [AUTO] 路由树自动重新生成
```

#### 2.4.2 Core 端修改文件

```text
repo/
├── pkg/
│   └── ports/
│       └── instance_observability.go    [MODIFY] 新增 CreateConsoleSession 方法 + 请求/响应类型
├── pkg/adapters/runtime/
│   ├── local_instance_observability_service.go      [MODIFY] 实现 CreateConsoleSession
│   └── local_instance_observability_service_test.go [MODIFY] 补 console session 测试
└── services/ani-gateway/internal/router/
    ├── demo_instances.go                [MODIFY] 新增 createConsole handler + 注册路由
    └── demo_instances_test.go           [MODIFY] 补 console handler 测试
```

---

## 3. Data Model

> Console UI-only 批次无 DB migration。本节定义 Core port/adapter 类型与 Console TS 类型。

### 3.1 Core Port 类型扩展（`pkg/ports/instance_observability.go`）

```go
// 新增方法
type InstanceObservability interface {
    // ... 现有 5 个方法 ...
    CreateConsoleSession(ctx context.Context, request InstanceConsoleSessionCreateRequest) (InstanceConsoleSessionRecord, error)
}

// 新增请求类型
type InstanceConsoleSessionCreateRequest struct {
    TenantID       string
    InstanceID     string
    Protocol       string  // console / vnc / novnc / serial；空值默认 vnc
    IdempotencyKey string  // 可选；console 未在 OpenAPI 强制 idempotency_key
}

// 新增响应类型
type InstanceConsoleSessionRecord struct {
    SessionID  string
    Protocol   string
    ConnectURL string
    URL        string
    ExpiresAt  time.Time
    DevProfile DevProfileInfo
}
```

### 3.2 Console TS 类型（来自 `core-schema.d.ts` 自动生成）

Console 通过 `npm run gen-api` 重新生成类型，引用 `paths` 与 `components.schemas`：

```typescript
// 来自 openapi-fetch 自动推断
type InstanceLogEntry = components['schemas']['InstanceLogEntry']
type InstanceEvent = components['schemas']['InstanceEvent']
type InstanceMetrics = components['schemas']['InstanceMetrics']
type InstanceExecSession = components['schemas']['InstanceExecSession']
type InstanceConsoleSession = components['schemas']['InstanceConsoleSession']
type InstanceSecurityEvent = components['schemas']['InstanceSecurityEvent']
type ObservabilityQueryResponse = components['schemas']['ObservabilityQueryResponse']
```

### 3.3 Console 内部类型

```typescript
// observabilityTabsConfig.ts
type InstanceKind =
  | 'container' | 'gpu_container' | 'sandbox' | 'vm'
  | 'batch_job' | 'notebook' | 'k8s_cluster' | 'bare_metal' | 'dpu_node'

type ObservabilityTabId =
  | 'logs' | 'events' | 'metrics' | 'terminal' | 'console' | 'security-events'

interface ObservabilityTabConfig {
  tabs: ObservabilityTabId[]
  metricsSupported: boolean
}

const INSTANCE_OBSERVABILITY_TAB_CONFIG: Record<InstanceKind, ObservabilityTabConfig> = {
  container:       { tabs: ['logs','events','metrics','terminal'], metricsSupported: true },
  gpu_container:   { tabs: ['logs','events','metrics','terminal'], metricsSupported: true },
  sandbox:         { tabs: ['logs','events','metrics','terminal','security-events'], metricsSupported: true },
  vm:              { tabs: ['logs','events','metrics','console'], metricsSupported: true },
  batch_job:       { tabs: ['logs','events','metrics'], metricsSupported: true },
  notebook:        { tabs: ['logs','events','metrics'], metricsSupported: true },
  k8s_cluster:     { tabs: ['logs','events'], metricsSupported: false },
  bare_metal:      { tabs: ['logs','events'], metricsSupported: false },
  dpu_node:        { tabs: ['logs','events'], metricsSupported: false },
}
```

### 3.4 Migration Plan

- **Core**：port 接口扩展属新增方法，不破坏现有 adapter 实现（接口新增方法需要所有 adapter 补实现；当前仅有 local + prometheus 两个 adapter，影响可控）。
- **Console**：纯前端文件新增，无 schema 迁移；`core-schema.d.ts` 通过 `npm run gen-api` 重新生成。
- **回滚**：Console 删除新建文件即可；Core port 新增方法回滚需同步删除所有 adapter 实现。

---

## 4. API Design

### 4.0 Frozen Facts Table

来源：`repo/api/openapi/v1.yaml`（Core OpenAPI 唯一真实来源）。

#### Frozen Paths（已声明，不可改）

| Method | Path | operationId | RBAC Scope | 成功 | 错误码 |
|--------|------|-------------|------------|------|--------|
| GET | `/api/v1/instances/{instance_id}/logs` | `listInstanceLogs` | `scope:instances:read` | 200 + `InstanceLogListResponse` | 401, 403, 404 |
| GET | `/api/v1/instances/{instance_id}/events` | `listInstanceEvents` | `scope:instances:read` | 200 + `InstanceEventListResponse` | 401, 403, 404 |
| GET | `/api/v1/instances/{instance_id}/metrics` | `getInstanceMetrics` | `scope:instances:read` | 200 + `InstanceMetrics` | 401, 403, 404 |
| POST | `/api/v1/instances/{instance_id}/exec` | `createInstanceExecSession` | `scope:instances:exec` | 200 + `InstanceExecSession` | 400, 401, 403, 404, 422 |
| POST | `/api/v1/instances/{instance_id}/console` | `createInstanceConsoleSession` | `scope:instances:console` | 200 + `InstanceConsoleSession` | 400, 401, 403, 404 |
| GET | `/api/v1/instances/{instance_id}/security-events` | `listInstanceSecurityEvents` | `scope:instances:read` | 200 + `InstanceSecurityEventListResponse` | 401, 403, 404 |
| GET | `/api/v1/observability/query` | `queryObservability` | `scope:observability:read` | 200 + `ObservabilityQueryResponse` | 400, 401, 403 |

#### Frozen Schemas（关键字段）

| Schema | 必填字段 | 可空字段（null） |
|--------|----------|-------------------|
| `InstanceLogEntry` | timestamp, level, message | container, stream |
| `InstanceEvent` | id, instance_id, type, reason, message, occurred_at | count |
| `InstanceMetrics` | instance_id, timestamp, dev_profile | cpu_utilization_pct, memory_*, gpu_*, network_* |
| `InstanceExecSession` | id, instance_id, ws_url, expires_at, dev_profile | token |
| `InstanceConsoleSession` | session_id, protocol, connect_url, url, expires_at | operation_id |
| `InstanceSecurityEvent` | id, instance_id, event_type, severity, occurred_at | description |
| `ObservabilityQueryResponse` | query, result_type, results, dev_profile | — |

#### Non-Frozen Capabilities（待补，不在本 SPEC 冻结）

| 能力 | 状态 | 归属 |
|------|------|------|
| PromQL 模板正文与 label 映射表 | 待补 | 运维文档 + 后续 Core 批次 |
| adapter 多 exporter 聚合实现（DCGM 路径） | 待补 | 后续 Core 批次 |
| exec WebSocket 帧格式细节（除本 SPEC §5.3 客户端协议契约外） | 待补 | 后续 Core 批次 |
| Prometheus real provider 完整 production ready | Sprint 13 已 production-shaped | 不在本 SPEC 范围 |

#### Known Risky Assumptions

| 假设 | 风险 | 验证方式 |
|------|------|----------|
| Console 实例详情路由 `compute/instances/$instanceId` | 实例管理 SPEC 可能用不同路径 | 与 `container-instance-management.md` 对齐 |
| `core-schema.d.ts` 已含 7 个 endpoint 类型 | 若 OpenAPI 与生成结果不一致 | `npm run gen-api` 后 typecheck |
| `idempotency_key` 仅 exec 强制 | console 未在 OpenAPI 强制 | 见 v1.yaml `CreateInstanceConsoleSessionRequest` |

### 4.1 Endpoints（Console 消费视角）

> Console 通过 `coreApi`（`/api/v1`）调用，类型由 `core-schema.d.ts` 推断。下方仅列 Console 使用的调用契约。

#### 4.1.1 日志

```typescript
coreApi.GET('/instances/{instance_id}/logs', {
  params: {
    path: { instance_id: instanceId },
    query: { limit: 100, cursor: nextCursor, level: selectedLevel },
  },
})
// → { data?: InstanceLogListResponse, error?: {...} }
```

#### 4.1.2 事件

```typescript
coreApi.GET('/instances/{instance_id}/events', {
  params: {
    path: { instance_id: instanceId },
    query: { limit: 100, cursor: nextCursor, type: typeFilter },
  },
})
```

#### 4.1.3 指标快照

```typescript
coreApi.GET('/instances/{instance_id}/metrics', {
  params: { path: { instance_id: instanceId } },
})
// → { data?: InstanceMetrics }，null 字段保留，UI 显示「暂不可用」
```

#### 4.1.4 PromQL 时序

```typescript
coreApi.GET('/observability/query', {
  params: {
    query: { query: renderedPromQL, timeout: '30s' },
  },
})
// renderedPromQL 由 promqlTemplates[templateId] 注入 instance_id 后生成
```

#### 4.1.5 exec

```typescript
coreApi.POST('/instances/{instance_id}/exec', {
  params: { path: { instance_id: instanceId } },
  body: {
    idempotency_key: generateIdempotencyKey(),
    container: selectedContainer,  // 可选
    command: ['/bin/sh'],           // 默认
    tty: true, rows: 24, cols: 80,
  },
})
// → { data?: InstanceExecSession { ws_url, expires_at } }
```

#### 4.1.6 VM console

```typescript
coreApi.POST('/instances/{instance_id}/console', {
  params: { path: { instance_id: instanceId } },
  body: { protocol: selectedProtocol },  // 默认 vnc
})
// → { data?: InstanceConsoleSession { connect_url, url, expires_at } }
```

#### 4.1.7 安全事件

```typescript
coreApi.GET('/instances/{instance_id}/security-events', {
  params: {
    path: { instance_id: instanceId },
    query: { severity: selectedSeverity, limit: 100 },
  },
})
```

### 4.2 Request/Response Schemas

详见 §4.0 Frozen Schemas 与 `repo/api/openapi/v1.yaml`。本 SPEC 不重复 schema 全文，仅补充 Console 使用的派生类型：

```typescript
// useInstanceObservability.ts
interface LogsQueryParams {
  instanceId: string
  limit?: number
  cursor?: string
  level?: 'debug' | 'info' | 'warn' | 'error'
}

interface ObservabilityChartParams {
  templateId: PromQLTemplateId
  instanceId: string
  range: '15m' | '1h' | '6h' | '24h'
}
```

### 4.3 Error Responses

Console 通过 `openapi-fetch` 的 `{ error }` 解构获取错误体，统一形态：

```typescript
// Core 错误响应（来自现有代码模式）
{
  code: string       // INSTANCE_NOT_FOUND / BAD_REQUEST / UNSUPPORTED / ...
  message: string
  request_id: string
}
```

| HTTP | code | 触发条件 | Console UI 行为 |
|------|------|----------|------------------|
| 400 | BAD_REQUEST | 参数错误 / 缺 idempotency_key | `Message.error` + message |
| 401 | UNAUTHORIZED | 未认证 | 全局重定向登录 |
| 403 | FORBIDDEN | 无对应 scope | `Alert theme="warning"` 显示无权限 |
| 404 | INSTANCE_NOT_FOUND | 实例不存在或跨租户 | `Alert theme="error"` + request_id |
| 422 | PRECONDITION_FAILED | exec 时 state != running | `Message.error`「仅运行中实例可连接」 |

### 4.4 Breaking Changes

**无破坏性变更。** 本 SPEC 仅消费已声明 OpenAPI 路径；Core 端 port 接口新增方法属非破坏性扩展（现有 adapter 需补实现，但接口语义不变）。

### 4.5 OpenAPI Change Plan（Core only）

| Change | operationId | Compatibility | idempotency_key |
|--------|-------------|---------------|-----------------|
| 无新增 OpenAPI 路径 | — | — | — |

> 7 个 endpoint 均已在 `v1.yaml` 声明。本 SPEC 仅补全 handler 实现，不修改 OpenAPI 契约。

---

## 5. Business Logic

### 5.1 Core Algorithms

#### 5.1.1 Kind × Tab 可见性判定（Console）

```text
function getVisibleTabs(kind: InstanceKind): ObservabilityTabId[] {
  return INSTANCE_OBSERVABILITY_TAB_CONFIG[kind].tabs
}

function isMetricsSupported(kind: InstanceKind): boolean {
  return INSTANCE_OBSERVABILITY_TAB_CONFIG[kind].metricsSupported
}
```

`k8s_cluster`、`bare_metal`、`dpu_node` 的 `metricsSupported=false` → 指标 Tab 不渲染（非空态隐藏）。

#### 5.1.2 实例存在性 + state 校验（Console）

```text
进入详情前：
  - 实例 deleted → 整页 Empty「实例已删除」，不渲染 Tab
  - 实例存在 → 渲染 Tab 组，按 kind 过滤可见 Tab

exec / console 操作：
  - state != running → 按钮 disabled + Tooltip
  - state == running + 有权限 → 可点击
```

#### 5.1.3 Core handler 调用模式（沿用现有 `demoInstanceAPI`）

```text
listLogs(ctx, c):
  record, err := instanceForObservation(ctx, c)  // 实例存在性 + 租户校验
  if err != nil: writeInstanceObservabilityError(c, err); return
  result, err := observability.ListLogs(ctx, InstanceObservationListRequest{
    TenantID, InstanceID, Limit, Cursor, Level
  })
  if err != nil: writeInstanceObservabilityError(c, err); return
  c.JSON(200, demoInstanceLogListFromResult(result))

createConsole(ctx, c)（待补，沿用同模式）:
  record, err := instanceForObservation(ctx, c)
  if err != nil: writeInstanceObservabilityError(c, err); return
  if record.Kind != "vm": writeDemoError(c, 400, "UNSUPPORTED", "console only for vm"); return
  if record.State != "running": writeDemoError(c, 422, "PRECONDITION_FAILED", "instance not running"); return
  result, err := observability.CreateConsoleSession(ctx, InstanceConsoleSessionCreateRequest{
    TenantID, InstanceID, Protocol
  })
  if err != nil: writeInstanceObservabilityError(c, err); return
  c.JSON(200, demoInstanceConsoleSessionFromResult(result))
```

### 5.2 PromQL 模板冻结表

> PromQL 模板 ID 与参数注入规则在本 SPEC 冻结；模板正文（PromQL 字符串）维护在运维文档，本 SPEC 仅定义 ID 与注入契约。Console 通过 `promqlTemplates[templateId]` 引用，运行时注入 `instance_id`。

#### 5.2.1 模板 ID 表

| TemplateId | 适用 kind | 系列 | 注入参数 | 描述 |
|------------|-----------|------|----------|------|
| `instance_cpu_utilization` | 全 supported kind | CPU 利用率 | `{instance_id}` | 15m/1h/6h/24h 范围 CPU 利用率 |
| `instance_memory_utilization` | 全 supported kind | 内存使用率 | `{instance_id}` | 内存 used/total 比率 |
| `instance_gpu_utilization` | `gpu_container` | GPU 利用率 | `{instance_id}` | GPU 利用率（DCGM） |
| `instance_gpu_memory_utilization` | `gpu_container` | GPU 显存使用率 | `{instance_id}` | GPU 显存 used/total |

#### 5.2.2 注入契约

```typescript
// promqlTemplates.ts
type PromQLTemplateId =
  | 'instance_cpu_utilization'
  | 'instance_memory_utilization'
  | 'instance_gpu_utilization'
  | 'instance_gpu_memory_utilization'

// 模板字符串包含占位符 {{instance_id}}，运行时替换
// 模板正文从运维文档冻结，本 SPEC 不写死 PromQL 字符串
const PROMQL_TEMPLATES: Record<PromQLTemplateId, string> = {
  // 实际 PromQL 正文由运维文档提供，运行时 import
  // 此处仅声明 ID 集合，实现时从 frozen-templates 模块加载
}

function renderPromQL(templateId: PromQLTemplateId, instanceId: string): string {
  const tpl = PROMQL_TEMPLATES[templateId]
  return tpl.replaceAll('{{instance_id}}', instanceId)
}
```

#### 5.2.3 时间范围映射

| Console range | PromQL 语义 |
|---------------|-------------|
| 15m | `rate(...[15m])` 或近 15 分钟 |
| 1h | 近 1 小时（默认） |
| 6h | 近 6 小时 |
| 24h | 近 24 小时 |

时间范围由 Console 通过 `queryObservability` 的 `time` 参数或 PromQL 内 `__range` 表达；具体实现归运维文档冻结模板。

### 5.3 exec WebSocket 客户端协议契约

> PRD OQ-2 决策：PRD 只要求「能连上」。本 SPEC 定义 Console 端最小可工作的客户端协议契约；鉴权头、xterm 适配、TTY resize 等实现细节归本 SPEC 冻结，后续 Core 批次实现服务端时遵循。

#### 5.3.1 连接建立

```text
1. Console 调用 POST /instances/{id}/exec，body 含 idempotency_key
2. Core 返回 InstanceExecSession { ws_url, expires_at, token? }
3. Console 用 ws_url 建立 WebSocket 连接
   - 若 token 存在，作为子协议或查询参数传递（实现细节归服务端）
4. 连接成功 → xterm.js 渲染终端
5. 连接失败 / now > expires_at → 提示「会话已过期，请重新连接」
```

#### 5.3.2 帧格式（最小契约）

```text
客户端 → 服务端：
  - 输入帧：{ type: 'stdin', data: string }
  - resize 帧：{ type: 'resize', rows: number, cols: number }

服务端 → 客户端：
  - 输出帧：{ type: 'stdout' | 'stderr', data: string }
  - 退出帧：{ type: 'exit', code: number }
  - 错误帧：{ type: 'error', message: string }
```

> 帧格式细节（二进制 vs JSON、base64 编码等）归服务端实现冻结；Console 客户端按服务端约定适配。本 SPEC 只冻结「能连上 + stdin/stdout 双向 + exit 通知」语义。

#### 5.3.3 Console 端实现约束

- 使用 `xterm` + `xterm-addon-fit`（需新增依赖）
- 连接生命周期由 TerminalTab 组件管理，组件卸载时关闭 ws
- 会话过期不自动重连，由用户点击「重新连接」触发新 idempotency_key

### 5.4 VM Console 协议契约

```text
1. Console 调用 POST /instances/{id}/console，body 含 protocol
2. Core 返回 InstanceConsoleSession { connect_url, url, expires_at }
3. Console 调用 window.open(connect_url, '_blank', 'noopener,noreferrer')
4. 新窗口承载 VNC/console 前端（Core 服务端提供）
5. 会话过期 → 用户在 Console Tab 重新点击「打开控制台」
```

支持的 `protocol` 值（来自 OpenAPI `CreateInstanceConsoleSessionRequest`）：
- `console`（默认_SERIAL）
- `vnc`（默认）
- `novnc`
- `serial`

### 5.5 Validation Rules

| 规则 | 触发层 | 行为 |
|------|--------|------|
| `instance_id` 非空 | Core + Console | Console 路由参数校验；Core 404 |
| `limit` 1-1000（logs）/ 1-500（events/security-events） | Core | 超范围 400 |
| `level` ∈ {debug,info,warn,error} | Core | 非法值 400 |
| `severity` ∈ {info,warning,critical} | Core | 非法值 400 |
| `type` ∈ {Normal,Warning}（events） | Core | 非法值 400 |
| exec `idempotency_key` 必填 | Core | 缺失 400 |
| exec 实例 `state == running` | Core | 否则 422 |
| console 实例 `kind == vm` | Core | 否则 400 |
| console 实例 `state == running` | Core | 否则 422 |
| PromQL `query` 非空 | Core | 空字符串 400 |

### 5.6 State Machine

#### 5.6.1 实例 state × 操作可见性

| 操作 | stopped | running | deleted |
|------|---------|---------|---------|
| logs / events / metrics / security-events | ✅ | ✅ | ❌（整页 Empty） |
| exec（容器类） | ❌（按钮 disabled） | ✅ | ❌ |
| console（vm） | ❌（按钮 disabled） | ✅ | ❌ |

#### 5.6.2 终端 Tab 状态机

```text
idle ──(用户点击连接)──→ connecting ──(POST 成功 + ws open)──→ connected
  │                          │                                       │
  │                          │ POST 4xx/422                          │ now > expires_at
  │                          ▼                                       ▼
  │                       error(idle)                              expired
  │                          │                                       │
  └──────────────────────────┴────────────(用户点击重新连接)─────────┘
```

disabled 子状态：`disabled-not-running`（state≠running）、`disabled-no-permission`（无 exec scope）。

### 5.7 Edge Cases

| 场景 | 处理 |
|------|------|
| 指标快照字段 null | 该卡片显示「暂不可用」，不显示 0；不隐藏整个指标 Tab |
| PromQL 无数据 | 图表区 `Empty`「所选时间范围暂无数据」；不伪造曲线 |
| PromQL 403 | 图表区 `Alert`「无权限查看趋势数据」；快照区不受影响 |
| 快照与趋势时间不一致 | 各自标注 `timestamp` / 查询时间 |
| 实例刚删除 | 整页 `Empty`「实例已删除」 |
| exec session 过期 | Banner + 「重新连接」按钮 |
| 日志 message 超长 | 列 ellipsis + tooltip；首期不做 ANSI 彩色解析 |
| 非法 `?tab=` 深链 | 回退到「日志」Tab |
| k8s_cluster/bare_metal/dpu_node | 指标 Tab 不渲染（非空态隐藏） |

---

## 6. Error Handling

### 6.1 Error Taxonomy

| HTTP | code | 触发条件 | Console UI |
|------|------|----------|------------|
| 400 | BAD_REQUEST | 参数非法 / 缺 idempotency_key | `Message.error` + message |
| 401 | UNAUTHORIZED | 未认证 | 全局重定向登录 |
| 403 | FORBIDDEN | 无对应 scope | `Alert theme="warning"` |
| 404 | INSTANCE_NOT_FOUND | 实例不存在 / 跨租户 | `Alert theme="error"` + request_id |
| 422 | PRECONDITION_FAILED | exec/console 时 state != running | `Message.error` |
| 5xx | — | 服务端错误 | `Alert theme="error"` + request_id + 重试 |

### 6.2 Retry Strategy

| 操作 | 可重试 | 策略 |
|------|--------|------|
| logs/events/metrics/security-events GET | 是 | 用户手动「重试」按钮；不自动重试 |
| PromQL query GET | 是 | 用户手动重试；不自动 |
| exec POST | 是 | 复用同一 `idempotency_key` 幂等重试 |
| console POST | 是 | 用户重新点击 |
| WebSocket 连接失败 | 否 | 不自动重连；用户点击「重新连接」 |

### 6.3 Failure Modes

| 依赖失败 | 降级方案 |
|----------|----------|
| Core adapter exporter 不可用 | `getInstanceMetrics` 返回 null 字段；UI「暂不可用」 |
| Prometheus 不可用 | `queryObservability` 返回错误；图表区错误态，快照区不影响 |
| exec WebSocket 断开 | 终端区显示断开，提供「重新连接」 |
| 实例管理 API 失败（取 instance） | 详情页整体错误态 |

---

## 7. Security

### 7.1 Authentication & Authorization

| 操作 | RBAC scope | 校验层 |
|------|------------|--------|
| logs / events / metrics / security-events | `scope:instances:read` | Core middleware + handler |
| exec | `scope:instances:exec` | Core middleware + handler |
| console | `scope:instances:console` | Core middleware + handler |
| PromQL query | `scope:observability:read` | Core middleware + handler |

Console 通过 `auth.ts` 的 Bearer Token 中间件自动注入 `Authorization` 头。

### 7.2 Input Validation

- 路径参数 `instance_id` 由 Console 路由保证非空
- 查询参数（level/severity/type/limit/cursor）由 Console Select 组件限定枚举值
- exec `idempotency_key` 由 Console 生成（`crypto.randomUUID()` 或 `chat-${Date.now()}` 模式）
- PromQL `query` 不接受用户自由输入，仅由冻结模板渲染

### 7.3 Data Protection

- 不向 Console 暴露 Prometheus 服务地址（PRD FR-8）
- exec `token`（若返回）不持久化，仅在内存中用于 ws 握手
- console `connect_url` 不写入 Console 状态，仅 `window.open` 后丢弃
- 日志 `message` 列首期纯文本，不做用户输入渲染（防 XSS）

---

## 8. Performance

### 8.1 Expected Load

| 场景 | 预估 | 来源 |
|------|------|------|
| 日志首屏 | ≤ 2s（100 条） | PRD §7 `[Assumption]` |
| 快照刷新 | ≤ 1s（local profile） | PRD §7 |
| PromQL 查询 | 依赖 Prometheus；UI 不阻塞 | — |
| 单租户并发详情 | < 10 个用户 | 估算 |

### 8.2 Optimization Strategy

| 策略 | 实现 |
|------|------|
| 日志 cursor 分页 | 不一次性加载全部；「加载更多」按需 |
| 指标快照缓存 | React Query 默认缓存 30s；`staleTime: 30_000` |
| 指标自动刷新 | Switch 开关，30s refetch 快照；图表随 range refetch |
| PromQL 查询缓存 | React Query key 含 `[templateId, instanceId, range]`；切换 range 自动 refetch |
| WebSocket 按需连接 | 终端 Tab 切走时关闭 ws；切回时需用户重新连接 |

### 8.3 Database Considerations

> Console UI-only 无 DB。Core 端不新增表；metrics 聚合数据来自 exporter，无持久化。

---

## 9. Testing Strategy

### 9.1 Console 单元测试

| 组件 | 测试点 | mock |
|------|--------|------|
| `observabilityTabsConfig` | 各 kind 的 Tab 列表与 metricsSupported | 无 |
| `LogsTab` | loading / empty / error / 加载更多 / 级别筛选 | mock `coreApi.GET` |
| `EventsTab` | loading / empty / error / cursor 分页 | mock |
| `MetricsTab` | 快照 null 字段「暂不可用」/ 图表 empty / 图表 403 | mock |
| `MetricsChart` | ECharts 渲染 / 无数据 / 错误态 | mock |
| `TerminalTab` | disabled-not-running / disabled-no-permission / connecting / expired | mock POST + ws |
| `ConsoleTab` | disabled / opening / opened / error | mock POST |
| `SecurityEventsTab` | loading / empty / error / severity 筛选 | mock |
| `promqlTemplates` | renderPromQL 占位符替换 | 无 |

### 9.2 Console 集成测试（browser）

| 场景 | kind | 验证点 |
|------|------|--------|
| Tab 差异 | container | 有终端；无控制台、无安全事件 |
| Tab 差异 | vm | 有控制台；无终端 |
| Tab 差异 | sandbox | 有终端 + 安全事件 |
| Tab 差异 | batch_job | 无终端 |
| Tab 差异 | k8s_cluster | 无指标 Tab |
| 日志 empty | any | Empty 非 error |
| 指标 partial null | gpu_container | GPU 卡片「暂不可用」 |
| 指标 chart empty | container | PromQL 无数据 Empty |
| 终端 disabled | container stopped | 按钮 disabled |
| exec 403 | container | Alert 无权限 |

### 9.3 Core 单元测试（待补）

| 测试 | 范围 |
|------|------|
| `createConsole` handler | 成功 / 非 vm 400 / 非 running 422 / 404 / 403 |
| `LocalInstanceObservabilityService.CreateConsoleSession` | 合成数据生成 / DevProfile 标记 |
| 现有 5 个 handler 回归 | Sprint 12 已覆盖，确保接口扩展不破坏 |

### 9.4 Acceptance Criteria Mapping

| US/FR | 测试 | 类型 | 描述 |
|-------|------|------|------|
| US-001 / FR-1 | `listLogs handler 200 + 404 + 403` | Core unit | Sprint 12 已覆盖 |
| US-002 / FR-1 | `listEvents handler 200 + 404 + 403` | Core unit | Sprint 12 已覆盖 |
| US-003 / FR-3 | `getInstanceMetrics null 字段保留` | Core unit + Console | adapter 聚合 + UI「暂不可用」 |
| US-004 / FR-1,4,5 | `createExecSession 422 + idempotency` | Core unit | Sprint 12 已覆盖 |
| US-005 / FR-1 | `createConsoleSession 200 + 400 + 422` | Core unit | **待补** |
| US-006 / FR-1 | `listSecurityEvents 200 + severity 筛选` | Core unit | Sprint 12 已覆盖 |
| US-007 / FR-9,10,11,12 | `Tab 差异 browser 验证` | Console browser | 5 种 kind Tab 差异 |
| US-008 / FR-6 | `LogsTab loading/empty/error` | Console unit | 全状态 |
| US-009 | `EventsTab loading/empty/error` | Console unit | 全状态 |
| US-010 / FR-6,13 | `MetricsSnapshot null「暂不可用」` | Console unit | GPU 字段缺失 |
| US-011 / FR-7,8 | `MetricsChart PromQL empty/error/403` | Console unit | 不伪造曲线 |
| US-012 / FR-9 | `TerminalTab disabled/connecting/expired` | Console unit | 状态机 |
| US-013 / FR-10 | `ConsoleTab disabled/opening/opened` | Console unit | VM only |
| US-014 / FR-11 | `SecurityEventsTab severity 筛选` | Console unit | sandbox only |
| US-015 / FR-12 | `k8s_cluster 无指标 Tab` | Console browser | unsupported kind 隐藏 |
| FR-2 | `跨租户 404` | Core unit | Sprint 12 已覆盖 |
| FR-7 | `PromQL 走冻结模板` | Console unit | 不硬编码 label |

---

## 10. Implementation Plan

### 10.1 Phases

| Phase | 范围 | 依赖 |
|-------|------|------|
| P0 Core console handler 补全 | port + local adapter + handler + 测试 | 无 |
| P1 Console 框架 + 上下文 | 路由壳层 + InstanceContext + observabilityTabsConfig | P0（handler 可用） |
| P2 Console 日志/事件 Tab | LogsTab + EventsTab | P1 |
| P3 Console 指标 Tab | MetricsTab + 快照 + PromQL 图表 | P1 + PromQL 模板 |
| P4 Console 终端/控制台 Tab | TerminalTab + ConsoleTab | P1 + P0（console handler） |
| P5 Console 安全事件 Tab | SecurityEventsTab | P1 |
| P6 browser 验证 + 收口 | 全 kind Tab 差异验证 + typecheck/lint | P2-P5 |

### 10.2 Issue Mapping

| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| #1 Core console handler 补全 | 3.1, 4.0, 5.1.3, 9.3 | high | — |
| #2 Console 路由壳层 + 上下文 | 2.4.1, 3.3, 5.1.1 | high | #1 |
| #3 Console 日志 Tab | 4.1.1, 5.7, 6.1, 9.1, 9.4(US-008) | medium | #2 |
| #4 Console 事件 Tab | 4.1.2, 9.4(US-009) | medium | #2 |
| #5 Console 指标 Tab（快照+图表） | 4.1.3, 4.1.4, 5.2, 8.2, 9.4(US-010,011) | high | #2 |
| #6 Console 终端 Tab | 4.1.5, 5.3, 5.6.2, 9.4(US-012) | high | #2 |
| #7 Console 控制台 Tab | 4.1.6, 5.4, 9.4(US-013) | medium | #1, #2 |
| #8 Console 安全事件 Tab | 4.1.7, 9.4(US-014) | medium | #2 |
| #9 Console browser 验证收口 | 9.2, 9.4(US-007,015) | medium | #3-#8 |

### 10.3 Incremental Delivery

- 每个 Issue 独立交付，按依赖顺序合并
- Core handler 补全（#1）可独立于 Console 验证（Core unit test + local profile）
- Console Tab 组件可按 Tab 独合（日志/事件先行，指标/终端后续）
- 指标 Tab 双通道可分两批：快照先合，PromQL 图表后合（依赖模板冻结）

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

| 问题 | 归属 | 状态 |
|------|------|------|
| PromQL 模板正文与 label 映射表 | 运维文档 + 后续 Core 批次 | 待补（非本 SPEC 阻塞项） |
| adapter 多 exporter 聚合实现细节（DCGM 路径） | 后续 Core 批次 | 待补 |
| exec WebSocket 服务端帧格式冻结 | 后续 Core 批次 | 本 SPEC 仅冻结客户端契约 |
| 实例详情路由最终路径（`compute/instances/$instanceId`） | 与实例管理 SPEC 对齐 | `[Assumption]` 待确认 |

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|-----------|
| 实例管理 SPEC 用不同详情路由 | Console 路由返工 | 与 `container-instance-management.md` 对齐后再落地 |
| `core-schema.d.ts` 与 OpenAPI 不一致 | typecheck 失败 | `npm run gen-api` 后 typecheck |
| Prometheus real provider 未 production ready | 时序图无数据 | UI 正确处理 empty/error；不伪造 |
| exec WebSocket 服务端未实现 | 终端 Tab 无法真连 | local profile 下 POST 返回合成 ws_url；UI 验证状态机 |
| xterm 依赖新增 | 包体积 | 已在 UX §1.2 指定；评估后引入 |

### 11.3 Assumptions

- Console 实例详情嵌入 `__root.tsx` Layout，不单独占侧栏菜单项（UX §1.1）
- 实例列表/概览 Tab 由实例管理 SPEC 实现；本 SPEC 仅定义观测 Tab 面板（UX §8.4）
- PromQL 由前端常量模块引用冻结模板 ID，运行时注入 `instance_id`（UX §8.4）
- 图表使用已有依赖 `echarts-for-react`（UX §1.2）
- 日志 `message` 列不做 ANSI 彩色解析（首期纯文本）（UX §8.4）
- URL `?tab=` 深链为可选增强，Phase 1 可仅用本地 Tab state（UX §8.4）
- Core 端 logs/events/metrics/security-events/exec handler 已 Sprint 12/13 完成，本 SPEC 仅补 console handler
- `idempotency_key` 仅 exec 在 OpenAPI 强制；console 未强制（见 v1.yaml）

---

## References

- `repo/api/openapi/v1.yaml`（Core OpenAPI 唯一真实来源）
- `repo/services/tasks/modules/prd/console/compute/prd-console-instance-observability.md`
- `repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability.md`
- `repo/services/docs/console-modules/compute/container-observability.md`（全 kind 扩展版，待覆盖）
- `repo/services/tasks/execution/CORE-HANDLER-IMPLEMENTATION-GUIDE.md` §TASK-CORE-005
- `repo/CURRENT-SPRINT.md`（Sprint 12/13 handler 现状）
- `repo/pkg/ports/instance_observability.go`（现有 port 定义）
- `repo/pkg/adapters/runtime/local_instance_observability_service.go`（现有 local adapter）
- `repo/pkg/adapters/runtime/prometheus_instance_observability.go`（Sprint 13 real adapter）
- `repo/services/ani-gateway/internal/router/demo_instances.go`（现有 handler）
- `repo/frontends/console/src/api/coreClient.ts`（Core API 客户端）
- `repo/frontends/console/src/routes/__root.tsx`（Layout 模板）
