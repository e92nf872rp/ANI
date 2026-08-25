package ports

import (
	"context"

	"github.com/google/uuid"
)

// Core 租户最小读 API 客户端端口（与配额套餐绑定 API、租户管理员用户 API 分离）。
//
//	GET /api/v1/admin/tenants/{tenant_id}
//	GET /api/v1/admin/tenant-admins/available-tenants
//
// 实现：services/tenant-service/internal/repo/adapters/core（封装 Core Go SDK anisdk.Client）。

// TenantSvcClient 定义通向 Core 租户 API 的调用客户端接口。
type TenantSvcClient interface {
	// GetTenant 查询租户最小视图（Core GET /admin/tenants/{id}）。
	// 租户不存在 → ErrTenantNotFound。
	GetTenant(ctx context.Context, tenantID uuid.UUID) (Tenant, error)

	// ListAvailableTenants 查询非 disabled 租户摘要（Core GET /admin/tenant-admins/available-tenants）。
	// 按 created_at DESC；不分页。
	ListAvailableTenants(ctx context.Context) ([]BoundTenant, error)
}
