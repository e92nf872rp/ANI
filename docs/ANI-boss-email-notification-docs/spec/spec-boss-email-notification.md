# SPEC: BOSS 邮件通知

> Technical specification derived from:
> - PRD: `docs/ANI-boss-email-notification-docs/prd/prd-boss-email-notification.md`
> - UX: `docs/ANI-boss-email-notification-docs/ux/ux-boss-email-notification.md`
> Generated: 2026-07-22 | Target branch: `main` | Product line: BOSS + Core

---

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 覆盖 BOSS「通知设置 → 邮件通知」功能的完整技术实现，包括：

- **Core OpenAPI 契约变更**：在 `repo/api/openapi/v1.yaml` 新增 `/notifications/email/*` 路径组与相关 schemas
- **Gateway handler 实现**：在 `repo/services/ani-gateway/` 实现邮件通知 API
- **BOSS 前端实现**：在 `repo/frontends/boss/` 新增 3 个子页（发信设置、收件邮箱、事件订阅）
- **联调验证**：Gateway + BOSS 本地联调，写路径幂等重试验证

本 SPEC 是跨 Core+BOSS 的完整联调批次，非纯 UI 批次。

### 1.2 PRD Reference

- Source: `docs/ANI-boss-email-notification-docs/prd/prd-boss-email-notification.md`
- UX source: `docs/ANI-boss-email-notification-docs/ux/ux-boss-email-notification.md`
- User Stories covered: US-001, US-002, US-003, US-004, US-005
- Functional Requirements covered: FR-1 ~ FR-10

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| API path 风格 | `/api/v1/notifications/email/*` | 顶部 notifications 域 + email 子资源；未来 IM/SMS 可平行扩展为 `/notifications/im/*` |
| RBAC scope | `scope:notifications:read` / `scope:notifications:write` | 对齐 v1.yaml 现有 `x-ani-rbac-scope` 模式（如 `scope:observability:read`） |
| idempotency_key | `Idempotency-Key` header | 对齐 GPU scheduling 队列模式；统一所有写操作 |
| SMTP 凭据存储 | CloudNativePG + 列级加密 | 密钥字段（password / auth_code）加密存储；响应仅返回 `has_password` / `has_auth_code` 布尔位 |
| password 与 auth_code 关系 | 独立保存、独立清除，服务端不强制二选一 | PRD 明确：由用户自行决定使用哪个；发送时 auth_code 优先 |
| 收件人/订阅存储 | CloudNativePG 普通表 | 无密钥，无需加密 |
| 测试发送 | 同步返回结果，无独立投递历史 | PRD Non-Goals：完整投递历史 = P2 |
| 事件订阅模式 | 批量 PUT（非行内 PATCH） | UX §5.3 + §6.3：Switch 批量保存，dirty 才可点 |
| 收件人启停 | 操作列编辑/停用/启用，无行内 Switch | UX §5.2 + §6.2：禁止表格行内 Switch |
| BOSS 前端路由 | 3 子页 + parent redirect | UX §2.1：`/integration/notification-settings/email/{smtp|recipients|subscriptions}` |

---

## 2. Architecture

### 2.1 System Context

```text
BOSS Frontend (repo/frontends/boss/)
  └── 通知设置 → 邮件通知
      ├── 发信设置 (SMTP)      ─┐
      ├── 收件邮箱 (Recipients)  ├──→ Core Client (openapi-fetch)
      └── 事件订阅 (Subscriptions)┘        │
                                            ↓
Core Gateway (repo/services/ani-gateway/)    │
  └── /api/v1/notifications/email/*  ←──────┘
      ├── SMTP config (GET/PUT)
      ├── Recipients CRUD (GET/POST/PATCH/DELETE)
      ├── Subscriptions (GET/PUT)
      └── Test send (POST)
                                            │
                                            ↓
CloudNativePG (encrypted credentials)       │
  └── email_smtp_config (1 row)             │
  └── email_recipients (N rows)             │
  └── email_subscriptions (N rows)    ───────┘
```

### 2.2 Component Design

#### 2.2.1 Core OpenAPI（`repo/api/openapi/v1.yaml`）

新增路径组 `/notifications/email/*`，挂在已有 `Notifications` tag 下。所有写操作通过 `Idempotency-Key` header 传递幂等键。

#### 2.2.2 Gateway Handler（`repo/services/ani-gateway/`）

新增 `internal/router/notifications_email_resources.go`，注册到 `RegisterWithOptions`。Handler 通过 ports 接口调用存储层，不直接依赖 PG driver。

#### 2.2.3 BOSS Frontend（`repo/frontends/boss/`）

新增 3 个路由文件 + 1 个 API 模块 + 1 个共享组件目录：

- `src/routes/integration/notification-settings/email/smtp.tsx`
- `src/routes/integration/notification-settings/email/recipients.tsx`
- `src/routes/integration/notification-settings/email/subscriptions.tsx`
- `src/api/notifications.ts`（封装 coreApi 调用）
- `src/components/notification-email/`（共享组件）

### 2.3 Module Interactions

```text
[BOSS 发信设置页]
  │ GET /notifications/email/smtp
  │ PUT /notifications/email/smtp (Idempotency-Key)
  │ POST /notifications/email/test (Idempotency-Key)
  ↓
[Gateway: notifications_email_resources.go]
  │ 鉴权 (RBAC scope:notifications:read/write)
  │ 幂等校验 (Idempotency-Key header)
  │ 参数校验
  ↓
[Ports: EmailNotificationStore (port)]
  ↓
[Adapter: PG email_notification_store.go]
  │ email_smtp_config (加密列)
  │ email_recipients
  │ email_subscriptions
  ↓
[CloudNativePG]
```

### 2.4 File Structure

**Core OpenAPI（修改）：**
```
repo/api/openapi/v1.yaml                           [MODIFY: 新增 paths + schemas]
```

**Gateway（新增）：**
```
repo/services/ani-gateway/
  internal/
    router/
      notifications_email_resources.go              [NEW: route 注册 + handlers]
      notifications_email_resources_test.go         [NEW: handler tests]
  pkg/ports/
    email_notification_store.go                     [NEW: port 接口]
  pkg/adapters/
    pg/
      email_notification_store.go                   [NEW: PG adapter]
      email_notification_store_test.go              [NEW: adapter tests]
```

**BOSS Frontend（新增）：**
```
repo/frontends/boss/
  src/
    routes/
      integration/
        notification-settings/
          email/
            index.tsx                               [NEW: redirect → /smtp]
            smtp.tsx                                [NEW: 发信设置页]
            recipients.tsx                          [NEW: 收件邮箱页]
            subscriptions.tsx                      [NEW: 事件订阅页]
    api/
      notifications.ts                              [NEW: API 封装]
    components/
      notification-email/
        SmtpForm.tsx                                [NEW: SMTP 表单]
        RecipientDrawer.tsx                         [NEW: 收件人抽屉]
        RecipientTable.tsx                          [NEW: 收件人表格]
        SubscriptionTable.tsx                       [NEW: 订阅表格]
        TestSendButton.tsx                          [NEW: 测试发送按钮]
        ApiNotReadyAlert.tsx                        [NEW: API 未就绪态]
```

**模块主文档（新建）：**
```
repo/services/docs/boss-modules/integration/
  email-notification.md                             [NEW: 模块主文档]
```

---

## 3. Data Model

### 3.1 Schema Changes (CloudNativePG)

#### email_smtp_config（单行配置）

```sql
CREATE TABLE email_smtp_config (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    smtp_host       TEXT NOT NULL,
    smtp_port       INTEGER NOT NULL CHECK (smtp_port >= 1 AND smtp_port <= 65535),
    encryption      TEXT NOT NULL CHECK (encryption IN ('none', 'starttls', 'ssl')),
    from_address    TEXT NOT NULL,
    username        TEXT NOT NULL,
    password_enc    BYTEA,                          -- 加密存储；NULL 表示未设置
    auth_code_enc   BYTEA,                          -- 加密存储；NULL 表示未设置；与 password 独立
    has_password    BOOLEAN GENERATED ALWAYS AS (password_enc IS NOT NULL) STORED,
    has_auth_code   BOOLEAN GENERATED ALWAYS AS (auth_code_enc IS NOT NULL) STORED,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- password_enc 与 auth_code_enc 各自独立加密、独立更新、独立清除。
-- 应用层在发送邮件时按优先级选择：若 auth_code_enc IS NOT NULL 则使用 auth_code，否则使用 password。
```

#### email_recipients（收件人列表）

```sql
CREATE TABLE email_recipients (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email       TEXT NOT NULL,
    label       TEXT,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT email_format CHECK (email ~* '^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$')
);
```

#### email_subscriptions（事件订阅）

```sql
CREATE TABLE email_subscriptions (
    event_type      TEXT PRIMARY KEY,
    description     TEXT NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT false,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 首期冻结事件初始化
INSERT INTO email_subscriptions (event_type, description, enabled) VALUES
    ('platform_alert_p0',  '平台告警 P0',     false),
    ('platform_alert_p1',  '平台告警 P1',     false),
    ('incident_created',   'Incident 创建',   false),
    ('incident_escalated', 'Incident 升级',   false),
    ('platform_task_failed','平台关键任务失败', false);
```

### 3.2 Entity Definitions (Go)

```go
// pkg/ports/email_notification_store.go

type EmailSmtpConfig struct {
    SmtpHost     string
    SmtpPort     int
    Encryption   string // "none" | "starttls" | "ssl"
    FromAddress  string
    Username     string
    HasPassword  bool
    HasAuthCode  bool
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

type EmailSmtpConfigWrite struct {
    SmtpHost    string
    SmtpPort    int
    Encryption  string
    FromAddress string
    Username    string
    Password    *string // nil = 不修改；空字符串 = 清除；非空 = 覆盖（加密存储）
    AuthCode    *string // nil = 不修改；空字符串 = 清除；非空 = 覆盖（加密存储）
    // Password 与 AuthCode 独立处理，互不影响
}

type EmailRecipient struct {
    ID        string
    Email     string
    Label     string
    Enabled   bool
    CreatedAt time.Time
    UpdatedAt time.Time
}

type EmailRecipientWrite struct {
    Email  string
    Label  string
}

type EmailSubscription struct {
    EventType   string
    Description string
    Enabled     bool
    UpdatedAt   time.Time
}

type EmailTestSendResult struct {
    Success   bool
    Message   string
    RequestID string
}

type EmailNotificationStore interface {
    GetSmtpConfig(ctx context.Context) (*EmailSmtpConfig, error)
    PutSmtpConfig(ctx context.Context, idempotencyKey string, cfg EmailSmtpConfigWrite) (*EmailSmtpConfig, error)

    ListRecipients(ctx context.Context) ([]EmailRecipient, error)
    CreateRecipient(ctx context.Context, idempotencyKey string, w EmailRecipientWrite) (*EmailRecipient, error)
    UpdateRecipient(ctx context.Context, id string, w EmailRecipientWrite) (*EmailRecipient, error)
    SetRecipientEnabled(ctx context.Context, id string, enabled bool) (*EmailRecipient, error)
    DeleteRecipient(ctx context.Context, id string) error

    ListSubscriptions(ctx context.Context) ([]EmailSubscription, error)
    PutSubscriptions(ctx context.Context, idempotencyKey string, subs map[string]bool) ([]EmailSubscription, error)

    SendTestEmail(ctx context.Context, idempotencyKey string) (*EmailTestSendResult, error)
}
```

### 3.3 Relationships

- `email_smtp_config`：单行表（应用层保证至多 1 行），存储全局 SMTP 通道
- `email_recipients`：独立表，全局共享；所有已开启订阅的事件发往所有 `enabled=true` 收件人
- `email_subscriptions`：5 行固定枚举（首期），通过 `event_type` 主键标识
- 三表之间无 FK 关系；应用层在 `SendTestEmail` 中组合读取：SMTP config + enabled recipients

### 3.4 Migration Plan

1. 创建 3 张表 + 插入 5 行订阅初始数据
2. SMTP config 表初始为空（GET 返回 empty 态）
3. 回滚：DROP 3 张表（无外部依赖）

---

## 4. API Design

### 4.1 OpenAPI Change Plan (Core)

| Change | Method | Path | operationId | Compatibility | idempotency_key |
|--------|--------|------|-------------|---------------|-----------------|
| 新增 | GET | `/notifications/email/smtp` | `getEmailSmtpConfig` | 新增端点 | N/A |
| 新增 | PUT | `/notifications/email/smtp` | `putEmailSmtpConfig` | 新增端点 | Header |
| 新增 | GET | `/notifications/email/recipients` | `listEmailRecipients` | 新增端点 | N/A |
| 新增 | POST | `/notifications/email/recipients` | `createEmailRecipient` | 新增端点 | Header |
| 新增 | PATCH | `/notifications/email/recipients/{id}` | `updateEmailRecipient` | 新增端点 | Header |
| 新增 | DELETE | `/notifications/email/recipients/{id}` | `deleteEmailRecipient` | 新增端点 | Header |
| 新增 | GET | `/notifications/email/subscriptions` | `listEmailSubscriptions` | 新增端点 | N/A |
| 新增 | PUT | `/notifications/email/subscriptions` | `putEmailSubscriptions` | 新增端点 | Header |
| 新增 | POST | `/notifications/email/test` | `sendTestEmail` | 新增端点 | Header |

**Tags:** 所有操作挂 `Notifications` tag（v1.yaml line 5198 已存在）。

### 4.2 Endpoints Detail

#### 4.2.1 GET /notifications/email/smtp

```yaml
getEmailSmtpConfig:
  operationId: getEmailSmtpConfig
  summary: 获取邮件 SMTP 发信通道配置
  tags: [Notifications]
  x-ani-rbac-scope: "scope:notifications:read"
  responses:
    "200":
      description: SMTP 配置（可能为空态）
      content:
        application/json:
          schema: { $ref: '#/components/schemas/EmailSmtpConfigResponse' }
    "401": { $ref: '#/components/responses/Unauthorized' }
    "403": { $ref: '#/components/responses/Forbidden' }
```

#### 4.2.2 PUT /notifications/email/smtp

```yaml
putEmailSmtpConfig:
  operationId: putEmailSmtpConfig
  summary: 保存邮件 SMTP 发信通道配置
  tags: [Notifications]
  x-ani-rbac-scope: "scope:notifications:write"
  parameters:
    - { name: Idempotency-Key, in: header, required: true, schema: { type: string, format: uuid }, description: "幂等键" }
  requestBody:
    required: true
    content:
      application/json:
        schema: { $ref: '#/components/schemas/PutEmailSmtpConfigRequest' }
  responses:
    "200":
      description: 配置已保存
      content:
        application/json:
          schema: { $ref: '#/components/schemas/EmailSmtpConfigResponse' }
    "400": { $ref: '#/components/responses/BadRequest' }
    "401": { $ref: '#/components/responses/Unauthorized' }
    "403": { $ref: '#/components/responses/Forbidden' }
```

#### 4.2.3 GET /notifications/email/recipients

```yaml
listEmailRecipients:
  operationId: listEmailRecipients
  summary: 列出邮件收件人
  tags: [Notifications]
  x-ani-rbac-scope: "scope:notifications:read"
  responses:
    "200":
      description: 收件人列表
      content:
        application/json:
          schema: { $ref: '#/components/schemas/EmailRecipientListResponse' }
    "401": { $ref: '#/components/responses/Unauthorized' }
    "403": { $ref: '#/components/responses/Forbidden' }
```

#### 4.2.4 POST /notifications/email/recipients

```yaml
createEmailRecipient:
  operationId: createEmailRecipient
  summary: 新增收件人
  tags: [Notifications]
  x-ani-rbac-scope: "scope:notifications:write"
  parameters:
    - { name: Idempotency-Key, in: header, required: true, schema: { type: string, format: uuid } }
  requestBody:
    required: true
    content:
      application/json:
        schema: { $ref: '#/components/schemas/CreateEmailRecipientRequest' }
  responses:
    "201":
      description: 收件人已创建
      content:
        application/json:
          schema: { $ref: '#/components/schemas/EmailRecipient' }
    "400": { $ref: '#/components/responses/BadRequest' }
    "401": { $ref: '#/components/responses/Unauthorized' }
    "403": { $ref: '#/components/responses/Forbidden' }
    "409": { $ref: '#/components/responses/Conflict' }
```

#### 4.2.5 PATCH /notifications/email/recipients/{id}

```yaml
updateEmailRecipient:
  operationId: updateEmailRecipient
  summary: 更新收件人（邮箱地址、备注、启停）
  tags: [Notifications]
  x-ani-rbac-scope: "scope:notifications:write"
  parameters:
    - { name: id, in: path, required: true, schema: { type: string, format: uuid } }
    - { name: Idempotency-Key, in: header, required: true, schema: { type: string, format: uuid } }
  requestBody:
    required: true
    content:
      application/json:
        schema: { $ref: '#/components/schemas/UpdateEmailRecipientRequest' }
  responses:
    "200":
      description: 收件人已更新
      content:
        application/json:
          schema: { $ref: '#/components/schemas/EmailRecipient' }
    "400": { $ref: '#/components/responses/BadRequest' }
    "401": { $ref: '#/components/responses/Unauthorized' }
    "403": { $ref: '#/components/responses/Forbidden' }
    "404": { $ref: '#/components/responses/NotFound' }
```

#### 4.2.6 DELETE /notifications/email/recipients/{id}

```yaml
deleteEmailRecipient:
  operationId: deleteEmailRecipient
  summary: 删除收件人
  tags: [Notifications]
  x-ani-rbac-scope: "scope:notifications:write"
  parameters:
    - { name: id, in: path, required: true, schema: { type: string, format: uuid } }
    - { name: Idempotency-Key, in: header, required: true, schema: { type: string, format: uuid } }
  responses:
    "204":
      description: 收件人已删除
    "401": { $ref: '#/components/responses/Unauthorized' }
    "403": { $ref: '#/components/responses/Forbidden' }
    "404": { $ref: '#/components/responses/NotFound' }
```

#### 4.2.7 GET /notifications/email/subscriptions

```yaml
listEmailSubscriptions:
  operationId: listEmailSubscriptions
  summary: 列出邮件事件订阅
  tags: [Notifications]
  x-ani-rbac-scope: "scope:notifications:read"
  responses:
    "200":
      description: 订阅列表（固定 5 行）
      content:
        application/json:
          schema: { $ref: '#/components/schemas/EmailSubscriptionListResponse' }
    "401": { $ref: '#/components/responses/Unauthorized' }
    "403": { $ref: '#/components/responses/Forbidden' }
```

#### 4.2.8 PUT /notifications/email/subscriptions

```yaml
putEmailSubscriptions:
  operationId: putEmailSubscriptions
  summary: 批量保存邮件事件订阅
  tags: [Notifications]
  x-ani-rbac-scope: "scope:notifications:write"
  parameters:
    - { name: Idempotency-Key, in: header, required: true, schema: { type: string, format: uuid } }
  requestBody:
    required: true
    content:
      application/json:
        schema: { $ref: '#/components/schemas/PutEmailSubscriptionsRequest' }
  responses:
    "200":
      description: 订阅已保存
      content:
        application/json:
          schema: { $ref: '#/components/schemas/EmailSubscriptionListResponse' }
    "400": { $ref: '#/components/responses/BadRequest' }
    "401": { $ref: '#/components/responses/Unauthorized' }
    "403": { $ref: '#/components/responses/Forbidden' }
```

#### 4.2.9 POST /notifications/email/test

```yaml
sendTestEmail:
  operationId: sendTestEmail
  summary: 发送测试邮件
  tags: [Notifications]
  x-ani-rbac-scope: "scope:notifications:write"
  parameters:
    - { name: Idempotency-Key, in: header, required: true, schema: { type: string, format: uuid } }
  responses:
    "200":
      description: 测试发送结果
      content:
        application/json:
          schema: { $ref: '#/components/schemas/SendTestEmailResponse' }
    "401": { $ref: '#/components/responses/Unauthorized' }
    "403": { $ref: '#/components/responses/Forbidden' }
    "422":
      description: 前置条件不满足（未配置通道或无启用收件人）
      content:
        application/json:
          schema: { $ref: '#/components/schemas/ErrorResponse' }
```

### 4.3 Request/Response Schemas

#### EmailSmtpConfigResponse

```yaml
EmailSmtpConfigResponse:
  type: object
  properties:
    configured:
      type: boolean
      description: 是否已配置（false = 空态）
    smtp_host:
      type: string
    smtp_port:
      type: integer
    encryption:
      type: string
      enum: [none, starttls, ssl]
    from_address:
      type: string
      format: email
    username:
      type: string
    has_password:
      type: boolean
      description: 是否已设置密码（明文不回显）
    has_auth_code:
      type: boolean
      description: 是否已设置授权码（明文不回显）
    created_at:
      type: string
      format: date-time
    updated_at:
      type: string
      format: date-time
  required: [configured]
```

> `configured=false` 时其他字段可省略；`password` / `auth_code` 明文**永不**出现在响应中。

#### PutEmailSmtpConfigRequest

```yaml
PutEmailSmtpConfigRequest:
  type: object
  required: [smtp_host, smtp_port, encryption, from_address, username]
  properties:
    smtp_host:
      type: string
      minLength: 1
    smtp_port:
      type: integer
      minimum: 1
      maximum: 65535
    encryption:
      type: string
      enum: [none, starttls, ssl]
    from_address:
      type: string
      format: email
    username:
      type: string
    password:
      type: string
      format: password
      description: |
        SMTP 登录密码（企业邮箱场景）。
        - 省略或 null：不修改已有密码
        - 空字符串 ""：清除已有密码
        - 非空字符串：加密后覆盖
        明文永不回显；与 auth_code 独立保存，不互斥。
    auth_code:
      type: string
      format: password
      description: |
        SMTP 授权码（QQ/163/Gmail 等国内邮箱服务商 SMTP 授权登录）。
        - 省略或 null：不修改已有授权码
        - 空字符串 ""：清除已有授权码
        - 非空字符串：加密后覆盖
        明文永不回显；与 password 独立保存，不互斥。
        发送邮件时若 auth_code 已设置则优先使用 auth_code，否则使用 password。
  additionalProperties: false
```

> **password 与 auth_code 的独立语义：** 两者各自独立写入、独立清除、独立回显布尔位。服务端不强制二选一。用户可同时保留两个凭据，但发送邮件时按 auth_code 优先级选择。

#### EmailRecipient

```yaml
EmailRecipient:
  type: object
  properties:
    id:
      type: string
      format: uuid
    email:
      type: string
      format: email
    label:
      type: string
      nullable: true
    enabled:
      type: boolean
    created_at:
      type: string
      format: date-time
    updated_at:
      type: string
      format: date-time
  required: [id, email, enabled, created_at, updated_at]
```

#### EmailRecipientListResponse

```yaml
EmailRecipientListResponse:
  type: object
  properties:
    items:
      type: array
      items: { $ref: '#/components/schemas/EmailRecipient' }
    total:
      type: integer
  required: [items, total]
```

#### CreateEmailRecipientRequest

```yaml
CreateEmailRecipientRequest:
  type: object
  required: [email]
  properties:
    email:
      type: string
      format: email
    label:
      type: string
  additionalProperties: false
```

#### UpdateEmailRecipientRequest

```yaml
UpdateEmailRecipientRequest:
  type: object
  properties:
    email:
      type: string
      format: email
    label:
      type: string
      nullable: true
    enabled:
      type: boolean
  additionalProperties: false
```

#### EmailSubscription

```yaml
EmailSubscription:
  type: object
  properties:
    event_type:
      type: string
      enum:
        - platform_alert_p0
        - platform_alert_p1
        - incident_created
        - incident_escalated
        - platform_task_failed
    description:
      type: string
    enabled:
      type: boolean
    updated_at:
      type: string
      format: date-time
  required: [event_type, description, enabled, updated_at]
```

#### EmailSubscriptionListResponse

```yaml
EmailSubscriptionListResponse:
  type: object
  properties:
    items:
      type: array
      items: { $ref: '#/components/schemas/EmailSubscription' }
    total:
      type: integer
  required: [items, total]
```

#### PutEmailSubscriptionsRequest

```yaml
PutEmailSubscriptionsRequest:
  type: object
  required: [subscriptions]
  properties:
    subscriptions:
      type: array
      items:
        type: object
        required: [event_type, enabled]
        properties:
          event_type:
            type: string
            enum:
              - platform_alert_p0
              - platform_alert_p1
              - incident_created
              - incident_escalated
              - platform_task_failed
          enabled:
            type: boolean
      minItems: 1
  additionalProperties: false
```

#### SendTestEmailResponse

```yaml
SendTestEmailResponse:
  type: object
  properties:
    success:
      type: boolean
    message:
      type: string
      description: 成功或失败的可读信息
    request_id:
      type: string
      description: 请求 ID（用于排障）
    sent_at:
      type: string
      format: date-time
      nullable: true
  required: [success, message, request_id]
```

### 4.4 Error Responses

所有错误响应使用 v1.yaml 既有 `ErrorResponse` schema：

```yaml
ErrorResponse:
  type: object
  required: [code, message, request_id]
  properties:
    code:
      type: string
      description: UPPER_SNAKE 错误码
    message:
      type: string
    request_id:
      type: string
    details:
      type: object
      additionalProperties: true
```

### 4.5 Breaking Changes

**无破坏性变更。** 所有端点均为新增，不影响现有 API。Core API v1 允许新增可选字段和端点。

---

## 5. Business Logic

### 5.1 Core Algorithms

#### 5.1.1 SMTP 配置保存（PUT /notifications/email/smtp）

```text
1. 鉴权：RBAC scope:notifications:write
2. 幂等：Idempotency-Key header 必填；空则 400
3. 参数校验：
   - smtp_host 非空
   - smtp_port ∈ [1, 65535]
   - encryption ∈ {none, starttls, ssl}
   - from_address 合法 email
   - username 非空
4. 密钥处理：
   - password 字段：
     - 不存在或 null → 不修改已有密码
     - 空字符串 "" → 清除已有密码
     - 非空字符串 → 加密后覆盖
   - auth_code 字段（与 password 平行处理）：
     - 不存在或 null → 不修改已有授权码
     - 空字符串 "" → 清除已有授权码
     - 非空字符串 → 加密后覆盖
   - password 与 auth_code 可同时写入、同时保留；服务端不强制二选一
5. Upsert：若 email_smtp_config 无行则 INSERT，有行则 UPDATE
6. 返回 EmailSmtpConfigResponse（has_password / has_auth_code 布尔位；明文永不回显）
```

> **auth_code 与 password 的独立性：** 两者各自独立写入与保存，互不影响。留空表示不修改，空字符串表示清除，非空表示覆盖。发送邮件时优先使用 auth_code（若已设置），否则使用 password。

#### 5.1.2 测试发送（POST /notifications/email/test）

```text
1. 鉴权：RBAC scope:notifications:write
2. 幂等：Idempotency-Key header
3. 前置条件检查：
   a. 读取 email_smtp_config；不存在或未完整 → 422
      { code: "PRECONDITION_FAILED", message: "SMTP 通道未配置" }
   b. 读取 enabled=true 的收件人列表；为空 → 422
      { code: "PRECONDITION_FAILED", message: "无启用的收件人" }
   c. 检查凭据：has_password=false 且 has_auth_code=false → 422
      { code: "PRECONDITION_FAILED", message: "SMTP 凭据未配置（password 或 auth_code 至少设置一个）" }
4. 构造测试邮件：
   - From: smtp_config.from_address
   - To: 所有 enabled=true 收件人
   - Subject: "[ANI 测试] 邮件通知通道验证"
   - Body: 纯文本测试内容（含 request_id）
5. 发送：
   - 根据 encryption 选择 STARTTLS / SSL / 明文
   - 使用 smtp_host:smtp_port + username + 凭据登录
   - 凭据选择优先级：若 has_auth_code=true 则使用 auth_code，否则使用 password
   - 发送邮件
6. 返回 SendTestEmailResponse：
   - success=true：message "测试邮件已发送"
   - success=false：message 为 SMTP 错误摘要
7. 不记录投递历史（P2 范围）
```

#### 5.1.3 订阅批量保存（PUT /notifications/email/subscriptions）

```text
1. 鉴权 + 幂等同上
2. 校验：每个 event_type 必须在枚举内
3. 批量 UPSERT：对每个 (event_type, enabled) 对更新 email_subscriptions 表
4. 返回完整列表（5 行）
```

### 5.2 Validation Rules

| 字段 | 规则 | 失败码 |
|------|------|--------|
| Idempotency-Key | 必填 UUID | `400 BAD_REQUEST` |
| smtp_host | 非空字符串 | `400 BAD_REQUEST` |
| smtp_port | 1-65535 | `400 BAD_REQUEST` |
| encryption | `none` / `starttls` / `ssl` | `400 BAD_REQUEST` |
| from_address | 合法 email 格式 | `400 BAD_REQUEST` |
| username | 非空 | `400 BAD_REQUEST` |
| email（收件人） | 合法 email 格式 | `400 BAD_REQUEST` |
| event_type | 在枚举内 | `400 BAD_REQUEST` |
| recipient id | 存在 | `404 NOT_FOUND` |
| 测试发送前置 | SMTP 已配置 + ≥1 启用收件人 + (password 或 auth_code 至少一个已设置) | `422 PRECONDITION_FAILED` |

### 5.3 State Machine

#### SMTP 配置状态

```text
[empty] ──PUT──→ [configured]
[configured] ──PUT──→ [configured]（更新）
```

#### 收件人状态

```text
[created] ──PATCH enabled=false──→ [disabled]
[disabled] ──PATCH enabled=true──→ [enabled]
[*] ──DELETE──→ [deleted]
```

#### 订阅状态

```text
[off] ──PUT enabled=true──→ [on]
[on] ──PUT enabled=false──→ [off]
```

### 5.4 Edge Cases

| 场景 | 处理 |
|------|------|
| SMTP 配置未保存时 GET | 返回 `{ configured: false }`，HTTP 200 |
| 编辑 SMTP 时 password 留空 | 不覆盖原密码（保持 has_password=true） |
| 编辑 SMTP 时 password="" | 清除密码（has_password=false） |
| 编辑 SMTP 时 password="new" | 覆盖为新密码 |
| 编辑 SMTP 时 auth_code 留空 | 不覆盖原授权码（保持 has_auth_code=true） |
| 编辑 SMTP 时 auth_code="" | 清除授权码（has_auth_code=false） |
| 编辑 SMTP 时 auth_code="new" | 覆盖为新授权码 |
| 编辑 SMTP 时同时修改 password 和 auth_code | 两者各自独立加密保存；响应同时返回 has_password=true + has_auth_code=true |
| 编辑 SMTP 时仅修改 auth_code，password 留空 | password 保持原值不变，auth_code 更新为新值 |
| 编辑 SMTP 时仅修改 password，auth_code 留空 | auth_code 保持原值不变，password 更新为新值 |
| password 与 auth_code 同时填写 | 服务端接受，各自独立加密保存；发送时优先使用 auth_code |
| password 与 auth_code 均未设置时测试发送 | 422 PRECONDITION_FAILED，message "SMTP 凭据未配置" |
| 仅设置 auth_code（QQ/163 场景） | 发送时使用 auth_code 登录，password 不参与 |
| 仅设置 password（企业邮箱场景） | 发送时使用 password 登录，auth_code 不参与 |
| 收件人 email 重复 | 允许（不做唯一约束；用户自行管理） |
| 订阅列表 GET 时表为空 | 返回 5 行默认 false（由 migration 初始化） |
| 测试发送时 SMTP 连接超时 | success=false，message 含超时信息 |
| 测试发送时认证失败 | success=false，message 含 SMTP 错误码 |
| 无权限用户访问 | 403 FORBIDDEN |
| API 未就绪（handler 未实现） | 501 NOT_IMPLEMENTED（使用 notImplemented stub） |

---

## 6. Error Handling

### 6.1 Error Taxonomy

| Error Code | HTTP Status | Condition | User Message |
|------------|-------------|-----------|--------------|
| `BAD_REQUEST` | 400 | 参数校验失败 | 具体字段错误 |
| `UNAUTHORIZED` | 401 | 未登录 | 未登录或令牌过期 |
| `FORBIDDEN` | 403 | 无 RBAC 权限 | 权限不足 |
| `NOT_FOUND` | 404 | 收件人 id 不存在 | 收件人不存在 |
| `CONFLICT` | 409 | 幂等重放 | 重复请求（Idempotent-Replay header） |
| `PRECONDITION_FAILED` | 422 | 测试发送前置不满足 | SMTP 未配置或无启用收件人 |
| `NOT_IMPLEMENTED` | 501 | handler 未实现 | 接口尚未就绪 |
| `INTERNAL_ERROR` | 500 | 内部错误 | 系统内部错误 |

### 6.2 Retry Strategy

- **幂等写操作**：客户端可安全重试，复用同一 `Idempotency-Key`；服务端返回 409 + Idempotent-Replay header 表示重放
- **测试发送**：可重试（幂等键保护）；但实际副作用是发送邮件，重试可能发多封（本轮接受，P2 可改进）
- **读操作**：客户端可自由重试

### 6.3 Failure Modes

| 依赖失败 | 系统行为 |
|----------|---------|
| PG 不可用 | 返回 500 INTERNAL_ERROR |
| 加密服务不可用 | 密钥写入失败，返回 500 |
| SMTP 服务器不可达 | 测试发送返回 200 + success=false，message 含错误 |
| SMTP 认证失败 | 测试发送返回 200 + success=false，message 含 SMTP 错误码 |

---

## 7. Security

### 7.1 Authentication & Authorization

| 操作 | RBAC scope | 说明 |
|------|-----------|------|
| GET smtp / recipients / subscriptions | `scope:notifications:read` | 只读 |
| PUT smtp / POST/PATCH/DELETE recipients / PUT subscriptions / POST test | `scope:notifications:write` | 写操作 |
| 无权限 | 403 FORBIDDEN | 不信任 body 伪造身份 |

### 7.2 Input Validation

- 所有入参经 OpenAPI schema 校验（`required`、`format`、`enum`、`minLength`、`minimum/maximum`）
- email 字段经服务端正则二次校验
- `Idempotency-Key` header 必填且为 UUID 格式
- `additionalProperties: false` 防止未声明字段注入

### 7.3 Data Protection

| 字段 | 保护措施 |
|------|---------|
| `password` | 写入时列级加密存储；响应仅返回 `has_password` 布尔位；明文永不回显 |
| `auth_code` | 写入时列级加密存储；响应仅返回 `has_auth_code` 布尔位；明文永不回显；与 password 独立保存，不互斥 |
| `from_address` | 不脱敏（非敏感字段） |
| `username` | 不脱敏 |
| `webhook_url`（IM 对照） | 不在本 SPEC 范围 |

- 传输层：API 统一 HTTPS（`https://{host}/api/v1`）
- 审计：所有写操作经 Gateway Audit middleware 记录

---

## 8. Performance

### 8.1 Expected Load

- 平台管理员规模：个位数用户
- 配置操作频率：低（初始配置 + 偶尔调整）
- 测试发送频率：极低（上线前验证）
- 收件人数量：十级
- 订阅数量：5 行固定

**结论：** 无性能瓶颈，无需缓存或批量优化。

### 8.2 Optimization Strategy

- GET recipients 无分页（首期量小，<100 行）；P2 可加分页
- GET subscriptions 返回 5 行固定，无分页
- GET smtp 返回单行配置

### 8.3 Database Considerations

- `email_recipients` 表无索引需求（全表扫描 <100 行）
- `email_subscriptions` 表 PK 为 `event_type`（5 行）
- `email_smtp_config` 单行表，无索引需求

---

## 9. Testing Strategy

### 9.1 Unit Tests

#### Gateway Handler Tests（`notifications_email_resources_test.go`）

| 测试 | 覆盖 |
|------|------|
| `TestGetEmailSmtpConfig_Empty` | 未配置时返回 configured=false |
| `TestGetEmailSmtpConfig_Configured` | 已配置时返回完整字段 + has_password=true |
| `TestPutEmailSmtpConfig_New` | 首次保存 INSERT |
| `TestPutEmailSmtpConfig_UpdatePassword` | 更新密码，has_password 变化 |
| `TestPutEmailSmtpConfig_UpdateAuthCode` | 更新授权码，has_auth_code 变化 |
| `TestPutEmailSmtpConfig_KeepPassword` | password 留空，不修改原密码 |
| `TestPutEmailSmtpConfig_KeepAuthCode` | auth_code 留空，不修改原授权码 |
| `TestPutEmailSmtpConfig_ClearPassword` | password="" 清除密码 |
| `TestPutEmailSmtpConfig_ClearAuthCode` | auth_code="" 清除授权码 |
| `TestPutEmailSmtpConfig_BothCredentials` | 同时写入 password + auth_code，两者独立保存 |
| `TestPutEmailSmtpConfig_OnlyAuthCode` | 仅写入 auth_code（QQ/163 场景），password 保持 null |
| `TestPutEmailSmtpConfig_NoIdempotencyKey` | 缺 header → 400 |
| `TestPutEmailSmtpConfig_InvalidPort` | port 越界 → 400 |
| `TestListEmailRecipients_Empty` | 空列表 |
| `TestListEmailRecipients_WithItems` | 返回列表 |
| `TestCreateEmailRecipient_Valid` | 成功创建 |
| `TestCreateEmailRecipient_InvalidEmail` | 非法 email → 400 |
| `TestUpdateEmailRecipient_NotFound` | id 不存在 → 404 |
| `TestDeleteEmailRecipient_Success` | 204 |
| `TestListEmailSubscriptions_Default` | 5 行 false |
| `TestPutEmailSubscriptions_BatchUpdate` | 批量更新 |
| `TestPutEmailSubscriptions_InvalidEventType` | 枚举外 → 400 |
| `TestSendTestEmail_NoSmtp` | 422 PRECONDITION_FAILED |
| `TestSendTestEmail_NoRecipients` | 422 |
| `TestSendTestEmail_NoCredentials` | 422（password 和 auth_code 均未设置） |
| `TestSendTestEmail_PriorityAuthCode` | 已设置 auth_code 时优先使用 auth_code 登录 |
| `TestSendTestEmail_Success` | 200 + success=true |
| `TestSendTestEmail_SmtpError` | 200 + success=false |
| `TestAll_Unauthorized` | 401 |
| `TestAll_Forbidden` | 403 |

#### Adapter Tests（`email_notification_store_test.go`）

| 测试 | 覆盖 |
|------|------|
| `TestStore_PutGetSmtpConfig` | 写入后读取一致 |
| `TestStore_PasswordEncryption` | 密钥加密存储，读取不回显明文 |
| `TestStore_AuthCodeEncryption` | 授权码加密存储，读取不回显明文 |
| `TestStore_BothCredentialsIndependent` | password 与 auth_code 独立保存、独立清除 |
| `TestStore_RecipientCRUD` | 完整 CRUD |
| `TestStore_SubscriptionBatchUpdate` | 批量更新 |
| `TestStore_IdempotentReplay` | 同一幂等键重放返回相同结果 |

### 9.2 Integration Tests

| 测试 | 覆盖 |
|------|------|
| Gateway → PG → SMTP mock | 端到端 SMTP 配置 → 测试发送 |
| 幂等重放 | 同一 Idempotency-Key 两次请求返回相同结果 |
| RBAC 鉴权 | 无 scope 用户 403 |

### 9.3 Edge Case Tests

见 Section 5.4 所有 edge cases 均需测试覆盖。

### 9.4 Acceptance Criteria Mapping

| US/FR | Test | Type | Description |
|-------|------|------|-------------|
| US-001 AC1 | `TestPutEmailSmtpConfig_*` | unit | SMTP 字段保存 |
| US-001 AC2 | `TestPutEmailSmtpConfig_UpdatePassword` + `KeepPassword` | unit | 密码写入不回显 |
| US-001 AC3 | `TestPutEmailSmtpConfig_Success` | unit | 保存成功反馈 |
| US-001 AC4 | `TestPutEmailSmtpConfig_Forbidden` | unit | 无权限拦截 |
| US-001 AC5 | `TestPutEmailSmtpConfig_IdempotencyKey` | unit | 幂等键 |
| US-001 AC6 | `pnpm type-check && pnpm lint` | gate | 前端门禁 |
| US-001 AC7 | 浏览器验证 loading/empty/error | manual | 三态 |
| US-002 AC1 | `TestCreateEmailRecipient_*` + `TestUpdateEmailRecipient_*` | unit | 收件人 CRUD |
| US-002 AC2 | `TestListEmailRecipients_WithItems` | unit | 列表展示 |
| US-002 AC3 | `TestListEmailRecipients_Empty` | unit | 空态 |
| US-002 AC4 | 订阅 GET + recipients GET 共用全局列表 | integration | 全局收件人 |
| US-002 AC5-6 | type-check + 浏览器 | gate/manual | — |
| US-003 AC1 | `TestListEmailSubscriptions_Default` | unit | 5 行冻结枚举 |
| US-003 AC2 | `TestPutEmailSubscriptions_BatchUpdate` | unit | 批量保存 |
| US-003 AC3 | `TestPutEmailSubscriptions_PersistOnRefresh` | unit | 持久化 |
| US-003 AC4-5 | type-check + 浏览器 | gate/manual | — |
| US-004 AC1 | `TestSendTestEmail_Success` | unit | 前置满足可发送 |
| US-004 AC2 | `TestSendTestEmail_NoSmtp` + `NoRecipients` | unit | 前置不满足 422 |
| US-004 AC3 | `TestSendTestEmail_FailureMessage` | unit | 错误反馈含 message + request_id |
| US-004 AC4 | 无投递历史测试 | — | 本轮不实现 |
| US-004 AC5-6 | type-check + 浏览器 | gate/manual | — |
| US-005 AC1 | OpenAPI diff + `make validate-architecture` | gate | 契约对齐 |
| US-005 AC2 | 代码审查 + 架构门禁 | gate | BOSS 通过 Core Client |
| US-005 AC3 | 本地联调 | manual | Gateway + BOSS |
| US-005 AC4 | `make test` + `make validate-architecture` | gate | 门禁通过 |
| FR-1 | 3 子页路由存在 | integration | 页面可达 |
| FR-2 | `TestPutEmailSmtpConfig_Encryption` | unit | STARTTLS/SSL |
| FR-3 | `TestGetEmailSmtpConfig_NoPlaintext` | unit | 密钥不回显 |
| FR-4 | `TestStore_PasswordEncryption` + `AuthCodeEncryption` | unit | 加密存储 |
| FR-5 | `TestRecipientCRUD` + `SetEnabled` | unit | 收件人增删改启停 |
| FR-6 | `TestSubscriptionBatchUpdate` | unit | 订阅开关持久化 |
| FR-7 | `TestSendTestEmail_Preconditions` | unit | 测试发送前置校验 |
| FR-8 | 403 返回 + BOSS 展示 | unit/manual | RBAC |
| FR-9 | 501 返回 + BOSS Alert | unit/manual | API 未就绪态 |
| FR-10 | 所有写操作 Idempotency-Key | unit | 幂等 |

---

## 10. Implementation Plan

### 10.1 Phases

```text
Phase A: Core OpenAPI 契约（Issue #1）
  └── v1.yaml 新增 paths + schemas
  └── make gen-api 重新生成类型
  └── make validate-architecture 通过

Phase B: Gateway handler 实现（Issue #2）
  └── ports/email_notification_store.go
  └── adapters/pg/email_notification_store.go
  └── router/notifications_email_resources.go
  └── 单元测试 + 集成测试
  └── make test 通过

Phase C: BOSS 前端实现（Issue #3-#6，可并行）
  ├── C1: 发信设置页（Issue #3）
  ├── C2: 收件邮箱页（Issue #4）
  ├── C3: 事件订阅页（Issue #5）
  └── C4: 测试发送（Issue #6，依赖 C1+C2）

Phase D: 联调验证
  └── Gateway + BOSS 本地联调
  └── 幂等重试验证
  └── 浏览器三态验证
  └── make test + make validate-architecture 通过
```

### 10.2 Issue Mapping

| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| #1: Core OpenAPI — 邮件通知 paths 与 schemas | 2.4, 3, 4 | high | — |
| #2: Gateway handler — 邮件通知 API 实现 | 2.3, 2.4, 5, 6, 9 | high | #1 |
| #3: BOSS 发信设置页（SMTP 通道配置） | 2.2.3, 2.4 (frontend), UX §4.1, §5.1, §6.1 | high | #1, #2 |
| #4: BOSS 收件邮箱页 | 2.2.3, 2.4 (frontend), UX §4.2, §5.2, §6.2 | high | #1, #2 |
| #5: BOSS 事件订阅页 | 2.2.3, 2.4 (frontend), UX §4.3, §5.3, §6.3 | medium | #1, #2 |
| #6: BOSS 测试发送 | 2.2.3, 2.4 (frontend), UX §3.2, §6.4 | medium | #3, #4 |

### 10.3 Incremental Delivery

1. **Phase A** 完成后即可 `make gen-api` 生成类型，BOSS 前端可基于类型开始开发（mock 或 notImplemented stub）
2. **Phase B** 完成后可本地 curl 验证 API
3. **Phase C** 可并行开发 3 个子页（#3, #4, #5），#6 依赖 #3+#4
4. **Phase D** 联调验证

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions

- **SMTP 加密列实现方式：** 使用 CloudNativePG 的 `pgcrypto` 扩展还是应用层加密（如 AES-GCM）？建议应用层加密以解耦 PG 扩展依赖，具体加密密钥管理方式（KMS / 配置文件）待确认。
- **SMTP 发送库选择：** Go 标准库 `net/smtp` 还是第三方库（如 `gomail`）？建议标准库以避免新依赖，但 STARTTLS 支持需手动实现。
- **事件枚举与现有平台事件的映射：** `platform_alert_p0` 等事件是否已有对应的 incident/alert 内部事件流？本轮只管订阅开关，实际事件触发与投递由其他模块负责。

### 11.2 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|-----------|
| SMTP 服务器兼容性 | 测试发送失败 | 支持 STARTTLS/SSL/明文三种模式；错误 message 透传 SMTP 错误码 |
| 密钥加密密钥管理 | 密钥泄露 | 使用平台 KMS 或配置文件；不在代码中硬编码 |
| 幂等键存储 | 重放判断失败 | 使用 PG 表存储幂等键（可复用现有幂等中间件） |
| BOSS 前端骨架未落地 | 路由/组件缺失 | Phase C 可基于 Issue #12（BOSS 前端骨架）已建立的 Layout + TanStack Router |

### 11.3 Assumptions

- BOSS 前端骨架（`repo/frontends/boss/`）已由 Issue #12 建立，包括 TDesign Layout + TanStack Router + coreClient + gen-core-schema 脚本
- CloudNativePG 可用且支持 `pgcrypto` 或应用层加密
- Gateway 中间件链（RequestID → Auth → RBAC → RateLimit → Idempotency → Audit）可复用
- `x-ani-rbac-scope` 扩展已被 RBAC middleware 识别
- 测试发送使用 `net/smtp` 标准库，不引入第三方 SMTP 库
- 事件触发与实际邮件投递由其他模块负责（本 SPEC 只管配置与测试发送）
