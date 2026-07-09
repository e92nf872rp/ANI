package runtime

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestKubernetesGPUInventoryListsNVIDIADevicePluginNodes(t *testing.T) {
	var gotPath string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.String()
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		return jsonResponse(http.StatusOK, `{
  "kind": "NodeList",
  "items": [{
    "metadata": {
      "name": "ani-gpu-1",
      "labels": {
        "kubernetes.io/hostname": "ani-gpu-1",
        "nvidia.com/gpu.product": "NVIDIA-A100-SXM4-40GB",
        "ani.kubercloud.io/gpu-pool": "training"
      }
    },
    "status": {
      "capacity": {"nvidia.com/gpu": "2"},
      "allocatable": {"nvidia.com/gpu": "1"},
      "nodeInfo": {"kernelVersion": "6.8.0", "osImage": "Ubuntu 24.04", "kubeletVersion": "v1.36.1"},
      "conditions": [{"type": "Ready", "status": "True", "reason": "KubeletReady"}]
    }
  }, {
    "metadata": {
      "name": "cpu-only",
      "labels": {"kubernetes.io/hostname": "cpu-only"}
    },
    "status": {
      "capacity": {"cpu": "32"},
      "allocatable": {"cpu": "32"},
      "conditions": [{"type": "Ready", "status": "True"}]
    }
  }]
}`), nil
	})
	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host:        "https://kubernetes.example",
		BearerToken: "token-a",
		HTTPClient:  &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatal(err)
	}

	inventory := NewKubernetesGPUInventory(client)
	nodes, err := inventory.ListNodeClasses(context.Background(), ports.GPUDiscoveryFilter{})
	if err != nil {
		t.Fatalf("ListNodeClasses() error = %v", err)
	}
	if gotPath != "https://kubernetes.example/api/v1/nodes" {
		t.Fatalf("path = %s, want /api/v1/nodes", gotPath)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %+v, want one GPU node", nodes)
	}
	node := nodes[0]
	if node.NodeName != "ani-gpu-1" || node.Vendor != ports.GPUVendorNVIDIA || node.Model != "NVIDIA-A100-SXM4-40GB" || node.Pool != "training" || !node.Ready {
		t.Fatalf("node = %+v, want ready NVIDIA A100 training node", node)
	}
	if node.KernelVersion != "6.8.0" || node.OSImage != "Ubuntu 24.04" {
		t.Fatalf("node info = %+v, want Kubernetes nodeInfo", node)
	}
	if len(node.Devices) != 2 {
		t.Fatalf("devices = %+v, want capacity-sized device list", node.Devices)
	}
	if node.Allocatable["nvidia.com/gpu"] != "1" {
		t.Fatalf("allocatable = %+v, want nvidia.com/gpu=1 preserved", node.Allocatable)
	}
	device := nodes[0].Devices[0]
	if device.Vendor != ports.GPUVendorNVIDIA || device.Model != "NVIDIA-A100-SXM4-40GB" || device.ResourceName != "nvidia.com/gpu" || device.RuntimeVersion != "v1.36.1" {
		t.Fatalf("device = %+v, want NVIDIA device-plugin GPU", device)
	}
	if device.DriverVersion != "device-plugin" || device.VirtualizationMode != ports.GPUVirtualizationNone {
		t.Fatalf("device metadata = %+v, want contract defaults", device)
	}
}

func TestKubernetesGPUInventoryFiltersByProductLabelAndNodeName(t *testing.T) {
	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host: "https://kubernetes.example",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{
  "items": [{
    "metadata": {
      "name": "ani-gpu-a",
      "labels": {"kubernetes.io/hostname": "ani-gpu-a", "nvidia.com/gpu.product": "A100"}
    },
    "status": {"capacity": {"nvidia.com/gpu": "1"}, "allocatable": {"nvidia.com/gpu": "1"}}
  }, {
    "metadata": {
      "name": "ani-gpu-b",
      "labels": {"kubernetes.io/hostname": "ani-gpu-b", "nvidia.com/gpu.product": "L40S"}
    },
    "status": {"capacity": {"nvidia.com/gpu": "1"}, "allocatable": {"nvidia.com/gpu": "1"}}
  }]
}`), nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}

	nodes, err := NewKubernetesGPUInventory(client).ListNodeClasses(context.Background(), ports.GPUDiscoveryFilter{
		Labels: map[string]string{
			"nvidia.com/gpu.product": "L40S",
			"kubernetes.io/hostname": "ani-gpu-b",
		},
	})
	if err != nil {
		t.Fatalf("ListNodeClasses() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeName != "ani-gpu-b" || nodes[0].Model != "L40S" {
		t.Fatalf("nodes = %+v, want filtered L40S node", nodes)
	}
}

// gpuNodeListJSON returns a NodeList body with one ready GPU node advertising
// the given allocatable resources. Used by PlanScheduling tests.
func gpuNodeListJSON(t *testing.T, allocatableGPU, allocatableVGPU string) string {
	t.Helper()
	return `{
  "items": [{
    "metadata": {
      "name": "ani-gpu-1",
      "labels": {"kubernetes.io/hostname": "ani-gpu-1", "nvidia.com/gpu.product": "A100"}
    },
    "status": {
      "capacity": {"nvidia.com/gpu": "` + allocatableGPU + `", "nvidia.com/vgpu": "` + allocatableVGPU + `"},
      "allocatable": {"nvidia.com/gpu": "` + allocatableGPU + `", "nvidia.com/vgpu": "` + allocatableVGPU + `"},
      "nodeInfo": {"kubeletVersion": "v1.36.1"},
      "conditions": [{"type": "Ready", "status": "True", "reason": "KubeletReady"}]
    }
  }]
}`
}

func newTestGPUInventory(t *testing.T, body string) *KubernetesGPUInventory {
	t.Helper()
	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host: "https://kubernetes.example",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, body), nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewKubernetesGPUInventory(client)
}

func newTestGPUInventoryWithStore(t *testing.T, body string, store ports.GPUSchedulingQueueStore) *KubernetesGPUInventory {
	t.Helper()
	client, err := NewKubernetesRESTClient(KubernetesRESTClientConfig{
		Host: "https://kubernetes.example",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, body), nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewKubernetesGPUInventoryWithQueueStore(client, store)
}

// stubQueueStore is a minimal in-memory GPUSchedulingQueueStore for tests.
type stubQueueStore struct {
	queues []ports.GPUSchedulingQueue
	err    error
}

func (s stubQueueStore) List(context.Context, string) ([]ports.GPUSchedulingQueue, error) {
	return s.queues, s.err
}
func (s stubQueueStore) Get(context.Context, string, string) (ports.GPUSchedulingQueue, error) {
	return ports.GPUSchedulingQueue{}, ports.ErrQueueNotFound
}
func (s stubQueueStore) Create(context.Context, string, ports.GPUSchedulingQueueCreateRequest) (ports.GPUSchedulingQueue, error) {
	return ports.GPUSchedulingQueue{}, ports.ErrQueueStoreUnavailable
}
func (s stubQueueStore) Update(context.Context, string, string, ports.GPUSchedulingQueueUpdateRequest) (ports.GPUSchedulingQueue, error) {
	return ports.GPUSchedulingQueue{}, ports.ErrQueueStoreUnavailable
}
func (s stubQueueStore) Delete(context.Context, string, string) error {
	return ports.ErrQueueStoreUnavailable
}

func TestPlanSchedulingWholeCardSelectsNVIDIAGPUResource(t *testing.T) {
	inventory := newTestGPUInventory(t, gpuNodeListJSON(t, "2", "0"))
	decision, err := inventory.PlanScheduling(context.Background(), ports.GPUSchedulingRequest{
		TenantID:      "tenant-a",
		WorkloadID:    "workload-a",
		RequiredCount: 2,
	})
	if err != nil {
		t.Fatalf("PlanScheduling error = %v", err)
	}
	if decision.ResourceName != "nvidia.com/gpu" {
		t.Fatalf("resourceName = %q, want nvidia.com/gpu", decision.ResourceName)
	}
	if decision.ResourceQuantity != "2" {
		t.Fatalf("quantity = %q, want 2", decision.ResourceQuantity)
	}
	if decision.RuntimeClassName != "nvidia" {
		t.Fatalf("runtimeClassName = %q, want nvidia", decision.RuntimeClassName)
	}
	if decision.SchedulerName != "volcano" {
		t.Fatalf("schedulerName = %q, want volcano", decision.SchedulerName)
	}
	if decision.QueueName == "" {
		t.Fatalf("queueName is empty, want default queue")
	}
	if len(decision.Reasons) == 0 {
		t.Fatalf("reasons empty, want match explanation")
	}
}

func TestPlanSchedulingVGPUSelectsNVIDIAGVGPUResource(t *testing.T) {
	inventory := newTestGPUInventory(t, gpuNodeListJSON(t, "0", "4"))
	decision, err := inventory.PlanScheduling(context.Background(), ports.GPUSchedulingRequest{
		TenantID:            "tenant-a",
		WorkloadID:          "workload-a",
		RequiredCount:       2,
		VirtualizationModes: []ports.GPUVirtualizationMode{ports.GPUVirtualizationVGPU},
	})
	if err != nil {
		t.Fatalf("PlanScheduling error = %v", err)
	}
	// Non-HAMi node: vGPU scheduling uses nvidia.com/vgpu resource.
	if decision.ResourceName != "nvidia.com/vgpu" {
		t.Fatalf("resourceName = %q, want nvidia.com/vgpu", decision.ResourceName)
	}
	if decision.ResourceQuantity != "2" {
		t.Fatalf("quantity = %q, want 2", decision.ResourceQuantity)
	}
	if decision.RuntimeClassName != "hami-vgpu" {
		t.Fatalf("runtimeClassName = %q, want hami-vgpu", decision.RuntimeClassName)
	}
}

// TestPlanSchedulingVGPUOnHAMiNodeUsesNVIDIAGPUResource verifies that when a
// node has the HAMi annotation, vGPU scheduling requests use
// nvidia.com/gpu (HAMi's actual resource name) instead of nvidia.com/vgpu.
func TestPlanSchedulingVGPUOnHAMiNodeUsesNVIDIAGPUResource(t *testing.T) {
	body := `{
  "items": [{
    "metadata": {
      "name": "hami-node-1",
      "labels": {"kubernetes.io/hostname": "hami-node-1", "nvidia.com/gpu.product": "RTX4090"},
      "annotations": {
        "hami.io/node-nvidia-register": "[{\"id\":\"GPU-aaa\",\"count\":10,\"devmem\":49140,\"devcore\":100,\"type\":\"NVIDIA GeForce RTX 4090\",\"mode\":\"hami-core\",\"health\":true},{\"id\":\"GPU-bbb\",\"index\":1,\"count\":10,\"devmem\":49140,\"devcore\":100,\"type\":\"NVIDIA GeForce RTX 4090\",\"mode\":\"hami-core\",\"health\":true}]"
      }
    },
    "status": {
      "capacity": {"nvidia.com/gpu": "20"},
      "allocatable": {"nvidia.com/gpu": "20"},
      "nodeInfo": {"kubeletVersion": "v1.36.1"},
      "conditions": [{"type": "Ready", "status": "True", "reason": "KubeletReady"}]
    }
  }]
}`
	inventory := newTestGPUInventory(t, body)
	decision, err := inventory.PlanScheduling(context.Background(), ports.GPUSchedulingRequest{
		TenantID:            "tenant-a",
		WorkloadID:          "workload-a",
		RequiredCount:       2,
		VirtualizationModes: []ports.GPUVirtualizationMode{ports.GPUVirtualizationVGPU},
	})
	if err != nil {
		t.Fatalf("PlanScheduling error = %v", err)
	}
	if decision.ResourceName != "nvidia.com/gpu" {
		t.Fatalf("resourceName = %q, want nvidia.com/gpu (HAMi vGPU)", decision.ResourceName)
	}
	if decision.ResourceQuantity != "2" {
		t.Fatalf("quantity = %q, want 2", decision.ResourceQuantity)
	}
	if decision.RuntimeClassName != "hami-vgpu" {
		t.Fatalf("runtimeClassName = %q, want hami-vgpu", decision.RuntimeClassName)
	}
}

// TestListNodeClassesWithHAMiAnnotationCountsPhysicalCards verifies that
// HAMi-managed nodes report physical card count (from annotation) rather
// than the inflated vGPU split count in nvidia.com/gpu allocatable.
func TestListNodeClassesWithHAMiAnnotationCountsPhysicalCards(t *testing.T) {
	body := `{
  "items": [{
    "metadata": {
      "name": "hami-node-1",
      "labels": {"kubernetes.io/hostname": "hami-node-1"},
      "annotations": {
        "hami.io/node-nvidia-register": "[{\"id\":\"GPU-aaa\",\"count\":10,\"devmem\":49140,\"devcore\":100,\"type\":\"NVIDIA GeForce RTX 4090\",\"mode\":\"hami-core\",\"health\":true},{\"id\":\"GPU-bbb\",\"index\":1,\"count\":10,\"devmem\":49140,\"devcore\":100,\"type\":\"NVIDIA GeForce RTX 4090\",\"mode\":\"hami-core\",\"health\":true}]"
      }
    },
    "status": {
      "capacity": {"nvidia.com/gpu": "20"},
      "allocatable": {"nvidia.com/gpu": "20"},
      "nodeInfo": {"kubeletVersion": "v1.36.1"},
      "conditions": [{"type": "Ready", "status": "True", "reason": "KubeletReady"}]
    }
  }]
}`
	inventory := newTestGPUInventory(t, body)
	nodes, err := inventory.ListNodeClasses(context.Background(), ports.GPUDiscoveryFilter{})
	if err != nil {
		t.Fatalf("ListNodeClasses error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	node := nodes[0]
	if len(node.Devices) != 2 {
		t.Fatalf("len(devices) = %d, want 2 physical cards", len(node.Devices))
	}
	for i, device := range node.Devices {
		if device.VirtualizationMode != ports.GPUVirtualizationVGPU {
			t.Fatalf("device[%d].VirtualizationMode = %q, want vgpu", i, device.VirtualizationMode)
		}
		if device.ResourceName != "nvidia.com/gpu" {
			t.Fatalf("device[%d].ResourceName = %q, want nvidia.com/gpu", i, device.ResourceName)
		}
		if device.Model != "NVIDIA GeForce RTX 4090" {
			t.Fatalf("device[%d].Model = %q, want NVIDIA GeForce RTX 4090", i, device.Model)
		}
		if device.MemoryMiB != 49140 {
			t.Fatalf("device[%d].MemoryMiB = %d, want 49140", i, device.MemoryMiB)
		}
	}
}

func TestPlanSchedulingNoAvailableGPUReturnsReasons(t *testing.T) {
	inventory := newTestGPUInventory(t, gpuNodeListJSON(t, "0", "0"))
	decision, err := inventory.PlanScheduling(context.Background(), ports.GPUSchedulingRequest{
		TenantID:      "tenant-a",
		WorkloadID:    "workload-a",
		RequiredCount: 1,
	})
	if err != nil {
		t.Fatalf("PlanScheduling error = %v", err)
	}
	if len(decision.Reasons) == 0 {
		t.Fatalf("reasons empty, want no-available-GPU explanation")
	}
	if decision.ResourceName == "" {
		t.Fatalf("resourceName empty, want nvidia.com/gpu for diagnostics")
	}
}

func TestPlanSchedulingAscendVendorRejected(t *testing.T) {
	inventory := newTestGPUInventory(t, gpuNodeListJSON(t, "2", "0"))
	decision, err := inventory.PlanScheduling(context.Background(), ports.GPUSchedulingRequest{
		TenantID:         "tenant-a",
		WorkloadID:       "workload-a",
		RequiredCount:    1,
		PreferredVendors: []ports.GPUVendor{ports.GPUVendorHuawei},
	})
	if err != nil {
		t.Fatalf("PlanScheduling error = %v", err)
	}
	if len(decision.Reasons) == 0 {
		t.Fatalf("reasons empty, want Ascend P1 rejection")
	}
	if decision.ResourceName != "" {
		t.Fatalf("resourceName = %q, want empty for rejected vendor", decision.ResourceName)
	}
}

func TestPlanSchedulingMIGModeRejected(t *testing.T) {
	inventory := newTestGPUInventory(t, gpuNodeListJSON(t, "2", "0"))
	decision, err := inventory.PlanScheduling(context.Background(), ports.GPUSchedulingRequest{
		TenantID:            "tenant-a",
		WorkloadID:          "workload-a",
		RequiredCount:       1,
		VirtualizationModes: []ports.GPUVirtualizationMode{ports.GPUVirtualizationMIG},
	})
	if err != nil {
		t.Fatalf("PlanScheduling error = %v", err)
	}
	if len(decision.Reasons) == 0 {
		t.Fatalf("reasons empty, want MIG P1 rejection")
	}
	if decision.ResourceName != "" {
		t.Fatalf("resourceName = %q, want empty for rejected MIG", decision.ResourceName)
	}
}

func TestPlanSchedulingExplicitQueueValidatedByStore(t *testing.T) {
	store := stubQueueStore{queues: []ports.GPUSchedulingQueue{
		{Name: "proj-a-infer", WorkloadClass: ports.WorkloadClassInference},
	}}
	inventory := newTestGPUInventoryWithStore(t, gpuNodeListJSON(t, "2", "0"), store)
	decision, err := inventory.PlanScheduling(context.Background(), ports.GPUSchedulingRequest{
		TenantID:      "tenant-a",
		WorkloadID:    "workload-a",
		RequiredCount: 1,
		QueueName:     "proj-a-infer",
	})
	if err != nil {
		t.Fatalf("PlanScheduling error = %v", err)
	}
	if decision.QueueName != "proj-a-infer" {
		t.Fatalf("queueName = %q, want proj-a-infer", decision.QueueName)
	}
}

func TestPlanSchedulingExplicitQueueNotFoundReturnsReasons(t *testing.T) {
	store := stubQueueStore{queues: []ports.GPUSchedulingQueue{
		{Name: "proj-a-infer"},
	}}
	inventory := newTestGPUInventoryWithStore(t, gpuNodeListJSON(t, "2", "0"), store)
	decision, err := inventory.PlanScheduling(context.Background(), ports.GPUSchedulingRequest{
		TenantID:      "tenant-a",
		WorkloadID:    "workload-a",
		RequiredCount: 1,
		QueueName:     "missing-queue",
	})
	if err != nil {
		t.Fatalf("PlanScheduling error = %v", err)
	}
	if len(decision.Reasons) == 0 {
		t.Fatalf("reasons empty, want queue-not-found explanation")
	}
	if decision.QueueName != "" {
		t.Fatalf("queueName = %q, want empty for unresolved queue", decision.QueueName)
	}
}

func TestPlanSchedulingDefaultQueueInference(t *testing.T) {
	inventory := newTestGPUInventory(t, gpuNodeListJSON(t, "1", "0"))
	decision, err := inventory.PlanScheduling(context.Background(), ports.GPUSchedulingRequest{
		TenantID:      "tenant-a",
		WorkloadID:    "workload-a",
		RequiredCount: 1,
		WorkloadClass: ports.WorkloadClassInference,
	})
	if err != nil {
		t.Fatalf("PlanScheduling error = %v", err)
	}
	if decision.QueueName != "ani-inference" {
		t.Fatalf("queueName = %q, want ani-inference", decision.QueueName)
	}
}

func TestPlanSchedulingDefaultQueueTraining(t *testing.T) {
	inventory := newTestGPUInventory(t, gpuNodeListJSON(t, "1", "0"))
	decision, err := inventory.PlanScheduling(context.Background(), ports.GPUSchedulingRequest{
		TenantID:      "tenant-a",
		WorkloadID:    "workload-a",
		RequiredCount: 1,
		WorkloadClass: ports.WorkloadClassTraining,
	})
	if err != nil {
		t.Fatalf("PlanScheduling error = %v", err)
	}
	if decision.QueueName != "ani-training" {
		t.Fatalf("queueName = %q, want ani-training", decision.QueueName)
	}
}

func TestPlanSchedulingDefaultQueueBatchFallsBackToTraining(t *testing.T) {
	inventory := newTestGPUInventory(t, gpuNodeListJSON(t, "1", "0"))
	decision, err := inventory.PlanScheduling(context.Background(), ports.GPUSchedulingRequest{
		TenantID:      "tenant-a",
		WorkloadID:    "workload-a",
		RequiredCount: 1,
		WorkloadClass: ports.WorkloadClassBatch,
	})
	if err != nil {
		t.Fatalf("PlanScheduling error = %v", err)
	}
	if decision.QueueName != "ani-training" {
		t.Fatalf("queueName = %q, want ani-training (batch fallback)", decision.QueueName)
	}
}

func TestPlanSchedulingQueueStoreUnavailableReturnsReasons(t *testing.T) {
	store := stubQueueStore{err: ports.ErrQueueStoreUnavailable}
	inventory := newTestGPUInventoryWithStore(t, gpuNodeListJSON(t, "1", "0"), store)
	decision, err := inventory.PlanScheduling(context.Background(), ports.GPUSchedulingRequest{
		TenantID:      "tenant-a",
		WorkloadID:    "workload-a",
		RequiredCount: 1,
		QueueName:     "proj-a-infer",
	})
	if err != nil {
		t.Fatalf("PlanScheduling error = %v", err)
	}
	if len(decision.Reasons) == 0 {
		t.Fatalf("reasons empty, want queue-store-unavailable explanation")
	}
}

func TestPlanSchedulingInsufficientAllocatableReturnsReasons(t *testing.T) {
	inventory := newTestGPUInventory(t, gpuNodeListJSON(t, "1", "0"))
	decision, err := inventory.PlanScheduling(context.Background(), ports.GPUSchedulingRequest{
		TenantID:      "tenant-a",
		WorkloadID:    "workload-a",
		RequiredCount: 4,
	})
	if err != nil {
		t.Fatalf("PlanScheduling error = %v", err)
	}
	if len(decision.Reasons) == 0 {
		t.Fatalf("reasons empty, want insufficient allocatable explanation")
	}
}

func TestPlanSchedulingInvalidRequestReturnsError(t *testing.T) {
	inventory := newTestGPUInventory(t, gpuNodeListJSON(t, "1", "0"))
	_, err := inventory.PlanScheduling(context.Background(), ports.GPUSchedulingRequest{
		TenantID:   "",
		WorkloadID: "workload-a",
	})
	if err == nil {
		t.Fatalf("error nil, want invalid for missing tenant_id")
	}
	if !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}
