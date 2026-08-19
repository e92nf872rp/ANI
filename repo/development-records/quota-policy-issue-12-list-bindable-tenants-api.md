# QUOTA-POLICY-ISSUE-12 — 查询可绑定套餐的租户列表 API

## Summary

实现 `GET /tenant-plans/{planId}/bindable-tenants`（US-018）：返回 `status != disabled` 且 `plan_id IS DISTINCT FROM planId` 的租户摘要，供绑定弹窗选用。

## Changes

- OpenAPI：`/tenant-plans/{planId}/bindable-tenants`；响应复用 `BoundTenantsResponse`（无 search）
- Proto：`ListBindableTenants` RPC + Request/Response（items=`BoundTenant`）
- Store：`TenantPlanStore.ListBindableTenants`（`requirePlanExists` + `ORDER BY name`）
- Service：`TenantPlanService.ListBindableTenants`
- Gateway：`listBindableTenants` + `boundTenantsJSON`（顺带规范 bound tenants 序列化）

## Verification

```bash
cd repo/services/tenant-service
go build ./...
go test ./internal/service/ -run "ListBindableTenants|ListBoundTenants" -count=1

cd repo/services/ani-gateway
go build ./...
```

## Notes

- SQL 使用 `plan_id IS DISTINCT FROM $1`，避免 `NULL != planId` 被 SQL 三值逻辑排除。
- 按产品确认：**不提供**关键字模糊查询；前端 Select 可本地过滤展示。
- Issue Scope 未含前端；`BindTenantDialog` 仍可用 tenant-admins 兜底，后续可改接本端点。
