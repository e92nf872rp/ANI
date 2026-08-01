package main

import (
	"testing"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/bootstrap"
)

func TestGatewayInstanceRuntimeConfigFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://instance-runtime")
	t.Setenv("WORKLOAD_PROVIDER", "kubernetes_rest")
	t.Setenv("WORKLOAD_PROVIDER_APPLY_ENABLED", "true")
	t.Setenv("WORKLOAD_LIFECYCLE_PROVIDER", "kubernetes_rest")
	t.Setenv("WORKLOAD_LIFECYCLE_APPLY_ENABLED", "true")
	t.Setenv("WORKLOAD_OPS_PROVIDER", "kubernetes_rest")
	t.Setenv("WORKLOAD_OPS_ENABLED", "true")
	t.Setenv("KUBERNETES_API_HOST", "https://kubernetes.example.test")
	t.Setenv("KUBERNETES_PROVIDER_FIELD_MANAGER", "ani-gateway-test")

	cfg := gatewayInstanceRuntimeConfigFromEnv()

	if cfg.DatabaseURL != "postgres://instance-runtime" {
		t.Fatalf("DatabaseURL = %q, want environment value", cfg.DatabaseURL)
	}
	if cfg.WorkloadProvider != "kubernetes_rest" {
		t.Fatalf("WorkloadProvider = %q, want kubernetes_rest", cfg.WorkloadProvider)
	}
	if !cfg.WorkloadProviderApplyEnabled || cfg.WorkloadLifecycleProvider != "kubernetes_rest" || !cfg.WorkloadLifecycleApplyEnabled {
		t.Fatalf("workload lifecycle config = apply:%v provider:%q lifecycleApply:%v, want real provider enabled", cfg.WorkloadProviderApplyEnabled, cfg.WorkloadLifecycleProvider, cfg.WorkloadLifecycleApplyEnabled)
	}
	if cfg.WorkloadOpsProvider != "kubernetes_rest" || !cfg.WorkloadOpsEnabled {
		t.Fatalf("workload ops config = provider:%q enabled:%v, want kubernetes_rest enabled", cfg.WorkloadOpsProvider, cfg.WorkloadOpsEnabled)
	}
	if cfg.KubernetesAPIHost != "https://kubernetes.example.test" || cfg.KubernetesProviderFieldManager != "ani-gateway-test" {
		t.Fatalf("kubernetes config = host:%q manager:%q, want env values", cfg.KubernetesAPIHost, cfg.KubernetesProviderFieldManager)
	}
}

func TestNewGatewayInstanceRuntimeKeepsLocalProfileWithoutDatabase(t *testing.T) {
	runtime, closeRuntime, err := newGatewayInstanceRuntime(t.Context(), bootstrap.Config{
		DatabaseURL:      ":// invalid",
		WorkloadProvider: "local",
	}, runtimeadapter.NewLocalSecretService())
	if err != nil {
		t.Fatalf("newGatewayInstanceRuntime() error = %v", err)
	}
	if runtime.Service != nil || runtime.Store != nil || runtime.Operations != nil || runtime.KubernetesRESTClient != nil {
		t.Fatalf("runtime = %+v, want empty local runtime", runtime)
	}
	if closeRuntime == nil {
		t.Fatal("closeRuntime = nil, want no-op close function")
	}
	closeRuntime()
}
