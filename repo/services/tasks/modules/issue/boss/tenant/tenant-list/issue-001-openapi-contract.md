# Issue 1: OpenAPI 契约 — v1.yaml 新增租户列表管理端点与 schema

> **Status: DONE（以实现为准）**  
> 证据：`repo/development-records/tenant-list-issue-001-openapi-contract.md`

## Document Links
- PRD: [prd-new-boss-tenant-list.md](../../../../prd/boss/tenant/prd-new-boss-tenant-list.md)
- UX: [ux-boss-tenant-list.md](../../../../ux/boss/tenant/ux-boss-tenant-list.md)
- SPEC: [spec-new-boss-tenant-list.md](../../../../spec/boss/tenant/spec-new-boss-tenant-list.md)

## Description

在 `repo/api/openapi/v1.yaml`（Core）与 `repo/api/openapi/services/v1.yaml`（Services）中落地租户列表管理完整 OpenAPI 契约。Core 新增 9 个 `/admin/tenants*` 端点并扩展 `getTenant`；Services 新增 19 个相对路径 `/tenants*`（`servers[0].url` = `https://{host}/api/v1/svc`）。**SDK / pb.go 由人工生成，本 Issue 不生成。**

## Scope
- Product line: boss（Core + Services 契约层）
- Code paths: `repo/api/openapi/v1.yaml` + `repo/api/openapi/services/v1.yaml` only

## Acceptance Criteria

### Core v1.yaml（9 端点 + 1 扩展）
- [x] `POST /admin/tenants`（createTenant）：request 含 name/display_name/email/plan_id/admin_*；`admin_password_hash`（Core 不收明文）
- [x] `GET /admin/tenants`（listTenants）：limit/cursor/status/search；items 含 admin_count
- [x] `PUT /admin/tenants/{tenant_id}`（updateTenant）：display_name?/contact_email?；不可改 name/status
- [x] `POST .../freeze` / `unfreeze` / `disable`
- [x] `GET|PUT /admin/tenants/{tenant_id}/auth`（单一 Core auth 读写；Services SSO/MFA 拆两 PUT 映射到此）
- [x] `GET /admin/tenants/{tenant_id}/lifecycle`：limit/cursor/action（`create|freeze|unfreeze|disable`）
- [x] `getTenant` additive：contact_email / frozen_at / disabled_at / user_count / admin_count / auth（仅 sso_enabled/mfa_required）
- [x] 复用既有：getTenantQuota、listTenantUsers、listAvailableTenants、quota upsert（不在本模块重复定义）

### services/v1.yaml（19 端点，相对 `/tenants*`）
- [x] `GET /tenants/available-plans` → `{ items[] }`（仅 active）
- [x] `POST /tenants`：body **`idempotency_key` required**（可回落 `Idempotency-Key` header）；`{ id, message }`
- [x] `GET /tenants` / `GET /tenants/{tenantId}`（详情含 auth 两布尔）
- [x] `PUT /tenants/{tenantId}`；`POST .../freeze|unfreeze|disable`（均 body `idempotency_key`）
- [x] `GET|PUT /tenants/{id}/auth/sso`；`POST .../auth/sso/test`；`PUT .../auth/mfa`
- [x] `GET .../quota`；`POST|GET .../quota-requests`；`POST .../quota-requests/{reqId}/approve`
- [x] `GET .../lifecycle`；`GET .../audit-logs`；`GET .../admins`

### 共享 schemas 与错误码
- [x] Schemas：TenantListItem、TenantDetail、TenantAuthSummary、TenantAuthConfig、SsoTestResult、QuotaChangeRequest*、TenantLifecycleEntry、TenantAuditLogEntry、CursorPage 等
- [x] 错误码：VALIDATION_FAILED(400)、TENANT_NOT_FOUND(404)、TENANT_NAME_CONFLICT(409)、TENANT_STATE_INVALID(409)、TENANT_HAS_RUNNING_RESOURCES(409)、PLAN_NOT_ACTIVE(422)、TENANT_SSO_CONFIG_INVALID(422)、QUOTA_CHANGE_REQUEST_INVALID(422)、**QUOTA_RESOURCE_NOT_REGISTERED(422)**、**QUOTA_CHANGE_REQUEST_CONFLICT(409)**、QUOTA_CHANGE_REQUEST_NOT_PENDING(409)、QUOTA_CHANGE_REQUEST_NOT_FOUND(404)、GRPC_CLIENT_UNAVAILABLE(502)
- [x] YAML / API-split 校验通过

## Implementation notes（以实现为准）
- Services 路径写相对 `/tenants*`，不要写完整 `/api/v1/svc/tenants*`。
- Core 写端点无 idempotency；Services 写端点以 body `idempotency_key` 为主。
- SSO/MFA：Services 两 PUT → Core 单一 `PUT .../auth`。

## Dependencies
None

## Type
backend

## Priority
high

## References
- SPEC: §4.1 / §4.2 / §4.3 / §6.1
- Record: `repo/development-records/tenant-list-issue-001-openapi-contract.md`
