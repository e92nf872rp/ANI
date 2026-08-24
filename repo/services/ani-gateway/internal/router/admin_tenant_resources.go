package router

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/kubercloud/ani/pkg/ports"
)

// adminTenantAPI holds Core tenant service for /admin/tenants/*.
type adminTenantAPI struct {
	tenant ports.TenantService
}

// registerAdminTenantResources registers the Core tenant read endpoint:
//
//	GET /admin/tenants/:tenant_id
func registerAdminTenantResources(v1 *route.RouterGroup, tenant ports.TenantService) {
	if tenant == nil {
		return
	}
	api := adminTenantAPI{tenant: tenant}
	v1.GET("/admin/tenants/:tenant_id", api.getTenant)
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

func (api *adminTenantAPI) getTenant(ctx context.Context, c *app.RequestContext) {
	tenantID := c.Param("tenant_id")
	if api.tenant == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "TENANT_UNAVAILABLE", "tenant service unavailable")
		return
	}
	tenant, err := api.tenant.GetTenant(ctx, tenantID)
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
