package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type LocalOperationStore struct {
	mu        sync.RWMutex
	now       func() time.Time
	records   map[string]ports.WorkloadOperationRecord
	byIdem    map[string]string
	stepIndex map[string][]ports.WorkloadOperationStep
}

type OperationStoreOption func(*LocalOperationStore)

func WithOperationStoreClock(now func() time.Time) OperationStoreOption {
	return func(store *LocalOperationStore) {
		if now != nil {
			store.now = now
		}
	}
}

func NewLocalOperationStore(options ...OperationStoreOption) *LocalOperationStore {
	store := &LocalOperationStore{
		now:       time.Now,
		records:   map[string]ports.WorkloadOperationRecord{},
		byIdem:    map[string]string{},
		stepIndex: map[string][]ports.WorkloadOperationStep{},
	}
	for _, option := range options {
		option(store)
	}
	return store
}

func (s *LocalOperationStore) RecordOperation(_ context.Context, record ports.WorkloadOperationRecord) (ports.WorkloadOperationRecord, bool, error) {
	if err := validateOperationRecord(record); err != nil {
		return ports.WorkloadOperationRecord{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(record.IdempotencyKey) != "" {
		if id, ok := s.byIdem[idempotencyIndexKey(record.TenantID, record.IdempotencyKey)]; ok {
			existing := s.records[id]
			existing.Steps = append([]ports.WorkloadOperationStep(nil), s.stepIndex[id]...)
			return existing, true, nil
		}
	}
	now := firstNonZeroTime(record.CreatedAt, s.now().UTC())
	record.ID = firstNonEmpty(record.ID, uuid.NewString())
	record.Status = firstNonZeroOperationStatus(record.Status, ports.WorkloadOperationAccepted)
	record.CreatedAt = now
	record.UpdatedAt = firstNonZeroTime(record.UpdatedAt, now)
	record.Precheck = cloneMap(record.Precheck)
	record.DestructiveImpact = cloneMap(record.DestructiveImpact)
	record.BeforeSpec = cloneMap(record.BeforeSpec)
	record.AfterSpec = cloneMap(record.AfterSpec)
	record.ProviderRefs = append([]string(nil), record.ProviderRefs...)
	s.records[record.ID] = record
	if strings.TrimSpace(record.IdempotencyKey) != "" {
		s.byIdem[idempotencyIndexKey(record.TenantID, record.IdempotencyKey)] = record.ID
	}
	return record, false, nil
}

func (s *LocalOperationStore) GetOperation(_ context.Context, tenantID string, operationID string) (ports.WorkloadOperationRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[operationID]
	if !ok || record.TenantID != tenantID {
		return ports.WorkloadOperationRecord{}, ports.ErrNotFound
	}
	record.Steps = append([]ports.WorkloadOperationStep(nil), s.stepIndex[operationID]...)
	return record, nil
}

func (s *LocalOperationStore) GetOperationByIdempotencyKey(_ context.Context, tenantID string, idempotencyKey string) (ports.WorkloadOperationRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byIdem[idempotencyIndexKey(tenantID, idempotencyKey)]
	if !ok {
		return ports.WorkloadOperationRecord{}, ports.ErrNotFound
	}
	record := s.records[id]
	record.Steps = append([]ports.WorkloadOperationStep(nil), s.stepIndex[id]...)
	return record, nil
}

func (s *LocalOperationStore) ListOperations(_ context.Context, request ports.WorkloadOperationListRequest) (ports.WorkloadOperationListResult, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.InstanceID) == "" {
		return ports.WorkloadOperationListResult{}, fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	limit := request.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ports.WorkloadOperationRecord, 0, len(s.records))
	for _, record := range s.records {
		if record.TenantID != request.TenantID || record.InstanceID != request.InstanceID {
			continue
		}
		record.Steps = append([]ports.WorkloadOperationStep(nil), s.stepIndex[record.ID]...)
		items = append(items, record)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if len(items) > limit {
		return ports.WorkloadOperationListResult{
			Items:      append([]ports.WorkloadOperationRecord(nil), items[:limit]...),
			NextCursor: items[limit].CreatedAt.Format(time.RFC3339Nano),
		}, nil
	}
	return ports.WorkloadOperationListResult{Items: items}, nil
}

func (s *LocalOperationStore) AddOperationStep(_ context.Context, operationID string, step ports.WorkloadOperationStep) (ports.WorkloadOperationStep, error) {
	if strings.TrimSpace(operationID) == "" || strings.TrimSpace(step.StepName) == "" {
		return ports.WorkloadOperationStep{}, fmt.Errorf("%w: operationID and stepName are required", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[operationID]
	if !ok {
		return ports.WorkloadOperationStep{}, ports.ErrNotFound
	}
	now := firstNonZeroTime(step.CreatedAt, s.now().UTC())
	step.Status = firstNonZeroStepStatus(step.Status, ports.WorkloadOperationStepSucceeded)
	step.StartedAt = firstNonZeroTime(step.StartedAt, now)
	if step.Status == ports.WorkloadOperationStepSucceeded || step.Status == ports.WorkloadOperationStepFailed || step.Status == ports.WorkloadOperationStepSkipped {
		step.CompletedAt = firstNonZeroTime(step.CompletedAt, now)
	}
	step.CreatedAt = now
	s.stepIndex[operationID] = append(s.stepIndex[operationID], step)
	record.UpdatedAt = now
	if step.Status == ports.WorkloadOperationStepFailed {
		record.Status = ports.WorkloadOperationFailed
		record.FailureMessage = firstNonEmpty(record.FailureMessage, step.Message)
	}
	s.records[operationID] = record
	return step, nil
}

func (s *LocalOperationStore) UpdateOperation(_ context.Context, operationID string, update ports.WorkloadOperationUpdate) (ports.WorkloadOperationRecord, error) {
	if strings.TrimSpace(operationID) == "" {
		return ports.WorkloadOperationRecord{}, fmt.Errorf("%w: operationID is required", ports.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[operationID]
	if !ok {
		return ports.WorkloadOperationRecord{}, ports.ErrNotFound
	}
	if update.Status != "" {
		record.Status = update.Status
	}
	if strings.TrimSpace(update.InstanceID) != "" {
		record.InstanceID = update.InstanceID
	}
	if update.ProviderRefs != nil {
		record.ProviderRefs = append([]string(nil), update.ProviderRefs...)
	}
	record.FailureReason = update.FailureReason
	record.FailureMessage = update.FailureMessage
	record.RetryEligible = update.RetryEligible
	record.UpdatedAt = firstNonZeroTime(update.UpdatedAt, s.now().UTC())
	s.records[operationID] = record
	record.Steps = append([]ports.WorkloadOperationStep(nil), s.stepIndex[operationID]...)
	return record, nil
}

var _ ports.WorkloadOperationStore = (*LocalOperationStore)(nil)

type MetadataOperationStore struct {
	store ports.MetadataStore
	now   func() time.Time
}

func NewMetadataOperationStore(store ports.MetadataStore, options ...OperationStoreOption) *MetadataOperationStore {
	local := NewLocalOperationStore(options...)
	return &MetadataOperationStore{store: store, now: local.now}
}

func (s *MetadataOperationStore) RecordOperation(ctx context.Context, record ports.WorkloadOperationRecord) (ports.WorkloadOperationRecord, bool, error) {
	if s.store == nil {
		return ports.WorkloadOperationRecord{}, false, ports.ErrNotConfigured
	}
	if err := validateOperationRecord(record); err != nil {
		return ports.WorkloadOperationRecord{}, false, err
	}
	if record.IdempotencyKey != "" {
		existing, err := s.GetOperationByIdempotencyKey(ctx, record.TenantID, record.IdempotencyKey)
		if err == nil {
			return existing, true, nil
		}
	}
	now := firstNonZeroTime(record.CreatedAt, s.now().UTC())
	record.ID = firstNonEmpty(record.ID, uuid.NewString())
	record.Status = firstNonZeroOperationStatus(record.Status, ports.WorkloadOperationAccepted)
	record.CreatedAt = now
	record.UpdatedAt = firstNonZeroTime(record.UpdatedAt, now)
	precheck, destructive, beforeSpec, afterSpec, providerRefs, err := operationJSON(record)
	if err != nil {
		return ports.WorkloadOperationRecord{}, false, err
	}
	inserted := false
	err = s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, `
			INSERT INTO workload_instance_operations (
				id, tenant_id, instance_id, operation, status, idempotency_key,
				requested_by, precheck_json, destructive_impact_json, before_spec_json,
				after_spec_json, provider_refs_json, failure_reason, failure_message,
				retry_eligible, created_at, updated_at
			)
			VALUES (
				$1::uuid, $2::uuid, $3, $4, $5, NULLIF($6, ''),
				$7, $8::jsonb, $9::jsonb, $10::jsonb,
				$11::jsonb, $12::jsonb, NULLIF($13, ''), NULLIF($14, ''),
				$15, $16, $17
			)
			ON CONFLICT (tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
			RETURNING id::text, tenant_id::text, instance_id, operation, status,
				COALESCE(idempotency_key, ''), requested_by, precheck_json,
				destructive_impact_json, before_spec_json, after_spec_json,
				provider_refs_json, COALESCE(failure_reason, ''),
				COALESCE(failure_message, ''), retry_eligible, created_at::text, updated_at::text
		`, record.ID, record.TenantID, record.InstanceID, string(record.Operation), string(record.Status), record.IdempotencyKey,
			record.RequestedBy, precheck, destructive, beforeSpec, afterSpec, providerRefs, record.FailureReason,
			record.FailureMessage, record.RetryEligible, record.CreatedAt, record.UpdatedAt)
		if err != nil {
			return err
		}
		defer rows.Close()
		if rows.Next() {
			inserted = true
			return scanOperation(rows, &record)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if strings.TrimSpace(record.IdempotencyKey) == "" {
			return ports.ErrConflict
		}
		return scanOperation(tx.QueryRow(ctx, operationSelectSQL()+`
			WHERE idempotency_key = $1 AND tenant_id = $2::uuid
		`, record.IdempotencyKey, record.TenantID), &record)
	})
	if err != nil {
		return ports.WorkloadOperationRecord{}, false, err
	}
	record.Steps, err = s.listSteps(ctx, record.ID)
	if err != nil {
		return ports.WorkloadOperationRecord{}, false, err
	}
	return record, !inserted, nil
}

func (s *MetadataOperationStore) GetOperation(ctx context.Context, tenantID string, operationID string) (ports.WorkloadOperationRecord, error) {
	if s.store == nil {
		return ports.WorkloadOperationRecord{}, ports.ErrNotConfigured
	}
	var record ports.WorkloadOperationRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		return scanOperation(tx.QueryRow(ctx, operationSelectSQL()+`
			WHERE id = $1::uuid AND tenant_id = $2::uuid
		`, operationID, tenantID), &record)
	})
	if err != nil {
		return ports.WorkloadOperationRecord{}, err
	}
	record.Steps, err = s.listSteps(ctx, record.ID)
	return record, err
}

func (s *MetadataOperationStore) GetOperationByIdempotencyKey(ctx context.Context, tenantID string, idempotencyKey string) (ports.WorkloadOperationRecord, error) {
	if s.store == nil {
		return ports.WorkloadOperationRecord{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return ports.WorkloadOperationRecord{}, ports.ErrNotFound
	}
	var record ports.WorkloadOperationRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		return scanOperation(tx.QueryRow(ctx, operationSelectSQL()+`
			WHERE idempotency_key = $1 AND tenant_id = $2::uuid
		`, idempotencyKey, tenantID), &record)
	})
	if err != nil {
		return ports.WorkloadOperationRecord{}, err
	}
	record.Steps, err = s.listSteps(ctx, record.ID)
	return record, err
}

func (s *MetadataOperationStore) ListOperations(ctx context.Context, request ports.WorkloadOperationListRequest) (ports.WorkloadOperationListResult, error) {
	if s.store == nil {
		return ports.WorkloadOperationListResult{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.InstanceID) == "" {
		return ports.WorkloadOperationListResult{}, fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	limit := request.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var records []ports.WorkloadOperationRecord
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, operationSelectSQL()+`
			WHERE tenant_id = $1::uuid AND instance_id = $2
			ORDER BY created_at DESC
			LIMIT $3
		`, request.TenantID, request.InstanceID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var record ports.WorkloadOperationRecord
			if err := scanOperation(rows, &record); err != nil {
				return err
			}
			records = append(records, record)
		}
		return rows.Err()
	})
	if err != nil {
		return ports.WorkloadOperationListResult{}, err
	}
	for i := range records {
		steps, err := s.listSteps(ctx, records[i].ID)
		if err != nil {
			return ports.WorkloadOperationListResult{}, err
		}
		records[i].Steps = steps
	}
	return ports.WorkloadOperationListResult{Items: records}, nil
}

func (s *MetadataOperationStore) AddOperationStep(ctx context.Context, operationID string, step ports.WorkloadOperationStep) (ports.WorkloadOperationStep, error) {
	if s.store == nil {
		return ports.WorkloadOperationStep{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(operationID) == "" || strings.TrimSpace(step.StepName) == "" {
		return ports.WorkloadOperationStep{}, fmt.Errorf("%w: operationID and stepName are required", ports.ErrInvalid)
	}
	now := firstNonZeroTime(step.CreatedAt, s.now().UTC())
	step.Status = firstNonZeroStepStatus(step.Status, ports.WorkloadOperationStepSucceeded)
	step.StartedAt = firstNonZeroTime(step.StartedAt, now)
	if step.Status == ports.WorkloadOperationStepSucceeded || step.Status == ports.WorkloadOperationStepFailed || step.Status == ports.WorkloadOperationStepSkipped {
		step.CompletedAt = firstNonZeroTime(step.CompletedAt, now)
	}
	step.CreatedAt = now
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO workload_instance_operation_steps (
				tenant_id, operation_id, step_name, status, message, started_at, completed_at, created_at
			)
			SELECT tenant_id, id, $2, $3, NULLIF($4, ''), $5, $6, $7
			FROM workload_instance_operations
			WHERE id = $1::uuid
		`, operationID, step.StepName, string(step.Status), step.Message, nullableTime(step.StartedAt), nullableTime(step.CompletedAt), step.CreatedAt)
		return err
	})
	return step, err
}

func (s *MetadataOperationStore) UpdateOperation(ctx context.Context, operationID string, update ports.WorkloadOperationUpdate) (ports.WorkloadOperationRecord, error) {
	if s.store == nil {
		return ports.WorkloadOperationRecord{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(operationID) == "" {
		return ports.WorkloadOperationRecord{}, fmt.Errorf("%w: operationID is required", ports.ErrInvalid)
	}
	providerRefs, err := json.Marshal(firstNonNilStrings(update.ProviderRefs))
	if err != nil {
		return ports.WorkloadOperationRecord{}, fmt.Errorf("marshal provider refs: %w", err)
	}
	updatedAt := firstNonZeroTime(update.UpdatedAt, s.now().UTC())
	err = s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		_, err := tx.Exec(ctx, `
			UPDATE workload_instance_operations
			SET status = COALESCE(NULLIF($2, ''), status),
				provider_refs_json = CASE WHEN $3 THEN $4::jsonb ELSE provider_refs_json END,
				failure_reason = NULLIF($5, ''),
				failure_message = NULLIF($6, ''),
				retry_eligible = $7,
				updated_at = $8,
				instance_id = COALESCE(NULLIF($9, ''), instance_id)
			WHERE id = $1::uuid
		`, operationID, string(update.Status), update.ProviderRefs != nil, string(providerRefs),
			update.FailureReason, update.FailureMessage, update.RetryEligible, updatedAt, update.InstanceID)
		return err
	})
	if err != nil {
		return ports.WorkloadOperationRecord{}, err
	}
	return ports.WorkloadOperationRecord{
		ID:             operationID,
		InstanceID:     update.InstanceID,
		Status:         update.Status,
		ProviderRefs:   append([]string(nil), update.ProviderRefs...),
		FailureReason:  update.FailureReason,
		FailureMessage: update.FailureMessage,
		RetryEligible:  update.RetryEligible,
		UpdatedAt:      updatedAt,
	}, nil
}

var _ ports.WorkloadOperationStore = (*MetadataOperationStore)(nil)

func validateOperationRecord(record ports.WorkloadOperationRecord) error {
	if strings.TrimSpace(record.TenantID) == "" || strings.TrimSpace(record.InstanceID) == "" {
		return fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	if record.Operation == "" {
		return fmt.Errorf("%w: operation is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(record.RequestedBy) == "" {
		return fmt.Errorf("%w: requestedBy is required", ports.ErrInvalid)
	}
	return nil
}

func firstNonZeroOperationStatus(value ports.WorkloadOperationStatus, fallback ports.WorkloadOperationStatus) ports.WorkloadOperationStatus {
	if value != "" {
		return value
	}
	return fallback
}

func firstNonZeroStepStatus(value ports.WorkloadOperationStepStatus, fallback ports.WorkloadOperationStepStatus) ports.WorkloadOperationStepStatus {
	if value != "" {
		return value
	}
	return fallback
}

func idempotencyIndexKey(tenantID string, idempotencyKey string) string {
	return tenantID + "\x00" + idempotencyKey
}

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func operationJSON(record ports.WorkloadOperationRecord) (string, string, string, string, string, error) {
	precheck, err := json.Marshal(firstNonNilMap(record.Precheck))
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("marshal precheck: %w", err)
	}
	destructive, err := json.Marshal(firstNonNilMap(record.DestructiveImpact))
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("marshal destructive impact: %w", err)
	}
	beforeSpec, err := json.Marshal(firstNonNilMap(record.BeforeSpec))
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("marshal before spec: %w", err)
	}
	afterSpec, err := json.Marshal(firstNonNilMap(record.AfterSpec))
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("marshal after spec: %w", err)
	}
	providerRefs, err := json.Marshal(firstNonNilStrings(record.ProviderRefs))
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("marshal provider refs: %w", err)
	}
	return string(precheck), string(destructive), string(beforeSpec), string(afterSpec), string(providerRefs), nil
}

func firstNonNilMap(value map[string]any) map[string]any {
	if value != nil {
		return value
	}
	return map[string]any{}
}

func firstNonNilStrings(value []string) []string {
	if value != nil {
		return value
	}
	return []string{}
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func operationSelectSQL() string {
	return `
		SELECT id::text, tenant_id::text, instance_id, operation, status,
			COALESCE(idempotency_key, ''), requested_by, precheck_json,
			destructive_impact_json, before_spec_json, after_spec_json,
			provider_refs_json, COALESCE(failure_reason, ''),
			COALESCE(failure_message, ''), retry_eligible, created_at::text, updated_at::text
		FROM workload_instance_operations
	`
}

func (s *MetadataOperationStore) listSteps(ctx context.Context, operationID string) ([]ports.WorkloadOperationStep, error) {
	var steps []ports.WorkloadOperationStep
	err := s.store.WithTenantTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		rows, err := tx.Query(ctx, `
			SELECT step_name, status, COALESCE(message, ''), started_at, completed_at, created_at
			FROM workload_instance_operation_steps
			WHERE operation_id = $1::uuid
			ORDER BY created_at ASC
		`, operationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var step ports.WorkloadOperationStep
			var status string
			var startedAt *time.Time
			var completedAt *time.Time
			if err := rows.Scan(&step.StepName, &status, &step.Message, &startedAt, &completedAt, &step.CreatedAt); err != nil {
				return err
			}
			step.Status = ports.WorkloadOperationStepStatus(status)
			if startedAt != nil {
				step.StartedAt = *startedAt
			}
			if completedAt != nil {
				step.CompletedAt = *completedAt
			}
			steps = append(steps, step)
		}
		return rows.Err()
	})
	return steps, err
}

func scanOperation(row scanner, record *ports.WorkloadOperationRecord) error {
	var operation string
	var status string
	var precheckJSON []byte
	var destructiveJSON []byte
	var beforeSpecJSON []byte
	var afterSpecJSON []byte
	var providerRefsJSON []byte
	var createdAt string
	var updatedAt string
	if err := row.Scan(
		&record.ID,
		&record.TenantID,
		&record.InstanceID,
		&operation,
		&status,
		&record.IdempotencyKey,
		&record.RequestedBy,
		&precheckJSON,
		&destructiveJSON,
		&beforeSpecJSON,
		&afterSpecJSON,
		&providerRefsJSON,
		&record.FailureReason,
		&record.FailureMessage,
		&record.RetryEligible,
		&createdAt,
		&updatedAt,
	); err != nil {
		return err
	}
	var err error
	record.CreatedAt, err = parsePostgresTimestamp(createdAt)
	if err != nil {
		return fmt.Errorf("parse operation created_at: %w", err)
	}
	record.UpdatedAt, err = parsePostgresTimestamp(updatedAt)
	if err != nil {
		return fmt.Errorf("parse operation updated_at: %w", err)
	}
	record.Operation = ports.WorkloadLifecycleAction(operation)
	record.Status = ports.WorkloadOperationStatus(status)
	if err := json.Unmarshal(precheckJSON, &record.Precheck); err != nil {
		return fmt.Errorf("unmarshal precheck: %w", err)
	}
	if err := json.Unmarshal(destructiveJSON, &record.DestructiveImpact); err != nil {
		return fmt.Errorf("unmarshal destructive impact: %w", err)
	}
	if err := json.Unmarshal(beforeSpecJSON, &record.BeforeSpec); err != nil {
		return fmt.Errorf("unmarshal before spec: %w", err)
	}
	if err := json.Unmarshal(afterSpecJSON, &record.AfterSpec); err != nil {
		return fmt.Errorf("unmarshal after spec: %w", err)
	}
	if err := json.Unmarshal(providerRefsJSON, &record.ProviderRefs); err != nil {
		return fmt.Errorf("unmarshal provider refs: %w", err)
	}
	return nil
}

func parsePostgresTimestamp(value string) (time.Time, error) {
	raw := strings.TrimSpace(value)
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999-07",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05-07",
	} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp %q", value)
}
