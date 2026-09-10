package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

func TestNewGatewayPlatformWorkloadServiceDefaultsToLocal(t *testing.T) {
	service, closeStore, err := newGatewayPlatformWorkloadService(context.Background(), gatewayPlatformWorkloadRuntimeConfig{})
	if err != nil {
		t.Fatalf("newGatewayPlatformWorkloadService() error = %v", err)
	}
	defer closeStore()
	if _, ok := service.(*runtimeadapter.LocalPlatformWorkloadService); !ok {
		t.Fatalf("service = %T, want local", service)
	}
}

func TestNewGatewayPlatformWorkloadServiceUsesKubernetesProvider(t *testing.T) {
	service, closeStore, err := newGatewayPlatformWorkloadService(context.Background(), gatewayPlatformWorkloadRuntimeConfig{
		ProviderMode:         "kubernetes_rest",
		KubernetesAPIHost:    "https://kubernetes.example.test",
		KubernetesHTTPClient: &http.Client{Transport: gatewayPlatformWorkloadRoundTripper{}},
	})
	if err != nil {
		t.Fatalf("newGatewayPlatformWorkloadService() error = %v", err)
	}
	defer closeStore()
	if _, ok := service.(*runtimeadapter.KubernetesPlatformWorkloadService); !ok {
		t.Fatalf("service = %T, want kubernetes", service)
	}
}

func TestNewGatewayPlatformWorkloadServiceFailsClosedWithoutKubernetesHost(t *testing.T) {
	service, closeStore, err := newGatewayPlatformWorkloadService(context.Background(), gatewayPlatformWorkloadRuntimeConfig{
		ProviderMode: "kubernetes_rest",
	})
	defer closeStore()
	if err == nil || service != nil {
		t.Fatalf("service = %T err=%v, want fail-closed error", service, err)
	}
	if !errors.Is(err, ports.ErrInvalid) && !strings.Contains(err.Error(), "Kubernetes API host") {
		t.Fatalf("error = %v, want missing Kubernetes host", err)
	}
}

func TestNewGatewayPlatformWorkloadServiceRejectsUnsupportedProvider(t *testing.T) {
	if _, closeStore, err := newGatewayPlatformWorkloadService(context.Background(), gatewayPlatformWorkloadRuntimeConfig{
		ProviderMode: "immediately_running",
	}); err == nil {
		closeStore()
		t.Fatal("newGatewayPlatformWorkloadService() error = nil, want unsupported")
	}
}

func TestGatewayPlatformWorkloadRuntimeConfigFromEnv(t *testing.T) {
	t.Setenv("PLATFORM_WORKLOAD_PROVIDER", "kubernetes_rest")
	t.Setenv("KUBERNETES_API_HOST", "https://kubernetes.example.test")
	t.Setenv("PLATFORM_WORKLOAD_FIELD_MANAGER", "ani-platform-workload-test")
	t.Setenv("DATABASE_URL", "postgres://platform-workload")
	t.Setenv("MODEL_SERVICE_GRPC_ADDR", "model-service:9090")
	t.Setenv("MODEL_FETCHER_IMAGE_REF", "registry.local/model-fetcher@sha256:"+strings.Repeat("a", 64))
	t.Setenv("MODEL_FETCHER_ALLOW_INSECURE_HTTP", "true")
	t.Setenv("MINIO_ACCESS_KEY", "must-not-be-read")

	cfg := gatewayPlatformWorkloadRuntimeConfigFromEnv()
	if cfg.ProviderMode != "kubernetes_rest" || cfg.KubernetesAPIHost != "https://kubernetes.example.test" {
		t.Fatalf("cfg = %#v", cfg)
	}
	if cfg.KubernetesProviderFieldManager != "ani-platform-workload-test" || cfg.DatabaseURL != "postgres://platform-workload" {
		t.Fatalf("field manager/db = %#v", cfg)
	}
	if cfg.ModelServiceGRPCAddr != "model-service:9090" || cfg.ModelFetcherImageRef == "" || !cfg.ModelFetcherAllowInsecureHTTP {
		t.Fatalf("materialization config = %#v", cfg)
	}
}

func TestGatewayPlatformWorkloadRuntimeDisablesInsecureFetcherHTTPByDefault(t *testing.T) {
	t.Setenv("MODEL_FETCHER_ALLOW_INSECURE_HTTP", "")
	cfg := gatewayPlatformWorkloadRuntimeConfigFromEnv()
	if cfg.ModelFetcherAllowInsecureHTTP {
		t.Fatal("MODEL_FETCHER_ALLOW_INSECURE_HTTP must default to false")
	}
}

func TestProductionGatewayDeploymentWiresModelMaterializationConfig(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/real-k8s-lab/sprint13-production-shaped-gateway-deployment.yaml")
	if err != nil {
		t.Fatalf("read production gateway deployment: %v", err)
	}
	manifest := string(raw)
	for _, required := range []string{
		"name: MODEL_SERVICE_GRPC_ADDR",
		"key: model_service_grpc_addr",
		"optional: false",
		"name: MODEL_FETCHER_GRPC_ADDR",
		"key: model_fetcher_grpc_addr",
		"name: MODEL_FETCHER_IMAGE_REF",
		"key: model_fetcher_image_ref",
		"name: MODEL_FETCHER_ALLOW_INSECURE_HTTP",
		"key: model_fetcher_allow_insecure_http",
	} {
		if !strings.Contains(manifest, required) {
			t.Fatalf("production gateway deployment is missing %q", required)
		}
	}
}

type gatewayPlatformWorkloadRoundTripper struct{}

func (gatewayPlatformWorkloadRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
}
