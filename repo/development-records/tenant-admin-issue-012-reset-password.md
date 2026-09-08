# TENANT-ADMIN-ISSUE-012：重置密码 — 审查与验证

> **批次类型：** Feature batch（BOSS 租户管理员管理 Issue #12）
> **完成日期：** 2026-08-27
> **Scope：** `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/core/`、`repo/pkg/adapters/runtime/postgres_tenant_admin.go`、`repo/services/ani-gateway/internal/router/`
> **依赖：** #1 OpenAPI 契约、#2 接口/数据模型、#4 网关接入
> **Product line：** boss

## 交付内容

对 SPEC §5.1.7 `ResetTenantAdminPassword`（US-007）进行 review-it 审查。覆盖：明文不落审计/日志/响应、密码复杂度校验、bcrypt cost=12、旧 hash 比较逻辑、错误映射链全链路、幂等键处理、disabled 用户允许重置。

### 审查文件

| 文件 | 审查内容 |
|------|----------|
| `services/tenant-service/internal/service/tenant_admin_service.go#L837-L887` | ResetTenantAdminPassword RPC |
| `services/tenant-service/internal/service/tenant_admin_service.go#L926-L965` | validateTenantAdminPassword 密码复杂度校验 |
| `services/tenant-service/internal/service/errors.go#L62-L63` | ErrPasswordSameAsOld → codes.FailedPrecondition |
| `pkg/adapters/runtime/postgres_tenant_admin.go#L489-L546` | ResetPassword SQL（SELECT oldHash → bcrypt 比较 → bcrypt 生成 → UPDATE） |
| `services/tenant-service/internal/repo/adapters/core/tenant_admin_svc_client.go#L335-L346` | ResetPassword HTTP 调用 |
| `services/tenant-service/internal/repo/adapters/core/sdk_client.go#L60-L67` | mapSDKError（USER_NOT_FOUND / PASSWORD_SAME_AS_OLD） |
| `services/ani-gateway/internal/router/admin_tenant_admin_resources.go#L270-L292` | Core gateway resetTenantUserPassword handler |
| `services/ani-gateway/internal/router/admin_tenant_admin_resources.go#L344-L357` | writeAdminTenantAdminError 错误映射 |
| `services/ani-gateway/internal/router/tenant_admin_resources.go#L413-L448` | SVC gateway resetTenantAdminPassword handler |
| `services/ani-gateway/internal/router/tenant_admin_resources.go#L671-L723` | tenantAdminBusinessCodeByHTTP + mapTenantAdminGRPCError |
| `services/tenant-service/internal/service/tenant_admin_service_test.go#L1213-L1292` | 5 个子测试 |

### 测试

| 测试 | 覆盖点 |
|------|--------|
| `TestTenantAdminService_ResetPassword/success` | 正常重置 + 审计 action + new_password 不在审计 details 中 |
| `TestTenantAdminService_ResetPassword/same_as_old` | 旧 hash 相同 → 422 PASSWORD_SAME_AS_OLD |
| `TestTenantAdminService_ResetPassword/complexity_invalid` | 短密码 → 400 VALIDATION_FAILED |
| `TestTenantAdminService_ResetPassword/soft_deleted_not_found` | 软删除用户 → 404 TENANT_ADMIN_NOT_FOUND |
| `TestTenantAdminService_ResetPassword/disabled_user_allowed` | disabled 用户可重置 |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| 明文 new_password 不落审计/日志/响应 | Service details 只含 tenant_id + target_id；测试显式断言 new_password 不在 audit details | ✅ |
| 密码复杂度 8-64 字符 + 四类至少三类 | `validateTenantAdminPassword` 在 Service 层校验，调 Core 之前 | ✅ |
| bcrypt cost=12 | Core DB `bcrypt.GenerateFromPassword([]byte(newPassword), 12)` | ✅ |
| 旧 hash 相同 → 422 PASSWORD_SAME_AS_OLD | Core DB `bcrypt.CompareHashAndPassword` 比较 → `ErrPasswordSameAsOld` → 422 | ✅ |
| 软删除 → 404 TENANT_ADMIN_NOT_FOUND | Core DB `COALESCE(is_deleted, FALSE) = FALSE` → `ErrUserNotFound` → 404 | ✅ |
| disabled 用户允许重置 | Core DB SQL 只过滤 is_deleted，不过滤 status | ✅ |
| 幂等键由网关中间件处理 | svc gateway 补充 idempotency_key，网关中间件处理 HTTP 层幂等 | ✅ |
| 审计失败写入 | 所有错误路径（nil req / UUID 解析失败 / 复杂度失败 / Core 失败）都写审计 | ✅ |

## Design Decisions

None — 实现严格遵循 SPEC §5.1.7，无歧义点需要决策。

## Deviations

None — 实现与 SPEC 完全一致。

## Tradeoffs

### T1：Core DB 两次查 users 表（SELECT + UPDATE）vs 合并

- **备选 A（已选）：** 步骤 1 SELECT oldHash，步骤 4 UPDATE password_hash。两次查同一行。
- **备选 B：** 合并为单条 SQL（如 UPDATE ... RETURNING + 在 Go 中比较）
- **A 优势：** 代码可读性好——SELECT 和 UPDATE 职责分离，bcrypt 比较在 Go 代码中清晰可见。
- **A 劣势：** 同一事务内多一次 SQL round-trip。
- **B 优势：** 单次 SQL 更高效。
- **B 劣势：** SQL 复杂化（需要在 RETURNING 中返回 old_hash，再在 Go 中做 bcrypt 比较，如果相同还要回滚或跳过 UPDATE），可读性差。
- **结论：** 选 A — bcrypt 比较必须在 Go 代码中执行（SQL 无法做 `bcrypt.CompareHashAndPassword`），SELECT 不可省略。两次查询在同一事务内，性能影响可忽略。步骤 4 的 `RowsAffected == 0` 守卫处理了步骤 1 后用户被并发软删除的边界情况。

## Open Questions

None — review-it clean，无 actionable finding。

## 验证命令

```bash
cd repo/services/tenant-service
go test ./internal/service/ -run "TestTenantAdminService_ResetPassword" -v -count=1
# 5 sub-tests PASS:
#   success, same_as_old, complexity_invalid, soft_deleted_not_found, disabled_user_allowed

# review-it (2 rounds)
# Round 1: F1 两次查 users / F2 disabled 测试覆盖 / F3 Core gateway 错误码 / F4 unicode.IsSymbol 范围
#   → F1 撤回（bcrypt 比较必须在 Go 中，SELECT 不可省略）
#   → F2 观察项（Core DB 层未被集成测试覆盖，不影响正确性）
#   → F3 撤回（Core DB 返回 ErrUserNotFound 不是 ErrTenantAdminNotFound，Core gateway 已正确映射）
#   → F4 观察项（不影响安全性，bcrypt 正确处理 Unicode）
# Round 2: clean, no actionable finding
```

## 边界声明

- `disabled_user_allowed` 子测试使用 fake mock，验证 Service 层不拦截 disabled 用户。Core DB 层的 disabled 允许重置逻辑未被集成测试覆盖（需要真实数据库环境）。
- `unicode.IsPunct || unicode.IsSymbol` 将 Unicode 标点和符号都算作"特殊字符"类，包括 emoji 和数学符号。这不影响安全性（bcrypt 正确处理所有 Unicode 字符），但如果安全策略要求精确的 ASCII 特殊字符白名单，需调整 `validateTenantAdminPassword`。
- 错误码分层转换是设计合理的：Core gateway 返回 `USER_NOT_FOUND`（内部），Service 层 `mapStoreError` 转为 `ErrTenantAdminNotFound`，svc gateway 返回 `TENANT_ADMIN_NOT_FOUND`（前端消费）。两层 HTTP status 都是 404。
