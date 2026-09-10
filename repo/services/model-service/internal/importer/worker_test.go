package importer

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
	taskrepo "github.com/kubercloud/ani/pkg/repo"
	"github.com/kubercloud/ani/pkg/types"
	modelrepo "github.com/kubercloud/ani/services/model-service/internal/repo"
)

var (
	testTenantID = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	testModelID  = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	testTaskID   = uuid.MustParse("33333333-3333-3333-3333-333333333333")
	testImportID = uuid.MustParse("44444444-4444-4444-4444-444444444444")
)

const testHuggingFaceCommit = "0123456789abcdef0123456789abcdef01234567"

type workerFakeStore struct {
	task                 *taskrepo.AsyncTask
	importTask           *modelrepo.ImportTask
	acquired             bool
	acquireN             int
	completeN            int
	failN                int
	resolvedRevisionSetN int
	version              modelrepo.CreateVersionReq
	failMsg              string
	completeErr          error
	failErr              error
	heartbeatN           atomic.Int32
	completeCtxErr       error
	resolvedRevision     string
	resolveRevisionErr   error
	resolvedWorkerID     string
}

func (s *workerFakeStore) GetTask(context.Context, uuid.UUID, uuid.UUID) (*taskrepo.AsyncTask, error) {
	if s.task == nil {
		return nil, errors.New("task missing")
	}
	return s.task, nil
}

func (s *workerFakeStore) GetImport(context.Context, uuid.UUID, uuid.UUID) (*modelrepo.ImportTask, error) {
	if s.importTask == nil {
		return nil, errors.New("import missing")
	}
	return s.importTask, nil
}

func (s *workerFakeStore) AcquireLease(context.Context, uuid.UUID, uuid.UUID, string, time.Duration) (bool, error) {
	s.acquireN++
	return s.acquired, nil
}

func (s *workerFakeStore) Heartbeat(context.Context, uuid.UUID, uuid.UUID, string, time.Duration) error {
	s.heartbeatN.Add(1)
	return nil
}

func (s *workerFakeStore) Complete(ctx context.Context, _ *modelrepo.ImportTask, _ string, version modelrepo.CreateVersionReq, _ any) error {
	s.completeN++
	s.version = version
	s.completeCtxErr = ctx.Err()
	return s.completeErr
}

func (s *workerFakeStore) Fail(_ context.Context, _ *modelrepo.ImportTask, _ string, message string) error {
	s.failN++
	s.failMsg = message
	return s.failErr
}

func (s *workerFakeStore) SetResolvedRevision(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID, workerID, expected, resolved string) error {
	s.resolvedRevisionSetN++
	if s.resolveRevisionErr != nil {
		return s.resolveRevisionErr
	}
	if s.importTask != nil && strings.TrimSpace(s.importTask.Revision) != strings.TrimSpace(expected) {
		return errors.New("revision expectation mismatch")
	}
	s.resolvedRevision = resolved
	s.resolvedWorkerID = workerID
	if s.importTask != nil {
		s.importTask.ResolvedRevision = resolved
	}
	return nil
}

type workerFakeObjectStore struct {
	input ports.PutObjectInput
	data  []byte
	err   error
}

func (s *workerFakeObjectStore) Health(context.Context) error                          { return nil }
func (s *workerFakeObjectStore) EnsureBucket(context.Context, ports.BucketClass) error { return nil }
func (s *workerFakeObjectStore) BucketUsage(context.Context, ports.BucketClass, string) (ports.BucketUsage, error) {
	return ports.BucketUsage{}, nil
}
func (s *workerFakeObjectStore) PutObject(_ context.Context, input ports.PutObjectInput) (ports.ObjectMetadata, error) {
	s.input = input
	if input.Body != nil {
		s.data, _ = io.ReadAll(input.Body)
	}
	if s.err != nil {
		return ports.ObjectMetadata{}, s.err
	}
	return ports.ObjectMetadata{Ref: input.Ref, SizeBytes: int64(len(s.data)), Checksum: input.Checksum}, nil
}
func (s *workerFakeObjectStore) GetObject(context.Context, ports.ObjectRef) (io.ReadCloser, ports.ObjectMetadata, error) {
	return nil, ports.ObjectMetadata{}, errors.New("not implemented")
}
func (s *workerFakeObjectStore) DeleteObject(context.Context, ports.ObjectRef) error { return nil }
func (s *workerFakeObjectStore) StatObject(context.Context, ports.ObjectRef) (ports.ObjectMetadata, error) {
	return ports.ObjectMetadata{}, errors.New("not implemented")
}
func (s *workerFakeObjectStore) SignedUploadURL(context.Context, ports.ObjectRef, time.Duration) (ports.SignedURL, error) {
	return ports.SignedURL{}, errors.New("not implemented")
}
func (s *workerFakeObjectStore) SignedDownloadURL(context.Context, ports.ObjectRef, time.Duration) (ports.SignedURL, error) {
	return ports.SignedURL{}, errors.New("not implemented")
}

type workerFailingSource struct{ err error }

func (s workerFailingSource) List(context.Context, Repository) ([]RemoteFile, error) {
	return nil, s.err
}
func (s workerFailingSource) Open(context.Context, Repository, string) (io.ReadCloser, int64, error) {
	return nil, 0, s.err
}

func testImportMessage() (ImportMessage, *workerFakeStore) {
	path := "object://models/" + testTenantID.String() + "/" + testModelID.String() + "/import-" + testImportID.String() + "/archive/model.tar.gz"
	store := &workerFakeStore{
		task:       &taskrepo.AsyncTask{TenantID: testTenantID, ID: testTaskID, Status: "pending", IdempotencyKey: "import-key", TaskType: "model.import"},
		importTask: &modelrepo.ImportTask{ID: testImportID, TenantID: testTenantID, ModelID: testModelID, TaskID: testTaskID, Source: "huggingface", RepoID: "org/model", Revision: "main", Status: "pending", TargetStoragePath: path},
		acquired:   true,
	}
	return ImportMessage{TaskID: testTaskID, IdempotencyKey: "import-key", TenantID: testTenantID, ModelID: testModelID, Source: "huggingface", RepoID: "org/model", Revision: "main"}, store
}

func TestWorkerHandleUploadsArchiveAndCompletesAtomically(t *testing.T) {
	message, store := testImportMessage()
	store.importTask.ResolvedRevision = testHuggingFaceCommit
	objectStore := &workerFakeObjectStore{}
	source := fixtureSource{files: []RemoteFile{{Path: "config.json", Size: 2}}, data: map[string]string{"config.json": "{}"}}
	worker := NewWorker(store, objectStore, map[string]Source{"huggingface": source}, WorkerConfig{WorkerID: "worker-1", LeaseDuration: time.Minute})
	if err := worker.Handle(context.Background(), message); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.completeN != 1 || store.failN != 0 {
		t.Fatalf("complete/fail calls = %d/%d, want 1/0", store.completeN, store.failN)
	}
	if store.completeCtxErr != nil {
		t.Fatalf("Complete() received canceled context: %v", store.completeCtxErr)
	}
	if len(objectStore.data) == 0 || objectStore.input.Ref.TenantID != testTenantID.String() || objectStore.input.Ref.BucketClass != ports.BucketClassModel {
		t.Fatalf("uploaded ref/data = %+v/%d", objectStore.input.Ref, len(objectStore.data))
	}
	if !strings.HasSuffix(objectStore.input.Ref.ObjectKey, "/archive/model.tar.gz") || store.version.StoragePath != store.importTask.TargetStoragePath {
		t.Fatalf("object key/version path = %q/%q", objectStore.input.Ref.ObjectKey, store.version.StoragePath)
	}
	if store.version.ChecksumSHA256 == "" || store.version.SizeBytes <= 0 || store.version.IdempotencyKey == "" {
		t.Fatalf("version request lacks authoritative archive metadata: %+v", store.version)
	}
}

func TestWorkerPinsMutableHuggingFaceRevisionBeforeArchive(t *testing.T) {
	message, store := testImportMessage()
	objectStore := &workerFakeObjectStore{}
	source := &resolvingWorkerSource{
		fixtureSource: fixtureSource{files: []RemoteFile{{Path: "config.json", Size: 2}}, data: map[string]string{"config.json": "{}"}},
		resolved:      "0123456789abcdef0123456789abcdef01234567",
	}
	worker := NewWorker(store, objectStore, map[string]Source{"huggingface": source}, WorkerConfig{WorkerID: "worker-1", LeaseDuration: time.Minute})
	if err := worker.Handle(context.Background(), message); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.resolvedRevision != source.resolved || store.resolvedWorkerID != "worker-1" || source.listRevision != source.resolved || source.openRevision != source.resolved {
		t.Fatalf("resolved=%q worker=%q list=%q open=%q, want persisted commit with lease owner", store.resolvedRevision, store.resolvedWorkerID, source.listRevision, source.openRevision)
	}
}

func TestWorkerPinsMutableModelScopeRevisionBeforeArchive(t *testing.T) {
	message, store := testImportMessage()
	message.Source = "modelscope"
	store.importTask.Source = "modelscope"
	objectStore := &workerFakeObjectStore{}
	source := &resolvingWorkerSource{
		fixtureSource: fixtureSource{files: []RemoteFile{{Path: "config.json", Size: 2}}, data: map[string]string{"config.json": "{}"}},
		resolved:      "0123456789abcdef0123456789abcdef01234567",
	}
	worker := NewWorker(store, objectStore, map[string]Source{"modelscope": source}, WorkerConfig{WorkerID: "worker-1", LeaseDuration: time.Minute})
	if err := worker.Handle(context.Background(), message); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.resolvedRevision != source.resolved || store.resolvedWorkerID != "worker-1" || source.listRevision != source.resolved || source.openRevision != source.resolved {
		t.Fatalf("resolved=%q worker=%q list=%q open=%q, want persisted ModelScope commit with lease owner", store.resolvedRevision, store.resolvedWorkerID, source.listRevision, source.openRevision)
	}
}

func TestWorkerRejectsMutableHuggingFaceRevisionWithoutResolver(t *testing.T) {
	message, store := testImportMessage()
	objectStore := &workerFakeObjectStore{}
	source := fixtureSource{files: []RemoteFile{{Path: "config.json", Size: 2}}, data: map[string]string{"config.json": "{}"}}
	worker := NewWorker(store, objectStore, map[string]Source{"huggingface": source}, WorkerConfig{WorkerID: "worker-1"})
	if err := worker.Handle(context.Background(), message); err != nil {
		t.Fatalf("Handle() error = %v, want terminal ack", err)
	}
	if store.failN != 1 || !strings.Contains(store.failMsg, "revision") || store.completeN != 0 {
		t.Fatalf("failure=%d/%q complete=%d, want mutable revision rejection", store.failN, store.failMsg, store.completeN)
	}
}

func TestWorkerRejectsMutableModelScopeRevision(t *testing.T) {
	message, store := testImportMessage()
	message.Source = "modelscope"
	store.importTask.Source = "modelscope"
	objectStore := &workerFakeObjectStore{}
	source := fixtureSource{files: []RemoteFile{{Path: "config.json", Size: 2}}, data: map[string]string{"config.json": "{}"}}
	worker := NewWorker(store, objectStore, map[string]Source{"modelscope": source}, WorkerConfig{WorkerID: "worker-1"})
	if err := worker.Handle(context.Background(), message); err != nil {
		t.Fatalf("Handle() error = %v, want terminal ack", err)
	}
	if store.failN != 1 || !strings.Contains(store.failMsg, "revision") || store.completeN != 0 {
		t.Fatalf("failure = %d/%q complete=%d, want mutable revision rejection", store.failN, store.failMsg, store.completeN)
	}
}

func TestWorkerRejectsInvalidResolvedModelScopeRevisionBeforePersistence(t *testing.T) {
	message, store := testImportMessage()
	message.Source = "modelscope"
	store.importTask.Source = "modelscope"
	objectStore := &workerFakeObjectStore{}
	source := &resolvingWorkerSource{
		fixtureSource: fixtureSource{files: []RemoteFile{{Path: "config.json", Size: 2}}, data: map[string]string{"config.json": "{}"}},
		resolved:      "",
	}
	worker := NewWorker(store, objectStore, map[string]Source{"modelscope": source}, WorkerConfig{WorkerID: "worker-1"})
	if err := worker.Handle(context.Background(), message); err != nil {
		t.Fatalf("Handle() error = %v, want terminal ack", err)
	}
	if store.failN != 1 || !strings.Contains(store.failMsg, "revision") || store.resolvedRevisionSetN != 0 || store.completeN != 0 {
		t.Fatalf("failure=%d/%q persisted=%d complete=%d, want terminal rejection before snapshot persistence", store.failN, store.failMsg, store.resolvedRevisionSetN, store.completeN)
	}
}

func TestWorkerRejectsUntrustedPersistedHuggingFaceRevision(t *testing.T) {
	message, store := testImportMessage()
	store.importTask.ResolvedRevision = "main"
	objectStore := &workerFakeObjectStore{}
	source := fixtureSource{files: []RemoteFile{{Path: "config.json", Size: 2}}, data: map[string]string{"config.json": "{}"}}
	worker := NewWorker(store, objectStore, map[string]Source{"huggingface": source}, WorkerConfig{WorkerID: "worker-1"})
	if err := worker.Handle(context.Background(), message); err != nil {
		t.Fatalf("Handle() error = %v, want terminal ack", err)
	}
	if store.failN != 1 || !strings.Contains(store.failMsg, "revision") || store.completeN != 0 {
		t.Fatalf("failure = %d/%q complete=%d, want rejected revision", store.failN, store.failMsg, store.completeN)
	}
}

func TestWorkerTreatsResolvedRevisionConflictAsTerminal(t *testing.T) {
	message, store := testImportMessage()
	store.resolveRevisionErr = types.ErrConflict
	objectStore := &workerFakeObjectStore{}
	source := &resolvingWorkerSource{
		fixtureSource: fixtureSource{files: []RemoteFile{{Path: "config.json", Size: 2}}, data: map[string]string{"config.json": "{}"}},
		resolved:      "0123456789abcdef0123456789abcdef01234567",
	}
	worker := NewWorker(store, objectStore, map[string]Source{"huggingface": source}, WorkerConfig{WorkerID: "worker-1"})
	if err := worker.Handle(context.Background(), message); err != nil {
		t.Fatalf("Handle() error = %v, want terminal ack", err)
	}
	if store.failN != 1 || !strings.Contains(store.failMsg, "revision conflict") || store.completeN != 0 {
		t.Fatalf("failure = %d/%q complete=%d, want terminal conflict", store.failN, store.failMsg, store.completeN)
	}
}

func TestWorkerTreatsResolverPolicyFailureAsTerminal(t *testing.T) {
	message, store := testImportMessage()
	objectStore := &workerFakeObjectStore{}
	source := &resolvingWorkerSource{
		fixtureSource: fixtureSource{files: []RemoteFile{{Path: "config.json", Size: 2}}, data: map[string]string{"config.json": "{}"}},
		resolveErr:    ErrSourceRevisionRejected,
	}
	worker := NewWorker(store, objectStore, map[string]Source{"huggingface": source}, WorkerConfig{WorkerID: "worker-1"})
	if err := worker.Handle(context.Background(), message); err != nil {
		t.Fatalf("Handle() error = %v, want terminal ack", err)
	}
	if store.failN != 1 || !strings.Contains(store.failMsg, "revision rejected") || store.completeN != 0 {
		t.Fatalf("failure=%d/%q complete=%d, want terminal resolver policy failure", store.failN, store.failMsg, store.completeN)
	}
}

func TestWorkerTreatsResolverTransportFailureAsRetryable(t *testing.T) {
	message, store := testImportMessage()
	objectStore := &workerFakeObjectStore{}
	source := &resolvingWorkerSource{
		fixtureSource: fixtureSource{files: []RemoteFile{{Path: "config.json", Size: 2}}, data: map[string]string{"config.json": "{}"}},
		resolveErr:    errors.New("transport unavailable"),
	}
	worker := NewWorker(store, objectStore, map[string]Source{"huggingface": source}, WorkerConfig{WorkerID: "worker-1"})
	if err := worker.Handle(context.Background(), message); err == nil || store.failN != 1 || !strings.Contains(store.failMsg, "revision unavailable") || strings.Contains(err.Error(), "transport") {
		t.Fatalf("resolver transport failure err/failure = %v/%q, want redacted retry", err, store.failMsg)
	}
}

type resolvingWorkerSource struct {
	fixtureSource
	resolved     string
	resolveErr   error
	listRevision string
	openRevision string
}

func (s *resolvingWorkerSource) ResolveRevision(context.Context, Repository) (string, error) {
	if s.resolveErr != nil {
		return "", s.resolveErr
	}
	return s.resolved, nil
}

func (s *resolvingWorkerSource) List(ctx context.Context, repository Repository) ([]RemoteFile, error) {
	s.listRevision = repository.Revision
	return s.fixtureSource.List(ctx, repository)
}

func (s *resolvingWorkerSource) Open(ctx context.Context, repository Repository, path string) (io.ReadCloser, int64, error) {
	s.openRevision = repository.Revision
	return s.fixtureSource.Open(ctx, repository, path)
}

func TestWorkerHandleRenewsLeaseWhileArchiveIsBuilding(t *testing.T) {
	message, store := testImportMessage()
	store.importTask.ResolvedRevision = testHuggingFaceCommit
	objectStore := &workerFakeObjectStore{}
	source := &blockingWorkerSource{
		listed:  make(chan struct{}),
		release: make(chan struct{}),
	}
	worker := NewWorker(store, objectStore, map[string]Source{"huggingface": source}, WorkerConfig{
		WorkerID: "worker-1", LeaseDuration: 30 * time.Millisecond,
	})
	done := make(chan error, 1)
	go func() { done <- worker.Handle(context.Background(), message) }()
	<-source.listed
	time.Sleep(25 * time.Millisecond)
	close(source.release)
	if err := <-done; err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.heartbeatN.Load() == 0 {
		t.Fatal("long archive build did not renew its database lease")
	}
}

type blockingWorkerSource struct {
	listed  chan struct{}
	release chan struct{}
}

func (s *blockingWorkerSource) List(ctx context.Context, _ Repository) ([]RemoteFile, error) {
	close(s.listed)
	select {
	case <-s.release:
		return []RemoteFile{{Path: "config.json", Size: 2}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *blockingWorkerSource) Open(context.Context, Repository, string) (io.ReadCloser, int64, error) {
	return io.NopCloser(strings.NewReader("{}")), 2, nil
}

func TestWorkerHandleCompletedDeliveryIsNoOp(t *testing.T) {
	message, store := testImportMessage()
	store.task.Status = "completed"
	objectStore := &workerFakeObjectStore{}
	worker := NewWorker(store, objectStore, map[string]Source{"huggingface": fixtureSource{}}, WorkerConfig{WorkerID: "worker-1"})
	if err := worker.Handle(context.Background(), message); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.acquireN != 0 || store.completeN != 0 || len(objectStore.data) != 0 {
		t.Fatalf("completed delivery performed work: acquire=%d complete=%d data=%d", store.acquireN, store.completeN, len(objectStore.data))
	}
}

func TestWorkerHandleSourceFailureReturnsRetryableErrorAndRecordsRedactedFailure(t *testing.T) {
	message, store := testImportMessage()
	store.importTask.ResolvedRevision = testHuggingFaceCommit
	objectStore := &workerFakeObjectStore{}
	worker := NewWorker(store, objectStore, map[string]Source{"huggingface": workerFailingSource{err: errors.New("secret repository URL")}}, WorkerConfig{WorkerID: "worker-1"})
	err := worker.Handle(context.Background(), message)
	if err == nil || store.failN != 1 || !strings.Contains(store.failMsg, "source") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("source failure err/failure = %v/%q, want redacted retry and failure", err, store.failMsg)
	}
}

func TestWorkerHandleArchiveLimitIsTerminalAndCrossTenantIsRejected(t *testing.T) {
	message, store := testImportMessage()
	store.importTask.ResolvedRevision = testHuggingFaceCommit
	objectStore := &workerFakeObjectStore{}
	tooLarge := fixtureSource{files: []RemoteFile{{Path: "weights.bin", Size: 4}}, data: map[string]string{"weights.bin": "data"}}
	worker := NewWorker(store, objectStore, map[string]Source{"huggingface": tooLarge}, WorkerConfig{WorkerID: "worker-1", Limits: ArchiveLimits{MaxTotalBytes: 3}})
	if err := worker.Handle(context.Background(), message); err != nil {
		t.Fatalf("terminal archive error = %v, want ack after persisted failure", err)
	}
	if store.failN != 1 || !strings.Contains(store.failMsg, "archive") {
		t.Fatalf("archive failure = %d/%q", store.failN, store.failMsg)
	}

	message, store = testImportMessage()
	message.TenantID = uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	worker = NewWorker(store, objectStore, map[string]Source{"huggingface": tooLarge}, WorkerConfig{WorkerID: "worker-1"})
	if err := worker.Handle(context.Background(), message); err != nil {
		t.Fatalf("cross-tenant Handle() error = %v, want dropped message", err)
	}
	if store.acquireN != 0 || store.completeN != 0 || store.failN != 0 {
		t.Fatalf("cross-tenant message touched store: %+v", store)
	}
}

func TestWorkerHandleObjectStoreAndDatabaseFailuresAreRetryable(t *testing.T) {
	message, store := testImportMessage()
	store.importTask.ResolvedRevision = testHuggingFaceCommit
	objectStore := &workerFakeObjectStore{err: errors.New("minio secret endpoint")}
	source := fixtureSource{files: []RemoteFile{{Path: "config.json", Size: 2}}, data: map[string]string{"config.json": "{}"}}
	worker := NewWorker(store, objectStore, map[string]Source{"huggingface": source}, WorkerConfig{WorkerID: "worker-1"})
	if err := worker.Handle(context.Background(), message); err == nil || !strings.Contains(store.failMsg, "object store") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("object-store failure err/failure = %v/%q", err, store.failMsg)
	}

	message, store = testImportMessage()
	store.importTask.ResolvedRevision = testHuggingFaceCommit
	objectStore = &workerFakeObjectStore{}
	store.completeErr = errors.New("postgres password=secret")
	worker = NewWorker(store, objectStore, map[string]Source{"huggingface": source}, WorkerConfig{WorkerID: "worker-1"})
	if err := worker.Handle(context.Background(), message); err == nil || !strings.Contains(store.failMsg, "database") || strings.Contains(err.Error(), "password") {
		t.Fatalf("database failure err/failure = %v/%q", err, store.failMsg)
	}
}
