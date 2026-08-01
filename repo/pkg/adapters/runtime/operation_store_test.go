package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestMetadataOperationStoreRecordOperationUsesAtomicIdempotencyInsert(t *testing.T) {
	tx := &fakeOperationMetadataTx{
		insertRows: fakeRows{values: [][]any{{
			"00000000-0000-4000-8000-000000000001",
			"5dbb1d01-0000-4000-8000-000000000001",
			"pending:00000000-0000-4000-8000-000000000001",
			"create",
			"in_progress",
			"idem-a",
			"user-a",
			[]byte(`{"allowed":true}`),
			[]byte(`{}`),
			[]byte(`{}`),
			[]byte(`{"name":"app-01"}`),
			[]byte(`[]`),
			"",
			"",
			false,
			time.Unix(100, 0),
			time.Unix(100, 0),
		}}},
		stepRows: fakeRows{},
	}
	store := NewMetadataOperationStore(fakeOperationMetadataStore{tx: tx}, WithOperationStoreClock(func() time.Time {
		return time.Unix(100, 0)
	}))

	record, existing, err := store.RecordOperation(context.Background(), ports.WorkloadOperationRecord{
		ID:             "00000000-0000-4000-8000-000000000001",
		TenantID:       "5dbb1d01-0000-4000-8000-000000000001",
		InstanceID:     "pending:00000000-0000-4000-8000-000000000001",
		Operation:      ports.WorkloadLifecycleCreate,
		Status:         ports.WorkloadOperationInProgress,
		IdempotencyKey: "idem-a",
		RequestedBy:    "user-a",
		Precheck:       map[string]any{"allowed": true},
		AfterSpec:      map[string]any{"name": "app-01"},
	})
	if err != nil {
		t.Fatalf("RecordOperation error = %v", err)
	}
	if existing {
		t.Fatalf("existing = true, want false for inserted operation")
	}
	if record.ID == "" || record.IdempotencyKey != "idem-a" {
		t.Fatalf("record = %+v, want inserted idempotent operation", record)
	}
	if len(tx.queries) == 0 || !strings.Contains(tx.queries[0], "ON CONFLICT (tenant_id, idempotency_key)") {
		t.Fatalf("insert query = %q, want atomic idempotency conflict clause", tx.queries)
	}
}

func TestMetadataOperationStorePersistsAndReadsStepTaskCorrelation(t *testing.T) {
	started := time.Unix(200, 0)
	completed := time.Unix(201, 0)
	tx := &fakeOperationMetadataTx{stepRows: fakeRows{values: [][]any{{
		"provision_volume", "succeeded", "done", "task-a", "volume", "volume-a",
		started, completed, completed,
	}}}}
	store := NewMetadataOperationStore(fakeOperationMetadataStore{tx: tx})
	step := ports.WorkloadOperationStep{
		StepName: "provision_volume", TaskID: "task-a", ResourceType: "volume", ResourceID: "volume-a",
	}
	if _, err := store.AddOperationStep(context.Background(), "00000000-0000-4000-8000-000000000001", step); err != nil {
		t.Fatalf("AddOperationStep() error = %v", err)
	}
	if len(tx.execSQL) != 1 ||
		!strings.Contains(tx.execSQL[0], "task_id") ||
		!strings.Contains(tx.execSQL[0], "resource_type") ||
		!strings.Contains(tx.execSQL[0], "resource_id") {
		t.Fatalf("step insert SQL = %q, want task correlation columns", tx.execSQL)
	}
	steps, err := store.listSteps(context.Background(), "00000000-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatalf("listSteps() error = %v", err)
	}
	if len(steps) != 1 || steps[0].TaskID != "task-a" || steps[0].ResourceType != "volume" || steps[0].ResourceID != "volume-a" {
		t.Fatalf("steps = %+v, want persisted task correlation", steps)
	}
}

type fakeOperationMetadataTx struct {
	queries    []string
	execSQL    []string
	execArgs   [][]any
	insertRows fakeRows
	stepRows   fakeRows
}

type fakeOperationMetadataStore struct {
	tx *fakeOperationMetadataTx
}

func (s fakeOperationMetadataStore) Ping(context.Context) error {
	return nil
}

func (s fakeOperationMetadataStore) WithTenantTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	return fn(ctx, s.tx)
}

func (s fakeOperationMetadataStore) WithPlatformTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	return fn(ctx, s.tx)
}

func (tx *fakeOperationMetadataTx) Exec(_ context.Context, sql string, args ...any) (ports.CommandTag, error) {
	tx.execSQL = append(tx.execSQL, sql)
	tx.execArgs = append(tx.execArgs, args)
	return ports.CommandTag{RowsAffected: 1}, nil
}

func (tx *fakeOperationMetadataTx) Query(_ context.Context, sql string, _ ...any) (ports.Rows, error) {
	tx.queries = append(tx.queries, sql)
	if strings.Contains(sql, "INSERT INTO workload_instance_operations") {
		rows := tx.insertRows
		return &rows, nil
	}
	rows := tx.stepRows
	return &rows, nil
}

func (tx *fakeOperationMetadataTx) QueryRow(context.Context, string, ...any) ports.Row {
	return fakeRow{}
}

type fakeRows struct {
	values [][]any
	index  int
}

func (r *fakeRows) Close() {}

func (r *fakeRows) Err() error { return nil }

func (r *fakeRows) Next() bool {
	if r.index >= len(r.values) {
		return false
	}
	r.index++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	return assignScanValues(dest, r.values[r.index-1])
}

type fakeRow struct{}

func (fakeRow) Scan(...any) error {
	return ports.ErrNotFound
}

func assignScanValues(dest []any, values []any) error {
	for i, target := range dest {
		switch ptr := target.(type) {
		case *string:
			*ptr = values[i].(string)
		case *[]byte:
			*ptr = values[i].([]byte)
		case *bool:
			*ptr = values[i].(bool)
		case *time.Time:
			*ptr = values[i].(time.Time)
		case **time.Time:
			value := values[i].(time.Time)
			*ptr = &value
		default:
			return ports.ErrUnsupported
		}
	}
	return nil
}
