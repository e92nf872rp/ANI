# 新增 Port 契约定义（三个 interface + 哨兵错误 + 类型）

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/quota/prd-quota-service.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/quota/spec-quota-service.md`
- Plan: `repo/services/tasks/modules/plan/plan-quota-service.md`

## Description

定义三个 port interface 和相关类型/哨兵错误，作为 adapter 和 handler 的契约边界。三个 port 分离：QuotaService（扣减）、QuotaStoreService（配置查询）、QuotaAdminService（租户生命周期管理）——操作同一组表但调用方、事务模型、UPSERT 语义不同。

## Scope

- Product line: core
- Code paths allowed: `pkg/ports/` only

## Acceptance Criteria

- [ ] 新增 `pkg/ports/quota.go`，定义 `QuotaService` interface（Try/TryMany/Confirm/Cancel/Release）+ `QuotaStoreService` interface（Put/List/GetMy/GetTotalForUpdateTx）+ `ResourceType` 常量（8 维度：gpu_count/cpu_core/memory_gb/storage_gb/token_count/kb_query_count/member_count/inference_service_count）+ 类型（QuotaTryRequest/QuotaReservation/QuotaView/QuotaPutRequest/QuotaListRequest/QuotaListResult）
- [ ] 新增 `pkg/ports/quota_admin.go`，定义 `QuotaAdminService` interface（CreateTenantQuota/UpdateTenantQuota/GetTenantQuota/DeleteTenantQuota/ListQuotaMeta）+ 类型（QuotaItemInput/QuotaItemUpdate/QuotaMeta/QuotaInfo）
- [ ] 修改 `pkg/ports/errors.go`，追加哨兵错误：`ErrQuotaExceeded`/`ErrQuotaResourceNotRegistered`/`ErrQuotaIdempotencyConflict`/`ErrQuotaNotFound`/`ErrQuotaAlreadyExists`（`ErrTenantNotFound` 复用已有定义）
- [ ] `QuotaService.Confirm/Cancel/Release` 接收外部 `MetadataTx`，不在 port 内自开事务
- [ ] `QuotaStoreService.GetTotalForUpdateTx` 接收外部 `MetadataTx`，返回 int64 total
- [ ] Typecheck/lint 通过

## Dependencies

#1

## Type

core

## Priority

high

## Labels

core

## Batch

TBD

## References

- SPEC: §3.2, §5.1
- Plan: §3
