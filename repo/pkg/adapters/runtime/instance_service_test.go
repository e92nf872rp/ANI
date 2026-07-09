package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalInstanceServiceCreatesContainerThroughOrchestrator(t *testing.T) {
	orchestrator := &fakeInstanceOrchestrator{}
	service := NewLocalInstanceService(orchestrator, &fakeInstanceStore{}, NewLocalInstanceOpsGuard())
	result, err := service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
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
	existing, _, err := operations.RecordOperation(context.Background(), ports.WorkloadOperationRecord{
		TenantID:       "tenant-a",
		InstanceID:     "pending:operation-a",
		Operation:      ports.WorkloadLifecycleCreate,
		Status:         ports.WorkloadOperationInProgress,
		IdempotencyKey: "create-key-in-progress",
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

	result, err := service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey: "create-key-in-progress",
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
		t.Fatalf("Create duplicate in-progress error = %v", err)
	}
	if !result.IdempotentReplay || result.OperationID != existing.ID {
		t.Fatalf("result replay=%v op=%q, want replay op %q", result.IdempotentReplay, result.OperationID, existing.ID)
	}
	if orchestrator.creates != 0 {
		t.Fatalf("creates = %d, want 0 for in-progress idempotent replay", orchestrator.creates)
	}
}

func TestLocalInstanceServiceRejectsUnsupportedCreateKind(t *testing.T) {
	_, err := NewLocalInstanceService(&fakeInstanceOrchestrator{}, &fakeInstanceStore{}, NewLocalInstanceOpsGuard()).Create(context.Background(), ports.WorkloadInstanceCreateRequest{
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
	if store.upserts != 1 {
		t.Fatalf("upserts = %d, want 1", store.upserts)
	}

	record, err = service.Delete(context.Background(), ports.WorkloadInstanceLifecycleRequest{
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

func TestMetadataWorkloadIdentityRevokeScansPostgresTextTimestamps(t *testing.T) {
	tx := &fakeMetadataTx{row: fakeMetadataRow{values: []any{
		"00000000-0000-4000-8000-000000000001",
		"5dbb1d01-0000-4000-8000-000000000001",
		"inst_1",
		"ani_wi_5dbb1d010000",
		[]string{"instances:read"},
		false,
		time.Date(2026, 6, 30, 9, 0, 0, 0, time.UTC),
		"",
		"2026-06-30 09:13:14.394711+00",
	}}}
	identity := NewMetadataWorkloadIdentityService(fakeMetadataStore{tx: tx})

	record, err := identity.RevokeForInstance(context.Background(), ports.WorkloadIdentityRevokeRequest{
		TenantID:    "5dbb1d01-0000-4000-8000-000000000001",
		InstanceID:  "inst_1",
		RequestedAt: time.Date(2026, 6, 30, 9, 13, 14, 394711000, time.UTC),
	})
	if err != nil {
		t.Fatalf("RevokeForInstance error = %v", err)
	}
	want := time.Date(2026, 6, 30, 9, 13, 14, 394711000, time.UTC)
	if !record.RevokedAt.Equal(want) {
		t.Fatalf("revoked_at = %s, want %s", record.RevokedAt, want)
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
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{},
		store,
		NewLocalInstanceOpsGuard(),
		WithOperationStore(operations),
	)

	record, err := service.Start(context.Background(), ports.WorkloadInstanceLifecycleRequest{
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
	lifecycle := &fakeLifecycleExecutor{}
	service := NewLocalInstanceServiceWithOptions(
		&fakeInstanceOrchestrator{},
		store,
		NewLocalInstanceOpsGuard(),
		WithOperationStore(operations),
		WithInstanceLifecycleExecutor(lifecycle),
	)

	record, err := service.Rollback(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "rollback-container-a",
		TenantID:        "tenant-a",
		InstanceID:      "container-a",
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
		RequestedAt:     time.Unix(1700, 0),
	})
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if lifecycle.calls != 0 {
		t.Fatalf("lifecycle calls = %d, want 0 for local rollback", lifecycle.calls)
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
	if result.Token == "" || !strings.Contains(result.ConnectURL, "/console/") || !strings.Contains(result.ConnectURL, "token=") {
		t.Fatalf("result token=%q connect=%q, want console websocket url with token", result.Token, result.ConnectURL)
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
	creates int
}

func (o *fakeInstanceOrchestrator) Create(_ context.Context, request ports.WorkloadInstanceCreateRequest) (ports.WorkloadInstanceCreateResult, error) {
	o.creates++
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

func hasOperationStep(steps []ports.WorkloadOperationStep, name string, status ports.WorkloadOperationStepStatus) bool {
	for _, step := range steps {
		if step.StepName == name && step.Status == status {
			return true
		}
	}
	return false
}
