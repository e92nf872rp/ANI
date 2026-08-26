package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
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
	lastListReq        *tenantv1.ListAllTenantAdminsRequest
	listResp           *tenantv1.ListAllTenantAdminsResponse
	listErr            error
	detailResp         *tenantv1.AdminWithTenant
	detailErr          error
	inviteErr          error
	inviteResp         *tenantv1.InvitationResult
	lastInviteReq      *tenantv1.InviteTenantAdminRequest
	inviteCalls        int
	resendErr          error
	resendResp         *tenantv1.InvitationResult
	lastResendReq      *tenantv1.ResendTenantAdminInvitationRequest
	resendCalls        int
	lastResendToken    string
	invitationSettled  bool
	changeRoleErr      error
	roleResp           *tenantv1.UserPermissions
	roleErr            error
	availableResp      *tenantv1.ListAvailableTenantsResponse
	availableErr       error
	listAvailableCalls int
	invitedUserID      string
	lastInviteToken    string
}

func (f *fakeTenantAdminGRPC) InviteTenantAdmin(_ context.Context, req *tenantv1.InviteTenantAdminRequest, _ ...grpc.CallOption) (*tenantv1.InvitationResult, error) {
	f.inviteCalls++
	f.lastInviteReq = req
	if f.inviteErr != nil {
		return nil, f.inviteErr
	}
	if f.inviteResp != nil {
		f.lastInviteToken = f.inviteResp.GetToken()
		return f.inviteResp, nil
	}
	token := "tok-once-only"
	f.lastInviteToken = token
	if f.invitedUserID == "" {
		f.invitedUserID = "u-invited"
	}
	return &tenantv1.InvitationResult{
		Id:       "inv-1",
		Token:    token,
		ExpireAt: timestamppb.New(time.Now().UTC().Add(72 * time.Hour)),
		Message:  "invitation sent",
	}, nil
}
func (f *fakeTenantAdminGRPC) ResendTenantAdminInvitation(_ context.Context, req *tenantv1.ResendTenantAdminInvitationRequest, _ ...grpc.CallOption) (*tenantv1.InvitationResult, error) {
	f.resendCalls++
	f.lastResendReq = req
	if f.resendErr != nil {
		return nil, f.resendErr
	}
	if f.invitationSettled {
		return nil, status.Error(codes.FailedPrecondition, "TENANT_INVITATION_SETTLED: invitation already settled")
	}
	if f.resendResp != nil {
		f.lastResendToken = f.resendResp.GetToken()
		return f.resendResp, nil
	}
	token := "tok-resend-once"
	f.lastResendToken = token
	return &tenantv1.InvitationResult{
		Id:       "inv-1",
		Token:    token,
		ExpireAt: timestamppb.New(time.Now().UTC().Add(72 * time.Hour)),
		Message:  "invitation resent",
	}, nil
}
func (f *fakeTenantAdminGRPC) ListAllTenantAdmins(_ context.Context, req *tenantv1.ListAllTenantAdminsRequest, _ ...grpc.CallOption) (*tenantv1.ListAllTenantAdminsResponse, error) {
	f.lastListReq = req
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listResp != nil {
		return f.listResp, nil
	}
	// After invite: surface the invited user with is_inviting=true; token never appears in list.
	if f.inviteCalls > 0 && f.invitedUserID != "" {
		return &tenantv1.ListAllTenantAdminsResponse{
			Items: []*tenantv1.AdminWithTenant{
				{
					Id: f.invitedUserID, Email: "a@acme.io", Username: "acme_admin",
					Role: "user", Status: "active", IsInviting: true, Source: "local",
					Tenant: &tenantv1.TenantAdminTenantRef{Id: "t1", Name: "acme", DisplayName: "ACME"},
				},
			},
		}, nil
	}
	return &tenantv1.ListAllTenantAdminsResponse{}, nil
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
	f.listAvailableCalls++
	if f.availableErr != nil {
		return nil, f.availableErr
	}
	if f.availableResp != nil {
		return f.availableResp, nil
	}
	return &tenantv1.ListAvailableTenantsResponse{}, nil
}

func newTenantAdminTestServer(grpcClient tenantv1.TenantAdminServiceClient) *server.Hertz {
	h := server.Default()
	svc := h.Group("/api/v1/svc")
	registerTenantAdminsWithClient(svc, grpcClient)
	return h
}

func TestHandler_InviteFlow(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	client := &fakeTenantAdminGRPC{invitedUserID: "u-invited"}
	h := newTenantAdminTestServer(client)

	body := `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440000","email":"a@acme.io","username":"acme_admin"}`
	inviteResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/svc/tenants/t1/admins/invite",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)})
	if inviteResp.Code != http.StatusOK {
		t.Fatalf("invite status=%d body=%s", inviteResp.Code, inviteResp.Body.String())
	}
	var inviteBody map[string]any
	if err := json.Unmarshal(inviteResp.Body.Bytes(), &inviteBody); err != nil {
		t.Fatalf("invite json: %v", err)
	}
	token, _ := inviteBody["token"].(string)
	if token == "" {
		t.Fatalf("token missing: %#v", inviteBody)
	}
	if inviteBody["expire_at"] == nil || inviteBody["id"] == nil {
		t.Fatalf("incomplete invite: %#v", inviteBody)
	}
	if client.inviteCalls != 1 || client.lastInviteToken != token {
		t.Fatalf("inviteCalls=%d lastToken=%q", client.inviteCalls, client.lastInviteToken)
	}

	listResp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/svc/tenant-admins?tenant_id=t1", nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listResp.Code, listResp.Body.String())
	}
	var listBody map[string]any
	if err := json.Unmarshal(listResp.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("list json: %v", err)
	}
	items, _ := listBody["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items=%#v", listBody["items"])
	}
	first, _ := items[0].(map[string]any)
	if first["is_inviting"] != true {
		t.Fatalf("is_inviting=%v want true", first["is_inviting"])
	}
	if _, hasToken := first["token"]; hasToken {
		t.Fatalf("token must not appear in list: %#v", first)
	}
	// token is one-shot: list must not echo the invite token value in any field
	rawList := listResp.Body.String()
	if strings.Contains(rawList, token) {
		t.Fatalf("invite token leaked into list response")
	}
}

func TestHandler_ResendFlow(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	client := &fakeTenantAdminGRPC{}
	h := newTenantAdminTestServer(client)

	body := `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440020"}`
	resendResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/svc/tenants/t1/admins/u1/invitation/resend",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)})
	if resendResp.Code != http.StatusOK {
		t.Fatalf("resend status=%d body=%s", resendResp.Code, resendResp.Body.String())
	}
	var resendBody map[string]any
	if err := json.Unmarshal(resendResp.Body.Bytes(), &resendBody); err != nil {
		t.Fatalf("resend json: %v", err)
	}
	token, _ := resendBody["token"].(string)
	if token == "" || resendBody["expire_at"] == nil || resendBody["id"] == nil {
		t.Fatalf("incomplete resend: %#v", resendBody)
	}
	if resendBody["message"] != "invitation resent" {
		t.Fatalf("message=%v", resendBody["message"])
	}
	if client.resendCalls != 1 || client.lastResendToken != token {
		t.Fatalf("resendCalls=%d lastToken=%q", client.resendCalls, client.lastResendToken)
	}
	if client.lastResendReq == nil || client.lastResendReq.GetTenantId() != "t1" || client.lastResendReq.GetUserId() != "u1" {
		t.Fatalf("path not forwarded: %+v", client.lastResendReq)
	}

	// settling: accepted/rejected → 409 TENANT_INVITATION_SETTLED
	client.invitationSettled = true
	settledBody := `{"idempotency_key":"550e8400-e29b-41d4-a716-446655440021"}`
	settledResp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/svc/tenants/t1/admins/u1/invitation/resend",
		&ut.Body{Body: bytes.NewBufferString(settledBody), Len: len(settledBody)})
	if settledResp.Code != http.StatusConflict {
		t.Fatalf("settled status=%d want 409 body=%s", settledResp.Code, settledResp.Body.String())
	}
	var errBody map[string]any
	if err := json.Unmarshal(settledResp.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("settled json: %v", err)
	}
	if errBody["code"] != "TENANT_INVITATION_SETTLED" {
		t.Fatalf("code=%v", errBody["code"])
	}
}

func TestHandler_ListAvailableTenants(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	client := &fakeTenantAdminGRPC{
		availableResp: &tenantv1.ListAvailableTenantsResponse{
			Items: []*tenantv1.AvailableTenant{
				{Id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Name: "acme", DisplayName: "Acme", Status: "active"},
				{Id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Name: "beta", DisplayName: "Beta", Status: "frozen"},
			},
		},
	}
	h := newTenantAdminTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/svc/tenant-admins/tenants", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	if client.listAvailableCalls != 1 {
		t.Fatalf("grpc calls=%d", client.listAvailableCalls)
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items=%#v", body["items"])
	}
	first, _ := items[0].(map[string]any)
	for _, key := range []string{"id", "name", "display_name", "status"} {
		if first[key] == nil || first[key] == "" {
			t.Fatalf("missing %s in %#v", key, first)
		}
	}
	if first["status"] == "disabled" {
		t.Fatalf("disabled leaked: %#v", first)
	}
	// ordering preserved from gRPC (created_at DESC at Core)
	if first["name"] != "acme" {
		t.Fatalf("order/name=%v", first["name"])
	}
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

func TestTenantAdminRoutes_ListFilterMutualExclusion(t *testing.T) {
	t.Setenv("ANI_AUTH_MODE", "dev")
	client := &fakeTenantAdminGRPC{
		listErr: status.Error(codes.InvalidArgument, "VALIDATION_FAILED: status, is_inviting, and is_expired are mutually exclusive"),
	}
	h := newTenantAdminTestServer(client)
	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/svc/tenant-admins?status=active&is_inviting=true", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", resp.Code, resp.Body.String())
	}
	var errBody map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("json: %v", err)
	}
	if errBody["code"] != "VALIDATION_FAILED" {
		t.Fatalf("code=%v", errBody["code"])
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
