package reconcile_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/catalog"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/reconcile"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	runtimeport "github.com/kubercloud/ani/services/inference-service/internal/runtime"
	runtimefake "github.com/kubercloud/ani/services/inference-service/internal/runtime/fake"
	"github.com/kubercloud/ani/services/inference-service/internal/service"
)

type memoryCatalog struct {
	versions map[string]catalog.ModelVersion
}

func newMemoryCatalog() *memoryCatalog {
	return &memoryCatalog{versions: map[string]catalog.ModelVersion{}}
}

func (c *memoryCatalog) put(tenantID uuid.UUID, version catalog.ModelVersion) {
	c.versions[tenantID.String()+"/"+version.ID.String()] = version
}

func (c *memoryCatalog) Resolve(_ context.Context, tenantID, versionID uuid.UUID) (catalog.ModelVersion, error) {
	version, ok := c.versions[tenantID.String()+"/"+versionID.String()]
	if !ok {
		return catalog.ModelVersion{}, catalog.ErrModelNotFound
	}
	return version, nil
}

type memoryStore struct {
	mu         sync.Mutex
	resource   domain.Service
	operation  domain.Operation
	operations map[uuid.UUID]domain.Operation
	claimed    bool
}

func (*memoryStore) FindCreateReplay(context.Context, uuid.UUID, string, uuid.UUID, string) (repository.CreateResult, bool, error) {
	return repository.CreateResult{}, false, nil
}

func (m *memoryStore) CreateWithOperation(_ context.Context, resource domain.Service, operation domain.Operation) (repository.CreateResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resource, m.operation = resource, operation
	m.operations = map[uuid.UUID]domain.Operation{operation.ID: operation}
	return repository.CreateResult{Service: resource, Operation: operation}, nil
}
func (m *memoryStore) BindRuntimeRef(_ context.Context, binding repository.RuntimeBinding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.resource.TenantID != binding.TenantID || m.resource.ID != binding.ServiceID ||
		m.resource.Generation != binding.Generation || m.resource.CurrentOperationID != binding.OperationID {
		return repository.ErrStaleGeneration
	}
	m.resource.RuntimeRef = binding.RuntimeRef
	m.resource.Status = domain.StatusDeploying
	return nil
}
func (m *memoryStore) AbortCreate(_ context.Context, binding repository.RuntimeBinding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.resource.ID != binding.ServiceID || m.resource.RuntimeRef != uuid.Nil {
		return repository.ErrStaleGeneration
	}
	m.resource = domain.Service{}
	m.operation = domain.Operation{}
	delete(m.operations, binding.OperationID)
	return nil
}
func (m *memoryStore) AbortPendingMutation(_ context.Context, abort repository.MutationAbort) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.resource.ID != abort.ServiceID || m.resource.Generation != abort.TargetGeneration ||
		m.resource.CurrentOperationID != abort.OperationID {
		return repository.ErrStaleGeneration
	}
	m.resource.DesiredSpec = abort.RestoredSpec
	m.resource.Status = abort.RestoredStatus
	m.resource.DesiredState = abort.RestoredDesired
	m.resource.Generation = abort.RestoredGeneration
	m.resource.CurrentOperationID = uuid.Nil
	m.resource.ActiveOperationID = uuid.Nil
	m.resource.ActiveOperation = ""
	if operation, ok := m.operations[abort.OperationID]; ok {
		operation.State = domain.OperationCancelled
		m.operations[abort.OperationID] = operation
		m.operation = operation
	}
	return nil
}
func (m *memoryStore) GetService(_ context.Context, tenantID, serviceID uuid.UUID) (domain.Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.resource.TenantID != tenantID || m.resource.ID != serviceID || m.resource.DeletedAt != nil {
		return domain.Service{}, repository.ErrNotFound
	}
	return m.resource, nil
}
func (m *memoryStore) GetOperation(_ context.Context, tenantID, operationID uuid.UUID) (domain.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	operation, ok := m.operations[operationID]
	if !ok || operation.TenantID != tenantID {
		return domain.Operation{}, repository.ErrNotFound
	}
	return operation, nil
}

func (m *memoryStore) ListServices(_ context.Context, tenantID uuid.UUID) ([]domain.Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.resource.TenantID != tenantID || m.resource.DeletedAt != nil {
		return []domain.Service{}, nil
	}
	return []domain.Service{m.resource}, nil
}

func (m *memoryStore) MutateService(_ context.Context, request repository.MutationRequest) (repository.MutationResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.resource.TenantID != request.TenantID || m.resource.ID != request.ServiceID || m.resource.DeletedAt != nil {
		return repository.MutationResult{}, repository.ErrNotFound
	}
	transition, err := domain.BeginTransition(m.resource, request.Action, request.TargetSpec, request.OperationID)
	if err != nil {
		return repository.MutationResult{}, err
	}
	if transition.Disposition == domain.TransitionReuseOperation {
		operation := m.operations[transition.OperationID]
		operation.Replayed = true
		return repository.MutationResult{Service: m.resource, Operation: operation, Disposition: transition.Disposition}, nil
	}
	if transition.Disposition == domain.TransitionAlreadyDesired {
		completedAt := request.Now
		operation := domain.Operation{
			ID: request.OperationID, TenantID: request.TenantID, ServiceID: request.ServiceID,
			Type: request.Action, State: domain.OperationCompleted, TargetGeneration: m.resource.Generation,
			BeforeSpec: m.resource.AppliedSpec, TargetSpec: m.resource.DesiredSpec,
			OperationScope: request.OperationScope, IdempotencyKey: request.IdempotencyKey,
			RequestHash: request.RequestHash, CreatedAt: request.Now, UpdatedAt: request.Now, CompletedAt: &completedAt,
		}
		m.operations[operation.ID] = operation
		m.operation = operation
		m.resource.CurrentOperationID = operation.ID
		return repository.MutationResult{Service: m.resource, Operation: operation, Disposition: transition.Disposition}, nil
	}
	operation := transition.Operation
	operation.OperationScope = request.OperationScope
	operation.IdempotencyKey = request.IdempotencyKey
	operation.RequestHash = request.RequestHash
	operation.NextAttemptAt = request.Now
	operation.CreatedAt = request.Now
	operation.UpdatedAt = request.Now
	if operation.PreemptedOperationID != uuid.Nil {
		preempted := m.operations[operation.PreemptedOperationID]
		preempted.State = domain.OperationCancelled
		m.operations[preempted.ID] = preempted
	}
	m.resource = transition.Service
	m.operation = operation
	m.operations[operation.ID] = operation
	m.claimed = false
	return repository.MutationResult{Service: m.resource, Operation: operation, Disposition: transition.Disposition}, nil
}
func (m *memoryStore) ClaimOperation(_ context.Context, owner string, now time.Time, duration time.Duration) (domain.Operation, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.claimed || m.operation.State != domain.OperationPending {
		return domain.Operation{}, false, nil
	}
	m.claimed = true
	m.operation.State = domain.OperationRunning
	m.operation.LeaseOwner = owner
	m.operation.LeaseToken = uuid.New()
	until := now.Add(duration)
	m.operation.LeaseUntil = &until
	m.operations[m.operation.ID] = m.operation
	return m.operation, true, nil
}
func (m *memoryStore) ApplyObservation(_ context.Context, observation repository.Observation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.resource.TenantID != observation.TenantID || m.resource.ID != observation.ServiceID ||
		m.resource.Generation != observation.TargetGeneration || m.resource.ActiveOperationID != observation.OperationID {
		return repository.ErrStaleGeneration
	}
	m.resource.Status = observation.Status
	m.resource.RuntimeRef = observation.RuntimeRef
	m.resource.RuntimeEndpoint = observation.RuntimeEndpoint
	m.resource.ReadyReplicas = observation.ReadyReplicas
	if observation.Complete {
		m.resource.ObservedGeneration = observation.TargetGeneration
		m.resource.AppliedSpec = observation.AppliedSpec
		m.operation.State = domain.OperationCompleted
		m.resource.ActiveOperationID = uuid.Nil
		m.resource.ActiveOperation = ""
		if observation.Deleted {
			deletedAt := time.Now().UTC()
			m.resource.DeletedAt = &deletedAt
		}
	}
	m.operations[m.operation.ID] = m.operation
	return nil
}
func (m *memoryStore) FailOperation(_ context.Context, failure repository.Failure) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.operation.Attempt++
	m.operation.ErrorCode = failure.ErrorCode
	m.operation.ErrorMessage = failure.ErrorMessage
	switch {
	case failure.DeadLetter:
		m.operation.State = domain.OperationDeadLetter
		m.resource.Status = domain.StatusFailed
		m.resource.ActiveOperationID = uuid.Nil
		m.resource.ActiveOperation = ""
	case failure.RetryAt == nil:
		m.operation.State = domain.OperationFailed
		m.resource.Status = domain.StatusFailed
		m.resource.ActiveOperationID = uuid.Nil
		m.resource.ActiveOperation = ""
	default:
		m.operation.State = domain.OperationPending
		m.claimed = false
	}
	m.operations[m.operation.ID] = m.operation
	return nil
}

func (m *memoryStore) BeginScaleRollback(_ context.Context, request repository.ScaleRollback) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.resource.DesiredState == domain.DesiredStateDeleted {
		return 0, domain.ErrDeleted
	}
	if m.operation.RollbackGeneration != 0 {
		return m.operation.RollbackGeneration, nil
	}
	if m.resource.Generation != request.TargetGeneration || m.resource.ActiveOperationID != request.OperationID {
		return 0, repository.ErrStaleGeneration
	}
	m.resource.DesiredSpec = m.resource.AppliedSpec
	m.resource.Generation++
	m.resource.Status = domain.StatusDeploying
	m.operation.RollbackGeneration = m.resource.Generation
	m.operations[m.operation.ID] = m.operation
	return m.operation.RollbackGeneration, nil
}

func (m *memoryStore) FinishScaleRollback(_ context.Context, finish repository.ScaleRollbackFinish) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if finish.Success {
		m.resource.Status = domain.StatusRunning
		m.resource.AppliedSpec = finish.AppliedSpec
		m.resource.DesiredSpec = finish.AppliedSpec
		m.resource.ObservedGeneration = m.resource.Generation
		m.resource.RuntimeEndpoint = finish.RuntimeEndpoint
		m.resource.ReadyReplicas = finish.ReadyReplicas
		m.operation.State = domain.OperationFailed
		m.operation.ErrorCode = "SCALE_ROLLED_BACK"
	} else {
		m.resource.Status = domain.StatusFailed
		m.resource.StatusReason = "ROLLBACK_FAILED"
		m.resource.RuntimeEndpoint = ""
		m.operation.State = domain.OperationFailed
		m.operation.ErrorCode = "ROLLBACK_FAILED"
	}
	m.resource.ActiveOperationID = uuid.Nil
	m.resource.ActiveOperation = ""
	m.operations[m.operation.ID] = m.operation
	return nil
}

func TestCreateToRunningWithFakeDependencies(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	tenantID := uuid.New()
	versionID := uuid.New()
	catalogPort := newMemoryCatalog()
	cpuProfile := catalog.EngineProfile{ID: "vllm-chat-cpu", Version: "1", Runtime: "vllm", ImageRef: "registry/vllm@sha256:image"}
	catalogPort.put(tenantID, catalog.ModelVersion{
		ID: versionID, ModelID: uuid.New(), DisplayName: "Qwen 7B / immutable-v1", Ready: true,
		Format: "safetensors", ArtifactRef: "object://models/qwen/v1", ArtifactDigest: "sha256:model",
		CPUProfile: &cpuProfile,
	})
	store := &memoryStore{}
	runtimePort := runtimefake.New()
	creator := service.NewCreator(store, catalogPort, func() time.Time { return now }).WithRuntime(runtimePort)
	created, operation, err := creator.Create(ctx, tenantID, service.CreateInput{
		IdempotencyKey: uuid.New(), Name: "qwen-chat", ModelVersionID: versionID,
		ImageRef: "registry.local/user/vllm@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Spec:     domain.Spec{Replicas: 1, CPU: "4", Memory: "16Gi", PlacementMode: "auto"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Status != domain.StatusDeploying || created.RuntimeRef == uuid.Nil || operation.State != domain.OperationPending {
		t.Fatalf("create must dispatch runtime and stay pending for align: service=%+v operation=%+v", created, operation)
	}

	worker := reconcile.NewWorker(store, runtimePort, "worker-flow", func() time.Time { return now })
	handled, err := worker.RunOnce(ctx)
	if err != nil || !handled {
		t.Fatalf("RunOnce() = (%v, %v), want (true, nil)", handled, err)
	}
	finished, err := store.GetService(ctx, tenantID, created.ID)
	if err != nil {
		t.Fatalf("GetService() error = %v", err)
	}
	finishedOperation, err := store.GetOperation(ctx, tenantID, operation.ID)
	if err != nil {
		t.Fatalf("GetOperation() error = %v", err)
	}
	if finished.Status != domain.StatusRunning || finished.ObservedGeneration != 1 || finished.RuntimeRef == uuid.Nil {
		t.Fatalf("service did not converge to running: %+v", finished)
	}
	if finishedOperation.State != domain.OperationCompleted {
		t.Fatalf("operation state = %s, want completed", finishedOperation.State)
	}
}

func TestFullLifecycleConvergesWithFakeDependencies(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 13, 0, 0, 0, time.UTC)
	tenantID := uuid.New()
	versionID := uuid.New()
	catalogPort := newMemoryCatalog()
	cpuProfile := catalog.EngineProfile{ID: "vllm-chat-cpu", Version: "1", Runtime: "vllm", ImageRef: "registry/vllm@sha256:image"}
	catalogPort.put(tenantID, catalog.ModelVersion{
		ID: versionID, ModelID: uuid.New(), DisplayName: "Qwen 7B / immutable-v1", Ready: true,
		Format: "safetensors", ArtifactRef: "object://models/qwen/v1", ArtifactDigest: "sha256:model",
		CPUProfile: &cpuProfile,
	})
	store := &memoryStore{}
	runtimePort := runtimefake.New()
	creator := service.NewCreator(store, catalogPort, func() time.Time { return now }).WithRuntime(runtimePort)
	created, _, err := creator.Create(ctx, tenantID, service.CreateInput{
		IdempotencyKey: uuid.New(), Name: "qwen-lifecycle", ModelVersionID: versionID,
		ImageRef: "registry.local/user/vllm@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Spec:     domain.Spec{Replicas: 1, CPU: "4", Memory: "16Gi", PlacementMode: "single_node"},
	})
	if err != nil {
		t.Fatal(err)
	}
	worker := reconcile.NewWorker(store, runtimePort, "worker-flow", func() time.Time { return now })
	controller := service.NewController(store, func() time.Time { now = now.Add(time.Second); return now }).WithRuntime(runtimePort)
	run := func(action string) {
		t.Helper()
		handled, runErr := worker.RunOnce(ctx)
		if runErr != nil || !handled {
			t.Fatalf("%s worker = (%v,%v)", action, handled, runErr)
		}
	}

	run("create")
	if _, err := controller.Scale(ctx, tenantID, created.ID, uuid.New(), 2); err != nil {
		t.Fatal(err)
	}
	run("scale")
	assertFlowStatus(t, store, tenantID, created.ID, domain.StatusRunning, 2)

	if _, err := controller.Lifecycle(ctx, tenantID, created.ID, uuid.New(), domain.ActionStop); err != nil {
		t.Fatal(err)
	}
	run("stop")
	stopped := assertFlowStatus(t, store, tenantID, created.ID, domain.StatusStopped, 0)
	if stopped.RuntimeRef == uuid.Nil || stopped.RuntimeEndpoint != "" {
		t.Fatalf("stop did not retain identity/clear endpoint: %+v", stopped)
	}

	if _, err := controller.Lifecycle(ctx, tenantID, created.ID, uuid.New(), domain.ActionStart); err != nil {
		t.Fatal(err)
	}
	run("start")
	assertFlowStatus(t, store, tenantID, created.ID, domain.StatusRunning, 2)

	if _, err := controller.Lifecycle(ctx, tenantID, created.ID, uuid.New(), domain.ActionRestart); err != nil {
		t.Fatal(err)
	}
	run("restart")
	assertFlowStatus(t, store, tenantID, created.ID, domain.StatusRunning, 2)

	if _, err := controller.Delete(ctx, tenantID, created.ID); err != nil {
		t.Fatal(err)
	}
	run("delete")
	if _, err := store.GetService(ctx, tenantID, created.ID); err != repository.ErrNotFound {
		t.Fatalf("deleted service lookup error = %v", err)
	}
}

func TestCreatePreemptedByStopCannotRestoreRunning(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 14, 14, 0, 0, 0, time.UTC)
	tenantID, versionID := uuid.New(), uuid.New()
	catalogPort := newMemoryCatalog()
	cpuProfile := catalog.EngineProfile{ID: "cpu", Version: "1", Runtime: "vllm", ImageRef: "registry/vllm@sha256:image"}
	catalogPort.put(tenantID, catalog.ModelVersion{
		ID: versionID, ModelID: uuid.New(), DisplayName: "Qwen", Ready: true,
		ArtifactRef: "object://qwen", ArtifactDigest: "sha256:model", CPUProfile: &cpuProfile,
	})
	store := &memoryStore{}
	runtimePort := runtimefake.New()
	created, createOperation, err := service.NewCreator(store, catalogPort, func() time.Time { return now }).
		WithRuntime(runtimePort).Create(
		ctx, tenantID, service.CreateInput{
			IdempotencyKey: uuid.New(), Name: "qwen-preempt", ModelVersionID: versionID,
			ImageRef: "registry.local/user/vllm@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Spec:     domain.Spec{Replicas: 1, CPU: "2", Memory: "8Gi", PlacementMode: "single_node"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	claimedCreate, claimed, err := store.ClaimOperation(ctx, "old-create-worker", now, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim create = (%+v,%v,%v)", claimedCreate, claimed, err)
	}
	controller := service.NewController(store, func() time.Time { return now.Add(time.Second) }).WithRuntime(runtimePort)
	if _, err := controller.Lifecycle(ctx, tenantID, created.ID, uuid.New(), domain.ActionStop); err != nil {
		t.Fatal(err)
	}
	worker := reconcile.NewWorker(store, runtimePort, "stop-worker", func() time.Time { return now.Add(2 * time.Second) })
	if handled, err := worker.RunOnce(ctx); err != nil || !handled {
		t.Fatalf("stop worker = (%v,%v)", handled, err)
	}
	stopped := assertFlowStatus(t, store, tenantID, created.ID, domain.StatusStopped, 0)
	if stopped.RuntimeEndpoint != "" {
		t.Fatalf("preempted create endpoint survived stop: %+v", stopped)
	}
	if _, err := runtimePort.Ensure(ctx, runtimeport.EnsureRequest{
		TenantID: tenantID, ServiceID: created.ID, Generation: createOperation.TargetGeneration,
		IdempotencyKey: uuid.New(), Name: created.Name, ServedModelName: created.ServedModelName,
		Spec: createOperation.TargetSpec,
	}); !errors.Is(err, runtimeport.ErrStaleRuntimeGeneration) {
		t.Fatalf("late create after stop error = %v", err)
	}
	if err := store.ApplyObservation(ctx, repository.Observation{
		TenantID: tenantID, ServiceID: created.ID, OperationID: createOperation.ID,
		TargetGeneration: createOperation.TargetGeneration, Status: domain.StatusRunning,
		AppliedSpec: createOperation.TargetSpec, RuntimeRef: uuid.New(),
		RuntimeEndpoint: "http://late-create.internal.svc:8000", ReadyReplicas: 1, Complete: true,
		LeaseToken: claimedCreate.LeaseToken,
	}); err != repository.ErrStaleGeneration {
		t.Fatalf("old create observation error = %v", err)
	}
}

func assertFlowStatus(t *testing.T, store *memoryStore, tenantID, serviceID uuid.UUID, status domain.Status, ready int) domain.Service {
	t.Helper()
	resource, err := store.GetService(context.Background(), tenantID, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Status != status || resource.ReadyReplicas != ready || resource.ObservedGeneration != resource.Generation {
		t.Fatalf("service status = %+v, want status=%s ready=%d", resource, status, ready)
	}
	return resource
}
