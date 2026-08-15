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
	if err := admitPlatformWorkloadAccelerator(ports.PlatformWorkloadCapabilities{}, spec); !errors.Is(err, ports.ErrFailedPrecondition) {
		t.Fatalf("empty capabilities error = %v", err)
	}
	caps := ports.PlatformWorkloadCapabilities{AcceleratorSpecs: []ports.PlatformWorkloadAcceleratorCapability{{
		SpecID: "gpu-a100", Available: true, MaxSingleNodeCount: 1,
	}}}
	if err := admitPlatformWorkloadAccelerator(caps, spec); err != nil {
		t.Fatalf("advertised accelerator error = %v", err)
	}
	if err := admitPlatformWorkloadAccelerator(caps, ports.PlatformWorkloadResources{}); err != nil {
		t.Fatalf("cpu admission error = %v", err)
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

	lws := sampleCPUPlatformWorkloadSpec("6df72d71-9d49-46c4-a48a-52bb37b082ab", "inference-lws")
	lws.Topology.Mode = "leader_worker"
	lws.Topology.HasLeader = true
	lws.Scheduling.Gang = true
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
