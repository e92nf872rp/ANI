package router

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type fakeAdminPlatformUserStore struct {
	createIn  ports.PlatformUserCreate
	createRes ports.PlatformUserAdmin
	createErr error

	listRes ports.PlatformUserListResult
	getRes  ports.PlatformUserAdmin
	getErr  error

	rolesRes []ports.PlatformRole
	permsRes ports.PlatformUserPermissions
	permsErr error
}

func (f *fakeAdminPlatformUserStore) Create(_ context.Context, in ports.PlatformUserCreate) (ports.PlatformUserAdmin, error) {
	f.createIn = in
	if f.createErr != nil {
		return ports.PlatformUserAdmin{}, f.createErr
	}
	if f.createRes.ID != uuid.Nil {
		return f.createRes, nil
	}
	return ports.PlatformUserAdmin{}, ports.ErrNotImplemented
}
func (f *fakeAdminPlatformUserStore) List(context.Context, ports.PlatformUserFilter) (ports.PlatformUserListResult, error) {
	return f.listRes, nil
}
func (f *fakeAdminPlatformUserStore) Get(context.Context, uuid.UUID) (ports.PlatformUserAdmin, error) {
	if f.getErr != nil {
		return ports.PlatformUserAdmin{}, f.getErr
	}
	return f.getRes, nil
}
func (f *fakeAdminPlatformUserStore) ChangeRole(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}
func (f *fakeAdminPlatformUserStore) ResetPassword(context.Context, uuid.UUID, string) error {
	return nil
}
func (f *fakeAdminPlatformUserStore) SetStatus(context.Context, uuid.UUID, string) error { return nil }
func (f *fakeAdminPlatformUserStore) SoftDelete(context.Context, uuid.UUID) error        { return nil }
func (f *fakeAdminPlatformUserStore) CountActivePlatformAdmins(context.Context, uuid.UUID) (int, error) {
	return 0, nil
}
func (f *fakeAdminPlatformUserStore) ListPlatformRoles(context.Context) ([]ports.PlatformRole, error) {
	return f.rolesRes, nil
}
func (f *fakeAdminPlatformUserStore) GetPlatformUserPermissions(_ context.Context, userID uuid.UUID) (ports.PlatformUserPermissions, error) {
	if f.permsErr != nil {
		return ports.PlatformUserPermissions{}, f.permsErr
	}
	if f.permsRes.UserID != uuid.Nil {
		return f.permsRes, nil
	}
	return ports.PlatformUserPermissions{UserID: userID}, nil
}

func setupAdminPlatformUserTestServer(t *testing.T, store ports.PlatformUserAdminStore) *server.Hertz {
	t.Helper()
	h := server.Default()
	RegisterWithOptions(h, RegisterOptions{PlatformUserAdminStore: store})
	return h
}

func performAdminPlatformUser(h *server.Hertz, method, path, body string) *protocol.Response {
	var b *ut.Body
	headers := []ut.Header{}
	if body != "" {
		b = &ut.Body{Body: strings.NewReader(body), Len: len(body)}
		headers = append(headers, ut.Header{Key: "Content-Type", Value: "application/json"})
	}
	return ut.PerformRequest(h.Engine, method, path, b, headers...).Result()
}

func TestHandler_AdminCreatePlatformUser(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	store := &fakeAdminPlatformUserStore{
		createRes: ports.PlatformUserAdmin{ID: id, Role: "platform-ops"},
	}
	h := setupAdminPlatformUserTestServer(t, store)

	body := `{"idempotency_key":"44444444-4444-4444-4444-444444444444","email":"ops@ani.io","username":"ops","display_name":"Ops","role_id":"00000000-0000-0000-0000-000000000006","password":"Abcd1234!"}`
	resp := performAdminPlatformUser(h, http.MethodPost, "/api/v1/admin/platform-users", body)
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), string(resp.Body()))
	}
	if store.createIn.PasswordHash == "" || store.createIn.PasswordHash == "Abcd1234!" {
		t.Fatalf("handler must bcrypt password before store: %+v", store.createIn)
	}
	var out map[string]string
	if err := json.Unmarshal(resp.Body(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["id"] != id.String() || out["message"] == "" {
		t.Fatalf("out=%v", out)
	}
}

func TestHandler_AdminCreatePlatformUser_WeakPassword(t *testing.T) {
	t.Parallel()
	store := &fakeAdminPlatformUserStore{}
	h := setupAdminPlatformUserTestServer(t, store)

	body := `{"idempotency_key":"44444444-4444-4444-4444-444444444444","email":"ops@ani.io","username":"ops","display_name":"Ops","role_id":"00000000-0000-0000-0000-000000000006","password":"short"}`
	resp := performAdminPlatformUser(h, http.MethodPost, "/api/v1/admin/platform-users", body)
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), string(resp.Body()))
	}
	if !strings.Contains(string(resp.Body()), "VALIDATION_FAILED") {
		t.Fatalf("body=%s", string(resp.Body()))
	}
	if store.createIn.PasswordHash != "" {
		t.Fatalf("must not call store on weak password: %+v", store.createIn)
	}
}

func TestHandler_AdminCreatePlatformUser_UsernameConflict(t *testing.T) {
	t.Parallel()
	store := &fakeAdminPlatformUserStore{createErr: ports.ErrUsernameAlreadyExists}
	h := setupAdminPlatformUserTestServer(t, store)

	body := `{"idempotency_key":"44444444-4444-4444-4444-444444444444","email":"ops@ani.io","username":"ops","display_name":"Ops","role_id":"00000000-0000-0000-0000-000000000006","password":"Abcd1234!"}`
	resp := performAdminPlatformUser(h, http.MethodPost, "/api/v1/admin/platform-users", body)
	if resp.StatusCode() != http.StatusConflict {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), string(resp.Body()))
	}
	if !strings.Contains(string(resp.Body()), "USERNAME_ALREADY_EXISTS") {
		t.Fatalf("body=%s", string(resp.Body()))
	}
}

func TestHandler_AdminGetPlatformUser(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	dn := "Ops"
	store := &fakeAdminPlatformUserStore{
		getRes: ports.PlatformUserAdmin{
			ID: id, Email: "ops@ani.io", Username: "local:ops", DisplayName: &dn,
			RoleID: uuid.MustParse("00000000-0000-0000-0000-000000000006"), Role: "platform-ops", Status: "active", Source: "local",
			CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	h := setupAdminPlatformUserTestServer(t, store)
	resp := performAdminPlatformUser(h, http.MethodGet, "/api/v1/admin/platform-users/"+id.String(), "")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), string(resp.Body()))
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["username"] != "ops" || payload["role_id"] != "00000000-0000-0000-0000-000000000006" || payload["role"] != "platform-ops" {
		t.Fatalf("payload=%v", payload)
	}
}

func TestHandler_AdminListPlatformUsers(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	dn := "Ops"
	store := &fakeAdminPlatformUserStore{
		listRes: ports.PlatformUserListResult{
			Items: []ports.PlatformUserAdmin{{
				ID: id, Email: "ops@ani.io", Username: "local:ops", DisplayName: &dn,
				RoleID: uuid.MustParse("00000000-0000-0000-0000-000000000006"), Role: "platform-ops", Status: "active", Source: "local",
				CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			}},
			NextCursor: "next1",
		},
	}
	h := setupAdminPlatformUserTestServer(t, store)
	resp := performAdminPlatformUser(h, http.MethodGet, "/api/v1/admin/platform-users?limit=10&role_id=00000000-0000-0000-0000-000000000006&search=ops", "")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), string(resp.Body()))
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body(), &payload); err != nil {
		t.Fatal(err)
	}
	items, _ := payload["items"].([]any)
	first, _ := items[0].(map[string]any)
	if first["username"] != "ops" {
		t.Fatalf("username=%v", first["username"])
	}
}

func TestHandler_AdminListPlatformUserRoles(t *testing.T) {
	t.Parallel()
	store := &fakeAdminPlatformUserStore{
		rolesRes: []ports.PlatformRole{{
			ID: uuid.MustParse("00000000-0000-0000-0000-000000000006"), Name: "platform-ops",
			Permissions: []map[string]any{{"resource": "tenants", "actions": []any{"*"}, "scope": "platform"}},
		}},
	}
	h := setupAdminPlatformUserTestServer(t, store)
	resp := performAdminPlatformUser(h, http.MethodGet, "/api/v1/admin/platform-users/roles", "")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), string(resp.Body()))
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body(), &payload); err != nil {
		t.Fatal(err)
	}
	items, _ := payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("payload=%v", payload)
	}
	first, _ := items[0].(map[string]any)
	if first["id"] != "00000000-0000-0000-0000-000000000006" || first["name"] != "platform-ops" {
		t.Fatalf("item=%v", first)
	}
	if _, hasLabel := first["label"]; hasLabel {
		t.Fatalf("label must not be present: %v", first)
	}
	if _, hasDesc := first["description"]; hasDesc {
		t.Fatalf("description must not be present: %v", first)
	}
	perms, _ := first["permissions"].([]any)
	if len(perms) != 1 {
		t.Fatalf("permissions=%v", first["permissions"])
	}
}

func TestHandler_AdminGetPlatformUserPermissions(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	store := &fakeAdminPlatformUserStore{
		permsRes: ports.PlatformUserPermissions{
			UserID: id, RoleID: uuid.MustParse("00000000-0000-0000-0000-000000000006"), Role: "platform-ops",
			Permissions: []map[string]any{{"resource": "tenants", "actions": []any{"read"}, "scope": "platform"}},
		},
	}
	h := setupAdminPlatformUserTestServer(t, store)
	resp := performAdminPlatformUser(h, http.MethodGet, "/api/v1/admin/platform-users/"+id.String()+"/permissions", "")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), string(resp.Body()))
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["user_id"] != id.String() || payload["role_id"] != "00000000-0000-0000-0000-000000000006" || payload["role"] != "platform-ops" {
		t.Fatalf("payload=%v", payload)
	}
	if _, hasLabel := payload["label"]; hasLabel {
		t.Fatalf("label must not be present: %v", payload)
	}
	if _, hasDesc := payload["description"]; hasDesc {
		t.Fatalf("description must not be present: %v", payload)
	}
	perms, _ := payload["permissions"].([]any)
	if len(perms) != 1 {
		t.Fatalf("permissions=%v", payload["permissions"])
	}
}

func TestHandler_AdminRolesDoesNotCollideWithUserID(t *testing.T) {
	t.Parallel()
	store := &fakeAdminPlatformUserStore{
		rolesRes: []ports.PlatformRole{{ID: uuid.MustParse("00000000-0000-0000-0000-000000000006"), Name: "platform-admin", Permissions: []map[string]any{}}},
		getErr:   ports.ErrPlatformUserNotFound,
	}
	h := setupAdminPlatformUserTestServer(t, store)
	roles := performAdminPlatformUser(h, http.MethodGet, "/api/v1/admin/platform-users/roles", "")
	if roles.StatusCode() != http.StatusOK {
		t.Fatalf("roles status=%d body=%s", roles.StatusCode(), string(roles.Body()))
	}
	// /roles 不得落入 /:userId
	get := performAdminPlatformUser(h, http.MethodGet, "/api/v1/admin/platform-users/roles", "")
	if get.StatusCode() != http.StatusOK {
		t.Fatalf("repeated roles hit=%d", get.StatusCode())
	}
}

func TestHandler_AdminGetPlatformUserPermissions_NotFound(t *testing.T) {
	t.Parallel()
	store := &fakeAdminPlatformUserStore{permsErr: ports.ErrPlatformUserNotFound}
	h := setupAdminPlatformUserTestServer(t, store)
	resp := performAdminPlatformUser(h, http.MethodGet, "/api/v1/admin/platform-users/11111111-1111-1111-1111-111111111111/permissions", "")
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), string(resp.Body()))
	}
	if !strings.Contains(string(resp.Body()), "PLATFORM_USER_NOT_FOUND") {
		t.Fatalf("body=%s", string(resp.Body()))
	}
}
