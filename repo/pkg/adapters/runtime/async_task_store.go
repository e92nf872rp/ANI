package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
	if err := validateAsyncTaskCreate(record); err != nil {
		return ports.AsyncTaskRecord{}, false, err
	}
	if record.ID == "" {
		record.ID = uuid.NewString()
	}
	normalizeAsyncTaskRecord(&record)
	cloned, err := cloneAsyncTaskRecord(record)
	if err != nil {
		return ports.AsyncTaskRecord{}, false, err
	}
	key := record.TenantID + "\x00" + record.IdempotencyKey
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.byKey[key]; ok {
		existing := s.byID[record.TenantID+"\x00"+id]
		if existing.TaskType != record.TaskType || existing.ResourceType != record.ResourceType || existing.ResourceID != record.ResourceID {
			return ports.AsyncTaskRecord{}, false, fmt.Errorf("%w: idempotency key reused for different task", ports.ErrConflict)
		}
		clonedExisting, cloneErr := cloneAsyncTaskRecord(existing)
		return clonedExisting, true, cloneErr
	}
	s.byKey[key] = record.ID
	s.byID[record.TenantID+"\x00"+record.ID] = cloned
	created, err := cloneAsyncTaskRecord(cloned)
	return created, false, err
}

func (s *LocalAsyncTaskStore) Get(_ context.Context, tenantID, taskID string) (ports.AsyncTaskRecord, error) {
	s.mu.RLock()
	record, ok := s.byID[tenantID+"\x00"+taskID]
	s.mu.RUnlock()
	if !ok {
		return ports.AsyncTaskRecord{}, ports.ErrNotFound
	}
	return cloneAsyncTaskRecord(record)
}

func (s *LocalAsyncTaskStore) Update(_ context.Context, update ports.AsyncTaskUpdate) (ports.AsyncTaskRecord, error) {
	if err := validateAsyncTaskUpdate(update); err != nil {
		return ports.AsyncTaskRecord{}, err
	}
	result, err := cloneAnyMap(update.Result)
	if err != nil {
		return ports.AsyncTaskRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := update.TenantID + "\x00" + update.ID
	record, ok := s.byID[key]
	if !ok {
		return ports.AsyncTaskRecord{}, ports.ErrNotFound
	}
	if isTerminalAsyncTaskStatus(record.Status) && record.Status != update.Status {
		// Terminal write guard: the stored terminal row wins over a late
		// concurrent writer with a different status.
		return cloneAsyncTaskRecord(record)
	}
	record.Status, record.AttemptCount, record.ProgressPct = update.Status, update.AttemptCount, update.ProgressPct
	record.Result, record.ErrorMessage = result, update.ErrorMessage
	record.DeadLetterAt, record.CompletedAt = update.DeadLetterAt, update.CompletedAt
	s.byID[key] = record
	return cloneAsyncTaskRecord(record)
}

func (s *LocalAsyncTaskStore) List(_ context.Context, tenantID string, filter ports.AsyncTaskListFilter) ([]ports.AsyncTaskRecord, string, error) {
	if err := validateAsyncTaskListFilter(filter); err != nil {
		return nil, "", err
	}
	anchorAt, anchorID, err := decodeAsyncTaskCursor(filter.Cursor)
	if err != nil {
		return nil, "", err
	}
	s.mu.RLock()
	matched := make([]ports.AsyncTaskRecord, 0)
	for _, record := range s.byID {
		if record.TenantID != tenantID {
			continue
		}
		if filter.Status != "" && record.Status != filter.Status {
			continue
		}
		if filter.TaskType != "" && record.TaskType != filter.TaskType {
			continue
		}
		if filter.ResourceType != "" && record.ResourceType != filter.ResourceType {
			continue
		}
		if anchorID != "" && !asyncTaskBeforeAnchor(record, anchorAt, anchorID) {
			continue
		}
		matched = append(matched, record)
	}
	s.mu.RUnlock()
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].CreatedAt.After(matched[j].CreatedAt)
		}
		return matched[i].ID > matched[j].ID
	})
	nextCursor := ""
	if len(matched) > filter.Limit {
		anchor := matched[filter.Limit-1]
		nextCursor = encodeAsyncTaskCursor(anchor.CreatedAt, anchor.ID)
		matched = matched[:filter.Limit]
	}
	records := make([]ports.AsyncTaskRecord, 0, len(matched))
	for _, record := range matched {
		cloned, err := cloneAsyncTaskRecord(record)
		if err != nil {
			return nil, "", err
		}
		records = append(records, cloned)
	}
	return records, nextCursor, nil
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
	if err := validateAsyncTaskCreate(record); err != nil {
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
	resourceID := record.ResourceID
	if resourceID != "" {
		resourceID = uuid.MustParse(resourceID).String()
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
	if err := validateAsyncTaskUpdate(update); err != nil {
		return ports.AsyncTaskRecord{}, err
	}
	if _, err := uuid.Parse(update.TenantID); err != nil {
		return ports.AsyncTaskRecord{}, fmt.Errorf("%w: tenant ID must be UUID", ports.ErrInvalid)
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
				AND (status NOT IN ('completed','failed','cancelled','dead_letter') OR status=$3)
			RETURNING tenant_id::text, id::text, idempotency_key, task_type,
				COALESCE(resource_type, ''), COALESCE(resource_id::text, ''), status,
				attempt_count, max_attempts, progress_pct, COALESCE(result, '{}'::jsonb),
				COALESCE(error_message, ''), dead_letter_at, created_at, completed_at
		`, update.TenantID, update.ID, update.Status, update.AttemptCount, update.ProgressPct,
			string(resultJSON), update.ErrorMessage, nullTime(update.DeadLetterAt), nullTime(update.CompletedAt)), &record)
		if errors.Is(err, pgx.ErrNoRows) {
			// Zero rows means either a missing row or the terminal-status guard
			// blocked a late concurrent write. Re-read so the current terminal
			// row wins instead of reporting a misleading ErrNotFound.
			readErr := scanAsyncTask(tx.QueryRow(ctx, asyncTaskSelectSQL+` WHERE tenant_id=$1::uuid AND id=$2::uuid`, update.TenantID, update.ID), &record)
			if errors.Is(readErr, pgx.ErrNoRows) {
				return ports.ErrNotFound
			}
			return readErr
		}
		return err
	})
	return record, err
}

func (s *MetadataAsyncTaskStore) List(ctx context.Context, tenantID string, filter ports.AsyncTaskListFilter) ([]ports.AsyncTaskRecord, string, error) {
	if s.store == nil {
		return nil, "", ports.ErrNotConfigured
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return nil, "", fmt.Errorf("%w: tenant ID must be UUID", ports.ErrInvalid)
	}
	if err := validateAsyncTaskListFilter(filter); err != nil {
		return nil, "", err
	}
	anchorAt, anchorID, err := decodeAsyncTaskCursor(filter.Cursor)
	if err != nil {
		return nil, "", err
	}
	where := " WHERE tenant_id=$1::uuid"
	args := []any{tenantID}
	if filter.Status != "" {
		args = append(args, filter.Status)
		where += fmt.Sprintf(" AND status=$%d", len(args))
	}
	if filter.TaskType != "" {
		args = append(args, filter.TaskType)
		where += fmt.Sprintf(" AND task_type=$%d", len(args))
	}
	if filter.ResourceType != "" {
		args = append(args, filter.ResourceType)
		where += fmt.Sprintf(" AND resource_type=$%d", len(args))
	}
	if anchorID != "" {
		args = append(args, anchorAt, anchorID)
		where += fmt.Sprintf(" AND (created_at, id) < ($%d::timestamptz, $%d::uuid)", len(args)-1, len(args))
	}
	args = append(args, filter.Limit+1)
	query := asyncTaskSelectSQL + where + fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", len(args))
	var records []ports.AsyncTaskRecord
	var nextCursor string
	err = s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var record ports.AsyncTaskRecord
			if err := scanAsyncTask(rows, &record); err != nil {
				return err
			}
			records = append(records, record)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(records) > filter.Limit {
			anchor := records[filter.Limit-1]
			nextCursor = encodeAsyncTaskCursor(anchor.CreatedAt, anchor.ID)
			records = records[:filter.Limit]
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return records, nextCursor, nil
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

func validateAsyncTaskCreate(record ports.AsyncTaskRecord) error {
	if strings.TrimSpace(record.TenantID) == "" || strings.TrimSpace(record.IdempotencyKey) == "" || strings.TrimSpace(record.TaskType) == "" || !validAsyncTaskStatus(record.Status) {
		return fmt.Errorf("%w: idempotency key, task type, and valid status are required", ports.ErrInvalid)
	}
	if record.ID != "" {
		if _, err := uuid.Parse(record.ID); err != nil {
			return fmt.Errorf("%w: task ID must be UUID", ports.ErrInvalid)
		}
	}
	if record.ResourceID != "" {
		if _, err := uuid.Parse(record.ResourceID); err != nil {
			return fmt.Errorf("%w: resource ID must be UUID", ports.ErrInvalid)
		}
	}
	if record.ProgressPct < 0 || record.ProgressPct > 100 {
		return fmt.Errorf("%w: progress must be between 0 and 100", ports.ErrInvalid)
	}
	return nil
}

func validateAsyncTaskUpdate(update ports.AsyncTaskUpdate) error {
	if strings.TrimSpace(update.TenantID) == "" {
		return fmt.Errorf("%w: tenant ID is required", ports.ErrInvalid)
	}
	if _, err := uuid.Parse(update.ID); err != nil {
		return fmt.Errorf("%w: task ID must be UUID", ports.ErrInvalid)
	}
	if !validAsyncTaskStatus(update.Status) {
		return fmt.Errorf("%w: valid status is required", ports.ErrInvalid)
	}
	if update.ProgressPct < 0 || update.ProgressPct > 100 {
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

func isTerminalAsyncTaskStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled", "dead_letter":
		return true
	default:
		return false
	}
}

func validateAsyncTaskListFilter(filter ports.AsyncTaskListFilter) error {
	if filter.Status != "" && !validAsyncTaskStatus(filter.Status) {
		return fmt.Errorf("%w: invalid status filter", ports.ErrInvalid)
	}
	if filter.Limit < 1 || filter.Limit > 100 {
		return fmt.Errorf("%w: limit must be between 1 and 100", ports.ErrInvalid)
	}
	return nil
}

func encodeAsyncTaskCursor(createdAt time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(createdAt.UTC().Format(time.RFC3339Nano) + "\x00" + id))
}

func decodeAsyncTaskCursor(cursor string) (time.Time, string, error) {
	if strings.TrimSpace(cursor) == "" {
		return time.Time{}, "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(cursor))
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: cursor is invalid", ports.ErrInvalid)
	}
	parts := strings.SplitN(string(raw), "\x00", 2)
	if len(parts) != 2 || parts[1] == "" {
		return time.Time{}, "", fmt.Errorf("%w: cursor is invalid", ports.ErrInvalid)
	}
	anchorAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: cursor is invalid", ports.ErrInvalid)
	}
	return anchorAt, parts[1], nil
}

// asyncTaskBeforeAnchor reports whether record sorts strictly after the
// cursor anchor in (created_at DESC, id DESC) order, i.e. whether it belongs
// on the next page.
func asyncTaskBeforeAnchor(record ports.AsyncTaskRecord, anchorAt time.Time, anchorID string) bool {
	if !record.CreatedAt.Equal(anchorAt) {
		return record.CreatedAt.Before(anchorAt)
	}
	return record.ID < anchorID
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

func cloneAsyncTaskRecord(record ports.AsyncTaskRecord) (ports.AsyncTaskRecord, error) {
	result, err := cloneAnyMap(record.Result)
	if err != nil {
		return ports.AsyncTaskRecord{}, err
	}
	record.Result = result
	return record, nil
}

func cloneAnyMap(value map[string]any) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("clone async task result: %w", err)
	}
	var clone map[string]any
	if err := json.Unmarshal(data, &clone); err != nil {
		return nil, fmt.Errorf("clone async task result: %w", err)
	}
	if clone == nil {
		clone = map[string]any{}
	}
	return clone, nil
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

var _ ports.AsyncTaskStore = (*LocalAsyncTaskStore)(nil)
var _ ports.AsyncTaskStore = (*MetadataAsyncTaskStore)(nil)
