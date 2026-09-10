package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	natsmsg "github.com/kubercloud/ani/pkg/nats"
	"github.com/kubercloud/ani/pkg/types"
)

// CreateImportRequest is the transaction-bound input for a remote model
// import. It intentionally contains no credentials or signed URLs.
type CreateImportRequest struct {
	TenantID       uuid.UUID
	Source         string
	RepoID         string
	Revision       string
	IdempotencyKey string
	WebhookURL     string
	RequestHash    string
}

// ImportTask is the immutable hand-off between model-service and the import
// worker. TargetStoragePath is allocated before the outbox event is written,
// so retries always address the same object identity.
type ImportTask struct {
	ID                uuid.UUID
	TenantID          uuid.UUID
	ModelID           uuid.UUID
	TaskID            uuid.UUID
	Source            string
	RepoID            string
	Revision          string
	ResolvedRevision  string
	Status            string
	TargetStoragePath string
}

const (
	modelImportTaskType       = "model.import"
	modelImportTargetBucket   = "model"
	modelImportTargetDir      = "archive"
	modelImportArchiveName    = "model.tar.gz"
	modelImportMaxAttempts    = 3
	modelImportOperationScope = "model.import"
)

// SQL fragments are kept in this file so the complete model/task/outbox
// transaction is easy to audit. Every statement carries tenant_id directly;
// RLS is an additional boundary, not a replacement for explicit predicates.
const claimModelImportSQL = `
	SELECT id, tenant_id, model_id, async_task_id, source, source_repo_id,
		revision, COALESCE(resolved_revision, ''), status, request_hash, target_storage_path
	FROM model_import_tasks
	WHERE tenant_id=$1 AND idempotency_key=$2
	FOR UPDATE
`

const createImportedModelSQL = `
	INSERT INTO models (id, tenant_id, name, display_name, source, source_repo_id, capabilities)
	VALUES ($1, $2, $3, $4, $5, $6, $7)
	ON CONFLICT (tenant_id, name) DO NOTHING
	RETURNING id
`

const createModelImportTaskSQL = `
	INSERT INTO model_import_tasks (
		id, tenant_id, model_id, async_task_id, source, source_repo_id, revision,
		idempotency_key, request_hash, target_storage_path, status
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'pending')
	RETURNING id
`

const createModelImportAsyncTaskSQL = `
	INSERT INTO async_tasks (
		id, tenant_id, idempotency_key, task_type, resource_type, resource_id,
		max_attempts, webhook_url, payload
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9)
	RETURNING id
`

const createModelImportOutboxSQL = `
	INSERT INTO outbox_events (
		aggregate_type, aggregate_id, event_type, tenant_id, payload
	)
	VALUES ($1, $2, $3, $4, $5)
`

func (r *PostgresModelRepo) CreateImport(ctx context.Context, tx pgx.Tx, req CreateImportRequest) (*ImportTask, bool, error) {
	req.Source = strings.ToLower(strings.TrimSpace(req.Source))
	req.RepoID = strings.TrimSpace(req.RepoID)
	req.Revision = strings.TrimSpace(req.Revision)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	req.WebhookURL = strings.TrimSpace(req.WebhookURL)
	req.RequestHash = strings.TrimSpace(req.RequestHash)
	if err := validateCreateImportRequest(req); err != nil {
		return nil, false, err
	}
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return nil, false, fmt.Errorf("modelRepo.CreateImport set tenant: %w", err)
	}

	// A missing idempotency row cannot be locked with SELECT FOR UPDATE. The
	// advisory lock serializes first creates as well as replays, while the
	// unique index in the migration remains the durable fence.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, modelMutationLockKey(req.TenantID, modelImportOperationScope, req.IdempotencyKey)); err != nil {
		return nil, false, fmt.Errorf("modelRepo.CreateImport idempotency lock: %w", err)
	}

	existing := &ImportTask{}
	var existingHash string
	err := tx.QueryRow(ctx, claimModelImportSQL, req.TenantID, req.IdempotencyKey).Scan(
		&existing.ID, &existing.TenantID, &existing.ModelID, &existing.TaskID,
		&existing.Source, &existing.RepoID, &existing.Revision,
		&existing.ResolvedRevision, &existing.Status, &existingHash, &existing.TargetStoragePath,
	)
	if err == nil {
		if existingHash != req.RequestHash {
			return nil, false, fmt.Errorf("%w: idempotency key reused with a different request", types.ErrConflict)
		}
		if existing.TaskID == uuid.Nil || existing.ModelID == uuid.Nil || existing.ID == uuid.Nil {
			return nil, false, errors.New("modelRepo.CreateImport found incomplete import task")
		}
		return existing, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("modelRepo.CreateImport idempotency lookup: %w", err)
	}

	modelID := uuid.New()
	modelName := importedModelName(req.RepoID, req.Source, "")
	inserted, err := insertImportedModel(ctx, tx, req, modelID, modelName)
	if err != nil {
		return nil, false, err
	}
	if !inserted {
		modelName = importedModelName(req.RepoID, req.Source, req.RequestHash)
		inserted, err = insertImportedModel(ctx, tx, req, modelID, modelName)
		if err != nil {
			return nil, false, err
		}
		if !inserted {
			return nil, false, fmt.Errorf("%w: imported model name is already reserved", types.ErrConflict)
		}
	}

	importID := uuid.New()
	taskID := uuid.New()
	targetStoragePath := modelImportTargetPath(req.TenantID, modelID, importID)
	targetPrefix := modelID.String() + "/import-" + importID.String() + "/" + modelImportTargetDir
	payload, err := json.Marshal(natsmsg.ModelImportMsg{
		TaskID:         taskID,
		IdempotencyKey: req.IdempotencyKey,
		TenantID:       req.TenantID,
		ModelID:        modelID,
		Source:         req.Source,
		RepoID:         req.RepoID,
		Revision:       req.Revision,
		TargetBucket:   modelImportTargetBucket,
		TargetPrefix:   targetPrefix,
		WebhookURL:     req.WebhookURL,
	})
	if err != nil {
		return nil, false, fmt.Errorf("modelRepo.CreateImport marshal payload: %w", err)
	}

	if err := tx.QueryRow(ctx, createModelImportAsyncTaskSQL, taskID, req.TenantID, req.IdempotencyKey, modelImportTaskType, "model", modelID, modelImportMaxAttempts, req.WebhookURL, payload).Scan(&taskID); err != nil {
		return nil, false, fmt.Errorf("modelRepo.CreateImport insert async task: %w", err)
	}
	// async_task_id is an FK, so the task row must exist before the import
	// descriptor references it. Both inserts remain in this transaction and
	// roll back together if either step fails.
	if err := tx.QueryRow(ctx, createModelImportTaskSQL, importID, req.TenantID, modelID, taskID, req.Source, req.RepoID, req.Revision, req.IdempotencyKey, req.RequestHash, targetStoragePath).Scan(&importID); err != nil {
		return nil, false, fmt.Errorf("modelRepo.CreateImport insert import task: %w", err)
	}
	if _, err := tx.Exec(ctx, createModelImportOutboxSQL, "model_import", taskID, natsmsg.SubjectModelImport, req.TenantID, payload); err != nil {
		return nil, false, fmt.Errorf("modelRepo.CreateImport insert outbox: %w", err)
	}

	return &ImportTask{
		ID: importID, TenantID: req.TenantID, ModelID: modelID, TaskID: taskID,
		Source: req.Source, RepoID: req.RepoID, Revision: req.Revision,
		Status: "pending", TargetStoragePath: targetStoragePath,
	}, false, nil
}

func insertImportedModel(ctx context.Context, tx pgx.Tx, req CreateImportRequest, modelID uuid.UUID, name string) (bool, error) {
	displayName := req.Source + "/" + req.RepoID
	if err := tx.QueryRow(ctx, createImportedModelSQL, modelID, req.TenantID, name, displayName, req.Source, req.RepoID, importedModelCapabilities(req.RepoID)).Scan(&modelID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("modelRepo.CreateImport insert model: %w", err)
	}
	return true, nil
}

// importedModelCapabilities uses only information present in the public
// import request. An empty capability list is interpreted by inference-service
// as generation for backward compatibility, so remote imports must persist an
// explicit task instead of relying on that implicit default. Repository names
// are the only classifier available before the asynchronous source download;
// ambiguous names are conservatively treated as text-generation, while the
// well-known embedding naming families select the embedding profile.
func importedModelCapabilities(repoID string) []string {
	normalized := strings.ToLower(strings.TrimSpace(repoID))
	embeddingSignals := []string{
		"embedding", "embed", "sentence-transformers", "sentence_transformers",
		"text2vec", "bge-", "bge_", "gte-", "gte_", "e5-", "e5_",
		"instructor-", "instructor_", "jina-embeddings", "m3e",
	}
	for _, signal := range embeddingSignals {
		if strings.Contains(normalized, signal) {
			return []string{"embedding"}
		}
	}
	return []string{"text-generation"}
}

func validateCreateImportRequest(req CreateImportRequest) error {
	if req.TenantID == uuid.Nil {
		return types.Wrapf(types.ErrBadRequest, "modelRepo.CreateImport tenant_id required")
	}
	if req.Source != "huggingface" && req.Source != "modelscope" {
		return types.Wrapf(types.ErrBadRequest, "modelRepo.CreateImport source must be huggingface or modelscope")
	}
	if strings.TrimSpace(req.RepoID) == "" {
		return types.Wrapf(types.ErrBadRequest, "modelRepo.CreateImport repo_id required")
	}
	if strings.TrimSpace(req.Revision) == "" {
		return types.Wrapf(types.ErrBadRequest, "modelRepo.CreateImport revision required")
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return types.Wrapf(types.ErrBadRequest, "modelRepo.CreateImport idempotency_key required")
	}
	if strings.TrimSpace(req.RequestHash) == "" {
		return types.Wrapf(types.ErrBadRequest, "modelRepo.CreateImport request_hash required")
	}
	return nil
}

func modelImportTargetPath(tenantID, modelID, importID uuid.UUID) string {
	return "object://models/" + tenantID.String() + "/" + modelID.String() + "/import-" + importID.String() + "/" + modelImportTargetDir + "/" + modelImportArchiveName
}

func importedModelName(repoID, source, requestHash string) string {
	base := strings.TrimSpace(repoID)
	if slash := strings.LastIndexAny(base, "/\\"); slash >= 0 {
		base = base[slash+1:]
	}
	base = strings.ToLower(base)
	var builder strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	base = strings.Trim(builder.String(), ".-")
	if base == "" {
		base = "imported-model"
	}
	if (base[0] < 'a' || base[0] > 'z') && (base[0] < '0' || base[0] > '9') {
		base = "model-" + base
	}
	if source == "" {
		source = "import"
	}
	_ = source // source is part of the request hash; names stay repo-centric.
	if requestHash != "" {
		suffix := "-" + strings.ToLower(strings.TrimSpace(requestHash))
		if len(suffix) > 9 {
			suffix = suffix[:9]
		}
		maxBase := 63 - len(suffix)
		if len(base) > maxBase {
			base = strings.TrimRight(base[:maxBase], ".-")
		}
		base += suffix
	}
	if len(base) > 63 {
		base = strings.TrimRight(base[:63], ".-")
	}
	return base
}
