package runtime

import (
	"errors"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestMetadataPlatformWorkloadStoreWritesTenantScopedSQL(t *testing.T) {
	tx := &fakeMetadataTx{row: fakeMetadataRow{err: ports.ErrNotFound}}
	store := NewMetadataPlatformWorkloadStore(fakeMetadataStore{tx: tx})
	item := kubernetesPlatformWorkload{
		record: ports.PlatformWorkloadRecord{
			ID:       "11111111-1111-1111-1111-111111111111",
			TenantID: "22222222-2222-2222-2222-222222222222",
			Name:     "inference-cpu-example",
			State:    ports.PlatformWorkloadProvisioning,
		},
		spec: sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-example"),
	}
	if err := store.put(item); err != nil {
		t.Fatalf("put() error = %v", err)
	}
	if !strings.Contains(tx.sql, "INSERT INTO platform_workloads") {
		t.Fatalf("sql = %q, want platform_workloads upsert", tx.sql)
	}
	if err := store.putIntent(item.record.TenantID, item.spec.IdempotencyKey, platformWorkloadIntent{
		fingerprint: "fp",
		workloadID:  item.record.ID,
		status:      platformWorkloadIntentPending,
	}); err != nil {
		t.Fatalf("putIntent() error = %v", err)
	}
	if !strings.Contains(tx.sql, "INSERT INTO platform_workload_intents") {
		t.Fatalf("sql = %q, want intent upsert", tx.sql)
	}
	if _, err := store.get(item.record.TenantID, item.record.ID); err == nil {
		t.Fatal("get() error = nil, want not found from empty fake")
	}
}

func TestMetadataPlatformWorkloadStoreIntentPropagatesDBError(t *testing.T) {
	tx := &fakeMetadataTx{row: fakeMetadataRow{err: errors.New("connection reset")}}
	store := NewMetadataPlatformWorkloadStore(fakeMetadataStore{tx: tx})
	if _, _, err := store.intent("22222222-2222-2222-2222-222222222222", "1df72d71-9d49-46c4-a48a-52bb37b082ab"); err == nil {
		t.Fatal("intent() error = nil, want database error")
	}
	if _, _, err := store.getRaw("22222222-2222-2222-2222-222222222222", "11111111-1111-1111-1111-111111111111"); err == nil {
		t.Fatal("getRaw() error = nil, want database error")
	}
}

func TestMetadataPlatformWorkloadStorePutIntentRejectsFingerprintOverwrite(t *testing.T) {
	tx := &fakeMetadataTx{
		zeroRows: true,
		row: fakeMetadataRow{values: []any{
			"old-fingerprint",
			"11111111-1111-1111-1111-111111111111",
			platformWorkloadIntentPending,
		}},
	}
	store := NewMetadataPlatformWorkloadStore(fakeMetadataStore{tx: tx})
	err := store.putIntent("22222222-2222-2222-2222-222222222222", "1df72d71-9d49-46c4-a48a-52bb37b082ab", platformWorkloadIntent{
		fingerprint: "new-fingerprint",
		workloadID:  "11111111-1111-1111-1111-111111111111",
		status:      platformWorkloadIntentPending,
	})
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("putIntent() error = %v, want conflict", err)
	}
	if !strings.Contains(tx.sql, "WHERE platform_workload_intents.fingerprint = EXCLUDED.fingerprint") {
		t.Fatalf("sql = %q, want fingerprint-preserving conflict update", tx.sql)
	}
}
