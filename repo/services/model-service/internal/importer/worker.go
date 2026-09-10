package importer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	natsmsg "github.com/kubercloud/ani/pkg/nats"
	"github.com/kubercloud/ani/pkg/ports"
	taskrepo "github.com/kubercloud/ani/pkg/repo"
	"github.com/kubercloud/ani/pkg/types"
	modelrepo "github.com/kubercloud/ani/services/model-service/internal/repo"
)

// ImportMessage is an alias so the worker and all publishers share the
// canonical NATS payload. It is intentionally not re-declared in this package.
type ImportMessage = natsmsg.ModelImportMsg

const (
	defaultImportLeaseDuration = 30 * time.Minute
	archiveContentType         = "application/gzip"
)

var (
	errWorkerUnavailable      = errors.New("model import worker unavailable")
	errDatabaseUnavailable    = errors.New("model import database unavailable")
	errObjectStoreUnavailable = errors.New("model import object store unavailable")
	errSourceUnavailable      = errors.New("model import source unavailable")
)

// ImportStore is the atomic control-plane boundary used by Worker. Complete
// and Fail must update the import descriptor and async task in one transaction.
// The test-facing interface also makes that atomicity explicit without exposing
// database handles to the importer package.
type ImportStore interface {
	GetTask(context.Context, uuid.UUID, uuid.UUID) (*taskrepo.AsyncTask, error)
	GetImport(context.Context, uuid.UUID, uuid.UUID) (*modelrepo.ImportTask, error)
	AcquireLease(context.Context, uuid.UUID, uuid.UUID, string, time.Duration) (bool, error)
	Heartbeat(context.Context, uuid.UUID, uuid.UUID, string, time.Duration) error
	Complete(context.Context, *modelrepo.ImportTask, string, modelrepo.CreateVersionReq, any) error
	Fail(context.Context, *modelrepo.ImportTask, string, string) error
}

type importProgressStore interface {
	UpdateProgress(context.Context, uuid.UUID, uuid.UUID, string, int) error
}

type importRevisionStore interface {
	SetResolvedRevision(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, string, string) error
}

type WorkerConfig struct {
	WorkerID      string
	LeaseDuration time.Duration
	Limits        ArchiveLimits
}

type Worker struct {
	store         ImportStore
	objectStore   ports.ObjectStore
	sources       map[string]Source
	workerID      string
	leaseDuration time.Duration
	limits        ArchiveLimits
}

func NewWorker(store ImportStore, objectStore ports.ObjectStore, sources map[string]Source, cfg WorkerConfig) *Worker {
	if strings.TrimSpace(cfg.WorkerID) == "" {
		cfg.WorkerID = generatedWorkerID()
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = defaultImportLeaseDuration
	}
	// Keep in-process callers aligned with the production worker's bounded
	// workspace even when they omit optional limits. The command entrypoint
	// validates explicit env overrides before constructing the worker.
	cfg.Limits = NormalizeWorkerArchiveLimits(cfg.Limits)
	if sources == nil {
		sources = map[string]Source{
			"huggingface": NewHuggingFaceSource(),
			"modelscope":  NewModelScopeSource(),
		}
	}
	// Copy the map to ensure a caller cannot mutate source selection while a
	// message is in flight.
	copySources := make(map[string]Source, len(sources))
	for name, source := range sources {
		copySources[strings.ToLower(strings.TrimSpace(name))] = source
	}
	return &Worker{
		store:         store,
		objectStore:   objectStore,
		sources:       copySources,
		workerID:      cfg.WorkerID,
		leaseDuration: cfg.LeaseDuration,
		limits:        cfg.Limits,
	}
}

func generatedWorkerID() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown-host"
	}
	return "model-import-worker-" + host + "-" + uuid.NewString()
}

// Handle processes one import event. It returns nil for completed deliveries,
// malformed/foreign poison messages, and terminal policy failures so NATS can
// acknowledge them. Only transient source/database/object-store failures are
// returned for redelivery.
func (w *Worker) Handle(ctx context.Context, message ImportMessage) error {
	if w == nil || w.store == nil || w.objectStore == nil {
		return errWorkerUnavailable
	}
	if err := validateImportMessage(message); err != nil {
		return nil
	}
	ctx = types.WithTenant(ctx, &types.TenantContext{TenantID: message.TenantID})

	task, err := w.store.GetTask(ctx, message.TenantID, message.TaskID)
	if err != nil {
		if errors.Is(err, types.ErrNotFound) {
			return nil
		}
		return errDatabaseUnavailable
	}
	if task == nil || task.TenantID != message.TenantID || task.ID != message.TaskID {
		// A message with a mismatched tenant/task identity is a poison message.
		// Do not call Fail: doing so would mutate a row outside the message's
		// authenticated tenant boundary.
		return nil
	}
	if terminalTaskStatus(task.Status) {
		return nil
	}
	slog.Info("model import started", "task_id", message.TaskID, "model_id", message.ModelID, "source", message.Source, "repo_id", message.RepoID, "stage", "starting", "progress_pct", 0)

	importTask, err := w.store.GetImport(ctx, message.TenantID, message.TaskID)
	if err != nil {
		if errors.Is(err, types.ErrNotFound) {
			return nil
		}
		return errDatabaseUnavailable
	}
	if !importMatchesMessage(importTask, message) {
		return w.persistTerminalFailure(ctx, importTask, "import message rejected")
	}

	acquired, err := w.store.AcquireLease(ctx, message.TenantID, message.TaskID, w.workerID, w.leaseDuration)
	if err != nil {
		return errDatabaseUnavailable
	}
	if !acquired {
		// Another worker owns the lease, or the task completed between the read
		// and claim. A later delivery/reconciler will observe the durable state.
		return nil
	}
	completionCtx := ctx
	workCtx, cancelWork := context.WithCancel(ctx)
	heartbeatErr := make(chan error, 1)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		interval := w.leaseDuration / 3
		if interval <= 0 {
			interval = w.leaseDuration
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-workCtx.Done():
				return
			case <-ticker.C:
				if err := w.store.Heartbeat(workCtx, message.TenantID, message.TaskID, w.workerID, w.leaseDuration); err != nil {
					heartbeatErr <- err
					cancelWork()
					return
				}
			}
		}
	}()
	heartbeatStopped := false
	stopHeartbeat := func() error {
		if !heartbeatStopped {
			heartbeatStopped = true
			cancelWork()
			<-heartbeatDone
		}
		select {
		case err := <-heartbeatErr:
			return err
		default:
			return nil
		}
	}
	defer func() { _ = stopHeartbeat() }()
	ctx = workCtx
	heartbeatFailure := func() error {
		select {
		case err := <-heartbeatErr:
			return err
		default:
			return nil
		}
	}
	if progress, ok := w.store.(importProgressStore); ok {
		if err := progress.UpdateProgress(ctx, message.TenantID, message.TaskID, w.workerID, 5); err != nil {
			return w.persistRetryableFailure(ctx, importTask, "database unavailable", errDatabaseUnavailable)
		}
	}
	slog.Info("model import progress", "task_id", message.TaskID, "model_id", message.ModelID, "stage", "resolving_revision", "progress_pct", 5)

	sourceName := strings.ToLower(strings.TrimSpace(message.Source))
	source := w.sources[sourceName]
	if source == nil {
		return w.persistTerminalFailure(ctx, importTask, "import source rejected")
	}
	revision := strings.TrimSpace(importTask.Revision)
	if strings.TrimSpace(importTask.ResolvedRevision) != "" {
		revision = strings.TrimSpace(importTask.ResolvedRevision)
		if !immutableSourceRevision(sourceName, revision) {
			return w.persistTerminalFailure(ctx, importTask, "source revision rejected")
		}
	} else if resolver, ok := source.(RevisionResolver); ok {
		resolved, resolveErr := resolver.ResolveRevision(ctx, Repository{
			Source: sourceName, RepoID: message.RepoID, Revision: revision,
		})
		if resolveErr != nil {
			if permanentRevisionResolutionError(resolveErr) {
				return w.persistTerminalFailure(ctx, importTask, "source revision rejected")
			}
			return w.persistRetryableFailure(ctx, importTask, "source revision unavailable", errSourceUnavailable)
		}
		resolved = strings.TrimSpace(resolved)
		if !immutableSourceRevision(sourceName, resolved) {
			return w.persistTerminalFailure(ctx, importTask, "source revision rejected")
		}
		store, canPersist := w.store.(importRevisionStore)
		if !canPersist {
			return w.persistTerminalFailure(ctx, importTask, "source revision persistence unavailable")
		}
		if err := store.SetResolvedRevision(ctx, message.TenantID, importTask.ID, importTask.TaskID, w.workerID, revision, resolved); err != nil {
			if errors.Is(err, types.ErrConflict) {
				return w.persistTerminalFailure(ctx, importTask, "source revision conflict")
			}
			return w.persistRetryableFailure(ctx, importTask, "database unavailable", errDatabaseUnavailable)
		}
		importTask.ResolvedRevision = resolved
		revision = resolved
	}
	// Every public source must reach the archive builder at one immutable
	// snapshot. If a source was misconfigured without a resolver, reject a
	// mutable revision instead of silently archiving a moving branch.
	if !immutableSourceRevision(sourceName, revision) {
		return w.persistTerminalFailure(ctx, importTask, "source revision rejected")
	}
	archive, manifest, err := BuildArchive(ctx, source, Repository{
		Source: sourceName, RepoID: message.RepoID, Revision: revision,
	}, w.limits)
	if heartbeatFailure() != nil {
		return errDatabaseUnavailable
	}
	if err != nil {
		if errors.Is(err, ErrArchiveRejected) {
			return w.persistTerminalFailure(ctx, importTask, "archive rejected")
		}
		return w.persistRetryableFailure(ctx, importTask, "source unavailable", errSourceUnavailable)
	}
	defer archive.Close()
	if progress, ok := w.store.(importProgressStore); ok {
		if err := progress.UpdateProgress(ctx, message.TenantID, message.TaskID, w.workerID, 90); err != nil {
			return w.persistRetryableFailure(ctx, importTask, "database unavailable", errDatabaseUnavailable)
		}
	}
	slog.Info("model import progress", "task_id", message.TaskID, "model_id", message.ModelID, "stage", "archive_ready", "progress_pct", 90)

	objectRef, err := importObjectRef(importTask.TargetStoragePath, importTask)
	if err != nil {
		return w.persistTerminalFailure(ctx, importTask, "import target rejected")
	}
	if heartbeatFailure() != nil {
		return errDatabaseUnavailable
	}
	metadata, err := w.objectStore.PutObject(ctx, ports.PutObjectInput{
		Ref:         objectRef,
		Body:        archive,
		SizeBytes:   manifest.SizeBytes,
		ContentType: archiveContentType,
		Checksum:    "sha256:" + manifest.SHA256,
	})
	if heartbeatFailure() != nil {
		return errDatabaseUnavailable
	}
	if err != nil {
		return w.persistRetryableFailure(ctx, importTask, "object store unavailable", errObjectStoreUnavailable)
	}
	if metadata.SizeBytes > 0 && metadata.SizeBytes != manifest.SizeBytes {
		return w.persistRetryableFailure(ctx, importTask, "object store integrity check failed", errObjectStoreUnavailable)
	}
	if metadata.Checksum != "" && !checksumMatches(metadata.Checksum, manifest.SHA256) {
		return w.persistRetryableFailure(ctx, importTask, "object store integrity check failed", errObjectStoreUnavailable)
	}
	if progress, ok := w.store.(importProgressStore); ok {
		if err := progress.UpdateProgress(ctx, message.TenantID, message.TaskID, w.workerID, 95); err != nil {
			return w.persistRetryableFailure(ctx, importTask, "database unavailable", errDatabaseUnavailable)
		}
	}
	slog.Info("model import progress", "task_id", message.TaskID, "model_id", message.ModelID, "stage", "version_registering", "progress_pct", 95)

	versionRequest := modelrepo.CreateVersionReq{
		TenantID: message.TenantID,
		ModelID:  message.ModelID,
		Version:  "import-" + importTask.ID.String(),
		// The model_versions schema deliberately limits format to safetensors,
		// gguf and pytorch. A remote repository is packaged as a deterministic
		// archive; pytorch is the existing generic repository format marker and
		// keeps the worker within that contract.
		Format:         "pytorch",
		StoragePath:    importTask.TargetStoragePath,
		ChecksumSHA256: "sha256:" + manifest.SHA256,
		SizeBytes:      manifest.SizeBytes,
		IdempotencyKey: "model-import-version:" + importTask.ID.String(),
		RequestHash: modelrepo.ModelMutationHash(message.TenantID, "model.import.version",
			importTask.ID.String(), manifest.SHA256, fmt.Sprintf("%d", manifest.SizeBytes), importTask.TargetStoragePath),
	}
	result := map[string]any{
		"model_version_id": "",
		"storage_path":     importTask.TargetStoragePath,
		"checksum_sha256":  versionRequest.ChecksumSHA256,
		"size_bytes":       manifest.SizeBytes,
	}
	if err := stopHeartbeat(); err != nil {
		return errDatabaseUnavailable
	}
	if err := w.store.Complete(completionCtx, importTask, w.workerID, versionRequest, result); err != nil {
		return w.persistRetryableFailure(completionCtx, importTask, "database unavailable", errDatabaseUnavailable)
	}
	slog.Info("model import completed", "task_id", message.TaskID, "model_id", message.ModelID, "stage", "completed", "progress_pct", 100)
	return nil
}

func (w *Worker) persistTerminalFailure(ctx context.Context, importTask *modelrepo.ImportTask, message string) error {
	if importTask == nil {
		return nil
	}
	if err := w.store.Fail(ctx, importTask, w.workerID, message); err != nil {
		return errDatabaseUnavailable
	}
	return nil
}

func (w *Worker) persistRetryableFailure(ctx context.Context, importTask *modelrepo.ImportTask, message string, retryErr error) error {
	if importTask == nil {
		return retryErr
	}
	if err := w.store.Fail(ctx, importTask, w.workerID, message); err != nil {
		return errDatabaseUnavailable
	}
	return retryErr
}

func validateImportMessage(message ImportMessage) error {
	if message.TaskID == uuid.Nil || message.TenantID == uuid.Nil || message.ModelID == uuid.Nil {
		return errors.New("import message identifiers are required")
	}
	if strings.TrimSpace(message.IdempotencyKey) == "" || strings.TrimSpace(message.Source) == "" ||
		strings.TrimSpace(message.RepoID) == "" || strings.TrimSpace(message.Revision) == "" {
		return errors.New("import message fields are required")
	}
	return nil
}

func immutableSourceRevision(source, revision string) bool {
	switch source {
	case "huggingface":
		return isHuggingFaceCommit(revision)
	case "modelscope":
		return isModelScopeCommit(revision)
	default:
		return false
	}
}

// permanentRevisionResolutionError distinguishes a deterministic provider
// policy/identity failure from a transient dependency failure. A malformed
// or unsuccessful revision response, an unsafe redirect, and an HTTP 4xx
// (apart from timeout/rate-limit hints) cannot be fixed by redelivery. Network
// errors and 5xx/429 responses remain retryable.
func permanentRevisionResolutionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrSourceRevisionRejected) || errors.Is(err, ErrImmutableRevisionRequired) {
		return true
	}
	var httpErr *sourceHTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	if httpErr.redirect {
		return true
	}
	return httpErr.status >= http.StatusBadRequest && httpErr.status < http.StatusInternalServerError &&
		httpErr.status != http.StatusRequestTimeout && httpErr.status != http.StatusTooEarly &&
		httpErr.status != http.StatusTooManyRequests
}

func terminalTaskStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "dead_letter", "cancelled":
		return true
	default:
		return false
	}
}

func importMatchesMessage(task *modelrepo.ImportTask, message ImportMessage) bool {
	if task == nil || task.ID == uuid.Nil || task.TaskID == uuid.Nil || task.ModelID == uuid.Nil {
		return false
	}
	return task.TenantID == message.TenantID && task.TaskID == message.TaskID && task.ModelID == message.ModelID &&
		strings.EqualFold(strings.TrimSpace(task.Source), strings.TrimSpace(message.Source)) &&
		strings.TrimSpace(task.RepoID) == strings.TrimSpace(message.RepoID) &&
		strings.TrimSpace(task.Revision) == strings.TrimSpace(message.Revision)
}

func checksumMatches(got, expected string) bool {
	got = strings.TrimSpace(strings.ToLower(got))
	got = strings.TrimPrefix(got, "sha256:")
	return got == strings.ToLower(strings.TrimSpace(expected))
}

// importObjectRef parses and re-derives the object key from the persisted
// descriptor. The persisted URI is data, not authority: every identity
// component is checked against the tenant/model/import row before a PutObject.
func importObjectRef(raw string, task *modelrepo.ImportTask) (ports.ObjectRef, error) {
	if task == nil || task.TenantID == uuid.Nil || task.ModelID == uuid.Nil || task.ID == uuid.Nil {
		return ports.ObjectRef{}, errors.New("missing import identity")
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "object" || parsed.Host != "models" || parsed.User != nil ||
		parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" {
		return ports.ObjectRef{}, errors.New("invalid import object URI")
	}
	cleanPath := strings.TrimPrefix(parsed.Path, "/")
	parts := strings.Split(cleanPath, "/")
	if len(parts) != 5 || parts[0] != task.TenantID.String() || parts[1] != task.ModelID.String() ||
		parts[2] != "import-"+task.ID.String() || parts[3] != "archive" || parts[4] != "model.tar.gz" {
		return ports.ObjectRef{}, errors.New("import object URI identity mismatch")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.Contains(part, "\\") {
			return ports.ObjectRef{}, errors.New("invalid import object path")
		}
	}
	return ports.ObjectRef{
		TenantID:    task.TenantID.String(),
		BucketClass: ports.BucketClassModel,
		ObjectKey:   path.Join(task.ModelID.String(), "import-"+task.ID.String(), "archive", "model.tar.gz"),
		Version:     "import-" + task.ID.String(),
	}, nil
}

// PostgresImportStore wires the worker's atomic operations to the existing
// shared async-task repository and model repository.
type PostgresImportStore struct {
	pool   *pgxpool.Pool
	models *modelrepo.PostgresModelRepo
	tasks  *taskrepo.PostgresAsyncTaskRepo
}

func NewPostgresImportStore(pool *pgxpool.Pool, models *modelrepo.PostgresModelRepo, tasks *taskrepo.PostgresAsyncTaskRepo) *PostgresImportStore {
	return &PostgresImportStore{pool: pool, models: models, tasks: tasks}
}

func (s *PostgresImportStore) GetTask(ctx context.Context, _ uuid.UUID, taskID uuid.UUID) (*taskrepo.AsyncTask, error) {
	if s == nil || s.pool == nil || s.tasks == nil {
		return nil, errDatabaseUnavailable
	}
	return s.tasks.GetByID(ctx, s.pool, taskID)
}

func (s *PostgresImportStore) GetImport(ctx context.Context, tenantID, taskID uuid.UUID) (*modelrepo.ImportTask, error) {
	if s == nil || s.pool == nil || s.models == nil {
		return nil, errDatabaseUnavailable
	}
	return s.models.GetImportByTask(ctx, s.pool, tenantID, taskID)
}

func (s *PostgresImportStore) SetResolvedRevision(ctx context.Context, tenantID, importID, taskID uuid.UUID, workerID, expectedRevision, resolvedRevision string) error {
	if s == nil || s.pool == nil || s.models == nil {
		return errDatabaseUnavailable
	}
	return s.models.SetResolvedImportRevision(ctx, s.pool, tenantID, importID, taskID, workerID, expectedRevision, resolvedRevision)
}

func (s *PostgresImportStore) AcquireLease(ctx context.Context, _ uuid.UUID, taskID uuid.UUID, workerID string, duration time.Duration) (bool, error) {
	if s == nil || s.pool == nil || s.tasks == nil {
		return false, errDatabaseUnavailable
	}
	acquired, _, err := s.tasks.AcquireLease(ctx, s.pool, taskID, workerID, duration)
	return acquired, err
}

func (s *PostgresImportStore) Heartbeat(ctx context.Context, _ uuid.UUID, taskID uuid.UUID, workerID string, duration time.Duration) error {
	if s == nil || s.pool == nil || s.tasks == nil {
		return errDatabaseUnavailable
	}
	return s.tasks.Heartbeat(ctx, s.pool, taskID, workerID, duration)
}

func (s *PostgresImportStore) UpdateProgress(ctx context.Context, _ uuid.UUID, taskID uuid.UUID, workerID string, pct int) error {
	if s == nil || s.pool == nil || s.tasks == nil {
		return errDatabaseUnavailable
	}
	return s.tasks.UpdateProgress(ctx, s.pool, taskID, workerID, pct)
}

func (s *PostgresImportStore) Complete(ctx context.Context, task *modelrepo.ImportTask, workerID string, req modelrepo.CreateVersionReq, result any) error {
	if s == nil || s.pool == nil || s.models == nil || s.tasks == nil || task == nil {
		return errDatabaseUnavailable
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return err
	}
	version, err := s.models.CreateVersion(ctx, tx, req)
	if err != nil {
		return err
	}
	if err := s.models.CompleteImportTask(ctx, tx, task.TenantID, task.ID, task.TaskID, version.ID); err != nil {
		return err
	}
	if resultMap, ok := result.(map[string]any); ok {
		resultMap["model_version_id"] = version.ID.String()
	}
	if err := s.tasks.Complete(ctx, tx, task.TaskID, workerID, result); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresImportStore) Fail(ctx context.Context, task *modelrepo.ImportTask, workerID, message string) error {
	if s == nil || s.pool == nil || s.models == nil || s.tasks == nil || task == nil {
		return errDatabaseUnavailable
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := types.SetDBTenant(ctx, tx); err != nil {
		return err
	}
	if err := s.models.FailImportTask(ctx, tx, task.TenantID, task.ID, task.TaskID, message); err != nil {
		return err
	}
	if err := s.tasks.Fail(ctx, tx, task.TaskID, workerID, message, ""); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var _ ImportStore = (*PostgresImportStore)(nil)
