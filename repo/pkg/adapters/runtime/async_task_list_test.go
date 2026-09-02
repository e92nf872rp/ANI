package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kubercloud/ani/pkg/ports"
)

func newAsyncTaskListTestStore(t *testing.T) *LocalAsyncTaskStore {
	t.Helper()
	store := NewLocalAsyncTaskStore()
	createdAt := time.Unix(1000, 0).UTC()
	specs := []struct {
		idempotencyKey string
		taskType       string
		resourceType   string
		status         string
		offsetSeconds  int
		fixedID        string
	}{
		{idempotencyKey: "list-a", taskType: "instance.create", resourceType: "instance", status: "running", offsetSeconds: 0, fixedID: "11111111-1111-4111-8111-111111111111"},
		{idempotencyKey: "list-b", taskType: "instance.delete", resourceType: "instance", status: "completed", offsetSeconds: 10, fixedID: "22222222-2222-4222-8222-222222222222"},
		// Same created_at as list-b: id DESC is the tiebreaker.
		{idempotencyKey: "list-c", taskType: "kb.parse", resourceType: "kb_document", status: "pending", offsetSeconds: 10, fixedID: "33333333-3333-4333-8333-333333333333"},
		{idempotencyKey: "list-d", taskType: "volume.expand", resourceType: "volume", status: "completed", offsetSeconds: 30, fixedID: "44444444-4444-4444-8444-444444444444"},
		{idempotencyKey: "list-e", taskType: "instance.start", resourceType: "instance", status: "failed", offsetSeconds: 40, fixedID: "55555555-5555-4555-8555-555555555555"},
	}
	for _, spec := range specs {
		if _, _, err := store.Create(context.Background(), ports.AsyncTaskRecord{
			TenantID:       "11111111-1111-1111-1111-111111111111",
			ID:             spec.fixedID,
			IdempotencyKey: spec.idempotencyKey,
			TaskType:       spec.taskType,
			ResourceType:   spec.resourceType,
			Status:         spec.status,
			MaxAttempts:    1,
			CreatedAt:      createdAt.Add(time.Duration(spec.offsetSeconds) * time.Second),
		}); err != nil {
			t.Fatalf("Create(%s) error = %v", spec.idempotencyKey, err)
		}
	}
	return store
}

func TestLocalAsyncTaskStoreListOrdersByCreatedAtDescThenIDDesc(t *testing.T) {
	store := newAsyncTaskListTestStore(t)
	records, nextCursor, err := store.List(context.Background(), "11111111-1111-1111-1111-111111111111", ports.AsyncTaskListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if nextCursor != "" {
		t.Fatalf("List() nextCursor = %q, want empty on last page", nextCursor)
	}
	wantOrder := []string{
		"55555555-5555-4555-8555-555555555555",
		"44444444-4444-4444-8444-444444444444",
		"33333333-3333-4333-8333-333333333333",
		"22222222-2222-4222-8222-222222222222",
		"11111111-1111-4111-8111-111111111111",
	}
	if len(records) != len(wantOrder) {
		t.Fatalf("List() = %d records, want %d", len(records), len(wantOrder))
	}
	for i, want := range wantOrder {
		if records[i].ID != want {
			t.Fatalf("List()[%d].ID = %s, want %s", i, records[i].ID, want)
		}
	}
}

func TestLocalAsyncTaskStoreListCursorPaginationCoversAllRows(t *testing.T) {
	store := newAsyncTaskListTestStore(t)
	tenantID := "11111111-1111-1111-1111-111111111111"
	seen := make([]string, 0, 5)
	cursor := ""
	pages := 0
	for {
		records, nextCursor, err := store.List(context.Background(), tenantID, ports.AsyncTaskListFilter{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("List(cursor=%q) error = %v", cursor, err)
		}
		for _, record := range records {
			seen = append(seen, record.ID)
		}
		pages++
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}
	if pages != 3 {
		t.Fatalf("pages = %d, want 3", pages)
	}
	if len(seen) != 5 || len(uniqueIDs(seen)) != 5 {
		t.Fatalf("paginated ids = %v, want 5 unique rows", seen)
	}
}

func uniqueIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	return unique
}

func TestLocalAsyncTaskStoreListFilters(t *testing.T) {
	store := newAsyncTaskListTestStore(t)
	tenantID := "11111111-1111-1111-1111-111111111111"
	tests := []struct {
		name           string
		filter         ports.AsyncTaskListFilter
		wantIDs        []string
		wantEmptyMatch bool
	}{
		{
			name:    "status filter",
			filter:  ports.AsyncTaskListFilter{Limit: 10, Status: "completed"},
			wantIDs: []string{"44444444-4444-4444-8444-444444444444", "22222222-2222-4222-8222-222222222222"},
		},
		{
			name:    "task_type filter",
			filter:  ports.AsyncTaskListFilter{Limit: 10, TaskType: "instance.create"},
			wantIDs: []string{"11111111-1111-4111-8111-111111111111"},
		},
		{
			name:    "resource_type filter",
			filter:  ports.AsyncTaskListFilter{Limit: 10, ResourceType: "instance"},
			wantIDs: []string{"55555555-5555-4555-8555-555555555555", "22222222-2222-4222-8222-222222222222", "11111111-1111-4111-8111-111111111111"},
		},
		{
			name:           "unmatched task_type returns empty list",
			filter:         ports.AsyncTaskListFilter{Limit: 10, TaskType: "inference.deploy"},
			wantEmptyMatch: true,
		},
		{
			name:           "unmatched resource_type returns empty list",
			filter:         ports.AsyncTaskListFilter{Limit: 10, ResourceType: "model_version"},
			wantEmptyMatch: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			records, _, err := store.List(context.Background(), tenantID, test.filter)
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if test.wantEmptyMatch {
				if len(records) != 0 {
					t.Fatalf("List() = %d records, want empty", len(records))
				}
				return
			}
			if len(records) != len(test.wantIDs) {
				t.Fatalf("List() = %d records, want %d", len(records), len(test.wantIDs))
			}
			for i, want := range test.wantIDs {
				if records[i].ID != want {
					t.Fatalf("List()[%d].ID = %s, want %s", i, records[i].ID, want)
				}
			}
		})
	}
}

func TestLocalAsyncTaskStoreListRejectsInvalidInput(t *testing.T) {
	store := NewLocalAsyncTaskStore()
	tenantID := "11111111-1111-1111-1111-111111111111"
	tests := []struct {
		name   string
		filter ports.AsyncTaskListFilter
	}{
		{name: "limit zero", filter: ports.AsyncTaskListFilter{Limit: 0}},
		{name: "limit above max", filter: ports.AsyncTaskListFilter{Limit: 101}},
		{name: "invalid status", filter: ports.AsyncTaskListFilter{Limit: 10, Status: "succeeded"}},
		{name: "invalid cursor", filter: ports.AsyncTaskListFilter{Limit: 10, Cursor: "not-base64!!!"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := store.List(context.Background(), tenantID, test.filter); !errors.Is(err, ports.ErrInvalid) {
				t.Fatalf("List(%+v) error = %v, want ErrInvalid", test.filter, err)
			}
		})
	}
}

func TestLocalAsyncTaskStoreListTenantIsolation(t *testing.T) {
	store := newAsyncTaskListTestStore(t)
	otherTenant := "22222222-2222-4222-8222-222222222222"
	records, nextCursor, err := store.List(context.Background(), otherTenant, ports.AsyncTaskListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 0 || nextCursor != "" {
		t.Fatalf("List(other tenant) = (%d records, %q), want empty", len(records), nextCursor)
	}
}

func TestLocalAsyncTaskStoreUpdateTerminalWriteGuard(t *testing.T) {
	store := NewLocalAsyncTaskStore()
	tenantID := "11111111-1111-1111-1111-111111111111"
	created, _, err := store.Create(context.Background(), ports.AsyncTaskRecord{
		TenantID:       tenantID,
		IdempotencyKey: "guard-task",
		TaskType:       "instance.create",
		ResourceType:   "instance",
		Status:         "running",
		ProgressPct:    10,
		MaxAttempts:    1,
		Result:         map[string]any{"state": "provisioning"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// A late writer that observed a stale provisioning state must not be able
	// to revert the terminal row written by an earlier completion.
	completedAt := time.Unix(2000, 0).UTC()
	updated, err := store.Update(context.Background(), ports.AsyncTaskUpdate{
		TenantID: tenantID, ID: created.ID, Status: "completed", ProgressPct: 100,
		Result: map[string]any{"state": "running"}, CompletedAt: completedAt,
	})
	if err != nil || updated.Status != "completed" || updated.CompletedAt.IsZero() {
		t.Fatalf("Update(completed) = (%+v, %v), want completed record", updated, err)
	}
	stale, err := store.Update(context.Background(), ports.AsyncTaskUpdate{
		TenantID: tenantID, ID: created.ID, Status: "running", ProgressPct: 20,
		Result: map[string]any{"state": "provisioning"},
	})
	if err != nil {
		t.Fatalf("Update(stale running) error = %v, want guard hit without error", err)
	}
	if stale.Status != "completed" || stale.ProgressPct != 100 || stale.CompletedAt.IsZero() {
		t.Fatalf("Update(stale running) = (%+v), want current terminal record to win", stale)
	}

	// Same-status writes to a terminal row stay allowed (idempotent refresh).
	refreshed, err := store.Update(context.Background(), ports.AsyncTaskUpdate{
		TenantID: tenantID, ID: created.ID, Status: "completed", ProgressPct: 100,
		Result: map[string]any{"state": "running", "extra": true}, CompletedAt: completedAt,
	})
	if err != nil || refreshed.Result["extra"] != true {
		t.Fatalf("Update(completed again) = (%+v, %v), want same-status rewrite allowed", refreshed, err)
	}

	// Non-terminal rows still update normally.
	other, _, err := store.Create(context.Background(), ports.AsyncTaskRecord{
		TenantID: tenantID, IdempotencyKey: "guard-open",
		TaskType: "instance.start", ResourceType: "instance", Status: "running", ProgressPct: 10, MaxAttempts: 1,
	})
	if err != nil {
		t.Fatalf("Create(open) error = %v", err)
	}
	moved, err := store.Update(context.Background(), ports.AsyncTaskUpdate{
		TenantID: tenantID, ID: other.ID, Status: "running", ProgressPct: 40,
		Result: map[string]any{"state": "starting"},
	})
	if err != nil || moved.ProgressPct != 40 {
		t.Fatalf("Update(open row) = (%+v, %v), want normal update", moved, err)
	}
}

// fakeAsyncTaskRows serves multiple rows for MetadataAsyncTaskStore.List.
type fakeAsyncTaskRows struct {
	rows   []fakeMetadataRow
	cursor int
}

func (r *fakeAsyncTaskRows) Close() {}

func (r *fakeAsyncTaskRows) Err() error { return nil }

func (r *fakeAsyncTaskRows) Next() bool { return r.cursor < len(r.rows) }

func (r *fakeAsyncTaskRows) Scan(dest ...any) error {
	if r.cursor >= len(r.rows) {
		return ports.ErrUnsupported
	}
	row := r.rows[r.cursor]
	r.cursor++
	return row.Scan(dest...)
}

// asyncTaskFakeStore pops queued QueryRow results in call order so the guard
// path (UPDATE 0 rows -> re-read) can be simulated deterministically.
type asyncTaskFakeStore struct {
	tx *asyncTaskFakeTx
}

func (s asyncTaskFakeStore) Ping(context.Context) error { return nil }

func (s asyncTaskFakeStore) WithTenantTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	return fn(ctx, s.tx)
}

func (s asyncTaskFakeStore) WithPlatformTx(ctx context.Context, fn func(context.Context, ports.MetadataTx) error) error {
	return fn(ctx, s.tx)
}

type asyncTaskFakeTx struct {
	queryRows []ports.Row
	rows      ports.Rows
	querySQL  string
	rowSQLs   []string
}

func (tx *asyncTaskFakeTx) Exec(context.Context, string, ...any) (ports.CommandTag, error) {
	return ports.CommandTag{RowsAffected: 1}, nil
}

func (tx *asyncTaskFakeTx) Query(_ context.Context, sql string, _ ...any) (ports.Rows, error) {
	tx.querySQL = sql
	if tx.rows == nil {
		return &fakeAsyncTaskRows{}, nil
	}
	return tx.rows, nil
}

func (tx *asyncTaskFakeTx) QueryRow(_ context.Context, sql string, _ ...any) ports.Row {
	tx.rowSQLs = append(tx.rowSQLs, sql)
	if len(tx.queryRows) == 0 {
		return fakeMetadataRow{err: ports.ErrUnsupported}
	}
	row := tx.queryRows[0]
	tx.queryRows = tx.queryRows[1:]
	return row
}

func asyncTaskRowValues(tenantID, taskID, taskType, resourceType, status string, progress int, createdAt time.Time) []any {
	return []any{
		tenantID, taskID, "idem-" + taskID, taskType, resourceType, "",
		status, 1, 1, progress, []byte(`{}`), "",
		nil, createdAt, nil,
	}
}

func TestMetadataAsyncTaskStoreListQueriesKeysetOrdering(t *testing.T) {
	createdAt := time.Unix(1000, 0).UTC()
	// The fake tx returns rows verbatim (filtering is the database's job), so
	// only the post-cursor row is served.
	rows := &fakeAsyncTaskRows{rows: []fakeMetadataRow{
		{values: asyncTaskRowValues("11111111-1111-1111-1111-111111111111", "11111111-1111-4111-8111-111111111111", "instance.create", "instance", "running", 10, createdAt)},
	}}
	tx := &asyncTaskFakeTx{rows: rows}
	store := NewMetadataAsyncTaskStore(asyncTaskFakeStore{tx: tx})
	cursor := encodeAsyncTaskCursor(createdAt.Add(10*time.Second), "22222222-2222-4222-8222-222222222222")
	records, nextCursor, err := store.List(context.Background(), "11111111-1111-1111-1111-111111111111", ports.AsyncTaskListFilter{
		Status: "running", TaskType: "instance.create", ResourceType: "instance", Limit: 2, Cursor: cursor,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 || records[0].ID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("List() = %+v, want the single served row", records)
	}
	if nextCursor != "" {
		t.Fatalf("List() nextCursor = %q, want empty when rows <= limit", nextCursor)
	}
	for _, want := range []string{
		"ORDER BY created_at DESC, id DESC",
		"AND status=$2",
		"AND task_type=$3",
		"AND resource_type=$4",
		"(created_at, id) < ($5::timestamptz, $6::uuid)",
		"LIMIT $7",
	} {
		if !strings.Contains(tx.querySQL, want) {
			t.Fatalf("List() SQL = %q, want %q", tx.querySQL, want)
		}
	}
}

func TestMetadataAsyncTaskStoreListProbesLimitPlusOneCursor(t *testing.T) {
	createdAt := time.Unix(1000, 0).UTC()
	rows := &fakeAsyncTaskRows{rows: []fakeMetadataRow{
		{values: asyncTaskRowValues("11111111-1111-1111-1111-111111111111", "22222222-2222-4222-8222-222222222222", "instance.delete", "instance", "completed", 100, createdAt.Add(10*time.Second))},
		{values: asyncTaskRowValues("11111111-1111-1111-1111-111111111111", "11111111-1111-4111-8111-111111111111", "instance.create", "instance", "running", 10, createdAt)},
	}}
	tx := &asyncTaskFakeTx{rows: rows}
	store := NewMetadataAsyncTaskStore(asyncTaskFakeStore{tx: tx})
	records, nextCursor, err := store.List(context.Background(), "11111111-1111-1111-1111-111111111111", ports.AsyncTaskListFilter{Limit: 1})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List() = %d records, want limit truncation", len(records))
	}
	if nextCursor == "" {
		t.Fatal("List() nextCursor empty, want a cursor when more rows exist")
	}
	anchorAt, anchorID, err := decodeAsyncTaskCursor(nextCursor)
	if err != nil {
		t.Fatalf("decodeAsyncTaskCursor() error = %v", err)
	}
	if !anchorAt.Equal(createdAt.Add(10*time.Second)) || anchorID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("cursor anchor = (%v, %s), want the last returned row", anchorAt, anchorID)
	}
}

func TestMetadataAsyncTaskStoreUpdateTerminalGuardReReadsCurrentRow(t *testing.T) {
	createdAt := time.Unix(1000, 0).UTC()
	completedAt := time.Unix(2000, 0).UTC()
	current := []any{
		"11111111-1111-1111-1111-111111111111", "33333333-3333-4333-8333-333333333333", "guard-task",
		"instance.create", "instance", "", "completed", 1, 1, 100, []byte(`{"state":"running"}`), "",
		nil, createdAt, completedAt,
	}
	// QueryRow call order: the guarded UPDATE returns no rows, the follow-up
	// SELECT returns the current terminal row.
	tx := &asyncTaskFakeTx{queryRows: []ports.Row{
		fakeMetadataRow{err: pgx.ErrNoRows},
		fakeMetadataRow{values: current},
	}}
	store := NewMetadataAsyncTaskStore(asyncTaskFakeStore{tx: tx})
	updated, err := store.Update(context.Background(), ports.AsyncTaskUpdate{
		TenantID: "11111111-1111-1111-1111-111111111111",
		ID:       "33333333-3333-4333-8333-333333333333",
		Status:   "running", ProgressPct: 20,
		Result: map[string]any{"state": "provisioning"},
	})
	if err != nil {
		t.Fatalf("Update(guard hit) error = %v, want re-read without error", err)
	}
	if updated.Status != "completed" || updated.ProgressPct != 100 {
		t.Fatalf("Update(guard hit) = (%s, %d), want the terminal row to win", updated.Status, updated.ProgressPct)
	}
	if len(tx.rowSQLs) == 0 || !strings.Contains(tx.rowSQLs[0], "status NOT IN ('completed','failed','cancelled','dead_letter')") {
		t.Fatalf("Update() SQL = %q, want terminal-status guard", tx.rowSQLs)
	}
}
