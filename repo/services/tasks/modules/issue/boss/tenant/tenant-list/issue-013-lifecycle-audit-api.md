# Issue 13: 生命周期与操作历史 — GET /lifecycle + GET /audit-logs

> **Status: DONE（以实现为准）**  
> 证据：`repo/development-records/tenant-list-issue-013-lifecycle-audit-api.md`

## Document Links
- PRD: [prd-new-boss-tenant-list.md](../../../../prd/boss/tenant/prd-new-boss-tenant-list.md)
- UX: [ux-boss-tenant-list.md](../../../../ux/boss/tenant/ux-boss-tenant-list.md)
- SPEC: [spec-new-boss-tenant-list.md](../../../../spec/boss/tenant/spec-new-boss-tenant-list.md)

## Description

US-015 / US-016：lifecycle 走 Core `tenant_lifecycle`；audit-logs 直读 service 侧 `audit_logs`（`AuditStore.ListTenantAuditLogs`）。两端点只读。

## Scope
- Product line: boss
- Code paths:
  - `repo/services/tenant-service/internal/service/tenant_service.go`
  - `repo/pkg/adapters/runtime/postgres_tenant.go`（`ListTenantLifecycle`）
  - `repo/services/tenant-service/internal/repo/adapters/core/tenant_svc_client.go`
  - `repo/services/tenant-service/internal/repo/adapters/postgres/audit_store.go`（`ListTenantAuditLogs`）

## Acceptance Criteria

### ListTenantLifecycle
- [x] Core keyset：`created_at DESC, id DESC`；limit 默认 20 / ≤100
- [x] action 过滤：`TenantLifecycleAction` = `create|freeze|unfreeze|disable`（**≠** `TenantStatus` 的 active/frozen/disabled）
- [x] 非法 action → 400（校验可先于/并行于存在性检查，以实现为准）
- [x] 租户不存在 → 404 TENANT_NOT_FOUND
- [x] items：id/action/reason/user_id/request_id/created_at

### ListTenantAuditLogs
- [x] `AuditStore.ListTenantAuditLogs`：tenant_id + 可选 action/result + keyset
- [x] items：id/action/resource/result/details/user_id/created_at（resource 透传展示，无过滤参数）
- [x] 非法 result/cursor → 400；租户存在性经 GetTenant 等路径校验 → 404
- [x] **不是**复用 `TenantPlanAuditStore` 类型名；tenant 作用域独立方法

### 通用
- [x] 只读：不写审计、无幂等键

### 测试
- [x] 单测：lifecycle 分页/过滤；audit 过滤与顺序

## Dependencies
Issue 3、4；数据依赖 Issue 5/8 产生的记录

## Type
backend

## Priority
high

## References
- SPEC: §4.1 / §8.3
- Record: `repo/development-records/tenant-list-issue-013-lifecycle-audit-api.md`
