package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
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
func (stubAdminTenantService) FreezeTenant(context.Context, string) (ports.Tenant, error) {
	return ports.Tenant{}, ports.ErrUnsupported
}
func (stubAdminTenantService) UnfreezeTenant(context.Context, string) (ports.Tenant, error) {
	return ports.Tenant{}, ports.ErrUnsupported
}
func (stubAdminTenantService) DisableTenant(context.Context, string) (ports.Tenant, error) {
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
	return newAdminTenantTestServerWithInfra(tenant, nil, nil)
}

func newAdminTenantTestServerWithInfra(tenant ports.TenantService, registry ports.ImageRegistry, kubeApply runtimeadapter.TenantNamespaceApplier) *server.Hertz {
	h := server.Default()
	v1 := h.Group("/api/v1")
	registerAdminTenantResources(v1, tenant, registry, kubeApply)
	return h
}

// stubProvisionTenantService：GetTenant 成功（存在性校验通过），其余不支持。
type stubProvisionTenantService struct {
	stubAdminTenantService
}

func (stubProvisionTenantService) GetTenant(context.Context, string) (ports.Tenant, error) {
	return ports.Tenant{ID: "t1", Name: "acme", Status: ports.TenantStatusActive}, nil
}

type stubEnsureProjectRegistry struct {
	ports.ImageRegistry // 嵌入接口：测试仅覆盖 EnsureProject，其余方法不可达
	ensured             []string
	ensureErr           error
}

func (s *stubEnsureProjectRegistry) EnsureProject(_ context.Context, projectID string) error {
	if s.ensureErr != nil {
		return s.ensureErr
	}
	s.ensured = append(s.ensured, projectID)
	return nil
}

type stubTenantNamespaceApplier struct {
	applied []ports.WorkloadManifest
	err     error
}

func (s *stubTenantNamespaceApplier) ApplyManifests(_ context.Context, manifests []ports.WorkloadManifest) ([]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.applied = append(s.applied, manifests...)
	return make([]string, len(manifests)), nil
}

func TestAdminTenantProvision_RouteRegistered(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	h := newAdminTenantTestServerWithInfra(stubProvisionTenantService{}, nil, nil)
	resp := ut.PerformRequest(h.Engine, http.MethodPost,
		"/api/v1/admin/tenants/11111111-1111-1111-1111-111111111111/provision", nil)
	if resp.Code == http.StatusNotFound {
		t.Fatal("provision route not registered (404)")
	}
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAdminTenantProvision_EnsuresRegistryProjectAndNamespace(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	tenantID := "11111111-1111-1111-1111-111111111111"
	registry := &stubEnsureProjectRegistry{}
	kube := &stubTenantNamespaceApplier{}
	h := newAdminTenantTestServerWithInfra(stubProvisionTenantService{}, registry, kube)

	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/admin/tenants/"+tenantID+"/provision", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["tenant_id"] != tenantID {
		t.Fatalf("tenant_id=%v", body["tenant_id"])
	}
	if proj, ok := body["registry_project"].(map[string]any); !ok || proj["ensured"] != true {
		t.Fatalf("registry_project=%v", body["registry_project"])
	}
	if ns, ok := body["namespace"].(map[string]any); !ok || ns["ensured"] != true {
		t.Fatalf("namespace=%v", body["namespace"])
	}
	if len(registry.ensured) != 1 || registry.ensured[0] != tenantID {
		t.Fatalf("registry.ensured=%v", registry.ensured)
	}
	if len(kube.applied) != 1 || kube.applied[0].Kind != "Namespace" {
		t.Fatalf("kube.applied=%+v", kube.applied)
	}
}

func TestAdminTenantProvision_KubeNotConfiguredDegrades(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	tenantID := "11111111-1111-1111-1111-111111111111"
	registry := &stubEnsureProjectRegistry{}
	h := newAdminTenantTestServerWithInfra(stubProvisionTenantService{}, registry, nil)

	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/admin/tenants/"+tenantID+"/provision", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if ns, ok := body["namespace"].(map[string]any); !ok || ns["ensured"] != false {
		t.Fatalf("namespace must degrade to ensured=false, got %v", body["namespace"])
	}
	if ns, ok := body["namespace"].(map[string]any); !ok || ns["reason"] == nil {
		t.Fatalf("namespace.reason=%v", body["namespace"])
	}
	if proj, ok := body["registry_project"].(map[string]any); !ok || proj["ensured"] != true {
		t.Fatalf("registry_project=%v", body["registry_project"])
	}
}

func TestAdminTenantProvision_InvalidUUIDRejected(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	h := newAdminTenantTestServerWithInfra(stubProvisionTenantService{}, nil, nil)
	resp := ut.PerformRequest(h.Engine, http.MethodPost,
		"/api/v1/admin/tenants/not-a-uuid/provision", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func TestAdminTenantProvision_TenantNotFound(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	registry := &stubEnsureProjectRegistry{}
	kube := &stubTenantNamespaceApplier{}
	h := server.Default()
	v1 := h.Group("/api/v1")
	// GetTenant 返回 ErrTenantNotFound → 404 TENANT_NOT_FOUND
	registerAdminTenantResources(v1, notFoundTenantService{}, registry, kube)
	resp := ut.PerformRequest(h.Engine, http.MethodPost,
		"/api/v1/admin/tenants/11111111-1111-1111-1111-111111111111/provision", nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	if len(registry.ensured) != 0 || len(kube.applied) != 0 {
		t.Fatalf("must not provision infra for missing tenant: registry=%v kube=%+v",
			registry.ensured, kube.applied)
	}
}

// notFoundTenantService：GetTenant 返回 ErrTenantNotFound（其余不支持）。
type notFoundTenantService struct {
	stubAdminTenantService
}

func (notFoundTenantService) GetTenant(context.Context, string) (ports.Tenant, error) {
	return ports.Tenant{}, ports.ErrTenantNotFound
}

func TestAdminTenantProvision_RegistryFailureReturns500(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	registry := &stubEnsureProjectRegistry{ensureErr: errors.New("harbor unreachable")}
	kube := &stubTenantNamespaceApplier{}
	h := newAdminTenantTestServerWithInfra(stubProvisionTenantService{}, registry, kube)
	resp := ut.PerformRequest(h.Engine, http.MethodPost,
		"/api/v1/admin/tenants/11111111-1111-1111-1111-111111111111/provision", nil)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["code"] != "INFRA_PROVISION_FAILED" {
		t.Fatalf("code=%v", body["code"])
	}
	if len(kube.applied) != 0 {
		t.Fatalf("namespace must not be ensured after registry failure")
	}
}

func TestAdminTenantProvision_NamespaceFailureReturns500(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	registry := &stubEnsureProjectRegistry{}
	kube := &stubTenantNamespaceApplier{err: errors.New("k8s api error")}
	h := newAdminTenantTestServerWithInfra(stubProvisionTenantService{}, registry, kube)
	resp := ut.PerformRequest(h.Engine, http.MethodPost,
		"/api/v1/admin/tenants/11111111-1111-1111-1111-111111111111/provision", nil)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["code"] != "INFRA_PROVISION_FAILED" {
		t.Fatalf("code=%v", body["code"])
	}
	if len(registry.ensured) != 1 {
		t.Fatalf("registry project must still be ensured (idempotent replay covers namespace)")
	}
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
		ID: "t1", Name: "acme", DisplayName: "ACME", Status: ports.TenantStatusFrozen,
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
	auth, ok := got["auth"].(map[string]any)
	if !ok {
		t.Fatalf("auth=%T", got["auth"])
	}
	if auth["sso_enabled"] != false || auth["mfa_required"] != false {
		t.Fatalf("auth defaults=%v", auth)
	}
}

func TestAdminActorUserID_PrefersTrustedHeader(t *testing.T) {
	c := app.NewContext(0)
	c.Request.Header.Set("X-ANI-Actor-User-ID", "cccccccc-cccc-cccc-cccc-cccccccccccc")
	c.Set("user_id", "dddddddd-dddd-dddd-dddd-dddddddddddd")
	if got := adminActorUserID(c); got != "cccccccc-cccc-cccc-cccc-cccccccccccc" {
		t.Fatalf("got=%q", got)
	}
}

func TestAdminActorUserID_FallsBackToAuthUser(t *testing.T) {
	c := app.NewContext(0)
	c.Set("user_id", "dddddddd-dddd-dddd-dddd-dddddddddddd")
	if got := adminActorUserID(c); got != "dddddddd-dddd-dddd-dddd-dddddddddddd" {
		t.Fatalf("got=%q", got)
	}
}
