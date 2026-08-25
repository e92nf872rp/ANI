package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestPostgresTenantAdminLookupUser(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	userID := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	createdAt := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{
		userID, tenantID, "admin@acme.io", "acme_admin", nil, "active", "user",
		nil, createdAt, createdAt,
		tenantID, "acme", "Acme",
	}})
	svc := NewPostgresTenantAdmin(&quotaFakeStore{tx: tx})

	got, err := svc.LookupUser(context.Background(), tenantID.String(), "admin@acme.io", "acme_admin")
	if err != nil {
		t.Fatalf("LookupUser: %v", err)
	}
	if got.ID != userID.String() || got.Email != "admin@acme.io" || got.Role != "user" {
		t.Fatalf("user=%+v", got)
	}
	if got.Source != "local" || got.Tenant.Name != "acme" {
		t.Fatalf("source/tenant=%+v", got)
	}
}

func TestPostgresTenantAdminLookupUserEmptyRole(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	userID := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	createdAt := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{
		userID, tenantID, "admin@acme.io", "acme_admin", nil, "active", "",
		nil, createdAt, createdAt,
		tenantID, "acme", "Acme",
	}})
	svc := NewPostgresTenantAdmin(&quotaFakeStore{tx: tx})

	got, err := svc.LookupUser(context.Background(), tenantID.String(), "admin@acme.io", "acme_admin")
	if err != nil {
		t.Fatalf("LookupUser: %v", err)
	}
	if got.Role != "" {
		t.Fatalf("role=%q want empty", got.Role)
	}
}

func TestPostgresTenantAdminLookupUserNotFound(t *testing.T) {
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{err: pgx.ErrNoRows})
	svc := NewPostgresTenantAdmin(&quotaFakeStore{tx: tx})
	_, err := svc.LookupUser(context.Background(), uuid.NewString(), "a@b.io", "u1")
	if !errors.Is(err, ports.ErrUserNotFound) {
		t.Fatalf("want ErrUserNotFound, got %v", err)
	}
}

func TestPostgresTenantAdminLookupUserInvalid(t *testing.T) {
	svc := NewPostgresTenantAdmin(&quotaFakeStore{tx: &quotaFakeTx{}})
	_, err := svc.LookupUser(context.Background(), "bad", "a@b.io", "u1")
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	_, err = svc.LookupUser(context.Background(), uuid.NewString(), "", "u1")
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("empty email: %v", err)
	}
}

func TestPostgresTenantAdminGetUserAndIsTenantAdmin(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	userID := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	createdAt := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	display := "Admin"
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{
		userID, tenantID, "admin@acme.io", "oidc:acme_admin", &display, "active", "tenant-admin",
		nil, createdAt, createdAt,
		tenantID, "acme", "Acme",
	}})
	tx.enqueueRows(quotaFakeRow{values: []any{
		userID, tenantID, "admin@acme.io", "oidc:acme_admin", &display, "active", "tenant-admin",
		nil, createdAt, createdAt,
		tenantID, "acme", "Acme",
	}})
	svc := NewPostgresTenantAdmin(&quotaFakeStore{tx: tx})

	got, err := svc.GetUser(context.Background(), tenantID.String(), userID.String())
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Role != "tenant-admin" || got.Source != "third_party" || got.DisplayName == nil || *got.DisplayName != "Admin" {
		t.Fatalf("user=%+v", got)
	}

	ok, err := svc.IsTenantAdmin(context.Background(), tenantID.String(), userID.String())
	if err != nil {
		t.Fatalf("IsTenantAdmin: %v", err)
	}
	if !ok {
		t.Fatalf("want true")
	}
}

func TestPostgresTenantAdminIsTenantAdminNotAdmin(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	userID := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	createdAt := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{
		userID, tenantID, "u@acme.io", "local:u1", nil, "active", "user",
		nil, createdAt, createdAt,
		tenantID, "acme", "Acme",
	}})
	svc := NewPostgresTenantAdmin(&quotaFakeStore{tx: tx})
	ok, err := svc.IsTenantAdmin(context.Background(), tenantID.String(), userID.String())
	if err != nil {
		t.Fatalf("IsTenantAdmin: %v", err)
	}
	if ok {
		t.Fatalf("want false for role=user")
	}
}

func TestPostgresTenantAdminIsTenantAdminPlatformRole(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	userID := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	createdAt := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{values: []any{
		userID, tenantID, "ops@acme.io", "ops", nil, "active", "platform-admin",
		nil, createdAt, createdAt,
		tenantID, "acme", "Acme",
	}})
	svc := NewPostgresTenantAdmin(&quotaFakeStore{tx: tx})
	ok, err := svc.IsTenantAdmin(context.Background(), tenantID.String(), userID.String())
	if err != nil {
		t.Fatalf("IsTenantAdmin: %v", err)
	}
	if ok {
		t.Fatalf("want false for platform-* role")
	}
}
