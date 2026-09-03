package ports

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Core 租户管理 API 客户端端口（扩展 Issue-2 九方法）。
//
//	POST/GET /api/v1/admin/tenants
//	PUT  /api/v1/admin/tenants/{tenant_id}
//	POST /api/v1/admin/tenants/{tenant_id}/freeze|unfreeze|disable
//	GET/PUT /api/v1/admin/tenants/{tenant_id}/auth
//	GET  /api/v1/admin/tenants/{tenant_id}/lifecycle
//
// 实现：services/tenant-service/internal/repo/adapters/core（封装 Core Go SDK anisdk.Client）。
// 配额变更申请查询/持久化归属 TenantStore（见 tenant_store.go），不经本客户端。

// CreateTenantInput 是 Core POST /admin/tenants 请求体（密码已 bcrypt）。
type CreateTenantInput struct {
	Name              string
	DisplayName       string
	ContactEmail      string
	PlanID            uuid.UUID
	AdminEmail        string
	AdminName         string
	AdminPasswordHash string
}

// UpdateTenantInput 是 Core PUT /admin/tenants/{id} 部分更新。
type UpdateTenantInput struct {
	DisplayName  *string
	ContactEmail *string
}

// TenantAuth 是 Core tenant_auth 视图。
type TenantAuth struct {
	TenantID    uuid.UUID
	SsoEnabled  bool
	SsoProvider *string
	MfaRequired bool
	UpdatedAt   time.Time
}

// TenantAuthPatch 是 Core PUT /admin/tenants/{id}/auth 部分更新。
type TenantAuthPatch struct {
	SsoEnabled  *bool
	SsoProvider *string
	MfaRequired *bool
}

// TenantLifecycleEntry 是 Core tenant_lifecycle 记录（GET /admin/tenants/{id}/lifecycle）。
type TenantLifecycleEntry struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Action    string // create | freeze | unfreeze | disable
	Reason    *string
	UserID    *uuid.UUID
	RequestID *string
	CreatedAt time.Time
}

// TenantLifecycleFilter 过滤 Core GET /admin/tenants/{id}/lifecycle。
type TenantLifecycleFilter struct {
	Limit  int
	Cursor string
	Action string
}

// TenantLifecycleListResult 是生命周期游标分页结果。
type TenantLifecycleListResult struct {
	Items      []TenantLifecycleEntry
	NextCursor string
}

// TenantListItem 是 Core GET /admin/tenants 列表项。
type TenantListItem struct {
	ID          uuid.UUID
	Name        string
	DisplayName string
	Status      TenantStatus
	PlanID      uuid.UUID
	AdminCount  int64
	CreatedAt   time.Time
}

// ListTenantsFilter 过滤 Core GET /admin/tenants。
type ListTenantsFilter struct {
	Limit  int
	Cursor string
	Status string
	Search string
}

// TenantListResult 是 Core 租户列表游标分页结果。
type TenantListResult struct {
	Items      []TenantListItem
	NextCursor string
}

// TenantSvcClient 定义通向 Core 租户 API 的调用客户端接口。
type TenantSvcClient interface {
	// GetTenant 查询租户（Core GET /admin/tenants/{id}）。
	// 租户不存在 → ErrTenantNotFound。
	GetTenant(ctx context.Context, tenantID uuid.UUID) (Tenant, error)

	// ListAvailableTenants 查询非 disabled 租户摘要（Core GET /admin/tenant-admins/available-tenants）。
	// 按 created_at DESC；不分页。
	ListAvailableTenants(ctx context.Context) ([]BoundTenant, error)

	// CreateTenant 创建租户（Core POST /admin/tenants）。
	// name UNIQUE 冲突 → ErrTenantNameConflict。
	CreateTenant(ctx context.Context, in CreateTenantInput) (Tenant, error)

	// ListTenants 游标分页列表（Core GET /admin/tenants）。
	ListTenants(ctx context.Context, filter ListTenantsFilter) (TenantListResult, error)

	// UpdateTenant 部分更新基本信息（Core PUT /admin/tenants/{id}）。
	UpdateTenant(ctx context.Context, tenantID uuid.UUID, in UpdateTenantInput) (Tenant, error)

	// FreezeTenant 冻结租户（Core POST /admin/tenants/{id}/freeze）。
	FreezeTenant(ctx context.Context, tenantID uuid.UUID, requestID string) (Tenant, error)

	// UnfreezeTenant 解冻租户（Core POST /admin/tenants/{id}/unfreeze）。
	UnfreezeTenant(ctx context.Context, tenantID uuid.UUID, requestID string) (Tenant, error)

	// DisableTenant 禁用租户（Core POST /admin/tenants/{id}/disable）。
	DisableTenant(ctx context.Context, tenantID uuid.UUID, requestID string) (Tenant, error)

	// GetTenantAuth 读取认证配置（Core GET /admin/tenants/{id}/auth）。
	GetTenantAuth(ctx context.Context, tenantID uuid.UUID) (TenantAuth, error)

	// UpdateTenantAuth 更新认证配置（Core PUT /admin/tenants/{id}/auth）。
	UpdateTenantAuth(ctx context.Context, tenantID uuid.UUID, patch TenantAuthPatch) (TenantAuth, error)

	// ListTenantLifecycle 查询租户生命周期（Core GET /admin/tenants/{id}/lifecycle）。
	// action 空串表示不过滤。
	ListTenantLifecycle(ctx context.Context, tenantID uuid.UUID, filter TenantLifecycleFilter) (TenantLifecycleListResult, error)
}
