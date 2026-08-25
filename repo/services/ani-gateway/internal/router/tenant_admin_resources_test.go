package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type fakeTenantAdminGRPC struct {
	lastListReq   *tenantv1.ListAllTenantAdminsRequest
	listResp      *tenantv1.ListAllTenantAdminsResponse
	listErr       error
	detailResp    *tenantv1.AdminWithTenant
	detailErr     error
	inviteErr     error
	changeRoleErr error
	roleResp      *tenantv1.UserPermissions
	roleErr       error
}

func (f *fakeTenantAdminGRPC) InviteTenantAdmin(_ context.Context, req *tenantv1.InviteTenantAdminRequest, _ ...grpc.CallOption) (*tenantv1.InvitationResult, error) {
	if f.inviteErr != nil {
		return nil, f.inviteErr
	}
	return &tenantv1.InvitationResult{Id: "inv-1", Token: "tok", Message: "sent"}, nil
}
func (f *fakeTenantAdminGRPC) ResendTenantAdminInvitation(context.Context, *tenantv1.ResendTenantAdminInvitationRequest, ...grpc.CallOption) (*tenantv1.InvitationResult, error) {
	return nil, status.Error(codes.Unimplemented, "not used")
}
func (f *fakeTenantAdminGRPC) ListAllTenantAdmins(_ context.Context, req *tenantv1.ListAllTenantAdminsRequest, _ ...grpc.CallOption) (*tenantv1.ListAllTenantAdminsResponse, error) {
	f.lastListReq = req
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listResp, nil
}
func (f *fakeTenantAdminGRPC) GetTenantAdminDetail(context.Context, *tenantv1.GetTenantAdminDetailRequest, ...grpc.CallOption) (*tenantv1.AdminWithTenant, error) {
	if f.detailErr != nil {
		return nil, f.detailErr
	}
	return f.detailResp, nil
}
func (f *fakeTenantAdminGRPC) UpdateTenantAdminRole(_ context.Context, _ *tenantv1.UpdateTenantAdminRoleRequest, _ ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	if f.changeRoleErr != nil {
		return nil, f.changeRoleErr
	}
	return &commonv1.IdempotentResult{Id: "u1", Message: "role updated"}, nil
}
func (f *fakeTenantAdminGRPC) GetTenantAdminRole(_ context.Context, _ *tenantv1.GetTenantAdminRoleRequest, _ ...grpc.CallOption) (*tenantv1.UserPermissions, error) {
	if f.roleErr != nil {
		return nil, f.roleErr
	}
	return f.roleResp, nil
}
func (f *fakeTenantAdminGRPC) GetChangeableRoles(context.Context, *tenantv1.GetChangeableRolesRequest, ...grpc.CallOption) (*tenantv1.GetChangeableRolesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not used")
}
func (f *fakeTenantAdminGRPC) ResetTenantAdminPassword(context.Context, *tenantv1.ResetTenantAdminPasswordRequest, ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	return nil, status.Error(codes.Unimplemented, "not used")
}
func (f *fakeTenantAdminGRPC) DisableTenantAdmin(context.Context, *tenantv1.DisableTenantAdminRequest, ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	return nil, status.Error(codes.Unimplemented, "not used")
}
func (f *fakeTenantAdminGRPC) EnableTenantAdmin(context.Context, *tenantv1.EnableTenantAdminRequest, ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	return nil, status.Error(codes.Unimplemented, "not used")
}
func (f *fakeTenantAdminGRPC) DeleteTenantAdmin(context.Context, *tenantv1.DeleteTenantAdminRequest, ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	return nil, status.Error(codes.Unimplemented, "not used")
}
func (f *fakeTenantAdminGRPC) ListTenantAdminAuditLogs(context.Context, *tenantv1.ListTenantAdminAuditLogsRequest, ...grpc.CallOption) (*tenantv1.ListTenantAdminAuditLogsResponse, error) {
	return &tenantv1.ListTenantAdminAuditLogsResponse{}, nil
}
func (f *fakeTenantAdminGRPC) ListAvailableTenants(context.Context, *tenantv1.ListAvailableTenantsRequest, ...grpc.CallOption) (*tenantv1.ListAvailableTenantsResponse, error) {
	return &tenantv1.ListAvailableTenantsResponse{}, nil
}

func newTenantAdminTestServer(grpcClient tenantv1.TenantAdminServiceClient) *server.Hertz {
	h := server.Default()
	svc := h.Group("/api/v1/svc")
	registerTenantAdminsWithClient(svc, grpcClient)
	return h
}

func TestTenantAdminRoutes_NilGRPCClient(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	h := newTenantAdminTestServer(nil)
	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/svc/tenant-admins", nil)
	if resp.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.Code)
	}
}

func TestTenantAdminRoutes_ListForwardsToGRPC(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	client := &fakeTenantAdminGRPC{
		listResp: &tenantv1.ListAllTenantAdminsResponse{
			Items: []*tenantv1.AdminWithTenant{
				{
					Id: "u1", Email: "a@acme.io", Username: "acme_admin",
					Role: "tenant-admin", Status: "active",
					IsInviting: false, IsExpired: false, Source: "local",
					Tenant: &tenantv1.TenantAdminTenantRef{Id: "t1", Name: "acme", DisplayName: "ACME"},
				},
			},
			NextCursor: "next-1",
		},
	}
	h := newTenantAdminTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/svc/tenant-admins?limit=10&tenant_id=t1&search=acme", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	if client.lastListReq == nil || client.lastListReq.GetTenantId() != "t1" {
		t.Fatalf("tenant_id not forwarded: %+v", client.lastListReq)
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %#v", body["items"])
	}
}

func TestTenantAdminRoutes_GRPCBusinessCodeMapping(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	client := &fakeTenantAdminGRPC{
		inviteErr: status.Error(codes.AlreadyExists, "TENANT_ADMIN_ALREADY_ADMIN: user is already admin"),
	}
	h := newTenantAdminTestServer(client)
	body := `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440000","email":"a@acme.io","username":"acme_admin"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/svc/tenants/t1/admins/invite",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)})
	if resp.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 body=%s", resp.Code, resp.Body.String())
	}
	var errBody map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("json: %v", err)
	}
	if errBody["code"] != "TENANT_ADMIN_ALREADY_ADMIN" {
		t.Fatalf("code = %v", errBody["code"])
	}
}

func TestTenantAdminRoutes_UpdateRoleGRPCErrorMapping(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	client := &fakeTenantAdminGRPC{
		changeRoleErr: status.Error(codes.NotFound, "TENANT_ADMIN_NOT_FOUND: user not found"),
	}
	h := newTenantAdminTestServer(client)
	body := `{"role":"user","idempotency_key":"550e8400-e29b-41d4-a716-446655440000"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPut, "/api/v1/svc/tenants/t1/admins/u1/role",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)})
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 body=%s", resp.Code, resp.Body.String())
	}
}

func TestTenantAdminRoutes_GetRolePermissions(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	permStruct, _ := structpb.NewStruct(map[string]any{
		"resource": "compute",
		"action":   "write",
		"scope":    "tenant",
	})
	client := &fakeTenantAdminGRPC{
		roleResp: &tenantv1.UserPermissions{
			UserId:   "u1",
			TenantId: wrapperspb.String("t1"),
			Role:     "tenant-admin",
			Permissions: &structpb.ListValue{
				Values: []*structpb.Value{structpb.NewStructValue(permStruct)},
			},
		},
	}
	h := newTenantAdminTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/svc/tenants/t1/admins/u1/role", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestTenantAdminRoutes_DetailIncludesTimestamps(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	now := timestamppb.New(time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC))
	client := &fakeTenantAdminGRPC{
		detailResp: &tenantv1.AdminWithTenant{
			Id: "u1", Email: "a@acme.io", Username: "acme_admin",
			Role: "tenant-admin", Status: "active", Source: "local",
			CreatedAt: now, UpdatedAt: now,
			Tenant: &tenantv1.TenantAdminTenantRef{Id: "t1", Name: "acme", DisplayName: "ACME"},
		},
	}
	h := newTenantAdminTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/svc/tenants/t1/admins/u1", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["created_at"] == nil || body["updated_at"] == nil {
		t.Fatalf("detail timestamps missing: %#v", body)
	}
}
