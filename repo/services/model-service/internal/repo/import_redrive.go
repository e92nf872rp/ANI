package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	natsmsg "github.com/kubercloud/ani/pkg/nats"
)

const defaultImportRedriveLimit = 50

// StalledImport is a model import whose driving outbox event was published but
// never reached the import worker: the async task still has attempt_count=0 and
// no lease, so nothing has claimed it. The ANI_TASKS stream expires undelivered
// messages silently, which leaves no database trace to react to.
type StalledImport struct {
	TenantID uuid.UUID
	ImportID uuid.UUID
	TaskID   uuid.UUID
	Payload  []byte
}

// candidateImportTenantsSQL lists tenants owning a never-claimed import task.
// async_tasks carries only permissive RLS policies, so this platform-scope read
// is valid without a tenant context. model_import_tasks additionally carries a
// RESTRICTIVE tenant policy, so candidates of a tenant are selected inside that
// tenant's transaction (see selectStalledImportSQL).
const candidateImportTenantsSQL = `
	SELECT DISTINCT tenant_id
	FROM async_tasks
	WHERE task_type = 'model.import'
	  AND status = 'pending'
	  AND attempt_count = 0
	  AND lease_owner IS NULL
	  AND created_at < NOW() - make_interval(secs => $1::double precision)
	ORDER BY tenant_id
	LIMIT $2
`

// selectStalledImportSQL returns the never-claimed imports of one tenant and
// locks them for the duration of the sweep transaction, so concurrent
// model-service replicas cannot queue the same redrive twice. The correlated
// LATERAL reuses the original published payload (same idempotency key) instead
// of rebuilding the message. A pending redrive already in the outbox is skipped
// to avoid piling up duplicate events while the relay is behind.
const selectStalledImportSQL = `
	SELECT it.tenant_id, it.id, it.async_task_id, original.payload
	FROM model_import_tasks it
	JOIN async_tasks task ON task.id = it.async_task_id
	JOIN LATERAL (
		SELECT event.payload
		FROM outbox_events event
		WHERE event.payload->>'task_id' = it.async_task_id::text
		ORDER BY event.created_at ASC, event.id ASC
		LIMIT 1
	) original ON TRUE
	WHERE it.status = 'pending'
	  AND it.created_at < NOW() - make_interval(secs => $1::double precision)
	  AND task.status = 'pending'
	  AND task.attempt_count = 0
	  AND task.lease_owner IS NULL
	  AND NOT EXISTS (
		SELECT 1
		FROM outbox_events pending
		WHERE pending.payload->>'task_id' = it.async_task_id::text
		  AND NOT pending.published
	  )
	ORDER BY it.created_at ASC, it.id ASC
	LIMIT $2
	FOR UPDATE OF it SKIP LOCKED
`

// insertImportRedriveOutboxSQL queues the original import message again. The
// publish itself stays with the existing outbox relay, so the redrive observes
// the same transactional-outbox discipline as the first delivery.
const insertImportRedriveOutboxSQL = `
	INSERT INTO outbox_events (
		aggregate_type, aggregate_id, event_type, tenant_id, payload
	)
	VALUES ($1, $2, $3, $4, $5)
`

// CandidateImportTenants returns the tenants that own an import task which has
// never been claimed and is older than olderThan. It runs in platform scope and
// must not be called with a tenant already set on tx.
func CandidateImportTenants(ctx context.Context, tx pgx.Tx, olderThan time.Duration, limit int) ([]uuid.UUID, error) {
	if tx == nil {
		return nil, fmt.Errorf("modelRepo.CandidateImportTenants transaction required")
	}
	rows, err := tx.Query(ctx, candidateImportTenantsSQL, olderThan.Seconds(), normalizeImportRedriveLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("modelRepo.CandidateImportTenants query: %w", err)
	}
	defer rows.Close()
	var tenants []uuid.UUID
	for rows.Next() {
		var tenantID uuid.UUID
		if err := rows.Scan(&tenantID); err != nil {
			return nil, fmt.Errorf("modelRepo.CandidateImportTenants scan: %w", err)
		}
		tenants = append(tenants, tenantID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("modelRepo.CandidateImportTenants rows: %w", err)
	}
	return tenants, nil
}

// SelectStalledImports returns the redrive candidates of a single tenant. The
// caller must have set app.current_tenant_id on tx before calling it.
func SelectStalledImports(ctx context.Context, tx pgx.Tx, olderThan time.Duration, limit int) ([]StalledImport, error) {
	if tx == nil {
		return nil, fmt.Errorf("modelRepo.SelectStalledImports transaction required")
	}
	rows, err := tx.Query(ctx, selectStalledImportSQL, olderThan.Seconds(), normalizeImportRedriveLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("modelRepo.SelectStalledImports query: %w", err)
	}
	defer rows.Close()
	var candidates []StalledImport
	for rows.Next() {
		var candidate StalledImport
		if err := rows.Scan(&candidate.TenantID, &candidate.ImportID, &candidate.TaskID, &candidate.Payload); err != nil {
			return nil, fmt.Errorf("modelRepo.SelectStalledImports scan: %w", err)
		}
		if len(candidate.Payload) == 0 {
			continue
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("modelRepo.SelectStalledImports rows: %w", err)
	}
	return candidates, nil
}

// InsertImportRedriveOutbox writes the redrive event for one candidate. It uses
// the same aggregate identity and event type as the original import event so
// the existing relay publishes it to ani.tasks.model.import unchanged.
func InsertImportRedriveOutbox(ctx context.Context, tx pgx.Tx, candidate StalledImport) error {
	if tx == nil {
		return fmt.Errorf("modelRepo.InsertImportRedriveOutbox transaction required")
	}
	if _, err := tx.Exec(ctx, insertImportRedriveOutboxSQL,
		"model_import", candidate.TaskID, natsmsg.SubjectModelImport, candidate.TenantID, candidate.Payload); err != nil {
		return fmt.Errorf("modelRepo.InsertImportRedriveOutbox insert outbox: %w", err)
	}
	return nil
}

func normalizeImportRedriveLimit(limit int) int {
	if limit <= 0 {
		return defaultImportRedriveLimit
	}
	return limit
}
