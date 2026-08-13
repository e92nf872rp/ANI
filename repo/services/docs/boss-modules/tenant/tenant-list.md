# 租户列表

## 页面定位

`租户列表` 是 BOSS **租户与客户管理** 域下的 **平台租户 CRUD 与生命周期** 入口：跨租户筛选、新建客户、停用/恢复、查看配额与活跃摘要，并跳转配额策略与管理员重置。

本页属于 **Core / Tenants** 视角下的 **平台 RBAC** 页面。产品规划见 ANI-02 §2.4 `/api/v1/tenants/`；当前 `v1.yaml` **未声明** `/tenants*` paths — 正文只能写页面目标与 **TODO-YAML**，**不得** 写成已实现。

Console [`tenant-management.md`](../../console-modules/tenant/tenant-management.md) 承接 `Services /api/v1/svc/tenant/members` 等 **租户内** 路径；本页 **不得** 把 `inviteTenantMember` 写成 BOSS「创建租户」正式 API。

## 文档管理规则

- 本文是 `租户列表` 的主维护源；页面定位、参数口径、字段映射和验收标准以本文为准
- `prototypes/ani-services-prototype-boss.html` 只保留摘要与详文入口
- [`prd-boss-tenant-list.md`](../../tasks/modules/prd/boss/tenant/prd-boss-tenant-list.md) 与 [`spec-boss-tenant-list.md`](../../tasks/modules/spec/boss/tenant/spec-boss-tenant-list.md) 为辅助材料，不替代本文
- 如本文与辅助材料冲突，先对照 `v1.yaml` / `services/v1.yaml`，再统一回写 PRD/SPEC
- 流程：ANI-14 Phase 1 + [`module-delivery-workflow.md`](../governance/module-delivery-workflow.md) + [`boss-full-depth-checklist.md`](../governance/boss-full-depth-checklist.md)

## Core 层要求

- 平台租户资源归属 **Core**（规划 `/api/v1/tenants*`）
- Core tenants CRUD **ADDED-TO-YAML**（`listTenants`/`createTenant`/`getTenant`/`updateTenant`）
- **不得** 把 `Services GET/POST /api/v1/svc/tenant/members` 写成 BOSS 平台租户 CRUD
- **不得** 自造 `/api/v1/boss/tenants` 或 `/api/v1/svc/tenants`
- 平台写操作 POST/PUT 须 `idempotency_key` + 平台 RBAC
- 平台 RBAC 鉴权；**不得** 信任 body/query 中未授权的 `tenant_id` 越权
- 统一错误结构：`{"code":"UPPER_SNAKE","message":"...","request_id":"..."}`
- `422 PreconditionFailed` 仅 YAML 已声明 operation 可写冻结（§2.10）
- 禁止自造 `TenantRecord` / `TenantListResponse` schema 为已冻结
- OpenAPI 已声明 ≠ handler 已实现

## 页面职责

- 提供 **全平台** 租户 list、搜索、筛选、排序与分页（**ADDED-TO-YAML**）
- 提供 **新建租户向导**（基础信息 → 配额摘要 → 初始 tenant-admin → SSO 选项 → 确认）
- 提供行内操作：查看详情、编辑配额、停用、恢复、重置管理员
- 展示配额与活跃摘要列（依赖 tenants 内嵌字段或 quota 子资源 · 待 YAML）
- 为 [`tenant-quota-policy.md`](tenant-quota-policy.md)、[`tenant-admin.md`](tenant-admin.md)、[`tenant-usage-billing.md`](tenant-usage-billing.md) 提供 `tenant_id` 跳转上下文

## 页面结构

- 首屏至少包含：`筛选与搜索区`、`租户表格`、`新建向导入口`、`边界说明`
- 表格与筛选 **共享同一查询上下文**（status、关键词、分页 cursor）
- 无数据态、无权限态、平台 API 未就绪态、写操作失败态须可区分

```text
租户列表
├── 筛选与搜索
│   ├── status（active / suspended / deleted）
│   ├── 名称 / slug 关键词
│   └── last_active_at 时间范围
├── 租户表格（name / status / quota 摘要 / 活跃 / 操作）
├── 新建租户向导
│   ├── 基础信息（name / display_name / slug）
│   ├── 配额设置（内嵌或跳转 quota-policy）
│   ├── 初始 tenant-admin（邮箱）
│   ├── SSO 选项（Phase 2）
│   └── 确认创建（POST + idempotency_key）
└── 行内操作
    ├── 查看详情 → GET by id
    ├── 编辑配额 → tenant-quota-policy
    ├── 停用 / 恢复 → PUT status
    └── 重置管理员 → tenant-admin
```

## 数据来源与分层约束

### 数据来源划分

| 层 | 路径 / 来源 | 本页用法 |
|---|---|---|
| Core | `/api/v1/tenants*` **TODO-YAML** | BOSS 正式租户 CRUD |
| Core | quota 子资源 **TODO-YAML** | 表格配额摘要列 |
| Core | 平台 metering aggregate **TODO-YAML** | 用量摘要列（可选） |
| Services | `/api/v1/svc/tenant/members` 等 | **Console 专用**；BOSS 不用于创建租户 |
| Services | inference/KB 计数 aggregate **TODO-YAML** | 业务资源计数列 |

### 关键边界

- `listTenantMembers` / `inviteTenantMember` 为 **租户 JWT 上下文** 成员管理，**不能** 替代 `POST /api/v1/tenants`
- 用量摘要 **不得** 用逐租户切换 JWT 轮询 `/metering/usage` 作为 BOSS 正式方案
- `deleted` 租户的生命周期与恢复策略以 YAML 合入后为准；当前为产品目标 enum
- SSO 开通与 Console `PUT /svc/tenant/sso` 联动 — Phase 2；BOSS 向导仅收集选项

## 页面区块与数据来源映射

| 区块 | 主要来源层 | 数据口径说明 | 跳转目标 |
|---|---|---|---|
| 筛选区 | tenants list API **待 YAML** | status / q / time range | 刷新表格 |
| 租户表格 | GET /tenants **待 YAML** | 分页 `cursor` + `total` | 行内操作 |
| 配额摘要列 | quota 内嵌或子资源 **待 YAML** | max/used GPU 等 | tenant-quota-policy |
| 用量摘要列 | metering platform **待 YAML** | 可选轻量 KPI | tenant-usage-billing |
| 新建向导 | POST /tenants **待 YAML** | `idempotency_key` 必填 | 详情页 |
| 停用/恢复 | PUT /tenants/{id} **待 YAML** | status 变更 | — |
| 边界说明 | 规划项 | Services members ≠ BOSS CRUD | tenant-management |

## BOSS 与 Console 分工

| 场景 | BOSS 租户列表 | Console 租户管理 |
|---|---|---|
| 创建客户租户 | ✅ 新建向导（待 YAML） | ❌ |
| 停用/恢复租户 | ✅ PUT status（待 YAML） | ❌ |
| 跨租户 list | ✅ | ❌ |
| 重置 tenant-admin | ✅ → tenant-admin | ❌ |
| 邀请普通成员 | ❌ | ✅ POST members |
| SSO / Webhook | 开通选项（Phase 2） | ✅ `/svc/tenant/*` |
| RBAC | 平台 tenant 治理 | 租户 JWT |

## 当前冻结事实

| 方法 | 路径 | operationId | 说明 |
|---|---|---|---|
| GET | `/api/v1/tenants` | `listTenants` | 平台 list · **ADDED-TO-YAML** |
| POST | `/api/v1/tenants` | `createTenant` | 创建 · **ADDED-TO-YAML** |
| GET | `/api/v1/tenants/{tenant_id}` | `getTenant` | 详情 · **ADDED-TO-YAML** |
| PUT | `/api/v1/tenants/{tenant_id}` | `updateTenant` | 更新 · **ADDED-TO-YAML** |

| Services 参考（**非 BOSS 租户 CRUD**） | 路径 | operationId |
|---|---|---|
| GET | `/api/v1/svc/tenant/members` | `listTenantMembers` |
| POST | `/api/v1/svc/tenant/members` | `inviteTenantMember` |

| 能力 | 状态 |
|---|---|
| Core tenants CRUD | **ADDED-TO-YAML** P1 |
| 平台 RBAC scope 命名 | 待 Core 确认 |
| inference/kb 跨租户计数 | **ADDED-TO-YAML**（Tenant schema 内嵌字段） |

### 建议 TODO-YAML（非冻结）

<!-- ADDED-TO-YAML: GET/POST /api/v1/tenants, GET/PUT /api/v1/tenants/{tenant_id} -->

| 建议能力 | 建议路径 | 归属 |
|---|---|---|
| 列出租户 | `GET /api/v1/tenants` | Core |
| 创建租户 | `POST /api/v1/tenants` | Core |
| 查询租户 | `GET /api/v1/tenants/{tenant_id}` | Core |
| 更新状态/资料 | `PUT /api/v1/tenants/{tenant_id}` | Core |

## 字段级定义

### 查询字段（tenants list · **ADDED-TO-YAML**）

| 字段 | 来源 | 必填 | 说明 |
|---|---|---|---|
| `status` | query | ❌ | active / suspended / deleted |
| `q` | query | ❌ | name / slug 关键词 |
| `last_active_after` | query | ❌ | date-time |
| `limit` | query | ❌ | 1–100 |
| `cursor` | query | ❌ | 分页游标 |

### 请求字段 — 创建租户（**ADDED-TO-YAML**）

| 字段 | 必填 | 说明 |
|---|---|---|
| `idempotency_key` | ✅ | 幂等 |
| `name` / `display_name` | ✅ | 标识与展示 |
| `slug` | ✅ | URL/登录 slug；须唯一 |
| `initial_admin_email` | ✅ | 初始 tenant-admin |
| `quota` | 可选 | 内嵌配额对象或引用 |
| `sso_enabled` | ❌ | Phase 2 |

### 返回字段（页面目标 · **ADDED-TO-YAML**）

| 字段 | 说明 | 展示 |
|---|---|---|
| `id` / `tenant_id` | 租户 ID | 表格主键 |
| `name` / `display_name` | 标识 | 主列 |
| `slug` | slug | 等宽字体 |
| `status` | active / suspended / deleted | 标签 |
| `tenant_admin_email` | 主管理员 | 跳转 tenant-admin |
| `max_gpu_count` / `used_gpu_count` | GPU 配额摘要 | 已用/上限 |
| `max_cpu` / `max_memory_gb` | CPU/内存配额 | 数字 |
| `inference_service_count` | 推理服务数 | aggregate **待 YAML** |
| `kb_count` | 知识库数 | aggregate **待 YAML** |
| `last_active_at` | 最近活跃 | 相对时间 |
| `created_at` | 开通时间 | 绝对时间 |

### 展示字段（UI 计算 · 非 API 字段）

| 字段 | 说明 |
|---|---|
| `gpu_utilization_pct` | used/max × 100 |
| `status_badge_color` | active 绿 / suspended 黄 / deleted 灰 |
| `tenant_count` | 筛选结果 total |

## 字段展示规则

| 场景 | 展示规则 | 说明 |
|---|---|---|
| API 未就绪 | 表格占位 + TODO-YAML 横幅 + 禁用新建 | **不** 伪造租户数据 |
| 正常态（YAML 后） | 筛选 + 表格 + 分页 | 共享查询上下文 |
| 无租户 | 「暂无租户，点击新建」 | 引导向导 |
| `suspended` | 黄色标签；行内可恢复 | — |
| `deleted` | 灰显；仅查看/审计 | 写操作 disabled |
| 创建成功 | 跳转详情或刷新 list | 201 |
| 写失败 | 向导内错误 + request_id | 保留输入 |
| 无权限态 | 403 | 不渲染假数据 |
| slug 冲突 | 409 提示 | 创建时 |

## 字段口径与单位

| 字段 | 口径 | 单位/格式 |
|---|---|---|
| `status` | 产品三态（待 YAML enum） | string |
| `max_gpu_count` / `used_gpu_count` | 整数 | count |
| `max_memory_gb` | 内存配额 | GiB |
| `last_active_at` | ISO 8601 | date-time |
| `gpu_utilization_pct` | UI 计算 | % |

## 状态与能力口径

### status（产品目标 · **待 YAML**）

| 状态 | 含义 | 允许操作 |
|---|---|---|
| `active` | 正常使用 | 查看、改配额、停用、重置管理员 |
| `suspended` | 平台停用 | 查看、改配额、恢复；租户侧不可新建资源 |
| `deleted` | 软删除/归档 | 仅查看审计；恢复待产品确认 |

本页 **不承担** Console 成员邀请；成员操作跳转 Console 或只读参考 `listTenantMembers`（须租户 JWT，BOSS 不默认调用）。

## 创建前置条件

| 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
|---|---|---|
| 平台 tenant 读 RBAC | 已授权（**TODO-YAML** `/tenants*` scope 待 Core 定义） | `403 FORBIDDEN` |
| 平台 tenant 写 RBAC（新建/停用） | 已授权（**TODO-YAML**） | `403 FORBIDDEN` |
| 用户已认证 | 平台登录态 | `401 UNAUTHORIZED` |
| 创建 POST | `idempotency_key` + slug 唯一 | `400` / `409 CONFLICT` |
| 创建 POST | `initial_admin_email` 合法且未被占用 | `409` / `422`（**待 YAML**） |
| 停用 | 当前 status=active | `422`（**待 YAML**） |
| 恢复 | 当前 status=suspended | `422`（**待 YAML**） |

## 操作可用性矩阵

| 操作 | 平台只读 | 平台运营 | 平台管理员 | 说明 |
|---|---|---|---|---|
| 查看租户 list | ✅（待 YAML） | ✅ | ✅ | GET |
| 搜索/筛选 | ✅ | ✅ | ✅ | query |
| 新建租户 | ❌ | ✅ | ✅ | POST + 向导 |
| 查看详情 | ✅ | ✅ | ✅ | GET by id |
| 编辑配额 | ❌ | ✅ | ✅ | → quota-policy |
| 停用 | ❌ | ✅ | ✅ | PUT |
| 恢复 | ❌ | ✅ | ✅ | PUT |
| 重置管理员 | ❌ | ✅ | ✅ | → tenant-admin |
| 硬删除 | ❌ | 待产品 | 待产品 | 默认无 UI |

## 删除前置校验与当前契约边界

默认 **软删除**（`status=deleted`）via PUT，**无**物理 DELETE UI。

若 YAML 合入 DELETE：

| 校验项 | 要求 | 失败响应 |
|---|---|---|
| tenant 无活跃 billable 资源 | 产品规则 | `422`（**待 YAML**） |
| write RBAC | 平台管理员 | `403` |

合入前 **N/A**。

## 接口冻结规则

### Core `/api/v1/tenants*`（**ADDED-TO-YAML**）

<!-- TODO-YAML: GET/POST /api/v1/tenants, GET/PUT /api/v1/tenants/{tenant_id} -->

- 路径前缀须 **Core** `/api/v1/*`
- **不得** 使用 `/api/v1/boss/tenants` 或 Services 前缀
- 合入前 **不得** 写入「已冻结路径」表

### `POST /api/v1/svc/tenant/members`（Services · **非本页创建 API**）

- `operationId`：`inviteTenantMember`
- 语义：**邀请成员**加入**已有**租户
- **不得** 替代 `POST /api/v1/tenants` 创建新客户

## 使用规则

- **不得** 把 Services members API 暴露为 BOSS 新建租户入口
- **不得** 在无 tenants API 时展示伪造生产租户 list
- 创建/停用/恢复 **必须** 带 `idempotency_key`（YAML 合入后）
- 用量/inference 计数列须标注数据来源待 YAML，**禁止** 逐租户 JWT 轮询冒充
- slug 在 UI 层校验格式；唯一性由后端 409 确认

## 待补能力边界

- Core tenants CRUD — **ADDED-TO-YAML**
- 平台 RBAC scope — 待 Core 团队
- SSO 向导与 Console `/svc/tenant/sso` — Phase 2
- 批量导入租户 — Phase 3

## 响应示例

### 租户 list 目标响应（**待 YAML · 非已冻结**）

```json
{
  "items": [
    {
      "id": "ten-acme",
      "display_name": "ACME Corp",
      "slug": "acme",
      "status": "active",
      "tenant_admin_email": "admin@acme.example",
      "quota_summary": {
        "max_gpu_count": 8,
        "used_gpu_count": 3,
        "max_cpu": 64,
        "max_memory_gb": 256
      },
      "last_active_at": "2026-06-16T08:00:00Z",
      "created_at": "2026-01-10T00:00:00Z"
    }
  ],
  "total": 1,
  "next_cursor": null
}
```

## 错误示例

### 无平台 tenant 读权限

```json
{
  "code": "FORBIDDEN",
  "message": "permission denied",
  "request_id": "req-boss-tenant-403-001"
}
```

> **注**：适用于 **`GET /api/v1/tenants`（TODO-YAML）** 场景。`message` 为示例文案，**非** YAML 已冻结 `x-ani-rbac-scope` 名。

### slug 已存在（创建目标契约）

```json
{
  "code": "CONFLICT",
  "message": "tenant slug already exists",
  "request_id": "req-boss-tenant-409-001"
}
```

### 停用非法状态（目标契约）

```json
{
  "code": "PRECONDITION_FAILED",
  "message": "tenant is not active",
  "request_id": "req-boss-tenant-422-001"
}
```

## 相关模块

- [tenant-quota-policy.md](tenant-quota-policy.md)、[tenant-admin.md](tenant-admin.md)、[tenant-usage-billing.md](tenant-usage-billing.md)
- Console：[tenant-management.md](../../console-modules/tenant/tenant-management.md)

## 回填验收标准

- [x] 满配章节齐全（对照 [`boss-full-depth-checklist.md`](../governance/boss-full-depth-checklist.md)）
- [x] BOSS vs Console / Services members 边界清晰
- [x] 无伪造 `/tenants` 为已冻结
- [x] 含响应示例与错误示例（403 + 409 + 422）
- [x] 独立字段定义（查询/创建/返回/展示）
- [ ] tenants YAML 合入后回写「已冻结路径」表
- [x] PRD/SPEC/HTML 与本文同步
