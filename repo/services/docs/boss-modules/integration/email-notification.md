# 平台邮件通知

## 页面定位

`平台邮件通知` 是 BOSS **平台集成与通知** 域下的 **平台级邮件通知渠道** 专页：配置 SMTP 发信通道、管理全局收件邮箱、订阅平台事件，供 SRE 与平台运营通过邮件接收 **跨租户** 平台事件通知。

**本页 ≠ 企业通知集成。** [`enterprise-notification.md`](enterprise-notification.md) 为平台级 IM Bot（企业微信/钉钉）渠道；本页为 **SMTP 邮件通道**，两者共享事件订阅 enum 但通道配置与收件人独立。

本页属于 **Core / Integration** 视角下的 **平台 RBAC** 页面。一级权威源为 `ANI-main/repo/api/openapi/v1.yaml`，`Notifications` tag 下已声明 `/notifications/email/*` 路径（10 个 operation）。

## 文档管理规则

- 本文是 `平台邮件通知` 的主维护源；页面定位、参数口径、字段映射和验收标准以本文为准
- [`prd-boss-email-notification.md`](../../../tasks/modules/prd/boss/integration/prd-boss-email-notification.md) 与 [`spec-boss-email-notification.md`](../../../tasks/modules/spec/boss/integration/spec-boss-email-notification.md) 为辅助材料，不替代本文
- 如本文与辅助材料冲突，先对照 `v1.yaml`，再统一回写 PRD/SPEC
- 流程：ANI-14 Phase 1 + [`module-delivery-workflow.md`](../governance/module-delivery-workflow.md) + [`boss-full-depth-checklist.md`](../governance/boss-full-depth-checklist.md)
- Sibling 模块：[`enterprise-notification.md`](enterprise-notification.md)（IM Bot 通知）

## Core 层要求

- 平台邮件通知渠道归属 **Core**；`Notifications` tag 下已声明 10 个 operation
- 路径前缀：`/api/v1/notifications/email/*`
- 平台 RBAC 鉴权：`scope:notifications:read`（GET）、`scope:notifications:write`（写操作）
- **不得** 信任 body/query 中未授权的 `tenant_id`；全平台单例配置
- 写操作 PUT/POST/PATCH/DELETE 须 `idempotency_key`（UUID，24 小时内去重）
- 统一错误结构：`{"code":"UPPER_SNAKE","message":"...","request_id":"..."}`
- SMTP 密码字段 `write_only: true`；响应 **不** 回显明文；留空表示不修改已有密码
- 当前 handler 已实现（local adapter + 可选 SMTP provider），非 501 stub
- OpenAPI 已声明 ≠ handler 已实现

## 页面职责

- 提供 SMTP 发信通道配置（平台级单例）
- 管理全局收件邮箱列表（CRUD + 启停）
- 管理事件邮件订阅开关（5 个冻结事件类型）
- 提供 **测试发送** 验证发信通道与收件人可用
- 与 [`enterprise-notification.md`](enterprise-notification.md) 联动：共享事件 enum，IM 与邮件并行通知
- 与 [`platform-alerts-pending.md`](../overview/platform-alerts-pending.md) 联动：P0/P1 告警可通过邮件通知
- 与 [`incident-handling.md`](../health/incident-handling.md) 联动：Incident 创建/升级可触发邮件通知

## 页面结构

- 首屏包含三个子页面（侧边栏「通知设置」下）：
  - **发信设置** — SMTP 配置表单 + 测试发送
  - **收件邮箱** — 收件人表格 + 添加/编辑/删除/启停
  - **事件订阅** — 5 个事件类型开关 + 批量保存

```text
平台邮件通知
├── 发信设置（SMTP 主机/端口/加密/发件人/账号密码 + 测试发送）
├── 收件邮箱（邮箱表格 + 添加/编辑/删除/启停）
└── 事件订阅（5 事件开关 + 批量保存）
    └── 跳转：enterprise-notification / platform-alerts-pending / incident-handling
```

## 数据来源与分层约束

### 数据来源划分

| 层 | 路径 / 模块 | 本页用法 |
|---|---|---|
| Core | `GET/PUT /api/v1/notifications/email/smtp` | SMTP 配置读写 |
| Core | `GET/POST /api/v1/notifications/email/recipients` | 收件邮箱列表/创建 |
| Core | `PATCH/DELETE /api/v1/notifications/email/recipients/{recipient_id}` | 收件邮箱更新/删除 |
| Core | `GET/PUT /api/v1/notifications/email/subscriptions` | 事件订阅列表/保存 |
| Core | `GET /api/v1/notifications/email/events` | 事件类型列表 |
| Core | `POST /api/v1/notifications/email/test` | 测试发送 |
| 产品 | platform-alerts-pending / incident-handling | 通知路由与跳转 |

### 关键边界

- SMTP 配置为 **平台级单例**（非按租户）
- 收件邮箱为 **全局列表**（所有已开启订阅的事件共用）
- 事件订阅为 **固定 5 行**（首期冻结 enum）
- 测试发送前置条件：已配置 SMTP + 至少一个启用中的收件人 → 否则 `422 PRECONDITION_FAILED`
- SMTP 密码 **不** 明文回显（`write_only: true`）
- 收件邮箱 `email` 字段 **唯一约束**（重复 → `409 CONFLICT`）

## 当前冻结事实

| 方法 | 路径 | operationId | 说明 |
|---|---|---|---|
| GET | `/api/v1/notifications/email/smtp` | `getEmailSmtpConfig` | YAML 已声明 |
| PUT | `/api/v1/notifications/email/smtp` | `putEmailSmtpConfig` | YAML 已声明；须 `idempotency_key` |
| GET | `/api/v1/notifications/email/recipients` | `listEmailRecipients` | YAML 已声明 |
| POST | `/api/v1/notifications/email/recipients` | `createEmailRecipient` | YAML 已声明；须 `idempotency_key` |
| PATCH | `/api/v1/notifications/email/recipients/{recipient_id}` | `updateEmailRecipient` | YAML 已声明；须 `idempotency_key` |
| DELETE | `/api/v1/notifications/email/recipients/{recipient_id}` | `deleteEmailRecipient` | YAML 已声明 |
| GET | `/api/v1/notifications/email/subscriptions` | `listEmailSubscriptions` | YAML 已声明 |
| PUT | `/api/v1/notifications/email/subscriptions` | `putEmailSubscriptions` | YAML 已声明；须 `idempotency_key` |
| GET | `/api/v1/notifications/email/events` | `listEmailEvents` | YAML 已声明 |
| POST | `/api/v1/notifications/email/test` | `sendEmailTest` | YAML 已声明；须 `idempotency_key` |

| Schema | 状态 |
|---|---|
| `EmailSmtpConfig` | YAML 已声明 |
| `PutEmailSmtpConfigRequest` | YAML 已声明 |
| `EmailRecipient` | YAML 已声明 |
| `EmailRecipientListResponse` | YAML 已声明 |
| `CreateEmailRecipientRequest` | YAML 已声明 |
| `UpdateEmailRecipientRequest` | YAML 已声明 |
| `EmailSubscription` | YAML 已声明 |
| `EmailSubscriptionListResponse` | YAML 已声明 |
| `PutEmailSubscriptionsRequest` | YAML 已声明 |
| `EmailEventInfo` | YAML 已声明 |
| `EmailEventListResponse` | YAML 已声明 |
| `EmailTestSendRequest` | YAML 已声明 |
| `EmailTestSendResponse` | YAML 已声明 |
| `NotImplemented` response | YAML 已声明 |

### Handler 实现状态

| 状态 | 说明 |
|---|---|
| 已实现 | 所有 10 个 operation 已实现（local adapter + SMTP provider） |
| RBAC | 全局中间件链（Auth → RBAC）在到达 handler 前执行；dev 模式跳过 |
| SMTP 适配器 | 已实现 — `smtp_provider.go`，通过 `EMAIL_SMTP_PROVIDER=smtp` 环境变量启用真实发送 |
| 存储 | local adapter（内存存储，重启丢失）；follow-up batch 将迁移至持久化存储 |
| 邮件发送 worker | 同步发送（测试邮件），异步事件发送有待 follow-up batch |

## 字段级定义

### EmailSmtpConfig

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `smtp_host` | string | 是 | SMTP 服务器主机 |
| `smtp_port` | integer (1-65535) | 是 | SMTP 服务器端口 |
| `encryption` | enum: none / starttls / ssl | 是 | 加密方式 |
| `from_address` | string (email) | 是 | 发件人地址 |
| `username` | string | 否 | 登录账号 |
| `password` | string (write_only) | 否 | 登录密码；仅写入不回显；留空表示不修改 |
| `configured` | boolean (readOnly) | — | 是否已配置（响应字段） |

### EmailRecipient

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `id` | string (uuid, readOnly) | — | 收件人唯一标识 |
| `email` | string (email) | 是 | 收件邮箱地址 |
| `label` | string (nullable, max 128) | 否 | 备注 |
| `enabled` | boolean (default: true) | — | 启用状态 |
| `created_at` | string (date-time, readOnly) | — | 创建时间 |
| `updated_at` | string (date-time, readOnly) | — | 更新时间 |

### EmailSubscription

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `event_type` | enum (见下) | 是 | 事件类型 |
| `enabled` | boolean (default: false) | — | 是否启用邮件通知 |

### 事件类型 enum（首期冻结）

| 值 | 展示名 | 说明 |
|---|---|---|
| `platform_alert_p0` | 平台告警 P0 | 平台级 P0 告警事件 |
| `platform_alert_p1` | 平台告警 P1 | 平台级 P1 告警事件 |
| `incident_created` | Incident 创建 | Incident 创建事件 |
| `incident_escalated` | Incident 升级 | Incident 升级事件 |
| `platform_task_failed` | 平台关键任务失败 | 平台关键任务失败事件 |

### EmailTestSendResponse

| 字段 | 类型 | 说明 |
|---|---|---|
| `status` | enum: sent / failed | 发送状态 |
| `message` | string | 附加信息 |
| `request_id` | string | 请求追踪 ID（失败时必返回） |

## 状态与能力口径

### 状态 × 操作可用性矩阵

| 操作 \ 数据就绪 | stub (501) | handler 已实现 |
|---|---|---|
| GET SMTP 配置 | — | ✅ |
| PUT SMTP 配置 | — | ✅ |
| 列出收件邮箱 | — | ✅ |
| 创建收件邮箱 | — | ✅ |
| 更新收件邮箱 | — | ✅ |
| 删除收件邮箱 | — | ✅ |
| 列出订阅 | — | ✅ |
| 保存订阅 | — | ✅ |
| 列出事件类型 | — | ✅ |
| 测试发送 | — | ✅ |

✅ = handler 已实现并可用。

## 创建前置条件

| 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
|---|---|---|
| 平台 notification 读 RBAC | 已授权 `scope:notifications:read` | `403 FORBIDDEN` |
| 用户已认证 | 平台登录态 | `401 UNAUTHORIZED` |
| 写操作 RBAC | `scope:notifications:write` | `403 FORBIDDEN` |
| `idempotency_key` | 写操作必填 UUID | `400 BAD_REQUEST` |
| SMTP 配置已保存 | `configured=true`（测试发送前置） | `422 PRECONDITION_FAILED` |
| 至少一个启用收件人 | `enabled=true`（测试发送前置） | `422 PRECONDITION_FAILED` |
| 收件邮箱 email 唯一 | 不重复 | `409 CONFLICT` |
| 收件邮箱存在 | PATCH/DELETE 目标存在 | `404 NOT_FOUND` |

## 操作可用性矩阵

| 操作 | 平台只读 | SRE | 平台管理员 | 说明 |
|---|---|---|---|---|
| 查看 SMTP 配置 | ✅ | ✅ | ✅ | GET |
| 保存 SMTP 配置 | ❌ | ✅ | ✅ | PUT |
| 列出收件邮箱 | ✅ | ✅ | ✅ | GET |
| 添加收件邮箱 | ❌ | ✅ | ✅ | POST |
| 编辑/启停收件邮箱 | ❌ | ✅ | ✅ | PATCH |
| 删除收件邮箱 | ❌ | ✅ | ✅ | DELETE |
| 查看订阅 | ✅ | ✅ | ✅ | GET |
| 保存订阅 | ❌ | ✅ | ✅ | PUT |
| 测试发送 | ❌ | ✅ | ✅ | POST |

## 接口冻结规则

### Core · 平台邮件通知（YAML 已声明 · handler stub）

- 须平台 RBAC（`scope:notifications:read/write`）
- 写操作须 `idempotency_key`
- `password` 字段 `write_only: true`
- `501 NOT_IMPLEMENTED` 响应包含 `code` / `message` / `request_id`
- Handler 实现后，501 → 200/201/204 转换无需前端改动（已处理 api_not_ready）

## 删除前置校验

DELETE 收件邮箱 — YAML 已声明。

| 校验项 | 未通过响应 | 说明 |
|---|---|---|
| 收件人存在 | `404 NOT_FOUND` | — |
| 平台写 RBAC | `403 FORBIDDEN` | — |
| 认证 | `401 UNAUTHORIZED` | — |

## 字段展示规则

| 场景 | 展示规则 | 说明 |
|---|---|---|
| 正常态 | 表单/表格/开关 | 依据子页面 |
| 无数据态 | Empty + CTA | 收件邮箱页空态 |
| API 未就绪 (501) | Alert「邮件通知接口尚未就绪」 | 不显示 mock 数据 |
| 无权限态 (403) | Alert/Result 403 | 平台 RBAC |
| 只读态 | 表单/开关 disabled | 有 read 无 write scope |
| SMTP 密码 | placeholder「已配置，留空表示不修改」 | 不回显明文 |
| 收件邮箱状态 | Tag success=启用 / default=停用 | — |
| 测试发送不可用 | 按钮 disabled + Tooltip | 前置条件不满足 |

## 错误响应

| 错误码 | HTTP | 条件 | 用户消息 (zh-CN) |
|---|---|---|---|
| `BAD_REQUEST` | 400 | 输入无效 | 请求参数无效：{details} |
| `UNAUTHORIZED` | 401 | 未认证 | 请先登录 |
| `FORBIDDEN` | 403 | 缺少 RBAC scope | 无权限操作邮件通知配置 |
| `NOT_FOUND` | 404 | 收件人或 SMTP 不存在 | 资源不存在 |
| `CONFLICT` | 409 | 收件邮箱重复 | 收件邮箱已存在 |
| `PRECONDITION_FAILED` | 422 | 测试发送前置不满足 | 请先配置发信通道，并在「收件邮箱」中添加至少一个启用的收件人 |
| `NOT_IMPLEMENTED` | 501 | handler stub | 邮件通知接口尚未实现 |
| `INTERNAL_ERROR` | 500 | 服务器错误 | 内部错误，请稍后重试 |

## BOSS 与 Console 分工

| 维度 | BOSS 平台邮件通知 | Console 租户通知 |
|---|---|---|
| 范围 | 全平台邮件通知 | 当前租户业务通知 |
| 事件源 | 平台告警、Incident、任务失败等 | 租户业务事件 |
| SMTP 配置 | 平台级单例 | 租户级（如有） |
| 收件人 | 全局列表 | 租户级列表 |
| RBAC | `scope:notifications:read/write` | 租户 JWT |
| 路径前缀 | `/api/v1/notifications/email/*` | `/api/v1/svc/...`（如有） |

## 相关模块

- [`enterprise-notification.md`](enterprise-notification.md)（平台 IM Bot 通知 — 兄弟模块）
- [`ops-webhook.md`](ops-webhook.md)（HTTPS 平台出站 Webhook）
- [`platform-alerts-pending.md`](../overview/platform-alerts-pending.md)（告警路由）
- [`incident-handling.md`](../health/incident-handling.md)（Incident 通知路由）
- [`module-delivery-workflow.md`](../governance/module-delivery-workflow.md)（交付流程）

## 待补能力边界

- 存储实现（Postgres/CRDB）— **follow-up batch**（当前为 local adapter 内存存储）
- 邮件发送 worker（异步事件驱动发送）— **follow-up batch**
- 密码加密存储（KMS/SM4）— **follow-up batch**
- 投递日志与失败重试 — **Phase 2**
- 邮件模板编辑 — **Phase 2**
- 事件类型扩展（超过首期 5 个）— **Phase 2**
- 按事件类型单独配置收件人 — **Phase 2**

## 回填验收标准

- [x] 满配章节齐全（对照 [`boss-full-depth-checklist.md`](../governance/boss-full-depth-checklist.md)）
- [x] 明确 Core 路径与 handler 状态（YAML 已声明，handler stub 返回 501）
- [x] 含字段展示规则、字段口径与单位、状态与能力口径
- [x] 含错误响应表（400/401/403/404/409/422/501）
- [x] 独立字段定义（SMTP / Recipient / Subscription / Event / TestSend）
- [x] 删除前置校验已声明
- [x] RBAC scope 已声明（`scope:notifications:read/write`）
- [x] PRD/SPEC 与本文同步
