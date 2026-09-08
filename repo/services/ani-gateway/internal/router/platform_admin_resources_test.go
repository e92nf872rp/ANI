package router

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/google/uuid"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	platformsettingsv1 "github.com/kubercloud/ani/pkg/generated/pb/platform_settings/v1"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type fakePlatformAdminClient struct {
	lastList      *platformsettingsv1.ListPlatformAdminsRequest
	lastGetID     string
	lastPermID    string
	lastUpdate    *platformsettingsv1.UpdatePlatformAdminRoleRequest
	lastCreate    *platformsettingsv1.CreatePlatformAdminRequest
	lastDisable   *platformsettingsv1.DisablePlatformAdminRequest
	lastEnable    *platformsettingsv1.EnablePlatformAdminRequest
	lastDelete    *platformsettingsv1.DeletePlatformAdminRequest
	lastReset     *platformsettingsv1.ResetPlatformAdminPasswordRequest
	lastListAudit *platformsettingsv1.ListPlatformAdminAuditLogsRequest
	listResp      *platformsettingsv1.ListPlatformAdminsResponse
	rolesResp     *platformsettingsv1.ListPlatformAdminRolesResponse
	getResp       *platformsettingsv1.PlatformAdminDetail
	permsResp     *platformsettingsv1.PlatformAdminPermissions
	err           error

	// stateful flow helpers（DisableEnableFlow / DeleteFlow / LastAdminProtection / ResetPasswordFlow / AuditLogsFlow）
	status          string
	deleted         bool
	lastAdminErr    bool
	currentPassword string
	auditLogs       []*platformsettingsv1.PlatformAdminAuditLog
	auditSeq        int
	auditOperatorID string
}

func (f *fakePlatformAdminClient) appendAudit(userID, action, result string) {
	f.auditSeq++
	details, _ := structpb.NewStruct(map[string]any{"target_id": userID})
	log := &platformsettingsv1.PlatformAdminAuditLog{
		Id:        uuid.New().String(),
		Action:    action,
		Resource:  "platform_user",
		Result:    result,
		Details:   details,
		CreatedAt: timestamppb.New(time.Date(2026, 9, 2, 12, 0, f.auditSeq, 0, time.UTC)),
	}
	if op := strings.TrimSpace(f.auditOperatorID); op != "" {
		log.UserId = wrapperspb.String(op)
	}
	f.auditLogs = append(f.auditLogs, log)
}

func (f *fakePlatformAdminClient) CreatePlatformAdmin(_ context.Context, in *platformsettingsv1.CreatePlatformAdminRequest, _ ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	f.lastCreate = in
	if f.err != nil {
		return nil, f.err
	}
	id := "11111111-1111-1111-1111-111111111111"
	f.appendAudit(id, "platform_admin.create", "success")
	return &commonv1.IdempotentResult{Id: id, Message: "platform admin created"}, nil
}
func (f *fakePlatformAdminClient) ListPlatformAdmins(_ context.Context, in *platformsettingsv1.ListPlatformAdminsRequest, _ ...grpc.CallOption) (*platformsettingsv1.ListPlatformAdminsResponse, error) {
	f.lastList = in
	if f.err != nil {
		return nil, f.err
	}
	return f.listResp, nil
}
func (f *fakePlatformAdminClient) ListPlatformAdminRoles(context.Context, *platformsettingsv1.ListPlatformAdminRolesRequest, ...grpc.CallOption) (*platformsettingsv1.ListPlatformAdminRolesResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.rolesResp != nil {
		return f.rolesResp, nil
	}
	return &platformsettingsv1.ListPlatformAdminRolesResponse{}, nil
}
func (f *fakePlatformAdminClient) GetPlatformAdmin(_ context.Context, in *platformsettingsv1.GetPlatformAdminRequest, _ ...grpc.CallOption) (*platformsettingsv1.PlatformAdminDetail, error) {
	f.lastGetID = in.GetUserId()
	if f.err != nil {
		return nil, f.err
	}
	if f.deleted {
		return nil, status.Error(codes.NotFound, "PLATFORM_USER_NOT_FOUND")
	}
	if f.getResp != nil {
		out := proto.Clone(f.getResp).(*platformsettingsv1.PlatformAdminDetail)
		if f.status != "" {
			out.Status = f.status
		}
		return out, nil
	}
	st := f.status
	if st == "" {
		st = "active"
	}
	return &platformsettingsv1.PlatformAdminDetail{Id: in.GetUserId(), Username: "ops", Status: st}, nil
}
func (f *fakePlatformAdminClient) GetPlatformAdminPermissions(_ context.Context, in *platformsettingsv1.GetPlatformAdminPermissionsRequest, _ ...grpc.CallOption) (*platformsettingsv1.PlatformAdminPermissions, error) {
	f.lastPermID = in.GetUserId()
	if f.err != nil {
		return nil, f.err
	}
	if f.permsResp != nil {
		return f.permsResp, nil
	}
	return &platformsettingsv1.PlatformAdminPermissions{UserId: in.GetUserId(), Role: "platform-ops"}, nil
}
func (f *fakePlatformAdminClient) UpdatePlatformAdminRole(_ context.Context, in *platformsettingsv1.UpdatePlatformAdminRoleRequest, _ ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	f.lastUpdate = in
	if f.err != nil {
		return nil, f.err
	}
	f.appendAudit(in.GetUserId(), "platform_admin.change_role", "success")
	return &commonv1.IdempotentResult{Id: in.GetUserId(), Message: "platform admin role updated"}, nil
}
func (f *fakePlatformAdminClient) ResetPlatformAdminPassword(_ context.Context, in *platformsettingsv1.ResetPlatformAdminPasswordRequest, _ ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	f.lastReset = in
	if f.err != nil {
		return nil, f.err
	}
	f.currentPassword = in.GetNewPassword()
	f.appendAudit(in.GetUserId(), "platform_admin.reset_password", "success")
	return &commonv1.IdempotentResult{Id: in.GetUserId(), Message: "platform admin password reset"}, nil
}

func (f *fakePlatformAdminClient) verifyPassword(password string) bool {
	return f.currentPassword == password
}
func (f *fakePlatformAdminClient) DisablePlatformAdmin(_ context.Context, in *platformsettingsv1.DisablePlatformAdminRequest, _ ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	f.lastDisable = in
	if f.lastAdminErr {
		return nil, status.Error(codes.FailedPrecondition, "LAST_PLATFORM_ADMIN")
	}
	if f.err != nil {
		return nil, f.err
	}
	f.status = "disabled"
	f.appendAudit(in.GetUserId(), "platform_admin.disable", "success")
	return &commonv1.IdempotentResult{Id: in.GetUserId(), Message: "platform admin disabled"}, nil
}
func (f *fakePlatformAdminClient) EnablePlatformAdmin(_ context.Context, in *platformsettingsv1.EnablePlatformAdminRequest, _ ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	f.lastEnable = in
	if f.err != nil {
		return nil, f.err
	}
	f.status = "active"
	f.appendAudit(in.GetUserId(), "platform_admin.enable", "success")
	return &commonv1.IdempotentResult{Id: in.GetUserId(), Message: "platform admin enabled"}, nil
}
func (f *fakePlatformAdminClient) DeletePlatformAdmin(_ context.Context, in *platformsettingsv1.DeletePlatformAdminRequest, _ ...grpc.CallOption) (*commonv1.IdempotentResult, error) {
	f.lastDelete = in
	if f.lastAdminErr {
		return nil, status.Error(codes.FailedPrecondition, "LAST_PLATFORM_ADMIN")
	}
	if f.err != nil {
		return nil, f.err
	}
	f.deleted = true
	f.appendAudit(in.GetUserId(), "platform_admin.delete", "success")
	return &commonv1.IdempotentResult{Id: in.GetUserId(), Message: "platform admin deleted"}, nil
}
func (f *fakePlatformAdminClient) ListPlatformAdminAuditLogs(_ context.Context, in *platformsettingsv1.ListPlatformAdminAuditLogsRequest, _ ...grpc.CallOption) (*platformsettingsv1.ListPlatformAdminAuditLogsResponse, error) {
	f.lastListAudit = in
	if f.err != nil {
		return nil, f.err
	}
	limit := 20
	if page := in.GetPage(); page != nil && page.GetLimit() > 0 {
		limit = int(page.GetLimit())
		if limit > 100 {
			limit = 100
		}
	}
	var matched []*platformsettingsv1.PlatformAdminAuditLog
	for _, log := range f.auditLogs {
		if log.GetDetails() == nil || log.GetDetails().GetFields()["target_id"].GetStringValue() != in.GetUserId() {
			continue
		}
		if action := strings.TrimSpace(in.GetAction()); action != "" && log.GetAction() != action {
			continue
		}
		if result := strings.TrimSpace(in.GetResult()); result != "" && log.GetResult() != result {
			continue
		}
		matched = append(matched, log)
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].GetCreatedAt().AsTime().After(matched[j].GetCreatedAt().AsTime())
	})
	nextCursor := ""
	if len(matched) > limit {
		nextCursor = matched[limit-1].GetId()
		matched = matched[:limit]
	}
	return &platformsettingsv1.ListPlatformAdminAuditLogsResponse{
		Items:      matched,
		NextCursor: nextCursor,
	}, nil
}
func setupPlatformAdminTestServer(t *testing.T, client platformsettingsv1.PlatformAdminServiceClient) *server.Hertz {
	t.Helper()
	h := server.Default()
	h.Use(middleware.RequestID())
	registerPlatformAdminsAPI(h.Group("/api/v1/svc"), &platformAdminsAPI{client: client})
	return h
}

func performPlatformAdmin(h *server.Hertz, method, path, body string) *protocol.Response {
	var bodyArg *ut.Body
	if body != "" {
		bodyArg = &ut.Body{Body: strings.NewReader(body), Len: len(body)}
	}
	headers := []ut.Header{{Key: "Content-Type", Value: "application/json"}}
	return ut.PerformRequest(h.Engine, method, path, bodyArg, headers...).Result()
}

func TestMapPlatformAdminError_Table(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"validation", status.Error(codes.InvalidArgument, "VALIDATION_FAILED: bad"), http.StatusBadRequest, "VALIDATION_FAILED"},
		{"not found", status.Error(codes.NotFound, "PLATFORM_USER_NOT_FOUND: x"), http.StatusNotFound, "PLATFORM_USER_NOT_FOUND"},
		{"role not found", status.Error(codes.NotFound, "ROLE_NOT_FOUND"), http.StatusNotFound, "ROLE_NOT_FOUND"},
		{"username", status.Error(codes.AlreadyExists, "USERNAME_ALREADY_EXISTS"), http.StatusConflict, "USERNAME_ALREADY_EXISTS"},
		{"already exists fallback", status.Error(codes.AlreadyExists, "conflict"), http.StatusConflict, "USERNAME_ALREADY_EXISTS"},
		{"last admin", status.Error(codes.FailedPrecondition, "LAST_PLATFORM_ADMIN"), http.StatusUnprocessableEntity, "LAST_PLATFORM_ADMIN"},
		{"password", status.Error(codes.FailedPrecondition, "PASSWORD_SAME_AS_OLD"), http.StatusUnprocessableEntity, "PASSWORD_SAME_AS_OLD"},
		{"role change", status.Error(codes.FailedPrecondition, "ROLE_CHANGE_INVALID"), http.StatusUnprocessableEntity, "ROLE_CHANGE_INVALID"},
		{"core", status.Error(codes.Unavailable, "CORE_UNAVAILABLE: down"), http.StatusBadGateway, "CORE_UNAVAILABLE"},
		{"unimplemented", status.Error(codes.Unimplemented, "NOT_IMPLEMENTED"), http.StatusNotImplemented, "NOT_IMPLEMENTED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &app.RequestContext{}
			mapPlatformAdminError(c, tc.err)
			if c.Response.StatusCode() != tc.status {
				t.Fatalf("status=%d want %d", c.Response.StatusCode(), tc.status)
			}
			body := string(c.Response.Body())
			if !strings.Contains(body, tc.code) {
				t.Fatalf("body=%s", body)
			}
		})
	}
}

func TestPlatformAdmins_ListForwardParams(t *testing.T) {
	fake := &fakePlatformAdminClient{
		listResp: &platformsettingsv1.ListPlatformAdminsResponse{
			Items: []*platformsettingsv1.PlatformAdminListItem{
				{Id: "u1", Username: "local:ops", RoleId: "00000000-0000-0000-0000-000000000006", Role: "platform-ops", Status: "active", Source: "local"},
			},
			NextCursor: "n1",
		},
	}
	h := setupPlatformAdminTestServer(t, fake)
	resp := performPlatformAdmin(h, http.MethodGet, "/api/v1/svc/platform-admins?limit=7&cursor=c1&role_id=00000000-0000-0000-0000-000000000006&status=active&source=local&search=ops", "")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), resp.Body())
	}
	if fake.lastList == nil || fake.lastList.GetRoleId() != "00000000-0000-0000-0000-000000000006" || fake.lastList.GetPage().GetLimit() != 7 ||
		fake.lastList.GetPage().GetCursor() != "c1" || fake.lastList.GetSearch() != "ops" {
		t.Fatalf("lastList=%+v", fake.lastList)
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["next_cursor"] != "n1" {
		t.Fatalf("payload=%v", payload)
	}
}

func TestPlatformAdmins_RolesDoesNotCollideWithUserID(t *testing.T) {
	perm, err := structpb.NewStruct(map[string]any{"resource": "tenants", "actions": []any{"*"}, "scope": "platform"})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakePlatformAdminClient{
		rolesResp: &platformsettingsv1.ListPlatformAdminRolesResponse{
			Items: []*platformsettingsv1.PlatformRole{{
				Id: "00000000-0000-0000-0000-000000000006", Name: "platform-admin", Permissions: []*structpb.Struct{perm},
			}},
		},
	}
	h := setupPlatformAdminTestServer(t, fake)

	roles := performPlatformAdmin(h, http.MethodGet, "/api/v1/svc/platform-admins/roles", "")
	if roles.StatusCode() != http.StatusOK {
		t.Fatalf("roles status=%d body=%s", roles.StatusCode(), roles.Body())
	}
	if fake.lastGetID != "" {
		t.Fatalf("roles hit get with userId=%s", fake.lastGetID)
	}
	var rolesPayload map[string]any
	_ = json.Unmarshal(roles.Body(), &rolesPayload)
	items, _ := rolesPayload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("roles payload=%v", rolesPayload)
	}
	first, _ := items[0].(map[string]any)
	perms, ok := first["permissions"].([]any)
	if !ok || len(perms) != 1 {
		t.Fatalf("permissions must be array: %v", first)
	}
	if _, hasLabel := first["label"]; hasLabel {
		t.Fatalf("label must not be present: %v", first)
	}

	get := performPlatformAdmin(h, http.MethodGet, "/api/v1/svc/platform-admins/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "")
	if get.StatusCode() != http.StatusOK {
		t.Fatalf("get status=%d body=%s", get.StatusCode(), get.Body())
	}
	if fake.lastGetID != "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" {
		t.Fatalf("lastGetID=%s", fake.lastGetID)
	}
}

func TestPlatformAdmins_GetPermissionsForward(t *testing.T) {
	const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	perm, err := structpb.NewStruct(map[string]any{"resource": "metering", "actions": []any{"read"}, "scope": "platform"})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakePlatformAdminClient{
		permsResp: &platformsettingsv1.PlatformAdminPermissions{
			UserId: id, RoleId: "00000000-0000-0000-0000-000000000006", Role: "platform-readonly", Permissions: []*structpb.Struct{perm},
		},
	}
	h := setupPlatformAdminTestServer(t, fake)
	resp := performPlatformAdmin(h, http.MethodGet, "/api/v1/svc/platform-admins/"+id+"/permissions", "")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), resp.Body())
	}
	if fake.lastPermID != id {
		t.Fatalf("lastPermID=%s", fake.lastPermID)
	}
	var payload map[string]any
	_ = json.Unmarshal(resp.Body(), &payload)
	if payload["user_id"] != id || payload["role_id"] != "00000000-0000-0000-0000-000000000006" || payload["role"] != "platform-readonly" {
		t.Fatalf("payload=%v", payload)
	}
	perms, ok := payload["permissions"].([]any)
	if !ok || len(perms) != 1 {
		t.Fatalf("permissions=%v", payload["permissions"])
	}
}

func TestPlatformAdmins_UpdateRoleForwardBody(t *testing.T) {
	fake := &fakePlatformAdminClient{}
	h := setupPlatformAdminTestServer(t, fake)
	body := `{"role_id":"00000000-0000-0000-0000-000000000006","idempotency_key":"44444444-4444-4444-4444-444444444444"}`
	resp := performPlatformAdmin(h, http.MethodPut, "/api/v1/svc/platform-admins/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/role", body)
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), resp.Body())
	}
	if fake.lastUpdate == nil || fake.lastUpdate.GetUserId() != "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" ||
		fake.lastUpdate.GetRoleId() != "00000000-0000-0000-0000-000000000006" || fake.lastUpdate.GetIdempotencyKey() == "" {
		t.Fatalf("lastUpdate=%+v", fake.lastUpdate)
	}
}

func TestPlatformAdmins_CreateForwardBody(t *testing.T) {
	fake := &fakePlatformAdminClient{}
	h := setupPlatformAdminTestServer(t, fake)
	body := `{"email":"a@x.com","username":"ops","display_name":"Ops","role_id":"00000000-0000-0000-0000-000000000006","password":"Abcd1234!","idempotency_key":"44444444-4444-4444-4444-444444444444"}`
	resp := performPlatformAdmin(h, http.MethodPost, "/api/v1/svc/platform-admins", body)
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), resp.Body())
	}
	if fake.lastCreate == nil || fake.lastCreate.GetEmail() != "a@x.com" || fake.lastCreate.GetIdempotencyKey() == "" {
		t.Fatalf("lastCreate=%+v", fake.lastCreate)
	}
}

func TestHandler_CreateFlow(t *testing.T) {
	const id = "11111111-1111-1111-1111-111111111111"
	fake := &fakePlatformAdminClient{
		listResp: &platformsettingsv1.ListPlatformAdminsResponse{
			Items: []*platformsettingsv1.PlatformAdminListItem{
				{Id: id, Username: "ops", DisplayName: "Ops", RoleId: "00000000-0000-0000-0000-000000000006", Role: "platform-ops", Status: "active", Source: "local"},
			},
		},
		getResp: &platformsettingsv1.PlatformAdminDetail{
			Id: id, Email: "ops@ani.io", Username: "ops", DisplayName: "Ops",
			RoleId: "00000000-0000-0000-0000-000000000006", Role: "platform-ops", Status: "active", Source: "local",
		},
	}
	h := setupPlatformAdminTestServer(t, fake)

	createBody := `{"email":"ops@ani.io","username":"ops","display_name":"Ops","role_id":"00000000-0000-0000-0000-000000000006","password":"Abcd1234!","idempotency_key":"44444444-4444-4444-4444-444444444444"}`
	createResp := performPlatformAdmin(h, http.MethodPost, "/api/v1/svc/platform-admins", createBody)
	if createResp.StatusCode() != http.StatusOK {
		t.Fatalf("create status=%d body=%s", createResp.StatusCode(), createResp.Body())
	}
	var created map[string]any
	if err := json.Unmarshal(createResp.Body(), &created); err != nil {
		t.Fatal(err)
	}
	if created["id"] != id || created["message"] != "platform admin created" {
		t.Fatalf("created=%v", created)
	}
	if fake.lastCreate == nil || fake.lastCreate.GetPassword() == "" || fake.lastCreate.GetIdempotencyKey() == "" {
		t.Fatalf("lastCreate=%+v", fake.lastCreate)
	}

	listResp := performPlatformAdmin(h, http.MethodGet, "/api/v1/svc/platform-admins?role_id=00000000-0000-0000-0000-000000000006", "")
	if listResp.StatusCode() != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listResp.StatusCode(), listResp.Body())
	}
	var listed map[string]any
	_ = json.Unmarshal(listResp.Body(), &listed)
	items, _ := listed["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list=%v", listed)
	}
	first, _ := items[0].(map[string]any)
	if _, hasEmail := first["email"]; hasEmail {
		t.Fatalf("list item must not include email: %v", first)
	}
	for _, key := range []string{"id", "username", "display_name", "role_id", "role", "status", "source"} {
		if first[key] == nil || first[key] == "" {
			t.Fatalf("missing list field %s: %v", key, first)
		}
	}
	if first["username"] != "ops" {
		t.Fatalf("username=%v", first["username"])
	}

	detailResp := performPlatformAdmin(h, http.MethodGet, "/api/v1/svc/platform-admins/"+id, "")
	if detailResp.StatusCode() != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detailResp.StatusCode(), detailResp.Body())
	}
	var detail map[string]any
	_ = json.Unmarshal(detailResp.Body(), &detail)
	for _, key := range []string{"id", "email", "username", "display_name", "role_id", "role", "status", "source"} {
		if detail[key] == nil || detail[key] == "" {
			t.Fatalf("missing detail field %s: %v", key, detail)
		}
	}
	if detail["email"] != "ops@ani.io" || detail["role_id"] != "00000000-0000-0000-0000-000000000006" || detail["role"] != "platform-ops" || detail["username"] != "ops" {
		t.Fatalf("detail=%v", detail)
	}
}

func TestPlatformAdmins_ListInvalidLimit(t *testing.T) {
	fake := &fakePlatformAdminClient{}
	h := setupPlatformAdminTestServer(t, fake)
	resp := performPlatformAdmin(h, http.MethodGet, "/api/v1/svc/platform-admins?limit=abc", "")
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), resp.Body())
	}
	if !strings.Contains(string(resp.Body()), "VALIDATION_FAILED") {
		t.Fatalf("body=%s", resp.Body())
	}
	if fake.lastList != nil {
		t.Fatalf("unexpected list call on invalid limit")
	}
}

func TestPlatformAdmins_ListEmpty(t *testing.T) {
	fake := &fakePlatformAdminClient{
		listResp: &platformsettingsv1.ListPlatformAdminsResponse{Items: nil, NextCursor: ""},
	}
	h := setupPlatformAdminTestServer(t, fake)
	resp := performPlatformAdmin(h, http.MethodGet, "/api/v1/svc/platform-admins", "")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), resp.Body())
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body(), &payload); err != nil {
		t.Fatal(err)
	}
	items, _ := payload["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("want empty items got %v", payload)
	}
	if payload["next_cursor"] != nil {
		t.Fatalf("want next_cursor null/empty got %v", payload["next_cursor"])
	}
}

func TestPlatformAdmins_GetNotFound(t *testing.T) {
	fake := &fakePlatformAdminClient{err: status.Error(codes.NotFound, "PLATFORM_USER_NOT_FOUND")}
	h := setupPlatformAdminTestServer(t, fake)
	resp := performPlatformAdmin(h, http.MethodGet, "/api/v1/svc/platform-admins/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "")
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), resp.Body())
	}
	if !strings.Contains(string(resp.Body()), "PLATFORM_USER_NOT_FOUND") {
		t.Fatalf("body=%s", resp.Body())
	}
}

func TestPlatformAdmins_UnimplementedMaps501(t *testing.T) {
	fake := &fakePlatformAdminClient{err: status.Error(codes.Unimplemented, "NOT_IMPLEMENTED")}
	h := setupPlatformAdminTestServer(t, fake)
	resp := performPlatformAdmin(h, http.MethodPost, "/api/v1/svc/platform-admins",
		`{"email":"a@x.com","username":"ops","display_name":"Ops","role_id":"00000000-0000-0000-0000-000000000006","password":"Abcd1234!","idempotency_key":"44444444-4444-4444-4444-444444444444"}`)
	if resp.StatusCode() != http.StatusNotImplemented {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), resp.Body())
	}
}

func TestHandler_ResetPasswordFlow(t *testing.T) {
	const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const oldPwd = "OldPass1!"
	const newPwd = "NewPass2@"
	fake := &fakePlatformAdminClient{currentPassword: oldPwd}
	h := setupPlatformAdminTestServer(t, fake)

	if !fake.verifyPassword(oldPwd) {
		t.Fatal("precondition: old password must match before reset")
	}

	body := `{"new_password":"NewPass2@","idempotency_key":"44444444-4444-4444-4444-444444444444"}`
	reset := performPlatformAdmin(h, http.MethodPost, "/api/v1/svc/platform-admins/"+id+"/reset-password", body)
	if reset.StatusCode() != http.StatusOK {
		t.Fatalf("reset status=%d body=%s", reset.StatusCode(), reset.Body())
	}
	if fake.lastReset == nil || fake.lastReset.GetUserId() != id || fake.lastReset.GetNewPassword() != newPwd || fake.lastReset.GetIdempotencyKey() == "" {
		t.Fatalf("lastReset=%+v", fake.lastReset)
	}
	var payload map[string]any
	if err := json.Unmarshal(reset.Body(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["id"] != id || payload["message"] != "platform admin password reset" {
		t.Fatalf("payload=%v", payload)
	}
	if _, hasPwd := payload["new_password"]; hasPwd {
		t.Fatalf("response must not echo password: %v", payload)
	}

	if fake.verifyPassword(oldPwd) {
		t.Fatal("old password must fail after reset")
	}
	if !fake.verifyPassword(newPwd) {
		t.Fatal("new password must succeed after reset")
	}
}

func TestHandler_DisableEnableFlow(t *testing.T) {
	const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	fake := &fakePlatformAdminClient{
		status: "active",
		getResp: &platformsettingsv1.PlatformAdminDetail{
			Id: id, Username: "ops", Role: "platform-ops", Status: "active", Source: "local",
		},
	}
	h := setupPlatformAdminTestServer(t, fake)
	body := `{"idempotency_key":"44444444-4444-4444-4444-444444444444"}`

	disable := performPlatformAdmin(h, http.MethodPost, "/api/v1/svc/platform-admins/"+id+"/disable", body)
	if disable.StatusCode() != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", disable.StatusCode(), disable.Body())
	}
	if fake.lastDisable == nil || fake.lastDisable.GetUserId() != id || fake.lastDisable.GetIdempotencyKey() == "" {
		t.Fatalf("lastDisable=%+v", fake.lastDisable)
	}

	detailDisabled := performPlatformAdmin(h, http.MethodGet, "/api/v1/svc/platform-admins/"+id, "")
	if detailDisabled.StatusCode() != http.StatusOK {
		t.Fatalf("get after disable status=%d", detailDisabled.StatusCode())
	}
	var afterDisable map[string]any
	_ = json.Unmarshal(detailDisabled.Body(), &afterDisable)
	if afterDisable["status"] != "disabled" {
		t.Fatalf("want disabled got %v", afterDisable)
	}

	enable := performPlatformAdmin(h, http.MethodPost, "/api/v1/svc/platform-admins/"+id+"/enable", body)
	if enable.StatusCode() != http.StatusOK {
		t.Fatalf("enable status=%d body=%s", enable.StatusCode(), enable.Body())
	}
	if fake.lastEnable == nil || fake.lastEnable.GetUserId() != id {
		t.Fatalf("lastEnable=%+v", fake.lastEnable)
	}

	detailEnabled := performPlatformAdmin(h, http.MethodGet, "/api/v1/svc/platform-admins/"+id, "")
	var afterEnable map[string]any
	_ = json.Unmarshal(detailEnabled.Body(), &afterEnable)
	if afterEnable["status"] != "active" {
		t.Fatalf("want active got %v", afterEnable)
	}
}

func TestHandler_DeleteFlow(t *testing.T) {
	const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	fake := &fakePlatformAdminClient{
		getResp: &platformsettingsv1.PlatformAdminDetail{Id: id, Username: "ops", Status: "active"},
	}
	h := setupPlatformAdminTestServer(t, fake)
	body := `{"idempotency_key":"44444444-4444-4444-4444-444444444444"}`

	del := performPlatformAdmin(h, http.MethodDelete, "/api/v1/svc/platform-admins/"+id, body)
	if del.StatusCode() != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", del.StatusCode(), del.Body())
	}
	if fake.lastDelete == nil || fake.lastDelete.GetUserId() != id {
		t.Fatalf("lastDelete=%+v", fake.lastDelete)
	}

	detail := performPlatformAdmin(h, http.MethodGet, "/api/v1/svc/platform-admins/"+id, "")
	if detail.StatusCode() != http.StatusNotFound {
		t.Fatalf("get after delete status=%d body=%s", detail.StatusCode(), detail.Body())
	}
	if !strings.Contains(string(detail.Body()), "PLATFORM_USER_NOT_FOUND") {
		t.Fatalf("body=%s", detail.Body())
	}
}

func TestHandler_LastAdminProtection(t *testing.T) {
	const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	fake := &fakePlatformAdminClient{lastAdminErr: true}
	h := setupPlatformAdminTestServer(t, fake)
	body := `{"idempotency_key":"44444444-4444-4444-4444-444444444444"}`

	disable := performPlatformAdmin(h, http.MethodPost, "/api/v1/svc/platform-admins/"+id+"/disable", body)
	if disable.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("disable status=%d body=%s", disable.StatusCode(), disable.Body())
	}
	if !strings.Contains(string(disable.Body()), "LAST_PLATFORM_ADMIN") {
		t.Fatalf("disable body=%s", disable.Body())
	}

	del := performPlatformAdmin(h, http.MethodDelete, "/api/v1/svc/platform-admins/"+id, body)
	if del.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("delete status=%d body=%s", del.StatusCode(), del.Body())
	}
	if !strings.Contains(string(del.Body()), "LAST_PLATFORM_ADMIN") {
		t.Fatalf("delete body=%s", del.Body())
	}
}

func TestHandler_AuditLogsFlow(t *testing.T) {
	const id = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const operatorID = "99999999-9999-9999-9999-999999999999"
	fake := &fakePlatformAdminClient{status: "active", auditOperatorID: operatorID}
	h := setupPlatformAdminTestServer(t, fake)
	body := `{"idempotency_key":"44444444-4444-4444-4444-444444444444"}`

	disable := performPlatformAdmin(h, http.MethodPost, "/api/v1/svc/platform-admins/"+id+"/disable", body)
	if disable.StatusCode() != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", disable.StatusCode(), disable.Body())
	}
	enable := performPlatformAdmin(h, http.MethodPost, "/api/v1/svc/platform-admins/"+id+"/enable", body)
	if enable.StatusCode() != http.StatusOK {
		t.Fatalf("enable status=%d body=%s", enable.StatusCode(), enable.Body())
	}

	logs := performPlatformAdmin(h, http.MethodGet, "/api/v1/svc/platform-admins/"+id+"/audit-logs?limit=20", "")
	if logs.StatusCode() != http.StatusOK {
		t.Fatalf("audit-logs status=%d body=%s", logs.StatusCode(), logs.Body())
	}
	if fake.lastListAudit == nil || fake.lastListAudit.GetUserId() != id || fake.lastListAudit.GetPage().GetLimit() != 20 {
		t.Fatalf("lastListAudit=%+v", fake.lastListAudit)
	}
	var payload map[string]any
	if err := json.Unmarshal(logs.Body(), &payload); err != nil {
		t.Fatal(err)
	}
	items, _ := payload["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items=%v", payload)
	}
	first, _ := items[0].(map[string]any)
	second, _ := items[1].(map[string]any)
	if first["action"] != "platform_admin.enable" || second["action"] != "platform_admin.disable" {
		t.Fatalf("order=%v / %v", first, second)
	}
	for _, key := range []string{"id", "action", "resource", "result", "created_at", "user_id"} {
		if first[key] == nil || first[key] == "" {
			t.Fatalf("missing %s in %v", key, first)
		}
	}
	if first["user_id"] != operatorID {
		t.Fatalf("operator=%v want %s", first["user_id"], operatorID)
	}
	if _, hasPwd := first["password"]; hasPwd {
		t.Fatalf("audit item must not include password: %v", first)
	}

	filtered := performPlatformAdmin(h, http.MethodGet, "/api/v1/svc/platform-admins/"+id+"/audit-logs?action=platform_admin.disable", "")
	if filtered.StatusCode() != http.StatusOK {
		t.Fatalf("filtered status=%d body=%s", filtered.StatusCode(), filtered.Body())
	}
	var filteredPayload map[string]any
	_ = json.Unmarshal(filtered.Body(), &filteredPayload)
	filteredItems, _ := filteredPayload["items"].([]any)
	if len(filteredItems) != 1 {
		t.Fatalf("filtered=%v", filteredPayload)
	}
	row, _ := filteredItems[0].(map[string]any)
	if row["action"] != "platform_admin.disable" || row["result"] != "success" {
		t.Fatalf("row=%v", row)
	}

	empty := performPlatformAdmin(h, http.MethodGet, "/api/v1/svc/platform-admins/"+id+"/audit-logs?action=platform_admin.create", "")
	var emptyPayload map[string]any
	_ = json.Unmarshal(empty.Body(), &emptyPayload)
	emptyItems, _ := emptyPayload["items"].([]any)
	if len(emptyItems) != 0 {
		t.Fatalf("empty filter=%v", emptyPayload)
	}
	if emptyPayload["next_cursor"] != nil {
		t.Fatalf("next_cursor=%v", emptyPayload["next_cursor"])
	}
}
