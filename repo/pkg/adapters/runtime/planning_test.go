package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type fakeGPUInventory struct{}

func (fakeGPUInventory) ListNodeClasses(context.Context, ports.GPUDiscoveryFilter) ([]ports.GPUNodeClass, error) {
	return nil, nil
}

func (fakeGPUInventory) GetNodeClass(context.Context, string) (ports.GPUNodeClass, error) {
	return ports.GPUNodeClass{}, nil
}

func (fakeGPUInventory) PlanScheduling(context.Context, ports.GPUSchedulingRequest) (ports.GPUSchedulingDecision, error) {
	return ports.GPUSchedulingDecision{
		NodeSelector:     map[string]string{"ani.kubercloud.io/gpu-node": "true"},
		ResourceName:     "nvidia.com/gpu",
		ResourceQuantity: "1",
		RuntimeClassName: "nvidia",
		SchedulerName:    "volcano",
		QueueName:        "ani-inference",
	}, nil
}

func TestPlanningRuntimeCreatesVMWithDefaultPlanesAndRootDisk(t *testing.T) {
	runtime := NewPlanningRuntime(WithClock(func() time.Time {
		return time.Unix(100, 0)
	}))

	ref, err := runtime.Create(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "vm-01",
		Kind:     ports.WorkloadKindVM,
		VM: &ports.VMInstanceSpec{
			BootImage: "ubuntu.qcow2",
			RootDisk: ports.WorkloadStorageAttachment{
				Name:    "root",
				Kind:    ports.StorageAttachmentRootDisk,
				SizeGiB: 100,
			},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	status, err := runtime.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if status.State != ports.WorkloadStatePending {
		t.Fatalf("state = %s, want %s", status.State, ports.WorkloadStatePending)
	}
	if len(status.Networks) != 3 {
		t.Fatalf("networks = %d, want 3", len(status.Networks))
	}
	if status.Networks[0].Plane != ports.NetworkPlaneTenantVPC || !status.Networks[0].Primary {
		t.Fatalf("first network = %+v, want primary tenant_vpc", status.Networks[0])
	}
	if len(status.Storage) != 1 || status.Storage[0].Kind != ports.StorageAttachmentRootDisk {
		t.Fatalf("storage = %+v, want root disk", status.Storage)
	}
}

func TestPlanningRuntimeRejectsVMWithBootImageAndBootMedia(t *testing.T) {
	runtime := NewPlanningRuntime()

	_, err := runtime.Create(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "vm-both",
		Kind:     ports.WorkloadKindVM,
		VM: &ports.VMInstanceSpec{
			BootImage:        "ubuntu.qcow2",
			BootMedia:        ports.VMBootMediaISO,
			BootMediaImageID: "img-1",
			RootDisk:         ports.WorkloadStorageAttachment{Name: "root", Kind: ports.StorageAttachmentRootDisk, SizeGiB: 40},
		},
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("Create() error = %v, want ErrInvalid", err)
	}
}

func TestPlanningRuntimeRejectsVMISOBootMediaWithoutImageID(t *testing.T) {
	runtime := NewPlanningRuntime()

	_, err := runtime.Create(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "vm-no-image",
		Kind:     ports.WorkloadKindVM,
		VM: &ports.VMInstanceSpec{
			BootMedia: ports.VMBootMediaISO,
			RootDisk:  ports.WorkloadStorageAttachment{Name: "root", Kind: ports.StorageAttachmentRootDisk, SizeGiB: 40},
		},
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("Create() error = %v, want ErrInvalid", err)
	}
}

func TestPlanningRuntimeRejectsVMISOBootMediaWithoutRootDiskSize(t *testing.T) {
	runtime := NewPlanningRuntime()

	_, err := runtime.Create(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "vm-no-root-size",
		Kind:     ports.WorkloadKindVM,
		VM: &ports.VMInstanceSpec{
			BootMedia:        ports.VMBootMediaISO,
			BootMediaImageID: "img-1",
			RootDisk:         ports.WorkloadStorageAttachment{Name: "root", Kind: ports.StorageAttachmentRootDisk},
		},
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("Create() error = %v, want ErrInvalid", err)
	}
}

type fakeImageImportServiceForPlanning struct {
	state ports.ImageState
	err   error
}

func (f fakeImageImportServiceForPlanning) CreateUpload(context.Context, ports.ImageUploadCreateRequest) (ports.ImageUploadSession, error) {
	return ports.ImageUploadSession{}, nil
}

func (f fakeImageImportServiceForPlanning) Get(_ context.Context, req ports.ImageGetRequest) (ports.ImageRecord, error) {
	if f.err != nil {
		return ports.ImageRecord{}, f.err
	}
	return ports.ImageRecord{ID: req.ImageID, TenantID: req.TenantID, State: f.state}, nil
}

func (f fakeImageImportServiceForPlanning) List(context.Context, ports.ImageListRequest) (ports.ImageListResult, error) {
	return ports.ImageListResult{}, nil
}

func (f fakeImageImportServiceForPlanning) Delete(context.Context, ports.ImageDeleteRequest) (ports.ImageRecord, error) {
	return ports.ImageRecord{}, nil
}

func TestPlanningRuntimeCreatesVMWithISOBootMediaWhenImageReady(t *testing.T) {
	runtime := NewPlanningRuntime(WithImageImportService(fakeImageImportServiceForPlanning{state: ports.ImageStateReady}))

	ref, err := runtime.Create(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "vm-iso-ready",
		Kind:     ports.WorkloadKindVM,
		VM: &ports.VMInstanceSpec{
			BootMedia:        ports.VMBootMediaISO,
			BootMediaImageID: "img-ready",
			RootDisk:         ports.WorkloadStorageAttachment{Name: "vm-iso-ready-root", Kind: ports.StorageAttachmentRootDisk, SizeGiB: 40},
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	status, err := runtime.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	foundCDROM := false
	for _, attachment := range status.Storage {
		if attachment.Kind == ports.StorageAttachmentCDROM {
			foundCDROM = true
			if attachment.SourceRef != "img-ready" {
				t.Fatalf("cdrom sourceRef = %q, want img-ready", attachment.SourceRef)
			}
		}
	}
	if !foundCDROM {
		t.Fatalf("storage = %+v, want a cdrom attachment", status.Storage)
	}
}

func TestPlanningRuntimeRejectsVMISOBootMediaWhenImageNotReady(t *testing.T) {
	runtime := NewPlanningRuntime(WithImageImportService(fakeImageImportServiceForPlanning{state: ports.ImageStateUploading}))

	_, err := runtime.Create(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "vm-iso-not-ready",
		Kind:     ports.WorkloadKindVM,
		VM: &ports.VMInstanceSpec{
			BootMedia:        ports.VMBootMediaISO,
			BootMediaImageID: "img-uploading",
			RootDisk:         ports.WorkloadStorageAttachment{Name: "vm-iso-not-ready-root", Kind: ports.StorageAttachmentRootDisk, SizeGiB: 40},
		},
	})
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("Create() error = %v, want ErrConflict", err)
	}
}

func TestPlanningRuntimeRejectsVMISOBootMediaWhenImageNotFound(t *testing.T) {
	runtime := NewPlanningRuntime(WithImageImportService(fakeImageImportServiceForPlanning{err: ports.ErrNotFound}))

	_, err := runtime.Create(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "vm-iso-missing",
		Kind:     ports.WorkloadKindVM,
		VM: &ports.VMInstanceSpec{
			BootMedia:        ports.VMBootMediaISO,
			BootMediaImageID: "img-missing",
			RootDisk:         ports.WorkloadStorageAttachment{Name: "vm-iso-missing-root", Kind: ports.StorageAttachmentRootDisk, SizeGiB: 40},
		},
	})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Create() error = %v, want ErrNotFound", err)
	}
}

func TestPlanningRuntimeRejectsContainerWithoutTenantVPC(t *testing.T) {
	runtime := NewPlanningRuntime()

	_, err := runtime.Create(context.Background(), ports.WorkloadSpec{
		TenantID: "tenant-a",
		Name:     "bad-container",
		Kind:     ports.WorkloadKindContainer,
		Image:    "harbor/app:1",
		Network: ports.WorkloadNetworkPolicy{
			Attachments: []ports.WorkloadNetworkAttachment{
				{
					Plane:     ports.NetworkPlaneFoundationMesh,
					NetworkID: "ani-foundation",
					Primary:   true,
					Required:  true,
				},
			},
		},
	})
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("Create() error = %v, want ErrInvalid", err)
	}
}

func TestPlanningRuntimePlansGPUContainerWithInventory(t *testing.T) {
	runtime := NewPlanningRuntime(WithGPUInventory(fakeGPUInventory{}))

	ref, err := runtime.Create(context.Background(), ports.WorkloadSpec{
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
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	status, err := runtime.ApplyLifecycle(context.Background(), ref, ports.WorkloadLifecycleStart)
	if err != nil {
		t.Fatalf("ApplyLifecycle(start) error = %v", err)
	}
	if status.State != ports.WorkloadStateRunning {
		t.Fatalf("state = %s, want %s", status.State, ports.WorkloadStateRunning)
	}
}

func TestPlanningRuntimeRejectsGPUContainerWithoutInventory(t *testing.T) {
	runtime := NewPlanningRuntime()

	_, err := runtime.Create(context.Background(), ports.WorkloadSpec{
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
	if !errors.Is(err, ports.ErrNotConfigured) {
		t.Fatalf("Create() error = %v, want ErrNotConfigured", err)
	}
}
