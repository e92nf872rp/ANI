# Issue 2: 接口与结构体声明 — gRPC 接口 + ports/store + Go struct 定义（仅声明，无实现）

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)

## Description

创建配额套餐的接口与数据模型契约：

- **传输层**：`api/proto/tenant/v1/tenant_plan.proto` 定义 gRPC 接口（`TenantPlanService` 13 RPC + `TenantService` 1 RPC）与 message 数据模型，用 buf 生成 `pkg/generated/pb/tenant/v1/`。
- **存储层（双模型）**：`tenant-service/internal/repo/ports/` 定义 store 接口与 domain struct（DB 实体/store 出入参），与 gRPC message（`tenantv1.*`）并存，service 层负责两者映射。
- **审计层**：`tenant-service/internal/repo/ports/tenant_plan_audit_store.go` 定义 `TenantPlanAuditStore` 接口（Create + ListPlanAuditLogs）与 `AuditLog`/`AuditLogFilter`/`AuditLogListResult` 结构体，复用 audit_logs 分区表。
- **service 层**：`tenant-service/internal/service/tenant_plan_service.go` 为 gRPC server 实现（嵌入 `UnimplementedTenantPlanServiceServer` + `Register(server)`，方法体占位）。

> **实现补充说明：** 原 issue 定稿时 `TenantPlanService` 含 10 个 RPC，后续 issue-010/011/012 追加 `UpdateTenantPlan`/`ListQuotaMeta`/`ListBindableTenants` 共 3 个 RPC，总计 13 个。`GetQuotaLimitViews` 从 store 方法改为 service 层 `buildQuotaLimitViews` 函数（组装 store 原始行 + Core meta）。

## Scope
- Product line: boss
- Code paths allowed: `api/proto/tenant/` + `pkg/generated/pb/tenant/` + `services/tenant-service/internal/repo/` + `services/tenant-service/`

## Acceptance Criteria
- [x] 创建 `api/proto/tenant/v1/tenant_plan.proto`：定义 `TenantPlanService`（ListTenantPlans/CreateTenantPlan/GetTenantPlan/UpdateTenantPlan/DeleteTenantPlan/GetTenantPlanQuotaLimits/UpdateTenantPlanQuotaLimits/ActivateTenantPlan/DisableTenantPlan/ListTenantPlanBoundTenants/ListBindableTenants/ListTenantPlanAuditLogs/ListQuotaMeta）+ `TenantService`（BindPlanQuota），复用 `common/v1` 分页；message 覆盖 TenantPlan(TenantCount)/PlanQuotaLimitInput(Int64Value total)/PlanQuotaLimitView/BoundTenant/AuditLog/QuotaMetaItem
- [x] 用 buf generate 生成 `pkg/generated/pb/tenant/v1/*.go`（项目固定插件版本：protoc-gen-go v1.33.0 / protoc-gen-go-grpc v1.3.0，不污染其他模块 pb）
- [x] 创建 `services/tenant-service/internal/repo/ports/tenant_plan_store.go`：定义 `TenantPlanStore` interface（Create/GetByID/List/Update/Activate/Disable/Delete/GetQuotaLimits/UpdateQuotaLimits/ListBoundTenants/ListBindableTenants/GetApprovedQuotaChanges）。展示与绑定下发用 service 层 `buildQuotaLimitViews` 函数组装（store.GetQuotaLimits 原始行 + Core ListQuotaMeta，COALESCE total/default_quota 兜底）；`GetQuotaLimits` 返回原始行（含 NULL）供 service 层组装；`UpdateQuotaLimits` 供修改限额写入 plan_quota_limits
- [x] 创建 `services/tenant-service/internal/repo/ports/tenant_plan_audit_store.go`：定义 `TenantPlanAuditStore` interface（`Create` 写入 + `ListPlanAuditLogs` 按套餐查询）。审计按业务域拆分——其余业务域（租户列表/租户管理员/平台运营账户）各自独立 audit store，留待对应 PR——若已存在则复用
- [x] 创建 `services/tenant-service/internal/repo/ports/tenant_store.go`：定义最小化 `TenantStore` interface（GetByID 查租户状态、UpdatePlan 换 plan_id）——供 issue-009 绑定套餐同步 tenants.plan_id 及判 disabled；不含 SSO/MFA（TenantAuthStore）与 UpdateQuotas（配额走 Core QuotaSvcClient）
- [x] 定义所有 domain struct：TenantPlan、TenantPlanListItem（含 TenantCount）、PlanQuotaLimit、PlanQuotaLimitView、CreateTenantPlanInput、UpdateTenantPlanInput、PlanQuotaLimitInput、TenantPlanListFilter（Limit/Cursor/Status/Search）、TenantPlanListResult（Items/Total/NextCursor，具体类型，不用泛型）、AuditLog、AuditLogFilter（Limit/Cursor）、AuditLogListResult、Tenant、BoundTenant、ApprovedQuotaChange
- [x] 定义 `QuotaSvcClient` interface 签名（`ports/core_quota.go`）：
  - `ListQuotaMeta(ctx) ([]QuotaMeta, error)` — 调 Core `GET /admin/quota-meta`，校验维度 enabled（approved 维度跳过依据）
  - `GetQuota(ctx, tenantID) ([]CoreQuotaResult, error)` — 调 Core `GET /admin/tenants/{tenant_id}/quota`，绑定套餐前判断配额行是否存在（不存在→POST 新建，已存在→PUT 修改）；返回 `[]CoreQuotaResult`（含 Total/Used/Reserved/Tightened）
  - `PutQuota(ctx, tenantID, items) ([]CoreQuotaResult, error)` — 调 Core `PUT /admin/tenants/{tenant_id}/quota`，改限额/换套餐时同步存量租户（自动收紧）
  - `CreateQuota(ctx, tenantID, items) ([]CoreQuotaResult, error)` — 调 Core `POST /admin/tenants/{tenant_id}/quota`，绑定套餐时新建配额行（used/reserved 初始 0）
  - `DeleteQuota(ctx, tenantID) error` — 调 Core `DELETE /admin/tenants/{tenant_id}/quota`，租户禁用时清理配额行
  - 入参 `CoreQuotaItem{ ResourceType, Total }`、`CoreQuotaResult{ ResourceType, Total, Used, Reserved, Tightened }`、`QuotaMeta{ ResourceType, Enabled, DefaultQuota, DisplayName, Unit, IsDiscrete }` 与 Core schema 对齐
- [x] 创建 `repo/services/tenant-service/internal/service/tenant_plan_service.go`：`TenantPlanService` gRPC server 结构（嵌入 `tenantv1.UnimplementedTenantPlanServiceServer` + `Register(server *grpc.Server)`）+ 全部 RPC 方法签名（占位实现；`TenantService` 的 BindPlanQuota 在 `tenant_service.go` 实现）
- [x] 创建 `repo/services/tenant-service/go.mod`（module 声明 + grpc/protobuf 依赖 + replace pkg）
- [x] 创建 `services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go`：`PostgresTenantPlanStore` struct 实现 TenantPlanStore interface
- [x] 创建 `services/tenant-service/internal/repo/adapters/postgres/tenant_store.go`：`PostgresTenantStore` struct 实现 TenantStore interface
- [x] 创建 `services/tenant-service/internal/repo/adapters/postgres/tenant_plan_audit_store.go`：`PostgresTenantPlanAuditStore` struct 实现 TenantPlanAuditStore 接口
- [x] 创建 `services/tenant-service/internal/repo/adapters/core/quota_svc_client.go`：QuotaSvcClient struct（HTTP 客户端实现）
- [x] `go build ./...`（pkg / tenant-service 两模块）编译通过

## Dependencies
#1 — OpenAPI 契约（生成类型供参考，但 Go struct 手写不依赖代码生成）

## Type
backend

## Priority
high

## References
- SPEC: §2.2 Component Design, §2.4 File Structure, §3.2 Entity Definitions
