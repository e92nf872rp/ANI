# Issue 9: SSO/MFA 认证配置 — GET/PUT auth/sso + PUT auth/mfa

> **Status: DONE（以实现为准）**  
> 证据：`repo/development-records/tenant-list-issue-009-tenant-auth-api.md`  
> **不含** TestTenantSso（Issue-010，当前 stub→501）。

## Document Links
- PRD: [prd-new-boss-tenant-list.md](../../../../prd/boss/tenant/prd-new-boss-tenant-list.md)
- UX: [ux-boss-tenant-list.md](../../../../ux/boss/tenant/ux-boss-tenant-list.md)
- SPEC: [spec-new-boss-tenant-list.md](../../../../spec/boss/tenant/spec-new-boss-tenant-list.md)

## Description

US-008 / US-009 写部分 / US-010：`GetTenantAuth`、`UpdateTenantSso`、`UpdateTenantMfa`。Services 两 PUT 映射 Core 单一 `UpdateTenantAuth`。

## Scope
- Product line: boss
- Code paths:
  - `repo/services/tenant-service/internal/service/tenant_service.go`
  - `repo/pkg/adapters/runtime/postgres_tenant.go`
  - `repo/services/tenant-service/internal/repo/adapters/core/tenant_svc_client.go`

## Acceptance Criteria

### GetTenantAuth
- [x] 返回 `{ sso_enabled, provider, mfa_required, updated_at }`
- [x] 缺行防御：双 false；租户不存在 → 404

### UpdateTenantSso
- [x] 有效 sso_enabled=true 时 provider 必填（联动）→ 否则 422 TENANT_SSO_CONFIG_INVALID
- [x] provider：`null`/省略 = 不更新；`""` = 清空
- [x] **disabled 租户改 Auth → Core 409 TENANT_STATE_INVALID**；frozen 允许
- [x] Core **动态 SET**；缺行时可插入/回填，`updated_at=Now()`
- [x] 审计 `tenant.sso.update`

### UpdateTenantMfa
- [x] Gateway 要求 body 显式带 `mfa_required`（omit/null → 400；proto bool 无法区分未传）
- [x] 审计 `tenant.mfa.update`；响应 `{ id, message }`

### 测试
- [x] 单测：联动 422、缺行默认、disabled 409

### Deferred
- [ ] MFA 拦截登录（产品 follow-up）
- [ ] TestTenantSso → Issue-010

## Dependencies
Issue 3、4

## Type
backend

## Priority
high

## References
- SPEC: §4.2 / §5.2 / §5.4-6
- Record: `repo/development-records/tenant-list-issue-009-tenant-auth-api.md`
