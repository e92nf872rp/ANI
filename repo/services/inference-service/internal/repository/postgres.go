package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
)

const setTenantSQL = `SELECT set_config('app.current_tenant_id', $1, true)`

const lockIdempotencySQL = `
SELECT pg_advisory_xact_lock(hashtextextended($1, 0))
`

const findReplaySQL = `
SELECT id, service_id, type, state, target_generation, COALESCE(rollback_generation, 0),
       before_spec, target_spec,
       operation_scope, idempotency_key, request_hash, attempt, next_attempt_at,
       COALESCE(lease_owner, ''), lease_until,
       COALESCE(lease_token, '00000000-0000-0000-0000-000000000000'::uuid),
       COALESCE(runtime_task_id, ''),
       COALESCE(error_code, ''), COALESCE(error_message, ''),
       COALESCE(result_snapshot, 'null'::jsonb),
       created_at, updated_at, completed_at
FROM inference_operations
WHERE tenant_id = $1 AND operation_scope = $2 AND idempotency_key = $3
`

const insertServiceSQL = `
INSERT INTO inference_services (
    id, tenant_id, name, model_version_id, served_model_name, model_display_snapshot,
    desired_spec, applied_spec, placement_mode, status, status_reason, status_message,
    generation, observed_generation, desired_state, runtime_ref, runtime_endpoint,
    invocation_url, ready_replicas, current_operation_id, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
    $13, $14, $15, NULLIF($16, '00000000-0000-0000-0000-000000000000'::uuid),
    NULLIF($17, ''), NULLIF($18, ''), $19, $20, $21, $22
)
`

const insertOperationSQL = `
INSERT INTO inference_operations (
    id, tenant_id, service_id, type, operation_scope, idempotency_key, request_hash,
    target_generation, before_spec, target_spec, state, attempt, next_attempt_at,
    result_snapshot, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
`

const getServiceSQL = `
SELECT service.id, service.tenant_id, service.name, service.model_version_id,
       service.served_model_name, service.model_display_snapshot,
       service.status, COALESCE(service.status_reason, ''), COALESCE(service.status_message, ''),
       service.desired_state, service.generation, service.observed_generation,
       service.desired_spec, service.applied_spec,
       COALESCE(service.runtime_ref, '00000000-0000-0000-0000-000000000000'::uuid),
       COALESCE(service.runtime_endpoint, ''), COALESCE(service.invocation_url, ''),
       service.ready_replicas,
       COALESCE(service.current_operation_id, '00000000-0000-0000-0000-000000000000'::uuid),
       COALESCE(active.type, ''), COALESCE(active.state, ''),
       service.created_at, service.updated_at, service.deleted_at, service.legacy_quarantined
FROM inference_services AS service
LEFT JOIN inference_operations AS active
  ON active.id = service.current_operation_id AND active.state IN ('pending', 'running')
WHERE service.tenant_id = $1 AND service.id = $2 AND service.deleted_at IS NULL
`

const getServiceForMutationSQL = `
SELECT service.id, service.tenant_id, service.name, service.model_version_id,
       service.served_model_name, service.model_display_snapshot,
       service.status, COALESCE(service.status_reason, ''), COALESCE(service.status_message, ''),
       service.desired_state, service.generation, service.observed_generation,
       service.desired_spec, service.applied_spec,
       COALESCE(service.runtime_ref, '00000000-0000-0000-0000-000000000000'::uuid),
       COALESCE(service.runtime_endpoint, ''), COALESCE(service.invocation_url, ''),
       service.ready_replicas,
       COALESCE(service.current_operation_id, '00000000-0000-0000-0000-000000000000'::uuid),
       COALESCE((SELECT operation.type FROM inference_operations AS operation
                 WHERE operation.id = service.current_operation_id
                   AND operation.state IN ('pending', 'running')), ''),
       COALESCE((SELECT operation.state FROM inference_operations AS operation
                 WHERE operation.id = service.current_operation_id
                   AND operation.state IN ('pending', 'running')), ''),
       service.created_at, service.updated_at, service.deleted_at, service.legacy_quarantined
FROM inference_services AS service
WHERE service.tenant_id = $1 AND service.id = $2 AND service.deleted_at IS NULL
FOR UPDATE
`

const listServicesSQL = `
SELECT service.id, service.tenant_id, service.name, service.model_version_id,
       service.served_model_name, service.model_display_snapshot,
       service.status, COALESCE(service.status_reason, ''), COALESCE(service.status_message, ''),
       service.desired_state, service.generation, service.observed_generation,
       service.desired_spec, service.applied_spec, service.ready_replicas,
       COALESCE(service.current_operation_id, '00000000-0000-0000-0000-000000000000'::uuid),
       service.created_at, service.updated_at, service.deleted_at, service.legacy_quarantined
FROM inference_services AS service
WHERE service.tenant_id = $1 AND service.deleted_at IS NULL
ORDER BY service.created_at, service.id
`

const cancelOperationSQL = `
UPDATE inference_operations
SET state = 'cancelled', lease_owner = NULL, lease_until = NULL, lease_token = NULL,
    completed_at = $4, updated_at = $4
WHERE tenant_id = $1 AND service_id = $2 AND id = $3
  AND state IN ('pending', 'running')
`

const updateServiceTransitionSQL = `
UPDATE inference_services
SET desired_spec = $3, desired_state = $4, generation = $5,
    current_operation_id = $6, status = $7, updated_at = $8
WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
`

const updateServiceCurrentOperationSQL = `
UPDATE inference_services
SET current_operation_id = $3, updated_at = $4
WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
`

const insertCompletedOperationSQL = `
INSERT INTO inference_operations (
    id, tenant_id, service_id, type, operation_scope, idempotency_key, request_hash,
    target_generation, before_spec, target_spec, state, attempt, next_attempt_at,
    created_at, updated_at, completed_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'completed', 0, $11, $11, $11, $11)
`

const getOperationSQL = `
SELECT id, service_id, type, state, target_generation, COALESCE(rollback_generation, 0),
       before_spec, target_spec,
       operation_scope, idempotency_key, request_hash, attempt, next_attempt_at,
       COALESCE(lease_owner, ''), lease_until,
       COALESCE(lease_token, '00000000-0000-0000-0000-000000000000'::uuid),
       COALESCE(runtime_task_id, ''),
       COALESCE(error_code, ''), COALESCE(error_message, ''),
       COALESCE(result_snapshot, 'null'::jsonb), created_at, updated_at, completed_at
FROM inference_operations
WHERE tenant_id = $1 AND id = $2
`

const claimOperationSQL = `
WITH candidate AS (
    SELECT id
    FROM inference_operations
    WHERE state IN ('pending', 'running')
      AND next_attempt_at <= $2
      AND (lease_until IS NULL OR lease_until <= $2)
    ORDER BY next_attempt_at, created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE inference_operations AS operation
SET state = 'running', lease_owner = $1, lease_until = $3, lease_token = $4, updated_at = $2
FROM candidate
WHERE operation.id = candidate.id
RETURNING operation.id, operation.tenant_id, operation.service_id, operation.type,
          operation.state, operation.target_generation, COALESCE(operation.rollback_generation, 0),
          operation.before_spec, operation.target_spec, operation.operation_scope,
          operation.idempotency_key, operation.request_hash, operation.attempt,
          operation.next_attempt_at, operation.lease_owner, operation.lease_until,
          operation.lease_token, COALESCE(operation.runtime_task_id, ''),
          COALESCE(operation.error_code, ''), COALESCE(operation.error_message, ''),
          COALESCE(operation.result_snapshot, 'null'::jsonb), operation.created_at,
          operation.updated_at, operation.completed_at
`

const applyObservationSQL = `
UPDATE inference_services
SET status = $5,
    applied_spec = CASE WHEN $11 THEN $6 ELSE applied_spec END,
    runtime_ref = NULLIF($7, '00000000-0000-0000-0000-000000000000'::uuid),
    runtime_endpoint = NULLIF($8, ''), ready_replicas = $9,
    observed_generation = CASE WHEN $11 THEN $3 ELSE observed_generation END,
    deleted_at = CASE WHEN $13 THEN $10 ELSE deleted_at END,
    updated_at = $10
WHERE tenant_id = $1 AND id = $2 AND generation = $3 AND current_operation_id = $4
  AND EXISTS (
      SELECT 1 FROM inference_operations AS operation
      WHERE operation.id = $4 AND operation.tenant_id = $1
        AND operation.lease_token = $12 AND operation.lease_until > $10
        AND operation.state = 'running'
  )
`

const completeOperationSQL = `
UPDATE inference_operations
SET state = 'completed', lease_owner = NULL, lease_until = NULL,
    lease_token = NULL, completed_at = $5, updated_at = $5
WHERE tenant_id = $1 AND service_id = $2 AND target_generation = $3 AND id = $4
  AND lease_token = $6 AND lease_until > $5 AND state = 'running'
`

const failOperationSQL = `
UPDATE inference_operations
SET state = $5, attempt = attempt + 1, next_attempt_at = COALESCE($6, next_attempt_at),
    lease_owner = NULL, lease_until = NULL, lease_token = NULL, error_code = $7, error_message = $8,
    completed_at = CASE WHEN $6 IS NULL THEN $9 ELSE NULL END, updated_at = $9
WHERE tenant_id = $1 AND service_id = $2 AND target_generation = $3 AND id = $4
  AND lease_token = $10 AND lease_until > $9 AND state = 'running'
`

const failServiceSQL = `
UPDATE inference_services
SET status = 'failed', status_reason = $5, status_message = $6,
    runtime_endpoint = NULL, ready_replicas = 0, updated_at = $7
WHERE tenant_id = $1 AND id = $2 AND generation = $3 AND current_operation_id = $4
`

const beginScaleRollbackServiceSQL = `
UPDATE inference_services
SET desired_spec = applied_spec, generation = generation + 1, status = 'deploying',
    status_reason = 'SCALE_ROLLING_BACK', status_message = $5, updated_at = $6
WHERE tenant_id = $1 AND id = $2 AND generation = $3 AND current_operation_id = $4
  AND desired_state <> 'deleted'
RETURNING generation
`

const beginScaleRollbackOperationSQL = `
UPDATE inference_operations
SET rollback_generation = $4, error_code = 'SCALE_ROLLING_BACK', error_message = $5,
    updated_at = $6
WHERE tenant_id = $1 AND service_id = $2 AND id = $3
  AND lease_token = $7 AND lease_until > $6 AND state = 'running'
`

const finishScaleRollbackServiceSQL = `
UPDATE inference_services
SET status = $5, applied_spec = CASE WHEN $6 THEN $7 ELSE applied_spec END,
    observed_generation = CASE WHEN $6 THEN generation ELSE observed_generation END,
    runtime_ref = CASE WHEN $6 THEN NULLIF($8, '00000000-0000-0000-0000-000000000000'::uuid) ELSE runtime_ref END,
    runtime_endpoint = CASE WHEN $6 THEN NULLIF($9, '') ELSE NULL END,
    ready_replicas = CASE WHEN $6 THEN $10 ELSE 0 END,
    status_reason = $11, status_message = $12, updated_at = $13
WHERE tenant_id = $1 AND id = $2 AND generation = $3 AND current_operation_id = $4
`

const finishScaleRollbackOperationSQL = `
UPDATE inference_operations
SET state = 'failed', error_code = $5, error_message = $6,
    lease_owner = NULL, lease_until = NULL, lease_token = NULL,
    completed_at = $7, updated_at = $7
WHERE tenant_id = $1 AND service_id = $2 AND id = $3
  AND rollback_generation = $4 AND lease_token = $8 AND lease_until > $7 AND state = 'running'
`

const bindRuntimeRefSQL = `
UPDATE inference_services
SET runtime_ref = $5, status = 'deploying', updated_at = $6
WHERE tenant_id = $1 AND id = $2 AND generation = $3 AND current_operation_id = $4
  AND deleted_at IS NULL
`

const clearCreateCurrentOperationSQL = `
UPDATE inference_services
SET current_operation_id = NULL, updated_at = $4
WHERE tenant_id = $1 AND id = $2 AND current_operation_id = $3
  AND runtime_ref IS NULL AND status IN ('pending', 'deploying') AND deleted_at IS NULL
`

const abortCreateOperationSQL = `
DELETE FROM inference_operations
WHERE tenant_id = $1 AND service_id = $2 AND id = $3 AND type = 'create' AND state = 'pending'
`

const abortCreateServiceSQL = `
DELETE FROM inference_services
WHERE tenant_id = $1 AND id = $2 AND current_operation_id IS NULL
  AND runtime_ref IS NULL AND status IN ('pending', 'deploying') AND deleted_at IS NULL
`

const abortPendingMutationServiceSQL = `
UPDATE inference_services
SET desired_spec = $5, desired_state = $6, generation = $7, status = $8,
    current_operation_id = NULL, updated_at = $9
WHERE tenant_id = $1 AND id = $2 AND generation = $3 AND current_operation_id = $4
  AND deleted_at IS NULL
`

const abortPendingMutationOperationSQL = `
UPDATE inference_operations
SET state = 'cancelled', lease_owner = NULL, lease_until = NULL, lease_token = NULL,
    completed_at = $4, updated_at = $4
WHERE tenant_id = $1 AND service_id = $2 AND id = $3 AND state = 'pending'
`

// Postgres 实现 Store / ControlStore。tenantPool 走租户 schema。
type Postgres struct {
	tenantPool   *pgxpool.Pool
	platformPool *pgxpool.Pool
}

func NewPostgres(tenantPool, platformPool *pgxpool.Pool) *Postgres {
	return &Postgres{tenantPool: tenantPool, platformPool: platformPool}
}

func OpenStore(ctx context.Context, tenantDSN, platformDSN string) (*Postgres, func(), error) {
	if tenantDSN == "" {
		return nil, nil, errors.New("inference tenant database url is required")
	}
	if platformDSN == "" {
		platformDSN = tenantDSN
	}
	tenantPool, err := pgxpool.New(ctx, tenantDSN)
	if err != nil {
		return nil, nil, fmt.Errorf("open inference tenant pool: %w", err)
	}
	platformPool, err := pgxpool.New(ctx, platformDSN)
	if err != nil {
		tenantPool.Close()
		return nil, nil, fmt.Errorf("open inference platform pool: %w", err)
	}
	return NewPostgres(tenantPool, platformPool), func() {
		tenantPool.Close()
		platformPool.Close()
	}, nil
}

func classifyReplay(existingHash, requestedHash string) error {
	if existingHash != requestedHash {
		return ErrIdempotencyConflict
	}
	return nil
}

func leaseAvailable(until *time.Time, now time.Time) bool {
	return until == nil || !until.After(now)
}

type createResultSnapshot struct {
	Service   domain.Service   `json:"service"`
	Operation domain.Operation `json:"operation"`
}

func decodeCreateResult(operation domain.Operation) (CreateResult, error) {
	if len(operation.ResultSnapshot) == 0 || string(operation.ResultSnapshot) == "null" {
		return CreateResult{}, errors.New("create operation result snapshot is missing")
	}
	var snapshot createResultSnapshot
	if err := json.Unmarshal(operation.ResultSnapshot, &snapshot); err != nil {
		return CreateResult{}, fmt.Errorf("decode create operation result snapshot: %w", err)
	}
	snapshot.Operation.ResultSnapshot = operation.ResultSnapshot
	snapshot.Operation.Replayed = true
	return CreateResult{Service: snapshot.Service, Operation: snapshot.Operation, Replayed: true}, nil
}

func (p *Postgres) FindCreateReplay(ctx context.Context, tenantID uuid.UUID, scope string, key uuid.UUID, requestHash string) (CreateResult, bool, error) {
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return CreateResult{}, false, fmt.Errorf("begin find inference create replay: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return CreateResult{}, false, err
	}
	operation, err := scanOperation(tx.QueryRow(ctx, findReplaySQL, tenantID, scope, key), tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CreateResult{}, false, nil
	}
	if err != nil {
		return CreateResult{}, false, fmt.Errorf("find inference create replay: %w", err)
	}
	if err := classifyReplay(operation.RequestHash, requestHash); err != nil {
		return CreateResult{}, false, err
	}
	result, err := decodeCreateResult(operation)
	if err != nil {
		return CreateResult{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateResult{}, false, fmt.Errorf("commit inference create replay lookup: %w", err)
	}
	return result, true, nil
}

func (p *Postgres) CreateWithOperation(ctx context.Context, service domain.Service, operation domain.Operation) (result CreateResult, err error) {
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return result, fmt.Errorf("begin create inference service: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = setTenant(ctx, tx, service.TenantID); err != nil {
		return result, err
	}
	lockKey := service.TenantID.String() + "/" + operation.OperationScope + "/" + operation.IdempotencyKey.String()
	if _, err = tx.Exec(ctx, lockIdempotencySQL, lockKey); err != nil {
		return result, fmt.Errorf("lock inference idempotency key: %w", err)
	}

	existing, findErr := scanOperation(tx.QueryRow(ctx, findReplaySQL, service.TenantID, operation.OperationScope, operation.IdempotencyKey), service.TenantID)
	if findErr == nil {
		if err = classifyReplay(existing.RequestHash, operation.RequestHash); err != nil {
			return result, err
		}
		replayed, decodeErr := decodeCreateResult(existing)
		if decodeErr != nil {
			return result, decodeErr
		}
		if err = tx.Commit(ctx); err != nil {
			return result, fmt.Errorf("commit inference replay: %w", err)
		}
		return replayed, nil
	}
	if !errors.Is(findErr, pgx.ErrNoRows) {
		return result, fmt.Errorf("query inference idempotency key: %w", findErr)
	}

	desired, err := json.Marshal(service.DesiredSpec)
	if err != nil {
		return result, fmt.Errorf("marshal desired inference spec: %w", err)
	}
	applied, err := json.Marshal(service.AppliedSpec)
	if err != nil {
		return result, fmt.Errorf("marshal applied inference spec: %w", err)
	}
	placementMode := service.DesiredSpec.PlacementMode
	if placementMode == "" {
		placementMode = "auto"
	}
	_, err = tx.Exec(ctx, insertServiceSQL,
		service.ID, service.TenantID, service.Name, service.ModelVersionID, service.ServedModelName,
		service.ModelSnapshot, desired, applied, placementMode, service.Status,
		service.StatusReason, service.StatusMessage, service.Generation, service.ObservedGeneration,
		service.DesiredState, service.RuntimeRef, service.RuntimeEndpoint, service.InvocationURL,
		service.ReadyReplicas, operation.ID, service.CreatedAt, service.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return result, ErrNameConflict
		}
		return result, fmt.Errorf("insert inference service: %w", err)
	}
	before, err := json.Marshal(operation.BeforeSpec)
	if err != nil {
		return result, fmt.Errorf("marshal before inference spec: %w", err)
	}
	target, err := json.Marshal(operation.TargetSpec)
	if err != nil {
		return result, fmt.Errorf("marshal target inference spec: %w", err)
	}
	snapshot, err := json.Marshal(createResultSnapshot{Service: service, Operation: operation})
	if err != nil {
		return result, fmt.Errorf("marshal inference create result snapshot: %w", err)
	}
	operation.ResultSnapshot = snapshot
	_, err = tx.Exec(ctx, insertOperationSQL,
		operation.ID, operation.TenantID, operation.ServiceID, operation.Type,
		operation.OperationScope, operation.IdempotencyKey, operation.RequestHash,
		operation.TargetGeneration, before, target, operation.State, operation.Attempt,
		operation.NextAttemptAt, snapshot, operation.CreatedAt, operation.UpdatedAt,
	)
	if err != nil {
		return result, fmt.Errorf("insert inference operation: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return result, fmt.Errorf("commit inference create: %w", err)
	}
	return CreateResult{Service: service, Operation: operation}, nil
}

func (p *Postgres) BindRuntimeRef(ctx context.Context, binding RuntimeBinding) error {
	if binding.RuntimeRef == uuid.Nil {
		return errors.New("runtime reference is required")
	}
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin bind inference runtime: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx, binding.TenantID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, bindRuntimeRefSQL, binding.TenantID, binding.ServiceID,
		binding.Generation, binding.OperationID, binding.RuntimeRef, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("bind inference runtime: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleGeneration
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit bind inference runtime: %w", err)
	}
	return nil
}

func (p *Postgres) AbortCreate(ctx context.Context, binding RuntimeBinding) error {
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin abort inference create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx, binding.TenantID); err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, clearCreateCurrentOperationSQL,
		binding.TenantID, binding.ServiceID, binding.OperationID, now); err != nil {
		return fmt.Errorf("clear aborted inference create: %w", err)
	}
	if _, err := tx.Exec(ctx, abortCreateOperationSQL,
		binding.TenantID, binding.ServiceID, binding.OperationID); err != nil {
		return fmt.Errorf("delete aborted inference create operation: %w", err)
	}
	tag, err := tx.Exec(ctx, abortCreateServiceSQL, binding.TenantID, binding.ServiceID)
	if err != nil {
		return fmt.Errorf("delete aborted inference create service: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleGeneration
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit abort inference create: %w", err)
	}
	return nil
}

func (p *Postgres) AbortPendingMutation(ctx context.Context, abort MutationAbort) error {
	desired, err := json.Marshal(abort.RestoredSpec)
	if err != nil {
		return fmt.Errorf("marshal restored inference spec: %w", err)
	}
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin abort inference mutation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx, abort.TenantID); err != nil {
		return err
	}
	now := time.Now().UTC()
	tag, err := tx.Exec(ctx, abortPendingMutationServiceSQL, abort.TenantID, abort.ServiceID,
		abort.TargetGeneration, abort.OperationID, desired, abort.RestoredDesired,
		abort.RestoredGeneration, abort.RestoredStatus, now)
	if err != nil {
		return fmt.Errorf("restore aborted inference mutation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleGeneration
	}
	tag, err = tx.Exec(ctx, abortPendingMutationOperationSQL,
		abort.TenantID, abort.ServiceID, abort.OperationID, now)
	if err != nil {
		return fmt.Errorf("cancel aborted inference mutation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleGeneration
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit abort inference mutation: %w", err)
	}
	return nil
}

func (p *Postgres) GetService(ctx context.Context, tenantID, serviceID uuid.UUID) (domain.Service, error) {
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return domain.Service{}, fmt.Errorf("begin get inference service: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return domain.Service{}, err
	}
	service, err := scanService(tx.QueryRow(ctx, getServiceSQL, tenantID, serviceID))
	if err != nil {
		return domain.Service{}, mapNotFound(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Service{}, fmt.Errorf("commit get inference service: %w", err)
	}
	return service, nil
}

func (p *Postgres) ListServices(ctx context.Context, tenantID uuid.UUID) ([]domain.Service, error) {
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin list inference services: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, listServicesSQL, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list inference services: %w", err)
	}
	defer rows.Close()
	services := make([]domain.Service, 0)
	for rows.Next() {
		service, scanErr := scanPublicService(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		services = append(services, service)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inference services: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit list inference services: %w", err)
	}
	return services, nil
}

func (p *Postgres) MutateService(ctx context.Context, request MutationRequest) (result MutationResult, err error) {
	if err := validateMutationRequest(request); err != nil {
		return result, err
	}
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return result, fmt.Errorf("begin mutate inference service: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = setTenant(ctx, tx, request.TenantID); err != nil {
		return result, err
	}
	lockKey := request.TenantID.String() + "/" + request.OperationScope + "/" + request.IdempotencyKey.String()
	if _, err = tx.Exec(ctx, lockIdempotencySQL, lockKey); err != nil {
		return result, fmt.Errorf("lock inference mutation idempotency key: %w", err)
	}
	existing, findErr := scanOperation(tx.QueryRow(ctx, findReplaySQL,
		request.TenantID, request.OperationScope, request.IdempotencyKey), request.TenantID)
	if findErr == nil {
		if err = classifyReplay(existing.RequestHash, request.RequestHash); err != nil {
			return result, err
		}
		service, loadErr := scanService(tx.QueryRow(ctx, getServiceForMutationSQL, request.TenantID, request.ServiceID))
		if loadErr != nil {
			return result, mapNotFound(loadErr)
		}
		if err = tx.Commit(ctx); err != nil {
			return result, fmt.Errorf("commit inference mutation replay: %w", err)
		}
		existing.Replayed = true
		return MutationResult{Service: service, Operation: existing, Disposition: domain.TransitionReuseOperation}, nil
	}
	if !errors.Is(findErr, pgx.ErrNoRows) {
		return result, fmt.Errorf("query inference mutation idempotency key: %w", findErr)
	}

	service, err := scanService(tx.QueryRow(ctx, getServiceForMutationSQL, request.TenantID, request.ServiceID))
	if err != nil {
		return result, mapNotFound(err)
	}
	transition, err := domain.BeginTransition(service, request.Action, request.TargetSpec, request.OperationID)
	if err != nil {
		return result, err
	}
	now := request.Now.UTC()
	if transition.Disposition == domain.TransitionReuseOperation {
		operation, loadErr := scanOperation(tx.QueryRow(ctx, getOperationSQL, request.TenantID, transition.OperationID), request.TenantID)
		if loadErr != nil {
			return result, mapNotFound(loadErr)
		}
		if err = tx.Commit(ctx); err != nil {
			return result, fmt.Errorf("commit active inference operation replay: %w", err)
		}
		operation.Replayed = true
		return MutationResult{Service: transition.Service, Operation: operation, Disposition: transition.Disposition}, nil
	}
	if transition.Disposition == domain.TransitionAlreadyDesired {
		operation := domain.Operation{
			ID: request.OperationID, TenantID: request.TenantID, ServiceID: request.ServiceID,
			Type: request.Action, State: domain.OperationCompleted,
			TargetGeneration: service.Generation, BeforeSpec: service.AppliedSpec, TargetSpec: service.DesiredSpec,
			OperationScope: request.OperationScope, IdempotencyKey: request.IdempotencyKey,
			RequestHash: request.RequestHash, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now, CompletedAt: &now,
		}
		before, marshalErr := json.Marshal(operation.BeforeSpec)
		if marshalErr != nil {
			return result, fmt.Errorf("marshal no-op before inference spec: %w", marshalErr)
		}
		target, marshalErr := json.Marshal(operation.TargetSpec)
		if marshalErr != nil {
			return result, fmt.Errorf("marshal no-op target inference spec: %w", marshalErr)
		}
		if _, err = tx.Exec(ctx, insertCompletedOperationSQL,
			operation.ID, operation.TenantID, operation.ServiceID, operation.Type,
			operation.OperationScope, operation.IdempotencyKey, operation.RequestHash,
			operation.TargetGeneration, before, target, now,
		); err != nil {
			return result, fmt.Errorf("insert completed inference no-op: %w", err)
		}
		if _, err = tx.Exec(ctx, updateServiceCurrentOperationSQL,
			request.TenantID, request.ServiceID, operation.ID, now); err != nil {
			return result, fmt.Errorf("record completed inference no-op: %w", err)
		}
		transition.Service.CurrentOperationID = operation.ID
		transition.Service.UpdatedAt = now
		if err = tx.Commit(ctx); err != nil {
			return result, fmt.Errorf("commit completed inference no-op: %w", err)
		}
		return MutationResult{Service: transition.Service, Operation: operation, Disposition: transition.Disposition}, nil
	}

	operation := transition.Operation
	operation.OperationScope = request.OperationScope
	operation.IdempotencyKey = request.IdempotencyKey
	operation.RequestHash = request.RequestHash
	operation.NextAttemptAt = now
	operation.CreatedAt = now
	operation.UpdatedAt = now
	if operation.PreemptedOperationID != uuid.Nil {
		tag, cancelErr := tx.Exec(ctx, cancelOperationSQL, request.TenantID, request.ServiceID,
			operation.PreemptedOperationID, now)
		if cancelErr != nil {
			return result, fmt.Errorf("cancel preempted inference operation: %w", cancelErr)
		}
		if tag.RowsAffected() != 1 {
			return result, ErrStaleGeneration
		}
	}
	desired, err := json.Marshal(transition.Service.DesiredSpec)
	if err != nil {
		return result, fmt.Errorf("marshal mutated desired inference spec: %w", err)
	}
	tag, err := tx.Exec(ctx, updateServiceTransitionSQL,
		request.TenantID, request.ServiceID, desired, transition.Service.DesiredState,
		transition.Service.Generation, operation.ID, transition.Service.Status, now)
	if err != nil {
		return result, fmt.Errorf("update inference service transition: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return result, ErrStaleGeneration
	}
	before, err := json.Marshal(operation.BeforeSpec)
	if err != nil {
		return result, fmt.Errorf("marshal mutation before inference spec: %w", err)
	}
	target, err := json.Marshal(operation.TargetSpec)
	if err != nil {
		return result, fmt.Errorf("marshal mutation target inference spec: %w", err)
	}
	if _, err = tx.Exec(ctx, insertOperationSQL,
		operation.ID, operation.TenantID, operation.ServiceID, operation.Type,
		operation.OperationScope, operation.IdempotencyKey, operation.RequestHash,
		operation.TargetGeneration, before, target, operation.State, operation.Attempt,
		operation.NextAttemptAt, nil, operation.CreatedAt, operation.UpdatedAt,
	); err != nil {
		return result, fmt.Errorf("insert inference mutation operation: %w", err)
	}
	transition.Service.UpdatedAt = now
	if err = tx.Commit(ctx); err != nil {
		return result, fmt.Errorf("commit inference service mutation: %w", err)
	}
	return MutationResult{Service: transition.Service, Operation: operation, Disposition: transition.Disposition}, nil
}

func (p *Postgres) GetOperation(ctx context.Context, tenantID, operationID uuid.UUID) (domain.Operation, error) {
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return domain.Operation{}, fmt.Errorf("begin get inference operation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return domain.Operation{}, err
	}
	operation, err := scanOperation(tx.QueryRow(ctx, getOperationSQL, tenantID, operationID), tenantID)
	if err != nil {
		return domain.Operation{}, mapNotFound(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Operation{}, fmt.Errorf("commit get inference operation: %w", err)
	}
	return operation, nil
}

func (p *Postgres) ClaimOperation(ctx context.Context, owner string, now time.Time, leaseDuration time.Duration) (domain.Operation, bool, error) {
	if owner == "" || leaseDuration <= 0 {
		return domain.Operation{}, false, errors.New("lease owner and positive duration are required")
	}
	tx, err := p.platformPool.Begin(ctx)
	if err != nil {
		return domain.Operation{}, false, fmt.Errorf("begin claim inference operation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	leaseToken := uuid.New()
	operation, err := scanClaimedOperation(tx.QueryRow(ctx, claimOperationSQL, owner, now, now.Add(leaseDuration), leaseToken))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Operation{}, false, nil
	}
	if err != nil {
		return domain.Operation{}, false, fmt.Errorf("claim inference operation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Operation{}, false, fmt.Errorf("commit inference operation claim: %w", err)
	}
	return operation, true, nil
}

func (p *Postgres) ApplyObservation(ctx context.Context, observation Observation) error {
	tx, err := p.platformPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin inference observation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	applied, err := json.Marshal(observation.AppliedSpec)
	if err != nil {
		return fmt.Errorf("marshal applied inference spec: %w", err)
	}
	now := time.Now().UTC()
	tag, err := tx.Exec(ctx, applyObservationSQL, observation.TenantID, observation.ServiceID,
		observation.TargetGeneration, observation.OperationID, observation.Status, applied,
		observation.RuntimeRef, observation.RuntimeEndpoint, observation.ReadyReplicas, now,
		observation.Complete, observation.LeaseToken, observation.Deleted)
	if err != nil {
		return fmt.Errorf("apply inference observation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleGeneration
	}
	if observation.Complete {
		tag, err = tx.Exec(ctx, completeOperationSQL, observation.TenantID, observation.ServiceID,
			observation.TargetGeneration, observation.OperationID, now, observation.LeaseToken)
		if err != nil {
			return fmt.Errorf("complete inference operation: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return ErrStaleGeneration
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit inference observation: %w", err)
	}
	return nil
}

func (p *Postgres) FailOperation(ctx context.Context, failure Failure) error {
	state := domain.OperationFailed
	switch {
	case failure.DeadLetter:
		state = domain.OperationDeadLetter
	case failure.RetryAt != nil:
		state = domain.OperationPending
	}
	now := time.Now().UTC()
	tx, err := p.platformPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin fail inference operation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, failOperationSQL, failure.TenantID, failure.ServiceID,
		failure.TargetGeneration, failure.OperationID, state, failure.RetryAt,
		failure.ErrorCode, failure.ErrorMessage, now, failure.LeaseToken)
	if err != nil {
		return fmt.Errorf("fail inference operation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleGeneration
	}
	if failure.RetryAt == nil {
		if _, err := tx.Exec(ctx, failServiceSQL, failure.TenantID, failure.ServiceID,
			failure.TargetGeneration, failure.OperationID, failure.ErrorCode,
			failure.ErrorMessage, now); err != nil {
			return fmt.Errorf("fail inference service: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit failed inference operation: %w", err)
	}
	return nil
}

func (p *Postgres) BeginScaleRollback(ctx context.Context, request ScaleRollback) (int64, error) {
	now := time.Now().UTC()
	tx, err := p.platformPool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin inference scale rollback: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var desiredState domain.DesiredState
	var currentGeneration int64
	var currentOperation uuid.UUID
	var rollbackGeneration int64
	if err := setTenant(ctx, tx, request.TenantID); err != nil {
		return 0, err
	}
	err = tx.QueryRow(ctx, `
SELECT service.desired_state, service.generation, COALESCE(service.current_operation_id, '00000000-0000-0000-0000-000000000000'::uuid),
       COALESCE(operation.rollback_generation, 0)
FROM inference_services AS service
JOIN inference_operations AS operation
  ON operation.id = $3 AND operation.tenant_id = $1 AND operation.service_id = $2
WHERE service.tenant_id = $1 AND service.id = $2
`, request.TenantID, request.ServiceID, request.OperationID).Scan(
		&desiredState, &currentGeneration, &currentOperation, &rollbackGeneration)
	if err != nil {
		return 0, fmt.Errorf("load inference scale rollback: %w", err)
	}
	if desiredState == domain.DesiredStateDeleted {
		return 0, domain.ErrDeleted
	}
	if rollbackGeneration != 0 {
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("commit existing inference scale rollback: %w", err)
		}
		return rollbackGeneration, nil
	}
	if currentOperation != request.OperationID {
		return 0, fmt.Errorf("%w: scale rollback operation is not current", ErrStaleGeneration)
	}
	if err := tx.QueryRow(ctx, beginScaleRollbackServiceSQL, request.TenantID, request.ServiceID,
		currentGeneration, request.OperationID, "scale is rolling back to the previously applied spec", now).Scan(&rollbackGeneration); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("%w: scale rollback service generation %d", ErrStaleGeneration, currentGeneration)
		}
		return 0, fmt.Errorf("begin inference scale rollback service: %w", err)
	}
	tag, err := tx.Exec(ctx, beginScaleRollbackOperationSQL, request.TenantID, request.ServiceID,
		request.OperationID, rollbackGeneration,
		"scale is rolling back to the previously applied spec", now, request.LeaseToken)
	if err != nil {
		return 0, fmt.Errorf("begin inference scale rollback operation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return 0, ErrStaleGeneration
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit inference scale rollback: %w", err)
	}
	return rollbackGeneration, nil
}

func (p *Postgres) FinishScaleRollback(ctx context.Context, finish ScaleRollbackFinish) error {
	now := time.Now().UTC()
	status := domain.StatusFailed
	reason := "ROLLBACK_FAILED"
	message := "ROLLBACK_FAILED: inference scale rollback failed"
	if finish.Success {
		status = domain.StatusRunning
		reason = "SCALE_ROLLED_BACK"
		message = "SCALE_ROLLED_BACK: inference scale rolled back to the previously applied spec"
	}
	applied, err := json.Marshal(finish.AppliedSpec)
	if err != nil {
		return fmt.Errorf("marshal scale rollback spec: %w", err)
	}
	tx, err := p.platformPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin finish inference scale rollback: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx, finish.TenantID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, finishScaleRollbackServiceSQL, finish.TenantID, finish.ServiceID,
		finish.RollbackGeneration, finish.OperationID, status, finish.Success, applied,
		finish.RuntimeRef, finish.RuntimeEndpoint, finish.ReadyReplicas, reason, message, now)
	if err != nil {
		return fmt.Errorf("finish inference scale rollback service: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleGeneration
	}
	tag, err = tx.Exec(ctx, finishScaleRollbackOperationSQL, finish.TenantID, finish.ServiceID,
		finish.OperationID, finish.RollbackGeneration, reason, message, now, finish.LeaseToken)
	if err != nil {
		return fmt.Errorf("finish inference scale rollback operation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleGeneration
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit finish inference scale rollback: %w", err)
	}
	return nil
}

func setTenant(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
	if tenantID == uuid.Nil {
		return errors.New("tenant id is required")
	}
	if _, err := tx.Exec(ctx, setTenantSQL, tenantID.String()); err != nil {
		return fmt.Errorf("set inference tenant context: %w", err)
	}
	return nil
}

func validateMutationRequest(request MutationRequest) error {
	switch {
	case request.TenantID == uuid.Nil:
		return errors.New("tenant id is required")
	case request.ServiceID == uuid.Nil:
		return errors.New("service id is required")
	case request.OperationID == uuid.Nil:
		return errors.New("operation id is required")
	case request.IdempotencyKey == uuid.Nil:
		return errors.New("idempotency key is required")
	case request.OperationScope == "":
		return errors.New("operation scope is required")
	case request.RequestHash == "":
		return errors.New("request hash is required")
	case request.Now.IsZero():
		return errors.New("mutation time is required")
	default:
		return nil
	}
}

func scanService(row pgx.Row) (service domain.Service, err error) {
	var desired, applied []byte
	var activeAction domain.Action
	var activeState domain.OperationState
	err = row.Scan(&service.ID, &service.TenantID, &service.Name, &service.ModelVersionID,
		&service.ServedModelName, &service.ModelSnapshot, &service.Status, &service.StatusReason,
		&service.StatusMessage, &service.DesiredState, &service.Generation,
		&service.ObservedGeneration, &desired, &applied, &service.RuntimeRef,
		&service.RuntimeEndpoint, &service.InvocationURL, &service.ReadyReplicas,
		&service.CurrentOperationID, &activeAction, &activeState,
		&service.CreatedAt, &service.UpdatedAt, &service.DeletedAt, &service.LegacyQuarantined)
	if err != nil {
		return service, err
	}
	if err := json.Unmarshal(desired, &service.DesiredSpec); err != nil {
		return service, fmt.Errorf("decode desired inference spec: %w", err)
	}
	if err := json.Unmarshal(applied, &service.AppliedSpec); err != nil {
		return service, fmt.Errorf("decode applied inference spec: %w", err)
	}
	if activeState == domain.OperationPending || activeState == domain.OperationRunning {
		service.ActiveOperationID = service.CurrentOperationID
		service.ActiveOperation = activeAction
	}
	return service, nil
}

func scanPublicService(row pgx.Row) (service domain.Service, err error) {
	var desired, applied []byte
	err = row.Scan(&service.ID, &service.TenantID, &service.Name, &service.ModelVersionID,
		&service.ServedModelName, &service.ModelSnapshot, &service.Status, &service.StatusReason,
		&service.StatusMessage, &service.DesiredState, &service.Generation,
		&service.ObservedGeneration, &desired, &applied, &service.ReadyReplicas,
		&service.CurrentOperationID, &service.CreatedAt, &service.UpdatedAt,
		&service.DeletedAt, &service.LegacyQuarantined)
	if err != nil {
		return service, err
	}
	if err := json.Unmarshal(desired, &service.DesiredSpec); err != nil {
		return service, fmt.Errorf("decode desired inference spec: %w", err)
	}
	if err := json.Unmarshal(applied, &service.AppliedSpec); err != nil {
		return service, fmt.Errorf("decode applied inference spec: %w", err)
	}
	return service, nil
}

func scanOperation(row pgx.Row, tenantID uuid.UUID) (operation domain.Operation, err error) {
	operation.TenantID = tenantID
	var before, target []byte
	err = row.Scan(&operation.ID, &operation.ServiceID, &operation.Type, &operation.State,
		&operation.TargetGeneration, &operation.RollbackGeneration, &before, &target,
		&operation.OperationScope, &operation.IdempotencyKey, &operation.RequestHash,
		&operation.Attempt, &operation.NextAttemptAt, &operation.LeaseOwner, &operation.LeaseUntil,
		&operation.LeaseToken, &operation.RuntimeTaskID, &operation.ErrorCode, &operation.ErrorMessage,
		&operation.ResultSnapshot,
		&operation.CreatedAt, &operation.UpdatedAt, &operation.CompletedAt)
	if err != nil {
		return operation, err
	}
	if err := json.Unmarshal(before, &operation.BeforeSpec); err != nil {
		return operation, fmt.Errorf("decode before inference spec: %w", err)
	}
	if err := json.Unmarshal(target, &operation.TargetSpec); err != nil {
		return operation, fmt.Errorf("decode target inference spec: %w", err)
	}
	return operation, nil
}

func scanClaimedOperation(row pgx.Row) (operation domain.Operation, err error) {
	var before, target []byte
	err = row.Scan(&operation.ID, &operation.TenantID, &operation.ServiceID, &operation.Type,
		&operation.State, &operation.TargetGeneration, &operation.RollbackGeneration, &before, &target,
		&operation.OperationScope, &operation.IdempotencyKey, &operation.RequestHash,
		&operation.Attempt, &operation.NextAttemptAt, &operation.LeaseOwner,
		&operation.LeaseUntil, &operation.LeaseToken, &operation.RuntimeTaskID, &operation.ErrorCode,
		&operation.ErrorMessage, &operation.ResultSnapshot, &operation.CreatedAt, &operation.UpdatedAt,
		&operation.CompletedAt)
	if err != nil {
		return operation, err
	}
	if err := json.Unmarshal(before, &operation.BeforeSpec); err != nil {
		return operation, fmt.Errorf("decode before inference spec: %w", err)
	}
	if err := json.Unmarshal(target, &operation.TargetSpec); err != nil {
		return operation, fmt.Errorf("decode target inference spec: %w", err)
	}
	return operation, nil
}

func mapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
