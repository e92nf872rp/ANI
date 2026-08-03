# 平台企业通知

## 页面定位

`平台企业通知` 是 BOSS **平台集成与通知** 域下的 **平台级企业 IM 渠道** 专页：配置与管理 ANI 平台向 **企业微信 / 钉钉** 等平台级 Bot Webhook 推送运维通知、告警摘要与 Incident 通报，供 SRE 与平台运营在 IM 内接收 **跨租户** 平台事件。

**本页 ≠ Console 租户 Bot 集成。** Services `POST /api/v1/svc/integrations/bots`（`createIntegrationBot`）为 **租户上下文** 企微/钉钉 Bot（见 Console [`integration-bot.md`](../../console-modules/integration/integration-bot.md)）；本页为 **平台 RBAC + 全平台渠道治理**，Bot 绑定平台事件路由而非单租户业务事件。

本页属于 **Core / Integration** 视角下的 **平台 RBAC** 页面。一级权威源为 `ANI-main/repo/api/openapi/v1.yaml`（**paths 段无** `/platform/notifications*` 或 platform bot path）；Services 仅声明 **租户** Bot 创建。

Console [`integration-third-party.md`](../../console-modules/integration/integration-third-party.md) 为租户第三方集成 list；[`integration-webhook-overview.md`](../../console-modules/integration/integration-webhook-overview.md) 澄清 Webhook vs Bot 分工 — **不得** 将租户 integrations 路径写成 BOSS 正式 API。

## 文档管理规则

- 本文是 `平台企业通知` 的主维护源；页面定位、参数口径、字段映射和验收标准以本文为准
- `prototypes/ani-services-prototype-boss.html` 只保留摘要与详文入口
- [`prd-boss-enterprise-notification.md`](../../tasks/modules/prd/boss/integration/prd-boss-enterprise-notification.md) 与 [`spec-boss-enterprise-notification.md`](../../tasks/modules/spec/boss/integration/spec-boss-enterprise-notification.md) 为辅助材料，不替代本文
- 如本文与辅助材料冲突，先对照 `v1.yaml` + `services/v1.yaml`，再统一回写 PRD/SPEC
- 流程：ANI-14 Phase 1 + [`module-delivery-workflow.md`](../governance/module-delivery-workflow.md) + [`boss-full-depth-checklist.md`](../governance/boss-full-depth-checklist.md)
- Console 对照：[`integration-bot.md`](../../console-modules/integration/integration-bot.md)、[`integration-third-party.md`](../../console-modules/integration/integration-third-party.md)

## Core 层要求

- 平台企业通知渠道归属 **Core**；平台跨租户 list/CRUD — **TODO-YAML** **P2**
- Services `POST /api/v1/svc/integrations/bots` — **租户只读参考**（platform: wecom|dingtalk, webhook_url）；**NO DELETE/PATCH bots** in YAML
- Services `GET /api/v1/svc/integrations` — **租户只读参考**；BOSS **不得** 逐租户 JWT 轮询冒充平台 Bot 列表
- 平台 RBAC 鉴权；**不得** 信任 body/query 中未授权的 `tenant_id`
- 写操作 POST/PUT/PATCH/DELETE（待 YAML）须 `idempotency_key`（UUID）
- 统一错误结构：`{"code":"UPPER_SNAKE","message":"...","request_id":"..."}`
- `422 PreconditionFailed`：`createIntegrationBot` YAML **已声明 422** — **仅适用于租户 Services 路径**；平台 API 422 须待 Core 声明后再写冻结（§2.10）
- 禁止自造 `PlatformNotificationChannel` schema / operationId / 路径为已冻结
- OpenAPI 已声明 ≠ handler 已实现

## 页面职责

- 提供 **全平台** 企微/钉钉通知渠道 list 与筛选（list API 待 YAML P2）
- 支持创建/更新/启停平台 Bot 渠道（写 API 待 YAML P2）
- 展示 platform（wecom/dingtalk）、Webhook URL 摘要、订阅事件、最近投递状态
- 支持 **测试发送** 平台通知（test API 待 YAML P2）
- 与 [`incident-handling.md`](../health/incident-handling.md) 联动：Incident 分派/升级可选推送本页渠道
- 与 [`platform-alerts-pending.md`](../overview/platform-alerts-pending.md) 联动：P0/P1 摘要可路由至 IM
- 与 [`ops-webhook.md`](ops-webhook.md) 对照：HTTPS 出站 Webhook vs IM Bot 通道边界
- 明确 **不** 承担租户业务 Bot CRUD（跳转 Console integration-bot）

## 页面结构

- 首屏至少包含：`筛选区`、`渠道表格`、`创建/编辑抽屉`、`测试发送`、`边界说明`
- 无数据态、无权限态、API 未就绪态、渠道 error 态须可区分

```text
平台企业通知
├── 筛选（platform / active / 关键字 — list 待 YAML）
├── 渠道表格（名称 / 平台 / URL 摘要 / 状态 / 最近投递）
├── 创建 / 编辑抽屉（platform / webhook_url / name / events）
├── 测试发送（test API 待 YAML P2）
│   └── 跳转：incident-handling / platform-alerts-pending
└── 边界说明（租户 Bot 见 Console integration-bot）
```

## 数据来源与分层约束

### 数据来源划分

| 层 | 路径 / 模块 | 本页用法 |
|---|---|---|
| Core | 平台 notification channel list/CRUD **TODO-YAML** P2 | BOSS 正式数据源 |
| Core | 平台 notification test send **TODO-YAML** P2 | 测试消息 |
| Core | 平台 notification deliveries **TODO-YAML** P2 | 投递日志（可选 Phase 2） |
| Services | `POST /api/v1/svc/integrations/bots` | **租户只读参考**；字段口径参考 |
| Services | `GET /api/v1/svc/integrations` | **租户只读参考**；status enum 参考 |
| 产品 | incident-handling / platform-alerts-pending | 通知路由与跳转 |

### 关键边界

- Core v1.yaml **无** platform notification path — 不得写「已实现平台 Bot list」
- Services **无** `DELETE/PATCH` bots — 租户 Bot 删除/更新 **YAML 未声明**；本页平台 DELETE 亦 **待 YAML**
- **禁止** 逐租户 JWT 轮询 `listIntegrations` + 客户端筛选冒充平台 list
- 平台 HTTPS 出站 — 见 [`ops-webhook.md`](ops-webhook.md)；本页 **仅** IM Bot（wecom/dingtalk）
- `webhook_url` 在 UI **脱敏**；secret/token **不** 明文回显

## 页面区块与数据来源映射

| 区块 | 主要来源层 | 数据口径说明 | 跳转目标 |
|---|---|---|---|
| 筛选区 | UI + list API **待 YAML** | platform / active | 刷新表格 |
| 渠道表格 | list API **待 YAML** | 平台 IM 渠道摘要 | 详情 / 编辑 |
| 创建/编辑 | write API **待 YAML** | platform / webhook_url / events | — |
| 测试发送 | test API **待 YAML** | 单条测试消息 | — |
| 边界说明 | 规划项 | 租户 Bot ≠ 本页 | integration-bot |
| Incident 路由提示 | incident-handling | 可选通知渠道 | incident-handling |

## BOSS 与 Console 分工

| 维度 | BOSS 平台企业通知 | Console 企微/钉钉 Bot |
|---|---|---|
| 范围 | 全平台运维 IM 通知 | 当前租户业务通知 |
| 事件源 | 平台告警、Incident、任务失败等 | 租户 inference/KB 等业务事件 |
| List | platform channel list **待 YAML** P2 | `listIntegrations` + 客户端筛选 Bot |
| Create | platform POST **待 YAML** P2 | `createIntegrationBot`（Services 已声明） |
| Update/Delete | **待 YAML** P2 | **YAML 未声明** DELETE/PATCH |
| platform enum | wecom / dingtalk（目标对齐 Services） | wecom / dingtalk（Services 已冻结） |
| RBAC | 平台 notification scope **待 YAML** | 租户 JWT + 集成写 scope |
| 测试发送 | platform test **待 YAML** P2 | 待补 |
| HTTPS 出站 | 见 ops-webhook | 不适用 |

## 当前冻结事实

| 方法 | 路径 | operationId | 说明 |
|---|---|---|---|
| — | `/platform/notifications*` 或等价 | **不存在** | Core 无 platform bot path |
| POST | `/api/v1/svc/integrations/bots` | `createIntegrationBot` | **Services 租户参考** |
| GET | `/api/v1/svc/integrations` | `listIntegrations` | **Services 租户参考** |
| — | DELETE/PATCH bots | **未声明** | Services YAML 无 |

| 能力 | 状态 |
|---|---|
| 平台 notification channel list | **TODO-YAML** **P2** |
| 平台 channel create/update/delete | **TODO-YAML** **P2** |
| 平台 test send | **TODO-YAML** **P2** |
| 投递日志 list | **TODO-YAML** P2（可选） |
| `PlatformNotificationChannel` schema | **TODO-YAML** **P2** |

### 页面目标字段（待 YAML 合入后对齐）

| 字段 | 说明 |
|---|---|
| `id` | 渠道 UUID |
| `name` | 展示名称 |
| `platform` | wecom / dingtalk |
| `webhook_url` | IM Bot Webhook URL |
| `events` | 订阅平台事件（待 YAML；租户 Bot 无此字段） |
| `status` | active / inactive / error |
| `created_at` | 创建时间 |
| `last_delivery_at` | 最近投递；可空 |

### Services 租户 Bot 参考（**非 BOSS 正式**）

#### createIntegrationBot 请求

| 字段 | 必填 | 说明 |
|---|---|---|
| `idempotency_key` | 是 | UUID |
| `platform` | 是 | wecom / dingtalk |
| `webhook_url` | 是 | uri |
| `name` | 否 | 名称 |

#### createIntegrationBot 响应

| 字段 | 说明 |
|---|---|
| `id` | UUID |
| `platform` | string |
| `status` | string |

#### listIntegrations 项（参考）

| 字段 | 说明 |
|---|---|
| `id` | UUID |
| `name` | string |
| `provider` | string |
| `status` | active / inactive / error |

## 字段级定义

### 查询字段（平台 list API 目标 · 待 YAML）

| 字段 | 来源 | 必填 | 说明 |
|---|---|---|---|
| `platform` | query | 可选 | wecom / dingtalk |
| `status` | query | 可选 | active / inactive / error |
| `active` | query | 可选 | 与 status 对齐或独立（待 YAML） |
| `q` | query | 可选 | 名称关键字 |
| `limit` | query | 可选 | 分页 |
| `cursor` | query | 可选 | 游标 |

### 写入字段（create/update · 待 YAML）

| 字段 | 来源 | 必填 | 说明 |
|---|---|---|---|
| `idempotency_key` | body | POST/PATCH 必填 | UUID |
| `name` | body | 建议 | 展示名 |
| `platform` | body | 必填 | wecom / dingtalk |
| `webhook_url` | body | 必填 | IM Bot URI |
| `events` | body | 建议 | 平台事件订阅（待 enum） |
| `active` | body | 可选 | 启停 |

### 返回字段（PlatformNotificationChannel · **待 YAML**）

| 字段 | 来源 | 说明 |
|---|---|---|
| `id` | schema | UUID |
| `name` | schema | 名称 |
| `platform` | schema | wecom / dingtalk |
| `webhook_url` | schema | URI；详情可脱敏 |
| `events` | schema | string[] |
| `status` | schema | active / inactive / error |
| `created_at` | schema | ISO 8601 |
| `updated_at` | schema | 可空 |
| `last_delivery_at` | schema | 可空 |
| `last_error` | schema | 可空；error 态摘要 |

### 展示字段（UI 计算 · 非 API 字段）

| 字段 | 说明 |
|---|---|
| `platform_label` | 企业微信 / 钉钉 |
| `webhook_url_display` | 脱敏 URL |
| `status_badge` | active 绿 / inactive 灰 / error 红 |
| `events_summary` | 事件类型摘要 |
| `platform_icon` | wecom / dingtalk 图标 |

## 字段展示规则

| 场景 | 展示规则 | 说明 |
|---|---|---|
| 正常态 | 筛选 + 表格 + 可编辑 | list 待 YAML |
| 无数据态 | 「暂无平台企业通知渠道」 | **不** 伪造行 |
| API 未就绪 | 「平台企业通知 API 待 Core 合入（P2）」 | 不得用 tenant bots 冒充 |
| 无权限态 | 403 提示 | 平台 RBAC |
| `status=error` | 红色行 + `last_error` 摘要 | 引导编辑或测试 |
| `status=inactive` | 灰色「已停用」 | — |
| `webhook_url` | 列表/详情脱敏 | **不** 完整 token |
| platform 非法 | 前端 enum 校验 | 合入后 400 |
| 租户 Bot | 边界区链 Console | **不** 在本页 CRUD 租户 Bot |

## 字段口径与单位

| 字段 | 口径 | 单位/格式 |
|---|---|---|
| `platform` | wecom / dingtalk | enum（对齐 Services createIntegrationBot） |
| `webhook_url` | 合法 URI | string (uri) |
| `status` | active / inactive / error | enum（参考 Services Integration.status） |
| `events` | 平台事件 enum（待 YAML） | string[] |
| `created_at` / `updated_at` | 后端原值 | ISO 8601 date-time |
| `last_delivery_at` | 最近成功/失败投递时间 | ISO 8601；可空 |

## 状态与能力口径

### PlatformNotificationChannel.status（页面目标 · 待 YAML；参考 Integration.status）

| 值 | 含义 | UI |
|---|---|---|
| `active` | 渠道可用且启用 | 绿色 |
| `inactive` | 已停用 | 灰色 |
| `error` | 最近投递或校验失败 | 红色 + last_error |

### 状态 × 操作可用性矩阵

| 操作 \ 数据就绪 | list 待 YAML | list 已合入 |
|---|---|---|
| 列表筛选 | ⏳ 未就绪态 | ✅ |
| 创建渠道 | ⏳ | ✅ |
| 编辑/启停 | ⏳ | ✅ |
| 测试发送 | ⏳ P2 | ⏳ P2 |
| 删除渠道 | ⏳ | ✅（待 YAML DELETE） |
| 投递日志 | ⏳ P2 | ⏳ P2 |

⏳ = API 待 YAML 合入。

## 创建前置条件

| 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
|---|---|---|
| 平台 notification 读 RBAC | 已授权（**TODO-YAML**） | `403 FORBIDDEN` |
| 用户已认证 | 平台登录态 | `401 UNAUTHORIZED` |
| 创建写 RBAC | 平台 integration 写 **待 YAML** | `403 FORBIDDEN` |
| `idempotency_key` | POST 携带 UUID | `400 BAD_REQUEST` |
| `platform` | wecom 或 dingtalk | `400 BAD_REQUEST` |
| `webhook_url` | 合法 URI | `400 BAD_REQUEST` |
| IM Webhook 无效（若 test 校验） | 业务规则 **待 YAML** | `422` 待 platform operation 声明 |

> 租户 `createIntegrationBot` 的 **422**（Services 已声明）**不** 自动适用于平台 API。

## 操作可用性矩阵

| 操作 | 平台只读 | SRE | 平台管理员 | 说明 |
|---|---|---|---|---|
| 列表（待 YAML） | ✅ | ✅ | ✅ | platform channel list |
| 创建渠道 | ❌ | ✅ **待 YAML** | ✅ **待 YAML** | POST |
| 编辑/启停 | ❌ | ✅ **待 YAML** | ✅ **待 YAML** | PATCH |
| 测试发送 | ❌ | ✅ **待 YAML** P2 | ✅ **待 YAML** P2 | test POST |
| 删除渠道 | ❌ | ✅ **待 YAML** | ✅ **待 YAML** | DELETE |
| Incident 通知路由 | ✅ 跳转 | ✅ | ✅ | incident-handling |
| 租户 Bot 管理 | 跳转 Console | 跳转 | 跳转 | integration-bot |

## 删除前置校验与当前契约边界

DELETE 平台 notification channel — **TODO-YAML** P2。预期前置校验：

| 校验项 | 未通过响应 | 说明 |
|---|---|---|
| 渠道存在 | `404 NOT_FOUND` | — |
| 平台写 RBAC | `403 FORBIDDEN` | — |
| 渠道为 Incident 默认通知路由（若 YAML 声明） | `409` / `422` | 须先更换路由 |
| 进行中的批量通知任务（若 YAML 声明） | `422 PreconditionFailed` | operation 声明后可写冻结 |

Services **无** bot DELETE — 租户侧删除 **YAML 未声明**；**不得** 引用为平台 DELETE 契约。

## 接口冻结规则

### Core · 平台企业通知（待补 · **P2**）

<!-- TODO-YAML: GET/POST /api/v1/platform/notification-channels 或等价 -->

- 须平台 RBAC
- `platform` enum：wecom / dingtalk（建议与 Services 对齐）
- create 须 `idempotency_key`
- **合入前不得写入「已冻结」**

### Core · 平台 test send（待补 · **P2**）

<!-- TODO-YAML: POST .../notification-channels/{id}/test -->

- 须 `idempotency_key`
- 错误：`400`、`401`、`403`、`404`、`422`（待声明）

### `POST /api/v1/svc/integrations/bots`（Services · **租户参考 · 非 BOSS 正式**）

- `operationId`：`createIntegrationBot`
- 必填：`idempotency_key`、`platform`（wecom|dingtalk）、`webhook_url`
- 成功：`201`（id, platform, status）
- 错误：`400`、`401`、`422`（**422 已声明** — 仅本 operation）
- **BOSS 不得将此 path 作为平台 dashboard 数据源**

### `GET /api/v1/svc/integrations`（Services · **租户参考**）

- `operationId`：`listIntegrations`
- 项：`id, name, provider, status`（active/inactive/error）
- 成功：`200`
- 错误：`401`、`403`

## 使用规则

- **不得** 写 platform notification list 为已实现
- **不得** 把 Services tenant bots/integrations 路径写成 BOSS 正式 API
- **禁止** 逐租户 JWT 轮询冒充平台 Bot 列表
- 租户 Bot 创建/列表 — 仅 Console 引用；BOSS **不** 复用 tenant handler 做平台治理
- P0/P1 告警路由 — 见 [`platform-alerts-pending.md`](../overview/platform-alerts-pending.md)
- Incident IM 通知 — 见 [`incident-handling.md`](../health/incident-handling.md)
- HTTPS 通用出站 — 见 [`ops-webhook.md`](ops-webhook.md)；**不** 与本页混用契约
- Services `createIntegrationBot` 422 **仅** 描述 tenant operation；平台 422 待 YAML
- OpenAPI 已声明 ≠ handler 已实现

## 待补能力边界

- 平台 notification channel list/CRUD — **TODO-YAML** **P2**
- 平台 test send — **TODO-YAML** **P2**
- 投递日志与失败重试 — P2
- `@mention` / 群路由高级配置 — P2 产品
- 与 ops-webhook 统一事件 enum — Core P2
- 租户 Bot DELETE/PATCH — Services **YAML 未声明**（Console 待补）

## 响应示例

### 平台 notification channel list 成功（页面目标 · **待 YAML**）

```json
{
  "items": [
    {
      "id": "pnc-001",
      "name": "SRE 钉钉值班群",
      "platform": "dingtalk",
      "webhook_url": "https://oapi.dingtalk.com/robot/send?access_token=***",
      "events": ["platform.alert.firing.p0", "platform.incident.created"],
      "status": "active",
      "created_at": "2026-06-01T10:00:00Z",
      "last_delivery_at": "2026-06-17T08:30:00Z"
    }
  ],
  "next_cursor": null,
  "total": 3
}
```

### 平台 test send 成功（页面目标 · **待 YAML P2**）

```json
{
  "delivery_id": "td-1001",
  "status": "success",
  "sent_at": "2026-06-17T10:00:00Z"
}
```

## 错误示例

### platform 枚举非法（create · **待 YAML**）

```json
{
  "code": "BAD_REQUEST",
  "message": "platform must be one of: wecom, dingtalk",
  "request_id": "req-boss-pnc-400-001"
}
```

### 无平台 notification 写权限（**TODO-YAML**）

```json
{
  "code": "FORBIDDEN",
  "message": "permission denied",
  "request_id": "req-boss-pnc-403-001"
}
```

> **注**：适用于 **platform notification write/list（TODO-YAML）** 场景。

## 相关模块

- [`ops-webhook.md`](ops-webhook.md)（HTTPS 平台出站 · P1）
- [`incident-handling.md`](../health/incident-handling.md)（Incident IM 通知）
- [`platform-alerts-pending.md`](../overview/platform-alerts-pending.md)（告警路由）
- Console：[`integration-bot.md`](../../console-modules/integration/integration-bot.md)、[`integration-third-party.md`](../../console-modules/integration/integration-third-party.md)

## 回填验收标准

- [x] 满配章节齐全（对照 [`boss-full-depth-checklist.md`](../governance/boss-full-depth-checklist.md)）
- [x] 明确 Core **无** platform notification path；tenant bots **非** BOSS 正式
- [x] 含字段展示规则、字段口径与单位、状态与能力口径
- [x] 含响应示例与错误示例（400 + 403）
- [x] 独立字段定义（查询/返回/展示）
- [x] 删除前置校验已声明（待 YAML DELETE；Services 无 bot DELETE）
- [ ] platform notification YAML 合入后回写「已冻结路径」表
- [x] PRD/SPEC/HTML 与本文同步
