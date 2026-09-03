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

	got, err := svc.CreateTenant(context.Background(), ports.CreateTenantInput{
		Name: "acme", DisplayName: "Acme", ContactEmail: "ops@acme.io", PlanID: planID.String(),
		AdminEmail: "admin@acme.io", AdminName: "acme_admin", AdminPasswordHash: "$2a$12$hash",
		RequestID: "req-1", ActorUserID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
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
