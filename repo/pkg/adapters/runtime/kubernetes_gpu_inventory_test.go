package runtime

import (
	"context"
	"encoding/json"
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
func (s stubQueueStore) Create(context.Context, string, string, ports.GPUSchedulingQueueCreateRequest) (ports.GPUSchedulingQueueCreateResult, error) {
	return ports.GPUSchedulingQueueCreateResult{}, ports.ErrQueueStoreUnavailable
}
func (s stubQueueStore) Update(context.Context, string, string, string, ports.GPUSchedulingQueueUpdateRequest) (ports.GPUSchedulingQueueUpdateResult, error) {
	return ports.GPUSchedulingQueueUpdateResult{}, ports.ErrQueueStoreUnavailable
}
func (s stubQueueStore) Delete(context.Context, string, string) error {
	return ports.ErrQueueStoreUnavailable
}

func TestKubernetesGPUInventoryListsVolcanoVGPUNodes(t *testing.T) {
	body := `{
  "kind": "NodeList",
  "items": [{
    "metadata": {
      "name": "ani-vgpu-1",
      "labels": {
        "kubernetes.io/hostname": "ani-vgpu-1",
        "nvidia.com/gpu.product": "NVIDIA-GeForce-RTX-4090"
      }
    },
    "status": {
      "capacity": {"volcano.sh/vgpu-number": "4", "volcano.sh/vgpu-memory": "24576"},
      "allocatable": {"volcano.sh/vgpu-number": "4", "volcano.sh/vgpu-memory": "24576"},
      "nodeInfo": {"kubeletVersion": "v1.36.1"},
      "conditions": [{"type": "Ready", "status": "True", "reason": "KubeletReady"}]
    }
  }, {
    "metadata": {"name": "cpu-only", "labels": {"kubernetes.io/hostname": "cpu-only"}},
    "status": {
      "capacity": {"cpu": "32"},
      "allocatable": {"cpu": "32"},
      "conditions": [{"type": "Ready", "status": "True"}]
    }
  }]
}`
	inventory := newTestGPUInventory(t, body)
	nodes, err := inventory.ListNodeClasses(context.Background(), ports.GPUDiscoveryFilter{})
	if err != nil {
		t.Fatalf("ListNodeClasses() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeName != "ani-vgpu-1" {
		t.Fatalf("nodes = %+v, want volcano vGPU node", nodes)
	}
	if nodes[0].Allocatable["volcano.sh/vgpu-number"] != "4" {
		t.Fatalf("allocatable = %+v", nodes[0].Allocatable)
	}
	if len(nodes[0].Devices) != 4 {
		t.Fatalf("devices = %+v, want one device per vGPU slice", nodes[0].Devices)
	}
	device := nodes[0].Devices[0]
	if device.ResourceName != "volcano.sh/vgpu-number" || device.VirtualizationMode != ports.GPUVirtualizationVGPU || device.MemoryMiB != 6144 {
		t.Fatalf("device = %+v", device)
	}
}

// stubSpecStore is a minimal in-memory GPUSpecStore for ListSpecAvailability tests.
type stubSpecStore struct {
	specs []ports.GPUSpecCRD
	err   error
}

func (s stubSpecStore) List(context.Context) ([]ports.GPUSpecCRD, error) {
	return s.specs, s.err
}
func (s stubSpecStore) Get(context.Context, string) (ports.GPUSpecCRD, error) {
	return ports.GPUSpecCRD{}, ports.ErrGPUSpecNotFound
}
func (s stubSpecStore) Create(context.Context, string, ports.GPUSpecCRD) (ports.GPUSpecCRD, error) {
	return ports.GPUSpecCRD{}, ports.ErrUnsupported
}
func (s stubSpecStore) Delete(context.Context, string, string) error {
	return ports.ErrUnsupported
}

// stubQuotaStore is a minimal in-memory QuotaStoreService for ListSpecAvailability tests.
type stubQuotaStore struct {
	view ports.QuotaView
	err  error
}

func (s stubQuotaStore) Put(context.Context, string, ports.QuotaPutRequest) (ports.QuotaView, error) {
	return ports.QuotaView{}, ports.ErrUnsupported
}
func (s stubQuotaStore) List(context.Context, ports.QuotaListRequest) (ports.QuotaListResult, error) {
	return ports.QuotaListResult{}, ports.ErrUnsupported
}
func (s stubQuotaStore) GetMy(context.Context, string) (ports.QuotaView, error) {
	return s.view, s.err
}

// stubQuotaAdmin is a minimal in-memory QuotaAdminService for
// ListSpecAvailability tests that need allocated_gpu_count.
type stubQuotaAdmin struct {
	reservation ports.ReservationView
	err         error
}

func (s stubQuotaAdmin) CreateTenantQuota(context.Context, string, []ports.QuotaItemInput) ([]ports.QuotaInfo, error) {
	return nil, ports.ErrUnsupported
}
func (s stubQuotaAdmin) UpdateTenantQuota(context.Context, string, []ports.QuotaItemUpdate) ([]ports.QuotaInfo, error) {
	return nil, ports.ErrUnsupported
}
func (s stubQuotaAdmin) GetTenantQuota(context.Context, string) ([]ports.QuotaInfo, error) {
	return nil, ports.ErrUnsupported
}
func (s stubQuotaAdmin) DeleteTenantQuota(context.Context, string) error {
	return ports.ErrUnsupported
}
func (s stubQuotaAdmin) ListQuotaMeta(context.Context) ([]ports.QuotaMeta, error) {
	return nil, ports.ErrUnsupported
}
func (s stubQuotaAdmin) UpsertTenantQuota(context.Context, string, []ports.QuotaItemInput) ([]ports.QuotaInfo, error) {
	return nil, ports.ErrUnsupported
}
func (s stubQuotaAdmin) PutReservation(context.Context, string, ports.ReservationPutRequest) (ports.ReservationView, error) {
	return ports.ReservationView{}, ports.ErrUnsupported
}
func (s stubQuotaAdmin) GetReservation(context.Context, string) (ports.ReservationView, error) {
	return s.reservation, s.err
}
func (s stubQuotaAdmin) GetReservationTx(context.Context, ports.MetadataTx, string) (ports.ReservationView, error) {
	return s.reservation, s.err
}
func (s stubQuotaStore) GetTotalForUpdateTx(context.Context, ports.MetadataTx, string, ports.ResourceType) (int64, error) {
	return 0, ports.ErrUnsupported
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
	if decision.RuntimeClassName != "" {
		t.Fatalf("runtimeClassName = %q, want empty for whole-card", decision.RuntimeClassName)
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
	// vGPU scheduling uses nvidia.com/vgpu resource.
	if decision.ResourceName != "nvidia.com/vgpu" {
		t.Fatalf("resourceName = %q, want nvidia.com/vgpu", decision.ResourceName)
	}
	if decision.ResourceQuantity != "2" {
		t.Fatalf("quantity = %q, want 2", decision.ResourceQuantity)
	}
	// Runtime class is always empty with HAMi removed.
	if decision.RuntimeClassName != "" {
		t.Fatalf("runtimeClassName = %q, want empty for vGPU", decision.RuntimeClassName)
	}
}

// TestHAMiRemoved verifies that HAMi-related code has been removed:
// the scheduler name is always "volcano" (never "hami-scheduler") and
// the runtime class is always empty regardless of virtualization mode.
func TestHAMiRemoved(t *testing.T) {
	// vGPU scheduling on a node with volcano vGPU annotation should use
	// volcano scheduler and empty runtime class (no hami-scheduler).
	body := `{
  "items": [{
    "metadata": {
      "name": "vgpu-node-1",
      "labels": {"kubernetes.io/hostname": "vgpu-node-1", "nvidia.com/gpu.product": "RTX4090"},
      "annotations": {
        "volcano.sh/node-vgpu-register": "[{\"id\":\"GPU-aaa\",\"count\":10,\"devmem\":49140,\"devcore\":100,\"type\":\"NVIDIA GeForce RTX 4090\",\"health\":true},{\"id\":\"GPU-bbb\",\"index\":1,\"count\":10,\"devmem\":49140,\"devcore\":100,\"type\":\"NVIDIA GeForce RTX 4090\",\"health\":true}]"
      }
    },
    "status": {
      "capacity": {"nvidia.com/vgpu": "20"},
      "allocatable": {"nvidia.com/vgpu": "20"},
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
	if decision.SchedulerName != "volcano" {
		t.Fatalf("schedulerName = %q, want volcano (hami-scheduler removed)", decision.SchedulerName)
	}
	if decision.RuntimeClassName != "" {
		t.Fatalf("runtimeClassName = %q, want empty (hami-vgpu removed)", decision.RuntimeClassName)
	}
	if decision.ResourceName != "nvidia.com/vgpu" {
		t.Fatalf("resourceName = %q, want nvidia.com/vgpu", decision.ResourceName)
	}
}

// TestParseVolcanoVGPUAnnotation verifies parsing of the
// volcano.sh/node-vgpu-register annotation for vGPU device count.
func TestParseVolcanoVGPUAnnotation(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		wantDevices int
	}{
		{
			name:        "nil annotations",
			annotations: nil,
			wantDevices: 0,
		},
		{
			name:        "empty annotations",
			annotations: map[string]string{},
			wantDevices: 0,
		},
		{
			name: "annotation absent",
			annotations: map[string]string{
				"other-annotation": "value",
			},
			wantDevices: 0,
		},
		{
			name: "annotation empty string",
			annotations: map[string]string{
				"volcano.sh/node-vgpu-register": "",
			},
			wantDevices: 0,
		},
		{
			name: "annotation whitespace only",
			annotations: map[string]string{
				"volcano.sh/node-vgpu-register": "   ",
			},
			wantDevices: 0,
		},
		{
			name: "annotation invalid JSON",
			annotations: map[string]string{
				"volcano.sh/node-vgpu-register": "not-json",
			},
			wantDevices: 0,
		},
		{
			name: "single device with count=10",
			annotations: map[string]string{
				"volcano.sh/node-vgpu-register": `[{"id":"GPU-aaa","count":10,"devmem":49140,"devcore":100,"type":"NVIDIA GeForce RTX 4090","health":true}]`,
			},
			wantDevices: 10,
		},
		{
			name: "two devices with count=10 each",
			annotations: map[string]string{
				"volcano.sh/node-vgpu-register": `[{"id":"GPU-aaa","count":10,"devmem":49140,"devcore":100,"type":"NVIDIA GeForce RTX 4090","health":true},{"id":"GPU-bbb","index":1,"count":10,"devmem":49140,"devcore":100,"type":"NVIDIA GeForce RTX 4090","health":true}]`,
			},
			wantDevices: 20,
		},
		{
			name: "two devices with count=0 fallback to 1 each",
			annotations: map[string]string{
				"volcano.sh/node-vgpu-register": `[{"id":"GPU-aaa","count":0},{"id":"GPU-bbb","count":0}]`,
			},
			wantDevices: 2,
		},
		{
			// Real cluster format (plan.md §4.6): colon-separated physical
			// GPUs, each with comma-separated fields. count=4 per GPU.
			name: "comma-separated two GPUs count=4 each (real cluster)",
			annotations: map[string]string{
				"volcano.sh/node-vgpu-register": "GPU-4e7a0d18-9e71-50ac-2de3-05245d7d89b5,4,4914,NVIDIA-NVIDIA GeForce RTX 4090,true,hami-core:GPU-81a7a1b7-3671-2da0-cd94-9ae5e92700da,4,4914,NVIDIA-NVIDIA GeForce RTX 4090,true,hami-core:",
			},
			wantDevices: 8,
		},
		{
			name: "comma-separated single GPU count=10 (real cluster)",
			annotations: map[string]string{
				"volcano.sh/node-vgpu-register": "GPU-aaa,10,4914,NVIDIA-RTX4090,true,hami-core:",
			},
			wantDevices: 10,
		},
		{
			name: "comma-separated two GPUs with different counts",
			annotations: map[string]string{
				"volcano.sh/node-vgpu-register": "GPU-aaa,4,4914,NVIDIA-RTX4090,true,hami-core:GPU-bbb,8,4914,NVIDIA-RTX4090,true,hami-core:",
			},
			wantDevices: 12,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseVolcanoVGPUAnnotation(tt.annotations)
			if got != tt.wantDevices {
				t.Fatalf("parseVolcanoVGPUAnnotation() = %d, want %d", got, tt.wantDevices)
			}
		})
	}
}

// TestInventoryNodeLabelDerivation verifies that GPUNodeClass fields
// GPUMode, GPUSpec, GPUSharingSpec, GPUSharingPolicy are derived from
// the corresponding node labels.
func TestInventoryNodeLabelDerivation(t *testing.T) {
	body := `{
  "items": [{
    "metadata": {
      "name": "wholecard-node",
      "labels": {
        "kubernetes.io/hostname": "wholecard-node",
        "nvidia.com/gpu.product": "A100",
        "ani.kubercloud.io/gpu-mode": "wholecard",
        "ani.kubercloud.io/gpu-spec": "NVIDIA-A100-SXM4-80GB"
      }
    },
    "status": {
      "capacity": {"nvidia.com/gpu": "4"},
      "allocatable": {"nvidia.com/gpu": "4"},
      "nodeInfo": {"kubeletVersion": "v1.36.1"},
      "conditions": [{"type": "Ready", "status": "True", "reason": "KubeletReady"}]
    }
  }, {
    "metadata": {
      "name": "vgpu-node",
      "labels": {
        "kubernetes.io/hostname": "vgpu-node",
        "nvidia.com/gpu.product": "L40S",
        "ani.kubercloud.io/gpu-mode": "vgpu",
        "ani.kubercloud.io/gpu-sharing-spec": "NVIDIA-L40S-HALF",
        "ani.kubercloud.io/gpu-sharing-policy": "half"
      }
    },
    "status": {
      "capacity": {"nvidia.com/gpu": "2"},
      "allocatable": {"nvidia.com/gpu": "2"},
      "nodeInfo": {"kubeletVersion": "v1.36.1"},
      "conditions": [{"type": "Ready", "status": "True", "reason": "KubeletReady"}]
    }
  }, {
    "metadata": {
      "name": "legacy-node",
      "labels": {
        "kubernetes.io/hostname": "legacy-node",
        "nvidia.com/gpu.product": "V100"
      }
    },
    "status": {
      "capacity": {"nvidia.com/gpu": "1"},
      "allocatable": {"nvidia.com/gpu": "1"},
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
	if len(nodes) != 3 {
		t.Fatalf("len(nodes) = %d, want 3", len(nodes))
	}

	// Wholecard node: GPUMode=wholecard, GPUSpec set, sharing fields empty.
	wholecard := nodes[0]
	if wholecard.GPUMode != "wholecard" {
		t.Fatalf("wholecard node GPUMode = %q, want wholecard", wholecard.GPUMode)
	}
	if wholecard.GPUSpec != "NVIDIA-A100-SXM4-80GB" {
		t.Fatalf("wholecard node GPUSpec = %q, want NVIDIA-A100-SXM4-80GB", wholecard.GPUSpec)
	}
	if wholecard.GPUSharingSpec != "" {
		t.Fatalf("wholecard node GPUSharingSpec = %q, want empty", wholecard.GPUSharingSpec)
	}
	if wholecard.GPUSharingPolicy != "" {
		t.Fatalf("wholecard node GPUSharingPolicy = %q, want empty", wholecard.GPUSharingPolicy)
	}

	// vGPU node: GPUMode=vgpu, GPUSharingSpec and GPUSharingPolicy set, GPUSpec empty.
	vgpu := nodes[1]
	if vgpu.GPUMode != "vgpu" {
		t.Fatalf("vgpu node GPUMode = %q, want vgpu", vgpu.GPUMode)
	}
	if vgpu.GPUSpec != "" {
		t.Fatalf("vgpu node GPUSpec = %q, want empty", vgpu.GPUSpec)
	}
	if vgpu.GPUSharingSpec != "NVIDIA-L40S-HALF" {
		t.Fatalf("vgpu node GPUSharingSpec = %q, want NVIDIA-L40S-HALF", vgpu.GPUSharingSpec)
	}
	if vgpu.GPUSharingPolicy != "half" {
		t.Fatalf("vgpu node GPUSharingPolicy = %q, want half", vgpu.GPUSharingPolicy)
	}

	// Legacy node: no new labels, all derived fields should be empty.
	legacy := nodes[2]
	if legacy.GPUMode != "" {
		t.Fatalf("legacy node GPUMode = %q, want empty", legacy.GPUMode)
	}
	if legacy.GPUSpec != "" {
		t.Fatalf("legacy node GPUSpec = %q, want empty", legacy.GPUSpec)
	}
	if legacy.GPUSharingSpec != "" {
		t.Fatalf("legacy node GPUSharingSpec = %q, want empty", legacy.GPUSharingSpec)
	}
	if legacy.GPUSharingPolicy != "" {
		t.Fatalf("legacy node GPUSharingPolicy = %q, want empty", legacy.GPUSharingPolicy)
	}
}

// newTestGPUInventoryWithSpecStore builds an inventory with spec and quota
// stores for ListSpecAvailability tests. The body is the NodeList JSON.
func newTestGPUInventoryWithSpecStore(t *testing.T, body string, specStore ports.GPUSpecStore, quotaStore ports.QuotaStoreService) *KubernetesGPUInventory {
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
	return NewKubernetesGPUInventoryWithSpecStore(client, nil, specStore, quotaStore)
}

// gpuNodeListJSONWithLabels returns a NodeList body with one ready GPU node
// advertising the given allocatable resources and node labels. Annotation
// values are JSON-encoded to handle embedded JSON arrays safely.
func gpuNodeListJSONWithLabels(t *testing.T, allocatableGPU, allocatableVGPU string, labels map[string]string, annotations map[string]string) string {
	t.Helper()
	return gpuNodeListJSONFull(t, allocatableGPU, allocatableGPU, allocatableVGPU, allocatableVGPU, labels, annotations)
}

// gpuNodeListJSONFull returns a NodeList body with independent capacity and
// allocatable values. Used to simulate nodes where capacity > 0 but
// allocatable = 0 (all devices in use) for device_full tests.
func gpuNodeListJSONFull(t *testing.T, capacityGPU, allocatableGPU, capacityVGPU, allocatableVGPU string, labels map[string]string, annotations map[string]string) string {
	t.Helper()
	labelJSON := `"kubernetes.io/hostname": "ani-gpu-1"`
	for k, v := range labels {
		encoded, _ := json.Marshal(v)
		labelJSON += `, ` + `"` + k + `": ` + string(encoded)
	}
	annJSON := ""
	for k, v := range annotations {
		if annJSON != "" {
			annJSON += ", "
		}
		encoded, _ := json.Marshal(v)
		annJSON += `"` + k + `": ` + string(encoded)
	}
	annotationsSection := ""
	if annJSON != "" {
		annotationsSection = `,"annotations": {` + annJSON + `}`
	}
	return `{
  "items": [{
    "metadata": {
      "name": "ani-gpu-1",
      "labels": {` + labelJSON + `}` + annotationsSection + `
    },
    "status": {
      "capacity": {"nvidia.com/gpu": "` + capacityGPU + `", "nvidia.com/vgpu": "` + capacityVGPU + `"},
      "allocatable": {"nvidia.com/gpu": "` + allocatableGPU + `", "nvidia.com/vgpu": "` + allocatableVGPU + `"},
      "nodeInfo": {"kubeletVersion": "v1.36.1"},
      "conditions": [{"type": "Ready", "status": "True", "reason": "KubeletReady"}]
    }
  }]
}`
}

func TestListSpecAvailabilityAvailable(t *testing.T) {
	// Quota: total=10, used=2, reserved=1 → remaining=7
	// Spec: wholecard, GPUType matches node GPUSpec
	// Wholecard device count = nvidia.com/gpu allocatable = 4
	// AvailableCount = min(7, 4) = 4
	body := gpuNodeListJSONWithLabels(t, "4", "0",
		map[string]string{
			"ani.kubercloud.io/gpu-mode": "wholecard",
			"ani.kubercloud.io/gpu-spec": "NVIDIA-A100-SXM4-80GB",
		},
		nil, // wholecard reads nvidia.com/gpu allocatable, not the volcano annotation
	)
	specStore := stubSpecStore{specs: []ports.GPUSpecCRD{
		{ID: "a100-wholecard", GPUType: "NVIDIA-A100-SXM4-80GB", GPUMode: "wholecard", Shares: 1},
	}}
	quotaStore := stubQuotaStore{view: ports.QuotaView{
		Total:    map[ports.ResourceType]int64{ports.QuotaGPUCount: 10},
		Used:     map[ports.ResourceType]int64{ports.QuotaGPUCount: 2},
		Reserved: map[ports.ResourceType]int64{ports.QuotaGPUCount: 1},
	}}
	inventory := newTestGPUInventoryWithSpecStore(t, body, specStore, quotaStore)
	result, err := inventory.ListSpecAvailability(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("ListSpecAvailability error = %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	avail := result[0]
	if avail.SpecID != "a100-wholecard" {
		t.Fatalf("SpecID = %q, want a100-wholecard", avail.SpecID)
	}
	if avail.Status != ports.GPUSpecStatusAvailable {
		t.Fatalf("Status = %q, want available", avail.Status)
	}
	if !avail.HasMatchingNodes {
		t.Fatalf("HasMatchingNodes = false, want true")
	}
	if avail.AvailableCount != 4 {
		t.Fatalf("AvailableCount = %d, want 4", avail.AvailableCount)
	}
	if avail.DeviceIdleCount != 4 {
		t.Fatalf("DeviceIdleCount = %d, want 4", avail.DeviceIdleCount)
	}
	if !avail.HasIdleDevices {
		t.Fatalf("HasIdleDevices = false, want true")
	}
}

func TestListSpecAvailabilityFull(t *testing.T) {
	// Quota: total=5, used=5, reserved=0 → remaining=0 → status=full
	// (wholecard node with nvidia.com/gpu allocatable=4, but quota
	// exhausted takes precedence over device count)
	body := gpuNodeListJSONWithLabels(t, "4", "0",
		map[string]string{
			"ani.kubercloud.io/gpu-mode": "wholecard",
			"ani.kubercloud.io/gpu-spec": "NVIDIA-A100-SXM4-80GB",
		},
		nil,
	)
	specStore := stubSpecStore{specs: []ports.GPUSpecCRD{
		{ID: "a100-wholecard", GPUType: "NVIDIA-A100-SXM4-80GB", GPUMode: "wholecard", Shares: 1},
	}}
	quotaStore := stubQuotaStore{view: ports.QuotaView{
		Total:    map[ports.ResourceType]int64{ports.QuotaGPUCount: 5},
		Used:     map[ports.ResourceType]int64{ports.QuotaGPUCount: 5},
		Reserved: map[ports.ResourceType]int64{},
	}}
	inventory := newTestGPUInventoryWithSpecStore(t, body, specStore, quotaStore)
	result, err := inventory.ListSpecAvailability(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("ListSpecAvailability error = %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	avail := result[0]
	if avail.Status != ports.GPUSpecStatusFull {
		t.Fatalf("Status = %q, want full", avail.Status)
	}
	if avail.AvailableCount != 0 {
		t.Fatalf("AvailableCount = %d, want 0", avail.AvailableCount)
	}
	if !avail.HasMatchingNodes {
		t.Fatalf("HasMatchingNodes = false, want true (nodes match but quota is full)")
	}
}

func TestListSpecAvailabilityDeviceFull(t *testing.T) {
	// Quota: remaining=5, but nvidia.com/gpu allocatable=0 (capacity=4 but
	// all allocated) → device_idle_count=0 → device_full.
	// The node still advertises capacity > 0 so hasGPUResource keeps it;
	// gpuAllocatableCount reads the allocatable map (which is 0).
	body := gpuNodeListJSONFull(t, "4", "0", "0", "0",
		map[string]string{
			"ani.kubercloud.io/gpu-mode": "wholecard",
			"ani.kubercloud.io/gpu-spec": "NVIDIA-A100-SXM4-80GB",
		},
		nil,
	)
	specStore := stubSpecStore{specs: []ports.GPUSpecCRD{
		{ID: "a100-wholecard", GPUType: "NVIDIA-A100-SXM4-80GB", GPUMode: "wholecard", Shares: 1},
	}}
	quotaStore := stubQuotaStore{view: ports.QuotaView{
		Total:    map[ports.ResourceType]int64{ports.QuotaGPUCount: 10},
		Used:     map[ports.ResourceType]int64{ports.QuotaGPUCount: 5},
		Reserved: map[ports.ResourceType]int64{},
	}}
	inventory := newTestGPUInventoryWithSpecStore(t, body, specStore, quotaStore)
	result, err := inventory.ListSpecAvailability(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("ListSpecAvailability error = %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	avail := result[0]
	if avail.Status != ports.GPUSpecStatusDeviceFull {
		t.Fatalf("Status = %q, want device_full", avail.Status)
	}
	if avail.AvailableCount != 0 {
		t.Fatalf("AvailableCount = %d, want 0", avail.AvailableCount)
	}
	if !avail.HasMatchingNodes {
		t.Fatalf("HasMatchingNodes = false, want true (nodes match but no idle devices)")
	}
	if avail.HasIdleDevices {
		t.Fatalf("HasIdleDevices = true, want false")
	}
}

func TestListSpecAvailabilityUnavailable(t *testing.T) {
	// Node has wholecard labels, but spec is vGPU → no matching nodes → unavailable
	body := gpuNodeListJSONWithLabels(t, "4", "0",
		map[string]string{
			"ani.kubercloud.io/gpu-mode": "wholecard",
			"ani.kubercloud.io/gpu-spec": "NVIDIA-A100-SXM4-80GB",
		},
		map[string]string{
			"volcano.sh/node-vgpu-register": `[{"id":"GPU-1"}]`,
		},
	)
	specStore := stubSpecStore{specs: []ports.GPUSpecCRD{
		{ID: "l40s-vgpu-half", GPUType: "NVIDIA-L40S-HALF", GPUMode: "vgpu", Shares: 2},
	}}
	quotaStore := stubQuotaStore{view: ports.QuotaView{
		Total:    map[ports.ResourceType]int64{ports.QuotaGPUCount: 10},
		Used:     map[ports.ResourceType]int64{ports.QuotaGPUCount: 0},
		Reserved: map[ports.ResourceType]int64{},
	}}
	inventory := newTestGPUInventoryWithSpecStore(t, body, specStore, quotaStore)
	result, err := inventory.ListSpecAvailability(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("ListSpecAvailability error = %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	avail := result[0]
	if avail.Status != ports.GPUSpecStatusUnavailable {
		t.Fatalf("Status = %q, want unavailable", avail.Status)
	}
	if avail.AvailableCount != 0 {
		t.Fatalf("AvailableCount = %d, want 0", avail.AvailableCount)
	}
	if avail.HasMatchingNodes {
		t.Fatalf("HasMatchingNodes = true, want false (no matching nodes)")
	}
}

func TestListSpecAvailabilityVGPUAvailable(t *testing.T) {
	// vGPU spec matches node with gpu-mode=vgpu and gpu-sharing-spec matching GPUType
	// Volcano annotation: 2 physical GPUs, each split into 4 vGPU slices → total=8 slices
	// Quota remaining=10 → AvailableCount = min(10, 8) = 8
	// GPUCount = 1 (1 vGPU slice = 1 card per instance request)
	body := gpuNodeListJSONWithLabels(t, "0", "4",
		map[string]string{
			"ani.kubercloud.io/gpu-mode":           "vgpu",
			"ani.kubercloud.io/gpu-sharing-spec":   "NVIDIA-L40S-HALF",
			"ani.kubercloud.io/gpu-sharing-policy": "half",
		},
		map[string]string{
			"volcano.sh/node-vgpu-register": "GPU-aaa,4,4914,NVIDIA-L40S,true,hami-core:GPU-bbb,4,4914,NVIDIA-L40S,true,hami-core:",
		},
	)
	specStore := stubSpecStore{specs: []ports.GPUSpecCRD{
		{ID: "l40s-vgpu-half", GPUType: "NVIDIA-L40S-HALF", GPUMode: "vgpu", Shares: 2},
	}}
	quotaStore := stubQuotaStore{view: ports.QuotaView{
		Total:    map[ports.ResourceType]int64{ports.QuotaGPUCount: 10},
		Used:     map[ports.ResourceType]int64{ports.QuotaGPUCount: 0},
		Reserved: map[ports.ResourceType]int64{},
	}}
	inventory := newTestGPUInventoryWithSpecStore(t, body, specStore, quotaStore)
	result, err := inventory.ListSpecAvailability(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("ListSpecAvailability error = %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	avail := result[0]
	if avail.Status != ports.GPUSpecStatusAvailable {
		t.Fatalf("Status = %q, want available", avail.Status)
	}
	if avail.AvailableCount != 8 {
		t.Fatalf("AvailableCount = %d, want 8", avail.AvailableCount)
	}
	if !avail.HasMatchingNodes {
		t.Fatalf("HasMatchingNodes = false, want true")
	}
	if avail.DeviceIdleCount != 8 {
		t.Fatalf("DeviceIdleCount = %d, want 8", avail.DeviceIdleCount)
	}
	if avail.GPUCount != 1 {
		t.Fatalf("GPUCount = %d, want 1 (1 vGPU slice = 1 card per instance)", avail.GPUCount)
	}
}

func TestListSpecAvailabilityUnsupportedWithoutStores(t *testing.T) {
	// Without specStore/quotaStore injected, ListSpecAvailability should
	// return ErrUnsupported.
	inventory := newTestGPUInventory(t, gpuNodeListJSON(t, "1", "0"))
	_, err := inventory.ListSpecAvailability(context.Background(), "tenant-a")
	if err == nil {
		t.Fatalf("error nil, want ErrUnsupported")
	}
	if !errors.Is(err, ports.ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
}

func TestListSpecAvailabilityUsesAllocatedGPUCount(t *testing.T) {
	// plan.md §4.4.1: quota_remaining = allocated_gpu_count - used - reserved
	// total=10, allocated=4, used=1, reserved=1 → remaining=2 (not 8)
	// device_idle=4 → AvailableCount = min(2, 4) = 2
	body := gpuNodeListJSONWithLabels(t, "4", "0",
		map[string]string{
			"ani.kubercloud.io/gpu-mode": "wholecard",
			"ani.kubercloud.io/gpu-spec": "NVIDIA-A100-SXM4-80GB",
		},
		nil,
	)
	specStore := stubSpecStore{specs: []ports.GPUSpecCRD{
		{ID: "a100-wholecard", GPUType: "NVIDIA-A100-SXM4-80GB", GPUMode: "wholecard", Shares: 1},
	}}
	quotaStore := stubQuotaStore{view: ports.QuotaView{
		Total:    map[ports.ResourceType]int64{ports.QuotaGPUCount: 10},
		Used:     map[ports.ResourceType]int64{ports.QuotaGPUCount: 1},
		Reserved: map[ports.ResourceType]int64{ports.QuotaGPUCount: 1},
	}}
	quotaAdmin := stubQuotaAdmin{reservation: ports.ReservationView{
		TenantID:          "tenant-a",
		AllocatedGPUCount: 4,
		Used:              1,
		Reserved:          1,
		Available:         2,
	}}
	inventory := newTestGPUInventoryWithSpecStore(t, body, specStore, quotaStore)
	inventory.WithQuotaAdmin(quotaAdmin)
	result, err := inventory.ListSpecAvailability(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("ListSpecAvailability error = %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	avail := result[0]
	if avail.AvailableCount != 2 {
		t.Fatalf("AvailableCount = %d, want 2 (allocated=4 - used=1 - reserved=1 = 2, not total=10 - 1 - 1 = 8)", avail.AvailableCount)
	}
}

func TestListSpecAvailabilityFallsBackToTotalWithoutQuotaAdmin(t *testing.T) {
	// When quotaAdmin is not injected, fall back to total.
	// total=10, used=2, reserved=1 → remaining=7
	// device_idle=4 → AvailableCount = min(7, 4) = 4
	body := gpuNodeListJSONWithLabels(t, "4", "0",
		map[string]string{
			"ani.kubercloud.io/gpu-mode": "wholecard",
			"ani.kubercloud.io/gpu-spec": "NVIDIA-A100-SXM4-80GB",
		},
		nil,
	)
	specStore := stubSpecStore{specs: []ports.GPUSpecCRD{
		{ID: "a100-wholecard", GPUType: "NVIDIA-A100-SXM4-80GB", GPUMode: "wholecard", Shares: 1},
	}}
	quotaStore := stubQuotaStore{view: ports.QuotaView{
		Total:    map[ports.ResourceType]int64{ports.QuotaGPUCount: 10},
		Used:     map[ports.ResourceType]int64{ports.QuotaGPUCount: 2},
		Reserved: map[ports.ResourceType]int64{ports.QuotaGPUCount: 1},
	}}
	inventory := newTestGPUInventoryWithSpecStore(t, body, specStore, quotaStore)
	// No WithQuotaAdmin — should fall back to total.
	result, err := inventory.ListSpecAvailability(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("ListSpecAvailability error = %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	avail := result[0]
	if avail.AvailableCount != 4 {
		t.Fatalf("AvailableCount = %d, want 4 (fallback: total=10 - used=2 - reserved=1 = 7, min(7,4)=4)", avail.AvailableCount)
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
