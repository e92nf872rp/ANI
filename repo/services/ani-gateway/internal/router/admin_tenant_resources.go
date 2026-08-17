package router

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/kubercloud/ani/pkg/ports"
)

// adminTenantAPI holds TenantService for Core /admin/tenants/* (not Services stubs).
type adminTenantAPI struct {
	tenants ports.TenantService
}

// registerAdminTenantResources registers Core tenant minimal endpoints:
//
//	GET /admin/tenants/:tenant_id
//	PUT /admin/tenants/:tenant_id/plan
//
// Does not touch /api/v1/svc/tenant/* (registerTenant).
func registerAdminTenantResources(v1 *route.RouterGroup, tenants ports.TenantService) {
	if tenants == nil {
		return
	}
	api := adminTenantAPI{tenants: tenants}
	v1.GET("/admin/tenants/:tenant_id", api.getTenant)
	v1.PUT("/admin/tenants/:tenant_id/plan", api.updateTenantPlan)
}

type adminTenantResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Status      string    `json:"status"`
	PlanID      string    `json:"plan_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type adminTenantPlanUpdateRequest struct {
	PlanID string `json:"plan_id"`
}

func (api *adminTenantAPI) getTenant(ctx context.Context, c *app.RequestContext) {
	tenantID := c.Param("tenant_id")
	tenant, err := api.tenants.GetTenant(ctx, tenantID)
	if err != nil {
		writeAdminTenantError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAdminTenantResponse(tenant))
}

func (api *adminTenantAPI) updateTenantPlan(ctx context.Context, c *app.RequestContext) {
	tenantID := c.Param("tenant_id")
	var req adminTenantPlanUpdateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid tenant plan update request")
		return
	}
	if strings.TrimSpace(req.PlanID) == "" {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "plan_id required")
		return
	}
	tenant, err := api.tenants.UpdateTenantPlan(ctx, tenantID, req.PlanID)
	if err != nil {
		writeAdminTenantError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAdminTenantResponse(tenant))
}

func toAdminTenantResponse(t ports.Tenant) adminTenantResponse {
	return adminTenantResponse{
		ID:          t.ID,
		Name:        t.Name,
		DisplayName: t.DisplayName,
		Status:      t.Status,
		PlanID:      t.PlanID,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
	}
}

func writeAdminTenantError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", err.Error())
	case errors.Is(err, ports.ErrTenantNotFound):
		writeDemoError(c, http.StatusNotFound, "TENANT_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrTenantPlanNotFound):
		writeDemoError(c, http.StatusNotFound, "TENANT_PLAN_NOT_FOUND", err.Error())
	default:
		writeDemoError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}
