package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
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

// RecoveryStore lists durable model-import payloads whose task lease is
// available again. It is separate from ImportStore so the normal per-message
// path remains tenant-scoped while the worker's explicit recovery scan can
// use the platform task entry point.
type RecoveryStore interface {
	ListRecoverableImports(context.Context, int) ([]ImportMessage, error)
}

type importProgressStore interface {
	UpdateProgress(context.Context, uuid.UUID, uuid.UUID, string, int) error
}

type importFileStore interface {
	EnsureImportFiles(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, []modelrepo.ImportFileSpec) error
	ListImportFiles(context.Context, uuid.UUID, uuid.UUID) ([]modelrepo.ImportFile, error)
	SaveImportFileCheckpoint(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, string, string, int64, []modelrepo.ImportFilePart) error
	CompleteImportFile(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, string, string, int64) error
}

type importRevisionStore interface {
	SetResolvedRevision(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, string, string) error
}

type WorkerConfig struct {
	WorkerID      string
	LeaseDuration time.Duration
	// Limits apply only to legacy archive descriptors. New snapshot imports
	// stream files directly and are bounded by their provider-declared sizes.
	Limits ArchiveLimits
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
	// Keep legacy archive callers aligned with the production worker's bounded
	// workspace even when they omit optional limits. Snapshot imports do not use
	// these limits.
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
	if strings.HasSuffix(strings.TrimSpace(importTask.TargetStoragePath), "/snapshot/manifest.json") {
		fileStore, ok := w.store.(importFileStore)
		if !ok {
			return w.persistTerminalFailure(ctx, importTask, "import file persistence unavailable")
		}
		snapshot, manifestSHA, manifestSize, streamErr := w.streamSnapshot(ctx, message, importTask, source, revision, fileStore)
		if heartbeatFailure() != nil {
			return errDatabaseUnavailable
		}
		if streamErr != nil {
			switch {
			case errors.Is(streamErr, errDatabaseUnavailable), errors.Is(streamErr, types.ErrLeaseTaken):
				return w.persistRetryableFailure(ctx, importTask, "database unavailable", errDatabaseUnavailable)
			case errors.Is(streamErr, errSourceUnavailable):
				return w.persistRetryableFailure(ctx, importTask, "source unavailable", errSourceUnavailable)
			case errors.Is(streamErr, errObjectStoreUnavailable):
				return w.persistRetryableFailure(ctx, importTask, "object store unavailable", errObjectStoreUnavailable)
			default:
				return w.persistTerminalFailure(ctx, importTask, "model snapshot rejected")
			}
		}
		if progress, ok := w.store.(importProgressStore); ok {
			if err := progress.UpdateProgress(ctx, message.TenantID, message.TaskID, w.workerID, 95); err != nil {
				return w.persistRetryableFailure(ctx, importTask, "database unavailable", errDatabaseUnavailable)
			}
		}
		return w.completeImportedVersion(completionCtx, message, importTask, revision, manifestSHA, manifestSize, snapshot.TotalSizeBytes, stopHeartbeat)
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
	defer func() {
		if err := archive.Close(); err != nil {
			slog.Warn("model import archive cleanup failed", "task_id", message.TaskID, "model_id", message.ModelID)
		}
	}()
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

// streamSnapshot transfers one immutable repository file at a time. The
// source response is consumed directly by the object-store adapter; only the
// SHA-256 state and database checkpoint are retained in memory.
func (w *Worker) streamSnapshot(ctx context.Context, message ImportMessage, task *modelrepo.ImportTask, source Source, revision string, filesStore importFileStore) (types.ModelSnapshot, string, int64, error) {
	files, err := source.List(ctx, Repository{Source: message.Source, RepoID: message.RepoID, Revision: revision})
	if err != nil {
		return types.ModelSnapshot{}, "", 0, fmt.Errorf("%w: list source files", errSourceUnavailable)
	}
	if len(files) == 0 {
		return types.ModelSnapshot{}, "", 0, errors.New("source contains no regular files")
	}
	specs := make([]modelrepo.ImportFileSpec, 0, len(files))
	base := path.Join(task.ModelID.String(), "import-"+task.ID.String(), "snapshot", "files")
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	seenPaths := make(map[string]struct{}, len(files))
	for _, file := range files {
		if file.Size < 0 || !validSnapshotFilePath(file.Path) {
			return types.ModelSnapshot{}, "", 0, errors.New("source file path is invalid")
		}
		if _, exists := seenPaths[file.Path]; exists {
			return types.ModelSnapshot{}, "", 0, errors.New("source contains duplicate file paths")
		}
		seenPaths[file.Path] = struct{}{}
		specs = append(specs, modelrepo.ImportFileSpec{Path: file.Path, ObjectKey: path.Join(base, file.Path), SizeBytes: file.Size})
	}
	if err := filesStore.EnsureImportFiles(ctx, message.TenantID, task.ID, task.TaskID, w.workerID, specs); err != nil {
		return types.ModelSnapshot{}, "", 0, errDatabaseUnavailable
	}
	records, err := filesStore.ListImportFiles(ctx, message.TenantID, task.ID)
	if err != nil {
		return types.ModelSnapshot{}, "", 0, errDatabaseUnavailable
	}
	byPath := make(map[string]modelrepo.ImportFile, len(records))
	for _, record := range records {
		byPath[record.Path] = record
	}
	snapshot := types.ModelSnapshot{Schema: types.ModelSnapshotSchema, Revision: revision}
	for _, file := range files {
		record, found := byPath[file.Path]
		if !found {
			return types.ModelSnapshot{}, "", 0, errors.New("import file row missing")
		}
		objectRef := ports.ObjectRef{TenantID: message.TenantID.String(), BucketClass: ports.BucketClassModel, ObjectKey: record.ObjectKey, Version: "import-" + task.ID.String()}
		if record.Status == "completed" && len(record.SHA256) == 64 {
			if w.snapshotFileObjectValid(ctx, objectRef, file.Size, record.SHA256) {
				snapshot.Files = append(snapshot.Files, types.ModelSnapshotFile{Path: file.Path, SizeBytes: file.Size, SHA256: record.SHA256, ObjectKey: record.ObjectKey})
				continue
			}
		}
		checksum, uploadErr := w.uploadSnapshotFile(ctx, message, task, source, revision, file, record, objectRef, filesStore)
		if uploadErr != nil {
			return types.ModelSnapshot{}, "", 0, uploadErr
		}
		if err := filesStore.CompleteImportFile(ctx, message.TenantID, task.ID, task.TaskID, w.workerID, file.Path, checksum, file.Size); err != nil {
			return types.ModelSnapshot{}, "", 0, errDatabaseUnavailable
		}
		snapshot.Files = append(snapshot.Files, types.ModelSnapshotFile{Path: file.Path, SizeBytes: file.Size, SHA256: checksum, ObjectKey: record.ObjectKey})
		slog.Info("model import file completed", "task_id", message.TaskID, "model_id", message.ModelID, "file_path", file.Path, "file_size_bytes", file.Size, "stage", "file_uploaded")
	}
	for _, item := range snapshot.Files {
		if item.SizeBytes > 0 && snapshot.TotalSizeBytes > (1<<63-1)-item.SizeBytes {
			return types.ModelSnapshot{}, "", 0, errors.New("model snapshot size overflows int64")
		}
		snapshot.TotalSizeBytes += item.SizeBytes
	}
	if err := snapshot.Validate(); err != nil {
		return types.ModelSnapshot{}, "", 0, err
	}
	manifestBytes, err := json.Marshal(snapshot)
	if err != nil {
		return types.ModelSnapshot{}, "", 0, err
	}
	manifestRef, err := importObjectRef(task.TargetStoragePath, task)
	if err != nil {
		return types.ModelSnapshot{}, "", 0, err
	}
	digest := sha256.Sum256(manifestBytes)
	manifestSHA := hex.EncodeToString(digest[:])
	metadata, err := w.objectStore.PutObject(ctx, ports.PutObjectInput{Ref: manifestRef, Body: bytes.NewReader(manifestBytes), SizeBytes: int64(len(manifestBytes)), ContentType: "application/json", Checksum: "sha256:" + manifestSHA})
	if err != nil || metadata.SizeBytes != int64(len(manifestBytes)) {
		return types.ModelSnapshot{}, "", 0, errObjectStoreUnavailable
	}
	slog.Info("model import snapshot ready", "task_id", message.TaskID, "model_id", message.ModelID, "file_count", len(snapshot.Files), "total_size_bytes", snapshot.TotalSizeBytes, "manifest_size_bytes", len(manifestBytes), "stage", "manifest_ready")
	return snapshot, manifestSHA, int64(len(manifestBytes)), nil
}

func (w *Worker) snapshotFileObjectValid(ctx context.Context, ref ports.ObjectRef, size int64, checksum string) bool {
	metadata, err := w.objectStore.StatObject(ctx, ref)
	if err != nil || metadata.SizeBytes != size {
		return false
	}
	if size == 0 {
		return true
	}
	if checksumMatches(metadata.Checksum, checksum) {
		return true
	}
	if strings.TrimSpace(metadata.Checksum) != "" {
		return false
	}
	verifier, ok := w.objectStore.(ports.ObjectStoreContentVerifier)
	return ok && verifier.VerifyObject(ctx, ref, size, checksum) == nil
}

func validSnapshotFilePath(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, "//") || strings.Contains(value, "..") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

const defaultMultipartPartSize int64 = 128 << 20

func (w *Worker) uploadSnapshotFile(ctx context.Context, message ImportMessage, task *modelrepo.ImportTask, source Source, revision string, file RemoteFile, record modelrepo.ImportFile, objectRef ports.ObjectRef, filesStore importFileStore) (string, error) {
	// A worker may have completed the remote multipart upload and crashed
	// before its file row was marked complete. The upload ID is the durable
	// hint for that window; recover the visible object before asking MinIO for
	// parts on an upload that no longer exists.
	if strings.TrimSpace(record.UploadID) != "" {
		if checksum, ok := w.readStoredSnapshotFile(ctx, objectRef, file.Size); ok {
			return checksum, nil
		}
	}
	// Multipart is selected only when both sides support ranged streaming. The
	// direct PutObject path still streams and is useful for small files and
	// legacy object-store implementations.
	multipart, multipartOK := w.objectStore.(ports.MultipartObjectStore)
	ranged, rangedOK := source.(RangeSource)
	if multipartOK && rangedOK && file.Size > defaultMultipartPartSize {
		return w.uploadMultipartFile(ctx, message, task, source, revision, ranged, file, record, objectRef, filesStore, multipart)
	}
	body, actualSize, openErr := source.Open(ctx, Repository{Source: message.Source, RepoID: message.RepoID, Revision: revision}, file.Path)
	if openErr != nil || body == nil {
		if body != nil {
			_ = body.Close()
		}
		return "", fmt.Errorf("%w: open %s", errSourceUnavailable, file.Path)
	}
	if actualSize >= 0 && actualSize != file.Size {
		_ = body.Close()
		return "", errors.New("source file size changed")
	}
	digest := sha256.New()
	metadata, putErr := w.objectStore.PutObject(ctx, ports.PutObjectInput{Ref: objectRef, Body: io.TeeReader(body, digest), SizeBytes: file.Size, ContentType: "application/octet-stream"})
	closeErr := body.Close()
	if putErr != nil || closeErr != nil {
		return "", fmt.Errorf("%w: upload %s", errObjectStoreUnavailable, file.Path)
	}
	if metadata.SizeBytes != file.Size {
		return "", errors.New("uploaded file size mismatch")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (w *Worker) uploadMultipartFile(ctx context.Context, message ImportMessage, task *modelrepo.ImportTask, source Source, revision string, ranged RangeSource, file RemoteFile, record modelrepo.ImportFile, objectRef ports.ObjectRef, filesStore importFileStore, multipart ports.MultipartObjectStore) (string, error) {
	partSize := defaultMultipartPartSize
	if required := (file.Size + 9999) / 10000; required > partSize {
		partSize = ((required + (1 << 20) - 1) / (1 << 20)) * (1 << 20)
	}
	uploadID := strings.TrimSpace(record.UploadID)
	if uploadID == "" {
		var err error
		uploadID, err = multipart.BeginMultipart(ctx, objectRef, "application/octet-stream")
		if err != nil {
			return "", fmt.Errorf("%w: begin multipart upload", errObjectStoreUnavailable)
		}
		if err := filesStore.SaveImportFileCheckpoint(ctx, message.TenantID, task.ID, task.TaskID, w.workerID, file.Path, uploadID, 0, nil); err != nil {
			_ = multipart.AbortMultipart(ctx, objectRef, uploadID)
			return "", errDatabaseUnavailable
		}
	}
	remoteParts, err := multipart.ListParts(ctx, objectRef, uploadID)
	if err != nil {
		return "", fmt.Errorf("%w: list multipart parts", errObjectStoreUnavailable)
	}
	partsByNumber := make(map[int]ports.MultipartPart, len(remoteParts))
	for _, part := range remoteParts {
		partsByNumber[part.Number] = part
	}
	parts := make([]ports.MultipartPart, 0, (file.Size+partSize-1)/partSize)
	digest := sha256.New()
	hashedAll := true
	uploadedBytes := int64(0)
	for offset, number := int64(0), 1; offset < file.Size; offset, number = offset+partSize, number+1 {
		length := partSize
		if remain := file.Size - offset; remain < length {
			length = remain
		}
		if existing, ok := partsByNumber[number]; ok && existing.Size == length {
			parts = append(parts, existing)
			uploadedBytes += existing.Size
			hashedAll = false
			continue
		}
		body, openErr := ranged.OpenRange(ctx, Repository{Source: message.Source, RepoID: message.RepoID, Revision: revision}, file.Path, offset, length)
		if openErr != nil || body == nil {
			if body != nil {
				_ = body.Close()
			}
			return "", fmt.Errorf("%w: open range %s", errSourceUnavailable, file.Path)
		}
		part, uploadErr := multipart.UploadPart(ctx, objectRef, uploadID, number, io.TeeReader(body, digest), length)
		closeErr := body.Close()
		if uploadErr != nil || closeErr != nil {
			return "", fmt.Errorf("%w: upload part %s", errObjectStoreUnavailable, file.Path)
		}
		parts = append(parts, part)
		uploadedBytes += part.Size
		repoParts := make([]modelrepo.ImportFilePart, len(parts))
		for i, item := range parts {
			repoParts[i] = modelrepo.ImportFilePart{Number: item.Number, ETag: item.ETag, Size: item.Size}
		}
		if err := filesStore.SaveImportFileCheckpoint(ctx, message.TenantID, task.ID, task.TaskID, w.workerID, file.Path, uploadID, uploadedBytes, repoParts); err != nil {
			return "", errDatabaseUnavailable
		}
		slog.Info("model import part completed", "task_id", message.TaskID, "model_id", message.ModelID, "file_path", file.Path, "part_number", number, "part_size_bytes", length, "uploaded_bytes", uploadedBytes, "file_size_bytes", file.Size, "stage", "part_uploaded")
	}
	if _, err := multipart.CompleteMultipart(ctx, objectRef, uploadID, parts); err != nil {
		return "", fmt.Errorf("%w: complete multipart upload", errObjectStoreUnavailable)
	}
	if hashedAll {
		return hex.EncodeToString(digest.Sum(nil)), nil
	}
	reader, metadata, err := w.objectStore.GetObject(ctx, objectRef)
	if err != nil || reader == nil {
		return "", fmt.Errorf("%w: verify multipart object", errObjectStoreUnavailable)
	}
	defer reader.Close()
	verifyDigest := sha256.New()
	read, err := io.Copy(verifyDigest, reader)
	if err != nil || read != file.Size || metadata.SizeBytes != 0 && metadata.SizeBytes != file.Size {
		return "", errors.New("multipart object size verification failed")
	}
	return hex.EncodeToString(verifyDigest.Sum(nil)), nil
}

func (w *Worker) readStoredSnapshotFile(ctx context.Context, ref ports.ObjectRef, expectedSize int64) (string, bool) {
	reader, metadata, err := w.objectStore.GetObject(ctx, ref)
	if err != nil || reader == nil {
		return "", false
	}
	defer reader.Close()
	digest := sha256.New()
	read, err := io.Copy(digest, reader)
	if err != nil || read != expectedSize || metadata.SizeBytes != 0 && metadata.SizeBytes != expectedSize {
		return "", false
	}
	return hex.EncodeToString(digest.Sum(nil)), true
}

func (w *Worker) completeImportedVersion(ctx context.Context, message ImportMessage, task *modelrepo.ImportTask, revision, manifestSHA string, manifestSize, contentSize int64, stopHeartbeat func() error) error {
	slog.Info("model import progress", "task_id", message.TaskID, "model_id", message.ModelID, "stage", "version_registering", "progress_pct", 95, "revision", revision)
	if err := stopHeartbeat(); err != nil {
		return errDatabaseUnavailable
	}
	versionRequest := modelrepo.CreateVersionReq{TenantID: message.TenantID, ModelID: message.ModelID, Version: "import-" + task.ID.String(), Format: "pytorch", StoragePath: task.TargetStoragePath, ChecksumSHA256: "sha256:" + manifestSHA, SizeBytes: manifestSize, ContentSizeBytes: contentSize, IdempotencyKey: "model-import-version:" + task.ID.String(), RequestHash: modelrepo.ModelMutationHash(message.TenantID, "model.import.version", task.ID.String(), manifestSHA, fmt.Sprintf("%d", manifestSize), fmt.Sprintf("%d", contentSize), task.TargetStoragePath)}
	result := map[string]any{"model_version_id": "", "storage_path": task.TargetStoragePath, "checksum_sha256": versionRequest.ChecksumSHA256, "size_bytes": manifestSize, "content_size_bytes": contentSize}
	if err := w.store.Complete(ctx, task, w.workerID, versionRequest, result); err != nil {
		slog.Error("model import version registration failed", "task_id", message.TaskID, "model_id", message.ModelID, "err", err)
		return w.persistRetryableFailure(ctx, task, "database unavailable", errDatabaseUnavailable)
	}
	slog.Info("model import completed", "task_id", message.TaskID, "model_id", message.ModelID, "stage", "completed", "progress_pct", 100, "revision", revision)
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
		parts[2] != "import-"+task.ID.String() ||
		!((parts[3] == "archive" && parts[4] == "model.tar.gz") || (parts[3] == "snapshot" && parts[4] == "manifest.json")) {
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
		ObjectKey:   path.Join(task.ModelID.String(), "import-"+task.ID.String(), parts[3], parts[4]),
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

// ListRecoverableImports reads only durable model-import payloads. The
// async_tasks RLS policy has an explicit platform path when no tenant setting
// is present; no model or import row is read here. Handle reclaims the lease
// and re-checks all tenant-scoped identities before doing any work.
func (s *PostgresImportStore) ListRecoverableImports(ctx context.Context, limit int) ([]ImportMessage, error) {
	if s == nil || s.pool == nil {
		return nil, errDatabaseUnavailable
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, errDatabaseUnavailable
	}
	defer tx.Rollback(ctx) //nolint:errcheck // commit below is the success path.
	rows, err := tx.Query(ctx, `
		SELECT payload
		FROM async_tasks
		WHERE task_type='model.import'
		  AND status IN ('pending','running','failed')
		  AND (status <> 'running' OR lease_until IS NULL OR lease_until < NOW())
		ORDER BY created_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list recoverable model imports: %w", err)
	}
	defer rows.Close()
	messages := make([]ImportMessage, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan recoverable model import: %w", err)
		}
		var message ImportMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			slog.Warn("ignoring malformed durable model import payload", "err", err)
			continue
		}
		if validateImportMessage(message) != nil {
			slog.Warn("ignoring invalid durable model import payload", "task_id", message.TaskID)
			continue
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recoverable model imports: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit recoverable model imports: %w", err)
	}
	return messages, nil
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

func (s *PostgresImportStore) EnsureImportFiles(ctx context.Context, tenantID, importID, taskID uuid.UUID, workerID string, files []modelrepo.ImportFileSpec) error {
	if s == nil || s.pool == nil || s.models == nil {
		return errDatabaseUnavailable
	}
	return s.models.EnsureImportFiles(ctx, s.pool, tenantID, importID, taskID, workerID, files)
}

func (s *PostgresImportStore) ListImportFiles(ctx context.Context, tenantID, importID uuid.UUID) ([]modelrepo.ImportFile, error) {
	if s == nil || s.pool == nil || s.models == nil {
		return nil, errDatabaseUnavailable
	}
	return s.models.ListImportFiles(ctx, s.pool, tenantID, importID)
}

func (s *PostgresImportStore) SaveImportFileCheckpoint(ctx context.Context, tenantID, importID, taskID uuid.UUID, workerID, filePath, uploadID string, uploadedBytes int64, parts []modelrepo.ImportFilePart) error {
	if s == nil || s.pool == nil || s.models == nil {
		return errDatabaseUnavailable
	}
	return s.models.SaveImportFileCheckpoint(ctx, s.pool, tenantID, importID, taskID, workerID, filePath, uploadID, uploadedBytes, parts)
}

func (s *PostgresImportStore) CompleteImportFile(ctx context.Context, tenantID, importID, taskID uuid.UUID, workerID, filePath, checksum string, size int64) error {
	if s == nil || s.pool == nil || s.models == nil {
		return errDatabaseUnavailable
	}
	return s.models.CompleteImportFile(ctx, s.pool, tenantID, importID, taskID, workerID, filePath, checksum, size)
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
