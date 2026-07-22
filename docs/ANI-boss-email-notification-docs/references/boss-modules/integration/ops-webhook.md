# 平台运维 Webhook

## 页面定位

`平台运维 Webhook` 是 BOSS **平台集成与通知** 域下的 **平台告警/事件出站** 专页：配置与管理 ANI 平台向外部运维系统（PagerDuty、Slack、自定义 HTTPS 等）推送 **平台级** 告警与运营事件的 Webhook 端点、订阅事件类型、投递日志与重试策略。

**本页 ≠ 租户事件 Webhook。** Services `GET/POST /api/v1/svc/tenant/webhooks` 为 **租户上下文** 业务事件订阅（见 Console [`tenant-management.md`](../../console-modules/tenant/tenant-management.md)、投递运维 [`tenant-webhook-ops.md`](../../console-modules/tenant/tenant-webhook-ops.md)）；本页为 **平台 RBAC + 跨租户出站**，事件源来自平台告警、AsyncTask 失败、Incident 升级等（见 [`platform-alerts-pending.md`](../overview/platform-alerts-pending.md)、[`incident-handling.md`](../health/incident-handling.md)）。

本页属于 **Core / Integration** 视角下的 **平台 RBAC** 页面。一级权威源为 `ANI-main/repo/api/openapi/v1.yaml`（Core `info.description` 提及异步任务可配置 Webhook 回调，但 **paths 段无** `/platform/webhooks*` 或等价 platform ops webhook path）。

Console [`integration-webhook-overview.md`](../../console-modules/integration/integration-webhook-overview.md) 为接入域 **导航说明**（澄清租户 Webhook vs Bot）；**不得** 将其 tenant 路径或 Bot 契约复制为本页 BOSS 正式 API。

## 文档管理规则

- 本文是 `平台运维 Webhook` 的主维护源；页面定位、参数口径、字段映射和验收标准以本文为准
- `prototypes/ani-services-prototype-boss.html` 只保留摘要与详文入口
- [`prd-boss-ops-webhook.md`](../../tasks/modules/prd/boss/integration/prd-boss-ops-webhook.md) 与 [`spec-boss-ops-webhook.md`](../../tasks/modules/spec/boss/integration/spec-boss-ops-webhook.md) 为辅助材料，不替代本文
- 如本文与辅助材料冲突，先对照 `v1.yaml` + `services/v1.yaml`，再统一回写 PRD/SPEC
- 流程：ANI-14 Phase 1 + [`module-delivery-workflow.md`](../governance/module-delivery-workflow.md) + [`boss-full-depth-checklist.md`](../governance/boss-full-depth-checklist.md)
- Console 对照：[`integration-webhook-overview.md`](../../console-modules/integration/integration-webhook-overview.md)、[`tenant-webhook-ops.md`](../../console-modules/tenant/tenant-webhook-ops.md)

## Core 层要求

- 平台 ops webhook 归属 **Core**；平台跨租户 list/CRUD — **ADDED-TO-YAML**
- Core `AsyncTask` 文档说明 202 响应可轮询 `GET /api/v1/tasks/{task_id}` **或配置 Webhook** — 此为 **通用异步回调机制**，**不是** 本页 platform ops webhook list 的已冻结替代
- Services `/api/v1/svc/tenant/webhooks*` — **租户只读参考**；BOSS **不得** 将其写成平台正式契约或逐租户 JWT 轮询冒充平台 list
- 平台 RBAC 鉴权；**不得** 信任 body/query 中未授权的 `tenant_id` 越权筛选（本页资源 **无** 租户归属字段为默认）
- 写操作 POST/PUT/PATCH/DELETE（待 YAML）须 `idempotency_key`（UUID）
- 统一错误结构：`{"code":"UPPER_SNAKE","message":"...","request_id":"..."}`
- `422 PreconditionFailed` 仅 YAML 已声明 operation 可写冻结（§2.10）
- 禁止自造 `PlatformOpsWebhook` schema / operationId / 路径为已冻结
- OpenAPI 已声明 ≠ handler 已实现

## 页面职责

- 提供 **全平台** 运维 Webhook 端点 list 与多维筛选（list API 待 YAML P1）
- 支持创建/更新/启停平台出站 Webhook（写 API 待 YAML P1）
- 展示订阅事件类型、目标 URL 摘要、最近投递状态与失败率
- 提供 **投递日志** 分页与状态筛选（list deliveries 待 YAML P1）
- 支持从 [`platform-alerts-pending.md`](../overview/platform-alerts-pending.md) 深链「配置出站 Webhook」或查看某端点投递
- 支持 Incident 通知路由边界说明 — 跳转 [`incident-handling.md`](../health/incident-handling.md)
- 与 Console 租户 Webhook 对照，明确 **平台出站 ≠ 租户事件订阅**

## 页面结构

- 首屏至少包含：`筛选区`、`Webhook 端点表格`、`创建/编辑抽屉`、`投递日志 Tab`、`边界说明`
- 无数据态、无权限态、list API 未就绪态、投递失败高亮态须可区分

```text
平台运维 Webhook
├── 筛选（active / event_type / 关键字 — list 待 YAML）
├── Webhook 端点表格（名称 / URL 摘要 / 事件 / 状态 / 最近投递）
├── 创建 / 编辑抽屉（url / events / secret / active）
├── 详情 · 投递日志 Tab（status / response_code / 时间 — deliveries 待 YAML）
│   └── 跳转：platform-alerts-pending / incident-handling
└── 边界说明（租户 Webhook 见 Console tenant-management）
```

## 数据来源与分层约束

### 数据来源划分

| 层 | 路径 / 模块 | 本页用法 |
|---|---|---|
| Core | 平台 ops webhook list/CRUD **ADDED-TO-YAML** | BOSS 正式数据源 |
| Core | 平台 ops webhook deliveries list **ADDED-TO-YAML** | 投递日志 |
| Core | `GET /api/v1/tasks/{task_id}` | **只读参考** AsyncTask 与 webhook 回调机制边界 |
| Services | `GET/POST /api/v1/svc/tenant/webhooks*` | **租户只读参考**；**禁止** 作为 BOSS 正式 API |
| Services | `GET .../tenant/webhooks/{id}/deliveries` | **租户只读参考**；投递 schema 可对齐设计 |
| 产品 | platform-alerts-pending / incident-handling | 事件源与跳转 |

### 关键边界

- v1.yaml **无** platform ops webhook path — 正文不得写「已实现平台 Webhook list」
- 租户 `Webhook` schema（`id, url, events, active, created_at`）**可参考** 字段命名，但 BOSS 资源须 Core 独立 schema（含 `scope: platform`、无 `tenant_id` 或显式 platform-only）
- **禁止** 生产环境逐租户 JWT 轮询 `listWebhooks` 冒充平台 list
- **禁止** 把 Services `createIntegrationBot` 当作平台告警出站通道（Bot 见 [`enterprise-notification.md`](enterprise-notification.md)）
- 异步任务完成回调 Webhook 与 **平台运维出站 Webhook** 可共用底层投递引擎（待 Core 设计），但 **API 面须分域**

## 页面区块与数据来源映射

| 区块 | 主要来源层 | 数据口径说明 | 跳转目标 |
|---|---|---|---|
| 筛选区 | UI + list API **待 YAML** | active / event_type | 刷新表格 |
| 端点表格 | list API **待 YAML** | 平台出站端点摘要 | 详情 / 编辑 |
| 创建/编辑 | write API **待 YAML** | url / events / secret | — |
| 投递日志 Tab | deliveries list **待 YAML** | 跨端点或单端点 | — |
| 边界说明 | 规划项 | 租户 Webhook ≠ 本页 | tenant-webhook-ops |
| 告警关联提示 | platform-alerts-pending | firing 事件可触发出站 | platform-alerts-pending |

## BOSS 与 Console 分工

| 维度 | BOSS 平台运维 Webhook | Console 租户 Webhook |
|---|---|---|
| 范围 | 全平台运维出站 | 当前租户业务事件 |
| 事件源 | 平台告警、任务失败、Incident 等 | 租户 inference/KB/成员等业务事件 |
| List | platform ops webhook list **待 YAML** P1 | `listWebhooks`（Services 已声明） |
| 投递日志 | platform deliveries **待 YAML** P1 | `listWebhookDeliveries`（Services 已声明） |
| CRUD | platform write **待 YAML** P1 | create/delete tenant webhook（Services） |
| RBAC | 平台 integration/ops scope **待 YAML** | 租户 JWT + tenant admin |
| 聚合 | 单 API 跨全局端点 | 单租户 JWT 上下文 |
| Bot/企微 | **不** 承担；见 enterprise-notification | Bot 见 integration-bot |

## 当前冻结事实

| 方法 | 路径 | operationId | 说明 |
|---|---|---|---|
| GET | `/api/v1/platform/webhooks` | `listPlatformWebhooks` | 平台 list · **ADDED-TO-YAML** |
| POST | `/api/v1/platform/webhooks` | `createPlatformWebhook` | 创建 · **ADDED-TO-YAML** |
| GET | `/api/v1/platform/webhooks/{webhook_id}` | `getPlatformWebhook` | 详情 · **ADDED-TO-YAML** |
| PATCH | `/api/v1/platform/webhooks/{webhook_id}` | `updatePlatformWebhook` | 编辑/启停 · **ADDED-TO-YAML** |
| DELETE | `/api/v1/platform/webhooks/{webhook_id}` | `deletePlatformWebhook` | 删除 · **ADDED-TO-YAML** |
| GET | `/api/v1/platform/webhooks/{webhook_id}/deliveries` | `listPlatformWebhookDeliveries` | 投递日志 · **ADDED-TO-YAML** |
| GET | `/api/v1/tasks/{task_id}` | `getTask` | AsyncTask 单查 · **路径已声明**；**非** webhook list |
| GET | `/api/v1/svc/tenant/webhooks` | `listWebhooks` | **Services 租户参考**；**非** BOSS 正式 |
| POST | `/api/v1/svc/tenant/webhooks` | `createWebhook` | **Services 租户参考** |
| GET | `/api/v1/svc/tenant/webhooks/{webhook_id}/deliveries` | `listWebhookDeliveries` | **Services 租户参考** |
| DELETE | `/api/v1/svc/tenant/webhooks/{webhook_id}` | `deleteWebhook` | **Services 租户参考** |

| 能力 | 状态 |
|---|---|
| 平台 ops webhook list | **ADDED-TO-YAML** **P1** |
| 平台 ops webhook create/update/delete | **ADDED-TO-YAML** **P1** |
| 平台 ops webhook deliveries list | **ADDED-TO-YAML** **P1** |
| 手动重试投递 | **TODO-YAML** P2 |
| `PlatformOpsWebhook` schema | **ADDED-TO-YAML** P1 |

### 页面目标字段（待 YAML 合入后对齐）

| 字段 | 说明 |
|---|---|
| `id` | 端点 UUID |
| `name` | 展示名称 |
| `url` | HTTPS 目标 URL |
| `events` | 订阅平台事件类型列表 |
| `active` | 是否启用 |
| `created_at` | 创建时间 |
| `updated_at` | 更新时间（若 YAML 声明） |
| `last_delivery_at` | 最近投递时间（list 聚合或详情） |
| `last_delivery_status` | 最近投递状态摘要 |

### Services 租户 Webhook 参考 schema（**非 BOSS 正式**）

| 字段 | 来源 | 说明 |
|---|---|---|
| `id` | `Webhook` | uuid |
| `url` | `Webhook` | uri |
| `events` | `Webhook` | string[] |
| `active` | `Webhook` | boolean |
| `created_at` | `Webhook` | date-time |

### WebhookDelivery 参考（租户 Services · **非 BOSS 正式**）

| 字段 | 说明 |
|---|---|
| `id` | 投递 UUID |
| `event` | 事件类型 |
| `status` | pending / success / failed / retrying |
| `response_code` | HTTP 状态码；可空 |
| `created_at` | 投递时间 |

## 字段级定义

### 查询字段（平台 list API 目标 · 待 YAML 合入后对齐）

| 字段 | 来源 | 必填 | 说明 |
|---|---|---|---|
| `active` | query | 可选 | true/false 筛选 |
| `event_type` | query | 可选 | 订阅事件类型 |
| `q` | query | 可选 | 名称/URL 关键字 |
| `limit` | query | 可选 | 分页条数 |
| `cursor` | query | 可选 | 游标分页 |

### 查询字段（deliveries list · 待 YAML）

| 字段 | 来源 | 必填 | 说明 |
|---|---|---|---|
| `webhook_id` | path | 必填 | 端点 ID |
| `status` | query | 可选 | pending/success/failed/retrying |
| `start_time` | query | 可选 | 时间下限 |
| `end_time` | query | 可选 | 时间上限 |
| `limit` | query | 可选 | 分页 |
| `cursor` | query | 可选 | 游标 |

### 写入字段（create/update · 待 YAML）

| 字段 | 来源 | 必填 | 说明 |
|---|---|---|---|
| `idempotency_key` | body | POST/PATCH 必填 | UUID |
| `name` | body | 建议 | 展示名 |
| `url` | body | 必填 | HTTPS URI |
| `events` | body | 必填 | minItems ≥ 1 |
| `secret` | body | 可选 | 签名密钥；UI 不回显明文 |
| `active` | body | 可选 | 默认 true |

### 返回字段（PlatformOpsWebhook · **待 YAML**）

| 字段 | 来源 | 说明 |
|---|---|---|
| `id` | schema | UUID |
| `name` | schema | 名称 |
| `url` | schema | 目标 URL |
| `events` | schema | 事件类型数组 |
| `active` | schema | 启用状态 |
| `created_at` | schema | ISO 8601 |
| `updated_at` | schema | 可空 |
| `last_delivery_at` | schema 或 list 聚合 | 可空 |
| `last_delivery_status` | schema 或 list 聚合 | 可空 |

### 展示字段（UI 计算 · 非 API 字段）

| 字段 | 说明 |
|---|---|
| `url_display` | 脱敏 URL（隐藏 query token） |
| `events_summary` | 前 N 个 event + 「+k」 |
| `active_badge` | 启用绿 / 停用灰 |
| `delivery_status_badge` | 对齐 WebhookDelivery.status 色 |
| `failure_rate_24h` | 24h 失败率（若 aggregate 待 YAML） |

## 字段展示规则

| 场景 | 展示规则 | 说明 |
|---|---|---|
| 正常态 | 筛选 + 表格 + 可开详情/编辑 | list 待 YAML |
| 无数据态 | 「暂无平台运维 Webhook 端点」 | **不** 伪造行 |
| list API 未就绪 | 说明「平台 ops webhook API 待 Core 合入（P1）」 | 不得用 tenant webhooks 冒充 |
| 无权限态 | 403 提示 | 平台 RBAC |
| `active=false` | 灰色行 + 「已停用」标签 | — |
| 最近投递 failed/retrying | 行级橙色/红色高亮 | 跳转投递 Tab |
| `secret` | 创建时可填；详情仅「已配置/未配置」 | **不** 明文展示 |
| 时间范围非法 | start ≥ end 时前端拦截 | 合入后 400 |
| 租户 Webhook 入口 | 边界区链到 Console 文档 | **不** 在本页 CRUD 租户资源 |

## 字段口径与单位

| 字段 | 口径 | 单位/格式 |
|---|---|---|
| `url` | 合法 HTTPS URI | string (uri) |
| `events` | 平台事件 enum（待 YAML） | string[] |
| `active` | 布尔 | boolean |
| `created_at` / `updated_at` | 后端原值 | ISO 8601 date-time |
| `response_code` | HTTP 状态码整数 | integer；可空 |
| `status`（投递） | pending/success/failed/retrying | enum（参考 Services WebhookDelivery） |
| `failure_rate_24h` | 0–100 百分比 | %（aggregate 待 YAML） |

## 状态与能力口径

### PlatformOpsWebhook.active

| 值 | 含义 | UI |
|---|---|---|
| `true` | 启用，参与出站投递 | 绿色「启用」 |
| `false` | 停用，不投递 | 灰色「停用」 |

### WebhookDelivery.status（页面目标 · 待 YAML；参考 Services enum）

| 值 | 含义 | UI |
|---|---|---|
| `pending` | 排队中 | 蓝色 |
| `success` | 投递成功 | 绿色 |
| `failed` | 最终失败 | 红色 |
| `retrying` | 重试中 | 橙色 |

### 状态 × 操作可用性矩阵

| 操作 \ 数据就绪 | list 待 YAML | list 已合入 |
|---|---|---|
| 列表筛选 | ⏳ 未就绪态 | ✅ |
| 创建端点 | ⏳ | ✅ |
| 编辑/启停 | ⏳ | ✅ |
| 查看投递日志 | ⏳ | ✅ |
| 删除端点 | ⏳ | ✅（待 YAML DELETE） |
| 手动重试 | ⏳ P2 | ⏳ P2 |

⏳ = API 待 YAML 合入。

## 创建前置条件

| 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
|---|---|---|
| 平台 ops webhook 读 RBAC | 已授权（**TODO-YAML** scope 待 Core 定义） | `403 FORBIDDEN` |
| 用户已认证 | 平台登录态 | `401 UNAUTHORIZED` |
| 创建/更新写 RBAC | 平台 integration 写 scope **待 YAML** | `403 FORBIDDEN` |
| `idempotency_key` | POST/PATCH 携带 UUID | `400 BAD_REQUEST` |
| `url` | 合法 HTTPS | `400 BAD_REQUEST` |
| `events` | 非空数组 | `400 BAD_REQUEST` |
| 重复 URL+events（若产品禁止） | 业务规则 **待 YAML** | `409` / `422` 待声明 |

## 操作可用性矩阵

| 操作 | 平台只读 | SRE | 平台管理员 | 说明 |
|---|---|---|---|---|
| 列表（待 YAML） | ✅ | ✅ | ✅ | platform ops webhook list |
| 创建端点 | ❌ | ✅ **待 YAML** | ✅ **待 YAML** | POST + idempotency_key |
| 编辑/启停 | ❌ | ✅ **待 YAML** | ✅ **待 YAML** | PATCH |
| 查看投递日志 | ✅ **待 YAML** | ✅ | ✅ | deliveries list |
| 删除端点 | ❌ | ✅ **待 YAML** | ✅ **待 YAML** | DELETE |
| 手动重试投递 | ❌ | P2 | P2 | **待 YAML** |
| 跳转告警待办 | ✅ | ✅ | ✅ | platform-alerts-pending |

## 删除前置校验与当前契约边界

DELETE 平台 ops webhook — **ADDED-TO-YAML**（含 422 PreconditionFailed）。预期前置校验（待 Core 产品确认）：

| 校验项 | 未通过响应 | 说明 |
|---|---|---|
| 端点存在 | `404 NOT_FOUND` | — |
| 平台写 RBAC | `403 FORBIDDEN` | — |
| 端点被 Incident 路由引用（若 YAML 声明） | `409 CONFLICT` 或 `422` | 须先解除绑定 |
| 进行中的 retry 队列（若 YAML 声明） | `422 PreconditionFailed` | 仅 operation 声明后可写冻结 |

Services `deleteWebhook`（租户）**不得** 作为本页 DELETE 契约引用。

## 接口冻结规则

### Core · 平台 ops webhook（待补 · **P1**）

<!-- ADDED-TO-YAML: GET/POST /api/v1/platform/webhooks -->

- 须平台 RBAC 鉴权
- list 预期 query：`active`、`event_type`、`q`、`limit`、`cursor`
- create/update 须 `idempotency_key`
- **合入前不得写入「已冻结」正文**

### Core · 平台 ops webhook deliveries list（待补 · **P1**）

<!-- ADDED-TO-YAML: GET /api/v1/platform/webhooks/{webhook_id}/deliveries -->

- 投递 status enum 建议与 Services `WebhookDelivery.status` 对齐
- 错误：`400`、`401`、`403`、`404`
- **合入前不得写入已实现**

### `GET /api/v1/tasks/{task_id}`（Core · **路径已声明 · 非本页 list**）

- **无** operationId / **无** `x-ani-rbac-scope`（以 v1.yaml 为准）
- **不得** 描述为 platform webhook 端点 list
- 仅说明 AsyncTask 与 webhook 回调的 **机制边界**

### Services 租户 Webhook（**只读参考 · 禁止写为 BOSS 正式**）

#### `GET /api/v1/svc/tenant/webhooks` — `listWebhooks`

- 成功：`200` + items（`Webhook`）
- 错误：`401`
- **BOSS 不得调用此 path 做平台 dashboard 聚合**

#### `POST /api/v1/svc/tenant/webhooks` — `createWebhook`

- 必填：`idempotency_key`、`url`、`events`
- 成功：`201 + Webhook`
- 错误：`400`、`409`、`422`（YAML 已声明 createWebhook 422）

#### `GET /api/v1/svc/tenant/webhooks/{webhook_id}/deliveries` — `listWebhookDeliveries`

- 成功：`200 + WebhookDeliveryListResponse`
- 错误：`401`、`403`、`404`

#### `DELETE /api/v1/svc/tenant/webhooks/{webhook_id}` — `deleteWebhook`

- 成功：`204`
- 错误：`404`

## 使用规则

- **不得** 写 platform ops webhook list 为已实现（Core v1.yaml 无对应 path）
- **不得** 把 Services tenant webhooks 路径写成 BOSS 正式 API
- **禁止** 逐租户 JWT 轮询 `listWebhooks` 冒充平台 list
- 租户 Webhook CRUD/投递 — 仅 Console 文档引用，BOSS UI **不** 复用其 handler
- 平台告警 firing 列表 — 见 [`platform-alerts-pending.md`](../overview/platform-alerts-pending.md)；**不得** 用 rules list 长度冒充
- Incident 通知 — 跳转 [`incident-handling.md`](../health/incident-handling.md)；出站通道配置在本页
- 写操作须待 YAML + `idempotency_key`
- `422` 仅对已声明 operation 写冻结（如 tenant `createWebhook` 422 **不** 代表 platform API）
- OpenAPI 已声明 ≠ handler 已实现

## 待补能力边界

- 平台 ops webhook list/create/update/delete — **ADDED-TO-YAML**
- 平台 ops webhook deliveries list — **ADDED-TO-YAML**
- `PlatformOpsWebhook` / 平台 event enum schema — P1
- 投递手动重试 POST — P2
- 24h 失败率 aggregate — P2
- 与 AsyncTask webhook 回调共用投递引擎 — Core 设计 P2
- 签名算法轮换 UI — P2

## 响应示例

### 平台 ops webhook list 成功（页面目标 · **待 YAML 合入后对齐**）

```json
{
  "items": [
    {
      "id": "pow-001",
      "name": "PagerDuty 平台 P0",
      "url": "https://events.pagerduty.com/v2/enqueue",
      "events": ["platform.alert.firing.p0", "platform.task.failed"],
      "active": true,
      "created_at": "2026-06-10T08:00:00Z",
      "updated_at": "2026-06-15T12:30:00Z",
      "last_delivery_at": "2026-06-17T09:05:00Z",
      "last_delivery_status": "success"
    }
  ],
  "next_cursor": "cursor-pow-page-2",
  "total": 12
}
```

### 平台 ops webhook 投递日志成功（页面目标 · **待 YAML**）

```json
{
  "items": [
    {
      "id": "del-9001",
      "event": "platform.alert.firing.p0",
      "status": "success",
      "response_code": 202,
      "created_at": "2026-06-17T09:05:00Z"
    }
  ],
  "next_cursor": null
}
```

## 错误示例

### URL 非法（create · **待 YAML**）

```json
{
  "code": "BAD_REQUEST",
  "message": "url must be a valid HTTPS URI",
  "request_id": "req-boss-pow-400-001"
}
```

### 无平台 ops webhook 写权限（**TODO-YAML**）

```json
{
  "code": "FORBIDDEN",
  "message": "permission denied",
  "request_id": "req-boss-pow-403-001"
}
```

> **注**：适用于 **platform ops webhook write/list（TODO-YAML）** 场景。`message` 为示例文案，**非** YAML 已冻结 scope 名。

## 相关模块

- [`platform-alerts-pending.md`](../overview/platform-alerts-pending.md)（事件源 / 深链）
- [`incident-handling.md`](../health/incident-handling.md)（Incident 通知路由）
- [`enterprise-notification.md`](enterprise-notification.md)（企微/钉钉 Bot · 平台级 P2）
- Console：[`integration-webhook-overview.md`](../../console-modules/integration/integration-webhook-overview.md)、[`tenant-webhook-ops.md`](../../console-modules/tenant/tenant-webhook-ops.md)

## 回填验收标准

- [x] 满配章节齐全（对照 [`boss-full-depth-checklist.md`](../governance/boss-full-depth-checklist.md)）
- [x] 明确 Core **无** platform ops webhook path；tenant webhooks **非** BOSS 正式
- [x] 含字段展示规则、字段口径与单位、状态与能力口径
- [x] 含响应示例与错误示例（400 + 403）
- [x] 独立字段定义（查询/返回/展示）
- [x] 删除前置校验已声明（待 YAML DELETE）
- [ ] platform ops webhook YAML 合入后回写「已冻结路径」表
- [x] PRD/SPEC/HTML 与本文同步
