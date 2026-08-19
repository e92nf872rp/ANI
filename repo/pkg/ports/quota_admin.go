package ports

import (
	"context"
	"time"
)

// QuotaItemInput is one dimension supplied when creating a tenant's quota.
// A Total <= 0 means "not provided" and falls back to resource_quota_meta
// default_quota.
type QuotaItemInput struct {
	ResourceType ResourceType
	Total        int64 // <=0 means not provided; use default_quota
}

// QuotaItemUpdate is one dimension supplied when updating a tenant's quota
// total. A shrink is clamped to reserved+used so the total never violates the
// CHECK constraint.
type QuotaItemUpdate struct {
	ResourceType ResourceType
	Total        int64
}

// QuotaMeta describes an enabled quota dimension from resource_quota_meta.
type QuotaMeta struct {
	ResourceType ResourceType
	DisplayName  string
	Unit         string
	DefaultQuota int64
	IsDiscrete   bool
}

// QuotaInfo is one tenant quota row enriched with its resource_quota_meta
// metadata.
type QuotaInfo struct {
	TenantID     string
	ResourceType ResourceType
	Total        int64
	Reserved     int64
	Used         int64
	Tightened    bool   // true when a PUT shrink clamped total above the request
	Unit         string // from meta (returned on GET JOIN)
	DisplayName  string // from meta (returned on GET JOIN)
	IsDiscrete   bool   // from meta (returned on GET JOIN)
	UpdatedAt    time.Time
}

// QuotaAdminService manages a tenant's quota lifecycle and the quota dimension
// catalog. All methods self-open a platform transaction, so the adapter must
// operate with the RLS platform bypass policy to administer any tenant.
type QuotaAdminService interface {
	CreateTenantQuota(ctx context.Context, tenantID string, items []QuotaItemInput) ([]QuotaInfo, error)
	UpdateTenantQuota(ctx context.Context, tenantID string, items []QuotaItemUpdate) ([]QuotaInfo, error)
	GetTenantQuota(ctx context.Context, tenantID string) ([]QuotaInfo, error)
	DeleteTenantQuota(ctx context.Context, tenantID string) error
	ListQuotaMeta(ctx context.Context) ([]QuotaMeta, error)
	UpsertTenantQuota(ctx context.Context, tenantID string, items []QuotaItemInput) ([]QuotaInfo, error)
}
