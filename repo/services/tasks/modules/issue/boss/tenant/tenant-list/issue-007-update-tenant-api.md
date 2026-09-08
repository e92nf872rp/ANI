# Issue 7: 修改租户基本信息 — PUT /tenants/{tenantId}

> **Status: DONE（以实现为准）**  
> 证据：`repo/development-records/tenant-list-issue-007-update-tenant-api.md`

## Document Links
- PRD: [prd-new-boss-tenant-list.md](../../../../prd/boss/tenant/prd-new-boss-tenant-list.md)
- UX: [ux-boss-tenant-list.md](../../../../ux/boss/tenant/ux-boss-tenant-list.md)
- SPEC: [spec-new-boss-tenant-list.md](../../../../spec/boss/tenant/spec-new-boss-tenant-list.md)

## Description

US-005：部分更新 display_name / contact_email；不可改 name/status；disabled 终态不可编辑。

## Scope
- Product line: boss
- Code paths:
  - `repo/services/tenant-service/internal/service/tenant_service.go`
  - `repo/pkg/adapters/runtime/postgres_tenant.go`
  - `repo/services/tenant-service/internal/repo/adapters/core/tenant_svc_client.go`

## Acceptance Criteria

### service
- [x] 至少提供一个可更新字段，否则 400
- [x] **disabled → 409 TENANT_STATE_INVALID**（service 预检）；frozen 可改
- [x] 成功审计 `tenant.update`；响应 `{ id, message }`

### Core
- [x] **动态 SET**（仅更新提供的字段），非盲目 COALESCE 全列
- [x] `WHERE id AND status <> 'disabled'`：disabled 与不存在均 **404**（OpenAPI Core 口径）；service 层对 disabled 先发 409
- [x] 不触碰 name / status / plan_id

### 测试
- [x] 单测：空更新、disabled、部分字段更新

## Dependencies
Issue 3、4

## Type
backend

## Priority
high

## References
- SPEC: §4.1 / §5.2
- Record: `repo/development-records/tenant-list-issue-007-update-tenant-api.md`
