package runtime

import (
	"context"
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
	device := node.Devices[0]
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
