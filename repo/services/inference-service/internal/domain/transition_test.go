package domain

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestBeginTransitionScaleRequiresRunning(t *testing.T) {
	service := Service{
		ID:           uuid.New(),
		TenantID:     uuid.New(),
		Status:       StatusStopped,
		DesiredState: DesiredStateStopped,
		Generation:   3,
		DesiredSpec:  Spec{Replicas: 1},
		AppliedSpec:  Spec{Replicas: 1},
	}

	_, err := BeginTransition(service, ActionScale, Spec{Replicas: 2}, uuid.New())

	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition, got %v", err)
	}
}

func TestBeginTransitionScalePreemptsInFlightScale(t *testing.T) {
	activeOperationID := uuid.New()
	newOperationID := uuid.New()
	service := Service{
		ID: uuid.New(), TenantID: uuid.New(), Status: StatusDeploying, DesiredState: DesiredStateRunning,
		Generation: 2, DesiredSpec: Spec{Replicas: 2}, AppliedSpec: Spec{Replicas: 1},
		ActiveOperationID: activeOperationID, ActiveOperation: ActionScale,
	}

	result, err := BeginTransition(service, ActionScale, Spec{Replicas: 1}, newOperationID)
	if err != nil {
		t.Fatalf("BeginTransition() error = %v", err)
	}
	if result.Disposition != TransitionCreated || result.Service.DesiredSpec.Replicas != 1 ||
		result.Service.ActiveOperationID != newOperationID || result.Operation.PreemptedOperationID != activeOperationID {
		t.Fatalf("scale did not preempt the in-flight scale: %+v", result)
	}
}

func TestBeginTransitionStopPreemptsCreate(t *testing.T) {
	activeOperationID := uuid.New()
	newOperationID := uuid.New()
	service := Service{
		ID:                uuid.New(),
		TenantID:          uuid.New(),
		Status:            StatusDeploying,
		DesiredState:      DesiredStateRunning,
		Generation:        1,
		DesiredSpec:       Spec{Replicas: 1},
		ActiveOperationID: activeOperationID,
		ActiveOperation:   ActionCreate,
	}

	result, err := BeginTransition(service, ActionStop, Spec{}, newOperationID)
	if err != nil {
		t.Fatalf("BeginTransition() error = %v", err)
	}
	updated, operation := result.Service, result.Operation
	if result.Disposition != TransitionCreated || result.OperationID != newOperationID {
		t.Fatalf("unexpected transition disposition: %+v", result)
	}
	if updated.Generation != 2 || updated.Status != StatusStopping || updated.DesiredState != DesiredStateStopped {
		t.Fatalf("unexpected updated service: %+v", updated)
	}
	if updated.ActiveOperationID != newOperationID || updated.ActiveOperation != ActionStop {
		t.Fatalf("new operation was not made active: %+v", updated)
	}
	if operation.PreemptedOperationID != activeOperationID {
		t.Fatalf("preempted operation = %s, want %s", operation.PreemptedOperationID, activeOperationID)
	}
	if operation.TargetGeneration != 2 || operation.State != OperationPending {
		t.Fatalf("unexpected operation: %+v", operation)
	}
}

func TestBeginTransitionDeleteCannotBeReversed(t *testing.T) {
	service := Service{
		ID:           uuid.New(),
		TenantID:     uuid.New(),
		Status:       StatusStopping,
		DesiredState: DesiredStateDeleted,
		Generation:   4,
	}

	_, err := BeginTransition(service, ActionStart, Spec{}, uuid.New())

	if !errors.Is(err, ErrDeleted) {
		t.Fatalf("expected ErrDeleted, got %v", err)
	}
}

func TestBeginTransitionDeleteReplayDoesNotCreateNewGeneration(t *testing.T) {
	operationID := uuid.New()
	service := Service{
		ID: uuid.New(), TenantID: uuid.New(), Status: StatusStopping,
		DesiredState: DesiredStateDeleted, Generation: 4,
		CurrentOperationID: operationID, ActiveOperationID: operationID, ActiveOperation: ActionDelete,
	}

	result, err := BeginTransition(service, ActionDelete, Spec{}, uuid.New())
	if err != nil {
		t.Fatalf("delete replay error = %v", err)
	}
	if result.Service.Generation != service.Generation || result.OperationID != operationID || result.Disposition != TransitionReuseOperation {
		t.Fatalf("delete replay created a new transition: result=%+v", result)
	}
	if result.Operation.ID != uuid.Nil {
		t.Fatalf("delete replay synthesized an operation: %+v", result.Operation)
	}
}

func TestBeginTransitionStopReplayDoesNotCreateNewGeneration(t *testing.T) {
	operationID := uuid.New()
	service := Service{
		ID: uuid.New(), TenantID: uuid.New(), Status: StatusStopping,
		DesiredState: DesiredStateStopped, Generation: 4,
		CurrentOperationID: operationID, ActiveOperationID: operationID, ActiveOperation: ActionStop,
	}

	result, err := BeginTransition(service, ActionStop, Spec{}, uuid.New())
	if err != nil {
		t.Fatalf("stop replay error = %v", err)
	}
	if result.Service.Generation != service.Generation || result.OperationID != operationID || result.Disposition != TransitionReuseOperation {
		t.Fatalf("stop replay created a new transition: result=%+v", result)
	}
	if result.Operation.ID != uuid.Nil {
		t.Fatalf("stop replay synthesized an operation: %+v", result.Operation)
	}
}

func TestBeginTransitionAlreadyAtDesiredStateIsIdempotent(t *testing.T) {
	tests := []struct {
		name    string
		service Service
		action  Action
	}{
		{
			name: "stop stopped service",
			service: Service{ID: uuid.New(), TenantID: uuid.New(), Status: StatusStopped,
				DesiredState: DesiredStateStopped, Generation: 4},
			action: ActionStop,
		},
		{
			name: "start running service",
			service: Service{ID: uuid.New(), TenantID: uuid.New(), Status: StatusRunning,
				DesiredState: DesiredStateRunning, Generation: 4},
			action: ActionStart,
		},
		{
			name: "scale to current replicas",
			service: Service{ID: uuid.New(), TenantID: uuid.New(), Status: StatusRunning,
				DesiredState: DesiredStateRunning, Generation: 4, DesiredSpec: Spec{Replicas: 2}},
			action: ActionScale,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := Spec{}
			if tt.action == ActionScale {
				target = tt.service.DesiredSpec
			}
			result, err := BeginTransition(tt.service, tt.action, target, uuid.New())
			if err != nil {
				t.Fatalf("idempotent transition error = %v", err)
			}
			if result.Disposition != TransitionAlreadyDesired || result.Service.Generation != tt.service.Generation || result.Operation.ID != uuid.Nil {
				t.Fatalf("idempotent transition mutated state: result=%+v", result)
			}
		})
	}
}

func TestBeginTransitionRejectsLegacyQuarantinedService(t *testing.T) {
	service := Service{
		ID: uuid.New(), TenantID: uuid.New(), Status: StatusStopped,
		DesiredState: DesiredStateStopped, Generation: 1, LegacyQuarantined: true,
	}

	result, err := BeginTransition(service, ActionStart, Spec{}, uuid.New())
	if !errors.Is(err, ErrLegacyQuarantined) {
		t.Fatalf("error = %v, want ErrLegacyQuarantined", err)
	}
	if result.Service.ID != uuid.Nil || result.Operation.ID != uuid.Nil {
		t.Fatalf("quarantined transition returned mutations: result=%+v", result)
	}
}

func TestSpecUsesAcceleratorPresenceInsteadOfLegacyGPUFields(t *testing.T) {
	cpu := Spec{LegacyGPUType: "A100", LegacyGPUCountPerPod: 8}
	if cpu.UsesAccelerator() {
		t.Fatal("legacy GPU compatibility fields must not select GPU execution")
	}

	gpu := Spec{Accelerator: &Accelerator{SpecID: "gpu-spec-a100", CountPerReplica: 1}}
	if !gpu.UsesAccelerator() {
		t.Fatal("accelerator presence must select GPU execution")
	}
}
