# UX: GPU 规格与配额管理

> Interaction specification derived from: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
> Part of ani-workflow artifact triad — next: `/prd-to-spec`
> Generated: 2026-08-12 | Product: BOSS + Console | UI stack: TDesign React + TanStack Router

---

## 1. Page Type

### 1.1 Classification

| Screen | Page type | In app shell? | Route |
|--------|-----------|---------------|-------|
| BOSS GPU 资源池（改版） | list + management | yes | `/ops/gpu-pool` |
| BOSS 规格管理面板 | list + CRUD dialog | yes（Tab 内嵌） | `/ops/gpu-pool` (Tab: 规格目录) |
| BOSS 配额分配弹框 | form (Drawer) | yes（弹框触发） | `/ops/gpu-pool` |
| BOSS 预留分配弹框 | form (Drawer) | yes（弹框触发） | `/ops/gpu-pool` |
| Console GPU 容器实例列表（扩展） | list | yes | `/compute/gpu-containers` |
| Console 创建 GPU 容器 Dialog（改版） | form (Dialog) | yes（弹框触发） | `/compute/gpu-containers` |
| Console GPU 调度队列设置（扩展） | list + CRUD dialog | yes | `/settings/gpu-queues` |

### 1.2 Pattern Reference

- **BOSS GPU 资源池**：复用现有 `gpu-pool.tsx` 页面结构（KPI Cards + Tabs + Table），扩展为管理面板
- **BOSS 弹框**：复用现有 `RecipientDrawer` 模式（Drawer + Form，480px）
- **Console 创建 Dialog**：复用现有 `-create-dialog.tsx`（Dialog + Form），移除旧字段改规格下拉
- **Console 调度队列页**：复用现有 `gpu-queues.tsx`（439 行），仅扩展 Queue status 展示
- **Console 列表页**：复用现有 `ConsolePage` + `ConsolePageHeader` + `ConsoleContentCard` 三件套

---

## 2. Information Architecture

### 2.1 Routes & Entry Points

| Route | Entry | Auth required |
|-------|-------|---------------|
| `/ops/gpu-pool` | 侧边栏 "资源池与基础设施" > "GPU 资源池管理" | yes（platform admin） |
| `/compute/gpu-containers` | 侧边栏 "算力与云资源" > "GPU 容器实例" | yes（tenant user） |
| `/settings/gpu-queues` | 侧边栏 "设置" > "GPU 调度队列" | yes（tenant admin/member） |

### 2.2 Navigation Relationship

**BOSS 侧**：`/ops/gpu-pool` 在侧边栏 "资源池与基础设施" SubMenu 下，是当前唯一的 ops 页面。改版后不新增路由，在页面内通过 Tabs 切换"节点聚合 | 异常设备 | 调度队列 | 规格目录"四个面板。

**Console 侧**：
- `/compute/gpu-containers` 在 "算力与云资源" SubMenu 下，与 `/compute/gpu`（GPU 管理页）平级
- `/settings/gpu-queues` 在 "设置" SubMenu 下，已有页面仅扩展

### 2.3 PRD Coverage Map

| PRD item | Screen / section |
|----------|------------------|
| US-001 (Core API 契约) | 非前端，不涉及 UX |
| US-002 (Ports 层) | 非前端，不涉及 UX |
| US-003 (Adapters 层) | 非前端，不涉及 UX |
| US-004 (Handler 层) | 非前端，不涉及 UX |
| US-005 (BOSS 前端) | BOSS GPU 资源池改版 + 规格管理 + 配额/预留弹框 |
| US-006 (Console 前端) | Console 创建 Dialog 改版 + 列表页配额/预留展示 + 队列页扩展 |
| US-007 (集成验收) | 非前端，不涉及 UX |
| FR-1~FR-3 (规格目录 CRUD) | BOSS 规格管理 Tab |
| FR-4 (节点标签只读) | BOSS 节点聚合 Tab + KPI |
| FR-5 (设备状态管理) | BOSS 异常设备 Tab |
| FR-6~FR-9 (配额/预留管理) | BOSS 配额/预留弹框 |
| FR-10~FR-11 (Console 自查) | Console 列表页配额/预留卡片 |
| FR-12~FR-13 (规格可用性) | Console 创建 Dialog 规格下拉 |
| FR-14 (spec_id 创建模式) | Console 创建 Dialog |
| FR-24 (Queue status) | Console 队列设置页扩展 |

---

## 3. User Flow

### 3.1 Primary Flow: BOSS 管理员分配配额 → 租户使用

```text
[BOSS 管理员]
  1. 进入 /ops/gpu-pool
  2. 查看节点聚合 Tab：KPI 展示总量/已分配/空闲/异常 + 整卡/vGPU 节点数
  3. 切换到"规格目录"Tab：查看已有规格列表
     3a. 点击"新建规格"→ Drawer 表单：填写 spec_id/gpu_type/gpu_mode/shares/mb_per_share
         → 校验 gpu_type 对齐节点标签 → 提交成功 → 刷新列表
     3b. 点击行操作"删除"→ Popconfirm 确认 → 有实例引用则提示禁止 → 无引用则删除成功
  4. 切换到"节点聚合"Tab：选中节点行 → 点击"配额分配"→ Drawer 表单
     4a. 选择租户（Select，搜索已有租户列表）
     4b. 填写配额上限 total（gpu_count 维度）
     4c. 填写预留额度 allocated_gpu_count（<= total）
     4d. 提交 → 成功 → Message.success
  5. 切换到"异常设备"Tab：选中异常设备行 → 点击"恢复空闲"→ 设备状态翻转

[Console 租户用户]
  6. 进入 /compute/gpu-containers
  7. 查看配额/预留卡片：展示本租户配额上限 + 预留额度 + 可用余量
  8. 点击"创建"→ Dialog 打开
     8a. 规格下拉：调用 GET /gpu-specs/availability，按四态标注/过滤
         - available → 可选，展示"剩余 N"
         - full → 置灰，标注"配额已满"
         - device_full → 置灰，标注"设备已满，暂无空闲"
         - unavailable → 置灰，标注"暂无匹配节点"
     8b. 选中规格后：本地重算 quota_remaining，刷新其他规格 available_count
     8c. 选择调度队列（Select，必选）
     8d. 填写实例名称
     8e. 点击"创建"→ POST /instances (spec_id) → 成功 → 跳转实例详情页
```

### 3.2 Secondary Flow: 调度队列管理

```text
[Console 租户管理员]
  1. 进入 /settings/gpu-queues
  2. 查看平台默认队列（只读）+ 我的队列（CRUD）
  3. 查看队列 status.allocated 展示（新增字段）+ state 徽标（open=绿色 / closed=灰色 / unknown=默认）
  4. 新建/编辑/删除队列（已有功能，不变）
```

### 3.3 Secondary Flow: BOSS 下调配额

```text
[BOSS 管理员]
  1. 进入 /ops/gpu-pool → 节点聚合 Tab
  2. 选中租户行 → 点击"配额分配"→ Drawer
  3. 填写更低的 total 或 allocated_gpu_count
  4. 服务端 clamp 到 used+reserved → 返回 tightened=true
  5. UI 展示提示"配额已下调至实际使用量（N 卡），差额无法回收"
```

---

## 4. Layout Regions

### 4.1 BOSS GPU 资源池页（改版）

```text
┌─────────────────────────────────────────────────────┐
│ [PageHeader: GPU 资源池管理 | 刷新按钮]                │
├─────────────────────────────────────────────────────┤
│ [Alert: 作用域提示（已有，保留）]                        │
├─────────────────────────────────────────────────────┤
│ [KPI Cards: 总量 | 已分配 | 空闲 | 异常 | 整卡节点 | vGPU节点] │
├─────────────────────────────────────────────────────┤
│ [型号分布 Card（已有，保留）]                            │
├─────────────────────────────────────────────────────┤
│ [Tabs: 节点聚合 | 异常设备 | 调度队列 | 规格目录]         │
│  ┌─────────────────────────────────────────────┐    │
│  │ [Tab 内容区 — 见下方各 Tab 详细布局]           │    │
│  └─────────────────────────────────────────────┘    │
├─────────────────────────────────────────────────────┤
│ [租户排行 Card（已有占位，保留）]                        │
└─────────────────────────────────────────────────────┘
```

#### Tab 1: 节点聚合（扩展配额分配入口）

| Region | Content | Notes |
|--------|---------|-------|
| toolbar | 按租户筛选 Select + "配额分配" Button(primary) | 新增租户筛选 + 配额入口 |
| table | node_name / gpu_mode / gpu_spec / total / in_use / available / fault / 操作 | 扩展 gpu_mode/gpu_spec 列 |
| table 操作列 | "配额分配" Button(text) | 点击打开 Drawer |

#### Tab 2: 异常设备（扩展设备状态管理）

| Region | Content | Notes |
|--------|---------|-------|
| toolbar | 按状态筛选 Select | 已有 |
| table | node_name / gpu_type / gpu_index / status(Tag) / 操作 | 已有，新增操作列 |
| table 操作列 | "标维护" Button(text) / "恢复空闲" Button(text) | status=maintenance 时显示"恢复空闲" |

#### Tab 3: 调度队列（已有只读，扩展 status.allocated + status.state 展示）

| Region | Content | Notes |
|--------|---------|-------|
| table | name / workload_class / weight / reclaimable / scope(Tag) / allocated(新增) / state(新增 Tag 徽标) | 新增 allocated 列展示 Queue status.allocated；新增 state 列展示 Queue status.state（open=success/Closed= default/unknown=default Tag） |

#### Tab 4: 规格目录（新增）

| Region | Content | Notes |
|--------|---------|-------|
| toolbar | "新建规格" Button(primary) | |
| table | spec_id / gpu_type / gpu_mode(Tag) / shares / mb_per_share / 操作 | |
| table 操作列 | "删除" Button(text, danger) + Popconfirm | 有实例引用时禁用 + Tooltip 提示 |

### 4.2 BOSS 配额分配 Drawer

```text
┌─────────────────────────────────┐
│ [Drawer Header: 配额分配]          │
├─────────────────────────────────┤
│ [Form]                          │
│  选择租户 *                      │
│  [Select: 搜索租户]               │
│                                 │
│  配额上限 (卡数) *                │
│  [InputNumber: min=0]            │
│  说明：resource_quota.total       │
│                                 │
│  预留额度 (卡数) *                │
│  [InputNumber: min=0]            │
│  说明：<= 配额上限，单维度不分 spec  │
│                                 │
│  [Alert: 下调提示（条件展示）]      │
├─────────────────────────────────┤
│ [Footer: 取消(default) | 确定(primary)] │
└─────────────────────────────────┘
```

| Region | Content | Notes |
|--------|---------|-------|
| Drawer | size="480px"，复用 RecipientDrawer 模式 | |
| Form | Form.useForm() + labelAlign="top" | |
| 租户选择 | Select, filterable, 必选 | 来源 GET /tenants（Services API） |
| 配额上限 | InputNumber, min=0, 必填 | PUT /admin/tenants/{id}/quota |
| 预留额度 | InputNumber, min=0, 必填, <= 配额上限 | PUT /admin/tenants/{id}/reservations |
| 下调提示 | Alert(warning) 当 used+reserved > 新值 | 条件展示 |

### 4.3 BOSS 规格管理 Drawer

```text
┌─────────────────────────────────┐
│ [Drawer Header: 新建规格]          │
├─────────────────────────────────┤
│ [Form]                          │
│  规格 ID *                       │
│  [Input: 格式 {gpu_type}-{mem}-{shares}] │
│                                 │
│  GPU 型号 *                      │
│  [Select: 从节点标签派生的 gpu_type 列表] │
│                                 │
│  GPU 模式 *                      │
│  [Radio.Group: 整卡(wholecard) / vGPU(vgpu)] │
│                                 │
│  切分数 (vGPU 模式时显示)          │
│  [InputNumber: min=1, 默认 1]    │
│  整卡时=1，vGPU 时从节点标签派生    │
│                                 │
│  每份显存 MB (vGPU 模式时显示)     │
│  [InputNumber: min=1]            │
│  从 gpu-sharing-spec 派生         │
├─────────────────────────────────┤
│ [Footer: 取消(default) | 确定(primary)] │
└─────────────────────────────────┘
```

### 4.4 Console GPU 容器实例列表页（扩展）

```text
┌─────────────────────────────────────────────────────┐
│ [ConsolePageHeader: GPU 容器实例 | 创建 Button]        │
├─────────────────────────────────────────────────────┤
│ [配额/预留卡片行（新增）]                               │
│ ┌─────────┐ ┌─────────┐ ┌─────────┐                  │
│ │ 配额上限  │ │ 预留额度  │ │ 可用余量  │                  │
│ │  N 卡    │ │  N 卡    │ │  N 卡   │                  │
│ └─────────┘ └─────────┘ └─────────┘                  │
├─────────────────────────────────────────────────────┤
│ [ConsoleContentCard]                                 │
│  [Filter: 名称 Input | 状态 Select]                   │
│  [Table: 名称 | 状态 | 规格 | GPU数量 | 调度队列 | 操作]   │
│  [Pagination]                                        │
└─────────────────────────────────────────────────────┘
```

| Region | Content | Notes |
|--------|---------|-------|
| 配额卡片行 | 3 个 MetricCard：配额上限(total) / 预留额度(allocated) / 可用余量(allocated-used-reserved) | 新增，调用 GET /quotas/me + GET /reservations/me |
| 表格 | 扩展列：规格(spec_id 替代 model) | 已有 Table 结构不变，列内容改 |

### 4.5 Console 创建 GPU 容器 Dialog（改版）

```text
┌─────────────────────────────────────┐
│ [Dialog Header: 创建 GPU 容器 (520w)]  │
├─────────────────────────────────────┤
│ [Form]                              │
│  名称 *                              │
│  [Input]                            │
│                                     │
│  GPU 规格 *                          │
│  [Select: 规格下拉，filterable]       │
│  ┌────────────────────────────────┐ │
│  │ NVIDIA-A100-SXM4-80GB  剩余 4  │ │
│  │ NVIDIA-A100-SXM4-20GB  剩余 8  │ │
│  │ RTX4090-24GB-2  配额已满(置灰) │ │
│  │ RTX4090-24GB-1  设备已满(置灰) │ │
│  │ Ascend910-32GB  暂无匹配(置灰) │ │
│  └────────────────────────────────┘ │
│                                     │
│  调度队列 *                          │
│  [Select: 队列列表，必选]              │
│                                     │
│  [Alert: 规格说明（选中后展示）]       │
│  显示选中规格的 gpu_type/mode/shares  │
├─────────────────────────────────────┤
│ [Footer: 取消(default) | 创建(primary, loading)] │
└─────────────────────────────────────┘
```

| Region | Content | Notes |
|--------|---------|-------|
| Dialog | width=520，复用现有 Dialog 模式 | |
| 规格下拉 | Select, filterable, 必选 | 调用 GET /gpu-specs/availability |
| 下拉项展示 | 每项 label: spec_id + 状态标注 + 剩余数 | 四态标注/过滤 |
| 选中后重算 | 本地 quota_remaining - 已选 gpu_count | 避免跨规格超选 |
| 队列下拉 | Select, 必选 | 调用 GET /gpu-scheduling/queues |
| 规格说明 | Alert(info) 展示选中规格详情 | 选中后条件展示 |

### 4.6 Console GPU 调度队列设置页（扩展）

```text
┌─────────────────────────────────────────────────────┐
│ [ConsolePageHeader: GPU 调度队列]                      │
├─────────────────────────────────────────────────────┤
│ [ConsoleContentCard: 平台默认队列（只读）]              │
│  [Table: 名称 | 类型 | 权重 | 可回收 | 已分配(新增) | scope]│
├─────────────────────────────────────────────────────┤
│ [ConsoleContentCard: 我的队列]                         │
│  [Toolbar: 新建队列 Button]                            │
│  [Table: 名称 | 类型 | 权重 | 可回收 | 已分配(新增) | 操作] │
└─────────────────────────────────────────────────────┘
```

| Region | Content | Notes |
|--------|---------|-------|
| 已分配列 | 新增列，展示 Queue status.allocated (map[string]string) | 格式化展示 key: value |
| 状态列 | 新增列，展示 Queue status.state（open/closed/unknown）徽标 | open=success 绿色 / closed=default 灰色 / unknown=default 默认 |
| 其余 | 已有功能不变 | 仅扩展展示 |

---

## 5. Component Mapping

### 5.1 BOSS 组件映射

| UI element | TDesign component | Props / variant | Data source |
|------------|-------------------|-----------------|-------------|
| KPI 卡片 | `Card` + `Statistic` | `Row gutter={16}` + `Col span={4}` (6 卡) | GET /gpu-inventory/occupancy |
| 页面标签页 | `Tabs` + `Tabs.TabPanel` | 4 个 Tab | — |
| 节点聚合表格 | `Table` | columns: node_name/gpu_mode/gpu_spec/total/in_use/available/fault/op | GET /gpu-inventory |
| 规格目录表格 | `Table` | columns: spec_id/gpu_type/gpu_mode/shares/mb_per_share/op | GET /gpu-specs |
| 异常设备表格 | `Table` | columns: node_name/gpu_type/gpu_index/status/op | GET /gpu-inventory (已有) |
| 调度队列表格 | `Table` | columns: name/workload_class/weight/reclaimable/scope/allocated/state | GET /gpu-scheduling/queues |
| 配额分配弹框 | `Drawer` | `size="480px"`，header="配额分配" | — |
| 规格管理弹框 | `Drawer` | `size="480px"`，header="新建规格" | — |
| 租户选择 | `Select` | `filterable`, `clearable`, placeholder="搜索租户" | GET /tenants (Services API) |
| 配额上限输入 | `InputNumber` | `min=0`, `step=1` | 用户输入 |
| 预留额度输入 | `InputNumber` | `min=0`, `step=1` | 用户输入 |
| GPU 型号选择 | `Select` | placeholder="从节点标签派生" | GET /gpu-inventory (派生 gpu_type) |
| GPU 模式选择 | `Radio.Group` | options: [整卡, vGPU] | 静态 |
| 切分数输入 | `InputNumber` | `min=1`, vGPU 模式时显示 | 用户输入 |
| 新建规格按钮 | `Button` | `theme="primary"`, `AddIcon` | — |
| 配额分配按钮 | `Button` | `variant="text"`, 行内操作 | — |
| 删除规格 | `Button` | `variant="text"`, `theme="danger"` | + `Popconfirm` |
| 设备状态翻转 | `Button` | `variant="text"` | 行内操作 |
| gpu_mode Tag | `Tag` | wholecard=`theme="default"`, vgpu=`theme="primary"` | — |
| status Tag | `Tag` | available=`success`, maintenance=`warning`, fault=`danger` | — |
| queue state Tag | `Tag` | open=`success`, closed=`default`, unknown=`default` | Queue status.state |
| 下调提示 | `Alert` | `theme="warning"` | 条件展示 |
| 表单 | `Form` + `Form.FormItem` | `labelAlign="top"`, `resetType="empty"` | — |
| 提交按钮 | `Button` | `theme="primary"`, `loading={mutation.isPending}` | — |
| 取消按钮 | `Button` | `theme="default"` | — |
| 成功提示 | `MessagePlugin` | `.success` | — |
| 错误提示 | `MessagePlugin` | `.error` | — |
| 删除确认 | `Popconfirm` | `theme="danger"`, content="确定删除此规格？" | — |
| 刷新按钮 | `Button` | `variant="outline"`, `RefreshIcon` | 已有 |
| 空状态 | `Empty` | description="暂无规格" / "暂无异常设备" | — |
| 加载状态 | `Skeleton` | KPI 卡片骨架 | — |

### 5.2 Console 组件映射

| UI element | TDesign component | Props / variant | Data source |
|------------|-------------------|-----------------|-------------|
| 页面壳 | `ConsolePage` | gap=16 | — |
| 页面头 | `ConsolePageHeader` | title="GPU 容器实例", actions=创建 Button | — |
| 内容卡片 | `ConsoleContentCard` | bordered | — |
| 配额卡片 | `Card` + `Statistic` | 3 个：配额上限/预留额度/可用余量 | GET /quotas/me + GET /reservations/me |
| 实例列表表格 | `Table` | columns: 名称/状态/规格/GPU数量/调度队列/操作 | GET /instances?kind=gpu_container |
| 创建 Dialog | `Dialog` | `width=520`, footer=取消+创建 | — |
| 表单 | `Form` + `Form.FormItem` | `labelWidth=100`, `labelAlign="right"` | — |
| 名称输入 | `Input` | required, placeholder="请输入实例名称" | 用户输入 |
| 规格下拉 | `Select` | `filterable`, required, placeholder="选择 GPU 规格" | GET /gpu-specs/availability |
| 规格项标注 | `Tag` | available=`success`+剩余N, full=`default`+配额已满, device_full=`warning`+设备已满, unavailable=`default`+暂无匹配 | — |
| 队列下拉 | `Select` | required, placeholder="选择调度队列" | GET /gpu-scheduling/queues |
| 规格说明 | `Alert` | `theme="info"`, 选中后展示 | — |
| 创建按钮 | `Button` | `theme="primary"`, `loading={mutation.isPending}` | — |
| 取消按钮 | `Button` | `theme="default"` | — |
| 状态 Tag | `Tag` | 复用现有 STATE_THEME 映射 | — |
| 名称筛选 | `Input` | width=220, placeholder="按名称搜索" | 已有 |
| 状态筛选 | `Select` | width=180, clearable | 已有 |
| 空状态 | `Empty` | description="暂无 GPU 容器实例" | 已有 |
| 加载状态 | `Skeleton` | gradient animation | 已有 |
| 错误横幅 | `Alert` | `theme="error"` + retry Button | 已有 |
| 成功提示 | `MessagePlugin` | `.success("GPU 容器创建已提交")` | 已有 |
| 错误提示 | `MessagePlugin` | `.error` 含 API message | 已有 |
| 行内详情 | `Link` + `Button` | `variant="text"` "查看详情" | 已有 |

### 5.3 Console 队列页新增组件

| UI element | TDesign component | Props / variant | Data source |
|------------|-------------------|-----------------|-------------|
| 已分配列 | `Table` column | colKey="allocated", cell 格式化 | GET /gpu-scheduling/queues (status.allocated) |
| allocated 展示 | `Tag` | 多个 key:value Tag 展示 | — |
| 状态列 | `Table` column | colKey="state", cell 格式化为 Tag 徽标 | GET /gpu-scheduling/queues (status.state) |
| state 徽标 | `Tag` | open=`success` 绿色, closed=`default` 灰色, unknown=`default` 默认 | — |

---

## 6. State Design

### 6.1 BOSS GPU 资源池页

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| idle | 初始加载成功 | KPI 展示数值，表格展示数据 | Card+Statistic, Table |
| loading | 首次 fetch / 刷新中 | KPI 用 Skeleton，表格用 Table loading | Skeleton, Table loading |
| empty | 集群无 GPU 节点 | KPI 归零 + Empty 占位 | Empty |
| error | API 失败 | Alert(error) + 重试按钮 | Alert, Button |
| partial | inventory 或 occupancy 单侧失败 | Alert(warning) 提示部分数据 | Alert |
| forbidden | 403 | Alert(error) 提示无权限 | Alert |

### 6.2 BOSS 规格目录 Tab

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| idle | 加载成功 | 表格展示规格列表 | Table |
| loading | fetch 中 | Table loading | Table loading |
| empty | 无规格 | Empty + "新建规格"引导 | Empty, Button |
| error | API 失败 | Alert(error) + 重试 | Alert, Button |
| delete-blocked | 有实例引用 | 删除按钮禁用 + Tooltip "有运行中实例引用" | Button(disabled), Tooltip |
| delete-confirm | 点击删除 | Popconfirm 确认 | Popconfirm |
| submitting | 新建规格提交中 | 提交按钮 loading | Button loading |

### 6.3 BOSS 配额分配 Drawer

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| idle | Drawer 打开 | 表单空白可填写 | Drawer, Form |
| loading | 提交中 | 确定按钮 loading + 表单禁用 | Button loading |
| success | 提交成功 | Message.success + Drawer 关闭 + 刷新列表 | MessagePlugin |
| error | 提交失败 | Message.error + 保留 Drawer | MessagePlugin |
| validation-error | 预留 > 配额上限 | 表单内联错误 + 不提交 | FormItem error |
| tightened | 下调被 clamp | Alert(warning) 提示实际值 | Alert |
| tenant-loading | 租户列表 fetch 中 | Select loading | Select loading |

### 6.4 Console 创建 GPU 容器 Dialog

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| idle | Dialog 打开 | 规格下拉 fetch 中 + 队列下拉 fetch 中 | Select loading |
| loading | 规格可用性 fetch 中 | 规格下拉 loading | Select loading |
| spec-loaded | 规格列表加载完成 | 下拉展示四态标注项 | Select, Tag |
| empty-spec | 无可用规格 | 下拉空 + Alert(info)"暂无可用规格" | Alert |
| spec-error | 可用性查询失败 | 下拉空 + Alert(error) + 重试 | Alert, Button |
| selected | 选中规格 | 本地重算 quota_remaining + 刷新其他项 + Alert 展示规格详情 | Alert, Select |
| submitting | 创建提交中 | 创建按钮 loading + 表单禁用 | Button loading |
| success | 创建成功 | Message.success + Dialog 关闭 + 跳转详情页 | MessagePlugin, navigate |
| error-409 | QUOTA_EXCEEDED | Message.error "配额不足" | MessagePlugin |
| error-409-r | RESERVED_INSUFFICIENT | Message.error "预留额度不足" | MessagePlugin |
| error-other | 其他失败 | Message.error + API message | MessagePlugin |
| queue-required | 未选队列 | FormItem 内联错误 "请选择调度队列" | FormItem error |

### 6.5 Console 列表页配额卡片

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| idle | 加载成功 | 展示三个数值 | Card+Statistic |
| loading | fetch 中 | Skeleton 骨架 | Skeleton |
| error | fetch 失败 | 数值展示"—" + 不阻断列表 | Statistic |
| empty | 无配额数据 | 数值展示 0 | Statistic |

### 6.6 Console 队列页扩展

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| idle | 加载成功 | allocated 列展示 + state 徽标列展示 | Table, Tag |
| empty-allocated | allocated 为空 | 展示"—" | — |
| empty-state | state 为空 | 徽标展示"—" | — |
| loading | fetch 中 | Table loading（已有） | Table loading |

---

## 7. Copy & Feedback

### 7.1 Labels & Buttons

| Element | Copy (zh-CN) | Notes |
|---------|--------------|-------|
| BOSS 页面标题 | GPU 资源池管理 | 已有 |
| BOSS KPI | 总量 / 已分配 / 空闲 / 异常 / 整卡节点 / vGPU 节点 | 新增后两个 |
| BOSS Tab 1 | 节点聚合 | 已有 |
| BOSS Tab 2 | 异常设备 | 已有 |
| BOSS Tab 3 | 调度队列 | 已有 |
| BOSS Tab 4 | 规格目录 | 新增 |
| BOSS 规格新建 | 新建规格 | Drawer header |
| BOSS 配额分配 | 配额分配 | Drawer header + 行操作 |
| BOSS 设备维护 | 标维护 / 恢复空闲 | 行操作按钮 |
| BOSS 删除规格 | 删除 | 行操作 + Popconfirm |
| Console 页面标题 | GPU 容器实例 | 已有 |
| Console 创建按钮 | 创建 | 已有 |
| Console Dialog 标题 | 创建 GPU 容器 | 已有 |
| Console 规格字段 | GPU 规格 | 新增 |
| Console 队列字段 | 调度队列 | 已有改必选 |
| Console 配额卡片 | 配额上限 / 预留额度 / 可用余量 | 新增 |
| Console 队列页标题 | GPU 调度队列 | 已有 |
| Console 队列已分配列 | 已分配 | 新增 |
| Console 队列状态列 | 状态 | 新增 |
| Console 队列状态-open | 开放 | 徽标 success |
| Console 队列状态-closed | 已关闭 | 徽标 default |
| Console 队列状态-unknown | 未知 | 徽标 default |

### 7.2 Messages

| Scenario | Type | Copy |
|----------|------|------|
| 规格创建成功 | `Message.success` | 规格创建成功 |
| 规格删除成功 | `Message.success` | 规格已删除 |
| 规格删除失败（有引用） | `Message.error` | 该规格有运行中实例引用，无法删除 |
| 配额分配成功 | `Message.success` | 配额分配成功 |
| 配额分配成功（下调 clamp） | `Message.success` | 配额已下调至 N 卡（实际使用量），差额无法回收 |
| 预留分配成功 | `Message.success` | 预留额度设置成功 |
| 设备标维护成功 | `Message.success` | 设备已标记维护 |
| 设备恢复空闲成功 | `Message.success` | 设备已恢复空闲 |
| Console 创建成功 | `Message.success` | GPU 容器创建已提交 |
| Console 配额不足 | `Message.error` | 配额不足，无法创建 |
| Console 预留不足 | `Message.error` | 预留额度不足，无法创建 |
| Console 规格不可用 | `Message.error` | 该规格当前不可用 |
| 网络错误 | `Message.error` | 网络错误，请重试 |
| 权限不足 | `Message.error` | 权限不足，请联系管理员 |
| 表单校验失败 | FormItem inline | 请填写必填项 / 预留额度不能超过配额上限 |

### 7.3 Status Tag Copy

| Status | Tag copy | Theme |
|--------|----------|-------|
| available | 剩余 N | `success` |
| full | 配额已满 | `default` |
| device_full | 设备已满 | `warning` |
| unavailable | 暂无匹配节点 | `default` |
| wholecard | 整卡 | `default` |
| vgpu | vGPU | `primary` |
| maintenance | 维护中 | `warning` |
| fault | 异常 | `danger` |
| available(device) | 空闲 | `success` |

---

## 8. Boundaries & Non-Goals

### 8.1 In Scope (UX)

- BOSS GPU 资源池页改版：KPI 扩展 + 4 Tab（节点聚合/异常设备/调度队列/规格目录）
- BOSS 配额分配 Drawer + 预留分配 Drawer（统一在一个 Drawer 内）
- BOSS 规格管理 Drawer（新建规格）
- BOSS 设备状态翻转（标维护/恢复空闲）
- Console 创建 Dialog 改版（规格下拉四态 + 队列必选）
- Console 列表页配额/预留卡片
- Console 队列页 allocated 列扩展 + state 徽标列扩展

### 8.2 Explicitly Out of Scope

- 配额策略页（套餐 CRUD）—— 后续迭代
- 待审批配额页面 —— 后续迭代
- 批量分配空闲卡 —— 本次单卡分配
- 节点标签管理 UI —— P0 不做节点标签写入接口，只读展示
- 算力切分 UI —— 字段预留，不实现
- VPC/子网选择 UI —— adapter 兜底，前端不展示
- 多集群切换 UI —— 本期单集群假设
- Console 创建 Dialog 旧模式切换 —— 仅规格模式，不暴露旧字段
- 独立的规格管理路由 /ops/gpu-specs —— 统一在资源池页 Tab 内
- 新建队列配置路由 —— 复用 /settings/gpu-queues

### 8.3 Open UX Questions

- BOSS 规格管理的 GPU 型号下拉源数据：是直接从 `GET /gpu-inventory` 派生已有节点的 gpu_type，还是需要新的"列出集群可用 gpu_type"接口？（假设从 inventory 派生）
- Console 规格下拉的"剩余 N"数字在用户选规格后重算时，是否需要动画过渡？（假设无动画，直接刷新）
- BOSS 配额/预留分配是否需要合为一个 Drawer 两个步骤，还是两个独立 Drawer？（假设合为一个 Drawer 两个 InputNumber 字段）

### 8.4 Assumptions

- 复用现有 BOSS `_authenticated` layout shell（Layout.Header + Layout.Aside + Layout.Content）
- 复用现有 Console `ConsolePage` + `ConsolePageHeader` + `ConsoleContentCard` 三件套
- 复用现有 `coreApi`（openapi-fetch）+ `@tanstack/react-query` 数据获取模式
- 复用现有 `coreApi` for BOSS（已在 `coreClient.ts`）+ Services API for 租户列表
- 复用现有 Drawer + Form 模式（RecipientDrawer 参考）
- 复用现有 Dialog + Form 模式（-create-dialog 参考）
- 规格下拉四态标注用 Tag + 文字，不用独立图标
- KPI 卡片样式复用现有 Card +Statistic，不引入新组件
- Console 创建 Dialog 旧字段（allocation_mode/model/gpu_count）从 UI 移除，API 兼容但前端不暴露
