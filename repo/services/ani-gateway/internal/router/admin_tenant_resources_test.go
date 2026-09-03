package router

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/pkg/ports"
)

type stubAdminTenantService struct{}

func (stubAdminTenantService) GetTenant(context.Context, string) (ports.Tenant, error) {
	return ports.Tenant{}, ports.ErrUnsupported
}
func (stubAdminTenantService) ListAvailableTenants(context.Context) ([]ports.TenantSummary, error) {
	return nil, ports.ErrUnsupported
}
func (stubAdminTenantService) CreateTenant(context.Context, ports.CreateTenantInput) (ports.Tenant, error) {
	return ports.Tenant{}, ports.ErrUnsupported
}
func (stubAdminTenantService) ListTenants(context.Context, ports.ListTenantsFilter) (ports.TenantListResult, error) {
	return ports.TenantListResult{}, ports.ErrUnsupported
}
func (stubAdminTenantService) UpdateTenant(context.Context, string, ports.UpdateTenantInput) (ports.Tenant, error) {
	return ports.Tenant{}, ports.ErrUnsupported
}
func (stubAdminTenantService) FreezeTenant(context.Context, string, string) (ports.Tenant, error) {
	return ports.Tenant{}, ports.ErrUnsupported
}
func (stubAdminTenantService) UnfreezeTenant(context.Context, string, string) (ports.Tenant, error) {
	return ports.Tenant{}, ports.ErrUnsupported
}
func (stubAdminTenantService) DisableTenant(context.Context, string, string) (ports.Tenant, error) {
	return ports.Tenant{}, ports.ErrUnsupported
}
func (stubAdminTenantService) GetTenantAuth(context.Context, string) (ports.TenantAuth, error) {
	return ports.TenantAuth{}, ports.ErrUnsupported
}
func (stubAdminTenantService) UpdateTenantAuth(context.Context, string, ports.TenantAuthPatch) (ports.TenantAuth, error) {
	return ports.TenantAuth{}, ports.ErrUnsupported
}
func (stubAdminTenantService) ListTenantLifecycle(context.Context, string, ports.TenantLifecycleFilter) (ports.TenantLifecycleListResult, error) {
	return ports.TenantLifecycleListResult{}, ports.ErrUnsupported
}

func newAdminTenantTestServer(tenant ports.TenantService) *server.Hertz {
	h := server.Default()
	v1 := h.Group("/api/v1")
	registerAdminTenantResources(v1, tenant)
	return h
}

func TestAdminTenantRoutes_RegisterNinePlusExisting(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	h := newAdminTenantTestServer(stubAdminTenantService{})
	tenantID := "11111111-1111-1111-1111-111111111111"
	paths := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/admin/tenant-admins/available-tenants"},
		{http.MethodGet, "/api/v1/admin/tenants/" + tenantID},
		{http.MethodPost, "/api/v1/admin/tenants"},
		{http.MethodGet, "/api/v1/admin/tenants"},
		{http.MethodPut, "/api/v1/admin/tenants/" + tenantID},
		{http.MethodPost, "/api/v1/admin/tenants/" + tenantID + "/freeze"},
		{http.MethodPost, "/api/v1/admin/tenants/" + tenantID + "/unfreeze"},
		{http.MethodPost, "/api/v1/admin/tenants/" + tenantID + "/disable"},
		{http.MethodGet, "/api/v1/admin/tenants/" + tenantID + "/auth"},
		{http.MethodPut, "/api/v1/admin/tenants/" + tenantID + "/auth"},
		{http.MethodGet, "/api/v1/admin/tenants/" + tenantID + "/lifecycle"},
	}
	// 既有 2 端点 + Issue-004 新增 9 端点 = 11
	if len(paths) != 11 {
		t.Fatalf("want 11 routes, got %d", len(paths))
	}
	for _, tc := range paths {
		resp := ut.PerformRequest(h.Engine, tc.method, tc.path, nil)
		if resp.Code == http.StatusNotFound {
			t.Fatalf("%s %s not registered (404)", tc.method, tc.path)
		}
	}
}

func TestWriteAdminTenantError_NameConflictAndStateInvalid(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	h := server.Default()
	h.GET("/err/name", func(_ context.Context, c *app.RequestContext) {
		writeAdminTenantError(c, ports.ErrTenantNameConflict)
	})
	h.GET("/err/state", func(_ context.Context, c *app.RequestContext) {
		writeAdminTenantError(c, ports.ErrTenantStateInvalid)
	})

	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/err/name", nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("name conflict status=%d", resp.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["code"] != "TENANT_NAME_CONFLICT" {
		t.Fatalf("code=%v", body["code"])
	}

	resp = ut.PerformRequest(h.Engine, http.MethodGet, "/err/state", nil)
	if resp.Code != http.StatusConflict {
		t.Fatalf("state invalid status=%d", resp.Code)
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["code"] != "TENANT_STATE_INVALID" {
		t.Fatalf("code=%v", body["code"])
	}
}

func TestToAdminTenantResponse_IncludesNullableFields(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	frozen := now.Add(-time.Hour)
	got := toAdminTenantResponse(ports.Tenant{
		ID: "t1", Name: "acme", DisplayName: "ACME", Status: "frozen",
		PlanID: "p1", ContactEmail: "a@acme.io", FrozenAt: &frozen,
		CreatedAt: now, UpdatedAt: now, AdminCount: 2,
	})
	if got["contact_email"] != "a@acme.io" {
		t.Fatalf("contact_email=%v", got["contact_email"])
	}
	if got["frozen_at"] == nil {
		t.Fatal("frozen_at missing")
	}
	if got["disabled_at"] != nil {
		t.Fatalf("disabled_at=%v", got["disabled_at"])
	}
}
