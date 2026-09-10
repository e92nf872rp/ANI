package runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// fakeReobserveStorageProvider implements the storage provider ports with a
// configurable observation state so tests can simulate WFFC PVCs binding after
// the create-time apply-observe cycle.
type fakeReobserveStorageProvider struct {
	observes     atomic.Int32
	observeState ports.StorageResourceState
	lastObserve  ports.StorageProviderStatusRequest
}

func (p *fakeReobserveStorageProvider) DryRun(_ context.Context, request ports.StorageProviderDryRunRequest) (ports.StorageProviderDryRunResult, error) {
	return ports.StorageProviderDryRunResult{
		Accepted:      true,
		Provider:      "kubernetes",
		ManifestCount: len(request.Manifests),
		ResourceRefs:  storageResourceRefs(request.Manifests),
	}, nil
}

func (p *fakeReobserveStorageProvider) Apply(_ context.Context, request ports.StorageProviderApplyRequest) (ports.StorageProviderApplyResult, error) {
	return ports.StorageProviderApplyResult{
		Applied:       true,
		Provider:      "kubernetes",
		ManifestCount: len(request.Manifests),
		Operation:     request.Operation,
		ResourceRefs:  storageResourceRefs(request.Manifests),
	}, nil
}

func (p *fakeReobserveStorageProvider) Observe(_ context.Context, request ports.StorageProviderStatusRequest) (ports.StorageProviderStatusResult, error) {
	p.observes.Add(1)
	p.lastObserve = request
	return ports.StorageProviderStatusResult{
		TenantID:     request.TenantID,
		ResourceKind: request.ResourceKind,
		ResourceID:   request.ResourceID,
		Provider:     request.ApplyResult.Provider,
		ResourceRefs: request.ApplyResult.ResourceRefs,
		State:        p.observeState,
		Reason:       "observed by fake storage provider",
		ObservedAt:   time.Unix(500, 0).UTC(),
	}, nil
}

// TestLocalStorageServiceGetVolumeReobservesPendingViaStore reproduces the
// live gap: create-time observe always sees a WaitForFirstConsumer PVC as
// Pending, and without re-observation the control-plane state never leaves
// pending even after the PVC binds.
func TestLocalStorageServiceGetVolumeReobservesPendingViaStore(t *testing.T) {
	store := newSharedMemoryStorageStore()
	createProvider := &fakeReobserveStorageProvider{observeState: ports.StorageResourcePending}
	writer := NewLocalStorageService(
		WithStorageResourceStore(store),
		WithStorageProvider(NewKubernetesStorageRenderer(), createProvider, createProvider, createProvider,
			StorageProviderExecutionConfig{UserID: "test", PermissionProof: "test"}),
	)
	volume, err := writer.CreateVolume(context.Background(), ports.StorageVolumeCreateRequest{
		TenantID:       storageStoreTenantID,
		IdempotencyKey: "reobserve-volume",
		Name:           "reobserve-volume",
		SizeGiB:        10,
	})
	if err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if volume.State != ports.StorageResourcePending {
		t.Fatalf("created volume state = %q, want pending", volume.State)
	}

	observeProvider := &fakeReobserveStorageProvider{observeState: ports.StorageResourceAvailable}
	reader := NewLocalStorageService(
		WithStorageResourceStore(store),
		WithStorageProvider(NewKubernetesStorageRenderer(), observeProvider, observeProvider, observeProvider,
			StorageProviderExecutionConfig{UserID: "test", PermissionProof: "test"}),
	)

	got, err := reader.GetVolume(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: volume.VolumeID,
	})
	if err != nil {
		t.Fatalf("reader GetVolume() error = %v", err)
	}
	if got.State != ports.StorageResourceAvailable {
		t.Fatalf("reader GetVolume() state = %q, want available after re-observe", got.State)
	}
	if observeProvider.observes.Load() != 1 {
		t.Fatalf("provider Observe() calls = %d, want 1", observeProvider.observes.Load())
	}
	request := observeProvider.lastObserve
	if !request.ApplyResult.Applied || request.ApplyResult.Provider != "kubernetes" {
		t.Fatalf("re-observe apply result = %#v, want applied kubernetes result", request.ApplyResult)
	}
	wantRef := "kubernetes/PersistentVolumeClaim/" + storageProviderName("vol", volume.VolumeID)
	if len(request.ApplyResult.ResourceRefs) != 1 || request.ApplyResult.ResourceRefs[0] != wantRef {
		t.Fatalf("re-observe resource refs = %#v, want [%s]", request.ApplyResult.ResourceRefs, wantRef)
	}

	// The refreshed state must be persisted to the shared store.
	reread, err := writer.GetVolume(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: volume.VolumeID,
	})
	if err != nil {
		t.Fatalf("writer GetVolume() error = %v", err)
	}
	if reread.State != ports.StorageResourceAvailable {
		t.Fatalf("writer GetVolume() state = %q, want available persisted via store", reread.State)
	}

	// UI polling must be throttled: an immediate second get reuses the
	// previous observation instead of hitting the provider again.
	if _, err := reader.GetVolume(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: volume.VolumeID,
	}); err != nil {
		t.Fatalf("reader second GetVolume() error = %v", err)
	}
	if got := observeProvider.observes.Load(); got != 1 {
		t.Fatalf("provider Observe() calls after throttled get = %d, want 1", got)
	}
}

// TestLocalStorageServiceGetFilesystemReobservesPendingViaStore mirrors the
// volume re-observe test for NFS/CephFS filesystem PVCs.
func TestLocalStorageServiceGetFilesystemReobservesPendingViaStore(t *testing.T) {
	store := newSharedMemoryStorageStore()
	createProvider := &fakeReobserveStorageProvider{observeState: ports.StorageResourcePending}
	writer := NewLocalStorageService(
		WithStorageResourceStore(store),
		WithStorageProvider(NewKubernetesStorageRenderer(), createProvider, createProvider, createProvider,
			StorageProviderExecutionConfig{UserID: "test", PermissionProof: "test"}),
	)
	filesystem, err := writer.CreateFilesystem(context.Background(), ports.StorageFilesystemCreateRequest{
		TenantID:       storageStoreTenantID,
		IdempotencyKey: "reobserve-fs",
		Name:           "reobserve-fs",
		Protocol:       "nfs",
		SizeGiB:        100,
	})
	if err != nil {
		t.Fatalf("CreateFilesystem() error = %v", err)
	}
	if filesystem.State != ports.StorageResourcePending {
		t.Fatalf("created filesystem state = %q, want pending", filesystem.State)
	}

	observeProvider := &fakeReobserveStorageProvider{observeState: ports.StorageResourceAvailable}
	reader := NewLocalStorageService(
		WithStorageResourceStore(store),
		WithStorageProvider(NewKubernetesStorageRenderer(), observeProvider, observeProvider, observeProvider,
			StorageProviderExecutionConfig{UserID: "test", PermissionProof: "test"}),
	)
	got, err := reader.GetFilesystem(context.Background(), ports.StorageResourceGetRequest{
		TenantID:   storageStoreTenantID,
		ResourceID: filesystem.FilesystemID,
	})
	if err != nil {
		t.Fatalf("reader GetFilesystem() error = %v", err)
	}
	if got.State != ports.StorageResourceAvailable {
		t.Fatalf("reader GetFilesystem() state = %q, want available after re-observe", got.State)
	}
	request := observeProvider.lastObserve
	wantRef := "kubernetes/PersistentVolumeClaim/" + storageProviderName("fs", filesystem.FilesystemID)
	if len(request.ApplyResult.ResourceRefs) != 1 || request.ApplyResult.ResourceRefs[0] != wantRef {
		t.Fatalf("re-observe resource refs = %#v, want [%s]", request.ApplyResult.ResourceRefs, wantRef)
	}
}

// TestLocalInstanceResourceResolverAcceptsPendingFilesystemForContainerMount
// locks in the WFFC mount semantics: mounting a filesystem is itself the first
// consumer, so a Pending RWX PVC must be attachable while Failed stays rejected.
func TestLocalInstanceResourceResolverAcceptsPendingFilesystemForContainerMount(t *testing.T) {
	storage := NewLocalStorageService()
	filesystem, err := storage.CreateFilesystem(context.Background(), ports.StorageFilesystemCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "resolver-pending-fs",
		Name:           "pending-fs",
		Protocol:       "nfs",
		SizeGiB:        100,
	})
	if err != nil {
		t.Fatalf("CreateFilesystem error = %v", err)
	}
	storage.mu.Lock()
	pending := storage.filesystems[filesystem.FilesystemID]
	pending.State = ports.StorageResourcePending
	pending.Reason = "observed Kubernetes PVC phase Pending"
	storage.filesystems[filesystem.FilesystemID] = pending
	storage.mu.Unlock()

	resolver := NewLocalInstanceResourceResolver(nil, storage)
	result, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindContainer,
			Container: &ports.ContainerInstanceSpec{
				FilesystemMounts: []ports.InstanceFilesystemMount{{FilesystemID: filesystem.FilesystemID, MountPath: "/data"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("ResolveCreate error = %v", err)
	}
	if len(result.ResourceRefs) != 1 || result.ResourceRefs[0] != "filesystem/"+filesystem.FilesystemID {
		t.Fatalf("resource refs = %#v, want pending filesystem ref", result.ResourceRefs)
	}

	// Failed filesystems remain rejected.
	storage.mu.Lock()
	failed := storage.filesystems[filesystem.FilesystemID]
	failed.State = ports.StorageResourceFailed
	storage.filesystems[filesystem.FilesystemID] = failed
	storage.mu.Unlock()
	if _, err := resolver.ResolveCreate(context.Background(), ports.WorkloadResourceResolveRequest{
		TenantID: "tenant-a",
		Spec: ports.WorkloadSpec{
			TenantID: "tenant-a",
			Kind:     ports.WorkloadKindContainer,
			Container: &ports.ContainerInstanceSpec{
				FilesystemMounts: []ports.InstanceFilesystemMount{{FilesystemID: filesystem.FilesystemID, MountPath: "/data"}},
			},
		},
	}); err == nil {
		t.Fatalf("ResolveCreate with failed filesystem = nil error, want conflict")
	}
}

// TestLocalStorageServiceMountFilesystemAllowsCreatingTarget locks in the WFFC
// mount semantics on the storage service: a provider-mode mount target is
// Creating until the backing PVC binds, and the pod mount consumes the shared
// PVC through CSI, so Creating targets must remain attachable.
func TestLocalStorageServiceMountFilesystemAllowsCreatingTarget(t *testing.T) {
	provider := &fakeReobserveStorageProvider{observeState: ports.StorageResourcePending}
	service := NewLocalStorageService(
		WithStorageProvider(NewKubernetesStorageRenderer(), provider, provider, provider,
			StorageProviderExecutionConfig{UserID: "test", PermissionProof: "test"}),
	)
	filesystem, err := service.CreateFilesystem(context.Background(), ports.StorageFilesystemCreateRequest{
		TenantID:       "tenant-a",
		IdempotencyKey: "creating-target-fs",
		Name:           "creating-target-fs",
		Protocol:       "nfs",
		SizeGiB:        100,
	})
	if err != nil {
		t.Fatalf("CreateFilesystem() error = %v", err)
	}
	target, err := service.CreateFilesystemMountTarget(context.Background(), ports.FilesystemMountTargetCreateRequest{
		TenantID:       "tenant-a",
		FilesystemID:   filesystem.FilesystemID,
		IdempotencyKey: "creating-target-mt",
		SubnetID:       "subnet-1",
	})
	if err != nil {
		t.Fatalf("CreateFilesystemMountTarget() error = %v", err)
	}
	if target.Status != ports.MountTargetCreating {
		t.Fatalf("provider-mode mount target status = %q, want creating", target.Status)
	}

	mounted, err := service.MountFilesystem(context.Background(), ports.StorageFilesystemMountRequest{
		TenantID:       "tenant-a",
		FilesystemID:   filesystem.FilesystemID,
		IdempotencyKey: "creating-target-mount",
		InstanceID:     "inst-1",
		InstanceRoute:  "instances/inst-1",
		MountPath:      "/data",
		AutoMount:      true,
	})
	if err != nil {
		t.Fatalf("MountFilesystem() with creating target error = %v", err)
	}
	if mounted.Mounts != 1 {
		t.Fatalf("MountFilesystem() = %#v, want 1 mount", mounted)
	}
}
