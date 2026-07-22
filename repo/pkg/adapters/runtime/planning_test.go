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
		RuntimeClassName: "",
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
