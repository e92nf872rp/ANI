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
			time.Unix(100, 0).UTC().Format(time.RFC3339Nano),
			time.Unix(100, 0).UTC().Format(time.RFC3339Nano),
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

func TestMetadataOperationStoreRecordOperationScansPostgresTextTimestamps(t *testing.T) {
	tx := &fakeOperationMetadataTx{
		insertRows: fakeRows{values: [][]any{{
			"00000000-0000-4000-8000-000000000001",
			"5dbb1d01-0000-4000-8000-000000000001",
			"inst_1",
			"delete",
			"succeeded",
			"idem-delete",
			"user-a",
			[]byte(`{}`),
			[]byte(`{}`),
			[]byte(`{}`),
			[]byte(`{}`),
			[]byte(`[]`),
			"",
			"",
			false,
			"2026-06-30 09:13:14.394711+00",
			"2026-06-30 09:13:14.394711+00",
		}}},
		stepRows: fakeRows{},
	}
	store := NewMetadataOperationStore(fakeOperationMetadataStore{tx: tx})

	record, _, err := store.RecordOperation(context.Background(), ports.WorkloadOperationRecord{
		ID:             "00000000-0000-4000-8000-000000000001",
		TenantID:       "5dbb1d01-0000-4000-8000-000000000001",
		InstanceID:     "inst_1",
		Operation:      ports.WorkloadLifecycleDelete,
		Status:         ports.WorkloadOperationSucceeded,
		IdempotencyKey: "idem-delete",
		RequestedBy:    "user-a",
	})
	if err != nil {
		t.Fatalf("RecordOperation error = %v", err)
	}
	want := time.Date(2026, 6, 30, 9, 13, 14, 394711000, time.UTC)
	if !record.CreatedAt.Equal(want) || !record.UpdatedAt.Equal(want) {
		t.Fatalf("timestamps = %s/%s, want %s", record.CreatedAt, record.UpdatedAt, want)
	}
}

type fakeOperationMetadataTx struct {
	queries    []string
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

func (tx *fakeOperationMetadataTx) Exec(context.Context, string, ...any) (ports.CommandTag, error) {
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
			switch value := values[i].(type) {
			case time.Time:
				*ptr = value
			case string:
				parsed, err := time.Parse(time.RFC3339Nano, value)
				if err != nil {
					return err
				}
				*ptr = parsed
			default:
				return ports.ErrUnsupported
			}
		default:
			return ports.ErrUnsupported
		}
	}
	return nil
}
