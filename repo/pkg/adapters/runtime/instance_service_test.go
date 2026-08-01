package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalInstanceServiceCreatesContainerThroughOrchestrator(t *testing.T) {
	orchestrator := &fakeInstanceOrchestrator{}
	service := NewLocalInstanceService(orchestrator, &fakeInstanceStore{}, NewLocalInstanceOpsGuard())
	result, err := service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey: "create-app-01",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Name:     "app-01",
			Kind:     ports.WorkloadKindContainer,
			Image:    "harbor/app:1",
		},
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if orchestrator.creates != 1 {
		t.Fatalf("creates = %d, want 1", orchestrator.creates)
	}
	if result.Ref.InstanceID == "" {
		t.Fatalf("instance id is empty")
	}
}

func TestLocalInstanceServiceResolvesReferencedResourcesBeforeOrchestration(t *testing.T) {
	orchestrator := &fakeInstanceOrchestrator{}
	resolver := &capturingInstanceResourceResolver{
		result: ports.WorkloadResourceResolveResult{
			Spec:         ports.WorkloadSpec{ImageSummary: ports.InstanceImageSummary{ID: "resolved-image"}},
			ResourceRefs: []string{"vpc/vpc-1", "volume/vol-1"},
		},
	}
	service := NewLocalInstanceServiceWithOptions(
		orchestrator,
		&fakeInstanceStore{},
		NewLocalInstanceOpsGuard(),
		WithInstanceResourceResolver(resolver),
	)
	request := ports.WorkloadInstanceCreateRequest{
		IdempotencyKey: "create-resolved",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Name:     "app-01",
			Kind:     ports.WorkloadKindContainer,
			ImageID:  "image-requested",
			Network:  ports.WorkloadNetworkPolicy{VPCID: "vpc-1"},
		},
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
	}
	if _, err := service.Create(context.Background(), request); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if resolver.calls != 1 || resolver.request.Spec.ImageID != "image-requested" || resolver.request.UserID != "user-a" {
		t.Fatalf("resolver request = %+v, calls = %d, want original spec and one call", resolver.request, resolver.calls)
	}
	if orchestrator.last.Spec.ImageSummary.ID != "resolved-image" {
		t.Fatalf("orchestrator spec image summary = %+v, want resolver result", orchestrator.last.Spec.ImageSummary)
	}
}

func TestLocalInstanceServiceRequiresCreateIdempotencyKey(t *testing.T) {
	orchestrator := &fakeInstanceOrchestrator{}
	service := NewLocalInstanceService(orchestrator, &fakeInstanceStore{}, NewLocalInstanceOpsGuard())

	_, err := service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Name:     "app-01",
			Kind:     ports.WorkloadKindContainer,
			Image:    "harbor/app:1",
		},
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("Create() error = %v, want ErrInvalid", err)
	}
	if orchestrator.creates != 0 {
		t.Fatalf("orchestrator creates = %d, want 0", orchestrator.creates)
	}
}

func TestLocalInstanceServiceCreateRecordsOperationAndIdempotency(t *testing.T) {
	orchestrator := &fakeInstanceOrchestrator{}
	operations := NewLocalOperationStore(WithOperationStoreClock(func() time.Time {
		return time.Unix(1000, 0)
	}))
	service := NewLocalInstanceServiceWithOptions(
		orchestrator,
		&fakeInstanceStore{},
		NewLocalInstanceOpsGuard(),
		WithOperationStore(operations),
	)
	request := ports.WorkloadInstanceCreateRequest{
		IdempotencyKey: "create-key-1",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Name:     "app-01",
			Kind:     ports.WorkloadKindContainer,
			Image:    "harbor/app:1",
		},
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
		RequestedAt:     time.Unix(900, 0),
	}

	first, err := service.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	if first.OperationID == "" {
		t.Fatalf("OperationID is empty")
	}
	second, err := service.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}
	if second.OperationID != first.OperationID {
		t.Fatalf("duplicate OperationID = %q, want %q", second.OperationID, first.OperationID)
	}
	if !second.IdempotentReplay {
		t.Fatalf("duplicate IdempotentReplay = false, want true")
	}
	if orchestrator.creates != 1 {
		t.Fatalf("creates = %d, want 1 after duplicate idempotency key", orchestrator.creates)
	}
	list, err := operations.ListOperations(context.Background(), ports.WorkloadOperationListRequest{
		TenantID:   "tenant-a",
		InstanceID: first.Ref.InstanceID,
	})
	if err != nil {
		t.Fatalf("ListOperations error = %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("operations = %d, want 1", len(list.Items))
	}
	if list.Items[0].Status != ports.WorkloadOperationSucceeded {
		t.Fatalf("operation status = %s, want succeeded", list.Items[0].Status)
	}
	if list.Items[0].InstanceID != first.Ref.InstanceID {
		t.Fatalf("operation instance id = %q, want %q", list.Items[0].InstanceID, first.Ref.InstanceID)
	}
	if len(list.Items[0].Steps) == 0 {
		t.Fatalf("operation steps are empty")
	}
}

func TestLocalInstanceServiceRejectsIdempotencyKeyReusedForDifferentCreateIntent(t *testing.T) {
	orchestrator := &fakeInstanceOrchestrator{}
	operations := NewLocalOperationStore()
	service := NewLocalInstanceServiceWithOptions(
		orchestrator,
		&fakeInstanceStore{},
		NewLocalInstanceOpsGuard(),
		WithOperationStore(operations),
	)
	first := ports.WorkloadInstanceCreateRequest{
		IdempotencyKey: "create-key-conflict",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Name:     "app-01",
			Kind:     ports.WorkloadKindContainer,
			Image:    "harbor/app:1",
		},
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
	}
	if _, err := service.Create(context.Background(), first); err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}

	second := first
	second.Spec.Name = "app-02"
	second.Spec.Image = "harbor/app:2"
	_, err := service.Create(context.Background(), second)
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("Create(second) error = %v, want ErrConflict", err)
	}
	if orchestrator.creates != 1 {
		t.Fatalf("orchestrator creates = %d, want 1", orchestrator.creates)
	}
}

func TestLocalInstanceServiceCreateBindsWorkloadIdentity(t *testing.T) {
	orchestrator := &fakeInstanceOrchestrator{}
	operations := NewLocalOperationStore()
	identity := NewLocalWorkloadIdentityService(WithWorkloadIdentityClock(func() time.Time {
		return time.Unix(1100, 0)
	}))
	service := NewLocalInstanceServiceWithOptions(
		orchestrator,
		&fakeInstanceStore{},
		NewLocalInstanceOpsGuard(),
		WithOperationStore(operations),
		WithWorkloadIdentityService(identity),
	)

	result, err := service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey: "create-with-identity",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Name:     "app-01",
			Kind:     ports.WorkloadKindContainer,
			Image:    "harbor/app:1",
		},
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
		RequestedAt:     time.Unix(1090, 0),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if result.Identity == nil || result.Identity.KeyValue == "" || !result.Identity.Active {
		t.Fatalf("identity = %+v, want active one-time key binding", result.Identity)
	}
	record, err := identity.GetForInstance(context.Background(), "tenant-a", result.Ref.InstanceID)
	if err != nil {
		t.Fatalf("GetForInstance() error = %v", err)
	}
	if record.KeyValue != "" || record.KeyPrefix != result.Identity.KeyPrefix {
		t.Fatalf("record identity = %+v, want persisted summary without key value", record)
	}
	operation, err := operations.GetOperation(context.Background(), "tenant-a", result.OperationID)
	if err != nil {
		t.Fatalf("GetOperation error = %v", err)
	}
	if !hasOperationStep(operation.Steps, "workload_identity_bind", ports.WorkloadOperationStepSucceeded) {
		t.Fatalf("steps = %#v, want workload_identity_bind succeeded", operation.Steps)
	}
}

func TestLocalInstanceServiceCreateIdempotencyInProgressDoesNotRecreate(t *testing.T) {
	operations := NewLocalOperationStore()
	spec := ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "app-01",
		Kind:     ports.WorkloadKindContainer,
		Image:    "harbor/app:1",
	}
	fingerprint, err := createIntentFingerprint(spec)
	if err != nil {
		t.Fatalf("createIntentFingerprint() error = %v", err)
	}
	existing, _, err := operations.RecordOperation(context.Background(), ports.WorkloadOperationRecord{
		TenantID:       "tenant-a",
		InstanceID:     "pending:operation-a",
		Operation:      ports.WorkloadLifecycleCreate,
		Status:         ports.WorkloadOperationInProgress,
		IdempotencyKey: "create-key-in-progress",
		RequestedBy:    "user-a",
		Precheck:       map[string]any{"request_fingerprint": fingerprint},
	})
	if err != nil {
		t.Fatalf("RecordOperation error = %v", err)
	}
	orchestrator := &fakeInstanceOrchestrator{}
	service := NewLocalInstanceServiceWithOptions(
		orchestrator,
		&fakeInstanceStore{},
		NewLocalInstanceOpsGuard(),
		WithOperationStore(operations),
	)

	result, err := service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey:  "create-key-in-progress",
		Spec:            spec,
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
	})
	if err != nil {
		t.Fatalf("Create duplicate in-progress error = %v", err)
	}
	if !result.IdempotentReplay || result.OperationID != existing.ID {
		t.Fatalf("result replay=%v op=%q, want replay op %q", result.IdempotentReplay, result.OperationID, existing.ID)
	}
	if orchestrator.creates != 0 {
		t.Fatalf("creates = %d, want 0 for in-progress idempotent replay", orchestrator.creates)
	}
}

func TestLocalInstanceServiceRejectsCreateReplayWithoutIntentFingerprint(t *testing.T) {
	operations := NewLocalOperationStore()
	_, _, err := operations.RecordOperation(context.Background(), ports.WorkloadOperationRecord{
		TenantID:       "tenant-a",
		InstanceID:     "pending:legacy-operation",
		Operation:      ports.WorkloadLifecycleCreate,
		Status:         ports.WorkloadOperationInProgress,
		IdempotencyKey: "legacy-create-key",
		RequestedBy:    "user-a",
	})
	if err != nil {
		t.Fatalf("RecordOperation error = %v", err)
	}
	orchestrator := &fakeInstanceOrchestrator{}
	service := NewLocalInstanceServiceWithOptions(
		orchestrator,
		&fakeInstanceStore{},
		NewLocalInstanceOpsGuard(),
		WithOperationStore(operations),
	)

	_, err = service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey: "legacy-create-key",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Name:     "app-01",
			Kind:     ports.WorkloadKindContainer,
			Image:    "harbor/app:1",
		},
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
	})
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("Create() error = %v, want ErrConflict", err)
	}
	if orchestrator.creates != 0 {
		t.Fatalf("creates = %d, want 0", orchestrator.creates)
	}
}

func TestLocalInstanceServiceRejectsSandboxCreateBeforeRecordingWhenRuntimeMissing(t *testing.T) {
	operations := NewLocalOperationStore()
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{},
		&fakeInstanceStore{},
		NewLocalInstanceOpsGuard(),
		WithOperationStore(operations),
	)

	_, err := service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey: "sandbox-missing-runtime",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Name:     "sandbox-a",
			Kind:     ports.WorkloadKindSandbox,
			Image:    "harbor/sandbox:1",
		},
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
	})
	if !errors.Is(err, ports.ErrNotConfigured) {
		t.Fatalf("Create() error = %v, want ErrNotConfigured", err)
	}
	_, err = operations.GetOperationByIdempotencyKey(context.Background(), "tenant-a", "sandbox-missing-runtime")
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("GetOperationByIdempotencyKey() error = %v, want ErrNotFound", err)
	}
}

func TestLocalInstanceServiceReplaysFailedCreateErrorWithoutRecreating(t *testing.T) {
	orchestrator := &fakeInstanceOrchestrator{createErr: ports.ErrNotConfigured}
	service := NewLocalInstanceService(orchestrator, &fakeInstanceStore{}, NewLocalInstanceOpsGuard())
	request := ports.WorkloadInstanceCreateRequest{
		IdempotencyKey: "failed-create-key",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Name:     "app-01",
			Kind:     ports.WorkloadKindContainer,
			Image:    "harbor/app:1",
		},
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
	}

	if _, err := service.Create(context.Background(), request); !errors.Is(err, ports.ErrNotConfigured) {
		t.Fatalf("Create(first) error = %v, want ErrNotConfigured", err)
	}
	if _, err := service.Create(context.Background(), request); !errors.Is(err, ports.ErrNotConfigured) {
		t.Fatalf("Create(replay) error = %v, want ErrNotConfigured", err)
	}
	if orchestrator.creates != 1 {
		t.Fatalf("creates = %d, want 1", orchestrator.creates)
	}
}

func TestLocalInstanceServiceRejectsUnsupportedCreateKind(t *testing.T) {
	_, err := NewLocalInstanceService(&fakeInstanceOrchestrator{}, &fakeInstanceStore{}, NewLocalInstanceOpsGuard()).Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey: "create-unsupported",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Name:     "batch-01",
			Kind:     ports.WorkloadKindBatchJob,
			Image:    "harbor/job:1",
		},
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
	})
	if err == nil {
		t.Fatalf("Create() error = nil, want unsupported kind")
	}
	if !strings.Contains(err.Error(), "vm, container, gpu_container, and sandbox") {
		t.Fatalf("error = %q, want supported kind list", err)
	}
}

func TestLocalInstanceServiceRejectsInvalidApprovedCreateIntent(t *testing.T) {
	tests := []struct {
		name string
		spec ports.WorkloadSpec
	}{
		{
			name: "cross kind vm config",
			spec: ports.WorkloadSpec{
				TenantID: "tenant-a",
				Name:     "container-a",
				Kind:     ports.WorkloadKindContainer,
				VM:       &ports.VMInstanceSpec{},
			},
		},
		{
			name: "disk source modes conflict",
			spec: ports.WorkloadSpec{
				TenantID: "tenant-a",
				Name:     "vm-a",
				Kind:     ports.WorkloadKindVM,
				VM: &ports.VMInstanceSpec{
					SystemDisk: &ports.InstanceDiskSpec{
						VolumeID: "volume-a",
						Name:     "new-disk",
						SizeGiB:  40,
					},
				},
			},
		},
		{
			name: "environment value and secret conflict",
			spec: ports.WorkloadSpec{
				TenantID: "tenant-a",
				Name:     "container-a",
				Kind:     ports.WorkloadKindContainer,
				Container: &ports.ContainerInstanceSpec{
					Env: []ports.InstanceEnvVar{{Name: "TOKEN", Value: stringPointer("plain"), SecretRef: "secret/token"}},
				},
			},
		},
		{
			name: "gpu spec and legacy model conflict",
			spec: ports.WorkloadSpec{
				TenantID:  "tenant-a",
				Name:      "gpu-a",
				Kind:      ports.WorkloadKindGPUContainer,
				Container: &ports.ContainerInstanceSpec{},
				GPUSpec:   &ports.InstanceGPUSpecReference{SpecID: "spec-a", GPUType: "A100", Shares: 8, MBPerShare: 10240},
				Resources: ports.WorkloadResourceRequest{
					GPU: ports.GPUSchedulingRequest{PreferredModels: []string{"H100"}},
				},
			},
		},
		{
			name: "gpu spec and legacy vendor conflict",
			spec: ports.WorkloadSpec{
				TenantID:  "tenant-a",
				Name:      "gpu-a",
				Kind:      ports.WorkloadKindGPUContainer,
				Container: &ports.ContainerInstanceSpec{},
				GPUSpec:   &ports.InstanceGPUSpecReference{SpecID: "spec-a", GPUType: "A100", Shares: 8, MBPerShare: 10240},
				Resources: ports.WorkloadResourceRequest{
					GPU: ports.GPUSchedulingRequest{PreferredVendors: []ports.GPUVendor{ports.GPUVendorNVIDIA}},
				},
			},
		},
		{
			name: "gpu spec and legacy count conflict",
			spec: ports.WorkloadSpec{
				TenantID:  "tenant-a",
				Name:      "gpu-a",
				Kind:      ports.WorkloadKindGPUContainer,
				Container: &ports.ContainerInstanceSpec{},
				GPUSpec:   &ports.InstanceGPUSpecReference{SpecID: "spec-a", GPUType: "A100", Shares: 8, MBPerShare: 10240},
				Resources: ports.WorkloadResourceRequest{
					GPU: ports.GPUSchedulingRequest{RequiredCount: 2},
				},
			},
		},
		{
			name: "gpu spec and legacy allocation mode conflict",
			spec: ports.WorkloadSpec{
				TenantID:  "tenant-a",
				Name:      "gpu-a",
				Kind:      ports.WorkloadKindGPUContainer,
				Container: &ports.ContainerInstanceSpec{},
				GPUSpec:   &ports.InstanceGPUSpecReference{SpecID: "spec-a", GPUType: "A100", Shares: 8, MBPerShare: 10240},
				Resources: ports.WorkloadResourceRequest{
					GPU: ports.GPUSchedulingRequest{VirtualizationModes: []ports.GPUVirtualizationMode{ports.GPUVirtualizationNone}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orchestrator := &fakeInstanceOrchestrator{}
			service := NewLocalInstanceService(orchestrator, &fakeInstanceStore{}, NewLocalInstanceOpsGuard())
			_, err := service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
				IdempotencyKey:  "invalid-create",
				Spec:            tt.spec,
				UserID:          "user-a",
				PermissionProof: "rbac:create:workload",
			})
			if !errors.Is(err, ports.ErrInvalid) {
				t.Fatalf("Create() error = %v, want ErrInvalid", err)
			}
			if orchestrator.creates != 0 {
				t.Fatalf("orchestrator creates = %d, want 0", orchestrator.creates)
			}
		})
	}
}

func TestLocalInstanceServiceQueriesStore(t *testing.T) {
	store := &fakeInstanceStore{
		last: ports.WorkloadInstanceRecord{
			TenantID:   "tenant-a",
			InstanceID: "instance-a",
			Name:       "app-01",
			Kind:       ports.WorkloadKindContainer,
			Status: ports.WorkloadStatus{
				State: ports.WorkloadStateRunning,
			},
		},
	}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())
	record, err := service.Get(context.Background(), ports.WorkloadInstanceGetRequest{
		TenantID:   "tenant-a",
		InstanceID: "instance-a",
	})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if record.Status.State != ports.WorkloadStateRunning {
		t.Fatalf("state = %s, want running", record.Status.State)
	}
	records, err := service.List(context.Background(), ports.WorkloadInstanceListRequest{
		TenantID: "tenant-a",
		Kind:     ports.WorkloadKindContainer,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
}

func TestLocalInstanceServiceFiltersAndSortsApprovedInstanceList(t *testing.T) {
	store := &fakeInstanceStore{
		records: []ports.WorkloadInstanceRecord{
			{
				TenantID:    "tenant-a",
				InstanceID:  "gpu-b",
				Name:        "beta-worker",
				Description: "training pool",
				Kind:        ports.WorkloadKindGPUContainer,
				Image:       ports.InstanceImageSummary{ID: "image-b"},
				Compute:     ports.InstanceComputeSummary{SpecID: "spec-a", NodeName: "node-b"},
				Status:      ports.WorkloadStatus{State: ports.WorkloadStateRunning},
				CreatedAt:   time.Unix(200, 0),
			},
			{
				TenantID:    "tenant-a",
				InstanceID:  "gpu-a",
				Name:        "alpha-worker",
				Description: "training pool",
				Kind:        ports.WorkloadKindGPUContainer,
				Image:       ports.InstanceImageSummary{ID: "image-a"},
				Compute:     ports.InstanceComputeSummary{SpecID: "spec-a", NodeName: "node-a"},
				Status:      ports.WorkloadStatus{State: ports.WorkloadStateRunning},
				CreatedAt:   time.Unix(100, 0),
			},
			{
				TenantID:   "tenant-a",
				InstanceID: "gpu-c",
				Name:       "failed-worker",
				Kind:       ports.WorkloadKindGPUContainer,
				Compute:    ports.InstanceComputeSummary{SpecID: "spec-a"},
				Status:     ports.WorkloadStatus{State: ports.WorkloadStateFailed},
				CreatedAt:  time.Unix(300, 0),
			},
		},
	}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())

	records, err := service.List(context.Background(), ports.WorkloadInstanceListRequest{
		TenantID: "tenant-a",
		Kind:     ports.WorkloadKindGPUContainer,
		State:    ports.WorkloadStateRunning,
		Keyword:  "training",
		SpecID:   "spec-a",
		Sort:     "name_asc",
		Limit:    20,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 2 || records[0].InstanceID != "gpu-a" || records[1].InstanceID != "gpu-b" {
		t.Fatalf("records = %+v, want gpu-a then gpu-b", records)
	}
}

func TestLocalInstanceServiceNodeFilterPrefersReconciledStatus(t *testing.T) {
	store := &fakeInstanceStore{records: []ports.WorkloadInstanceRecord{{
		TenantID: "tenant-a", InstanceID: "instance-a", Kind: ports.WorkloadKindContainer,
		Compute: ports.InstanceComputeSummary{NodeName: "node-old"},
		Status:  ports.WorkloadStatus{State: ports.WorkloadStateRunning, NodeName: "node-current"},
	}}}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())

	current, err := service.List(context.Background(), ports.WorkloadInstanceListRequest{
		TenantID: "tenant-a", NodeName: "node-current",
	})
	if err != nil {
		t.Fatalf("List(current node) error = %v", err)
	}
	if len(current) != 1 {
		t.Fatalf("current node records = %d, want 1", len(current))
	}
	old, err := service.List(context.Background(), ports.WorkloadInstanceListRequest{
		TenantID: "tenant-a", NodeName: "node-old",
	})
	if err != nil {
		t.Fatalf("List(old node) error = %v", err)
	}
	if len(old) != 0 {
		t.Fatalf("old node records = %d, want 0", len(old))
	}
}

func TestLocalInstanceServiceDoesNotTruncateUnpaginatedInternalList(t *testing.T) {
	records := make([]ports.WorkloadInstanceRecord, 25)
	for i := range records {
		records[i] = ports.WorkloadInstanceRecord{
			TenantID: "tenant-a", InstanceID: fmt.Sprintf("instance-%02d", i),
			Kind: ports.WorkloadKindContainer, CreatedAt: time.Unix(int64(i), 0),
		}
	}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, &fakeInstanceStore{records: records}, NewLocalInstanceOpsGuard())

	got, err := service.List(context.Background(), ports.WorkloadInstanceListRequest{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != len(records) {
		t.Fatalf("List() records = %d, want %d", len(got), len(records))
	}
	_, err = service.List(context.Background(), ports.WorkloadInstanceListRequest{TenantID: "tenant-a", Cursor: "opaque"})
	if !errors.Is(err, ports.ErrUnsupported) {
		t.Fatalf("List(cursor) error = %v, want ErrUnsupported", err)
	}
}

func TestLocalInstanceServiceLifecycleOperationsUpdateStore(t *testing.T) {
	store := &fakeInstanceStore{
		last: ports.WorkloadInstanceRecord{
			TenantID:   "tenant-a",
			InstanceID: "instance-a",
			Name:       "app-01",
			Kind:       ports.WorkloadKindContainer,
			Status: ports.WorkloadStatus{
				State: ports.WorkloadStateStopped,
			},
		},
	}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())
	record, err := service.Start(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "start-instance-a",
		TenantID:        "tenant-a",
		InstanceID:      "instance-a",
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
		RequestedAt:     time.Unix(800, 0),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if record.Status.State != ports.WorkloadStateRunning {
		t.Fatalf("state = %s, want running", record.Status.State)
	}
	if !record.Access.ExecAvailable {
		t.Fatalf("running access = %+v, want exec available", record.Access)
	}
	if store.upserts != 1 {
		t.Fatalf("upserts = %d, want 1", store.upserts)
	}

	record, err = service.Delete(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "delete-instance-a",
		TenantID:        "tenant-a",
		InstanceID:      "instance-a",
		UserID:          "user-a",
		PermissionProof: "rbac:delete:workload",
	})
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if record.Status.State != ports.WorkloadStateDeleted {
		t.Fatalf("state = %s, want deleted", record.Status.State)
	}
	if record.Access.ExecAvailable {
		t.Fatalf("deleted access = %+v, want exec unavailable", record.Access)
	}
}

func TestLocalInstanceServiceDeleteRevokesWorkloadIdentity(t *testing.T) {
	store := &fakeInstanceStore{
		last: ports.WorkloadInstanceRecord{
			TenantID:   "tenant-a",
			InstanceID: "instance-a",
			Name:       "app-01",
			Kind:       ports.WorkloadKindContainer,
			Status: ports.WorkloadStatus{
				State: ports.WorkloadStateRunning,
			},
		},
	}
	operations := NewLocalOperationStore()
	identity := NewLocalWorkloadIdentityService()
	if _, err := identity.BindScopedKey(context.Background(), ports.WorkloadIdentityBindRequest{
		TenantID:     "tenant-a",
		InstanceID:   "instance-a",
		InstanceName: "app-01",
		Kind:         ports.WorkloadKindContainer,
	}); err != nil {
		t.Fatalf("BindScopedKey error = %v", err)
	}
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{},
		store,
		NewLocalInstanceOpsGuard(),
		WithOperationStore(operations),
		WithWorkloadIdentityService(identity),
	)

	record, err := service.Delete(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "delete-instance-a",
		TenantID:        "tenant-a",
		InstanceID:      "instance-a",
		UserID:          "user-a",
		PermissionProof: "rbac:delete:workload",
		RequestedAt:     time.Unix(1250, 0),
	})
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if record.Identity == nil || record.Identity.Active {
		t.Fatalf("identity = %+v, want revoked binding", record.Identity)
	}
	operation, err := operations.GetOperation(context.Background(), "tenant-a", record.OperationID)
	if err != nil {
		t.Fatalf("GetOperation error = %v", err)
	}
	if !hasOperationStep(operation.Steps, "workload_identity_revoke", ports.WorkloadOperationStepSucceeded) {
		t.Fatalf("steps = %#v, want workload_identity_revoke succeeded", operation.Steps)
	}
}

func TestLocalInstanceServiceLifecycleRecordsOperation(t *testing.T) {
	store := &fakeInstanceStore{
		last: ports.WorkloadInstanceRecord{
			TenantID:   "tenant-a",
			InstanceID: "instance-a",
			Name:       "app-01",
			Kind:       ports.WorkloadKindContainer,
			Status: ports.WorkloadStatus{
				State: ports.WorkloadStateStopped,
			},
		},
	}
	operations := NewLocalOperationStore()
	lifecycle := &fakeLifecycleExecutor{}
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{},
		store,
		NewLocalInstanceOpsGuard(),
		WithOperationStore(operations),
		WithInstanceLifecycleExecutor(lifecycle),
	)

	record, err := service.Start(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "start-operation-a",
		TenantID:        "tenant-a",
		InstanceID:      "instance-a",
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
		RequestedAt:     time.Unix(1200, 0),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if record.OperationID == "" {
		t.Fatalf("OperationID is empty")
	}
	operation, err := operations.GetOperation(context.Background(), "tenant-a", record.OperationID)
	if err != nil {
		t.Fatalf("GetOperation error = %v", err)
	}
	if operation.Operation != ports.WorkloadLifecycleStart || operation.Status != ports.WorkloadOperationSucceeded {
		t.Fatalf("operation=%s status=%s, want start/succeeded", operation.Operation, operation.Status)
	}
	if len(operation.Steps) == 0 {
		t.Fatalf("operation steps are empty")
	}
	resized, err := service.Resize(context.Background(), ports.WorkloadInstanceResizeRequest{
		IdempotencyKey:  "resize-key-1",
		TenantID:        "tenant-a",
		InstanceID:      "instance-a",
		Resources:       ports.WorkloadResourceRequest{CPU: "4", Memory: "8Gi"},
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
		RequestedAt:     time.Unix(1300, 0),
	})
	if err != nil {
		t.Fatalf("Resize() error = %v", err)
	}
	duplicate, err := service.Resize(context.Background(), ports.WorkloadInstanceResizeRequest{
		IdempotencyKey:  "resize-key-1",
		TenantID:        "tenant-a",
		InstanceID:      "instance-a",
		Resources:       ports.WorkloadResourceRequest{CPU: "4", Memory: "8Gi"},
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
		RequestedAt:     time.Unix(1301, 0),
	})
	if err != nil {
		t.Fatalf("Resize(duplicate) error = %v", err)
	}
	if duplicate.OperationID != resized.OperationID {
		t.Fatalf("duplicate resize operation id = %q, want %q", duplicate.OperationID, resized.OperationID)
	}
	list, err := operations.ListOperations(context.Background(), ports.WorkloadOperationListRequest{
		TenantID:   "tenant-a",
		InstanceID: "instance-a",
	})
	if err != nil {
		t.Fatalf("ListOperations error = %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("operations = %d, want start + resize only", len(list.Items))
	}
}

func TestLocalInstanceServiceTerminationProtectionBlocksDangerousVMOperation(t *testing.T) {
	store := &fakeInstanceStore{
		last: ports.WorkloadInstanceRecord{
			TenantID:   "tenant-a",
			InstanceID: "vm-a",
			Name:       "vm-01",
			Kind:       ports.WorkloadKindVM,
			Lifecycle: ports.InstanceLifecyclePolicy{
				TerminationProtection: true,
			},
			Status: ports.WorkloadStatus{
				State: ports.WorkloadStateRunning,
			},
		},
	}
	operations := NewLocalOperationStore()
	lifecycle := &fakeLifecycleExecutor{}
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{},
		store,
		NewLocalInstanceOpsGuard(),
		WithOperationStore(operations),
		WithInstanceLifecycleExecutor(lifecycle),
	)

	_, err := service.Stop(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "stop-protected-vm",
		TenantID:        "tenant-a",
		InstanceID:      "vm-a",
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
		RequestedAt:     time.Unix(1400, 0),
	})
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("Stop() error = %v, want ErrConflict", err)
	}
	if lifecycle.calls != 0 {
		t.Fatalf("lifecycle calls = %d, want 0 when precheck blocks", lifecycle.calls)
	}
	if store.upserts != 0 {
		t.Fatalf("upserts = %d, want 0 when precheck blocks", store.upserts)
	}
	list, err := operations.ListOperations(context.Background(), ports.WorkloadOperationListRequest{
		TenantID:   "tenant-a",
		InstanceID: "vm-a",
	})
	if err != nil {
		t.Fatalf("ListOperations error = %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("operations = %d, want 1 failed precheck operation", len(list.Items))
	}
	operation := list.Items[0]
	if operation.Status != ports.WorkloadOperationFailed || operation.FailureReason != "termination_protection_enabled" {
		t.Fatalf("operation status=%s reason=%q, want failed termination_protection_enabled", operation.Status, operation.FailureReason)
	}
	if operation.Precheck["allowed"] != false || operation.Precheck["termination_protection"] != true {
		t.Fatalf("precheck = %#v, want denied termination protection", operation.Precheck)
	}
	if len(operation.Steps) != 1 || operation.Steps[0].Status != ports.WorkloadOperationStepFailed {
		t.Fatalf("steps = %#v, want failed precheck step", operation.Steps)
	}
}

func TestLocalInstanceServiceVMSnapshotRecordsLocalProfile(t *testing.T) {
	store := &fakeInstanceStore{
		last: ports.WorkloadInstanceRecord{
			TenantID:   "tenant-a",
			InstanceID: "vm-a",
			Name:       "vm-01",
			Kind:       ports.WorkloadKindVM,
			Provider:   "kubevirt",
			Status: ports.WorkloadStatus{
				State: ports.WorkloadStateRunning,
			},
		},
	}
	operations := NewLocalOperationStore()
	lifecycle := &fakeLifecycleExecutor{}
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{},
		store,
		NewLocalInstanceOpsGuard(),
		WithOperationStore(operations),
		WithInstanceLifecycleExecutor(lifecycle),
	)

	record, err := service.Snapshot(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "snap-vm-a",
		TenantID:        "tenant-a",
		InstanceID:      "vm-a",
		SnapshotName:    "before-upgrade",
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
		RequestedAt:     time.Unix(1500, 0),
	})
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if lifecycle.calls != 0 {
		t.Fatalf("lifecycle calls = %d, want 0 for local snapshot metadata", lifecycle.calls)
	}
	if store.upserts != 1 {
		t.Fatalf("upserts = %d, want 1", store.upserts)
	}
	if record.Status.State != ports.WorkloadStateRunning {
		t.Fatalf("state = %s, want running", record.Status.State)
	}
	if len(record.Snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(record.Snapshots))
	}
	snapshot := record.Snapshots[0]
	if snapshot.ID != "snap_snap-vm-a" || snapshot.Name != "before-upgrade" || snapshot.State != "ready" {
		t.Fatalf("snapshot = %+v, want ready named before-upgrade", snapshot)
	}
	if snapshot.SourceInstanceID != "vm-a" || !snapshot.ReadyAt.Equal(time.Unix(1500, 0)) {
		t.Fatalf("snapshot source=%q ready=%s, want vm-a at request time", snapshot.SourceInstanceID, snapshot.ReadyAt)
	}
	operation, err := operations.GetOperation(context.Background(), "tenant-a", record.OperationID)
	if err != nil {
		t.Fatalf("GetOperation(snapshot) error = %v", err)
	}
	if operation.Operation != ports.WorkloadLifecycleSnapshot || operation.Status != ports.WorkloadOperationSucceeded {
		t.Fatalf("operation=%s status=%s, want snapshot/succeeded", operation.Operation, operation.Status)
	}
	if got := operation.DestructiveImpact["creates_snapshot"]; got != true {
		t.Fatalf("creates_snapshot = %v, want true", got)
	}
	if got := operation.AfterSpec["snapshot_count"]; got != 1 {
		t.Fatalf("after snapshot_count = %v, want 1", got)
	}
	if len(operation.Steps) != 2 || operation.Steps[1].StepName != "create_snapshot" {
		t.Fatalf("steps = %#v, want precheck + create_snapshot", operation.Steps)
	}
}

func TestLocalInstanceServiceVMRollbackUsesReadySnapshot(t *testing.T) {
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "vm-a", Kind: ports.WorkloadKindVM,
		Status: ports.WorkloadStatus{State: ports.WorkloadStateStopped},
		Snapshots: []ports.VMInstanceSnapshot{{
			ID: "snapshot-a", SourceInstanceID: "vm-a", State: "ready",
		}},
	}}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())

	record, err := service.Rollback(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "rollback-vm-a", TenantID: "tenant-a", InstanceID: "vm-a",
		SnapshotID: "snapshot-a", UserID: "user-a", PermissionProof: "rbac:update:workload",
	})
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if record.Status.State != ports.WorkloadStateRunning || store.upserts != 1 {
		t.Fatalf("record state=%s upserts=%d, want running/1", record.Status.State, store.upserts)
	}
}

func TestLocalInstanceServiceRejectsVMRollbackWithUnknownSnapshot(t *testing.T) {
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "vm-a", Kind: ports.WorkloadKindVM,
		Status: ports.WorkloadStatus{State: ports.WorkloadStateStopped},
	}}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())

	_, err := service.Rollback(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "rollback-vm-missing", TenantID: "tenant-a", InstanceID: "vm-a",
		SnapshotID: "snapshot-missing", UserID: "user-a", PermissionProof: "rbac:update:workload",
	})
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("Rollback() error = %v, want ErrConflict", err)
	}
	if store.upserts != 0 {
		t.Fatalf("upserts = %d, want 0", store.upserts)
	}
}

func TestLocalInstanceServiceVMVolumeBindingLocalProfile(t *testing.T) {
	store := &fakeInstanceStore{
		last: ports.WorkloadInstanceRecord{
			TenantID:   "tenant-a",
			InstanceID: "vm-a",
			Name:       "vm-01",
			Kind:       ports.WorkloadKindVM,
			Provider:   "kubevirt",
			Status: ports.WorkloadStatus{
				State: ports.WorkloadStateRunning,
				Storage: []ports.WorkloadStorageAttachment{
					{Name: "vm-root", Kind: ports.StorageAttachmentRootDisk, SourceRef: "images/ubuntu.qcow2", SizeGiB: 40},
				},
			},
		},
	}
	operations := NewLocalOperationStore()
	lifecycle := &fakeLifecycleExecutor{}
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{},
		store,
		NewLocalInstanceOpsGuard(),
		WithOperationStore(operations),
		WithInstanceLifecycleExecutor(lifecycle),
	)

	attached, err := service.AttachVolume(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "attach-volume-a",
		TenantID:        "tenant-a",
		InstanceID:      "vm-a",
		VolumeID:        "vol-data-a",
		MountPath:       "/mnt/vol-data-a",
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
		RequestedAt:     time.Unix(1600, 0),
	})
	if err != nil {
		t.Fatalf("AttachVolume() error = %v", err)
	}
	if lifecycle.calls != 0 {
		t.Fatalf("lifecycle calls = %d, want 0 for local volume binding", lifecycle.calls)
	}
	if attached.Status.State != ports.WorkloadStateRunning || len(attached.Status.Storage) != 2 {
		t.Fatalf("state=%s storage=%d, want running with root+data disk", attached.Status.State, len(attached.Status.Storage))
	}
	if got := attached.Status.Storage[1]; got.Name != "vol-data-a" || got.Kind != ports.StorageAttachmentDataDisk || got.MountPath != "/mnt/vol-data-a" {
		t.Fatalf("attached volume = %+v, want local data disk binding", got)
	}
	attachOperation, err := operations.GetOperation(context.Background(), "tenant-a", attached.OperationID)
	if err != nil {
		t.Fatalf("GetOperation(attach) error = %v", err)
	}
	if attachOperation.Operation != ports.WorkloadLifecycleAttachVolume || attachOperation.Status != ports.WorkloadOperationSucceeded {
		t.Fatalf("attach operation=%s status=%s, want attach_volume/succeeded", attachOperation.Operation, attachOperation.Status)
	}
	if attachOperation.DestructiveImpact["mutates_storage"] != true || attachOperation.AfterSpec["volume_id"] != "vol-data-a" {
		t.Fatalf("attach impact=%#v after=%#v, want storage mutation for vol-data-a", attachOperation.DestructiveImpact, attachOperation.AfterSpec)
	}
	if len(attachOperation.Steps) != 2 || attachOperation.Steps[1].StepName != "attach_volume" {
		t.Fatalf("attach steps = %#v, want attach_volume", attachOperation.Steps)
	}

	detached, err := service.DetachVolume(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "detach-volume-a",
		TenantID:        "tenant-a",
		InstanceID:      "vm-a",
		VolumeID:        "vol-data-a",
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
		RequestedAt:     time.Unix(1610, 0),
	})
	if err != nil {
		t.Fatalf("DetachVolume() error = %v", err)
	}
	if detached.Status.State != ports.WorkloadStateRunning || len(detached.Status.Storage) != 1 {
		t.Fatalf("state=%s storage=%d, want running with root disk only", detached.Status.State, len(detached.Status.Storage))
	}
	detachOperation, err := operations.GetOperation(context.Background(), "tenant-a", detached.OperationID)
	if err != nil {
		t.Fatalf("GetOperation(detach) error = %v", err)
	}
	if detachOperation.Operation != ports.WorkloadLifecycleDetachVolume || detachOperation.Status != ports.WorkloadOperationSucceeded {
		t.Fatalf("detach operation=%s status=%s, want detach_volume/succeeded", detachOperation.Operation, detachOperation.Status)
	}
	if len(detachOperation.Steps) != 2 || detachOperation.Steps[1].StepName != "detach_volume" {
		t.Fatalf("detach steps = %#v, want detach_volume", detachOperation.Steps)
	}
	if store.upserts != 2 {
		t.Fatalf("upserts = %d, want 2", store.upserts)
	}
}

func TestLocalInstanceServiceContainerRollbackLocalProfile(t *testing.T) {
	store := &fakeInstanceStore{
		last: ports.WorkloadInstanceRecord{
			TenantID:   "tenant-a",
			InstanceID: "container-a",
			Name:       "app-01",
			Kind:       ports.WorkloadKindContainer,
			Provider:   "kubernetes",
			Container: &ports.ContainerInstanceStatus{
				Replicas:      3,
				ReadyReplicas: 3,
				Revision:      "rev-v2",
				RolloutStatus: "healthy",
				History: []ports.ContainerRevisionHistory{
					{Revision: "rev-v1", Image: "harbor/app:1", CreatedAt: time.Unix(1500, 0)},
					{Revision: "rev-v2", Image: "harbor/app:2", CreatedAt: time.Unix(1600, 0)},
				},
			},
			Status: ports.WorkloadStatus{
				State: ports.WorkloadStateRunning,
			},
		},
	}
	operations := NewLocalOperationStore()
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{},
		store,
		NewLocalInstanceOpsGuard(),
		WithOperationStore(operations),
	)

	record, err := service.Rollback(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "rollback-container-a",
		TenantID:        "tenant-a",
		InstanceID:      "container-a",
		Revision:        "rev-v1",
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
		RequestedAt:     time.Unix(1700, 0),
	})
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if record.Status.State != ports.WorkloadStateRunning {
		t.Fatalf("state = %s, want running", record.Status.State)
	}
	if record.Container == nil || record.Container.Revision != "rev-v1" || record.Container.RolloutStatus != "rolled_back" {
		t.Fatalf("container = %+v, want rollback to rev-v1", record.Container)
	}
	if len(record.Container.History) != 3 || record.Container.History[2].Revision != "rev-v1" {
		t.Fatalf("history = %#v, want rollback event appended", record.Container.History)
	}
	operation, err := operations.GetOperation(context.Background(), "tenant-a", record.OperationID)
	if err != nil {
		t.Fatalf("GetOperation(rollback) error = %v", err)
	}
	if operation.Operation != ports.WorkloadLifecycleRollback || operation.Status != ports.WorkloadOperationSucceeded {
		t.Fatalf("operation=%s status=%s, want rollback/succeeded", operation.Operation, operation.Status)
	}
	if operation.DestructiveImpact["mutates_rollout"] != true {
		t.Fatalf("impact = %#v, want mutates_rollout", operation.DestructiveImpact)
	}
	if operation.AfterSpec["container_revision"] != "rev-v1" || operation.AfterSpec["container_rollout_status"] != "rolled_back" {
		t.Fatalf("after = %#v, want rolled_back rev-v1", operation.AfterSpec)
	}
	if len(operation.Steps) != 2 || operation.Steps[1].StepName != "rollback_revision" {
		t.Fatalf("steps = %#v, want rollback_revision", operation.Steps)
	}
}

func TestLocalInstanceServiceAppliesApprovedContainerLifecyclePayloads(t *testing.T) {
	store := &fakeInstanceStore{
		last: ports.WorkloadInstanceRecord{
			TenantID:   "tenant-a",
			InstanceID: "container-a",
			Name:       "container-a",
			Kind:       ports.WorkloadKindContainer,
			Container:  &ports.ContainerInstanceStatus{Replicas: 1, ReadyReplicas: 1},
			Status: ports.WorkloadStatus{
				State: ports.WorkloadStateRunning,
			},
		},
	}
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{},
		store,
		NewLocalInstanceOpsGuard(),
		WithOperationStore(NewLocalOperationStore()),
	)

	scaled, err := service.ApplyLifecycle(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "scale-container-a",
		TenantID:        "tenant-a",
		InstanceID:      "container-a",
		Action:          ports.WorkloadLifecycleScale,
		Replicas:        int32Pointer(3),
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
		RequestedAt:     time.Unix(735, 0),
	})
	if err != nil {
		t.Fatalf("ApplyLifecycle(scale) error = %v", err)
	}
	if scaled.Container == nil || scaled.Container.Replicas != 3 {
		t.Fatalf("scaled container = %+v, want replicas=3", scaled.Container)
	}

	mounted, err := service.ApplyLifecycle(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "attach-filesystem-container-a",
		TenantID:        "tenant-a",
		InstanceID:      "container-a",
		Action:          ports.WorkloadLifecycleAttachFilesystem,
		FilesystemID:    "filesystem-a",
		MountPath:       "/shared",
		ReadOnly:        boolPointer(true),
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
		RequestedAt:     time.Unix(736, 0),
	})
	if err != nil {
		t.Fatalf("ApplyLifecycle(attach_filesystem) error = %v", err)
	}
	if len(mounted.StorageAttachments) != 1 {
		t.Fatalf("storage attachments = %+v, want one", mounted.StorageAttachments)
	}
	attachment := mounted.StorageAttachments[0]
	if attachment.ResourceType != "filesystem" || attachment.ResourceID != "filesystem-a" || attachment.MountPath != "/shared" || !attachment.ReadOnly {
		t.Fatalf("filesystem attachment = %+v", attachment)
	}
}

func TestLocalInstanceServiceClearsSecurityGroupsWithExplicitEmptyList(t *testing.T) {
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "vm-a", Kind: ports.WorkloadKindVM,
		Network: ports.InstanceNetworkSummary{
			SecurityGroups: []ports.InstanceSecurityGroupSummary{{ID: "sg-a"}},
		},
		Status: ports.WorkloadStatus{State: ports.WorkloadStateRunning},
	}}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())

	record, err := service.ApplyLifecycle(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "clear-security-groups", TenantID: "tenant-a", InstanceID: "vm-a",
		Action: ports.WorkloadLifecycleChangeSecurityGroups, SecurityGroupIDs: []string{},
		UserID: "user-a", PermissionProof: "rbac:update:workload",
	})
	if err != nil {
		t.Fatalf("ApplyLifecycle() error = %v", err)
	}
	if record.Network.SecurityGroups == nil || len(record.Network.SecurityGroups) != 0 {
		t.Fatalf("security groups = %#v, want explicit empty list", record.Network.SecurityGroups)
	}
}

func TestLocalInstanceServiceUpdatesResizeComputeSummary(t *testing.T) {
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "vm-a", Kind: ports.WorkloadKindVM,
		Compute: ports.InstanceComputeSummary{CPU: "2", Memory: "4Gi", NodeName: "node-a"},
		Status:  ports.WorkloadStatus{State: ports.WorkloadStateStopped},
	}}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())

	record, err := service.Resize(context.Background(), ports.WorkloadInstanceResizeRequest{
		IdempotencyKey: "resize-summary", TenantID: "tenant-a", InstanceID: "vm-a",
		Resources: ports.WorkloadResourceRequest{CPU: "4", Memory: "8Gi"},
		UserID:    "user-a", PermissionProof: "rbac:update:workload",
	})
	if err != nil {
		t.Fatalf("Resize() error = %v", err)
	}
	if record.Compute.CPU != "4" || record.Compute.Memory != "8Gi" || record.Compute.NodeName != "node-a" {
		t.Fatalf("compute = %+v, want updated resources with preserved provider placement", record.Compute)
	}
}

func TestLocalInstanceServiceClearsStaleImageMetadataOnUpdate(t *testing.T) {
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "container-a", Kind: ports.WorkloadKindContainer,
		Image: ports.InstanceImageSummary{
			ID: "image-old", Ref: "registry/old:tag", Digest: "sha256:old", Name: "old", Tag: "tag",
		},
		Container: &ports.ContainerInstanceStatus{Replicas: 1},
		Status:    ports.WorkloadStatus{State: ports.WorkloadStateRunning},
	}}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())

	record, err := service.ApplyLifecycle(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "update-image-summary", TenantID: "tenant-a", InstanceID: "container-a",
		Action: ports.WorkloadLifecycleUpdateImage, ImageID: "image-new",
		UserID: "user-a", PermissionProof: "rbac:update:workload",
	})
	if err != nil {
		t.Fatalf("ApplyLifecycle() error = %v", err)
	}
	if record.Image != (ports.InstanceImageSummary{ID: "image-new"}) {
		t.Fatalf("image = %+v, want only new image ID", record.Image)
	}
}

func TestLocalInstanceServiceRoutesVMRollbackThroughConfiguredProvider(t *testing.T) {
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "vm-a", Kind: ports.WorkloadKindVM,
		Provider: "kubevirt", ResourceRefs: []string{"kubevirt/VirtualMachine/vm-a"},
		Status: ports.WorkloadStatus{State: ports.WorkloadStateStopped},
		Snapshots: []ports.VMInstanceSnapshot{{
			ID: "snapshot-a", SourceInstanceID: "vm-a", State: "ready",
		}},
	}}
	lifecycle := &fakeLifecycleExecutor{}
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard(),
		WithInstanceLifecycleExecutor(lifecycle),
	)

	_, err := service.Rollback(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "rollback-provider-vm", TenantID: "tenant-a", InstanceID: "vm-a",
		SnapshotID: "snapshot-a", UserID: "user-a", PermissionProof: "rbac:update:workload",
	})
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if lifecycle.calls != 1 || lifecycle.action != ports.WorkloadLifecycleRollback {
		t.Fatalf("lifecycle calls=%d action=%s, want provider rollback", lifecycle.calls, lifecycle.action)
	}
}

func TestLocalInstanceServiceRejectsIdempotencyKeyReusedForDifferentLifecycleIntent(t *testing.T) {
	store := &fakeInstanceStore{
		last: ports.WorkloadInstanceRecord{
			TenantID:   "tenant-a",
			InstanceID: "container-a",
			Name:       "container-a",
			Kind:       ports.WorkloadKindContainer,
			Container:  &ports.ContainerInstanceStatus{Replicas: 1},
			Status:     ports.WorkloadStatus{State: ports.WorkloadStateRunning},
		},
	}
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{},
		store,
		NewLocalInstanceOpsGuard(),
		WithOperationStore(NewLocalOperationStore()),
	)
	request := ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "scale-container-conflict",
		TenantID:        "tenant-a",
		InstanceID:      "container-a",
		Action:          ports.WorkloadLifecycleScale,
		Replicas:        int32Pointer(2),
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
	}
	if _, err := service.ApplyLifecycle(context.Background(), request); err != nil {
		t.Fatalf("ApplyLifecycle(first) error = %v", err)
	}

	request.Replicas = int32Pointer(3)
	_, err := service.ApplyLifecycle(context.Background(), request)
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("ApplyLifecycle(second) error = %v, want ErrConflict", err)
	}
	if store.last.Container == nil || store.last.Container.Replicas != 2 {
		t.Fatalf("stored container = %+v, want first intent replicas=2", store.last.Container)
	}
}

func TestLocalInstanceServiceRejectsLifecycleReplayWithoutIntentFingerprint(t *testing.T) {
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "container-a", Kind: ports.WorkloadKindContainer,
		Container: &ports.ContainerInstanceStatus{Replicas: 1},
		Status:    ports.WorkloadStatus{State: ports.WorkloadStateRunning},
	}}
	operations := NewLocalOperationStore()
	_, _, err := operations.RecordOperation(context.Background(), ports.WorkloadOperationRecord{
		TenantID:       "tenant-a",
		InstanceID:     "container-a",
		Operation:      ports.WorkloadLifecycleScale,
		Status:         ports.WorkloadOperationInProgress,
		IdempotencyKey: "legacy-scale-key",
		RequestedBy:    "user-a",
	})
	if err != nil {
		t.Fatalf("RecordOperation() error = %v", err)
	}
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard(), WithOperationStore(operations),
	)

	_, err = service.ApplyLifecycle(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "legacy-scale-key", TenantID: "tenant-a", InstanceID: "container-a",
		Action: ports.WorkloadLifecycleScale, Replicas: int32Pointer(2),
		UserID: "user-a", PermissionProof: "rbac:update:workload",
	})
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("ApplyLifecycle() error = %v, want ErrConflict", err)
	}
	if store.upserts != 0 {
		t.Fatalf("upserts = %d, want 0", store.upserts)
	}
}

func TestLocalInstanceServiceRechecksLifecycleFingerprintAfterAtomicInsert(t *testing.T) {
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "container-a", Kind: ports.WorkloadKindContainer,
		Container: &ports.ContainerInstanceStatus{Replicas: 1},
		Status:    ports.WorkloadStatus{State: ports.WorkloadStateRunning},
	}}
	first := ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "scale-race", TenantID: "tenant-a", InstanceID: "container-a",
		Action: ports.WorkloadLifecycleScale, Replicas: int32Pointer(2),
		UserID: "user-a", PermissionProof: "rbac:update:workload",
	}
	fingerprint, err := lifecycleIntentFingerprint(first)
	if err != nil {
		t.Fatalf("lifecycleIntentFingerprint() error = %v", err)
	}
	operations := &atomicReplayOperationStore{
		LocalOperationStore: NewLocalOperationStore(),
		existing: ports.WorkloadOperationRecord{
			ID: "operation-first", TenantID: "tenant-a", InstanceID: "container-a",
			Operation: ports.WorkloadLifecycleScale, IdempotencyKey: "scale-race",
			Precheck: map[string]any{"request_fingerprint": fingerprint},
		},
	}
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard(), WithOperationStore(operations),
	)
	second := first
	second.Replicas = int32Pointer(3)

	_, err = service.ApplyLifecycle(context.Background(), second)
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("ApplyLifecycle() error = %v, want ErrConflict", err)
	}
	if store.upserts != 0 {
		t.Fatalf("upserts = %d, want 0", store.upserts)
	}
}

func TestLocalInstanceServiceRejectsLifecycleActionOutsideKindMatrix(t *testing.T) {
	store := &fakeInstanceStore{
		last: ports.WorkloadInstanceRecord{
			TenantID:   "tenant-a",
			InstanceID: "vm-a",
			Name:       "vm-a",
			Kind:       ports.WorkloadKindVM,
			Status:     ports.WorkloadStatus{State: ports.WorkloadStateStopped},
		},
	}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())

	_, err := service.ApplyLifecycle(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "scale-vm-a",
		TenantID:        "tenant-a",
		InstanceID:      "vm-a",
		Action:          ports.WorkloadLifecycleScale,
		Replicas:        int32Pointer(2),
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
	})
	if !errors.Is(err, ports.ErrUnsupported) {
		t.Fatalf("ApplyLifecycle(scale vm) error = %v, want ErrUnsupported", err)
	}

	store.last = ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "container-a", Kind: ports.WorkloadKindContainer,
		Status: ports.WorkloadStatus{State: ports.WorkloadStateRunning},
	}
	for _, tc := range []struct {
		action       ports.WorkloadLifecycleAction
		snapshotName string
	}{
		{action: ports.WorkloadLifecycleRebuild},
		{action: ports.WorkloadLifecycleSnapshot, snapshotName: "container-snapshot"},
	} {
		_, err := service.ApplyLifecycle(context.Background(), ports.WorkloadInstanceLifecycleRequest{
			IdempotencyKey: "container-" + string(tc.action), TenantID: "tenant-a", InstanceID: "container-a",
			Action: tc.action, SnapshotName: tc.snapshotName,
			UserID: "user-a", PermissionProof: "rbac:update:workload",
		})
		if !errors.Is(err, ports.ErrUnsupported) {
			t.Fatalf("ApplyLifecycle(%s container) error = %v, want ErrUnsupported", tc.action, err)
		}
	}

	store.last = ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "sandbox-a", Kind: ports.WorkloadKindSandbox,
		Status: ports.WorkloadStatus{State: ports.WorkloadStateRunning},
	}
	for _, action := range []ports.WorkloadLifecycleAction{
		ports.WorkloadLifecycleStart,
		ports.WorkloadLifecycleStop,
		ports.WorkloadLifecycleRestart,
	} {
		_, err := service.ApplyLifecycle(context.Background(), ports.WorkloadInstanceLifecycleRequest{
			IdempotencyKey: "sandbox-" + string(action), TenantID: "tenant-a", InstanceID: "sandbox-a",
			Action: action, UserID: "user-a", PermissionProof: "rbac:update:workload",
		})
		if !errors.Is(err, ports.ErrUnsupported) {
			t.Fatalf("ApplyLifecycle(%s sandbox) error = %v, want ErrUnsupported", action, err)
		}
	}
}

func TestLocalInstanceServiceRejectsRunningVMResize(t *testing.T) {
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "vm-a", Kind: ports.WorkloadKindVM,
		Status: ports.WorkloadStatus{State: ports.WorkloadStateRunning},
	}}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())

	_, err := service.ApplyLifecycle(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "resize-running-vm", TenantID: "tenant-a", InstanceID: "vm-a",
		Action:    ports.WorkloadLifecycleResize,
		Resources: ports.WorkloadResourceRequest{CPU: "4", Memory: "8Gi"},
		UserID:    "user-a", PermissionProof: "rbac:update:workload",
	})
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("ApplyLifecycle(resize running vm) error = %v, want ErrConflict", err)
	}
}

func TestLocalInstanceServiceRequiresLifecycleIdempotencyKey(t *testing.T) {
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "vm-a", Kind: ports.WorkloadKindVM,
		Status: ports.WorkloadStatus{State: ports.WorkloadStateStopped},
	}}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())

	_, err := service.Start(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		TenantID:        "tenant-a",
		InstanceID:      "vm-a",
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("Start() error = %v, want ErrInvalid", err)
	}
	if store.upserts != 0 {
		t.Fatalf("upserts = %d, want 0", store.upserts)
	}
}

func TestLocalInstanceServiceRejectsCrossActionLifecycleFields(t *testing.T) {
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "container-a", Kind: ports.WorkloadKindContainer,
		Container: &ports.ContainerInstanceStatus{Replicas: 1},
		Status:    ports.WorkloadStatus{State: ports.WorkloadStateRunning},
	}}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())

	_, err := service.ApplyLifecycle(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "scale-cross-field",
		TenantID:        "tenant-a",
		InstanceID:      "container-a",
		Action:          ports.WorkloadLifecycleScale,
		Replicas:        int32Pointer(2),
		ImageID:         "image-not-allowed",
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("ApplyLifecycle() error = %v, want ErrInvalid", err)
	}
}

func TestLocalInstanceServiceAppliesTerminationProtectionForContainer(t *testing.T) {
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "container-a", Kind: ports.WorkloadKindContainer,
		Status: ports.WorkloadStatus{State: ports.WorkloadStateRunning},
	}}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())

	protected, err := service.ApplyLifecycle(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "protect-container",
		TenantID:        "tenant-a",
		InstanceID:      "container-a",
		Action:          ports.WorkloadLifecycleSetTerminationProtection,
		Enabled:         boolPointer(true),
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
	})
	if err != nil {
		t.Fatalf("ApplyLifecycle() error = %v", err)
	}
	if !protected.Lifecycle.TerminationProtection {
		t.Fatalf("termination protection = false, want true")
	}
	_, err = service.Delete(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "delete-protected-container", TenantID: "tenant-a", InstanceID: "container-a",
		UserID: "user-a", PermissionProof: "rbac:update:workload",
	})
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("Delete() error = %v, want ErrConflict", err)
	}
}

func TestLocalInstanceServiceTerminationProtectionDoesNotCallProvider(t *testing.T) {
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "vm-a", Kind: ports.WorkloadKindVM,
		Status: ports.WorkloadStatus{State: ports.WorkloadStateRunning},
	}}
	lifecycle := &fakeLifecycleExecutor{}
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard(),
		WithInstanceLifecycleExecutor(lifecycle),
	)

	record, err := service.ApplyLifecycle(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "protect-with-provider", TenantID: "tenant-a", InstanceID: "vm-a",
		Action: ports.WorkloadLifecycleSetTerminationProtection, Enabled: boolPointer(true),
		UserID: "user-a", PermissionProof: "rbac:update:workload",
	})
	if err != nil {
		t.Fatalf("ApplyLifecycle() error = %v", err)
	}
	if lifecycle.calls != 0 || !record.Lifecycle.TerminationProtection {
		t.Fatalf("provider calls=%d lifecycle=%+v, want metadata-only protection", lifecycle.calls, record.Lifecycle)
	}
}

func TestLocalInstanceServiceRejectsNonContractLifecycleAction(t *testing.T) {
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "vm-a", Kind: ports.WorkloadKindVM,
		Status: ports.WorkloadStatus{State: ports.WorkloadStateRunning},
	}}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())

	for _, action := range []ports.WorkloadLifecycleAction{
		ports.WorkloadLifecycleCreate,
		ports.WorkloadLifecycleConsoleSession,
		"",
	} {
		_, err := service.ApplyLifecycle(context.Background(), ports.WorkloadInstanceLifecycleRequest{
			IdempotencyKey: "invalid-" + string(action), TenantID: "tenant-a", InstanceID: "vm-a",
			Action: action, UserID: "user-a", PermissionProof: "rbac:update:workload",
		})
		if !errors.Is(err, ports.ErrUnsupported) {
			t.Fatalf("ApplyLifecycle(%q) error = %v, want ErrUnsupported", action, err)
		}
	}
	if store.upserts != 0 {
		t.Fatalf("upserts = %d, want 0", store.upserts)
	}
}

func TestLocalInstanceServiceRejectsConflictingFilesystemBindings(t *testing.T) {
	attachment := ports.WorkloadStorageAttachment{
		ResourceType: "filesystem", ResourceID: "filesystem-a", MountPath: "/shared",
	}
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "container-a", Kind: ports.WorkloadKindContainer,
		Status:             ports.WorkloadStatus{State: ports.WorkloadStateRunning, Storage: []ports.WorkloadStorageAttachment{attachment}},
		StorageAttachments: []ports.WorkloadStorageAttachment{attachment},
	}}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())

	for _, tc := range []struct {
		name      string
		action    ports.WorkloadLifecycleAction
		id        string
		mountPath string
	}{
		{name: "duplicate attach", action: ports.WorkloadLifecycleAttachFilesystem, id: "filesystem-a", mountPath: "/shared"},
		{name: "missing detach", action: ports.WorkloadLifecycleDetachFilesystem, id: "filesystem-missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.ApplyLifecycle(context.Background(), ports.WorkloadInstanceLifecycleRequest{
				IdempotencyKey:  "filesystem-conflict-" + tc.name,
				TenantID:        "tenant-a",
				InstanceID:      "container-a",
				Action:          tc.action,
				FilesystemID:    tc.id,
				MountPath:       tc.mountPath,
				UserID:          "user-a",
				PermissionProof: "rbac:update:workload",
			})
			if !errors.Is(err, ports.ErrConflict) {
				t.Fatalf("ApplyLifecycle() error = %v, want ErrConflict", err)
			}
		})
	}
}

func TestLocalInstanceServiceSynchronizesVolumeAttachmentSummary(t *testing.T) {
	root := ports.WorkloadStorageAttachment{Name: "root", ResourceType: "volume", ResourceID: "root", Kind: ports.StorageAttachmentRootDisk}
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "vm-a", Kind: ports.WorkloadKindVM,
		Status:             ports.WorkloadStatus{State: ports.WorkloadStateRunning, Storage: []ports.WorkloadStorageAttachment{root}},
		StorageAttachments: []ports.WorkloadStorageAttachment{root},
	}}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())

	attached, err := service.AttachVolume(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "attach-summary", TenantID: "tenant-a", InstanceID: "vm-a",
		VolumeID: "data-a", MountPath: "/data", ReadOnly: boolPointer(true),
		UserID: "user-a", PermissionProof: "rbac:update:workload",
	})
	if err != nil {
		t.Fatalf("AttachVolume() error = %v", err)
	}
	if !hasVolume(attached.StorageAttachments, "data-a") {
		t.Fatalf("storage summary = %+v, want data-a", attached.StorageAttachments)
	}
	got := attached.StorageAttachments[len(attached.StorageAttachments)-1]
	if got.ResourceType != "volume" || got.ResourceID != "data-a" || got.Status != "mounted" || got.MountPath != "/data" || !got.ReadOnly {
		t.Fatalf("attached volume summary = %+v, want canonical requested values", got)
	}

	detached, err := service.DetachVolume(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "detach-summary", TenantID: "tenant-a", InstanceID: "vm-a",
		VolumeID: "data-a", UserID: "user-a", PermissionProof: "rbac:update:workload",
	})
	if err != nil {
		t.Fatalf("DetachVolume() error = %v", err)
	}
	if hasVolume(detached.StorageAttachments, "data-a") {
		t.Fatalf("storage summary = %+v, want data-a removed", detached.StorageAttachments)
	}
}

func TestLocalInstanceServiceAllowsContainerVolumeBinding(t *testing.T) {
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: "container-a", Kind: ports.WorkloadKindContainer,
		Container: &ports.ContainerInstanceStatus{Replicas: 1},
		Status:    ports.WorkloadStatus{State: ports.WorkloadStateRunning},
	}}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())

	attached, err := service.AttachVolume(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "attach-container-volume", TenantID: "tenant-a", InstanceID: "container-a",
		VolumeID: "data-a", MountPath: "/data",
		UserID: "user-a", PermissionProof: "rbac:update:workload",
	})
	if err != nil {
		t.Fatalf("AttachVolume() error = %v", err)
	}
	if !hasVolume(attached.StorageAttachments, "data-a") {
		t.Fatalf("storage summary = %+v, want data-a", attached.StorageAttachments)
	}

	detached, err := service.DetachVolume(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "detach-container-volume", TenantID: "tenant-a", InstanceID: "container-a",
		VolumeID: "data-a", UserID: "user-a", PermissionProof: "rbac:update:workload",
	})
	if err != nil {
		t.Fatalf("DetachVolume() error = %v", err)
	}
	if hasVolume(detached.StorageAttachments, "data-a") {
		t.Fatalf("storage summary = %+v, want data-a removed", detached.StorageAttachments)
	}
}

func TestLocalInstanceServiceUpdatesSandboxRuntimeLifecycle(t *testing.T) {
	sandbox := NewLocalSandboxRuntime(WithSandboxRuntimeClock(func() time.Time { return time.Unix(100, 0) }))
	status, err := sandbox.Create(context.Background(), ports.SandboxCreateRequest{
		TenantID: "tenant-a", Name: "sandbox-a", AutoStart: true,
	})
	if err != nil {
		t.Fatalf("sandbox.Create() error = %v", err)
	}
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: status.InstanceID, Kind: ports.WorkloadKindSandbox,
		Status: ports.WorkloadStatus{State: ports.WorkloadStateRunning}, Sandbox: &status,
	}}
	lifecycle := &fakeLifecycleExecutor{}
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard(),
		WithSandboxRuntime(sandbox), WithInstanceLifecycleExecutor(lifecycle),
	)

	paused, err := service.ApplyLifecycle(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "pause-sandbox", TenantID: "tenant-a", InstanceID: status.InstanceID,
		Action: ports.WorkloadLifecyclePause, UserID: "user-a", PermissionProof: "rbac:update:workload",
		RequestedAt: time.Unix(110, 0),
	})
	if err != nil {
		t.Fatalf("ApplyLifecycle(pause) error = %v", err)
	}
	if paused.Sandbox == nil || paused.Sandbox.SessionState != "paused" {
		t.Fatalf("sandbox = %+v, want paused", paused.Sandbox)
	}

	extended, err := service.ApplyLifecycle(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "extend-sandbox", TenantID: "tenant-a", InstanceID: status.InstanceID,
		Action: ports.WorkloadLifecycleExtend, Duration: 5 * time.Minute,
		UserID: "user-a", PermissionProof: "rbac:update:workload", RequestedAt: time.Unix(120, 0),
	})
	if err != nil {
		t.Fatalf("ApplyLifecycle(extend) error = %v", err)
	}
	if extended.Sandbox == nil || extended.Sandbox.Config.SessionTimeout != 35*time.Minute {
		t.Fatalf("sandbox = %+v, want session timeout 35m", extended.Sandbox)
	}

	deleted, err := service.Delete(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "delete-sandbox", TenantID: "tenant-a", InstanceID: status.InstanceID,
		UserID: "user-a", PermissionProof: "rbac:update:workload", RequestedAt: time.Unix(130, 0),
	})
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.Status.State != ports.WorkloadStateDeleted {
		t.Fatalf("deleted state = %s, want deleted", deleted.Status.State)
	}
	if lifecycle.calls != 0 {
		t.Fatalf("provider lifecycle calls = %d, want sandbox runtime to own delete", lifecycle.calls)
	}
	if _, err := sandbox.Get(context.Background(), ports.SandboxGetRequest{
		TenantID: "tenant-a", InstanceID: status.InstanceID,
	}); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("sandbox runtime Get() after delete error = %v, want ErrNotFound", err)
	}
}

func TestLocalInstanceServiceFinalizesFailedSandboxLifecycleOperation(t *testing.T) {
	status := ports.SandboxInstanceStatus{
		TenantID: "tenant-a", InstanceID: "sandbox-missing", Kind: ports.WorkloadKindSandbox,
		State: ports.SandboxStateRunning, SessionState: "running",
	}
	store := &fakeInstanceStore{last: ports.WorkloadInstanceRecord{
		TenantID: "tenant-a", InstanceID: status.InstanceID, Kind: ports.WorkloadKindSandbox,
		Status: ports.WorkloadStatus{State: ports.WorkloadStateRunning}, Sandbox: &status,
	}}
	operations := NewLocalOperationStore()
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard(),
		WithSandboxRuntime(NewLocalSandboxRuntime()), WithOperationStore(operations),
	)
	request := ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey: "pause-missing-sandbox", TenantID: "tenant-a", InstanceID: status.InstanceID,
		Action: ports.WorkloadLifecyclePause, UserID: "user-a", PermissionProof: "rbac:update:workload",
	}

	if _, err := service.ApplyLifecycle(context.Background(), request); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("ApplyLifecycle(first) error = %v, want ErrNotFound", err)
	}
	operation, err := operations.GetOperationByIdempotencyKey(context.Background(), "tenant-a", request.IdempotencyKey)
	if err != nil {
		t.Fatalf("GetOperationByIdempotencyKey() error = %v", err)
	}
	if operation.Status != ports.WorkloadOperationFailed || operation.FailureReason != "sandbox_lifecycle_failed.not_found" {
		t.Fatalf("operation = %+v, want failed sandbox lifecycle", operation)
	}
	if _, err := service.ApplyLifecycle(context.Background(), request); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("ApplyLifecycle(replay) error = %v, want ErrNotFound", err)
	}
}

func TestLocalInstanceServiceRejectsMissingLifecyclePayloadFields(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kind    ports.WorkloadKind
		action  ports.WorkloadLifecycleAction
		request ports.WorkloadInstanceLifecycleRequest
	}{
		{name: "resize", kind: ports.WorkloadKindVM, action: ports.WorkloadLifecycleResize},
		{name: "snapshot", kind: ports.WorkloadKindVM, action: ports.WorkloadLifecycleSnapshot},
		{name: "attach volume", kind: ports.WorkloadKindVM, action: ports.WorkloadLifecycleAttachVolume, request: ports.WorkloadInstanceLifecycleRequest{VolumeID: "volume-a"}},
		{name: "rollback", kind: ports.WorkloadKindContainer, action: ports.WorkloadLifecycleRollback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := ports.WorkloadInstanceRecord{Kind: tc.kind}
			tc.request.Action = tc.action
			if err := validateLifecycleIntent(record, tc.request); !errors.Is(err, ports.ErrInvalid) {
				t.Fatalf("validateLifecycleIntent() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestLocalInstanceServiceAllowsFileSecretMountPath(t *testing.T) {
	record := ports.WorkloadInstanceRecord{Kind: ports.WorkloadKindContainer}
	err := validateLifecycleIntent(record, ports.WorkloadInstanceLifecycleRequest{
		Action: ports.WorkloadLifecycleBindSecret, SecretID: "secret-a",
		BindingType: "file", MountPath: "/run/secrets/app",
	})
	if err != nil {
		t.Fatalf("validateLifecycleIntent() error = %v", err)
	}
}

func TestValidateInstanceEnvVarAcceptsExplicitEmptyValue(t *testing.T) {
	empty := ""
	if err := validateInstanceEnvVar(ports.InstanceEnvVar{Name: "OPTIONAL_FLAG", Value: &empty}); err != nil {
		t.Fatalf("validateInstanceEnvVar() error = %v", err)
	}
}

func TestLocalInstanceServiceLifecycleUsesProviderExecutor(t *testing.T) {
	store := &fakeInstanceStore{
		last: ports.WorkloadInstanceRecord{
			TenantID:     "tenant-a",
			InstanceID:   "instance-a",
			Name:         "app-01",
			Kind:         ports.WorkloadKindContainer,
			Provider:     "kubernetes",
			ResourceRefs: []string{"kubernetes/Deployment/app-01"},
			Status: ports.WorkloadStatus{
				State: ports.WorkloadStateStopped,
			},
		},
	}
	lifecycle := &fakeLifecycleExecutor{}
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{},
		store,
		NewLocalInstanceOpsGuard(),
		WithInstanceLifecycleExecutor(lifecycle),
	)

	record, err := service.Start(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "start-provider-a",
		TenantID:        "tenant-a",
		InstanceID:      "instance-a",
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if lifecycle.calls != 1 || lifecycle.action != ports.WorkloadLifecycleStart {
		t.Fatalf("lifecycle calls=%d action=%s, want start", lifecycle.calls, lifecycle.action)
	}
	if record.Status.State != ports.WorkloadStateRunning {
		t.Fatalf("state = %s, want running", record.Status.State)
	}
}

func TestLocalInstanceServiceOpsUsesOpsGuard(t *testing.T) {
	store := &fakeInstanceStore{
		last: ports.WorkloadInstanceRecord{
			TenantID:   "tenant-a",
			InstanceID: "instance-a",
			Name:       "app-01",
			Kind:       ports.WorkloadKindContainer,
			Status: ports.WorkloadStatus{
				State: ports.WorkloadStateRunning,
			},
		},
	}
	service := NewLocalInstanceService(&fakeInstanceOrchestrator{}, store, NewLocalInstanceOpsGuard())
	result, err := service.Ops(context.Background(), ports.WorkloadInstanceOpsRequest{
		TenantID:        "tenant-a",
		InstanceID:      "instance-a",
		Action:          ports.WorkloadInstanceOpsLogs,
		UserID:          "user-a",
		PermissionProof: "rbac:read:workload",
	})
	if err != nil {
		t.Fatalf("Ops() error = %v", err)
	}
	if result.Accepted {
		t.Fatalf("Accepted = true, want disabled ops guard")
	}
	if !strings.Contains(result.Reason, "disabled") {
		t.Fatalf("Reason = %q, want disabled", result.Reason)
	}
}

func TestLocalInstanceServiceVMConsoleOpsCreatesSession(t *testing.T) {
	store := &fakeInstanceStore{
		last: ports.WorkloadInstanceRecord{
			TenantID:   "tenant-a",
			InstanceID: "instance-a",
			Name:       "vm-01",
			Kind:       ports.WorkloadKindVM,
			Status: ports.WorkloadStatus{
				State: ports.WorkloadStateRunning,
			},
		},
	}
	operations := NewLocalOperationStore()
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{},
		store,
		NewLocalInstanceOpsGuard(WithInstanceOpsEnabled(true)),
		WithOperationStore(operations),
	)
	result, err := service.Ops(context.Background(), ports.WorkloadInstanceOpsRequest{
		TenantID:        "tenant-a",
		InstanceID:      "instance-a",
		Action:          ports.WorkloadInstanceOpsVMVNC,
		UserID:          "user-a",
		PermissionProof: "rbac:console:workload",
	})
	if err != nil {
		t.Fatalf("Ops(vm_vnc) error = %v", err)
	}
	if !result.Accepted || result.Protocol != "vnc" || result.ConnectURL == "" {
		t.Fatalf("result accepted=%v protocol=%q connect=%q, want vnc session", result.Accepted, result.Protocol, result.ConnectURL)
	}
	if result.OperationID == "" || result.URL != result.ConnectURL || result.ExpiresAt.IsZero() {
		t.Fatalf("result operation=%q url=%q connect=%q expires=%s, want operation/url/expires", result.OperationID, result.URL, result.ConnectURL, result.ExpiresAt)
	}
	operation, err := operations.GetOperation(context.Background(), "tenant-a", result.OperationID)
	if err != nil {
		t.Fatalf("GetOperation(console session) error = %v", err)
	}
	if operation.Operation != ports.WorkloadLifecycleConsoleSession || operation.Status != ports.WorkloadOperationSucceeded {
		t.Fatalf("operation=%s status=%s, want console_session/succeeded", operation.Operation, operation.Status)
	}
	if len(operation.Steps) != 1 || operation.Steps[0].StepName != "issue_session" {
		t.Fatalf("steps = %#v, want issue_session", operation.Steps)
	}
}

type fakeInstanceOrchestrator struct {
	creates   int
	createErr error
	last      ports.WorkloadInstanceCreateRequest
}

func (o *fakeInstanceOrchestrator) Create(_ context.Context, request ports.WorkloadInstanceCreateRequest) (ports.WorkloadInstanceCreateResult, error) {
	o.creates++
	o.last = request
	if o.createErr != nil {
		return ports.WorkloadInstanceCreateResult{}, o.createErr
	}
	return ports.WorkloadInstanceCreateResult{
		Ref: ports.WorkloadRef{
			TenantID:   request.Spec.TenantID,
			InstanceID: "instance-a",
			Kind:       request.Spec.Kind,
		},
		FinalStatus: ports.WorkloadStatus{
			State:     ports.WorkloadStateRunning,
			UpdatedAt: time.Unix(950, 0),
		},
		Admission: ports.WorkloadAdmissionResult{
			Allowed: true,
			Reason:  "accepted",
		},
		DryRun: ports.WorkloadProviderDryRunResult{
			Accepted: true,
			Reason:   "accepted",
		},
		Apply: ports.WorkloadProviderApplyResult{
			Applied:      true,
			Reason:       "applied",
			ResourceRefs: []string{"kubernetes/Deployment/app-01"},
		},
		Observation:  ports.WorkloadProviderObservation{Provider: "kubernetes", Phase: "Running"},
		Reconcile:    ports.WorkloadReconcileResult{Changed: true, Reason: "state reconciled"},
		Orchestrated: true,
	}, nil
}

var _ ports.WorkloadInstanceOrchestrator = (*fakeInstanceOrchestrator)(nil)

type capturingInstanceResourceResolver struct {
	calls   int
	request ports.WorkloadResourceResolveRequest
	result  ports.WorkloadResourceResolveResult
}

func (r *capturingInstanceResourceResolver) ResolveCreate(_ context.Context, request ports.WorkloadResourceResolveRequest) (ports.WorkloadResourceResolveResult, error) {
	r.calls++
	r.request = request
	result := r.result
	result.Spec.TenantID = request.Spec.TenantID
	result.Spec.Name = request.Spec.Name
	result.Spec.Kind = request.Spec.Kind
	result.Spec.ImageID = request.Spec.ImageID
	result.Spec.Network = request.Spec.Network
	return result, nil
}

var _ ports.WorkloadInstanceResourceResolver = (*capturingInstanceResourceResolver)(nil)

type fakeLifecycleExecutor struct {
	calls  int
	action ports.WorkloadLifecycleAction
}

func (e *fakeLifecycleExecutor) Apply(_ context.Context, request ports.WorkloadInstanceLifecycleRequest, _ ports.WorkloadInstanceRecord) (ports.WorkloadInstanceLifecycleResult, error) {
	e.calls++
	e.action = request.Action
	return ports.WorkloadInstanceLifecycleResult{
		Action:   request.Action,
		Accepted: true,
	}, nil
}

var _ ports.WorkloadInstanceLifecycleExecutor = (*fakeLifecycleExecutor)(nil)

type atomicReplayOperationStore struct {
	*LocalOperationStore
	existing ports.WorkloadOperationRecord
}

func (s *atomicReplayOperationStore) GetOperationByIdempotencyKey(_ context.Context, _, _ string) (ports.WorkloadOperationRecord, error) {
	return ports.WorkloadOperationRecord{}, ports.ErrNotFound
}

func (s *atomicReplayOperationStore) RecordOperation(_ context.Context, _ ports.WorkloadOperationRecord) (ports.WorkloadOperationRecord, bool, error) {
	return s.existing, true, nil
}

func hasOperationStep(steps []ports.WorkloadOperationStep, name string, status ports.WorkloadOperationStepStatus) bool {
	for _, step := range steps {
		if step.StepName == name && step.Status == status {
			return true
		}
	}
	return false
}

func stringPointer(value string) *string { return &value }

func int32Pointer(value int32) *int32 { return &value }

func boolPointer(value bool) *bool { return &value }
