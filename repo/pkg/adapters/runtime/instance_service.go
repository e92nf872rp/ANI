package runtime

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type LocalInstanceService struct {
	orchestrator ports.WorkloadInstanceOrchestrator
	store        ports.WorkloadInstanceStore
	operations   ports.WorkloadOperationStore
	lifecycle    ports.WorkloadInstanceLifecycleExecutor
	identity     ports.WorkloadIdentityService
	sandbox      ports.SandboxRuntime
	ops          ports.WorkloadInstanceOps
}

type InstanceServiceOption func(*LocalInstanceService)

func WithInstanceLifecycleExecutor(lifecycle ports.WorkloadInstanceLifecycleExecutor) InstanceServiceOption {
	return func(service *LocalInstanceService) {
		service.lifecycle = lifecycle
	}
}

func WithOperationStore(operations ports.WorkloadOperationStore) InstanceServiceOption {
	return func(service *LocalInstanceService) {
		service.operations = operations
	}
}

func WithWorkloadIdentityService(identity ports.WorkloadIdentityService) InstanceServiceOption {
	return func(service *LocalInstanceService) {
		service.identity = identity
	}
}

func WithSandboxRuntime(sandbox ports.SandboxRuntime) InstanceServiceOption {
	return func(service *LocalInstanceService) {
		service.sandbox = sandbox
	}
}

func NewLocalInstanceService(orchestrator ports.WorkloadInstanceOrchestrator, store ports.WorkloadInstanceStore, ops ports.WorkloadInstanceOps) *LocalInstanceService {
	return &LocalInstanceService{
		orchestrator: orchestrator,
		store:        store,
		ops:          ops,
	}
}

func NewLocalInstanceServiceWithOptions(orchestrator ports.WorkloadInstanceOrchestrator, store ports.WorkloadInstanceStore, ops ports.WorkloadInstanceOps, options ...InstanceServiceOption) *LocalInstanceService {
	service := NewLocalInstanceService(orchestrator, store, ops)
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *LocalInstanceService) Create(ctx context.Context, request ports.WorkloadInstanceCreateRequest) (ports.WorkloadInstanceCreateResult, error) {
	if s.orchestrator == nil {
		return ports.WorkloadInstanceCreateResult{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(request.Spec.TenantID) == "" {
		return ports.WorkloadInstanceCreateResult{}, fmt.Errorf("%w: tenantID is required", ports.ErrInvalid)
	}
	if request.Spec.Kind != ports.WorkloadKindVM &&
		request.Spec.Kind != ports.WorkloadKindContainer &&
		request.Spec.Kind != ports.WorkloadKindGPUContainer &&
		request.Spec.Kind != ports.WorkloadKindSandbox {
		return ports.WorkloadInstanceCreateResult{}, fmt.Errorf("%w: instance service supports vm, container, gpu_container, and sandbox create", ports.ErrUnsupported)
	}
	if strings.TrimSpace(request.UserID) == "" {
		return ports.WorkloadInstanceCreateResult{}, fmt.Errorf("%w: user id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.PermissionProof) == "" {
		return ports.WorkloadInstanceCreateResult{}, fmt.Errorf("%w: permission proof is required", ports.ErrInvalid)
	}
	var operation ports.WorkloadOperationRecord
	preRecorded := false
	if s.operations != nil && strings.TrimSpace(request.IdempotencyKey) != "" {
		opID := uuid.NewString()
		var existing bool
		var err error
		operation, existing, err = s.operations.RecordOperation(ctx, ports.WorkloadOperationRecord{
			ID:             opID,
			TenantID:       request.Spec.TenantID,
			InstanceID:     pendingOperationInstanceID(opID),
			Operation:      ports.WorkloadLifecycleCreate,
			Status:         ports.WorkloadOperationInProgress,
			IdempotencyKey: request.IdempotencyKey,
			RequestedBy:    request.UserID,
			Precheck:       map[string]any{"allowed": true},
			AfterSpec:      workloadSpecSummary(request.Spec),
			CreatedAt:      firstNonZeroTime(request.RequestedAt),
			UpdatedAt:      firstNonZeroTime(request.RequestedAt),
		})
		if err != nil {
			return ports.WorkloadInstanceCreateResult{}, err
		}
		if existing {
			return ports.WorkloadInstanceCreateResult{
				Ref: ports.WorkloadRef{
					TenantID:   operation.TenantID,
					InstanceID: operation.InstanceID,
					Kind:       request.Spec.Kind,
				},
				OperationID:      operation.ID,
				IdempotentReplay: true,
			}, nil
		}
		preRecorded = true
	}
	if request.Spec.Kind == ports.WorkloadKindSandbox && s.sandbox != nil {
		return s.createSandbox(ctx, request, operation, preRecorded)
	}
	result, err := s.orchestrator.Create(ctx, request)
	if err != nil {
		if preRecorded {
			_, _ = s.operations.UpdateOperation(ctx, operation.ID, ports.WorkloadOperationUpdate{
				Status:         ports.WorkloadOperationFailed,
				FailureReason:  "create_failed",
				FailureMessage: err.Error(),
				RetryEligible:  true,
				UpdatedAt:      firstNonZeroTime(request.RequestedAt),
			})
		}
		return ports.WorkloadInstanceCreateResult{}, err
	}
	if s.operations == nil {
		return result, nil
	}
	if !preRecorded {
		operation, _, err = s.operations.RecordOperation(ctx, ports.WorkloadOperationRecord{
			TenantID:       result.Ref.TenantID,
			InstanceID:     result.Ref.InstanceID,
			Operation:      ports.WorkloadLifecycleCreate,
			Status:         ports.WorkloadOperationInProgress,
			IdempotencyKey: request.IdempotencyKey,
			RequestedBy:    request.UserID,
			Precheck:       map[string]any{"allowed": true},
			AfterSpec:      workloadSpecSummary(request.Spec),
			ProviderRefs:   result.Apply.ResourceRefs,
			CreatedAt:      firstNonZeroTime(request.RequestedAt),
			UpdatedAt:      firstNonZeroTime(request.RequestedAt),
		})
		if err != nil {
			return ports.WorkloadInstanceCreateResult{}, err
		}
	}
	result.OperationID = operation.ID
	if err := s.recordCreateTimeline(ctx, operation.ID, result); err != nil {
		return ports.WorkloadInstanceCreateResult{}, err
	}
	if s.identity != nil && result.Identity == nil {
		identity, err := s.identity.BindScopedKey(ctx, ports.WorkloadIdentityBindRequest{
			TenantID:     result.Ref.TenantID,
			InstanceID:   result.Ref.InstanceID,
			InstanceName: request.Spec.Name,
			Kind:         result.Ref.Kind,
			UserID:       request.UserID,
			RequestedAt:  firstNonZeroTime(request.RequestedAt, result.FinalStatus.UpdatedAt),
		})
		if err != nil {
			_, _ = s.operations.AddOperationStep(ctx, operation.ID, ports.WorkloadOperationStep{
				StepName: "workload_identity_bind",
				Status:   ports.WorkloadOperationStepFailed,
				Message:  err.Error(),
			})
			_, _ = s.operations.UpdateOperation(ctx, operation.ID, ports.WorkloadOperationUpdate{
				InstanceID:     result.Ref.InstanceID,
				Status:         ports.WorkloadOperationFailed,
				FailureReason:  "workload_identity_bind_failed",
				FailureMessage: err.Error(),
				RetryEligible:  true,
				UpdatedAt:      firstNonZeroTime(request.RequestedAt, result.FinalStatus.UpdatedAt),
			})
			return ports.WorkloadInstanceCreateResult{}, err
		}
		result.Identity = &identity
	}
	if result.Identity != nil {
		if _, err := s.operations.AddOperationStep(ctx, operation.ID, ports.WorkloadOperationStep{
			StepName: "workload_identity_bind",
			Status:   ports.WorkloadOperationStepSucceeded,
			Message:  "scoped api key " + result.Identity.KeyPrefix + " bound to instance",
		}); err != nil {
			return ports.WorkloadInstanceCreateResult{}, err
		}
	}
	if _, err := s.operations.UpdateOperation(ctx, operation.ID, ports.WorkloadOperationUpdate{
		InstanceID:   result.Ref.InstanceID,
		Status:       ports.WorkloadOperationSucceeded,
		ProviderRefs: result.Apply.ResourceRefs,
		UpdatedAt:    firstNonZeroTime(result.FinalStatus.UpdatedAt, request.RequestedAt),
	}); err != nil {
		return ports.WorkloadInstanceCreateResult{}, err
	}
	return result, nil
}

func (s *LocalInstanceService) createSandbox(ctx context.Context, request ports.WorkloadInstanceCreateRequest, operation ports.WorkloadOperationRecord, preRecorded bool) (ports.WorkloadInstanceCreateResult, error) {
	if s.sandbox == nil {
		return ports.WorkloadInstanceCreateResult{}, ports.ErrNotConfigured
	}
	if s.store == nil {
		return ports.WorkloadInstanceCreateResult{}, ports.ErrNotConfigured
	}
	instance, err := s.sandbox.Create(ctx, ports.SandboxCreateRequest{
		TenantID:  request.Spec.TenantID,
		Name:      request.Spec.Name,
		Image:     request.Spec.Image,
		Config:    firstNonNilSandboxConfig(request.Spec.Sandbox),
		AutoStart: request.Spec.Lifecycle.AutoStart,
		CreatedAt: request.RequestedAt,
	})
	if err != nil {
		if preRecorded {
			_, _ = s.operations.UpdateOperation(ctx, operation.ID, ports.WorkloadOperationUpdate{
				Status:         ports.WorkloadOperationFailed,
				FailureReason:  "sandbox_create_failed",
				FailureMessage: err.Error(),
				RetryEligible:  true,
				UpdatedAt:      firstNonZeroTime(request.RequestedAt),
			})
		}
		return ports.WorkloadInstanceCreateResult{}, err
	}
	ref := ports.WorkloadRef{
		TenantID:   instance.TenantID,
		InstanceID: instance.InstanceID,
		Kind:       ports.WorkloadKindSandbox,
		ProviderID: instance.Provider + "/" + instance.InstanceID,
	}
	status := ports.WorkloadStatus{
		Ref:       ref,
		State:     workloadStateFromSandboxState(instance.State),
		Reason:    string(instance.State),
		UpdatedAt: instance.UpdatedAt,
	}
	vpcID, subnetID, privateIP := selectedNetworkFields(request.Spec.Network.Attachments)
	record := ports.WorkloadInstanceRecord{
		TenantID:     instance.TenantID,
		InstanceID:   instance.InstanceID,
		Name:         instance.Name,
		Kind:         ports.WorkloadKindSandbox,
		VPCID:        vpcID,
		SubnetID:     subnetID,
		PrivateIP:    privateIP,
		Provider:     instance.Provider,
		Lifecycle:    request.Spec.Lifecycle,
		Sandbox:      &instance,
		ResourceRefs: []string{"sandbox/local/" + instance.InstanceID},
		Status:       status,
		CreatedAt:    instance.CreatedAt,
		UpdatedAt:    instance.UpdatedAt,
	}
	if preRecorded {
		record.OperationID = operation.ID
	}
	if err := s.store.UpsertStatus(ctx, record); err != nil {
		return ports.WorkloadInstanceCreateResult{}, err
	}

	result := ports.WorkloadInstanceCreateResult{
		Ref:         ref,
		OperationID: record.OperationID,
		AuditID:     "sandbox_local_" + instance.InstanceID,
		Apply: ports.WorkloadProviderApplyResult{
			Applied:      true,
			Reason:       "local sandbox profile created",
			ResourceRefs: record.ResourceRefs,
			AppliedAt:    instance.UpdatedAt,
		},
		FinalStatus:  status,
		Orchestrated: true,
	}
	if preRecorded {
		if _, err := s.operations.AddOperationStep(ctx, operation.ID, ports.WorkloadOperationStep{
			StepName: "sandbox_runtime_create",
			Status:   ports.WorkloadOperationStepSucceeded,
			Message:  "local sandbox runtime accepted Kata profile intent",
		}); err != nil {
			return ports.WorkloadInstanceCreateResult{}, err
		}
		if _, err := s.operations.UpdateOperation(ctx, operation.ID, ports.WorkloadOperationUpdate{
			InstanceID:   instance.InstanceID,
			Status:       ports.WorkloadOperationSucceeded,
			ProviderRefs: record.ResourceRefs,
			UpdatedAt:    instance.UpdatedAt,
		}); err != nil {
			return ports.WorkloadInstanceCreateResult{}, err
		}
	}
	return result, nil
}

func (s *LocalInstanceService) Get(ctx context.Context, request ports.WorkloadInstanceGetRequest) (ports.WorkloadInstanceRecord, error) {
	if s.store == nil {
		return ports.WorkloadInstanceRecord{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(request.TenantID) == "" {
		return ports.WorkloadInstanceRecord{}, fmt.Errorf("%w: tenantID is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.InstanceID) == "" {
		return ports.WorkloadInstanceRecord{}, fmt.Errorf("%w: instanceID is required", ports.ErrInvalid)
	}
	record, err := s.store.Get(ctx, request.TenantID, request.InstanceID)
	if err != nil {
		return ports.WorkloadInstanceRecord{}, err
	}
	return s.withIdentity(ctx, record), nil
}

func (s *LocalInstanceService) List(ctx context.Context, request ports.WorkloadInstanceListRequest) ([]ports.WorkloadInstanceRecord, error) {
	if s.store == nil {
		return nil, ports.ErrNotConfigured
	}
	if strings.TrimSpace(request.TenantID) == "" {
		return nil, fmt.Errorf("%w: tenantID is required", ports.ErrInvalid)
	}
	records, err := s.store.List(ctx, request.TenantID, request.Kind)
	if err != nil {
		return nil, err
	}
	for i := range records {
		records[i] = s.withIdentity(ctx, records[i])
	}
	return records, nil
}

func (s *LocalInstanceService) Start(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest) (ports.WorkloadInstanceRecord, error) {
	request.Action = ports.WorkloadLifecycleStart
	return s.applyLifecycle(ctx, request)
}

func (s *LocalInstanceService) Stop(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest) (ports.WorkloadInstanceRecord, error) {
	request.Action = ports.WorkloadLifecycleStop
	return s.applyLifecycle(ctx, request)
}

func (s *LocalInstanceService) Restart(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest) (ports.WorkloadInstanceRecord, error) {
	request.Action = ports.WorkloadLifecycleRestart
	return s.applyLifecycle(ctx, request)
}

func (s *LocalInstanceService) Resize(ctx context.Context, request ports.WorkloadInstanceResizeRequest) (ports.WorkloadInstanceRecord, error) {
	lifecycle := ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  request.IdempotencyKey,
		TenantID:        request.TenantID,
		InstanceID:      request.InstanceID,
		Action:          ports.WorkloadLifecycleResize,
		UserID:          request.UserID,
		PermissionProof: request.PermissionProof,
		RequestedAt:     request.RequestedAt,
	}
	return s.applyLifecycle(ctx, lifecycle)
}

func (s *LocalInstanceService) Delete(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest) (ports.WorkloadInstanceRecord, error) {
	request.Action = ports.WorkloadLifecycleDelete
	return s.applyLifecycle(ctx, request)
}

func (s *LocalInstanceService) Snapshot(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest) (ports.WorkloadInstanceRecord, error) {
	request.Action = ports.WorkloadLifecycleSnapshot
	return s.applyLifecycle(ctx, request)
}

func (s *LocalInstanceService) AttachVolume(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest) (ports.WorkloadInstanceRecord, error) {
	request.Action = ports.WorkloadLifecycleAttachVolume
	return s.applyLifecycle(ctx, request)
}

func (s *LocalInstanceService) DetachVolume(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest) (ports.WorkloadInstanceRecord, error) {
	request.Action = ports.WorkloadLifecycleDetachVolume
	return s.applyLifecycle(ctx, request)
}

func (s *LocalInstanceService) Rollback(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest) (ports.WorkloadInstanceRecord, error) {
	request.Action = ports.WorkloadLifecycleRollback
	return s.applyLifecycle(ctx, request)
}

func (s *LocalInstanceService) Ops(ctx context.Context, request ports.WorkloadInstanceOpsRequest) (ports.WorkloadInstanceOpsResult, error) {
	if s.store == nil || s.ops == nil {
		return ports.WorkloadInstanceOpsResult{}, ports.ErrNotConfigured
	}
	record, err := s.Get(ctx, ports.WorkloadInstanceGetRequest{
		TenantID:   request.TenantID,
		InstanceID: request.InstanceID,
	})
	if err != nil {
		return ports.WorkloadInstanceOpsResult{}, err
	}
	result, err := s.ops.Run(ctx, request, record)
	if err != nil {
		return ports.WorkloadInstanceOpsResult{}, err
	}
	if s.operations != nil && isSessionOpsAction(request.Action) {
		operation, _, err := s.operations.RecordOperation(ctx, ports.WorkloadOperationRecord{
			TenantID:    request.TenantID,
			InstanceID:  request.InstanceID,
			Operation:   ports.WorkloadLifecycleConsoleSession,
			Status:      opsOperationStatus(result.Accepted),
			RequestedBy: request.UserID,
			Precheck: map[string]any{
				"allowed":  true,
				"action":   string(request.Action),
				"protocol": result.Protocol,
			},
			DestructiveImpact: map[string]any{
				"read_only":           true,
				"opens_remote_access": true,
			},
			BeforeSpec: workloadRecordSummary(record),
			AfterSpec: map[string]any{
				"session_id":  result.SessionID,
				"protocol":    result.Protocol,
				"connect_url": result.ConnectURL,
				"url":         result.URL,
				"expires_at":  result.ExpiresAt.Format(time.RFC3339),
			},
			FailureReason:  opsFailureReason(result),
			FailureMessage: opsFailureMessage(result),
			RetryEligible:  !result.Accepted,
			CreatedAt:      firstNonZeroTime(request.RequestedAt, result.CheckedAt),
			UpdatedAt:      firstNonZeroTime(result.CheckedAt, request.RequestedAt),
		})
		if err != nil {
			return ports.WorkloadInstanceOpsResult{}, err
		}
		result.OperationID = operation.ID
		if _, err := s.operations.AddOperationStep(ctx, operation.ID, ports.WorkloadOperationStep{
			StepName:    "issue_session",
			Status:      opsStepStatus(result.Accepted),
			Message:     result.Reason,
			StartedAt:   firstNonZeroTime(result.CheckedAt, request.RequestedAt),
			CompletedAt: firstNonZeroTime(result.CheckedAt, request.RequestedAt),
		}); err != nil {
			return ports.WorkloadInstanceOpsResult{}, err
		}
	}
	return result, nil
}

func (s *LocalInstanceService) applyLifecycle(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest) (ports.WorkloadInstanceRecord, error) {
	if s.store == nil {
		return ports.WorkloadInstanceRecord{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.InstanceID) == "" {
		return ports.WorkloadInstanceRecord{}, fmt.Errorf("%w: tenantID and instanceID are required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.UserID) == "" || strings.TrimSpace(request.PermissionProof) == "" {
		return ports.WorkloadInstanceRecord{}, fmt.Errorf("%w: user id and permission proof are required", ports.ErrInvalid)
	}
	record, err := s.store.Get(ctx, request.TenantID, request.InstanceID)
	if err != nil {
		return ports.WorkloadInstanceRecord{}, err
	}
	if s.operations != nil {
		if strings.TrimSpace(request.IdempotencyKey) != "" {
			existing, err := s.operations.GetOperationByIdempotencyKey(ctx, request.TenantID, request.IdempotencyKey)
			if err == nil {
				record.OperationID = existing.ID
				return record, nil
			}
		}
	}
	next, err := transition(record.Status.State, request.Action)
	if err != nil {
		return ports.WorkloadInstanceRecord{}, err
	}
	precheck := lifecyclePrecheck(record, request, next)
	snapshot := vmSnapshotFor(record, request)
	volume := volumeAttachmentFor(request)
	rollback := containerRollbackFor(record, request)
	opID := ""
	if s.operations != nil {
		status := ports.WorkloadOperationInProgress
		if !precheck.allowed {
			status = ports.WorkloadOperationFailed
		}
		operation, existing, err := s.operations.RecordOperation(ctx, ports.WorkloadOperationRecord{
			TenantID:          request.TenantID,
			InstanceID:        request.InstanceID,
			Operation:         request.Action,
			Status:            status,
			IdempotencyKey:    request.IdempotencyKey,
			RequestedBy:       request.UserID,
			Precheck:          precheck.details,
			DestructiveImpact: lifecycleDestructiveImpact(record, request.Action),
			BeforeSpec:        workloadRecordSummary(record),
			AfterSpec:         lifecycleAfterSpec(record, request, next, snapshot, rollback),
			FailureReason:     precheck.failureReason,
			FailureMessage:    precheck.message,
			RetryEligible:     precheck.retryEligible,
			CreatedAt:         request.RequestedAt,
			UpdatedAt:         request.RequestedAt,
		})
		if err != nil {
			return ports.WorkloadInstanceRecord{}, err
		}
		opID = operation.ID
		if existing {
			record.OperationID = opID
			return record, nil
		}
		if !precheck.allowed {
			if _, err := s.operations.AddOperationStep(ctx, opID, ports.WorkloadOperationStep{
				StepName: "precheck",
				Status:   ports.WorkloadOperationStepFailed,
				Message:  precheck.message,
			}); err != nil {
				return ports.WorkloadInstanceRecord{}, err
			}
			return ports.WorkloadInstanceRecord{}, fmt.Errorf("%w: %s", ports.ErrConflict, precheck.message)
		}
		if _, err := s.operations.AddOperationStep(ctx, opID, ports.WorkloadOperationStep{
			StepName: "precheck",
			Status:   ports.WorkloadOperationStepSucceeded,
			Message:  "lifecycle transition accepted",
		}); err != nil {
			return ports.WorkloadInstanceRecord{}, err
		}
	}
	if !precheck.allowed {
		return ports.WorkloadInstanceRecord{}, fmt.Errorf("%w: %s", ports.ErrConflict, precheck.message)
	}
	if s.lifecycle != nil && usesProviderLifecycleForRecord(request.Action, record, request) {
		result, err := s.lifecycle.Apply(ctx, request, record)
		if err != nil {
			if opID != "" {
				_, _ = s.operations.UpdateOperation(ctx, opID, ports.WorkloadOperationUpdate{
					Status:         ports.WorkloadOperationFailed,
					FailureReason:  "provider_lifecycle_failed",
					FailureMessage: err.Error(),
					RetryEligible:  true,
					UpdatedAt:      request.RequestedAt,
				})
			}
			return ports.WorkloadInstanceRecord{}, err
		}
		if result.OperationID != "" {
			opID = result.OperationID
		}
		if !result.Accepted {
			record.Status.Reason = result.Reason
			if !result.CheckedAt.IsZero() {
				record.Status.UpdatedAt = result.CheckedAt.UTC()
				record.UpdatedAt = result.CheckedAt.UTC()
			}
			if err := s.store.UpsertStatus(ctx, record); err != nil {
				return ports.WorkloadInstanceRecord{}, err
			}
			record.OperationID = opID
			if opID != "" {
				_, _ = s.operations.UpdateOperation(ctx, opID, ports.WorkloadOperationUpdate{
					Status:         ports.WorkloadOperationFailed,
					FailureReason:  "provider_lifecycle_rejected",
					FailureMessage: result.Reason,
					RetryEligible:  true,
					UpdatedAt:      record.UpdatedAt,
				})
			}
			return record, nil
		}
	}
	if snapshot != nil {
		record.Snapshots = append(record.Snapshots, *snapshot)
	}
	record.Status.Storage = applyVolumeBinding(record.Status.Storage, request.Action, volume, request.VolumeID)
	if rollback != nil {
		record.Container = rollback
	}
	record.Status.State = next
	record.Status.Reason = "lifecycle " + string(request.Action) + " requested"
	if !request.RequestedAt.IsZero() {
		record.Status.UpdatedAt = request.RequestedAt.UTC()
		record.UpdatedAt = request.RequestedAt.UTC()
	}
	if err := s.store.UpsertStatus(ctx, record); err != nil {
		return ports.WorkloadInstanceRecord{}, err
	}
	record.OperationID = opID
	if opID != "" {
		if _, err := s.operations.AddOperationStep(ctx, opID, ports.WorkloadOperationStep{
			StepName: lifecycleApplyStepName(request.Action),
			Status:   ports.WorkloadOperationStepSucceeded,
			Message:  "lifecycle " + string(request.Action) + " accepted",
		}); err != nil {
			return ports.WorkloadInstanceRecord{}, err
		}
		if request.Action == ports.WorkloadLifecycleDelete && s.identity != nil {
			identity, err := s.identity.RevokeForInstance(ctx, ports.WorkloadIdentityRevokeRequest{
				TenantID:    request.TenantID,
				InstanceID:  request.InstanceID,
				RequestedAt: firstNonZeroTime(request.RequestedAt, record.UpdatedAt),
			})
			if err != nil {
				_, _ = s.operations.AddOperationStep(ctx, opID, ports.WorkloadOperationStep{
					StepName: "workload_identity_revoke",
					Status:   ports.WorkloadOperationStepFailed,
					Message:  err.Error(),
				})
				_, _ = s.operations.UpdateOperation(ctx, opID, ports.WorkloadOperationUpdate{
					Status:         ports.WorkloadOperationFailed,
					FailureReason:  "workload_identity_revoke_failed",
					FailureMessage: err.Error(),
					RetryEligible:  true,
					UpdatedAt:      firstNonZeroTime(request.RequestedAt, record.UpdatedAt),
				})
				return ports.WorkloadInstanceRecord{}, err
			}
			record.Identity = &identity
			message := "scoped api key revoked"
			if identity.KeyPrefix != "" {
				message = "scoped api key " + identity.KeyPrefix + " revoked"
			}
			if _, err := s.operations.AddOperationStep(ctx, opID, ports.WorkloadOperationStep{
				StepName: "workload_identity_revoke",
				Status:   ports.WorkloadOperationStepSucceeded,
				Message:  message,
			}); err != nil {
				return ports.WorkloadInstanceRecord{}, err
			}
		}
		if _, err := s.operations.UpdateOperation(ctx, opID, ports.WorkloadOperationUpdate{
			Status:       ports.WorkloadOperationSucceeded,
			UpdatedAt:    record.UpdatedAt,
			ProviderRefs: record.ResourceRefs,
		}); err != nil {
			return ports.WorkloadInstanceRecord{}, err
		}
	}
	return record, nil
}

var _ ports.WorkloadInstanceService = (*LocalInstanceService)(nil)

func (s *LocalInstanceService) recordCreateTimeline(ctx context.Context, operationID string, result ports.WorkloadInstanceCreateResult) error {
	steps := []ports.WorkloadOperationStep{
		{StepName: "plan", Status: ports.WorkloadOperationStepSucceeded, Message: "workload reference allocated"},
		{StepName: "render", Status: ports.WorkloadOperationStepSucceeded, Message: fmt.Sprintf("%d provider manifest(s) rendered", len(result.Manifests))},
		{StepName: "admission", Status: boolStepStatus(result.Admission.Allowed), Message: result.Admission.Reason},
		{StepName: "audit", Status: nonEmptyStepStatus(result.AuditID), Message: "plan audit recorded"},
		{StepName: "dry_run", Status: boolStepStatus(result.DryRun.Accepted), Message: result.DryRun.Reason},
		{StepName: "apply", Status: applyStepStatus(result.Apply.Applied), Message: result.Apply.Reason},
	}
	if result.Apply.Applied {
		steps = append(steps,
			ports.WorkloadOperationStep{StepName: "observe", Status: nonEmptyStepStatus(result.Observation.Provider), Message: result.Observation.Phase},
			ports.WorkloadOperationStep{StepName: "reconcile", Status: boolStepStatus(result.Reconcile.Changed || result.Orchestrated), Message: result.Reconcile.Reason},
		)
	}
	for _, step := range steps {
		if _, err := s.operations.AddOperationStep(ctx, operationID, step); err != nil {
			return err
		}
	}
	return nil
}

func (s *LocalInstanceService) withIdentity(ctx context.Context, record ports.WorkloadInstanceRecord) ports.WorkloadInstanceRecord {
	if s.identity == nil {
		return record
	}
	identity, err := s.identity.GetForInstance(ctx, record.TenantID, record.InstanceID)
	if err != nil {
		return record
	}
	record.Identity = &identity
	return record
}

func boolStepStatus(ok bool) ports.WorkloadOperationStepStatus {
	if ok {
		return ports.WorkloadOperationStepSucceeded
	}
	return ports.WorkloadOperationStepFailed
}

func nonEmptyStepStatus(value string) ports.WorkloadOperationStepStatus {
	if strings.TrimSpace(value) == "" {
		return ports.WorkloadOperationStepSkipped
	}
	return ports.WorkloadOperationStepSucceeded
}

func applyStepStatus(applied bool) ports.WorkloadOperationStepStatus {
	if applied {
		return ports.WorkloadOperationStepSucceeded
	}
	return ports.WorkloadOperationStepSkipped
}

func workloadSpecSummary(spec ports.WorkloadSpec) map[string]any {
	summary := map[string]any{
		"tenant_id":              spec.TenantID,
		"name":                   spec.Name,
		"kind":                   string(spec.Kind),
		"image":                  spec.Image,
		"cpu":                    spec.Resources.CPU,
		"memory":                 spec.Resources.Memory,
		"gpu_count":              spec.Resources.GPU.RequiredCount,
		"runtime_class":          spec.RuntimeClassName,
		"termination_protection": spec.Lifecycle.TerminationProtection,
	}
	if spec.Sandbox != nil {
		summary["sandbox_runtime_class"] = spec.Sandbox.RuntimeClass
		summary["sandbox_session_timeout"] = spec.Sandbox.SessionTimeout.String()
		summary["sandbox_network_egress_policy"] = string(spec.Sandbox.NetworkEgressPolicy)
	}
	if vpcID, subnetID, privateIP := selectedNetworkFields(spec.Network.Attachments); subnetID != "" {
		summary["vpc_id"] = vpcID
		summary["subnet_id"] = subnetID
		summary["private_ip"] = privateIP
	}
	return summary
}

func firstNonNilSandboxConfig(config *ports.SandboxConfig) ports.SandboxConfig {
	if config == nil {
		return ports.SandboxConfig{}
	}
	return *config
}

func workloadStateFromSandboxState(state ports.SandboxState) ports.WorkloadState {
	switch state {
	case ports.SandboxStateRunning:
		return ports.WorkloadStateRunning
	case ports.SandboxStateExpired, ports.SandboxStateStopped:
		return ports.WorkloadStateStopped
	default:
		return ports.WorkloadStatePending
	}
}

func workloadRecordSummary(record ports.WorkloadInstanceRecord) map[string]any {
	summary := map[string]any{
		"tenant_id":              record.TenantID,
		"instance_id":            record.InstanceID,
		"name":                   record.Name,
		"kind":                   string(record.Kind),
		"state":                  string(record.Status.State),
		"provider":               record.Provider,
		"termination_protection": record.Lifecycle.TerminationProtection,
		"snapshot_count":         len(record.Snapshots),
		"volume_count":           len(record.Status.Storage),
	}
	if record.Container != nil {
		summary["container_revision"] = record.Container.Revision
		summary["container_rollout_status"] = record.Container.RolloutStatus
		summary["container_history_count"] = len(record.Container.History)
	}
	return summary
}

type lifecyclePrecheckResult struct {
	allowed       bool
	failureReason string
	message       string
	retryEligible bool
	details       map[string]any
}

func lifecyclePrecheck(record ports.WorkloadInstanceRecord, request ports.WorkloadInstanceLifecycleRequest, next ports.WorkloadState) lifecyclePrecheckResult {
	details := map[string]any{
		"allowed":                true,
		"action":                 string(request.Action),
		"from_state":             string(record.Status.State),
		"to_state":               string(next),
		"termination_protection": record.Lifecycle.TerminationProtection,
	}
	if record.Kind == ports.WorkloadKindVM &&
		record.Lifecycle.TerminationProtection &&
		terminationProtectedAction(request.Action) {
		message := "termination_protection is enabled; disable it before " + string(request.Action)
		details["allowed"] = false
		details["reason"] = "termination_protection_enabled"
		return lifecyclePrecheckResult{
			allowed:       false,
			failureReason: "termination_protection_enabled",
			message:       message,
			retryEligible: false,
			details:       details,
		}
	}
	if request.Action == ports.WorkloadLifecycleSnapshot && record.Kind != ports.WorkloadKindVM {
		message := "snapshot is only supported for vm instances in the local profile"
		details["allowed"] = false
		details["reason"] = "snapshot_requires_vm"
		return lifecyclePrecheckResult{
			allowed:       false,
			failureReason: "snapshot_requires_vm",
			message:       message,
			retryEligible: false,
			details:       details,
		}
	}
	if request.Action == ports.WorkloadLifecycleAttachVolume || request.Action == ports.WorkloadLifecycleDetachVolume {
		volumeID := strings.TrimSpace(request.VolumeID)
		details["volume_id"] = volumeID
		if record.Kind != ports.WorkloadKindVM {
			return blockedLifecyclePrecheck(details, "volume_binding_requires_vm", "volume binding is only supported for vm instances in the local profile")
		}
		if volumeID == "" {
			return blockedLifecyclePrecheck(details, "volume_id_required", "volume_id is required for volume binding")
		}
		attached := hasVolume(record.Status.Storage, volumeID)
		if request.Action == ports.WorkloadLifecycleAttachVolume && attached {
			return blockedLifecyclePrecheck(details, "volume_already_attached", "volume is already attached")
		}
		if request.Action == ports.WorkloadLifecycleDetachVolume {
			if isRootVolume(record.Status.Storage, volumeID) {
				return blockedLifecyclePrecheck(details, "root_volume_detach_forbidden", "root disk cannot be detached")
			}
			if !attached {
				return blockedLifecyclePrecheck(details, "volume_not_attached", "volume is not attached")
			}
		}
	}
	if request.Action == ports.WorkloadLifecycleRollback {
		revision := strings.TrimSpace(request.Revision)
		details["revision"] = revision
		if record.Kind != ports.WorkloadKindContainer && record.Kind != ports.WorkloadKindGPUContainer {
			return blockedLifecyclePrecheck(details, "rollback_requires_container", "rollback is only supported for container and gpu_container instances in the local profile")
		}
		if record.Container == nil {
			return blockedLifecyclePrecheck(details, "container_status_missing", "container rollout status is required for rollback")
		}
		if _, ok := rollbackTarget(record.Container, revision); !ok {
			return blockedLifecyclePrecheck(details, "rollback_revision_not_found", "rollback revision was not found in rollout history")
		}
	}
	return lifecyclePrecheckResult{allowed: true, details: details}
}

func blockedLifecyclePrecheck(details map[string]any, reason string, message string) lifecyclePrecheckResult {
	details["allowed"] = false
	details["reason"] = reason
	return lifecyclePrecheckResult{
		allowed:       false,
		failureReason: reason,
		message:       message,
		retryEligible: false,
		details:       details,
	}
}

func terminationProtectedAction(action ports.WorkloadLifecycleAction) bool {
	switch action {
	case ports.WorkloadLifecycleStop, ports.WorkloadLifecycleDelete, ports.WorkloadLifecycleRebuild:
		return true
	default:
		return false
	}
}

func usesProviderLifecycle(action ports.WorkloadLifecycleAction) bool {
	switch action {
	case ports.WorkloadLifecycleSnapshot, ports.WorkloadLifecycleAttachVolume, ports.WorkloadLifecycleDetachVolume, ports.WorkloadLifecycleRollback:
		return false
	default:
		return true
	}
}

func usesProviderLifecycleForRecord(action ports.WorkloadLifecycleAction, record ports.WorkloadInstanceRecord, request ports.WorkloadInstanceLifecycleRequest) bool {
	if action == ports.WorkloadLifecycleDetachVolume {
		return record.Kind == ports.WorkloadKindVM && volumeKind(record.Status.Storage, request.VolumeID) == ports.StorageAttachmentCDROM
	}
	return usesProviderLifecycle(action)
}

func lifecycleApplyStepName(action ports.WorkloadLifecycleAction) string {
	if action == ports.WorkloadLifecycleSnapshot {
		return "create_snapshot"
	}
	if action == ports.WorkloadLifecycleAttachVolume {
		return "attach_volume"
	}
	if action == ports.WorkloadLifecycleDetachVolume {
		return "detach_volume"
	}
	if action == ports.WorkloadLifecycleRollback {
		return "rollback_revision"
	}
	return "apply_lifecycle"
}

func lifecycleAfterSpec(record ports.WorkloadInstanceRecord, request ports.WorkloadInstanceLifecycleRequest, next ports.WorkloadState, snapshot *ports.VMInstanceSnapshot, rollback *ports.ContainerInstanceStatus) map[string]any {
	after := workloadRecordSummary(record)
	after["state"] = string(next)
	if request.Action == ports.WorkloadLifecycleSnapshot && snapshot != nil {
		after["snapshot"] = map[string]any{
			"id":         snapshot.ID,
			"name":       snapshot.Name,
			"state":      snapshot.State,
			"created_at": snapshot.CreatedAt.Format(time.RFC3339),
		}
		after["snapshot_count"] = len(record.Snapshots) + 1
	}
	if request.Action == ports.WorkloadLifecycleAttachVolume {
		after["volume_id"] = strings.TrimSpace(request.VolumeID)
		after["volume_count"] = len(record.Status.Storage) + 1
	}
	if request.Action == ports.WorkloadLifecycleDetachVolume {
		after["volume_id"] = strings.TrimSpace(request.VolumeID)
		if len(record.Status.Storage) > 0 {
			after["volume_count"] = len(record.Status.Storage) - 1
		}
	}
	if request.Action == ports.WorkloadLifecycleRollback && rollback != nil {
		after["container_revision"] = rollback.Revision
		after["container_rollout_status"] = rollback.RolloutStatus
		after["container_history_count"] = len(rollback.History)
	}
	return after
}

func vmSnapshotFor(record ports.WorkloadInstanceRecord, request ports.WorkloadInstanceLifecycleRequest) *ports.VMInstanceSnapshot {
	if request.Action != ports.WorkloadLifecycleSnapshot {
		return nil
	}
	now := firstNonZeroTime(request.RequestedAt, time.Now().UTC())
	name := firstNonEmpty(request.SnapshotName, "snapshot-"+now.Format("20060102150405"))
	idSeed := firstNonEmpty(request.IdempotencyKey, record.InstanceID+"-"+name+"-"+now.Format("20060102150405"))
	return &ports.VMInstanceSnapshot{
		ID:               "snap_" + sanitizeSnapshotID(idSeed),
		Name:             name,
		SourceInstanceID: record.InstanceID,
		State:            "ready",
		Reason:           "snapshot metadata recorded by local profile; provider snapshot execution is a follow-up capability",
		CreatedAt:        now,
		ReadyAt:          now,
	}
}

var snapshotIDPattern = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func sanitizeSnapshotID(value string) string {
	value = strings.Trim(snapshotIDPattern.ReplaceAllString(value, "_"), "_")
	if value == "" {
		return "local"
	}
	if len(value) > 48 {
		return value[:48]
	}
	return value
}

func volumeAttachmentFor(request ports.WorkloadInstanceLifecycleRequest) *ports.WorkloadStorageAttachment {
	if request.Action != ports.WorkloadLifecycleAttachVolume {
		return nil
	}
	volumeID := strings.TrimSpace(request.VolumeID)
	if volumeID == "" {
		return nil
	}
	return &ports.WorkloadStorageAttachment{
		Name:      volumeID,
		Kind:      ports.StorageAttachmentDataDisk,
		MountPath: "/mnt/" + sanitizeSnapshotID(volumeID),
		SourceRef: volumeID,
		Required:  true,
	}
}

func applyVolumeBinding(storage []ports.WorkloadStorageAttachment, action ports.WorkloadLifecycleAction, volume *ports.WorkloadStorageAttachment, volumeID string) []ports.WorkloadStorageAttachment {
	switch action {
	case ports.WorkloadLifecycleAttachVolume:
		if volume == nil {
			return storage
		}
		next := append([]ports.WorkloadStorageAttachment(nil), storage...)
		next = append(next, *volume)
		return next
	case ports.WorkloadLifecycleDetachVolume:
		volumeID = strings.TrimSpace(volumeID)
		next := make([]ports.WorkloadStorageAttachment, 0, len(storage))
		for _, attachment := range storage {
			if sameVolume(attachment, volumeID) {
				continue
			}
			next = append(next, attachment)
		}
		return next
	default:
		return storage
	}
}

func hasVolume(storage []ports.WorkloadStorageAttachment, volumeID string) bool {
	for _, attachment := range storage {
		if sameVolume(attachment, volumeID) {
			return true
		}
	}
	return false
}

func isRootVolume(storage []ports.WorkloadStorageAttachment, volumeID string) bool {
	for _, attachment := range storage {
		if attachment.Kind == ports.StorageAttachmentRootDisk && sameVolume(attachment, volumeID) {
			return true
		}
	}
	return false
}

func volumeKind(storage []ports.WorkloadStorageAttachment, volumeID string) ports.StorageAttachmentKind {
	for _, attachment := range storage {
		if sameVolume(attachment, volumeID) {
			return attachment.Kind
		}
	}
	return ""
}

func sameVolume(attachment ports.WorkloadStorageAttachment, volumeID string) bool {
	volumeID = strings.TrimSpace(volumeID)
	return volumeID != "" && (attachment.Name == volumeID || attachment.SourceRef == volumeID)
}

func containerRollbackFor(record ports.WorkloadInstanceRecord, request ports.WorkloadInstanceLifecycleRequest) *ports.ContainerInstanceStatus {
	if request.Action != ports.WorkloadLifecycleRollback || record.Container == nil {
		return nil
	}
	target, ok := rollbackTarget(record.Container, request.Revision)
	if !ok {
		return nil
	}
	next := *record.Container
	next.Revision = target.Revision
	next.RolloutStatus = "rolled_back"
	next.History = append([]ports.ContainerRevisionHistory(nil), record.Container.History...)
	if len(next.History) == 0 || next.History[len(next.History)-1].Revision != target.Revision {
		next.History = append(next.History, target)
	}
	return &next
}

func rollbackTarget(container *ports.ContainerInstanceStatus, revision string) (ports.ContainerRevisionHistory, bool) {
	if container == nil {
		return ports.ContainerRevisionHistory{}, false
	}
	revision = strings.TrimSpace(revision)
	if revision != "" {
		for _, item := range container.History {
			if item.Revision == revision {
				return item, true
			}
		}
		return ports.ContainerRevisionHistory{}, false
	}
	for i := len(container.History) - 1; i >= 0; i-- {
		if container.History[i].Revision != "" && container.History[i].Revision != container.Revision {
			return container.History[i], true
		}
	}
	return ports.ContainerRevisionHistory{}, false
}

func isSessionOpsAction(action ports.WorkloadInstanceOpsAction) bool {
	switch action {
	case ports.WorkloadInstanceOpsTerminal, ports.WorkloadInstanceOpsExec,
		ports.WorkloadInstanceOpsVMConsole, ports.WorkloadInstanceOpsVMVNC, ports.WorkloadInstanceOpsVMSerial:
		return true
	default:
		return false
	}
}

func opsOperationStatus(accepted bool) ports.WorkloadOperationStatus {
	if accepted {
		return ports.WorkloadOperationSucceeded
	}
	return ports.WorkloadOperationFailed
}

func opsStepStatus(accepted bool) ports.WorkloadOperationStepStatus {
	if accepted {
		return ports.WorkloadOperationStepSucceeded
	}
	return ports.WorkloadOperationStepFailed
}

func opsFailureReason(result ports.WorkloadInstanceOpsResult) string {
	if result.Accepted {
		return ""
	}
	return "ops_session_rejected"
}

func opsFailureMessage(result ports.WorkloadInstanceOpsResult) string {
	if result.Accepted {
		return ""
	}
	return result.Reason
}

func lifecycleDestructiveImpact(record ports.WorkloadInstanceRecord, action ports.WorkloadLifecycleAction) map[string]any {
	return map[string]any{
		"action":                string(action),
		"workload_kind":         string(record.Kind),
		"state":                 string(record.Status.State),
		"stops_running_compute": action == ports.WorkloadLifecycleStop || action == ports.WorkloadLifecycleDelete || action == ports.WorkloadLifecycleRebuild,
		"creates_snapshot":      action == ports.WorkloadLifecycleSnapshot,
		"mutates_storage":       action == ports.WorkloadLifecycleAttachVolume || action == ports.WorkloadLifecycleDetachVolume,
		"mutates_rollout":       action == ports.WorkloadLifecycleRollback,
		"may_delete_storage":    action == ports.WorkloadLifecycleDelete && !record.Lifecycle.RetainStorage,
	}
}

func pendingOperationInstanceID(operationID string) string {
	return "pending:" + operationID
}
