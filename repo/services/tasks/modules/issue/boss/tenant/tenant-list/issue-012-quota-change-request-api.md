# Issue 12: 配额变更申请三件套 — 提交 / 列表 / 审批

> **Status: DONE（以实现为准）**  
> 证据：`repo/development-records/tenant-list-issue-012-quota-change-request-api.md`

## Document Links
- PRD: [prd-new-boss-tenant-list.md](../../../../prd/boss/tenant/prd-new-boss-tenant-list.md)
- UX: [ux-boss-tenant-list.md](../../../../ux/boss/tenant/ux-boss-tenant-list.md)
- SPEC: [spec-new-boss-tenant-list.md](../../../../spec/boss/tenant/spec-new-boss-tenant-list.md)

## Description

US-012～014：落 service 自有表 `tenant_quota_change`；不新增 Core 端点。复合主键 `(tenant_id, request_id, resource_type)`。跨请求同维 pending **允许**；同 request 同维禁止。

## Scope
- Product line: boss
- Code paths:
  - `repo/services/tenant-service/internal/service/tenant_service.go`
  - `repo/services/tenant-service/internal/repo/adapters/postgres/tenant_store.go`
  - 迁移：`repo/deploy/migrations/20260811000300_tenant_quota_change.sql`

## Acceptance Criteria

### SubmitQuotaChangeRequest
- [x] items≥1；resource_type 格式；new_value≥0；批内维不重复 → 422 `QUOTA_CHANGE_REQUEST_INVALID`
- [x] `request_id` 来自网关 `x-request-id`；兼容 **`req_<uuid>`** 前缀；缺失/非法 → 400；**service 不 uuid.New()**
- [x] **`x-user-id` 必填** 作为 `requested_by`，缺失 → 400
- [x] 顺序：先 `ListQuotaMeta`（未注册/未启用 → **422 `QUOTA_RESOURCE_NOT_REGISTERED`**）再 `GetQuota` 冻结 `old_value`
- [x] `old_value`：API/proto 为 **int64**；无行时表现为 **0**（非 SQL NULL 透出）
- [x] 仅 INSERT；同 `(tenant_id,request_id,resource_type)` 冲突 → **409 `QUOTA_CHANGE_REQUEST_CONFLICT`**
- [x] 跨请求同维 pending 不阻断；不 UPDATE 他人 pending
- [x] 审计 `tenant.quota_change_request.submit`

### ListQuotaChangeRequests
- [x] 可选 status 过滤；created_at DESC；**不分页**

### ReviewQuotaChangeRequest
- [x] 整批按 request_id：无 → 404 `QUOTA_CHANGE_REQUEST_NOT_FOUND`；非 pending → 409 `QUOTA_CHANGE_REQUEST_NOT_PENDING`
- [x] approved：先 SetStatus 后逐维 UpsertQuota；Core 失败不回滚审批态，审计 failure
- [x] rejected：不触 Core 配额；审计 approve/reject

### 并发
- [x] 不建「全局 pending 唯一」索引；同请求同维靠批内去重 + PK

### 测试
- [x] 单测覆盖 request_id / meta / 冲突 / 整批审批

## Implementation notes
- Store 方法名：`InsertPendingQuotaChanges` / `SetQuotaChangeStatusByRequestID` 等（以实现为准）。
- proto 若仍写 “overwrites pending”，以本 Issue **跨请求允许、不 UPDATE** 为准。

## Dependencies
Issue 4、11；`QuotaSvcClient.ListQuotaMeta` / `GetQuota` / `UpsertQuota`

## Type
backend

## Priority
high

## References
- SPEC: §5.1 / §5.2 / §5.4（以本文 AC 为准）
- Record: `repo/development-records/tenant-list-issue-012-quota-change-request-api.md`
