# QUOTA-POLICY-ISSUE-009：绑定套餐 + 绑定租户列表 — service + store + Core 同步 + 回滚

> **批次类型：** Feature batch（BOSS 租户配额套餐功能流 Issue #9）
> **完成日期：** 2026-08-11
> **Scope：** `repo/services/tenant-service/internal/service/tenant_service.go`、`repo/services/tenant-service/internal/service/tenant_plan_service.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_store.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go`
> **依赖：** #2 gRPC 接口与 ports、#4 网关接入、#8 QuotaSvcClient（Get/Put/Create）
> **Product line：** boss

## 交付内容

实现 `BindPlanQuota`（US-009）和 `ListTenantPlanBoundTenants`（US-010）两个 gRPC RPC，以及 `TenantStore` 基础设施（`GetByID`/`UpdatePlan`）和 `TenantPlanStore.ListBoundTenants`。

### BindPlanQuota（US-009 绑定套餐）

- **校验链：** tenant_id/plan_id UUID 校验 → 套餐存在且未删除（404）→ 套餐 status=active（422 PLAN_NOT_ACTIVE）→ 租户存在（404）→ 租户 status≠disabled（409 TENANT_STATE_INVALID）。
- **组装限额视图：** `buildQuotaLimitViews`（store GetQuotaLimits + Core ListQuotaMeta），NULL total 回写 default_quota。
- **plan_id 变更：** `prevPlanID != planID` 时调 `UpdatePlan` 更新 tenants.plan_id，记下旧值用于回滚。
- **Core 同步：** `syncPlanQuotaToTenant` 跳过 approved 维度 → `applyTenantQuotaItems` 按 GetQuota 存在性分流 Put/Create。
- **Core 失败回滚：** Core 下发失败时 best-effort 回滚 plan_id 到 prevPlanID；回滚失败时审计记录 `rollback_error`。
- **审计：** 成功写 `tenant.bind_plan_quota`（含 plan_id/tenant_id/skipped_approved/updated）；每步失败写 `failure` 审计。
- **同套餐跳过 UpdatePlan：** `planChanged=false` 时不调 UpdatePlan，但仍调 Core 同步（approved 跳过）。

### ListTenantPlanBoundTenants（US-010 绑定租户列表）

- **校验：** plan_id UUID 校验 → 套餐存在（404）。
- **查询：** `ListBoundTenants` 查 `WHERE plan_id = $1 AND status <> 'disabled'`，含 frozen 租户，按 name 排序。
- **响应：** `items[]{id, name, display_name, status}`，不分页。

### TenantStore 基础设施

- `GetByID`：按主键查租户（id/name/display_name/status/plan_id），not found → `ErrTenantNotFound`。
- `UpdatePlan`：`UPDATE tenants SET plan_id = $2, updated_at = now() WHERE id = $1 RETURNING ...`，FK violation → `ErrTenantPlanNotFound`。

### 测试覆盖

| 测试 | 覆盖路径 |
|---|---|
| `TestTenantService_BindPlanQuota` | 正常绑定 + CreateQuota + UpdatePlan + 审计 + tenant_id 关联 |
| `Test_BindPlanQuota_PlanNotActive` | draft 套餐 → 422 PLAN_NOT_ACTIVE |
| `Test_BindPlanQuota_DisabledTenant` | disabled 租户 → 409 TENANT_STATE_INVALID |
| `Test_BindPlanQuota_CoreFailRollsBackPlanID` | Core 失败 → plan_id 回滚 + 审计 failure + rolled_back=true |
| `Test_BindPlanQuota_ApprovedSkip` | approved 维度跳过 + Put 分流 + 同套餐不 UpdatePlan |
| `TestTenantPlanService_ListBoundTenants` | 正常列表（含 frozen）+ 404 套餐不存在 |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| BindPlanQuota 实现 | 7 步校验 + Core 同步 + 回滚 | ✅ |
| 校验链完整 | plan 404 / 非 active 422 / tenant disabled 409 | ✅ |
| Core Put/Create 分流 | GetQuota → existSet → Put/Create | ✅ |
| approved 维度跳过 | GetApprovedQuotaChanges + approvedSet | ✅ |
| Core 失败回滚 plan_id | best-effort UpdatePlan(prevPlanID) + 审计 | ✅ |
| 同套餐跳过 UpdatePlan | planChanged=false 时不调 | ✅ |
| ListBoundTenants 实现 | store 查询 + 404 校验 + 不分页 | ✅ |
| TenantStore.GetByID/UpdatePlan | 按主键查 / FK 映射 | ✅ |
| 审计 | 成功/失败/回滚均写 + tenant_id 关联 | ✅ |
| 编译 | `go build` → EXIT=0 | ✅ |
| 测试 | `go test -run "Bind\|ListBound"` → 6/6 PASS | ✅ |
| review-it | 1 个中严重程度 finding（审计失败阻塞成功响应），待修复 | ⚠️ |

## 验证命令

```bash
cd repo
go build ./services/ani-gateway/... ./services/tenant-service/...
go test ./services/tenant-service/internal/... -run "Bind|ListBound" -v
```

## 边界声明

- `BindPlanQuota` 的 plan_id 更新和 Core 下发不在同一事务内（跨服务），Core 失败时 best-effort 回滚 plan_id，存在短暂不一致窗口。
- 审计写入失败会阻塞成功响应（review-it 发现 #1），待修复为 log warning 不返回 error。
- `ListBoundTenants` 包含 frozen 租户，与 `Delete` 的绑定检查和 `tenant_count` 子查询语义一致。

## Open Questions

1. **审计失败阻塞成功响应：** `writeAuditSuccess` 失败时 `return nil, err` 导致绑定已生效但用户看到 500。建议改为 log warning 不阻塞，与 issue-008 的处理方式对齐。待确认是否修复。
