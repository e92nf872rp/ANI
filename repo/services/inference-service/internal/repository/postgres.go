package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
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

const findAccessPolicyMutationSQL = `
SELECT request_hash, result_snapshot
FROM inference_access_policy_mutations
WHERE tenant_id = $1 AND operation_scope = $2 AND idempotency_key = $3
`

const insertAccessPolicyMutationSQL = `
INSERT INTO inference_access_policy_mutations (
    tenant_id, operation_scope, idempotency_key, request_hash, result_snapshot
) VALUES ($1, $2, $3, $4, $5::jsonb)
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
       service.created_at, service.updated_at, service.deleted_at, service.legacy_quarantined,
       service.publication_desired, service.publication_generation,
       service.publication_observed_generation, service.publication_phase,
       COALESCE(service.publication_last_error, ''), service.publication_updated_at
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
       service.created_at, service.updated_at, service.deleted_at, service.legacy_quarantined,
       service.publication_desired, service.publication_generation,
       service.publication_observed_generation, service.publication_phase,
       COALESCE(service.publication_last_error, ''), service.publication_updated_at
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
       service.created_at, service.updated_at, service.deleted_at, service.legacy_quarantined,
       service.publication_desired, service.publication_generation,
       service.publication_observed_generation, service.publication_phase,
       COALESCE(service.publication_last_error, ''), service.publication_updated_at
FROM inference_services AS service
WHERE service.tenant_id = $1 AND service.deleted_at IS NULL
ORDER BY service.created_at, service.id
`

const cancelOperationSQL = `
UPDATE inference_operations
SET state = 'cancelled', lease_owner = NULL, lease_until = NULL, lease_token = NULL,
    completed_at = $4, updated_at = $4
WHERE tenant_id = $1 AND service_id = $2 AND id = $3
  AND state = 'pending'
`

const updateServiceTransitionSQL = `
UPDATE inference_services
SET desired_spec = $3, desired_state = $4, generation = $5,
    current_operation_id = $6, status = $7, updated_at = $8,
    publication_desired = CASE WHEN $9 THEN 'unpublished' ELSE publication_desired END,
    publication_generation = CASE WHEN $9 THEN $5 ELSE publication_generation END,
    publication_phase = CASE WHEN $9 THEN 'pending' ELSE publication_phase END,
    publication_last_error = CASE WHEN $9 THEN NULL ELSE publication_last_error END,
    publication_updated_at = CASE WHEN $9 THEN $8 ELSE publication_updated_at END,
    invocation_url = CASE WHEN $9 THEN NULL ELSE invocation_url END
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

const claimPublicationSQL = `
WITH candidate AS (
    SELECT service.id
    FROM inference_services AS service
    WHERE service.deleted_at IS NULL
      AND (service.publication_generation <> service.publication_observed_generation
           OR service.publication_phase IN ('pending', 'publishing', 'unpublishing', 'failed'))
      AND (service.publication_lease_until IS NULL OR service.publication_lease_until <= $2)
    ORDER BY service.publication_updated_at, service.id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE inference_services AS service
SET publication_phase = CASE WHEN service.publication_desired = 'published' THEN 'publishing' ELSE 'unpublishing' END,
    publication_lease_owner = $1,
    publication_lease_until = $3,
    publication_lease_token = gen_random_uuid(),
    publication_updated_at = $2
FROM candidate
WHERE service.id = candidate.id
RETURNING service.tenant_id, service.id, service.publication_generation, service.publication_desired,
          service.served_model_name,
          COALESCE(NULLIF(service.desired_spec #>> '{execution_profile,task}', ''), 'generate'),
          COALESCE(service.runtime_endpoint, ''), service.publication_lease_token
`

const completePublicationSQL = `
UPDATE inference_services
SET publication_observed_generation = $3,
    publication_phase = $6,
    invocation_url = CASE
        WHEN $6 = 'published' AND publication_desired = 'published' THEN NULLIF($7, '')
        WHEN $6 = 'unpublished' AND publication_desired = 'unpublished' THEN NULL
        ELSE invocation_url
    END,
    publication_lease_owner = NULL,
    publication_lease_until = NULL,
    publication_lease_token = NULL,
    publication_last_error = NULL,
    publication_updated_at = $8
WHERE tenant_id = $1 AND id = $2 AND publication_generation = $3
  AND publication_lease_token = $4 AND publication_lease_until > $5
  AND ((publication_desired = 'published' AND publication_phase = 'publishing' AND $6 = 'published')
       OR (publication_desired = 'unpublished' AND publication_phase = 'unpublishing' AND $6 = 'unpublished'))
`

const renewPublicationSQL = `
UPDATE inference_services
SET publication_lease_until = $5
WHERE tenant_id = $1 AND id = $2 AND publication_generation = $3
  AND publication_lease_token = $4 AND publication_lease_until > $6
  AND publication_phase IN ('publishing', 'unpublishing')
`

const failPublicationSQL = `
UPDATE inference_services
SET publication_phase = 'failed',
    invocation_url = CASE WHEN publication_desired = 'published' THEN NULL ELSE invocation_url END,
    publication_lease_owner = NULL,
    publication_lease_until = NULL,
    publication_lease_token = NULL,
    publication_last_error = $5,
    publication_updated_at = $6
WHERE tenant_id = $1 AND id = $2 AND publication_generation = $3
  AND publication_lease_token = $4 AND publication_lease_until > $6
`

const publicationWithdrawnSQL = `
SELECT EXISTS (
    SELECT 1
    FROM inference_services
    WHERE tenant_id = $1 AND id = $2 AND publication_generation = $3
      AND publication_observed_generation = $3
      AND publication_desired = 'unpublished' AND publication_phase = 'unpublished'
      AND invocation_url IS NULL AND deleted_at IS NULL
)
`

const resolvePublishedServiceSQL = `
SELECT service.id, service.tenant_id, service.name, service.model_version_id,
       service.served_model_name, service.model_display_snapshot,
       service.status, COALESCE(service.status_reason, ''), COALESCE(service.status_message, ''),
       service.desired_state, service.generation, service.observed_generation,
       service.desired_spec, service.applied_spec,
       COALESCE(service.runtime_ref, '00000000-0000-0000-0000-000000000000'::uuid),
       COALESCE(service.runtime_endpoint, ''), COALESCE(service.invocation_url, ''),
       service.ready_replicas,
       COALESCE(service.current_operation_id, '00000000-0000-0000-0000-000000000000'::uuid),
       '' AS active_type, '' AS active_state,
       service.created_at, service.updated_at, service.deleted_at, service.legacy_quarantined,
       service.publication_desired, service.publication_generation,
       service.publication_observed_generation, service.publication_phase,
       COALESCE(service.publication_last_error, ''), service.publication_updated_at
FROM inference_services AS service
WHERE service.tenant_id = $1
  AND service.served_model_name = $2
  AND service.deleted_at IS NULL
  AND (service.status = 'running'
      OR (service.status = 'deploying'
          AND EXISTS (
              SELECT 1
              FROM inference_operations AS operation
              WHERE operation.id = service.current_operation_id
                AND operation.tenant_id = service.tenant_id
                AND operation.service_id = service.id
                AND operation.type = 'scale'
                AND operation.state IN ('pending', 'running')
                AND (service.generation = operation.target_generation
                     OR service.generation = operation.rollback_generation)
          )
      )
  )
  AND service.desired_state = 'running'
  AND service.publication_desired = 'published'
  AND service.publication_phase = 'published'
  AND service.publication_generation = service.publication_observed_generation
  AND service.invocation_url IS NOT NULL
LIMIT 1
`

const applyObservationSQL = `
UPDATE inference_services AS service
SET status = $5,
    applied_spec = CASE WHEN $11 THEN $6 ELSE applied_spec END,
    runtime_ref = NULLIF($7, '00000000-0000-0000-0000-000000000000'::uuid),
    runtime_endpoint = NULLIF($8, ''), ready_replicas = $9,
    observed_generation = CASE WHEN $11 THEN $3 ELSE observed_generation END,
    deleted_at = CASE WHEN $13 THEN $10 ELSE deleted_at END,
    publication_desired = CASE WHEN $11 AND $14 AND $5 = 'running' AND NOT $13 AND service.desired_state = 'running' AND operation.type IN ('create', 'start', 'restart') THEN 'published' ELSE publication_desired END,
    publication_generation = CASE WHEN $11 AND $14 AND $5 = 'running' AND NOT $13 AND service.desired_state = 'running' AND operation.type IN ('create', 'start', 'restart') THEN $3 ELSE publication_generation END,
    publication_phase = CASE WHEN $11 AND $14 AND $5 = 'running' AND NOT $13 AND service.desired_state = 'running' AND operation.type IN ('create', 'start', 'restart') THEN 'pending' ELSE publication_phase END,
    publication_last_error = CASE WHEN $11 AND $14 AND $5 = 'running' AND NOT $13 AND service.desired_state = 'running' AND operation.type IN ('create', 'start', 'restart') THEN NULL ELSE publication_last_error END,
    publication_updated_at = CASE WHEN $11 AND $14 AND $5 = 'running' AND NOT $13 AND service.desired_state = 'running' AND operation.type IN ('create', 'start', 'restart') THEN $10 ELSE publication_updated_at END,
    invocation_url = CASE WHEN $11 AND $14 AND $5 = 'running' AND NOT $13 AND service.desired_state = 'running' AND operation.type IN ('create', 'start', 'restart') THEN NULL ELSE invocation_url END,
    updated_at = $10
FROM inference_operations AS operation
WHERE service.tenant_id = $1 AND service.id = $2 AND service.generation = $3
  AND service.current_operation_id = $4
  AND operation.id = $4 AND operation.tenant_id = $1 AND operation.service_id = $2
  AND operation.target_generation = $3 AND operation.lease_token = $12
  AND operation.lease_until > $10 AND operation.state = 'running'
`

const completeOperationSQL = `
UPDATE inference_operations
SET state = 'completed', lease_owner = NULL, lease_until = NULL,
    lease_token = NULL, error_code = NULL, error_message = NULL,
    next_attempt_at = $5, completed_at = $5, updated_at = $5
WHERE tenant_id = $1 AND service_id = $2 AND target_generation = $3 AND id = $4
  AND lease_token = $6 AND lease_until > $5 AND state = 'running'
`

const failOperationSQL = `
UPDATE inference_operations
SET state = $5,
    attempt = attempt + CASE
        WHEN $7 IN ('GATEWAY_UNPUBLISH_PENDING', 'GATEWAY_UNPUBLISH_CHECK_FAILED') THEN 0
        ELSE 1
    END,
    next_attempt_at = COALESCE($6, next_attempt_at),
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

// Ping proves both tenant-scoped and platform claim pools are reachable.
func (p *Postgres) Ping(ctx context.Context) error {
	if p == nil || p.tenantPool == nil || p.platformPool == nil {
		return errors.New("inference database readiness failed")
	}
	if err := p.tenantPool.Ping(ctx); err != nil {
		return errors.New("inference database readiness failed")
	}
	if err := p.platformPool.Ping(ctx); err != nil {
		return errors.New("inference database readiness failed")
	}
	return nil
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

func requiresPublicationWithdrawal(action domain.Action) bool {
	switch action {
	case domain.ActionStop, domain.ActionRestart, domain.ActionDelete:
		return true
	default:
		return false
	}
}

func mutationTransitionArgs(request MutationRequest, service domain.Service, operationID uuid.UUID, desired []byte, now time.Time) ([]any, bool) {
	withdraw := requiresPublicationWithdrawal(request.Action)
	return []any{
		request.TenantID, request.ServiceID, desired, service.DesiredState,
		service.Generation, operationID, service.Status, now, withdraw,
	}, withdraw
}

func observationArgs(observation Observation, applied []byte, now time.Time) []any {
	return []any{
		observation.TenantID, observation.ServiceID, observation.TargetGeneration,
		observation.OperationID, observation.Status, applied, observation.RuntimeRef,
		observation.RuntimeEndpoint, observation.ReadyReplicas, now, observation.Complete,
		observation.LeaseToken, observation.Deleted, observation.Publish,
	}
}

func withPublicationWithdrawal(service domain.Service, now time.Time) domain.Service {
	service.Publication.Desired = domain.PublicationUnpublished
	service.Publication.Generation = service.Generation
	service.Publication.Phase = domain.PublicationPending
	service.Publication.LastError = ""
	service.Publication.UpdatedAt = now
	service.InvocationURL = ""
	return service
}

func validatePreemptionFence(service domain.Service, operation domain.Operation) error {
	if operation.PreemptedOperationID != uuid.Nil &&
		service.ActiveOperation == domain.ActionCreate && service.RuntimeRef == uuid.Nil {
		return fmt.Errorf("%w: create runtime identity is not bound", domain.ErrOperationInProgress)
	}
	return nil
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
		// Request-path create may be inside Core Ensure before BindRuntimeRef. Since
		// the Core lifecycle API cannot fence by generation, cancelling this row
		// would let a late Ensure leave an untracked workload. Once binding commits,
		// a retry has a stable runtime identity that stop/delete can converge.
		if err = validatePreemptionFence(service, operation); err != nil {
			return result, err
		}
		tag, cancelErr := tx.Exec(ctx, cancelOperationSQL, request.TenantID, request.ServiceID,
			operation.PreemptedOperationID, now)
		if cancelErr != nil {
			return result, fmt.Errorf("cancel preempted inference operation: %w", cancelErr)
		}
		if tag.RowsAffected() != 1 {
			return result, fmt.Errorf("%w: active inference operation is already claimed", domain.ErrOperationInProgress)
		}
	}
	desired, err := json.Marshal(transition.Service.DesiredSpec)
	if err != nil {
		return result, fmt.Errorf("marshal mutated desired inference spec: %w", err)
	}
	transitionArgs, withdrawPublication := mutationTransitionArgs(request, transition.Service, operation.ID, desired, now)
	tag, err := tx.Exec(ctx, updateServiceTransitionSQL, transitionArgs...)
	if err != nil {
		return result, fmt.Errorf("update inference service transition: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return result, ErrStaleGeneration
	}
	if withdrawPublication {
		transition.Service = withPublicationWithdrawal(transition.Service, now)
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

const (
	maxPublicationLease       = 5 * time.Minute
	publicationFailureMessage = "GATEWAY_PUBLISH_FAILED"
)

func redactPublicationError(string) string {
	return publicationFailureMessage
}

func (p *Postgres) ClaimPublication(ctx context.Context, owner string, now time.Time, leaseDuration time.Duration) (PublicationTarget, bool, error) {
	if strings.TrimSpace(owner) == "" || leaseDuration <= 0 || leaseDuration > maxPublicationLease {
		return PublicationTarget{}, false, errors.New("publication lease owner and bounded positive duration are required")
	}
	tx, err := p.platformPool.Begin(ctx)
	if err != nil {
		return PublicationTarget{}, false, fmt.Errorf("begin claim inference publication: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var target PublicationTarget
	err = tx.QueryRow(ctx, claimPublicationSQL, owner, now.UTC(), now.UTC().Add(leaseDuration)).Scan(
		&target.TenantID, &target.ServiceID, &target.Generation, &target.Desired,
		&target.ServedModelName, &target.Task, &target.RuntimeEndpoint, &target.LeaseToken,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublicationTarget{}, false, nil
	}
	if err != nil {
		return PublicationTarget{}, false, fmt.Errorf("claim inference publication: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PublicationTarget{}, false, fmt.Errorf("commit inference publication claim: %w", err)
	}
	return target, true, nil
}

func (p *Postgres) RenewPublication(ctx context.Context, target PublicationTarget, now time.Time, leaseDuration time.Duration) error {
	if target.TenantID == uuid.Nil || target.ServiceID == uuid.Nil || target.Generation < 0 || target.LeaseToken == uuid.Nil || now.IsZero() || leaseDuration <= 0 || leaseDuration > maxPublicationLease {
		return errors.New("publication renewal requires tenant, service, generation, lease token, time, and bounded duration")
	}
	tx, err := p.platformPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin renew inference publication: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx, target.TenantID); err != nil {
		return err
	}
	now = now.UTC()
	tag, err := tx.Exec(ctx, renewPublicationSQL, target.TenantID, target.ServiceID, target.Generation, target.LeaseToken, now.Add(leaseDuration), now)
	if err != nil {
		return fmt.Errorf("renew inference publication: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleGeneration
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit inference publication renewal: %w", err)
	}
	return nil
}

func (p *Postgres) CompletePublication(ctx context.Context, result PublicationResult) error {
	if result.TenantID == uuid.Nil || result.ServiceID == uuid.Nil || result.Generation < 0 || result.LeaseToken == uuid.Nil || result.Now.IsZero() {
		return errors.New("publication completion requires tenant, service, generation, lease token, and time")
	}
	if result.Phase != domain.PublicationPublishedOK && result.Phase != domain.PublicationUnpublishedOK {
		return errors.New("publication completion phase must be published or unpublished")
	}
	if result.Phase == domain.PublicationPublishedOK && strings.TrimSpace(result.InvocationURL) == "" {
		return errors.New("published publication completion requires invocation url")
	}
	tx, err := p.platformPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin complete inference publication: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx, result.TenantID); err != nil {
		return err
	}
	now := result.Now.UTC()
	tag, err := tx.Exec(ctx, completePublicationSQL, result.TenantID, result.ServiceID, result.Generation,
		result.LeaseToken, now, result.Phase, strings.TrimSpace(result.InvocationURL), now)
	if err != nil {
		return fmt.Errorf("complete inference publication: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleGeneration
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit inference publication completion: %w", err)
	}
	return nil
}

func (p *Postgres) FailPublication(ctx context.Context, target PublicationTarget, message string, now time.Time) error {
	if target.TenantID == uuid.Nil || target.ServiceID == uuid.Nil || target.Generation < 0 || target.LeaseToken == uuid.Nil || now.IsZero() {
		return errors.New("publication failure requires tenant, service, generation, lease token, and time")
	}
	tx, err := p.platformPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin fail inference publication: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx, target.TenantID); err != nil {
		return err
	}
	now = now.UTC()
	tag, err := tx.Exec(ctx, failPublicationSQL, target.TenantID, target.ServiceID, target.Generation,
		target.LeaseToken, redactPublicationError(message), now)
	if err != nil {
		return fmt.Errorf("fail inference publication: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleGeneration
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit failed inference publication: %w", err)
	}
	return nil
}

func (p *Postgres) PublicationWithdrawn(ctx context.Context, tenantID, serviceID uuid.UUID, generation int64) (bool, error) {
	if tenantID == uuid.Nil || serviceID == uuid.Nil || generation < 0 {
		return false, errors.New("publication withdrawal lookup requires tenant, service, and generation")
	}
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin inference publication withdrawal lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return false, err
	}
	var withdrawn bool
	if err := tx.QueryRow(ctx, publicationWithdrawnSQL, tenantID, serviceID, generation).Scan(&withdrawn); err != nil {
		return false, fmt.Errorf("lookup inference publication withdrawal: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit inference publication withdrawal lookup: %w", err)
	}
	return withdrawn, nil
}

func (p *Postgres) ResolvePublishedService(ctx context.Context, tenantID uuid.UUID, servedModelName string) (domain.Service, error) {
	if tenantID == uuid.Nil || strings.TrimSpace(servedModelName) == "" {
		return domain.Service{}, errors.New("published service resolution requires tenant and served model name")
	}
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return domain.Service{}, fmt.Errorf("begin resolve published inference service: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return domain.Service{}, err
	}
	service, err := scanService(tx.QueryRow(ctx, resolvePublishedServiceSQL, tenantID, servedModelName))
	if err != nil {
		return domain.Service{}, mapNotFound(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Service{}, fmt.Errorf("commit resolve published inference service: %w", err)
	}
	return service, nil
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
	tag, err := tx.Exec(ctx, applyObservationSQL, observationArgs(observation, applied, now)...)
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

const accessPolicySelectSQL = `
SELECT p.id, p.tenant_id, p.name, p.status, COALESCE(p.description,''), p.priority,
       p.scope_type, p.allow_all_tenant_keys, COALESCE(p.rate_qps,0), COALESCE(p.rate_rpm,0),
       COALESCE(p.max_in_flight,0), p.lease_ttl_seconds,
       COALESCE(array_agg(DISTINCT s.inference_service_id) FILTER (WHERE s.inference_service_id IS NOT NULL), '{}'),
       COALESCE(array_agg(DISTINCT k.api_key_id::text) FILTER (WHERE k.effect = 'scope'), '{}'),
       COALESCE(array_agg(DISTINCT k.api_key_id::text) FILTER (WHERE k.effect = 'allow'), '{}'),
       COALESCE(array_agg(DISTINCT k.api_key_id::text) FILTER (WHERE k.effect = 'deny'), '{}'),
       p.created_at, p.updated_at
FROM inference_access_policies p
LEFT JOIN inference_access_policy_services s ON s.policy_id = p.id AND s.tenant_id = p.tenant_id
LEFT JOIN inference_access_policy_api_keys k ON k.policy_id = p.id AND k.tenant_id = p.tenant_id
WHERE p.tenant_id = $1 AND p.deleted_at IS NULL
GROUP BY p.id
ORDER BY p.priority, p.created_at, p.id`

func scanAccessPolicy(row pgx.Row) (domain.AccessPolicy, error) {
	var p domain.AccessPolicy
	var scope string
	var serviceIDs []uuid.UUID
	var scopeKeyIDs, allowKeyIDs, denyKeyIDs []string
	if err := row.Scan(&p.ID, &p.TenantID, &p.Name, &p.Status, &p.Description, &p.Priority, &scope, &p.Access.AllowAllTenantKeys, &p.RateLimits.QPS, &p.RateLimits.RPM, &p.Concurrency.MaxInFlight, &p.Concurrency.LeaseTTLSeconds, &serviceIDs, &scopeKeyIDs, &allowKeyIDs, &denyKeyIDs, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return domain.AccessPolicy{}, err
	}
	p.Scope.Type = domain.AccessPolicyScopeType(scope)
	p.Scope.InferenceServiceIDs = serviceIDs
	p.Scope.APIKeyIDs = scopeKeyIDs
	p.Access.AllowAPIKeyIDs = allowKeyIDs
	p.Access.DenyAPIKeyIDs = denyKeyIDs
	return p, nil
}

func (p *Postgres) ListAccessPolicies(ctx context.Context, tenantID uuid.UUID) ([]domain.AccessPolicy, error) {
	if tenantID == uuid.Nil {
		return nil, errors.New("tenant id is required")
	}
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, accessPolicySelectSQL, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list access policies: %w", err)
	}
	defer rows.Close()
	items := make([]domain.AccessPolicy, 0)
	for rows.Next() {
		item, scanErr := scanAccessPolicy(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan access policy: %w", scanErr)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func (p *Postgres) GetAccessPolicy(ctx context.Context, tenantID, policyID uuid.UUID) (domain.AccessPolicy, error) {
	if tenantID == uuid.Nil || policyID == uuid.Nil {
		return domain.AccessPolicy{}, errors.New("tenant and policy ids are required")
	}
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return domain.AccessPolicy{}, err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return domain.AccessPolicy{}, err
	}
	query := strings.Replace(accessPolicySelectSQL, "WHERE p.tenant_id = $1", "WHERE p.tenant_id = $1 AND p.id = $2", 1)
	row := tx.QueryRow(ctx, query+" LIMIT 1", tenantID, policyID)
	item, err := scanAccessPolicy(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AccessPolicy{}, ErrNotFound
	}
	if err != nil {
		return domain.AccessPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AccessPolicy{}, err
	}
	return item, nil
}

func hashAccessPolicyMutation(operationScope string, intent any) (string, error) {
	encoded, err := json.Marshal(struct {
		OperationScope string `json:"operation_scope"`
		Intent         any    `json:"intent"`
	}{OperationScope: operationScope, Intent: intent})
	if err != nil {
		return "", fmt.Errorf("marshal access policy mutation intent: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func isCanonicalSHA256(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size
}

func beginAccessPolicyMutation(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, operationScope string, idempotencyKey uuid.UUID, requestHash string) ([]byte, bool, error) {
	if idempotencyKey == uuid.Nil {
		return nil, false, errors.New("idempotency key is required")
	}
	lockKey := tenantID.String() + "/" + operationScope + "/" + idempotencyKey.String()
	if _, err := tx.Exec(ctx, lockIdempotencySQL, lockKey); err != nil {
		return nil, false, fmt.Errorf("lock access policy idempotency key: %w", err)
	}
	var existingHash string
	var snapshot []byte
	err := tx.QueryRow(ctx, findAccessPolicyMutationSQL, tenantID, operationScope, idempotencyKey).Scan(&existingHash, &snapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("query access policy idempotency key: %w", err)
	}
	if err := classifyReplay(existingHash, requestHash); err != nil {
		return nil, false, err
	}
	return snapshot, true, nil
}

func completeAccessPolicyMutation(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, operationScope string, idempotencyKey uuid.UUID, requestHash string, result any) error {
	snapshot, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal access policy mutation result: %w", err)
	}
	if _, err := tx.Exec(ctx, insertAccessPolicyMutationSQL, tenantID, operationScope, idempotencyKey, requestHash, string(snapshot)); err != nil {
		return fmt.Errorf("persist access policy idempotency result: %w", err)
	}
	return nil
}

func decodeAccessPolicyMutation[T any](snapshot []byte) (T, error) {
	var result T
	if err := json.Unmarshal(snapshot, &result); err != nil {
		return result, fmt.Errorf("decode access policy idempotency result: %w", err)
	}
	return result, nil
}

func (p *Postgres) CreateAccessPolicy(ctx context.Context, policy domain.AccessPolicy, idempotencyKey uuid.UUID) (domain.AccessPolicy, error) {
	if policy.Concurrency.LeaseTTLSeconds == 0 {
		policy.Concurrency.LeaseTTLSeconds = 60
	}
	if err := policy.Validate(); err != nil {
		return domain.AccessPolicy{}, err
	}
	if idempotencyKey == uuid.Nil {
		return domain.AccessPolicy{}, errors.New("idempotency key is required")
	}
	intent := policy
	intent.ID, intent.CreatedAt, intent.UpdatedAt = uuid.Nil, time.Time{}, time.Time{}
	operationScope := "inference_access_policy.create"
	requestHash, err := hashAccessPolicyMutation(operationScope, intent)
	if err != nil {
		return domain.AccessPolicy{}, err
	}
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return domain.AccessPolicy{}, err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, policy.TenantID); err != nil {
		return domain.AccessPolicy{}, err
	}
	snapshot, replayed, err := beginAccessPolicyMutation(ctx, tx, policy.TenantID, operationScope, idempotencyKey, requestHash)
	if err != nil {
		return domain.AccessPolicy{}, err
	}
	if replayed {
		result, decodeErr := decodeAccessPolicyMutation[domain.AccessPolicy](snapshot)
		if decodeErr != nil {
			return domain.AccessPolicy{}, decodeErr
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.AccessPolicy{}, err
		}
		return result, nil
	}
	if policy.ID == uuid.Nil {
		policy.ID = uuid.New()
	}
	policy.CreatedAt = time.Now().UTC()
	policy.UpdatedAt = policy.CreatedAt
	_, err = tx.Exec(ctx, `INSERT INTO inference_access_policies (id,tenant_id,name,status,description,priority,scope_type,allow_all_tenant_keys,rate_qps,rate_rpm,max_in_flight,lease_ttl_seconds,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,0),NULLIF($10,0),NULLIF($11,0),$12,$13,$14)`, policy.ID, policy.TenantID, policy.Name, policy.Status, policy.Description, policy.Priority, policy.Scope.Type, policy.Access.AllowAllTenantKeys, policy.RateLimits.QPS, policy.RateLimits.RPM, policy.Concurrency.MaxInFlight, policy.Concurrency.LeaseTTLSeconds, policy.CreatedAt, policy.UpdatedAt)
	if err != nil {
		return domain.AccessPolicy{}, mapPolicyWriteError(err)
	}
	for _, serviceID := range policy.Scope.InferenceServiceIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO inference_access_policy_services(policy_id,tenant_id,inference_service_id) VALUES($1,$2,$3)`, policy.ID, policy.TenantID, serviceID); err != nil {
			return domain.AccessPolicy{}, err
		}
	}
	for _, keyID := range policy.Scope.APIKeyIDs {
		parsed, parseErr := uuid.Parse(keyID)
		if parseErr != nil {
			return domain.AccessPolicy{}, parseErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO inference_access_policy_api_keys(policy_id,tenant_id,api_key_id,key_prefix,effect) VALUES($1,$2,$3,$4,'scope') ON CONFLICT DO NOTHING`, policy.ID, policy.TenantID, parsed, ""); err != nil {
			return domain.AccessPolicy{}, err
		}
	}
	for _, keyID := range policy.Access.AllowAPIKeyIDs {
		parsed, parseErr := uuid.Parse(keyID)
		if parseErr != nil {
			return domain.AccessPolicy{}, parseErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO inference_access_policy_api_keys(policy_id,tenant_id,api_key_id,key_prefix,effect) VALUES($1,$2,$3,$4,'allow') ON CONFLICT DO NOTHING`, policy.ID, policy.TenantID, parsed, ""); err != nil {
			return domain.AccessPolicy{}, err
		}
	}
	for _, keyID := range policy.Access.DenyAPIKeyIDs {
		parsed, parseErr := uuid.Parse(keyID)
		if parseErr != nil {
			return domain.AccessPolicy{}, parseErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO inference_access_policy_api_keys(policy_id,tenant_id,api_key_id,key_prefix,effect) VALUES($1,$2,$3,$4,'deny') ON CONFLICT DO NOTHING`, policy.ID, policy.TenantID, parsed, ""); err != nil {
			return domain.AccessPolicy{}, err
		}
	}
	if err := completeAccessPolicyMutation(ctx, tx, policy.TenantID, operationScope, idempotencyKey, requestHash, policy); err != nil {
		return domain.AccessPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AccessPolicy{}, err
	}
	return policy, nil
}

func (p *Postgres) UpdateAccessPolicy(ctx context.Context, policy domain.AccessPolicy, idempotencyKey uuid.UUID, requestHash string) (domain.AccessPolicy, error) {
	if policy.Concurrency.LeaseTTLSeconds == 0 {
		policy.Concurrency.LeaseTTLSeconds = 60
	}
	if err := policy.Validate(); err != nil {
		return domain.AccessPolicy{}, err
	}
	if policy.ID == uuid.Nil || idempotencyKey == uuid.Nil {
		return domain.AccessPolicy{}, errors.New("policy and idempotency ids are required")
	}
	if !isCanonicalSHA256(requestHash) {
		return domain.AccessPolicy{}, errors.New("canonical request hash is required")
	}
	operationScope := "inference_access_policy.update/" + policy.ID.String()
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return domain.AccessPolicy{}, err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, policy.TenantID); err != nil {
		return domain.AccessPolicy{}, err
	}
	snapshot, replayed, err := beginAccessPolicyMutation(ctx, tx, policy.TenantID, operationScope, idempotencyKey, requestHash)
	if err != nil {
		return domain.AccessPolicy{}, err
	}
	if replayed {
		result, decodeErr := decodeAccessPolicyMutation[domain.AccessPolicy](snapshot)
		if decodeErr != nil {
			return domain.AccessPolicy{}, decodeErr
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.AccessPolicy{}, err
		}
		return result, nil
	}
	policy.UpdatedAt = time.Now().UTC()
	err = tx.QueryRow(ctx, `UPDATE inference_access_policies SET name=$3,status=$4,description=$5,priority=$6,scope_type=$7,allow_all_tenant_keys=$8,rate_qps=NULLIF($9,0),rate_rpm=NULLIF($10,0),max_in_flight=NULLIF($11,0),lease_ttl_seconds=$12,updated_at=$13 WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NULL RETURNING created_at`, policy.TenantID, policy.ID, policy.Name, policy.Status, policy.Description, policy.Priority, policy.Scope.Type, policy.Access.AllowAllTenantKeys, policy.RateLimits.QPS, policy.RateLimits.RPM, policy.Concurrency.MaxInFlight, policy.Concurrency.LeaseTTLSeconds, policy.UpdatedAt).Scan(&policy.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AccessPolicy{}, ErrNotFound
		}
		return domain.AccessPolicy{}, mapPolicyWriteError(err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM inference_access_policy_services WHERE tenant_id=$1 AND policy_id=$2`, policy.TenantID, policy.ID); err != nil {
		return domain.AccessPolicy{}, err
	}
	for _, serviceID := range policy.Scope.InferenceServiceIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO inference_access_policy_services(policy_id,tenant_id,inference_service_id) VALUES($1,$2,$3)`, policy.ID, policy.TenantID, serviceID); err != nil {
			return domain.AccessPolicy{}, err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM inference_access_policy_api_keys WHERE tenant_id=$1 AND policy_id=$2`, policy.TenantID, policy.ID); err != nil {
		return domain.AccessPolicy{}, err
	}
	for _, keyID := range policy.Scope.APIKeyIDs {
		parsed, parseErr := uuid.Parse(keyID)
		if parseErr != nil {
			return domain.AccessPolicy{}, parseErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO inference_access_policy_api_keys(policy_id,tenant_id,api_key_id,key_prefix,effect) VALUES($1,$2,$3,$4,'scope')`, policy.ID, policy.TenantID, parsed, ""); err != nil {
			return domain.AccessPolicy{}, err
		}
	}
	for _, keyID := range policy.Access.AllowAPIKeyIDs {
		parsed, parseErr := uuid.Parse(keyID)
		if parseErr != nil {
			return domain.AccessPolicy{}, parseErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO inference_access_policy_api_keys(policy_id,tenant_id,api_key_id,key_prefix,effect) VALUES($1,$2,$3,$4,'allow')`, policy.ID, policy.TenantID, parsed, ""); err != nil {
			return domain.AccessPolicy{}, err
		}
	}
	for _, keyID := range policy.Access.DenyAPIKeyIDs {
		parsed, parseErr := uuid.Parse(keyID)
		if parseErr != nil {
			return domain.AccessPolicy{}, parseErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO inference_access_policy_api_keys(policy_id,tenant_id,api_key_id,key_prefix,effect) VALUES($1,$2,$3,$4,'deny')`, policy.ID, policy.TenantID, parsed, ""); err != nil {
			return domain.AccessPolicy{}, err
		}
	}
	if err := completeAccessPolicyMutation(ctx, tx, policy.TenantID, operationScope, idempotencyKey, requestHash, policy); err != nil {
		return domain.AccessPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AccessPolicy{}, err
	}
	return policy, nil
}

func (p *Postgres) DeleteAccessPolicy(ctx context.Context, tenantID, policyID, idempotencyKey uuid.UUID) error {
	if tenantID == uuid.Nil || policyID == uuid.Nil || idempotencyKey == uuid.Nil {
		return errors.New("tenant, policy, and idempotency ids are required")
	}
	operationScope := "inference_access_policy.delete/" + policyID.String()
	requestHash, err := hashAccessPolicyMutation(operationScope, struct {
		PolicyID uuid.UUID `json:"policy_id"`
	}{PolicyID: policyID})
	if err != nil {
		return err
	}
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return err
	}
	_, replayed, err := beginAccessPolicyMutation(ctx, tx, tenantID, operationScope, idempotencyKey, requestHash)
	if err != nil {
		return err
	}
	if replayed {
		return tx.Commit(ctx)
	}
	tag, err := tx.Exec(ctx, `UPDATE inference_access_policies SET deleted_at=NOW(),status='disabled',updated_at=NOW() WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NULL`, tenantID, policyID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	if err := completeAccessPolicyMutation(ctx, tx, tenantID, operationScope, idempotencyKey, requestHash, struct{}{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func mapPolicyWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrNameConflict
	}
	return err
}

func (p *Postgres) ListServiceAccessPolicies(ctx context.Context, tenantID, serviceID uuid.UUID) ([]domain.AccessPolicy, error) {
	if tenantID == uuid.Nil || serviceID == uuid.Nil {
		return nil, errors.New("tenant and service ids are required")
	}
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return nil, err
	}
	items, err := listServiceAccessPoliciesTx(ctx, tx, tenantID, serviceID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func listServiceAccessPoliciesTx(ctx context.Context, tx pgx.Tx, tenantID, serviceID uuid.UUID) ([]domain.AccessPolicy, error) {
	query := strings.Replace(accessPolicySelectSQL, "WHERE p.tenant_id = $1", "WHERE p.tenant_id = $1 AND EXISTS (SELECT 1 FROM inference_access_policy_services ps WHERE ps.policy_id=p.id AND ps.tenant_id=$1 AND ps.inference_service_id=$2)", 1)
	rows, err := tx.Query(ctx, query, tenantID, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.AccessPolicy, 0)
	for rows.Next() {
		item, scanErr := scanAccessPolicy(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (p *Postgres) ReplaceServiceAccessPolicies(ctx context.Context, tenantID, serviceID uuid.UUID, policyIDs []uuid.UUID, idempotencyKey uuid.UUID) ([]domain.AccessPolicy, error) {
	if tenantID == uuid.Nil || serviceID == uuid.Nil || idempotencyKey == uuid.Nil {
		return nil, errors.New("tenant, service, and idempotency ids are required")
	}
	normalizedPolicyIDs := append([]uuid.UUID(nil), policyIDs...)
	sort.Slice(normalizedPolicyIDs, func(i, j int) bool { return normalizedPolicyIDs[i].String() < normalizedPolicyIDs[j].String() })
	uniquePolicyIDs := normalizedPolicyIDs[:0]
	for _, policyID := range normalizedPolicyIDs {
		if policyID == uuid.Nil {
			return nil, errors.New("policy id is required")
		}
		if len(uniquePolicyIDs) == 0 || uniquePolicyIDs[len(uniquePolicyIDs)-1] != policyID {
			uniquePolicyIDs = append(uniquePolicyIDs, policyID)
		}
	}
	policyIDs = uniquePolicyIDs
	operationScope := "inference_access_policy.binding/" + serviceID.String()
	requestHash, err := hashAccessPolicyMutation(operationScope, struct {
		ServiceID uuid.UUID   `json:"service_id"`
		PolicyIDs []uuid.UUID `json:"policy_ids"`
	}{ServiceID: serviceID, PolicyIDs: policyIDs})
	if err != nil {
		return nil, err
	}
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return nil, err
	}
	snapshot, replayed, err := beginAccessPolicyMutation(ctx, tx, tenantID, operationScope, idempotencyKey, requestHash)
	if err != nil {
		return nil, err
	}
	if replayed {
		result, decodeErr := decodeAccessPolicyMutation[[]domain.AccessPolicy](snapshot)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return result, nil
	}
	if _, err = tx.Exec(ctx, `DELETE FROM inference_access_policy_services WHERE tenant_id=$1 AND inference_service_id=$2`, tenantID, serviceID); err != nil {
		return nil, err
	}
	for _, policyID := range policyIDs {
		tag, execErr := tx.Exec(ctx, `INSERT INTO inference_access_policy_services(policy_id,tenant_id,inference_service_id) SELECT $1,$2,$3 WHERE EXISTS (SELECT 1 FROM inference_access_policies WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL)`, policyID, tenantID, serviceID)
		if execErr != nil {
			return nil, execErr
		}
		if tag.RowsAffected() != 1 {
			return nil, ErrNotFound
		}
	}
	items, err := listServiceAccessPoliciesTx(ctx, tx, tenantID, serviceID)
	if err != nil {
		return nil, err
	}
	if err := completeAccessPolicyMutation(ctx, tx, tenantID, operationScope, idempotencyKey, requestHash, items); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func (p *Postgres) RecordAccessPolicyEvent(ctx context.Context, event domain.AccessPolicyEvent) error {
	if event.TenantID == uuid.Nil {
		return errors.New("tenant id is required")
	}
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, event.TenantID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO inference_access_policy_events(id,tenant_id,policy_id,inference_service_id,api_key_id,key_prefix,request_id,openai_path,external_model,decision,reason_code,http_status,retry_after_seconds,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, event.ID, event.TenantID, event.PolicyID, event.InferenceServiceID, event.APIKeyID, event.KeyPrefix, event.RequestID, event.OpenAIPath, event.ExternalModel, event.Decision, event.ReasonCode, event.HTTPStatus, event.RetryAfterSeconds, event.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ListAccessPolicyEvents(ctx context.Context, tenantID uuid.UUID, query domain.AccessPolicyEventQuery) ([]domain.AccessPolicyEvent, string, error) {
	if tenantID == uuid.Nil {
		return nil, "", errors.New("tenant id is required")
	}
	limit := query.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	tx, err := p.tenantPool.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(ctx)
	if err := setTenant(ctx, tx, tenantID); err != nil {
		return nil, "", err
	}
	var serviceFilter, policyFilter, keyFilter any
	if query.InferenceServiceID != nil {
		serviceFilter = *query.InferenceServiceID
	}
	if query.PolicyID != nil {
		policyFilter = *query.PolicyID
	}
	if query.APIKeyID != nil {
		keyFilter = *query.APIKeyID
	}
	rows, err := tx.Query(ctx, `SELECT id,tenant_id,policy_id,inference_service_id,api_key_id,COALESCE(key_prefix,''),COALESCE(request_id,''),COALESCE(openai_path,''),COALESCE(external_model,''),decision,reason_code,http_status,COALESCE(retry_after_seconds,0),created_at FROM inference_access_policy_events WHERE tenant_id=$1 AND ($2::uuid IS NULL OR inference_service_id=$2) AND ($3::uuid IS NULL OR policy_id=$3) AND ($4::uuid IS NULL OR api_key_id=$4) AND ($5='' OR decision=$5) ORDER BY created_at DESC,id DESC LIMIT $6`, tenantID, serviceFilter, policyFilter, keyFilter, query.Decision, limit)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]domain.AccessPolicyEvent, 0)
	for rows.Next() {
		var item domain.AccessPolicyEvent
		if err := rows.Scan(&item.ID, &item.TenantID, &item.PolicyID, &item.InferenceServiceID, &item.APIKeyID, &item.KeyPrefix, &item.RequestID, &item.OpenAIPath, &item.ExternalModel, &item.Decision, &item.ReasonCode, &item.HTTPStatus, &item.RetryAfterSeconds, &item.CreatedAt); err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", err
	}
	return items, "", nil
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
		&service.CreatedAt, &service.UpdatedAt, &service.DeletedAt, &service.LegacyQuarantined,
		&service.Publication.Desired, &service.Publication.Generation,
		&service.Publication.ObservedGeneration, &service.Publication.Phase,
		&service.Publication.LastError, &service.Publication.UpdatedAt)
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
		&service.DeletedAt, &service.LegacyQuarantined,
		&service.Publication.Desired, &service.Publication.Generation,
		&service.Publication.ObservedGeneration, &service.Publication.Phase,
		&service.Publication.LastError, &service.Publication.UpdatedAt)
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
