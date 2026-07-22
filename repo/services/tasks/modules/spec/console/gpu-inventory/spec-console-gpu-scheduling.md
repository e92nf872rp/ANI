# SPEC: K8s GPU 调度 — Console 前端 v1.0.0 P0

> Technical specification derived from:
> - PRD: [`prd-k8s-gpu-hami-volcano-scheduling.md`](../../../prd/console/gpu-inventory/prd-k8s-gpu-hami-volcano-scheduling.md)
> - UX: [`ux-console-gpu-scheduling.md`](../../../prd/console/gpu-inventory/ux-console-gpu-scheduling.md)
> - Sibling SPEC: `spec-core-gpu-scheduling.md`（Core 后端）/ `spec-boss-gpu-pool.md`（BOSS）
> Generated: 2026-07-03 | Target branch: `feature/gpu-scheduling-p0-console` | Commit: TBD
>
> **Scope:** **only** `repo/frontends/console/`
> **Source of truth:** 消费 OpenAPI — no backend changes in UI-only batch
> 前端技术栈：TDesign React 1.10 + TanStack Router 1.40 + TanStack Query 5.40 + openapi-fetch 0.12 + Zustand 4.5 + ECharts 5.5

---

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 覆盖 Console 前端 P0 GPU 调度 UI：① Console Shell 组件（ConsolePage/Header/ContentCard）新建；② GPU 算力管理页（`/compute/gpu`）—— KPI + 型号分布 + Tabs(节点/设备/占用分布)；③ GPU 容器实例列表 + 创建 Dialog + 详情；④ GPU 调度队列设置页（`/settings/gpu-queues`）—— 租户管理员 CRUD + 项目成员只读；⑤ 概览页 GPU 利用率卡。

### 1.2 PRD Reference

- Source: `repo/services/tasks/modules/prd/console/gpu-inventory/prd-k8s-gpu-hami-volcano-scheduling.md`
- UX source: `ux-console-gpu-scheduling.md`
- User Stories covered: US-004a, US-005, US-006, US-008
- Functional Requirements covered: FR-11, FR-13

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| API 客户端 | `coreApi`（openapi-fetch，`/api/v1` 前缀） | 已有模式，v1.yaml 生成类型 |
| 路由结构 | `/compute/gpu`、`/compute/gpu-containers`、`/settings/gpu-queues` | UX §2.1 规划 |
| Shell 组件 | P0 新建 ConsolePage/Header/ContentCard | 代码库当前不存在 |
| 概览页 GPU 卡 | 复用 `getGPUOccupancy`，新增到 `routes/index.tsx` | 当前仅有占位注释 |
| DCGM 利用率 | 经 `coreApi.GET /observability/query` PromQL | Core SPEC 冻结模板 |

---

## 2. Architecture

### 2.1 System Context

```text
Console Frontend (repo/frontends/console/)
  ├── coreApi (openapi-fetch, /api/v1)
  │   ├── GET /gpu-inventory           → GPU 算力管理页
  │   ├── GET /gpu-inventory/occupancy → KPI + 概览卡
  │   ├── GET/POST/PATCH/DELETE /gpu-scheduling/queues → 队列设置页
  │   ├── GET/POST /instances          → GPU 容器实例
  │   └── GET /observability/query     → DCGM 利用率
  └── api (openapi-fetch, /api/v1/svc) — 不涉及
```

### 2.2 Component Design

| 组件 | 位置 | 职责 | 状态 |
|------|------|------|------|
| **ConsolePage** | `src/components/shell/ConsolePage.tsx` | 页面壳布局容器 | **P0 新建** |
| **ConsolePageHeader** | `src/components/shell/ConsolePageHeader.tsx` | 标题 + 副标题 + 操作区 | **P0 新建** |
| **ConsoleContentCard** | `src/components/shell/ConsoleContentCard.tsx` | 内容卡片容器 | **P0 新建** |
| GPU 算力管理页 | `src/routes/compute/gpu.tsx` | KPI + 型号分布 + Tabs | **P0 新建** |
| GPU 容器列表 | `src/routes/compute/gpu-containers/index.tsx` | 列表 + 创建入口 | **P0 新建** |
| GPU 容器详情 | `src/routes/compute/gpu-containers/$instanceId.tsx` | Descriptions + state_reason Alert | **P0 新建** |
| 创建 Dialog | `src/routes/compute/gpu-containers.create-dialog.tsx` | 表单 + 调度字段 | **P0 新建** |
| 队列设置页 | `src/routes/settings/gpu-queues.tsx` | 默认队列只读 + 我的队列 CRUD | **P0 新建** |
| 概览页 GPU 卡 | `src/routes/index.tsx` | 修改：新增 GPU 利用率卡 | **P0 修改** |
| 侧栏菜单 | `src/routes/__root.tsx` | 修改：新增「算力与云资源」分组 | **P0 修改** |
| API 类型 | `src/api/core-schema.d.ts` | regen：`make gen-core-schema` | **P0 regen** |

### 2.3 Module Interactions

```text
概览页 → coreApi.GET /gpu-inventory/occupancy → KPI 卡 → link → /compute/gpu
GPU 算力管理 → coreApi.GET /gpu-inventory + /gpu-inventory/occupancy + /observability/query
GPU 容器列表 → coreApi.GET /instances?kind=gpu_container
创建 Dialog → coreApi.GET /gpu-scheduling/queues (Select options) + coreApi.POST /instances
队列设置 → coreApi.GET/POST/PATCH/DELETE /gpu-scheduling/queues
```

### 2.4 File Structure

```text
repo/frontends/console/src/
├── components/shell/
│   ├── ConsolePage.tsx                  [NEW]
│   ├── ConsolePageHeader.tsx            [NEW]
│   └── ConsoleContentCard.tsx          [NEW]
├── routes/
│   ├── index.tsx                        [MODIFY: 新增 GPU 利用率卡]
│   ├── __root.tsx                       [MODIFY: 新增「算力与云资源」菜单组]
│   ├── compute/
│   │   ├── gpu.tsx                      [NEW: GPU 算力管理]
│   │   └── gpu-containers/
│   │       ├── index.tsx                [NEW: 列表]
│   │       └── $instanceId.tsx          [NEW: 详情]
│   │   └── create-dialog.tsx            [NEW: 创建 Dialog 组件]
│   └── settings/
│       └── gpu-queues.tsx               [NEW: 队列设置]
└── api/
    └── core-schema.d.ts                [REGEN: make gen-core-schema]
```

---

## 3. Data Model

### 3.1 Consumed Schemas（从 Core OpenAPI 生成）

前端不定义 schema，全部从 `v1.yaml` 生成 `core-schema.d.ts`。P0 新增的 Core schema（见 Core SPEC §3.1）生成后前端直接消费：

```ts
type GPUSchedulingQueue = components['schemas']['GPUSchedulingQueue']
type GPUSchedulingQueueCreateRequest = components['schemas']['GPUSchedulingQueueCreateRequest']
type GPUOccupancyStats = components['schemas']['GPUOccupancyStats']
type GPUInventoryRecord = components['schemas']['GPUInventoryRecord']
type InstanceRecord = components['schemas']['InstanceRecord']
```

### 3.2 前端状态管理

- 队列列表：TanStack Query `useQuery(['gpu-queues'], ...)`
- 创建/编辑 Dialog 表单：组件内 `useState` 或 `useRef`
- RBAC scope 判定：从 auth context（`useAuth`）读取 `scope:gpu-scheduling:write`

---

## 4. API Design（消费侧）

### 4.1 消费端点

| Method | Path | 用途 | 调用页面 |
|--------|------|------|---------|
| GET | `/gpu-inventory` | 设备列表 | GPU 算力管理 |
| GET | `/gpu-inventory/occupancy` | KPI + 概览卡 | GPU 算力管理 + 概览 |
| GET | `/gpu-scheduling/queues` | 队列列表 | 队列设置 + 创建 Dialog |
| POST | `/gpu-scheduling/queues` | 创建队列 | 队列设置 |
| PATCH | `/gpu-scheduling/queues/{id}` | 更新队列 | 队列设置 |
| DELETE | `/gpu-scheduling/queues/{id}` | 删除队列 | 队列设置 |
| GET | `/instances?kind=gpu_container` | 容器列表 | GPU 容器实例 |
| POST | `/instances` | 创建容器 | 创建 Dialog |
| GET | `/instances/{id}` | 容器详情 | GPU 容器详情 |
| GET | `/observability/query` | DCGM 利用率 | GPU 算力管理 |

### 4.2 无新增 API

UI-only batch，不新增/修改任何 OpenAPI 端点。全部消费 Core SPEC 冻结的端点。

---

## 5. Business Logic

### 5.1 页面行为

**GPU 算力管理（`/compute/gpu`）：**
1. 并行请求 occupancy + inventory + observability(query DCGM)
2. KPI 5 卡：总量/已分配/空闲/平均利用率/异常
3. 型号分布：ECharts Progress 或条形图（`by_gpu_type`）
4. Tabs：节点（聚合）/设备（明细）/占用分布
5. 利用率 DCGM 降级：query 失败时显示「监控未就绪」Tag

**创建 GPU 容器 Dialog：**
1. 打开时 fetch queues → Select options
2. 表单：名称 + GPU 数量 + 分配模式(整卡/vGPU) + 工作负载类型 + 调度队列 + 型号偏好(可选)
3. 提交生成 `idempotency_key`（`crypto.randomUUID()`）
4. 成功：`Message.success` + 跳转详情或刷新列表
5. 422：`Message.error` + 保留表单

**队列设置（`/settings/gpu-queues`）：**
1. fetch queues → 分两组：`is_platform_default=true`（只读）/ `false`（可 CRUD）
2. RBAC：无 `scope:gpu-scheduling:write` → 隐藏「新建」+ 行操作 + Alert 提示
3. CRUD Dialog：name/weight/reclaimable/workload_class/project_id(可选)
4. 删除：`Popconfirm` → `Message.success`

### 5.2 Validation Rules

- 队列名：前端 K8s 资源名正则校验 `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
- GPU 数量：InputNumber min 1
- idempotency_key：前端生成 UUID v4

### 5.3 Edge Cases

- DCGM 未就绪 → 利用率 KPI 显示「监控未就绪」Tag，不阻塞页面
- 403 → Alert「无权查看」
- 队列列表为空 → Empty + write 用户显示 CTA
- 创建 422 → 高亮对应字段 + Message.error

---

## 6. Error Handling

### 6.1 Error Display

| Error | UI behavior | Components |
|-------|-------------|------------|
| 401 | 重定向登录 | 路由守卫 |
| 403 | Alert「无权查看」 | `Alert` |
| 5xx | 页顶 `Alert theme="error"` + 重试 | `Alert` + `Button` |
| 422 (创建) | `Message.error` + 保留表单 | `Message` |
| 网络超时 | TanStack Query 自动重试 3 次 | — |

### 6.2 Loading States

所有页面必须实现 loading/empty/error 三态（见 UX §6）。

---

## 7. Security

### 7.1 RBAC UI

| Scope | UI 行为 |
|-------|---------|
| `scope:gpu-inventory:read` | 可见 GPU 算力管理 + 概览卡 |
| `scope:gpu-scheduling:read` | 可见队列列表 |
| `scope:gpu-scheduling:write` | 可见「新建队列」+ 行内编辑/删除 |
| 无 write scope | 隐藏「新建」+ 行操作 + Alert「仅租户管理员可管理队列」 |

### 7.2 租户隔离

前端不传 `tenant_id`，由 JWT 注入。API 客户端 `coreApi` 自动携带 Authorization header。

---

## 8. Performance

- TanStack Query `staleTime: 30s`（inventory/occupancy/queues）
- DCGM query `staleTime: 60s`
- 列表分页 cursor 模式（如 API 返回 `next_cursor`）
- 无客户端重型计算

---

## 9. Testing Strategy

### 9.1 Browser Verification

按 UX §6 三态验证：
- `/compute/gpu`：loading / empty-inventory / empty-occupancy / util-unavailable / error / forbidden
- `/settings/gpu-queues`：loading / empty-custom / read-only-user / delete-confirm / error
- `/compute/gpu-containers`：loading / empty / error
- 创建 Dialog：submitting / validation-error / schedule-failed / success
- 详情页：failed-with-reason / provisioning-hint / error / 404

### 9.2 Build Gates

```bash
cd repo/frontends/console
pnpm type-check
pnpm lint
pnpm test
pnpm build
```

---

## 10. Implementation Plan

### 10.1 Phases

| Phase | 内容 | 依赖 |
|-------|------|------|
| **P0-① API regen** | `make gen-core-schema` 拿到新类型 | Core SPEC P0-① |
| **P0-② Shell 组件** | ConsolePage/Header/ContentCard | 无 |
| **P0-③ 页面实现** | 3 页面 + 创建 Dialog + 概览卡修改 | P0-①, P0-② |
| **P0-④ 侧栏注册** | `__root.tsx` 新增菜单组 | P0-③ |
| **P0-⑤ Browser 验证** | 三态 + RBAC + CRUD 闭环 | P0-③ |

### 10.2 Issue Mapping

| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| #7: Console Shell 组件 | §2.2 | high | — |
| #8: Console GPU 算力管理页 | UX §4.2, §5.1 | high | Core #1, #7 |
| #9: Console GPU 容器实例 + 创建 Dialog | UX §4.3-4.6, §5.2 | high | Core #1, #3, #7 |
| #10: Console 队列设置页 | UX §4.5, §5.3 | high | Core #1, #2, #7 |
| #11: Console 概览页 GPU 卡 | UX §4.1 | medium | Core #1, #7 |

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

- Shell 组件 API 设计（props 接口）需在 P0-② 确认
- 创建 Dialog 的 `resource_name` 字段如何映射到 payload（待 Core SPEC P0-① 冻结 `InstanceRecord.gpu.resource_name`）

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|-----------|
| Shell 组件不存在 | 页面阻塞 | P0-② 优先创建 |
| `core-schema.d.ts` regen 失败 | 类型缺失 | 确保 Core OpenAPI 先改 |
| 概览页 GPU 卡与现有推理服务卡布局冲突 | UI 错位 | 复用 Row/Col span=6 模式 |

### 11.3 Assumptions

- 复用现有 `coreApi`（`src/api/coreClient.ts`）调用模式
- `ConsolePage` 等组件基于 TDesign `Layout` 封装，与 `__root.tsx` 已有 Layout 兼容
- 侧栏「算力与云资源」分组在 `__root.tsx` 新增 Menu.MenuItem

---

## Frozen Facts Table

| 类别 | 事实 | 来源 | 状态 |
|------|------|------|------|
| 已存在 | `coreApi` 客户端 | `src/api/coreClient.ts` | ✅ exists |
| 已存在 | GPU 清单页 | `src/routes/gpu-inventory.tsx` | ✅ exists（需重构到 `/compute/gpu`） |
| 已存在 | 概览页骨架 | `src/routes/index.tsx` | ✅ exists（GPU 卡为占位） |
| 已存在 | 侧栏菜单 | `src/routes/__root.tsx` | ✅ exists（需加菜单组） |
| 已存在 | TDesign + TanStack 技术栈 | `package.json` | ✅ exists |
| 待补 | `GPUSchedulingQueue` 类型 | `core-schema.d.ts` | **待 Core P0-①** |
| 待补 | `InstanceRecord.gpu.queue_name` | `core-schema.d.ts` | **待 Core P0-①** |
| 不存在 | Console Shell 组件 | `src/components/shell/` | **P0 新建** |
| 不存在 | `/compute/gpu` 路由 | `src/routes/compute/` | **P0 新建** |
| 不存在 | `/settings/gpu-queues` 路由 | `src/routes/settings/` | **P0 新建** |
