package ports

import (
	"context"
	"time"
)

// Tenant is the Core tenant view used by platform admin flows
// (e.g. binding a quota plan, tenant list management).
type Tenant struct {
	ID           string
	Name         string
	DisplayName  string
	Status       string // active | frozen | disabled
	PlanID       string
	ContactEmail string
	FrozenAt     *time.Time
	DisabledAt   *time.Time
	UserCount    int64 // getTenant / listTenants computed column
	AdminCount   int64 // tenant-admin role member count
	Auth         *TenantAuthSummary // getTenant JOIN tenant_auth；仅两开关；nil/缺行 → 双 false
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// TenantSummary is the bound/bindable tenant list row (no plan_id / timestamps).
type TenantSummary struct {
	ID          string
	Name        string
	DisplayName string
	Status      string // active | frozen | disabled
}

// TenantListItem is a single row in GET /admin/tenants list responses.
type TenantListItem struct {
	ID          string
	Name        string
	DisplayName string
	Status      string
	PlanID      string
	AdminCount  int64
	CreatedAt   time.Time
}

// TenantListResult is the cursor-paginated tenant list (created_at DESC, id DESC).
type TenantListResult struct {
	Items      []TenantListItem
	NextCursor string // "" = no more
}

// TenantAuthSummary is the getTenant embedded auth snippet (switches only).
type TenantAuthSummary struct {
	SsoEnabled  bool
	MfaRequired bool
}

// TenantAuth is the tenant_auth row (1:1 with tenants).
type TenantAuth struct {
	TenantID    string
	SsoEnabled  bool
	SsoProvider *string
	MfaRequired bool
	UpdatedAt   time.Time
}

// TenantLifecycleEntry is one tenant_lifecycle audit row.
type TenantLifecycleEntry struct {
	ID        string
	TenantID  string
	Action    string // create | freeze | unfreeze | disable
	Reason    *string
	UserID    *string
	RequestID *string
	CreatedAt time.Time
}

// CreateTenantInput is the Core createTenant request body
// (admin_password_hash is bcrypt output from tenant-service).
// RequestID / ActorUserID 由 Gateway 从中间件注入，不在 OpenAPI body 中。
type CreateTenantInput struct {
	Name              string
	DisplayName       string
	ContactEmail      string
	PlanID            string
	AdminEmail        string
	AdminName         string
	AdminPasswordHash string
	RequestID         string // optional → tenant_lifecycle.request_id
	ActorUserID       string // optional platform user UUID → tenant_lifecycle.user_id
}

// UpdateTenantInput is a partial update for display_name / contact_email.
type UpdateTenantInput struct {
	DisplayName  *string
	ContactEmail *string
}

// TenantAuthPatch is a partial update for tenant_auth fields.
type TenantAuthPatch struct {
	SsoEnabled  *bool
	SsoProvider *string
	MfaRequired *bool
}

// ListTenantsFilter filters GET /admin/tenants.
type ListTenantsFilter struct {
	Limit  int
	Cursor string
	Status string
	Search string
}

// TenantLifecycleFilter filters GET /admin/tenants/{id}/lifecycle.
type TenantLifecycleFilter struct {
	Limit  int
	Cursor string
	Action string
}

// TenantLifecycleListResult is the cursor-paginated lifecycle list.
type TenantLifecycleListResult struct {
	Items      []TenantLifecycleEntry
	NextCursor string
}

// TenantService reads and writes tenant rows under platform RLS bypass.
type TenantService interface {
	GetTenant(ctx context.Context, tenantID string) (Tenant, error)
	// ListAvailableTenants 返回 status <> 'disabled' 的租户列表，按 created_at DESC 排序。
	// OpenAPI：GET /admin/tenant-admins/available-tenants（TenantUsers）；实现归属 TenantService。
	ListAvailableTenants(ctx context.Context) ([]TenantSummary, error)

	// CreateTenant 事务内建 tenants + tenant_auth + users + user_roles + tenant_lifecycle('create')。
	CreateTenant(ctx context.Context, in CreateTenantInput) (Tenant, error)
	// ListTenants 游标分页列表；admin_count 由 LATERAL 连表统计。
	ListTenants(ctx context.Context, filter ListTenantsFilter) (TenantListResult, error)
	// UpdateTenant 部分更新 display_name / contact_email；不可改 name / status。
	UpdateTenant(ctx context.Context, tenantID string, in UpdateTenantInput) (Tenant, error)
	// FreezeTenant active -> frozen。
	FreezeTenant(ctx context.Context, tenantID, requestID string) (Tenant, error)
	// UnfreezeTenant frozen -> active。
	UnfreezeTenant(ctx context.Context, tenantID, requestID string) (Tenant, error)
	// DisableTenant active/frozen -> disabled（终态）。
	DisableTenant(ctx context.Context, tenantID, requestID string) (Tenant, error)
	// GetTenantAuth 读取 tenant_auth 配置。
	GetTenantAuth(ctx context.Context, tenantID string) (TenantAuth, error)
	// UpdateTenantAuth 部分更新 SSO/MFA 字段。
	UpdateTenantAuth(ctx context.Context, tenantID string, patch TenantAuthPatch) (TenantAuth, error)
	// ListTenantLifecycle 查询租户生命周期记录。
	ListTenantLifecycle(ctx context.Context, tenantID string, filter TenantLifecycleFilter) (TenantLifecycleListResult, error)
}
