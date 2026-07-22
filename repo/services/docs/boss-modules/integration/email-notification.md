# 邮件通知

## 页面定位

`邮件通知` 是 BOSS **平台集成与通知** 域下的 **平台级邮件通道** 专页：配置与管理 ANI 平台通过 **SMTP** 向平台管理员发送运维通知、告警摘要与 Incident 通报，供 SRE 与平台运营在邮箱接收 **跨租户** 平台事件。

**本页 ≠ Console 租户侧邮件通知。** 本页为 **平台 RBAC + 全平台邮件通道治理**，管理 SMTP 通道、全局收件人列表与事件订阅开关。

本页属于 **Core / Integration** 视角下的 **平台 RBAC** 页面。一级权威源为 `ANI-main/repo/api/openapi/v1.yaml`。

与 [`enterprise-notification.md`](enterprise-notification.md) 同属「平台集成与通知」：IM 管 Bot，本页管 **Email 通道与订阅**，职责分离。

## 文档管理规则

- 本文是 `邮件通知` 的主维护源；页面定位、参数口径、字段映射和验收标准以本文为准
- PRD：`docs/ANI-boss-email-notification-docs/prd/prd-boss-email-notification.md`
- UX：`docs/ANI-boss-email-notification-docs/ux/ux-boss-email-notification.md`
- SPEC：`docs/ANI-boss-email-notification-docs/spec/spec-boss-email-notification.md`
- 如本文与辅助材料冲突，先对照 `v1.yaml`，再统一回写 PRD/SPEC
- 流程：ANI-14 Phase 1 + [`module-delivery-workflow.md`](../governance/module-delivery-workflow.md) + [`boss-full-depth-checklist.md`](../governance/boss-full-depth-checklist.md)
- Console 对照：本页无 Console 对照（仅 BOSS 平台侧）

## Core 层要求

- 平台邮件通知渠道归属 **Core**；路径前缀 `/api/v1/notifications/email/*`
- 平台 RBAC 鉴权；**不得** 信任 body/query 中未授权的 `tenant_id`
- 写操作 POST/PUT/PATCH/DELETE 须 `Idempotency-Key` header（UUID）
- 统一错误结构：`{"code":"UPPER_SNAKE","message":"...","request_id":"..."}`
- `422 PreconditionFailed`：测试发送前置条件不满足（SMTP 未配置 / 无启用收件人 / 无凭据）
- 禁止自造未写入 OpenAPI 的 schema / operationId / 路径为已冻结
- OpenAPI 已声明 ≠ handler 已实现

## 页面职责

- 提供 **全平台** SMTP 发信通道配置（首期仅 SMTP；云厂商 API = P2）
- 支持平台级收件邮箱的增删改与启停（全局一份列表）
- 支持按首期事件枚举开关邮件订阅并持久化
- 支持 **测试发送** 平台通知邮件
- 与 [`incident-handling.md`](../health/incident-handling.md) 联动：Incident 创建/升级可走邮件
- 与 [`platform-alerts-pending.md`](../overview/platform-alerts-pending.md) 联动：P0/P1 摘要可路由至邮件
- 与 [`enterprise-notification.md`](enterprise-notification.md) 对照：IM Bot vs Email 通道边界
- 明确 **不** 承担租户侧邮件通知（本页仅 BOSS 平台侧）

## 页面结构

- 首屏至少包含：`SMTP 表单`、`收件人表格`、`订阅开关表`、`测试发送`、`边界说明`
- 无数据态、无权限态、API 未就绪态、错误态须可区分

```text
邮件通知
├── 发信设置（SMTP 通道配置 + 测试发送）
├── 收件邮箱（全局收件人列表 + 增删改启停）
└── 事件订阅（5 事件开关 + 批量保存）
```

## 数据来源与分层约束

### 数据来源划分

| 层 | 路径 / 模块 | 本页用法 |
|---|---|---|
| Core | `GET/PUT /api/v1/notifications/email/smtp` | SMTP 通道配置 |
| Core | `GET/POST/PATCH/DELETE /api/v1/notifications/email/recipients[/{id}]` | 收件人 CRUD |
| Core | `GET/PUT /api/v1/notifications/email/subscriptions` | 事件订阅开关 |
| Core | `POST /api/v1/notifications/email/test` | 测试发送 |
| 产品 | incident-handling / platform-alerts-pending | 通知路由与跳转 |

### 关键边界

- Core v1.yaml **已声明** `/notifications/email/*` 路径（本批次新增）
- Services `integrations` / `bots` 路径 **不** 作为本页数据源
- `password` / `auth_code` 在 UI **脱敏**；明文 **不** 回显；响应仅返回 `has_password` / `has_auth_code` 布尔位
- `password` 与 `auth_code` 独立保存、独立清除，服务端不强制二选一
- 发送邮件时若 `auth_code` 已设置则优先使用 `auth_code`，否则使用 `password`

## 页面区块与数据来源映射

| 区块 | 主要来源层 | 数据口径说明 | 跳转目标 |
|---|---|---|---|
| 发信设置 | Core SMTP API | smtp_host / port / encryption / from_address / username / has_password / has_auth_code | — |
| 测试发送 | Core test API | 同步返回 success / message / request_id | — |
| 收件邮箱 | Core recipients API | email / label / enabled | — |
| 事件订阅 | Core subscriptions API | 5 事件 × enabled 开关 | — |
| 边界说明 | 规划项 | IM 见 enterprise-notification | enterprise-notification |

## BOSS 与 Console 分工

| 维度 | BOSS 邮件通知 | Console 租户侧 |
|---|---|---|
| 范围 | 全平台运维邮件通知 | 不适用（本 PRD 仅 BOSS） |
| 事件源 | 平台告警、Incident、任务失败等 | — |
| List | `listEmailRecipients` / `listEmailSubscriptions` | — |
| Create/Update/Delete | Core recipients API | — |
| RBAC | `scope:notifications:read/write` | — |
| 测试发送 | `sendTestEmail` | — |

## 当前冻结事实

| 方法 | 路径 | operationId | 说明 |
|---|---|---|---|
| GET | `/api/v1/notifications/email/smtp` | `getEmailSmtpConfig` | SMTP 通道配置读取 |
| PUT | `/api/v1/notifications/email/smtp` | `putEmailSmtpConfig` | SMTP 通道配置保存 |
| GET | `/api/v1/notifications/email/recipients` | `listEmailRecipients` | 收件人列表 |
| POST | `/api/v1/notifications/email/recipients` | `createEmailRecipient` | 新增收件人 |
| PATCH | `/api/v1/notifications/email/recipients/{recipient_id}` | `updateEmailRecipient` | 更新收件人 |
| DELETE | `/api/v1/notifications/email/recipients/{recipient_id}` | `deleteEmailRecipient` | 删除收件人 |
| GET | `/api/v1/notifications/email/subscriptions` | `listEmailSubscriptions` | 订阅列表 |
| PUT | `/api/v1/notifications/email/subscriptions` | `putEmailSubscriptions` | 批量保存订阅 |
| POST | `/api/v1/notifications/email/test` | `sendTestEmail` | 测试发送 |

### 页面目标字段

#### SMTP 配置

| 字段 | 说明 |
|---|---|
| `configured` | 是否已配置（false = 空态） |
| `smtp_host` | SMTP 服务器主机 |
| `smtp_port` | SMTP 服务器端口 |
| `encryption` | none / starttls / ssl |
| `from_address` | 发件人地址 |
| `username` | 登录账号 |
| `has_password` | 是否已设置密码（明文不回显） |
| `has_auth_code` | 是否已设置授权码（明文不回显） |
| `created_at` | 创建时间 |
| `updated_at` | 更新时间 |

#### 收件人

| 字段 | 说明 |
|---|---|
| `id` | 收件人 UUID |
| `email` | 邮箱地址 |
| `label` | 备注（可空） |
| `enabled` | 启用 / 停用 |
| `created_at` | 创建时间 |
| `updated_at` | 更新时间 |

#### 事件订阅

| 字段 | 说明 |
|---|---|
| `event_type` | 事件枚举（首期 5 个） |
| `description` | 事件描述 |
| `enabled` | 邮件开关 |
| `updated_at` | 更新时间 |

### 首期冻结事件枚举

| 展示名 | event_type |
|--------|------------|
| 平台告警 P0 | `platform_alert_p0` |
| 平台告警 P1 | `platform_alert_p1` |
| Incident 创建 | `incident_created` |
| Incident 升级 | `incident_escalated` |
| 平台关键任务失败 | `platform_task_failed` |

## 字段展示规则

| 场景 | 展示规则 | 说明 |
|---|---|---|
| 正常态 | 表单 / 表格 / 开关 | 已配置 |
| 无数据态 | 「暂无收件人」+ CTA | **不** 伪造行 |
| API 未就绪 | 「邮件通知 API 尚未就绪」 | 501 NOT_IMPLEMENTED |
| 无权限态 | 403 提示 | 平台 RBAC |
| `password` / `auth_code` | 列表/详情脱敏 | **不** 完整明文；仅 `has_*` 布尔位 |
| 非法 email | 前端校验 | 提交后 400 |

## 字段口径与单位

| 字段 | 口径 | 单位/格式 |
|---|---|---|
| `smtp_host` | 主机名或 IP | string |
| `smtp_port` | 1-65535 | integer |
| `encryption` | none / starttls / ssl | enum |
| `from_address` | 合法 email | string (email) |
| `password` | write-only | string (password)；省略=不修改，""=清除，非空=覆盖 |
| `auth_code` | write-only | string (password)；与 password 独立保存，不互斥 |
| `email` | 合法 email | string (email) |
| `enabled` | true / false | boolean |
| `event_type` | 首期 5 枚举 | string enum |

## 状态与能力口径

### SMTP 配置状态

| 值 | 含义 | UI |
|---|---|---|
| `configured=false` | 未配置 | 空白表单 |
| `configured=true` | 已配置 | 回填；密码/授权码不回显 |

### 收件人状态

| 值 | 含义 | UI |
|---|---|---|
| `enabled=true` | 启用 | 绿色 Tag「启用」 |
| `enabled=false` | 停用 | 灰色 Tag「停用」 |

### 订阅状态

| 值 | 含义 | UI |
|---|---|---|
| `enabled=true` | 开启邮件 | Switch on |
| `enabled=false` | 关闭邮件 | Switch off |

### 状态 × 操作可用性矩阵

| 操作 \ 数据就绪 | API 未就绪 | API 已就绪 |
|---|---|---|
| 查看 SMTP 配置 | ⏳ 未就绪态 | ✅ |
| 保存 SMTP 配置 | ⏳ | ✅ |
| 查看收件人列表 | ⏳ | ✅ |
| 新增/编辑/删除收件人 | ⏳ | ✅ |
| 查看订阅列表 | ⏳ | ✅ |
| 保存订阅 | ⏳ | ✅ |
| 测试发送 | ⏳ | ✅（前置条件满足时） |

⏳ = API 未就绪（501）。

## 创建前置条件

| 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
|---|---|---|
| 平台 notifications 读 RBAC | 已授权 | `403 FORBIDDEN` |
| 用户已认证 | 平台登录态 | `401 UNAUTHORIZED` |
| 保存写 RBAC | 平台 notifications 写 | `403 FORBIDDEN` |
| `Idempotency-Key` | 写操作必填 UUID | `400 BAD_REQUEST` |
| `smtp_host` | 非空 | `400 BAD_REQUEST` |
| `smtp_port` | 1-65535 | `400 BAD_REQUEST` |
| `encryption` | none / starttls / ssl | `400 BAD_REQUEST` |
| `from_address` | 合法 email | `400 BAD_REQUEST` |
| `email`（收件人） | 合法 email | `400 BAD_REQUEST` |
| 测试发送前置 | SMTP 已配置 + ≥1 启用收件人 + (password 或 auth_code 至少一个已设置) | `422 PRECONDITION_FAILED` |

## 操作可用性矩阵

| 操作 | 平台只读 | SRE | 平台管理员 | 说明 |
|---|---|---|---|---|
| 查看 SMTP 配置 | ✅ | ✅ | ✅ | GET |
| 保存 SMTP 配置 | ❌ | ✅ | ✅ | PUT |
| 查看收件人列表 | ✅ | ✅ | ✅ | GET |
| 新增收件人 | ❌ | ✅ | ✅ | POST |
| 编辑/启停收件人 | ❌ | ✅ | ✅ | PATCH |
| 删除收件人 | ❌ | ✅ | ✅ | DELETE |
| 查看订阅列表 | ✅ | ✅ | ✅ | GET |
| 保存订阅 | ❌ | ✅ | ✅ | PUT |
| 测试发送 | ❌ | ✅ | ✅ | POST |

## 接口冻结规则

### Core · 邮件通知 SMTP 配置

- `GET /api/v1/notifications/email/smtp` — 只读
- `PUT /api/v1/notifications/email/smtp` — 须 `Idempotency-Key` header
- 须平台 RBAC `scope:notifications:read/write`
- `password` / `auth_code` write-only，响应仅返回 `has_password` / `has_auth_code` 布尔位

### Core · 邮件通知收件人

- `GET/POST/PATCH/DELETE /api/v1/notifications/email/recipients[/{id}]`
- 写操作须 `Idempotency-Key` header

### Core · 邮件通知订阅

- `GET/PUT /api/v1/notifications/email/subscriptions`
- PUT 须 `Idempotency-Key` header
- 批量保存（非行内 PATCH）

### Core · 邮件通知测试发送

- `POST /api/v1/notifications/email/test`
- 须 `Idempotency-Key` header
- 错误：`400`、`401`、`403`、`422`（前置条件不满足）

## 使用规则

- **不得** 把 Services tenant integrations/bots 路径写成 BOSS 正式 API
- **不得** 伪造 SMTP 配置为已保存成功
- `password` / `auth_code` 明文**永不**回显
- `password` 与 `auth_code` 独立保存、独立清除，服务端不强制二选一
- 发送邮件时若 `auth_code` 已设置则优先使用 `auth_code`，否则使用 `password`
- 测试发送前置条件：SMTP 已配置 + ≥1 启用收件人 + (password 或 auth_code 至少一个已设置)
- P0/P1 告警路由 — 见 [`platform-alerts-pending.md`](../overview/platform-alerts-pending.md)
- Incident 邮件通知 — 见 [`incident-handling.md`](../health/incident-handling.md)
- IM Bot 通知 — 见 [`enterprise-notification.md`](enterprise-notification.md)；**不** 与本页混用契约
- OpenAPI 已声明 ≠ handler 已实现

## 待补能力边界

- 云厂商邮件 API（SendGrid/SES 等）— P2
- 按事件组绑定不同收件人 — P2
- 完整投递历史 list / 独立历史中心 — P2
- 邮件模板在线编辑 / 可视化排版 — 不做
- Console 租户侧邮件通知自助配置 — 不做（本 PRD 仅 BOSS）

## 响应示例

### SMTP 配置已保存

```json
{
  "configured": true,
  "smtp_host": "smtp.example.com",
  "smtp_port": 465,
  "encryption": "ssl",
  "from_address": "ani-alert@example.com",
  "username": "ani-alert@example.com",
  "has_password": true,
  "has_auth_code": false,
  "created_at": "2026-07-22T10:00:00Z",
  "updated_at": "2026-07-22T10:00:00Z"
}
```

### SMTP 配置空态

```json
{
  "configured": false
}
```

### 收件人列表

```json
{
  "items": [
    {
      "id": "rcp-001",
      "email": "sre-oncall@example.com",
      "label": "SRE 值班",
      "enabled": true,
      "created_at": "2026-07-22T10:00:00Z",
      "updated_at": "2026-07-22T10:00:00Z"
    }
  ],
  "total": 1
}
```

### 订阅列表

```json
{
  "items": [
    { "event_type": "platform_alert_p0", "description": "平台告警 P0", "enabled": true, "updated_at": "2026-07-22T10:00:00Z" },
    { "event_type": "platform_alert_p1", "description": "平台告警 P1", "enabled": false, "updated_at": "2026-07-22T10:00:00Z" },
    { "event_type": "incident_created", "description": "Incident 创建", "enabled": true, "updated_at": "2026-07-22T10:00:00Z" },
    { "event_type": "incident_escalated", "description": "Incident 升级", "enabled": false, "updated_at": "2026-07-22T10:00:00Z" },
    { "event_type": "platform_task_failed", "description": "平台关键任务失败", "enabled": true, "updated_at": "2026-07-22T10:00:00Z" }
  ],
  "total": 5
}
```

### 测试发送成功

```json
{
  "success": true,
  "message": "测试邮件已发送",
  "request_id": "req-001",
  "sent_at": "2026-07-22T10:00:00Z"
}
```

### 测试发送失败

```json
{
  "success": false,
  "message": "SMTP 认证失败：535 5.7.3 Authentication unsuccessful",
  "request_id": "req-002",
  "sent_at": null
}
```

## 错误示例

### SMTP 端口越界

```json
{
  "code": "BAD_REQUEST",
  "message": "smtp_port must be between 1 and 65535",
  "request_id": "req-boss-email-400-001"
}
```

### 无平台 notifications 写权限

```json
{
  "code": "FORBIDDEN",
  "message": "permission denied",
  "request_id": "req-boss-email-403-001"
}
```

### 测试发送前置不满足

```json
{
  "code": "PRECONDITION_FAILED",
  "message": "SMTP 凭据未配置（password 或 auth_code 至少设置一个）",
  "request_id": "req-boss-email-422-001"
}
```

## 相关模块

- [`enterprise-notification.md`](enterprise-notification.md)（企微/钉钉 IM Bot · 同域对照）
- [`ops-webhook.md`](ops-webhook.md)（HTTPS 平台出站 · 对照）
- [`incident-handling.md`](../health/incident-handling.md)（Incident 邮件通知路由）
- [`platform-alerts-pending.md`](../overview/platform-alerts-pending.md)（告警路由）

## 回填验收标准

- [x] 满配章节齐全（对照 [`boss-full-depth-checklist.md`](../governance/boss-full-depth-checklist.md)）
- [x] 明确 Core `/notifications/email/*` 路径已声明
- [x] 含字段展示规则、字段口径与单位、状态与能力口径
- [x] 含响应示例与错误示例（400 + 403 + 422）
- [x] 独立字段定义（SMTP / 收件人 / 订阅）
- [x] `password` / `auth_code` 独立保存语义已声明
- [x] PRD/UX/SPEC 与本文同步
