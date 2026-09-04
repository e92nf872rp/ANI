package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	"github.com/kubercloud/ani/services/inference-service/internal/runtime"
	runtimefake "github.com/kubercloud/ani/services/inference-service/internal/runtime/fake"
)

type controlStoreStub struct {
	service   domain.Service
	operation domain.Operation
	list      []domain.Service
	mutate    func(repository.MutationRequest) (repository.MutationResult, error)
	aborts    int
	binds     int
}

func (s *controlStoreStub) GetService(context.Context, uuid.UUID, uuid.UUID) (domain.Service, error) {
	return s.service, nil
}

func (s *controlStoreStub) GetOperation(context.Context, uuid.UUID, uuid.UUID) (domain.Operation, error) {
	return s.operation, nil
}

func (s *controlStoreStub) ListServices(context.Context, uuid.UUID) ([]domain.Service, error) {
	return s.list, nil
}

func (s *controlStoreStub) MutateService(_ context.Context, request repository.MutationRequest) (repository.MutationResult, error) {
	return s.mutate(request)
}

func (s *controlStoreStub) BindRuntimeRef(context.Context, repository.RuntimeBinding) error {
	s.binds++
	return nil
}

func (s *controlStoreStub) AbortPendingMutation(context.Context, repository.MutationAbort) error {
	s.aborts++
	return nil
}

func runningControlService() domain.Service {
	return domain.Service{
		ID: uuid.New(), TenantID: uuid.New(), Name: "qwen", ModelVersionID: uuid.New(),
		ServedModelName: "qwen", ModelSnapshot: json.RawMessage(`{"display_name":"Qwen / v1"}`),
		Status: domain.StatusRunning, DesiredState: domain.DesiredStateRunning,
		Generation: 2, ObservedGeneration: 2, DesiredSpec: domain.Spec{
			Replicas: 1, CPU: "4", Memory: "16Gi", PlacementMode: "single_node",
		}, AppliedSpec: domain.Spec{Replicas: 1, CPU: "4", Memory: "16Gi", PlacementMode: "single_node"},
		RuntimeRef: uuid.New(), RuntimeEndpoint: "http://internal.svc:8000",
		CreatedAt: time.Unix(10, 0).UTC(), UpdatedAt: time.Unix(20, 0).UTC(),
	}
}

func TestScaleHashesNormalizedServiceAndReplicaIntent(t *testing.T) {
	resource := runningControlService()
	var hashes []string
	store := &controlStoreStub{service: resource}
	store.mutate = func(request repository.MutationRequest) (repository.MutationResult, error) {
		if request.Action != domain.ActionScale || request.OperationScope != "inference_service.scale" || request.TargetSpec.Replicas != 2 {
			t.Fatalf("unexpected scale request: %+v", request)
		}
		hashes = append(hashes, request.RequestHash)
		return repository.MutationResult{Service: resource, Operation: domain.Operation{ID: request.OperationID}}, nil
	}
	controller := NewController(store, func() time.Time { return time.Unix(30, 0).UTC() })

	if _, err := controller.Scale(context.Background(), resource.TenantID, resource.ID, uuid.New(), 2); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Scale(context.Background(), resource.TenantID, resource.ID, uuid.New(), 2); err != nil {
		t.Fatal(err)
	}
	if len(hashes) != 2 || hashes[0] == "" || hashes[0] != hashes[1] {
		t.Fatalf("scale hashes = %v", hashes)
	}
}

func TestScaleRejectsMultiNodeReplicaChangeBeforeMutation(t *testing.T) {
	resource := runningControlService()
	resource.DesiredSpec.PlacementMode = "multi_node"
	store := &controlStoreStub{service: resource, mutate: func(repository.MutationRequest) (repository.MutationResult, error) {
		t.Fatal("multi-node replica change must not reach mutation")
		return repository.MutationResult{}, nil
	}}

	_, err := NewController(store, time.Now).Scale(context.Background(), resource.TenantID, resource.ID, uuid.New(), 2)
	if err == nil {
		t.Fatal("multi-node replica change was accepted")
	}
}

func TestLifecycleReturnsRealOperationForActiveStopReplay(t *testing.T) {
	resource := runningControlService()
	realOperation := domain.Operation{
		ID: uuid.New(), TenantID: resource.TenantID, ServiceID: resource.ID,
		Type: domain.ActionStop, State: domain.OperationPending,
		IdempotencyKey: uuid.New(), RequestHash: "sha256:real", CreatedAt: time.Unix(20, 0).UTC(),
	}
	store := &controlStoreStub{service: resource, mutate: func(repository.MutationRequest) (repository.MutationResult, error) {
		return repository.MutationResult{
			Service: resource, Operation: realOperation, Disposition: domain.TransitionReuseOperation,
		}, nil
	}}

	result, err := NewController(store, time.Now).Lifecycle(
		context.Background(), resource.TenantID, resource.ID, uuid.New(), domain.ActionStop,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != realOperation.ID || result.State != realOperation.State || result.RequestHash != realOperation.RequestHash {
		t.Fatalf("lifecycle did not return persisted operation: %+v", result)
	}
}

func TestLifecycleReturnsPersistedCompletedNoop(t *testing.T) {
	resource := runningControlService()
	completed := domain.Operation{ID: uuid.New(), Type: domain.ActionStart, State: domain.OperationCompleted}
	store := &controlStoreStub{service: resource, mutate: func(repository.MutationRequest) (repository.MutationResult, error) {
		return repository.MutationResult{
			Service: resource, Operation: completed, Disposition: domain.TransitionAlreadyDesired,
		}, nil
	}}

	result, err := NewController(store, time.Now).Lifecycle(
		context.Background(), resource.TenantID, resource.ID, uuid.New(), domain.ActionStart,
	)
	if err != nil || result.ID != completed.ID || result.State != domain.OperationCompleted {
		t.Fatalf("completed no-op = (%+v,%v)", result, err)
	}
}

func TestStopRestartDeleteDoNotTouchRuntimeBeforePublisherWithdraws(t *testing.T) {
	for _, action := range []domain.Action{domain.ActionStop, domain.ActionRestart, domain.ActionDelete} {
		t.Run(string(action), func(t *testing.T) {
			resource := runningControlService()
			store := &controlStoreStub{service: resource}
			store.mutate = func(request repository.MutationRequest) (repository.MutationResult, error) {
				updated := resource
				updated.Generation++
				updated.CurrentOperationID = request.OperationID
				operation := domain.Operation{
					ID: request.OperationID, TenantID: resource.TenantID, ServiceID: resource.ID,
					Type: request.Action, State: domain.OperationPending,
					TargetGeneration: updated.Generation, TargetSpec: resource.DesiredSpec,
				}
				return repository.MutationResult{
					Service: updated, Operation: operation, Disposition: domain.TransitionCreated,
				}, nil
			}
			rt := runtimefake.New()
			controller := NewController(store, time.Now).WithRuntime(rt)

			var err error
			if action == domain.ActionDelete {
				_, err = controller.Delete(context.Background(), resource.TenantID, resource.ID)
			} else {
				_, err = controller.Lifecycle(context.Background(), resource.TenantID, resource.ID, uuid.New(), action)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(rt.EnsureCalls) != 0 || len(rt.LifecycleCalls) != 0 || len(rt.DeleteCalls) != 0 {
				t.Fatalf("request path touched runtime before withdrawal: ensure=%d lifecycle=%d delete=%d",
					len(rt.EnsureCalls), len(rt.LifecycleCalls), len(rt.DeleteCalls))
			}
		})
	}
}

func TestLifecyclePropagatesClaimedOperationConflict(t *testing.T) {
	resource := runningControlService()
	store := &controlStoreStub{service: resource, mutate: func(repository.MutationRequest) (repository.MutationResult, error) {
		return repository.MutationResult{}, fmt.Errorf("preempt active operation: %w", domain.ErrOperationInProgress)
	}}

	_, err := NewController(store, time.Now).Lifecycle(
		context.Background(), resource.TenantID, resource.ID, uuid.New(), domain.ActionStop,
	)
	if !errors.Is(err, domain.ErrOperationInProgress) {
		t.Fatalf("Lifecycle() error = %v, want ErrOperationInProgress", err)
	}
}

func TestDeleteUsesServiceDesiredStateForDeduplication(t *testing.T) {
	resource := runningControlService()
	var keys []uuid.UUID
	store := &controlStoreStub{service: resource, mutate: func(request repository.MutationRequest) (repository.MutationResult, error) {
		if request.Action != domain.ActionDelete || request.OperationScope != "inference_service.delete" {
			t.Fatalf("unexpected delete request: %+v", request)
		}
		keys = append(keys, request.IdempotencyKey)
		return repository.MutationResult{Operation: domain.Operation{ID: request.OperationID}}, nil
	}}
	controller := NewController(store, time.Now)

	if _, err := controller.Delete(context.Background(), resource.TenantID, resource.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Delete(context.Background(), resource.TenantID, resource.ID); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] == uuid.Nil || keys[0] != keys[1] {
		t.Fatalf("delete deduplication keys = %v", keys)
	}
}

func TestQueriesNeverProjectRuntimeEndpoint(t *testing.T) {
	resource := runningControlService()
	operation := domain.Operation{ID: uuid.New(), RuntimeTaskID: "core-task-secret"}
	operation.IdempotencyKey = uuid.New()
	store := &controlStoreStub{service: resource, operation: operation, list: []domain.Service{resource}}
	controller := NewController(store, time.Now)

	view, err := controller.Get(context.Background(), resource.TenantID, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	operationView, err := controller.GetOperation(context.Background(), resource.TenantID, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(struct {
		Service   ServiceView   `json:"service"`
		Operation OperationView `json:"operation"`
	}{view, operationView})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"internal.svc", "runtime_ref", "runtime_endpoint", "core-task-secret", "runtime_task_id"} {
		if stringContains(string(encoded), forbidden) {
			t.Fatalf("public query projection contains %q: %s", forbidden, encoded)
		}
	}
	if view.EndpointURL != nil || view.InvocationURL != nil {
		t.Fatalf("P0 public URLs must be null: %+v", view)
	}
	if operationView.IdempotencyKey != operation.IdempotencyKey.String() {
		t.Fatalf("operation idempotency key = %q", operationView.IdempotencyKey)
	}
}

func TestProjectServicePublishesOnlyPublicInvocationURL(t *testing.T) {
	resource := runningControlService()
	resource.InvocationURL = " https://ai.example.com/v1/chat/completions "
	view := ProjectService(resource)
	if view.InvocationURL == nil || *view.InvocationURL != "https://ai.example.com/v1/chat/completions" {
		t.Fatalf("invocation_url = %v", view.InvocationURL)
	}
	if view.EndpointURL != nil {
		t.Fatalf("endpoint_url leaked: %v", view.EndpointURL)
	}
}

func TestProjectOperationIncludesStableErrorCode(t *testing.T) {
	operation := domain.Operation{
		ID: uuid.New(), ServiceID: uuid.New(), State: domain.OperationFailed,
		ErrorCode: "SCALE_ROLLED_BACK", ErrorMessage: "inference scale rolled back to the previously applied spec",
		IdempotencyKey: uuid.New(),
	}
	view := ProjectOperation(operation)
	if view.ErrorMessage == nil || *view.ErrorMessage != "SCALE_ROLLED_BACK: inference scale rolled back to the previously applied spec" {
		t.Fatalf("operation error_message = %v", view.ErrorMessage)
	}
	if view.Status != domain.OperationFailed {
		t.Fatalf("operation status = %s", view.Status)
	}
}

func TestScaleDispatchesCoreAndReturnsCapacityError(t *testing.T) {
	resource := runningControlService()
	store := &controlStoreStub{service: resource}
	store.mutate = func(request repository.MutationRequest) (repository.MutationResult, error) {
		operation := domain.Operation{
			ID: request.OperationID, TenantID: resource.TenantID, ServiceID: resource.ID,
			Type: domain.ActionScale, State: domain.OperationPending,
			TargetGeneration: resource.Generation + 1, TargetSpec: request.TargetSpec,
		}
		updated := resource
		updated.Generation = operation.TargetGeneration
		updated.Status = domain.StatusDeploying
		updated.DesiredSpec = request.TargetSpec
		return repository.MutationResult{
			Service: updated, Operation: operation, Disposition: domain.TransitionCreated,
		}, nil
	}
	rt := runtimefake.New()
	rt.EnsureError = runtime.ErrInsufficientCapacity
	_, err := NewController(store, time.Now).WithRuntime(rt).
		Scale(context.Background(), resource.TenantID, resource.ID, uuid.New(), 2)
	if !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("Scale() error = %v, want ErrInsufficientCapacity", err)
	}
	if store.aborts != 1 {
		t.Fatalf("failed scale must revert pending mutation, aborts=%d", store.aborts)
	}
}

func stringContains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
