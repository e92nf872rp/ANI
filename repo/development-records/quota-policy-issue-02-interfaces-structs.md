# QUOTA-POLICY-ISSUE-02：gRPC 接口 + ports/store + Go struct 声明（仅声明，无实现）

> **批次类型：** Feature batch（BOSS 租户配额套餐功能流 Issue #2）
> **完成日期：** 2026-08-10
> **Scope：** `api/proto/tenant/`、`pkg/generated/pb/tenant/`、`services/tenant-service/internal/repo/`、`services/tenant-service/`
> **依赖：** #1 — OpenAPI 契约
> **Product line：** boss

## 交付内容

创建配额套餐的接口与数据模型契约，**只写声明/接口/数据模型，不写实现逻辑**（方法体以 `panic("not implemented")` 占位）。目的是建立编译通过的类型契约，供后续 Issue 填充实现。核心决策是把 tenant-service 从「HTTP handler」改为「gRPC server」并下沉 ports/adapters 到其自有 `internal/repo`。

### 传输层（gRPC）

- `api/proto/tenant/v1/tenant_plan.proto`：`TenantPlanService` 10 RPC + `TenantService.BindPlanQuota`；复用 `common/v1` 分页；message 覆盖 TenantPlan(TenantCount)/PlanQuotaLimitInput(Int64Value total)/PlanQuotaLimitView/BoundTenant/AuditLog。
- `buf generate` 生成 `pkg/generated/pb/tenant/v1/*.go`（固定插件：protoc-gen-go v1.33.0 / protoc-gen-go-grpc v1.3.0 / grpc-gateway v2.19.1，不污染其它模块 pb）。

### 存储层（双模型，下沉 tenant-service 自有 `internal/repo`）

- `internal/repo/ports/tenant_plan_store.go`：`TenantPlanStore` interface（Create/GetByID/GetByCode/List/Update/Activate/Disable/Delete/GetQuotaLimits/GetQuotaLimitViews/UpdateQuotaLimits/ListBoundTenants/GetApprovedQuotaChanges）。
- `internal/repo/ports/tenant_plan_audit_store.go`：`TenantPlanAuditStore` interface（配额套餐域审计：Create + ListPlanAuditLogs）。审计按业务域拆分，其余三域留待对应 PR。
- `internal/repo/ports/tenant_store.go`：最小化 `TenantStore` interface（GetByID 判状态 / UpdatePlan 换 plan_id）。
- `internal/repo/adapters/postgres/{tenant_plan_store,tenant_store,tenant_plan_audit_store}.go`：`Postgres*` 占位实现。
- `internal/repo/adapters/core/quota_svc_client.go`：`QuotaSvcClient` 占位实现（PutQuota）。
- 所有 domain struct：TenantPlan、TenantPlanListItem、PlanQuotaLimit、PlanQuotaLimitView、CreateTenantPlanInput、PlanQuotaLimitInput、TenantPlanListFilter、TenantPlanListResult、AuditLogListResult、Tenant、BoundTenant、ApprovedQuotaChange。

### Service 层（gRPC server）

- `internal/service/tenant_plan_service.go`：`TenantPlanService` gRPC server（嵌入 `UnimplementedTenantPlanServiceServer` + `Register(server)`），全部 RPC 方法占位。
- `internal/service/tenant_service.go`：`TenantService` gRPC server（BindPlanQuota）。
- `go.mod`：module + grpc/protobuf + replace pkg。

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| proto 定义 | TenantPlanService + TenantService | ✅ |
| buf 生成 pb | `pkg/generated/pb/tenant/v1/*.go` | ✅ |
| 3 个 store interface | tenant_plan_store / tenant_plan_audit_store / tenant_store | ✅ |
| domain structs | 全部定义 | ✅ |
| QuotaSvcClient | PutQuota 签名 | ✅ |
| 2 个 gRPC server | tenant_plan_service / tenant_service | ✅ |
| go.mod | 模块 + replace | ✅ |
| 编译 | `go build ./...`（pkg / tenant-service 两模块）→ ✅ EXIT=0 | ✅ |
| 边界校验 | `python scripts/validate_services_boundary.py` → passed | ✅ |
| review-it | 分页 total、平铺、gRPC 化、issue 同步 → 无遗留可行动项 | ✅ |

## 验证命令

```bash
cd repo
go build ./...          # repo/pkg + repo/services/tenant-service
go vet ./...
python scripts/validate_services_boundary.py --root .
buf generate --template api/proto/buf.gen.yaml .
```

## 边界声明

- 本 Issue 仅声明接口与数据模型，方法体为占位（panic），不含业务逻辑。业务实现属后续 Issue（#5-#10）。
- gRPC 契约与 `ports` 领域模型为双模型并存，service 层负责映射。
- 未完成：`cmd/main.go` + config 装配（启动入口）在本会话追加补齐，见 issue-04 记录。

## 补充交付（同 Issue 内追加）

- `internal/config/config.go` + `main.go`：仿 model-service 启动骨架，经 `bootstrap.ConnectDB` 只连 DB（不初始化 NATS/Redis/capabilities），装配两个 store/client 后 `bootstrap.RunGRPC` 注册两个 gRPC service。`go build/vet` 通过，见 `quota-policy-batch-note-it.md` Open Questions。
