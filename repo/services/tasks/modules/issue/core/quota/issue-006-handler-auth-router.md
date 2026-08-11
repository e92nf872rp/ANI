# 实现 QuotaAdminService 5 个 Core API handler + 鉴权扩展 + router 接线

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/quota/prd-quota-service.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/quota/spec-quota-service.md`
- Plan: `repo/services/tasks/modules/plan/plan-quota-service.md`

## Description

实现 5 个 Core API handler（持有 `QuotaAdminService` 接口，照搬 demo_instances.go 模式），扩展 `scopeAllowedForPath` 放行 `/api/v1/admin/*` 要求 platform scope，并在 router 注册 QuotaAdminService。

## Scope

- Product line: core
- Code paths allowed: `repo/services/ani-gateway/internal/router/`, `repo/services/ani-gateway/internal/middleware/`

## Acceptance Criteria

- [ ] 新增 `repo/services/ani-gateway/internal/router/quota_resources.go`，定义 `quotaAPI` struct + `registerQuotaResources` 函数注册 5 个路由
- [ ] `createTenantQuota`：解析 `QuotaCreateRequest` → 调 `CreateTenantQuota` → 响应 `Quota`(200) 或错误码
- [ ] `updateTenantQuota`：解析 `QuotaUpdateRequest` → 调 `UpdateTenantQuota` → 响应 `Quota`(200)，保留 tightened 字段
- [ ] `getTenantQuota`：调 `GetTenantQuota` → 响应 `Quota`(200) 或 TENANT_NOT_FOUND(404)，tightened 用 omitempty 省略
- [ ] `deleteTenantQuota`：调 `DeleteTenantQuota` → 响应 `QuotaDeleteResponse`(200) 或 TENANT_NOT_FOUND(404)
- [ ] `listQuotaMeta`：调 `ListQuotaMeta` → 响应 `QuotaMetaListResponse`(200)
- [ ] 错误统一用 `writeDemoError` 三段式 + `middleware.GetRequestID(c)`
- [ ] tenant_id 全部从路径参数 `c.Param("tenant_id")` 取
- [ ] 哨兵错误映射：ErrTenantNotFound→404/TENANT_NOT_FOUND、ErrQuotaNotFound→404/QUOTA_NOT_FOUND、ErrQuotaResourceNotRegistered→422/QUOTA_RESOURCE_NOT_REGISTERED、ErrQuotaAlreadyExists→409/QUOTA_ALREADY_EXISTS
- [ ] 修改 `middleware/auth.go` 的 `scopeAllowedForPath`，新增 `/api/v1/admin/` 前缀放行 platform scope
- [ ] 修改 `router/router.go` 的 `RegisterOptions` 新增 `QuotaAdminService ports.QuotaAdminService` 字段
- [ ] `RegisterWithOptions` 新增 `registerQuotaResources(v1, options.QuotaAdminService)` 调用
- [ ] 调研确认无现有 `/api/v1/admin/` 路由被误伤
- [ ] Typecheck/lint 通过

## Dependencies

#1, #2

## Type

core

## Priority

high

## Labels

core

## Batch

TBD

## References

- SPEC: §4.3, §7.1
- Plan: §8, §9
