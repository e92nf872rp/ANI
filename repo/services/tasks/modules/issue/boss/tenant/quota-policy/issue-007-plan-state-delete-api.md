# 后端：套餐状态转换 + 删除 API

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)

## Description
实现套餐状态域（关联 API 合并为一个 issue）：`POST /tenant-plans/{planId}/activate`（发布）、`POST /tenant-plans/{planId}/disable`（禁用）、`DELETE /tenant-plans/{planId}`（软删除，校验无租户关联）。填充 `ActivateTenantPlan`/`DisableTenantPlan`/`DeleteTenantPlan` RPC 与 `TenantPlanStore.Activate`/`Disable`/`Delete`。覆盖 US-005、US-006、US-007。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/tenant_plan_service.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_audit_store.go`

## Acceptance Criteria
### 状态机（US-005 / US-006）
- [x] `POST /activate`：draft/disabled → active；active 再 activate → 409 `PLAN_STATE_INVALID`；需 `idempotency_key`
- [x] `POST /disable`：active → disabled；draft 直接 disable → 409 `PLAN_STATE_INVALID`；需 `idempotency_key`
- [x] 实现 `TenantPlanStore.Activate`（UPDATE status='active' WHERE status IN ('draft','disabled')）/ `Disable`（UPDATE status='disabled' WHERE status='active'）+ `stateTransitionReject` 区分 404/409
- [x] 状态转换成功写审计：`tenant_plan.activate` / `tenant_plan.disable`

### 删除（US-007）
- [x] `DELETE /tenant-plans/{planId}` 软删除（is_deleted=TRUE + deleted_at=now()），任意状态均可删；不幂等
- [x] 有租户关联（COUNT tenants WHERE plan_id=? AND status!='disabled'）> 0 → 409 `TENANT_PLAN_IN_USE`
- [x] 实现 `TenantPlanStore.Delete`（3 步：存在性检查 → 绑定租户检查 → 软删除；软删除不触发 ON DELETE CASCADE，plan_quota_limits 行随套餐行保留）
- [x] 删除写审计 `tenant_plan.delete`
- [x] 响应 `{ id, message: "tenant plan activated/disabled/deleted" }`

### RPC 落点
- [x] `TenantPlanService.ActivateTenantPlan` / `DisableTenantPlan` / `DeleteTenantPlan` RPC 方法体实现
- [x] 网关转发与错误码映射在 #4 网关 issue 完成
- [x] 单元测试：激活/禁用/删除、active 再 activate 409、draft disable 409、有租户关联 409、软删除后 code 可复用（SPEC §9.1 + §9.3 Test_Delete_SoftDeleteCodeReuse）
- [x] `go build ./...` 编译通过

## Dependencies
#2、#3、#5（审计 Write 复用）、#6

## Type
backend

## Priority
high

## References
- SPEC: §5.3 State Machine / §5.1.4 Delete / §4.2 activate|disable|DELETE schema / §6.1 / §9
- Plan: 租户管理plan v3.0 §5.3
