package router

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

// reservationAPI holds the admin and tenant-scoped services needed by the
// reservation and self-query endpoints. admin (QuotaAdminService) backs the
// BOSS-facing PUT/GET /admin/tenants/:tenant_id/reservations and the
// tenant-facing GET /reservations/me (it self-opens a platform tx with RLS
// bypass so it can read any tenant's allocation row). store (QuotaStoreService)
// backs GET /quotas/me via GetMy (self-opens a tenant tx so RLS filters to
// the authenticated tenant).
type reservationAPI struct {
	admin ports.QuotaAdminService
	store ports.QuotaStoreService
}

// registerReservationResources registers the 4 reservation / self-query
// endpoints introduced by SPEC §4.3:
//   - PUT  /admin/tenants/:tenant_id/reservations (BOSS set allocated_gpu_count)
//   - GET  /admin/tenants/:tenant_id/reservations (BOSS query reservation)
//   - GET  /quotas/me                           (tenant self-query quota)
//   - GET  /reservations/me                     (tenant self-query reservation)
func registerReservationResources(v1 *route.RouterGroup, admin ports.QuotaAdminService, store ports.QuotaStoreService) {
	api := reservationAPI{admin: admin, store: store}
	v1.PUT("/admin/tenants/:tenant_id/reservations", api.putTenantReservations)
	v1.GET("/admin/tenants/:tenant_id/reservations", api.getTenantReservations)
	v1.GET("/quotas/me", api.getMyQuota)
	v1.GET("/reservations/me", api.getMyReservations)
}

// ---- request / response structs (aligned with v1.yaml schemas) ----

type reservationPutRequest struct {
	AllocatedGPUCount int64 `json:"allocated_gpu_count"`
}

type reservationViewResponse struct {
	TenantID          string `json:"tenant_id"`
	AllocatedGPUCount int64  `json:"allocated_gpu_count"`
	Used              int64  `json:"used"`
	Reserved          int64  `json:"reserved"`
	Available         int64  `json:"available"`
	Tightened         bool   `json:"tightened,omitempty"`
}

// ---- handlers ----

// putTenantReservations sets the tenant's allocated_gpu_count (BOSS reservation).
// Validates Idempotency-Key header, parses body, delegates to QuotaAdminService.
// PutReservation validates allocated <= total (422) and clamps on shrink.
func (api *reservationAPI) putTenantReservations(ctx context.Context, c *app.RequestContext) {
	if api.admin == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "NOT_CONFIGURED", "quota admin service is not configured")
		return
	}
	tenantID := c.Param("tenant_id")
	idempotencyKey := strings.TrimSpace(string(c.Request.Header.Peek("Idempotency-Key")))
	if idempotencyKey == "" {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "Idempotency-Key header is required")
		return
	}
	var req reservationPutRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid reservation request")
		return
	}
	if req.AllocatedGPUCount < 0 {
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", "allocated_gpu_count must be >= 0")
		return
	}
	view, err := api.admin.PutReservation(ctx, idempotencyKey, ports.ReservationPutRequest{
		TenantID:          tenantID,
		AllocatedGPUCount: req.AllocatedGPUCount,
	})
	if err != nil {
		writeReservationError(c, err)
		return
	}
	c.JSON(http.StatusOK, reservationViewFromPort(view))
}

// getTenantReservations queries a tenant's reservation view (BOSS admin).
func (api *reservationAPI) getTenantReservations(ctx context.Context, c *app.RequestContext) {
	if api.admin == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "NOT_CONFIGURED", "quota admin service is not configured")
		return
	}
	tenantID := c.Param("tenant_id")
	view, err := api.admin.GetReservation(ctx, tenantID)
	if err != nil {
		writeReservationError(c, err)
		return
	}
	c.JSON(http.StatusOK, reservationViewFromPort(view))
}

// getMyQuota returns the authenticated tenant's quota view (Console self-query).
// Uses QuotaStoreService.GetMy which self-opens a tenant-scoped transaction so
// RLS filters to the current tenant.
func (api *reservationAPI) getMyQuota(ctx context.Context, c *app.RequestContext) {
	if api.store == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "NOT_CONFIGURED", "quota store service is not configured")
		return
	}
	tenantID := middleware.GetTenantID(c)
	if tenantID == "" {
		writeDemoError(c, http.StatusUnauthorized, "UNAUTHORIZED", "tenant context is required")
		return
	}
	view, err := api.store.GetMy(ctx, tenantID)
	if err != nil {
		writeReservationError(c, err)
		return
	}
	c.JSON(http.StatusOK, quotaResponseFromView(view))
}

// getMyReservations returns the authenticated tenant's reservation view
// (Console self-query). Reuses QuotaAdminService.GetReservation with the
// tenant ID from the auth middleware.
func (api *reservationAPI) getMyReservations(ctx context.Context, c *app.RequestContext) {
	if api.admin == nil {
		writeDemoError(c, http.StatusServiceUnavailable, "NOT_CONFIGURED", "quota admin service is not configured")
		return
	}
	tenantID := middleware.GetTenantID(c)
	if tenantID == "" {
		writeDemoError(c, http.StatusUnauthorized, "UNAUTHORIZED", "tenant context is required")
		return
	}
	view, err := api.admin.GetReservation(ctx, tenantID)
	if err != nil {
		writeReservationError(c, err)
		return
	}
	c.JSON(http.StatusOK, reservationViewFromPort(view))
}

// ---- helpers ----

func reservationViewFromPort(v ports.ReservationView) reservationViewResponse {
	return reservationViewResponse{
		TenantID:          v.TenantID,
		AllocatedGPUCount: v.AllocatedGPUCount,
		Used:              v.Used,
		Reserved:          v.Reserved,
		Available:         v.Available,
		Tightened:         v.Tightened,
	}
}

// quotaResponseFromView converts a ports.QuotaView (map keyed by resource type)
// into the Quota response schema ({tenant_id, items: QuotaItem[]}).
// GetMy does not JOIN resource_quota_meta, so unit/display_name/is_discrete
// are omitted (they are optional in the QuotaItem schema).
func quotaResponseFromView(v ports.QuotaView) quotaResponse {
	items := make([]quotaItem, 0, len(v.Total))
	for rt, total := range v.Total {
		items = append(items, quotaItem{
			ResourceType: rt,
			Total:        total,
			Used:         v.Used[rt],
			Reserved:     v.Reserved[rt],
		})
	}
	return quotaResponse{TenantID: v.TenantID, TenantName: v.TenantName, Items: items}
}

// writeReservationError maps adapter sentinel errors to HTTP three-part errors.
func writeReservationError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "VALIDATION_FAILED", err.Error())
	case errors.Is(err, ports.ErrTenantNotFound):
		writeDemoError(c, http.StatusNotFound, "TENANT_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrQuotaNotFound):
		writeDemoError(c, http.StatusNotFound, "QUOTA_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrReservationNotFound):
		writeDemoError(c, http.StatusNotFound, "RESERVATION_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrReservationExceedsQuota):
		writeDemoError(c, http.StatusUnprocessableEntity, "RESERVATION_EXCEEDS_QUOTA", err.Error())
	default:
		writeDemoError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}
