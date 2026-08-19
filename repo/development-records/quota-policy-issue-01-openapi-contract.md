# QUOTA-POLICY-ISSUE-01：OpenAPI 新增 tenant-plans 路径与 schema

> **批次类型：** Feature batch（BOSS 租户配额套餐功能流 Issue #1）
> **完成日期：** 2026-08-10
> **Scope：** `repo/api/openapi/services/v1.yaml`
> **依赖：** 无（功能流第一个 Issue）
> **Product line：** boss

## 交付内容

在 `repo/api/openapi/services/v1.yaml` 中新增套餐管理的完整 OpenAPI 契约：11 个端点的 paths、request/response schemas、错误码定义。Core `v1.yaml` 不修改。此 Issue 是后续所有实现 Issue 的前置依赖——前端与后端均从此契约生成类型和客户端。

### 新增端点（11 个）

| operationId/路径 | Method | 说明 |
|---|---|---|
| `POST /tenant-plans` | POST | 创建套餐，body `{code,name,description,quota_limits}`，响应 200 `{id,message}` |
| `GET /tenant-plans` | GET | 套餐列表，query（limit/cursor/status/search），响应 200 游标分页（items/total/next_cursor） |
| `GET /tenant-plans/{planId}` | GET | 套餐详情，响应 200 `TenantPlan` |
| `GET /tenant-plans/{planId}/quota-limits` | GET | 套餐配额限额，响应 200 `{items[]}` |
| `POST /tenant-plans/{planId}/activate` | POST | 激活套餐，body `{idempotency_key}` |
| `POST /tenant-plans/{planId}/disable` | POST | 停用套餐，body `{idempotency_key}` |
| `DELETE /tenant-plans/{planId}` | DELETE | 删除套餐（软删除） |
| `PUT /tenant-plans/{planId}/quota-limits` | PUT | 修改限额，body `{idempotency_key, items[]}` |
| `POST /tenants/{tenantId}/plan` | POST | 绑定套餐，body `{idempotency_key, plan_id}` |
| `GET /tenant-plans/{planId}/tenants` | GET | 已绑租户列表，响应 200 `{items[]}` |
| `GET /tenant-plans/{planId}/audit-logs` | GET | 操作历史，query（limit/cursor/action/result），响应 200 游标分页 |

### 共享 Schema

`TenantPlan`、`TenantPlanListResponse`、`PlanQuotaLimitView`、`PlanQuotaLimitItem`、`TenantPlanListQuery`、`AuditLogResponse` 等。

### 错误码

`VALIDATION_FAILED(400)`、`TENANT_PLAN_NOT_FOUND(404)`、`PLAN_CODE_CONFLICT(409)`、`PLAN_STATE_INVALID(409)`、`TENANT_PLAN_IN_USE(409)`、`TENANT_STATE_INVALID(409)`、`QUOTA_RESOURCE_NOT_REGISTERED(422)`。

### 幂等键要求

`POST /tenant-plans`、`PUT /tenant-plans/{id}/quota-limits`、`POST /tenants/{id}/plan` 均带 `idempotency_key`。

## Acceptance Criteria 验证

| AC | 证据 | 结果 |
|---|---|---|
| 11 端点 | v1.yaml paths 全部就位 | ✅ |
| 共享 schemas | TenantPlan/TenantPlanListResponse 等定义 | ✅ |
| 7 错误码 | 枚举全部存在 | ✅ |
| Idempotency-Key | 3 个写端点带 `idempotency_key` body | ✅ |
| Typecheck/lint | `npx yaml-lint services/v1.yaml` → pass | ✅ |
| AC checklist | 15/15 satisfied | ✅ |
| review-it | 全部痛点通过审查（无 accepted findings） | ✅ |

## 验证命令

```bash
cd repo
npx yaml-lint api/openapi/services/v1.yaml
git diff --stat   # 1 file changed, 352 insertions(+)
```

## 边界声明

- 本 Issue 只修改 Core Services OpenAPI 契约，不涉及 handler/adapter/前端实现。
- Handler/adapter/服务端实现属后续 Issue（#2-#10）。
- 本批次不声明 runtime ready 或 production ready。
