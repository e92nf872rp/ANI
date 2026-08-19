# 后端：绑定套餐 + 绑定租户列表 API（含 TenantStore 基础设施）

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)
- Core API: [配额core层对接api设计.md](../../../../../配额core层对接api设计.md)

## Description
实现套餐绑定域（关联 API 合并为一个 issue）：`POST /tenants/{tenantId}/plan`（绑定套餐并下发 Core 配额）与 `GET /tenant-plans/{planId}/tenants`（绑定租户列表）。填充 `TenantService.BindPlanQuota`、`TenantPlanService.ListTenantPlanBoundTenants` RPC，实现 `TenantStore.GetByID`/`UpdatePlan` 基础设施与 `TenantPlanStore.GetQuotaLimits`（绑定用）/`GetApprovedQuotaChanges`/`ListBoundTenants`。覆盖 US-009、US-010。

> **实现补充说明：** 原 issue 规格提到 `TenantPlanStore.GetQuotaLimitViews`（store 方法），实际改为 service 层 `buildQuotaLimitViews` 函数（同 issue-008）。Core 同步失败时有 best-effort 回滚 `tenants.plan_id`（尝试恢复原 plan_id，但回滚失败不阻塞）。审计失败为 best-effort（只 Warn 不阻塞成功响应，与 issue-010/013 对齐）。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/tenant_service.go`、`repo/services/tenant-service/internal/service/tenant_plan_service.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_store.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_audit_store.go`、`repo/services/tenant-service/internal/repo/adapters/core/quota_svc_client.go`

## Acceptance Criteria
### 绑定套餐（US-009）
- [x] `POST /tenants/{tenantId}/plan` 入参 plan_id，需 `idempotency_key`（可选）
- [x] 校验：plan_id 存在且 is_deleted=FALSE（否则 404 `TENANT_PLAN_NOT_FOUND`）+ status='active'（否则 422 `PLAN_NOT_ACTIVE`）+ tenant.status != 'disabled'（否则 409 `TENANT_STATE_INVALID`）
- [x] 实现 `TenantStore.GetByID`（查租户 status 判 disabled）+ `UpdatePlan`（改 tenants.plan_id）
- [x] 读 `TenantPlanStore.GetQuotaLimits` + service 层 `buildQuotaLimitViews`（COALESCE total, default_quota 兜底为具体 total）计算待下发有效值
- [x] 取 `GetApprovedQuotaChanges`，已 approved 维度跳过；收集待下发维度后调 QuotaSvcClient 下发配额（对齐 Core SDK 语义）：
  - 先 `GetQuota`（Core `GET /admin/tenants/{tenant_id}/quota`）判断配额行是否存在
  - 不存在 → `CreateQuota`（Core `POST`，新建配额行，used/reserved 初始 0）
  - 已存在（更换套餐场景）→ `PutQuota`（Core `PUT`，修改 total，自动收紧；提取 tightened=true 不报错）
- [x] Core 错误映射：`QUOTA_ALREADY_EXISTS`(409)/`TENANT_NOT_FOUND`(404)/`QUOTA_RESOURCE_NOT_REGISTERED`(422)
- [x] 成功后 `TenantStore.UpdatePlan` 先更新 tenants.plan_id（若与当前不同），再同步 Core；Core 同步失败时 best-effort 回滚 plan_id（回滚失败也返回错误）
- [x] 写审计 action='tenant.bind_plan_quota'，details={plan_id/tenant_id/tenant_name/tenant_display_name/skipped_approved/tightened/updated}；审计失败 best-effort（只 Warn 不阻塞成功响应）
- [x] 响应 `{ id, message: "quota bound to plan" }`
- [x] `TenantService.BindPlanQuota` gRPC 方法体实现

### 绑定租户列表（US-010）
- [x] `GET /tenant-plans/{planId}/tenants` 返回 items[]{id/name/display_name/status}，不分页
- [x] 实现 `TenantPlanStore.ListBoundTenants`（SELECT ... FROM tenants WHERE plan_id=$planId AND status!='disabled'）
- [x] 套餐不存在 → 404 `TENANT_PLAN_NOT_FOUND`
- [x] `TenantPlanService.ListTenantPlanBoundTenants` RPC 方法体实现

### 通用
- [x] 网关转发与错误码映射在 #4 网关 issue 完成
- [x] 单元测试：bind plan 校验（plan 404/非 active 422/disabled 409）、approved 维度跳过、绑定租户列表（SPEC §9.1 TestTenantService_BindPlanQuota + §9.3 Test_BindPlanQuota_ApprovedSkip/DisabledTenant）
- [x] `go build ./...` 编译通过

## Dependencies
#2、#3、#8（复用 QuotaSvcClient）

## Type
backend

## Priority
high

## References
- SPEC: §5.1.3 BindPlanQuota / §5.4 Edge Cases / §4.2 绑定 schema / §6.1 / §9
- Core API: §2 修改配额端点
- Plan: 租户管理plan v3.0 §5.3
