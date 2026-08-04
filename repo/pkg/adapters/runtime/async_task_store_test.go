package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestMetadataAsyncTaskStoreCreatesCompletedTaskInTenantTransaction(t *testing.T) {
	createdAt := time.Unix(300, 0).UTC()
	tx := &fakeMetadataTx{row: fakeMetadataRow{values: []any{
		"11111111-1111-1111-1111-111111111111", "33333333-3333-4333-8333-333333333333",
		"code-a", "sandbox.code_run.create", "sandbox_code_run", "",
		"completed", 1, 1, 100, []byte(`{"code_run":{"status":"succeeded"}}`), "",
		nil, createdAt, createdAt,
	}}}
	store := NewMetadataAsyncTaskStore(fakeMetadataStore{tx: tx})
	created, replay, err := store.Create(context.Background(), ports.AsyncTaskRecord{
		TenantID: "11111111-1111-1111-1111-111111111111", ID: "33333333-3333-4333-8333-333333333333",
		IdempotencyKey: "code-a", TaskType: "sandbox.code_run.create", ResourceType: "sandbox_code_run",
		Status: "completed", AttemptCount: 1, MaxAttempts: 1, ProgressPct: 100,
		Result:    map[string]any{"code_run": map[string]any{"status": "succeeded"}},
		CreatedAt: createdAt, CompletedAt: createdAt,
	})
	if err != nil || replay {
		t.Fatalf("Create() = (%+v, %v, %v), want persisted task", created, replay, err)
	}
	if created.Result["code_run"] == nil {
		t.Fatalf("Create() result = %+v", created.Result)
	}
	if !strings.Contains(tx.queryRowSQL, "INSERT INTO async_tasks") || !strings.Contains(tx.queryRowSQL, "result") {
		t.Fatalf("SQL = %q, want async_tasks result persistence", tx.queryRowSQL)
	}
}

func TestLocalAsyncTaskStorePersistsTenantScopedCompletedTask(t *testing.T) {
	store := NewLocalAsyncTaskStore()
	record := ports.AsyncTaskRecord{
		TenantID: "11111111-1111-1111-1111-111111111111", IdempotencyKey: "checkpoint-a",
		TaskType: "sandbox.checkpoint.create", ResourceType: "sandbox_checkpoint",
		Status: "completed", AttemptCount: 1, MaxAttempts: 1, ProgressPct: 100,
		Result:    map[string]any{"checkpoint": map[string]any{"id": "checkpoint-a"}},
		CreatedAt: time.Unix(100, 0).UTC(), CompletedAt: time.Unix(100, 0).UTC(),
	}
	created, replay, err := store.Create(context.Background(), record)
	if err != nil || replay {
		t.Fatalf("Create() = (%+v, %v, %v), want new task", created, replay, err)
	}
	if created.ID == "" {
		t.Fatal("Create() ID is empty")
	}
	again, replay, err := store.Create(context.Background(), record)
	if err != nil || !replay || again.ID != created.ID {
		t.Fatalf("Create(replay) = (%+v, %v, %v), want same task", again, replay, err)
	}
	got, err := store.Get(context.Background(), record.TenantID, created.ID)
	if err != nil || got.Result["checkpoint"] == nil {
		t.Fatalf("Get() = (%+v, %v), want persisted result", got, err)
	}
	if _, err := store.Get(context.Background(), "22222222-2222-2222-2222-222222222222", created.ID); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("cross-tenant Get() error = %v, want ErrNotFound", err)
	}
}

func TestLocalAsyncTaskStoreRejectsIdempotencyKeyReuse(t *testing.T) {
	store := NewLocalAsyncTaskStore()
	base := ports.AsyncTaskRecord{
		TenantID: "11111111-1111-1111-1111-111111111111", IdempotencyKey: "same-key",
		TaskType: "sandbox.code_run.create", Status: "completed", MaxAttempts: 1,
	}
	if _, _, err := store.Create(context.Background(), base); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	base.TaskType = "sandbox.checkpoint.create"
	if _, _, err := store.Create(context.Background(), base); !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("Create(reused key) error = %v, want ErrConflict", err)
	}
}

func TestAsyncTaskStoresRejectMalformedCreateIdentity(t *testing.T) {
	base := ports.AsyncTaskRecord{
		TenantID: "11111111-1111-1111-1111-111111111111", ID: "33333333-3333-4333-8333-333333333333",
		IdempotencyKey: "task-a", TaskType: "volume.snapshot.create", Status: "pending",
	}
	tests := []struct {
		name   string
		mutate func(*ports.AsyncTaskRecord)
	}{
		{name: "task ID", mutate: func(record *ports.AsyncTaskRecord) { record.ID = "task-a" }},
		{name: "resource ID", mutate: func(record *ports.AsyncTaskRecord) { record.ResourceID = "volume-a" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := base
			test.mutate(&record)
			stores := []ports.AsyncTaskStore{
				NewLocalAsyncTaskStore(),
				NewMetadataAsyncTaskStore(fakeMetadataStore{tx: &fakeMetadataTx{}}),
			}
			for _, store := range stores {
				if _, _, err := store.Create(context.Background(), record); !errors.Is(err, ports.ErrInvalid) {
					t.Fatalf("%T.Create() error = %v, want ErrInvalid", store, err)
				}
			}
		})
	}
}

func TestMetadataAsyncTaskStoreRequiresUUIDTenantIdentity(t *testing.T) {
	store := NewMetadataAsyncTaskStore(fakeMetadataStore{tx: &fakeMetadataTx{}})
	if _, _, err := store.Create(context.Background(), ports.AsyncTaskRecord{
		TenantID: "tenant-a", IdempotencyKey: "task-a", TaskType: "volume.snapshot.create", Status: "pending",
	}); !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("Create() error = %v, want ErrInvalid", err)
	}
	if _, err := store.Update(context.Background(), ports.AsyncTaskUpdate{
		TenantID: "tenant-a", ID: "33333333-3333-4333-8333-333333333333", Status: "running",
	}); !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("Update() error = %v, want ErrInvalid", err)
	}
}

func TestAsyncTaskStoresRejectMalformedUpdate(t *testing.T) {
	updates := []ports.AsyncTaskUpdate{
		{TenantID: "11111111-1111-1111-1111-111111111111", ID: "task-a", Status: "running"},
		{TenantID: "11111111-1111-1111-1111-111111111111", ID: "33333333-3333-4333-8333-333333333333", Status: "running", ProgressPct: 101},
	}
	for _, update := range updates {
		stores := []ports.AsyncTaskStore{
			NewLocalAsyncTaskStore(),
			NewMetadataAsyncTaskStore(fakeMetadataStore{tx: &fakeMetadataTx{}}),
		}
		for _, store := range stores {
			if _, err := store.Update(context.Background(), update); !errors.Is(err, ports.ErrInvalid) {
				t.Fatalf("%T.Update(%+v) error = %v, want ErrInvalid", store, update, err)
			}
		}
	}
}

func TestLocalAsyncTaskStoreRejectsUncloneableResultWithoutPersistence(t *testing.T) {
	store := NewLocalAsyncTaskStore()
	record := ports.AsyncTaskRecord{
		TenantID: "11111111-1111-1111-1111-111111111111", ID: "33333333-3333-4333-8333-333333333333",
		IdempotencyKey: "bad-result", TaskType: "volume.snapshot.create", Status: "pending",
		Result: map[string]any{"unsupported": make(chan int)},
	}
	if _, _, err := store.Create(context.Background(), record); err == nil {
		t.Fatal("Create() error = nil, want JSON clone error")
	}
	if _, err := store.Get(context.Background(), record.TenantID, record.ID); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Get() after rejected Create error = %v, want ErrNotFound", err)
	}
}

func TestLocalAsyncTaskStoreRejectsUncloneableUpdateAndKeepsPreviousResult(t *testing.T) {
	store := NewLocalAsyncTaskStore()
	record := ports.AsyncTaskRecord{
		TenantID: "11111111-1111-1111-1111-111111111111", ID: "33333333-3333-4333-8333-333333333333",
		IdempotencyKey: "update-result", TaskType: "volume.snapshot.create", Status: "pending",
		Result: map[string]any{"state": "before"},
	}
	if _, _, err := store.Create(context.Background(), record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := store.Update(context.Background(), ports.AsyncTaskUpdate{
		TenantID: record.TenantID, ID: record.ID, Status: "running",
		Result: map[string]any{"unsupported": make(chan int)},
	}); err == nil {
		t.Fatal("Update() error = nil, want JSON clone error")
	}
	got, err := store.Get(context.Background(), record.TenantID, record.ID)
	if err != nil || got.Result["state"] != "before" {
		t.Fatalf("Get() = (%+v, %v), want unchanged result", got, err)
	}
}

func TestLocalAsyncTaskStoreNormalizesNilResult(t *testing.T) {
	store := NewLocalAsyncTaskStore()
	created, _, err := store.Create(context.Background(), ports.AsyncTaskRecord{
		TenantID: "11111111-1111-1111-1111-111111111111", IdempotencyKey: "nil-result",
		TaskType: "volume.snapshot.create", Status: "pending",
	})
	if err != nil || created.Result == nil {
		t.Fatalf("Create() = (%+v, %v), want non-nil empty result", created, err)
	}
}
