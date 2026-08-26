package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type instanceStorageBinder interface {
	MountVolume(ctx context.Context, request ports.StorageVolumeMountRequest) (ports.StorageVolumeRecord, error)
	MountFilesystem(ctx context.Context, request ports.StorageFilesystemMountRequest) (ports.StorageFilesystemRecord, error)
}

type LocalInstanceService struct {
	orchestrator ports.WorkloadInstanceOrchestrator
	store        ports.WorkloadInstanceStore
	operations   ports.WorkloadOperationStore
	lifecycle    ports.WorkloadInstanceLifecycleExecutor
	identity     ports.WorkloadIdentityService
	resources    ports.WorkloadInstanceResourceResolver
	storage      instanceStorageBinder
	sandbox      ports.SandboxRuntime
	ops          ports.WorkloadInstanceOps
	// quotaService performs TCC Cancel+Release on Delete (SPEC §5.1).
	// nil means GPU quota is disabled.
	quotaService ports.QuotaService
	// metadataStore opens tenant-scoped transactions for atomic quota +
	// status writes on Delete.
	metadataStore ports.MetadataStore
	// storeTx writes instance status inside the same tenant transaction as
	// the quota Cancel+Release on Delete.
	storeTx ports.WorkloadInstanceStoreTx
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

func WithInstanceResourceResolver(resources ports.WorkloadInstanceResourceResolver) InstanceServiceOption {
	return func(service *LocalInstanceService) {
		service.resources = resources
	}
}

func WithSandboxRuntime(sandbox ports.SandboxRuntime) InstanceServiceOption {
	return func(service *LocalInstanceService) {
		service.sandbox = sandbox
	}
}

func WithInstanceStorageService(storage instanceStorageBinder) InstanceServiceOption {
	return func(service *LocalInstanceService) {
		service.storage = storage
	}
}

// WithInstanceQuotaService injects the TCC quota service used for Cancel +
// Release on Delete (SPEC §5.1). When nil, quota calls are skipped.
func WithInstanceQuotaService(service ports.QuotaService) InstanceServiceOption {
	return func(s *LocalInstanceService) {
		s.quotaService = service
	}
}

// WithInstanceMetadataStore injects the tenant transaction store used so
// Delete can atomically release quota and write the deleted status.
func WithInstanceMetadataStore(store ports.MetadataStore) InstanceServiceOption {
	return func(s *LocalInstanceService) {
		s.metadataStore = store
	}
}

// WithInstanceStoreTx injects the transactional status writer used inside the
// same tenant transaction as Cancel+Release on Delete.
func WithInstanceStoreTx(storeTx ports.WorkloadInstanceStoreTx) InstanceServiceOption {
	return func(s *LocalInstanceService) {
		s.storeTx = storeTx
	}
}

func NewLocalInstanceService(orchestrator ports.WorkloadInstanceOrchestrator, store ports.WorkloadInstanceStore, ops ports.WorkloadInstanceOps) *LocalInstanceService {
	return &LocalInstanceService{
		orchestrator: orchestrator,
		store:        store,
		operations:   NewLocalOperationStore(),
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
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.WorkloadInstanceCreateResult{}, fmt.Errorf("%w: idempotency key is required", ports.ErrInvalid)
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
	if request.Spec.Kind == ports.WorkloadKindSandbox && (s.sandbox == nil || s.store == nil) {
		return ports.WorkloadInstanceCreateResult{}, ports.ErrNotConfigured
	}
	var resolvedResourceRefs []string
	if s.resources != nil {
		resolved, err := s.resources.ResolveCreate(ctx, ports.WorkloadResourceResolveRequest{
			TenantID: request.Spec.TenantID,
			UserID:   request.UserID,
			Spec:     request.Spec,
		})
		if err != nil {
			return ports.WorkloadInstanceCreateResult{}, err
		}
		request.Spec = resolved.Spec
		resolvedResourceRefs = append([]string(nil), resolved.ResourceRefs...)
	}
	if err := validateCreateIntent(request.Spec); err != nil {
		return ports.WorkloadInstanceCreateResult{}, err
	}
	requestFingerprint, err := createIntentFingerprint(request.Spec)
	if err != nil {
		return ports.WorkloadInstanceCreateResult{}, err
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
			Precheck: map[string]any{
				"allowed":             true,
				"request_fingerprint": requestFingerprint,
				"resource_refs":       resolvedResourceRefs,
			},
			AfterSpec: workloadSpecSummary(request.Spec),
			CreatedAt: firstNonZeroTime(request.RequestedAt),
			UpdatedAt: firstNonZeroTime(request.RequestedAt),
		})
		if err != nil {
			return ports.WorkloadInstanceCreateResult{}, err
		}
		if existing {
			if err := validateOperationReplayFingerprint(operation, requestFingerprint, "create"); err != nil {
				return ports.WorkloadInstanceCreateResult{}, err
			}
			if operation.Status == ports.WorkloadOperationFailed {
				return ports.WorkloadInstanceCreateResult{}, failedLifecycleReplayError(operation)
			}
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
	if request.Spec.Kind == ports.WorkloadKindSandbox {
		return s.createSandbox(ctx, request, operation, preRecorded)
	}
	result, err := s.orchestrator.Create(ctx, request)
	if err != nil {
		if preRecorded {
			_, _ = s.operations.UpdateOperation(ctx, operation.ID, ports.WorkloadOperationUpdate{
				Status:         ports.WorkloadOperationFailed,
				FailureReason:  classifiedLifecycleFailureReason("create_failed", err),
				FailureMessage: err.Error(),
				RetryEligible:  true,
				UpdatedAt:      firstNonZeroTime(request.RequestedAt),
			})
		}
		return ports.WorkloadInstanceCreateResult{}, err
	}
	if err := s.bindCreateStorage(ctx, request, result); err != nil {
		if preRecorded || s.operations != nil {
			opID := operation.ID
			if opID == "" && result.OperationID != "" {
				opID = result.OperationID
			}
			if opID != "" {
				_, _ = s.operations.AddOperationStep(ctx, opID, ports.WorkloadOperationStep{
					StepName: "storage_mount",
					Status:   ports.WorkloadOperationStepFailed,
					Message:  err.Error(),
				})
				_, _ = s.operations.UpdateOperation(ctx, opID, ports.WorkloadOperationUpdate{
					InstanceID:     result.Ref.InstanceID,
					Status:         ports.WorkloadOperationFailed,
					FailureReason:  "storage_mount_failed",
					FailureMessage: err.Error(),
					RetryEligible:  true,
					UpdatedAt:      firstNonZeroTime(request.RequestedAt, result.FinalStatus.UpdatedAt),
				})
			}
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
			Precheck: map[string]any{
				"allowed":             true,
				"request_fingerprint": requestFingerprint,
				"resource_refs":       resolvedResourceRefs,
			},
			AfterSpec:    workloadSpecSummary(request.Spec),
			ProviderRefs: result.Apply.ResourceRefs,
			CreatedAt:    firstNonZeroTime(request.RequestedAt),
			UpdatedAt:    firstNonZeroTime(request.RequestedAt),
		})
		if err != nil {
			return ports.WorkloadInstanceCreateResult{}, err
		}
	}
	result.OperationID = operation.ID
	if err := s.recordCreateTimeline(ctx, operation.ID, request.Spec, resolvedResourceRefs, result); err != nil {
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

func validateCreateIntent(spec ports.WorkloadSpec) error {
	switch spec.Kind {
	case ports.WorkloadKindVM:
		if spec.Container != nil || spec.Sandbox != nil || spec.GPUSpec != nil {
			return fmt.Errorf("%w: vm create cannot include container, sandbox, or gpu spec config", ports.ErrInvalid)
		}
	case ports.WorkloadKindContainer:
		if spec.VM != nil || spec.Sandbox != nil || spec.GPUSpec != nil {
			return fmt.Errorf("%w: container create cannot include vm, sandbox, or gpu spec config", ports.ErrInvalid)
		}
	case ports.WorkloadKindGPUContainer:
		if spec.VM != nil || spec.Sandbox != nil {
			return fmt.Errorf("%w: gpu_container create cannot include vm or sandbox config", ports.ErrInvalid)
		}
	case ports.WorkloadKindSandbox:
		if spec.VM != nil || spec.Container != nil || spec.GPUSpec != nil {
			return fmt.Errorf("%w: sandbox create cannot include vm, container, or gpu spec config", ports.ErrInvalid)
		}
	}
	if spec.VM != nil {
		if spec.VM.SystemDisk != nil {
			if err := validateInstanceDiskSpec(*spec.VM.SystemDisk); err != nil {
				return err
			}
		}
		for _, disk := range spec.VM.DataDiskSpecs {
			if err := validateInstanceDiskSpec(disk); err != nil {
				return err
			}
		}
	}
	if spec.Container != nil {
		for _, env := range spec.Container.Env {
			if err := validateInstanceEnvVar(env); err != nil {
				return err
			}
		}
	}
	if spec.Sandbox != nil {
		for _, env := range spec.Sandbox.Env {
			if err := validateInstanceEnvVar(env); err != nil {
				return err
			}
		}
	}
	if spec.GPUSpec != nil {
		if strings.TrimSpace(spec.GPUSpec.SpecID) == "" || spec.GPUSpec.Shares < 1 || spec.GPUSpec.MBPerShare < 1 {
			return fmt.Errorf("%w: gpu spec id, shares, and mb_per_share are required", ports.ErrInvalid)
		}
		if err := validateLegacyGPUSelectorsAgainstSpec(spec.Resources.GPU, *spec.GPUSpec); err != nil {
			return err
		}
	}
	return nil
}

func validateLegacyGPUSelectorsAgainstSpec(request ports.GPUSchedulingRequest, spec ports.InstanceGPUSpecReference) error {
	if len(request.PreferredVendors) > 0 {
		return fmt.Errorf("%w: gpu spec cannot be combined with legacy vendor", ports.ErrInvalid)
	}
	for _, model := range request.PreferredModels {
		if strings.TrimSpace(spec.GPUType) == "" || !strings.EqualFold(strings.TrimSpace(model), strings.TrimSpace(spec.GPUType)) {
			return fmt.Errorf("%w: gpu spec conflicts with legacy model", ports.ErrInvalid)
		}
	}
	if request.RequiredCount > 1 {
		return fmt.Errorf("%w: gpu spec conflicts with legacy count", ports.ErrInvalid)
	}
	if len(request.VirtualizationModes) > 0 {
		expected := ports.GPUVirtualizationVGPU
		if spec.Shares == 1 {
			expected = ports.GPUVirtualizationNone
		}
		if len(request.VirtualizationModes) != 1 || request.VirtualizationModes[0] != expected {
			return fmt.Errorf("%w: gpu spec conflicts with legacy allocation mode", ports.ErrInvalid)
		}
	}
	return nil
}

func validateInstanceDiskSpec(disk ports.InstanceDiskSpec) error {
	hasExisting := strings.TrimSpace(disk.VolumeID) != ""
	hasNew := strings.TrimSpace(disk.Name) != "" || disk.SizeGiB > 0
	if hasExisting == hasNew {
		return fmt.Errorf("%w: disk must reference volume_id or declare name and size_gib", ports.ErrInvalid)
	}
	if hasNew && (strings.TrimSpace(disk.Name) == "" || disk.SizeGiB < 1) {
		return fmt.Errorf("%w: new disk requires name and size_gib", ports.ErrInvalid)
	}
	return nil
}

func validateInstanceEnvVar(env ports.InstanceEnvVar) error {
	if strings.TrimSpace(env.Name) == "" {
		return fmt.Errorf("%w: environment variable name is required", ports.ErrInvalid)
	}
	hasValue := env.Value != nil
	hasSecret := strings.TrimSpace(env.SecretRef) != ""
	if hasValue == hasSecret {
		return fmt.Errorf("%w: environment variable must use exactly one of value or secret_ref", ports.ErrInvalid)
	}
	return nil
}

func createIntentFingerprint(spec ports.WorkloadSpec) (string, error) {
	payload, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("%w: marshal create intent: %v", ports.ErrInvalid, err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(payload)), nil
}

func (s *LocalInstanceService) createSandbox(ctx context.Context, request ports.WorkloadInstanceCreateRequest, operation ports.WorkloadOperationRecord, preRecorded bool) (ports.WorkloadInstanceCreateResult, error) {
	if s.sandbox == nil {
		return ports.WorkloadInstanceCreateResult{}, ports.ErrNotConfigured
	}
	if s.store == nil {
		return ports.WorkloadInstanceCreateResult{}, ports.ErrNotConfigured
	}
	instance, err := s.sandbox.Create(ctx, ports.SandboxCreateRequest{
		TenantID:            request.Spec.TenantID,
		Name:                request.Spec.Name,
		Image:               request.Spec.Image,
		Config:              firstNonNilSandboxConfig(request.Spec.Sandbox),
		CheckpointSourceRef: request.Spec.SandboxCheckpointSourceRef,
		AutoStart:           request.Spec.Lifecycle.AutoStart,
		CreatedAt:           request.RequestedAt,
	})
	if err != nil {
		if preRecorded {
			_, _ = s.operations.UpdateOperation(ctx, operation.ID, ports.WorkloadOperationUpdate{
				Status:         ports.WorkloadOperationFailed,
				FailureReason:  classifiedLifecycleFailureReason("sandbox_create_failed", err),
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
	record := ports.WorkloadInstanceRecord{
		TenantID:           instance.TenantID,
		InstanceID:         instance.InstanceID,
		Name:               instance.Name,
		Description:        request.Spec.Description,
		Labels:             cloneInstanceLabels(request.Spec.Labels),
		Kind:               ports.WorkloadKindSandbox,
		Provider:           instance.Provider,
		Image:              instanceImageSummary(request.Spec),
		Compute:            instanceComputeSummary(request.Spec, status),
		Network:            instanceNetworkSummary(request.Spec, status),
		Access:             instanceAccessSummary(ports.WorkloadKindSandbox, status.State),
		StorageAttachments: instanceStorageAttachments(request.Spec, status),
		Lifecycle:          request.Spec.Lifecycle,
		Sandbox:            &instance,
		ResourceRefs:       sandboxResourceRefs(instance),
		Status:             status,
		CreatedAt:          instance.CreatedAt,
		UpdatedAt:          instance.UpdatedAt,
	}
	if preRecorded {
		record.OperationID = operation.ID
	}
	if err := s.store.UpsertStatus(ctx, record); err != nil {
		if preRecorded {
			_, _ = s.operations.UpdateOperation(ctx, operation.ID, ports.WorkloadOperationUpdate{
				Status:         ports.WorkloadOperationFailed,
				FailureReason:  classifiedLifecycleFailureReason("sandbox_status_persist_failed", err),
				FailureMessage: err.Error(),
				RetryEligible:  true,
				UpdatedAt:      firstNonZeroTime(request.RequestedAt, instance.UpdatedAt),
			})
		}
		return ports.WorkloadInstanceCreateResult{}, err
	}

	result := ports.WorkloadInstanceCreateResult{
		Ref:         ref,
		OperationID: record.OperationID,
		AuditID:     "sandbox_local_" + instance.InstanceID,
		Apply: ports.WorkloadProviderApplyResult{
			Applied:      true,
			Provider:     instance.Provider,
			Reason:       sandboxCreateReason(instance),
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
			Message:  sandboxCreateReason(instance),
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
	if request.Limit < 0 || request.Limit > 100 {
		return nil, fmt.Errorf("%w: limit must be between 1 and 100", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.Cursor) != "" {
		return nil, fmt.Errorf("%w: instance cursor pagination is not implemented in the service result boundary", ports.ErrUnsupported)
	}
	if request.Sort == "" {
		request.Sort = "created_at_desc"
	}
	if !validInstanceListSort(request.Sort) {
		return nil, fmt.Errorf("%w: unsupported instance sort %q", ports.ErrInvalid, request.Sort)
	}
	records, err := s.store.List(ctx, request.TenantID, request.Kind)
	if err != nil {
		return nil, err
	}
	filtered := make([]ports.WorkloadInstanceRecord, 0, len(records))
	for _, record := range records {
		if !matchesInstanceList(record, request) {
			continue
		}
		filtered = append(filtered, s.withIdentity(ctx, record))
	}
	sortInstanceRecords(filtered, request.Sort)
	if request.Limit > 0 && len(filtered) > request.Limit {
		filtered = filtered[:request.Limit]
	}
	return filtered, nil
}

func validInstanceListSort(value string) bool {
	switch value {
	case "created_at_asc", "created_at_desc", "name_asc", "name_desc":
		return true
	default:
		return false
	}
}

func matchesInstanceList(record ports.WorkloadInstanceRecord, request ports.WorkloadInstanceListRequest) bool {
	if request.State != "" && record.Status.State != request.State {
		return false
	}
	if keyword := strings.ToLower(strings.TrimSpace(request.Keyword)); keyword != "" {
		haystack := strings.ToLower(record.InstanceID + "\n" + record.Name + "\n" + record.Description)
		if !strings.Contains(haystack, keyword) {
			return false
		}
	}
	if !request.CreatedAfter.IsZero() && !record.CreatedAt.After(request.CreatedAfter) {
		return false
	}
	if !request.CreatedBefore.IsZero() && !record.CreatedAt.Before(request.CreatedBefore) {
		return false
	}
	if request.SpecID != "" && record.Compute.SpecID != request.SpecID {
		return false
	}
	if request.ImageID != "" && record.Image.ID != request.ImageID {
		return false
	}
	if request.NodeName != "" && firstNonEmpty(record.Status.NodeName, record.Compute.NodeName) != request.NodeName {
		return false
	}
	if request.RolloutStatus != "" && (record.Container == nil || record.Container.RolloutStatus != request.RolloutStatus) {
		return false
	}
	if request.GPUModel != "" && (record.GPU == nil || record.GPU.Model != request.GPUModel) {
		return false
	}
	if request.QueueName != "" && (record.GPU == nil || record.GPU.QueueName != request.QueueName) {
		return false
	}
	if request.SchedulingState != "" && (record.GPU == nil || record.GPU.SchedulingState != request.SchedulingState) {
		return false
	}
	if request.TemplateID != "" && (record.Sandbox == nil || record.Sandbox.TemplateID != request.TemplateID) {
		return false
	}
	if request.SessionState != "" && (record.Sandbox == nil || record.Sandbox.SessionState != request.SessionState) {
		return false
	}
	return true
}

func sortInstanceRecords(records []ports.WorkloadInstanceRecord, order string) {
	sort.SliceStable(records, func(i, j int) bool {
		left, right := records[i], records[j]
		switch order {
		case "created_at_asc":
			if left.CreatedAt.Equal(right.CreatedAt) {
				return left.InstanceID < right.InstanceID
			}
			return left.CreatedAt.Before(right.CreatedAt)
		case "name_asc":
			if left.Name == right.Name {
				return left.InstanceID < right.InstanceID
			}
			return left.Name < right.Name
		case "name_desc":
			if left.Name == right.Name {
				return left.InstanceID > right.InstanceID
			}
			return left.Name > right.Name
		default:
			if left.CreatedAt.Equal(right.CreatedAt) {
				return left.InstanceID > right.InstanceID
			}
			return left.CreatedAt.After(right.CreatedAt)
		}
	})
}

func (s *LocalInstanceService) ApplyLifecycle(ctx context.Context, request ports.WorkloadInstanceLifecycleRequest) (ports.WorkloadInstanceRecord, error) {
	return s.applyLifecycle(ctx, request)
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
		Resources:       request.Resources,
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
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return ports.WorkloadInstanceRecord{}, fmt.Errorf("%w: idempotency key is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.UserID) == "" || strings.TrimSpace(request.PermissionProof) == "" {
		return ports.WorkloadInstanceRecord{}, fmt.Errorf("%w: user id and permission proof are required", ports.ErrInvalid)
	}
	record, err := s.store.Get(ctx, request.TenantID, request.InstanceID)
	if err != nil {
		return ports.WorkloadInstanceRecord{}, err
	}
	if err := validateLifecycleIntent(record, request); err != nil {
		return ports.WorkloadInstanceRecord{}, err
	}
	requestFingerprint := ""
	if s.operations != nil {
		requestFingerprint, err = lifecycleIntentFingerprint(request)
		if err != nil {
			return ports.WorkloadInstanceRecord{}, err
		}
		if strings.TrimSpace(request.IdempotencyKey) != "" {
			existing, err := s.operations.GetOperationByIdempotencyKey(ctx, request.TenantID, request.IdempotencyKey)
			if err == nil {
				if err := validateOperationReplayFingerprint(existing, requestFingerprint, "lifecycle"); err != nil {
					return ports.WorkloadInstanceRecord{}, err
				}
				if existing.Status == ports.WorkloadOperationFailed {
					return ports.WorkloadInstanceRecord{}, failedLifecycleReplayError(existing)
				}
				record.OperationID = existing.ID
				return record, nil
			}
		}
	}
	next, err := transition(record.Status.State, request.Action)
	if err != nil {
		return ports.WorkloadInstanceRecord{}, err
	}
	previousState := record.Status.State
	precheck := lifecyclePrecheck(record, request, next)
	if requestFingerprint != "" {
		precheck.details["request_fingerprint"] = requestFingerprint
	}
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
			if err := validateOperationReplayFingerprint(operation, requestFingerprint, "lifecycle"); err != nil {
				return ports.WorkloadInstanceRecord{}, err
			}
			if operation.Status == ports.WorkloadOperationFailed {
				return ports.WorkloadInstanceRecord{}, failedLifecycleReplayError(operation)
			}
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
	if record.Kind == ports.WorkloadKindSandbox && usesSandboxRuntime(request.Action) {
		if s.sandbox == nil {
			err := ports.ErrNotConfigured
			s.failLifecycleOperation(ctx, opID, "sandbox_lifecycle_failed", err, request.RequestedAt)
			return ports.WorkloadInstanceRecord{}, err
		}
		execution, err := SandboxExecutionContextFromRecord(record)
		if err != nil {
			s.failLifecycleOperation(ctx, opID, "sandbox_lifecycle_failed", err, request.RequestedAt)
			return ports.WorkloadInstanceRecord{}, err
		}
		sandbox, err := s.sandbox.ApplyLifecycle(ctx, ports.SandboxLifecycleRequest{
			TenantID:    request.TenantID,
			InstanceID:  request.InstanceID,
			Execution:   &execution,
			Action:      request.Action,
			Duration:    request.Duration,
			RequestedAt: request.RequestedAt,
		})
		if err != nil {
			s.failLifecycleOperation(ctx, opID, "sandbox_lifecycle_failed", err, request.RequestedAt)
			return ports.WorkloadInstanceRecord{}, err
		}
		record.Sandbox = &sandbox
	}
	if s.lifecycle != nil && record.Kind != ports.WorkloadKindSandbox && usesProviderLifecycle(request.Action) {
		result, err := s.lifecycle.Apply(ctx, request, record)
		if err != nil {
			if opID != "" {
				_, _ = s.operations.UpdateOperation(ctx, opID, ports.WorkloadOperationUpdate{
					Status:         ports.WorkloadOperationFailed,
					FailureReason:  classifiedLifecycleFailureReason("provider_lifecycle_failed", err),
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
	record.StorageAttachments = applyVolumeBinding(record.StorageAttachments, request.Action, volume, request.VolumeID)
	if rollback != nil {
		record.Container = rollback
	}
	applyApprovedLifecycleSummary(&record, request)
	record.Status.State = next
	record.Status.Reason = "lifecycle " + string(request.Action) + " requested"
	record.Access = instanceAccessSummary(record.Kind, next)
	if !request.RequestedAt.IsZero() {
		record.Status.UpdatedAt = request.RequestedAt.UTC()
		record.UpdatedAt = request.RequestedAt.UTC()
	}
	if err := s.persistLifecycleWithQuota(ctx, record, request.Action, previousState); err != nil {
		return ports.WorkloadInstanceRecord{}, err
	}
	record.OperationID = opID
	if opID != "" {
		if _, err := s.operations.AddOperationStep(ctx, opID, ports.WorkloadOperationStep{
			StepName: lifecycleApplyStepName(request),
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

// persistLifecycleWithQuota writes the lifecycle status update and handles
// quota for two cases (SPEC §5.1):
//
//   - Delete: atomically releases both reserved and used quota (Cancel +
//     Release double-call, state-independent) in the same tenant transaction.
//   - Start or Rollback from failed: re-acquires quota via TryManyTx and
//     updates QuotaTxIDs on the record. When the instance went running→failed
//     the TCC reservation was released; starting or rolling back it again
//     needs a fresh Try so the reconciler can later Confirm (reserved→used).
//     TryManyTx's SQL WHERE guard (reserved+used+request <= total) prevents
//     oversell.
//
// For all other actions it falls back to the plain store.UpsertStatus path.
func (s *LocalInstanceService) persistLifecycleWithQuota(ctx context.Context, record ports.WorkloadInstanceRecord, action ports.WorkloadLifecycleAction, previous ports.WorkloadState) error {
	// Delete: Cancel + Release double-call (existing logic).
	if action == ports.WorkloadLifecycleDelete {
		if s.quotaService == nil || s.metadataStore == nil || s.storeTx == nil {
			return s.store.UpsertStatus(ctx, record)
		}
		if len(record.QuotaTxIDs) == 0 {
			return s.store.UpsertStatus(ctx, record)
		}
		return s.metadataStore.WithTenantTx(ctx, func(txCtx context.Context, tx ports.MetadataTx) error {
			if err := s.quotaService.Cancel(txCtx, tx, record.QuotaTxIDs); err != nil {
				return err
			}
			if err := s.quotaService.Release(txCtx, tx, record.QuotaTxIDs); err != nil {
				return err
			}
			return s.storeTx.UpsertStatusTx(txCtx, tx, record)
		})
	}

	// Start or Rollback from failed: re-acquire quota with a fresh TryManyTx.
	// Both actions transition failed→running; without re-acquiring quota the
	// instance would run with stale (released) QuotaTxIDs.
	if (action == ports.WorkloadLifecycleStart || action == ports.WorkloadLifecycleRollback) && previous == ports.WorkloadStateFailed {
		if s.quotaService == nil || s.metadataStore == nil || s.storeTx == nil {
			return s.store.UpsertStatus(ctx, record)
		}
		// GPU count from the instance record; default 1.
		gpuCount := 1
		if record.GPU != nil && record.GPU.Count > 0 {
			gpuCount = record.GPU.Count
		}
		return s.metadataStore.WithTenantTx(ctx, func(txCtx context.Context, tx ports.MetadataTx) error {
			reservations, err := s.quotaService.TryManyTx(txCtx, tx, []ports.QuotaTryRequest{{
				TenantID:     record.TenantID,
				ResourceType: ports.QuotaGPUCount,
				Amount:       int64(gpuCount),
			}})
			if err != nil {
				return err
			}
			record.QuotaTxIDs = reservationTxIDs(reservations)
			return s.storeTx.UpsertStatusTx(txCtx, tx, record)
		})
	}

	return s.store.UpsertStatus(ctx, record)
}

// SandboxExecutionContextFromRecord validates and projects the durable instance
// record into the request-scoped context consumed by a sandbox provider.
func SandboxExecutionContextFromRecord(record ports.WorkloadInstanceRecord) (ports.SandboxExecutionContext, error) {
	if record.Kind != ports.WorkloadKindSandbox {
		return ports.SandboxExecutionContext{}, fmt.Errorf("%w: instance kind %q is not sandbox", ports.ErrInvalid, record.Kind)
	}
	if strings.TrimSpace(record.TenantID) == "" || strings.TrimSpace(record.InstanceID) == "" || strings.TrimSpace(record.Name) == "" || strings.TrimSpace(record.Provider) == "" {
		return ports.SandboxExecutionContext{}, fmt.Errorf("%w: persisted sandbox identity is incomplete", ports.ErrFailedPrecondition)
	}
	if record.Sandbox == nil {
		return ports.SandboxExecutionContext{}, fmt.Errorf("%w: persisted sandbox status is missing", ports.ErrFailedPrecondition)
	}
	if record.Sandbox.TenantID != "" && record.Sandbox.TenantID != record.TenantID {
		return ports.SandboxExecutionContext{}, fmt.Errorf("%w: persisted sandbox tenant does not match instance", ports.ErrFailedPrecondition)
	}
	if record.Sandbox.InstanceID != "" && record.Sandbox.InstanceID != record.InstanceID {
		return ports.SandboxExecutionContext{}, fmt.Errorf("%w: persisted sandbox id does not match instance", ports.ErrFailedPrecondition)
	}
	return ports.SandboxExecutionContext{
		TenantID:     record.TenantID,
		InstanceID:   record.InstanceID,
		Name:         record.Name,
		Provider:     record.Provider,
		State:        record.Sandbox.State,
		SessionState: record.Sandbox.SessionState,
		Config:       record.Sandbox.Config,
		DevProfile:   record.Sandbox.DevProfile,
		Ports:        append([]ports.SandboxPortResult(nil), record.Sandbox.Ports...),
		ResourceRefs: append([]string(nil), record.ResourceRefs...),
		CreatedAt:    record.CreatedAt,
		UpdatedAt:    record.UpdatedAt,
	}, nil
}

var _ ports.WorkloadInstanceService = (*LocalInstanceService)(nil)

func failedLifecycleReplayError(operation ports.WorkloadOperationRecord) error {
	message := strings.TrimSpace(operation.FailureMessage)
	if message == "" {
		message = "prior lifecycle operation failed"
	}
	switch {
	case strings.HasSuffix(operation.FailureReason, ".not_found"):
		return fmt.Errorf("%w: %s", ports.ErrNotFound, message)
	case strings.HasSuffix(operation.FailureReason, ".not_configured"):
		return fmt.Errorf("%w: %s", ports.ErrNotConfigured, message)
	case strings.HasSuffix(operation.FailureReason, ".unsupported"):
		return fmt.Errorf("%w: %s", ports.ErrUnsupported, message)
	case strings.HasSuffix(operation.FailureReason, ".invalid"):
		return fmt.Errorf("%w: %s", ports.ErrInvalid, message)
	default:
		return fmt.Errorf("%w: %s", ports.ErrConflict, message)
	}
}

func (s *LocalInstanceService) failLifecycleOperation(ctx context.Context, operationID, reason string, cause error, updatedAt time.Time) {
	if s.operations == nil || operationID == "" {
		return
	}
	message := cause.Error()
	_, _ = s.operations.AddOperationStep(ctx, operationID, ports.WorkloadOperationStep{
		StepName: "sandbox_runtime_lifecycle",
		Status:   ports.WorkloadOperationStepFailed,
		Message:  message,
	})
	_, _ = s.operations.UpdateOperation(ctx, operationID, ports.WorkloadOperationUpdate{
		Status:         ports.WorkloadOperationFailed,
		FailureReason:  classifiedLifecycleFailureReason(reason, cause),
		FailureMessage: message,
		RetryEligible:  true,
		UpdatedAt:      updatedAt,
	})
}

func classifiedLifecycleFailureReason(reason string, cause error) string {
	switch {
	case errors.Is(cause, ports.ErrNotFound):
		return reason + ".not_found"
	case errors.Is(cause, ports.ErrNotConfigured):
		return reason + ".not_configured"
	case errors.Is(cause, ports.ErrUnsupported):
		return reason + ".unsupported"
	case errors.Is(cause, ports.ErrInvalid):
		return reason + ".invalid"
	default:
		return reason + ".conflict"
	}
}

func lifecycleIntentFingerprint(request ports.WorkloadInstanceLifecycleRequest) (string, error) {
	request.UserID = ""
	request.PermissionProof = ""
	request.RequestedAt = time.Time{}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("%w: marshal lifecycle intent: %v", ports.ErrInvalid, err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(payload)), nil
}

func validateOperationReplayFingerprint(operation ports.WorkloadOperationRecord, expected string, intent string) error {
	existing, _ := operation.Precheck["request_fingerprint"].(string)
	if strings.TrimSpace(existing) == "" {
		return fmt.Errorf("%w: existing %s operation has no request fingerprint and cannot be safely replayed", ports.ErrConflict, intent)
	}
	if existing != expected {
		return fmt.Errorf("%w: idempotency_key was already used for a different %s intent", ports.ErrConflict, intent)
	}
	return nil
}

func validateLifecycleIntent(record ports.WorkloadInstanceRecord, request ports.WorkloadInstanceLifecycleRequest) error {
	if !isContractLifecycleAction(request.Action) {
		return fmt.Errorf("%w: unsupported instance lifecycle action %q", ports.ErrUnsupported, request.Action)
	}
	if fields := unexpectedLifecycleFields(request); len(fields) > 0 {
		return fmt.Errorf("%w: action %s does not allow fields %s", ports.ErrInvalid, request.Action, strings.Join(fields, ", "))
	}
	if record.Kind == ports.WorkloadKindSandbox && !usesSandboxRuntime(request.Action) {
		return fmt.Errorf("%w: %s is not supported for sandbox instances", ports.ErrUnsupported, request.Action)
	}
	switch request.Action {
	case ports.WorkloadLifecycleResize:
		if strings.TrimSpace(request.Resources.CPU) == "" || strings.TrimSpace(request.Resources.Memory) == "" {
			return fmt.Errorf("%w: cpu and memory are required for resize", ports.ErrInvalid)
		}
	case ports.WorkloadLifecycleRebuild:
		if record.Kind != ports.WorkloadKindVM {
			return fmt.Errorf("%w: rebuild is only supported for vm instances", ports.ErrUnsupported)
		}
	case ports.WorkloadLifecycleSnapshot:
		if record.Kind != ports.WorkloadKindVM {
			return fmt.Errorf("%w: snapshot is only supported for vm instances", ports.ErrUnsupported)
		}
		if strings.TrimSpace(request.SnapshotName) == "" {
			return fmt.Errorf("%w: snapshot_name is required", ports.ErrInvalid)
		}
	case ports.WorkloadLifecycleAttachVolume:
		if !supportsVolumeBinding(record.Kind) {
			return fmt.Errorf("%w: volume binding is only supported for vm, container, and gpu_container instances", ports.ErrUnsupported)
		}
		if strings.TrimSpace(request.VolumeID) == "" || strings.TrimSpace(request.MountPath) == "" {
			return fmt.Errorf("%w: volume_id and mount_path are required for attach_volume", ports.ErrInvalid)
		}
	case ports.WorkloadLifecycleDetachVolume:
		if !supportsVolumeBinding(record.Kind) {
			return fmt.Errorf("%w: volume binding is only supported for vm, container, and gpu_container instances", ports.ErrUnsupported)
		}
		if strings.TrimSpace(request.VolumeID) == "" {
			return fmt.Errorf("%w: volume_id is required for detach_volume", ports.ErrInvalid)
		}
	case ports.WorkloadLifecycleRollback:
		hasRevision := strings.TrimSpace(request.Revision) != ""
		hasSnapshot := strings.TrimSpace(request.SnapshotID) != ""
		if hasRevision == hasSnapshot {
			return fmt.Errorf("%w: rollback requires exactly one of revision or snapshot_id", ports.ErrInvalid)
		}
		if record.Kind == ports.WorkloadKindVM && !hasSnapshot {
			return fmt.Errorf("%w: vm rollback requires snapshot_id", ports.ErrInvalid)
		}
		if (record.Kind == ports.WorkloadKindContainer || record.Kind == ports.WorkloadKindGPUContainer) && !hasRevision {
			return fmt.Errorf("%w: container rollback requires revision", ports.ErrInvalid)
		}
	case ports.WorkloadLifecycleAttachFilesystem, ports.WorkloadLifecycleDetachFilesystem:
		if record.Kind == ports.WorkloadKindSandbox {
			return fmt.Errorf("%w: filesystem binding is not supported for sandbox instances", ports.ErrUnsupported)
		}
		if strings.TrimSpace(request.FilesystemID) == "" {
			return fmt.Errorf("%w: filesystem_id is required", ports.ErrInvalid)
		}
		if request.Action == ports.WorkloadLifecycleAttachFilesystem && strings.TrimSpace(request.MountPath) == "" {
			return fmt.Errorf("%w: mount_path is required for attach_filesystem", ports.ErrInvalid)
		}
	case ports.WorkloadLifecycleScale:
		if record.Kind != ports.WorkloadKindContainer && record.Kind != ports.WorkloadKindGPUContainer {
			return fmt.Errorf("%w: scale is only supported for container and gpu_container instances", ports.ErrUnsupported)
		}
		if request.Replicas == nil || *request.Replicas < 1 {
			return fmt.Errorf("%w: replicas must be at least 1", ports.ErrInvalid)
		}
	case ports.WorkloadLifecycleUpdateImage:
		if record.Kind != ports.WorkloadKindContainer && record.Kind != ports.WorkloadKindGPUContainer {
			return fmt.Errorf("%w: update_image is only supported for container and gpu_container instances", ports.ErrUnsupported)
		}
		if strings.TrimSpace(request.ImageID) == "" {
			return fmt.Errorf("%w: image_id is required", ports.ErrInvalid)
		}
		if request.Strategy != "" && request.Strategy != "rolling" {
			return fmt.Errorf("%w: strategy must be rolling", ports.ErrInvalid)
		}
	case ports.WorkloadLifecycleBindSecret:
		if record.Kind != ports.WorkloadKindContainer && record.Kind != ports.WorkloadKindGPUContainer {
			return fmt.Errorf("%w: bind_secret is only supported for container and gpu_container instances", ports.ErrUnsupported)
		}
		if strings.TrimSpace(request.SecretID) == "" || (request.BindingType != "env" && request.BindingType != "file") {
			return fmt.Errorf("%w: secret_id and binding_type are required", ports.ErrInvalid)
		}
	case ports.WorkloadLifecycleUnbindSecret:
		if record.Kind != ports.WorkloadKindContainer && record.Kind != ports.WorkloadKindGPUContainer {
			return fmt.Errorf("%w: unbind_secret is only supported for container and gpu_container instances", ports.ErrUnsupported)
		}
		if strings.TrimSpace(request.SecretID) == "" {
			return fmt.Errorf("%w: secret_id is required", ports.ErrInvalid)
		}
	case ports.WorkloadLifecycleChangeSecurityGroups:
		if record.Kind == ports.WorkloadKindSandbox {
			return fmt.Errorf("%w: change_security_groups is not supported for sandbox instances", ports.ErrUnsupported)
		}
		if request.SecurityGroupIDs == nil {
			return fmt.Errorf("%w: security_group_ids are required", ports.ErrInvalid)
		}
	case ports.WorkloadLifecycleSetTerminationProtection:
		if record.Kind != ports.WorkloadKindVM && record.Kind != ports.WorkloadKindContainer && record.Kind != ports.WorkloadKindGPUContainer {
			return fmt.Errorf("%w: termination protection is not supported for %s instances", ports.ErrUnsupported, record.Kind)
		}
		if request.Enabled == nil {
			return fmt.Errorf("%w: enabled is required", ports.ErrInvalid)
		}
	case ports.WorkloadLifecyclePause, ports.WorkloadLifecycleResume, ports.WorkloadLifecycleExtend, ports.WorkloadLifecycleTouchIdle:
		if record.Kind != ports.WorkloadKindSandbox {
			return fmt.Errorf("%w: %s is only supported for sandbox instances", ports.ErrUnsupported, request.Action)
		}
		if request.Action == ports.WorkloadLifecycleExtend && request.Duration <= 0 {
			return fmt.Errorf("%w: duration must be positive", ports.ErrInvalid)
		}
	}
	return nil
}

func isContractLifecycleAction(action ports.WorkloadLifecycleAction) bool {
	switch action {
	case ports.WorkloadLifecycleStart,
		ports.WorkloadLifecycleStop,
		ports.WorkloadLifecycleRestart,
		ports.WorkloadLifecycleResize,
		ports.WorkloadLifecycleRebuild,
		ports.WorkloadLifecycleDelete,
		ports.WorkloadLifecycleSnapshot,
		ports.WorkloadLifecycleAttachVolume,
		ports.WorkloadLifecycleDetachVolume,
		ports.WorkloadLifecycleAttachFilesystem,
		ports.WorkloadLifecycleDetachFilesystem,
		ports.WorkloadLifecycleRollback,
		ports.WorkloadLifecycleScale,
		ports.WorkloadLifecycleUpdateImage,
		ports.WorkloadLifecycleBindSecret,
		ports.WorkloadLifecycleUnbindSecret,
		ports.WorkloadLifecycleChangeSecurityGroups,
		ports.WorkloadLifecycleSetTerminationProtection,
		ports.WorkloadLifecyclePause,
		ports.WorkloadLifecycleResume,
		ports.WorkloadLifecycleExtend,
		ports.WorkloadLifecycleTouchIdle:
		return true
	default:
		return false
	}
}

func unexpectedLifecycleFields(request ports.WorkloadInstanceLifecycleRequest) []string {
	present := map[string]bool{
		"cpu":                strings.TrimSpace(request.Resources.CPU) != "",
		"memory":             strings.TrimSpace(request.Resources.Memory) != "",
		"snapshot_name":      strings.TrimSpace(request.SnapshotName) != "",
		"snapshot_id":        strings.TrimSpace(request.SnapshotID) != "",
		"include_data_disks": request.IncludeDataDisks != nil,
		"volume_id":          strings.TrimSpace(request.VolumeID) != "",
		"filesystem_id":      strings.TrimSpace(request.FilesystemID) != "",
		"mount_path":         strings.TrimSpace(request.MountPath) != "",
		"read_only":          request.ReadOnly != nil,
		"revision":           strings.TrimSpace(request.Revision) != "",
		"replicas":           request.Replicas != nil,
		"image_id":           strings.TrimSpace(request.ImageID) != "",
		"strategy":           strings.TrimSpace(request.Strategy) != "",
		"secret_id":          strings.TrimSpace(request.SecretID) != "",
		"binding_type":       strings.TrimSpace(request.BindingType) != "",
		"env_name":           strings.TrimSpace(request.EnvName) != "",
		"security_group_ids": request.SecurityGroupIDs != nil,
		"enabled":            request.Enabled != nil,
		"duration":           request.Duration != 0,
	}
	allowed := map[ports.WorkloadLifecycleAction]map[string]bool{
		ports.WorkloadLifecycleResize:                   fieldSet("cpu", "memory"),
		ports.WorkloadLifecycleSnapshot:                 fieldSet("snapshot_name", "include_data_disks"),
		ports.WorkloadLifecycleAttachVolume:             fieldSet("volume_id", "mount_path", "read_only"),
		ports.WorkloadLifecycleDetachVolume:             fieldSet("volume_id"),
		ports.WorkloadLifecycleAttachFilesystem:         fieldSet("filesystem_id", "mount_path", "read_only"),
		ports.WorkloadLifecycleDetachFilesystem:         fieldSet("filesystem_id"),
		ports.WorkloadLifecycleRollback:                 fieldSet("snapshot_id", "revision"),
		ports.WorkloadLifecycleScale:                    fieldSet("replicas"),
		ports.WorkloadLifecycleUpdateImage:              fieldSet("image_id", "strategy"),
		ports.WorkloadLifecycleBindSecret:               fieldSet("secret_id", "binding_type", "env_name", "mount_path"),
		ports.WorkloadLifecycleUnbindSecret:             fieldSet("secret_id"),
		ports.WorkloadLifecycleChangeSecurityGroups:     fieldSet("security_group_ids"),
		ports.WorkloadLifecycleSetTerminationProtection: fieldSet("enabled"),
		ports.WorkloadLifecycleExtend:                   fieldSet("duration"),
	}
	actionFields := allowed[request.Action]
	unexpected := make([]string, 0)
	for field, isPresent := range present {
		if isPresent && !actionFields[field] {
			unexpected = append(unexpected, field)
		}
	}
	sort.Strings(unexpected)
	return unexpected
}

func fieldSet(fields ...string) map[string]bool {
	set := make(map[string]bool, len(fields))
	for _, field := range fields {
		set[field] = true
	}
	return set
}

func isSandboxLifecycleAction(action ports.WorkloadLifecycleAction) bool {
	switch action {
	case ports.WorkloadLifecyclePause, ports.WorkloadLifecycleResume, ports.WorkloadLifecycleExtend, ports.WorkloadLifecycleTouchIdle:
		return true
	default:
		return false
	}
}

func usesSandboxRuntime(action ports.WorkloadLifecycleAction) bool {
	return isSandboxLifecycleAction(action) || action == ports.WorkloadLifecycleDelete
}

func sandboxResourceRefs(instance ports.SandboxInstanceStatus) []string {
	if len(instance.ResourceRefs) > 0 {
		return append([]string(nil), instance.ResourceRefs...)
	}
	return []string{"sandbox/local/" + instance.InstanceID}
}

func sandboxCreateReason(instance ports.SandboxInstanceStatus) string {
	if instance.DevProfile.RealProvider {
		return "kubernetes sandbox runtime applied Kata RuntimeClass workload"
	}
	return "local sandbox runtime accepted Kata profile intent"
}

func applyApprovedLifecycleSummary(record *ports.WorkloadInstanceRecord, request ports.WorkloadInstanceLifecycleRequest) {
	switch request.Action {
	case ports.WorkloadLifecycleResize:
		record.Compute.CPU = strings.TrimSpace(request.Resources.CPU)
		record.Compute.Memory = strings.TrimSpace(request.Resources.Memory)
	case ports.WorkloadLifecycleScale:
		if record.Container != nil && request.Replicas != nil {
			record.Container.Replicas = *request.Replicas
			if record.Container.ReadyReplicas > *request.Replicas {
				record.Container.ReadyReplicas = *request.Replicas
			}
			record.Container.RolloutStatus = "progressing"
		}
	case ports.WorkloadLifecycleUpdateImage:
		record.Image = ports.InstanceImageSummary{ID: strings.TrimSpace(request.ImageID)}
	case ports.WorkloadLifecycleAttachFilesystem:
		attachment := ports.WorkloadStorageAttachment{
			Name:         strings.TrimSpace(request.FilesystemID),
			Kind:         ports.StorageAttachmentSharedPVC,
			ResourceType: "filesystem",
			ResourceID:   strings.TrimSpace(request.FilesystemID),
			MountPath:    strings.TrimSpace(request.MountPath),
			ReadOnly:     request.ReadOnly != nil && *request.ReadOnly,
			Required:     true,
			SourceRef:    strings.TrimSpace(request.FilesystemID),
			Status:       "mounted",
		}
		record.Status.Storage = append(record.Status.Storage, attachment)
		record.StorageAttachments = append(record.StorageAttachments, attachment)
	case ports.WorkloadLifecycleDetachFilesystem:
		record.Status.Storage = removeStorageResource(record.Status.Storage, "filesystem", request.FilesystemID)
		record.StorageAttachments = removeStorageResource(record.StorageAttachments, "filesystem", request.FilesystemID)
	case ports.WorkloadLifecycleChangeSecurityGroups:
		record.Network.SecurityGroups = make([]ports.InstanceSecurityGroupSummary, 0, len(request.SecurityGroupIDs))
		for _, id := range request.SecurityGroupIDs {
			if id = strings.TrimSpace(id); id != "" {
				record.Network.SecurityGroups = append(record.Network.SecurityGroups, ports.InstanceSecurityGroupSummary{ID: id})
			}
		}
	case ports.WorkloadLifecycleSetTerminationProtection:
		record.Lifecycle.TerminationProtection = *request.Enabled
	}
}

func removeStorageResource(items []ports.WorkloadStorageAttachment, resourceType, resourceID string) []ports.WorkloadStorageAttachment {
	resourceID = strings.TrimSpace(resourceID)
	next := make([]ports.WorkloadStorageAttachment, 0, len(items))
	for _, item := range items {
		if item.ResourceType == resourceType && item.ResourceID == resourceID {
			continue
		}
		next = append(next, item)
	}
	return next
}

func hasStorageResource(items []ports.WorkloadStorageAttachment, resourceType, resourceID string) bool {
	resourceID = strings.TrimSpace(resourceID)
	for _, item := range items {
		if item.ResourceType == resourceType && item.ResourceID == resourceID {
			return true
		}
	}
	return false
}

func (s *LocalInstanceService) bindCreateStorage(ctx context.Context, request ports.WorkloadInstanceCreateRequest, result ports.WorkloadInstanceCreateResult) error {
	if s.storage == nil || (request.Spec.Kind != ports.WorkloadKindContainer && request.Spec.Kind != ports.WorkloadKindGPUContainer) {
		return nil
	}
	attachments := renderStorageAttachments(request.Spec)
	if len(attachments) == 0 {
		return nil
	}
	instanceRoute := "instances/" + result.Ref.InstanceID
	for _, attachment := range attachments {
		switch attachment.ResourceType {
		case "volume":
			if _, err := s.storage.MountVolume(ctx, ports.StorageVolumeMountRequest{
				TenantID:       result.Ref.TenantID,
				VolumeID:       attachment.ResourceID,
				IdempotencyKey: request.IdempotencyKey + ":mount-volume:" + attachment.ResourceID,
				InstanceID:     result.Ref.InstanceID,
				InstanceRoute:  instanceRoute,
				MountName:      firstNonEmpty(attachment.Name, attachment.MountPath),
			}); err != nil {
				return fmt.Errorf("mount volume %q: %w", attachment.ResourceID, err)
			}
		case "filesystem":
			if _, err := s.storage.MountFilesystem(ctx, ports.StorageFilesystemMountRequest{
				TenantID:       result.Ref.TenantID,
				FilesystemID:   attachment.ResourceID,
				IdempotencyKey: request.IdempotencyKey + ":mount-filesystem:" + attachment.ResourceID,
				InstanceID:     result.Ref.InstanceID,
				InstanceRoute:  instanceRoute,
				MountPath:      attachment.MountPath,
				AutoMount:      true,
			}); err != nil {
				return fmt.Errorf("mount filesystem %q: %w", attachment.ResourceID, err)
			}
		}
	}
	return nil
}

func (s *LocalInstanceService) recordCreateTimeline(ctx context.Context, operationID string, spec ports.WorkloadSpec, resourceRefs []string, result ports.WorkloadInstanceCreateResult) error {
	steps := []ports.WorkloadOperationStep{}
	if len(resourceRefs) > 0 {
		steps = append(steps, ports.WorkloadOperationStep{
			StepName: "resolve_resources",
			Status:   ports.WorkloadOperationStepSucceeded,
			Message:  fmt.Sprintf("%d referenced resource(s) resolved", len(resourceRefs)),
		})
	}
	if strings.TrimSpace(spec.Network.SubnetID) != "" || strings.TrimSpace(spec.Network.VPCID) != "" {
		steps = append(steps, ports.WorkloadOperationStep{
			StepName: "network_binding",
			Status:   ports.WorkloadOperationStepSucceeded,
			Message:  createNetworkBindingMessage(spec.Network),
		})
	}
	if storageCount := len(renderStorageAttachments(spec)); storageCount > 0 {
		status := ports.WorkloadOperationStepSucceeded
		message := fmt.Sprintf("%d storage attachment(s) bound for create", storageCount)
		if s.storage == nil {
			message = fmt.Sprintf("%d storage attachment(s) rendered; storage binder not configured", storageCount)
		}
		steps = append(steps, ports.WorkloadOperationStep{StepName: "storage_mount", Status: status, Message: message})
	}
	steps = append(steps,
		ports.WorkloadOperationStep{StepName: "plan", Status: ports.WorkloadOperationStepSucceeded, Message: "workload reference allocated"},
		ports.WorkloadOperationStep{StepName: "render", Status: ports.WorkloadOperationStepSucceeded, Message: fmt.Sprintf("%d provider manifest(s) rendered", len(result.Manifests))},
		ports.WorkloadOperationStep{StepName: "admission", Status: boolStepStatus(result.Admission.Allowed), Message: result.Admission.Reason},
		ports.WorkloadOperationStep{StepName: "audit", Status: nonEmptyStepStatus(result.AuditID), Message: "plan audit recorded"},
		ports.WorkloadOperationStep{StepName: "dry_run", Status: boolStepStatus(result.DryRun.Accepted), Message: result.DryRun.Reason},
		ports.WorkloadOperationStep{StepName: "apply", Status: applyStepStatus(result.Apply.Applied), Message: result.Apply.Reason},
	)
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

func createNetworkBindingMessage(network ports.WorkloadNetworkPolicy) string {
	parts := make([]string, 0, 3)
	if vpcID := strings.TrimSpace(network.VPCID); vpcID != "" {
		parts = append(parts, "vpc="+vpcID)
	}
	if subnetID := strings.TrimSpace(network.SubnetID); subnetID != "" {
		parts = append(parts, "subnet="+subnetID)
	}
	if len(network.SecurityGroupIDs) > 0 {
		parts = append(parts, "security_groups="+joinSecurityGroupIDs(network.SecurityGroupIDs))
	}
	if len(parts) == 0 {
		return "network policy accepted"
	}
	return strings.Join(parts, " ")
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
	if record.Lifecycle.TerminationProtection &&
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
	if request.Action == ports.WorkloadLifecycleResize &&
		record.Kind == ports.WorkloadKindVM &&
		record.Status.State != ports.WorkloadStateStopped {
		return blockedLifecyclePrecheck(details, "vm_resize_requires_stopped", "vm resize requires a stopped instance")
	}
	if request.Action == ports.WorkloadLifecycleAttachVolume || request.Action == ports.WorkloadLifecycleDetachVolume {
		volumeID := strings.TrimSpace(request.VolumeID)
		details["volume_id"] = volumeID
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
	if request.Action == ports.WorkloadLifecycleAttachFilesystem || request.Action == ports.WorkloadLifecycleDetachFilesystem {
		filesystemID := strings.TrimSpace(request.FilesystemID)
		details["filesystem_id"] = filesystemID
		attached := hasStorageResource(record.Status.Storage, "filesystem", filesystemID) ||
			hasStorageResource(record.StorageAttachments, "filesystem", filesystemID)
		if request.Action == ports.WorkloadLifecycleAttachFilesystem && attached {
			return blockedLifecyclePrecheck(details, "filesystem_already_attached", "filesystem is already attached")
		}
		if request.Action == ports.WorkloadLifecycleDetachFilesystem && !attached {
			return blockedLifecyclePrecheck(details, "filesystem_not_attached", "filesystem is not attached")
		}
	}
	if request.Action == ports.WorkloadLifecycleRollback {
		if record.Kind == ports.WorkloadKindVM {
			snapshotID := strings.TrimSpace(request.SnapshotID)
			details["snapshot_id"] = snapshotID
			snapshot, ok := vmRollbackSnapshot(record, snapshotID)
			if !ok {
				return blockedLifecyclePrecheck(details, "rollback_snapshot_not_found", "rollback snapshot was not found for this instance")
			}
			if snapshot.State != "ready" {
				return blockedLifecyclePrecheck(details, "rollback_snapshot_not_ready", "rollback snapshot is not ready")
			}
		} else {
			revision := strings.TrimSpace(request.Revision)
			details["revision"] = revision
			if record.Kind != ports.WorkloadKindContainer && record.Kind != ports.WorkloadKindGPUContainer {
				return blockedLifecyclePrecheck(details, "rollback_unsupported_kind", "rollback is not supported for this instance kind")
			}
			if record.Container == nil {
				return blockedLifecyclePrecheck(details, "container_status_missing", "container rollout status is required for rollback")
			}
			if _, ok := rollbackTarget(record.Container, revision); !ok {
				return blockedLifecyclePrecheck(details, "rollback_revision_not_found", "rollback revision was not found in rollout history")
			}
		}
	}
	return lifecyclePrecheckResult{allowed: true, details: details}
}

func supportsVolumeBinding(kind ports.WorkloadKind) bool {
	return kind == ports.WorkloadKindVM ||
		kind == ports.WorkloadKindContainer ||
		kind == ports.WorkloadKindGPUContainer
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
	case ports.WorkloadLifecycleSnapshot,
		ports.WorkloadLifecycleAttachVolume,
		ports.WorkloadLifecycleDetachVolume,
		ports.WorkloadLifecycleSetTerminationProtection:
		return false
	default:
		return true
	}
}

func lifecycleApplyStepName(request ports.WorkloadInstanceLifecycleRequest) string {
	action := request.Action
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
		if strings.TrimSpace(request.SnapshotID) != "" {
			return "rollback_snapshot"
		}
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
	if request.Action == ports.WorkloadLifecycleRollback && strings.TrimSpace(request.SnapshotID) != "" {
		after["snapshot_id"] = strings.TrimSpace(request.SnapshotID)
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
		Name:         volumeID,
		Kind:         ports.StorageAttachmentDataDisk,
		ResourceType: "volume",
		ResourceID:   volumeID,
		MountPath:    strings.TrimSpace(request.MountPath),
		ReadOnly:     request.ReadOnly != nil && *request.ReadOnly,
		SourceRef:    volumeID,
		Required:     true,
		Status:       "mounted",
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

func sameVolume(attachment ports.WorkloadStorageAttachment, volumeID string) bool {
	volumeID = strings.TrimSpace(volumeID)
	return volumeID != "" && (attachment.ResourceID == volumeID || attachment.Name == volumeID || attachment.SourceRef == volumeID)
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

func vmRollbackSnapshot(record ports.WorkloadInstanceRecord, snapshotID string) (ports.VMInstanceSnapshot, bool) {
	snapshotID = strings.TrimSpace(snapshotID)
	for _, snapshot := range record.Snapshots {
		if snapshot.ID == snapshotID && snapshot.SourceInstanceID == record.InstanceID {
			return snapshot, true
		}
	}
	return ports.VMInstanceSnapshot{}, false
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
