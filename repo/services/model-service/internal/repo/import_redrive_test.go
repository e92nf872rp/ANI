package repo

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestImportRedriveSelectsOnlyNeverClaimedImports(t *testing.T) {
	flat := strings.Join(strings.Fields(selectStalledImportSQL), " ")
	for _, predicate := range []string{
		"it.status = 'pending'",
		"it.created_at < NOW() - make_interval(secs => $1::double precision)",
		"task.status = 'pending'",
		"task.attempt_count = 0",
		"task.lease_owner IS NULL",
	} {
		if !strings.Contains(flat, predicate) {
			t.Errorf("stalled import SQL lacks predicate %q: %s", predicate, flat)
		}
	}
	if !strings.Contains(flat, "it.tenant_id") {
		t.Errorf("stalled import SQL lacks tenant fence: %s", flat)
	}
	if !strings.Contains(flat, "FOR UPDATE OF it SKIP LOCKED") {
		t.Errorf("stalled import SQL must lock candidates against concurrent sweeps: %s", flat)
	}
	if !strings.Contains(flat, "AND NOT pending.published") {
		t.Errorf("stalled import SQL must skip imports with a pending redrive event: %s", flat)
	}
}

func TestImportRedriveReusesOriginalPublishedPayload(t *testing.T) {
	flat := strings.Join(strings.Fields(selectStalledImportSQL), " ")
	// The redrive must republish the original message identity rather than
	// rebuilding it, so the ordering and payload->>'task_id' lookup matter.
	for _, fragment := range []string{
		"SELECT event.payload FROM outbox_events event",
		"WHERE event.payload->>'task_id' = it.async_task_id::text",
		"ORDER BY event.created_at ASC, event.id ASC LIMIT 1",
	} {
		if !strings.Contains(flat, fragment) {
			t.Errorf("stalled import SQL lacks payload reuse fragment %q: %s", fragment, flat)
		}
	}
}

func TestImportRedriveCandidateTenantsStayInPlatformScope(t *testing.T) {
	flat := strings.Join(strings.Fields(candidateImportTenantsSQL), " ")
	if !strings.Contains(flat, "tenant_id") || !strings.Contains(flat, "task_type = 'model.import'") {
		t.Fatalf("candidate tenant SQL must target model imports: %s", flat)
	}
	// model_import_tasks carries a RESTRICTIVE tenant policy, so the tenant
	// discovery read must not depend on it.
	if strings.Contains(flat, "model_import_tasks") {
		t.Fatalf("candidate tenant SQL must not read the restrictive model_import_tasks table: %s", flat)
	}
	for _, predicate := range []string{"status = 'pending'", "attempt_count = 0", "lease_owner IS NULL"} {
		if !strings.Contains(flat, predicate) {
			t.Errorf("candidate tenant SQL lacks predicate %q: %s", predicate, flat)
		}
	}
}

func TestImportRedriveOutboxEventMatchesOriginalImportEvent(t *testing.T) {
	flat := strings.Join(strings.Fields(insertImportRedriveOutboxSQL), " ")
	if !strings.Contains(flat, "INSERT INTO outbox_events") || !strings.Contains(flat, "tenant_id") {
		t.Fatalf("redrive outbox SQL must insert a tenant-scoped outbox event: %s", flat)
	}
	original := strings.Join(strings.Fields(createModelImportOutboxSQL), " ")
	if flat != original {
		t.Fatalf("redrive outbox insert must stay identical to the original import event: %q vs %q", flat, original)
	}
}

func TestImportRedriveRejectsMissingTransaction(t *testing.T) {
	if _, err := CandidateImportTenants(context.Background(), nil, time.Minute, 10); err == nil {
		t.Error("candidate tenant scan without a transaction must fail")
	}
	if _, err := SelectStalledImports(context.Background(), nil, time.Minute, 10); err == nil {
		t.Error("stalled import scan without a transaction must fail")
	}
	if err := InsertImportRedriveOutbox(context.Background(), nil, StalledImport{}); err == nil {
		t.Error("redrive insert without a transaction must fail")
	}
}
