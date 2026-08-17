package ports

import (
	"context"

	"github.com/google/uuid"
)

// Core 租户最小读写 API 客户端端口（与配额 API 分离）。
//   GET /api/v1/admin/tenants/{tenant_id}
//   PUT /api/v1/admin/tenants/{tenant_id}/plan
// 实现：services/tenant-service/internal/repo/adapters/core（封装 Core Go SDK anisdk.Client）。

// TenantSvcClient 定义通向 Core 租户 API 的调用客户端接口。
type TenantSvcClient interface {
	// GetTenant 查询租户最小视图（Core GET /admin/tenants/{id}）。
	// 租户不存在 → ErrTenantNotFound。
	GetTenant(ctx context.Context, tenantID uuid.UUID) (Tenant, error)

	// UpdateTenantPlan 更新租户绑定套餐（Core PUT /admin/tenants/{id}/plan）。
	// 仅改 plan_id；套餐不存在 → ErrTenantPlanNotFound；租户不存在 → ErrTenantNotFound。
	UpdateTenantPlan(ctx context.Context, tenantID uuid.UUID, planID uuid.UUID) (Tenant, error)
}
