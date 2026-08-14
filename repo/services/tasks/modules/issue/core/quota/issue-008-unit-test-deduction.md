# 扣减单元测试（QuotaService）

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/quota/prd-quota-service.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/quota/spec-quota-service.md`
- Plan: `repo/services/tasks/modules/plan/plan-quota-service.md`

## Description

为 QuotaService 扣减 adapter 编写单元测试，覆盖成功/失败/幂等/原子性场景。参考 `plan_audit_store_test.go` 的 `fakeMetadataTx` 模式。

## Scope

- Product line: core
- Code paths allowed: `pkg/adapters/runtime/postgres_quota_test.go`

## Acceptance Criteria

- [ ] 新增 `pkg/adapters/runtime/postgres_quota_test.go`
- [ ] 定义 `fakeMetadataTx` 实现 `ports.MetadataTx`，模拟 QueryRow/Exec 返回值
- [ ] Try 成功：meta enabled、lazy init、预占成功
- [ ] Try 失败：meta disabled → `ErrQuotaResourceNotRegistered`
- [ ] Try 失败：余量不足 → `ErrQuotaExceeded`
- [ ] TryMany 成功：多维度全占成功
- [ ] TryMany 原子性：第二维度不足 → 第一维度预占回滚（验证 fake tx rollback 调用）
- [ ] Confirm 幂等：state=reserved→confirmed，重复 Confirm→ErrNoRows→跳过
- [ ] Cancel 幂等：state=reserved→cancelled，重复 Cancel→跳过
- [ ] Release 幂等：state=confirmed→released，重复 Release→ErrNoRows→跳过
- [ ] Confirm 后 reserved 减少、used 增加
- [ ] Cancel 后 reserved 减少
- [ ] Release 后 used 减少
- [ ] Release 对非 confirmed 流水（reserved/cancelled）→ ErrNoRows→跳过，不改账本
- [ ] Typecheck/lint 通过

## Dependencies

#3

## Type

core

## Priority

medium

## Labels

core

## Batch

TBD

## References

- SPEC: §9.1
- Plan: §11.1
