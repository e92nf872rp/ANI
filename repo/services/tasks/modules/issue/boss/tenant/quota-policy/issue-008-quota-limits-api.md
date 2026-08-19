# 后端：限额查询 + 修改 API（含 QuotaSvcClient 基础设施）

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)
- Core API: [配额core层对接api设计.md](../../../../../配额core层对接api设计.md)

## Description
实现套餐限额域（关联 API 合并为一个 issue）：`GET /tenant-plans/{planId}/quota-limits`（查询展示视图）与 `PUT /tenant-plans/{planId}/quota-limits`（修改并同步存量租户到 Core）。填充 `GetTenantPlanQuotaLimits`/`UpdateTenantPlanQuotaLimits` RPC 与 `TenantPlanStore.GetQuotaLimits`/`UpdateQuotaLimits`，并首次实现 **QuotaSvcClient**（调 Core API 批量下发）基础设施。覆盖 US-004、US-008。

> **实现补充说明：** 原 issue 规格提到 `TenantPlanStore.GetQuotaLimitViews`（store 方法，JOIN resource_quota_meta），实际改为 service 层 `buildQuotaLimitViews` 函数（store.GetQuotaLimits 返回原始行含 NULL + Core ListQuotaMeta 组装兜底）。原规格提到"本地缓存启用维度集合"，实际无缓存，每次实时调 Core。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/tenant_plan_service.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go`、`repo/services/tenant-service/internal/repo/adapters/core/quota_svc_client.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_audit_store.go`

## Acceptance Criteria
### 查询限额（US-004）
- [x] `GET /tenant-plans/{planId}/quota-limits` 返回 resource_type/display_name/unit/total
- [x] 实现 `TenantPlanStore.GetQuotaLimits`（返回原始行，保留 NULL 语义）；~~`GetQuotaLimitViews`~~ — **改为 service 层 `buildQuotaLimitViews` 函数**（store 原始行 + Core ListQuotaMeta 组装，COALESCE total/default_quota 兜底）
- [x] `TenantPlanService.GetTenantPlanQuotaLimits` RPC 方法体实现

### 修改限额 + 同步存量租户（US-008）
- [x] `PUT /tenant-plans/{planId}/quota-limits` 入参 items[{resource_type, total}]，需 `idempotency_key`（可选）
- [x] items 至少 1 项、每项 total >= 0 或 null、同一 resource_type 不可重复 → 400 `VALIDATION_FAILED`；resource_type 未注册/enabled=false → 422 `QUOTA_RESOURCE_NOT_REGISTERED`
- [x] 任意状态（draft/active/disabled）均可修改限额
- [x] 实现 `TenantPlanStore.UpdateQuotaLimits`：事务内仅 UPSERT plan_quota_limits（INSERT ... ON CONFLICT (plan_id, resource_type) DO UPDATE SET total=EXCLUDED.total），事务内不写审计日志。nil total → default_quota 兜底在 service 层 `mapAndValidateQuotaLimits` 中完成，store 层接收的 total 始终为具体值
- [x] 审计日志在事务提交后写入：INSERT audit_logs(action='tenant_plan.update_quota_limits', details={plan_id/updated_dimensions/synced_tenant_count/skipped_approved/tightened})（对齐 issue-007 审计与业务解耦模式：审计失败不回滚已提交的限额变更）
- [x] 同步存量租户：查询 tenants WHERE plan_id=planId AND status!='disabled'，逐租户取 `GetApprovedQuotaChanges`（查 tenant_quota_change WHERE tenant_id=? AND status='approved'，已 approved 维度跳过），批量调 QuotaSvcClient
- [x] 首次实现 **QuotaSvcClient**（对齐 Core SDK `sdks/core/go/anisdk/client.go`，5 个方法）：
  - `GetQuota(ctx, tenantID)` → Core `GET /admin/tenants/{tenant_id}/quota`（issue-009 绑定前判断配额行是否存在，本 issue 一并落地基础设施）
  - `PutQuota(ctx, tenantID, items)` → Core `PUT /admin/tenants/{tenant_id}/quota`（本 issue 改限额同步用），提取 tightened 字段（=true 不报错）
  - `CreateQuota(ctx, tenantID, items)` → Core `POST /admin/tenants/{tenant_id}/quota`（issue-009 绑定新建配额行用，本 issue 一并落地基础设施）
  - `DeleteQuota(ctx, tenantID)` → Core `DELETE /admin/tenants/{tenant_id}/quota`（租户禁用清理用，预留）
  - `ListQuotaMeta(ctx)` → Core `GET /admin/quota-meta`（校验维度 enabled；#5 已首次实现，本 issue 复用）；~~本地缓存~~ **无缓存，每次实时调 Core**
  - 错误映射：`TENANT_NOT_FOUND`(404)/`QUOTA_NOT_FOUND`(404)/`QUOTA_ALREADY_EXISTS`(409)/`QUOTA_RESOURCE_NOT_REGISTERED`(422)/`VALIDATION_FAILED`(400)
  - 超时 + 连接池 + debug 请求日志
- [x] Core API 同步失败时记录 audit_logs(action='tenant.quota_init_failed') + 异步重试（最多 3 次，指数退避，`scheduleQuotaSyncRetry`）
- [x] 响应 `{ id, message: "quota limits updated" }`
- [x] 单元测试：mock Core API，验证 approved 维度跳过、tightened 不报错、同步租户计数（SPEC §9.1 UpdateQuotaLimits + §9.3 Test_UpdateQuotaLimits_Tightened）
- [x] `go build ./...` 编译通过

## Dependencies
#2、#3、#6

## Type
backend

## Priority
high

## References
- SPEC: §5.1.2 UpdateQuotaLimits / §4.2 quota-limits PUT schema / §6.2 Retry / §9
- Core API: §2 修改配额端点
- Plan: 租户管理plan v3.0 §5.3
