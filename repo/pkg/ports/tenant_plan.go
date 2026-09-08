package ports

import "context"

// TenantPlanService manages tenant plan binding + bound/bindable tenant views
// under platform RLS bypass.
type TenantPlanService interface {
	UpdateTenantPlan(ctx context.Context, tenantID string, planID string) (Tenant, error)
	CountBoundTenants(ctx context.Context, planIDs []string) (map[string]int64, error)
	ListBoundTenants(ctx context.Context, planID string) ([]TenantSummary, error)
	ListBindableTenants(ctx context.Context, planID string) ([]TenantSummary, error)
}
