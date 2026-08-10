# 配置查询单元测试（QuotaStoreService）

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/quota/prd-quota-service.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/quota/spec-quota-service.md`
- Plan: `repo/services/tasks/modules/plan/plan-quota-service.md`

## Description

为 QuotaStoreService adapter 编写单元测试，覆盖 Put/List/GetMy/GetTotalForUpdateTx 场景。

## Scope

- Product line: core
- Code paths allowed: `pkg/adapters/runtime/postgres_quota_store_test.go`

## Acceptance Criteria

- [ ] 新增 `pkg/adapters/runtime/postgres_quota_store_test.go`
- [ ] Put 新增（行不存在）→ UPSERT 建行成功
- [ ] Put 修改（行存在）→ UPSERT 覆盖 total 成功
- [ ] Put 资源未注册/enabled=false → `ErrQuotaResourceNotRegistered`
- [ ] Put total < used+reserved → 撞 CHECK 约束报错（不 clamp，透传错误）
- [ ] Put 多维度同时 PUT → 全部成功
- [ ] List 无过滤 → 按租户级分页返回，每页含完整多维度 QuotaView
- [ ] List tenant_id 过滤 → 直接返回指定租户全部维度（不分页）
- [ ] List 分页 cursor 衔接：第一页 NextCursor=末尾 tenant_id，第二页正确衔接不漏不重
- [ ] List 空表 → 返回空 items、空 cursor
- [ ] List 超过 limit 的一页 → hasMore=true，NextCursor 指向本页最后一个租户
- [ ] GetMy 返回当前租户多维度 map
- [ ] GetTotalForUpdateTx 行存在 → 返回 total
- [ ] GetTotalForUpdateTx 行不存在 → `ErrQuotaNotFound`
- [ ] Typecheck/lint 通过

## Dependencies

#4

## Type

core

## Priority

medium

## Labels

core

## Batch

TBD

## References

- SPEC: §9.2
- Plan: §11.2
