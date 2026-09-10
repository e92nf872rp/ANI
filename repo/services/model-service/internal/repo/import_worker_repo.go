package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kubercloud/ani/pkg/types"
)

// GetImportByTask returns the import descriptor associated with an async task.
// The tenant predicate is deliberately repeated alongside RLS: a malformed or
// cross-tenant message must never turn into a metadata side channel.
func (r *PostgresModelRepo) GetImportByTask(ctx context.Context, pool *pgxpool.Pool, tenantID, taskID uuid.UUID) (*ImportTask, error) {
	tx, err := beginTenantTx(ctx, pool)
	if err != nil {
		return nil, err
	}
	defer rollback(ctx, tx)

	const query = `
		SELECT id, tenant_id, COALESCE(model_id, '00000000-0000-0000-0000-000000000000'::uuid),
			COALESCE(async_task_id, '00000000-0000-0000-0000-000000000000'::uuid),
			source, source_repo_id, COALESCE(revision, 'main'), COALESCE(resolved_revision, ''), status,
			COALESCE(target_storage_path, '')
		FROM model_import_tasks
		WHERE tenant_id=$1 AND async_task_id=$2
		LIMIT 1
	`
	importTask := &ImportTask{}
	err = tx.QueryRow(ctx, query, tenantID, taskID).Scan(
		&importTask.ID, &importTask.TenantID, &importTask.ModelID, &importTask.TaskID,
		&importTask.Source, &importTask.RepoID, &importTask.Revision,
		&importTask.ResolvedRevision, &importTask.Status, &importTask.TargetStoragePath,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, types.Wrapf(types.ErrNotFound, "modelRepo.GetImportByTask task_id=%s", taskID)
	}
	if err != nil {
		return nil, fmt.Errorf("modelRepo.GetImportByTask query: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("modelRepo.GetImportByTask commit: %w", err)
	}
	return importTask, nil
}

// setResolvedImportRevisionSQL updates a snapshot only while the task lease
// owned by this worker is active. The duplicate tenant predicates are
// intentional: they keep a malformed cross-tenant message from bypassing the
// RLS context and ensure a stale lease cannot mutate the import row.
const setResolvedImportRevisionSQL = `
	UPDATE model_import_tasks
	SET resolved_revision=$6
	WHERE id=$1 AND tenant_id=$2 AND async_task_id=$3 AND revision=$5
	  AND (resolved_revision IS NULL OR resolved_revision=$6)
	  AND EXISTS (
		SELECT 1
		FROM async_tasks
		WHERE async_tasks.id=$3
		  AND async_tasks.tenant_id=$2
		  AND async_tasks.status='running'
		  AND async_tasks.lease_owner=$4
		  AND async_tasks.lease_until > NOW()
	  )
`

// SetResolvedImportRevision durably binds a mutable source revision to one
// immutable snapshot. Replays with the same value are idempotent; a different
// value is rejected rather than replacing the snapshot used by a prior try.
// The worker ID is part of the write fence: only the currently running task
// lease holder can persist the resolved revision.
func (r *PostgresModelRepo) SetResolvedImportRevision(ctx context.Context, pool *pgxpool.Pool, tenantID, importID, taskID uuid.UUID, workerID, expectedRevision, resolvedRevision string) error {
	workerID = strings.TrimSpace(workerID)
	expectedRevision = strings.TrimSpace(expectedRevision)
	resolvedRevision = strings.TrimSpace(resolvedRevision)
	if workerID == "" {
		return types.Wrapf(types.ErrBadRequest, "modelRepo.SetResolvedImportRevision worker_id required")
	}
	if expectedRevision == "" || resolvedRevision == "" {
		return types.Wrapf(types.ErrBadRequest, "modelRepo.SetResolvedImportRevision revision required")
	}
	tx, err := beginTenantTx(ctx, pool)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return fmt.Errorf("modelRepo.SetResolvedImportRevision set tenant: %w", err)
	}
	tag, err := tx.Exec(ctx, setResolvedImportRevisionSQL, importID, tenantID, taskID, workerID, expectedRevision, resolvedRevision)
	if err != nil {
		return fmt.Errorf("modelRepo.SetResolvedImportRevision update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return types.Wrapf(types.ErrConflict, "modelRepo.SetResolvedImportRevision snapshot conflict")
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("modelRepo.SetResolvedImportRevision commit: %w", err)
	}
	return nil
}

// CompleteImportTask marks the model-specific descriptor complete. The
// version ID is accepted as an explicit fence so callers cannot accidentally
// mark an unrelated import row complete.
func (r *PostgresModelRepo) CompleteImportTask(ctx context.Context, tx pgx.Tx, tenantID, importID, taskID, versionID uuid.UUID) error {
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return fmt.Errorf("modelRepo.CompleteImportTask set tenant: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE model_import_tasks
		SET status='completed', progress_pct=100, completed_at=NOW(), error_message=NULL
		WHERE id=$1 AND tenant_id=$2 AND async_task_id=$3 AND model_id IS NOT NULL
		  AND EXISTS (
			SELECT 1 FROM model_versions AS version
			WHERE version.id=$4 AND version.model_id=model_import_tasks.model_id
		  )
	`, importID, tenantID, taskID, versionID)
	if err != nil {
		return fmt.Errorf("modelRepo.CompleteImportTask update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return types.Wrapf(types.ErrNotFound, "modelRepo.CompleteImportTask import_id=%s version_id=%s", importID, versionID)
	}
	return nil
}

// FailImportTask records only a stable, operator-safe error message. Source
// URLs, credentials and provider internals are intentionally never persisted.
func (r *PostgresModelRepo) FailImportTask(ctx context.Context, tx pgx.Tx, tenantID, importID, taskID uuid.UUID, message string) error {
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return fmt.Errorf("modelRepo.FailImportTask set tenant: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE model_import_tasks
		SET status='failed', error_message=$4, completed_at=NOW()
		WHERE id=$1 AND tenant_id=$2 AND async_task_id=$3
	`, importID, tenantID, taskID, message)
	if err != nil {
		return fmt.Errorf("modelRepo.FailImportTask update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return types.Wrapf(types.ErrNotFound, "modelRepo.FailImportTask import_id=%s", importID)
	}
	return nil
}
