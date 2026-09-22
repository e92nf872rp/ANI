package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kubercloud/ani/pkg/types"
)

type ImportFileSpec struct {
	Path      string
	ObjectKey string
	SizeBytes int64
}

type ImportFilePart struct {
	Number int    `json:"number"`
	ETag   string `json:"etag"`
	Size   int64  `json:"size"`
}

type ImportFile struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	ImportTaskID   uuid.UUID
	Path           string
	ObjectKey      string
	SizeBytes      int64
	UploadedBytes  int64
	SHA256         string
	Status         string
	UploadID       string
	CompletedParts []ImportFilePart
	AttemptCount   int
}

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
	if _, err := tx.Exec(ctx, `
		UPDATE models
		SET status='error', error_message=$3, updated_at=NOW()
		WHERE tenant_id=$1 AND id=(SELECT model_id FROM model_import_tasks WHERE tenant_id=$1 AND id=$2)
	`, tenantID, importID, message); err != nil {
		return fmt.Errorf("modelRepo.FailImportTask model status: %w", err)
	}
	return nil
}

func (r *PostgresModelRepo) EnsureImportFiles(ctx context.Context, pool *pgxpool.Pool, tenantID, importID, taskID uuid.UUID, workerID string, files []ImportFileSpec) error {
	if tenantID == uuid.Nil || importID == uuid.Nil || taskID == uuid.Nil || strings.TrimSpace(workerID) == "" {
		return types.Wrapf(types.ErrBadRequest, "modelRepo.EnsureImportFiles identity required")
	}
	tx, err := beginTenantTx(ctx, pool)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return err
	}
	for _, file := range files {
		if strings.TrimSpace(file.Path) == "" || strings.TrimSpace(file.ObjectKey) == "" || file.SizeBytes < 0 {
			return types.Wrapf(types.ErrBadRequest, "modelRepo.EnsureImportFiles invalid file")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO model_import_files (tenant_id, import_task_id, file_path, object_key, size_bytes)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (tenant_id, import_task_id, file_path) DO UPDATE
			SET object_key=EXCLUDED.object_key, size_bytes=EXCLUDED.size_bytes, updated_at=NOW()
			WHERE model_import_files.status <> 'completed'
		`, tenantID, importID, file.Path, file.ObjectKey, file.SizeBytes); err != nil {
			return fmt.Errorf("modelRepo.EnsureImportFiles insert: %w", err)
		}
	}
	tag, err := tx.Exec(ctx, `
		UPDATE model_import_tasks
		SET total_bytes=$4, status='running', progress_pct=5
		WHERE tenant_id=$1 AND id=$2 AND async_task_id=$3
		  AND EXISTS (SELECT 1 FROM async_tasks WHERE id=$3 AND tenant_id=$1 AND status='running' AND lease_owner=$5 AND lease_until > NOW())
	`, tenantID, importID, taskID, sumImportFileSizes(files), workerID)
	if err != nil {
		return fmt.Errorf("modelRepo.EnsureImportFiles task: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return types.Wrapf(types.ErrLeaseTaken, "modelRepo.EnsureImportFiles import_id=%s", importID)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE models SET status='downloading', error_message=NULL, updated_at=NOW()
		WHERE tenant_id=$1 AND id=(SELECT model_id FROM model_import_tasks WHERE tenant_id=$1 AND id=$2)
	`, tenantID, importID); err != nil {
		return fmt.Errorf("modelRepo.EnsureImportFiles model: %w", err)
	}
	return tx.Commit(ctx)
}

func sumImportFileSizes(files []ImportFileSpec) int64 {
	var total int64
	for _, file := range files {
		if file.SizeBytes > 0 && total > (1<<63-1)-file.SizeBytes {
			return 1<<63 - 1
		}
		total += file.SizeBytes
	}
	return total
}

func (r *PostgresModelRepo) ListImportFiles(ctx context.Context, pool *pgxpool.Pool, tenantID, importID uuid.UUID) ([]ImportFile, error) {
	tx, err := beginTenantTx(ctx, pool)
	if err != nil {
		return nil, err
	}
	defer rollback(ctx, tx)
	rows, err := tx.Query(ctx, `
		SELECT id, tenant_id, import_task_id, file_path, object_key, size_bytes,
		       uploaded_bytes, COALESCE(sha256,''), status, COALESCE(upload_id,''),
		       completed_parts, attempt_count
		FROM model_import_files
		WHERE tenant_id=$1 AND import_task_id=$2
		ORDER BY file_path
	`, tenantID, importID)
	if err != nil {
		return nil, fmt.Errorf("modelRepo.ListImportFiles query: %w", err)
	}
	defer rows.Close()
	var files []ImportFile
	for rows.Next() {
		var file ImportFile
		var parts []byte
		if err := rows.Scan(&file.ID, &file.TenantID, &file.ImportTaskID, &file.Path, &file.ObjectKey, &file.SizeBytes, &file.UploadedBytes, &file.SHA256, &file.Status, &file.UploadID, &parts, &file.AttemptCount); err != nil {
			return nil, fmt.Errorf("modelRepo.ListImportFiles scan: %w", err)
		}
		if len(parts) != 0 && string(parts) != "null" {
			if err := json.Unmarshal(parts, &file.CompletedParts); err != nil {
				return nil, fmt.Errorf("modelRepo.ListImportFiles parts: %w", err)
			}
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("modelRepo.ListImportFiles rows: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return files, nil
}

func (r *PostgresModelRepo) SaveImportFileCheckpoint(ctx context.Context, pool *pgxpool.Pool, tenantID, importID, taskID uuid.UUID, workerID, filePath, uploadID string, uploadedBytes int64, parts []ImportFilePart) error {
	payload, err := json.Marshal(parts)
	if err != nil {
		return err
	}
	tx, err := beginTenantTx(ctx, pool)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE model_import_files f
		SET status='uploading', upload_id=$6, uploaded_bytes=$7, completed_parts=$8,
		    attempt_count=f.attempt_count+1, started_at=COALESCE(f.started_at,NOW()), updated_at=NOW()
		WHERE f.tenant_id=$1 AND f.import_task_id=$2 AND f.file_path=$5
		  AND EXISTS (SELECT 1 FROM model_import_tasks i JOIN async_tasks a ON a.id=i.async_task_id
		             WHERE i.tenant_id=$1 AND i.id=$2 AND i.async_task_id=$3 AND a.status='running' AND a.lease_owner=$4 AND a.lease_until > NOW())
	`, tenantID, importID, taskID, workerID, filePath, uploadID, uploadedBytes, payload)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return types.Wrapf(types.ErrLeaseTaken, "modelRepo.SaveImportFileCheckpoint file=%s", filePath)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE model_import_tasks i
		SET downloaded_bytes=COALESCE((SELECT SUM(uploaded_bytes) FROM model_import_files f WHERE f.tenant_id=i.tenant_id AND f.import_task_id=i.id),0),
		    progress_pct=CASE WHEN i.total_bytes > 0 THEN LEAST(90, 5 + ((LEAST(COALESCE((SELECT SUM(uploaded_bytes) FROM model_import_files f WHERE f.tenant_id=i.tenant_id AND f.import_task_id=i.id),0), i.total_bytes)::numeric * 85 / i.total_bytes)::int)) ELSE 5 END
		WHERE i.tenant_id=$1 AND i.id=$2 AND i.async_task_id=$3
	`, tenantID, importID, taskID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresModelRepo) CompleteImportFile(ctx context.Context, pool *pgxpool.Pool, tenantID, importID, taskID uuid.UUID, workerID, filePath, checksum string, size int64) error {
	tx, err := beginTenantTx(ctx, pool)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE model_import_files f
		SET status='completed', sha256=$6, uploaded_bytes=$7, completed_at=NOW(), updated_at=NOW(), error_message=NULL
		WHERE f.tenant_id=$1 AND f.import_task_id=$2 AND f.file_path=$5 AND f.size_bytes=$7
		  AND EXISTS (SELECT 1 FROM model_import_tasks i JOIN async_tasks a ON a.id=i.async_task_id
		             WHERE i.tenant_id=$1 AND i.id=$2 AND i.async_task_id=$3 AND a.status='running' AND a.lease_owner=$4 AND a.lease_until > NOW())
	`, tenantID, importID, taskID, workerID, filePath, checksum, size)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return types.Wrapf(types.ErrLeaseTaken, "modelRepo.CompleteImportFile file=%s", filePath)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE model_import_tasks i
		SET downloaded_bytes=COALESCE((SELECT SUM(uploaded_bytes) FROM model_import_files f WHERE f.tenant_id=i.tenant_id AND f.import_task_id=i.id),0),
		    progress_pct=CASE WHEN i.total_bytes > 0 THEN LEAST(90, 5 + ((LEAST(COALESCE((SELECT SUM(uploaded_bytes) FROM model_import_files f WHERE f.tenant_id=i.tenant_id AND f.import_task_id=i.id),0), i.total_bytes)::numeric * 85 / i.total_bytes)::int)) ELSE 5 END
		WHERE i.tenant_id=$1 AND i.id=$2 AND i.async_task_id=$3
	`, tenantID, importID, taskID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
