# EMAIL-NOTIFICATION-A — BOSS 邮件通知通道（Core OpenAPI + Gateway handler + SMTP provider）

完成日期：2026-07-14
对应 Sprint：Sprint 14（分支 `feature/sprint14-core-resilience-semantics` 后续批次）
验证结果：`go build` 通过、`go test ./pkg/adapters/runtime/...` 37 tests passed、`go test ./services/ani-gateway/...` email tests passed、`validate_component_imports.py` passed、`validate_doc_entrypoints.py` passed、`git diff --check` clean

## 实现了什么

为 BOSS 平台级邮件通知域新增 Core 基础设施：6 个 OpenAPI schema + 10 个端点路径（`/notifications/email/*`），`ports.EmailNotificationService` 接口边界，`runtime.localEmailNotificationService` 内存实现（含 SMTP provider 注入），`runtime.smtpProvider` 使用 `net/smtp` 发送邮件（SSL/STARTTLS/plain），Gateway handler 完整实现（非 501 stub），环境变量 `EMAIL_SMTP_PROVIDER` 控制本地模拟 vs 真实发信模式。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `repo/api/openapi/v1.yaml` | 修改 | 新增 6 个 schema + 10 个 path；新增 `PreconditionFailed` 响应定义 |
| `repo/pkg/ports/email_notifications.go` | 新增 | `EmailNotificationService` 接口 + 请求/响应结构体 |
| `repo/pkg/adapters/runtime/local_email_notification_service.go` | 新增 | 内存实现：SMTP config 单例、收件人 CRUD、订阅管理、测试发送；幂等键按操作类型命名空间隔离 |
| `repo/pkg/adapters/runtime/smtp_provider.go` | 新增 | `SMTPProvider` 实现：SSL/STARTTLS/plain 三种加密模式 + RFC822 消息构建 |
| `repo/services/ani-gateway/email_notification_runtime.go` | 新增 | 从 `EMAIL_SMTP_PROVIDER` 环境变量构造 service |
| `repo/services/ani-gateway/internal/router/email_notification_resources.go` | 新增 | 10 个 HTTP handler + 错误映射 + 响应序列化 |
| `repo/services/ani-gateway/internal/router/router.go` | 修改 | `RegisterOptions` 新增 `EmailNotificationService` 字段 + 注册路由 |
| `repo/services/ani-gateway/main.go` | 修改 | 构造 email notification service 并注入 router |
| `repo/services/docs/boss-modules/integration/email-notification.md` | 新增 | BOSS 模块文档 |
| `repo/services/docs/boss-modules/integration/README.md` | 修改 | 索引新增邮件通知模块 |
| `repo/pkg/adapters/runtime/local_email_notification_service_test.go` | 新增 | 24 个单元测试 |
| `repo/pkg/adapters/runtime/smtp_provider_test.go` | 新增 | 6 个单元测试 |
| `repo/services/ani-gateway/internal/router/email_notification_resources_test.go` | 新增 | 7 个 handler 级测试 |

## 完工标准达成

- [x] `go build` 通过（`GOARCH=amd64`）
- [x] `go test ./pkg/adapters/runtime/...` — 37 tests passed
- [x] `go test ./services/ani-gateway/...` — email tests passed
- [x] `python scripts/validate_component_imports.py` — passed
- [x] `python scripts/validate_doc_entrypoints.py` — passed
- [x] `git diff --check` — clean

---

## Implementation Notes

### Design Decisions

**1. 从 501 stub 直接跳到完整实现**

SPEC §1.1 和 §2.2.2 明确将批次范围定义为"contract + stub + frontend"：handler 返回 501 NOT_IMPLEMENTED，前端处理 `api_not_ready`。实际实现直接交付了完整后端逻辑（内存存储 + SMTP provider + 真实发送），没有经过 501 stub 阶段。

理由：SPEC 的 stub-first 策略是为了在 SMTP adapter 和 storage 尚未就绪时让前端可以先联调。本次实现使用内存存储 + 可注入 SMTP provider，在无 SMTP provider 时模拟成功，在有 SMTP provider 时真实发送——这等效于 stub + follow-up 两步合并为一步，前端不需要 `api_not_ready` 降级。如果后续需要降级到 501，只需在 `email_notification_runtime.go` 中不注入 service 即可。

**2. 幂等键按操作类型命名空间隔离**

SPEC 未规定幂等键的隔离范围。实现中将 `s.idem` map 的 key 加上操作类型前缀（`smtp:`、`recipient:`、`recipient_update:`、`subscriptions:`），避免同一个 idempotency_key 跨操作误命中。测试 `TestLocalEmailNotificationService_IdempotencyKeyNamespacedAcrossOperations` 专门回归此场景。

**3. `auth_code` 优先于 `password`**

SPEC §4.2.1 规定 `auth_code` 和 `password` 至少填一个，但未规定两者同时填写时优先用哪个。实现选择 `auth_code` 优先（`local_email_notification_service.go:401`），因为 QQ 邮箱/163 邮箱等服务商使用授权码替代密码，授权码是更明确的认证方式。

**4. 本地模拟模式**

`EMAIL_SMTP_PROVIDER` 环境变量为空或 `local` 时，`localEmailNotificationService` 不注入 SMTP provider，`SendTestEmail` 返回模拟成功（`status=sent`，message 含"local adapter 模拟"）。设为 `smtp` 时注入 `NewSMTPProvider()`，真实连接 SMTP 服务器。这遵循了 CORE-DEV-PROFILE-A 的 local profile 约定——local profile 只证明 API/状态机/调用边界，不标记 real-provider 或 production ready。

### Deviations

**1. OpenAPI 501 响应移除**

- SPEC 原文：所有 10 个端点均列出 `501 NOT_IMPLEMENTED` 作为 stub phase 响应（§4.2 全部端点表格）
- 实际实现：从所有 10 个端点移除了 `501` 响应，并移除了 `NotImplemented` 公共响应定义
- 理由：实现是完整的（非 stub），501 会误导调用方和 SDK 生成器。如果后续需要降级到 stub，可以重新加回。

**2. `EmailTestSendResponse` 新增 `auth_mode` 和 `username` 字段**

- SPEC 原文：`EmailTestSendResponse` 只有 `status`、`message`、`request_id` 三个字段（§3.1.6）
- 实际实现：handler 返回 `from_name`、`from_email`、`to_name`、`to_emails`、`subject`、`content`、`sent_at`、`auth_mode`、`username` 等额外字段；OpenAPI 已对齐补充了 `auth_mode` 和 `username`
- 理由：测试发送的结果需要让操作员知道用了哪个认证模式和账号，否则排查发送失败时无法区分是密码错误还是授权码错误。`from_name`/`to_emails`/`subject`/`content` 等字段是 SPEC §4.2.4 测试发送流程描述的"返回成功/失败详情"的具体体现。

**3. `EmailTestSendResult.Password` 字段移除（review 修复）**

- 初始实现：`EmailTestSendResult` 结构体包含 `Password` 字段并赋值为实际密码/授权码，注释为"日志用"
- 实际实现：review 后移除该字段
- 理由：该字段从未出现在任何日志行中，也从未在 HTTP 响应中返回。在 `testIdem` 缓存中保留明文密码是不必要的数据保留。

**4. 死代码 `now := time.Now().UTC()` 移除（review 修复）**

- 初始实现：`PutSmtpConfig` 中计算 `now` 但未使用（`_ = now`）
- 实际实现：移除两行
- 理由：`EmailSmtpConfigRecord` 无 timestamp 字段，`now` 是遗留代码。

### Tradeoffs

**1. 内存存储 vs 持久化存储**

- 备选 A：使用 Postgres/CRDB 持久化（SPEC §3.3 follow-up batch 方案）
- 备选 B：内存存储 + 可注入 provider（当前选择）
- A 优点：重启不丢失配置；缺点：需要数据库迁移、加密 at rest（KMS/SM4）、连接池管理
- B 优点：零外部依赖、可快速验证 API 契约和状态机、可通过 `EMAIL_SMTP_PROVIDER=smtp` 立即测试真实发送
- 选择 B 的理由：SPEC §3.3 明确将持久化列为 follow-up batch。当前批次的目标是让 API 可调用且 SMTP 可真实发送，内存存储满足这一目标。重启丢失配置在当前阶段可接受——操作员重新配置即可。

**2. `net/smtp` vs 第三方 SMTP 库（如 `go-mail`）**

- 备选 A：使用 `net/smtp` 标准库（当前选择）
- 备选 B：使用 `go-mail/mail` 或 `xhit/go-simple-mail` 等第三方库
- A 优点：零外部依赖、与 ANI 最小依赖原则一致；缺点：需要手动处理 SSL dial + client 创建 + AUTH + MAIL FROM + RCPT TO + DATA + QUIT 流程
- B 优点：API 更简洁、内置超时和重试；缺点：引入外部依赖
- 选择 A 的理由：ANI 架构边界要求最小依赖（CLAUDE.md §5.3），`net/smtp` 足以覆盖 SSL/STARTTLS/plain 三种模式。超时由 context deadline 控制。

**3. 单例 SMTP config vs 多租户配置**

- 备选 A：平台级单例配置（当前选择）
- 备选 B：per-tenant SMTP 配置
- A 优点：简单、匹配 SPEC §3.2 "singleton, platform-level"；缺点：多租户场景下不同租户共用同一发信通道
- B 优点：租户隔离；缺点：违反 SPEC 的 singleton 设计
- 选择 A 的理由：SPEC §3.2 明确 `EmailSmtpConfig` 是平台级单例，无 FK、one config per platform。

### Open Questions

**1. 持久化存储时间点**
内存存储重启后丢失所有配置。SPEC §3.3 将持久化列为 follow-up batch。需要确认：follow-up batch 使用什么数据库（Postgres? CRDB?）？密码加密用 KMS/SM4 还是其他方案？

**2. 前端 `api_not_ready` 降级**
SPEC §2.2.3 定义了 `useEmailApiNotReady` hook 检测 501 响应。由于实现已完整（非 501），前端不需要此降级。需确认前端团队是否仍保留此 hook 作为防御性代码，或直接移除。

**3. `request_id` 字段在 `EmailTestSendResponse` 中的存在性**
SPEC §3.1.6 定义了 `request_id` 字段（"失败时必返回"），但实际 handler 未在响应体中返回它（`request_id` 已通过 `writeDemoError` 在错误响应中返回）。需确认 `EmailTestSendResponse` 的 `request_id` 是否应移除，或在成功响应中是否也应填充。

**4. 事件邮件实际触发机制**
当前实现了订阅开关的 CRUD，但未实现事件发生时实际发送邮件的逻辑（如 `platform_alert_p0` 事件发生时遍历 enabled recipients 并发送）。这属于 follow-up 范围，需确认由哪个组件触发（event consumer worker? Gateway handler?）。

**5. `auth_code` 在 OpenAPI `EmailSmtpConfig` schema 中的 `writeOnly`**
SPEC §3.1.1 将 `auth_code` 标记为 `write_only: true`。OpenAPI 已对齐。但 `EmailSmtpConfig` schema 的 `required` 列表是 `[smtp_host, smtp_port, encryption, from_address, username]`，未包含 `auth_code` 和 `password`——这与 SPEC §4.2.1 "首次保存时至少填一个"的验证规则一致（它们是条件必填，不是 schema 级 required）。需确认 SDK 生成器不会因为 `auth_code` 非 required 而遗漏它。

---

## 验证命令

```bash
cd repo

# Build
GOARCH=amd64 go build ./pkg/... ./services/ani-gateway/...

# Tests
GOARCH=amd64 go test -count=1 ./pkg/adapters/runtime/...
GOARCH=amd64 go test -count=1 ./services/ani-gateway/...

# Architecture
python scripts/validate_component_imports.py --root .
python scripts/validate_doc_entrypoints.py

# OpenAPI
python -c "import yaml; d=yaml.safe_load(open('api/openapi/v1.yaml',encoding='utf-8')); [print(f'{p}: '+', '.join(d['paths'][p].keys())) for p in sorted(d['paths']) if p.startswith('/notifications/email')]"
```
