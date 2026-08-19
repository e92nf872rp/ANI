# QUOTA-POLICY-ISSUE-008：套餐限额修改 + 存量租户同步 — service + store + Core 分流

> **批次类型：** Feature batch（BOSS 租户配额套餐功能流 Issue #8）
> **完成日期：** 2026-08-11
> **Scope：** `repo/services/tenant-service/internal/service/tenant_plan_service.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go`、`repo/services/tenant-service/internal/repo/adapters/core/quota_svc_client.go`、`repo/services/tenant-service/internal/repo/ports/core_quota.go`
> **依赖：** #2 gRPC 接口与 ports、#4 网关接入、#5 CreateTenantPlan
> **Product line：** boss

## 交付内容

实现 `UpdateTenantPlanQuotaLimits`（US-008）和 `GetTenantPlanQuotaLimits`（US-004）两个 gRPC RPC，以及 store 层 `UpdateQuotaLimits`（UPSERT）、`GetQuotaLimits`、`ListBoundTenants`、`GetApprovedQuotaChanges` 四个方法。Core 客户端实现 `GetQuota`/`PutQuota`/`CreateQuota`/`DeleteQuota` 四个方法。

### UpdateTenantPlanQuotaLimits（US-008 修改限额并同步租户）

- **plan_id 校验：** `parsePlanID` 校验 UUID。
- **items 校验：** 至少 1 项，否则 `VALIDATION_FAILED`。
- **维度校验：** `mapAndValidateQuotaLimits` 调 Core `ListQuotaMeta`，nil total 用 default_quota 兜底。
- **UPSERT 限额：** store 层 `UpdateQuotaLimits` 用 `ON CONFLICT (plan_id, resource_type) DO UPDATE`，事务内逐项 UPSERT + 触摸 `updated_at`。
- **同步存量租户：** `syncBoundTenantQuotaLimits` 列出绑定租户 → 逐租户查 approved 维度跳过 → 调 Core Put/Create 下发。
- **Core 失败处理：** 写 `tenant.quota_init_failed` 审计 + 异步重试（3 次指数退避），不回滚已提交限额。
- **审计：** 成功写 `tenant_plan.update_quota_limits`（含 synced_tenant_count / skipped_approved / updated_dimensions）。

### GetTenantPlanQuotaLimits（US-004 查询限额展示视图）

- **buildQuotaLimitViews：** store `GetQuotaLimits` 读原始行 + Core `ListQuotaMeta` 补 display_name/unit/default_quota。
- **NULL 回写：** total=NULL 的维度用 default_quota 回写为具体值（读路径消灭 NULL 语义）。

### syncBoundTenantQuotaLimits 同步逻辑

- **approved 维度跳过：** 查 `tenant_quota_change` 表 status=approved 的维度，跳过覆盖。
- **Put/Create 分流：** 调 Core `GetQuota` 查租户已有维度 → 已有 `PutQuota`、缺失 `CreateQuota`。
- **错误吞咽：** `buildQuotaLimitViews` / `ListBoundTenants` 失败时静默返回 0（不阻塞用户成功响应）。Core 下发失败写审计 + 异步重试。

### Core 客户端完整实现

- `GetQuota`：GET `/admin/tenants/{id}/quota`。
- `PutQuota`：PUT `/admin/tenants/{id}/quota`（带 Idempotency-Key）。
- `CreateQuota`：POST `/admin/tenants/{id}/quota`（带 Idempotency-Key）。
- `DeleteQuota`：DELETE `/admin/tenants/{id}/quota`。
- `mapCoreHTTPError`：业务码精确映射 + HTTP status 兜底。

### Store 层新方法

- `GetQuotaLimits`：读原始限额行（保留 NULL），先校验套餐存在。
- `UpdateQuotaLimits`：本地事务 + UPSERT + 触摸 updated_at。
- `ListBoundTenants`：查非 disabled 绑定租户摘要。
- `GetApprovedQuotaChanges`：查 `tenant_quota_change` status=approved 维度。
- `requirePlanExists`：共享的套餐存在性校验。
- `Activate`/`Disable`：条件 UPDATE + RETURNING，未命中区分 404/409。
- `Delete`：软删除，有非 disabled 绑定租户时 409。

### 测试覆盖

| 测试 | 覆盖路径 |
|---|---|
| `TestTenantPlanService_GetQuotaLimits` | Get + display_name/unit 来自 Core |
| `TestTenantPlanService_GetQuotaLimits_BackfillNullTotal` | NULL → default_quota 回写 |
| `TestTenantPlanService_UpdateQuotaLimits` | 正常更新 + approved 跳过 + Put/Create 分流 |
| `Test_UpdateQuotaLimits_Tightened` | Core tightened 不报错 |
| `Test_UpdateQuotaLimits_CoreFailAudits` | Core 失败 → 审计 + 不回滚 |
| `Test_UpdateQuotaLimits_CreateWhenMissing` | GetQuota 空 → CreateQuota |
| `Test_UpdateQuotaLimits_Validation` | 空 items + 未注册维度 |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| UpdateTenantPlanQuotaLimits 实现 | service 6 步 + store UPSERT | ✅ |
| GetTenantPlanQuotaLimits 实现 | buildQuotaLimitViews + NULL 回写 | ✅ |
| approved 维度跳过 | GetApprovedQuotaChanges + approvedSet | ✅ |
| Put/Create 分流 | GetQuota → existSet → Put/Create | ✅ |
| Core 失败不回滚 | 限额先提交，同步异步重试 | ✅ |
| 异步重试 | scheduleQuotaSyncRetry 3 次指数退避 | ✅ |
| Core 客户端 5 方法 | Get/Put/Create/Delete + ListQuotaMeta | ✅ |
| store Activate/Disable | 条件 UPDATE + 404/409 区分 | ✅ |
| store Delete | 软删除 + TENANT_PLAN_IN_USE | ✅ |
| 审计 | 成功/失败均写 | ✅ |
| 编译 | `go build` → EXIT=0 | ✅ |
| 测试 | `go test -count=1` → PASS | ✅ |
| review-it | 1 个低严重程度 finding（静默吞错），用户确认设计合理 | ✅ |

## 验证命令

```bash
cd repo
go build ./services/ani-gateway/... ./services/tenant-service/...
go test ./services/tenant-service/internal/... -count=1
```

## 边界声明

- 本 Issue 实现 Update/Get QuotaLimits + 同步 + Core 5 方法 + store Activate/Disable/Delete。`ListTenantPlanBoundTenants`/`ListTenantPlanAuditLogs` 仍为 panic 占位。
- `BindPlanQuota`（TenantService）仍为 panic 占位（issue-009）。
- `syncBoundTenantQuotaLimits` 的 `buildQuotaLimitViews`/`ListBoundTenants` 失败时静默返回 0（不写审计），用户确认设计合理：修改套餐成功就返回成功，同步失败后续可手动同步。

## Open Questions

None — 用户确认同步失败不阻塞成功响应，后续可手动同步。
