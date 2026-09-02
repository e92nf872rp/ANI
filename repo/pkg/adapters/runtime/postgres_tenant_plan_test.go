package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestPostgresTenantCountBoundTenants(t *testing.T) {
	planA := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	planB := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	tx := &quotaFakeTx{}
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{planA, int64(12)}},
	}})
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})

	got, err := svc.CountBoundTenants(context.Background(), []string{planA.String(), planB.String(), planA.String()})
	if err != nil {
		t.Fatalf("CountBoundTenants: %v", err)
	}
	if got[planA.String()] != 12 {
		t.Fatalf("plan A count = %d, want 12", got[planA.String()])
	}
	if got[planB.String()] != 0 {
		t.Fatalf("missing plan B should be 0, got %d", got[planB.String()])
	}
}

func TestPostgresTenantCountBoundTenantsEmpty(t *testing.T) {
	svc := NewPostgresTenant(&quotaFakeStore{tx: &quotaFakeTx{}})
	got, err := svc.CountBoundTenants(context.Background(), nil)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty map len = %d", len(got))
	}
}

func TestPostgresTenantCountBoundTenantsInvalidID(t *testing.T) {
	svc := NewPostgresTenant(&quotaFakeStore{tx: &quotaFakeTx{}})
	_, err := svc.CountBoundTenants(context.Background(), []string{"not-a-uuid"})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestPostgresTenantListBoundAndBindableTenants(t *testing.T) {
	planID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	tenantA := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	tx := &quotaFakeTx{}
	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{tenantA, "acme", "Acme", "active"}},
	}})
	svc := NewPostgresTenant(&quotaFakeStore{tx: tx})

	bound, err := svc.ListBoundTenants(context.Background(), planID.String())
	if err != nil {
		t.Fatalf("ListBoundTenants: %v", err)
	}
	if len(bound) != 1 || bound[0].Name != "acme" || bound[0].Status != "active" {
		t.Fatalf("bound=%+v", bound)
	}

	tx.enqueueQuery(&quotaFakeRows{rows: []quotaFakeRow{
		{values: []any{tenantA, "beta", "Beta", "frozen"}},
	}})
	bindable, err := svc.ListBindableTenants(context.Background(), planID.String())
	if err != nil {
		t.Fatalf("ListBindableTenants: %v", err)
	}
	if len(bindable) != 1 || bindable[0].Name != "beta" || bindable[0].Status != "frozen" {
		t.Fatalf("bindable=%+v", bindable)
	}
}
