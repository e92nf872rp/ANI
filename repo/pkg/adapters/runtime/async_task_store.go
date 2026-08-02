package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/kubercloud/ani/pkg/ports"
)

type LocalAsyncTaskStore struct {
	mu    sync.RWMutex
	byID  map[string]ports.AsyncTaskRecord
	byKey map[string]string
}

func NewLocalAsyncTaskStore() *LocalAsyncTaskStore {
	return &LocalAsyncTaskStore{byID: make(map[string]ports.AsyncTaskRecord), byKey: make(map[string]string)}
}

func (s *LocalAsyncTaskStore) Create(_ context.Context, record ports.AsyncTaskRecord) (ports.AsyncTaskRecord, bool, error) {
	if err := validateAsyncTaskRecord(record); err != nil {
		return ports.AsyncTaskRecord{}, false, err
	}
	if record.ID == "" {
		record.ID = uuid.NewString()
	} else if _, err := uuid.Parse(record.ID); err != nil {
		return ports.AsyncTaskRecord{}, false, fmt.Errorf("%w: task ID must be UUID", ports.ErrInvalid)
	}
	normalizeAsyncTaskRecord(&record)
	key := record.TenantID + "\x00" + record.IdempotencyKey
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.byKey[key]; ok {
		existing := s.byID[record.TenantID+"\x00"+id]
		if existing.TaskType != record.TaskType || existing.ResourceType != record.ResourceType || existing.ResourceID != record.ResourceID {
			return ports.AsyncTaskRecord{}, false, fmt.Errorf("%w: idempotency key reused for different task", ports.ErrConflict)
		}
		return cloneAsyncTaskRecord(existing), true, nil
	}
	s.byKey[key] = record.ID
	s.byID[record.TenantID+"\x00"+record.ID] = cloneAsyncTaskRecord(record)
	return cloneAsyncTaskRecord(record), false, nil
}

func (s *LocalAsyncTaskStore) Get(_ context.Context, tenantID, taskID string) (ports.AsyncTaskRecord, error) {
	s.mu.RLock()
	record, ok := s.byID[tenantID+"\x00"+taskID]
	s.mu.RUnlock()
	if !ok {
		return ports.AsyncTaskRecord{}, ports.ErrNotFound
	}
	return cloneAsyncTaskRecord(record), nil
}

func (s *LocalAsyncTaskStore) Update(ctx context.Context, update ports.AsyncTaskUpdate) (ports.AsyncTaskRecord, error) {
	if strings.TrimSpace(update.TenantID) == "" || strings.TrimSpace(update.ID) == "" || !validAsyncTaskStatus(update.Status) {
		return ports.AsyncTaskRecord{}, fmt.Errorf("%w: tenant, task ID, and valid status are required", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := update.TenantID + "\x00" + update.ID
	record, ok := s.byID[key]
	if !ok {
		return ports.AsyncTaskRecord{}, ports.ErrNotFound
	}
	record.Status, record.AttemptCount, record.ProgressPct = update.Status, update.AttemptCount, update.ProgressPct
	record.Result, record.ErrorMessage = cloneAnyMap(update.Result), update.ErrorMessage
	record.DeadLetterAt, record.CompletedAt = update.DeadLetterAt, update.CompletedAt
	s.byID[key] = record
	_ = ctx
	return cloneAsyncTaskRecord(record), nil
}

type MetadataAsyncTaskStore struct {
	store ports.MetadataStore
	now   func() time.Time
}

func NewMetadataAsyncTaskStore(store ports.MetadataStore) *MetadataAsyncTaskStore {
	return &MetadataAsyncTaskStore{store: store, now: time.Now}
}

func (s *MetadataAsyncTaskStore) Create(ctx context.Context, record ports.AsyncTaskRecord) (ports.AsyncTaskRecord, bool, error) {
	if s.store == nil {
		return ports.AsyncTaskRecord{}, false, ports.ErrNotConfigured
	}
	if err := validateAsyncTaskRecord(record); err != nil {
		return ports.AsyncTaskRecord{}, false, err
	}
	if _, err := uuid.Parse(record.TenantID); err != nil {
		return ports.AsyncTaskRecord{}, false, fmt.Errorf("%w: tenant ID must be UUID", ports.ErrInvalid)
	}
	if record.ID == "" {
		record.ID = uuid.NewString()
	}
	normalizeAsyncTaskRecord(&record)
	resultJSON, err := json.Marshal(record.Result)
	if err != nil {
		return ports.AsyncTaskRecord{}, false, fmt.Errorf("marshal async task result: %w", err)
	}
	resourceID := ""
	if parsed, parseErr := uuid.Parse(record.ResourceID); parseErr == nil {
		resourceID = parsed.String()
	}
	created := ports.AsyncTaskRecord{}
	replay := false
	err = s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO async_tasks (
				tenant_id, id, idempotency_key, task_type, resource_type, resource_id,
				status, attempt_count, max_attempts, progress_pct, result, error_message,
				dead_letter_at, created_at, completed_at
			) VALUES (
				$1::uuid, $2::uuid, $3, $4, NULLIF($5, ''), NULLIF($6, '')::uuid,
				$7, $8, $9, $10, $11::jsonb, NULLIF($12, ''), $13, $14, $15
			) ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
			RETURNING tenant_id::text, id::text, idempotency_key, task_type,
				COALESCE(resource_type, ''), COALESCE(resource_id::text, ''), status,
				attempt_count, max_attempts, progress_pct, COALESCE(result, '{}'::jsonb),
				COALESCE(error_message, ''), dead_letter_at, created_at, completed_at
		`, record.TenantID, record.ID, record.IdempotencyKey, record.TaskType, record.ResourceType, resourceID,
			record.Status, record.AttemptCount, record.MaxAttempts, record.ProgressPct, string(resultJSON), record.ErrorMessage,
			nullTime(record.DeadLetterAt), record.CreatedAt, nullTime(record.CompletedAt))
		if scanErr := scanAsyncTask(row, &created); errors.Is(scanErr, pgx.ErrNoRows) {
			replay = true
			if scanErr = scanAsyncTask(tx.QueryRow(ctx, asyncTaskSelectSQL+` WHERE tenant_id=$1::uuid AND idempotency_key=$2`, record.TenantID, record.IdempotencyKey), &created); scanErr != nil {
				return scanErr
			}
			if created.TaskType != record.TaskType || created.ResourceType != record.ResourceType || created.ResourceID != resourceID {
				return fmt.Errorf("%w: idempotency key reused for different task", ports.ErrConflict)
			}
			return nil
		} else if scanErr != nil {
			return scanErr
		}
		return nil
	})
	return created, replay, err
}

func (s *MetadataAsyncTaskStore) Get(ctx context.Context, tenantID, taskID string) (ports.AsyncTaskRecord, error) {
	if s.store == nil {
		return ports.AsyncTaskRecord{}, ports.ErrNotConfigured
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return ports.AsyncTaskRecord{}, fmt.Errorf("%w: tenant ID must be UUID", ports.ErrInvalid)
	}
	if _, err := uuid.Parse(taskID); err != nil {
		return ports.AsyncTaskRecord{}, ports.ErrNotFound
	}
	var record ports.AsyncTaskRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		err := scanAsyncTask(tx.QueryRow(ctx, asyncTaskSelectSQL+` WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, taskID), &record)
		if errors.Is(err, pgx.ErrNoRows) {
			return ports.ErrNotFound
		}
		return err
	})
	return record, err
}

func (s *MetadataAsyncTaskStore) Update(ctx context.Context, update ports.AsyncTaskUpdate) (ports.AsyncTaskRecord, error) {
	if s.store == nil {
		return ports.AsyncTaskRecord{}, ports.ErrNotConfigured
	}
	if !validAsyncTaskStatus(update.Status) || update.ProgressPct < 0 || update.ProgressPct > 100 {
		return ports.AsyncTaskRecord{}, fmt.Errorf("%w: invalid async task update", ports.ErrInvalid)
	}
	resultJSON, err := json.Marshal(update.Result)
	if err != nil {
		return ports.AsyncTaskRecord{}, err
	}
	var record ports.AsyncTaskRecord
	err = s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		err := scanAsyncTask(tx.QueryRow(ctx, `
			UPDATE async_tasks SET status=$3, attempt_count=$4, progress_pct=$5,
				result=$6::jsonb, error_message=NULLIF($7, ''), dead_letter_at=$8,
				completed_at=$9, updated_at=NOW()
			WHERE tenant_id=$1::uuid AND id=$2::uuid
			RETURNING tenant_id::text, id::text, idempotency_key, task_type,
				COALESCE(resource_type, ''), COALESCE(resource_id::text, ''), status,
				attempt_count, max_attempts, progress_pct, COALESCE(result, '{}'::jsonb),
				COALESCE(error_message, ''), dead_letter_at, created_at, completed_at
		`, update.TenantID, update.ID, update.Status, update.AttemptCount, update.ProgressPct,
			string(resultJSON), update.ErrorMessage, nullTime(update.DeadLetterAt), nullTime(update.CompletedAt)), &record)
		if errors.Is(err, pgx.ErrNoRows) {
			return ports.ErrNotFound
		}
		return err
	})
	return record, err
}

const asyncTaskSelectSQL = `
	SELECT tenant_id::text, id::text, idempotency_key, task_type,
		COALESCE(resource_type, ''), COALESCE(resource_id::text, ''), status,
		attempt_count, max_attempts, progress_pct, COALESCE(result, '{}'::jsonb),
		COALESCE(error_message, ''), dead_letter_at, created_at, completed_at
	FROM async_tasks
`

func scanAsyncTask(row ports.Row, record *ports.AsyncTaskRecord) error {
	var resultJSON []byte
	var deadLetterAt, completedAt *time.Time
	if err := row.Scan(&record.TenantID, &record.ID, &record.IdempotencyKey, &record.TaskType,
		&record.ResourceType, &record.ResourceID, &record.Status, &record.AttemptCount,
		&record.MaxAttempts, &record.ProgressPct, &resultJSON, &record.ErrorMessage,
		&deadLetterAt, &record.CreatedAt, &completedAt); err != nil {
		return err
	}
	if deadLetterAt != nil {
		record.DeadLetterAt = *deadLetterAt
	}
	if completedAt != nil {
		record.CompletedAt = *completedAt
	}
	if len(resultJSON) > 0 {
		if err := json.Unmarshal(resultJSON, &record.Result); err != nil {
			return fmt.Errorf("decode async task result: %w", err)
		}
	}
	return nil
}

func validateAsyncTaskRecord(record ports.AsyncTaskRecord) error {
	if strings.TrimSpace(record.TenantID) == "" || strings.TrimSpace(record.IdempotencyKey) == "" || strings.TrimSpace(record.TaskType) == "" || !validAsyncTaskStatus(record.Status) {
		return fmt.Errorf("%w: idempotency key, task type, and valid status are required", ports.ErrInvalid)
	}
	if record.ProgressPct < 0 || record.ProgressPct > 100 {
		return fmt.Errorf("%w: progress must be between 0 and 100", ports.ErrInvalid)
	}
	return nil
}

func validAsyncTaskStatus(status string) bool {
	switch status {
	case "pending", "running", "completed", "failed", "cancelled", "dead_letter":
		return true
	default:
		return false
	}
}

func normalizeAsyncTaskRecord(record *ports.AsyncTaskRecord) {
	if record.MaxAttempts <= 0 {
		record.MaxAttempts = 3
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	if record.Status == "completed" && record.CompletedAt.IsZero() {
		record.CompletedAt = record.CreatedAt
	}
	if record.Result == nil {
		record.Result = map[string]any{}
	}
}

func cloneAsyncTaskRecord(record ports.AsyncTaskRecord) ports.AsyncTaskRecord {
	record.Result = cloneAnyMap(record.Result)
	return record
}

func cloneAnyMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	data, _ := json.Marshal(value)
	var clone map[string]any
	_ = json.Unmarshal(data, &clone)
	return clone
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

var _ ports.AsyncTaskStore = (*LocalAsyncTaskStore)(nil)
var _ ports.AsyncTaskStore = (*MetadataAsyncTaskStore)(nil)
