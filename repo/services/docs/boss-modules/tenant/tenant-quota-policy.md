# 配额策略

## 页面定位

`配额策略` 是 BOSS **租户与客户管理** 域下的 **跨租户资源上限与限流策略** 治理页：一览各租户配额、使用率、超额风险，并提交配额调整（须审计备注）。

本页属于 **Core / Tenants + Quota** 视角下的 **平台 RBAC** 页面。配额数据规划归属 Core（`/api/v1/tenants/{tenant_id}/quota` 或 tenants 内嵌 quota）；当前 `v1.yaml` **无** quota path — 全部 **TODO-YAML**。

Console **无对等页**；租户不可自助修改硬配额。用量只读参考见 `GET /api/v1/metering/usage`（**单租户上下文**）；BOSS 跨租户 used 量须 **平台 aggregate 待 YAML**。

## 文档管理规则

- 本文是 `配额策略` 的主维护源；页面定位、参数口径、字段映射和验收标准以本文为准
- [`prd-boss-tenant-quota-policy.md`](../../tasks/modules/prd/boss/tenant/prd-boss-tenant-quota-policy.md) 与 [`spec-boss-tenant-quota-policy.md`](../../tasks/modules/spec/boss/tenant/spec-boss-tenant-quota-policy.md) 为辅助材料，不替代本文
- 与 [`tenant-list.md`](tenant-list.md) 共享 `tenant_id`、status 口径
- 流程：ANI-14 Phase 1 + [`module-delivery-workflow.md`](../governance/module-delivery-workflow.md) + [`boss-full-depth-checklist.md`](../governance/boss-full-depth-checklist.md)

## Core 层要求

- 配额归属 **Core** 租户治理面（ANI-02 tenants 域）
- 当前 **ADDED-TO-YAML** `GET/PUT /api/v1/tenants/{tenant_id}/quota`
- Core 资源配额（GPU/CPU/内存/存储）与 Services 业务配额（推理服务数、知识库数）可能 **分属两层** — 正文须标注来源层
- PUT 调整配额须 `idempotency_key` + 平台 RBAC
- 调整配额 **必须** 写 `audit_note`（产品要求）；审计 API 见 audit 域 **TODO-YAML**
- **不得** 把 Console 用量页 `resource_type` 枚举冒充 quota 维度
- 统一错误结构；`422` 仅 YAML 已声明 operation 可写冻结（§2.10）
- 禁止自造 `TenantQuota` schema 为已冻结

## 页面职责

- 提供 **跨租户** 配额表格、筛选（状态/超额租户）与平台配额使用率概览
- 提供 **调整配额抽屉**：当前配额/使用、新配额、影响说明、生效时间、审计备注
- 支持从 [`tenant-list.md`](tenant-list.md) 带 `tenant_id` 深链打开抽屉
- 支持跳转 [`tenant-usage-billing.md`](tenant-usage-billing.md) 查看用量明细
- **不承担** 租户内业务对象 CRUD（推理/KB 创建限制由 Services 层 enforcement · 待架构确认）

## 页面结构

- 首屏至少包含：`租户筛选区`、`配额概览 KPI`、`配额表格`、`边界说明`
- 调整抽屉为 overlay；提交后刷新表格行
- 无数据态、无权限态、API 未就绪态、422 前置失败态须可区分

```text
配额策略
├── 筛选（status / 名称 / 超额租户 only）
├── 配额概览（全平台使用率、Top 紧张租户）
├── 配额表格
└── 调整配额抽屉
    ├── 当前配额 / 当前使用（只读）
    ├── 新配额（各维度）
    ├── 影响说明（必填）
    ├── effective_at（立即 / 预约）
    ├── audit_note（必填）
    └── 提交 PUT + idempotency_key
```

## 数据来源与分层约束

### 数据来源划分

| 层 | 路径 / 来源 | 本页用法 |
|---|---|---|
| Core | tenants/quota **TODO-YAML** | 配额 CRUD 正式数据源 |
| Core | `GET /api/v1/metering/usage` | **单租户** used 参考；**非** 跨租户正式方案 |
| Core | 平台 metering aggregate **TODO-YAML** | 跨租户 used 量 |
| Core | gpu-inventory / occupancy | GPU used 参考 · 租户 scope |
| Services | 业务配额（max_inference_services 等） | **待 YAML** / 架构确认 |
| Core | platform-audit-log | 配额变更审计 **TODO-YAML** |

### 关键边界

- `max_*` 字段来自 **quota**；`used_*` 来自 **metering/inventory aggregate** — 两层口径不得混为一列而不标注
- 调低配额时须校验 `new_max >= current_used` — 422 语义 **待 YAML 合入后冻结**
- 不得用 `/metering/usage` 的 `resource_type` 直接映射为 quota 字段名
- rate_limit_rps 可能归属 Gateway/Core 扩展 — **待 YAML**

## 页面区块与数据来源映射

| 区块 | 主要来源 | 数据口径说明 | 跳转目标 |
|---|---|---|---|
| 筛选区 | tenants/quota list **待 YAML** | 超额筛选 query | 刷新表格 |
| 概览 KPI | 平台 aggregate **待 YAML** | 全平台 quota 使用率 | — |
| 配额表格 | GET quota list **待 YAML** | max + used 分列 | 打开抽屉 |
| 调整抽屉 | PUT quota **待 YAML** | idempotency_key + audit_note | 刷新行 |
| 用量钻取 | tenant-usage-billing | 单租户用量 | billing |
| 边界说明 | 规划项 | Core vs Services 配额 | tenant-list |

## BOSS 与 Console 分工

| 维度 | BOSS 配额策略 | Console |
|---|---|---|
| 视角 | 跨租户表格 | 无对等页 |
| 调整硬配额 | 平台运营/SRE | ❌ 不可自助 |
| 查看当前使用 | 平台汇总 | usage-report 仅本租户 |
| 业务对象限额 | 可能 Services **待 YAML** | 受配额 enforcement |
| RBAC | 平台 tenant/quota write | 租户 JWT |

## 当前冻结事实

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/metering/usage` | 单租户用量；**operationId 未声明** |

| 能力 | 状态 |
|---|---|
| 平台 quota CRUD | **ADDED-TO-YAML** P1 |
| 跨租户 used aggregate | **ADDED-TO-YAML**（TenantQuota.used_* 字段） |
| Services 业务配额 | **待架构确认** |

### 建议 TODO-YAML（非冻结）

| 能力 | 建议路径 | 归属 |
|---|---|---|
| 查询租户配额 | `GET /api/v1/tenants/{tenant_id}/quota` | Core |
| 更新配额 | `PUT /api/v1/tenants/{tenant_id}/quota` | Core |
| 列出租户配额摘要 | `GET /api/v1/tenants?include=quota` | Core |

## 字段级定义

### 查询字段（quota list 目标 · **TODO-YAML**）

| 字段 | 来源 | 必填 | 说明 |
|---|---|---|---|
| `status` | query | ❌ | 租户 status 筛选 |
| `over_quota_only` | query | ❌ | 仅超额租户 |
| `q` | query | ❌ | 租户名关键词 |
| `limit` / `cursor` | query | ❌ | 分页 |

### 配额表格字段（页面目标 · 标注来源层）

| 字段 | 说明 | 来源层 |
|---|---|---|
| `tenant_id` / `display_name` | 租户 | Core tenants |
| `max_gpu_count` | GPU 上限 | Core quota |
| `used_gpu_count` | GPU 已用 | metering/inventory **待 YAML** |
| `max_cpu` | CPU 核数上限 | Core quota |
| `max_memory_gb` | 内存 GB 上限 | Core quota |
| `max_storage_gb` | 存储 GB 上限 | Core quota |
| `max_inference_services` | 推理服务数上限 | Services **待 YAML** |
| `max_knowledge_bases` | 知识库数上限 | Services **待 YAML** |
| `rate_limit_rps` | API 限流 | Gateway/Core **待 YAML** |
| `quota_utilization` | 综合使用率 | **UI 计算** |

### 调整配额抽屉 — 请求字段（目标 · **TODO-YAML**）

| 字段 | 必填 | 说明 |
|---|---|---|
| `idempotency_key` | ✅ | 幂等 |
| `max_gpu_count` 等 | 可选 | 部分更新 |
| `impact_summary` | ✅ | 影响说明（产品字段） |
| `effective_at` | ❌ | 立即或预约 |
| `audit_note` | ✅ | 审计备注 |

### 展示字段（UI 计算）

| 字段 | 说明 |
|---|---|
| `gpu_headroom` | max − used |
| `is_over_quota` | used > max 任一致 |
| `utilization_pct` | used/max × 100 |

## 字段展示规则

| 场景 | 展示规则 | 说明 |
|---|---|---|
| API 未就绪 | 表格占位 + TODO-YAML | **不** 伪造配额数据 |
| 超额租户 | 行高亮红色 | over_quota_only |
| used 不可用 | used 列「待接入」 | 不得伪造 0 |
| 调低失败 422 | 抽屉内展示「低于已用量」 | 保留输入 |
| 提交成功 | 关闭抽屉 + 刷新行 | 200/204 |
| 无写权限 | 隐藏提交；表格只读 | 403 |
| audit_note 空 | 客户端拦截提交 | 产品必填 |

## 字段口径与单位

| 字段 | 口径 | 单位/格式 |
|---|---|---|
| `max_gpu_count` / `used_gpu_count` | 整数 | count |
| `max_memory_gb` / `max_storage_gb` | 整数 | GiB |
| `max_cpu` | 整数 | vCPU |
| `rate_limit_rps` | 浮点或整数 | req/s |
| `quota_utilization` | max(维度使用率) 或加权 | % |

## 状态与能力口径

本页变更 **quota 上限**，不直接变更租户 `status`（停用见 tenant-list）。

| 操作 | 前置（目标） | 失败 |
|---|---|---|
| 调高配额 | 无 | — |
| 调低配额 | new ≥ used | `422` **待 YAML** |
| 调低 GPU | 无 running 超额实例 | `422` **待 YAML** |

批量调整 — Phase 2。

## 创建前置条件

| 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
|---|---|---|
| 平台 quota 读 RBAC | 已授权（**TODO-YAML** quota scope 待 Core 定义） | `403 FORBIDDEN` |
| 平台 quota 写 RBAC | 已授权（**TODO-YAML**） | `403 FORBIDDEN` |
| `idempotency_key` | 非空 | `400 BAD_REQUEST` |
| `audit_note` | 非空（产品） | UI 拦截；API `400` **待 YAML** |
| 调低配额 | new ≥ current_used | `422` **待 YAML** |
| 租户非 deleted | status 允许 | `422` **待 YAML** |

## 操作可用性矩阵

| 操作 | 平台只读 | 平台运营 | 平台管理员 | 说明 |
|---|---|---|---|---|
| 查看配额表格 | ✅（待 YAML） | ✅ | ✅ | GET |
| 筛选超额租户 | ✅ | ✅ | ✅ | query |
| 打开调整抽屉 | ❌ | ✅ | ✅ | — |
| 提交配额变更 | ❌ | ✅ | ✅ | PUT |
| 批量调整 | ❌ | Phase 2 | Phase 2 | — |
| 查看用量明细 | ✅ → billing | ✅ | ✅ | 深链 |

## 删除前置校验与当前契约边界

本页 **无 DELETE 配额**；归零配额通过 PUT 设置 max=0（**待 YAML** 语义与 422 规则）。

## 接口冻结规则

### `GET /api/v1/metering/usage`（单租户 · **非 quota API**）

- `operationId`：**未声明**
- `query.required`：`start_time`、`end_time`
- 用途：钻取单租户 **used** 参考；**不是** quota PUT

### Core quota API（**ADDED-TO-YAML**）

<!-- TODO-YAML: GET/PUT /api/v1/tenants/{tenant_id}/quota -->

- PUT 须 `idempotency_key` + `audit_note`（目标）
- 合入前不得写入「已冻结」表

## 使用规则

- 表格须 **分列** max（quota）与 used（metering/inventory）
- **不得** 在无 quota API 时启用生产环境 PUT
- 调低配额 **必须** 后端校验 used 量；不得仅前端校验
- audit_note **必填**；须写入 audit 域（API 待 YAML）
- Services 业务配额与 Core 资源配额 **不得** 混为同一 PUT 而不标注层

## 待补能力边界

- Core tenants/quota 路径 — **ADDED-TO-YAML**
- Services 业务配额与 Core 边界 — 架构评审
- 配额变更异步生效 + AsyncTask — Phase 2
- 预约生效 scheduler — Phase 2

## 响应示例

### 单租户 metering 只读参考（**非 quota 正式响应**）

```json
{
  "items": [
    {
      "resource_type": "instance_gpu_seconds",
      "total_quantity": 259200,
      "unit": "seconds",
      "period": "month"
    }
  ],
  "total": 1,
  "dev_profile": { "mode": "real", "real_provider": true }
}
```

### 配额查询目标响应（**待 YAML · 非已冻结**）

```json
{
  "tenant_id": "ten-acme",
  "max_gpu_count": 8,
  "used_gpu_count": 3,
  "max_cpu": 64,
  "used_cpu": 28,
  "max_memory_gb": 256,
  "used_memory_gb": 180,
  "max_inference_services": 10,
  "used_inference_services": 4,
  "rate_limit_rps": 1000,
  "updated_at": "2026-06-10T12:00:00Z"
}
```

## 错误示例

### 无 quota 写权限

```json
{
  "code": "FORBIDDEN",
  "message": "permission denied",
  "request_id": "req-boss-quota-403-001"
}
```

> **注**：适用于 **quota PUT（TODO-YAML）** 场景。`message` 为示例文案，**非** YAML 已冻结 scope 名。

### 调低低于已用量（目标契约）

```json
{
  "code": "PRECONDITION_FAILED",
  "message": "new max_gpu_count cannot be less than current usage",
  "request_id": "req-boss-quota-422-001"
}
```

## 相关模块

- [tenant-list.md](tenant-list.md)、[tenant-usage-billing.md](tenant-usage-billing.md)
- [`../metering/`](../metering/) 各平台计量专页
- Console：[usage-report.md](../../console-modules/tenant/usage-report.md)

## 回填验收标准

- [x] 满配章节齐全（对照 [`boss-full-depth-checklist.md`](../governance/boss-full-depth-checklist.md)）
- [x] Core/Services 配额分层说明
- [x] 无伪造 quota schema 为已冻结
- [x] 含响应示例与错误示例（403 + 422）
- [x] 独立字段定义（表格/抽屉/展示）
- [ ] quota YAML 合入后回写「已冻结路径」表
- [x] PRD/SPEC/HTML 与本文同步
