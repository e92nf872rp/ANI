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

// TenantService reads tenant rows under platform RLS bypass.
type TenantService interface {
	GetTenant(ctx context.Context, tenantID string) (Tenant, error)
}
