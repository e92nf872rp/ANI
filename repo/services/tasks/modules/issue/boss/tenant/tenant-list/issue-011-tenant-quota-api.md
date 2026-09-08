# Issue 11: 租户配额代理查询 — GET /tenants/{tenantId}/quota

> **Status: DONE（以实现为准）**  
> 证据：`repo/development-records/tenant-list-issue-011-tenant-quota-api.md`

## Document Links
- PRD: [prd-new-boss-tenant-list.md](../../../../prd/boss/tenant/prd-new-boss-tenant-list.md)
- UX: [ux-boss-tenant-list.md](../../../../ux/boss/tenant/ux-boss-tenant-list.md)
- SPEC: [spec-new-boss-tenant-list.md](../../../../spec/boss/tenant/spec-new-boss-tenant-list.md)

## Description

US-011：`GetTenantQuota` 代理 Core `GET /admin/tenants/{id}/quota`。**只调一次 GetQuota**（Core 响应已含 display_name/unit）；**不**再二次 `ListQuotaMeta`。空 display_name 兜底为 `resource_type`。

## Scope
- Product line: boss
- Code paths:
  - `repo/services/tenant-service/internal/service/tenant_service.go`
  - 复用既有 `QuotaSvcClient`（无新 Core 端点）

## Acceptance Criteria

- [x] `QuotaSvcClient.GetQuota(tenant_id)` 一次取回 used/total + meta 展示字段
- [x] 响应 `items[]{resource_type, display_name, used, total, unit}`；不分页
- [x] display_name 空 → 用 resource_type 兜底
- [x] 租户不存在 → 404 TENANT_NOT_FOUND
- [x] Core/gRPC 不可达 → Gateway **502 `GRPC_CLIENT_UNAVAILABLE`**（非 503）
- [x] 只读：不写审计

### 测试
- [x] 单测：多维度组装 + 兜底（mock QuotaSvcClient）

## Dependencies
Issue 4；既有 QuotaSvcClient

## Type
backend

## Priority
high

## References
- SPEC: §4.3 / §2.3
- Record: `repo/development-records/tenant-list-issue-011-tenant-quota-api.md`
