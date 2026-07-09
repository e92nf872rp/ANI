# SPEC: K8s GPU 调度 — BOSS 前端 v1.0.0 P0

> Technical specification derived from:
> - PRD: [`prd-k8s-gpu-hami-volcano-scheduling.md`](../../../prd/console/gpu-inventory/prd-k8s-gpu-hami-volcano-scheduling.md)
> - UX: [`ux-boss-gpu-pool.md`](../../../prd/console/gpu-inventory/ux-boss-gpu-pool.md)
> - Sibling SPEC: `spec-core-gpu-scheduling.md`（Core 后端）/ `spec-console-gpu-scheduling.md`（Console）
> Generated: 2026-07-03 | Target branch: `feature/gpu-scheduling-p0-boss` | Commit: TBD
>
> **Scope:** **only** `repo/frontends/boss/`
> **Source of truth:** 消费 OpenAPI — no backend changes in UI-only batch
> 前端技术栈：TDesign React + TanStack Router（与 Console 对齐，BOSS 前端骨架待落地）

---

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 覆盖 BOSS 前端 P0 GPU 资源池管理 UI：① BOSS 前端项目骨架初始化（`repo/frontends/boss/` 当前不存在）；② GPU 资源池管理页（`/ops/gpu-pool`）—— 集群级 KPI + 型号分布 + Tabs(节点/异常/调度队列只读)；③ 租户占用排行占位区块（P0 仅 Alert，P1 替换为真实 Table）。

### 1.2 PRD Reference

- Source: `repo/services/tasks/modules/prd/console/gpu-inventory/prd-k8s-gpu-hami-volcano-scheduling.md`
- UX source: `ux-boss-gpu-pool.md`
- User Stories covered: US-007
- Functional Requirements covered: FR-12, FR-13

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| BOSS 前端骨架 | 从零创建，复用 Console 技术栈 | `repo/frontends/boss/` 不存在 |
| 数据源 | 复用 `GET /gpu-inventory/occupancy`（平台 scope） | PRD §10.3；不新增 aggregate API |
| 租户排行 | P0 占位 Alert，不展示假数据 | PRD NG-9；P1 才有真实 API |
| 与 Console 分工 | 页内 Alert 常驻说明 | UX §2.2 |
| 调度队列 | P0 只读全览，无 CRUD | PRD §10.5 |

---

## 2. Architecture

### 2.1 System Context

```text
BOSS Frontend (repo/frontends/boss/) — P0 从零创建
  ├── coreApi (openapi-fetch, /api/v1)
  │   ├── GET /gpu-inventory           → 节点/异常设备列表
  │   ├── GET /gpu-inventory/occupancy → 集群级 KPI
  │   └── GET /gpu-scheduling/queues   → 调度队列只读全览
  └── 无 Services API 调用
```

### 2.2 Component Design

| 组件 | 位置 | 职责 | 状态 |
|------|------|------|------|
| **BOSS 项目骨架** | `repo/frontends/boss/` | package.json + vite + tsconfig + pnpm workspace | **P0 新建** |
| **BOSS API 客户端** | `src/api/coreClient.ts` | openapi-fetch，`/api/v1` 前缀 | **P0 新建** |
| **BOSS 布局壳** | `src/routes/__root.tsx` | TDesign Layout + 侧栏 | **P0 新建** |
| GPU 资源池管理页 | `src/routes/ops/gpu-pool.tsx` | KPI + 型号分布 + Tabs + 占位排行 | **P0 新建** |

### 2.3 Module Interactions

```text
BOSS /ops/gpu-pool
  → coreApi.GET /gpu-inventory/occupancy (平台 scope) → KPI 4 卡
  → coreApi.GET /gpu-inventory (平台 scope) → 节点/异常 Tab
  → coreApi.GET /gpu-scheduling/queues → 调度队列只读 Tab
  → 租户排行 Section → 静态 Alert（不请求 API）
```

### 2.4 File Structure

```text
repo/frontends/boss/                       [NEW: 整个目录]
├── package.json                           [NEW]
├── pnpm-workspace.yaml                    [NEW]
├── tsconfig.json                          [NEW]
├── vite.config.ts                         [NEW]
├── index.html                             [NEW]
└── src/
    ├── main.tsx                           [NEW]
    ├── App.tsx                            [NEW]
    ├── api/
    │   └── coreClient.ts                  [NEW: openapi-fetch /api/v1]
    │   └── core-schema.d.ts               [NEW: make gen-core-schema]
    ├── routes/
    │   ├── __root.tsx                     [NEW: Layout + 侧栏]
    │   └── ops/
    │       └── gpu-pool.tsx               [NEW: GPU 资源池管理]
    └── styles.css                         [NEW]
```

---

## 3. Data Model

### 3.1 Consumed Schemas（从 Core OpenAPI 生成）

```ts
type GPUOccupancyStats = components['schemas']['GPUOccupancyStats']
type GPUInventoryRecord = components['schemas']['GPUInventoryRecord']
type GPUSchedulingQueue = components['schemas']['GPUSchedulingQueue']
```

### 3.2 前端状态

- KPI + inventory：TanStack Query `useQuery(['boss-gpu-occupancy'], ...)` + `useQuery(['boss-gpu-inventory'], ...)`
- 队列只读：`useQuery(['boss-gpu-queues'], ...)`

---

## 4. API Design（消费侧）

### 4.1 消费端点

| Method | Path | 用途 | 平台 scope |
|--------|------|------|------------|
| GET | `/gpu-inventory/occupancy` | 集群级 KPI | 平台角色 JWT |
| GET | `/gpu-inventory` | 节点/异常设备列表 | 平台角色 JWT |
| GET | `/gpu-scheduling/queues` | 调度队列只读全览 | 平台角色 JWT |

### 4.2 无新增 API

UI-only batch，不新增/修改任何 OpenAPI 端点。

---

## 5. Business Logic

### 5.1 页面行为

**GPU 资源池管理（`/ops/gpu-pool`）：**

1. **范围说明 Alert**（常驻 `theme="info"`）：
   > 本页展示全平台 GPU 资源池。租户内资源请前往 Console「GPU 算力管理」。

2. **KPI Row**（4 列）：
   - 总量 `total`
   - 已分配 `in_use`
   - 空闲 `available`
   - 异常设备 `fault`
   - P0 无「平均利用率」强制要求（可选第四卡）

3. **型号分布**：`by_gpu_type` → Progress 或 Chart

4. **Tabs**：
   - 节点：聚合 inventory by `node_name`，列：节点/GPU总数/已用/空闲/异常数
   - 异常设备：filter `status=fault|maintenance`，列：node_name/gpu_type/gpu_index/status
   - 调度队列（只读）：`GET /gpu-scheduling/queues`，列：name/workload_class/weight/reclaimable/范围；**无操作列**

5. **租户占用排行 Section**：
   - **P0 仅占位**：`Alert theme="info"`「租户维度排行待平台 API（P1）。当前版本不展示跨租户占用排名。」
   - **禁止**渲染空 Table 假数据
   - **禁止**前端循环调多租户 inventory 拼排行

6. **刷新按钮**：`Button variant="outline"` refetch

### 5.2 Edge Cases

- total=0 → KPI 为 0；`Empty`「集群暂无 GPU 设备」
- inventory fail 但 occupancy OK → KPI 可用；Tab `Alert warning`「设备列表加载失败」
- 403 → Alert「无权查看平台 GPU 资源池」
- 5xx → 页顶 `Alert theme="error"` + 重试

---

## 6. Error Handling

| Error | UI behavior | Components |
|-------|-------------|------------|
| 403 | Alert「无权查看平台 GPU 资源池」 | `Alert` |
| 5xx | 页顶 `Alert theme="error"` + 重试 | `Alert` + `Button` |
| partial-data | occupancy OK + inventory fail → 分区 `Alert warning` | `Alert` |
| empty-cluster | total=0 → KPI 0 + `Empty` | `Empty` |

---

## 7. Security

### 7.1 RBAC

- BOSS 用户需平台 GPU 读权限（SPEC 冻结角色，见 Core SPEC §7.1）
- P0 BOSS 只读，无写操作
- 前端不传 `tenant_id`，平台 scope 由 JWT 平台角色自动生效

---

## 8. Performance

- TanStack Query `staleTime: 30s`
- 无客户端重型计算
- inventory 聚合按 `node_name` 在前端完成（数据量小，50-200 卡）

---

## 9. Testing Strategy

### 9.1 Browser Verification

按 UX §6 三态验证：
- `/ops/gpu-pool`：loading / empty-cluster / error / forbidden / partial-data / rank-placeholder

### 9.2 Build Gates

```bash
cd repo/frontends/boss
pnpm type-check
pnpm lint
pnpm build
```

---

## 10. Implementation Plan

### 10.1 Phases

| Phase | 内容 | 依赖 |
|-------|------|------|
| **P0-① 项目骨架** | package.json + vite + tsconfig + pnpm workspace | 无 |
| **P0-② API 客户端** | coreClient.ts + `make gen-core-schema` | Core SPEC P0-① |
| **P0-③ 布局壳** | `__root.tsx` Layout + 侧栏 | P0-① |
| **P0-④ GPU 资源池页** | KPI + Tabs + 占位排行 | P0-②, P0-③ |
| **P0-⑤ Browser 验证** | 三态 + 占位 | P0-④ |

### 10.2 Issue Mapping

| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| #12: BOSS 前端骨架初始化 | §2.4 | medium | — |
| #13: BOSS GPU 资源池管理页 | UX §4, §5 | medium | Core #1, #12 |

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

- BOSS 前端是否与 Console 共享 pnpm workspace？需在 P0-① 确认
- BOSS 侧栏分组结构以 `boss-modules/ops/` 为准，需对齐模块主文档

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|-----------|
| BOSS 前端从零开始 | 工期风险 | 复用 Console 的 package.json + vite 配置 |
| 平台 scope JWT 角色 Core 尚未冻结 | 403 | 依赖 Core SPEC P0-① 冻结平台角色 |

### 11.3 Assumptions

- BOSS 视觉与 Console 共用 TDesign Token（`产品设计规范-TDesign组件与Token-2.0.md`）
- P1 平台 aggregate API 上线后，排行 Section 替换为 `Table`，本 UX 预留 Section 位置不变
- BOSS App Shell 侧栏分组以 `boss-modules/ops/` 为准

---

## Frozen Facts Table

| 类别 | 事实 | 来源 | 状态 |
|------|------|------|------|
| 待补 | `GPUSchedulingQueue` 类型 | `core-schema.d.ts` | **待 Core P0-①** |
| 不存在 | BOSS 前端骨架 | `repo/frontends/boss/` | **P0 新建** |
| 不存在 | BOSS API 客户端 | — | **P0 新建** |
| 不存在 | BOSS 路由壳 | — | **P0 新建** |
| 不存在 | GPU 资源池页 | — | **P0 新建** |
| 冻结路径 | `GET /gpu-inventory/occupancy` | v1.yaml L4041 | ✅ frozen（平台 scope 待 Core 确认） |
| 冻结路径 | `GET /gpu-inventory` | v1.yaml L4019 | ✅ frozen |
| 冻结路径 | `GET /gpu-scheduling/queues` | Core SPEC P0-① | **待补** |
