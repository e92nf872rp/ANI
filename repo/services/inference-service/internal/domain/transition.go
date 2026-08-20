package domain

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var (
	ErrDeleted             = errors.New("inference service is deleted")
	ErrLegacyQuarantined   = errors.New("legacy inference service requires explicit migration")
	ErrInvalidTransition   = errors.New("invalid inference service state transition")
	ErrOperationInProgress = errors.New("inference service operation in progress")
)

// TransitionDisposition 描述 BeginTransition 是新建、重放还是已经是目标态。
type TransitionDisposition string

const (
	TransitionCreated        TransitionDisposition = "created"
	TransitionReuseOperation TransitionDisposition = "reuse_operation"
	TransitionAlreadyDesired TransitionDisposition = "already_desired"
)

type TransitionResult struct {
	Service     Service
	Operation   Operation
	OperationID uuid.UUID
	Disposition TransitionDisposition
}

// BeginTransition 在内存里推进状态机：generation+1、挂上 active operation。
// 不访问 Core。delete 可抢占未完成的 create/scale/start/restart。
func BeginTransition(service Service, action Action, target Spec, operationID uuid.UUID) (TransitionResult, error) {
	if service.LegacyQuarantined {
		return TransitionResult{}, ErrLegacyQuarantined
	}
	if service.DesiredState == DesiredStateDeleted {
		if action == ActionDelete && service.ActiveOperationID != uuid.Nil && service.ActiveOperation == ActionDelete {
			return replayActiveOperation(service, ActionDelete)
		}
		return TransitionResult{}, ErrDeleted
	}
	if action == ActionStop && service.DesiredState == DesiredStateStopped {
		if service.ActiveOperationID != uuid.Nil && service.ActiveOperation == ActionStop {
			return replayActiveOperation(service, ActionStop)
		}
		return TransitionResult{Service: service, Disposition: TransitionAlreadyDesired}, nil
	}
	if action == ActionStart && service.DesiredState == DesiredStateRunning && service.Status == StatusRunning {
		return TransitionResult{Service: service, Disposition: TransitionAlreadyDesired}, nil
	}
	if action == ActionScale && service.DesiredState == DesiredStateRunning && service.Status == StatusRunning &&
		service.ActiveOperationID == uuid.Nil && target.Replicas == service.DesiredSpec.Replicas {
		return TransitionResult{Service: service, Disposition: TransitionAlreadyDesired}, nil
	}
	if operationID == uuid.Nil {
		return TransitionResult{}, fmt.Errorf("%w: operation id is required", ErrInvalidTransition)
	}
	if !actionAllowed(service.Status, action) {
		return TransitionResult{}, fmt.Errorf("%w: cannot %s from %s", ErrInvalidTransition, action, service.Status)
	}

	preempted := uuid.Nil
	if service.ActiveOperationID != uuid.Nil {
		if !canPreempt(action, service.ActiveOperation) {
			return TransitionResult{}, fmt.Errorf("%w: %s", ErrOperationInProgress, service.ActiveOperationID)
		}
		preempted = service.ActiveOperationID
	}

	updated := service
	updated.Generation++
	updated.ActiveOperationID = operationID
	updated.CurrentOperationID = operationID
	updated.ActiveOperation = action
	updated.Status, updated.DesiredState = transitionTarget(action)

	targetSpec := service.DesiredSpec
	if action == ActionCreate || action == ActionScale {
		if target.Replicas < 1 {
			return TransitionResult{}, fmt.Errorf("%w: replicas must be positive", ErrInvalidTransition)
		}
		targetSpec = target
		updated.DesiredSpec = target
	}

	operation := Operation{
		ID:                   operationID,
		TenantID:             service.TenantID,
		ServiceID:            service.ID,
		Type:                 action,
		State:                OperationPending,
		TargetGeneration:     updated.Generation,
		BeforeSpec:           service.AppliedSpec,
		TargetSpec:           targetSpec,
		PreemptedOperationID: preempted,
	}
	return TransitionResult{
		Service: updated, Operation: operation, OperationID: operation.ID, Disposition: TransitionCreated,
	}, nil
}

// replayActiveOperation 把进行中的同类型操作当成幂等重放，不再开新 generation。
func replayActiveOperation(service Service, _ Action) (TransitionResult, error) {
	return TransitionResult{
		Service: service, OperationID: service.ActiveOperationID, Disposition: TransitionReuseOperation,
	}, nil
}

// actionAllowed 限制各 status 上允许的动作。create 只从 pending 出发。
func actionAllowed(status Status, action Action) bool {
	switch action {
	case ActionCreate:
		return status == StatusPending
	case ActionScale:
		return status == StatusRunning || status == StatusDeploying
	case ActionRestart:
		return status == StatusRunning
	case ActionStart:
		return status == StatusStopped
	case ActionStop:
		return status == StatusPending || status == StatusDeploying || status == StatusRunning
	case ActionDelete:
		return status == StatusPending || status == StatusDeploying || status == StatusRunning ||
			status == StatusStopping || status == StatusStopped || status == StatusFailed
	default:
		return false
	}
}

// canPreempt：delete 抢占一切非 delete；stop 可打断 create/scale/start/restart；scale 可打断未完成的 scale。
func canPreempt(next, active Action) bool {
	if next == ActionDelete {
		return active != ActionDelete
	}
	if next == ActionScale && active == ActionScale {
		return true
	}
	if next != ActionStop {
		return false
	}
	switch active {
	case ActionCreate, ActionScale, ActionStart, ActionRestart:
		return true
	default:
		return false
	}
}

// transitionTarget 给出动作对应的过渡 status 和最终 desired_state。
func transitionTarget(action Action) (Status, DesiredState) {
	switch action {
	case ActionCreate:
		return StatusPending, DesiredStateRunning
	case ActionScale, ActionStart, ActionRestart:
		return StatusDeploying, DesiredStateRunning
	case ActionStop:
		return StatusStopping, DesiredStateStopped
	case ActionDelete:
		return StatusStopping, DesiredStateDeleted
	default:
		return StatusFailed, DesiredStateRunning
	}
}
