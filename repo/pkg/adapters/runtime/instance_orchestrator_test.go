package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalInstanceOrchestratorCreatesAndReconciles(t *testing.T) {
	store := &fakeInstanceStore{}
	orchestrator := newTestInstanceOrchestrator(true, store)
	result, err := orchestrator.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Name:     "app-01",
			Kind:     ports.WorkloadKindContainer,
			Image:    "harbor/app:1",
		},
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
		RequestedAt:     time.Unix(500, 0),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if result.AuditID == "" {
		t.Fatalf("AuditID is empty")
	}
	if !result.Admission.Allowed {
		t.Fatalf("Admission.Allowed = false, reason = %s", result.Admission.Reason)
	}
	if !result.DryRun.Accepted {
		t.Fatalf("DryRun.Accepted = false, reason = %s", result.DryRun.Reason)
	}
	if !result.Apply.Applied {
		t.Fatalf("Apply.Applied = false, reason = %s", result.Apply.Reason)
	}
	if !result.Orchestrated {
		t.Fatalf("Orchestrated = false, want true")
	}
	if result.FinalStatus.State != ports.WorkloadStateRunning {
		t.Fatalf("FinalStatus.State = %s, want running", result.FinalStatus.State)
	}
	if store.upserts != 2 {
		t.Fatalf("store upserts = %d, want 2", store.upserts)
	}
	if store.last.Status.State != ports.WorkloadStateRunning {
		t.Fatalf("stored state = %s, want running", store.last.Status.State)
	}
}

func TestInstanceAccessSummaryRequiresRunningState(t *testing.T) {
	for _, state := range []ports.WorkloadState{
		ports.WorkloadStatePending,
		ports.WorkloadStateProvisioning,
		ports.WorkloadStateStopped,
		ports.WorkloadStateFailed,
	} {
		vm := instanceAccessSummary(ports.WorkloadKindVM, state)
		container := instanceAccessSummary(ports.WorkloadKindContainer, state)
		if vm.SSHAvailable || vm.ConsoleAvailable || container.ExecAvailable {
			t.Fatalf("state %s reports interactive access: vm=%+v container=%+v", state, vm, container)
		}
	}
	if running := instanceAccessSummary(ports.WorkloadKindContainer, ports.WorkloadStateRunning); !running.ExecAvailable {
		t.Fatalf("running container access = %+v, want exec available", running)
	}
}

func TestInstanceStorageAttachmentsNormalizesLegacyEntries(t *testing.T) {
	attachments := instanceStorageAttachments(ports.WorkloadSpec{
		Storage: []ports.WorkloadStorageAttachment{
			{Name: "root", Kind: ports.StorageAttachmentRootDisk},
			{Name: "shared", Kind: ports.StorageAttachmentSharedPVC, MountPath: "/shared"},
		},
	}, ports.WorkloadStatus{})

	if len(attachments) != 2 {
		t.Fatalf("attachments = %+v, want two", attachments)
	}
	if got := attachments[0]; got.ResourceType != "volume" || got.ResourceID != "root" || got.Status != "attached" {
		t.Fatalf("root attachment = %+v, want contract-valid volume summary", got)
	}
	if got := attachments[1]; got.ResourceType != "filesystem" || got.ResourceID != "shared" || got.Status != "mounted" {
		t.Fatalf("filesystem attachment = %+v, want contract-valid filesystem summary", got)
	}
}

func TestGPUStatusInfoRequiresNodeEvidenceForScheduledState(t *testing.T) {
	spec := ports.WorkloadSpec{Kind: ports.WorkloadKindGPUContainer}
	pending := gpuStatusInfo(spec, ports.WorkloadStatus{State: ports.WorkloadStateProvisioning})
	scheduled := gpuStatusInfo(spec, ports.WorkloadStatus{
		State: ports.WorkloadStateProvisioning, NodeName: "gpu-node-a",
	})

	if pending.SchedulingState != "pending" {
		t.Fatalf("pending scheduling state = %q, want pending", pending.SchedulingState)
	}
	if scheduled.SchedulingState != "scheduled" {
		t.Fatalf("scheduled state = %q, want scheduled", scheduled.SchedulingState)
	}
}

func TestLocalInstanceOrchestratorPersistsApprovedInstanceSummaries(t *testing.T) {
	store := &fakeInstanceStore{}
	orchestrator := newTestInstanceOrchestrator(true, store)

	_, err := orchestrator.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		Spec: ports.WorkloadSpec{
			TenantID:    "tenant-a",
			Name:        "gpu-app-01",
			Description: "approved contract summary",
			Kind:        ports.WorkloadKindGPUContainer,
			ImageID:     "image-a",
			ImageRef:    "harbor.example/tenant-a/gpu-app@sha256:abc",
			Image:       "harbor.example/tenant-a/gpu-app:1",
			ImageSummary: ports.InstanceImageSummary{
				ID:           "image-a",
				Ref:          "harbor.example/tenant-a/gpu-app@sha256:abc",
				Digest:       "sha256:abc",
				Name:         "gpu-app",
				Tag:          "1",
				Purpose:      "gpu",
				Architecture: "amd64",
			},
			Resources: ports.WorkloadResourceRequest{
				CPU:    "4",
				Memory: "16Gi",
				GPU: ports.GPUSchedulingRequest{
					PreferredModels: []string{"A100"},
					RequiredCount:   1,
					VirtualizationModes: []ports.GPUVirtualizationMode{
						ports.GPUVirtualizationVGPU,
					},
				},
			},
			GPUSpec: &ports.InstanceGPUSpecReference{
				SpecID:     "gpu-spec-a100-10g",
				GPUType:    "A100",
				Shares:     8,
				MBPerShare: 10240,
			},
			Network: ports.WorkloadNetworkPolicy{
				TenantIsolated:   true,
				VPCID:            "vpc-a",
				SubnetID:         "subnet-a",
				SecurityGroupIDs: []string{"sg-a"},
				PrivateIP:        "10.20.0.8",
			},
			Storage: []ports.WorkloadStorageAttachment{
				{
					Name:         "data-a",
					Kind:         ports.StorageAttachmentDataDisk,
					ResourceType: "volume",
					ResourceID:   "volume-a",
					MountPath:    "/data",
					Status:       "attached",
					TaskID:       "task-volume-a",
				},
				{
					Name:         "shared-a",
					Kind:         ports.StorageAttachmentSharedPVC,
					ResourceType: "filesystem",
					ResourceID:   "filesystem-a",
					MountPath:    "/shared",
					Status:       "mounted",
				},
			},
			Container: &ports.ContainerInstanceSpec{Replicas: 1},
			Labels:    map[string]string{"team": "platform"},
			Lifecycle: ports.InstanceLifecyclePolicy{AutoStart: true},
		},
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
		RequestedAt:     time.Unix(505, 0),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if store.last.Description != "approved contract summary" || store.last.Labels["team"] != "platform" {
		t.Fatalf("stored description/labels = %q/%v", store.last.Description, store.last.Labels)
	}
	if store.last.Image.ID != "image-a" || store.last.Image.Digest != "sha256:abc" {
		t.Fatalf("stored image = %+v", store.last.Image)
	}
	if store.last.Compute.SpecID != "gpu-spec-a100-10g" || store.last.Compute.GPUShares != 8 {
		t.Fatalf("stored compute = %+v", store.last.Compute)
	}
	if store.last.GPU == nil || store.last.GPU.SpecID != "gpu-spec-a100-10g" || store.last.GPU.Shares != 8 {
		t.Fatalf("stored gpu = %+v", store.last.GPU)
	}
	if store.last.Network.VPCID != "vpc-a" || store.last.Network.SubnetID != "subnet-a" {
		t.Fatalf("stored network = %+v", store.last.Network)
	}
	if len(store.last.StorageAttachments) != 2 || store.last.StorageAttachments[0].TaskID != "task-volume-a" {
		t.Fatalf("stored attachments = %+v", store.last.StorageAttachments)
	}
	if !store.last.Access.ExecAvailable || store.last.Access.ConsoleAvailable {
		t.Fatalf("stored access = %+v", store.last.Access)
	}
}

func TestLocalInstanceOrchestratorStopsBeforeObservationWhenApplyDisabled(t *testing.T) {
	store := &fakeInstanceStore{}
	orchestrator := newTestInstanceOrchestrator(false, store)
	result, err := orchestrator.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
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
	if result.Apply.Applied {
		t.Fatalf("Apply.Applied = true, want false")
	}
	if result.Orchestrated {
		t.Fatalf("Orchestrated = true, want false")
	}
	if result.FinalStatus.State != ports.WorkloadStatePending {
		t.Fatalf("FinalStatus.State = %s, want pending", result.FinalStatus.State)
	}
	if store.upserts != 1 {
		t.Fatalf("store upserts = %d, want 1", store.upserts)
	}
}

func TestLocalInstanceOrchestratorBuildsVMSSHConnectionInfo(t *testing.T) {
	store := &fakeInstanceStore{}
	orchestrator := newTestInstanceOrchestrator(true, store)
	result, err := orchestrator.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Name:     "vm-01",
			Kind:     ports.WorkloadKindVM,
			VM: &ports.VMInstanceSpec{
				BootImage:    "images/ubuntu-22.04.qcow2",
				SSHUsername:  "ani",
				SSHKeySecret: "secret/ssh-key-a",
				RootDisk: ports.WorkloadStorageAttachment{
					Name:      "vm-01-root",
					Kind:      ports.StorageAttachmentRootDisk,
					SizeGiB:   40,
					SourceRef: "images/ubuntu-22.04.qcow2",
					Required:  true,
				},
			},
			Lifecycle: ports.InstanceLifecyclePolicy{AutoStart: true},
		},
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
	})
	if err != nil {
		t.Fatalf("Create(vm) error = %v", err)
	}
	if result.Ref.InstanceID == "" {
		t.Fatalf("instance id is empty")
	}
	if store.last.SSH == nil {
		t.Fatalf("stored SSH is nil")
	}
	if store.last.SSH.Username != "ani" || store.last.SSH.KeyRef != "secret/ssh-key-a" {
		t.Fatalf("ssh = %+v, want configured username/key ref", store.last.SSH)
	}
	if store.last.SSH.Host == "" || store.last.SSH.Port != 22 || !store.last.SSH.Ready {
		t.Fatalf("ssh = %+v, want host/22/ready", store.last.SSH)
	}
}

func TestLocalInstanceOrchestratorBuildsContainerRolloutStatus(t *testing.T) {
	store := &fakeInstanceStore{}
	orchestrator := newTestInstanceOrchestrator(true, store)
	result, err := orchestrator.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Name:     "app-01",
			Kind:     ports.WorkloadKindContainer,
			Image:    "harbor/app:1",
			Container: &ports.ContainerInstanceSpec{
				Ports:    []int32{8080},
				Replicas: 3,
			},
			Lifecycle: ports.InstanceLifecyclePolicy{AutoStart: true},
		},
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
		RequestedAt:     time.Unix(700, 0),
	})
	if err != nil {
		t.Fatalf("Create(container) error = %v", err)
	}
	if result.FinalStatus.State != ports.WorkloadStateRunning {
		t.Fatalf("state = %s, want running", result.FinalStatus.State)
	}
	if store.last.Container == nil {
		t.Fatalf("stored Container is nil")
	}
	if store.last.Container.Replicas != 3 || store.last.Container.ReadyReplicas != 3 {
		t.Fatalf("container replicas=%d ready=%d, want 3/3", store.last.Container.Replicas, store.last.Container.ReadyReplicas)
	}
	if store.last.Container.Revision == "" || store.last.Container.RolloutStatus != "healthy" {
		t.Fatalf("container revision=%q rollout=%q, want revision + healthy", store.last.Container.Revision, store.last.Container.RolloutStatus)
	}
	if len(store.last.Container.History) != 1 || store.last.Container.History[0].Image != "harbor/app:1" {
		t.Fatalf("history = %#v, want one image revision", store.last.Container.History)
	}
}

func TestLocalInstanceOrchestratorBuildsGPUStatus(t *testing.T) {
	store := &fakeInstanceStore{}
	orchestrator := newTestInstanceOrchestrator(true, store)
	_, err := orchestrator.Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Name:     "gpu-01",
			Kind:     ports.WorkloadKindGPUContainer,
			Image:    "harbor/gpu:1",
			Resources: ports.WorkloadResourceRequest{
				GPU: ports.GPUSchedulingRequest{
					PreferredVendors: []ports.GPUVendor{ports.GPUVendorNVIDIA},
					PreferredModels:  []string{"A100"},
					RequiredCount:    2,
				},
			},
			Container: &ports.ContainerInstanceSpec{Ports: []int32{8080}, Replicas: 1},
			Lifecycle: ports.InstanceLifecyclePolicy{AutoStart: true},
		},
		UserID:          "user-a",
		PermissionProof: "rbac:create:workload",
		RequestedAt:     time.Unix(710, 0),
	})
	if err != nil {
		t.Fatalf("Create(gpu) error = %v", err)
	}
	if store.last.GPU == nil {
		t.Fatalf("stored GPU is nil")
	}
	if store.last.GPU.Vendor != ports.GPUVendorNVIDIA || store.last.GPU.Model != "A100" || store.last.GPU.Count != 2 {
		t.Fatalf("gpu = %+v, want nvidia/A100 x2", store.last.GPU)
	}
	if store.last.GPU.SchedulingReason == "" {
		t.Fatalf("gpu scheduling reason is empty")
	}
	if store.last.GPU.UtilizationPercent < 0 || store.last.GPU.UtilizationPercent > 100 {
		t.Fatalf("gpu utilization = %f, want 0..100", store.last.GPU.UtilizationPercent)
	}
}

// TestResolvedGPUModelPrefersSelectedNodeModel verifies that when the
// planning runtime writes the real selected node model into annotations
// (ani.kubercloud.io/gpu-selected-model), gpuStatusInfo uses it instead of
// the PreferredModels from the request. This ensures the demo API reports
// the actual GPU hardware (e.g. RTX4090) rather than the requested default
// (e.g. A100).
func TestResolvedGPUModelPrefersSelectedNodeModel(t *testing.T) {
	spec := ports.WorkloadSpec{
		Kind: ports.WorkloadKindGPUContainer,
		Resources: ports.WorkloadResourceRequest{
			GPU: ports.GPUSchedulingRequest{
				PreferredVendors: []ports.GPUVendor{ports.GPUVendorNVIDIA},
				PreferredModels:  []string{"A100"},
				RequiredCount:    1,
			},
		},
		Annotations: map[string]string{
			"ani.kubercloud.io/gpu-selected-model": "RTX4090",
		},
	}
	info := gpuStatusInfo(spec, ports.WorkloadStatus{State: ports.WorkloadStateRunning})
	if info == nil {
		t.Fatalf("gpuStatusInfo returned nil")
	}
	if info.Model != "RTX4090" {
		t.Fatalf("Model = %q, want RTX4090 (from annotation)", info.Model)
	}
	if !strings.Contains(info.SchedulingReason, "RTX4090") {
		t.Fatalf("SchedulingReason = %q, want it to contain RTX4090", info.SchedulingReason)
	}
}

// TestResolvedGPUModelFallsBackToPreferredModels verifies that without the
// selected-node-model annotation, gpuStatusInfo falls back to the
// PreferredModels in the request (preserving existing behavior).
func TestResolvedGPUModelFallsBackToPreferredModels(t *testing.T) {
	spec := ports.WorkloadSpec{
		Kind: ports.WorkloadKindGPUContainer,
		Resources: ports.WorkloadResourceRequest{
			GPU: ports.GPUSchedulingRequest{
				PreferredVendors: []ports.GPUVendor{ports.GPUVendorNVIDIA},
				PreferredModels:  []string{"A100"},
				RequiredCount:    1,
			},
		},
	}
	info := gpuStatusInfo(spec, ports.WorkloadStatus{State: ports.WorkloadStateRunning})
	if info == nil {
		t.Fatalf("gpuStatusInfo returned nil")
	}
	if info.Model != "A100" {
		t.Fatalf("Model = %q, want A100 (fallback to PreferredModels)", info.Model)
	}
}

func TestLocalInstanceOrchestratorRequiresPermissionProof(t *testing.T) {
	_, err := newTestInstanceOrchestrator(true, &fakeInstanceStore{}).Create(context.Background(), ports.WorkloadInstanceCreateRequest{
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Name:     "app-01",
			Kind:     ports.WorkloadKindContainer,
			Image:    "harbor/app:1",
		},
		UserID: "user-a",
	})
	if err == nil {
		t.Fatalf("Create() error = nil, want permission proof error")
	}
	if !strings.Contains(err.Error(), "permission proof") {
		t.Fatalf("error = %q, want permission proof", err)
	}
}

func newTestInstanceOrchestrator(applyEnabled bool, store ports.WorkloadInstanceStore) *LocalInstanceOrchestrator {
	planner := NewPlanningRuntime(WithGPUInventory(fakeGPUInventory{}))
	return NewLocalInstanceOrchestrator(
		planner,
		NewKubernetesDryRunRenderer(planner),
		NewLocalAdmissionGuard(),
		fakePlanAuditStore{},
		NewLocalProviderDryRun(),
		NewLocalProviderApply(WithProviderApplyEnabled(applyEnabled)),
		NewLocalProviderStatusReader(),
		NewLocalStatusReconciler(),
		WithInstanceStore(store),
	)
}

type fakePlanAuditStore struct{}

func (fakePlanAuditStore) RecordPlan(_ context.Context, record ports.WorkloadPlanAuditRecord) (string, error) {
	if record.TenantID == "" || record.InstanceName == "" || record.WorkloadKind == "" {
		return "", ports.ErrInvalid
	}
	return "audit-a", nil
}

var _ ports.WorkloadPlanAuditStore = fakePlanAuditStore{}

type fakeInstanceStore struct {
	upserts int
	last    ports.WorkloadInstanceRecord
	records []ports.WorkloadInstanceRecord
}

func (s *fakeInstanceStore) UpsertStatus(_ context.Context, record ports.WorkloadInstanceRecord) error {
	s.upserts++
	s.last = record
	return nil
}

func (s *fakeInstanceStore) Get(context.Context, string, string) (ports.WorkloadInstanceRecord, error) {
	return s.last, nil
}

func (s *fakeInstanceStore) List(context.Context, string, ports.WorkloadKind) ([]ports.WorkloadInstanceRecord, error) {
	if s.records != nil {
		return append([]ports.WorkloadInstanceRecord(nil), s.records...), nil
	}
	return []ports.WorkloadInstanceRecord{s.last}, nil
}

var _ ports.WorkloadInstanceStore = (*fakeInstanceStore)(nil)
