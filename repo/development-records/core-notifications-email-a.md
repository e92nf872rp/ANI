# CORE-NOTIFICATIONS-EMAIL-A — 平台邮件通知 Core API 契约 + local adapter 落地

完成日期：2026-07-20
对应 Sprint：Sprint 13（Core real provider 与 live gate 收敛期间，Core API additive 批次）
验证结果：
- `go build ./pkg/ports/... ./pkg/adapters/runtime/... ./pkg/bootstrap/... ./services/ani-gateway/...` PASS
- `go test ./pkg/adapters/runtime/... -run TestLocalNotification` 7 tests PASS
- `go test ./services/ani-gateway/internal/router/... -run TestNotificationAPI` 6 tests PASS
- `python scripts/validate_component_imports.py --root .` PASS（component import guard passed）
- `python scripts/validate_openapi_spec.py` PASS
- `python scripts/validate_spec_split_contract.py` PASS
- `git diff --check` 无空白错误
- `gofmt -l` 无未格式化文件

## 实现了什么

为 BOSS 平台侧邮件通知能力补齐 Core API 契约（已在前一 commit `5ad5c07` 完成）并落地 Tier1 local profile 实现：新增 `ports.NotificationService` port、`runtime.LocalNotificationService` local adapter、Gateway handler 与路由注册，使 `/api/v1/notifications/email/*` 与 `/api/v1/notifications/events` 在 Gateway 启动时可被调用。所有响应带 `dev_profile`，`real_provider=false`，不声明 runtime/production ready。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/notifications.go` | 新增 | `NotificationService` 接口 + 请求/记录/响应结构体（11 方法） |
| `pkg/adapters/runtime/local_notification_service.go` | 新增 | `LocalNotificationService` 内存实现：单例通道、收件人 CRUD、订阅 upsert、事件目录、测试发送前置条件 |
| `pkg/adapters/runtime/local_notification_service_test.go` | 新增 | 7 个测试覆盖通道幂等/密码 write-only、收件人 CRUD、事件目录、订阅 upsert、测试发送 422 分支 |
| `pkg/bootstrap/deps.go` | 修改 | `Capabilities` 新增 `NotificationService ports.NotificationService` 字段；`NewCapabilitiesWithConfig` 末尾构造 `NewLocalNotificationService()` |
| `services/ani-gateway/internal/router/notifications_resources.go` | 新增 | 6 个 path 的 handler + 路由注册 + 错误映射（404/409/422/400） |
| `services/ani-gateway/internal/router/notifications_resources_test.go` | 新增 | 6 个 handler 测试（API 层契约） |
| `services/ani-gateway/internal/router/router.go` | 修改 | `RegisterOptions` 新增 `NotificationService` 字段；`RegisterWithOptions` 调用 `registerNotificationsResourcesWithService` |
| `services/ani-gateway/notification_runtime.go` | 新增 | `gatewayNotificationRuntimeConfigFromEnv` + `newGatewayNotificationService`；当前只认 `local` / 空，其他返回 `ErrUnsupported` |
| `services/ani-gateway/main.go` | 修改 | 构造 `notificationService` 并注入 `router.RegisterOptions` |

## 契约回顾（前一 commit `5ad5c07` 已完成）

- 6 个 path：`/notifications/email/channel`（GET+PUT）、`/notifications/email/recipients`（GET+POST）、`/notifications/email/recipients/{recipient_id}`（GET+PATCH+DELETE）、`/notifications/events`（GET）、`/notifications/email/subscriptions`（GET+PUT）、`/notifications/email/test-send`（POST）
- 13 个 schema：`EmailChannel`、`EmailChannelUpdateRequest`、`EmailRecipient`、`EmailRecipientCreateRequest`、`EmailRecipientUpdateRequest`、`EmailRecipientListResponse`、`NotificationEvent`、`NotificationEventListResponse`、`EmailSubscription`、`EmailSubscriptionListResponse`、`EmailSubscriptionUpdateRequest`、`EmailTestSendRequest`、`EmailTestSendResponse`
- RBAC scope：`scope:notifications:read` / `:write`
- 所有写操作 required `idempotency_key`；`password` write-only

## 完工标准达成

- [x] `go build ./pkg/ports/... ./pkg/adapters/runtime/... ./pkg/bootstrap/... ./services/ani-gateway/...` 通过
- [x] `go test ./pkg/adapters/runtime/... -run TestLocalNotification` 7 tests PASS
- [x] `go test ./services/ani-gateway/internal/router/... -run TestNotificationAPI` 6 tests PASS
- [x] `python scripts/validate_component_imports.py --root .` 通过（未引入 forbidden SDK）
- [x] `python scripts/validate_openapi_spec.py` 通过
- [x] `python scripts/validate_spec_split_contract.py` 通过（Core/Services API 分层未破坏）
- [x] `git diff --check` 无空白错误
- [x] `gofmt -l` 所有新增/修改文件通过
- [x] PRD US-005「契约与联调闭环」：OpenAPI 已补齐（前一 commit）；BOSS 可通过 Core Client 调用 `/api/v1/notifications/email/*`；关键写路径支持 `idempotency_key` 幂等重试
- [x] PRD US-001「配置邮件发信通道」：`PUT /notifications/email/channel` 实现，`password` write-only，响应只暴露 `has_password`
- [x] PRD US-002「维护收件邮箱列表」：`/notifications/email/recipients` CRUD 实现，email 冲突 409，软删除
- [x] PRD US-003「配置事件邮件订阅开关」：`/notifications/events` 返回首期 5 个冻结枚举；`/notifications/email/subscriptions` PUT 批量 upsert，不支持的 `event_type` 返回 422
- [x] PRD US-004「测试发送邮件」：`/notifications/email/test-send` 校验前置条件（通道已配置 + 至少一个启用收件人），不满足返回 422

## 边界声明

- 本批次为 **Tier1 local profile**，`dev_profile.real_provider=false`，不声明 runtime ready / production ready
- `LocalNotificationService` 不与真实 SMTP 服务器通信；`SendEmailTest` 只校验前置条件并返回 `accepted` 入队结果，不实际投递邮件
- 真实 SMTP provider 与投递历史（PRD Non-Goals P2）在后续批次实现
- 前一 commit 已重新生成 SDK / API docs / Console schema，本 commit 不再触碰生成物

## 备注

- `TestDemoInstanceServiceRealShellExecutesCommand` 是与本批次无关的既有失败测试（Windows 386 环境下 shell 执行问题），在 stash 本批次改动后仍失败，非本批次引入
- `GOARCH=amd64` 用于绕过 `local_encryption_service.go:577` 在 32-bit 工具链下的既有 overflow 问题，与本批次无关
- 本批次未触碰 Services v1.yaml、Services 业务服务或前端代码
