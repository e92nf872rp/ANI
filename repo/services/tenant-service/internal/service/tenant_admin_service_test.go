package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeTenantAdminCoreClient struct {
	matchID    uuid.UUID
	matchErr   error
	isAdmin    bool
	isAdminErr error
}

func (f *fakeTenantAdminCoreClient) MatchUser(context.Context, uuid.UUID, string, string) (uuid.UUID, error) {
	if f.matchErr != nil {
		return uuid.Nil, f.matchErr
	}
	return f.matchID, nil
}
func (f *fakeTenantAdminCoreClient) IsAlreadyAdmin(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	if f.isAdminErr != nil {
		return false, f.isAdminErr
	}
	return f.isAdmin, nil
}
func (f *fakeTenantAdminCoreClient) GetUser(context.Context, uuid.UUID, uuid.UUID) (ports.AdminWithTenant, error) {
	return ports.AdminWithTenant{}, ports.ErrNotImplemented
}
func (f *fakeTenantAdminCoreClient) GetAdminDetail(context.Context, uuid.UUID, uuid.UUID) (ports.AdminWithTenant, error) {
	return ports.AdminWithTenant{}, ports.ErrNotImplemented
}
func (f *fakeTenantAdminCoreClient) ListTenantAdmins(context.Context, ports.TenantAdminListFilter) (ports.ListResult, error) {
	return ports.ListResult{}, ports.ErrNotImplemented
}
func (f *fakeTenantAdminCoreClient) ChangeRole(context.Context, uuid.UUID, uuid.UUID, string) error {
	return ports.ErrNotImplemented
}
func (f *fakeTenantAdminCoreClient) GetRolePermissions(context.Context, uuid.UUID, uuid.UUID) (ports.UserPermissions, error) {
	return ports.UserPermissions{}, ports.ErrNotImplemented
}
func (f *fakeTenantAdminCoreClient) GetChangeableRoles(context.Context, uuid.UUID, uuid.UUID) (ports.ChangeableRoles, error) {
	return ports.ChangeableRoles{}, ports.ErrNotImplemented
}
func (f *fakeTenantAdminCoreClient) SetStatus(context.Context, uuid.UUID, uuid.UUID, string) error {
	return ports.ErrNotImplemented
}
func (f *fakeTenantAdminCoreClient) SoftDelete(context.Context, uuid.UUID, uuid.UUID) error {
	return ports.ErrNotImplemented
}
func (f *fakeTenantAdminCoreClient) ResetPassword(context.Context, uuid.UUID, uuid.UUID, string) error {
	return ports.ErrNotImplemented
}

type fakeTenantAdminStore struct {
	pending     bool
	pendingErr  error
	insertErr   error
	latest      *ports.TenantAdminInvitation
	latestErr   error
	updateErr   error
	invitations []ports.TenantAdminInvitation
	insertCalls int
	updateCalls int
}

func (f *fakeTenantAdminStore) HasPendingInvitation(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	if f.pendingErr != nil {
		return false, f.pendingErr
	}
	return f.pending, nil
}
func (f *fakeTenantAdminStore) InsertInvitation(_ context.Context, inv ports.TenantAdminInvitation) (ports.TenantAdminInvitation, error) {
	f.insertCalls++
	if f.insertErr != nil {
		return ports.TenantAdminInvitation{}, f.insertErr
	}
	if inv.ID == uuid.Nil {
		inv.ID = uuid.New()
	}
	if inv.CreatedAt.IsZero() {
		inv.CreatedAt = time.Now().UTC()
	}
	f.invitations = append(f.invitations, inv)
	return inv, nil
}
func (f *fakeTenantAdminStore) GetLatestInvitation(context.Context, uuid.UUID, uuid.UUID) (ports.TenantAdminInvitation, error) {
	if f.latestErr != nil {
		return ports.TenantAdminInvitation{}, f.latestErr
	}
	if f.latest == nil {
		return ports.TenantAdminInvitation{}, ports.ErrTenantAdminInvitationNotFound
	}
	return *f.latest, nil
}
func (f *fakeTenantAdminStore) UpdateInvitation(_ context.Context, inv ports.TenantAdminInvitation) (ports.TenantAdminInvitation, error) {
	f.updateCalls++
	if f.updateErr != nil {
		return ports.TenantAdminInvitation{}, f.updateErr
	}
	if f.latest == nil {
		return ports.TenantAdminInvitation{}, ports.ErrTenantAdminInvitationNotFound
	}
	out := *f.latest
	out.ID = inv.ID
	if inv.ID == uuid.Nil {
		out.ID = f.latest.ID
	}
	out.TokenHash = inv.TokenHash
	out.ExpireAt = inv.ExpireAt
	out.Status = inv.Status
	out.AcceptedAt = nil
	out.RejectedAt = nil
	f.latest = &out
	return out, nil
}
func (f *fakeTenantAdminStore) ListAuditLogs(context.Context, uuid.UUID, uuid.UUID, ports.TenantAdminAuditLogFilter) (ports.TenantAdminAuditLogListResult, error) {
	return ports.TenantAdminAuditLogListResult{}, ports.ErrNotImplemented
}

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
	svc := NewTenantAdminService(nil, tenants, nil, nil)

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
	svc := NewTenantAdminService(nil, nil, nil, nil)
	_, err := svc.ListAvailableTenants(context.Background(), &tenantv1.ListAvailableTenantsRequest{})
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unavailable {
		t.Fatalf("want Unavailable, got %v", err)
	}
}

func TestTenantAdminService_Invite(t *testing.T) {
	t.Parallel()
	tenantID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	userID := uuid.MustParse("22222222-2222-4222-8222-222222222222")

	t.Run("success_token_expire_audit", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{matchID: userID}
		store := &fakeTenantAdminStore{}
		audit := &fakeAuditStore{}
		svc := NewTenantAdminService(core, nil, store, audit)

		before := time.Now().UTC()
		res, err := svc.InviteTenantAdmin(context.Background(), &tenantv1.InviteTenantAdminRequest{
			TenantId:       tenantID.String(),
			Email:          "admin@acme.io",
			Username:       "acme_admin",
			IdempotencyKey: "550e8400-e29b-41d4-a716-446655440000",
		})
		if err != nil {
			t.Fatalf("InviteTenantAdmin: %v", err)
		}
		if res.GetId() == "" || res.GetToken() == "" || res.GetExpireAt() == nil {
			t.Fatalf("incomplete result: %+v", res)
		}
		if len(res.GetToken()) != 64 { // 32 bytes hex
			t.Fatalf("token len=%d", len(res.GetToken()))
		}
		expireAt := res.GetExpireAt().AsTime()
		wantMin := before.Add(71 * time.Hour)
		wantMax := before.Add(73 * time.Hour)
		if expireAt.Before(wantMin) || expireAt.After(wantMax) {
			t.Fatalf("expire_at=%v not ~72h from now", expireAt)
		}
		if store.insertCalls != 1 || len(store.invitations) != 1 {
			t.Fatalf("insertCalls=%d invitations=%d", store.insertCalls, len(store.invitations))
		}
		inv := store.invitations[0]
		if inv.Status != ports.InvitationStatusInviting {
			t.Fatalf("status=%s", inv.Status)
		}
		sum := sha256.Sum256([]byte(res.GetToken()))
		wantHash := hex.EncodeToString(sum[:])
		if inv.TokenHash != wantHash {
			t.Fatalf("token_hash mismatch store=%s want=%s", inv.TokenHash, wantHash)
		}
		if len(audit.logs) != 1 {
			t.Fatalf("audit logs=%d", len(audit.logs))
		}
		log := audit.logs[0]
		if log.Action != "tenant_admin.invite" || log.Result != "success" || log.Resource != auditResourceTenantAdmin {
			t.Fatalf("audit=%+v", log)
		}
		if log.Details["target_id"] != userID.String() {
			t.Fatalf("target_id=%v", log.Details["target_id"])
		}
		if log.Details["token_hash"] != wantHash {
			t.Fatalf("audit token_hash=%v", log.Details["token_hash"])
		}
	})

	t.Run("not_found", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{matchErr: ports.ErrTenantAdminNotFound}
		audit := &fakeAuditStore{}
		svc := NewTenantAdminService(core, nil, &fakeTenantAdminStore{}, audit)
		_, err := svc.InviteTenantAdmin(context.Background(), &tenantv1.InviteTenantAdminRequest{
			TenantId:       tenantID.String(),
			Email:          "missing@acme.io",
			Username:       "missing",
			IdempotencyKey: "550e8400-e29b-41d4-a716-446655440001",
		})
		assertBusinessCode(t, err, codes.NotFound, "TENANT_ADMIN_NOT_FOUND")
		assertFailureAudit(t, audit, "tenant_admin.invite")
	})

	t.Run("already_admin", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{matchID: userID, isAdmin: true}
		audit := &fakeAuditStore{}
		svc := NewTenantAdminService(core, nil, &fakeTenantAdminStore{}, audit)
		_, err := svc.InviteTenantAdmin(context.Background(), &tenantv1.InviteTenantAdminRequest{
			TenantId:       tenantID.String(),
			Email:          "admin@acme.io",
			Username:       "acme_admin",
			IdempotencyKey: "550e8400-e29b-41d4-a716-446655440002",
		})
		assertBusinessCode(t, err, codes.AlreadyExists, "TENANT_ADMIN_ALREADY_ADMIN")
		assertFailureAudit(t, audit, "tenant_admin.invite")
	})

	t.Run("pending", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{matchID: userID}
		store := &fakeTenantAdminStore{pending: true}
		audit := &fakeAuditStore{}
		svc := NewTenantAdminService(core, nil, store, audit)
		_, err := svc.InviteTenantAdmin(context.Background(), &tenantv1.InviteTenantAdminRequest{
			TenantId:       tenantID.String(),
			Email:          "admin@acme.io",
			Username:       "acme_admin",
			IdempotencyKey: "550e8400-e29b-41d4-a716-446655440003",
		})
		assertBusinessCode(t, err, codes.AlreadyExists, "TENANT_INVITATION_PENDING")
		if store.insertCalls != 0 {
			t.Fatalf("should not insert when pending")
		}
		assertFailureAudit(t, audit, "tenant_admin.invite")
	})
}

func assertFailureAudit(t *testing.T, audit *fakeAuditStore, action string) {
	t.Helper()
	if len(audit.logs) != 1 {
		t.Fatalf("audit logs=%d want 1", len(audit.logs))
	}
	log := audit.logs[0]
	if log.Action != action || log.Result != "failure" || log.Resource != auditResourceTenantAdmin {
		t.Fatalf("audit=%+v", log)
	}
	if code, _ := log.Details["code"].(string); code == "" {
		t.Fatalf("failure audit missing code: %+v", log.Details)
	}
	if msg, _ := log.Details["message"].(string); msg == "" {
		t.Fatalf("failure audit missing message: %+v", log.Details)
	}
}

func assertBusinessCode(t *testing.T, err error, wantCode codes.Code, wantPrefix string) {
	t.Helper()
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("want gRPC status, got %v", err)
	}
	if st.Code() != wantCode {
		t.Fatalf("code=%v want %v msg=%s", st.Code(), wantCode, st.Message())
	}
	if !strings.HasPrefix(st.Message(), wantPrefix) {
		t.Fatalf("message=%q want prefix %q", st.Message(), wantPrefix)
	}
}

func TestTenantAdminService_Resend(t *testing.T) {
	t.Parallel()
	tenantID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	userID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	invID := uuid.MustParse("33333333-3333-4333-8333-333333333333")

	newLatest := func(status string) *ports.TenantAdminInvitation {
		oldHash := "old-token-hash"
		return &ports.TenantAdminInvitation{
			ID:        invID,
			TenantID:  tenantID,
			UserID:    userID,
			TokenHash: oldHash,
			Status:    status,
			ExpireAt:  time.Now().UTC().Add(-time.Hour),
			CreatedAt: time.Now().UTC().Add(-48 * time.Hour),
		}
	}

	for _, statusName := range []string{ports.InvitationStatusInviting, ports.InvitationStatusExpired} {
		statusName := statusName
		t.Run("success_"+statusName, func(t *testing.T) {
			t.Parallel()
			store := &fakeTenantAdminStore{latest: newLatest(statusName)}
			oldHash := store.latest.TokenHash
			audit := &fakeAuditStore{}
			svc := NewTenantAdminService(nil, nil, store, audit)

			before := time.Now().UTC()
			res, err := svc.ResendTenantAdminInvitation(context.Background(), &tenantv1.ResendTenantAdminInvitationRequest{
				TenantId:       tenantID.String(),
				UserId:         userID.String(),
				IdempotencyKey: "550e8400-e29b-41d4-a716-446655440010",
			})
			if err != nil {
				t.Fatalf("Resend: %v", err)
			}
			if res.GetId() != invID.String() || res.GetToken() == "" || res.GetExpireAt() == nil {
				t.Fatalf("incomplete: %+v", res)
			}
			if res.GetMessage() != "invitation resent" {
				t.Fatalf("message=%q", res.GetMessage())
			}
			if len(res.GetToken()) != 64 {
				t.Fatalf("token len=%d", len(res.GetToken()))
			}
			expireAt := res.GetExpireAt().AsTime()
			if expireAt.Before(before.Add(71*time.Hour)) || expireAt.After(before.Add(73*time.Hour)) {
				t.Fatalf("expire_at=%v not ~72h", expireAt)
			}
			if store.updateCalls != 1 || store.latest == nil {
				t.Fatalf("updateCalls=%d latest=%v", store.updateCalls, store.latest)
			}
			if store.latest.Status != ports.InvitationStatusInviting {
				t.Fatalf("status=%s want inviting", store.latest.Status)
			}
			if store.latest.TokenHash == oldHash || store.latest.TokenHash == "" {
				t.Fatalf("token_hash not refreshed: %q", store.latest.TokenHash)
			}
			sum := sha256.Sum256([]byte(res.GetToken()))
			wantHash := hex.EncodeToString(sum[:])
			if store.latest.TokenHash != wantHash {
				t.Fatalf("hash mismatch store=%s want=%s", store.latest.TokenHash, wantHash)
			}
			if len(audit.logs) != 1 || audit.logs[0].Action != "tenant_admin.resend_invitation" || audit.logs[0].Result != "success" {
				t.Fatalf("audit=%+v", audit.logs)
			}
			if audit.logs[0].Details["token_hash"] != wantHash {
				t.Fatalf("audit token_hash=%v", audit.logs[0].Details["token_hash"])
			}
		})
	}

	t.Run("not_found", func(t *testing.T) {
		t.Parallel()
		audit := &fakeAuditStore{}
		svc := NewTenantAdminService(nil, nil, &fakeTenantAdminStore{latestErr: ports.ErrTenantAdminInvitationNotFound}, audit)
		_, err := svc.ResendTenantAdminInvitation(context.Background(), &tenantv1.ResendTenantAdminInvitationRequest{
			TenantId:       tenantID.String(),
			UserId:         userID.String(),
			IdempotencyKey: "550e8400-e29b-41d4-a716-446655440011",
		})
		assertBusinessCode(t, err, codes.NotFound, "TENANT_ADMIN_INVITATION_NOT_FOUND")
		assertFailureAudit(t, audit, "tenant_admin.resend_invitation")
		if audit.logs[0].Details["code"] != "TENANT_ADMIN_INVITATION_NOT_FOUND" {
			t.Fatalf("audit code=%v", audit.logs[0].Details["code"])
		}
	})

	t.Run("settled_accepted", func(t *testing.T) {
		t.Parallel()
		audit := &fakeAuditStore{}
		svc := NewTenantAdminService(nil, nil, &fakeTenantAdminStore{latest: newLatest(ports.InvitationStatusAccepted)}, audit)
		_, err := svc.ResendTenantAdminInvitation(context.Background(), &tenantv1.ResendTenantAdminInvitationRequest{
			TenantId:       tenantID.String(),
			UserId:         userID.String(),
			IdempotencyKey: "550e8400-e29b-41d4-a716-446655440012",
		})
		assertBusinessCode(t, err, codes.FailedPrecondition, "TENANT_INVITATION_SETTLED")
		assertFailureAudit(t, audit, "tenant_admin.resend_invitation")
	})

	t.Run("settled_rejected", func(t *testing.T) {
		t.Parallel()
		audit := &fakeAuditStore{}
		svc := NewTenantAdminService(nil, nil, &fakeTenantAdminStore{latest: newLatest(ports.InvitationStatusRejected)}, audit)
		_, err := svc.ResendTenantAdminInvitation(context.Background(), &tenantv1.ResendTenantAdminInvitationRequest{
			TenantId:       tenantID.String(),
			UserId:         userID.String(),
			IdempotencyKey: "550e8400-e29b-41d4-a716-446655440013",
		})
		assertBusinessCode(t, err, codes.FailedPrecondition, "TENANT_INVITATION_SETTLED")
		assertFailureAudit(t, audit, "tenant_admin.resend_invitation")
	})
}

func TestTenantAdminService_Unimplemented(t *testing.T) {
	s := NewTenantAdminService(nil, nil, nil, nil)
	ctx := context.Background()

	checks := []struct {
		name string
		call func() error
	}{
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

	if len(checks) != 10 {
		t.Fatalf("want 10 remaining unimplemented RPCs, got %d", len(checks))
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
