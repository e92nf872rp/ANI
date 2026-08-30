# QUOTA-POLICY-ISSUE-04：网关接入 — tenant-plans 路由转发 + 错误映射 + request_id 透传

> **批次类型：** Feature batch（BOSS 租户配额套餐功能流 Issue #4）
> **完成日期：** 2026-08-11
> **Scope：** `repo/services/ani-gateway/internal/router/tenant_plans.go`、`repo/services/ani-gateway/internal/router/router.go`、`repo/deploy/migrations/20260810000300_alter_table_structures.sql`
> **依赖：** #1 OpenAPI 契约、#2 gRPC 接口与 ports
> **Product line：** boss

## 交付内容

在 ani-gateway router 层实现配额套餐 11 个 REST 端点到 tenant-service gRPC 的转发，含 gRPC client 管理、错误映射、request_id 透传、audit_logs 分区表修复。

### 网关路由（11 个端点）

| 路径 | Method | gRPC RPC | 说明 |
|---|---|---|---|
| `/tenant-plans` | POST | `CreateTenantPlan` | 创建套餐，body 含 quota_limits + idempotency_key |
| `/tenant-plans` | GET | `ListTenantPlans` | 套餐列表，status/search 过滤 + 游标分页 |
| `/tenant-plans/:plan_id` | GET | `GetTenantPlan` | 套餐详情 |
| `/tenant-plans/:plan_id` | DELETE | `DeleteTenantPlan` | 软删除套餐 |
| `/tenant-plans/:plan_id/quota-limits` | GET | `GetTenantPlanQuotaLimits` | 套餐限额视图 |
| `/tenant-plans/:plan_id/quota-limits` | PUT | `UpdateTenantPlanQuotaLimits` | 修改限额 + idempotency_key |
| `/tenant-plans/:plan_id/activate` | POST | `ActivateTenantPlan` | 激活套餐 |
| `/tenant-plans/:plan_id/disable` | POST | `DisableTenantPlan` | 停用套餐 |
| `/tenant-plans/:plan_id/tenants` | GET | `ListTenantPlanBoundTenants` | 已绑租户列表 |
| `/tenant-plans/:plan_id/audit-logs` | GET | `ListTenantPlanAuditLogs` | 操作历史，action/result 过滤 + 游标分页 |
| `/tenants/:tenant_id/plan` | POST | `BindPlanQuota` | 绑定套餐 + idempotency_key |

### gRPC client 管理

- `newTenantPlansAPI()` 由 `TENANT_SERVICE_ADDR`（缺省 `127.0.0.1:9105`）创建共享 gRPC conn，派生 `TenantPlanServiceClient` + `TenantServiceClient`。
- conn 建立失败时字段为 nil，由各 handler 做 nil 守卫兜底返回 502；消息区分 plan/tenant client。

### 错误映射（两阶段）

- **业务码前缀匹配：** `mapTenantPlanError` 先用 `status.Message()` 做 8 个业务码前缀最长匹配，精确还原 HTTP 状态与业务码。
- **gRPC code 兜底：** 未命中时按 `codes.NotFound/InvalidArgument/AlreadyExists/FailedPrecondition/Aborted/DeadlineExceeded/Unavailable/default` 粗粒度映射。
- 业务码 → HTTP 映射：`VALIDATION_FAILED(400)`、`TENANT_PLAN_NOT_FOUND(404)`、`PLAN_CODE_CONFLICT(409)`、`PLAN_STATE_INVALID(409)`、`TENANT_PLAN_IN_USE(409)`、`TENANT_STATE_INVALID(409)`、`PLAN_NOT_ACTIVE(422)`、`QUOTA_RESOURCE_NOT_REGISTERED(422)`。

### request_id 透传

- `tenantCallCtx(ctx, c)` 辅助函数：`context.WithTimeout(5s)` + `metadata.AppendToOutgoingContext("x-request-id", ...)`。
- 所有 11 个 handler 统一调用，把网关 request_id 注入 gRPC metadata，供 tenant-service 审计日志关联前端请求。

### protobuf Int64Value 类型转换

- `planQuotaLimitJSON{ ResourceType string, Total *int64 }` DTO 用标准 JSON 解析。
- `toPlanQuotaLimitInputs` 转为 `[]*tenantv1.PlanQuotaLimitInput`（`wrapperspb.Int64Value`），避免 `c.BindJSON` 无法解析 protobuf wrapper。

### audit_logs 分区表修复（迁移 003）

- 修复 `audit_logs` 分区表 PK：`(id)` → `(id, created_at)`，分区表唯一约束须含分区键。
- 补建 `audit_logs_2026_07` 和 `audit_logs_2026_08` 月分区（init_schema 只建了 2026_05/2026_06）。
- 移除 `tenants` 表废弃配额列（`max_gpu_count/max_cpu_cores/max_memory_gb`）。

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| 11 端点路由注册 | `registerTenantPlans(svc)` 全部注册 | ✅ |
| gRPC client 放 router 层 | `newTenantPlansAPI()` 在 router 创建 | ✅ |
| 错误映射 8 业务码 | `businessCodeByHTTP` + 前缀匹配 | ✅ |
| gRPC code 兜底 6 分支 | `mapTenantPlanError` switch | ✅ |
| request_id 透传 | `tenantCallCtx` 注入 metadata | ✅ |
| nil 守卫区分 plan/tenant | 消息分别 "plan/tenant grpc client unavailable" | ✅ |
| Int64Value 类型转换 | `planQuotaLimitJSON` DTO + `toPlanQuotaLimitInputs` | ✅ |
| audit_logs PK 修复 | 迁移 003 ALTER PRIMARY KEY | ✅ |
| audit_logs 月分区补建 | 2026_07 + 2026_08 分区 | ✅ |
| 编译 | `go build ./services/ani-gateway/...` → EXIT=0 | ✅ |
| review-it | clean，无 actionable findings | ✅ |

## 验证命令

```bash
cd repo
go build ./services/ani-gateway/...
```

## 边界声明

- 本 Issue 仅实现网关转发层，不涉及 tenant-service 业务逻辑实现（属 Issue #5-#10）。
- user_id 未透传到 gRPC metadata（见 Open Questions），审计日志 user_id 列暂为 NULL。
- 网关错误映射兜底分支保留后端 `status.Message()` 作为 message，不硬编码。

## Open Questions

1. **user_id 透传：** `tenantCallCtx` 只透传 `x-request-id`，未透传 `x-user-id`。审计日志 `user_id` 列为 NULL。待确认是否需要从 Auth middleware 取 user_id 注入 gRPC metadata。
2. **QuotaMeta Enabled 字段：** ports 定义含 `Enabled bool`，但 Core API 不返回此字段，客户端统一标记 `Enabled=true`。待确认 Core 是否应返回 enabled 或 ports 移除该字段。
