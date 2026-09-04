# Issue 8: 租户状态机 — freeze / unfreeze / disable

> **Status: DONE（以实现为准）**  
> 证据：`repo/development-records/tenant-list-issue-008-tenant-state-machine-api.md`

## Document Links
- PRD: [prd-new-boss-tenant-list.md](../../../../prd/boss/tenant/prd-new-boss-tenant-list.md)
- UX: [ux-boss-tenant-list.md](../../../../ux/boss/tenant/ux-boss-tenant-list.md)
- SPEC: [spec-new-boss-tenant-list.md](../../../../spec/boss/tenant/spec-new-boss-tenant-list.md)

## Description

US-006 / US-007：三状态转换 + lifecycle 同事务写入。禁用前置仅校验四维 `gpu_count`/`cpu_core`/`memory_gb`/`storage_gb` 的 `used+reserved>0`。

**本批不做：** 禁用时资源释放；登录拦截 `TENANT_FROZEN` / `TENANT_DISABLED`（follow-up）。

## Scope
- Product line: boss
- Code paths:
  - `repo/services/tenant-service/internal/service/tenant_service.go`
  - `repo/pkg/adapters/runtime/postgres_tenant.go`
  - `repo/services/tenant-service/internal/repo/adapters/core/tenant_svc_client.go`

## Acceptance Criteria

### service
- [x] freeze：仅 active；非法 → 409 TENANT_STATE_INVALID
- [x] unfreeze：仅 frozen
- [x] disable：四维任一 used+reserved>0 → 409 TENANT_HAS_RUNNING_RESOURCES；**不释放资源**
- [x] 审计 `tenant.freeze` / `tenant.unfreeze` / `tenant.disable`
- [x] 响应 `{ id, message }`；幂等由 Gateway

### Core（同事务）
- [x] freeze → frozen + frozen_at + lifecycle(`freeze`)
- [x] unfreeze → active + frozen_at=NULL + lifecycle(`unfreeze`)
- [x] disable → disabled + disabled_at + lifecycle(`disable`)；无 enable
- [x] lifecycle action 使用 `ports.TenantLifecycleAction*` 常量；**归因仅 ctx**（方法签名无 user_id/request_id）
- [x] 不改 users.status；保留 resource_quota 行

### 测试
- [x] 单测：四维守卫 / 非法转换 / lifecycle 写入

### Deferred（明确不在本批）
- [ ] 登录拦截 FROZEN/DISABLED
- [ ] 禁用时资源释放编排

## Dependencies
Issue 3、4；QuotaSvcClient.GetQuota

## Type
backend

## Priority
high

## References
- SPEC: §5.3 / §5.2 / §5.4-1
- Record: `repo/development-records/tenant-list-issue-008-tenant-state-machine-api.md`
