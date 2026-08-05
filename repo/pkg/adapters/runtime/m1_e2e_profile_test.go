package runtime

import (
	"context"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestM1E2EProfileVMContainerAndGPUContainer(t *testing.T) {
	service := newM1E2EService()

	vm := createE2EInstance(t, service, ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "vm-01",
		Kind:     ports.WorkloadKindVM,
		VM: &ports.VMInstanceSpec{
			BootImage: "ubuntu.qcow2",
			RootDisk: ports.WorkloadStorageAttachment{
				Name:    "root",
				Kind:    ports.StorageAttachmentRootDisk,
				SizeGiB: 80,
			},
		},
	})
	assertE2ELifecycle(t, service, vm.Ref.InstanceID, ports.WorkloadKindVM)
	assertE2EQuery(t, service, vm.Ref.InstanceID, ports.WorkloadKindVM)
	if _, err := service.Ops(context.Background(), ports.WorkloadInstanceOpsRequest{
		TenantID:        "tenant-a",
		InstanceID:      vm.Ref.InstanceID,
		Action:          ports.WorkloadInstanceOpsTerminal,
		UserID:          "user-a",
		PermissionProof: "rbac:ops:workload",
	}); err == nil {
		t.Fatalf("VM terminal ops error = nil, want unsupported")
	}
	assertE2EDelete(t, service, vm.Ref.InstanceID)

	container := createE2EInstance(t, service, ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "app-01",
		Kind:     ports.WorkloadKindContainer,
		Image:    "harbor/app:1",
	})
	assertE2ELifecycle(t, service, container.Ref.InstanceID, ports.WorkloadKindContainer)
	assertE2EQuery(t, service, container.Ref.InstanceID, ports.WorkloadKindContainer)
	assertE2EOps(t, service, container.Ref.InstanceID, ports.WorkloadInstanceOpsLogs)
	assertE2EOps(t, service, container.Ref.InstanceID, ports.WorkloadInstanceOpsTerminal)
	assertE2EDelete(t, service, container.Ref.InstanceID)

	gpu := createE2EInstance(t, service, ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "gpu-01",
		Kind:     ports.WorkloadKindGPUContainer,
		Image:    "harbor/runtime:cuda",
		Resources: ports.WorkloadResourceRequest{
			GPU: ports.GPUSchedulingRequest{
				RequiredCount: 1,
			},
		},
	})
	assertE2ELifecycle(t, service, gpu.Ref.InstanceID, ports.WorkloadKindGPUContainer)
	assertE2EQuery(t, service, gpu.Ref.InstanceID, ports.WorkloadKindGPUContainer)
	assertE2EOps(t, service, gpu.Ref.InstanceID, ports.WorkloadInstanceOpsMetrics)
	assertE2EOps(t, service, gpu.Ref.InstanceID, ports.WorkloadInstanceOpsExec)
	assertE2EDelete(t, service, gpu.Ref.InstanceID)
}

func newM1E2EService() ports.WorkloadInstanceService {
	planner := NewPlanningRuntime(WithGPUInventory(fakeGPUInventory{}))
	store := newMemoryInstanceStore()
	orchestrator := NewLocalInstanceOrchestrator(
		planner,
		NewKubernetesDryRunRenderer(planner),
		NewLocalAdmissionGuard(),
		fakePlanAuditStore{},
		NewLocalProviderDryRun(),
		NewLocalProviderApply(WithProviderApplyEnabled(true)),
		NewLocalProviderStatusReader(),
		NewLocalStatusReconciler(),
		WithInstanceStore(store),
	)
	return NewLocalInstanceService(orchestrator, store, NewLocalInstanceOpsGuard(WithInstanceOpsEnabled(true)))
}

func createE2EInstance(t *testing.T, service ports.WorkloadInstanceService, spec ports.WorkloadSpec) ports.WorkloadInstanceCreateResult {
	t.Helper()
	result, err := service.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		IdempotencyKey:  "create-" + string(spec.Kind) + "-" + spec.Name,
		Spec:            spec,
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
	})
	if err != nil {
		t.Fatalf("Create(%s) error = %v", spec.Kind, err)
	}
	if result.Ref.InstanceID == "" {
		t.Fatalf("Create(%s) instance id is empty", spec.Kind)
	}
	if result.FinalStatus.State != ports.WorkloadStateRunning {
		t.Fatalf("Create(%s) state = %s, want running", spec.Kind, result.FinalStatus.State)
	}
	if result.AuditID == "" || !result.DryRun.Accepted || !result.Apply.Applied || !result.Orchestrated {
		t.Fatalf("Create(%s) incomplete orchestration: audit=%q dryRun=%v apply=%v orchestrated=%v", spec.Kind, result.AuditID, result.DryRun.Accepted, result.Apply.Applied, result.Orchestrated)
	}
	return result
}

func assertE2ELifecycle(t *testing.T, service ports.WorkloadInstanceService, instanceID string, kind ports.WorkloadKind) {
	t.Helper()
	stop, err := service.Stop(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "stop-" + instanceID,
		TenantID:        "tenant-a",
		InstanceID:      instanceID,
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
	})
	if err != nil {
		t.Fatalf("Stop(%s) error = %v", instanceID, err)
	}
	if stop.Status.State != ports.WorkloadStateStopped {
		t.Fatalf("Stop(%s) state = %s, want stopped", instanceID, stop.Status.State)
	}
	start, err := service.Start(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "start-" + instanceID,
		TenantID:        "tenant-a",
		InstanceID:      instanceID,
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
	})
	if err != nil {
		t.Fatalf("Start(%s) error = %v", instanceID, err)
	}
	if start.Status.State != ports.WorkloadStateRunning {
		t.Fatalf("Start(%s) state = %s, want running", instanceID, start.Status.State)
	}
	restart, err := service.Restart(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "restart-" + instanceID,
		TenantID:        "tenant-a",
		InstanceID:      instanceID,
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
	})
	if err != nil {
		t.Fatalf("Restart(%s) error = %v", instanceID, err)
	}
	if restart.Status.State != ports.WorkloadStateRunning {
		t.Fatalf("Restart(%s) state = %s, want running", instanceID, restart.Status.State)
	}
	expectedResizeState := ports.WorkloadStateRunning
	if kind == ports.WorkloadKindVM {
		stopped, err := service.Stop(context.Background(), ports.WorkloadInstanceLifecycleRequest{
			IdempotencyKey:  "stop-for-resize-" + instanceID,
			TenantID:        "tenant-a",
			InstanceID:      instanceID,
			UserID:          "user-a",
			PermissionProof: "rbac:update:workload",
		})
		if err != nil {
			t.Fatalf("Stop before resize(%s) error = %v", instanceID, err)
		}
		if stopped.Status.State != ports.WorkloadStateStopped {
			t.Fatalf("Stop before resize(%s) state = %s, want stopped", instanceID, stopped.Status.State)
		}
		expectedResizeState = ports.WorkloadStateStopped
	}
	resize, err := service.Resize(context.Background(), ports.WorkloadInstanceResizeRequest{
		IdempotencyKey:  "resize-" + instanceID,
		TenantID:        "tenant-a",
		InstanceID:      instanceID,
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
		Resources: ports.WorkloadResourceRequest{
			CPU:    "2",
			Memory: "4Gi",
		},
	})
	if err != nil {
		t.Fatalf("Resize(%s) error = %v", instanceID, err)
	}
	if resize.Status.State != expectedResizeState {
		t.Fatalf("Resize(%s) state = %s, want %s", instanceID, resize.Status.State, expectedResizeState)
	}
}

func assertE2EQuery(t *testing.T, service ports.WorkloadInstanceService, instanceID string, kind ports.WorkloadKind) {
	t.Helper()
	record, err := service.Get(context.Background(), ports.WorkloadInstanceGetRequest{
		TenantID:   "tenant-a",
		InstanceID: instanceID,
	})
	if err != nil {
		t.Fatalf("Get(%s) error = %v", instanceID, err)
	}
	if record.Kind != kind {
		t.Fatalf("Get(%s) kind = %s, want %s", instanceID, record.Kind, kind)
	}
	records, err := service.List(context.Background(), ports.WorkloadInstanceListRequest{
		TenantID: "tenant-a",
		Kind:     kind,
	})
	if err != nil {
		t.Fatalf("List(%s) error = %v", kind, err)
	}
	if len(records) == 0 {
		t.Fatalf("List(%s) returned no records", kind)
	}
}

func assertE2EOps(t *testing.T, service ports.WorkloadInstanceService, instanceID string, action ports.WorkloadInstanceOpsAction) {
	t.Helper()
	result, err := service.Ops(context.Background(), ports.WorkloadInstanceOpsRequest{
		TenantID:        "tenant-a",
		InstanceID:      instanceID,
		Action:          action,
		UserID:          "user-a",
		PermissionProof: "rbac:ops:workload",
	})
	if err != nil {
		t.Fatalf("Ops(%s, %s) error = %v", instanceID, action, err)
	}
	if !result.Accepted {
		t.Fatalf("Ops(%s, %s) accepted = false, reason = %s", instanceID, action, result.Reason)
	}
}

func assertE2EDelete(t *testing.T, service ports.WorkloadInstanceService, instanceID string) {
	t.Helper()
	result, err := service.Delete(context.Background(), ports.WorkloadInstanceLifecycleRequest{
		IdempotencyKey:  "delete-" + instanceID,
		TenantID:        "tenant-a",
		InstanceID:      instanceID,
		UserID:          "user-a",
		PermissionProof: "rbac:update:workload",
	})
	if err != nil {
		t.Fatalf("Delete(%s) error = %v", instanceID, err)
	}
	if result.Status.State != ports.WorkloadStateDeleted {
		t.Fatalf("Delete(%s) state = %s, want deleted", instanceID, result.Status.State)
	}
}

type memoryInstanceStore struct {
	records map[string]ports.WorkloadInstanceRecord
}

func newMemoryInstanceStore() *memoryInstanceStore {
	return &memoryInstanceStore{records: map[string]ports.WorkloadInstanceRecord{}}
}

func (s *memoryInstanceStore) UpsertStatus(_ context.Context, record ports.WorkloadInstanceRecord) error {
	s.records[record.TenantID+"/"+record.InstanceID] = record
	return nil
}

func (s *memoryInstanceStore) Get(_ context.Context, tenantID string, instanceID string) (ports.WorkloadInstanceRecord, error) {
	record, ok := s.records[tenantID+"/"+instanceID]
	if !ok {
		return ports.WorkloadInstanceRecord{}, ports.ErrNotFound
	}
	return record, nil
}

func (s *memoryInstanceStore) List(_ context.Context, tenantID string, kind ports.WorkloadKind) ([]ports.WorkloadInstanceRecord, error) {
	var records []ports.WorkloadInstanceRecord
	for _, record := range s.records {
		if record.TenantID != tenantID {
			continue
		}
		if kind != "" && record.Kind != kind {
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

var _ ports.WorkloadInstanceStore = (*memoryInstanceStore)(nil)
