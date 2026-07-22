package main

import (
	"context"
	"testing"

	registryadapter "github.com/kubercloud/ani/pkg/adapters/registry"
)

func TestGatewayImageRegistryLocalModeHasNoRuntimeCloser(t *testing.T) {
	service, closeRuntime, err := newGatewayImageRegistry(context.Background(), gatewayRegistryRuntimeConfig{})
	if err != nil {
		t.Fatalf("newGatewayImageRegistry() error = %v", err)
	}
	if _, ok := service.(*registryadapter.LocalImageRegistry); !ok {
		t.Fatalf("service = %T, want *LocalImageRegistry", service)
	}
	if closeRuntime != nil {
		t.Fatal("closeRuntime = non-nil, want nil when no runtime resource was allocated")
	}
}
