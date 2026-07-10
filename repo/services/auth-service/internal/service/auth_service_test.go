package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
)

func TestCheckPermissionHonorsAPIKeyScopes(t *testing.T) {
	svc := &AuthService{}
	tenantID := uuid.New().String()

	allowed, err := svc.CheckPermission(context.Background(), &authv1.CheckPermissionRequest{
		TenantId: tenantID,
		Roles:    []string{"service-account", "scope:instances:create"},
		Resource: "instances",
		Action:   "create",
	})
	if err != nil {
		t.Fatalf("CheckPermission allow error: %v", err)
	}
	if !allowed.GetAllowed() {
		t.Fatalf("scope should allow create, got deny: %s", allowed.GetReason())
	}

	denied, err := svc.CheckPermission(context.Background(), &authv1.CheckPermissionRequest{
		TenantId: tenantID,
		Roles:    []string{"service-account", "scope:instances:create"},
		Resource: "instances",
		Action:   "delete",
	})
	if err != nil {
		t.Fatalf("CheckPermission deny error: %v", err)
	}
	if denied.GetAllowed() {
		t.Fatal("create-only scope unexpectedly allowed delete")
	}
}

func TestCheckPermissionHonorsAPIKeyWildcardScope(t *testing.T) {
	svc := &AuthService{}
	resp, err := svc.CheckPermission(context.Background(), &authv1.CheckPermissionRequest{
		TenantId: uuid.New().String(),
		Roles:    []string{"service-account", "scope:instances:*"},
		Resource: "instances",
		Action:   "delete",
	})
	if err != nil {
		t.Fatalf("CheckPermission error: %v", err)
	}
	if !resp.GetAllowed() {
		t.Fatalf("wildcard scope should allow delete, got deny: %s", resp.GetReason())
	}
}

func TestCheckPermissionExecRequiresAdminOrExplicitScope(t *testing.T) {
	svc := &AuthService{}
	tenantID := uuid.New().String()

	tests := []struct {
		name  string
		roles []string
		want  bool
	}{
		{name: "platform admin", roles: []string{"platform-admin"}, want: true},
		{name: "tenant admin", roles: []string{"tenant-admin"}, want: true},
		{name: "explicit scope", roles: []string{"service-account", "scope:instances:exec"}, want: true},
		{name: "ordinary user", roles: []string{"user"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.CheckPermission(context.Background(), &authv1.CheckPermissionRequest{
				TenantId: tenantID,
				Roles:    tt.roles,
				Resource: "instances",
				Action:   "exec",
			})
			if err != nil {
				t.Fatalf("CheckPermission error: %v", err)
			}
			if resp.GetAllowed() != tt.want {
				t.Fatalf("allowed = %v, want %v, reason: %s", resp.GetAllowed(), tt.want, resp.GetReason())
			}
		})
	}
}

func TestCheckPermissionUserCanDeleteOwnResources(t *testing.T) {
	svc := &AuthService{}
	resp, err := svc.CheckPermission(context.Background(), &authv1.CheckPermissionRequest{
		TenantId: uuid.New().String(),
		Roles:    []string{"user"},
		Resource: "images",
		Action:   "delete",
	})
	if err != nil {
		t.Fatalf("CheckPermission error: %v", err)
	}
	if !resp.GetAllowed() {
		t.Fatalf("user should be allowed to delete images, got deny: %s", resp.GetReason())
	}
}

func TestCheckPermissionAllowsConfiguredRolePermission(t *testing.T) {
	svc := &AuthService{
		rolePerms: fakeRolePermissionResolver{
			resolved: true,
			allowed:  true,
		},
	}

	resp, err := svc.CheckPermission(context.Background(), &authv1.CheckPermissionRequest{
		TenantId: uuid.New().String(),
		Roles:    []string{"user"},
		Resource: "instances",
		Action:   "exec",
	})
	if err != nil {
		t.Fatalf("CheckPermission error: %v", err)
	}
	if !resp.GetAllowed() {
		t.Fatalf("configured role permission should allow exec, got deny: %s", resp.GetReason())
	}
}

func TestCheckPermissionReturnsErrorWhenRolePermissionResolverFails(t *testing.T) {
	svc := &AuthService{
		rolePerms: fakeRolePermissionResolver{
			err: errors.New("database unavailable"),
		},
	}

	if _, err := svc.CheckPermission(context.Background(), &authv1.CheckPermissionRequest{
		TenantId: uuid.New().String(),
		Roles:    []string{"user"},
		Resource: "instances",
		Action:   "exec",
	}); err == nil {
		t.Fatalf("CheckPermission error = nil, want resolver error")
	}
}

func TestPermissionsAllowSupportsScopesAndStructuredPermissions(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		resource string
		action   string
		want     bool
	}{
		{name: "scope exact", raw: `["scope:instances:exec"]`, resource: "instances", action: "exec", want: true},
		{name: "scope read alias", raw: `["instances:read"]`, resource: "instances", action: "get", want: true},
		{name: "structured action", raw: `[{"resource":"instances","actions":["exec"]}]`, resource: "instances", action: "exec", want: true},
		{name: "structured wildcard", raw: `[{"resource":"instances","actions":["*"]}]`, resource: "instances", action: "delete", want: true},
		{name: "denied", raw: `["instances:read"]`, resource: "instances", action: "exec", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := permissionsAllow(json.RawMessage(tt.raw), tt.resource, tt.action); got != tt.want {
				t.Fatalf("permissionsAllow() = %v, want %v", got, tt.want)
			}
		})
	}
}

type fakeRolePermissionResolver struct {
	resolved bool
	allowed  bool
	err      error
}

func (r fakeRolePermissionResolver) Allows(context.Context, string, []string, string, string) (bool, bool, error) {
	return r.resolved, r.allowed, r.err
}
