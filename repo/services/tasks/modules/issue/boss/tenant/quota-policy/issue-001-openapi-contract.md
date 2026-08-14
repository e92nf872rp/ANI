# Issue 1: OpenAPI 契约 — services/v1.yaml 新增 tenant-plans 路径与 schema

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)

## Description

在 `repo/api/openapi/services/v1.yaml` 中新增套餐管理的完整 OpenAPI 契约，包含 14 个端点的 paths、request/response schemas、错误码定义。Core v1.yaml 不修改。此 Issue 是所有后续实现 Issue 的前置依赖——前端和后端均从此契约生成类型和客户端。

> **实现补充说明：** 原 issue 定稿时为 11 个端点，后续 issue-010/011/012 追加了 3 个端点（PUT 套餐基本信息、GET 配额元数据、GET 可绑定租户），契约同步扩展至 14 个。2026-08-14 再对齐实现：`TenantPlanListItem` 含 description；时间字段为本地展示串；列表 `next_cursor` 空串；`total:null` 物化落库说明；PUT 基本信息幂等为 **optional**。

## Scope
- Product line: boss
- Code paths allowed: `repo/api/openapi/services/v1.yaml` only
- Frozen exclusions: Core v1.yaml 不修改

## Acceptance Criteria
- [x] 新增 `POST /api/v1/svc/tenant-plans` path + request schema（code/name/description/quota_limits）+ response 200 `{ id, message }`
- [x] 新增 `GET /api/v1/svc/tenant-plans` path + query params（limit/cursor/status/search）+ response 200 游标分页结构（items/total/next_cursor）
- [x] 新增 `GET /api/v1/svc/tenant-plans/{planId}` path + response 200 套餐详情
- [x] 新增 `PUT /api/v1/svc/tenant-plans/{planId}` path + request（name/description 可选）+ response 200，**idempotency_key 可选**（issue-010）
- [x] 新增 `GET /api/v1/svc/tenant-plans/{planId}/quota-limits` path + response 200 `{ items[] }`
- [x] 新增 `POST /api/v1/svc/tenant-plans/{planId}/activate` path + response 200
- [x] 新增 `POST /api/v1/svc/tenant-plans/{planId}/disable` path + response 200
- [x] 新增 `DELETE /api/v1/svc/tenant-plans/{planId}` path + response 200
- [x] 新增 `PUT /api/v1/svc/tenant-plans/{planId}/quota-limits` path + request items[] + response 200，标注 Idempotency-Key required
- [x] 新增 `POST /api/v1/svc/tenants/{tenantId}/plan` path + request `{ plan_id }` + response 200，标注 Idempotency-Key required，错误响应含 400/404/409/422（绑定非 active 套餐 → 422 PLAN_NOT_ACTIVE）
- [x] 新增 `GET /api/v1/svc/tenant-plans/{planId}/tenants` path + response 200 `{ items[] }`
- [x] 新增 `GET /api/v1/svc/tenant-plans/{planId}/bindable-tenants` path + response 200 `{ items[] }`（issue-012 追加）
- [x] 新增 `GET /api/v1/svc/tenant-plans/{planId}/audit-logs` path + query params（limit/cursor）+ response 200 游标分页结构（items/total/next_cursor）
- [x] 新增 `GET /api/v1/svc/quota-meta` path + response 200 `{ items[] }`（issue-011 追加）
- [x] 定义所有共享 schemas：TenantPlan、TenantPlanListItem（含 description）、TenantPlanListResponse、PlanQuotaLimitView、PlanQuotaLimitInput、QuotaMetaItem、BoundTenant、PlanAuditLog、IdempotentResult、BindPlanRequest 等
- [x] 定义所有错误码：VALIDATION_FAILED(400)、TENANT_PLAN_NOT_FOUND(404)、PLAN_CODE_CONFLICT(409)、PLAN_STATE_INVALID(409)、TENANT_PLAN_IN_USE(409)、TENANT_STATE_INVALID(409)、TENANT_NOT_FOUND(404)、QUOTA_NOT_FOUND(404)、QUOTA_ALREADY_EXISTS(409)、PLAN_NOT_ACTIVE(422)、QUOTA_RESOURCE_NOT_REGISTERED(422)、GRPC_CLIENT_UNAVAILABLE(502)
- [x] 标注 Idempotency-Key required on: POST /tenant-plans, PUT /tenant-plans/{id}/quota-limits, POST /tenants/{id}/plan, POST activate/disable；**optional** on PUT /tenant-plans/{id}
- [x] Typecheck/lint passes

## Dependencies
None

## Type
backend

## Priority
high

## References
- SPEC: §4.1 Endpoints, §4.2 Request/Response Schemas, §4.3 Error Responses
