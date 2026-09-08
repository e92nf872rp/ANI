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
	mu                sync.Mutex
	operation         domain.Operation
	service           domain.Service
	claimed           bool
	repeatClaims      bool
	observations      []repository.Observation
	failures          []repository.Failure
	applyErr          error
	withdrawn         bool
	withdrawErr       error
	withdrawals       int
	claimAt           time.Time
	withdrawnButStale bool
	getServiceCalls   int
	getServiceErrAt   int
}

func (*workerStore) FindCreateReplay(context.Context, uuid.UUID, string, uuid.UUID, string) (repository.CreateResult, bool, error) {
	panic("unexpected FindCreateReplay")
}

func (*workerStore) CreateWithOperation(context.Context, domain.Service, domain.Operation) (repository.CreateResult, error) {
	panic("unexpected CreateWithOperation")
}
func (*workerStore) BindRuntimeRef(context.Context, repository.RuntimeBinding) error {
	panic("unexpected BindRuntimeRef")
}
func (*workerStore) AbortCreate(context.Context, repository.RuntimeBinding) error {
	panic("unexpected AbortCreate")
}
func (s *workerStore) PublicationWithdrawn(_ context.Context, tenantID, serviceID uuid.UUID, generation int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.withdrawals++
	if tenantID != s.operation.TenantID || serviceID != s.operation.ServiceID || generation != s.operation.TargetGeneration {
		return false, errors.New("unexpected publication withdrawal identity")
	}
	if s.withdrawn && !s.withdrawnButStale && !publicationWithdrawalMatches(s.service, s.operation) {
		s.service.Publication = domain.Publication{
			Desired: domain.PublicationUnpublished, Generation: generation,
			ObservedGeneration: generation, Phase: domain.PublicationUnpublishedOK,
			UpdatedAt: s.claimAt,
		}
		s.service.InvocationURL = ""
	}
	return s.withdrawn, s.withdrawErr
}

func (s *workerStore) GetService(context.Context, uuid.UUID, uuid.UUID) (domain.Service, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getServiceCalls++
	if s.getServiceErrAt == s.getServiceCalls {
		return domain.Service{}, errors.New("service lookup temporarily unavailable")
	}
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
	s.claimAt = now.UTC()
	until := now.Add(duration)
	s.operation.LeaseUntil = &until
	return s.operation, true, nil
}
func (s *workerStore) ApplyObservation(_ context.Context, observation repository.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observations = append(s.observations, observation)
	if s.applyErr != nil {
		return s.applyErr
	}
	s.service.Status = observation.Status
	if observation.RuntimeRef != uuid.Nil {
		s.service.RuntimeRef = observation.RuntimeRef
	}
	s.service.RuntimeEndpoint = observation.RuntimeEndpoint
	s.service.ReadyReplicas = observation.ReadyReplicas
	if observation.Complete {
		s.service.ObservedGeneration = observation.TargetGeneration
		s.service.AppliedSpec = observation.AppliedSpec
		s.service.ActiveOperationID = uuid.Nil
		s.service.ActiveOperation = ""
	}
	return nil
}
func (s *workerStore) FailOperation(_ context.Context, failure repository.Failure) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, failure)
	if failure.ErrorCode != codeGatewayUnpublishPending && failure.ErrorCode != codeGatewayUnpublishCheck {
		s.operation.Attempt++
	}
	s.operation.ErrorCode = failure.ErrorCode
	s.operation.ErrorMessage = failure.ErrorMessage
	switch {
	case failure.DeadLetter:
		s.operation.State = domain.OperationDeadLetter
		s.service.Status = domain.StatusFailed
		s.service.StatusReason = failure.ErrorCode
		s.service.RuntimeEndpoint = ""
		s.service.ActiveOperationID = uuid.Nil
		s.service.ActiveOperation = ""
	case failure.RetryAt == nil:
		s.operation.State = domain.OperationFailed
		s.service.Status = domain.StatusFailed
		s.service.StatusReason = failure.ErrorCode
		s.service.RuntimeEndpoint = ""
		s.service.ActiveOperationID = uuid.Nil
		s.service.ActiveOperation = ""
	default:
		s.operation.State = domain.OperationPending
		s.claimed = false
	}
	return nil
}

func (s *workerStore) BeginScaleRollback(_ context.Context, request repository.ScaleRollback) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.service.DesiredState == domain.DesiredStateDeleted {
		return 0, domain.ErrDeleted
	}
	if s.operation.RollbackGeneration != 0 {
		return s.operation.RollbackGeneration, nil
	}
	if s.service.Generation != request.TargetGeneration || s.service.ActiveOperationID != request.OperationID {
		return 0, repository.ErrStaleGeneration
	}
	s.service.DesiredSpec = s.service.AppliedSpec
	s.service.Generation++
	s.service.Status = domain.StatusDeploying
	s.service.StatusReason = "SCALE_ROLLING_BACK"
	s.operation.RollbackGeneration = s.service.Generation
	s.operation.ErrorCode = "SCALE_ROLLING_BACK"
	return s.operation.RollbackGeneration, nil
}

func (s *workerStore) FinishScaleRollback(_ context.Context, finish repository.ScaleRollbackFinish) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if finish.Success {
		s.service.Status = domain.StatusRunning
		s.service.AppliedSpec = finish.AppliedSpec
		s.service.DesiredSpec = finish.AppliedSpec
		s.service.ObservedGeneration = s.service.Generation
		s.service.RuntimeRef = finish.RuntimeRef
		s.service.RuntimeEndpoint = finish.RuntimeEndpoint
		s.service.ReadyReplicas = finish.ReadyReplicas
		s.service.StatusReason = codeScaleRolledBack
		s.operation.State = domain.OperationFailed
		s.operation.ErrorCode = codeScaleRolledBack
	} else {
		s.service.Status = domain.StatusFailed
		s.service.StatusReason = codeRollbackFailed
		s.service.RuntimeEndpoint = ""
		s.service.ReadyReplicas = 0
		s.operation.State = domain.OperationFailed
		s.operation.ErrorCode = codeRollbackFailed
	}
	s.service.ActiveOperationID = uuid.Nil
	s.service.ActiveOperation = ""
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
	smokeTasks   []domain.InferenceTask
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
func (r *runtimeStub) Health(context.Context, uuid.UUID, uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.healthCalls++
	return r.healthErr
}
func (r *runtimeStub) Smoke(_ context.Context, _, _ uuid.UUID, _ string, task domain.InferenceTask) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.smokeCalls++
	r.smokeTasks = append(r.smokeTasks, task)
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
		withdrawn: true,
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
	if !last.Publish {
		t.Fatalf("successful create did not request publication: %+v", last)
	}
	for i, observation := range store.observations[:len(store.observations)-1] {
		if observation.Publish {
			t.Fatalf("partial observation %d requested publication: %+v", i, observation)
		}
	}
	if runtime.healthCalls != 1 || runtime.smokeCalls != 1 {
		t.Fatalf("health/smoke calls = %d/%d, want 1/1", runtime.healthCalls, runtime.smokeCalls)
	}
	if len(runtime.requests) != 1 || runtime.requests[0].IdempotencyKey == uuid.Nil {
		t.Fatalf("runtime request lacks deterministic key: %+v", runtime.requests)
	}
}

func TestWorkerDoesNotPublishBeforeHealthAndSmokeSucceed(t *testing.T) {
	store, runtime := workerFixture()
	runtime.smokeErr = errors.New("task smoke failed")

	handled, err := NewWorker(store, runtime, "worker-smoke", time.Now).RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v,%v)", handled, err)
	}
	for _, observation := range store.observations {
		if observation.Publish {
			t.Fatalf("failed smoke requested publication: %+v", observation)
		}
	}
	if len(store.failures) != 1 || store.failures[0].ErrorCode != "RUNTIME_SMOKE_FAILED" {
		t.Fatalf("smoke failure = %+v", store.failures)
	}
}

func TestWorkerEmbeddingTaskPropagatesToSmoke(t *testing.T) {
	store, runtime := workerFixture()
	store.operation.TargetSpec.ExecutionProfile.Task = domain.InferenceTaskEmbed
	worker := NewWorker(store, runtime, "worker-embedding", func() time.Time { return time.Unix(100, 0).UTC() })

	handled, err := worker.RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", handled, err)
	}
	if len(runtime.smokeTasks) != 1 || runtime.smokeTasks[0] != domain.InferenceTaskEmbed {
		t.Fatalf("smoke tasks = %v, want [embed]", runtime.smokeTasks)
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
	runtime.observation.Ready = false
	runtime.observation.ReadyReplicas = 0
	now := time.Unix(200, 0).UTC()
	worker := NewWorker(store, runtime, "worker-a", func() time.Time { return now })

	for i := 0; i < 2; i++ {
		handled, err := worker.RunOnce(context.Background())
		if err != nil || !handled {
			t.Fatalf("retry RunOnce() = (%v, %v)", handled, err)
		}
	}
	if len(runtime.requests) != 1 || runtime.requests[0].IdempotencyKey == uuid.Nil {
		t.Fatalf("create retry must reuse the first Ensure key and then only observe: %+v", runtime.requests)
	}
	if runtime.observeCalls == 0 {
		t.Fatal("create retry after runtime_ref must Observe instead of Ensure")
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
	if len(store.failures) != 1 || store.failures[0].ErrorCode != codeAcceleratorUnavailable || store.failures[0].RetryAt != nil || store.failures[0].DeadLetter {
		t.Fatalf("failures = %+v", store.failures)
	}
	if len(runtime.deletes) != 1 {
		t.Fatalf("permanent create must delete provider runtime: %+v", runtime.deletes)
	}
}

func TestWorkerWaitsForRequestPathEnsureOnFreshCreate(t *testing.T) {
	store, runtime := workerFixture()
	created := time.Unix(100, 0).UTC()
	store.operation.CreatedAt = created
	worker := NewWorker(store, runtime, "worker-a", func() time.Time { return created.Add(time.Second) })

	handled, err := worker.RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v, %v)", handled, err)
	}
	if len(runtime.requests) != 0 {
		t.Fatalf("fresh create must not Ensure: %+v", runtime.requests)
	}
	if runtime.observeCalls != 0 || runtime.healthCalls != 0 {
		t.Fatal("fresh create must not Observe or Health")
	}
	if len(store.failures) != 1 || store.failures[0].ErrorCode != codeRuntimeNotBound || store.failures[0].RetryAt == nil {
		t.Fatalf("failures = %+v, want RUNTIME_NOT_BOUND retry", store.failures)
	}
}

func TestWorkerRecoversUnboundCreateAfterRequestPathGrace(t *testing.T) {
	store, runtime := workerFixture()
	created := time.Unix(100, 0).UTC()
	store.operation.CreatedAt = created
	worker := NewWorker(store, runtime, "worker-a", func() time.Time { return created.Add(requestPathEnsureGrace + time.Second) })

	handled, err := worker.RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v, %v)", handled, err)
	}
	if len(runtime.requests) != 1 {
		t.Fatalf("expired grace must Ensure once: %+v", runtime.requests)
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

func TestWorkerScaleCompletionDoesNotRequestPublication(t *testing.T) {
	store, runtime := workerFixture()
	store.operation.Type = domain.ActionScale
	store.operation.TargetGeneration = 2
	store.operation.TargetSpec.Replicas = 2
	store.service.Status = domain.StatusDeploying
	store.service.Generation = 2
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID
	runtime.observation.ReadyReplicas = 2

	handled, err := NewWorker(store, runtime, "worker-scale-publish", time.Now).RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v,%v)", handled, err)
	}
	last := store.observations[len(store.observations)-1]
	if !last.Complete || last.Status != domain.StatusRunning || last.Publish {
		t.Fatalf("scale completion publication = %+v", last)
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

func TestWorkerWaitsForPublicationWithdrawalBeforeRuntimeMutation(t *testing.T) {
	for _, action := range []domain.Action{domain.ActionStop, domain.ActionRestart, domain.ActionDelete} {
		t.Run(string(action), func(t *testing.T) {
			store, runtime := workerFixture()
			store.withdrawn = false
			store.operation.Type = action
			store.operation.TargetGeneration = 2
			store.service.Generation = 2
			store.service.RuntimeRef = runtime.observation.RuntimeRef
			store.service.ActiveOperationID = store.operation.ID
			if action == domain.ActionStop || action == domain.ActionDelete {
				store.service.Status = domain.StatusStopping
			} else {
				store.service.Status = domain.StatusDeploying
			}

			handled, err := NewWorker(store, runtime, "worker-withdraw", time.Now).RunOnce(context.Background())
			if err != nil || !handled {
				t.Fatalf("RunOnce() = (%v,%v)", handled, err)
			}
			if store.withdrawals != 1 {
				t.Fatalf("withdrawal checks = %d, want 1", store.withdrawals)
			}
			if len(runtime.requests) != 0 || len(runtime.lifecycles) != 0 || len(runtime.deletes) != 0 {
				t.Fatalf("runtime mutated before withdrawal: ensure=%d lifecycle=%d delete=%d",
					len(runtime.requests), len(runtime.lifecycles), len(runtime.deletes))
			}
			if len(store.failures) != 1 || store.failures[0].ErrorCode != "GATEWAY_UNPUBLISH_PENDING" || store.failures[0].RetryAt == nil {
				t.Fatalf("withdrawal pending failure = %+v", store.failures)
			}
		})
	}
}

func TestWorkerWithdrawalCheckFailureFailsClosed(t *testing.T) {
	store, runtime := workerFixture()
	store.withdrawErr = errors.New("publisher state unavailable")
	store.operation.Type = domain.ActionStop
	store.operation.TargetGeneration = 2
	store.service.Generation = 2
	store.service.Status = domain.StatusStopping
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID

	handled, err := NewWorker(store, runtime, "worker-withdraw-error", time.Now).RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v,%v)", handled, err)
	}
	if len(runtime.lifecycles) != 0 || len(runtime.deletes) != 0 {
		t.Fatal("runtime mutated when withdrawal check failed")
	}
	if len(store.failures) != 1 || store.failures[0].ErrorCode != "GATEWAY_UNPUBLISH_CHECK_FAILED" || store.failures[0].RetryAt == nil {
		t.Fatalf("withdrawal check failure = %+v", store.failures)
	}
}

func TestWorkerRunsRuntimeWhenWithdrawalRecoversOnFinalRetry(t *testing.T) {
	store, runtime := workerFixture()
	store.operation.Type = domain.ActionStop
	store.operation.TargetGeneration = 2
	store.operation.Attempt = 1
	store.operation.ErrorCode = "GATEWAY_UNPUBLISH_PENDING"
	store.service.Generation = 2
	store.service.Status = domain.StatusStopping
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID
	runtime.observation = runtimeport.Observation{RuntimeRef: store.service.RuntimeRef}
	worker := NewWorker(store, runtime, "worker-withdraw-recovered", time.Now)
	worker.maxAttempts = 2

	handled, err := worker.RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v,%v)", handled, err)
	}
	if len(runtime.lifecycles) != 1 || runtime.lifecycles[0].Action != domain.ActionStop {
		t.Fatalf("recovered withdrawal did not permit runtime stop: %+v", runtime.lifecycles)
	}
}

func TestWorkerKeepsTimedOutWithdrawalRetryableWithoutTouchingRuntime(t *testing.T) {
	createdAt := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	store, runtime := workerFixture()
	store.withdrawn = false
	store.operation.Type = domain.ActionDelete
	store.operation.TargetGeneration = 2
	store.operation.CreatedAt = createdAt
	store.service.Generation = 2
	store.service.Status = domain.StatusStopping
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID
	worker := NewWorker(store, runtime, "worker-withdraw-retryable", func() time.Time { return createdAt.Add(defaultDeployTimeout) })

	handled, err := worker.RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v,%v)", handled, err)
	}
	if len(runtime.deletes) != 0 || len(runtime.lifecycles) != 0 {
		t.Fatal("timed-out withdrawal retry touched runtime")
	}
	if len(store.failures) != 1 || store.failures[0].RetryAt == nil || store.failures[0].ErrorCode != "GATEWAY_UNPUBLISH_PENDING" {
		t.Fatalf("retryable withdrawal failure = %+v", store.failures)
	}
	if store.operation.State != domain.OperationPending || store.service.Status == domain.StatusFailed {
		t.Fatalf("withdrawal timeout became terminal: operation=%s service=%s", store.operation.State, store.service.Status)
	}
}

func TestWorkerWithdrawalRetryDoesNotConsumeRuntimeAttemptBudget(t *testing.T) {
	store, runtime := workerFixture()
	store.withdrawn = false
	store.operation.Type = domain.ActionStop
	store.operation.TargetGeneration = 2
	store.service.Generation = 2
	store.service.Status = domain.StatusStopping
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID

	handled, err := NewWorker(store, runtime, "worker-withdraw-budget", time.Now).RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v,%v)", handled, err)
	}
	if store.operation.Attempt != 0 {
		t.Fatalf("withdrawal retry consumed runtime attempt budget: %d", store.operation.Attempt)
	}
}

func TestWorkerRuntimeTimeoutStartsAtConfirmedWithdrawal(t *testing.T) {
	now := time.Date(2026, 8, 31, 14, 0, 0, 0, time.UTC)
	store, runtime := workerFixture()
	store.operation.Type = domain.ActionStop
	store.operation.TargetGeneration = 2
	store.operation.CreatedAt = now.Add(-20 * time.Minute)
	store.operation.ErrorCode = codeGatewayUnpublishPending
	store.service.Generation = 2
	store.service.Status = domain.StatusStopping
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID
	store.service.Publication = domain.Publication{
		Desired: domain.PublicationUnpublished, Generation: 2, ObservedGeneration: 2,
		Phase: domain.PublicationUnpublishedOK, UpdatedAt: now.Add(-time.Second),
	}
	runtime.observation = runtimeport.Observation{
		RuntimeRef: store.service.RuntimeRef, RuntimeEndpoint: "http://still-running.internal.svc:8000",
		ReadyReplicas: 1, Ready: true,
	}

	handled, err := NewWorker(store, runtime, "worker-runtime-budget", func() time.Time { return now }).RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v,%v)", handled, err)
	}
	if len(runtime.lifecycles) != 1 {
		t.Fatalf("runtime stop was not attempted after withdrawal: %+v", runtime.lifecycles)
	}
	if len(store.failures) != 1 || store.failures[0].ErrorCode != "RUNTIME_STOP_PENDING" || store.failures[0].RetryAt == nil {
		t.Fatalf("first runtime failure did not receive a fresh timeout budget: %+v", store.failures)
	}
}

func TestWorkerRejectsStaleWithdrawalSnapshotBeforeRuntimeMutation(t *testing.T) {
	store, runtime := workerFixture()
	store.operation.Type = domain.ActionDelete
	store.operation.TargetGeneration = 2
	store.service.Generation = 2
	store.service.Status = domain.StatusStopping
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID
	store.withdrawnButStale = true

	handled, err := NewWorker(store, runtime, "worker-stale-withdrawal", time.Now).RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v,%v)", handled, err)
	}
	if len(runtime.deletes) != 0 || len(runtime.lifecycles) != 0 || len(runtime.requests) != 0 {
		t.Fatalf("stale withdrawal snapshot touched runtime: delete=%d lifecycle=%d ensure=%d",
			len(runtime.deletes), len(runtime.lifecycles), len(runtime.requests))
	}
	if len(store.failures) != 1 || store.failures[0].ErrorCode != codeGatewayUnpublishPending || store.failures[0].RetryAt == nil {
		t.Fatalf("stale withdrawal failure = %+v", store.failures)
	}
}

func TestWorkerRuntimeRetriesUseNormalBudgetAfterWithdrawal(t *testing.T) {
	now := time.Date(2026, 8, 31, 14, 0, 0, 0, time.UTC)
	store, runtime := workerFixture()
	store.operation.Type = domain.ActionStop
	store.operation.TargetGeneration = 2
	store.operation.CreatedAt = now.Add(-14 * time.Minute)
	store.operation.ErrorCode = codeGatewayUnpublishPending
	store.service.Generation = 2
	store.service.Status = domain.StatusStopping
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID
	store.service.Publication = domain.Publication{
		Desired: domain.PublicationUnpublished, Generation: 2, ObservedGeneration: 2,
		Phase: domain.PublicationUnpublishedOK, UpdatedAt: now.Add(-time.Second),
	}
	runtime.observation = runtimeport.Observation{
		RuntimeRef: store.service.RuntimeRef, RuntimeEndpoint: "http://still-running.internal.svc:8000",
		ReadyReplicas: 1, Ready: true,
	}
	worker := NewWorker(store, runtime, "worker-runtime-retries", func() time.Time { return now })
	worker.maxAttempts = 2

	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("first RunOnce() = (%v,%v)", handled, err)
	}
	if len(store.failures) != 1 || store.failures[0].RetryAt == nil || store.operation.Attempt != 1 {
		t.Fatalf("first runtime retry = failures=%+v attempt=%d", store.failures, store.operation.Attempt)
	}
	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("second RunOnce() = (%v,%v)", handled, err)
	}
	if len(store.failures) != 2 || !store.failures[1].DeadLetter || store.failures[1].ErrorCode != "RUNTIME_STOP_PENDING" {
		t.Fatalf("runtime retry budget was not exhausted normally: %+v", store.failures)
	}
}

func TestWorkerWithdrawalCheckErrorUsesCompletedWithdrawalTimeoutBudget(t *testing.T) {
	now := time.Date(2026, 8, 31, 15, 0, 0, 0, time.UTC)
	store, runtime := workerFixture()
	store.withdrawn = false
	store.withdrawErr = errors.New("publisher lookup temporarily unavailable")
	store.operation.Type = domain.ActionStop
	store.operation.TargetGeneration = 2
	store.operation.CreatedAt = now.Add(-20 * time.Minute)
	store.operation.Attempt = 1
	store.operation.ErrorCode = "RUNTIME_STOP_PENDING"
	store.service.Generation = 2
	store.service.Status = domain.StatusStopping
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID
	store.service.Publication = domain.Publication{
		Desired: domain.PublicationUnpublished, Generation: 2, ObservedGeneration: 2,
		Phase: domain.PublicationUnpublishedOK, UpdatedAt: now.Add(-time.Minute),
	}

	handled, err := NewWorker(store, runtime, "worker-withdraw-check-budget", func() time.Time { return now }).RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v,%v)", handled, err)
	}
	if len(store.failures) != 1 || store.failures[0].ErrorCode != codeGatewayUnpublishCheck || store.failures[0].RetryAt == nil {
		t.Fatalf("withdrawal check error used the old operation deadline: %+v", store.failures)
	}
	if store.operation.Attempt != 1 {
		t.Fatalf("withdrawal check error changed runtime attempt budget: %d", store.operation.Attempt)
	}
}

func TestWorkerWithdrawalFalseUsesCompletedWithdrawalTimeoutBudget(t *testing.T) {
	now := time.Date(2026, 8, 31, 15, 0, 0, 0, time.UTC)
	store, runtime := workerFixture()
	store.withdrawn = false
	store.operation.Type = domain.ActionDelete
	store.operation.TargetGeneration = 2
	store.operation.CreatedAt = now.Add(-20 * time.Minute)
	store.operation.Attempt = 1
	store.operation.ErrorCode = "RUNTIME_DELETE_PENDING"
	store.service.Generation = 2
	store.service.Status = domain.StatusStopping
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID
	store.service.Publication = domain.Publication{
		Desired: domain.PublicationUnpublished, Generation: 2, ObservedGeneration: 2,
		Phase: domain.PublicationUnpublishedOK, UpdatedAt: now.Add(-time.Minute),
	}

	handled, err := NewWorker(store, runtime, "worker-withdraw-false-budget", func() time.Time { return now }).RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v,%v)", handled, err)
	}
	if len(store.failures) != 1 || store.failures[0].ErrorCode != codeGatewayUnpublishPending || store.failures[0].RetryAt == nil {
		t.Fatalf("false withdrawal result used the old operation deadline: %+v", store.failures)
	}
	if len(runtime.deletes) != 0 {
		t.Fatal("false withdrawal result touched runtime")
	}
}

func TestWorkerWithdrawalRefreshErrorUsesCompletedWithdrawalTimeoutBudget(t *testing.T) {
	now := time.Date(2026, 8, 31, 15, 0, 0, 0, time.UTC)
	store, runtime := workerFixture()
	store.operation.Type = domain.ActionRestart
	store.operation.TargetGeneration = 2
	store.operation.CreatedAt = now.Add(-20 * time.Minute)
	store.operation.Attempt = 1
	store.operation.ErrorCode = "RUNTIME_RESTART_PENDING"
	store.service.Generation = 2
	store.service.Status = domain.StatusDeploying
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID
	store.service.Publication = domain.Publication{
		Desired: domain.PublicationUnpublished, Generation: 2, ObservedGeneration: 2,
		Phase: domain.PublicationUnpublishedOK, UpdatedAt: now.Add(-time.Minute),
	}
	store.getServiceErrAt = 2

	handled, err := NewWorker(store, runtime, "worker-withdraw-refresh-budget", func() time.Time { return now }).RunOnce(context.Background())
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v,%v)", handled, err)
	}
	if len(store.failures) != 1 || store.failures[0].ErrorCode != codeGatewayUnpublishCheck || store.failures[0].RetryAt == nil {
		t.Fatalf("withdrawal refresh error used the old operation deadline: %+v", store.failures)
	}
	if len(runtime.lifecycles) != 0 || len(runtime.requests) != 0 {
		t.Fatal("withdrawal refresh error touched runtime")
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
	if !store.observations[1].Publish {
		t.Fatalf("successful start did not request publication: %+v", store.observations[1])
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
	if len(store.observations) == 0 || !store.observations[len(store.observations)-1].Publish {
		t.Fatalf("successful restart did not request publication: %+v", store.observations)
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

func TestWorkerImageUnavailableIsTerminalAndDeletesRuntime(t *testing.T) {
	store, runtime := workerFixture()
	runtime.ensureErr = runtimeport.ErrImageUnavailable
	worker := NewWorker(store, runtime, "worker-image", time.Now)
	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("RunOnce() = (%v, %v)", handled, err)
	}
	if len(store.failures) != 1 || store.failures[0].ErrorCode != codeImageUnavailable || store.failures[0].RetryAt != nil {
		t.Fatalf("failures = %+v", store.failures)
	}
	if len(runtime.deletes) != 1 || store.service.Status != domain.StatusFailed {
		t.Fatalf("image failure must release runtime: deletes=%+v status=%s", runtime.deletes, store.service.Status)
	}
}

func TestWorkerCreateExhaustedRetriesGoDeadLetterAndDelete(t *testing.T) {
	store, runtime := workerFixture()
	store.repeatClaims = true
	runtime.ensureErr = errors.New("core temporarily unavailable")
	worker := NewWorker(store, runtime, "worker-dead-letter", func() time.Time { return time.Unix(400, 0).UTC() })
	worker.maxAttempts = 2

	for i := 0; i < 2; i++ {
		if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
			t.Fatalf("RunOnce(%d) = (%v, %v)", i, handled, err)
		}
	}
	if len(store.failures) != 2 {
		t.Fatalf("failures = %+v", store.failures)
	}
	if store.failures[0].RetryAt == nil || store.failures[0].DeadLetter {
		t.Fatalf("first failure must retry: %+v", store.failures[0])
	}
	last := store.failures[1]
	if !last.DeadLetter || last.RetryAt != nil || last.ErrorCode != codeRuntimeMutationFailed {
		t.Fatalf("second failure must dead-letter: %+v", last)
	}
	if store.operation.State != domain.OperationDeadLetter || store.service.Status != domain.StatusFailed {
		t.Fatalf("dead-letter state = %s/%s", store.operation.State, store.service.Status)
	}
	if len(runtime.deletes) != 1 {
		t.Fatalf("dead-letter create must delete provider runtime: %+v", runtime.deletes)
	}
}

func TestWorkerCreateDeployTimeoutFailsAndDeletesRuntime(t *testing.T) {
	store, runtime := workerFixture()
	store.operation.CreatedAt = time.Unix(0, 0).UTC()
	runtime.observation.Ready = false
	runtime.observation.ReadyReplicas = 0
	worker := NewWorker(store, runtime, "worker-timeout", func() time.Time { return time.Unix(20*60, 0).UTC() })
	worker.deployTimeout = 15 * time.Minute
	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("RunOnce() = (%v, %v)", handled, err)
	}
	if len(store.failures) != 1 || store.failures[0].ErrorCode != codeDeployTimeout || store.failures[0].RetryAt != nil || store.failures[0].DeadLetter {
		t.Fatalf("timeout failure = %+v", store.failures)
	}
	if len(runtime.deletes) != 1 || store.service.Status != domain.StatusFailed {
		t.Fatalf("deploy timeout must release runtime: deletes=%+v status=%s", runtime.deletes, store.service.Status)
	}
	if runtime.deletes[0].IdempotencyKey == runtimeIdempotencyKey(store.operation.ServiceID, store.operation.TargetGeneration) {
		t.Fatal("cleanup must not reuse the create Ensure idempotency key")
	}
	if runtime.deletes[0].IdempotencyKey != cleanupIdempotencyKey(store.operation.ServiceID, store.operation.TargetGeneration) {
		t.Fatalf("cleanup key = %s", runtime.deletes[0].IdempotencyKey)
	}
}

func TestWorkerScaleFailureRollsBackToBeforeSpec(t *testing.T) {
	store, runtime := workerFixture()
	store.operation.Type = domain.ActionScale
	store.operation.TargetGeneration = 2
	store.operation.CreatedAt = time.Unix(0, 0).UTC()
	store.operation.BeforeSpec = domain.Spec{Replicas: 1}
	store.operation.TargetSpec = domain.Spec{Replicas: 2}
	store.service.Status = domain.StatusDeploying
	store.service.Generation = 2
	store.service.AppliedSpec = domain.Spec{Replicas: 1}
	store.service.DesiredSpec = domain.Spec{Replicas: 2}
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID
	runtime.observation.ReadyReplicas = 1
	runtime.observation.Ready = true

	worker := NewWorker(store, runtime, "worker-scale-rollback", func() time.Time { return time.Unix(16*60, 0).UTC() })
	worker.deployTimeout = 15 * time.Minute
	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("RunOnce() = (%v, %v)", handled, err)
	}
	if store.operation.RollbackGeneration != 3 {
		t.Fatalf("rollback generation = %d, want 3", store.operation.RollbackGeneration)
	}
	if len(runtime.requests) < 2 {
		t.Fatalf("scale+rollback Ensure calls = %+v", runtime.requests)
	}
	rollback := runtime.requests[len(runtime.requests)-1]
	if rollback.Spec.Replicas != 1 || rollback.Generation != 3 || rollback.IdempotencyKey != rollbackIdempotencyKey(store.operation.ID, 3) {
		t.Fatalf("rollback Ensure = %+v", rollback)
	}
	if store.service.Status != domain.StatusRunning || store.service.AppliedSpec.Replicas != 1 || store.service.DesiredSpec.Replicas != 1 {
		t.Fatalf("rolled-back service = %+v", store.service)
	}
	if store.operation.State != domain.OperationFailed || store.operation.ErrorCode != codeScaleRolledBack {
		t.Fatalf("rolled-back operation = %+v", store.operation)
	}
	if store.service.StatusReason != codeScaleRolledBack {
		t.Fatalf("service reason = %s", store.service.StatusReason)
	}
}

func TestWorkerRollbackEmbeddingTaskPropagatesToSmoke(t *testing.T) {
	store, runtime := workerFixture()
	store.operation.Type = domain.ActionScale
	store.operation.TargetGeneration = 2
	store.operation.CreatedAt = time.Unix(0, 0).UTC()
	store.operation.BeforeSpec = domain.Spec{Replicas: 1, ExecutionProfile: domain.ExecutionProfile{Task: domain.InferenceTaskEmbed}}
	store.operation.TargetSpec = domain.Spec{Replicas: 2, ExecutionProfile: domain.ExecutionProfile{Task: domain.InferenceTaskGenerate}}
	store.service.Status = domain.StatusDeploying
	store.service.Generation = 2
	store.service.AppliedSpec = store.operation.BeforeSpec
	store.service.DesiredSpec = store.operation.TargetSpec
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID
	runtime.observation.ReadyReplicas = 1
	runtime.observation.Ready = true

	worker := NewWorker(store, runtime, "worker-embedding-rollback", func() time.Time { return time.Unix(16*60, 0).UTC() })
	worker.deployTimeout = 15 * time.Minute
	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("RunOnce() = (%v, %v)", handled, err)
	}
	if len(runtime.smokeTasks) != 1 || runtime.smokeTasks[0] != domain.InferenceTaskEmbed {
		t.Fatalf("rollback smoke tasks = %v, want [embed]", runtime.smokeTasks)
	}
}

func TestWorkerNotReadyDoesNotDeadLetterBeforeTimeout(t *testing.T) {
	store, runtime := workerFixture()
	store.operation.Attempt = 19
	store.operation.CreatedAt = time.Unix(100, 0).UTC()
	runtime.observation.Ready = false
	runtime.observation.ReadyReplicas = 0
	worker := NewWorker(store, runtime, "worker-not-ready", func() time.Time { return time.Unix(130, 0).UTC() })
	worker.maxAttempts = 20
	worker.deployTimeout = 15 * time.Minute
	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("RunOnce() = (%v, %v)", handled, err)
	}
	if len(store.failures) != 1 || store.failures[0].RetryAt == nil || store.failures[0].DeadLetter {
		t.Fatalf("not-ready must keep retrying until deploy timeout: %+v", store.failures)
	}
	if len(runtime.deletes) != 0 {
		t.Fatalf("not-ready must not delete runtime before timeout: %+v", runtime.deletes)
	}
}

func TestWorkerStartPermanentFailureDeletesRuntime(t *testing.T) {
	store, runtime := workerFixture()
	store.operation.Type = domain.ActionStart
	store.operation.TargetGeneration = 2
	store.service.Status = domain.StatusDeploying
	store.service.Generation = 2
	store.service.RuntimeRef = uuid.Nil
	store.service.ActiveOperationID = store.operation.ID
	runtime.ensureErr = runtimeport.ErrImageUnavailable
	worker := NewWorker(store, runtime, "worker-start-fail", time.Now)
	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("RunOnce() = (%v, %v)", handled, err)
	}
	if len(store.failures) != 1 || store.failures[0].ErrorCode != codeImageUnavailable || store.failures[0].RetryAt != nil {
		t.Fatalf("start failure = %+v", store.failures)
	}
	if len(runtime.deletes) != 1 {
		t.Fatalf("start permanent failure must delete runtime: %+v", runtime.deletes)
	}
}

func TestWorkerScaleRollbackFailureMarksRollbackFailed(t *testing.T) {
	store, runtime := workerFixture()
	store.operation.Type = domain.ActionScale
	store.operation.TargetGeneration = 2
	store.operation.Attempt = 19
	store.operation.BeforeSpec = domain.Spec{Replicas: 1}
	store.operation.TargetSpec = domain.Spec{Replicas: 2}
	store.service.Status = domain.StatusDeploying
	store.service.Generation = 2
	store.service.AppliedSpec = domain.Spec{Replicas: 1}
	store.service.DesiredSpec = domain.Spec{Replicas: 2}
	store.service.RuntimeRef = runtime.observation.RuntimeRef
	store.service.ActiveOperationID = store.operation.ID
	runtime.ensureErr = runtimeport.ErrImageUnavailable

	worker := NewWorker(store, runtime, "worker-rollback-failed", time.Now)
	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("RunOnce() = (%v, %v)", handled, err)
	}
	if store.service.Status != domain.StatusFailed || store.service.StatusReason != codeRollbackFailed {
		t.Fatalf("service = status=%s reason=%s", store.service.Status, store.service.StatusReason)
	}
	if store.operation.State != domain.OperationFailed || store.operation.ErrorCode != codeRollbackFailed {
		t.Fatalf("operation = %+v", store.operation)
	}
	if store.operation.RollbackGeneration == 0 {
		t.Fatal("rollback generation must be recorded before ROLLBACK_FAILED")
	}
}

func TestWorkerScaleRollbackSkippedWhenDesiredDeleted(t *testing.T) {
	store, runtime := workerFixture()
	store.operation.Type = domain.ActionScale
	store.operation.TargetGeneration = 2
	store.operation.Attempt = 19
	store.service.Status = domain.StatusDeploying
	store.service.Generation = 2
	store.service.DesiredState = domain.DesiredStateDeleted
	store.service.AppliedSpec = domain.Spec{Replicas: 1}
	store.service.DesiredSpec = domain.Spec{Replicas: 2}
	store.service.ActiveOperationID = store.operation.ID

	worker := NewWorker(store, runtime, "worker-delete-wins", time.Now)
	if handled, err := worker.RunOnce(context.Background()); err != nil || !handled {
		t.Fatalf("RunOnce() = (%v, %v)", handled, err)
	}
	if store.operation.RollbackGeneration != 0 {
		t.Fatalf("delete-wins started rollback: %+v", store.operation)
	}
	if len(runtime.requests) != 0 {
		t.Fatalf("delete-wins must not Ensure previous spec: %+v", runtime.requests)
	}
}

func TestClassifyRuntimeErrorAndGenerationMatch(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{runtimeport.ErrRuntimeUnsupported, codeAcceleratorUnavailable},
		{runtimeport.ErrImageUnavailable, codeImageUnavailable},
		{runtimeport.ErrEngineProfileUnapproved, codeEngineProfileUnapproved},
		{runtimeport.ErrReservedFieldConflict, codeReservedFieldConflict},
		{errors.New("core 502"), codeRuntimeMutationFailed},
	}
	for _, tc := range cases {
		got := classifyRuntimeError(tc.err)
		if got.code != tc.code || got.retryable != retryableCode(tc.code) {
			t.Fatalf("classify(%v) = %+v", tc.err, got)
		}
	}
	service := domain.Service{ActiveOperationID: uuid.MustParse("70000000-0000-0000-0000-000000000007"), Generation: 3}
	operation := domain.Operation{ID: service.ActiveOperationID, TargetGeneration: 2, RollbackGeneration: 3}
	if !generationMatches(service, operation) {
		t.Fatal("rollback generation must match the compensated service generation")
	}
}
