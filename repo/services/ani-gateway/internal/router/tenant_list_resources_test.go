package router

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeTenantListGRPC struct {
	tenantv1.TenantServiceClient
	detailResp *tenantv1.TenantDetail
	detailErr  error
}

func (f *fakeTenantListGRPC) ListAvailablePlans(context.Context, *tenantv1.ListAvailablePlansRequest, ...grpc.CallOption) (*tenantv1.ListAvailablePlansResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) CreateTenant(context.Context, *tenantv1.CreateTenantRequest, ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) ListTenants(context.Context, *tenantv1.ListTenantsRequest, ...grpc.CallOption) (*tenantv1.ListTenantsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) GetTenantDetail(_ context.Context, _ *tenantv1.GetTenantDetailRequest, _ ...grpc.CallOption) (*tenantv1.TenantDetail, error) {
	if f.detailErr != nil {
		return nil, f.detailErr
	}
	if f.detailResp != nil {
		return f.detailResp, nil
	}
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) UpdateTenant(context.Context, *tenantv1.UpdateTenantRequest, ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) FreezeTenant(context.Context, *tenantv1.FreezeTenantRequest, ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) UnfreezeTenant(context.Context, *tenantv1.UnfreezeTenantRequest, ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) DisableTenant(context.Context, *tenantv1.DisableTenantRequest, ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) GetTenantAuth(context.Context, *tenantv1.GetTenantAuthRequest, ...grpc.CallOption) (*tenantv1.TenantAuthConfig, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) UpdateTenantSso(context.Context, *tenantv1.UpdateTenantSsoRequest, ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) TestTenantSso(context.Context, *tenantv1.TestTenantSsoRequest, ...grpc.CallOption) (*tenantv1.SsoTestResult, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) UpdateTenantMfa(context.Context, *tenantv1.UpdateTenantMfaRequest, ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) GetTenantQuota(context.Context, *tenantv1.GetTenantQuotaRequest, ...grpc.CallOption) (*tenantv1.GetTenantQuotaResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) SubmitQuotaChangeRequest(context.Context, *tenantv1.SubmitQuotaChangeRequestRequest, ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) ListQuotaChangeRequests(context.Context, *tenantv1.ListQuotaChangeRequestsRequest, ...grpc.CallOption) (*tenantv1.ListQuotaChangeRequestsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) ReviewQuotaChangeRequest(context.Context, *tenantv1.ReviewQuotaChangeRequestRequest, ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) ListTenantLifecycle(context.Context, *tenantv1.ListTenantLifecycleRequest, ...grpc.CallOption) (*tenantv1.ListTenantLifecycleResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) ListTenantAuditLogs(context.Context, *tenantv1.ListTenantAuditLogsRequest, ...grpc.CallOption) (*tenantv1.ListTenantAuditLogsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) ListTenantAdmins(context.Context, *tenantv1.ListTenantAdminsRequest, ...grpc.CallOption) (*tenantv1.ListTenantAdminsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}
func (f *fakeTenantListGRPC) BindPlanQuota(context.Context, *tenantv1.BindPlanQuotaRequest, ...grpc.CallOption) (*tenantv1.IdempotentResult, error) {
	return nil, status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")
}

func newTenantListTestServer(client tenantv1.TenantServiceClient) *server.Hertz {
	h := server.Default()
	svc := h.Group("/api/v1/svc")
	registerTenantListWithClient(svc, client)
	return h
}

func TestTenantListRoutes_RegisterNineteen(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	h := newTenantListTestServer(&fakeTenantListGRPC{})
	tenantID := "11111111-1111-1111-1111-111111111111"
	reqID := "22222222-2222-2222-2222-222222222222"
	paths := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/svc/tenants/available-plans"},
		{http.MethodGet, "/api/v1/svc/tenants"},
		{http.MethodPost, "/api/v1/svc/tenants"},
		{http.MethodGet, "/api/v1/svc/tenants/" + tenantID},
		{http.MethodPut, "/api/v1/svc/tenants/" + tenantID},
		{http.MethodPost, "/api/v1/svc/tenants/" + tenantID + "/freeze"},
		{http.MethodPost, "/api/v1/svc/tenants/" + tenantID + "/unfreeze"},
		{http.MethodPost, "/api/v1/svc/tenants/" + tenantID + "/disable"},
		{http.MethodGet, "/api/v1/svc/tenants/" + tenantID + "/auth/sso"},
		{http.MethodPut, "/api/v1/svc/tenants/" + tenantID + "/auth/sso"},
		{http.MethodPost, "/api/v1/svc/tenants/" + tenantID + "/auth/sso/test"},
		{http.MethodPut, "/api/v1/svc/tenants/" + tenantID + "/auth/mfa"},
		{http.MethodGet, "/api/v1/svc/tenants/" + tenantID + "/quota"},
		{http.MethodGet, "/api/v1/svc/tenants/" + tenantID + "/quota-requests"},
		{http.MethodPost, "/api/v1/svc/tenants/" + tenantID + "/quota-requests"},
		{http.MethodPost, "/api/v1/svc/tenants/" + tenantID + "/quota-requests/" + reqID + "/approve"},
		{http.MethodGet, "/api/v1/svc/tenants/" + tenantID + "/lifecycle"},
		{http.MethodGet, "/api/v1/svc/tenants/" + tenantID + "/audit-logs"},
		{http.MethodGet, "/api/v1/svc/tenants/" + tenantID + "/admins"},
	}
	if len(paths) != 19 {
		t.Fatalf("want 19 routes, got %d", len(paths))
	}
	for _, tc := range paths {
		resp := ut.PerformRequest(h.Engine, tc.method, tc.path, nil)
		if resp.Code == http.StatusNotFound {
			t.Fatalf("%s %s not registered (404)", tc.method, tc.path)
		}
	}
}

func TestTenantListRoutes_GetTenantDetailForwards(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	now := timestamppb.Now()
	client := &fakeTenantListGRPC{
		detailResp: &tenantv1.TenantDetail{
			Id: "t1", Name: "acme", DisplayName: "ACME", PlanId: "p1", Status: "active",
			CreatedAt: now, UpdatedAt: now,
		},
	}
	h := newTenantListTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/svc/tenants/t1", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["id"] != "t1" || body["name"] != "acme" {
		t.Fatalf("body=%#v", body)
	}
}

func TestMapTenantListError_AllBusinessCodes(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	cases := []struct {
		msg    string
		status int
		code   string
	}{
		{"VALIDATION_FAILED: bad", http.StatusBadRequest, "VALIDATION_FAILED"},
		{"TENANT_NOT_FOUND: gone", http.StatusNotFound, "TENANT_NOT_FOUND"},
		{"TENANT_NAME_CONFLICT: taken", http.StatusConflict, "TENANT_NAME_CONFLICT"},
		{"TENANT_STATE_INVALID: bad state", http.StatusConflict, "TENANT_STATE_INVALID"},
		{"TENANT_HAS_RUNNING_RESOURCES: used>0", http.StatusConflict, "TENANT_HAS_RUNNING_RESOURCES"},
		{"PLAN_NOT_ACTIVE: draft", http.StatusUnprocessableEntity, "PLAN_NOT_ACTIVE"},
		{"TENANT_SSO_CONFIG_INVALID: missing provider", http.StatusUnprocessableEntity, "TENANT_SSO_CONFIG_INVALID"},
		{"QUOTA_CHANGE_REQUEST_INVALID: items", http.StatusUnprocessableEntity, "QUOTA_CHANGE_REQUEST_INVALID"},
		{"QUOTA_CHANGE_REQUEST_NOT_PENDING: approved", http.StatusConflict, "QUOTA_CHANGE_REQUEST_NOT_PENDING"},
		{"QUOTA_CHANGE_REQUEST_NOT_FOUND: missing", http.StatusNotFound, "QUOTA_CHANGE_REQUEST_NOT_FOUND"},
		{"NOT_IMPLEMENTED", http.StatusNotImplemented, "NOT_IMPLEMENTED"},
		{"GRPC_CLIENT_UNAVAILABLE: down", http.StatusBadGateway, "GRPC_CLIENT_UNAVAILABLE"},
		{"STORE_UNAVAILABLE: db", http.StatusBadGateway, "STORE_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			h := server.Default()
			h.GET("/err", func(_ context.Context, c *app.RequestContext) {
				mapTenantListError(c, status.Error(codes.Unknown, tc.msg))
			})
			resp := ut.PerformRequest(h.Engine, http.MethodGet, "/err", nil)
			if resp.Code != tc.status {
				t.Fatalf("status=%d want %d body=%s", resp.Code, tc.status, resp.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
				t.Fatalf("json: %v", err)
			}
			if body["code"] != tc.code {
				t.Fatalf("code=%v want %s", body["code"], tc.code)
			}
		})
	}
}

func TestTenantListRoutes_NilGRPCClient(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	h := newTenantListTestServer(nil)
	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/svc/tenants/t1", nil)
	if resp.Code != http.StatusBadGateway {
		t.Fatalf("status=%d want 502", resp.Code)
	}
}
