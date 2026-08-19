# QUOTA-POLICY-ISSUE-13：查询套餐操作历史 — service + store 游标分页 + 网关 JSON 映射

> **批次类型：** Feature batch（BOSS 租户配额套餐功能流 Issue #13）
> **完成日期：** 2026-08-11
> **Scope：** `repo/services/tenant-service/internal/service/tenant_plan_service.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_audit_store.go`、`repo/services/tenant-service/internal/repo/ports/tenant_plan_audit_store.go`、`repo/services/ani-gateway/internal/router/tenant_plans.go`、`repo/api/proto/tenant/v1/tenant_plan.proto`、`repo/api/openapi/services/v1.yaml`
> **依赖：** #2 gRPC 接口与 ports、#4 网关接入、#3 audit_logs 分区表
> **Product line：** boss

## 交付内容

实现 `ListTenantPlanAuditLogs`（GET /tenant-plans/{planId}/audit-logs）端到端链路：proto RPC + 网关 handler + service 透传 + store 游标分页查询。复用 issue-003 的 audit_logs 分区表。

### Service 层

- **校验：** plan_id UUID 校验 → 套餐存在（GetByID，404）。
- **查询：** 调 `auditStore.ListPlanAuditLogs`，传 `AuditLogFilter{Limit, Cursor}`。
- **映射：** `auditLogToPB` 把 `ports.AuditLog` 映射为 gRPC `TenantPlanAuditLog`，details `map[string]any` → `structpb.NewStruct`。
- **响应：** `items + total + next_cursor`。

### Store 层

- **查询条件：** `resource = 'tenant_plan' AND details->>'plan_id' = $1`，按 `(created_at DESC, id DESC)` 排序。
- **游标分页：** `LIMIT N+1` 多取 1 条判断 hasNext，`types.DecodeCursor/EncodeCursor` 编码 `(created_at, id)` 复合游标。
- **total 统计：** 仅含 resource + plan_id 条件，不含游标，语义正确。
- **非法 cursor：** `ErrValidationFailed`。

### 网关 handler

- **入参：** plan_id（path）+ limit/cursor（query），action/result 参数未传（见 Deviations）。
- **响应映射：** `auditLogJSON` 手动映射为 snake_case JSON：id/action/result/user_id/request_id/details/created_at。
- **details nil → JSON null**，非 nil → `AsMap()`。
- **next_cursor 空串 → JSON null**（`nullIfEmpty`）。
- **created_at** 用 `pbTimestampFormat` 格式化为「年-月-日 时:分:秒」。

### Proto + OpenAPI

- proto `ListTenantPlanAuditLogs` RPC + `ListTenantPlanAuditLogsRequest{plan_id, page}` + `TenantPlanAuditLog` message。
- OpenAPI `GET /tenant-plans/{planId}/audit-logs` 端点 + `TenantPlanAuditLog` schema + 游标分页参数。

### 测试覆盖

| 测试 | 覆盖 | 结果 |
|---|---|---|
| `TestTenantPlanService_ListTenantPlanAuditLogs` | 正常查询 + 分页 + total + next_cursor | ✅ |
| `TestTenantPlanService_Create_AuditWriteError` | 审计写入失败不阻塞（Create） | ✅ |
| `Test_BindPlanQuota_AuditWriteErrorStillSucceeds` | 审计写入失败不阻塞（BindPlanQuota） | ✅ |
| `Test_UpdateQuotaLimits_CoreFailAudits` | Core 失败审计 | ✅ |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| ListTenantPlanAuditLogs 实现 | service 校验 + 查询 + 映射 | ✅ |
| store 游标分页 | (created_at, id) DESC + LIMIT N+1 + EncodeCursor | ✅ |
| total 语义 | 仅含 resource + plan_id 不含游标 | ✅ |
| 套餐不存在 → 404 | GetByID 校验 | ✅ |
| details JSON 映射 | structpb.NewStruct → auditLogJSON AsMap | ✅ |
| next_cursor null 处理 | nullIfEmpty | ✅ |
| proto RPC 定义 | ListTenantPlanAuditLogs + Request/Response | ✅ |
| OpenAPI 端点 | GET /tenant-plans/{planId}/audit-logs + schema | ✅ |
| 编译 | `go build` → EXIT=0 | ✅ |
| 测试 | `go test -run Audit` → 4/4 PASS | ✅ |
| review-it | 1 个低严重程度 finding（action/result 过滤未实现），设计决策 | ✅ |

## 验证命令

```bash
cd repo
go build ./services/ani-gateway/... ./services/tenant-service/...
go test ./services/tenant-service/internal/... -run "Audit" -v
```

## 边界声明

- action/result 过滤未实现（proto/ports/store/网关/OpenAPI 均未含此字段），后续版本添加。
- 审计写入失败不阻塞业务（Create/BindPlanQuota 均已修正为 log warning）。
- `ListTenantPlanAuditLogs` 端点参数仅 planId + limit + cursor，无 action/result query 参数。
