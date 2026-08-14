# QUOTA-POLICY-ISSUE-10：更新套餐基本信息（name / description）— proto + service + store + 网关

> **批次类型：** Feature batch（BOSS 租户配额套餐功能流 Issue #10）
> **完成日期：** 2026-08-11
> **Scope：** `repo/api/proto/tenant/v1/tenant_plan.proto`、`repo/api/openapi/services/v1.yaml`、`repo/services/ani-gateway/internal/router/tenant_plans.go`、`repo/services/tenant-service/internal/service/tenant_plan_service.go`、`repo/services/tenant-service/internal/repo/adapters/postgres/tenant_plan_store.go`
> **依赖：** #2 gRPC 接口与 ports、#4 网关接入
> **Product line：** boss

## 交付内容

实现 `UpdateTenantPlan`（PUT /tenant-plans/{planId}）端到端链路：proto RPC 定义 + 网关 handler + service 业务逻辑 + store 动态 SET 拼装。复用已有的 store `Update` 方法。

### Proto 定义

- 新增 `UpdateTenantPlan` RPC + `UpdateTenantPlanRequest` message。
- `name` / `description` 用 `google.protobuf.StringValue` 表达 optional 语义：未设置 = 不更新；空串 = 清空。

### 网关 handler

- body `Name *string` / `Description *string`：JSON 未传/null → 不设 proto 字段；空串 → `wrapperspb.String("")`。
- `IdempotencyKey` 从 body 或 header 回退。

### Service 层

- plan_id UUID 校验 → `writeAuditFailure`。
- 映射 `StringValue` → `*string`（nil = 不更新）。
- 调 store `Update`，未删除/不存在 → `TENANT_PLAN_NOT_FOUND`。
- 审计 `tenant_plan.update`（含 name_updated / description_updated 布尔标记）。
- `writeAuditSuccess` 返回值被忽略（审计失败不阻塞已更新成功的业务）。

### Store 层

- 动态拼 SET 子句：仅非 nil 字段参与 UPDATE，始终刷新 `updated_at`。
- `WHERE id = $1 AND is_deleted = FALSE`：软删除套餐不可更新。
- `pgx.ErrNoRows` → `ErrTenantPlanNotFound`。

### 测试覆盖

| 测试场景 | 覆盖 | 结果 |
|---|---|---|
| 正常更新 name（description 不变） | StringValue → *string → store | ✅ |
| 正常更新 description（含清空为空串） | 空串 = 清空 | ✅ |
| 同时更新 name + description | 两个字段均 nil → 非 nil | ✅ |
| 空 body 不报错（仅刷新 updated_at） | 两个 StringValue 均未设置 | ✅ |
| 套餐不存在 → 404 | store ErrNoRows → ErrTenantPlanNotFound | ✅ |

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| PUT /tenant-plans/{planId} 端点 | 网关路由 + OpenAPI 定义 | ✅ |
| name/description 可选字段语义 | proto StringValue + *string | ✅ |
| 未传 = 不更新 | StringValue 未设置 → nil | ✅ |
| 空串 = 清空 | StringValue("") → *string 指向空串 | ✅ |
| 套餐不存在 → 404 | store WHERE is_deleted=FALSE | ✅ |
| 审计记录 | tenant_plan.update + name_updated/description_updated | ✅ |
| 复用 store Update | 动态 SET 拼装 | ✅ |
| proto RPC 定义 | UpdateTenantPlan + UpdateTenantPlanRequest | ✅ |
| OpenAPI 端点 | PUT /tenant-plans/{planId} + UpdateTenantPlanRequest schema | ✅ |
| 编译 | `go build` → EXIT=0 | ✅ |
| 测试 | `go test -run UpdateTenantPlan` → 1/1 PASS | ✅ |
| review-it | 1 个低严重程度 finding（writeAuditSuccess 返回值忽略），实现正确 | ✅ |

## 验证命令

```bash
cd repo
go build ./services/ani-gateway/... ./services/tenant-service/...
go test ./services/tenant-service/internal/service/... -run "UpdateTenantPlan" -v
```

## 边界声明

- 本 Issue 仅实现 name/description 更新，不影响限额、状态、绑定关系。
- 空 body（两字段均未传）仍执行 UPDATE，仅刷新 `updated_at`——PUT 幂等语义，合理。
- `writeAuditSuccess` 返回值被忽略（审计失败不阻塞），与 `BindPlanQuota` 中审计失败返回 error 的处理方式不一致——当前实现是正确的，`BindPlanQuota` 的处理需要修正。

## Open Questions

1. **审计成功返回值处理不一致：** `UpdateTenantPlan` 忽略 `writeAuditSuccess` 返回值（正确），`BindPlanQuota` 返回 error（需修正）。待统一为忽略返回值。
