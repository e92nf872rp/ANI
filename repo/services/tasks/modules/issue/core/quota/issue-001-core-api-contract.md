# 新增 Core API 契约（v1.yaml 5 端点 + 9 schema + error responses）

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/quota/prd-quota-service.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/quota/spec-quota-service.md`
- Plan: `repo/services/tasks/modules/plan/plan-quota-service.md`

## Description

契约先行。在 `repo/api/openapi/v1.yaml` 新增 `/admin/tenants/{tenant_id}/quota` 4 个端点 + `/admin/quota-meta` 1 个端点，新增 9 个 schema + 5 个专用 error responses（SPEC 补充，对齐现有 v1.yaml 风格）。不删除/不修改现有端点和 schema。

## Scope

- Product line: core
- Code paths allowed: `repo/api/openapi/v1.yaml` only

## Acceptance Criteria

- [ ] 新增 5 个端点：POST/PUT/GET/DELETE `/admin/tenants/{tenant_id}/quota` + GET `/admin/quota-meta`
- [ ] operationId：createTenantQuota/updateTenantQuota/getTenantQuota/deleteTenantQuota/listQuotaMeta
- [ ] 新增 9 个 schema：QuotaCreateRequest/QuotaCreateItem/QuotaUpdateRequest/QuotaUpdateItem/Quota/QuotaItem/QuotaDeleteResponse/QuotaMetaListResponse/QuotaMeta（字段对齐 SPEC §4.4）
- [ ] 新增 5 个专用 error responses：TenantNotFound/QuotaNotFound/QuotaAlreadyExists/QuotaResourceNotRegistered/QuotaValidationFailed（引用 ErrorResponse schema，SPEC §4.5 补充）
- [ ] POST/PUT/DELETE 支持 `idempotency_key` header
- [ ] QuotaItem 包含 resource_type/total/used/reserved/tightened/unit/display_name/is_discrete
- [ ] 错误码对齐 plan §7.4：TENANT_NOT_FOUND(404)/QUOTA_NOT_FOUND(404)/QUOTA_ALREADY_EXISTS(409)/QUOTA_RESOURCE_NOT_REGISTERED(422)/VALIDATION_FAILED(400)
- [ ] 不删除/不修改现有端点和 schema（兼容性）
- [ ] `python scripts/validate_yaml.py api/openapi/v1.yaml` 通过

## Dependencies

None（可与 #0 并行）

## Type

core

## Priority

high

## Labels

core

## Batch

TBD

## References

- SPEC: §4.1, §4.2, §4.3, §4.4, §4.5
- Plan: §7
