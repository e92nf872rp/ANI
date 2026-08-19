# 后端：操作历史 API（配 TenantPlanAuditStore 查询）

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)

## Description
实现套餐操作历史查询 `GET /api/v1/svc/tenant-plans/{planId}/audit-logs`（游标分页）。填充 `TenantPlanService.ListTenantPlanAuditLogs` RPC 与 `TenantPlanAuditStore.ListPlanAuditLogs` 查询。覆盖 US-011。（审计写入 Create 已在各写操作 issue 实现。）

> **实现补充说明：** 原 issue 规格含 action/result 服务端过滤，实际实现中 `AuditLogFilter` 仅含 `Limit`/`Cursor`，未实现 action/result 服务端过滤（设计决策：审计日志量小，前端本地过滤即可）。前端 AuditLogsTab 仅做本地 `resultFilter` 过滤。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/tenant_plan_service.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_audit_store.go`

## Acceptance Criteria
- [x] `GET /tenant-plans/{planId}/audit-logs` 游标分页（limit/cursor + next_cursor）
- [x] 实现 `TenantPlanAuditStore.ListPlanAuditLogs`：查询 audit_logs WHERE resource='tenant_plan' AND details->>'plan_id'=$planId，按 created_at DESC 排序，游标分页返回 `AuditLogListResult`（Items/Total/NextCursor）
- [x] ~~action/result 服务端过滤~~ — **未实现（设计决策）**：审计日志量小，前端本地过滤即可；`AuditLogFilter` 仅含 `Limit`/`Cursor`
- [x] 返回模型仅 5 个字段（id/action/result/details/created_at）；tenant_id/user_id/request_id/resource/ip_address/user_agent 虽在 DB 表中存储，但不在 API 响应中暴露（gRPC AuditLog message 与网关 auditLogJSON 均仅返回这 5 个字段）
- [x] `TenantPlanService.ListTenantPlanAuditLogs` RPC 方法体实现，返回 `tenantv1.ListTenantPlanAuditLogsResponse`；details 字段经 `structpb.NewStruct` 转换
- [x] 套餐不存在 → 404 `TENANT_PLAN_NOT_FOUND`
- [x] 网关 GET 转发与错误码映射在 #4 网关 issue 完成
- [x] `go build ./...` 编译通过

## Dependencies
#2、#3、#7（Write 侧已实现审计写入）、#9

## Type
backend

## Priority
high

## References
- SPEC: §4.2 audit-logs schema / §5.1 审计 / §9.2 TestHandler_AuditLogs
- Plan: 租户管理plan v3.0 §5.3
