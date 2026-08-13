# 重新生成 Core SDK

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/quota/prd-quota-service.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/quota/spec-quota-service.md`
- Plan: `repo/services/tasks/modules/plan/plan-quota-service.md`

## Description

在契约改完后重新生成 Core SDK，确保新增 quotas operation 不漂移。`sdks/core/go/anisdk/client.go` 是 DO NOT EDIT 文件，改契约后必须 `make gen-core-sdk`，否则 `validate-sdk-beta` 报漂移。

## Scope

- Product line: core
- Code paths allowed: `sdks/core/go/anisdk/`（自动生成，DO NOT EDIT）

## Acceptance Criteria

- [ ] 执行 `make gen-core-sdk` 重新生成 `sdks/core/go/anisdk/client.go`
- [ ] 生成后 `Operations` 切片包含 createTenantQuota/updateTenantQuota/getTenantQuota/deleteTenantQuota/listQuotaMeta
- [ ] `make validate-sdk-beta` 通过（SDK 无漂移）
- [ ] `git diff --check -- sdks/core` 无空白错误

## Dependencies

#1

## Type

core

## Priority

medium

## Labels

core

## Batch

TBD

## References

- SPEC: §4.1
- Plan: §10.1
