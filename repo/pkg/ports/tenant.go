package ports

import (
	"context"
	"time"
)

// Tenant is the minimal Core tenant view used by platform admin flows
// (e.g. binding a quota plan). Full tenant lifecycle lives in Services.
type Tenant struct {
	ID          string
	Name        string
	DisplayName string
	Status      string // active | frozen | disabled
	PlanID      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TenantSummary is the bound/bindable tenant list row (no plan_id / timestamps).
type TenantSummary struct {
	ID          string
	Name        string
	DisplayName string
	Status      string // active | frozen | disabled
}

// TenantService reads/updates tenants rows under platform RLS bypass.
// Methods mirror the former Services TenantStore surface that migrated to Core.
type TenantService interface {
	GetTenant(ctx context.Context, tenantID string) (Tenant, error)
	UpdateTenantPlan(ctx context.Context, tenantID string, planID string) (Tenant, error)
	CountBoundTenants(ctx context.Context, planIDs []string) (map[string]int64, error)
	ListBoundTenants(ctx context.Context, planID string) ([]TenantSummary, error)
	ListBindableTenants(ctx context.Context, planID string) ([]TenantSummary, error)
}
