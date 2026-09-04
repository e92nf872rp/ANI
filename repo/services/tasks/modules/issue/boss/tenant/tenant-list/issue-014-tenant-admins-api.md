# Issue 14: 租户内管理员列表 — GET /tenants/{tenantId}/admins

> **Status: DONE（以实现为准）**  
> 证据：`repo/development-records/tenant-list-issue-014-tenant-admins-api.md`

## Document Links
- PRD: [prd-new-boss-tenant-list.md](../../../../prd/boss/tenant/prd-new-boss-tenant-list.md)
- UX: [ux-boss-tenant-list.md](../../../../ux/boss/tenant/ux-boss-tenant-list.md)
- SPEC: [spec-new-boss-tenant-list.md](../../../../spec/boss/tenant/spec-new-boss-tenant-list.md)
- 先例：[tenant-admin/issue-008-list-all-admins-api.md](../tenant-admin/issue-008-list-all-admins-api.md)

## Description

US-017：默认集合 = 该租户全部 **`tenant-admin`** ∪ **`inviting` 邀请目标**（不含无邀请普通成员；本阶段默认不含 expired）。复用 tenant-admin 模块 Core 列表 + 邀请表合并；**不新增 Core 端点**。

## Scope
- Product line: boss
- Code paths:
  - `repo/services/tenant-service/internal/service/tenant_service.go`（`ListTenantAdmins`；可同包复用 `TenantAdminService` helper）
  - 复用 `TenantAdminSvcClient` / `TenantAdminStore`
  - Proto：`TenantScopedAdmin`；OpenAPI 可仍引用 `AdminWithTenant`（形状对齐，类型名可不同）

## Acceptance Criteria

### 默认集合与字段
- [x] 未传收窄过滤：全部 tenant-admin ∪ 全部 inviting（去重；admin 且 inviting → 一行且 `is_inviting=true`）
- [x] 非 admin 且无 inviting **不得**出现
- [x] 展示字段：id / username / display_name / role / status；另含 email、`is_inviting`、`is_expired`、source、last_login_at、tenant{id,name,display_name}
- [x] **不**返回 `permissions[]`
- [x] `is_inviting` / `is_expired` 仅标记，不改写 role/status

### 查询与分页
- [x] keyset 游标；limit 默认 20 / ≤100
- [x] 可选 search / status / role；非法枚举 → 400（**可先于 GetTenant 校验**）
- [x] **不**暴露跨租户列表的 is_inviting/is_expired 三选一 query
- [x] 实现可为「内存合并后再 keyset 切片」（以实现为准）

### 错误与副作用
- [x] 404 TENANT_NOT_FOUND；不可达 → Unavailable / 502
- [x] 只读：不写审计、无幂等键

### 测试
- [x] 单测：2 admin + inviting + 普通 user 排除；去重；翻页

## Implementation notes
- `tenant_plan.proto` 若仍注释 “listTenantUsers proxy”，以本 Issue **Core admin 列表 + 邀请表** 为准。

## Dependencies
Issue 4；tenant-admin 模块已交付能力

## Type
backend

## Priority
high

## References
- SPEC: §4.1；tenant-admin 列表合并模式
- PRD: US-017
- Record: `repo/development-records/tenant-list-issue-014-tenant-admins-api.md`
- OpenAPI: `listTenantScopedAdmins`
