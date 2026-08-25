package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTenantAdminService_ListAvailableTenants(t *testing.T) {
	t.Parallel()
	id1 := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	id2 := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	tenants := &fakeTenantClient{
		available: []ports.BoundTenant{
			{ID: id1, Name: "acme", DisplayName: "Acme", Status: ports.TenantStatusActive},
			{ID: id2, Name: "beta", DisplayName: "Beta", Status: ports.TenantStatusFrozen},
		},
	}
	svc := NewTenantAdminService(nil, tenants)

	res, err := svc.ListAvailableTenants(context.Background(), &tenantv1.ListAvailableTenantsRequest{})
	if err != nil {
		t.Fatalf("ListAvailableTenants: %v", err)
	}
	if len(res.GetItems()) != 2 {
		t.Fatalf("items=%d", len(res.GetItems()))
	}
	if res.Items[0].GetId() != id1.String() || res.Items[0].GetStatus() != "active" {
		t.Fatalf("item0=%+v", res.Items[0])
	}
	if res.Items[1].GetId() != id2.String() || res.Items[1].GetStatus() != "frozen" {
		t.Fatalf("item1=%+v", res.Items[1])
	}
	for _, it := range res.Items {
		if it.GetStatus() == "disabled" {
			t.Fatalf("disabled leaked: %+v", it)
		}
		if it.GetId() == "" || it.GetName() == "" {
			t.Fatalf("incomplete fields: %+v", it)
		}
	}
}

func TestTenantAdminService_ListAvailableTenants_NilClient(t *testing.T) {
	t.Parallel()
	svc := NewTenantAdminService(nil, nil)
	_, err := svc.ListAvailableTenants(context.Background(), &tenantv1.ListAvailableTenantsRequest{})
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unavailable {
		t.Fatalf("want Unavailable, got %v", err)
	}
}

func TestTenantAdminService_Unimplemented(t *testing.T) {
	s := NewTenantAdminService(nil, nil)
	ctx := context.Background()

	checks := []struct {
		name string
		call func() error
	}{
		{"InviteTenantAdmin", func() error {
			_, err := s.InviteTenantAdmin(ctx, &tenantv1.InviteTenantAdminRequest{})
			return err
		}},
		{"ResendTenantAdminInvitation", func() error {
			_, err := s.ResendTenantAdminInvitation(ctx, &tenantv1.ResendTenantAdminInvitationRequest{})
			return err
		}},
		{"ListAllTenantAdmins", func() error {
			_, err := s.ListAllTenantAdmins(ctx, &tenantv1.ListAllTenantAdminsRequest{})
			return err
		}},
		{"GetTenantAdminDetail", func() error {
			_, err := s.GetTenantAdminDetail(ctx, &tenantv1.GetTenantAdminDetailRequest{})
			return err
		}},
		{"UpdateTenantAdminRole", func() error {
			_, err := s.UpdateTenantAdminRole(ctx, &tenantv1.UpdateTenantAdminRoleRequest{})
			return err
		}},
		{"GetTenantAdminRole", func() error {
			_, err := s.GetTenantAdminRole(ctx, &tenantv1.GetTenantAdminRoleRequest{})
			return err
		}},
		{"GetChangeableRoles", func() error {
			_, err := s.GetChangeableRoles(ctx, &tenantv1.GetChangeableRolesRequest{})
			return err
		}},
		{"ResetTenantAdminPassword", func() error {
			_, err := s.ResetTenantAdminPassword(ctx, &tenantv1.ResetTenantAdminPasswordRequest{})
			return err
		}},
		{"DisableTenantAdmin", func() error {
			_, err := s.DisableTenantAdmin(ctx, &tenantv1.DisableTenantAdminRequest{})
			return err
		}},
		{"EnableTenantAdmin", func() error {
			_, err := s.EnableTenantAdmin(ctx, &tenantv1.EnableTenantAdminRequest{})
			return err
		}},
		{"DeleteTenantAdmin", func() error {
			_, err := s.DeleteTenantAdmin(ctx, &tenantv1.DeleteTenantAdminRequest{})
			return err
		}},
		{"ListTenantAdminAuditLogs", func() error {
			_, err := s.ListTenantAdminAuditLogs(ctx, &tenantv1.ListTenantAdminAuditLogsRequest{})
			return err
		}},
	}

	if len(checks) != 12 {
		t.Fatalf("want 12 remaining unimplemented RPCs, got %d", len(checks))
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("got %v, want gRPC status", err)
			}
			if st.Code() != codes.Unimplemented {
				t.Fatalf("got code %v, want Unimplemented", st.Code())
			}
			if st.Message() != "NOT_IMPLEMENTED" {
				t.Fatalf("got message %q, want NOT_IMPLEMENTED", st.Message())
			}
		})
	}
}
