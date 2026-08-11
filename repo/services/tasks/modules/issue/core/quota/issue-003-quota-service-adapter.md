# 实现 QuotaService 扣减 adapter（Try/TryMany/Confirm/Cancel/Release）

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/quota/prd-quota-service.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/quota/spec-quota-service.md`
- Plan: `repo/services/tasks/modules/plan/plan-quota-service.md`

## Description

实现 `QuotaService` 的 PG adapter，包含 TCC 预占/实扣状态机，保证并发不超卖和幂等。一个 `PostgresQuota` struct 实现三个 interface，编译期接口断言。adapter 文件路径为 `pkg/adapters/runtime/postgres_quota.go`（SPEC §1.3 决策：放入现有 runtime/ 目录，不建 local 态）。

## Scope

- Product line: core
- Code paths allowed: `pkg/adapters/runtime/postgres_quota.go`

## Acceptance Criteria

- [ ] 新增 `pkg/adapters/runtime/postgres_quota.go`，定义 `PostgresQuota` struct（持有 `ports.MetadataStore`）+ `NewPostgresQuota` 构造函数 + 编译期接口断言（三个 interface）
- [ ] `tryInTx` 内部方法：校验 meta enabled → lazy init（ON CONFLICT DO NOTHING）→ 单行原子 UPDATE（WHERE 余量校验 `reserved + used + $1 <= total`）→ 插入预占流水返回 tx_id
- [ ] `Try`：自开 `WithTenantTx`，单维度预占，返回 `QuotaReservation`
- [ ] `TryMany`：自开 `WithTenantTx`，校验所有 req 的 tenant_id 一致，单事务内循环 `tryInTx`，任一失败则事务回滚无悬挂预占
- [ ] `Confirm`：接收外部 tx，流水 reserved→confirmed（WHERE state='reserved' 守卫幂等）+ reserved→used 转账
- [ ] `Cancel`：接收外部 tx，流水 reserved→cancelled（WHERE state='reserved' 守卫幂等）+ 释放 reserved
- [ ] `Release`：接收外部 tx，流水 confirmed→released（WHERE state='confirmed' 守卫幂等）+ used 减回
- [ ] Confirm/Cancel/Release 对已终态流水（pgx.ErrNoRows）continue 跳过，不重复扣减
- [ ] Typecheck/lint 通过

## Dependencies

#0（RLS 前提验证通过）, #2

## Type

core

## Priority

high

## Labels

core

## Batch

TBD

## References

- SPEC: §5.2 (tryInTx/Try/TryMany/Confirm/Cancel/Release), §5.3, §5.4
- Plan: §4
