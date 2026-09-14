# 后端：操作历史查询 API

## Document Links
- PRD: `repo/services/tasks/modules/prd/boss/settings/prd-new-boss-platform-admin.md`
- UX: `repo/services/tasks/modules/ux/boss/settings/ux-new-boss-platform-admin.md`
- SPEC: `repo/services/tasks/modules/spec/boss/settings/spec-new-boss-platform-admin.md`

## Description
实现平台账号操作历史查询的 Services 层业务逻辑与网关转发：`GET /api/v1/svc/platform-admins/{userId}/audit-logs`（对应 SPEC §4.2 / US-008）。platform-settings-service 直查 `audit_logs` 表（`user_id=$userId AND tenant_id IS NULL`），不调 Core API，不写审计。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/platform-settings-service/internal/service/`、`repo/services/platform-settings-service/internal/repo/adapters/postgres/`、`repo/services/ani-gateway/internal/router/`

## Acceptance Criteria
- [ ] `PlatformAdminService.ListAuditLogs` RPC 实现替换 501 占位：调用 `PlatformAdminStore.ListAuditLogs(userId, filter)`
- [ ] `platform_admin_store.go` 实现 `PostgresPlatformAdminStore.ListAuditLogs`（替换 #2 骨架）：`SELECT * FROM audit_logs WHERE user_id=$1 AND tenant_id IS NULL`，可选 AND action=$action AND result=$result，ORDER BY created_at DESC, id DESC，游标分页
- [ ] 支持 query 参数：limit（default 20, max 100）/ cursor / action（可选）/ result（success|failure，可选）
- [ ] 响应 CursorPage：items[]（每项含 id/action/resource/result/details/created_at）+ next_cursor
- [ ] action 值覆盖 `platform_admin.create / change_role / reset_password / disable / enable / delete`
- [ ] 无历史记录返回 items=[], next_cursor=""
- [ ] `platform_admin_resources.go` 实现 `GET /svc/platform-admins/{userId}/audit-logs` 网关转发（gRPC → platform-settings-service）
- [ ] **不调 Core API，不写审计日志**
- [ ] AuthZ: platform-admin/ops/readonly 均可访问
- [ ] 单元测试：audit_logs 查询 + 过滤 + 分页（SPEC §9.2 TestPlatformAdminService_ListAuditLogs）
- [ ] 集成测试 `TestHandler_AuditLogsFlow`：各写操作后 GET audit-logs 验证记录（SPEC §9.3）
- [ ] `make test` 通过

## Dependencies
#2 (接口+SDK), #5, #8, #9, #10 (审计写入由各写操作 Issue 完成，本 Issue 实现查询)

## Type
backend

## Priority
medium

## References
- SPEC: §5.2.7 ListAuditLogs / §4.2 audit-logs schema / §9.2-9.3
- UX: §3.1 详情-操作记录 Tab / §4.3 操作记录 Tab
