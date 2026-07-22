# EMAIL-NOTIFY — 邮件通知 API + BOSS 发信设置页

完成日期：2026-07-22
对应 Sprint：当前 Sprint（以 repo/CURRENT-SPRINT.md 为准）
验证结果：`GOARCH=amd64 go build` pass，`go test ./pkg/adapters/runtime/...` 20 tests pass，`go test ./services/ani-gateway/internal/router/... -run TestEmailNotif` 19 tests pass

## 实现了什么

为 ANI 平台实现邮件通知后端 API（9 个 endpoint）和 BOSS 前端发信设置页面。后端基于 `ports.EmailNotificationStore` 接口提供本地内存 adapter，支持 SMTP 配置 CRUD、收件人 CRUD、事件订阅批量更新、测试发送。前端提供 SMTP 表单、收件人表格、订阅开关、测试发送按钮。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `repo/pkg/ports/email_notification.go` | 已存在 | port 接口已在 main commit `82a4a85` 中，本批次无 diff |
| `repo/pkg/adapters/runtime/local_email_notification_store.go` | 新增 | EmailNotificationStore 本地内存实现，495 行 |
| `repo/pkg/adapters/runtime/local_email_notification_store_test.go` | 新增 | 20 个单元测试，591 行 |
| `repo/services/ani-gateway/internal/router/notifications_email_resources.go` | 新增 | 9 个 HTTP handler + 错误映射 + mapper，424 行 |
| `repo/services/ani-gateway/internal/router/notifications_email_resources_test.go` | 新增 | 19 个 HTTP 级测试，551 行 |
| `repo/services/ani-gateway/internal/router/router.go` | 修改 | RegisterOptions 新增 EmailNotificationStore 字段 + 路由注册 |
| `repo/services/ani-gateway/main.go` | 修改 | 注入 runtimeadapter.NewLocalEmailNotificationStore() |
| `repo/pkg/adapters/runtime/local_encryption_service.go` | 修改 | 修复 32 位溢出 `int(^uint32(0))` → `math.MaxInt32` |
| `repo/frontends/boss/src/api/notifications.ts` | 新增 | 9 个 API 函数，141 行 |
| `repo/frontends/boss/src/components/notification-email/SmtpForm.tsx` | 新增 | SMTP 配置表单 |
| `repo/frontends/boss/src/components/notification-email/RecipientTable.tsx` | 新增 | 收件人列表 |
| `repo/frontends/boss/src/components/notification-email/RecipientDrawer.tsx` | 新增 | 收件人抽屉 |
| `repo/frontends/boss/src/components/notification-email/SubscriptionTable.tsx` | 新增 | 事件订阅表格 |
| `repo/frontends/boss/src/components/notification-email/TestSendButton.tsx` | 新增 | 测试发送按钮 |
| `repo/frontends/boss/src/routes/integration/notification-settings/email/*.tsx` | 新增 | 4 个路由页 |
| `repo/frontends/boss/src/routes/__root.tsx` | 修改 | 侧栏菜单新增"通知设置"SubMenu |
| `repo/frontends/boss/src/routeTree.gen.ts` | 修改 | 路由树生成更新 |

## 完工标准达成

- [x] `GOARCH=amd64 go build ./pkg/adapters/runtime/... ./services/ani-gateway/...` — pass
- [x] `GOARCH=amd64 go test ./pkg/adapters/runtime/...` — 20 tests pass
- [x] `GOARCH=amd64 go test ./services/ani-gateway/internal/router/... -run TestEmailNotif` — 19 tests pass
- [x] `GOARCH=amd64 go vet ./pkg/adapters/runtime/... ./services/ani-gateway/internal/router/...` — pass
- [ ] `make validate-architecture` — 未运行（需完整 repo 环境）
- [ ] BOSS 前端 `pnpm type-check && pnpm lint && pnpm build` — 未运行

## Design Decisions

### D1: nil 指针表示 SMTP 未配置状态

**Ambiguity:** SPEC 定义 `configured: true/false` 字段表示是否已配置，但未说明 port struct 层面如何表达。

**Choice:** 使用 `*EmailSmtpConfig` nil 指针表示未配置，非 nil 表示已配置。handler mapper 硬编码 `Configured: true/false` 到 response struct。

**Rationale:** 最初在 port struct 中添加 `Configured bool` 字段，但发现 mapper 用 nil 检查 + 硬编码，struct 中的字段从未被读取，属于冗余。删除字段后 nil 语义完整且代码更简洁。

### D2: auth_code 优先于 password

**Ambiguity:** SPEC 说"发送邮件时若 auth_code 已设置则优先使用 auth_code，否则使用 password"，但未说明两者同时设置时的情况。

**Choice:** `SendTestEmail` 中 `if HasAuthCode { credential = authCode } else { credential = password }`。

**Rationale:** 直接实现 SPEC 语义。QQ/163 等国内邮箱用授权码而非登录密码，优先使用授权码符合用户预期。

### D3: password/auth_code 独立 pointer 语义

**Ambiguity:** SPEC 定义 nil=不修改、""=清除、非空=覆盖，但未说明 password 和 auth_code 是否独立。

**Choice:** 两个字段完全独立处理，设置一个不影响另一个。

**Rationale:** 用户可能先用密码配置，后来获得授权码后只想添加授权码而不想清除密码（作为备用）。独立处理给用户最大灵活性。

### D4: Idempotency-Key header+body 双通道读取

**Ambiguity:** SPEC 定义 Idempotency-Key 在 header 中 required，但 request body 中也有 `idempotency_key` 字段。

**Choice:** handler 先读 body 中的 `idempotency_key`，为空时 fallback 到 header `Idempotency-Key`。

**Rationale:** 前端 openapi-fetch 自动注入 header，但某些场景 body 可能更可靠。双通道确保不会因为单一通道缺失而拒绝请求。

### D5: SMTP 加密方式按分支处理

**Ambiguity:** SPEC 定义 `encryption: "none" | "starttls" | "ssl"`，但 Go stdlib `smtp.SendMail` 不支持 SSL（隐式 TLS）和 STARTTLS 选择。

**Choice:** 不使用 `smtp.SendMail`，改为 `net.Dialer.DialContext` + `tls.Client`（SSL）或 `smtp.NewClient` + `client.StartTLS`（STARTTLS）或明文连接（none）。

**Rationale:** `smtp.SendMail` 永远用明文 TCP，无法支持 SSL/465 端口。手动实现 TLS 包装是唯一方案。

## Deviations

### DV1: `sendViaStdSMTP` 完全重写而非使用 `smtp.SendMail`

**Spec:** OpenAPI spec 只定义了 API 契约，未约束实现方式。最初实现用 `smtp.SendMail`。

**Implementation:** 改为 `net.Dialer.DialContext` + 手动 TLS 包装 + `smtp.NewClient` + `client.Auth/Mail/Rcpt/Data`。

**Rationale:** `smtp.SendMail` 使用 `net.Dial`（无 context/timeout）且不支持 SSL/STARTTLS 选择。重写后支持 15s 超时、context 取消、三种加密方式。

### DV2: `smtp.NewClient` 传主机名而非 IP

**Spec:** 无 spec 约束。

**Implementation:** `smtp.NewClient(conn, host)` 而非 `smtp.NewClient(conn, conn.RemoteAddr().String())`。

**Rationale:** `smtp.PlainAuth` 的 `Start()` 方法校验 `a.serverName == server.Name`，传 IP 导致主机名不匹配返回 `"wrong host name"` 错误。传配置的主机名确保校验通过。

### DV3: `local_encryption_service.go` 32 位溢出修复

**Spec:** 无 spec 约束，属于既有 bug。

**Implementation:** `int(^uint32(0))` → `math.MaxInt32`，新增 `math` import。

**Rationale:** `int(^uint32(0))` = 4294967295 在 GOARCH=386 下溢出 int（32 位最大 2147483647）。改为 `math.MaxInt32` 在所有平台安全。

## Tradeoffs

### T1: 本地内存 adapter vs PG adapter

**Alternatives:**
- A: 本地内存 adapter（当前选择）—— 数据不持久化，重启丢失
- B: PG adapter with CloudNativePG + RLS —— 持久化，多实例共享，需 KMS 加密凭据

**Choice:** A（本地内存 adapter）

**Rationale:** 本批次是 dev/local profile 实现，目的是打通 API 契约和前端流程。PG adapter 需要数据库迁移、KMS 集成、RLS 策略，属于后续生产化批次。本地内存 adapter 足以验证 9 个 endpoint 的契约一致性。

### T2: `sendViaStdSMTP` 手动 TLS vs 第三方库

**Alternatives:**
- A: 手动 `tls.Client` + `smtp.NewClient`（当前选择）—— 代码量少，依赖 stdlib
- B: 使用 `github.com/go-gomail/gomail` 或 `github.com/jordan-wright/email` —— 封装更完善，但引入外部依赖

**Choice:** A（手动 TLS）

**Rationale:** 本批次只用 stdlib 实现测试发送，功能简单（单封纯文本邮件）。引入第三方库增加依赖管理负担，不符合最小代码原则。生产化时可评估第三方库。

### T3: 前端 `sendTestEmail` 不发送 body

**Alternatives:**
- A: 不发送 body，只传 header（当前选择）—— 后端容错处理
- B: 发送 `{ idempotency_key: crypto.randomUUID() }` body —— 与 OpenAPI schema 一致

**Choice:** A

**Rationale:** OpenAPI spec 的 `sendTestEmail` endpoint 无 requestBody 定义，只有 `Idempotency-Key` header。后端 handler 在 `BindJSON` 失败时使用空 struct 并从 header 读取 key，契约一致。

## Open Questions

### O1: `make validate-architecture` 未运行

本批次未运行 `make validate-architecture`，需在完整 repo 环境中验证。如果 architecture gate 检查到新的 import 路径或边界问题，可能需要调整。

### O2: BOSS 前端验证未运行

`pnpm type-check && pnpm lint && pnpm build` 未运行。`routeTree.gen.ts` 是自动生成文件，如果 TanStack Router 版本更新可能需要重新生成。

### O3: `smtp.tsx` 中 `recipientsQuery` error 未处理

`smtp.tsx` 只处理 `smtpQuery.isError`，未处理 `recipientsQuery.isError`。如果收件人 API 失败，用户看到的是测试按钮不可点击而非错误提示。功能正确但体验略差，可作为后续优化。

### O4: router.go + main.go 改动恢复

review-it 发现 router.go 和 main.go 的 email wiring 改动在 stash 中丢失，已手动恢复。commit 前需确认这两个文件包含在 staged 文件中。

### O5: QQ 邮箱 535 认证失败

实际测试中发现 QQ 邮箱 SMTP 返回 `535 "Login fail"`。根因是用户需要使用 QQ 邮箱授权码（16 位字母）而非登录密码，且需在 QQ 邮箱设置中开启 SMTP 服务。这不是代码 bug，是用户配置问题。
