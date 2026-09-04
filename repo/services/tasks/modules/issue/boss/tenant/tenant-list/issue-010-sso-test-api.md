# Issue 10: SSO 测试连接 — POST /tenants/{tenantId}/auth/sso/test

> **Status: OPEN / stub only（以实现为准）**  
> 当前：`TestTenantSso` → `tenantRPCNotImplemented()` → Gateway **501 `NOT_IMPLEMENTED`**  
> 无 `development-records/tenant-list-issue-010-*.md`；`main.go` 中 `ssoLoader`/`oidcTester` 为 **nil**；无 `adapters/sso/` 实现。

## Document Links
- PRD: [prd-new-boss-tenant-list.md](../../../../prd/boss/tenant/prd-new-boss-tenant-list.md)
- UX: [ux-boss-tenant-list.md](../../../../ux/boss/tenant/ux-boss-tenant-list.md)
- SPEC: [spec-new-boss-tenant-list.md](../../../../spec/boss/tenant/spec-new-boss-tenant-list.md)

## Description

US-009 测试部分：读当前 SSO 配置 → 加载 K8s Secret → OIDC discovery → 返回结果。**不写库、不写审计、不改 Core。**

**已具备（前置，不算本 Issue 完成）：** OpenAPI `testTenantSso`、Gateway 路由、proto RPC、ports `SsoConfigLoader`/`OidcDiscoveryTester`。

## Scope
- Product line: boss
- Code paths（待实现）:
  - `repo/services/tenant-service/internal/service/tenant_service.go`（填 `TestTenantSso`）
  - `repo/services/tenant-service/internal/repo/adapters/sso/`（新建 loader + oidc tester）
  - `repo/services/tenant-service/main.go`（注入非 nil）

## Acceptance Criteria

### 编排
- [ ] Core GetTenantAuth：sso_enabled=false 或 provider 空 → 422 TENANT_SSO_CONFIG_INVALID
- [ ] `SsoConfigLoader.Load`：读平台 namespace Secret（约定对齐 SPEC §11.1；默认假定 `ani-sso-{tenant_id}`，键 issuer_url/client_id/client_secret）
- [ ] Secret 缺失 → `{ success:false, error:"sso config not found" }`（非 5xx）

### OIDC
- [ ] GET `{issuer}/.well-known/openid-configuration`，超时有界（如 10s）
- [ ] 失败 → `{ success:false, error:"…" }`；成功 → `{ success:true, discovery_result, tested_at }`

### 边界
- [ ] 不写 tenant_auth / audit_logs
- [ ] **HTTP 方法为 POST**（非 GET）；幂等由产品决定（当前契约无 body idempotency）
- [ ] Core 不可达：沿既有 GetTenantAuth 映射（Gateway 侧多为 502 `GRPC_CLIENT_UNAVAILABLE`）

### 测试
- [ ] httptest：discovery 成功/404/超时；未启用 SSO → 422；Secret 缺失

## Dependencies
Issue 4、9；SPEC §11.1 Secret 约定

## Type
backend

## Priority
high

## References
- SPEC: §2.3 / §5.4-7 / §11.1 Q1
- PRD: US-009 测试连接
