# 后端：操作历史 API

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/tenant/prd-new-boss-tenant-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/tenant/ux-boss-tenant-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/tenant/spec-new-boss-tenant-admin.md`

## Description
实现管理员操作历史查询：`GET /api/v1/svc/tenants/{tenantId}/admins/{userId}/audit-logs`（对应 SPEC §4.1 / US-010）。按租户管理员游标分页查询审计日志。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/tenant-service/internal/service/`、`repo/services/tenant-service/internal/repo/adapters/postgres/`

## Acceptance Criteria
- [ ] `GET .../audit-logs` 游标分页，支持 limit（默认 20，最大 100）/ cursor / action / result（success/failure）过滤，响应 CursorPage（items + next_cursor）
- [ ] 查询 `audit_logs` WHERE tenant_id=tenantId AND details->>'target_id'=userId，按 created_at DESC，不调 Core API
- [ ] items 各含 id/action/resource/result/user_id/details/created_at
- [ ] 只读端点，platform-admin/ops/readonly 可访问（对齐 SPEC §6.1 读端点权限）
- [ ] 集成测试：邀请触发后断言审计记录存在（SPEC §9.2/§9.4 US-010 TestHandler_InviteFlow 审计记录断言）

## Dependencies
#2, #3

## Type
backend

## Priority
medium

## References
- SPEC: §5.1.10 ListAuditLogs / §4.2 audit-logs schema / §9
- Plan: 租户管理plan v3.0 §5.4.12 / §3.8 (GET .../admins/{userId}/audit-logs)
