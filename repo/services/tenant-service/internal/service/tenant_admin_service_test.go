package service

import (
	"context"
	"testing"

	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTenantAdminService_Unimplemented(t *testing.T) {
	s := NewTenantAdminService()
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
		{"TransferTenantOwnership", func() error {
			_, err := s.TransferTenantOwnership(ctx, &tenantv1.TransferTenantOwnershipRequest{})
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

	if len(checks) != 13 {
		t.Fatalf("want 13 RPCs, got %d", len(checks))
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
