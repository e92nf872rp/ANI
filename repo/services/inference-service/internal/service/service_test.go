package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/catalog"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	"github.com/kubercloud/ani/services/inference-service/internal/runtime"
	runtimefake "github.com/kubercloud/ani/services/inference-service/internal/runtime/fake"
)

type catalogStub struct {
	resolved catalog.ModelVersion
	err      error
	calls    int
}

func (c *catalogStub) Resolve(context.Context, uuid.UUID, uuid.UUID) (catalog.ModelVersion, error) {
	c.calls++
	return c.resolved, c.err
}

type storeStub struct {
	create func(domain.Service, domain.Operation) (repository.CreateResult, error)
	find   func(string) (repository.CreateResult, bool, error)
	calls  int
	binds  int
	aborts int
}

func (s *storeStub) FindCreateReplay(_ context.Context, _ uuid.UUID, _ string, _ uuid.UUID, hash string) (repository.CreateResult, bool, error) {
	if s.find == nil {
		return repository.CreateResult{}, false, nil
	}
	return s.find(hash)
}

func (s *storeStub) CreateWithOperation(_ context.Context, resource domain.Service, operation domain.Operation) (repository.CreateResult, error) {
	s.calls++
	return s.create(resource, operation)
}

func (s *storeStub) AbortCreate(context.Context, repository.RuntimeBinding) error {
	s.aborts++
	return nil
}
func (s *storeStub) BindRuntimeRef(_ context.Context, binding repository.RuntimeBinding) error {
	s.binds++
	if binding.RuntimeRef == uuid.Nil {
		return errors.New("runtime reference is required")
	}
	return nil
}
func (*storeStub) GetService(context.Context, uuid.UUID, uuid.UUID) (domain.Service, error) {
	panic("unexpected GetService")
}
func (*storeStub) GetOperation(context.Context, uuid.UUID, uuid.UUID) (domain.Operation, error) {
	panic("unexpected GetOperation")
}
func (*storeStub) ClaimOperation(context.Context, string, time.Time, time.Duration) (domain.Operation, bool, error) {
	panic("unexpected ClaimOperation")
}
func (*storeStub) ApplyObservation(context.Context, repository.Observation) error {
	panic("unexpected ApplyObservation")
}
func (*storeStub) FailOperation(context.Context, repository.Failure) error {
	panic("unexpected FailOperation")
}
func (*storeStub) BeginScaleRollback(context.Context, repository.ScaleRollback) (int64, error) {
	panic("unexpected BeginScaleRollback")
}
func (*storeStub) FinishScaleRollback(context.Context, repository.ScaleRollbackFinish) error {
	panic("unexpected FinishScaleRollback")
}

func readyVersion() catalog.ModelVersion {
	cpuProfile := catalog.EngineProfile{ID: "vllm-chat-cpu", Version: "1", Runtime: "vllm", ImageRef: "registry/vllm-cpu@sha256:image"}
	gpuProfile := catalog.EngineProfile{ID: "vllm-chat-gpu", Version: "1", Runtime: "vllm", ImageRef: "registry/vllm-gpu@sha256:image"}
	return catalog.ModelVersion{
		ID:             uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		ModelID:        uuid.MustParse("20000000-0000-0000-0000-000000000002"),
		DisplayName:    "Qwen 7B / v1",
		Ready:          true,
		Format:         "safetensors",
		ArtifactRef:    "object://models/qwen/v1",
		ArtifactDigest: "sha256:model",
		CPUProfile:     &cpuProfile,
		GPUProfile:     &gpuProfile,
	}
}

func validInput() CreateInput {
	return CreateInput{
		IdempotencyKey: uuid.MustParse("30000000-0000-0000-0000-000000000003"),
		Name:           "qwen-chat",
		ModelVersionID: readyVersion().ID,
		ImageRef:       "registry.local/user/vllm@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Spec: domain.Spec{
			Replicas: 1, CPU: "4", Memory: "16Gi", PlacementMode: "auto",
		},
	}
}

func TestCreateResolvesReadyModelAndPersistsPendingOperation(t *testing.T) {
	tenantID := uuid.MustParse("40000000-0000-0000-0000-000000000004")
	catalogPort := &catalogStub{resolved: readyVersion()}
	store := &storeStub{}
	store.create = func(resource domain.Service, operation domain.Operation) (repository.CreateResult, error) {
		if resource.TenantID != tenantID || resource.Generation != 1 || resource.Status != domain.StatusPending {
			t.Fatalf("unexpected resource: %+v", resource)
		}
		if resource.ModelSnapshot == nil || resource.DesiredSpec.ExecutionProfile.ID != "vllm-chat-cpu" {
			t.Fatalf("catalog snapshot/profile were not frozen: %+v", resource)
		}
		if resource.DesiredSpec.ExecutionProfile.ImageRef != validInput().ImageRef {
			t.Fatalf("create used catalog image %q, want request image %q", resource.DesiredSpec.ExecutionProfile.ImageRef, validInput().ImageRef)
		}
		if resource.DesiredSpec.ExecutionProfile.ImageRef == readyVersion().CPUProfile.ImageRef {
			t.Fatal("create must not freeze the catalog default image")
		}
		if operation.Type != domain.ActionCreate || operation.TaskType() != "inference_service.create" {
			t.Fatalf("unexpected operation: %+v", operation)
		}
		if operation.RequestHash == "" || operation.OperationScope != "inference_service.create" {
			t.Fatalf("idempotency identity missing: %+v", operation)
		}
		return repository.CreateResult{Service: resource, Operation: operation}, nil
	}

	creator := NewCreator(store, catalogPort, func() time.Time { return time.Unix(10, 0).UTC() })
	resource, operation, err := creator.Create(context.Background(), tenantID, validInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if resource.ID == uuid.Nil || operation.ID == uuid.Nil || store.calls != 1 || catalogPort.calls != 1 {
		t.Fatalf("create did not complete once: resource=%+v operation=%+v", resource, operation)
	}
}

func TestCreateFreezesEmbeddingTask(t *testing.T) {
	tenantID := uuid.MustParse("40000000-0000-0000-0000-000000000004")
	version := readyVersion()
	version.CPUProfile.ID = "vllm-embed-cpu"
	version.CPUProfile.Task = domain.InferenceTaskEmbed
	store := &storeStub{create: func(resource domain.Service, operation domain.Operation) (repository.CreateResult, error) {
		if resource.DesiredSpec.ExecutionProfile.Task != domain.InferenceTaskEmbed {
			t.Fatalf("execution task = %q, want %q", resource.DesiredSpec.ExecutionProfile.Task, domain.InferenceTaskEmbed)
		}
		return repository.CreateResult{Service: resource, Operation: operation}, nil
	}}

	resource, _, err := NewCreator(store, &catalogStub{resolved: version}, time.Now).
		Create(context.Background(), tenantID, validInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if resource.DesiredSpec.ExecutionProfile.Task != domain.InferenceTaskEmbed {
		t.Fatalf("persisted execution task = %q, want %q", resource.DesiredSpec.ExecutionProfile.Task, domain.InferenceTaskEmbed)
	}
}

func TestLegacyEmptyInferenceTaskDefaultsToGenerate(t *testing.T) {
	if got := domain.NormalizeInferenceTask(""); got != domain.InferenceTaskGenerate {
		t.Fatalf("NormalizeInferenceTask(\"\") = %q, want %q", got, domain.InferenceTaskGenerate)
	}
}

func TestLegacyUnknownInferenceTaskDefaultsToGenerate(t *testing.T) {
	task := domain.InferenceTask("unknown")
	if got := domain.NormalizeInferenceTask(task); got != domain.InferenceTaskGenerate {
		t.Fatalf("NormalizeInferenceTask(%q) = %q, want %q", task, got, domain.InferenceTaskGenerate)
	}
}

func TestCreateRejectsModelNotReadyBeforeDatabaseMutation(t *testing.T) {
	catalogPort := &catalogStub{resolved: catalog.ModelVersion{ID: readyVersion().ID, Ready: false}}
	store := &storeStub{create: func(domain.Service, domain.Operation) (repository.CreateResult, error) {
		t.Fatal("store must not be called for a non-ready model")
		return repository.CreateResult{}, nil
	}}
	creator := NewCreator(store, catalogPort, time.Now)

	_, _, err := creator.Create(context.Background(), uuid.New(), validInput())
	if !errors.Is(err, catalog.ErrModelNotReady) {
		t.Fatalf("expected ErrModelNotReady, got %v", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestCreateRequestHashIsDeterministicAndReplayIsReturned(t *testing.T) {
	var firstHash string
	var storedService domain.Service
	var storedOperation domain.Operation
	store := &storeStub{}
	store.create = func(resource domain.Service, operation domain.Operation) (repository.CreateResult, error) {
		if firstHash == "" {
			firstHash = operation.RequestHash
			storedService = resource
			storedOperation = operation
		} else if operation.RequestHash != firstHash {
			t.Fatalf("request hashes differ: %q != %q", operation.RequestHash, firstHash)
		}
		return repository.CreateResult{Service: storedService, Operation: storedOperation, Replayed: store.calls == 2}, nil
	}
	store.find = func(hash string) (repository.CreateResult, bool, error) {
		if firstHash == "" {
			return repository.CreateResult{}, false, nil
		}
		if hash != firstHash {
			t.Fatalf("replay request hashes differ: %q != %q", hash, firstHash)
		}
		return repository.CreateResult{Service: storedService, Operation: storedOperation, Replayed: true}, true, nil
	}
	creator := NewCreator(store, &catalogStub{resolved: readyVersion()}, time.Now)
	tenantID := uuid.New()

	firstService, firstOperation, err := creator.Create(context.Background(), tenantID, validInput())
	if err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	secondService, secondOperation, err := creator.Create(context.Background(), tenantID, validInput())
	if err != nil {
		t.Fatalf("second Create() error = %v", err)
	}
	if firstService.ID != secondService.ID || firstOperation.ID != secondOperation.ID {
		t.Fatal("same key and hash must return the originally persisted resource and operation")
	}
}

func TestCreateReplayDoesNotDependOnCatalogAvailability(t *testing.T) {
	storedService := domain.Service{ID: uuid.New(), TenantID: uuid.New(), Status: domain.StatusPending}
	storedOperation := domain.Operation{ID: uuid.New(), ServiceID: storedService.ID, Type: domain.ActionCreate}
	store := &storeStub{find: func(string) (repository.CreateResult, bool, error) {
		return repository.CreateResult{Service: storedService, Operation: storedOperation, Replayed: true}, true, nil
	}}
	catalogPort := &catalogStub{err: errors.New("catalog unavailable")}
	creator := NewCreator(store, catalogPort, time.Now)

	resource, operation, err := creator.Create(context.Background(), uuid.New(), validInput())
	if err != nil {
		t.Fatalf("Create() replay error = %v", err)
	}
	if resource.ID != storedService.ID || operation.ID != storedOperation.ID || catalogPort.calls != 0 {
		t.Fatalf("replay consulted catalog or changed result: resource=%+v operation=%+v calls=%d", resource, operation, catalogPort.calls)
	}
}

func TestCreateRequestHashDoesNotChangeWithCatalogRecommendation(t *testing.T) {
	var hashes []string
	store := &storeStub{create: func(resource domain.Service, operation domain.Operation) (repository.CreateResult, error) {
		hashes = append(hashes, operation.RequestHash)
		return repository.CreateResult{Service: resource, Operation: operation}, nil
	}}
	catalogPort := &catalogStub{resolved: readyVersion()}
	creator := NewCreator(store, catalogPort, time.Now)
	tenantID := uuid.New()
	if _, _, err := creator.Create(context.Background(), tenantID, validInput()); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	catalogPort.resolved.CPUProfile.Version = "2"
	if _, _, err := creator.Create(context.Background(), tenantID, validInput()); err != nil {
		t.Fatalf("second Create() error = %v", err)
	}
	if len(hashes) != 2 || hashes[0] != hashes[1] {
		t.Fatalf("catalog recommendation changed request identity: %v", hashes)
	}
}

func TestCreateRejectsUnavailableAcceleratorBeforePersist(t *testing.T) {
	store := &storeStub{create: func(domain.Service, domain.Operation) (repository.CreateResult, error) {
		t.Fatal("unavailable accelerator must not persist a service")
		return repository.CreateResult{}, nil
	}}
	creator := NewCreator(store, &catalogStub{resolved: readyVersion()}, time.Now).WithAdmission(admissionStub{
		err: runtime.ErrRuntimeUnsupported,
	})
	input := validInput()
	input.Spec.Accelerator = &domain.Accelerator{SpecID: "gpu-spec-a100", CountPerReplica: 1}
	_, _, err := creator.Create(context.Background(), uuid.New(), input)
	if !errors.Is(err, ErrAcceleratorSpecUnavailable) {
		t.Fatalf("Create() error = %v", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d", store.calls)
	}
}

func TestCreateRejectsUnsupportedTopologyBeforePersist(t *testing.T) {
	store := &storeStub{create: func(domain.Service, domain.Operation) (repository.CreateResult, error) {
		t.Fatal("unsupported topology must not persist a service")
		return repository.CreateResult{}, nil
	}}
	creator := NewCreator(store, &catalogStub{resolved: readyVersion()}, time.Now).WithAdmission(admissionStub{
		err: runtime.ErrUnsupportedTopology,
	})
	input := validInput()
	input.Spec.PlacementMode = "multi_node"
	input.Spec.Accelerator = &domain.Accelerator{SpecID: "gpu-spec-a100", CountPerReplica: 2}
	_, _, err := creator.Create(context.Background(), uuid.New(), input)
	if !errors.Is(err, ErrUnsupportedTopology) {
		t.Fatalf("Create() error = %v", err)
	}
}

type admissionStub struct{ err error }

func (a admissionStub) Admit(context.Context, uuid.UUID, domain.Spec) error { return a.err }

func TestCreatePropagatesNameConflict(t *testing.T) {
	store := &storeStub{create: func(domain.Service, domain.Operation) (repository.CreateResult, error) {
		return repository.CreateResult{}, repository.ErrNameConflict
	}}
	creator := NewCreator(store, &catalogStub{resolved: readyVersion()}, time.Now)

	_, _, err := creator.Create(context.Background(), uuid.New(), validInput())
	if !errors.Is(err, repository.ErrNameConflict) {
		t.Fatalf("expected ErrNameConflict, got %v", err)
	}
}

func TestCreateExecutionSelectionUsesAcceleratorPresenceOnly(t *testing.T) {
	call := 0
	store := &storeStub{}
	store.create = func(resource domain.Service, operation domain.Operation) (repository.CreateResult, error) {
		call++
		if call == 1 {
			if resource.DesiredSpec.UsesAccelerator() {
				t.Fatal("legacy GPU fields must not select GPU execution")
			}
			if resource.DesiredSpec.ExecutionProfile.ID != "vllm-chat-cpu" {
				t.Fatalf("CPU profile = %q", resource.DesiredSpec.ExecutionProfile.ID)
			}
		}
		if call == 2 {
			if !resource.DesiredSpec.UsesAccelerator() {
				t.Fatal("accelerator presence must select GPU execution")
			}
			if resource.DesiredSpec.ExecutionProfile.ID != "vllm-chat-gpu" {
				t.Fatalf("GPU profile = %q", resource.DesiredSpec.ExecutionProfile.ID)
			}
		}
		return repository.CreateResult{Service: resource, Operation: operation}, nil
	}
	creator := NewCreator(store, &catalogStub{resolved: readyVersion()}, time.Now)
	input := validInput()
	input.Spec.LegacyGPUType = "A100"
	input.Spec.LegacyGPUCountPerPod = 8

	if _, _, err := creator.Create(context.Background(), uuid.New(), input); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	input.IdempotencyKey = uuid.New()
	input.Spec.Accelerator = &domain.Accelerator{SpecID: "gpu-spec-a100", CountPerReplica: 1}
	if _, _, err := creator.Create(context.Background(), uuid.New(), input); err != nil {
		t.Fatalf("GPU Create() error = %v", err)
	}
}

func TestCreateRejectsMissingCompatibleExecutionProfile(t *testing.T) {
	version := readyVersion()
	version.GPUProfile = nil
	creator := NewCreator(&storeStub{}, &catalogStub{resolved: version}, time.Now)
	input := validInput()
	input.Spec.Accelerator = &domain.Accelerator{SpecID: "gpu-spec-a100", CountPerReplica: 1}

	_, _, err := creator.Create(context.Background(), uuid.New(), input)
	if !errors.Is(err, catalog.ErrNoCompatibleProfile) {
		t.Fatalf("error = %v, want ErrNoCompatibleProfile", err)
	}
}

func TestCreateRejectsInvalidResourceAndPlacementCombinations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CreateInput)
	}{
		{name: "empty cpu", mutate: func(input *CreateInput) { input.Spec.CPU = "" }},
		{name: "empty memory", mutate: func(input *CreateInput) { input.Spec.Memory = "" }},
		{name: "unknown placement", mutate: func(input *CreateInput) { input.Spec.PlacementMode = "somewhere" }},
		{name: "distributed cpu", mutate: func(input *CreateInput) { input.Spec.PlacementMode = "multi_node" }},
		{name: "missing image", mutate: func(input *CreateInput) { input.ImageID = ""; input.ImageRef = "" }},
		{name: "negative accelerator memory", mutate: func(input *CreateInput) {
			input.Spec.Accelerator = &domain.Accelerator{SpecID: "gpu-a100", CountPerReplica: 1, MemoryMB: -1}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &storeStub{create: func(domain.Service, domain.Operation) (repository.CreateResult, error) {
				t.Fatal("invalid request must not mutate the database")
				return repository.CreateResult{}, nil
			}}
			input := validInput()
			tt.mutate(&input)

			_, _, err := NewCreator(store, &catalogStub{resolved: readyVersion()}, time.Now).
				Create(context.Background(), uuid.New(), input)
			if err == nil {
				t.Fatal("Create() accepted an invalid resource/placement combination")
			}
			if store.calls != 0 {
				t.Fatalf("store calls = %d, want 0", store.calls)
			}
		})
	}
}

func TestCreateDispatchesCoreBeforeReturning(t *testing.T) {
	store := &storeStub{}
	store.create = func(resource domain.Service, operation domain.Operation) (repository.CreateResult, error) {
		return repository.CreateResult{Service: resource, Operation: operation}, nil
	}
	rt := runtimefake.New()
	resource, operation, err := NewCreator(store, &catalogStub{resolved: readyVersion()}, time.Now).
		WithRuntime(rt).
		Create(context.Background(), uuid.New(), validInput())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if resource.RuntimeRef == uuid.Nil || resource.Status != domain.StatusDeploying {
		t.Fatalf("create must bind runtime before return: %+v", resource)
	}
	if operation.State != domain.OperationPending {
		t.Fatalf("operation must stay pending for worker align: %+v", operation)
	}
	if len(rt.EnsureCalls) != 1 || store.binds != 1 || store.aborts != 0 {
		t.Fatalf("ensure/bind/abort = %d/%d/%d", len(rt.EnsureCalls), store.binds, store.aborts)
	}
	if rt.EnsureCalls[0].IdempotencyKey == uuid.Nil {
		t.Fatal("create dispatch must reuse a stable runtime idempotency key")
	}
}

func TestCreateReturnsCoreCapacityErrorToCaller(t *testing.T) {
	store := &storeStub{create: func(resource domain.Service, operation domain.Operation) (repository.CreateResult, error) {
		return repository.CreateResult{Service: resource, Operation: operation}, nil
	}}
	rt := runtimefake.New()
	rt.EnsureError = runtime.ErrInsufficientCapacity
	_, _, err := NewCreator(store, &catalogStub{resolved: readyVersion()}, time.Now).
		WithRuntime(rt).
		Create(context.Background(), uuid.New(), validInput())
	if !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("Create() error = %v, want ErrInsufficientCapacity", err)
	}
	if store.aborts != 1 || store.binds != 0 {
		t.Fatalf("failed create must abort pending row: binds=%d aborts=%d", store.binds, store.aborts)
	}
}

func TestCreateRejectsUnpinnedImageBeforeCatalog(t *testing.T) {
	store := &storeStub{create: func(domain.Service, domain.Operation) (repository.CreateResult, error) {
		t.Fatal("unpinned image must not persist a service")
		return repository.CreateResult{}, nil
	}}
	catalogPort := &catalogStub{resolved: readyVersion()}
	input := validInput()
	input.ImageRef = "registry.local/user/vllm:latest"
	_, _, err := NewCreator(store, catalogPort, time.Now).Create(context.Background(), uuid.New(), input)
	if !errors.Is(err, ErrImageUnavailable) {
		t.Fatalf("Create() error = %v, want ErrImageUnavailable", err)
	}
	if store.calls != 0 || catalogPort.calls != 0 {
		t.Fatalf("store/catalog calls = %d/%d", store.calls, catalogPort.calls)
	}
}

func TestCreateRequestHashChangesWithImage(t *testing.T) {
	var hashes []string
	store := &storeStub{create: func(resource domain.Service, operation domain.Operation) (repository.CreateResult, error) {
		hashes = append(hashes, operation.RequestHash)
		return repository.CreateResult{Service: resource, Operation: operation}, nil
	}}
	creator := NewCreator(store, &catalogStub{resolved: readyVersion()}, time.Now)
	first := validInput()
	if _, _, err := creator.Create(context.Background(), uuid.New(), first); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	second := validInput()
	second.IdempotencyKey = uuid.New()
	second.ImageRef = "registry.local/user/sglang@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if _, _, err := creator.Create(context.Background(), uuid.New(), second); err != nil {
		t.Fatalf("second Create() error = %v", err)
	}
	if len(hashes) != 2 || hashes[0] == hashes[1] {
		t.Fatalf("image must participate in request identity: %v", hashes)
	}
}
