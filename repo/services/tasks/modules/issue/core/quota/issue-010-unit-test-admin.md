# 管理单元测试（QuotaAdminService）

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/quota/prd-quota-service.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/quota/spec-quota-service.md`
- Plan: `repo/services/tasks/modules/plan/plan-quota-service.md`

## Description

为 QuotaAdminService adapter 编写单元测试，覆盖 Create/Update/Get/Delete/ListQuotaMeta 场景。

## Scope

- Product line: core
- Code paths allowed: `pkg/adapters/runtime/postgres_quota_admin_test.go`

## Acceptance Criteria

- [ ] 新增 `pkg/adapters/runtime/postgres_quota_admin_test.go`
- [ ] CreateTenantQuota 批量新建成功（含 total 省略取 default_quota）
- [ ] CreateTenantQuota 租户不存在 → `ErrTenantNotFound`
- [ ] CreateTenantQuota 资源未注册/enabled=false → `ErrQuotaResourceNotRegistered`
- [ ] CreateTenantQuota 已存在的维度 → ON CONFLICT DO NOTHING 跳过，其余正常创建
- [ ] CreateTenantQuota items 为空 → 校验错误
- [ ] UpdateTenantQuota 批量改 total 成功
- [ ] UpdateTenantQuota 维度行不存在 → `ErrQuotaNotFound`
- [ ] UpdateTenantQuota 资源未注册 → `ErrQuotaResourceNotRegistered`
- [ ] UpdateTenantQuota total < used（缩容）→ 成功，返回 tightened=true + 收紧后的 total
- [ ] UpdateTenantQuota total >= used+reserved → 成功，返回 tightened=false
- [ ] UpdateTenantQuota items 为空 → 校验错误
- [ ] GetTenantQuota 返回多行 + unit/display_name/is_discrete（JOIN meta）正确解析
- [ ] GetTenantQuota 租户存在但无配额行 → 返回空 items
- [ ] DeleteTenantQuota 删除成功（连同 resource_reservations 流水）
- [ ] DeleteTenantQuota 租户不存在 → `ErrTenantNotFound`
- [ ] DeleteTenantQuota used>0 时仍可删除（不守卫）
- [ ] ListQuotaMeta 返回 enabled=true 的维度列表（含 display_name/unit/default_quota/is_discrete）
- [ ] ListQuotaMeta enabled=false 的维度不返回
- [ ] ListQuotaMeta 空表 → 返回空 items
- [ ] Typecheck/lint 通过

## Dependencies

#5

## Type

core

## Priority

medium

## Labels

core

## Batch

TBD

## References

- SPEC: §9.3
- Plan: §11.3
