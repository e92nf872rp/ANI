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

func TestPostgresTenantGetTenant(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	planID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	createdAt := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(5 * time.Minute)
	tx := &quotaFakeTx{}
	tx.enqueueRows(quotaFakeRow{
		values: []any{tenantID, "acme", "Acme", "active", planID, createdAt, updatedAt},
	})
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})

	got, err := svc.GetTenant(context.Background(), tenantID.String())
	if err != nil {
		t.Fatalf("GetTenant: %v", err)
	}
	if got.ID != tenantID.String() || got.PlanID != planID.String() {
		t.Fatalf("tenant=%+v", got)
	}
	if got.DisplayName != "Acme" || got.Status != "active" {
		t.Fatalf("tenant fields=%+v", got)
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
