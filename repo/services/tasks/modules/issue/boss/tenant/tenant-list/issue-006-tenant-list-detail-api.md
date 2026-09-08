# Issue 6: 租户列表与详情 — GET /tenants + GET /tenants/{id}

> **Status: DONE（以实现为准）**  
> 证据：`repo/development-records/tenant-list-issue-006-tenant-list-detail-api.md`

## Document Links
- PRD: [prd-new-boss-tenant-list.md](../../../../prd/boss/tenant/prd-new-boss-tenant-list.md)
- UX: [ux-boss-tenant-list.md](../../../../ux/boss/tenant/ux-boss-tenant-list.md)
- SPEC: [spec-new-boss-tenant-list.md](../../../../spec/boss/tenant/spec-new-boss-tenant-list.md)

## Description

US-003 / US-004：`ListTenants`（keyset + status/search + plan_code + admin_count）与 `GetTenantDetail`（含 auth 两布尔）。Auth 专用读写见 Issue-9。

## Scope
- Product line: boss
- Code paths:
  - `repo/services/tenant-service/internal/service/tenant_service.go`
  - `repo/pkg/adapters/runtime/postgres_tenant.go`
  - `repo/pkg/ports/tenant.go`
  - `repo/services/tenant-service/internal/repo/adapters/core/tenant_svc_client.go`

## Acceptance Criteria

### ListTenants
- [x] keyset：`created_at DESC, id DESC`；limit 默认 20 / ≤100
- [x] status：`TenantStatus`（active/frozen/disabled）；search：name/display_name ILIKE（**不转义** `%`/`_`）
- [x] Core LATERAL 统计 admin_count（roles.name='tenant-admin'）
- [x] service 批量装 plan_code；套餐已删 → `plan_code=""`（非错误）
- [x] 列表**不含** auth；只读不写审计

### GetTenantDetail
- [x] Core GetTenant additive：contact_email / frozen_at / disabled_at / user_count / admin_count
- [x] 同查询 JOIN `tenant_auth` → `TenantAuthSummary` 仅 sso_enabled/mfa_required
- [x] 缺行 → 双 false；404 TENANT_NOT_FOUND
- [x] 完整 SSO/MFA 配置走 `GET .../auth/sso`（Issue-9）

### 测试
- [x] 单测：分页 / 过滤 / admin_count / auth 摘要（真库集成非强制）

## Dependencies
Issue 3、4；Auth 写见 Issue 9

## Type
backend

## Priority
high

## References
- SPEC: §4.1 / §4.3 / §8.2 / §8.3
- Record: `repo/development-records/tenant-list-issue-006-tenant-list-detail-api.md`
