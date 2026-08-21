package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestGatewayGPUInventoryFromConfigDefaultsToRouterLocalInventory(t *testing.T) {
	inventory, err := newGatewayGPUInventory(gatewayGPUInventoryRuntimeConfig{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("newGatewayGPUInventory() error = %v", err)
	}
	if inventory != nil {
		t.Fatalf("inventory = %T, want nil so router keeps local default", inventory)
	}
}

func TestGatewayGPUInventoryFromConfigUsesKubernetesProvider(t *testing.T) {
	inventory, err := newGatewayGPUInventory(gatewayGPUInventoryRuntimeConfig{
		ProviderMode:         "kubernetes_rest",
		KubernetesAPIHost:    "https://kubernetes.example.test",
		KubernetesHTTPClient: &http.Client{Transport: gatewayGPUInventoryRoundTripper{}},
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("newGatewayGPUInventory() error = %v", err)
	}
	if inventory == nil {
		t.Fatalf("inventory = nil, want Kubernetes GPU inventory")
	}
	nodes, err := inventory.ListNodeClasses(context.Background(), ports.GPUDiscoveryFilter{})
	if err != nil {
		t.Fatalf("ListNodeClasses() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeName != "gpu-node-a" || len(nodes[0].Devices) != 2 {
		t.Fatalf("nodes = %+v, want one Kubernetes GPU node with two devices", nodes)
	}
}

func TestGatewayGPUInventoryRejectsUnsupportedProvider(t *testing.T) {
	if _, err := newGatewayGPUInventory(gatewayGPUInventoryRuntimeConfig{
		ProviderMode: "dcgm_direct",
	}, nil, nil, nil); err == nil {
		t.Fatalf("newGatewayGPUInventory() error = nil, want unsupported provider error")
	}
}

func TestGatewayGPUInventoryConfigFromEnvIncludesInClusterKubernetesService(t *testing.T) {
	t.Setenv("GPU_INVENTORY_PROVIDER", "kubernetes_rest")
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.96.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	t.Setenv("KUBERNETES_SERVICE_ACCOUNT_TOKEN_FILE", "/var/run/secrets/kubernetes.io/serviceaccount/token")
	t.Setenv("KUBERNETES_SERVICE_ACCOUNT_CA_FILE", "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")

	cfg := gatewayGPUInventoryRuntimeConfigFromEnv()
	if cfg.KubernetesServiceHost != "10.96.0.1" || cfg.KubernetesServicePort != "443" {
		t.Fatalf("service host/port = %q/%q, want in-cluster Kubernetes service", cfg.KubernetesServiceHost, cfg.KubernetesServicePort)
	}
	if cfg.KubernetesServiceAccountTokenFile == "" || cfg.KubernetesServiceAccountCAFile == "" {
		t.Fatalf("service account files not loaded from env: %#v", cfg)
	}
}

type gatewayGPUInventoryRoundTripper struct{}

func (gatewayGPUInventoryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet || req.URL.Path != "/api/v1/nodes" {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(`{
  "items": [{
    "metadata": {
      "name": "gpu-node-a",
      "labels": {"kubernetes.io/hostname": "gpu-node-a", "nvidia.com/gpu.product": "NVIDIA-L40S"}
    },
    "status": {
      "capacity": {"nvidia.com/gpu": "2"},
      "allocatable": {"nvidia.com/gpu": "2"},
      "nodeInfo": {"kubeletVersion": "v1.36.1"},
      "conditions": [{"type": "Ready", "status": "True", "reason": "KubeletReady"}]
    }
  }]
}`)),
		Header: http.Header{},
	}, nil
}
