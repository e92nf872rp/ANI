# 实现 QuotaStoreService 配置查询 adapter（Put/List/GetMy/GetTotalForUpdateTx）

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/quota/prd-quota-service.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/quota/spec-quota-service.md`
- Plan: `repo/services/tasks/modules/plan/plan-quota-service.md`

## Description

实现 `QuotaStoreService` 的 PG adapter，支持 BOSS 运营配额管理、Console 自查和 GPU 预留校验锁行查询。与 #3 共享同一个 `PostgresQuota` struct，在同一文件 `pkg/adapters/runtime/postgres_quota.go` 中追加方法。

## Scope

- Product line: core
- Code paths allowed: `pkg/adapters/runtime/postgres_quota.go`

## Acceptance Criteria

- [ ] `Put`：自开 `WithPlatformTx`，UPSERT 覆盖 total（不 clamp，撞 CHECK 报错透传）；校验 meta enabled；回读所有维度返回 `QuotaView`
- [ ] `List`：自开 `WithPlatformTx`，无 tenant_id 时按租户级 keyset 分页（cursor=tenant_id），有 tenant_id 时直接调 GetMy 不分页；分页 limit 默认 50、上限 100；多查 1 条判断 hasMore
- [ ] `GetMy`：自开 `WithTenantTx`，RLS 自动过滤只看本租户，返回 `QuotaView`
- [ ] `GetTotalForUpdateTx`：接收外部 tx，`SELECT total ... FOR UPDATE` 锁行，行不存在返回 `ErrQuotaNotFound`
- [ ] `Put` 不做 GREATEST clamp，total < used+reserved 撞 CHECK 约束时透传错误
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

- SPEC: §5.2 (Put/List/GetMy/GetTotalForUpdateTx), §5.4
- Plan: §5
