package main

import (
	"context"
	"testing"
	"time"

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

func TestGatewayRegistryRuntimeConfigReadsCanonicalRegistryEnv(t *testing.T) {
	t.Setenv("REGISTRY_PROVIDER_MODE", "harbor")
	t.Setenv("HARBOR_ENDPOINT", "http://harbor.harbor.svc.cluster.local")
	t.Setenv("HARBOR_USERNAME", "admin")
	t.Setenv("HARBOR_PASSWORD", "secret")
	t.Setenv("HARBOR_REQUEST_TIMEOUT", "3s")
	t.Setenv("REGISTRY_TLS_INSECURE", "true")
	t.Setenv("REGISTRY_PULL_SECRET_FIELD_MANAGER", "ani-registry")

	cfg := gatewayRegistryRuntimeConfigFromEnv()

	if cfg.ProviderMode != "harbor" {
		t.Fatalf("ProviderMode = %q, want harbor", cfg.ProviderMode)
	}
	if cfg.HarborEndpoint != "http://harbor.harbor.svc.cluster.local" {
		t.Fatalf("HarborEndpoint = %q", cfg.HarborEndpoint)
	}
	if cfg.HarborUsername != "admin" || cfg.HarborPassword != "secret" {
		t.Fatalf("Harbor credentials were not read from canonical HARBOR_* env")
	}
	if cfg.HarborRequestTimeout != 3*time.Second {
		t.Fatalf("HarborRequestTimeout = %v, want 3s", cfg.HarborRequestTimeout)
	}
	if !cfg.RegistryTLSInsecure {
		t.Fatal("RegistryTLSInsecure = false, want true")
	}
	if cfg.KubernetesProviderManager != "ani-registry" {
		t.Fatalf("KubernetesProviderManager = %q, want ani-registry", cfg.KubernetesProviderManager)
	}
}

func TestGatewayRegistryRuntimeConfigIgnoresLegacyRegistryEnv(t *testing.T) {
	t.Setenv("REGISTRY_PROVIDER", "harbor")
	t.Setenv("REGISTRY_ENDPOINT", "http://harbor.harbor.svc.cluster.local")
	t.Setenv("REGISTRY_USERNAME", "admin")
	t.Setenv("REGISTRY_PASSWORD", "secret")
	t.Setenv("KUBERNETES_PROVIDER_FIELD_MANAGER", "legacy-manager")

	cfg := gatewayRegistryRuntimeConfigFromEnv()

	if cfg.ProviderMode != "" {
		t.Fatalf("ProviderMode = %q, want empty without REGISTRY_PROVIDER_MODE", cfg.ProviderMode)
	}
	if cfg.HarborEndpoint != "" || cfg.HarborUsername != "" || cfg.HarborPassword != "" {
		t.Fatalf("Harbor config = %q/%q/%q, want empty without HARBOR_* env", cfg.HarborEndpoint, cfg.HarborUsername, cfg.HarborPassword)
	}
	if cfg.KubernetesProviderManager != "" {
		t.Fatalf("KubernetesProviderManager = %q, want empty without REGISTRY_PULL_SECRET_FIELD_MANAGER", cfg.KubernetesProviderManager)
	}
}
