package router

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

// adminTenantAPI holds TenantService for Core /admin/tenants/* (not Services stubs).
type adminTenantAPI struct {
	tenants ports.TenantService
}

// registerAdminTenantResources registers Core tenant minimal endpoints:
//
//	GET /admin/plans/bound-tenant-counts
//	GET /admin/plans/:plan_id/bound-tenants
//	GET /admin/plans/:plan_id/bindable-tenants
//	GET /admin/tenants/:tenant_id
//	PUT /admin/tenants/:tenant_id/plan
//
// Does not touch /api/v1/svc/tenant/* (registerTenant).
func registerAdminTenantResources(v1 *route.RouterGroup, tenants ports.TenantService) {
	if tenants == nil {
		return
	}
	api := adminTenantAPI{tenants: tenants}
	v1.GET("/admin/plans/bound-tenant-counts", api.listPlanBoundTenantCounts)
	v1.GET("/admin/plans/:plan_id/bound-tenants", api.listPlanBoundTenants)
	v1.GET("/admin/plans/:plan_id/bindable-tenants", api.listPlanBindableTenants)
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

type planBoundTenantCountItem struct {
	PlanID string `json:"plan_id"`
	Count  int64  `json:"count"`
}

const maxBoundTenantCountPlanIDs = 100

func (api *adminTenantAPI) listPlanBoundTenantCounts(ctx context.Context, c *app.RequestContext) {
	planIDs := queryRepeat(c, "plan_id")
	if len(planIDs) == 0 {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "plan_id required")
		return
	}
	if len(planIDs) > maxBoundTenantCountPlanIDs {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "plan_id exceeds max 100")
		return
	}
	canonical := make([]string, 0, len(planIDs))
	for _, raw := range planIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "plan_id must be a uuid")
			return
		}
		canonical = append(canonical, id.String())
	}
	counts, err := api.tenants.CountBoundTenants(ctx, canonical)
	if err != nil {
		writeAdminTenantError(c, err)
		return
	}
	items := make([]planBoundTenantCountItem, 0, len(canonical))
	for _, id := range canonical {
		items = append(items, planBoundTenantCountItem{
			PlanID: id,
			Count:  counts[id],
		})
	}
	c.JSON(http.StatusOK, map[string]any{"items": items})
}

type tenantSummaryItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
}

func (api *adminTenantAPI) listPlanBoundTenants(ctx context.Context, c *app.RequestContext) {
	api.listPlanTenantSummaries(ctx, c, api.tenants.ListBoundTenants)
}

func (api *adminTenantAPI) listPlanBindableTenants(ctx context.Context, c *app.RequestContext) {
	api.listPlanTenantSummaries(ctx, c, api.tenants.ListBindableTenants)
}

func (api *adminTenantAPI) listPlanTenantSummaries(ctx context.Context, c *app.RequestContext, listFn func(context.Context, string) ([]ports.TenantSummary, error)) {
	planID := strings.TrimSpace(c.Param("plan_id"))
	if _, err := uuid.Parse(planID); err != nil {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "plan_id must be a uuid")
		return
	}
	rows, err := listFn(ctx, planID)
	if err != nil {
		writeAdminTenantError(c, err)
		return
	}
	items := make([]tenantSummaryItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, tenantSummaryItem{
			ID:          row.ID,
			Name:        row.Name,
			DisplayName: row.DisplayName,
			Status:      row.Status,
		})
	}
	c.JSON(http.StatusOK, map[string]any{"items": items})
}

func queryRepeat(c *app.RequestContext, key string) []string {
	raw := c.QueryArgs().PeekAll(key)
	out := make([]string, 0, len(raw))
	for _, b := range raw {
		s := strings.TrimSpace(string(b))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
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
