# 后端：查询配额元数据 API（前端创建/编辑套餐时获取可用维度）

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)
- Core API: [配额core层对接api设计.md](../../../../../配额core层对接api设计.md)

## Description
前端创建套餐（issue-011/013）和编辑限额（issue-008）时需要知道系统有哪些配额维度可用（resource_type / display_name / unit / default_quota / is_discrete），否则无法渲染限额表单。实现 `GET /quota-meta` 端点，网关转发到 tenant-service，tenant-service 透传 Core `GET /admin/quota-meta` 结果。复用已有的 `QuotaSvcClient.ListQuotaMeta`（issue-008 已实现）。覆盖 US-017。

## Scope
- Product line: boss
- Code paths allowed: `repo/api/proto/tenant/v1/tenant_plan.proto`、`repo/api/openapi/services/v1.yaml`、`repo/services/ani-gateway/internal/router/tenant_plans.go`、`repo/services/ani-gateway/internal/router/router.go`、`repo/services/tenant-service/internal/service/tenant_plan_service.go`

## Acceptance Criteria
### 查询配额元数据（US-017）
- [x] `GET /api/v1/svc/quota-meta` 返回 `items[]{resource_type, display_name, unit, default_quota, is_discrete}`
- [x] 前端创建套餐时调此端点获取可用维度列表，渲染限额表单的行选项
- [x] 前端编辑限额时调此端点获取维度元数据（display_name / unit 用于展示）
- [x] tenant-service 新增 `ListQuotaMeta` gRPC RPC，透传 Core 结果（复用 `QuotaSvcClient.ListQuotaMeta`）
- [x] proto 新增 `ListQuotaMetaRequest`（空）+ `ListQuotaMetaResponse{ items[] }` + `QuotaMetaItem` message
- [x] OpenAPI v1.yaml 新增 `GET /quota-meta` 端点 + `QuotaMetaItem` schema
- [x] 网关 `tenant_plans.go` 新增 `listQuotaMeta` handler + 路由注册

### 通用
- [x] Core 不可用时返回 502 `GRPC_CLIENT_UNAVAILABLE`（复用现有错误映射）
- [x] 无需鉴权特殊处理：复用现有 platform-admin / platform-ops / platform-readonly 角色
- [x] `go build ./...` 编译通过

## Dependencies
#2（gRPC 接口与 ports）、#4（网关接入框架）、#8（QuotaSvcClient.ListQuotaMeta 已实现）

## Type
backend

## Priority
high

## References
- SPEC: §5.1.5 ListQuotaMeta / §4.3 QuotaMeta schema
- Core API: §2 查询配额元数据端点
- Plan: 租户管理plan v3.0 §5.4
