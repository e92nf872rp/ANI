package service

import (
	"context"
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
