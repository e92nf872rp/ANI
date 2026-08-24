package router

import (
	"context"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

// adminTenantPlanAPI holds Core tenant-plan service for /admin/plans/* and tenant plan binding.
type adminTenantPlanAPI struct {
	tenantPlan ports.TenantPlanService
}

type adminTenantPlanUpdateRequest struct {
	PlanID string `json:"plan_id"`
}

type planBoundTenantCountItem struct {
	PlanID string `json:"plan_id"`
	Count  int64  `json:"count"`
}

type tenantSummaryItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
}

const maxBoundTenantCountPlanIDs = 100

// registerAdminTenantPlanResources registers the Core tenant-plan endpoints:
//
//	GET /admin/plans/bound-tenant-counts
//	GET /admin/plans/:plan_id/bound-tenants
//	GET /admin/plans/:plan_id/bindable-tenants
//	PUT /admin/tenants/:tenant_id/plan
func registerAdminTenantPlanResources(v1 *route.RouterGroup, tenantPlan ports.TenantPlanService) {
	if tenantPlan == nil {
		return
	}
	api := adminTenantPlanAPI{tenantPlan: tenantPlan}
	v1.GET("/admin/plans/bound-tenant-counts", api.listPlanBoundTenantCounts)
	v1.GET("/admin/plans/:plan_id/bound-tenants", api.listPlanBoundTenants)
	v1.GET("/admin/plans/:plan_id/bindable-tenants", api.listPlanBindableTenants)
	v1.PUT("/admin/tenants/:tenant_id/plan", api.updateTenantPlan)
}

func (api *adminTenantPlanAPI) listPlanBoundTenantCounts(ctx context.Context, c *app.RequestContext) {
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
	counts, err := api.tenantPlan.CountBoundTenants(ctx, canonical)
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

func (api *adminTenantPlanAPI) listPlanBoundTenants(ctx context.Context, c *app.RequestContext) {
	api.listPlanTenantSummaries(ctx, c, api.tenantPlan.ListBoundTenants)
}

func (api *adminTenantPlanAPI) listPlanBindableTenants(ctx context.Context, c *app.RequestContext) {
	api.listPlanTenantSummaries(ctx, c, api.tenantPlan.ListBindableTenants)
}

func (api *adminTenantPlanAPI) listPlanTenantSummaries(ctx context.Context, c *app.RequestContext, listFn func(context.Context, string) ([]ports.TenantSummary, error)) {
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

func (api *adminTenantPlanAPI) updateTenantPlan(ctx context.Context, c *app.RequestContext) {
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
	tenant, err := api.tenantPlan.UpdateTenantPlan(ctx, tenantID, req.PlanID)
	if err != nil {
		writeAdminTenantError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAdminTenantResponse(tenant))
}
