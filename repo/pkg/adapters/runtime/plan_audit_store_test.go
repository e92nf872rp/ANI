package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type fakeMetadataStore struct {
	tx *fakeMetadataTx
}

func (s fakeMetadataStore) Ping(context.Context) error {
	return nil
}

func (s fakeMetadataStore) WithTenantTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	return fn(ctx, s.tx)
}

func (s fakeMetadataStore) WithPlatformTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	return fn(ctx, s.tx)
}

type fakeMetadataTx struct {
	sql          string
	args         []any
	execs        []string
	queryRowSQL  string
	queryRowArgs []any
	row          fakeMetadataRow
}

func (tx *fakeMetadataTx) Exec(_ context.Context, sql string, args ...any) (ports.CommandTag, error) {
	tx.sql = sql
	tx.args = args
	tx.execs = append(tx.execs, sql)
	return ports.CommandTag{RowsAffected: 1}, nil
}

func (tx *fakeMetadataTx) Query(context.Context, string, ...any) (ports.Rows, error) {
	return nil, ports.ErrUnsupported
}

func (tx *fakeMetadataTx) QueryRow(_ context.Context, sql string, args ...any) ports.Row {
	tx.queryRowSQL = sql
	tx.queryRowArgs = args
	return tx.row
}

type fakeMetadataRow struct {
	values []any
	err    error
}

func (r fakeMetadataRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, target := range dest {
		switch ptr := target.(type) {
		case *string:
			*ptr = r.values[i].(string)
		case *bool:
			*ptr = r.values[i].(bool)
		case *time.Time:
			*ptr = r.values[i].(time.Time)
		case *[]byte:
			*ptr = r.values[i].([]byte)
		default:
			return ports.ErrUnsupported
		}
	}
	return nil
}

func TestMetadataPlanAuditStoreRecordsPlan(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataPlanAuditStore(fakeMetadataStore{tx: tx}, WithAuditClock(func() time.Time {
		return time.Unix(100, 0)
	}))

	id, err := store.RecordPlan(context.Background(), ports.WorkloadPlanAuditRecord{
		TenantID:     "5dbb1d01-0000-4000-8000-000000000001",
		InstanceName: "app-01",
		WorkloadKind: ports.WorkloadKindContainer,
		Provider:     "kubernetes",
		Manifests: []ports.WorkloadManifest{
			{Name: "app-01", Kind: "Deployment", Provider: "kubernetes", Content: "{}"},
		},
		AdmissionResult: ports.WorkloadAdmissionResult{
			Allowed: true,
			Reason:  "accepted",
		},
	})
	if err != nil {
		t.Fatalf("RecordPlan() error = %v", err)
	}
	if id == "" {
		t.Fatalf("RecordPlan() id is empty")
	}
	if !strings.Contains(tx.sql, "INSERT INTO instance_plan_audits") {
		t.Fatalf("sql = %q, want instance_plan_audits insert", tx.sql)
	}
	if got, want := tx.args[4], "app-01"; got != want {
		t.Fatalf("instance_name arg = %v, want %s", got, want)
	}
	if got, want := tx.args[9], true; got != want {
		t.Fatalf("admission_allowed arg = %v, want %v", got, want)
	}
}

func TestMetadataPlanAuditStoreRejectsMissingTenant(t *testing.T) {
	store := NewMetadataPlanAuditStore(fakeMetadataStore{tx: &fakeMetadataTx{}})

	_, err := store.RecordPlan(context.Background(), ports.WorkloadPlanAuditRecord{
		InstanceName: "app-01",
		WorkloadKind: ports.WorkloadKindContainer,
	})
	if err == nil {
		t.Fatalf("RecordPlan() error = nil, want error")
	}
}
