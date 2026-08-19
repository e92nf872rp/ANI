# QUOTA-POLICY-ISSUE-06：套餐列表 + 详情查询 — service 实现 + store 游标分页

> **批次类型：** Feature batch（BOSS 租户配额套餐功能流 Issue #6）
> **完成日期：** 2026-08-11
> **Scope：** `repo/services/tenant-service/internal/service/tenant_plan_service.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go`、`repo/services/tenant-service/internal/repo/ports/tenant_plan_store.go`
> **依赖：** #2 gRPC 接口与 ports、#4 网关接入
> **Product line：** boss

## 交付内容

实现 `ListTenantPlans`（US-002）和 `GetTenantPlan`（US-003）两个 gRPC RPC 的完整业务逻辑，以及 store 层 `List`（游标分页 + status/search 过滤）和 `GetByID`（按主键 + tenant_count 子查询）两个查询方法。

### ListTenantPlans（US-002 查询套餐列表）

- **status 枚举校验：** `ParseTenantPlanStatusFilter` 空=全部，非法值报 `VALIDATION_FAILED`。
- **游标分页入参：** 从 `CursorPageRequest` 取 limit/cursor，缺省 limit=20。
- **store 列表查询：** 调 `plans.List(filter)`，返回 `TenantPlanListItem`（不含 quota_limits）。
- **组装响应：** `listItemToPB` 映射为 gRPC `TenantPlan`，返回 `items + total + next_cursor`。

### GetTenantPlan（US-003 查询套餐详情）

- **plan_id 校验：** 空 → `VALIDATION_FAILED`，非 UUID → `VALIDATION_FAILED`。
- **按主键查询：** `plans.GetByID(id)`，未删除/不存在 → `TENANT_PLAN_NOT_FOUND`。
- **组装响应：** `planToPB` 映射为 gRPC `TenantPlan`（含 tenant_count）。

### Store 层 List（游标分页）

- **limit 边界：** `<=0 → 20, >100 → 100`（store 作为最后防线）。
- **过滤条件：** `is_deleted = FALSE` + 可选 `status =` + 可选 `name ILIKE %search%`。
- **total 统计：** 仅按 status/search，不含 cursor，语义正确（total 不随翻页变化）。
- **游标：** `(created_at DESC, id DESC)` 复合排序，WHERE `(created_at, id) < ($n, $n+1)` 确保不丢不重；非法 cursor → `VALIDATION_FAILED`。
- **多取 1 条：** `LIMIT N+1` 判断 hasNext，有多余则截断并编码 `next_cursor`。

### Store 层 GetByID

- **查询：** `WHERE p.id = $1 AND p.is_deleted = FALSE`，软删除套餐不返回。
- **tenant_count：** `COALESCE((SELECT COUNT(*) FROM tenants WHERE plan_id = p.id AND status <> 'disabled'), 0)`，与 List 一致。
- **not found：** `pgx.ErrNoRows` → `ErrTenantPlanNotFound`。

### JSON 字段命名

- proto `TenantPlan` struct tag 为 snake_case（`json:"tenant_count,omitempty"` / `json:"created_at,omitempty"` / `json:"updated_at,omitempty"`）。
- Hertz `c.JSON` 走 `encoding/json` 按 struct tag 序列化，与 OpenAPI 契约 `TenantPlan` schema 一致。

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| ListTenantPlans 实现 | service 层 status 校验 + 游标分页 + 组装响应 | ✅ |
| GetTenantPlan 实现 | service 层 UUID 校验 + GetByID + 组装响应 | ✅ |
| store.List 游标分页 | `(created_at, id) < ($n, $n+1)` + `LIMIT N+1` + next_cursor | ✅ |
| store.List total 语义 | total 仅含 status/search 不含 cursor | ✅ |
| store.GetByID tenant_count | 子查询排除 disabled 租户 | ✅ |
| store.GetByID 软删除过滤 | `is_deleted = FALSE` | ✅ |
| status 枚举校验 | `ParseTenantPlanStatusFilter` 空=全部/非法报错 | ✅ |
| plan_id UUID 校验 | 空 → 400 / 非 UUID → 400 / 不存在 → 404 | ✅ |
| JSON snake_case | proto struct tag 与 OpenAPI 一致 | ✅ |
| 编译 | `go build ./services/tenant-service/...` → EXIT=0 | ✅ |
| 测试 | `go test ./services/tenant-service/internal/...` → PASS (3.5s) | ✅ |
| review-it | clean，无 actionable findings | ✅ |

## 验证命令

```bash
cd repo
go build ./services/ani-gateway/... ./services/tenant-service/...
go test ./services/tenant-service/internal/...
```

## 边界声明

- 本 Issue 仅实现 `ListTenantPlans` + `GetTenantPlan`，其余 RPC（Create 已在 #5 实现，Activate/Disable/Delete/UpdateQuotaLimits/GetQuotaLimits/ListBoundTenants/ListAuditLogs）仍为 panic 占位，属后续 Issue。
- 列表项 `TenantPlanListItem` 不含 quota_limits，需另调 `GetQuotaLimitViews`（issue-008）。
- `listItemToPB` 和 `planToPB` 两个映射函数字段完全相同（id/code/name/description/status/tenant_count/created_at/updated_at），列表和详情返回结构一致，区别仅在数据来源（ListItem vs TenantPlan entity）。

## Open Questions

None — 实现按 SPEC §5.1.2 / §5.1.3 和 OpenAPI 契约执行，无偏离。
