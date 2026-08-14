# 网关接入：gRPC client + 路由 + 错误映射

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)

## Description
在 ani-gateway 接入配额套餐 REST → gRPC 转发链路：新增 tenant gRPC client、注册 `/api/v1/svc/tenant-plans*` 与 `/api/v1/svc/tenants/{tenantId}/plan` 全部路由，幂等与错误码映射。当前网关无 tenant gRPC client / 转发链路（tenant_resources.go 为 stub），需新建。

> **实现补充说明：** 原 issue 定稿时为 11 个端点，后续 issue-010/011/012 追加了 3 个端点（PUT 套餐基本信息、GET 配额元数据、GET 可绑定租户），网关注册端点扩展至 14 个。业务码从 8 个扩展至 12 个（追加 `TENANT_NOT_FOUND`/`QUOTA_NOT_FOUND`/`QUOTA_ALREADY_EXISTS`/`GRPC_CLIENT_UNAVAILABLE`）。

## Scope
- Product line: boss
- Code paths allowed: `repo/services/ani-gateway/internal/router/`、`repo/services/ani-gateway/internal/pkg/errors/`
- 说明：tenant gRPC client 在 router 层直接创建并持有，不单独建 client 文件，也不放 middleware（区别于 auth client 的做法）。

## Acceptance Criteria
- [x] 在 `internal/router/tenant_plans.go` 内直接创建并持有 tenant gRPC client（按 `TENANT_SERVICE_ADDR`，缺省 `127.0.0.1:9105` 读取；每方法 context.WithTimeout 超时）
- [x] 新建 `internal/router/tenant_plans.go`：注册全部套餐管理路径（对齐 #1 契约 & SPEC §4.1），经 router 层持有的 gRPC client 转发到 tenant-service：
  - `POST /tenant-plans` → `CreateTenantPlan`
  - `GET /tenant-plans` → `ListTenantPlans`
  - `GET /tenant-plans/{planId}` → `GetTenantPlan`
  - `PUT /tenant-plans/{planId}` → `UpdateTenantPlan`（issue-010 追加）
  - `GET /tenant-plans/{planId}/quota-limits` → `GetTenantPlanQuotaLimits`
  - `POST /tenant-plans/{planId}/activate` → `ActivateTenantPlan`
  - `POST /tenant-plans/{planId}/disable` → `DisableTenantPlan`
  - `DELETE /tenant-plans/{planId}` → `DeleteTenantPlan`
  - `PUT /tenant-plans/{planId}/quota-limits` → `UpdateTenantPlanQuotaLimits`
  - `POST /tenants/{tenantId}/plan` → `BindPlanQuota`
  - `GET /tenant-plans/{planId}/tenants` → `ListTenantPlanBoundTenants`
  - `GET /tenant-plans/{planId}/bindable-tenants` → `ListBindableTenants`（issue-012 追加）
  - `GET /tenant-plans/{planId}/audit-logs` → `ListTenantPlanAuditLogs`
  - `GET /quota-meta` → `ListQuotaMeta`（issue-011 追加）
- [x] 在 `internal/router/router.go` 的 `svc := h.Group("/api/v1/svc")` 下调用注册（对齐现有 registerTenant 模式）
- [x] 错误码映射：gRPC 业务错误 → HTTP 状态 + `APIError{code,message,request_id}`（对齐 SPEC §6.1；复用 `internal/pkg/errors/errors.go` 的 RespondError）：
  - 业务码全集（12 个）：`VALIDATION_FAILED`(400)、`TENANT_PLAN_NOT_FOUND`(404)、`TENANT_NOT_FOUND`(404)、`PLAN_CODE_CONFLICT`(409)、`PLAN_STATE_INVALID`(409)、`TENANT_PLAN_IN_USE`(409)、`TENANT_STATE_INVALID`(409)、`QUOTA_ALREADY_EXISTS`(409)、`PLAN_NOT_ACTIVE`(422)、`QUOTA_RESOURCE_NOT_REGISTERED`(422)、`QUOTA_NOT_FOUND`(404)、`GRPC_CLIENT_UNAVAILABLE`(502)
- [x] gRPC 业务码透传约定：后端（tenant-service）将具体业务码置于 `status.Message()` 前缀（`"<BUSINESS_CODE>: <detail>"`，如 `"PLAN_STATE_INVALID: plan already active"`），网关错误映射先做**业务码最长前缀匹配**精确还原 HTTP 状态 + 业务码；未命中业务码前缀时再按 gRPC code 粗粒度兜底（NotFound→404、InvalidArgument→400、AlreadyExists/FailedPrecondition/Aborted→409、Unavailable/DeadlineExceeded→502/504，其余→500）。由此保证同为 409 的 4 个业务码、同为 422 的 2 个业务码可被正确区分。
- [x] `tenantCallCtx` 注入超时 + request_id + user_id 从 HTTP middleware 到 gRPC metadata
- [x] `planQuotaLimitJSON` DTO 处理 protobuf `Int64Value` ↔ `*int64` 转换（`c.BindJSON` 无法直接解析 protobuf wrapper）
- [x] `pbTimestampFormat` 格式化为 `2006-01-02 15:04:05` Asia/Shanghai 时区
- [x] 幂等：POST/PUT 写操作经现有 `idempotency.go` 中间件（header `Idempotency-Key` 或 body `idempotency_key`，可选）；DELETE、GET 不幂等
- [x] RBAC：读端点（列表/详情/限额查询/绑定租户/操作历史/配额元数据/可绑定租户）允许 platform-readonly；写端点（创建/激活/禁用/删除/改限额/绑定/更新基本信息）不允许（对齐 SPEC §7.1）
- [x] 集成/冒烟：各路由经网关转发可达 tenant-service，错误码能正确映射为 HTTP 状态

## Dependencies
#2, #3

## Type
backend

## Priority
high

## References
- SPEC: §2.3 Module Interactions / §2.4 gateway files / §7.1 AuthZ / §6.3 Failure Modes
- Plan: 租户管理plan v3.0 §6.1（TENANT_SERVICE_ADDR 缺省 127.0.0.1:9105，与 tenant-service `GRPC_PORT` 一致）
