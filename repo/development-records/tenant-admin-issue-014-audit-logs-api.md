# TENANT-ADMIN-ISSUE-014：查询操作历史 — 审查与修复

> **批次类型：** Feature batch（BOSS 租户管理员管理 Issue #14）
> **完成日期：** 2026-08-28
> **Scope：** `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/postgres/`、`repo/services/ani-gateway/internal/router/`、`repo/services/tasks/modules/spec/boss/tenant/`
> **依赖：** #2 接口/数据模型、#3 数据库迁移
> **Product line：** boss

## 交付内容

对 SPEC §5.1.10 `ListTenantAdminAuditLogs`（US-010）进行 review-it 审查。覆盖：游标分页、action/result 过滤、target_id 查询逻辑、limit 三层截断、JSON 映射、错误处理、SPEC 一致性。

### 审查文件

| 文件 | 审查内容 |
|------|----------|
| `services/tenant-service/internal/service/tenant_admin_service.go#L1014-L1096` | ListTenantAdminAuditLogs RPC + tenantAdminAuditLogToPB 映射 |
| `services/tenant-service/internal/repo/adapters/postgres/tenant_admin_store.go#L311-L406` | ListAuditLogs SQL（WHERE + 游标 + 多取1条） |
| `services/tenant-service/internal/repo/ports/tenant_admin_store.go#L150-L208` | AuditLogListItem / TenantAdminAuditLogFilter / TenantAdminAuditLogListResult |
| `services/ani-gateway/internal/router/tenant_admin_resources.go#L214-L244` | svc gateway listTenantAdminAuditLogs handler |
| `services/ani-gateway/internal/router/tenant_admin_resources.go#L590-L612` | tenantAdminAuditLogJSON JSON 映射 |
| `services/ani-gateway/internal/router/tenant_common.go#L41-L60` | cursorLimit + nullIfEmpty 通用辅助 |
| `services/tenant-service/internal/service/tenant_admin_service_test.go#L319-L353` | fake mock ListAuditLogs |
| `services/tenant-service/internal/service/tenant_admin_service_test.go#L1464-L1557` | 2 个子测试 |
| `services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md` §4.1, §5.1.10 | SPEC 一致性 |

### 测试

| 测试 | 覆盖点 |
|------|--------|
| `TestTenantAdminService_ListAuditLogs/list_and_filter` | 列表查询 + action/result 过滤 + user_id + details 映射 |
| `TestTenantAdminService_ListAuditLogs/invite_then_audit_exists` | 邀请后审计记录断言（invite → audit → list 链路） |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| 游标分页 limit 默认20/最大100 | Service L1036-L1044 + Core DB L316-L321 + svc gateway L41-L52 三层截断 | ✅ |
| cursor / action / result 过滤 | Core DB L331-L339 参数化查询；Service L1048-L1054 透传 | ✅ |
| 响应 CursorPage（items + next_cursor） | Service L1068-L1071；svc gateway L240-L243 | ✅ |
| WHERE tenant_id + target_id | Core DB L324-L328 `tenant_id=$1 AND resource='tenant_admin' AND details->>'target_id'=$2` | ✅ |
| created_at DESC | Core DB L360 `ORDER BY created_at DESC, id DESC` | ✅ |
| 不调 Core API | Service 直接调 `s.store.ListAuditLogs`，无 `s.core` 调用 | ✅ |
| items 含 id/action/resource/result/user_id/details/created_at | JSON 映射 L594-L611 全字段 | ✅ |
| 只读端点 | 无审计写入、无 Core 写入 | ✅ |
| result 值 success/failure | SPEC §4.1 已修正为 `success|failure`；写入侧 audit.go 用 `"success"`/`"failure"` | ✅ |

## Design Decisions

### D1：查询条件用 `details->>'target_id'` 而非 `user_id`

- **歧义：** SPEC §5.1.10 原描述 `WHERE user_id=userId`，但 `audit_logs.user_id` 是**操作者**（执行操作的管理员），被操作的目标管理员 ID 存储在 `details.target_id` JSONB 字段中。
- **选择：** SQL 查询用 `details->>'target_id' = $2` 匹配目标管理员。
- **理由：** 审计日志的设计语义是"谁（user_id）对谁（details.target_id）做了什么（action）"。查询某个管理员的操作历史，应查以该管理员为**目标**的记录，而非以该管理员为**操作者**的记录。SPEC §5.1.10 已修正为 `WHERE details->>'target_id'=userId`。

### D2：limit 三层截断

- **歧义：** limit 的 max 100 截断应该在哪层执行？
- **选择：** 三层都执行——svc gateway `cursorLimit` L46-L48、Service L1042-L1044、Core DB L319-L321。
- **理由：** 纵深防御。svc gateway 是 HTTP 入口，早期截断避免大 limit 传入 gRPC；Service 层是业务逻辑层，保持自治；Core DB 层是最终兜底。三层一致使用 default 20、max 100。

## Deviations

### DV1：SPEC §4.1 result 过滤值从 `failed` 修正为 `failure`

- **SPEC 原描述：** §4.1 L554 `result`(success|failed)
- **实际实现：** SPEC 已修正为 `result`(success|failure)，与写入侧 `audit.go` 的 `Result: "success"` / `Result: "failure"` 一致。
- **原因：** 原始 SPEC 用的 `failed` 与代码写入的 `failure` 不一致。如果用户按 SPEC 传 `result=failed` 过滤，`WHERE result = 'failed'` 会匹配不到任何记录（DB 中存的是 `failure`）。修正 SPEC 与代码一致。

### DV2：SPEC §5.1.10 WHERE 条件从 `user_id=userId` 修正为 `details->>'target_id'=userId`

- **SPEC 原描述：** §5.1.10 L708 `WHERE tenant_id=tenantId AND user_id=userId`
- **实际实现：** SPEC 已修正为 `WHERE tenant_id=tenantId AND details->>'target_id'=userId`
- **原因：** `audit_logs.user_id` 是操作者 ID，不是被操作的管理员 ID。查询某管理员的操作历史应匹配 `details.target_id`。SPEC 描述与代码逻辑不一致，修正 SPEC。

## Tradeoffs

### T1：游标分页用 `details->>'target_id'` JSONB 字段查询 vs 冗余列

- **备选 A（已选）：** 直接查 `details->>'target_id' = $2`，利用 JSONB GIN 索引。
- **备选 B：** 在 `audit_logs` 表增加 `target_id UUID` 冗余列，建 B-tree 索引。
- **A 优势：** 不改表结构，复用现有 `audit_logs` 分区表。JSONB `->>` 操作符可走 GIN 索引。
- **A 劣势：** JSONB `->>` 查询比 B-tree 慢，特别是无 GIN 索引时全表扫描。
- **B 优势：** B-tree 索引查询更快。
- **B 劣势：** 需要迁移 `audit_logs` 表结构，影响所有审计日志写入方。`target_id` 语义仅适用于 `tenant_admin.*` 操作，其他审计日志无此字段。
- **结论：** 选 A — 不改表结构，避免影响面扩大。如果未来查询性能成为瓶颈，可考虑加 GIN 索引 `CREATE INDEX ON audit_logs USING GIN (details)` 或方案 B。

## Open Questions

None — review-it clean，无 actionable finding。

## 验证命令

```bash
cd repo/services/tenant-service
go test ./internal/service/ -run "TestTenantAdminService_ListAuditLogs" -v -count=1
# 2 sub-tests PASS:
#   list_and_filter, invite_then_audit_exists

cd repo/services/ani-gateway
go build ./internal/router/...
# PASS

# review-it (2 rounds)
# Round 1: F1 result 过滤值 failed vs failure / F2 SPEC WHERE 描述 / F3 Service 层缺 max 100
#   → F1 修复：SPEC §4.1 failed → failure
#   → F2 修复：SPEC §5.1.10 user_id → details->>'target_id'
#   → F3 修复：Service 层 limit 加 max 100 截断
# Round 2: clean, no actionable finding
```

## 边界声明

- `list_and_filter` 和 `invite_then_audit_exists` 子测试使用 fake mock，验证 Service 层逻辑和 JSON 映射。Core DB 层的 SQL 查询（`details->>'target_id'` 匹配、游标分页、多取1条）未被集成测试覆盖（需要真实数据库环境）。
- 非法 cursor 的 `ErrValidationFailed` → 400 错误路径未被测试覆盖（fake mock 不实现 cursor 解码）。
- `audit_logs` 表的 JSONB `->>'target_id'` 查询性能取决于是否有 GIN 索引，生产环境需确认索引策略。
