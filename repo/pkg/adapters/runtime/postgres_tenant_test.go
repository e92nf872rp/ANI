package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestPostgresTenantGetTenant(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	planID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(5 * time.Minute)
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{
		values: []any{
			tenantID, "acme", "Acme", "active", planID, "ops@acme.io",
			(*time.Time)(nil), (*time.Time)(nil), createdAt, updatedAt,
			int64(3), int64(1), true, false,
		},
	})
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})

	got, err := svc.GetTenant(context.Background(), tenantID.String())
	if err != nil {
		t.Fatalf("GetTenant: %v", err)
	}
	if got.ID != tenantID.String() || got.PlanID != planID.String() {
		t.Fatalf("tenant=%+v", got)
	}
	if got.DisplayName != "Acme" || got.Status != ports.TenantStatusActive || got.ContactEmail != "ops@acme.io" {
		t.Fatalf("tenant fields=%+v", got)
	}
	if got.UserCount != 3 || got.AdminCount != 1 {
		t.Fatalf("counts user=%d admin=%d", got.UserCount, got.AdminCount)
	}
	if got.Auth == nil || !got.Auth.SsoEnabled || got.Auth.MfaRequired {
		t.Fatalf("auth=%+v", got.Auth)
	}
}

func TestPostgresTenantGetTenantAuthDefaults(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	planID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{
		values: []any{
			tenantID, "acme", "Acme", "active", planID, nil,
			(*time.Time)(nil), (*time.Time)(nil), now, now,
			int64(0), int64(0), false, false,
		},
	})
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})
	got, err := svc.GetTenant(context.Background(), tenantID.String())
	if err != nil {
		t.Fatalf("GetTenant: %v", err)
	}
	if got.Auth == nil || got.Auth.SsoEnabled || got.Auth.MfaRequired {
		t.Fatalf("auth defaults=%+v", got.Auth)
	}
}

func TestPostgresTenantListTenants(t *testing.T) {
	id1 := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	id2 := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	planID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	t1 := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 9, 3, 11, 0, 0, 0, time.UTC)
	tx := &quotaFakeTx{}
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{id1, "acme", "Acme", "active", planID, t1, int64(2)}},
		{values: []any{id2, "beta", "Beta", "frozen", planID, t2, int64(1)}},
	}})
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})

	got, err := svc.ListTenants(context.Background(), ports.ListTenantsFilter{Limit: 20})
	if err != nil {
		t.Fatalf("ListTenants: %v", err)
	}
	if len(got.Items) != 2 || got.NextCursor != "" {
		t.Fatalf("got=%+v", got)
	}
	if got.Items[0].AdminCount != 2 || got.Items[0].Name != "acme" {
		t.Fatalf("item0=%+v", got.Items[0])
	}
}

func TestPostgresTenantListTenantsInvalidStatus(t *testing.T) {
	svc := NewPostgresTenant(&quotaFakeStore{tx: &quotaFakeTx{}})
	_, err := svc.ListTenants(context.Background(), ports.ListTenantsFilter{Status: "suspended"})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestPostgresTenantGetTenantInvalidID(t *testing.T) {
	svc := NewPostgresTenant(&quotaFakeStore{tx: &quotaFakeTx{}})
	_, err := svc.GetTenant(context.Background(), "not-a-uuid")
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestPostgresTenantGetTenantNotFound(t *testing.T) {
	svc := NewPostgresTenant(&quotaFakeStore{tx: &quotaFakeTx{}})
	tx := svc.store.(*quotaFakeStore).tx
	tx.enqueueRows(quotaFakeRow{err: pgx.ErrNoRows})
	_, err := svc.GetTenant(context.Background(), uuid.NewString())
	if !errors.Is(err, ports.ErrTenantNotFound) {
		t.Fatalf("want ErrTenantNotFound, got %v", err)
	}
}

func TestPostgresTenantListAvailableTenants(t *testing.T) {
	id1 := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	id2 := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	tx := &quotaFakeTx{}
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{id1, "acme", "Acme", "active"}},
		{values: []any{id2, "beta", "Beta", "frozen"}},
	}})
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})

	got, err := svc.ListAvailableTenants(context.Background())
	if err != nil {
		t.Fatalf("ListAvailableTenants: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].ID != id1.String() || got[0].Status != ports.TenantStatusActive || got[0].DisplayName != "Acme" {
		t.Fatalf("got[0]=%+v", got[0])
	}
	if got[1].ID != id2.String() || got[1].Status != ports.TenantStatusFrozen {
		t.Fatalf("got[1]=%+v", got[1])
	}
}

func TestPostgresTenantListAvailableTenantsEmpty(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueQuery(&quotaFakeRows{})
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})
	got, err := svc.ListAvailableTenants(context.Background())
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len=%d", len(got))
	}
}

func TestPostgresTenantCreateTenant_TransactionInserts(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	planID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	userID := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	roleID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	createdAt := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{false}}, // name 不存在
		quotaFakeRow{values: []any{tenantID, "acme", "Acme", "active", planID, "ops@acme.io", (*time.Time)(nil), (*time.Time)(nil), createdAt, createdAt}},
		quotaFakeRow{values: []any{userID}},
		quotaFakeRow{values: []any{roleID}},
	)
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})

	ctx := WithTenantLifecycleAttribution(context.Background(), "req-1", "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	got, err := svc.CreateTenant(ctx, ports.CreateTenantInput{
		Name: "acme", DisplayName: "Acme", ContactEmail: "ops@acme.io", PlanID: planID.String(),
		AdminEmail: "admin@acme.io", AdminName: "acme_admin", AdminPasswordHash: "$2a$12$hash",
	})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if got.ID != tenantID.String() || got.Status != ports.TenantStatusActive || got.AdminCount != 1 {
		t.Fatalf("got=%+v", got)
	}
	joined := joinExecs(tx)
	for _, want := range []string{
		"INSERT INTO tenant_auth",
		"INSERT INTO user_roles",
		"INSERT INTO tenant_lifecycle",
	} {
		if !hasExec(tx, want) {
			t.Fatalf("missing %s in:\n%s", want, joined)
		}
	}
	if len(tx.execSQLs) < 3 {
		t.Fatalf("exec count=%d", len(tx.execSQLs))
	}
}

func TestPostgresTenantCreateTenant_NameConflict(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{true}}) // name 已存在
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})
	_, err := svc.CreateTenant(context.Background(), ports.CreateTenantInput{
		Name: "acme", DisplayName: "Acme", ContactEmail: "ops@acme.io",
		PlanID: uuid.NewString(), AdminEmail: "a@a.io", AdminName: "admin", AdminPasswordHash: "hash",
	})
	if !errors.Is(err, ports.ErrTenantNameConflict) {
		t.Fatalf("want ErrTenantNameConflict, got %v", err)
	}
}

func TestPostgresTenantCreateTenant_NameConflictRace(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{false}}, // 预查未命中
		quotaFakeRow{err: &pgconn.PgError{Code: "23505"}}, // INSERT 撞 UNIQUE
	)
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})
	_, err := svc.CreateTenant(context.Background(), ports.CreateTenantInput{
		Name: "acme", DisplayName: "Acme", ContactEmail: "ops@acme.io",
		PlanID: uuid.NewString(), AdminEmail: "a@a.io", AdminName: "admin", AdminPasswordHash: "hash",
	})
	if !errors.Is(err, ports.ErrTenantNameConflict) {
		t.Fatalf("want ErrTenantNameConflict, got %v", err)
	}
}

func TestPostgresTenantUpdateTenant_PartialFields(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	planID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	createdAt := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{tenantID}}, // UPDATE RETURNING id
		quotaFakeRow{values: []any{ // GetTenant detail
			tenantID, "acme", "Acme Corp", "active", planID, "new@acme.io",
			(*time.Time)(nil), (*time.Time)(nil), createdAt, updatedAt,
			int64(2), int64(1), false, true,
		}},
	)
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})
	dn := "Acme Corp"
	email := "new@acme.io"
	got, err := svc.UpdateTenant(context.Background(), tenantID.String(), ports.UpdateTenantInput{
		DisplayName: &dn, ContactEmail: &email,
	})
	if err != nil {
		t.Fatalf("UpdateTenant: %v", err)
	}
	if got.DisplayName != "Acme Corp" || got.ContactEmail != "new@acme.io" {
		t.Fatalf("got=%+v", got)
	}
	if got.Name != "acme" || got.Status != ports.TenantStatusActive || got.PlanID != planID.String() {
		t.Fatalf("name/status/plan must stay unchanged: %+v", got)
	}
	if got.UserCount != 2 || got.AdminCount != 1 || got.Auth == nil || !got.Auth.MfaRequired {
		t.Fatalf("detail view=%+v", got)
	}
}

func TestPostgresTenantUpdateTenant_DisplayNameOnly(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	planID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{tenantID}},
		quotaFakeRow{values: []any{
			tenantID, "acme", "Only Name", "frozen", planID, "ops@acme.io",
			(*time.Time)(nil), (*time.Time)(nil), now, now,
			int64(0), int64(0), false, false,
		}},
	)
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})
	dn := "Only Name"
	got, err := svc.UpdateTenant(context.Background(), tenantID.String(), ports.UpdateTenantInput{DisplayName: &dn})
	if err != nil {
		t.Fatalf("UpdateTenant: %v", err)
	}
	if got.DisplayName != "Only Name" || got.ContactEmail != "ops@acme.io" || got.Status != ports.TenantStatusFrozen {
		t.Fatalf("got=%+v", got)
	}
}

func TestPostgresTenantUpdateTenant_EmptyRejected(t *testing.T) {
	svc := NewPostgresTenant(&quotaFakeStore{tx: &quotaFakeTx{}})
	_, err := svc.UpdateTenant(context.Background(), uuid.NewString(), ports.UpdateTenantInput{})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestPostgresTenantUpdateTenant_NotFound(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{err: pgx.ErrNoRows})
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})
	dn := "X"
	_, err := svc.UpdateTenant(context.Background(), uuid.NewString(), ports.UpdateTenantInput{DisplayName: &dn})
	if !errors.Is(err, ports.ErrTenantNotFound) {
		t.Fatalf("want ErrTenantNotFound, got %v", err)
	}
}

func TestPostgresTenantUpdateTenant_DisabledAsNotFound(t *testing.T) {
	// disabled 命中 WHERE status <> 'disabled' → 0 行，与不存在同为 NotFound
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{err: pgx.ErrNoRows})
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})
	dn := "Nope"
	_, err := svc.UpdateTenant(context.Background(), uuid.NewString(), ports.UpdateTenantInput{DisplayName: &dn})
	if !errors.Is(err, ports.ErrTenantNotFound) {
		t.Fatalf("want ErrTenantNotFound, got %v", err)
	}
}

func TestPostgresTenantFreezeTenant_Success(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	planID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	frozenAt := now
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{tenantID}}, // UPDATE RETURNING
		quotaFakeRow{values: []any{ // loadTenantDetail
			tenantID, "acme", "Acme", "frozen", planID, "ops@acme.io",
			&frozenAt, (*time.Time)(nil), now, now,
			int64(1), int64(1), false, false,
		}},
	)
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})
	ctx := WithTenantLifecycleAttribution(context.Background(), "req-freeze-1", "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	got, err := svc.FreezeTenant(ctx, tenantID.String())
	if err != nil {
		t.Fatalf("FreezeTenant: %v", err)
	}
	if got.Status != ports.TenantStatusFrozen {
		t.Fatalf("status=%s", got.Status)
	}
	if !hasExec(tx, "INSERT INTO tenant_lifecycle") {
		t.Fatal("missing lifecycle insert")
	}
}

func TestPostgresTenantFreezeTenant_StateInvalid(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{err: pgx.ErrNoRows},     // UPDATE miss
		quotaFakeRow{values: []any{"frozen"}}, // current status
	)
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})
	_, err := svc.FreezeTenant(WithTenantLifecycleAttribution(context.Background(), "r1", ""), tenantID.String())
	if !errors.Is(err, ports.ErrTenantStateInvalid) {
		t.Fatalf("want ErrTenantStateInvalid, got %v", err)
	}
}

func TestPostgresTenantUnfreezeTenant_ClearsFrozen(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	planID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{tenantID}},
		quotaFakeRow{values: []any{
			tenantID, "acme", "Acme", "active", planID, "ops@acme.io",
			(*time.Time)(nil), (*time.Time)(nil), now, now,
			int64(0), int64(0), false, false,
		}},
	)
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})
	got, err := svc.UnfreezeTenant(WithTenantLifecycleAttribution(context.Background(), "r1", ""), tenantID.String())
	if err != nil {
		t.Fatalf("UnfreezeTenant: %v", err)
	}
	if got.Status != ports.TenantStatusActive || got.FrozenAt != nil {
		t.Fatalf("got=%+v", got)
	}
}

func TestPostgresTenantDisableTenant_FromFrozen(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	planID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	disabledAt := now
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{values: []any{tenantID}},
		quotaFakeRow{values: []any{
			tenantID, "acme", "Acme", "disabled", planID, "ops@acme.io",
			(*time.Time)(nil), &disabledAt, now, now,
			int64(0), int64(0), false, false,
		}},
	)
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})
	got, err := svc.DisableTenant(WithTenantLifecycleAttribution(context.Background(), "r1", ""), tenantID.String())
	if err != nil {
		t.Fatalf("DisableTenant: %v", err)
	}
	if got.Status != ports.TenantStatusDisabled {
		t.Fatalf("status=%s", got.Status)
	}
}

func TestPostgresTenantDisableTenant_NotFound(t *testing.T) {
	tenantID := uuid.New()
	tx := &quotaFakeTx{}
	tx.enqueueRows(
		quotaFakeRow{err: pgx.ErrNoRows},
		quotaFakeRow{err: pgx.ErrNoRows},
	)
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})
	_, err := svc.DisableTenant(WithTenantLifecycleAttribution(context.Background(), "r1", ""), tenantID.String())
	if !errors.Is(err, ports.ErrTenantNotFound) {
		t.Fatalf("want ErrTenantNotFound, got %v", err)
	}
}
