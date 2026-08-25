package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalPlatformWorkloadCPUCreateGetStopStartDelete(t *testing.T) {
	svc := NewLocalPlatformWorkloadService()
	ctx := context.Background()
	tenant := "11111111-1111-1111-1111-111111111111"
	spec := sampleCPUPlatformWorkloadSpec("1df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-cpu-example")

	created, err := svc.Create(ctx, tenant, spec)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.State != ports.PlatformWorkloadRunning || created.InternalEndpoint == "" || created.RuntimeShape != "deployment" {
		t.Fatalf("created = %+v", created)
	}
	replay, err := svc.Create(ctx, tenant, spec)
	if err != nil || replay.ID != created.ID {
		t.Fatalf("idempotent Create() = %+v, %v", replay, err)
	}

	got, err := svc.Get(ctx, tenant, created.ID)
	if err != nil || got.ID != created.ID {
		t.Fatalf("Get() = %+v, %v", got, err)
	}
	if _, err := svc.Get(ctx, "22222222-2222-2222-2222-222222222222", created.ID); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("cross-tenant Get() error = %v", err)
	}

	stopped, err := svc.ApplyLifecycle(ctx, tenant, created.ID, "2df72d71-9d49-46c4-a48a-52bb37b082ab", "stop")
	if err != nil || stopped.State != ports.PlatformWorkloadStopped || stopped.InternalEndpoint != "" {
		t.Fatalf("stop = %+v, %v", stopped, err)
	}
	started, err := svc.ApplyLifecycle(ctx, tenant, created.ID, "3df72d71-9d49-46c4-a48a-52bb37b082ab", "start")
	if err != nil || started.State != ports.PlatformWorkloadRunning || started.InternalEndpoint == "" {
		t.Fatalf("start = %+v, %v", started, err)
	}

	if _, err := svc.Delete(ctx, tenant, created.ID, "4df72d71-9d49-46c4-a48a-52bb37b082ab"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := svc.Get(ctx, tenant, created.ID); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Get after delete error = %v", err)
	}
}

func TestAdmitPlatformWorkloadAcceleratorRequiresAdvertisedSpec(t *testing.T) {
	spec := ports.PlatformWorkloadResources{AcceleratorSpecID: "gpu-a100", AcceleratorCount: 1}
	if err := admitPlatformWorkloadAccelerator(ports.PlatformWorkloadCapabilities{}, spec, "single_node"); !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("empty capabilities error = %v", err)
	}
	caps := ports.PlatformWorkloadCapabilities{AcceleratorSpecs: []ports.PlatformWorkloadAcceleratorCapability{{
		SpecID: "gpu-a100", Available: true, MaxSingleNodeCount: 1, MaxWholeCardCount: 1,
	}}}
	if err := admitPlatformWorkloadAccelerator(caps, spec, "single_node"); err != nil {
		t.Fatalf("advertised accelerator error = %v", err)
	}
	legacy := ports.PlatformWorkloadResources{AcceleratorSpecID: "gpu-a100-8x", AcceleratorCount: 1}
	if err := admitPlatformWorkloadAccelerator(caps, legacy, "single_node"); err != nil {
		t.Fatalf("legacy suffix accelerator error = %v", err)
	}
	if err := admitPlatformWorkloadAccelerator(caps, ports.PlatformWorkloadResources{}, "single_node"); err != nil {
		t.Fatalf("cpu admission error = %v", err)
	}
}

func mixed4090AcceleratorNodes() []ports.GPUNodeClass {
	return []ports.GPUNodeClass{
		{
			NodeName: "gpu-full", Model: "NVIDIA-GeForce-RTX-4090", Ready: true,
			Allocatable: map[string]string{"nvidia.com/gpu": "1"},
			Devices:     []ports.GPUDeviceClass{{Model: "NVIDIA-GeForce-RTX-4090", ResourceName: "nvidia.com/gpu", VirtualizationMode: ports.GPUVirtualizationNone}},
		},
		{
			NodeName: "gpu-vgpu", Model: "NVIDIA-GeForce-RTX-4090", Ready: true,
			Allocatable: map[string]string{"volcano.sh/vgpu-number": "4", "volcano.sh/vgpu-memory": "24576"},
			Devices: []ports.GPUDeviceClass{{
				Model: "NVIDIA-GeForce-RTX-4090", ResourceName: "volcano.sh/vgpu-number",
				VirtualizationMode: ports.GPUVirtualizationVGPU, MemoryMiB: 6144,
			}},
		},
	}
}

func TestAdmitPlatformWorkloadAcceleratorDoesNotMixWholeAndVGPUCapacity(t *testing.T) {
	caps := ports.PlatformWorkloadCapabilities{AcceleratorSpecs: acceleratorSpecsFromGPUNodes(mixed4090AcceleratorNodes(), true)}
	model := "gpu-nvidia-geforce-rtx-4090"
	if err := admitPlatformWorkloadAccelerator(caps, ports.PlatformWorkloadResources{AcceleratorSpecID: model, AcceleratorCount: 4}, "single_node"); !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("whole-card count=4 over 1 physical card error = %v", err)
	}
	if err := admitPlatformWorkloadAccelerator(caps, ports.PlatformWorkloadResources{AcceleratorSpecID: model, AcceleratorCount: 1}, "single_node"); err != nil {
		t.Fatalf("whole-card count=1 error = %v", err)
	}
	if err := admitPlatformWorkloadAccelerator(caps, ports.PlatformWorkloadResources{AcceleratorSpecID: model, AcceleratorCount: 4, AcceleratorMemoryMB: 10240}, "single_node"); err != nil {
		t.Fatalf("vGPU count=4 error = %v", err)
	}
	if err := admitPlatformWorkloadAccelerator(caps, ports.PlatformWorkloadResources{AcceleratorSpecID: model, AcceleratorCount: 8, AcceleratorMemoryMB: 10240}, "single_node"); !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("vGPU count=8 over 4 slots error = %v", err)
	}
}

func TestAdmitPlatformWorkloadAcceleratorRejectsVGPUOnWholeCardOnlyNodes(t *testing.T) {
	nodes := []ports.GPUNodeClass{{
		NodeName: "gpu-a", Model: "A100", Ready: true,
		Allocatable: map[string]string{"nvidia.com/gpu": "2"},
		Devices:     []ports.GPUDeviceClass{{Model: "A100", ResourceName: "nvidia.com/gpu", VirtualizationMode: ports.GPUVirtualizationNone}},
	}}
	caps := ports.PlatformWorkloadCapabilities{AcceleratorSpecs: acceleratorSpecsFromGPUNodes(nodes, true)}
	if err := admitPlatformWorkloadAccelerator(caps, ports.PlatformWorkloadResources{AcceleratorSpecID: "gpu-a100", AcceleratorCount: 1, AcceleratorMemoryMB: 10240}, "single_node"); !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("vGPU on whole-card-only nodes error = %v", err)
	}
}

func TestLocalPlatformWorkloadRejectsNegativeAcceleratorMemory(t *testing.T) {
	svc := NewLocalPlatformWorkloadService()
	spec := sampleCPUPlatformWorkloadSpec("8df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-gpu-neg")
	spec.Resources.AcceleratorSpecID = "gpu-a100"
	spec.Resources.AcceleratorCount = 1
	spec.Resources.AcceleratorMemoryMB = -1
	if _, err := svc.Create(context.Background(), "11111111-1111-1111-1111-111111111111", spec); !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("negative memory Create() error = %v", err)
	}
}

func TestLocalPlatformWorkloadAcceptsAcceleratorAndRejectsLeaderWorker(t *testing.T) {
	svc := NewLocalPlatformWorkloadService()
	ctx := context.Background()
	tenant := "11111111-1111-1111-1111-111111111111"

	gpu := sampleCPUPlatformWorkloadSpec("5df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-gpu")
	gpu.Resources.AcceleratorSpecID = "gpu-a100"
	gpu.Resources.AcceleratorCount = 1
	created, err := svc.Create(ctx, tenant, gpu)
	if err != nil || created.RuntimeShape != "deployment" {
		t.Fatalf("accelerator Create() = %+v, %v", created, err)
	}

	lws := sampleLeaderWorkerPlatformWorkloadSpec("6df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-lws")
	if _, err := svc.Create(ctx, tenant, lws); !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("leader_worker Create() error = %v", err)
	}
}

func TestLocalPlatformWorkloadRejectsTagImage(t *testing.T) {
	svc := NewLocalPlatformWorkloadService()
	spec := sampleCPUPlatformWorkloadSpec("7df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-latest")
	spec.ImageRef = "registry.ani.internal/platform/runtime:latest"
	if _, err := svc.Create(context.Background(), "11111111-1111-1111-1111-111111111111", spec); !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("tag image Create() error = %v", err)
	}
}

func sampleCPUPlatformWorkloadSpec(key, name string) ports.PlatformWorkloadCreateSpec {
	return ports.PlatformWorkloadCreateSpec{
		IdempotencyKey: key,
		Name:           name,
		WorkloadClass:  "inference",
		RuntimeKind:    "container",
		ImageRef:       "registry.ani.internal/platform/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Command:        []string{"/opt/platform-runtime/serve"},
		Replicas:       1,
		Resources:      ports.PlatformWorkloadResources{CPU: "4", Memory: "16Gi"},
		Topology:       ports.PlatformWorkloadTopology{Mode: "single_node", ProfileID: "container-single-node", ProfileVersion: "v1"},
		Scheduling:     ports.PlatformWorkloadScheduling{QueueClass: "inference"},
		Network: ports.PlatformWorkloadNetwork{
			Exposure: "cluster_internal",
			Ports:    []ports.PlatformWorkloadPort{{Name: "http", Port: 8000}},
		},
		HealthCheck: ports.PlatformWorkloadHealthCheck{Protocol: "http", Path: "/health", PortName: "http"},
		Metadata:    ports.PlatformWorkloadMetadata{OwnerRef: "05f6f46f-3db8-4551-8497-c46debb4be22"},
	}
}

func sampleLeaderWorkerPlatformWorkloadSpec(key, name string) ports.PlatformWorkloadCreateSpec {
	spec := sampleCPUPlatformWorkloadSpec(key, name)
	spec.Resources.AcceleratorSpecID = "gpu-a100-full"
	spec.Resources.AcceleratorCount = 2
	spec.Topology = ports.PlatformWorkloadTopology{
		Mode: "leader_worker", ProfileID: "container-leader-worker", ProfileVersion: "v1",
		HasLeader: true, HasWorkers: true,
		Leader:  ports.PlatformWorkloadRole{Count: 1, Resources: ports.PlatformWorkloadResources{CPU: "8", Memory: "32Gi", AcceleratorSpecID: "gpu-a100-full", AcceleratorCount: 1}},
		Workers: ports.PlatformWorkloadRole{Count: 1, Resources: ports.PlatformWorkloadResources{CPU: "8", Memory: "32Gi", AcceleratorSpecID: "gpu-a100-full", AcceleratorCount: 1}},
	}
	spec.Scheduling.Gang = true
	return spec
}
