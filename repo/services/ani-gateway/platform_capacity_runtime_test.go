package main

import (
	"testing"

	"github.com/kubercloud/ani/pkg/adapters/runtime"
)

func TestGatewayPlatformCapacityDefaultsToRouterLocalFallback(t *testing.T) {
	service, err := newGatewayPlatformCapacityService(gatewayGPUInventoryRuntimeConfig{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("newGatewayPlatformCapacityService() error = %v", err)
	}
	if service != nil {
		t.Fatalf("service = %T, want nil so router keeps local default", service)
	}
}

func TestGatewayPlatformCapacityUsesKubernetesProvider(t *testing.T) {
	t.Setenv("PLATFORM_CAPACITY_PROVIDER", "kubernetes_rest")

	service, err := newGatewayPlatformCapacityService(
		gatewayGPUInventoryRuntimeConfig{},
		runtime.NewLocalGPUInventory(),
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("newGatewayPlatformCapacityService() error = %v", err)
	}
	if service == nil {
		t.Fatal("service = nil, want Kubernetes platform capacity service")
	}
}

func TestGatewayPlatformCapacityRejectsUnsupportedProvider(t *testing.T) {
	t.Setenv("PLATFORM_CAPACITY_PROVIDER", "dcgm_direct")

	if _, err := newGatewayPlatformCapacityService(gatewayGPUInventoryRuntimeConfig{}, nil, nil, nil); err == nil {
		t.Fatal("newGatewayPlatformCapacityService() error = nil, want unsupported provider error")
	}
}
