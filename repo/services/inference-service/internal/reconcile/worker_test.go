package reconcile

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	runtimeport "github.com/kubercloud/ani/services/inference-service/internal/runtime"
)

type workerStore struct {
	mu           sync.Mutex
	operation    domain.Operation
	service      domain.Service
	claimed      bool
	repeatClaims bool
	observations []repository.Observation
	failures     []repository.Failure
	applyErr     error
}

func (*workerStore) FindCreateReplay(context.Context, uuid.UUID, string, uuid.UUID, string) (repository.CreateResult, bool, error) {
	panic("unexpected FindCreateReplay")
}

func (*workerStore) CreateWithOperation(context.Context, domain.Service, domain.Operation) (repository.CreateResult, error) {
	panic("unexpected CreateWithOperation")
}
func (s *workerStore) GetService(context.Context, uuid.UUID, uuid.UUID) (domain.Service, error) {
	return s.service, nil
}
func (*workerStore) GetOperation(context.Context, uuid.UUID, uuid.UUID) (domain.Operation, error) {
	panic("unexpected GetOperation")
}
func (s *workerStore) ClaimOperation(_ context.Context, owner string, now time.Time, duration time.Duration) (domain.Operation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimed && !s.repeatClaims {
		return domain.Operation{}, false, nil
	}
	s.claimed = true
	s.operation.LeaseOwner = owner
	s.operation.LeaseToken = uuid.New()
	until := now.Add(duration)
	s.operation.LeaseUntil = &until
	return s.operation, true, nil
}
func (s *workerStore) ApplyObservation(_ context.Context, observation repository.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observations = append(s.observations, observation)
	return s.applyErr
}
func (s *workerStore) FailOperation(_ context.Context, failure repository.Failure) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, failure)
	return nil
}

type runtimeStub struct {
	mu           sync.Mutex
	observation  runtimeport.Observation
	observed     runtimeport.Observation
	observedSet  bool
	observeErr   error
	ensureErr    error
	healthErr    error
	smokeErr     error
	requests     []runtimeport.EnsureRequest
	lifecycles   []runtimeport.LifecycleRequest
	deletes      []runtimeport.DeleteRequest
	observeCalls int
	healthCalls  int
	smokeCalls   int
}

func (r *runtimeStub) Ensure(_ context.Context, request runtimeport.EnsureRequest) (runtimeport.Observation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
	return r.observation, r.ensureErr
}
func (r *runtimeStub) Observe(context.Context, runtimeport.RuntimeIdentity) (runtimeport.Observation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observeCalls++
	if r.observedSet {
		return r.observed, r.observeErr
	}
	return r.observation, r.observeErr
}
func (r *runtimeStub) ApplyLifecycle(_ context.Context, request runtimeport.LifecycleRequest) (runtimeport.Observation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lifecycles = append(r.lifecycles, request)
	return r.observation, nil
}
func (r *runtimeStub) Delete(_ context.Context, request runtimeport.DeleteRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deletes = append(r.deletes, request)
	return nil
}
func (r *runtimeStub) Health(context.Context, uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.healthCalls++
	return r.healthErr
}
func (r *runtimeStub) Smoke(context.Context, uuid.UUID, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.smokeCalls++
	return r.smokeErr
}
func (r *runtimeStub) Logs(context.Context, runtimeport.LogQuery) (runtimeport.LogPage, error) {
	return runtimeport.LogPage{}, nil
}

func workerFixture() (*workerStore, *runtimeStub) {
	tenantID := uuid.MustParse("50000000-0000-0000-0000-000000000005")
	serviceID := uuid.MustParse("60000000-0000-0000-0000-000000000006")
	operationID := uuid.MustParse("70000000-0000-0000-0000-000000000007")
	store := &workerStore{
		operation: domain.Operation{
			ID: operationID, TenantID: tenantID, ServiceID: serviceID,
			Type: domain.ActionCreate, State: domain.OperationPending, TargetGeneration: 1,
			TargetSpec: domain.Spec{Replicas: 1},
		},
		service: domain.Service{
			ID: serviceID, TenantID: tenantID, Name: "qwen-chat", ServedModelName: "qwen-chat",
			Status: domain.StatusPending, Generation: 1, DesiredSpec: domain.Spec{Replicas: 1},
			ActiveOperationID: operationID,
		},
	}
	runtime := &runtimeStub{observation: runtimeport.Observation{
		RuntimeRef:      uuid.MustParse("80000000-0000-0000-0000-000000000008"),
		RuntimeEndpoint: "http://inference.internal.svc:8000", ReadyReplicas: 1, Ready: true,
	}}
	return store, runtime
}

func TestRunOncePersistsRuntimeBeforeHealthAndOnlyThenMarksRunning(t *testing.T) {
	store, runtime := workerFixture()
	worker := NewWorker(store, runtime, "worker-a", func() time.Time { return time.Unix(100, 0).UTC() })

	handled, err := worker.RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", handled, err)
	}
	if len(store.observations) != 3 {
		t.Fatalf("observations = %d, want accepted ref + observed endpoint + running", len(store.observations))
	}
	first, last := store.observations[0], store.observations[len(store.observations)-1]
	if first.Status != domain.StatusDeploying || first.Complete || first.RuntimeRef == uuid.Nil {
		t.Fatalf("runtime reference was not persisted first: %+v", first)
	}
	if last.Status != domain.StatusRunning || !last.Complete {
		t.Fatalf("final running observation missing: %+v", last)
	}
	if runtime.healthCalls != 1 || runtime.smokeCalls != 1 {
		t.Fatalf("health/smoke calls = %d/%d, want 1/1", runtime.healthCalls, runtime.smokeCalls)
	}
	if len(runtime.requests) != 1 || runtime.requests[0].IdempotencyKey == uuid.Nil {
		t.Fatalf("runtime request lacks deterministic key: %+v", runtime.requests)
	}
}

func TestRunOnceSuppressesStaleGenerationCallback(t *testing.T) {
	store, runtime := workerFixture()
	store.applyErr = repository.ErrStaleGeneration
	worker := NewWorker(store, runtime, "worker-a", time.Now)

	handled, err := worker.RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("stale callback must be safely consumed, got (%v, %v)", handled, err)
	}
	if runtime.healthCalls != 0 || runtime.smokeCalls != 0 {
		t.Fatal("stale generation must not continue health or smoke")
	}
}

func TestRunOnceRetryKeepsGenerationAndRuntimeIdempotencyKey(t *testing.T) {
	store, runtime := workerFixture()
	store.repeatClaims = true
	runtime.ensureErr = errors.New("core temporarily unavailable")
	now := time.Unix(200, 0).UTC()
	worker := NewWorker(store, runtime, "worker-a", func() time.Time { return now })

	for i := 0; i < 2; i++ {
		handled, err := worker.RunOnce(context.Background())
		if err != nil || !handled {
			t.Fatalf("retry RunOnce() = (%v, %v)", handled, err)
		}
	}
	if len(runtime.requests) != 2 || runtime.requests[0].IdempotencyKey != runtime.requests[1].IdempotencyKey {
		t.Fatalf("runtime retry changed idempotency key: %+v", runtime.requests)
	}
	if len(store.failures) != 2 {
		t.Fatalf("failures = %d, want 2 retry schedules", len(store.failures))
	}
	for _, failure := range store.failures {
		if failure.TargetGeneration != store.operation.TargetGeneration || failure.RetryAt == nil {
			t.Fatalf("retry changed generation or became terminal: %+v", failure)
		}
	}
}

func TestConcurrentWorkersOnlyLeaseOneOperation(t *testing.T) {
	store, runtime := workerFixture()
	workerA := NewWorker(store, runtime, "worker-a", time.Now)
	workerB := NewWorker(store, runtime, "worker-b", time.Now)
	var wg sync.WaitGroup
	wg.Add(2)
	for _, worker := range []*Worker{workerA, workerB} {
		go func(worker *Worker) {
			defer wg.Done()
			_, _ = worker.RunOnce(context.Background())
		}(worker)
	}
	wg.Wait()
	if len(runtime.requests) != 1 {
		t.Fatalf("runtime side effects = %d, want one lease winner", len(runtime.requests))
	}
}

func TestWorkerPersistsRuntimeRefWhenEnsureReturnsRefAndError(t *testing.T) {
	store, runtime := workerFixture()
	runtime.ensureErr = errors.New("observe after create failed")
	worker := NewWorker(store, runtime, "worker-a", func() time.Time { return time.Unix(300, 0).UTC() })

	handled, err := worker.RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v, %v)", handled, err)
	}
	if len(store.observations) != 1 || store.observations[0].RuntimeRef == uuid.Nil || store.observations[0].Status != domain.StatusDeploying {
		t.Fatalf("runtime ref must be persisted before Ensure error retry: %+v", store.observations)
	}
	if len(store.failures) != 1 || store.failures[0].ErrorCode != "RUNTIME_MUTATION_FAILED" || store.failures[0].RetryAt == nil {
		t.Fatalf("Ensure error must retry after persisting ref: %+v", store.failures)
	}
}

func TestWorkerUnsupportedAcceleratorIsTerminal(t *testing.T) {
	store, runtime := workerFixture()
	runtime.ensureErr = runtimeport.ErrRuntimeUnsupported
	worker := NewWorker(store, runtime, "worker-gpu", time.Now)
	handled, err := worker.RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v, %v)", handled, err)
	}
	if len(store.failures) != 1 || store.failures[0].ErrorCode != "ACCELERATOR_SPEC_UNAVAILABLE" || store.failures[0].RetryAt != nil {
		t.Fatalf("failures = %+v", store.failures)
	}
}

func TestWorkerCreateRetryAfterRuntimeRefDoesNotEnsureAgain(t *testing.T) {
	store, runtime := workerFixture()
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.Status = domain.StatusDeploying
	worker := NewWorker(store, runtime, "worker-a", time.Now)

	handled, err := worker.RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v, %v)", handled, err)
	}
	if len(runtime.requests) != 0 {
		t.Fatalf("create retry Ensure() calls = %d, want 0", len(runtime.requests))
	}
	if runtime.observeCalls == 0 || runtime.healthCalls != 1 || runtime.smokeCalls != 1 {
		t.Fatalf("observe/health/smoke = %d/%d/%d", runtime.observeCalls, runtime.healthCalls, runtime.smokeCalls)
	}
	if store.observations[len(store.observations)-1].Status != domain.StatusRunning {
		t.Fatalf("final observation = %+v", store.observations[len(store.observations)-1])
	}
}

func TestWorkerScaleWaitsForTargetReadyReplicas(t *testing.T) {
	store, runtime := workerFixture()
	store.operation.Type = domain.ActionScale
	store.operation.TargetGeneration = 2
	store.operation.TargetSpec.Replicas = 2
	store.service.Status = domain.StatusDeploying
	store.service.Generation = 2
	store.service.ActiveOperationID = store.operation.ID
	runtime.observation.ReadyReplicas = 1
	runtime.observation.Ready = false

	worker := NewWorker(store, runtime, "worker-scale", time.Now)
	handled, err := worker.RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("scale RunOnce() = (%v,%v)", handled, err)
	}
	if len(store.observations) != 2 || store.observations[0].Complete || store.observations[1].Complete {
		t.Fatalf("scale completed before target replicas: %+v", store.observations)
	}
	if len(store.failures) != 1 || store.failures[0].RetryAt == nil {
		t.Fatalf("scale did not schedule retry: %+v", store.failures)
	}
}

func TestWorkerUsesObservedStateAfterMutationAcceptance(t *testing.T) {
	store, runtime := workerFixture()
	runtime.observedSet = true
	runtime.observed = runtimeport.Observation{
		RuntimeRef: runtime.observation.RuntimeRef, RuntimeEndpoint: runtime.observation.RuntimeEndpoint,
		ReadyReplicas: 0, Ready: false,
	}

	worker := NewWorker(store, runtime, "worker-observe", time.Now)
	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("observed pending RunOnce() = (%v,%v)", handled, err)
	}
	if runtime.observeCalls != 1 {
		t.Fatalf("observe calls = %d", runtime.observeCalls)
	}
	if len(store.observations) != 2 || store.observations[0].Complete || store.observations[1].Complete {
		t.Fatalf("mutation acceptance completed operation: %+v", store.observations)
	}
	if len(store.failures) != 1 || store.failures[0].RetryAt == nil {
		t.Fatalf("pending observation did not schedule retry: %+v", store.failures)
	}
}

func TestWorkerStopClearsEndpointButRetainsRuntimeRef(t *testing.T) {
	store, runtime := workerFixture()
	runtimeRef := runtime.observation.RuntimeRef
	store.operation.Type = domain.ActionStop
	store.operation.TargetGeneration = 2
	store.service.Status = domain.StatusStopping
	store.service.Generation = 2
	store.service.RuntimeRef = runtimeRef
	store.service.RuntimeEndpoint = "http://old.internal.svc:8000"
	store.service.ActiveOperationID = store.operation.ID
	runtime.observation = runtimeport.Observation{RuntimeRef: runtimeRef}

	worker := NewWorker(store, runtime, "worker-stop", time.Now)
	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("stop RunOnce() = (%v,%v)", handled, err)
	}
	if len(runtime.lifecycles) != 1 || runtime.lifecycles[0].Action != domain.ActionStop {
		t.Fatalf("stop lifecycle calls = %+v", runtime.lifecycles)
	}
	if len(store.observations) != 1 {
		t.Fatalf("stop observations = %+v", store.observations)
	}
	observation := store.observations[0]
	if observation.Status != domain.StatusStopped || !observation.Complete || observation.RuntimeRef != runtimeRef ||
		observation.RuntimeEndpoint != "" || observation.ReadyReplicas != 0 {
		t.Fatalf("stop final observation = %+v", observation)
	}
}

func TestWorkerStartRestoresStoppedRuntime(t *testing.T) {
	store, runtime := workerFixture()
	runtimeRef := runtime.observation.RuntimeRef
	store.operation.Type = domain.ActionStart
	store.operation.TargetGeneration = 2
	store.service.Status = domain.StatusDeploying
	store.service.Generation = 2
	store.service.RuntimeRef = runtimeRef
	store.service.RuntimeEndpoint = ""
	store.service.ActiveOperationID = store.operation.ID

	worker := NewWorker(store, runtime, "worker-start", time.Now)
	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("start RunOnce() = (%v,%v)", handled, err)
	}
	if len(runtime.lifecycles) != 1 || runtime.lifecycles[0].Action != domain.ActionStart {
		t.Fatalf("start lifecycle calls = %+v", runtime.lifecycles)
	}
	if len(store.observations) != 2 || store.observations[1].Status != domain.StatusRunning || !store.observations[1].Complete {
		t.Fatalf("start observations = %+v", store.observations)
	}
}

func TestWorkerRestartUsesLifecycleIdempotencyKey(t *testing.T) {
	store, runtime := workerFixture()
	store.operation.Type = domain.ActionRestart
	store.operation.TargetGeneration = 2
	store.service.Status = domain.StatusDeploying
	store.service.Generation = 2
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID

	worker := NewWorker(store, runtime, "worker-restart", time.Now)
	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("restart RunOnce() = (%v,%v)", handled, err)
	}
	if len(runtime.lifecycles) != 1 || runtime.lifecycles[0].Action != domain.ActionRestart || runtime.lifecycles[0].IdempotencyKey == uuid.Nil {
		t.Fatalf("restart lifecycle request = %+v", runtime.lifecycles)
	}
}

func TestWorkerDeleteTombstonesOnlyAfterRuntimeDeletion(t *testing.T) {
	store, runtime := workerFixture()
	store.operation.Type = domain.ActionDelete
	store.operation.TargetGeneration = 2
	store.service.Status = domain.StatusStopping
	store.service.Generation = 2
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID
	runtime.observeErr = runtimeport.ErrRuntimeNotFound

	worker := NewWorker(store, runtime, "worker-delete", time.Now)
	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("delete RunOnce() = (%v,%v)", handled, err)
	}
	if len(runtime.deletes) != 1 {
		t.Fatalf("delete calls = %+v", runtime.deletes)
	}
	if len(store.observations) != 1 || !store.observations[0].Deleted || !store.observations[0].Complete {
		t.Fatalf("delete final observation = %+v", store.observations)
	}
}

func TestWorkerDeleteWaitsWhileRuntimeIsStillObservable(t *testing.T) {
	store, runtime := workerFixture()
	store.operation.Type = domain.ActionDelete
	store.operation.TargetGeneration = 2
	store.service.Status = domain.StatusStopping
	store.service.Generation = 2
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID

	worker := NewWorker(store, runtime, "worker-delete-wait", time.Now)
	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("delete pending RunOnce() = (%v,%v)", handled, err)
	}
	if runtime.observeCalls != 1 {
		t.Fatalf("delete observe calls = %d", runtime.observeCalls)
	}
	if len(store.observations) != 0 {
		t.Fatalf("delete tombstoned observable runtime: %+v", store.observations)
	}
	if len(store.failures) != 1 || store.failures[0].RetryAt == nil {
		t.Fatalf("delete pending did not schedule retry: %+v", store.failures)
	}
}

func TestPreemptedCreateCannotRestoreRuntimeAfterStop(t *testing.T) {
	store, runtime := workerFixture()
	store.applyErr = repository.ErrStaleGeneration
	worker := NewWorker(store, runtime, "old-create-worker", time.Now)

	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("preempted create RunOnce() = (%v,%v)", handled, err)
	}
	if len(store.observations) != 1 || store.observations[0].Status != domain.StatusDeploying {
		t.Fatalf("preempted create advanced after fenced observation: %+v", store.observations)
	}
}
