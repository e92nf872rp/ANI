package runtime

import (
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
	store.putIntent(item.record.TenantID, item.spec.IdempotencyKey, platformWorkloadIntent{
		fingerprint: "fp",
		workloadID:  item.record.ID,
	})
	if !strings.Contains(tx.sql, "INSERT INTO platform_workload_intents") {
		t.Fatalf("sql = %q, want intent upsert", tx.sql)
	}
	if _, err := store.get(item.record.TenantID, item.record.ID); err == nil {
		t.Fatal("get() error = nil, want not found from empty fake")
	}
}
