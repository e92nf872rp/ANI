package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	tenantv1 "github.com/kubercloud/ani/pkg/generated/pb/tenant/v1"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type fakeTenantAdminCoreClient struct {
	matchID          uuid.UUID
	matchErr         error
	isAdmin          bool
	isAdminErr       error
	users            map[string]ports.AdminWithTenant // key tenant|user
	listItems        []ports.AdminWithTenant
	listErr          error
	roles            []ports.AssignableRole
	rolesErr         error
	changeRoleErr    error
	changeRoleID     uuid.UUID // if set, only this role_id succeeds
	lastChangeRoleID uuid.UUID
	perms            *ports.UserPermissions
	permsErr         error
	resetPasswordErr error
	setStatusErr     error
	lastStatus       string
	softDeleteErr    error
	softDeleted      bool
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
func (f *fakeTenantAdminCoreClient) GetUser(_ context.Context, tenantID, userID uuid.UUID) (ports.AdminWithTenant, error) {
	if f.users != nil {
		if u, ok := f.users[tenantUserKey(tenantID, userID)]; ok {
			return u, nil
		}
	}
	return ports.AdminWithTenant{}, ports.ErrTenantAdminNotFound
}
func (f *fakeTenantAdminCoreClient) BatchGetUsers(_ context.Context, tenantID uuid.UUID, userIDs []uuid.UUID) (map[uuid.UUID]ports.AdminWithTenant, error) {
	out := make(map[uuid.UUID]ports.AdminWithTenant, len(userIDs))
	for _, uid := range userIDs {
		if u, ok := f.users[tenantUserKey(tenantID, uid)]; ok {
			out[uid] = u
		}
	}
	return out, nil
}
func (f *fakeTenantAdminCoreClient) GetAdminDetail(ctx context.Context, tenantID, userID uuid.UUID) (ports.AdminWithTenant, error) {
	return f.GetUser(ctx, tenantID, userID)
}
func (f *fakeTenantAdminCoreClient) ListTenantAdmins(context.Context, ports.TenantAdminListFilter) (ports.ListResult, error) {
	if f.listErr != nil {
		return ports.ListResult{}, f.listErr
	}
	return ports.ListResult{Items: append([]ports.AdminWithTenant(nil), f.listItems...)}, nil
}
func (f *fakeTenantAdminCoreClient) ChangeRole(_ context.Context, _, _, roleID uuid.UUID) error {
	if f.changeRoleErr != nil {
		return f.changeRoleErr
	}
	if f.changeRoleID != uuid.Nil && roleID != f.changeRoleID {
		return ports.ErrRoleChangeInvalid
	}
	f.lastChangeRoleID = roleID
	// 模拟改绑后角色名变化，便于审计断言 new_role
	if f.perms != nil && f.perms.Role == "user" {
		cp := *f.perms
		cp.Role = "tenant-admin"
		cp.RoleID = roleID
		f.perms = &cp
	}
	return nil
}
func (f *fakeTenantAdminCoreClient) GetRolePermissions(_ context.Context, tenantID, userID uuid.UUID) (ports.UserPermissions, error) {
	if f.permsErr != nil {
		return ports.UserPermissions{}, f.permsErr
	}
	if f.perms != nil {
		return *f.perms, nil
	}
	if u, ok := f.users[tenantUserKey(tenantID, userID)]; ok {
		tid := tenantID
		return ports.UserPermissions{
			UserID: userID, TenantID: &tid, Role: u.Role,
			Permissions: []any{},
		}, nil
	}
	return ports.UserPermissions{}, ports.ErrTenantAdminNotFound
}
func (f *fakeTenantAdminCoreClient) ListAssignableRoles(context.Context, uuid.UUID) ([]ports.AssignableRole, error) {
	if f.rolesErr != nil {
		return nil, f.rolesErr
	}
	return append([]ports.AssignableRole(nil), f.roles...), nil
}
func (f *fakeTenantAdminCoreClient) SetStatus(_ context.Context, tenantID, userID uuid.UUID, status string) error {
	if f.setStatusErr != nil {
		return f.setStatusErr
	}
	// 模拟 Core DB 层重复校验：状态未变化 → ErrUserStateInvalid
	if u, ok := f.users[tenantUserKey(tenantID, userID)]; ok {
		if u.Status == status {
			return ports.ErrUserStateInvalid
		}
	}
	f.lastStatus = status
	return nil
}
func (f *fakeTenantAdminCoreClient) SoftDelete(_ context.Context, _, _ uuid.UUID) error {
	if f.softDeleteErr != nil {
		return f.softDeleteErr
	}
	f.softDeleted = true
	return nil
}
func (f *fakeTenantAdminCoreClient) ResetPassword(_ context.Context, _, _ uuid.UUID, _ string) error {
	if f.resetPasswordErr != nil {
		return f.resetPasswordErr
	}
	return nil
}

type fakeTenantAdminStore struct {
	pending     bool
	pendingErr  error
	insertErr   error
	latest      *ports.TenantAdminInvitation
	latestErr   error
	updateErr   error
	flags       []ports.InvitationFlag
	flagsErr    error
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
	// 模拟 store 事务语义：最新为 expired → 原地更新
	if f.latest != nil && f.latest.Status == ports.InvitationStatusExpired {
		f.updateCalls++
		out := *f.latest
		out.TokenHash = inv.TokenHash
		out.ExpireAt = inv.ExpireAt
		out.Status = ports.InvitationStatusInviting
		out.AcceptedAt = nil
		out.RejectedAt = nil
		f.latest = &out
		return out, nil
	}
	if inv.ID == uuid.Nil {
		inv.ID = uuid.New()
	}
	if inv.CreatedAt.IsZero() {
		inv.CreatedAt = time.Now().UTC()
	}
	f.invitations = append(f.invitations, inv)
	f.latest = &inv
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
func (f *fakeTenantAdminStore) ListInvitationFlags(_ context.Context, tenantID *uuid.UUID, statusFilter string) ([]ports.InvitationFlag, error) {
	if f.flagsErr != nil {
		return nil, f.flagsErr
	}
	f.expireStaleInvitations()
	source := f.flags
	if len(source) == 0 && f.latest != nil {
		source = []ports.InvitationFlag{invitationFlagFromLatest(*f.latest)}
	}
	out := make([]ports.InvitationFlag, 0, len(source))
	for _, flag := range source {
		if tenantID != nil && flag.TenantID != *tenantID {
			continue
		}
		switch statusFilter {
		case ports.InvitationStatusInviting:
			if !flag.IsInviting {
				continue
			}
		case ports.InvitationStatusExpired:
			if !flag.IsExpired {
				continue
			}
		}
		if !flag.IsInviting && !flag.IsExpired {
			continue
		}
		out = append(out, flag)
	}
	return out, nil
}
func (f *fakeTenantAdminStore) GetInvitationFlags(_ context.Context, tenantID, userID uuid.UUID) (ports.InvitationFlag, error) {
	if f.flagsErr != nil {
		return ports.InvitationFlag{}, f.flagsErr
	}
	f.expireStaleInvitations()
	if f.latest != nil && f.latest.TenantID == tenantID && f.latest.UserID == userID {
		return invitationFlagFromLatest(*f.latest), nil
	}
	for _, flag := range f.flags {
		if flag.TenantID == tenantID && flag.UserID == userID {
			return flag, nil
		}
	}
	out := ports.InvitationFlag{TenantID: tenantID, UserID: userID}
	if f.pending {
		out.IsInviting = true
	}
	return out, nil
}
func (f *fakeTenantAdminStore) expireStaleInvitations() {
	now := time.Now().UTC()
	if f.latest != nil &&
		f.latest.Status == ports.InvitationStatusInviting &&
		!f.latest.ExpireAt.IsZero() &&
		f.latest.ExpireAt.Before(now) {
		f.latest.Status = ports.InvitationStatusExpired
	}
	for i := range f.invitations {
		inv := &f.invitations[i]
		if inv.Status == ports.InvitationStatusInviting &&
			!inv.ExpireAt.IsZero() &&
			inv.ExpireAt.Before(now) {
			inv.Status = ports.InvitationStatusExpired
		}
	}
	// 同步显式 flags：与 latest 对齐（懒过期后）
	if f.latest != nil {
		refreshed := invitationFlagFromLatest(*f.latest)
		replaced := false
		for i := range f.flags {
			if f.flags[i].TenantID == f.latest.TenantID && f.flags[i].UserID == f.latest.UserID {
				f.flags[i] = refreshed
				replaced = true
			}
		}
		if !replaced && (refreshed.IsInviting || refreshed.IsExpired) {
			f.flags = append(f.flags, refreshed)
		}
	}
}
func invitationFlagFromLatest(inv ports.TenantAdminInvitation) ports.InvitationFlag {
	out := ports.InvitationFlag{TenantID: inv.TenantID, UserID: inv.UserID}
	switch inv.Status {
	case ports.InvitationStatusInviting:
		out.IsInviting = true
	case ports.InvitationStatusExpired:
		out.IsExpired = true
	}
	return out
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

func TestTenantAdminService_ListTenantRoles(t *testing.T) {
	t.Parallel()
	tenantID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	adminRoleID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	userRoleID := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	auditorID := uuid.MustParse("00000000-0000-0000-0000-000000000004")
	core := &fakeTenantAdminCoreClient{
		roles: []ports.AssignableRole{
			{
				ID: adminRoleID, Name: "tenant-admin",
				Permissions: []any{map[string]any{"resource": "*", "actions": []any{"*"}, "scope": "tenant"}},
			},
			{
				ID: userRoleID, Name: "user",
				Permissions: []any{map[string]any{"resource": "instances", "actions": []any{"read"}, "scope": "own"}},
			},
			{
				ID: auditorID, Name: "auditor",
				Permissions: []any{},
			},
		},
	}
	svc := NewTenantAdminService(core, nil, nil, nil)

	res, err := svc.ListTenantRoles(context.Background(), &tenantv1.ListTenantRolesRequest{
		TenantId: tenantID.String(),
	})
	if err != nil {
		t.Fatalf("ListTenantRoles: %v", err)
	}
	if len(res.GetItems()) != 3 {
		t.Fatalf("items=%d want 3", len(res.GetItems()))
	}
	byName := map[string]string{}
	for _, it := range res.Items {
		byName[it.GetName()] = it.GetId()
		if strings.HasPrefix(it.GetName(), "platform-") {
			t.Fatalf("platform role leaked: %+v", it)
		}
		if it.GetId() == "" || it.GetName() == "" {
			t.Fatalf("incomplete: %+v", it)
		}
		if it.GetTenantId() != nil {
			t.Fatalf("system role tenant_id want nil, got %v", it.GetTenantId())
		}
		if it.GetPermissions() == nil {
			t.Fatalf("permissions missing for %s", it.GetName())
		}
	}
	if byName["tenant-admin"] != adminRoleID.String() || byName["user"] == "" || byName["auditor"] == "" {
		t.Fatalf("unexpected roles: %#v", byName)
	}

	t.Run("invalid_tenant_id", func(t *testing.T) {
		t.Parallel()
		_, err := svc.ListTenantRoles(context.Background(), &tenantv1.ListTenantRolesRequest{TenantId: "bad"})
		assertBusinessCode(t, err, codes.InvalidArgument, "VALIDATION_FAILED")
	})
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

	t.Run("expired_latest_updates_in_place", func(t *testing.T) {
		t.Parallel()
		invID := uuid.MustParse("33333333-3333-4333-8333-333333333333")
		core := &fakeTenantAdminCoreClient{matchID: userID}
		store := &fakeTenantAdminStore{
			latest: &ports.TenantAdminInvitation{
				ID:        invID,
				TenantID:  tenantID,
				UserID:    userID,
				TokenHash: "old-expired-hash",
				Status:    ports.InvitationStatusExpired,
				ExpireAt:  time.Now().UTC().Add(-time.Hour),
			},
		}
		audit := &fakeAuditStore{}
		svc := NewTenantAdminService(core, nil, store, audit)

		before := time.Now().UTC()
		res, err := svc.InviteTenantAdmin(context.Background(), &tenantv1.InviteTenantAdminRequest{
			TenantId:       tenantID.String(),
			Email:          "admin@acme.io",
			Username:       "acme_admin",
			IdempotencyKey: "550e8400-e29b-41d4-a716-446655440004",
		})
		if err != nil {
			t.Fatalf("InviteTenantAdmin: %v", err)
		}
		if res.GetId() != invID.String() {
			t.Fatalf("id=%s want reuse %s", res.GetId(), invID)
		}
		if store.insertCalls != 1 || store.updateCalls != 1 || len(store.invitations) != 0 {
			t.Fatalf("insertCalls=%d updateCalls=%d invitations=%d want update-via-insert", store.insertCalls, store.updateCalls, len(store.invitations))
		}
		if store.latest == nil || store.latest.Status != ports.InvitationStatusInviting {
			t.Fatalf("status=%v want inviting", store.latest)
		}
		sum := sha256.Sum256([]byte(res.GetToken()))
		wantHash := hex.EncodeToString(sum[:])
		if store.latest.TokenHash != wantHash || store.latest.TokenHash == "old-expired-hash" {
			t.Fatalf("token_hash=%q", store.latest.TokenHash)
		}
		expireAt := res.GetExpireAt().AsTime()
		if expireAt.Before(before.Add(71*time.Hour)) || expireAt.After(before.Add(73*time.Hour)) {
			t.Fatalf("expire_at=%v not ~72h", expireAt)
		}
		if len(audit.logs) != 1 || audit.logs[0].Result != "success" {
			t.Fatalf("audit=%+v", audit.logs)
		}
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

func TestTenantAdminService_ListAll(t *testing.T) {
	t.Parallel()
	tenantID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	adminID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	invitingID := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	expiredID := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	plainUserID := uuid.MustParse("55555555-5555-4555-8555-555555555555")
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	mk := func(id uuid.UUID, role, status, username string, created time.Time) ports.AdminWithTenant {
		return ports.AdminWithTenant{
			ID: id, Email: username + "@acme.io", Username: username, Role: role, Status: status,
			Source: "local", CreatedAt: &created,
			Tenant: ports.TenantRef{ID: tenantID, Name: "acme", DisplayName: "Acme"},
		}
	}
	mkWithSource := func(id uuid.UUID, role, status, username, source string, created time.Time) ports.AdminWithTenant {
		return ports.AdminWithTenant{
			ID: id, Email: username + "@acme.io", Username: username, Role: role, Status: status,
			Source: source, CreatedAt: &created,
			Tenant: ports.TenantRef{ID: tenantID, Name: "acme", DisplayName: "Acme"},
		}
	}
	admin := mk(adminID, ports.TenantAdminRoleAdmin, "active", "admin", now)
	invitingUser := mk(invitingID, ports.TenantAdminRoleUser, "active", "invitee", now.Add(-time.Hour))
	expiredUser := mk(expiredID, ports.TenantAdminRoleAuditor, "active", "expired", now.Add(-2*time.Hour))
	plainUser := mk(plainUserID, ports.TenantAdminRoleUser, "active", "plain", now.Add(-3*time.Hour))
	oidcAdmin := mkWithSource(uuid.MustParse("66666666-6666-4666-8666-666666666666"), ports.TenantAdminRoleAdmin, "active", "oidc:extuser", "third_party", now.Add(-4*time.Hour))

	t.Run("all_admins_and_inviting_expired", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{
			listItems: []ports.AdminWithTenant{admin},
			users: map[string]ports.AdminWithTenant{
				tenantUserKey(tenantID, invitingID):  invitingUser,
				tenantUserKey(tenantID, expiredID):   expiredUser,
				tenantUserKey(tenantID, plainUserID): plainUser,
			},
		}
		store := &fakeTenantAdminStore{flags: []ports.InvitationFlag{
			{TenantID: tenantID, UserID: invitingID, IsInviting: true},
			{TenantID: tenantID, UserID: expiredID, IsExpired: true},
		}}
		svc := NewTenantAdminService(core, nil, store, nil)
		res, err := svc.ListAllTenantAdmins(context.Background(), &tenantv1.ListAllTenantAdminsRequest{
			Page: &commonv1.CursorPageRequest{Limit: 20},
		})
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		if len(res.GetItems()) != 3 {
			t.Fatalf("items=%d want 3 (admin+inviting+expired), got %+v", len(res.Items), res.Items)
		}
		byID := map[string]*tenantv1.AdminWithTenant{}
		for _, it := range res.Items {
			byID[it.GetId()] = it
		}
		if byID[adminID.String()] == nil || byID[adminID.String()].GetRole() != "tenant-admin" {
			t.Fatalf("admin missing: %+v", byID[adminID.String()])
		}
		inv := byID[invitingID.String()]
		if inv == nil || inv.GetRole() != "user" || !inv.GetIsInviting() {
			t.Fatalf("inviting user=%+v", inv)
		}
		exp := byID[expiredID.String()]
		if exp == nil || exp.GetRole() != "auditor" || !exp.GetIsExpired() {
			t.Fatalf("expired user=%+v", exp)
		}
		if _, ok := byID[plainUserID.String()]; ok {
			t.Fatalf("plain user must not appear")
		}
	})

	t.Run("inviting_keeps_role", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{
			users: map[string]ports.AdminWithTenant{
				tenantUserKey(tenantID, invitingID): invitingUser,
			},
		}
		store := &fakeTenantAdminStore{flags: []ports.InvitationFlag{
			{TenantID: tenantID, UserID: invitingID, IsInviting: true},
		}}
		svc := NewTenantAdminService(core, nil, store, nil)
		res, err := svc.ListAllTenantAdmins(context.Background(), &tenantv1.ListAllTenantAdminsRequest{
			IsInviting: wrapperspb.Bool(true),
		})
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		if len(res.Items) != 1 || res.Items[0].GetRole() != "user" || !res.Items[0].GetIsInviting() {
			t.Fatalf("got %+v", res.Items)
		}
	})

	t.Run("expired_keeps_role", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{
			users: map[string]ports.AdminWithTenant{
				tenantUserKey(tenantID, expiredID): expiredUser,
			},
		}
		store := &fakeTenantAdminStore{flags: []ports.InvitationFlag{
			{TenantID: tenantID, UserID: expiredID, IsExpired: true},
		}}
		svc := NewTenantAdminService(core, nil, store, nil)
		res, err := svc.ListAllTenantAdmins(context.Background(), &tenantv1.ListAllTenantAdminsRequest{
			IsExpired: wrapperspb.Bool(true),
		})
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		if len(res.Items) != 1 || res.Items[0].GetRole() != "auditor" || !res.Items[0].GetIsExpired() {
			t.Fatalf("got %+v", res.Items)
		}
	})

	t.Run("filter_mutual_exclusion", func(t *testing.T) {
		t.Parallel()
		svc := NewTenantAdminService(&fakeTenantAdminCoreClient{}, nil, &fakeTenantAdminStore{}, nil)
		_, err := svc.ListAllTenantAdmins(context.Background(), &tenantv1.ListAllTenantAdminsRequest{
			Status:     "active",
			IsInviting: wrapperspb.Bool(true),
		})
		assertBusinessCode(t, err, codes.InvalidArgument, "VALIDATION_FAILED")
	})

	t.Run("role_filter", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{
			listItems: []ports.AdminWithTenant{admin},
			users: map[string]ports.AdminWithTenant{
				tenantUserKey(tenantID, expiredID): expiredUser,
			},
		}
		store := &fakeTenantAdminStore{flags: []ports.InvitationFlag{
			{TenantID: tenantID, UserID: expiredID, IsExpired: true},
		}}
		svc := NewTenantAdminService(core, nil, store, nil)
		res, err := svc.ListAllTenantAdmins(context.Background(), &tenantv1.ListAllTenantAdminsRequest{
			Role: "auditor",
		})
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		if len(res.Items) != 1 || res.Items[0].GetRole() != "auditor" {
			t.Fatalf("expected only auditor, got %+v", res.Items)
		}
	})

	t.Run("source_filter", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{
			listItems: []ports.AdminWithTenant{admin, oidcAdmin},
		}
		svc := NewTenantAdminService(core, nil, &fakeTenantAdminStore{}, nil)
		res, err := svc.ListAllTenantAdmins(context.Background(), &tenantv1.ListAllTenantAdminsRequest{
			Source: "third_party",
		})
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		if len(res.Items) != 1 || res.Items[0].GetSource() != "third_party" {
			t.Fatalf("expected only third_party, got %+v", res.Items)
		}
	})

	t.Run("invalid_role", func(t *testing.T) {
		t.Parallel()
		svc := NewTenantAdminService(&fakeTenantAdminCoreClient{}, nil, &fakeTenantAdminStore{}, nil)
		_, err := svc.ListAllTenantAdmins(context.Background(), &tenantv1.ListAllTenantAdminsRequest{
			Role: "superadmin",
		})
		assertBusinessCode(t, err, codes.InvalidArgument, "VALIDATION_FAILED")
	})

	t.Run("invalid_source", func(t *testing.T) {
		t.Parallel()
		svc := NewTenantAdminService(&fakeTenantAdminCoreClient{}, nil, &fakeTenantAdminStore{}, nil)
		_, err := svc.ListAllTenantAdmins(context.Background(), &tenantv1.ListAllTenantAdminsRequest{
			Source: "unknown",
		})
		assertBusinessCode(t, err, codes.InvalidArgument, "VALIDATION_FAILED")
	})

	t.Run("lazy_expire_stale_inviting", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{
			listItems: []ports.AdminWithTenant{admin},
			users: map[string]ports.AdminWithTenant{
				tenantUserKey(tenantID, invitingID): invitingUser,
			},
		}
		store := &fakeTenantAdminStore{
			latest: &ports.TenantAdminInvitation{
				ID:       uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
				TenantID: tenantID, UserID: invitingID,
				Status:   ports.InvitationStatusInviting,
				ExpireAt: time.Now().UTC().Add(-time.Hour),
			},
			flags: []ports.InvitationFlag{
				{TenantID: tenantID, UserID: invitingID, IsInviting: true},
			},
		}
		svc := NewTenantAdminService(core, nil, store, nil)
		res, err := svc.ListAllTenantAdmins(context.Background(), &tenantv1.ListAllTenantAdminsRequest{
			Page: &commonv1.CursorPageRequest{Limit: 20},
		})
		if err != nil {
			t.Fatalf("ListAll: %v", err)
		}
		if store.latest.Status != ports.InvitationStatusExpired {
			t.Fatalf("latest status=%q want expired", store.latest.Status)
		}
		byID := map[string]*tenantv1.AdminWithTenant{}
		for _, it := range res.Items {
			byID[it.GetId()] = it
		}
		got := byID[invitingID.String()]
		if got == nil || got.GetIsInviting() || !got.GetIsExpired() {
			t.Fatalf("want expired flag after lazy expire, got %+v", got)
		}
	})
}

func TestTenantAdminService_GetDetail(t *testing.T) {
	t.Parallel()
	tenantID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	userID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	display := "Acme Admin"
	lastLogin := now.Add(-time.Hour)
	coreUser := ports.AdminWithTenant{
		ID: userID, Email: "a@acme.io", Username: "acme_admin", DisplayName: &display,
		Role: ports.TenantAdminRoleAdmin, Status: "active", Source: "local",
		LastLoginAt: &lastLogin, CreatedAt: &now, UpdatedAt: &now,
		Tenant: ports.TenantRef{ID: tenantID, Name: "acme", DisplayName: "Acme"},
	}

	t.Run("full_fields_with_inviting", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{
			users: map[string]ports.AdminWithTenant{
				tenantUserKey(tenantID, userID): coreUser,
			},
		}
		store := &fakeTenantAdminStore{
			flags: []ports.InvitationFlag{{
				TenantID: tenantID, UserID: userID, IsInviting: true, IsExpired: false,
			}},
		}
		svc := NewTenantAdminService(core, nil, store, nil)
		res, err := svc.GetTenantAdminDetail(context.Background(), &tenantv1.GetTenantAdminDetailRequest{
			TenantId: tenantID.String(),
			UserId:   userID.String(),
		})
		if err != nil {
			t.Fatalf("GetTenantAdminDetail: %v", err)
		}
		if res.GetId() != userID.String() || res.GetEmail() != "a@acme.io" || res.GetUsername() != "acme_admin" {
			t.Fatalf("identity=%+v", res)
		}
		if res.GetDisplayName() == nil || res.GetDisplayName().GetValue() != display {
			t.Fatalf("display_name=%v", res.GetDisplayName())
		}
		if res.GetRole() != "tenant-admin" || res.GetStatus() != "active" || res.GetSource() != "local" {
			t.Fatalf("role/status/source=%s/%s/%s", res.GetRole(), res.GetStatus(), res.GetSource())
		}
		if !res.GetIsInviting() || res.GetIsExpired() {
			t.Fatalf("flags inviting=%v expired=%v", res.GetIsInviting(), res.GetIsExpired())
		}
		if res.GetCreatedAt() == nil || res.GetUpdatedAt() == nil || res.GetLastLoginAt() == nil {
			t.Fatalf("timestamps missing created=%v updated=%v last=%v", res.GetCreatedAt(), res.GetUpdatedAt(), res.GetLastLoginAt())
		}
		if res.GetTenant() == nil || res.GetTenant().GetId() != tenantID.String() || res.GetTenant().GetName() != "acme" {
			t.Fatalf("tenant=%+v", res.GetTenant())
		}
	})

	t.Run("latest_expired_flag", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{
			users: map[string]ports.AdminWithTenant{
				tenantUserKey(tenantID, userID): coreUser,
			},
		}
		store := &fakeTenantAdminStore{
			latest: &ports.TenantAdminInvitation{
				TenantID: tenantID, UserID: userID, Status: ports.InvitationStatusExpired,
			},
		}
		svc := NewTenantAdminService(core, nil, store, nil)
		res, err := svc.GetTenantAdminDetail(context.Background(), &tenantv1.GetTenantAdminDetailRequest{
			TenantId: tenantID.String(),
			UserId:   userID.String(),
		})
		if err != nil {
			t.Fatalf("GetTenantAdminDetail: %v", err)
		}
		if res.GetIsInviting() || !res.GetIsExpired() {
			t.Fatalf("want expired only, got inviting=%v expired=%v", res.GetIsInviting(), res.GetIsExpired())
		}
	})

	t.Run("latest_settled_both_false", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{
			users: map[string]ports.AdminWithTenant{
				tenantUserKey(tenantID, userID): coreUser,
			},
		}
		store := &fakeTenantAdminStore{
			latest: &ports.TenantAdminInvitation{
				TenantID: tenantID, UserID: userID, Status: ports.InvitationStatusAccepted,
			},
		}
		svc := NewTenantAdminService(core, nil, store, nil)
		res, err := svc.GetTenantAdminDetail(context.Background(), &tenantv1.GetTenantAdminDetailRequest{
			TenantId: tenantID.String(),
			UserId:   userID.String(),
		})
		if err != nil {
			t.Fatalf("GetTenantAdminDetail: %v", err)
		}
		if res.GetIsInviting() || res.GetIsExpired() {
			t.Fatalf("want both false, got inviting=%v expired=%v", res.GetIsInviting(), res.GetIsExpired())
		}
	})

	t.Run("lazy_expire_stale_inviting", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{
			users: map[string]ports.AdminWithTenant{
				tenantUserKey(tenantID, userID): coreUser,
			},
		}
		store := &fakeTenantAdminStore{
			latest: &ports.TenantAdminInvitation{
				ID:       uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
				TenantID: tenantID, UserID: userID,
				Status:   ports.InvitationStatusInviting,
				ExpireAt: time.Now().UTC().Add(-time.Minute),
			},
		}
		svc := NewTenantAdminService(core, nil, store, nil)
		res, err := svc.GetTenantAdminDetail(context.Background(), &tenantv1.GetTenantAdminDetailRequest{
			TenantId: tenantID.String(),
			UserId:   userID.String(),
		})
		if err != nil {
			t.Fatalf("GetTenantAdminDetail: %v", err)
		}
		if store.latest.Status != ports.InvitationStatusExpired {
			t.Fatalf("latest status=%q want expired", store.latest.Status)
		}
		if res.GetIsInviting() || !res.GetIsExpired() {
			t.Fatalf("want expired only after lazy expire, got inviting=%v expired=%v", res.GetIsInviting(), res.GetIsExpired())
		}
	})

	t.Run("not_found", func(t *testing.T) {
		t.Parallel()
		svc := NewTenantAdminService(&fakeTenantAdminCoreClient{}, nil, &fakeTenantAdminStore{}, nil)
		_, err := svc.GetTenantAdminDetail(context.Background(), &tenantv1.GetTenantAdminDetailRequest{
			TenantId: tenantID.String(),
			UserId:   userID.String(),
		})
		assertBusinessCode(t, err, codes.NotFound, "TENANT_ADMIN_NOT_FOUND")
	})
}

func TestTenantAdminService_ChangeRole(t *testing.T) {
	t.Parallel()
	tenantID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	userID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	roleID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	idem := "550e8400-e29b-41d4-a716-446655440099"

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		tid := tenantID
		oldRoleID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
		core := &fakeTenantAdminCoreClient{
			perms: &ports.UserPermissions{
				UserID: userID, TenantID: &tid, RoleID: oldRoleID, Role: "user", Permissions: []any{},
			},
		}
		audit := &fakeAuditStore{}
		svc := NewTenantAdminService(core, nil, nil, audit)
		res, err := svc.UpdateTenantAdminRole(context.Background(), &tenantv1.UpdateTenantAdminRoleRequest{
			TenantId: tenantID.String(), UserId: userID.String(), RoleId: roleID.String(), IdempotencyKey: idem,
		})
		if err != nil {
			t.Fatalf("UpdateTenantAdminRole: %v", err)
		}
		if res.GetId() != userID.String() || res.GetMessage() != "role updated" {
			t.Fatalf("result=%+v", res)
		}
		if core.lastChangeRoleID != roleID {
			t.Fatalf("role_id forwarded=%s want %s", core.lastChangeRoleID, roleID)
		}
		if len(audit.logs) != 1 || audit.logs[0].Action != "tenant_admin.change_role" {
			t.Fatalf("audit=%+v", audit.logs)
		}
		if audit.logs[0].Details["old_role"] != "user" {
			t.Fatalf("old_role=%v", audit.logs[0].Details["old_role"])
		}
		if audit.logs[0].Details["new_role"] != "tenant-admin" {
			t.Fatalf("new_role=%v", audit.logs[0].Details["new_role"])
		}
	})

	t.Run("invalid_role_id", func(t *testing.T) {
		t.Parallel()
		tid := tenantID
		oldRID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
		core := &fakeTenantAdminCoreClient{
			perms:         &ports.UserPermissions{UserID: userID, TenantID: &tid, RoleID: oldRID, Role: "user", Permissions: []any{}},
			changeRoleErr: ports.ErrRoleChangeInvalid,
		}
		audit := &fakeAuditStore{}
		svc := NewTenantAdminService(core, nil, nil, audit)
		_, err := svc.UpdateTenantAdminRole(context.Background(), &tenantv1.UpdateTenantAdminRoleRequest{
			TenantId: tenantID.String(), UserId: userID.String(), RoleId: roleID.String(), IdempotencyKey: idem,
		})
		assertBusinessCode(t, err, codes.FailedPrecondition, "ROLE_CHANGE_INVALID")
		assertFailureAudit(t, audit, "tenant_admin.change_role")
	})

	t.Run("platform_role_rejected", func(t *testing.T) {
		t.Parallel()
		// Core 对 platform-* / tenant 不匹配统一返回 ErrRoleChangeInvalid
		tid := tenantID
		oldRID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
		platformRoleID := uuid.MustParse("00000000-0000-0000-0000-000000000099")
		core := &fakeTenantAdminCoreClient{
			perms:         &ports.UserPermissions{UserID: userID, TenantID: &tid, RoleID: oldRID, Role: "user", Permissions: []any{}},
			changeRoleErr: ports.ErrRoleChangeInvalid,
		}
		audit := &fakeAuditStore{}
		svc := NewTenantAdminService(core, nil, nil, audit)
		_, err := svc.UpdateTenantAdminRole(context.Background(), &tenantv1.UpdateTenantAdminRoleRequest{
			TenantId: tenantID.String(), UserId: userID.String(), RoleId: platformRoleID.String(), IdempotencyKey: idem,
		})
		assertBusinessCode(t, err, codes.FailedPrecondition, "ROLE_CHANGE_INVALID")
		assertFailureAudit(t, audit, "tenant_admin.change_role")
	})
}

func TestTenantAdminService_GetRolePermissions(t *testing.T) {
	t.Parallel()
	tenantID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	userID := uuid.MustParse("22222222-2222-4222-8222-222222222222")

	t.Run("tenant_member", func(t *testing.T) {
		t.Parallel()
		tid := tenantID
		roleID := uuid.MustParse("33333333-3333-4333-8333-333333333333")
		core := &fakeTenantAdminCoreClient{
			perms: &ports.UserPermissions{
				UserID: userID, TenantID: &tid, RoleID: roleID, Role: "tenant-admin",
				Permissions: []any{map[string]any{"resource": "*", "actions": []any{"*"}, "scope": "tenant"}},
			},
		}
		svc := NewTenantAdminService(core, nil, nil, nil)
		res, err := svc.GetTenantAdminRole(context.Background(), &tenantv1.GetTenantAdminRoleRequest{
			TenantId: tenantID.String(), UserId: userID.String(),
		})
		if err != nil {
			t.Fatalf("GetTenantAdminRole: %v", err)
		}
		if res.GetUserId() != userID.String() || res.GetRole() != "tenant-admin" {
			t.Fatalf("got %+v", res)
		}
		if res.GetRoleId() != roleID.String() {
			t.Fatalf("role_id=%v want %s", res.GetRoleId(), roleID.String())
		}
		if res.GetTenantId() == nil || res.GetTenantId().GetValue() != tenantID.String() {
			t.Fatalf("tenant_id=%v", res.GetTenantId())
		}
		if res.GetPermissions() == nil || len(res.GetPermissions().GetValues()) != 1 {
			t.Fatalf("permissions=%v", res.GetPermissions())
		}
	})

	t.Run("platform_account_rejected", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{
			permsErr: ports.ErrTenantAdminNotFound,
		}
		svc := NewTenantAdminService(core, nil, nil, nil)
		_, err := svc.GetTenantAdminRole(context.Background(), &tenantv1.GetTenantAdminRoleRequest{
			TenantId: tenantID.String(), UserId: userID.String(),
		})
		assertBusinessCode(t, err, codes.NotFound, "TENANT_ADMIN_NOT_FOUND")
	})
}

func TestTenantAdminService_ResetPassword(t *testing.T) {
	t.Parallel()
	tenantID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	userID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	validPassword := "NewP@ssw0rd"

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{}
		audit := &fakeAuditStore{}
		svc := NewTenantAdminService(core, nil, nil, audit)
		res, err := svc.ResetTenantAdminPassword(context.Background(), &tenantv1.ResetTenantAdminPasswordRequest{
			TenantId: tenantID.String(), UserId: userID.String(), NewPassword: validPassword,
		})
		if err != nil {
			t.Fatalf("ResetTenantAdminPassword: %v", err)
		}
		if res.GetId() != userID.String() || res.GetMessage() != "password reset" {
			t.Fatalf("result=%+v", res)
		}
		if len(audit.logs) != 1 || audit.logs[0].Action != "tenant_admin.reset_password" {
			t.Fatalf("audit=%+v", audit.logs)
		}
		if _, ok := audit.logs[0].Details["new_password"]; ok {
			t.Fatalf("password must not appear in audit: %+v", audit.logs[0].Details)
		}
	})

	t.Run("same_as_old", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{resetPasswordErr: ports.ErrPasswordSameAsOld}
		audit := &fakeAuditStore{}
		svc := NewTenantAdminService(core, nil, nil, audit)
		_, err := svc.ResetTenantAdminPassword(context.Background(), &tenantv1.ResetTenantAdminPasswordRequest{
			TenantId: tenantID.String(), UserId: userID.String(), NewPassword: validPassword,
		})
		assertBusinessCode(t, err, codes.FailedPrecondition, "PASSWORD_SAME_AS_OLD")
		assertFailureAudit(t, audit, "tenant_admin.reset_password")
	})

	t.Run("complexity_invalid", func(t *testing.T) {
		t.Parallel()
		svc := NewTenantAdminService(&fakeTenantAdminCoreClient{}, nil, nil, nil)
		_, err := svc.ResetTenantAdminPassword(context.Background(), &tenantv1.ResetTenantAdminPasswordRequest{
			TenantId: tenantID.String(), UserId: userID.String(), NewPassword: "short1",
		})
		assertBusinessCode(t, err, codes.InvalidArgument, "VALIDATION_FAILED")
	})

	t.Run("soft_deleted_not_found", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{resetPasswordErr: ports.ErrTenantAdminNotFound}
		svc := NewTenantAdminService(core, nil, nil, nil)
		_, err := svc.ResetTenantAdminPassword(context.Background(), &tenantv1.ResetTenantAdminPasswordRequest{
			TenantId: tenantID.String(), UserId: userID.String(), NewPassword: validPassword,
		})
		assertBusinessCode(t, err, codes.NotFound, "TENANT_ADMIN_NOT_FOUND")
	})

	t.Run("disabled_user_allowed", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{
			users: map[string]ports.AdminWithTenant{
				tenantUserKey(tenantID, userID): {
					ID: userID, Tenant: ports.TenantRef{ID: tenantID}, Status: "disabled",
				},
			},
		}
		svc := NewTenantAdminService(core, nil, nil, nil)
		res, err := svc.ResetTenantAdminPassword(context.Background(), &tenantv1.ResetTenantAdminPasswordRequest{
			TenantId: tenantID.String(), UserId: userID.String(), NewPassword: validPassword,
		})
		if err != nil {
			t.Fatalf("ResetTenantAdminPassword disabled user: %v", err)
		}
		if res.GetMessage() != "password reset" {
			t.Fatalf("result=%+v", res)
		}
	})
}

func TestTenantAdminService_DisableEnableDelete(t *testing.T) {
	t.Parallel()
	tenantID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	userID := uuid.MustParse("22222222-2222-4222-8222-222222222222")

	t.Run("disable", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{
			users: map[string]ports.AdminWithTenant{
				tenantUserKey(tenantID, userID): {ID: userID, Tenant: ports.TenantRef{ID: tenantID}, Status: "active"},
			},
		}
		audit := &fakeAuditStore{}
		svc := NewTenantAdminService(core, nil, nil, audit)
		res, err := svc.DisableTenantAdmin(context.Background(), &tenantv1.DisableTenantAdminRequest{
			TenantId: tenantID.String(), UserId: userID.String(),
		})
		if err != nil {
			t.Fatalf("DisableTenantAdmin: %v", err)
		}
		if res.GetId() != userID.String() || res.GetMessage() != "admin disabled" {
			t.Fatalf("result=%+v", res)
		}
		if core.lastStatus != "disabled" {
			t.Fatalf("status=%q want disabled", core.lastStatus)
		}
		if len(audit.logs) != 1 || audit.logs[0].Action != "tenant_admin.disable" {
			t.Fatalf("audit=%+v", audit.logs)
		}
	})

	t.Run("enable", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{
			users: map[string]ports.AdminWithTenant{
				tenantUserKey(tenantID, userID): {ID: userID, Tenant: ports.TenantRef{ID: tenantID}, Status: "disabled"},
			},
		}
		audit := &fakeAuditStore{}
		svc := NewTenantAdminService(core, nil, nil, audit)
		res, err := svc.EnableTenantAdmin(context.Background(), &tenantv1.EnableTenantAdminRequest{
			TenantId: tenantID.String(), UserId: userID.String(),
		})
		if err != nil {
			t.Fatalf("EnableTenantAdmin: %v", err)
		}
		if res.GetMessage() != "admin enabled" {
			t.Fatalf("result=%+v", res)
		}
		if core.lastStatus != "active" {
			t.Fatalf("status=%q want active", core.lastStatus)
		}
		if len(audit.logs) != 1 || audit.logs[0].Action != "tenant_admin.enable" {
			t.Fatalf("audit=%+v", audit.logs)
		}
	})

	t.Run("already_disabled", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{
			users: map[string]ports.AdminWithTenant{
				tenantUserKey(tenantID, userID): {ID: userID, Tenant: ports.TenantRef{ID: tenantID}, Status: "disabled"},
			},
		}
		svc := NewTenantAdminService(core, nil, nil, nil)
		_, err := svc.DisableTenantAdmin(context.Background(), &tenantv1.DisableTenantAdminRequest{
			TenantId: tenantID.String(), UserId: userID.String(),
		})
		assertBusinessCode(t, err, codes.FailedPrecondition, "USER_STATE_INVALID")
	})

	t.Run("already_active", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{
			users: map[string]ports.AdminWithTenant{
				tenantUserKey(tenantID, userID): {ID: userID, Tenant: ports.TenantRef{ID: tenantID}, Status: "active"},
			},
		}
		svc := NewTenantAdminService(core, nil, nil, nil)
		_, err := svc.EnableTenantAdmin(context.Background(), &tenantv1.EnableTenantAdminRequest{
			TenantId: tenantID.String(), UserId: userID.String(),
		})
		assertBusinessCode(t, err, codes.FailedPrecondition, "USER_STATE_INVALID")
	})

	t.Run("delete_soft", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{}
		audit := &fakeAuditStore{}
		svc := NewTenantAdminService(core, nil, nil, audit)
		res, err := svc.DeleteTenantAdmin(context.Background(), &tenantv1.DeleteTenantAdminRequest{
			TenantId: tenantID.String(), UserId: userID.String(),
		})
		if err != nil {
			t.Fatalf("DeleteTenantAdmin: %v", err)
		}
		if res.GetMessage() != "admin deleted" {
			t.Fatalf("result=%+v", res)
		}
		if !core.softDeleted {
			t.Fatal("SoftDelete not called")
		}
		if len(audit.logs) != 1 || audit.logs[0].Action != "tenant_admin.delete" {
			t.Fatalf("audit=%+v", audit.logs)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		t.Parallel()
		core := &fakeTenantAdminCoreClient{setStatusErr: ports.ErrTenantAdminNotFound}
		svc := NewTenantAdminService(core, nil, nil, nil)
		_, err := svc.DisableTenantAdmin(context.Background(), &tenantv1.DisableTenantAdminRequest{
			TenantId: tenantID.String(), UserId: userID.String(),
		})
		assertBusinessCode(t, err, codes.NotFound, "TENANT_ADMIN_NOT_FOUND")
	})
}

func TestTenantAdminService_ListAuditLogs(t *testing.T) {
	t.Parallel()
	tenantID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	userID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	actorID := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	logID := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)

	t.Run("list_and_filter", func(t *testing.T) {
		t.Parallel()
		tid := tenantID
		audit := &fakeAuditStore{
			logs: []ports.AuditLog{
				{
					ID: logID, Action: "tenant_admin.invite", Resource: "tenant_admin",
					Result: "success", UserID: &actorID, TenantID: &tid, CreatedAt: now,
					Details: map[string]any{"target_id": userID.String(), "tenant_id": tenantID.String()},
				},
				{
					ID:     uuid.MustParse("55555555-5555-4555-8555-555555555555"),
					Action: "tenant_admin.disable", Resource: "tenant_admin",
					Result: "failure", UserID: &actorID, TenantID: &tid, CreatedAt: now.Add(-time.Minute),
					Details: map[string]any{"target_id": userID.String(), "tenant_id": tenantID.String()},
				},
			},
		}
		svc := NewTenantAdminService(nil, nil, nil, audit)
		res, err := svc.ListTenantAdminAuditLogs(context.Background(), &tenantv1.ListTenantAdminAuditLogsRequest{
			TenantId: tenantID.String(), UserId: userID.String(),
			Page: &commonv1.CursorPageRequest{Limit: 20},
		})
		if err != nil {
			t.Fatalf("ListTenantAdminAuditLogs: %v", err)
		}
		if len(res.GetItems()) != 2 {
			t.Fatalf("items=%d", len(res.GetItems()))
		}
		first := res.GetItems()[0]
		if first.GetAction() != "tenant_admin.invite" || first.GetResource() != "tenant_admin" || first.GetResult() != "success" {
			t.Fatalf("first=%+v", first)
		}
		if first.GetUserId() == nil || first.GetUserId().GetValue() != actorID.String() {
			t.Fatalf("user_id=%v", first.GetUserId())
		}
		if first.GetDetails() == nil || first.GetDetails().AsMap()["target_id"] != userID.String() {
			t.Fatalf("details=%v", first.GetDetails())
		}

		filtered, err := svc.ListTenantAdminAuditLogs(context.Background(), &tenantv1.ListTenantAdminAuditLogsRequest{
			TenantId: tenantID.String(), UserId: userID.String(),
			Action: "tenant_admin.disable", Result: "failure",
		})
		if err != nil {
			t.Fatalf("filtered: %v", err)
		}
		if len(filtered.GetItems()) != 1 || filtered.GetItems()[0].GetResult() != "failure" {
			t.Fatalf("filtered=%+v", filtered.GetItems())
		}
	})

	t.Run("invite_then_audit_exists", func(t *testing.T) {
		t.Parallel()
		audit := &fakeAuditStore{}
		core := &fakeTenantAdminCoreClient{matchID: userID}
		store := &fakeTenantAdminStore{}
		svc := NewTenantAdminService(core, nil, store, audit)
		_, err := svc.InviteTenantAdmin(context.Background(), &tenantv1.InviteTenantAdminRequest{
			TenantId: tenantID.String(), Email: "a@acme.io", Username: "acme_admin",
			IdempotencyKey: "550e8400-e29b-41d4-a716-446655440014",
		})
		if err != nil {
			t.Fatalf("InviteTenantAdmin: %v", err)
		}
		if len(audit.logs) != 1 || audit.logs[0].Action != "tenant_admin.invite" {
			t.Fatalf("audit=%+v", audit.logs)
		}
		target, _ := audit.logs[0].Details["target_id"].(string)
		if target != userID.String() {
			t.Fatalf("target_id=%q", target)
		}
		audit.logs[0].ID = uuid.New()
		audit.logs[0].CreatedAt = now
		listed, err := svc.ListTenantAdminAuditLogs(context.Background(), &tenantv1.ListTenantAdminAuditLogsRequest{
			TenantId: tenantID.String(), UserId: userID.String(),
		})
		if err != nil {
			t.Fatalf("ListTenantAdminAuditLogs: %v", err)
		}
		if len(listed.GetItems()) != 1 || listed.GetItems()[0].GetAction() != "tenant_admin.invite" {
			t.Fatalf("listed=%+v", listed.GetItems())
		}
	})
}
