# TENANT-ADMIN-ISSUE-013：启用、停用、软删除 — 审查与修复

> **批次类型：** Feature batch（BOSS 租户管理员管理 Issue #13）
> **完成日期：** 2026-08-28
> **Scope：** `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/core/`、`repo/services/tenant-service/internal/repo/ports/`、`repo/services/tenant-service/internal/service/errors.go`、`repo/pkg/ports/errors.go`、`repo/pkg/adapters/runtime/postgres_tenant_admin.go`、`repo/services/ani-gateway/internal/router/`
> **依赖：** #1 OpenAPI 契约、#2 接口/数据模型、#4 网关接入
> **Product line：** boss

## 交付内容

对 SPEC §5.1.6 `DisableTenantAdmin` / `EnableTenantAdmin` / `DeleteTenantAdmin`（US-008）进行 review-it 审查。覆盖：状态值校验、不可重复 disable/enable 校验、Core DB 事务内 SELECT + UPDATE、错误映射链全链路、审计写入、SoftDelete 防重复删除。

### 审查文件

| 文件 | 审查内容 |
|------|----------|
| `services/tenant-service/internal/service/tenant_admin_service.go#L883-L1011` | DisableTenantAdmin / EnableTenantAdmin / DeleteTenantAdmin RPC |
| `services/tenant-service/internal/service/errors.go#L40-L43` | mapStoreError（ErrTenantStateInvalid + ErrUserStateInvalid） |
| `pkg/adapters/runtime/postgres_tenant_admin.go#L474-L558` | SetStatus（SELECT + 重复校验 + UPDATE） / SoftDelete |
| `pkg/ports/errors.go` | ErrUserStateInvalid 哨兵错误 |
| `services/tenant-service/internal/repo/ports/errors.go#L72` | ErrUserStateInvalid（SDK 层） |
| `services/tenant-service/internal/repo/adapters/core/sdk_client.go#L68-L69` | mapSDKError（USER_STATE_INVALID） |
| `services/ani-gateway/internal/router/admin_tenant_admin_resources.go#L246-L273` | Core gateway updateTenantUserStatus handler |
| `services/ani-gateway/internal/router/admin_tenant_admin_resources.go#L349-L363` | writeAdminTenantAdminError 错误映射 |
| `services/ani-gateway/internal/router/admin_tenant_admin_resources.go#L137-L151` | Core gateway deleteTenantUser handler |
| `services/ani-gateway/internal/router/tenant_admin_resources.go#L671-L686` | svc gateway tenantAdminBusinessCodeByHTTP |
| `services/tenant-service/internal/service/tenant_admin_service_test.go#L122-L134` | fake mock SetStatus（模拟 Core DB 重复校验） |
| `services/tenant-service/internal/service/tenant_admin_service_test.go#L1312-L1428` | 6 个子测试 |
| `services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md` §5.1.6, §7.1 | SPEC 一致性 |

### 测试

| 测试 | 覆盖点 |
|------|--------|
| `TestTenantAdminService_DisableEnableDelete/disable` | 正常禁用 + 审计 action + lastStatus 断言 |
| `TestTenantAdminService_DisableEnableDelete/enable` | 正常启用 + 审计 action + lastStatus 断言 |
| `TestTenantAdminService_DisableEnableDelete/already_disabled` | 重复禁用 → 409 USER_STATE_INVALID |
| `TestTenantAdminService_DisableEnableDelete/already_active` | 重复启用 → 409 USER_STATE_INVALID |
| `TestTenantAdminService_DisableEnableDelete/delete_soft` | 正常软删除 + 审计 action + softDeleted 断言 |
| `TestTenantAdminService_DisableEnableDelete/not_found` | SetStatus 返回 ErrTenantAdminNotFound → 404 |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| 不可重复 disable/enable | Core DB SetStatus 事务内 SELECT 当前 status，相同则返回 ErrUserStateInvalid | ✅ |
| 状态值校验 | Core 网关 `updateTenantUserStatus` 校验 body.Status 为 active/disabled；Core DB SetStatus switch 兜底 | ✅ |
| SoftDelete 防重复 | Core DB `WHERE COALESCE(is_deleted, FALSE) = FALSE`，RowsAffected==0 返回 ErrUserNotFound | ✅ |
| 审计写入 | Disable/Enable/Delete 所有路径（success + failure）都写审计 | ✅ |
| 错误码全链路一致 | Core DB → Core 网关 → SDK → Service → svc gateway 全部使用 USER_STATE_INVALID | ✅ |
| SPEC §5.1.6 一致 | SPEC 已更新为当前架构 | ✅ |
| SPEC §7.1 错误表一致 | TENANT_STATE_INVALID → USER_STATE_INVALID | ✅ |

## Design Decisions

### D1：状态值校验放在 Core 网关层而非 Service 层

- **歧义：** SPEC 未明确指定状态值校验在哪一层执行。
- **选择：** Core 网关 `updateTenantUserStatus` handler 中校验 `body.Status` 为 `active` 或 `disabled`，Core DB `SetStatus` 的 `switch status` 作为兜底。
- **理由：** 用户明确要求"不要在 Service 中校验状态，直接发请求"。Core 网关是 HTTP 入口层，早期校验避免无意义的 Core DB 事务。Core DB switch 是最终兜底，防止绕过网关直接调用。

### D2：不可重复 disable/enable 校验放在 Core DB 层

- **歧义：** 重复校验可以在 Service 层（GetUser + 比对）或 Core DB 层（SELECT + 比对）执行。
- **选择：** Core DB `SetStatus` 事务内 SELECT 当前 status，相同则返回 `ErrUserStateInvalid`。Service 层直接发一次 `SetStatus` 请求，不预先 GetUser。
- **理由：** 用户明确要求"不可重复更换在 core 中校验"。Core DB 事务内 SELECT + 比对保证原子性，避免 Service 层 GetUser 与 SetStatus 之间的 TOCTOU 竞态。

### D3：新增 `ErrUserStateInvalid` 哨兵错误，与 `ErrTenantStateInvalid` 分离

- **歧义：** 原有 `ErrTenantStateInvalid` 用于租户状态（如 disabled 绑套餐），是否复用给用户状态？
- **选择：** 新增 `ErrUserStateInvalid`（`pkg/ports` + `tenant-service/ports`），专门表示用户状态非法（重复 disable/enable）。`ErrTenantStateInvalid` 保留用于租户状态场景。
- **理由：** 语义清晰——租户状态和用户状态是不同的业务概念，分离哨兵错误避免混淆。错误码 `USER_STATE_INVALID` 与 `TENANT_STATE_INVALID` 分离，前端可区分处理。

## Deviations

### DV1：Service 层不发送 GetUser 请求

- **SPEC 原描述：** §5.1.6 初版描述 Service 发两次 Core 请求（GetUser + SetStatus），Service 层校验 status。
- **实际实现：** Service 层直接发一次 `SetStatus` 请求，无 GetUser，无 status 校验。
- **原因：** 用户明确要求"不要在 Service 中校验状态，直接发请求"。状态值校验移到 Core 网关层，重复校验移到 Core DB 层。SPEC §5.1.6 已同步更新为当前架构。

### DV2：svc gateway `tenantAdminBusinessCodeByHTTP` 移除 `TENANT_STATE_INVALID`

- **SPEC 原描述：** §7.1 错误表有 `TENANT_STATE_INVALID` 行。
- **实际实现：** tenant admin 上下文的 `tenantAdminBusinessCodeByHTTP` 移除了 `TENANT_STATE_INVALID` 映射行，仅保留 `USER_STATE_INVALID`。`TENANT_STATE_INVALID` 仍保留在 `tenant_plans.go` 的 `tenantPlanBusinessCodeByHTTP` 中用于租户状态场景。
- **原因：** tenant admin 上下文不再产生 `TENANT_STATE_INVALID` 错误码，保留无用映射会造成混淆。SPEC §7.1 已更新为 `USER_STATE_INVALID`。

## Tradeoffs

### T1：Core DB 事务内 SELECT + UPDATE vs 单条 UPDATE ... WHERE status != $1

- **备选 A（已选）：** 事务内 SELECT 当前 status → Go 代码比对 → UPDATE。两次 SQL。
- **备选 B：** 单条 `UPDATE users SET status=$1 WHERE id=$2 AND tenant_id=$3 AND status!=$1`，通过 RowsAffected 判断是否重复。
- **A 优势：** 代码可读性好——SELECT 和 UPDATE 职责分离，重复校验逻辑在 Go 代码中清晰可见。可以区分"用户不存在"（ErrUserNotFound）和"状态未变化"（ErrUserStateInvalid）两种情况。
- **A 劣势：** 同一事务内多一次 SQL round-trip。
- **B 优势：** 单次 SQL 更高效。
- **B 劣势：** RowsAffected==0 无法区分"用户不存在"和"状态未变化"——都需要额外 SELECT 来区分。实际并未减少 SQL 次数。
- **结论：** 选 A — 需要区分两种错误情况（404 vs 409），单条 UPDATE 无法做到。两次查询在同一事务内，性能影响可忽略。

### T2：状态常量定义在 `tenant-service/ports` 而非 `pkg/ports`

- **现状：** `TenantAdminUserStatusActive` / `TenantAdminUserStatusDisabled` 定义在 `tenant-service/internal/repo/ports/`。Core DB 层（`pkg/adapters/runtime/`）的 `SetStatus` switch 用硬编码字符串 `"active"` / `"disabled"`，无法引用 tenant-service 的常量。
- **备选 A（已选）：** 保持现状，Core DB 用硬编码字符串。
- **备选 B：** 将状态常量提升到 `pkg/ports`，Core DB 和 Service 共用。
- **A 优势：** 不改变现有包依赖关系。Core DB 层是平台层，不应依赖 tenant-service 的 ports。
- **A 劣势：** Core DB switch 中的硬编码字符串可能与常量值不一致（虽然目前一致）。
- **结论：** 选 A — Core DB 是兜底校验，硬编码字符串 `"active"` / `"disabled"` 是 DB schema 约定值，不会变化。提升常量到 `pkg/ports` 会引入不必要的耦合。

## Open Questions

None — review-it clean，无 actionable finding。

## 验证命令

```bash
cd repo/services/tenant-service
go test ./internal/service/ -run "TestTenantAdminService_DisableEnableDelete" -v -count=1
# 6 sub-tests PASS:
#   disable, enable, already_disabled, already_active, delete_soft, not_found

cd repo/services/ani-gateway
go build ./internal/router/...
# PASS

# review-it (3 rounds)
# Round 1: F1 编译错误 (ports.ErrTenantStateInvalid 不在 pkg/ports) / F2 Core 网关缺映射 / F3 SDK 缺映射 / F4 SPEC 不一致 / F5 硬编码字符串
#   → F1 修复：新增 ErrUserStateInvalid 到 pkg/ports
#   → F2 修复：writeAdminTenantAdminError 添加 ErrUserStateInvalid → 409
#   → F3 修复：mapSDKError + mapStoreError + tenantAdminBusinessCodeByHTTP 添加 USER_STATE_INVALID
#   → F4 修复：SPEC §5.1.6 更新
#   → F5 观察项（不影响正确性）
# Round 2: F1 SPEC §7.1 错误表仍用 TENANT_STATE_INVALID / F2 硬编码字符串 / F3 Core 网关缺 status 校验
#   → F1 修复：SPEC §7.1 TENANT_STATE_INVALID → USER_STATE_INVALID
#   → F3 修复：Core 网关 updateTenantUserStatus 添加 status 值校验
#   → F2 不处理（Core DB 层硬编码字符串是 DB schema 约定值）
# Round 3: F1 SPEC §7.1 错误表遗留 TENANT_STATE_INVALID
#   → F1 修复：SPEC §7.1 更新 + svc gateway 移除残留 TENANT_STATE_INVALID 映射行
#   → clean, no actionable finding
```

## 边界声明

- `already_disabled` / `already_active` 子测试使用 fake mock，验证 Service 层错误映射。Core DB 层的 SELECT + 重复校验逻辑未被集成测试覆盖（需要真实数据库环境）。
- `not_found` 子测试通过 mock `setStatusErr` 模拟 `ErrTenantAdminNotFound`，未覆盖 Core DB `RowsAffected == 0` 返回 `ErrUserNotFound` 的路径。
- Core DB `SetStatus` 的 `switch status` 硬编码字符串 `"active"` / `"disabled"` 是 DB schema 约定值，与 `tenant-service/ports` 中的常量值一致但无编译时关联。
