# 后端：创建套餐 API

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)

## Description
实现创建套餐后端逻辑：`POST /api/v1/svc/tenant-plans`（对应 SPEC §4.1 / US-001）。填充 `TenantPlanService.CreateTenantPlan` RPC 与 `TenantPlanStore.Create` 持久化，事务内写审计。覆盖 US-001。

> **实现补充说明：** 原 issue 规格提到"本地缓存启用维度集合供校验"，实际实现无缓存，每次调 `QuotaSvcClient.ListQuotaMeta` 实时获取。`code` 唯一性由数据库 partial unique index 保证，Create 时 unique violation → `PLAN_CODE_CONFLICT`，无需独立 `GetByCode` 方法。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/tenant_plan_service.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_audit_store.go`、`repo/services/tenant-service/internal/repo/adapters/core/quota_svc_client.go`

## Acceptance Criteria
- [x] `POST /tenant-plans` 入参 code/name/description/quota_limits；OpenAPI 要求 body `idempotency_key`（实际去重由 Gateway 中间件，header 亦可；service 不校验该字段）
- [x] code 格式 `^[a-z0-9-]{3,40}$` 校验，未删除间冲突 → 409 `PLAN_CODE_CONFLICT`（由数据库 partial unique index 保证，unique violation → 业务码映射）
- [x] name 1-64 字符、description ≤ 512 字符
- [x] quota_limits 每项 resource_type 校验（enabled=true）：调 `QuotaSvcClient.ListQuotaMeta`（Core `GET /admin/quota-meta`）取启用维度集合，resource_type 不在启用集合 → 422 `QUOTA_RESOURCE_NOT_REGISTERED`；total null → **物化为 default_quota 落库**（或 >= 0）；同一 resource_type 不可重复 → 400 `VALIDATION_FAILED`
- [x] 首次实现 `QuotaSvcClient.ListQuotaMeta`（Core `GET /admin/quota-meta`）：返回 `[]QuotaMeta{ ResourceType, Enabled }`，~~本地缓存启用维度集合~~ **无缓存，每次实时调 Core**；超时 + debug 请求日志；Core 不可用时降级返回 502 `GRPC_CLIENT_UNAVAILABLE`
- [x] 实现 `TenantPlanStore.Create`：事务内 INSERT tenant_plans(status='draft') + INSERT plan_quota_limits
- [x] 实现 `TenantPlanAuditStore.Create`：事务提交后 best-effort INSERT audit_logs(action='tenant_plan.create', resource='tenant_plan', details={plan_id, code, quota_limits})
- [x] `TenantPlanService.CreateTenantPlan` RPC 方法体实现，返回 `tenantv1.TenantPlan` / `{ id, message: "tenant plan created" }`
- [x] 网关层 POST 转发与错误码映射在 #4 网关 issue 完成
- [x] 单元测试：code 格式/冲突 409、维度校验 422、事务一致性、审计写入（SPEC §9.1 TestTenantPlanService_Create + DuplicateCode + #9.3 Test_Create_QuotaResourceNotRegistered）
- [x] `go build ./...` 编译通过

## Dependencies
#2（接口与结构体）、#3（数据库迁移）、#4（网关）

## Type
backend

## Priority
high

## References
- SPEC: §5.1.1 Create / §4.2 创建 schema / §6.1 errors / §9
- Plan: 租户管理plan v3.0 §5.3
