package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	anisdk "github.com/kubercloud/ani-sdks/core-go/anisdk"
	"github.com/kubercloud/ani/services/platform-settings-service/internal/repo/ports"
)

func TestCorePlatformUserClient_List_ParamsAndDecode(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/admin/platform-users" {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("limit") != "10" || q.Get("cursor") != "c1" || q.Get("role_id") != "00000000-0000-0000-0000-000000000006" ||
			q.Get("status") != "active" || q.Get("source") != "local" || q.Get("search") != "ops" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{
					"id": "11111111-1111-1111-1111-111111111111", "email": "a@example.com",
					"username": "local:ops", "display_name": "Ops", "role_id": "00000000-0000-0000-0000-000000000006", "role": "platform-ops",
					"status": "active", "source": "local", "created_at": "2026-08-31T00:00:00Z",
				},
			},
			"next_cursor": "c2",
		})
	}))
	defer srv.Close()

	client := &CorePlatformUserClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	out, err := client.List(context.Background(), ports.PlatformUserListFilter{
		Limit: 10, Cursor: "c1", RoleID: "00000000-0000-0000-0000-000000000006", Status: "active", Source: "local", Search: "ops",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out.Items) != 1 || out.NextCursor != "c2" || out.Items[0].Username != "local:ops" {
		t.Fatalf("unexpected: %+v", out)
	}
}

func TestCorePlatformUserClient_Create(t *testing.T) {
	t.Parallel()

	id := "22222222-2222-2222-2222-222222222222"
	var sawCreate, sawGet bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/admin/platform-users":
			sawCreate = true
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "message": "ok"})
		case r.Method == http.MethodGet:
			sawGet = true
			t.Fatalf("Create must not call GET: %s %s", r.Method, r.URL.Path)
		default:
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := &CorePlatformUserClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	out, err := client.Create(context.Background(), ports.PlatformUserCreateInput{
		Email: "b@example.com", Username: "admin", DisplayName: "Admin", RoleID: "00000000-0000-0000-0000-000000000006", Password: "Abcd1234!",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !sawCreate || sawGet || out != id {
		t.Fatalf("create=%v get=%v out=%q", sawCreate, sawGet, out)
	}
}

func TestMapSDKError_BusinessCodes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		code string
		want error
	}{
		{"PLATFORM_USER_NOT_FOUND", ports.ErrPlatformUserNotFound},
		{"ROLE_NOT_FOUND", ports.ErrRoleNotFound},
		{"USERNAME_ALREADY_EXISTS", ports.ErrUsernameAlreadyExists},
		{"LAST_PLATFORM_ADMIN", ports.ErrLastPlatformAdmin},
		{"PASSWORD_SAME_AS_OLD", ports.ErrPasswordSameAsOld},
		{"STATUS_UNCHANGED", ports.ErrStatusUnchanged},
		{"ROLE_CHANGE_INVALID", ports.ErrRoleChangeInvalid},
		{"VALIDATION_FAILED", ports.ErrValidationFailed},
		{"NOT_FOUND", ports.ErrCoreUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			err := mapSDKError(anisdk.NewAPIError(tc.code, "detail", "req", nil))
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCorePlatformUserClient_Get_Success(t *testing.T) {
	t.Parallel()

	id := "33333333-3333-3333-3333-333333333333"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/admin/platform-users/"+id {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": id, "email": "ops@ani.io", "username": "local:ops", "display_name": "Ops",
			"role_id": "00000000-0000-0000-0000-000000000006", "role": "platform-ops", "status": "active", "source": "local",
			"created_at": "2026-08-31T00:00:00Z",
		})
	}))
	defer srv.Close()

	client := &CorePlatformUserClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	out, err := client.Get(context.Background(), uuid.MustParse(id))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if out.ID != id || out.Username != "local:ops" || out.RoleID != "00000000-0000-0000-0000-000000000006" || out.Role != "platform-ops" {
		t.Fatalf("out=%+v", out)
	}
}

func TestCorePlatformUserClient_Get_404CoreUnavailable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "NOT_FOUND", "message": "no route"})
	}))
	defer srv.Close()

	client := &CorePlatformUserClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	_, err := client.Get(context.Background(), uuid.MustParse("33333333-3333-3333-3333-333333333333"))
	if err == nil || !errors.Is(err, ports.ErrCoreUnavailable) {
		t.Fatalf("expected ErrCoreUnavailable, got %v", err)
	}
}

func TestCorePlatformUserClient_ListPlatformRoles(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/admin/platform-users/roles" {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{{
				"id": "00000000-0000-0000-0000-000000000006", "name": "platform-ops",
				"permissions": []map[string]any{{"resource": "tenants", "actions": []any{"*"}, "scope": "platform"}},
			}},
		})
	}))
	defer srv.Close()

	client := &CorePlatformUserClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	out, err := client.ListPlatformRoles(context.Background())
	if err != nil {
		t.Fatalf("ListPlatformRoles: %v", err)
	}
	if len(out) != 1 || out[0].ID != "00000000-0000-0000-0000-000000000006" || out[0].Name != "platform-ops" || len(out[0].Permissions) != 1 {
		t.Fatalf("out=%+v", out)
	}
}

func TestCorePlatformUserClient_GetPlatformUserPermissions(t *testing.T) {
	t.Parallel()

	id := "44444444-4444-4444-4444-444444444444"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/admin/platform-users/"+id+"/permissions" {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user_id": id, "role_id": "00000000-0000-0000-0000-000000000006", "role": "platform-readonly",
			"permissions": []map[string]any{{"resource": "metering", "actions": []any{"read"}, "scope": "platform"}},
		})
	}))
	defer srv.Close()

	client := &CorePlatformUserClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	out, err := client.GetPlatformUserPermissions(context.Background(), uuid.MustParse(id))
	if err != nil {
		t.Fatalf("GetPlatformUserPermissions: %v", err)
	}
	if out.UserID != id || out.RoleID != "00000000-0000-0000-0000-000000000006" || out.Role != "platform-readonly" || len(out.Permissions) != 1 {
		t.Fatalf("out=%+v", out)
	}
}

func TestCorePlatformUserClient_GetPlatformUserPermissions_NotFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "PLATFORM_USER_NOT_FOUND", "message": "not found"})
	}))
	defer srv.Close()

	client := &CorePlatformUserClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	_, err := client.GetPlatformUserPermissions(context.Background(), uuid.MustParse("44444444-4444-4444-4444-444444444444"))
	if err == nil || !errors.Is(err, ports.ErrPlatformUserNotFound) {
		t.Fatalf("expected ErrPlatformUserNotFound, got %v", err)
	}
}

func TestCorePlatformUserClient_SetStatus_DisableEnable(t *testing.T) {
	t.Parallel()
	id := "55555555-5555-5555-5555-555555555555"
	var sawDisable, sawEnable bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/admin/platform-users/"+id+"/disable":
			sawDisable = true
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "message": "disabled"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/admin/platform-users/"+id+"/enable":
			sawEnable = true
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "message": "enabled"})
		default:
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	client := &CorePlatformUserClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	uid := uuid.MustParse(id)
	if err := client.SetStatus(context.Background(), uid, "disabled"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := client.SetStatus(context.Background(), uid, "active"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !sawDisable || !sawEnable {
		t.Fatalf("disable=%v enable=%v", sawDisable, sawEnable)
	}
}

func TestCorePlatformUserClient_SoftDelete(t *testing.T) {
	t.Parallel()
	id := "66666666-6666-6666-6666-666666666666"
	var sawDelete bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/admin/platform-users/"+id {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		sawDelete = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "message": "deleted"})
	}))
	defer srv.Close()

	client := &CorePlatformUserClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	if err := client.SoftDelete(context.Background(), uuid.MustParse(id)); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if !sawDelete {
		t.Fatal("delete not called")
	}
}

func TestCorePlatformUserClient_ResetPassword(t *testing.T) {
	t.Parallel()
	id := "77777777-7777-7777-7777-777777777777"
	var sawBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/admin/platform-users/"+id+"/reset-password" {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&sawBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "message": "ok"})
	}))
	defer srv.Close()

	client := &CorePlatformUserClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	if err := client.ResetPassword(context.Background(), uuid.MustParse(id), "NewPass2@"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if sawBody["new_password"] != "NewPass2@" {
		t.Fatalf("body=%v", sawBody)
	}
}

func TestCorePlatformUserClient_ResetPassword_SameAsOld(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "PASSWORD_SAME_AS_OLD", "message": "same as old"})
	}))
	defer srv.Close()

	client := &CorePlatformUserClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	err := client.ResetPassword(context.Background(), uuid.MustParse("77777777-7777-7777-7777-777777777777"), "Abcd1234!")
	if err == nil || !errors.Is(err, ports.ErrPasswordSameAsOld) {
		t.Fatalf("expected ErrPasswordSameAsOld, got %v", err)
	}
}

func TestCorePlatformUserClient_SetStatus_LastAdmin(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "LAST_PLATFORM_ADMIN", "message": "last admin"})
	}))
	defer srv.Close()

	client := &CorePlatformUserClient{sdk: anisdk.NewClient(strings.TrimRight(srv.URL, "/")+"/api/v1", "")}
	err := client.SetStatus(context.Background(), uuid.MustParse("55555555-5555-5555-5555-555555555555"), "disabled")
	if err == nil || !errors.Is(err, ports.ErrLastPlatformAdmin) {
		t.Fatalf("expected ErrLastPlatformAdmin, got %v", err)
	}
}
